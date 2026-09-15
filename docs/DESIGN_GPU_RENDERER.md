# Design — The frame draw list and the GPU renderer

`internal/drawlist`, `internal/platform/gpurender`, and the recording side of
`internal/client`. One committed-frame walk records one ordered list of draw
commands; two executors replay it. The **classic** executor is the software
composer writing palette indices into a byte surface. The **modern** executor
replays the same list through Ebitengine, composing in true colour on the GPU.
The public choices are `--renderer=classic|modern`, default modern, also
selectable on the Nanolathe options page and with F10. The simulation cannot
tell which executor is selected.

This document states what the code does now. The measurement record behind it —
benchmark runs, capture comparisons, the rounds of work that got here, and the
mechanisms that were retired on the way — is
[GPU_RENDERER_HISTORY.md](GPU_RENDERER_HISTORY.md).

This document is listed by [ARCHITECTURE.md](ARCHITECTURE.md), which owns
package boundaries. [DESIGN_PRESENTATION_CLIENT.md](DESIGN_PRESENTATION_CLIENT.md)
owns what a frame contains and in what order; this document owns how that walk
is recorded and how the recording reaches pixels. Rules every diff is reviewed
against are in [INVARIANTS.md](INVARIANTS.md); I11's single sanctioned
presentation switch is this one.

**Section numbers are stable.** More than three hundred code comments cite them,
so a section keeps its number and its subject even when its content is rewritten.
§8, §12 and §24 were whole-section records and now live in the history file;
their numbers are retired rather than reused. Reading order:

| Read for | Sections |
|---|---|
| Purpose and boundary | §1 |
| Packages, files and key types | §2 |
| Contracts | §3 |
| The pipeline: record → compile → submit | §11, §13 |
| The model lane | §22, with §9, §10 and §17 for what it inherited |
| The Enhanced layers, one each | §14–§21, §23, §25–§29, §31–§34 |
| Player and developer controls | §30 |
| Verification gates and recipes | §6, §11.4, §13.4 |
| Retail behaviour that is not a bug; divergences | §4, §5 |
| Research map | §7 |
| Known limitations and owed work | §35 |

## 1. Purpose and boundary

These are implementation decisions approved by the user, not retail findings.
Retail evidence remains in the owning research document.

1. **Original** targets established retail behavior and preserves the software
   rasterizer. Existing research gaps remain explicit; this is not a claim that
   every current pixel has been verified against retail. Rasterization and CPU
   captures need no GPU; the current interactive Ebitengine window still does.
   A GPU-free window backend is separate, deferred platform work.
2. **GPU Classic** — a modern executor that stayed in palette index space and
   reproduced the retail composite byte for byte — was retired in 2026-09, because
   an index-exact composite needs one render pass per destination read and that is
   what kept it off a refresh-rate cadence. Its design and measurements are in the
   history file; nothing in the current executor descends from it but the
   scheduler and the source atlases.
3. **Enhanced** is the modern executor. It composes in true colour with the
   palette tables' generating arithmetic in place of their nearest-palette
   quantization (§13.3), presents at the display's refresh rate and interpolates
   committed poses (§13.5). Differences from Original are measured and reviewed
   visually, including in motion; a pixel threshold alone is never approval. No
   enhancement changes authoritative simulation.

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

* `List` — one frame's commands in record order. It is **not** one slice of a
  sum type: a private `order []tag` names `(family, index)` pairs and each
  family has its own backing slice, so a command is a plain struct appended to
  its own array. `family` is private, so order is expressible only through
  `Replay`. `Reset()` truncates every slice to zero length without releasing
  capacity, so a re-recorded frame that fits allocates nothing.
* `Sink` — the executor interface, one method per mandatory command family
  (§3 C-G3): `Clear`, `Terrain`, `Sprite`, `Glyphs`, `Fill`, `Line`, `Points`,
  `Flash`, `Halo`, `Model`, `Fog`, `Surface`, `Cursor`, `Expand`.
  `List.Replay(Sink)` walks `order` and calls them in record order.
* Optional sinks, type-asserted once by `Replay`: `TrailSink`, `WorldSink`,
  `MarkerSink`, `ScorchSink`, `SurfaceWakeSink`, `LensSink`. A sink without one
  replays the frame unchanged, which is how the classic executor and every test
  collector ignore the Enhanced-only families.
* The list carries **no digest or hash**. The only digest in this area is
  `client.PresentationInputs`, which is an input-side validity digest (§13.10).

Command families, each a plain struct carrying **physical palette indices**
(`uint8`) and immutable resource references:

| Family | Carries | Classic executes as |
|---|---|---|
| `Terrain` | tile set reference, camera, view scale, optional 2× detail tiles, `Water` | the tile blitter |
| `Sprite` | GAF frame reference, position, clip rect, blit kind (`Keyed`, `Tinted`, `Lit(row)`, `Scaled`, `FeatureNormal`, `FeatureShadow`), transparent key | the keyed, ALP-tinted, LHT-lit and scaled GAF blitters |
| `Glyphs` | FNT reference, glyph run, baseline, colour byte | the FNT blitter `[03 §7.1]` |
| `Fill` | rect, index, style (`Solid`, `Outline`, `LitRect(row)`, `ShadeRect(row)`, `SolidInclusive`, `FrameInclusive`) | `fillIndexedRect`, `frameIndexedRect`, the UI light/shade rects |
| `Line` | two endpoints, index | `drawIndexedLine` |
| `Points` | a capped sub-slice of the recorder's point arena, `PointPlain` or `PointLit` | nanolathe particles `[03 §5.5]`, sprinkle fills |
| `Flash`, `Halo` | one lit disc or ground halo as a command (§13.11), each with an `Expand` that emits the points it stands for | the points `Expand` emits |
| `Model` | the classic packet and/or the geometry packet of one composed subject | the model rasterizer and its commit |
| `Fog` | the op list `render.BuildFogOpsWindowInto` produced, already clipped, plus the gray and black GAF entries | the three fog fills and the fog GAF blit `[03 §3.3]` |
| `Surface` | an indexed byte surface (minimap, radar), destination rect, optional identity/revision | `UIBlitIndexed` |
| `Cursor` | GAF frame reference, hot spot | `drawCursor` `[07 §8]` |
| `Lens` | projected center, view scale, viewport clip, transparent key | ordered snapshot refraction through the generated displacement map |
| `Expand` | none | the marker that ends the composite |
| `Trails`, `WorldSpace`, `Markers`, `SurfaceWakes`, `ScorchMarks` | the Enhanced families, reached through the optional sinks | not executed |

A `Model` record is the durable form of a unit, feature model or projectile
model. Its classic packet owns the completed body or staging colour, coverage
and optional key planes; the pre-punched shadow planes; and every image
placement scalar. Its modern packet carries the face list with per-vertex screen
position, height key, UV and shade row, the primitive's texture reference (or
LOGOS frame choice), the flat colour byte, the face's material annotation
(§29.1), plus the subject flags — key plane or painter order
`[03 R-REN-03A §2]`, the doubled `Supersample` lane `[03 R-REN-03A §6]` (§17),
waterline threshold, digger erase `[03 R-REN-03A §8]`, nanoframe reveal bands
and outline colour, shadow shear or `Silhouette`, and the attached children with
their height deltas `[03 R-REN-03A §4]`.

**Source lifetime.** Immutable-after-load resources are shared by pointer for
the life of the process: GAF frames and entries, PCX, FNT, the palette tables,
the terrain and its detail tiles, a marker atlas. Mutable per-frame buffers are
**borrowed until `Reset`**: the point arena sub-slices, the fog op slice, a
surface's bytes, and the trail, marker, wake and scorch mark slices. A consumer
that retains a frame calls `Clone()`, which deep-copies the order, every family
slice, those arenas, each `Model`'s classic and geometry packets, and each
terrain command's camera by value, while still sharing the immutable resources.
`RecordFrame` states the borrowing contract from the caller's side: the returned
list must be replayed before the next frame is recorded and must not be retained.

### 2.2 `internal/client` — recorder and classic executor

`drawCommittedFrame` keeps its ten barriers and every gate it has today. Each
place that would write into `c.indexed` records into `c.list`, and the
byte-writing code sits behind `classicSink`, a `Sink` implementation in the same
package whose methods are the existing blitters. `Frame` records and replays
through the classic sink, including cursor and expansion commands. The parity
fixtures that digest the indexed surface see nothing change.

The recorder never batches, reorders or culls beyond what the walk already does.
If two sprites overlap, the list says so in that order.

`ComposeFrameSnapshot` returns the recorded `List` beside `Indexed` and `RGBA`,
which is what the diff tool replays through the GPU executor.

**Pooled frame scratch.** `composeIndexed` arms a per-client `modelScratch` pool
for the recording pass and disarms it after. Feature, projectile and debris
draws borrow from it — `borrowDrawScratch`, `borrowProjectileScratch`,
`borrowPacketScratch`, `borrowModelPacket`, `borrowPolys`, `borrowOutline`,
`borrowModelStates` — rather than allocating a fresh state, transform, piece and
vertex arena per subject per frame. A borrow outside a recording pass allocates
and is owned by its caller, so diagnostic callers are unaffected. The pool
resets with the frame; a borrowed draw stays valid until its slot is borrowed
again. Each `record_parallel.go` worker holds its own pool (§13.9).

**PERF-REND-04 minimap surface submission.** `DrawMinimapLayout` keeps the
canonical two-step integer sampling and unchanged letterbox bars, clips the
picture to the framebuffer, and records one owned indexed `Surface` at
one-to-one output size. Frame-local byte storage is reused in lockstep with list
reset; multiple packets own disjoint ranges, and retained clones copy their
bytes. The shared scaled-index shader subtracts the destination atlas origin
before integer source mapping. Both executors consume that same packet; viewport
markers remain later point commands. This removes per-pixel point/quad
submission without changing the separate PICTURE/MAPPED/FINAL lifecycle
[03 R-MM-01 §1]. A `Surface` may carry a nonzero presentation identity and
revision. That pair identifies immutable source content for the duration of a
durable command: a zero identity retains the dynamic per-call upload behaviour,
and a cloned list preserves both fields while owning its byte slice. The GPU
holds a small bounded set of such textures and writes pixels again only when the
revision (or dimensions) changes; it never hashes a whole source every frame.
MAPPED consumes the committed visibility mapping version, whereas FINAL still
refreshes for committed contacts and blink phase.

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
or a missing image that the structure/key-plane contract requires. The cached
lane also applies the three-term gate the classic composer applies, including
the cached-image discard a `cache`/`shade` script setter raises
[03 R-COMP-01 §4][04 R-MOV-03 §4], so a piece whose cache bit comes back cannot
end up in neither lane's present. An image-less mobile subject with a valid
state and no required key plane draws `All` directly without changing the cached
orientation reference. The cached lane uses the local composition projection;
the direct live lane adds world position before flooring. A keyed body copies
into staging before its live lane and children resolve under the key; a keyless
body commits first and live pieces and children draw directly in painter order.
The retained unit orientation also governs live-piece transform expansion:
sub-threshold turns retain it while current script pose changes still apply.
Live pieces rasterize at 1x into the current union target, including key-colored
texels that erase cached colors. Each attached child uses its own cached/live
present; group waterline and Digger processing follows child composition.
Construction reveal changes a frame-owned copy, preserving the raw retained
body. The direct fill keeps the same exclusive last-row/column clip as the local
rasterizer.

Classic recording consumes these lanes directly. Geometry-only modern recording
retains only cached-lane faces beside the same presentation identity and emits
current live faces every frame; a 3DO feature's projected faces are retained the
same way, beside its map position (§13.12 "Wrecks and other 3DO features"). A
keyed packet carries both lanes: cached faces
resolve, reveal and outline first, then native unshaded live faces enter the
same key plane. A keyless packet commits its cached faces and records direct
projected live faces as the later Model command. Neither route creates a classic
image or a device image cache.
[03 R-REN-03A §4][03 R-RAST-01 §2][03 §5.2][03 R-COMP-01 §4]

Each cached body keeps its own face and vertex arenas and the frame's packet is
copied into them, rather than allocating a fresh packet per rebuild: under
Enhanced interpolation every mobile subject rebuilds every presented frame,
because its blended pose genuinely moves (§13.5).

For its own retained-raster memoization, the classic client also records the
effective presentation inputs that alter the cached physical-index image: the
shaded renderer choice, effective structure supersampling, effective camera
scale, and installed palette-table identity. A change rebuilds the local
memoized image before reuse. These inputs are not additional retail
script-validity or image-purge gates; they prevent a Nanolathe retained raster
from surviving a presentation configuration that changes its pixels.

### 2.3 `internal/platform/gpurender` — the modern executor

Imports Ebitengine; joins `internal/platform/ebitenapp`, `internal/audiobackend`
and `cmd/nanolathe` in the architecture test's Ebitengine allowlist. Forty-five
non-test files; the ones a reader starts from:

| File | Owns |
|---|---|
| `renderer.go` | the `Renderer` value, `New`/`NewChecked`, `Execute`, `Clear`, `Expand`, `SetDisplayPalette` |
| `schedule.go` | the phase scheduler: placement, streams, the cell grid, pooled batch storage, submission, the world affine transform |
| `shaders.go` | the two compiled scene passes — `scene2D` (opaque) and `sceneDest` (destination-compositing) |
| `tables.go`, `atlas.go` | the packed palette table atlas; the shared source atlas for GAF frames, glyph strips, PCX and surfaces |
| `terrain.go`, `sprites.go`, `draw.go`, `deststage.go`, `text.go` | the 2D families |
| `fog.go`, `fog_ordered.go`, `fog_shaders.go` | the fog composite and its ordered-leaf fallback |
| `model_direct.go`, `model_prepare.go`, `model_quads.go`, `model_atlas.go`, `model_shaders.go`, `model_scratch.go`, `model_retain.go`, `models.go` | the model lane (§22), its retained-lane store and `ModelStats` |
| `points.go`, `flash.go` | the lit point plane and the lit-disc atlas |
| `readcopy.go` | the shared "copy only the region you sample" helper |
| `glow.go`, `lighting.go`, `ground_light.go`, `nano.go`, `metal_glint.go`, `model_finish.go` | the Enhanced light and finish layers |
| `water.go`, `water_reflections.go`, `trails.go`, `scorch.go`, `distortion.go`, `tree_heat.go`, `aircraft_shadow.go`, `lens.go` | the remaining Enhanced layers |
| `world.go`, `strategic_icons.go` | the world region transform and the strategic marker/icon layer |
| `visual_controls.go`, `source_lifecycle.go`, `paused.go`, `debug_capture.go` | `SetEffects`, `ResetSources`, `ExecuteOver`, diagnostics |

Public API:

* `New(pal *palette.Tables, w, h int) *Renderer` — uploads the tables once into
  one 256-wide RGBA8 atlas with fixed row offsets (rows 0–255 `ALP`, 256 `PAL`,
  257–288 `SHD`, 289–320 `LHT`, 321 `Gray`, 322 `Blue`), each storing indices in
  the red channel, and compiles every pass. `w`/`h` may be zero to defer surface
  allocation. `NewChecked` is the same and also returns the first shader compile
  error; a family whose shader is nil no-ops rather than panicking.
* `(*Renderer).SetDisplayPalette([256][4]byte)` — updates only the final colour
  row. Index remapping tables and source assets remain immutable
  `[07 R-FE-01 §11]`.
* `(*Renderer).Execute(list *drawlist.List, w, h int) *ebiten.Image` — the
  `Sink`. It sizes the surfaces, runs the pre-`Replay` preparation passes
  (battle lighting, reflections, blast distortion, tree heat, the model lane's
  atlas, projectile reflections), replays the list, submits the schedule and
  returns the composite. It returns nil for a nil list or a degenerate size.
* `(*Renderer).ExecuteOver(list, background *ebiten.Image, w, h int)` — the
  paused path: copy a retained world composite in, then execute a
  foreground-only list over it (§13.10 "Paused world reuse").
* `(*Renderer).SetEffects`, `SetGlow`, `ResetSources`, and the diagnostics
  `DeviceDraws`, `ModelStats`, `FogContentError`, `DebugSnapshot`,
  `DebugLastFrame`.
* Resource caches keyed by pointer identity: tile atlases per (tile set, detail
  set, scale), GAF frame atlases filled on first use, FNT glyph atlases, the 3DO
  texture atlas with its LOGOS frames.

There is **no art binder**. Everything but the palette arrives as recorded
commands; the executor caches by the pointer identity the record carries.

**Two surfaces, both true colour.** `surfaces[0]` is the composite the whole
frame is drawn into and the image `Execute` returns: every source index is
resolved through `PAL` at the fragment that writes it, so there is no expansion
pass (§13.3, C-G8 as amended). `surfaces[1]` holds the read copy the fog run and
the `readcopy.go` layers sample. Index-carrying *sources* — tiles, GAF frames,
glyph strips, the model pages' key plane — stay in the red channel of RGBA8,
sampled nearest in Kage pixel mode, so `index = int(r*255 + 0.5)` recovers the
byte exactly (C-G4).

Batching is specified by the compiled executor of §11: the scheduler groups
commands into phases wherever that is order-preserving (C-G3). The families
whose result depends on what is already there — `Tinted`, `Lit`, `LitRect`,
`ShadeRect`, the model shadow commit, the trail marks, the lit discs and the fog
composite — are device blends rather than destination reads since §13.3, but the
order they impose is the same, so the placement rules are unchanged. Two
overlapping smoke puffs blend one after the other, exactly as the byte writers
do `[03 R-COMP-01 §2]` `[03 R-FX-02 §3]`.

**Source lifetime (host ownership).** The window compares the client's terrain
binding generation after a joined update and before either executor draws. A
changed binding, including teardown to no terrain and reloading the same map,
discards speculative recording and the paused-world image and identity, then
calls `Renderer.ResetSources`. This retires terrain pages, GAF/PCX/FNT source
atlases, model texture pages, fog sources and their dependent compiled runs,
upload identities and frame scratch. Shared images are released once. Shader
programs, palette tables, output surfaces, the player's effect selection and
renderer settings survive. Stable bindings, zoom changes and ordinary frames
retain their caches.

This is a host resource-lifetime correction, not a retail rendering claim.
Previously the one process-lived renderer retained each newly loaded terrain and
immutable source identity forever, so memory grew with the number of battles.
The terrain reference also retains its movement-service binding.

### 2.4 `internal/platform/ebitenapp` — the switch

The adapter owns one `client.Client` and, lazily, one `gpurender.Renderer` built
on the first modern Draw, so a classic run never allocates device textures.
`RendererMode` is `RendererClassic` or `RendererModern`; it is host state, never
client state, and an unrecognised value presents classic. `Draw` either uploads
the classic bytes or executes the list (§13.10).

Runtime selection exists and has two routes, both funnelling through one
`setRenderer` cleanup: **F10**, which the client counts and the adapter services
inside an update body, and the **options page / settings file**, polled once per
update through `RunOptions.PresentationSettings`. `setRenderer` cancels any
speculative record, disarms the pipeline, and turns interpolation, the Enhanced
flag and the synthesized detail art off when classic takes over, or on when
modern does; the retained screen bridges the swap. Neither key is a retail
binding.

### 2.5 `cmd/nanolathe` — flags and capture

`--renderer classic|modern` selects the start-up executor and the default
`--shot` executor. `--shot-renderer modern` records geometry, executes it in a
hidden Ebitengine loop, then reads pixels for the PNG. Explicit
`--shot-renderer both` also composes the independent classic reference and
writes both images and their diff. Readback and PNG encoding are capture costs,
excluded from presentation timing (§6). The diff tool is `tools/framediff`.

The render-type-2 `Lens` is a destination reader at its exact projectile-list
position [03 R-FX-01 §4]. Both executors sample a copy containing earlier
commands; later lenses see earlier lens output. Its fixed map is shared immutable
data. Geometry follows the existing view-scale policy, with no age deformation
or light/alpha-table capability gate. Safe sampling computes only admitted output
cells; the verified map's changed samples remain inside the viewport.

The command carries the consumer's transparent key explicitly. The producer uses
zero under SPEC_CONFLICTS SC18 and retains a `TODO(question)` for retail's
uninitialized key. Classic compares indexed source bytes exactly. Modern's
accumulated surface is RGBA: its explicit approximation compares sampled RGB to
the active display palette's key colour. Duplicate palette colours and enhanced
colours do not retain original index identity. This limitation changes no lens
geometry and does not justify introducing an unverified retail key constant.

## 3. Contracts — C-G1 … C-G11

* **C-G1 One walk.** `drawCommittedFrame` is the only committed-frame ordering.
  It records; it does not know which executor will replay. No executor walks the
  committed frame `[03 §1]` [I6].
* **C-G2 Physical indices only.** Every colour byte in a record is a physical
  `PALETTE.PAL` index in a `uint8` field. Logical GUI colours, primitive
  colours, FNT colours and `dcb[]` entries are resolved through the logical map
  **before** recording, as the presentation design's C7 already requires of the
  byte writers `[03 §4.3]`. GAF, PCX and TNT bytes are physical already and pass
  through. Geometry is `int32`; the only wider integers in a record are
  non-index scalars (a scorch variant, a water tick, a wind heading).
* **C-G3 Replay preserves order.** `List.Replay` visits commands in record
  order. An executor may merge consecutive commands into one device draw only
  when no merged command's pixels depend on another merged command's result:
  opaque writes merge freely; destination-compositing commands merge while their
  rectangles are pairwise disjoint, and, since §13.11, also while they belong to
  the same blend stream, because one pass applies its fragments in primitive
  order and that order is record order. A run's vertex storage is bounded by
  `schedRunVertexLimit` (2²⁰ vertices) and a batch that reaches it is submitted
  and restarted before the next quad; this executor limit never drops or
  reorders geometry. Device indices are `uint32` through
  `DrawTrianglesShader32`, so no quad count can wrap an index onto earlier
  geometry.
* **C-G4 Exact index arithmetic on the source side.** Every index a source
  carries lives in the red channel of an RGBA8 image, sampled nearest in pixel
  mode, decoded by rounding, and every table operation applied to it is an
  integer texel fetch on the uploaded table. There is no linear filtering of an
  index, no blending arithmetic on indices, and no float intermediate that can
  land between two entries. Where a fragment must blend — the coverage resolve
  of §17, the filtered terrain of §16.3 — it blends **colours**, after the `PAL`
  lookup, never indices.
* **C-G5 Durable model composition.** `drawlist.Model.Classic` owns each mutable
  classic body/staging/shadow plane and placement scalar at record time; a
  retained `List.Clone` deep-copies those planes. The pre-punched shadow is
  built while recording, so classic replay has no `UnitDraw`, client model
  state, or per-frame index lookup. It preserves shadow, one body blit, then
  trace-observer order, including a carried child's earlier shadow-only command.
  The optional trace image and observer are diagnostic-only and cannot affect
  pixels. Modern geometry stays a separate owned packet.

  In the modern executor every subject's faces are rasterized into a region of
  a per-frame 2× atlas page and committed as one quad (§22). Ordinary face
  pixels take a per-subject maximum-key pass and a colour pass in original face
  order, so ties retain the later face [03 R-REN-03A §2–§3]. Conventional
  triangle interpolation is an intentional GPU approximation (§5.1); tests
  isolate key admission from that approximation. No scene-wide depth buffer may
  replace subject painter order. Texture key-colored texels still participate in
  face ownership; composition transparency is applied at the image boundary
  [03 R-REN-03A §5]. Cached body, live pieces and attached children remain
  separate stages, and the nanoframe reveal, the outline, the waterline tint and
  the Digger erase remain per-texel verdicts in their established order — the
  lane evaluates them at the fragment rather than in separate passes. A carried
  child that casts a shadow composes a second time in a region of its own, with
  its own keys and verdicts and no carrier clip, because a shadow is cut from
  its subject's own finished image [03 R-REN-03D §1]. A subject no page can hold
  takes the native painter-order fallback and its shadow is omitted; invalid
  geometry or unavailable resources remain explicit skips (§9). It never
  substitutes a CPU-rendered model image.

  Classic image copies use a separate pool owned by the recording draw list.
  Every body, shadow and trace copy gets a distinct slot until `List.Reset`;
  composition scratch never aliases the recorded planes. `List.Clone` and
  `ModelCommands` still deep-copy all mutable planes for retained consumers.
  Carrier/factory staging images borrow their own composition scratch slot and
  are copied into the draw list only after child composition finishes.
* **C-G6 Structure supersample.** The classic executor preserves the cached/all
  versus live gate, pre-shear doubled projection, ordered ALP colour resolve,
  and top-left key resolve [03 R-REN-03A §6–§7]. Live pieces draw at native
  scale afterward. Mobile units are not supersampled in retail. Enhanced
  replaces this with the subject-wide coverage supersample of §17: every subject
  is rasterized at 2× and box-resolved by coverage in the commit fragment, live
  lane included, outline endpoints whole pixels, no fringe (§22). The Anti-Alias
  display option gates both, but it gates different things: in classic it
  decides whether a structure is doubled; in modern it decides only whether the
  **recorder** hands the executor a pre-doubled raster, because the lane doubles
  the native corners itself when it does not.
* **C-G7 Fog composition.** The recorded fog ops are converted to a per-tile
  grid texture (kind, variant, frame, pattern parity) and applied by one shader
  over the world image: solid fills write the dark index, gray fills desaturate,
  patterned fills test the same `(x + y + parity) & 1` the byte writer tests, and
  fog GAF frames sample their frame `[03 §3.3]` `[03 §4.3.3 R-RR16-A §1]`. The
  visible result per pixel is the byte writer's, subject to Enhanced's approved
  colour arithmetic (§13.3). A composite frame, a frame that extends past its
  atlas tile, or a frame beyond the atlas's range takes the ordered leaf path of
  §13.3 rather than a flattened atlas mask.
* **C-G8 Expansion last, PAL only.** In the classic executor the final pass maps
  index to colour through `PALETTE.PAL` alone, forcing alpha opaque as the
  software expansion does. Nothing after it in the retail composite exists.
  Enhanced has **no** separate expansion pass: it resolves each source index
  through `PALETTE.PAL` at the moment it is written, which is the same lookup
  applied per fragment instead of per frame, and every colour the retail
  composite would have looked up is looked up the same way. `Expand` survives as
  a barrier: it submits everything pending so a caller reading the composite
  after the marker sees a finished frame.
* **C-G9 Model textures resolve once.** The 3DO texture atlas is built from the
  same resolution the classic path performs at load: case-insensitive name
  against the side's texture set then the fallback set, a miss becoming flat
  colour, exactly-ten-frame entries being LOGOS team textures chosen per owner
  at draw time, other multi-frame entries animated by the phase-7 sequence
  cursors `[03 §2.4.1]`. The record carries the resolved frame, so the atlas
  never re-resolves a name. The recorder memoizes the resolution per compiled
  model (`modelTexRefs`) and reads the face's material annotation once at that
  bind, under a generation stamp, rather than per face (§29.1).
* **C-G10 No device in tests.** `internal/drawlist` and the recorder are covered
  by ordinary tests with no window. GPU parity runs in the retail tier, through
  `--shot-renderer both` and `tools/framediff`, because CI has no GPU. Small
  authored GPU fixtures run on a device-equipped host under
  `NANOLATHE_GPU_DEVICE_TEST=1`, without retail assets. Backend compilation and
  pixel execution require real device validation.
* **C-G11 Classic is the reference.** Where modern differs from classic, the
  difference must be diagnosed as a defect or an intentional approximation or
  enhancement in §5, with reproducible visual evidence. A divergence in §5 names
  the classic behaviour it replaces and cites the research the classic behaviour
  implements. Retail parity remains classic mode's contract, owned by the
  presentation design; Enhanced follows its separately designed presentation
  divergences.

### Retained list camera ownership

`List.Clone` captures each non-nil terrain camera by value, including zoom;
subsequent movement of the live camera cannot change a cloned terrain command.
Nil-camera projection remains distinct. The ordinary recording path retains its
same-frame borrowed camera and adds no snapshot allocation. Modern terrain
already uses the command's copied origin and destination dimensions.

Together with C-G5's owned classic model planes, this makes a cloned list
durable across later recording. Modern geometry packets remain independently
owned. Retained terrain and model tests replay A after recording B with changed
camera or pose and compare actual classic pixels, including zoom and nil-camera
projection.

## 4. Retail behaviour that is not a bug

Everything the presentation design lists under this heading holds for both
executors, because both replay the same record. Two are worth restating because
a GPU habit would "fix" them:

* **A structure paints over an aircraft in a later row.** Cross-subject order is
  Y-row painter order with no depth test `[03 R-RAST-01 §7]`. The height key of
  C-G5 is a property of one subject's image, never of the scene. Each subject
  gets its own atlas region so that no two subjects share a key plane.
* **The anti-aliased building has a coloured fringe.** The classic two-by-two
  resolve blends the background index in `[03 R-REN-03A §7]`. A resolve that
  excludes uncovered texels is cleaner and wrong — so classic keeps it, and
  Enhanced deliberately drops it (§17.6).

## 5. Divergences

### 5.1 Model raster approximation

Model faces are triangulated and their attributes interpolated by the graphics
backend instead of by the classic authored-polygon fixed-point edge/span walk
[03 R-RAST-01 §1]. This changes face interiors as well as edges. The reason is
to use conventional GPU rasterization while preserving the original look. Large
occlusion errors, unexpected missing subjects, incorrect stage ordering and
unstable seams are defects, not covered by this allowance. Human review of
captures and motion is the acceptance gate.

Two properties keep the approximation bounded. A four-corner face — flat or
textured — evaluates retail's own two-chain span mapping per fragment from the
parameter image (§11.2 "Textured quads without strips"), rather than a generic
inverse-bilinear map, so a non-parallelogram quad does not develop the diagonal
bend that motivated the original strip path. And the key pass and the colour
pass evaluate the same mapping, so the two never disagree about a texel's key.
Rings that are not four-corner faces interpolate their lanes linearly.

The remaining visible difference inside a face is shade rounding: retail looks a
shaded texel up in the SHD table, and the lane multiplies by the scale that
table was generated from (§13.2). The device raster is also sensitive to where a
subject's region lands on the atlas page — the shader arithmetic is
translation-invariant, but the device's edge and varying interpolation are
evaluated at absolute page coordinates, so a lane sitting exactly on a rounding
boundary can flip when a region moves. It is a few texels per two-megapixel
capture. A capture comparison between two revisions is therefore only
byte-exact when their packing is identical; a packing change is reviewed on the
model preview captures and by inspection, not by pixel count.

### 5.2 Enhanced zoom and strategic view

The detailed view is designed in §14: a view scale in half steps (1×, 1.5×, 2×),
2× terrain and feature art synthesized from the map's own pixels at load time and
resampled to 1.5× by nearest sampling, and model geometry rasterized at output
scale, with every world-space layer, picking, fog and the minimap sharing the one
transform.

Continuous zoom between the steps, and the strategic view below 1×, are **§16**,
and they are modern-only: the classic executor keeps §14's three steps exactly.
Strategic markers show only player-known information — they take the minimap's
own committed contact records and its own admission gate, so the two cannot
disagree (§16.11). Identified units draw generated icons (§18); unidentified
contacts keep §16.11's square. HUD and cursor scale stay independent of the view
scale in every case.

The user-requested expanded sidebar prototype is specified in
[DESIGN_INTERFACE_HUD_INPUT §3.3](DESIGN_INTERFACE_HUD_INPUT.md#modern-expanded-sidebar-prototype).
It uses spare framebuffer height to show authored orders and build controls
together, adding complete build rows as space permits. This is a modern HUD
composition change; the GPU executor and world zoom transform are unchanged.
Classic retains the single-page rail of [07 R-HUD-05].

### 5.3 Enhanced interpolation

Enhanced targets the display's refresh rate while retaining the 30 Hz
authoritative simulation. Rendering more often without interpolation repeats
committed poses. Enhanced interpolation uses two immutable committed snapshots;
it never writes interpolated values back or consumes simulation RNG. Object
identity across slot reuse, spawn and death, teleportation, child attachment
changes, piece animation, input latency and pause behaviour are specified in
§13.5. Original retains committed-tick sampling; I6 names Enhanced as the one
presentation path allowed to read two committed ticks.

### 5.4 The Enhanced layers

Lighting, glow and antialiasing are no longer deferred: model antialiasing is
§17, glow is §19, battle lighting is §23 and §31. Each is a named divergence
with its own section, its own switch (§30) and its own verification. The
classic executor composes identical pixels whatever any of them says.

## 6. Verification

Three independent gates replace a single tolerance gate:

1. **Classic regression.** Compare current classic against a baseline from the
   same simulation/content revision. Renderer-only work must preserve classic
   bytes and equal-tick simulation fingerprints. Attribute upstream baseline
   changes before refreshing; never hide them in a tolerance.
2. **GPU comparison.** Record once and replay the same ordered list through
   classic and modern, save both images and a diff, report changed
   pixels/clusters, and fail on execution/capture errors. Test that the tool
   rejects a deliberately wrong image. Exact non-model fixtures remain exact;
   model approximations and every blended Enhanced pixel are reported, not
   gated, because they differ by design. No rule that merely connects a
   difference cluster to an edge, and no threshold derived as automatic
   approval. `tools/gpu-compare` is the runner; `--shot-renderer both` and
   `tools/framediff` are what it drives.
3. **Performance.** Repeated device-backed replay of one frozen list after
   warm-up, or the live battle benchmark of
   [BATTLE_BENCHMARK.md](BATTLE_BENCHMARK.md). Do not re-record a frozen list in
   the measured loop: recording can consume private presentation RNG and drain
   audio. Report one-time preparation separately. Screenshot readback and PNG
   encoding are excluded from all rendering/cadence metrics. Report CPU
   submission duration and observed Draw-to-Draw cadence (median/p95/p99), with
   pacing settings, hardware/backend, resolution, frames and scene. Cadence
   includes device backpressure, presentation and host scheduling; submission
   alone is not GPU time, and Ebitengine's public API exposes neither GPU
   completion nor actual presentation timestamps, so both are reported as
   unavailable. A frozen-list benchmark excludes simulation and recurring
   preparation, so it cannot establish a universal gameplay frame-rate claim.
   Existing headless `--profile-seconds` measures classic composition only.

**Device fixtures.** `NANOLATHE_GPU_DEVICE_TEST=1 go test
./internal/platform/gpurender` runs the opt-in authored fixtures on a real
device; ordinary tests skip them, and `TestMain` keeps the optional Ebiten loop
on the process main goroutine for macOS. They need no retail assets. The
fixtures cover the model lane's verdicts, fog, tinted overlap, carrier/child
composition, glow, water, reflections, distortion, tree heat, scorch, trails,
aircraft shadows, the strategic icon batch and the paused-world split. Several
accept an environment variable naming an output PNG directory so a reviewer can
look at what the fixture asserted.

They are not optional at merge time. `tools/check-retail`, the pre-merge gate
(ARCHITECTURE §6), ends by running them whenever the session has a window
server — an Aqua session on macOS, `$DISPLAY`/`$WAYLAND_DISPLAY` elsewhere — and
on a headless host prints `GPU device fixtures: SKIPPED` instead of counting
them as passed, because a missing graphics device is a property of the machine
and not of the change. Renderer work is merged from a host with a display. That
run inherits the gate's exported retail root, so the handful of fixtures that
render the installed art — the explosion sequence's onset contract among them —
opt themselves in there; on a bare `NANOLATHE_GPU_DEVICE_TEST=1` run by hand
they return early and assert nothing, which is why a developer run of the
fixtures is not a substitute for the gate's.

The gate runs them as a **separate** `go test` invocation, filtered to
`-run '^TestDeviceFixtureLoop$'`, and that separation is load-bearing.
Ebitengine allows one `RunGame` per process and rejects image allocation once it
returns, so `TestMain` hosts every device check inside the one hidden loop and
`skipAfterDeviceLoop` skips the fixtures that build their own renderer — the
scheduler, atlas and retained-list cases — once the loop has finished. In a
single process the two sets are mutually exclusive, so setting the variable for
the gate's whole-tree run would have bought the device fixtures by quietly
dropping the others. Two processes keep both: the whole-tree run sees no device
loop, and this run is nothing else. `TestMain` starts the loop whatever `-run`
selects, and every other device test in the package reports the same single
loop result, so the filtered invocation exercises all of them; the per-area test
names exist so a failure reads as the area a reviewer is looking at.

**Scenes.** Add authored focused fixtures plus model-rich captures:
flat/textured, shaded/unshaded, equal keys, painter order, cached/live structure
parts, children, waterline/digger, reveal/outline, shadows and overlapping
effects. The activated, open ARMSOLAR is a mandatory human-review scene: its
light-coloured base must be visible around and between the intersecting panels.
That ownership relationship is a hard correctness gate, not an allowed raster
approximation, and the capture must report that the GPU model path was used.
Capture closed and activated/open poses, a close crop and an ordinary
gameplay-scale view. The solar/lab ownership experiment [03 R-REN-03A §3]
isolates this composition behavior but proves no texture interpolation contract.
Inspect sequential frames, resize, clipping and cache reuse. Backend coverage is
reported honestly; one host is not cross-platform proof.

Capture recipes, command settings and revision metadata are versioned; retail
images and assets stay local and uncommitted. Preserve the classic reference
independently; §9 prohibits substituting its output for a missing GPU stage.
Unsupported cases are listed in the handoff, never described as full GPU parity.

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
| Waterline and digger erase | `[03 R-REN-03A §8]` `[03 R-WATER-01 §2]` |
| Shadow raster, punch-out, tinted commit | `[03 R-REN-03D]` |
| Palette, `ALP`, `LHT`, `SHD`, gray table and their builders | `[03 §4.3]` `[03 §4.3.3 R-RR16-A §1]` `[03 §4.3.4]` |
| Tinted blitter family for strips | `[03 R-COMP-01 §2]` `[03 R-FX-01 §3]` `[03 R-FX-02 §2]` `[03 R-FX-02 §3]` |
| Fog composite tiles and variants | `[03 §3.3]` |
| Nanolathe particles | `[03 §5.5]` |
| Calculated flash tables and the ground halo | `[06 R-WFX-01 §2]` `[03 §4.3.1]` |
| Beam and segment strokes | `[06 R-WFX-01 §4]` `[03 §5.4]` |
| Minimap sampling and blip admission | `[03 R-MM-01 §1]` `[03 R-MM-01 §3]` `[03 §3.9]` |
| FNT text | `[03 §7.1]` |
| Software cursor | `[07 §8]` |
| Camera glide, scroll pass and the wheel's owner | `[07 R-CAM-01 §12]` `[07 §10]` `[07 §2]` |
| The clock's scaled timebase and budget carry | `[01 §4.1]` `[01 §4.2]` |

## 8. (retired)

The P0–P3 prototype work sequence and the staged model-packet API it defined.
Its model-image adapters, CPU fallbacks and native-only structure restriction
were removed; §9–§10 and §22 are the current API and behaviour. The section's
text is in [GPU_RENDERER_HISTORY.md](GPU_RENDERER_HISTORY.md). Do not
reintroduce those adapters.

## 9. GPU-only modern execution

`--renderer=modern` exercises the GPU implementation even where coverage is
incomplete. CPU-rendered model bodies and shadows are never substituted into the
modern frame. Classic remains unchanged and separate.

- `Client.RecordFrame` records geometry and the non-model draw commands without
  allocating or rasterizing software model colour, coverage or height planes.
  CPU transforms, visibility and order decisions, texture decoding and GPU
  vertex preparation remain legitimate preparation work.
- Model shadows, nanoframe reveal and outline, waterline and Digger processing,
  carrier/child composition and the supersample all have GPU passes (§22).
  Unsupported geometry or missing material resources remain **explicit skips**,
  counted in `ModelStats` (`Skipped`, `NoBody`, `ShadowsOmitted`,
  `DirectOverflow`). Resolved subjects retain selection chrome, and both
  recording routes preserve per-primitive texture cursor registration. A missing
  carrier still presents valid children.
- There is no model-image source adapter in the live platform or modern
  screenshot paths. The GPU executor consumes geometry; missing geometry or
  resources records a skip rather than consulting a CPU renderer.
- When `--shot-renderer` is omitted, screenshots follow `--renderer`, so
  `--renderer=modern --shot=frame.png` exercises the GPU. Explicit `classic` and
  `both` remain diagnostic choices. The CPU-only `--profile-seconds` path is
  rejected with `--renderer=modern`; use the GPU capture profiler instead.
- An explicit `--shot-renderer=both` comparison composes the classic reference
  separately as diagnostic work. It does not supply pixels to the modern
  executor, and it preserves a single presentation walk where possible so
  presentation RNG and audio side effects are not repeated.

### Current recording API

`Client.RecordFrame()` selects geometry-only model preparation for live modern
presentation; `RecordModernFrame()` is the same pass without the host
presentation advance, which is what the pre-record worker runs (§13.10). Its
returned list is same-frame data; a caller retaining a frozen diagnostic list
calls `Clone()`. `ComposeFrameSnapshot()` additionally composes the classic
reference and includes GPU geometry and stage metadata; its classic image is
never a GPU input. `ModelPreviewRenderer.RecordGeometry()` returns a durable
list, palette and background with a nil `Image`, while `RecordModel()` retains
the classic preview image for explicit comparisons.

`gpurender` consumes `Model.Geometry` and never reads `Model.Classic`; that
packet belongs only to the classic sink. `ModelStats` reports the lane's
accounting — subjects and shadows placed, faces appended, rings culled, packets
the atlas could not hold, atlas passes, rows and pages, cargo images, lanes
replayed from and captured into the retained store, device draws, phases,
passes, submitted vertices — plus the per-family counters the
Enhanced layers publish. These are diagnostic counts, not simulation data.

## 10. Composition stages the modern executor implements

Model shadows, cached/live composition, reveal and outline, waterline and Digger
processing, attached-unit staging and the supersample are all implemented, and
since the model lane landed they are all **fragment verdicts inside the lane's
two passes** rather than separate stages; §22 is where the current mechanism is
written down. This section records what each stage owes retail, because that is
what a reviewer checks the lane against.

### Model shadows

Only a **structure** projects a separate, quarter-sheared, punched shadow. The
recorder reuses `collectShadowPolys` and `shadowAnchor`, preserving the classic
projection, ordering and placement without allocating software image planes; the
executor rasterizes the silhouette into its own region, and the shadow commit
punches a pixel whose body block is wholly covered and composites the ALP
half-colour once, so overlapping silhouette faces of one subject darken the
ground once and shadows of different subjects darken it twice
[03 R-REN-03D §4–§5].

A **mobile or Digger** subject's shadow is the body's own silhouette, as
retail's is: the recorder emits a faceless packet (`ModelGeometry.Silhouette`)
carrying the body's box, the shadow anchor and the buried or submerged clip key,
and the commit reads the body's own finished raster at that placement, erasing a
texel at or below the clip key and compositing the half-colour of index 0
[03 R-REN-03D §1]. Classic copies the finished body image for those subjects,
clears transparent colour-key coverage, flattens to index 0 and applies the
inclusive cutoff; the two executors agree on the shape. The producer applies the
corrected master/vehicle/Digger gates independently of the structure body's
`Shading` preference [03 R-REN-03D §1, §4].

### Feature sprite raster selection

Feature commands retain their already-offset destination and shadow-before-body
order. `FeatureShadows` is a separate client preference populated by display
settings. `Sprite.Trans` carries the selected raster route: definition flags
apply to static and rest-cursor sprites, while a published live event selects
opaque for both commands [03 R-RAST-01 §6][03 §5.3.1]. Both executors use the
existing keyed and tinted primitives for these selections, including the startup
palette capability; there is no separate feature-shadow shader or shade-row
policy. The publisher carries independent `RuntimeLive` and `ShadowEnabled`
state. A live record without a drawable event cursor does not select static rest
art.

### Reveal, outline and submerged geometry

A geometry packet carries the producer's resolved nanoframe bands, complete
outline rings, and waterline/sonar/owner decision. The reveal is applied after
material lookup, writing the composition background where erased while retaining
key ownership; a replaced reveal index is written flat, as retail rewrites its
plane after shading. Outline geometry is the two row endpoints from each ring,
key-tested against the pixel's key; no polygon-border line primitive replaces
that pass. BLUE TABLE or erasure applies at the inclusive waterline threshold,
then the Digger erase. Keyless subjects skip both clipping passes. The final
body coverage punches its shadow, as in classic. No software image planes are
allocated during recording. [03 R-COMP-01 §3][03 R-WATER-01 §2]

### Attached-unit composition

A keyed carrier owns an ordered list of child geometry and signed height deltas.
Each child keeps its own reveal, outline, waterline and Digger processing on its
**own** key; the carrier's waterline and Digger then clip the child on the
**shifted** key, which is what the staging image's passes do. Its shadow-only
command remains before the carrier's shadow/body command. The signed shifted key
is compared before narrowing to the stored byte, and the next child sees that
narrowed key. The carrier's live lane enters before those merges; after the
final child, waterline and Digger process the combined plane once, then the
group commits once. [03 R-REN-03A §4]

A keyless carrier records independent body commands in painter order; a missing
carrier still records each valid child independently. Nested child groups are
not emitted by the current presentation walk, and the modern executor still
omits a child packet that is keyless or itself has children.

### Structure resolve

Retail doubles a structure's cached lane, resolves each 2×2 block through three
ordered ALP lookups with the top-left sample as the resolved key, and draws live
faces at native scale afterward [03 R-REN-03A §6–§7]. The classic executor does
exactly that. Enhanced replaces it with §17's subject-wide coverage supersample:
every subject is rasterized at 2×, the live lane included, and the commit
fragment box-resolves coverage. Only cached faces enter the recorder's doubled
lane; native live faces join the same raster unshaded.

### Weapon, effect and asset coverage

Weapon and effect producers emit palette-index draw commands through both
executors. The modern path uses GPU lines, points, sprites, destination
compositing and model geometry. A separate per-weapon GPU implementation would
duplicate the presentation rules and is unnecessary. The device capture audit
supplies authored committed views using installed definition properties,
composes the classic reference, then independently calls `RecordFrame` for the
modern list; it resets the presentation CRT copy for reproducible lightning.
Every case must change visible pixels from an empty terrain frame, and model
cases must report GPU body execution and no skips. The audit covers every
installed projectile model identity, the published laser, lightning, flame and
plasma paths, all three calculated-flash tables, named explosion/smoke/flame
art, every published strip family, overlapping smoke and impacts, every
installed unit definition at two headings, every unique feature model, and
representative feature sprite banks and transparency modes.

This is approximate parity with the current classic implementation, not proof of
complete retail behavior or of every animated pose. The shared classic gaps, the
covered-index-1 discrepancy at model commit, and producerless world passes
remain explicit. Render type 2's fixed global sprite binding remains unresolved
in classic as well.

## 11. Compiled execution

Implementation policy, not retail behavior. Classic is untouched by everything
in this section.

### 11.2 Design

The recorded `drawlist.List` is unchanged and remains the contract with the
recorder and the classic sink. The modern executor implements `drawlist.Sink`,
but its Sink methods **compile** the command into a small number of batched
passes; `Execute` submits those passes after `Replay` returns. Order is
preserved exactly where pixels depend on each other and relaxed everywhere else
(C-G3). Byte semantics of every family are unchanged (C-G2, C-G4, C-G7, C-G8).

**One table atlas.** `PAL`, `Gray`, `Blue`, `SHD`, `LHT` and `ALP` are packed
into one 256-wide RGBA8 image with fixed row offsets, index in red. Every shader
receives it as one source image, which frees Ebitengine's four image slots and
lets unrelated families share a shader.

**One scene shader for the 2D families.** Terrain tiles, keyed GAF sprites,
feature sprites, PCX, glyphs, fills, lines, points, indexed surfaces, the cursor
and model body commits are all "opaque" writes: their result does not depend on
what is already there. They draw with one Kage shader (`scene2D`) whose
per-vertex custom attributes select the source — constant index, GAF atlas texel
keyed on the green flag, table row remap for `BlitLit`, scaled integer mapping
for `BlitScaled` and `Surface`, the model commit's coverage resolve. It also
carries `BlitTinted` and the translucent feature body and shadow: they
composite, but under source-over, which is the blend an opaque write already
draws with, so evaluating them in this shader is what lets a tinted sprite share
a device run with the opaque writes around it instead of opening a phase against
them. The remaining destination-compositing families (`FillLitRect`,
`FillShadeRect`, `PointLit`, the trail marks, the lit discs, the model shadow
commits, the strategic marker layer) draw with one
destination shader (`sceneDest`) whose custom attributes select the table and
row. Every parameter rides the vertex, and the aircraft shadow layer's
`AircraftWater` is the **one** uniform on a per-frame draw in the executor: its
four operands are frame constants and its custom lanes were spent on the
subject's atlas rectangle (§34). Sources are the scene atlas (GAF frames, glyph
strips, PCX and the per-frame indexed surface packed into 2048-square pages on
first use), the read copy, the table atlas and the model lane's pages.

**Rectangles and lines compile as runs, not per-pixel quads.** A one-pixel
inclusive frame is four contiguous runs, because every pixel of an edge shares
the edge's row or column and the surviving pixels are the edge clamped to the
intersection of the frame, the clip rectangle and the framebuffer — one run per
edge instead of 2(W+H) one-pixel quads. A Bresenham line walks the identical
integer sequence the byte writer walks, but emits one run per row: within a row
the walk advances x by the same step every iteration and never returns to a row
it has left, so a row's visible points are consecutive. A shallow line costs
about one run per row; a steep line has runs of one and costs what it always
did. The pixel set is unchanged in both cases [03 §5.4][R-SEL-02A].

**The scheduler.** `Execute` keeps a coarse 32-pixel screen grid. Each compiled
command has a clipped screen rectangle and a class, opaque or
destination-compositing. Commands are appended to *phases*; a phase submits its
opaque batch and then its destination batch. A command is placed in the
**earliest** phase its overlaps permit (§11.5), and the placement rules are:

* a destination-compositing command must follow, by a whole phase, every earlier
  destination-compositing command it overlaps **of a different blend stream**,
  or of any stream when either samples the phase's read copy (§13.11);
* a destination-compositing command must be preceded, in the same or an earlier
  phase, by every earlier opaque write it covers, because a phase draws its
  opaque batch before its destination batch;
* an opaque write must follow, by a whole phase, every earlier
  destination-compositing command it covers, because it has to overwrite that
  result;
* an opaque write may share a phase with earlier opaque writes, because one
  batch rasterizes in vertex order, which is record order.

A cell remembers up to four owners with their rectangles, phases and classes;
the rectangles decide, so merely sharing a cell constrains nothing. A cell that
runs out of slots forgets its **oldest** owner and folds that owner's
contribution into a floor every later placement over the cell takes, kept per
query stream — conservative, so it can cost a phase that was not needed and can
never place a command too early. An opaque owner whose phase is zero is not
recorded at all, which keeps the frame's terrain, background fills and
first-phase sprites off the grid entirely.

One relaxation of the rectangle test is exact, not heuristic: a lit point tags
the cell of its own pixel rather than the cells of its batch's bounding
rectangle, and two points conflict only when they write the same pixel. The
pixel set is one screen-sized pixel→phase table stamped with the segment serial
— so nothing is cleared at a barrier — laid out cell-major, so one cell's page
is a contiguous 8 KiB block. A run of points is placed **once**, with the
maximum of its pixels' individual answers: a run's x values are consecutive and
distinct, so no pixel of a run can depend on another pixel of the same run, and
stamping the whole run leaves exactly the tag state placing them one at a time
leaves. A command of another stream sharing a cell with a point is still pushed
past it, because a rectangle test cannot enumerate the point set.

**Barriers.** The clear, a composed attached-unit group's shared staging image,
a model subject the atlas could not hold, and the `Expand` marker end a
*segment*: everything compiled so far is submitted and every later command is
placed after it. Fog is **not** a barrier — it is an ordinary
destination-compositing command whose rectangle is the visible fog region.

**Fog as one pass** (C-G7). The recorded fog ops become a per-cell grid texture
(kind, variant, frame and parity in the four channels) covering the visible cell
range, rebuilt each frame from the record and re-uploaded only when its bytes
differ. One draw reads the phase's read copy, the grid, the fog GAF atlas (all
four variants of both families packed once per family identity and scale) and
the table atlas, and writes the fog result in place of one draw per cell.
Per-pixel results equal the byte writers' [03 §3.3]. The sequential
cell-to-cell dependency the byte writers have is reproduced by the shader
carrying a running index through the 2×2 block of cells that can reach a pixel,
in the op list's own row-major order. A frame that reaches past its atlas tile
is left out and **reported** (`FogContentError`), never silently clipped.

**Textured quads without strips.** Retail's span mapper finds, for each row, the
active edge on the decreasing-index chain and on the increasing-index chain of
the quad, interpolates every lane along each edge by the row's parameter, then
interpolates across the row by the column's parameter [03 R-RAST-01 §1]. A
four-corner face therefore draws as two device triangles whose key and colour
passes evaluate exactly that two-chain mapping per fragment from a per-frame
parameter image — twelve RGBA8 texels per entry: corner positions, corner texel
coordinates, corner key and shade, and the subject's verdict lanes — then floor
before sampling. This is not a generic inverse-bilinear map, which differs from
the retail mapping on a non-parallelogram. It removes the diagonal bend the
strip path was introduced for (§5.1) without CPU scanline work. Rings that are
not four-corner faces interpolate their lanes linearly.

**Allocation policy.** Steady-state frames allocate nothing in the executor.
Batch vertex and index storage is pooled by power-of-two size class and each
batch remembers what it held at the last reset, so its first growth asks the
pool for that size and skips the doublings. The phase list is a high-water pool,
never truncated. The draw options value, the copy quad's vertex and index
arrays, the point rows and runs, the point plane and the model lane's arenas are
all retained across frames. No `map[string]any` uniform map is built per frame —
the aircraft shadow layer's single map and its float slice are allocated once
and written in place (§34) — and there are no per-strip or per-face slice
literals; texture and glyph atlases are built once
per identity, and a surface whose revision changed but whose bytes did not is
not re-uploaded, because Ebitengine's Metal driver builds a staging texture per
`WritePixels`.

**Every atlas cell carries a border — contract Z9.** Under the world transform a
nearest sample near a quad's far edge can round one texel past its source
rectangle, which shows as an intermittent tile seam or a stray foreign pixel
beside a sprite. Ebitengine cannot confine a sample to a sub-rectangle, so three
atlases carry a border of duplicated edge texels: the tile atlas pads each cell
by one texel of the tile's own edge (`tileAtlasPad`), the scene atlas reserves
and fills the same one-texel border around every packed frame, glyph strip, PCX
and surface (`sceneAtlasPad`), and the model lane leaves a two-texel margin
around every region (`modelDirectMargin`). An entry's recorded placement stays
its first **inner** texel in all three, so no recorded source rectangle changes
and a rest step composes exactly what it composed without them.

### 11.3 Public API

`New`, `NewChecked`, `Execute(list, w, h) *ebiten.Image`, `ExecuteOver`,
`ModelStats()` (fields may be removed, never given new meaning), and the
`drawlist.Sink` implementation. `Execute` visits the list in record order.

### 11.4 Verification

Gates in addition to §6: the battle benchmark on both renderers before and after
each change, comparing modern against its own baseline capture; the instrumented
device-call count (draws, phases and passes per frame) reported in the commit
message; the fog, tinted-overlap, carrier/child and ARMSOLAR device fixtures;
and zero steady-state allocations in the executor, measured by the benchmark's
allocation delta.

### 11.5 Render passes, not draws

The unit of device cost is the **destination switch**, not the draw. Ebitengine's
Metal driver opens a render command encoder whenever the destination image
changes, loading and storing the whole attachment, and that switch costs on the
order of 70 µs whatever it draws. The executor's job is therefore to issue as
few as possible; the draw count within a pass and the pixels a pass touches are
secondary. `ModelStats` reports `Phases` and `Passes` (device destination
switches) so the benchmark rows record them.

**Critical-path placement.** A command is placed in the earliest phase its
overlaps permit, not the latest open one — the rule stated in §11.2. Each phase
keeps its own vertex and index storage and a command appends to the storage of
the phase it was placed in, so a batch is still record-ordered inside itself.

**One pass per phase** — two full-size surfaces alternating as the destination so
the read copy and its pass disappear — is designed but **not landed**.
Implemented as written it composes a battle frame whose model shadow commits
lose about five hundred pixels of two million, and the loss survives every
variation tried while the same placement over the read-copy submission is
byte-identical. Why the alternation itself changes those pixels is unexplained;
the executor keeps a read copy per phase that needs one (`TODO(H1)` at the
submit site) until it is. Since §13.3 only fog binds a read slot, so the cost
this would remove is now small.

**CPU.** Both renderers spend the larger part of their main-thread CPU in the Go
allocator rather than in any renderer code: on this platform every fresh heap
span costs a page re-commit and the heap of a loaded battle grows for seconds
between collections, so per-frame allocation is paid at allocation time, not at
collection. The policy of §11.2 stands — a steady-state frame allocates nothing
in the executor, and the recorder and HUD retain their per-frame buffers across
frames. What a frame still allocates is Ebitengine's per-device-call boxing and
its per-destination temporary vertex and index buffers, which reallocate on
every new high-water mark. Preparation records that reference recorder geometry
are cleared after submission and page subject references are retired at the next
frame boundary, including dormant overflow pages; numeric arenas retain their
capacity and contents. The recorded list is the contract and must not change:
classic output stays byte-identical.

**Hidden-frame command lifetime.** Ebitengine 2.10.1 is the minimum backend
version: it completes graphics frames even when the window is hidden or
occluded, without presenting them. An earlier release candidate called Draw but
skipped the queue flush, accumulating ordinary frames' vertex and index copies
until presentation resumed. This is a backend lifetime correction; background
simulation and model geometry are unchanged.

**Capture diagnostics.** The executor counts the actual vertex and index slice
lengths at every triangle submission, including model padding and overflow
passes. `ModelKeyVertices` and `ModelColourVertices` separate the expensive
model lanes; `MaxSubmissionVertices` identifies a single unusually large draw.
The renderer retains the full statistics and `Execute` sequence number of the
frame with the most submitted vertices. These counters exclude image-copy draws,
adapter draws and dependency-internal work, and describe submissions, not
retained heap or GPU memory. They require no per-frame allocation and never
enter authoritative state.

## 12. (retired)

The work units for §11 (G1–G5, H0–H3). See
[GPU_RENDERER_HISTORY.md](GPU_RENDERER_HISTORY.md).

## 13. True-colour composite and refresh-rate presentation

Implementation decisions approved by the user on 2026-09-08, not retail
findings. The goal was 120 presented frames per second with the 30 Hz
simulation interpolated, staying visually close to Original without requiring
index-exact pixels. Why the index-exact executor could not get there is in the
history file.

**CPU/allocation policy.** Reaching a refresh-rate cadence made the executor's
own steady-state allocation the binding cost, so the rounds below established
one rule and every later layer follows it: **a steady-state frame allocates
nothing in the executor**, beyond an atlas that genuinely grows. In practice
that means three things. Scratch is *pooled and hinted* rather than owned by a
frame-varying index: a batch's load is similar from one frame to the next even
though which phase holds it is not, so storage is sized in powers of two,
returned to a pool when a segment ends, and re-taken at the size the same batch
last held, which skips the doublings — and their copies — that walking up from
the smallest class would cost. An arena that must grow grows to hold *what the
frame has already handed out plus the new request*, so it converges in one step
instead of once per request. And a constant reaches a shader as a literal rather
than a uniform, because a uniform map is a per-frame allocation for a value that
never changes. The same rule is why a value read after a call returns must not be
borrowed from per-frame scratch. §11.2 "Allocation policy" states the standing
policy and §11.5 "CPU" says where the frame's remaining allocation actually
lives.

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

(The floor is the mean distance from the formula's colour to the nearest palette
entry; where the two columns agree the table adds nothing beyond quantization.)
So an executor that evaluates the arithmetic in RGB reproduces the retail
composite up to palette rounding, and the rounding is the only thing it loses.
That is the whole of the visual change.

### 13.3 The Enhanced composite

The recorded `drawlist.List` is unchanged; the classic sink and the recorder are
untouched. Sources stay indexed: GAF frames, tiles, glyph strips, PCX and the
model pages' key plane carry the index in red exactly as before (C-G4 applies to
every source). What changes is the framebuffer and the destination-side
families.

**Framebuffer.** One RGBA8 composite surface holding colour. Every opaque
family's fragment resolves its index through the PAL row of the table atlas
before writing (C-G8 as amended). There is no expansion pass; `Expand` remains a
barrier and the composite is what `Execute` returns. The clear writes PAL[0].

**Source-side lookups stay exact.** `BlitLit` (source through one LHT row) and
the model lane's SHD, ALP resolve and BLUE waterline all remap a texel before it
is written; they keep their integer texel fetches and resolve through PAL at the
end.

**Destination-side lookups become blends** with the arithmetic of §13.2:

* *ALP families* — `BlitTinted`, translucent feature body and shadow, and the
  model shadow commits — write `mix(dst, PAL[src], 1/2)`: source-over with the
  premultiplied fragment `(PAL[src]/2, 1/2)`. A shadow commit keeps its body
  punch in the shader so overlapping silhouette faces of one subject darken
  once; overlapping shadows of different subjects darken twice, as the byte
  writers do [03 R-REN-03D §4–§5]. Source-over is also the opaque blend, so the
  first two — the ones that need no source of their own beyond the scene atlas —
  ride the OPAQUE stream and the scene shader (§11.2); the others keep shaders
  of their own and stay in the destination class.
* *Row families* — `FillLitRect`, `FillShadeRect`, `PointLit`, the lit discs and
  the trail marks — scale the destination: LHT row r by `1 + r/30`, SHD row r by
  `0.06875·r`. One blend serves both: source factor destination-colour,
  destination factor source-alpha, so `out = dst · (src.rgb + src.a)`; the
  fragment carries `(min(k,1), min(k,1), min(k,1), max(k−1, 0))`. A factor above
  2 clamps at 2 (SHD row 31 is 2.13; the difference is below the quantization
  floor).
* *Fog keeps one read copy.* The gray remap is a desaturation, which no
  fixed-function blend expresses, so the fog command stays a shader run over a
  copy of its region: luminance `floor((r+g+b)/3)` for the gray fills and gray
  GAF, PAL[dark] for the solid and checker fills, keyed copies for the black
  family. Ordinary stock frames keep one copy pass per frame. If a selected
  frame is composite, extends outside its atlas tile, or lies beyond the atlas's
  frame range, the whole fog command takes the **ordered fallback**: it walks ops
  and children in their original order and clips each leaf only to the target
  [03 R-COMP-01 §2]. Gray/checker recursion applies the raw gate before each
  parent or child and ignores alternate selectors; black recursion selects ALP
  for an alternate child and propagates tint through its descendants. Keyed and
  tinted leaves use the existing sprite streams; gray and checker leaves use a
  masked fog shader in the read-copy stream, so overlapping gray leaves receive
  separate read copies even though modern desaturation is idempotent. Child
  anchors can reach before the cell origin or outside the parent's dimensions
  without atlas clipping. The fallback changes ordering representation, not this
  section's colour arithmetic.

**The scheduler keeps its placement and loses its snapshots.** Critical-path
placement still decides order among overlapping commands — the blends are
order-dependent exactly where the table lookups were. A phase submits its opaque
runs and then its destination runs into the same surface, with no copy between
them; a run is keyed by images, shader and blend, and within one phase's
destination batch the runs may be grouped by blend because the batch's
rectangles are pairwise disjoint or same-stream (§13.11). A whole segment is
therefore one render pass, except where fog takes its copy.

**Blend classes.** `schedOpaque` draws with source-over: alpha 1 or 0 for an
opaque write, and the ALP half-colour fragment for the tinted strip and feature
blit, which is the same blend and so the same class and the same run.
`schedDest` splits into source-over (the ALP families that bind their own
shader) and scale (row families); the fog run binds its own shader and read
slot.

**Attached-unit staging.** Every composed group of the frame is placed on one
staging atlas ordered by destination, with one pass for every group's background
and parent and one pass per child index across all groups, rather than three
passes per group. The merge order inside a group is unchanged.

### 13.4 Verification

* Device fixtures: for every fixture whose commands are all opaque, the modern
  surface equals the classic bytes expanded through PAL exactly. For fixtures
  with blended commands, pixels no blended command covers are exact, and the
  covered pixels' mean RGB distance from the classic expansion is at or below
  the §13.2 floor for that family plus 5 units; the fixture states which
  family's floor it uses.
* Captures through `tools/gpu-compare --report-only`, viewed beside classic;
  `--shot-renderer both` pixel counts are reported, not gated, because every
  blended pixel now differs by design.
* Battle benchmark at 30, 60 and 120 TPS: passes and phases per frame, Submit
  and on-cadence share reported in the merge commit.
* Ebitengine allowlist and `docs/INVARIANTS.md` checks unchanged.

### 13.5 Refresh-rate presentation and interpolation

The composite draws one recorded list in a few passes, so the modern window can
record and replay a list every presented frame. Interpolation needs no new
draw-list family: the recorder is handed a blended view of the two most recent
committed ticks and records it exactly as it records a committed tick.
Everything below is an Enhanced presentation rule; Original keeps committed-tick
sampling.

**Cadence.** The window's Update stays at 30 per second. Everything the battle
step does per host frame — input edges, the follow-camera glide's per-frame step
[07 R-CAM-01 §12], the scroll pass [07 §10], the sub-tick budget [01 §4.2] —
keeps the cadence retail gives it. Draw is called at the display's refresh rate
regardless of TPS; Original presents only after an Update, and Enhanced presents
on every Draw. `--fps N` caps how often Enhanced presents: a Draw that arrives
sooner than the cap's interval (less an eighth of it, the vsync jitter
allowance) returns without recording and the retained screen keeps the last
frame. Draw still sits on the display's vsync grid, so the cap lands on the
nearest refresh multiple below it. Original ignores the cap.

The default cap is 60 FPS. The Nanolathe options page offers 30 / 60 / 120 and
previews changes immediately; OK persists them, Cancel restores the entry value.
Explicit `--fps` overrides the saved preference at window startup, with zero
retaining display-refresh presentation. Captures and benchmarks use their
command-line settings independently of saved window preferences.

**Pointer latency.** Ebitengine's public cursor API reads its most recent Update
snapshot, so the window still samples pointer motion at 30 Hz. Modern positions
the recorded software cursor from that snapshot immediately before GPU replay,
after joining the recorder (`PositionPresentationCursor`). This removes the
additional presented frame of positional delay from deferred input publication;
it does not make input polling refresh-rate-driven. The cursor's shape and
animation, hover, orders, placement previews and camera continue using the
ordinary host step. The GAF hotspot remains authored [07 §8]. Capture keeps the
pointer hidden; the release frame preserves its saved restore point
[07 R-CAM-01 §11]. F11 reads the submitted GPU image, cursor included.

**Fraction.** Read at Draw time, when the modern path records. The scheduler's
time source is the scaled timebase floor(milliseconds × 30 / 1000) [01 §4.1], so
its delta is a whole number of thirtieths and, at the nominal speed, the
budget's carry is identically zero after every step: the carry alone never
resolves a position inside a tick. The client therefore takes a `TickFraction`
option beside `Step`, and the battle supplies **the time since the most recent
tick actually fired**: the controller notes the host millisecond, the carry and
the global tick after every session step, stamping them only when the global
tick moved, and the fraction is `carry at the fire + elapsed seconds × 30 × eff`
with `eff` the clock's effective speed (active × 0.1 [01 §4.2]), clamped to
[0, 1). While paused the budget does not run and the battle returns the value it
last returned unpaused, so the blend is frozen. Measuring from the fire is what
makes the fraction monotonic inside a tick: an Update that releases nothing
saturates it and holds the pose, where reading the wall clock's own phase slid
every blended pose back toward the previous tick for a whole Update and then
jumped two ticks forward.

**Camera.** The camera moves in the 30 Hz step, so Enhanced blends it too: the
client samples the precise camera origin and zoom at the end of every `Step`,
after the scroll and follow passes have moved it, keeps the previous sample, and
while recording presents the affine transform blended between those samples
(§16.5), restoring the complete live camera after the record. The camera's
fraction is **not** the tick fraction: the camera advances on the window's
Update grid, which is not phase-aligned with the simulation's scaled units, so
blending it by the tick fraction would snap it back whenever a tick fired
mid-update. The window adapter timestamps each Update and hands the client
`(now − lastUpdate) × 30`, clamped to [0, 1), before each modern Draw
(`SetCameraFraction`); this is platform time in the adapter, where the input
timestamps already live, and never reaches the client's clock or the sim. A jump
larger than the viewport in either axis at an unchanged zoom (a minimap click or
a bookmark recall) snaps rather than sweeps.

**Two committed ticks.** `frame.Buffer.Previous()` is the slot published
immediately before `Current()`, or nil before the second publication or while a
write is in progress. Presentation reads both slots on the main goroutine
between Updates, when no write is in progress. A nil previous is a snap.

**Blended view.** The Enhanced path records a shallow copy of the current
`Frame` whose `Units`, `Projectiles` and `Effects` slices are replaced by
blended copies held in retained client buffers; every other slice and scalar
(fog, visibility, selection, orders, events, HUD readouts, `Tick`) is the
current tick's. Blending is `prev + (cur − prev)·f` with the fraction as 16.16
and truncation toward zero for `numeric.Fixed`, and along the shortest arc for
`uint16` angles. Blended fields:

* unit `X, Y, Z`, `Heading, Pitch, Bank`, and each piece's `Tx, Ty, Tz`,
  `RotX, RotY, RotZ`;
* projectile `X, Y, Z`, `StartX..Z`, `TailX..Z`, `Yaw, Pitch, Roll`,
  `PropellerRoll`, `MeteorPitch`;
* effect `X, Y, Z` — position only; the sprite and animation cursors, the flash
  tables and the strip assignment are the current tick's.

**Identity and snap.** A published unit match first requires equal nonzero
`InstanceID` values. Publication assigns that presentation-only identity to a
live object and changes it when a pool slot is reused; it is not authoritative
state. Frames that both carry zero retain the fixture fallback: pool handles
carry no generation [01 §6.1], so the match is a handle plus consistency. A unit
then requires the same `Slot` with equal `DefID` and `Owner`, unchanged
`Carrier` and `MoverMode`, the same number of pieces, and a horizontal
displacement of at most 64 world units in the tick, tested per axis first so a
map-crossing teleport cannot overflow the squared distance. If only one unit has
a usable identity, it takes the current pose. A projectile first requires an
equal nonzero `PresentationID`, then retains the existing `WeaponID`, `Shooter`
and `CreationTick` continuity checks; combat assigns one process-local,
presentation-only admission identity for every root and burst clone, carries it
in a parallel array through stable pool compaction, and publishes it without
changing the authoritative packed handle, RNG or retail save state. An effect
matches on `PresentationID` when nonzero, else on `ID`, `EventSeq` and
`StartTick`. Anything else takes the current pose. The 64-unit bound is a
presentation constant chosen above any retail movement rate; it is not a retail
datum.

**Never interpolated.** Sprite and animation frame indices, the nanoframe reveal
band, damage flashes, palette rows, fog, visibility, selection, the cursor and
the HUD. A piece hidden in either tick is drawn as the current tick says. A COB
`turn` with no speed sweeps over one tick instead of jumping; this is accepted.

**Benchmark.** `--benchmark-tps=120` runs one authoritative step every fourth
Draw and presents the four frames at fractions 0, ¼, ½ and ¾, so the report
measures the interpolated presentation; 30 and 60 keep one step per Draw.

**I6.** Amended for Enhanced only: the presentation may read the two most recent
committed ticks and the clock's carry; it writes nothing back and consumes no
simulation RNG. `--shot` and Original never blend.

### 13.7 Outcome of the true-colour round

The composite reduced a 1080p battle frame from about a hundred device
destination switches to about a dozen, which is what put the modern executor on
a refresh-rate cadence at all; the measurements are in the history file. Two
items remained above two per cent of a frame afterwards and each became its own
round: Ebitengine's per-draw vertex conversion, and the lit point volume — one
quad per covered pixel of every flash disc [03 R-FX-01 §4] — which §13.8 and
§13.11 between them removed from the modern lane entirely.

### 13.8 The lit point plane

`PointLit` brightens one destination pixel through one LHT row [03 §4.3.1]. A
disc's ramp is jittered per pixel by a CRT draw [06 R-WFX-01 §2], so the row
changes almost every pixel and span coalescing cannot help: the average span is
1.19 pixels. Two mechanisms keep the batch cheap.

**Placement per run.** A batch arrives as contiguous horizontal runs, because a
disc is recorded row by row, and `placePointSpan` places a whole run at once
(§11.2). `placePoint` survives as the per-pixel definition the run placement is
checked against.

**Geometry as a texture.** The runs of one batch that landed in ONE phase are
written into a rectangle of a per-frame RGBA8 atlas, one texel per covered
pixel, and committed as a single quad over their bounding box (`points.go`).
Four bytes per pixel replace the two hundred and sixteen a quad costs. A group
too sparse to pay for its box, too small, or larger than the atlas can serve
keeps the quad path. The atlas is grouped by phase *number* and its regions are
shelved by height class.

Two properties make that byte-identical rather than close. *The lane*: the scale
blend forms `dst × (src.rgb + src.a)`, and for every LHT row `k = 1 + row/30` is
at least one, so the low lane is exactly 1 and the whole of the per-pixel
information is the high lane. That lane is a multiple of 2⁻²³, so three bytes
hold it exactly; the shader reassembles `r·65536 + g·256 + b` — every term and
partial sum an integer below 2²⁴, so exact in binary32 in any association order
— and scales by 2⁻²³, exact because it is a power of two. *The box*: placement
is unchanged, so two points of one phase group can never be the same pixel, and
one quad brightens each texel exactly once. An uncovered texel is zero, whose
fragment is `(1,1,1,0)`: the blend forms `dst × 1` and writes the destination
back unchanged, which is what lets one quad cover a whole box including pixels
other commands of the same phase own.

**Nothing in the modern lane produces `PointLit` today.** Its two producers, the
flash disc and the ground halo, are lit-disc commands since §13.11, and the
benchmark's point-pixel counter is zero on every frame. `points.go`,
`drawLitPoints` and the `PointLit` path stay in place — they are the classic
sink's own family, the fallback for any future producer, and the definition the
disc atlas's lane encoding is checked against.

**The model overflow barrier stays.** A subject the atlas cannot fit rasterizes
into a fallback page at commit time, and because the page is reused that is a
scheduler barrier, one per overflowing subject. A ring of fallback pages that
merges two segments across an overflow was implemented and measured: with a ring
of two the frame is byte-identical, and with a ring of four the composed frame
**changes**, for a reason the placement rules do not explain, while Submit gets
slightly worse. It is not a performance lever, and the barrier stays.

### 13.9 Two-stage unit recording

A unit's piece transforms, projection, material resolution, polygon collection
and cached-lane rebase read the committed frame and the unit's own retained body
and touch nothing another unit's does, so recording a frame's units is split in
two.

*Stage one* runs after the bucket build, over a persistent pool sized to
`runtime.NumCPU()` participants — the recording goroutine plus `NumCPU()-1`
workers parked on a wake channel between frames, because a goroutine per unit
per frame would spend a visible part of an 8.3 ms budget on the scheduler. Its
job list is every unit the two passes will present on their own: the pass-A
window and mover-mode split of [03 R-RAST-01 §7] and then `presentUnit`'s
strategic-view, carrier-link and model-name gates, resolved once so both passes
read one slot array. Each job computes the unit's geometry pair into a slot
indexed by that unit's position in the committed unit slice. Participants take
jobs from a shared cursor, because per-unit cost varies by an order of magnitude
and a shared cursor balances better than a fixed stripe.

*Stage two* is the unchanged sequential walk. It visits the buckets in exactly
the order [03 R-RAST-01 §7] fixes and appends the Model commands from those
slots. **Nothing is read from a slot until stage two reaches that unit's place in
the bucket order**, so completion order cannot reach the recorded list and the
list is identical whatever the worker count [I1]. A slot carries the
presentation identity it was computed for and stage two checks it, so a slot
that does not belong to the subject in hand is rebuilt inline rather than used.

**Shared state.** Each worker records through a shallow copy of the recording
client that shares every immutable and read-only field with it and owns its own
model scratch arena, so two workers never receive the same borrowed slot; the
arena is carried across the per-frame refresh, so a steady-state frame still
allocates nothing per unit. Writes a worker makes to its own copy are dropped,
which is safe only because stage one is a pure function of the committed frame
and of cache entries that already exist: every orientation and cached-body map
entry a job can reach is created on the recording goroutine **before** stage one
starts, so workers read those maps and write only through the pointers the
entries hold. It is the insertion, not the entry, that cannot be concurrent —
distinct units own distinct entries — and an entry with neither geometry nor
image is indistinguishable from a missing one at every read, so pre-creating one
changes no decision.

Three lanes stay sequential and are named here rather than left to be
rediscovered. **The classic composer** keeps the whole sequential path: its
per-unit work rasterizes palette planes through a different set of borrowed
slots. **Attached children** are computed in stage two, because a child's forced
key plane depends on whether its carrier turned out to have one, which is known
only after the carrier's own pair exists. **A standalone model registry** is
excluded, because it loads and binds models on first use and that is a write to
shared registry maps; a battle registry has every model bound before the first
frame. A parity trace sink is excluded as a diagnostic path.

### 13.10 The record/submit pipeline

Ebitengine runs Update and Draw on the game goroutine and encodes to the device
on a render thread of its own. `Execute` only *enqueues*: the device's vertices
are copied at enqueue, so by the time it returns the client's draw list and its
scratch arenas are nobody's. With VSync on the end-of-frame flush is then
**synchronous**: after our Draw returns, the game goroutine sits in the flush and
the swap until the next Update, and at the Enhanced rate that idle window is
several milliseconds every frame.

**The shape.** At the end of a modern Draw, after `Execute` and the screen blit,
the client records the **next** frame on one persistent goroutine. The game
goroutine joins that record before it touches client state again — at the top of
Update, before input, and at the common entry to the next Draw, for both
executors. An executor switch cancels the pending speculative record, including
its saved presentation-CRT state, before classic can advance presentation. The
modern Draw tail rechecks the executor after its deferred Update before
launching another record: an F10 handled there may have selected classic.
Recording owns the client for exactly the span the game goroutine spends in the
window layer, and the §13.9 worker pool runs inside it, so the pre-record is
itself parallel. Nothing is double-buffered: the list, the point and surface
arenas and the model scratch are reused in place, because the frame that used
them has already been enqueued and copied.

**When the pre-recorded list may be presented.** Only when it is the list a
synchronous record would have produced at this Draw. That is decided by
comparing a `PresentationInputs` digest taken at the launch against one taken at
the Draw that would consume it. The digest is deliberately not a field list of
everything the recorder reads — that list is most of the client and would drift
out of date behind it. It is:

- **the host's mutation epoch**, bumped once per window Update and once per
  benchmark step, which is every point where the host writes client state: input
  and its selection, hover, command page, minimap viewport, pointer capture and
  focus; the simulation step and its publication; the camera the scroll pass and
  the follow glide moved; the executor toggle;
- **the committed frame**, by pointer identity and tick, as a cross-check on the
  epoch;
- **the two blend fractions of §13.5** and whether the camera's is set at all;
- **the camera origin and its two stepped samples**, the surface size, and the
  interpolation and Enhanced switches;
- **the caption ring's producer and display cursors**, because the audio drain
  is the one thing that runs on the game goroutine between a launch and the Draw
  that consumes it, and the ring is what it writes that the recorder reads
  [07 R-HUD-03 §14];
- **the displayed resource pair**, predicted purely for the next presentation
  when launching, compared against the pair advanced by the consuming Draw
  [05 R-ECO-01 §6][07 R-HUD-03 §4].

A mismatch discards the list and records synchronously exactly as before. Misses
are counted by reason (`MissEpoch`, `MissCommitted`, `MissTickFraction`,
`MissCameraFraction`, `MissOther`) so the window's readout says which.

**The pre-record blends at the predicted fraction.** The launcher installs the
two predicted fractions as the recording pass's **input**, not as a hint it may
re-derive: the worker blends with exactly those numbers and the digest is taken
from them, so a hit presents the list the digest describes. Re-reading the
wall-clock producer inside the pass would record the world at the launch instant
while the camera used the prediction, which is the world a present interval
behind its camera. The Draw that consumes the list settles its own fractions and
the digest comparison decides; a miss simply re-records with those.

**What stayed on the game goroutine.** The audio step is called from Draw on
every presented frame whether the list was pre-recorded or not, so its cadence
and its thread are unchanged [03 §8.3] C18. It moved *ahead* of the recording
pass rather than into it: `recordFrame` is the host presentation advance
followed by `recordFrameNoAudio`, and a pre-recorded list was recorded after the
previous frame's drain rather than after this one's — which is why the ring
cursors are in the digest. Resolving the blend fraction moved with it, into
`ResolveTickFraction`, so the digest and the record read one sample of a
wall-clock producer rather than two. The cursor blit resolves its art and
visibility inside the recording pass, covered by the epoch; the window then
replaces only that command's coordinates with the latest Ebitengine pointer
snapshot before replay, without publishing input or changing the recorded world.

**Displayed stocks share the host boundary.** `BeginPresentationFrame` steps the
retained stock pair and drains presentation audio once per presented frame,
before `TakePreRecord`. `Frame` and `RecordFrame` call that boundary; the modern
host calls it explicitly. `RecordModernFrame`, `ComposeFrame` and snapshots do
not advance it. The pre-record worker selects a pure next-step value for
`UIFrame.Resources`; it never writes the retained pair, and the launch records
that same prediction in the digest. Stock changes therefore continue to hit when
prediction and presentation agree, including multiple frames on one committed
tick. Retried or discarded records need no stock rollback. The `--shot` entry
advances once before either or both executors compose.

**What a discarded record must undo.** Almost nothing: the list and arenas are
reset by the next pass, the blended view is rebuilt from scratch, the lazy art
caches are memos, and the trail layer and the feature animation cursors are both
guarded against advancing twice within one committed tick. The exception is the
presentation CRT the segmented projectile pass draws from, which is a stream;
the launch snapshots it and a discard puts it back [03 §2.4.1][I4].

**The tolerance is the host's.** The benchmark and `--shot` pass **zero**, so a
measured frame is byte-identical to a synchronous record; the benchmark also
knows the next frame exactly — the group's next fraction is
`(phase+1)/drawsPerTick` — and a draw that publishes a tick records in place.
The window passes **one present interval**, computed per Draw as
`period × 30 × 65536` quanta, where the period is the one the window
**nominally** presents at (the `--fps` cap, the display's own rate, or the wider
of the two). It is deliberately not the interval this particular prediction was
extrapolated over: a frame that hitched measures a long period, and a tolerance
computed from that period would widen by exactly the lateness it exists to
catch. So the window presents a matching list **at the fractions it was recorded
for**, and the pipeline's one presentation divergence is stated as what it is: a
pre-recorded frame is presented at the instant it was predicted for, and the
error is bounded by present jitter and capped at one present interval. A fixed
tolerance cannot work here: the battle's tick fraction reads a millisecond
source, so at the nominal speed it moves in steps of about 1966 quanta, and a
tolerance below a producer's own quantisation can never be met by a prediction
of that producer.

**The update body runs in the Draw's idle window.** A pre-record can only serve a
frame if every client write that frame reads happened before the launch, and the
launch is at the end of the *previous* Draw. An Ebitengine Update is exactly such
a write, so a frame with an Update in front of it used to be a frame no
pre-record could serve. Ebitengine runs a frame as *(zero or more Updates) →
Draw → flush and swap*, and takes a fresh input snapshot immediately before each
Update it calls; a frame with no Update leaves the game-visible input state
exactly as the last tick saw it. The body of an update therefore moves to **the
end of the modern Draw that the Update call precedes**, after `Execute` and
before the launch. An `updateLedger` counts every Update call and guarantees one
body per call: the call defers when the modern executor's Draw tail is alive and
nothing is owed already, and otherwise runs every owed body inline, so the
simulation can neither step twice for one update period nor skip one however the
window behaves. The classic executor never defers; a Draw skipped by `--fps`
runs no tail, and the next Update call runs the body itself. An exit request
seen from a tail cannot return a Termination, so it is recorded and the next
Update call returns it.

*What it costs is one presented frame of input latency, and that is the floor
rather than an accident of this design.* A list recorded during the previous
frame's flush cannot contain input that arrived after that flush began. Running
the body *early* instead is strictly worse: Ebitengine refreshes the
game-visible input snapshot per tick and not per frame, so an early body would
read the previous tick's snapshot, costing a whole update period rather than a
present. The blend absorbs the shift: the frame that used to present the new
tick at fraction 0 now presents the previous pair at a fraction clamped just
under 1, and `prev + (cur − prev)·f` at `f` just under one is the same pose, so
the sequence of presented positions is unchanged and only its labelling moves.

**Benchmark pacing.** Harness version 2 runs every Draw callback with Ebitengine
updates synchronized to drawing and one explicit host deadline. If a draw
arrives late, the next deadline is based on its arrival; the harness never
catches up with a burst of unpaced draws. A fixed draw-to-tick ratio keeps 30
authoritative ticks per target second at every supported presentation rate;
falling below the target slows wall-clock battle progression without changing
the measured tick sequence [I6]. Renderer warmup covers two simulated seconds.
The cadence timestamp is taken before simulation, after the intentional pacing
wait; the first measured interval is invalid because profiling setup separates
it from warmup. Reports distinguish complete host callback work, explicit pacing
sleep, and time outside the previous callback. See
[BATTLE_BENCHMARK.md](BATTLE_BENCHMARK.md) for field meanings.

#### Paused world reuse

The modern window retains the completed world through its fog barrier while
paused. The retained result is one framebuffer-sized GPU colour image; there is
no retained duplicate draw list or model geometry, and `ExecuteOver` copies it
back before a foreground-only list. A matching world skips recording,
interpolation rebuild, model-cache pruning and world GPU replay. The ordinary
foreground still records and executes every presented frame: drag selection,
strategic markers, HUD, messages, modal panels, build previews, order overlays
and the software cursor. Restoring the world image before that foreground
preserves destination-compositing shade and overlay operations and removes the
previous cursor or gesture. Original and `--shot` keep their full-frame paths.

Pause truth comes from the scheduling bridge's `SetPaused` result;
`Frame.Paused` alone is insufficient because a stopped scheduler publishes no
new tick. Battle attach/restore installs the scheduler's truth after binding the
snapshot; replacing the snapshot clears this mirror. Unpause and executor
switches discard the retained image; resizing replaces its allocation.

`PausedWorldInputs` compares the committed frame identity and tick, frozen tick
fraction, interpolation and Enhanced switches, actual blended camera origin,
viewport and map extents, scale and smooth zoom factor, dimensions, world/asset
binding revision, terrain, detail art, font, palette and display colours, the
shadow, shading, antialias, fog and damage-bar options, and the effect selection
(§30). The actual origin uses §13.5's integer blend and teleport snap, so a
stationary camera can reuse across changing camera fractions while pan, follow
and zoom redraw at their existing cadence. Unit flags, selection and group
labels, fog, features and model-texture animation remain owned by committed
publication and the stopped phase-7 service. The host mutation epoch is
deliberately absent: input changes it covers are rendered freshly in the
foreground.

The per-present randomized segmented-projectile family is an exception: any
published member conservatively disables world reuse, even offscreen, because
full recording preserves its CRT draws and painter position [03 §5.4][I4]. A
renderer trace also disables reuse. No fixed refresh throttle or altered
animation cadence is introduced, so this is not a promise of minimal paused CPU
for every scene. Entering the split joins and cancels speculative recording with
the ordinary CRT rollback; paused Draws never launch another pre-record. Audio
draining and displayed-resource advancement still run once per presentation
before the split, and every Draw still reaches the update-ledger tail. Opt-in
`--stats` counts world recordings and reuses; F11's renderer metadata retains
pipeline and paused-world counters regardless of that setting.

### 13.11 Flash quads and same-stream phases

Two changes, the first exact and the second an Enhanced-only approximation the
user approved on 2026-09-10: the modern executor does not have to match retail's
lit brightening of flashes and halos exactly, it has to look similar. Classic
output is byte-identical either way.

**The same-stream rule.** Until this round a destination-reading command opened
the next phase whenever it overlapped another of the current phase. That rule
was written when a destination read meant sampling a per-phase copy. Since
§13.3 it does not: the row families and the ALP families hand the device a
fragment and a fixed-function blend, and the blend is a read-modify-write of the
real attachment. A pass applies its fragments in primitive order, and inside a
phase's destination batch that order **is** record order — runs are appended in
record order and a run's vertices are its commands in record order. Two
overlapping commands of the same blend stream therefore leave exactly the
per-pixel sequence the phase split left.

A destination-compositing command now opens the next phase only when it overlaps
an earlier one of a **different** stream, or when either samples the phase's read
copy. The streams are the ALP half-blend, the row-family scale blend, and the
read-copy stream; the last has exactly one member, **fog**, so fog splits from
every destination command it overlaps in both directions — the copy is taken
once per phase, and a second command over the same region would read a state the
copy no longer describes. Cross-stream pairs keep the split rather than an
argument about commuting two different pieces of arithmetic. Every other rule
stands: an opaque write over an earlier destination read still opens the next
phase, a lit point still follows any earlier point at its own pixel by a phase,
and the cell grid's forgotten-owner floor stays conservative and is kept per
query stream, because what a forgotten owner costs depends on who asks. A
command's stream is derived from the blend and read slot it already binds, so no
family outside the scheduler changed.

**The lit discs as quads.** An explosion's calculated disc [06 R-WFX-01 §2] and
an effect's ground halo [03 §4.3.1] are the same operation the row families
already express — every covered pixel folded through one LHT row — and the
recorder used to carry each of them as one lit point per covered screen pixel.
Each disc is one command instead:

* `drawlist.Flash` carries the generated frame's identity (table and clamped
  frame index), its `Side` and `Offset`, its texels resolved to LHT rows with
  `FlashTransparentRow` outside the disc, the screen anchor, the view scale and
  the **gate as a rectangle** — what `terrainScreenCoverage` already is: two
  independent half-open range tests against the map, so the admitted set is the
  map rectangle in screen space and no executor needs a callback.
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
persistent RGBA8 intensity atlas (`flash.go`), one 1024² page, which is enough
for all three tables at once. A texel stores exactly what a lit point plane
texel stores — the row family's high lane as a 24-bit fixed-point triple, from
the same `pointLaneBytes` table — so the two paths run the same fragment op and
the brightening per row cannot drift between them; an uncovered texel is zero,
whose fragment is the identity scale, so the disc's transparent ring needs no key
test, and each frame gets a one-texel identity border for the magnified sampler.
The flash is then one quad over its projected extent, the halo one quad whose
fragment recovers the integer `(dx, dy)` from its interpolated corner lanes and
runs the byte writer's own `dx² + dy² ≤ r²` test. Both are row-stream commands,
so they never split a phase among themselves or against the trails and the
fills.

**Divergences (Enhanced only).** The disc is texture-sampled rather than
magnified by a per-source-pixel loop. At the native and detail scales that is
the same pixel set — the loop's span for source pixel `c` is
`[Project(c−Offset), Project(c−Offset+1))`, which is one and two screen pixels
exactly — and both were measured byte-identical. At the 1.5× step the loop's
alternating one- and two-wide columns become nearest-sample columns, which
shifts some ramp columns by a pixel; every differing pixel lies inside a disc
footprint, and the largest difference is what a pixel gaining or losing the
brightest ring costs (`dst × 2` against `dst × 1`), not a lane error. The halo
has no texture and stays exact at every scale. Flash and halo pixels that also
fall under a later opaque command are still overwritten, exactly as before.

### 13.12 Model slot identity and retained shadows

The executor's per-subject slot atlas and its device-side **residency table are
gone**: the model lane of §22 rasterizes every subject each frame into a
per-frame atlas and keeps no region between frames. The recorder's cache key
survives, and it is load-bearing in two places — it gates the retained shadow
projection below, and it is the key of the lane's retained packed-vertex store
(§22.1), which is CPU preparation work rather than device residency. What
survives on the recorder side, and what the executor still owes the frame, is
below; the retired design and its measurements are in the history file.

`drawlist.ModelCacheKey` is what the recorder stamps on a packet:

* `Body` — a serial the recorder gives a retained cached body the first time it
  stores geometry, unique for the life of the process. It is not the
  presentation identity: `InvalidateModelImages` drops every body at once and
  the replacements would otherwise present an identity a consumer still holds a
  raster for.
* `Revision` — incremented on every `replaceCachedGeometry`, which is the one
  place the cached lane is stored. An equal `(Body, Revision)` means literally
  the same retained faces.
* `HalfX`, `HalfY` — the frame's half-pixel offset, which the rebase adds to the
  **doubled** corners alone (§17), so the packet's own origin does not imply it.
* `Lane` — which of the retained object's rasters this is, body or shadow. The
  two share the serial and keep separate revisions.

**The key names the retained CACHED lane alone.** A keyed packet may also carry
a reveal, an outline or a live lane this frame; all three are per-frame, and an
executor composes them after that lane — they follow the cached lane in retail's
order [03 R-REN-03A §4] whether or not the lane was replayed — so none of them
clears the key. The comment on `drawlist.ModelCacheKey` is the authoritative
wording of that rule. A nanoframe's cached lane rebuilds when its construction
fraction moves and takes a new revision then; between those the faces are the
same and the reveal band rides the per-frame verdict entry.

A zero `Body` means "not reusable", and it is the value of every packet whose
raster inputs the recorder cannot prove stable: the direct projected lanes,
children, a shadow whose subject has no retained body, and a feature the recorder
projects per frame. The recorder clears the
key explicitly rather than relying on a consumer to notice. The team texture is
folded in — the cached lane rebuilds when `unitTeamColor` changes, which bumps
the revision. An **animated** model texture is not: the retained lane keeps the
`GAFFrame` it was stored with until some other gate rebuilds it, so the recorder
is already showing a frozen frame there. That is a pre-existing recorder
property.

#### Wrecks and other 3DO features

A 3DO feature — a wreck, or a map-authored model feature — is retained the way a
unit's cached lane is: its projected faces are stored beside an identity and
rebased onto this frame's placement, so its packet carries a key and the
recorder projects the model only when an input of the projection changes
(`featureGeometry`, `internal/client/model_cached_live.go`). The inputs are
exactly the unit lane's: the model by name, the folded piece pose — which is
what carries a corpse's orientation triple [03 R-RAST-01 §6] — the published team
colour for LOGOS faces, the shading option, the supersample gate, the view
scale, the palette tables and the texture index generation the model's table was
resolved under. The **position is not one**: every corner is model-local and the
placement, half-pixel offset, lighting height and waterline are supplied by the
rebase each frame, so the rebased packet is the per-frame projection corner for
corner. The retained path is not taken, and the per-frame projection kept, for a
draw with no frame arena, a pose that does not describe every piece, or a model
with an **animated** texture, whose frames the per-frame walk advances and a
retained lane would freeze.

The identity is the **map position**, because the session publishes no feature
`InstanceID` (§35): one feature stands at a cell and never moves off its anchor
while it stands [05 "Feature instance and terrain cell"], a sinking feature
keeping its X and Z. Feature keys are marked in their top bits so they can never
collide with a unit's publication identity, and two features that stood at one
cell in turn share a key harmlessly — the retained lane is a pure function of the
compared inputs, so a different corpse re-projects and an identical one reuses
faces that are identical.

Feature bodies are pruned by **age**, not by membership of the frame's feature
set: a committed frame carries thousands of features, and building a per-frame
set over them cost more allocation than the retention saved. A body records the
committed tick it was last recorded on and is dropped after `featureRetainTicks`
(30, one simulated second), so a wreck that scrolls out of view and back inside
that window is rebased rather than re-projected, and one that is gone frees its
arenas. A stale entry can never draw a departed feature — the next recording
compares the inputs — it can only hold its arenas for a second.

#### Shadows — contract P4

A shadow is a subject like any other — its own region, its own raster, its own
commit. **The shadow's inputs are not the body's**, and this is established by
reading rather than assumed: the body's retained lane is the *cached* piece lane
frozen at its last rebuild, with the orientation cache holding the root angles
until an axis moves more than seven units, while the shadow projects **every**
piece from the **current** pose. A subject whose retained body is untouched can
have a shadow that genuinely moved — a turret slewing, a factory pad opening —
so gating the shadow on the body's rebuild would freeze a silhouette the body is
not freezing. The two lanes are retained side by side and gated apart.

What the projection reads is exactly: the **model**, by name, as the body's own
gate reads it; the **folded piece pose** (`UnitDraw.PieceStates`), from which
`BuildUnitDrawInto` derives every piece's transform, its hidden verdict and its
world corners, so an equal pose is an equal projection; the **model scale**, and
whether the doubled lane is present at all; and the **shadow gate** —
`CastsShadow` with the palette the projection requires. The subject's
**position is not an input**: `PieceDraw.WorldVertices` are the transformed
corners plus `worldPos` and `shadowLocalVertex` subtracts `worldPos` straight
back off in fixed point, so the cancellation is exact. Neither is the terrain
under the unit, the waterline or the Digger clip: those reach the shadow through
its *placement* (`shadowPlacement`, whose Y is sheared by `GroundY`
[03 R-REN-03D §3]) and through the body packet's own clip fields, never through
the projected corners. A `DontShadow` piece flag exists in the pose and is
compared with it, though the projection does not yet consult it — a pre-existing
gap this section does not close.

The retained lane is therefore stored with **no placement at all** — anchor
zero, half-pixel offset zero — and the rebase supplies both, exactly as it does
for the body. A draw whose pose does not describe every piece of its model is
outside that derivation and keeps the per-frame projection.

**Only a structure's shadow is projected and retained this way.** A Digger or
mobile subject's shadow is a faceless packet (`ModelGeometry.Silhouette`)
carrying the body's box, the shadow anchor and a clip key, and the executor
reads the body's own raster at that placement (§22): nothing is projected,
retained or keyed for it.

#### Page planes are unmanaged

An Ebitengine image is by default a **region of a shared texture**, and which
region it gets depends on its own size and on what else is on that atlas. The
model passes address a page by whole page pixels — the colour pass reads the key
stored at its own destination texel, the commit resolves the 2×2 block under a
native pixel — and that addressing does not survive the page moving. Making a
page's planes taller, with the packing and every subject untouched, moved three
per cent of a battle frame. The page planes are therefore allocated
**unmanaged**, so they are their own textures at every size, and the same
experiment then moves no pixel. Two device fixtures lock the property:
`checkModelSlotNeighbourBleed` varies a neighbour's colour, shape, texture and
scale and moves the subject's own region with a filler recorded before it, and
`checkModelSlotPageSizeIndependence` grows the page past the atlas threshold
with committed-off-screen fillers; both assert the finished frame does not
change. Neither reproduces the defect at fixture scale, so the failing evidence
is the measurement in the history file, which the 180-frame benchmark reproduces
in two minutes.

## 14. The detail view: 1.5× and 2× steps and load-time remaster

### 14.1 Decision

The view scale is one of three steps: 1×, 1.5× and 2×. `camera.Scale`
[F-P1-008] is a `camera.ViewScale`, the scale in half steps —
`ViewScaleNative` (2), `ViewScaleMid` (3) and `ViewScaleDetail` (4), zero
reading as native — and the free fractional zoom the camera once clamped to
0.25..4 stays retired, with every `float32` in the projection. Retail has one
scale, and the point of the magnified views is that each is *the same view drawn
from more pixels*. The projection is integer at every step,

    screenX = Project(worldX − camX) + originX
    screenY = Project(worldZ − (worldY >> 1) − camZ) + originY
    Project(v) = ceil(v·s / 2)

and the inverse used for picking, `world = cam + floor(2·(screen − origin) / s)`,
is its exact inverse at every step: every screen pixel names one world pixel,
and every world pixel projects to the first screen pixel that picks it. At 1×
and 2× both reduce to the multiply and the floor divide the detail view was
built on, so a 2× asset lands on the pixel grid one-to-one and a 1× asset
doubled by nearest sampling lands on the same grid. At 1.5× consecutive world
pixels are alternately two and one screen pixel apart, which is nearest sampling
at 3/2, and the arithmetic is the named type's: `Project` for a position, `Px` —
`v·s/2` rounded half away from zero — for an extent or an authored offset, and
`Inverse` for a picked pixel. The type is distinct from `int32` so that a plain
multiply against a pixel count does not compile.

### 14.2 The view transform — contract D1

Every world-space command is recorded in screen space by the recorder, and the
recorder is the one place the scale is applied. The executors replay recorded
coordinates and never rescale; both replay the same list, so the parity gate of
§6 applies at 2× exactly as at 1×.

At 1.5× every row below holds with `Px` in place of `×s` and the 1.5× variant of
§14.3 in place of the 2× one: terrain tiles are 48 pixels, fog cells 48, the
health bar's half-extents `Px(17)` and `Px(2)`, the shadow's five-pixel step
`Px(5) = 8`. A pre-scaled sprite cannot be phase-exact at a fractional factor —
a source column covers two screen pixels or one depending on where its anchor
projects — so a 1.5× sprite may sit one pixel off the terrain's own sampling
phase; that is the cost of drawing variants rather than sampling on the device,
and it is invisible at 1× and 2×.

| Layer | Position | Size and art at s = 2 |
|---|---|---|
| Terrain | tile origin through `WorldToScreen` | 64×64 tiles from the detail tile set (§14.3), or the 32×32 tile doubled by nearest sampling when there is none; the record carries the scale and the detail tiles |
| Feature sprites, normal and shadow, opaque and ALP-tinted | anchor through `WorldToScreen`, authored offsets ×s | the frame's 2× variant (§14.3), drawn one-to-one through the same blit kind |
| Effect and projectile sprites | anchor through `WorldToScreen`, offsets ×s | the 2× variant; the load-time remaster covers features only, so these are nearest-doubled variants |
| Models: units, 3DO features, projectiles, the build ghost | anchor through `WorldToScreen` | Enhanced: projected local offsets ×s (`scaleModelLocal`, integer) and the image rasterized at output scale from the scaled geometry, textures sampling nearest so each texel covers s×s pixels; shadow, outline, reveal and waterline use the same scaled geometry. Original: the image is rasterized at native size exactly as at 1× and the classic blit doubles it about the anchor, the shadow's five-pixel step included, so the classic frame is a pure nearest upscale. Height keys, the digger erase threshold and the sea level are world heights and do not scale in either |
| Fog | cell rectangle through the same projection | cell edge 32·s; the gray-remap and solid fills cover the scaled rectangle; the fog GAF cell is drawn once from its 2× variant. The dither checker is a destination-pixel test inside the blit, so it stays one pixel at any scale on its own — tiling the 32×32 frame s×s times stamped four cloud edges per cell and drew a cross through every boundary cell |
| Fills: health bars, selection plate, footprint and drag rectangles | through `WorldToScreen` | extents ×s |
| Lit discs and halos | centre through `WorldToScreen` | radius ×s (§13.11) |
| Lines: nano beams, lasers, selection quad, dotted paths | endpoints through `WorldToScreen` | one pixel wide at every scale — a divergence accepted for now; a scaled width needs a width on the record in both executors |
| World-anchored text: group digits, labels | anchor through `WorldToScreen` | glyphs unscaled; text is interface, not world |
| Cursor, HUD, minimap, messages, menus | unchanged | unscaled; the minimap's viewport rectangle comes from `EffectiveView`, the view divided by s |

Classic attached-unit staging uses the child-minus-carrier world projection at
native raster scale, then magnifies the completed union about the carrier anchor
once. It must not reuse the already magnified screen-anchor difference or invert
that rounded difference: the latter loses a pixel at 1.5× for some anchor
phases. At 1× this is the original staging placement [03 R-REN-03A §4].

Picking goes through `ScreenToWorld` and the viewport transform, which already
funnel every pointer conversion through the camera: hover and selection hulls are
projected from world corners, so they scale with the projection; the selection
rectangle converts to a world rectangle with floor division; the terrain cursor
resolve runs on the world point. The camera clamp measures the view in world
pixels (`EffectiveView`, insets divided by s, integer division). Scrolling and
the follow glide move in world pixels per host frame as retail does, so the
screen moves twice as fast at 2×; that is the retail behaviour at twice the
magnification, not a defect. Middle-drag converts the screen delta by 1/s so the
world stays under the pointer.

The audit that lands D1 is the recorder's emission inventory: every `emitSprite`,
`emitFill`, `emitLine`, `emitPoints`, `emitModel`, `emitFog` and `emitTerrain`
site whose coordinates come from the world. A site that draws in HUD space is
left alone.

Fog edge clipping: both executors keep the projected cell origin for GAF
placement and clip destination writes only after the frame offset is applied
[03 §3.3][R-RR16-A §3]. Clamping the origin before the blitter pins a partially
offscreen top or left cell's art to the screen edge, which at 2× displaces the
cloud by nearly 64 pixels. Fill rectangles remain clipped. Regression fixtures
pan black, gray and dithered gray masks past both edges at 1×, 1.5× and 2× and
compare with a crop of the unpanned image, including actual GPU readback.

### 14.3 Detail art — contract D2

The client takes one detail-art provider, installed by the command layer at
battle entry and cleared with the terrain:

* a detail tile set — one 64×64 index tile per entry of `Terrain.TileSet`, in
  the same order — recorded on the terrain command beside the scale; and
* detail sprite banks — for a feature GAF bank named by the map's feature
  definitions, a second bank with the same entry names and frame counts whose
  frames are 2×: width, height and the authored anchor offsets doubled, the same
  colour key, pixels and transparency at four times the count.

The client maps a loaded frame to its 2× variant by entry and frame index, not
by content, so the remastered bank and the loaded bank need not share pointers.
The provider is consulted only while the Enhanced executor presents: the
synthesized art is an Enhanced feature, and Original draws the authored tiles
and frames at every scale, so switching to classic at 2× shows the original art
nearest-doubled. The adapter tells the client which executor presents on every
switch. Where no variant exists — an effect, a projectile sprite, a bank the
remaster did not cover, the whole provider when `--auto-remaster=false`, or any
frame while Original presents — the client builds the nearest-doubled frame on
first use and keeps it for the life of the client, keyed by the source frame's
pointer. The doubled frame is a plain frame: composites with alternate children
are doubled leaf by leaf. Executors see only frames; a variant is just another
frame to the sprite atlas.

**The 1.5× variants.** `formats.(*GAFFrame).Resampled(num, den)` is the general
nearest resample — sizes `ceil(v·num/den)`, anchors rounded half away from zero,
output pixel `j` reading source `floor(j·den/num)` — of which `Doubled` is the
2/1 case. At 1.5× the client draws the provider's 2× variant resampled at 3/4
when one exists (the remaster loses every fourth row and column, which reads
better than the authored frame at 3/2 with its uneven columns) and the authored
frame at 3/2 otherwise; both are built once per source frame and kept, in caches
separate from the doubled ones. The detail tile set is decimated the same way,
64×64 to 48×48, once per provider, and recorded on the terrain command in place
of the 64×64 set: the record's tiles are always at the screen tile size of its
scale, so an executor copies a detail tile one-to-one and resamples the 32×32
tile through `Inverse` only when there is none. The fog cloud frames, never
remastered, take the 3/2 variant in both executors. This variant path is
**classic-only** from §16.2 on: modern's 1.5× is the 2× step shrunk by three
quarters.

### 14.4 Load-time remaster — contract D3

`internal/upscale` holds the two synthesizers that lived in
`tools/mapupscale/patchmatchgo` and `tools/mapupscale/featupscale`; the tools are
thin wrappers over the package, and on a fixed input the wrapper's output is
byte-identical to the tool's output before the move. The premise and the
algorithm are documented in those READMEs; this section records only the engine
contract.

* **Inputs and outputs.** Terrain: the tile set, the tile map, the palette and
  the ALP table in; one 64×64 index tile per source tile out. Sprites: a query
  bank, its example banks, the palette and the ALP in; a parallel 2× bank out.
  Every option keeps the tools' shipped defaults; the engine passes none.
* **Determinism.** Seeds derive from tile and sample indices and tiles are
  independent, so worker count does not change the result. The cache relies on
  that: a cached result and a fresh one are identical.
* **Coverage.** Terrain, and the feature banks the map's plot cells name through
  their feature definitions' `Filename`. The queries are the entries those
  definitions name — the rest sequence and the burn, die and reclaim sequences —
  not every entry of the bank: a bank can hold art no feature uses, and
  synthesizing it would lengthen the first load for nothing on screen. The
  examples are the bank's own entries less the tool's default exclusions (fire,
  explosion, smoke and reclaim art, whose colours leak into idle art); the
  package expresses that as a filtered bank passed in `examples`, while `skip`
  names entries not synthesized at all. Shadow sequences are flat two-colour art
  the synthesizer handles badly; they are skipped and nearest-doubled by the
  client. An entry skipped and an entry the definitions do not name both leave
  nil frame slots, which the client doubles itself (D2). 3DO features and effect
  banks are not remastered.
* **Cache.** `os.UserCacheDir()/nanolathe/upscale/<format version>/<key>` where
  the key is a SHA-256 over the algorithm version and every input byte. One file
  per result with a magic and a version; a file that fails to parse is recomputed
  and rewritten. The cache is derived retail art and is never committed or
  shipped.
* **When.** On the loader goroutine after the session composes, before the
  battle is adopted, for the frontend load and the `--map` direct route; the
  capture route runs it inline and only at `--zoom 2`; a save restore adopts on
  the render thread and runs it inline too, blocking on a cold cache once for
  that map. Every windowed load pays it, even at 1×, because F9 can raise the
  scale later. First load of a map costs seconds; a cached load costs a file
  read. Progress is reported through the loading screen's progress callback
  under its own family so the Terrain bar moves. A synthesis failure is reported
  on stderr and the client falls back to nearest doubling; it never fails the
  load.
* **Remaster status popup.** After 350 ms of remaster work on the loading
  screen, a centered, non-interactive popup uses the installed MSGBOX panel art,
  frontend font and unscaled LIGHTBAR grille. It labels preparation, map tiles
  and sprites separately, and its extra bar reports completed unique tiles
  during terrain synthesis, then frame progress across the sorted sprite banks
  with equal weight per bank. This measures work, not estimated time; the phase
  change may reset the percentage. Each part reports its boundary even on a
  cache hit or fallback, and the family's completion removes the popup. Quick
  cached loads finish before the delay, and disabled remastering never opens it.
  The worker publishes immutable phase/percentage snapshots; elapsed time and
  painting remain on the render thread.
* **Switches.** `--auto-remaster` (default on) enables it; `--remaster <dir>` is
  unchanged — hand-authored 1× overrides mounted above retail are what the
  synthesizer then sees, and are remastered like retail art.

### 14.5 The modern executor at 2× — contract D4

The executor replays recorded coordinates, so most layers need nothing. The
terrain pass builds one atlas per (tile set, detail set, scale): 64×64 tiles from
the record's detail tiles when present, else the 32×32 tiles doubled, sampled
exactly as the native atlas at the native scale. The largest retail tile set is
Lava & Two Hills at 11,561 tiles (a scan of all 276 retail maps), which is a
108×108 grid and 6,912² pixels at 64×64 — one page under a 16,384 maximum image
size and three under a 4,096 one, so the atlas pages against
`ebiten.MaxImageSize()` and draws one pass per page. Storing the index in one
channel would cut the texture to a quarter; that is a follow-up (§35). Model
geometry arrives in screen space and rasterizes as at 1×.

### 14.6 Runtime switches

* **F9** cycles the view scale 1× → 1.5× → 2× → 1× about the viewport centre
  (`ViewScale.Next`) in classic; modern's cycle is §16.8.
* **F10** toggles the executor between classic and modern. The client publishes
  the requested executor; the adapter switches at the next Update, turning
  interpolation and the synthesized art off when classic takes over (§14.3), and
  the retained screen bridges the swap. Neither key is a retail binding. F10
  also updates the shell preference and persists only the renderer field. The
  Nanolathe options page uses the same swap cleanup for live previews and Cancel
  restoration (DESIGN_INTERFACE_HUD_INPUT §3.4.1).
* `--zoom` sets the scale at battle entry and applies to captures too; it
  replaced `--shot-zoom`, whose free fractional values are gone. Classic accepts
  only 1, 1.5 or 2; modern accepts any factor in the free range (§16.8). Left
  unset, the window opens at 1.5× in classic when its framebuffer exceeds
  800×600 in either dimension — the retail 640×480 and 800×600 modes stay native
  — and a restart keeps the scale the player was on. A capture and the battle
  benchmark take no such default: unset is native there, so every existing
  capture and benchmark scene is unchanged and a run's scale is always the one on
  its command line. `--shot-focus` stays. `--fps` is §13.5.

### 14.7 Verification

1. **1× unchanged.** Every capture of the §6 matrix and the battle scene is
   byte-identical on both executors; the 6000- and 54000-tick fingerprints are
   unchanged.
2. **The list relationship.** A test records one committed frame at scale 1 and
   again at scale 2 with the camera on the same world origin and asserts, for
   every world-space command, that the scale-2 coordinates are the scale-1
   coordinates doubled about the beam origin and the extents doubled; HUD
   commands are identical between the two lists. This is the automated form of
   the §14.2 audit.
3. **Picking.** For every screen pixel of a viewport at scale 2, the round trip
   screen → world → screen lands on the pixel's own 2×2 block, and a drag
   rectangle at scale 2 converts to the same world rectangle as the scale-1
   rectangle over the same world.
4. **Parity at 2×.** `--shot-renderer both --zoom 2` on the matrix: the two
   executors differ only in the model raster approximation of §5.1 and the
   blended Enhanced pixels of §13.3, reported as counts; terrain, sprites, fog
   and fills are identical in structure.
5. **The remaster.** The terrain tool's output on one exported map, and the
   sprite tool's output on one bank, are byte-identical before and after the move
   to the package; the cache round trip returns the computed bytes.
6. **Viewed.** 2× captures with the remaster and with nearest doubling, beside
   the 1× capture of the same tick, at least the battle scenes and one
   sprite-heavy map; then the window itself, both executors, both scales,
   toggled with F9 and F10 during motion.

## 15. Trails: Enhanced ground marks

Mobile ground units leave fading marks on the terrain: alternating footprints
for legged units, a pair of track segments for tracked ones. Retail leaves no
marks, so this is a Nanolathe presentation feature — on while the modern
executor presents, absent from Original, never a simulation input [I6]. The
marks are geometry, not art: an oval and a segment whose coverage the shader
evaluates, darkening whatever terrain is under them, so there is nothing to
author and the look is right on every tile set because the terrain's own colour
is what fades. The Marks switch gates the layer (§30).

### 15.2 Placement — contract T1

`internal/client/trails.go` keeps one ring of at most 4,096 marks and one tracker
per unit, keyed by the publication identity the orientation cache uses and pruned
with the live unit set. Once per committed tick, only while Enhanced presents,
every unit the classifier accepts is compared with the point where it last laid a
mark:

- **Class.** The FBI's `TEDClass` word decides: `KBOT` and `COMMANDER` lay feet,
  `TANK` lays tracks, the fixed and flying classes lay nothing. The movement
  class names describe footprint size and terrain rules (most kbots ride
  `TANKSH2`), so they only exclude: a `HOVER` or `BOAT` class lays nothing.
  `CNSTR` and `SPECIAL` cover both walkers and vehicles and fall back to the
  model: a piece named for a leg makes it a walker. The class is cached per
  definition.
- **Gate.** The unit must be mobile (BMcode), on the ground (mode mirror 1),
  complete, within two world pixels of the terrain under it, above sea level, and
  visible to the local player by the painter's own gate: a trail is the memory of
  a walk that was watched, never a sensor. A unit that fails the gate restarts
  its stride where it next qualifies.
- **Stride.** Feet every 10 world pixels, tracks every 8, laid along the straight
  line from the last mark to the current position, several per tick if the unit
  is fast. A step above eight strides is a move (factory exit, transport drop,
  restore), not a walk, and bridges nothing. Feet alternate sides at each mark.
- **Age.** A mark lives 300 committed ticks and its strength fades linearly to
  zero over that life. Ages are tick differences, never wall time.

### 15.3 Recording and drawing — contract T2

Recording projects each live mark through the view transform of §14.2 at the
terrain height under it, culls to the viewport plus a margin, and records the
whole frame as ONE `drawlist.Trails` batch between the terrain record and strip
0, so features, shadows, units and the fog composite draw over it. The batch
rides the optional `drawlist.TrailSink` hook, so a sink without it replays the
frame unchanged.

| mark | centre | half-length | half-width | peak darkening |
|---|---|---|---|---|
| footprint | ±2·FootX·s px across the path, alternating | 4·s px along the path | 2·s px | 0.4 |
| track (two per mark) | ±(4·FootX + 2)·s px across the path | 4·s px (the stride, so segments join) | 1.75·s px | 0.3 |

The modern executor draws the batch as one destination command over the union of
its marks under the row families' scale blend (§13.3): each mark is a rotated
quad whose fragment is `1 − strength × coverage`, the coverage a soft oval for a
footprint and a soft-sided, hard-ended segment for a track, evaluated from the
quad's local coordinates. Multiplies commute, so marks that overlap need no
phase ordering between them, and the scheduler still places the batch after the
terrain it darkens and before everything drawn over it. The capture route
observes every tick it advances (`Client.ObserveCommittedTick`) so a `--shot`
shows the marks the window would.

### 15.4 Verification

`internal/client/trails_test.go` locks the classifier table; a walker laying two
alternating footprints along its step; nothing on first sighting; nothing
re-recording the same tick; fading and expiry; nothing in Original; and no marks
for airborne, elevated or moved units.
`internal/platform/gpurender/trails_test.go` locks the optional-sink replay and,
on a device, that a full-strength footprint darkens its centre to near zero and
leaves the field untouched beyond its half-width and half-length, and that a
half-strength track halves the field along its whole length and not beside or
past it.

## 16. Smooth zoom and the strategic view (modern)

### 16.1 Decision

The modern executor's view scale is a free factor. §14's three half steps stay
exactly what they are for the **classic** executor, and nothing in this section
changes a classic pixel. In the modern executor the factor is continuous: the
mouse wheel over the battle viewport moves it, it eases toward its target on the
host Update grid, and below half scale the world becomes the **strategic view**
— terrain, fog, features and selection with the units drawn as icons. Retail has
one world scale and no wheel zoom; the simulation cannot tell what the factor is
[I6]. The invariant everything rests on:

> **The recorder emits at an integer step; the executor scales what it emitted.**

At 1× and 2× the two are the same number, the executor's transform is the
identity, and the output is byte-for-byte what the build without this section
composed. That is what keeps the §6 parity gate intact while the view in between
is free.

### 16.2 The factor and the step — contract Z1

`camera.Zoom` is the live factor in 1/1024 units: `ZoomUnit` is 1×, `ZoomMax`
2×. It is the free generalization of `camera.ViewScale`, and at the three rest
factors (1024, 1536, 2048) its `Project`, `Inverse` and `Px` answer exactly what
the matching half step's own arithmetic answers — the unit is 1/1024 rather than
16.16 precisely so those three are exact small integers.

`Camera` carries both. `Scale` is the **record step** the recorder projects at:
`WorldToScreen`, the terrain record, the detail-art selection and the fog op
builder read it and are unchanged. `Zoom` is the **live factor** the player sees:
`EffectiveView`, `clampInsets`, `BattleView`, `Drag`, `ScreenToWorld` and the
minimap's viewport rectangle read it, because they measure the view in world
pixels and the view is what is on screen. A zero `Zoom` reads as the step's own
factor, which is the classic executor's permanent state.

The record step follows the factor for the modern executor (`Zoom.Step`): the 2×
step above 1×, the native step at or below it. The classic executor does not
derive its step from a factor: a factor handed to it is one of the three views
and is set through `SetScaleAbout`, which writes the step and the factor
together (`ViewScaleForZoom` names the step). Deriving it instead was a defect
the capture gate caught — it turned a classic 1.5× capture into a 2× one.

### 16.3 The transform and the record extent — contract Z2

**The world region.** The recorder brackets its world commands with a
`drawlist.WorldSpace` marker carrying the factor, the step, the record extent
and the battle viewport. A positive `Factor` carries the unquantized live zoom,
overriding `Zoom` for GPU replay; `OffsetX` and `OffsetY` carry framebuffer
translation after scaling. It reaches an executor through the optional
`drawlist.WorldSink`, so the classic executor and every existing fixture are
unaffected. The region opens after the clear and closes before the chrome; the
strategic marker layer sits between them.

**The rule the region encodes.** A draw positioned from WORLD coordinates
belongs inside a world region; a draw positioned from POINTER or framebuffer
coordinates belongs outside one. A pick test compares like with like: presented
pointer against presented positions, or record pointer (`ScreenToRecord`)
against record positions. Two consequences worth naming, because the first build
broke the rule in both directions:

* The **drag-selection rectangle** takes the pointer's own framebuffer corners,
  so it records after the close marker and its clip is the framebuffer's rather
  than the record extent's.
* The **build ghost** and the **order-queue overlay** are world-positioned but
  composed in the UI stage, after the close marker, so the client exposes
  `BeginWorldOverlay`/`EndWorldOverlay`, a **second** world region the UI stage
  brackets those two with. The executor submits its schedule at every boundary,
  so a second region costs two more submissions on the frames that open one, and
  the battle opens one only when a placement is armed or Shift is held. Inside an
  overlay region the UI helpers that bake a framebuffer bound into the recorded
  command — `UIBlit`'s clip and `UIText`'s default control width — take the
  record extent instead.

**The transform.** Inside the region the scheduler scales every rectangle it
places and every vertex it appends by *factor / step's factor*, about the
**surface origin**. The integer recording origin maps to the framebuffer origin
before the subpixel correction; during interpolated presentation each axis also
receives `(recordOrigin − preciseOrigin) × preciseZoom` framebuffer pixels of
translation. Positions, conservative overlap bounds and inverse shader sampling
use this same affine transform; lengths receive only its scale. The precise
factor is never above the step's, so the transform only ever shrinks, though
translation can arm it even at a rest scale. It is applied at the scheduler's
intake, so the overlap tests that decide a command's phase compare what actually
lands on the composite.

**The record extent.** Below the step the recorded world has to cover more pixels
than the framebuffer has: `recordW = Project_step(Inverse_factor(width))` plus a
two-step pad for the rounding of the two conversions, and the same on the other
axis. `internal/client` keeps it in `recordW`/`recordH`, refreshed once per
recorded frame, and **equal to the framebuffer whenever the factor is on the
step** — which is always, in classic. Every world emission site clips against
it; interface sites keep `c.width`/`c.height`. The modern executor clips world
commands against the extent the marker carries and interface commands against
the framebuffer. The classic byte writer for point batches bounds its own store,
because a `--shot-renderer both` capture replays one list through both executors
and the list may be recorded past the framebuffer.

**Fog.** The fog composite is the one family the generic transform cannot carry.
It reads the pre-fog copy 1:1 under each fragment, so its source coordinates have
to equal its destination coordinates in screen space, and its shader recovers a
fog cell from the fragment's own position, so it has to be told which record
pixel that position is. Its region is therefore transformed by hand, the generic
transform is held off for that one command, and the screen-per-record factor
rides the colour lane the fog quad never used. The shader's cell lattice and
atlas tile stay in record pixels; its dither checker stays a test on the
destination pixel, and so stays one screen pixel wide.

**Lit points.** A lit point READS the destination and brightens it through an
LHT row, so a screen pixel must receive each batch at most once. Scaled quad by
quad, a shrinking transform lands two or three record points on one screen pixel
and brightens it two or three times, which drew the halo as a lattice of over-lit
pixels. The executor therefore **resamples** a lit batch under the transform:
each screen pixel is lit by exactly the record point nearest sampling chooses for
its centre, and the point is placed in screen pixels with the transform held off.
The forward map is the **ceil** form, `sx = ceil(x·k + ox − 0.5)`, at every
offset rather than only at zero: selecting by `floor(x·k)` instead lost whole
screen columns for a non-dyadic factor, because the record point the filter keeps
for screen pixel `s` — `floor((s + 0.5 − o)/k)` — can satisfy `floor(x·k) < s`,
so no point ever claimed `s` and the halo showed unlit stripes. With the ceil
form the two are inverse for every factor at or below one: `floor((s+0.5−o)/k) =
x` implies `x·k ≤ s+0.5−o < (x+1)·k`, so `ceil(x·k + o − 0.5)` is at most `s`
and, since `k ≤ 1`, greater than `s−1` — that is, exactly `s`. At a rest step the
batch takes the path it always took.

**Sampling — contract Z10.** A palette index cannot be interpolated, so every
index-space lookup is a nearest texel fetch and stays one (C-G4). Filtering, when
it happens, happens **after** the palette resolve: four texels, each resolved
through PAL, blended in colour. The terrain takes that path whenever the world
transform is not the identity, because one screen pixel then covers more than one
tile texel and a nearest fetch picks an arbitrary one, so the map's noise crawls
as the view eases and sparkles between adjacent factors. The four taps ride the
tile atlas's own border, so they need no clamp of their own, and the choice rides
a vertex lane rather than a second shader, so the terrain still merges into the
frame's own opaque run. At a rest step the lane is zero and the fetch is the
nearest one this pass has always made, which is what keeps the §6 parity gate
exact. Sprites, model commits and the fog atlas stay nearest. `TODO(question)`:
a keyed source cannot be blended the same way — the four taps straddle the colour
key, so the blend would have to weight by coverage and hand the composite a
fractional alpha, which is an antialiased sprite edge and a look to approve
rather than a correctness fix.

**One screen pixel, exactly one.** A world quad that would shrink below one
screen pixel is given a span of exactly 1.0, not "at least one". The world is
full of one-pixel primitives — the selection quad's lines, a dotted path's dots,
a lit point's row span — and without the floor they fall between two pixel
centres and vanish at an arbitrary subset of factors; with a floor that rounded
up any further they would cover one centre at some factors and two at others, and
a one-pixel line would flicker in width as the view eased. A span of exactly 1.0
contains exactly one pixel centre wherever it starts. Nothing already wider is
touched, and since the transform only ever shrinks, a one-record-pixel primitive
can never arrive wider than one screen pixel.

Atlas borders (contract Z9) are in §11.2 "Every atlas cell carries a border",
because they serve every scale, not only this one.

### 16.4 Picking — contract Z3

Screen to world is `cameraOrigin + floor((screen − viewportOrigin) / factor)`,
integer throughout: the exact inverse of the projection at a rest factor and the
obvious floor in flight. Every pointer conversion funnels through
`Camera.ScreenToWorld`, so the terrain cursor, order targets and the minimap
follow it for nothing. The two pick tests that cannot be expressed as a world
point — the hover hull polygon and the drag rectangle's containment test —
compare the pointer against corners produced by `WorldToScreen`, which projects
at the **record step**; `Camera.ScreenToRecord` bridges them by composing the
live inverse with the record projection, and it is the identity at every rest
factor.

### 16.5 Zoom about a point — contract Z4

`Camera.SetZoomAbout(mx, my, f)` is `SetScaleAbout` generalized: the world point
under (mx, my) is computed through the old factor, the new origin is that point
less the same quantity at the new factor, the record step is re-derived, and the
camera is clamped. The world point under the anchor does not move. The anchor is
in **beam pixels** — the framebuffer point plus the viewport offset (128, 32) —
which is the space `ScreenToWorld` takes, because the recorder stores a world
point at its beam position less that offset [03 §2.5]. The battle's zoom writers
convert the pointer, the viewport centre and `--shot-focus` from framebuffer
pixels at one seam. Anchoring on the raw framebuffer point instead holds the
world 128/f pixels left of and 32/f above the pointer fixed, and the map slides
under the cursor.

**Continuous Enhanced presentation.** The 30 Hz zoom controller owns input
targets and the ease, and its integer camera remains available to ordinary input
and clamp consumers. Beside that origin, zoom operations retain the precise
anchor using `origin + anchor/oldZoom − anchor/newZoom`; a clamped axis takes the
integer clamp result, and another writer changing the integer origin or factor
invalidates the retained fraction. Every host sample carries
`(originX, originZ, zoom)`. At presentation fraction `t`,
`zoom = lerp(previousZoom, currentZoom, t)` and, for each axis,
`origin = lerp(previousOrigin × previousZoom, currentOrigin × currentZoom, t) / zoom`.
This interpolates the complete screen transform, so a world point anchored at
both endpoints stays anchored throughout; blending the origin and the zoom
independently introduces a curved drift, and an origin-only blend pairs the new
30 Hz zoom with the old camera position and repeatedly displaces the world and
pulls it back toward the cursor.

The temporary recording camera uses the whole origin and a quantized factor for
integer culling and layer thresholds, selecting its recording step from the
precise factor; the world boundary carries the unquantized factor and remaining
translation. Record extents round the factor down and add the usual pad. Icons
and tactical guides project through the same precise view before rounding to
their final screen pixels. Strategic picking retains the transform actually
submitted; paused-world and speculative recording identities include the precise
camera samples. The entire live camera is restored after recording.

### 16.6 The wheel, the steps and the ease — contract Z5

`camera.ZoomController` is the state machine, driven once per host Update from
the battle's camera pass. Easing uses host Updates and scroll cooldown uses the
supplied monotonic host milliseconds; neither reads simulation time [I6].

* **The steps.** `ZoomSteps` is the ascending list {0.25, 1, 2}: a tactical
  overview, the default native view, and the detail view. Fractional stops above
  1× were removed after visual feedback on uneven sprite and model scaling and
  its mismatch with filtered terrain. The 0.25× overview shows four times the
  native span on each axis when the map is large enough; smaller maps clamp to
  the minimum factor that fills the viewport (§16.7), and that floor need not be
  one of the named steps.
* **The wheel** requires `ZoomScrollThreshold` of accumulated travel — 1000
  thousandths, one Ebitengine wheel unit — so one conventional mouse click is one
  step. A call moves at most one stop whatever the delta. After an accepted step
  `ZoomScrollCooldownMillis` discards further scroll input for 500 host
  milliseconds; discarded input neither accumulates nor extends the deadline, and
  the triggering event's excess is discarded too, so a burst from 2× first
  targets 1× and cannot queue a second jump to 0.25×. Below-threshold fractions
  accumulate; reversing direction clears that remainder. A step refused at a zoom
  limit does not start a cooldown. From a free factor the wheel takes the nearest
  stop in its direction of travel, and zoom stays anchored at the pointer
  throughout the animation. F9 and pinch bypass the cooldown and clear pending
  wheel state.
* **macOS two-finger scrolling** pans both axes in Enhanced. A local AppKit
  monitor uses `hasPreciseScrollingDeltas` to distinguish point-based touch
  scrolling from conventional wheel events; Magic Mouse touch scrolling also
  pans. Raw point deltas follow the user's macOS scrolling direction, convert
  through the window's letterbox scale into logical pixels, then divide by the
  live zoom. Fractional world-pixel remainders carry between direct deltas, so
  slow scrolling still moves at 2×. Panning clears camera follow.
  `momentumPhase != 0` events never pan or zoom: movement stops on finger lift
  instead of continuing through the inertial tail. GUI controls retain all scroll
  events in Ebitengine wheel units.
* **macOS pinch** accumulates signed magnification deltas until their net
  magnitude reaches `pinchThreshold` (0.12), then requests one adjacent zoom
  stop, anchored at the pointer sampled when the gesture began. Even a large,
  reversed or long-held pinch cannot step again until a new gesture begins, and
  an attempt at a zoom limit spends the gesture. End and cancel events retire it;
  cancellation does not undo an already accepted target. Ordered pinch events
  survive host batching and the semantic input copy. Blocked camera input cancels
  the active pinch.
* **The ease** closes `ZoomEaseFraction` of the remaining gap per Update, moves
  at least one unit so an integer factor cannot stall, and settles outright
  inside `ZoomSettleEpsilon`. It is what makes a notch a glide rather than a cut,
  and it is the only time the live factor is off a step.

Every one of those names is a **feel-tuning knob**, not a derived value; the
wheel and ease knobs live at the top of `internal/camera/zoomfeel.go` and
`pinchThreshold` in `cmd/nanolathe/trackpad.go`. The native monitor implements
these choices using Apple's gesture and scroll-event semantics and returns events
unchanged; empty native polls stay empty rather than replaying Ebiten's copy, and
other platforms retain Ebitengine wheel zoom with no device classification
guessed from delta magnitude or timing.

The wheel binding is Nanolathe's, not retail's. Retail leaves the wheel to the
active GUI list under the pointer [07 §2][07 §10], and the UI boundary still
consumes it first: the camera pass sees only a wheel the chrome did not want, and
takes it only over the battle viewport, only outside TALK, only with no modal
open and the pointer off the minimap, and only in the executor that can present a
free factor. Trackpad controls share these gates and additionally require window
focus and no command palette or unit-info ownership. Skipping the camera pass
clears gesture state.

### 16.7 The minimum factor — contract Z6

`Camera.MinZoom` is `max(viewW/mapW, viewH/mapH)` over the battle viewport and
the playable map, rounded up: the factor at which the view in world pixels
exactly covers the map in whichever axis runs out first, so the clamp never has
to letterbox. Targets below it are clamped, both at the controller and at the
camera. The viewport span is taken in framebuffer pixels, because the chrome does
not move with the zoom.

The camera also retains the requested factor before clamping. Wheel, pinch and F9
navigate that request, so the tactical and native stops remain distinct even if
the map floor gives them the same live factor; animation changes only the live
factor, direct jumps retain the request, and classic step changes replace it with
their own factor. In Enhanced presentation a sub-native request clamped by the
floor becomes a full strategic view when the live factor reaches that floor,
which makes the furthest-out stop usable on small maps and large framebuffers
without turning an ordinary native view into icons. Before arrival the usual fade
applies; at arrival models disappear, icons become fully opaque, and icon picking
and selection outlines take over together. Choosing native or detail clears the
exception immediately. The paused world cache includes this gate; resolution
changes refit the retained tactical request to the new floor.

The step writer deliberately does **not** apply this floor: a step is always at
least 1×, and `clampAxis`'s view-larger-than-map domain stays exactly where
[07 §10] left it.

### 16.8 Runtime switches

* **F9** in classic is the unchanged 1× → 1.5× → 2× step cycle about the
  viewport centre. Modern cycles 1× → 2× → 0.25× → 1× as animated targets,
  sharing the wheel's step list; a free factor cycles to the first step above it,
  wrapping to 0.25× at the top, and the map floor still applies.
* **The wheel** is §16.6.
* **`--zoom`** accepts any factor in the free range for the modern executor and
  only 1, 1.5 or 2 for classic — the restriction is applied after parsing,
  because `--renderer` may follow `--zoom` on the command line. A capture follows
  `--shot-renderer` when one is given. The map-derived floor is applied at battle
  entry, where the map is known. Modern defaults to 1× at every resolution;
  classic keeps its resolution default. Captures and the benchmark stay native. A
  restart keeps the factor the player was on. Battle entry, a restart and a
  capture take the factor outright rather than easing it.
* **F10** is §14.6.

### 16.9 Gating summary

| Factor | World |
|---|---|
| 2× … 1× | everything, recorded at the 2× step |
| 1× … 0.625× | everything, recorded at the 1× step |
| 0.625× … 0.5× (exclusive) | everything, plus the marker/icon layer fading in |
| 0.5× and below | terrain, fog, features and their shadows, selection fills, the build ghost and queue overlay, drag rectangle, dotted paths, icons |

### 16.10 What the strategic view drops — contract Z7

At and below `strategicModelCut` (0.5×) — inclusive, so the 0.25× tactical target
is a marker view — or at the clamped Enhanced tactical stop of §16.7, the
recorder does not emit unit models, projectiles, effect strips, trails, unit
labels or health bars. Terrain, fog, **the features** — sprite and 3DO alike,
with their shadows — the selection quad, the build ghost, the order-queue
overlay, the drag rectangle and the dotted paths still record: they are the map,
and the map is what the strategic view is for. Features were originally dropped
with the units; a play test found that jarring and it is — a marker stands in for
a unit and nothing stands in for a rock, a tree or a wreck, so the map lost its
landmarks at exactly the factor a player pulls out to read them by.

Terrain below 0.5× is the 1× tile set sampled down by the transform.
`TODO(question)`: a half-resolution tile set would sample better and cost a
quarter of the atlas; whether the load-time cost is worth it is unmeasured. The
recorder emits many more tiles and fog cells at a low factor. That path is
allocation-free per frame as the rest of the recorder is, but the per-frame tile
and cell counts grow with 1/factor², which is the cost of the view.

### 16.11 The marker layer — contract Z8

This generic-marker contract is superseded by §18 for identified units and
Enhanced team colour; it remains the contract for unidentified contacts. Below
`strategicMarkerOn` (0.625×) such a contact becomes one filled square of
`strategicMarkerSize` (4) **framebuffer** pixels.

* **Its records and its gate are the minimap's own.** The markers come from the
  committed radar contact list and pass exactly `render.MinimapBlipAdmitted`, the
  gate the minimap's dots take [03 §3.9][03 R-MM-01 §3]. Visibility is not
  re-derived: a unit the minimap will not show has no marker either.
* **Its colour is the minimap's own.** The client is handed the `radlogo` blip
  art at battle entry and takes each player colour's marker index from that
  colour's own frame — its most common opaque index, resolved once per colour.
  Naming a colour instead would be a second table to keep in step with the art.
  (Identified-unit icons take their ink from `logos.gaf` instead; see §18.4 for
  why.)
* **Selection** adds a one-pixel outline in the selection colour, which fades with
  the layer rather than appearing at full strength first.
* **The fade** is linear from alpha 0 at 0.625× to 255 at 0.5×, with full opacity
  at the clamped Enhanced tactical stop. Models are hard-cut at 0.5×; a model
  cross-fade needs an alpha lane on the model commit, whose fragment is opaque
  today, and is a follow-up (`TODO(question)` at `Client.markerAlpha`).
* **No projectile markers.** A shot is an event, not a thing on the map.
* Markers are recorded **outside** the world region, already positioned through
  the live factor, because a fixed-pixel mark must not be scaled by the world
  transform. They draw through a destination op as a premultiplied flat index at
  the layer's alpha.

### 16.12 Verification

1. **The rest steps are untouched.** Classic captures at 1×, 1.5× and 2× and
   modern captures at 1× and 2× are byte-identical to the build before this
   section.
2. **The camera.** `internal/camera/zoom_test.go`: the free factor agrees with
   the half steps at each of them; picking is the exact inverse at rest and the
   floor in flight; zooming about a point leaves that point fixed; the minimum
   factor is tight against the map; the step writer keeps the classic camera on
   its step; `ScreenToRecord` is the identity at rest.
3. **The state machine.** `internal/camera/zoomfeel_test.go`: the wheel's
   threshold, cooldown and direction reversal; the ease terminates.
4. **The recorder.** `internal/client/world_zoom_test.go`: the record extent; one
   world region per frame with the right operands; the strategic drops, the
   surviving selection fills and the surviving features; the drag rectangle
   recorded outside every region; the marker count and the alpha ramp; the
   minimap gate.
5. **The UI stage's own region.** `cmd/nanolathe/battle_world_overlay_test.go`:
   an armed placement records two open/close pairs, the ghost's fills lie inside
   a region, and the recorded rectangle put through factor/step is the presented
   `f·(site − camera)` to within a pixel.
6. **The executor.** `internal/platform/gpurender/world_test.go`: a rest step
   compiles byte-identical geometry, a non-rest factor places a known sprite at
   the expected scaled rectangle, a sub-pixel world primitive compiles to a span
   of exactly one screen pixel while a wider one is untouched, and the lit-point
   resample is the inverse of nearest sampling at every factor and offset.
   `atlas_pad_test.go`: each of the three atlases keeps its border, and the tile
   border holds the cell's own edge.
7. **Viewed.** Modern captures at several intermediate factors: terrain seamless,
   HUD unscaled, features present at every factor, icons present and unit models
   absent below 0.5×; and a seam sweep across many factors before and after the
   atlas borders.

## 17. Model antialiasing: subject-wide supersampling with a coverage resolve

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
composition background index blended in — the coloured fringe of §4. The classic
executor keeps that behaviour exactly (C-G6). The fringe is not preserved: the
user's decision is that it was a defect of the original rather than a look to
keep, and Enhanced is the mode that is allowed to diverge.

The Anti-Alias display option keeps its name and gates both, but it gates
different things. In classic it decides whether a structure is doubled. In
modern it decides only whether the **recorder** builds the doubled lane
(`Client.supersampleGeometry`, which tests the option and a palette and nothing
about the subject); the model lane doubles the native corners itself when there
is no doubled lane, so **every subject is rasterized at 2× either way**. What
the option actually changes in modern is which corners the 2× raster comes from
— the recorder's exact doubled projection, or the native corners times two.

MSAA was considered and rejected: Ebitengine exposes no multisampled render
target, its `AntiAlias` draw option is a stencil path for solid vector fills, and
the model passes are index-space shaders with a key-plane discard that such a
path cannot express.

### 17.2 The recorder — contract S1

When `supersampleGeometry` holds, every recorded `ModelGeometry` carries a
`Supersample` packet at scale 2 holding the subject's own doubled raster: its
`Faces` (the cached lane, or every lane for a direct subject), its `LiveFaces`
and its `Reveal`, all in the doubled packet's local image coordinates. The
outline endpoints stay on the native packet, which the model lane draws as whole
pixel blocks (§22). The outer packet keeps its native faces, box, origin and
anchor; the lane rasterizes none of the native faces when a doubled raster is
present, but the box remains the commit rectangle and the anchor the subject's
screen position.

The doubled projection is retail's: corner `(x, y)` of the native local
projection lands at `2·(x + originX) + hx`, `2·(y + originY) − odd + hy`, where
`odd` is the low bit of the corner's model-relative height, so the doubled shear
is `2z − y` rather than `2·(z − (y >> 1))` [03 R-REN-03A §6], and `(hx, hy)` is
the subject's half-pixel offset (§17.3). The shadow's doubled quarter shear moves
the corner one raster pixel right as well as up when the second bit of the height
is set, which is the same identity applied to `ry >> 2`. At a magnified view
scale the correction is the view's own pixel (`ViewScale.Px(1)`), because the
scaled native offset already lost the half it restores. The doubled packet is
filled straight from the unplaced polygons, so the lane costs no copy of their
corner lanes.

The cached lane is retained **without** the half-pixel offset; the offset is
added when the retained lane is rebased into the frame's packet, so a subject
that moves without changing pose keeps its cache. A keyed subject whose pieces
are all live retains an empty doubled lane so its live faces have a raster to
join. The geometry cache's identity carries this gate separately from the classic
image's structure-only one, so toggling the option rebuilds the right lane. A
direct-projected subject's packet declares its corners' own extent with retail's
two-pixel margin, origin at the box pixel of screen (0,0) and anchor at screen
(0,0), so its region is the subject's size and can be doubled — declaring the
whole record extent instead overflowed the atlas and cost a frame dozens of
passes.

### 17.3 Half-pixel positioning — contract S2

`Camera.WorldToScreenDoubled` is `WorldToScreen` carried in 16.16 at twice the
record step: `floor(2·s·(x − cameraX))` and `floor(s·(2z − y − 2·cameraZ))`,
viewport origin included. A supersampled subject is anchored on that result's
whole pixel (`sx2 >> 1`, `sy2 >> 1`) and its doubled raster is offset inside its
region by the half (`sx2 & 1`, `sy2 & 1`), so the offset is 0 or 1 on both axes
at every view scale and the composition box's margin always holds it. The anchor
can sit one pixel from `WorldToScreen`'s, because retail's shear halves the whole
part of the height while the doubled projection halves the height itself; that is
the subject drawn where it is rather than where retail's truncation put it, and
the classic executor keeps retail's pixel. At 2× the retail anchor was always an
even screen pixel, so units moved in two-pixel steps; the doubled projection
restores the missing step. The shadow projects the ground point, which is what
its anchor projects [03 R-REN-03D §3], with `shadowAnchor`'s five-pixel X offset
on the whole pixel. A direct-projected subject has no shared offset: its corners
are projected one by one, so each carries its own exact doubled position and the
doubled lane takes those.

### 17.4 The resolve

The executor stages §17 designed — a page with separate key, colour and resolved
planes, a resolve pass, and the outline and live lanes as their own passes — went
with the slot stage. The model lane of §22 resolves coverage **in the commit
fragment** instead: it box-resolves the four 2× texels under a native pixel, the
colour the mean of the covered ones and the alpha their share, with the
composition transparent index dropped at the fragment. The outline of a
supersampled subject is drawn from the NATIVE rows as whole pixel blocks rather
than two raster pixels wide, because the doubled lane's own rows would put an
endpoint at a doubled column, straddling two pixels.

For the shadow, both sources are resolved the same way: a structure's silhouette
resolves from its own region and punches a pixel whose body block is wholly
covered, so under a partly covered body edge the shadow stays and the body's own
blend restores the shadowed ground in proportion — the composite the byte
writers' opaque punch approximated [03 R-REN-03D §4–§5]. A group composes at its
parent's scale in one region and resolves once at its commit.

### 17.6 Divergences

* Edge pixels of every subject are blended with the composite by coverage; face
  boundaries inside a subject are averaged. Neither exists in retail.
* Structures lose the ALP fringe.
* The live lane and the outline are supersampled with the body; retail draws
  both at native scale after the structure resolve.
* A subject's doubled raster sits at its half-pixel position, so a subject can
  present half a pixel from where the classic executor puts it, and its resolved
  silhouette differs by that.
* A mobile subject is supersampled too, where retail draws it at 1×.

### 17.7 Verification

1. **Device fixture.** `model_fixture_test.go` and `model_direct_test.go`: the
   supersampled subject's left pixel resolves the mean of its four covered
   samples with the live lane having joined the doubled raster; its right pixel
   resolves three covered samples at three-quarter coverage over the background
   after the child merge and the carrier's waterline; the one-sample edge subject
   resolves at a quarter over the background with no fringe.
2. **CI tier.** `go test ./...` green; the recorder's direct-route tests assert
   the tight box and screen anchor.
3. **Captures.** Model-rich scenes through `--renderer=modern`, magnified:
   silhouettes smooth, no fringe, shadows intact.
4. **Benchmark.** The modern battle benchmark at 120 TPS, run back to back
   against the build without the change.

## 18. Generated strategic icons (modern)

### 18.1 Scope and visual direction

For **identified** units in the modern strategic view, §16.11's four-pixel contact
squares become a small generated symbol vocabulary. Enhanced presentation under
I6/I11, not recovered retail behavior: unidentified contacts keep §16.11's square,
and classic, the simulation and unit definitions are unchanged. The organization —
family shape, role symbol, level mark — follows BAR's public Strategic Icons guide
(accessed 2026-09-11) with independently authored geometry; BAR's implementation,
artwork, balance roles and tier meanings are not inputs. Icons are generated
deterministically from vector geometry into one shared atlas at load time, with no
network generation, randomness or image generation during play; units of the same
verified family and role may share one, and exact identity stays available only
through the existing permitted hover information.

| Component | Meaning | Design |
|---|---|---|
| Outer contour | Family | circle for kbot, diamond for vehicle, triangle for aircraft, trapezoid for hovercraft, flat-topped rounded hull for ship, capsule for submarine, square for structure |
| Inner symbol | Primary purpose | construction tool, factory, extractor, energy, storage, sensor, jammer, transport, weapon or generic support |
| Bottom ticks | Presentation level | none for 0/1; two for 2; three for 3 or more; commander appearances have none |
| Colour | Owner | dominant opaque shade of the published lobby colour's `32xlogos` frame |
| Halo | Selection / hover | separate from role and ownership; retains contrast over bright and dark terrain |

**Size and placement.** 24 framebuffer pixels from a 32-pixel supersampled source
tile, fixed at every camera zoom including the 0.25× tactical target and
map-clamped intermediate factors; picking uses the same footprint. Icons stay
upright, centred on the current marker projection, clipped to the battle viewport
and outside the scaled world region. The transition is §16.11's 0.625× fade-in /
0.5× full icons with §16.7's clamped tactical-stop exception.

**Vocabulary.** A larger crown marks commander appearances and one factory
silhouette serves every product family; the remaining role glyphs are authored
geometry in `strategic_icon_art.go`. Level ticks are light with dark keylines
across the lower border — white cores of 2×4 source pixels, a 0.75-pixel dark
keyline, centres five source pixels apart — carried on the white and black mask
channels so brightness is independent of team colour; a keyline may cross the
contour rather than shrink the frame or glyph.

**Levels** follow Enhanced presentation policy, not retail tech-tier semantics:
the minimum count of factories on a final build-menu path from a true commander.
Entering a structure builder with a nonempty final product menu adds one, other
edges add zero, the destination factory counts, and cycles cannot increase a
shortest path. Failing a route, a sole positive numeric LEVEL token is the
fallback; an unreachable unit without one stays unresolved and unmarked. Commander
and decoy appearances suppress levels entirely. The graph is computed once at
catalog load, and a name-resolved route applies only to its resolved record. Never
infer from cost, names or descriptions.

### 18.3 Classification contract

A presentation-only descriptor table compiled from the final catalog, indexed by
canonical definition identity within it. Each descriptor stores family, role,
optional subtype and evidence: source logical path and provider, the authored
fields read, rule identifier, and whether the result is established input, a
reviewed presentation mapping, or unresolved. Icon geometry and classification
rules carry their own revision; neither changes the definition hash.

| # | Evidence and order | Never |
|---|---|---|
| 1 | Structure versus mobile from the compiled BMCode convention, capabilities partitioned from role; explicit category family tokens first, compatible `TEDClass` metadata next; contradictory or absent evidence keeps a generic family. Comparison is case-insensitive whole-token [02 "Unit record"][fmt fbi] | infer legs from a TANK movement name; read `NOTAIR` as an aircraft token or `NOTSUB` as a submarine one |
| 2 | Specific purpose before incidental stats: feature conversion, commander appearance, resurrection/construction, manufacturing/repair, dedicated economy and sensors, then combat and general support; every supported capability is retained even when the icon displays one | collapse capabilities into the displayed role |
| 3 | A structure builder with a nonempty final product menu uses the single factory glyph | add a subtype or badge for a product family |
| 4 | Explicit economy and sensor tags plus capability fields distinguish dedicated functions; energy generation must include negative EnergyUse, wind and tide as documented inputs [02 "Unit record"] | let a constructor's production, a plant's storage or a gun's radar replace a primary role |
| 5 | Only active resolved weapon slots, first active slot in authored 1/2/3 order; interceptor, dropped, water-weapon and paralyzer flags are capability evidence, and a primary role read from one needs a reviewed mapping | use ExplodeAs or SelfDestructAs; combine slots into a mixed glyph; infer scout/artillery/heavy/AA from speed, range, cost, damage or weapon-name thresholds |
| 6 | A small explicit presentation mapping, only with recorded evidence; an unknown or modded definition gets a generic fallback | parse localized display names at runtime; give a definition the role of an unrelated stock unit with the same name |

`TEDClass` is retained in `UnitDef.Unknown` and inert in retail gameplay
[fmt fbi], so reading it as authored editor metadata gives it no simulation
behavior. The audit behind these rules is in the history file. **Unknown:** the
complete AA / fighter / artillery distinction, ambiguous physical families,
exact-unit differentiation, and levels with neither a reachable build path nor one
unambiguous authored token. Missing evidence becomes `TODO(question)` at the
classifier site; a generic symbol is a complete supported fallback.

### 18.4 Visibility and interaction contract

Committed `UnitView`s carry model visibility, owner colour and selection;
`RadarContactView`s also carry concealed definition metadata, so contact presence,
`Visible`, `Graphic` and `Commander` are **not** identification proofs.

- **Admission.** Enemies are gated by explored fog at the anchor and the committed
  hull visibility predicate [03 §3.2][03 §3.3]; attached models inherit their
  carrier's admission through its published Cargo list, and piece-less cargo is
  omitted [03 R-RAST-01 §7]. Visible-unit icons use that world-painter admission
  alone — never the minimap's contact latch or its blink term — and iterate the
  committed units even with no radar record. A same-publication slot
  lookup suppresses duplicate radar marks and resolves attachment links; missing
  links and nested cargo cannot reveal attached units. Visible nanoframes
  (`BuildRemaining > 0`) have no icon and no icon hit, though the identified lane
  still consumes their radar records.
- **Sensor-only contacts** retain `MinimapBlipAdmitted`, blink included, never
  expose definition art or a `UnitView` hit, and draw at full opacity at every
  Enhanced zoom including the fade band — a hidden unit has no visible model to
  replace.
- **Placement.** The foreground pass follows fog and precedes HUD chrome, and runs
  when the paused world is reused. Identities are not remembered across visibility
  loss; retained hover identity uses `InstanceID`, not a reusable pool slot.
- **Team ink** is the most frequent opaque index in the published lobby selector's
  authored `textures/logos.gaf` `32xlogos` frame — **not** `radlogo`, whose gray
  border outnumbers its team-colour interior. Colour resolves once per
  art/selector binding; no owner-slot-to-colour assumption or alternate RGB team
  table. Missing team art gives neutral ink, never invisibility.
- **Disguise** is an information boundary even for visible units: until the retail
  disclosure contract is verified, visible non-owned commanders and decoys share
  one commander-looking symbol with no truthful commander/decoy badge, and the
  protection covers the complete descriptor — contour, role, secondary badges,
  size and any newly exposed hover metadata. Unresolved commander-looking
  definitions keep that shared appearance until reviewed.
- **Picking.** A visible typed icon uses the same screen bounds for drawing and
  picking (`PickSnapshotUnit` scores model hulls by size, so the two must agree),
  and selection uses the icon's contour halo alone; the ground footprint quad is
  suppressed there and kept during the fade, at normal zoom, and for presentation
  without generated icons. Overlaps draw ordinary icons with sensor contacts
  before visible units in stable publication order and selected icons last, and
  hit-test in reverse draw order; a hover halo does not reorder. That order is
  shared across cursor, click selection, tooltips and command targeting. Generic
  contacts retain existing command and knowledge restrictions: a hit must not hand
  hidden `UnitView` metadata to a tooltip or enable a new target action. Drag
  selection keeps its visible projected-centre policy. Visibility loss and changes
  of viewer, camera, viewport or mode invalidate presented icon and hit-test lists
  immediately.

### 18.5 Implementation boundaries

- A client-owned `StrategicIconCatalog` compiles immutable descriptors from
  `content.Catalog` and returns a descriptor plus evidence, with a generic
  fallback. No sim package imports it; `content.UnitDef` is unchanged.
- Client layout takes the committed frame, viewer, live camera, viewport, catalog,
  radar option flags, bound logo/radlogo/palette resources and reusable storage,
  and returns admitted icon instances with the corresponding restricted hit
  targets for the same presented frame, rebuilding admission and the
  same-publication slot lookup on every record and pick in reusable storage.
- Drawlist icon records carry immutable atlas identity and UV bounds, screen
  bounds, tint, alpha, selection and clip, and own or safely retain referenced data
  through `List.Clone`, replay and asynchronous record/submit buffering.
- The executor uploads the generated atlas once per resource identity and batches
  lightweight quads through a bounded four-entry cache retired on source reset.
  R/G/B/A hold disjoint team, white, selected-halo and black coverage — not a
  premultiplied RGBA image, so a stored alpha of zero is mask data that must
  survive upload — and the fragment combines coverage with the live display
  palette and applies the layer fade to colour and opacity together. Palette
  changes do not re-upload the masks. It must not allocate an image, scan
  definitions, rasterize geometry or upload a texture per unit per frame. One
  batch serves typed icons and generic contacts: the shared atlas is bound for
  both and a flat-palette shader branch draws the generic squares, so alternating
  kinds do not fragment the run.
- Input uses the projection of the last successfully submitted list while frame,
  tick, viewer, zoom, viewport, catalog and camera samples still match. The
  record/submit pipeline can accept a predicted camera fraction within its
  tolerance, so retaining the actual submitted origin is what stops a later
  measured fraction from moving the clickable rectangle (§13.10); speculative
  records cannot replace it. Changed publication or projection invalidates it, and
  visibility and identity are always rechecked. Icon hover and art are foreground
  state and do not invalidate the paused terrain/fog cache.

Classification, geometry, layout and evidence live in
`internal/client/strategic_icon_{catalog,art,layout}.go`; `strategic_markers.go`
records the shared layout; `internal/drawlist/world.go` and `list.go` own the
records and their lifetime; `internal/platform/gpurender/world.go` and
`strategic_icons.go` execute them.

### 18.6 Review sheet and acceptance

`go run ./tools/strategic-icon-sheet -root <install> -out <external-directory>`
emits `index.html`, `catalog.png`, `vocabulary.png`, a constructor-only
`constructors.png` comparison and `audit.json` from the loaded catalog and the
same masks the GPU uses. Generated sheets, audit output and captures stay outside
the repository; committed assets are our authored geometry and rules plus light
synthetic fixtures, and retail tests skip without assets and assert relationships,
never a fixed catalog census. Acceptance is a human review of that sheet in which
every generic fallback is resolved or explicitly accepted; the checklist, the
fallbacks accepted on the reference mount and the live benchmark runs are in the
history file (§18.7, §18.8). No runtime name or description guessing and no
stock-name override table exists; teleporters use the literal Teleporter
capability. No aggregation, displacement or cluster counts: inspect dense overlap
before designing any.

## 19. Glow: Enhanced bloom from the world's light sources

### 19.1 Decision

Retail's composite has no light. A laser is a one-pixel Bresenham line in a
palette colour, an explosion is animated art with a brightening of the pixels
under it, and nothing spills past its own pixels. The glow layer gives the
world's light sources a halo: an Enhanced presentation feature, on by default
while the modern executor presents, absent from Original, never a simulation
input [I6]. With the layer switched off the modern executor's output is
byte-identical to the executor without it.

The user's brief relaxed the requirement that Enhanced match retail's software
composite, so the layer is designed for the look rather than for a retail-derived
arithmetic. Two things are still not invented: which pixels are a light, which
the recording already knows, and what colour a light adds, which is either the
palette colour retail draws or the brightening retail applies.

### 19.2 The sources — contract L1

The recording marks its light sources and nothing else changes in it.
`drawlist.Line` and `drawlist.Sprite` carry an `Emissive` flag; the recorder sets
it on the beam and segment strokes of the projectile renderer (lasers and
lightning) [06 R-WFX-01 §4], never on the selection quad or a path; and on the
effect, projectile and strip sprites (fire, explosion animation, smoke), never on
unit, feature, chrome or shadow art. The flag is metadata: every executor draws
the flagged command exactly as it did, and the classic sink and every parity
fixture ignore it. The lit discs and halos of §13.11 need no flag — they are
modern-only commands and always light sources.

| source | emission |
|---|---|
| stroke | `PAL[index] × glowGain` over a quad `glowLineWidth` **world** px wide, converted to screen pixels by the frame's view scale (below) and extended by its half-width at both ends — a one-pixel line has too little energy to survive the blur |
| sprite | the texel's `PAL` colour × `smoothstep(glowThreshold, 1, max channel)` × `glowSpriteGain`; a tinted strip sprite at half that, so fire and flares emit and smoke and debris do not; only the sprite's keyed texels |
| lit disc | the composite colour under the fragment × the disc atlas lane `max(k−1, 0)` × `glowLightGain`, read from the same atlas texel the disc used (§13.11), so the glow's colour is the lit ground's |
| halo | the composite colour under the fragment × the row's high lane × `glowLightGain`, inside the same disc test the halo runs |
| nanolathe spray | a palette-coloured quad two world pixels beyond each side of the particle core, at gain 0.45 (§23.5) |

The composite is bound as a source image while the glow plane is the destination,
so reading it there is legal and reads the frame as replayed so far. The emission
fragment's alpha is its largest channel, so the plane stays a valid premultiplied
image through the linear-filtered shrinks.

### 19.3 The resolve — contract L2

The batch is additive and order-free, so it rides no phase: sources append quads
(with the world transform of §16.3 applied to their vertices exactly as the
scheduler applies it) to runs keyed by image bindings, and the resolve runs at
most once per frame, at the first of: the fog command, before it compiles — so
the grey composite dims the glow and the black one hides it, exactly as they
treat the light sources themselves — or the close of the world region, so a frame
recorded without a fog composite still resolves before the chrome is painted over
the world. A front-end frame has no emissive commands and drops nothing.

The resolve submits the scheduler first, so it is a barrier costing one segment,
then: clears the full-frame emission plane and draws the runs into it under
`BlendLighter`; shrinks it to a half and a quarter of the frame with linear
filtering, blurs the quarter plane with a separable nine-tap Gaussian
(`glowSigma` 2, ping-pong), shrinks that to an eighth and blurs again; and adds
the quarter plane (×4, `glowNearWeight`) and the eighth plane (×8, `glowFarWeight`)
onto the composite with linear magnification under a **screen** blend,
`out = src + dst × (1 − src)`. Screen rather than additive is what keeps a
fireball's own art: an already-white core stays white instead of clipping, and
the halo shows where the ground is darker.

**The halo is sized in world pixels, not in framebuffer pixels.** Everything a
halo surrounds — the beam, the sprite, the flash disc — is drawn at the frame's
**view scale**: the record step the recorder projected the world at times the
live zoom factor the scheduler applies (§14.2, §16.2). The terrain record
carries the step, so the executor reads it from the same place the aircraft
shadow does (`glowViewScale`); a frame with no terrain is the native view. A
halo fixed in framebuffer pixels is half as wide, *relative to the units it
comes from*, at the 2× step as at 1×, and changes size under the wheel. So
`glowNearSigmaWorld` states the near octave's blur radius in world pixels and
`glowBlurStep` converts it once, into the separable blur's tap spacing in texels
of the octave being blurred; the stroke quad's half-width takes the same view
scale. The far octave keeps the same spacing on a texel twice as wide, so it
stays twice the near halo. At the native view the spacing is exactly one texel
and the quad four screen pixels, so a 1× frame composes exactly as it did.
Blur fetches are nearest, so a spacing below one texel folds taps onto the same
texel: the kernel narrows toward the octave's own resolution rather than
aliasing, which is the right failure at a zoomed-out view where the halo is
already finer than the octave can hold.

That is nine device passes per frame with something glowing and about 2.4 frames
of fill, most of it the two magnified adds; a frame with nothing emissive costs
nothing. The planes are allocated once per frame size, and the emission plane is
**unmanaged** so its texels never depend on an atlas placement (§13.12 "Page
planes are unmanaged"). Steady-state frames allocate no options, no uniform map
and no geometry here.

The knobs (`glowLineWidth` 4 world px, `glowGain` 1, `glowThreshold` 0.65,
`glowSpriteGain` 0.6, `glowLightGain` 0.35, `glowNearWeight` 0.65,
`glowFarWeight` 0.5, `glowSigma` 2 over `glowTapCount` 4 taps a side,
`glowNearSigmaWorld` = `glowSigma` × `glowOctaveNear` = 8 world px, which is the
eight framebuffer pixels the layer was tuned at when the view scale is 1) are
presentation choices tuned by eye on
the battle benchmark capture; a first pass at 0.8/0.5 with an additive composite
and no sprite gain blew every fireball to a white blob, which is the case the
screen blend and the sprite threshold exist for.

### 19.4 The switch

`settings.Display.Glow` (`display.glow`, default 1) is a Nanolathe option with no
retail bit. The shell and the capture route apply it with the other display bits
(`applyVisualOptions` → `Client.SetGlow`), the `+glow` chat command toggles and
persists it beside `+antialias` and `+dither`, and every modern executor site
copies the client's switch to the renderer (`Renderer.SetGlow`) before `Execute`,
next to the display palette and the effect selection. A settings file that omits
the key keeps the default because the loader decodes over the defaults
[02 "Settings"]. Off, no source appends and the resolve is a no-op. It is
deliberately **not** one of the five `Effects` families of §30.

### 19.5 Verification

1. **Device fixture.** `glow_test.go` under `NANOLATHE_GPU_DEVICE_TEST=1`: a flat
   field with one emissive stroke inside a rest-factor world region. Off, the
   frame is the exact classic expansion and the counters are zero. On, the
   stroke's own pixels are unchanged (screen leaves white white), the field beside
   it is clearly brighter, the brightening falls off with distance, and the far
   corner is the field to within the blur's last tap.
2. **CI tier.** Unit tests lock the normalized kernel, run-relative indices and
   run splitting of the batch, the stroke quad's geometry, and the recorder's
   emissive marks on beam and segment strokes.
3. **Byte-identical off.** The modern battle benchmark with `display.glow` 0
   against the build without the layer.
4. **Look.** The benchmark capture with the switch on beside the same frame off,
   cropped 1:1 around an explosion cluster and around a laser: the fireballs keep
   their texture with a soft warm halo, the beam glows, burning trees glow, smoke
   does not, and the fogged half of the map shows the glow greyed.

## 20. Shift tactical range guides (Enhanced)

### 20.1 Presentation policy

Holding Shift at any modern-renderer zoom, including 1× and 2×, draws ranges for
selected own units and the hovered identified unit. An armed build product also
shows its prospective ranges at the snapped site, including an invalid site while
the player repositions it. This is an Enhanced UI choice, not a retail hotkey or
an authoritative coverage calculation. It uses the product definition, footprint
centre and validated preview height; arming or displaying a guide never submits a
construction order. Releasing the key, losing focus, switching to classic,
opening a modal or result screen, or entering chat hides the guides, and Shift
retains its existing selection and command-queue handling. A single-line on-screen
legend names only the categories present, in their guide colours.

| Guide | Ink | Radius source |
|---|---|---|
| Weapon | Orange, solid | each independently enabled, active weapon slot's `Range` |
| Radar | Cyan, solid | `RadarDistance` |
| Sonar | Blue, dashed | `SonarDistance` |
| Radar jammer | Purple, dashed | `RadarDistanceJam` |
| Sonar jammer | Pink, dashed | `SonarDistanceJam` |
| Build | Green, dashed | builder's `BuildDistance` |
| Interceptor | Yellow, dashed | interceptor weapon's `Coverage` |

Equal weapon radii on one unit collapse to one ring. Sonar is dashed so radar and
sonar remain visible when their authored distances coincide. Preview products use
all active authored slots; live units use the committed independently enabled
slot bits. Record-zero NOWEAPON links are omitted even if they author a range.
Stockpiling alone does not select interception coverage. Sensor guides for an
inactive switchable unit become dashed; they describe its nominal capability.

### 20.2 Data boundary and limits

**Established source contracts:** ordinary `Range` is in whole world units
[06 §3.3]; definition activity and independent slot bits are [06 R-WPN-05 §3].
Sensor and jammer readers sign-extend their stored 16-bit fields, while
construction reach zero-extends its 16-bit field [07 R-P0-11 §3]. No unit-name
lookup table or invented weapon range is involved.

**These are planning circles.** Actual firing also tests terrain, target
restrictions, firing arcs and ballistic feasibility [06 §3.3]. Actual interceptor
acquisition uses an inclusive X/Z square about the incoming projectile's stored
aim point [06 §11.2]; its named coverage circle is a visual guide. Actual build
and repair reach includes footprint terms and differs from reclaim reach
[05 R-WORK-01 §2]. Sensor coverage also depends on activation, terrain, altitude,
water and detection/jamming gates [03 §3.4][03 R-VIS-01 §4–5]. The HUD therefore
calls these range guides, not guaranteed coverage.

At icon zoom the client admits targets through the committed strategic icon
layout; at model zoom it applies the same committed visibility and carrier checks
directly, using projected world anchors with the same screen margin, so that path
needs no icon catalog and no nonzero marker alpha. Hidden units, radar-only
contacts, carried passengers excluded by that layout, and off-screen unit anchors
expose no definition to the overlay. Selected own units remain subject to the same
friendly visibility policy as §18. Commander-looking enemy units are omitted so
differences in truthful ranges cannot identify a decoy through otherwise
identical icon art.

### 20.3 Rendering and verification

`TacticalOverlayStage` runs after the committed world, outside its scale region,
before the icons and HUD. The same live zoom and camera origin as the icons
project the ring. Each terrain endpoint uses the greater of centre height and
sampled ground height [07 R-P0-11 §3]. One-pixel palette-coloured segments are
clipped to the battle viewport before recording. Wide integer coordinates avoid
long-range wrap; only clipping ratios use transient floating point.
Screen-adaptive 32..512 chords per ring bound tessellation, including huge
authored ranges. These bounds, the dash pattern and the colours are Enhanced
presentation constants. The Shift-off path does not resolve colours, visit
targets or record range lines.

Focused tests cover inactive placeholder weapons, independent slot admission,
interceptor versus stockpile data, deduplication, field narrowing, held-key
release and focus loss, prospective product and site selection, invalid
placement, classic fallback, all-zoom committed visibility, terrain projection,
viewport clipping, bounded long-range geometry and pre-icon draw ordering.
`--shot-shift --shot-select` captures selected ranges; `--shot-build <name>`
previews a named product beside the first selection, or at the world viewport
centre without a selection, without an order. The capture switches apply after
simulation and zoom setup, so matched scenes retain the same simulation state.

## 21. Modern resource construction input

The modern-only resource double-click shortcut is specified in
DESIGN_INTERFACE_HUD_INPUT §3.10. The active executor gates input recognition;
ordinary session build commands and placement validation own all resulting
construction. Modern drag construction, rectangular area work and free-form
formation commands are specified in
[DESIGN_INTERFACE_HUD_INPUT §3.11](DESIGN_INTERFACE_HUD_INPUT.md#311-modern-drag-commands);
their previews share the world-overlay transform of §16.3 and ordinary indexed
line and fill primitives. Alt grid capture takes precedence over tactical ranges.
These are explicit input extensions beyond visual differences; no renderer state
enters construction or movement, and there is no alternate economy, construction
or simulation behavior.

## 22. The model lane

### 22.1 What it is

The modern executor's **one** model path. Its predecessor, the slot-atlas stage,
reproduced retail's per-unit composition image exactly through a key pass, a body
pass, reveal, outline, clipping, a coverage resolve and a residency table, and
was the executor's largest CPU term. The lane keeps what retail's picture needs
and drops the rest. `internal/platform/gpurender/model_direct.go` is the whole of
it, plus three ops in the scene and destination shaders, `scheduler.tris`, the
outline row walk (`model_prepare.go`), the parameter packing
(`model_quads.go`), the retained-lane store (`model_retain.go`) and the texture
page (`model_atlas.go`).

Before `Replay`, every subject of the frame and its shadow are given a region of
a per-frame 2× atlas page — 4096 × 4096 texels, two planes, shelf-packed
tallest-first, **no residency** — and their faces are fan-triangulated straight
from the packet's projected corners. A 1080p battle frame uses about 1,300 rows
of the first page and the 2× detail view about 4,000; a second page opens when
the first is full (128 MiB of device memory a page, allocated on demand and
retained), and only a frame that fills both takes the fallback. Region dimensions
are rounded up to even so a native pixel's 2×2 block never straddles regions, and
a two-texel margin separates them (§11.2 "Every atlas cell carries a border").
The doubled lane is the recorder's own packet when it carries one, half-pixel
offset included (§17.3); a packet without one has its native corners doubled
here.

An attached-unit group composes in **one** region over the union of its bounds:
the carrier's faces, then each mergeable child's with its signed height delta
added to the keys, saturating at the byte's range where retail would wrap
[03 R-REN-03A §4]. A carried child that casts a shadow composes a **second** time
in a region of its own — its own keys, its own verdicts, no carrier clip — because
a shadow is cut from its subject's own finished image and the group region holds
the carrier's texels too [03 R-REN-03D §1]; `ModelStats.DirectCargoImages` counts
them, and the second composition is suppressed from the reflection source so the
same world geometry does not reflect twice. A shadow whose source region is
invalid is **omitted**, never cut from texels that are not the subject's.

Two passes over ONE vertex batch draw the whole frame's subjects at once:

1. **Key.** Each face's height key, narrowed to a byte as the span writers narrow
   it, into a key plane under a MAX blend, so a texel holds the highest key drawn
   there. A four-corner face, flat or textured, takes its key from the span
   writer's two-chain mapping of its corners, evaluated per fragment from the
   parameter image; any other ring interpolates its lanes linearly.
2. **Colour.** Each face's texel where its own key is not below the stored one —
   retail's `stored ≤ incoming` admission [03 R-REN-03A §2] — into a colour
   plane, faces in RECORDED order so a tie goes to the later-drawn face as
   retail's does. That tie is what puts a solar collector's base rim over its
   open panels, which lie at its height; a painter's sort by mean key loses it. A
   mapped face's key is the same mapping the key pass wrote, so the passes never
   disagree about a texel. Shadow silhouettes draw in their index with no key
   test. The nanoframe reveal, the waterline tint and the Digger erase are
   verdicts on the height key [03 §5.2][03 R-WATER-01 §2][03 R-REN-03A §8]; the
   fragment evaluates them on the PIXEL's key — the stored key at the block's
   top-left texel, which is the key retail's 1× image holds after the 2:1 resolve
   samples it [03 R-REN-03A §6] — so all four texels under a pixel take one
   verdict and a band one key wide resolves to whole pixels. A replaced reveal
   index is written flat, as retail rewrites its plane after shading. A carried
   child's own verdicts read its own key (the entry carries the delta, taken back
   off at the fragment) and the carrier's waterline and Digger then clip the
   child on the shifted key, which is what the staging image's passes do
   [03 R-REN-03A §4]. The subject's verdicts ride the parameter image beside the
   mapped faces, eight texels of a twelve-texel entry; its ninth texel carries
   the subject's **frame origin**, the atlas texel of the raster's local (0,0),
   because a parameter entry is packed **subject-local** — corners in the
   raster's own frame at the atlas scale, plus a bias that keeps the edge walk's
   operands non-negative — and the fragment shifts its atlas position by that
   origin. Every non-shadow subject therefore has an entry, negated when it
   carries the frame alone, so the verdict block still gates on the sign.
   Outline endpoints come
   from the span writer's own row walk over the NATIVE packet
   (`prepareModelOutline`) and draw as one pixel block each, key-tested once
   against the pixel's key, between the cached and live lanes in retail's order
   [03 R-COMP-01 §3][03 R-REN-03A §4].

`Replay` then compiles, per subject and in record order through the scheduler, a
shadow commit and a body commit. A structure's shadow commit resolves the four
silhouette texels under a pixel from the shadow's page, punches a pixel whose
body block — read from the body's page, which may be the other one — is wholly
covered, and composites the ALP half-colour fragment [03 R-REN-03D §4–§5]. A
Digger's or a mobile's shadow is retail's copy of the finished body: the recorder
emits a faceless packet (`Silhouette`) with the body's box, the shadow anchor and
the buried or submerged clip key, and its commit resolves the body's own colour
texels at the shadow's placement — cached lane, live lane and staged children, as
the classic copy holds them — erasing a texel at or below the clip key read from
the key page, and composites the half-colour of index 0 [03 R-REN-03D §1]. It
spends no region, no faces and no projection, and it binds the colour page in the
projected shadow's slots so the two kinds share a run. The body commit
box-resolves the four texels under a pixel: colour the mean of the covered ones,
alpha their share — §17's coverage resolve done in the commit, for models only;
sprites and terrain are untouched, as terrain is authored to be drawn as is.

| retail | model lane |
|---|---|
| per-pixel height key, `stored ≤ incoming` | the same test against a max-blended key plane; ties by recorded order |
| back faces culled by ring winding | same rule, same sign |
| SHD row lookup per texel | `0.06875 × row` interpolated across the face (§13.2); the table's nearest-index rounding is the visible difference |
| textured quad by two-chain span mapping | the same mapping, evaluated per fragment from the parameter image (§11.2 "Textured quads without strips"); flat quads mapped the same way for their key and shade |
| inclusive span fill | corners on the far side of the face centroid pushed one 2× texel; a linear textured face clamps its texel to its authored bounds |
| composition transparent index 1 | dropped at the fragment |
| reveal, waterline, Digger over the 1× image after the resolve | the same verdicts per texel on the pixel's nearest-sampled key |
| outline endpoints written at 1× after the resolve, key-tested once | native rows drawn as pixel blocks, key-tested once against the pixel's key |
| structure supersample, ALP downscale blending with index 1 | every subject at 2×, resolved in the commit fragment by coverage: an edge or a thin feature is a coverage alpha over what is beneath, never the red/purple fringe [03 R-REN-03A §7]; a mobile subject is supersampled too, where retail draws it at 1× |
| structure shadow punched by body coverage | same punch, both planes resolved from the pages |
| Digger and mobile shadow: the finished body image copied, flattened, clipped, blitted at the ground point five pixels right | the body's own raster read at that placement in the commit fragment; a mobile is never punched, as retail's is not |
| child composed alone, its erased pixels transparent, then `prior > key + delta` keeps prior, wrapped store | child faces in the group region under the same admission with the shifted key; the sum saturates instead of wrapping; a child texel its own reveal or clip erases stays a hole where retail shows the carrier through it |

A subject no page can hold falls back to painter-order native triangles straight
on the composite (no key, no supersample, no reveal, no children) and its shadow
is omitted; the benchmark never overflows at either view. The fallback fragment
carries an opacity lane, so an overflowed cloaked subject draws the ALP
half-colour (§33) rather than an opaque body. The parameter image grows to what a
frame uses up to 2,048 rows (174,762 entries); a frame past it draws its
remaining faces linearly. The commit quads bind only the colour plane, so they
share a run with the sprites around them.

**Retained packed vertices.** The lane's largest CPU term was re-deriving, for
every cached-lane face of every presented frame, what the recorder had already
proved unchanged: the winding test, the centroid, the texture slot, the parameter
entry, the glint, the finish, the shade lanes and the packed vertices. An equal
`drawlist.ModelCacheKey` means literally the same retained faces (§13.12), so
`model_retain.go` keeps, per key, that lane's packed vertex array and packed
parameter block **in a frame that does not know where on the atlas the subject
lands**: the vertices' destination coordinates are relative to the subject's
frame origin, which is what the cold path adds to every corner; the parameter
block is subject-local by construction; and a face's parameter index is kept
relative to the block. Only the placement, the verdict entry, the block-relative
parameter index and the per-face battle light are rewritten on replay, the light
from the retained centroid and normal through the same arithmetic the cold path
uses. The store is a **bounded LRU** (`modelRetainCap` 2,048 entries, stubs
included — a 1080p battle frame has about four hundred subjects, each on up to
four half-pixel keys, §17.3): a key's first sighting primes a stub, its second
captures the cold append, and every later one replays, so a subject that rebuilds
every frame — a turning turret, a walking kbot — costs a stub and nothing else.
The replay is byte-for-byte what the cold path would have produced, because the
integer-valued lanes are exact in binary32 and the light is the same function of
the same operands. A finish switch change (§30) or a source reset drops the
store. This is **CPU-side** retention of preparation work; the atlas regions
themselves are still per-frame and carry no residency.

**The warm path: a body across its revisions.** A key that never returns gets
nothing from the store above: a turning unit's cached lane rebuilds on every
presented frame under retail's orientation threshold [03 §5.2] and takes a new
revision each time, so its every sighting was a stub and a cold append — in
the 1080p battle about 112 of 390 packets a frame, each at the full cold cost.
Its packed vertices are a function of the pose, but most of what the cold
append derives per face is not. Reading the recorder's face construction
(`collectDrawPolysLaneProjected`): the texture frame, the flat colour, the
shade switch, the material and the corner count come from the model and its
primitive, and the texel coordinates are the texture's own dimensions in
corner order — none of them moves with the pose. What does: the corners and
their keys, the SHD rows, the corner heights and the lighting normal, and so
everything derived from them — the winding test and its culling, the
parameter entry, the centroid, the battle light, the glint and the finish
response, the shade lanes. The store therefore keeps a second index, keyed on
`(Body, Lane)` across revisions, holding the pose-independent products of the
last cold append: per face, in the order the cold lane appends them (the
shared page's faces, then each standalone texture's), the run image the face
binds and its texture-derived lanes (`modelFaceTex`: the slot origin, and the
ColorA/ColorB pair in both the mapped and the texel-bounds form). A key miss
whose body hits appends **warm**: the same core the cold lane runs
(`appendFaceCore`), fed this frame's corners, keys, rows, heights and normals,
with the texture resolution, the page/standalone run routing and the texel
bounds answered by the body entry — so the warm append is the cold append's
bytes by construction, verified exactly by `model_retain_test.go` and on the
device by the retain fixture's turned frame. The body is used only when the
raster's face list has the same length and, face by face, the same corner
count and the same texture frame as the recorded one — a different build
state, a damage variant or an advanced animated texture frame fails that in
one pointer compare per face — and when the texture page the routing was
decided against is still the page; anything else is cold, and every cold
append of a keyed packet records the body anew, the priming sighting
included, because a turning unit never reaches a capture. A reflecting
packet, cold for the replay because the reflection batch is placement-bound,
goes warm too: the warm append runs per face and reflects each one as the
cold append does. What stays cold under the warm path is what has no key or
composes as a group. The body index is a bounded LRU of its own
(`modelRetainBodyCap` 1,024 entries) and is dropped with the store. Measured
on the 160-face authored packet: cold 12.7 µs, warm 10.3 µs, replayed 1.65 µs
— the warm path removes the texture lookups, the run routing and the texel
bounds and keeps the pose-dependent arithmetic, which is most of a cold
append; the scale-one corner copy before the parameter packing, dead work in
both paths, went with it.

**An outline and a live lane ride over a replayed lane.** Neither disqualifies a
packet, because the key names the cached faces alone (§13.12): both are appended
**cold, after** the replayed lane, in retail's own order [03 R-REN-03A §4] — the
capture closes before either is appended, and the replay puts the cached lane's
vertices, parameter block and runs back exactly where the cold append would, so
the per-frame lanes that follow land on the same batch either way. The one thing
they touch is the doubled raster's slot box, whose even-rounded corner is the
lane's frame origin: the entry keeps the box of the **cached faces alone**
(`retainedSlotBounds`) and the replay unions this frame's live and outline
corners into it (`slotFor`), which is the box the cold path measures. So a unit
with a `DontCache` piece, a nanoframe under its reveal and outline, and a wreck
(§13.12) all replay. What stays **cold**: a packet with a group delta or a water
reflection this frame (the reflection batch is placement-bound), the solo cargo
pass, the fallback, and a subject whose parameter block would not fit the image
this frame.

**Counters.** `DirectRetained` reports the lanes replayed, `DirectWarm` the
lanes appended warm, and `DirectCaptured` the lanes captured into the store —
through a cold or a warm append, so it overlaps `DirectWarm` on the second
sighting of a key whose body was already held. `DirectRetainedLive` and
`DirectRetainedOutline` are the replays that also carried a per-frame lane,
and `DirectRetainEvicted` the store entries a frame's inserts evicted — store
pressure whenever it is not zero. The cold packets are counted by reason, once
each, at the first that applies: `DirectColdNoKey` (no reusable key, with
`DirectColdNoKeyOutline` and `DirectColdNoKeyLive` the subsets the recorder
used to zero the key for), `DirectColdGroup` (a group child, its delta or the
solo pass), `DirectColdReflect` (a reflecting body the warm path could not
take), `DirectColdPrimed` (a key's first sighting, the stub, when its body was
not held either), `DirectColdShape` (a captured key whose packet shape
changed, captured again cold) and `DirectColdParams` (a replay or cold capture
the parameter image could not take). A packet is one of replayed, warm or cold
by reason; a fall in `DirectRetained` or `DirectWarm` can therefore be read
from the benchmark's counters without a profiler.

The recorder feeds the lane once per display frame. Two of its per-unit costs
went with the silhouette shadow: every cached unit used to project all of its
faces again each frame only to measure its composition box, which is now the
retained lane's envelope unioned with the live pieces' extent
(`retainedModelExtent`), and every face used to resolve its texture through a
name-key map, a lowercase scan and two index lookups, which is now a per-model
table (`modelTexRefs`) keyed on the compiled model and rebuilt when the client
installs new indices — units and features only, because a projectile or debris
draw is one piece copied into a scratch model every such draw reuses.

The isolated model preview exercises the lane's verdicts without a session:
`--shot-model-build-remaining` poses a nanoframe; `--shot-model-world-height`
sets the world height, and as the preview has no map its sea level is zero, so a
negative height submerges the model and runs the waterline erase;
`--shot-model-underwater-exempt` sets the sonar-contact bit that turns the erase
into the blue tint; a Digger definition (`armamb`, `cortoast`, `corvipe`) brings
its own clip. The preview's list opens with its clear and background fill, so
both executors compose over the same background, and `--shot-renderer=both`
keeps the classic structure supersample so the Enhanced classic image is the
like-for-like reference.

### 22.3 Verification

`model_direct_test.go` holds a device fixture (in the hidden loop) locking the
key test against draw order, the tie rule, the reveal verdicts written flat, the
waterline erase and the blue tint at and below their key, the Digger erase, a
carried child's reveal on its own key under its carrier's clip on the shifted
key, an outline endpoint drawn whole against the pixel's key (and rejected under
a higher body key), the shadow half-blend beside its body and the half-covered
far edge, and a body whose shadow is its own silhouette placed beside it with a
clip key (the low half casts nothing, the high half the half-blend of index 0, no
region spent) — run once plainly and once behind a page-wide filler so every
subject draws and commits from the second page. A unit test locks the shelf
packer and its page turn; `model_quads_test.go` locks the parameter packing; and
`model_retain_test.go` holds the retained store to its contract, with a device
check that a replayed frame is byte-identical to a cold frame of the same list at
a shifted placement, including a nanoframe replayed under both per-frame lanes
and a doubled live face reaching past the retained box (which is what locks
`slotFor`). Client tests lock the keying rule: a keyed unit with a `DontCache`
piece keeps its key across two frames and changes it on a validity clear; a
nanoframe keeps its key across ticks and takes a new revision on construction
progress; and a 3DO feature is retained, its rebased packet is corner for corner
the per-frame projection, it re-projects on an orientation or team-colour change
and it ages out. The
classic executor remains the byte-exact reference for retail's composition; the
lane's departures are the table above.

Isolated previews against classic, `--shot-renderer=both`: a submerged submarine
(`armsub` at world height −6) erased and, with the exemption bit, blue-tinted; a
submerged solar collector; and the pop-up `armamb` erased at and below its
origin. Inside each model the two images agree, and every difference of more than
a few levels sits on an edge or a thin feature, where the lane's coverage alpha
stands against classic's fringe blend (a structure) or its 1× raster (a mobile).

The stock transport check exercises ARMATLAS with ARMPW and CORVALK with CORAK
through normal pickup, flight and return-site unload orders and COB. The active
modern recorder forces each attached child to a key-plane packet; a previously
cached keyless child is rebuilt on attachment. These checkpoints have no skipped
subjects, no-body subjects or region overflow. The stock carrier census and the
normal admission path bound the separate nested-child concern: ground and sea
carriers and airbases exceed stock transport size; a loaded air transport remains
airborne, which rejects its pickup; a carried aircraft starts its own pickup by
detaching first, and airbase landing transfers cargo before attaching the
aircraft [04 §10.2][04 R-AIR-01 §7][04 R-AIR-01 §10]. This does not establish
arbitrary mod or authored nested-packet behavior. The modern executor still omits
a child packet that is keyless or itself has children; the stock paths above did
not reproduce that omission, and no software fallback is added.

### 22.4 Owed

1. Richer material response and per-pixel lighting remain future work beyond
   §23, §29 and §31.
2. A carried child texel that the child's own reveal or clip erases stays a hole
   where retail shows the carrier through it, because the child's key reached the
   group's key plane; reproducing that needs the child composed in its own region
   and merged under the staging admission. Only a transport's cargo is carried in
   this build and it is a finished unit, so no stock scene reaches it.
3. The recorder still builds the doubled packet's faces. A packet with a doubled
   lane has its native faces read only by the fallback and the bounds; the doubled
   corners of a direct projection are exact rather than native × 2 plus an offset,
   so the offset alone cannot replace them.

## 23. Battle lighting (Enhanced)

### 23.1 Scope and inputs

A user-authorized presentation design, not retail evidence. It adds coloured
diffuse light to model faces and soft illumination to smoke around visible
explosions and weapon impacts, keeping the projection, existing shade,
silhouette, composition key, fog, effect lifetime and simulation unchanged.
Shadows from point lights, terrain relighting from geometry, material masks and
reflections are not part of it. The player's Lighting switch (§30) is its only
control and defaults on; `BattleLights`, `LitModelFaces` and `LitSmokeSprites`
are frame diagnostics.

**Contract BL1 — sources.** Named art for explicit explosion, impact and water
impact events contributes only while the primary animation is active and its
frame resolves. Source metadata additionally requires `PointVisible` at the event
position for the current viewing player; absent visibility fails closed. This
extra gate changes light emission alone, not the original art draw. The
calculated secondary flash and the generic glow flag are **not** additional
sources: counting both layers would double an explosion, and the glow flag also
marks smoke. Smoke-puff and vent-steam strip families are receivers; art colour
does not identify their producer. Existing effect and strip composition remains
[03 R-FX-01][03 R-FX-02], with light applied beneath the existing fog boundary.

**Contract BL2 — physical coordinates.** Model faces carry outward normals in
world X, world Z and height axes. The producer computes the standard cross
product from transformed vertices, then mirrors model Z to match the visual
projection [03 §2.5]. Corners carry model-relative height in recording-scale
pixels, independent of the wrapping composition key. Every packet refreshes its
current absolute origin height, including retained, direct and attached subjects.
Supersampling doubles raster coordinates only: physical height and normals stay
unchanged. Adding half the absolute height to projected Y recovers unsheared Y
for receiver-minus-source distances. The same final world transform applies to
the lit subject and the source, so lighting is evaluated in record coordinates.

### 23.2 Bounded lighting and composition

**Contract BL3 — artistic response.** Before model preparation, the executor
borrows source metadata recorded once before composite art is decomposed into
leaf blits. It caches each immutable art frame's emission colour: covered texels
above maximum-channel brightness 0.45 receive squared weights
`((brightness − 0.45) / 0.55)²`, and their weighted mean RGB is scaled by the
square root of mean weight across all covered texels, so dark trailing animation
frames lose energy and colour comes from the displayed palette. Palette changes
clear the cache; source reset releases it. Frames below 0.015 peak emission are
ignored. Composite frames are measured on a bounded 32×32 sampling grid,
compositing their ordered leaves over black with the authored keyed/half-alpha
selection, so overwritten bright leaves do not emit and one composite consumes
one budget slot. This is an approximate intrinsic emission measurement,
independent of the ground behind a translucent effect; the thresholds are
presentation choices, not authored material data.

The explosion source radius is 1.4 times the largest width or height across its
own resolved animation **entry**, clamped to 48–192 world pixels and extended by
50% (72–288), then multiplied by the record scale. The recorder carries this
immutable native-art extent as `LightingSize`, independently of the Distortion
switch; colour still comes from the current frame. Using the entry rather than
the current frame makes the reach available during the initial flash instead of
growing into nearby receivers as the fireball dims, and it adds no age curve or
lingering source. The point is lifted one quarter of the frame height above the
event, with its ground position fixed, to represent the bright volume above an
impact. At most 64 sources survive per frame, retaining the strongest with stable
ties. Each subject chooses at most eight nearby sources using a conservative
projected bound and distance-weighted source strength. Each face evaluates those
sources at its physical centroid, with outward Lambert response and squared
radial falloff `(1 − distance²/radius²)²` and gain 3.25. Back-facing faces
receive zero; distance at or beyond the radius receives zero. Contributions sum
and are capped at two per channel before storage.

**Contract BL4 — existing passes.** A constant numeric RGB code carries the
face's contribution through the otherwise unused fourth custom vertex lane in the
atlas colour pass — three base-128 digits for [0,2] channel values, not a float
bitcast, whose 21-bit maximum leaves device-float rounding headroom at channel
carry boundaries. The native overflow fallback carries the same code in its
unused third custom lane. The shader adds albedo times incident light to the
existing shaded colour and clamps to one; zero contribution uses the previous
colour expression exactly. Outline endpoints and shadow silhouettes receive no
light; key, reveal and waterline processing retain their order. No new model
pass, texture or per-frame uniform map is needed.

Smoke uses gain 2.5 and the same nearby sources and radial falloff with a soft
response of 0.8 independent of facing. Its four clipped corners carry RGB
contributions through the existing tinted-sprite vertices; interpolation supplies
the interior. The fragment adds `(0.2 + 0.8 × source colour) × light` to the
smoke colour, clamps it, and preserves the original one-half premultiplied alpha.
The source transparency mask, blend order and fog are unchanged. Smoke is never
promoted to a light source. This approximates scattering without volumetric
geometry.

### 23.3 Limits

The face-centroid response is intentionally coarse and has no light occlusion
between visible objects. Smoke uses flat sprite depth. Material-specific
reflectance, per-pixel normals and additional emitter families are outside this
prototype. The source visibility gate prevents hidden events from lighting
visible receivers; ordinary final fog still controls receiver presentation.

The synthetic tier verifies outward roof winding, record-scale heights across
retention and supersampling, explicit source/family classification, missing or
hidden visibility exclusion, directional falloff, common translation and scale,
owned list replay and shader compilation. The opt-in real-device loop checks lit
versus back-facing model surfaces, lit versus untagged smoke, and unchanged
output after disabling the pass, and can write comparison PNGs through
`NANOLATHE_LIGHTING_SHOTS`.

### 23.5 Nanolathe glow and local illumination

The source is the committed strip-6 particles of [03 §5.5], with their
established two-pixel marks, palette ramp, motion, coverage gate and draw order;
the nanolathe event itself adds no synthetic beam or additional particle
lifetime.

**NL1 — source admission.** Only fills explicitly tagged by the nano strip family
emit. The tag is attached after the existing `PointVisible` gate and carries
absolute particle height, recording scale and recording viewport. Ordinary fills
and impact sprinkles never emit, even with the same palette colour. A particle
whose core misses the recorded viewport contributes neither bloom nor lighting.
The viewport travels with the fill because source gathering precedes world-region
replay, and fractional zoom can record beyond the device's pixel extent. Fill
ownership, clone and reset retain or release the metadata with its pixels.

**NL2 — spray glow.** The existing glow source pass receives a palette-coloured
quad extending two world pixels beyond each side of the particle core, at gain
0.45. The existing two blur octaves resolve it beneath fog and interface. No
shader, render target or extra blur pass is added, and the existing glow switch
controls it.

**NL3 — local lighting.** Before model preparation, visible particles join the
nearest existing cluster within 24 world pixels in unsheared physical space.
Clusters follow the arithmetic mean of their particles' positions, with no
screen-grid snapping. Each particle adds 0.06 times the mean displayed RGB of the
seven-entry nano palette ramp [03 §5.5]; the completed cluster is uniformly
scaled down if its peak exceeds 0.7, and its radius is 80 world pixels. At most
64 clusters occupy fixed scratch storage; later particles may join an existing
cluster but cannot open a 65th. Clusters then compete with explosions for the
64-light budget by peak energy with stable ties. Subject selection, outward face
response, smoke scattering and radial falloff are the BL3–BL4 path. Dense
overlapping clusters may add together; there is no per-builder brightness
normalization. All source state is rebuilt from the current recorded particles
and displayed palette and disappears when those particles expire. The broad
illumination stays **constant** across the seven-step particle shimmer: using
each particle's instantaneous palette entry made the factory faces and ground
pools flash. The particle cores and their small glow retain that shimmer. No
temporal history or delayed extinction is introduced; particle count, grouping
and motion still affect illumination, and an overloaded scene may drop distant
construction sources. The Lighting switch disables both explosion and nano
lighting; glow remains independent.

### 23.7 Metallic glint

A small directional highlight using the existing outward face normals. One fixed
unit half-vector, `(-0.35, -0.15, 0.9246621)` in world X/Z/height axes, defines
an artistic overhead key; the clamped normal dot product is squared five times
(power 32), once per rendered face. There is no camera position, clock, RNG,
per-pixel normal, point-light loop or additional geometry: rotating panels change
their response and a stationary panel keeps its highlight.

The face-constant ColorG attribute holds the original palette byte plus 256 times
the rounded 0–255 highlight weight; both the atlas body shader and the native
overflow shader decode those sixteen numeric bits, and the packed RGB battle
light retains its own precision. Shadows and outline endpoints carry zero
highlight, and construction bands that replace material colour suppress it.
Palette lookup, waterline, transparency and composition ownership retain order,
with the highlight applied to surviving model colour under final fog.

The shader masks dark seams with a brightness smoothstep from 0.12 to 0.35, and
saturated paint with one minus a saturation smoothstep from 0.2 to 0.65. Its
highlight tint is 35% white plus 65% albedo, with peak gain 0.48; RGB clamps to
one and keeps the original coverage. These are tunable artistic choices, not
authored metalness or roughness — neutral painted panels can look metallic too,
and there is no shadow occlusion or map-specific sun direction. No passes,
textures, uniforms, normal buffers or per-frame allocations are added. The
player's **Finish** switch (§30) is its only control.

## 24. (retired)

Wind in vegetation, removed at the user's request after live review: the initial
filtered sprite bend blurred the foliage, and replacing it with integer row
shifts preserved colours but looked glitchy in motion. Vegetation uses the
ordinary static sprite path, and the prototype's name heuristic, presentation
filter, sprite displacement, GPU operation and dedicated wind snapshot payload
were removed with it. The coastal treatment in §26 separately publishes the
existing wind for water motion, and the simulation's wind behavior is unchanged.
Future vegetation animation would need a separate art decision; this section
prescribes no replacement. The removal record is in
[GPU_RENDERER_HISTORY.md](GPU_RENDERER_HISTORY.md).

## 25. Explosion distortion

A modern presentation experiment, not retail evidence. It uses §23's resolved,
player-visible primary explosion and impact art sources. The recorder adds
elapsed ticks since the published `StartTick` (plus the presentation fraction
when interpolation is on) and the maximum authored animation extent in world
pixels, cached once per immutable entry. Classic ignores this metadata. No
persistent emitter history, wall clock, simulation mutation or RNG is used;
replaying a list, pausing, changing cameras and restarting cannot restart a wave,
and a finished or newly hidden primary animation stops contributing immediately.

Art at least 64 world pixels across produces a ring lasting 15 simulation ticks
(half a second at normal speed). Its radius grows linearly from 12 pixels to 2.5
times the art extent, clamped to 120–320 pixels. Band half-width is
`10 + 0.12 × art extent`. Displacement strength is `min(age, 1)` times
`(1 − age/15)²` times `min(extent/16, 7)`. All lengths take recording scale and
the same final zoom transform as the source. The bipolar radial profile has zero
displacement at either band edge, and bilinear sampling lets fractional
displacement fade smoothly instead of snapping off. These are artistic tuning
choices.

The executor retains at most 32 strongest rings with stable source-order ties. It
flushes the glow and world work, copies **only the region the batch samples** out
of the composite into the read surface (`readcopy.go`), and draws clipped ring
quads in one batch before fog and chrome. Tree heat and wreck shimmer share that
copy and batch (§27.2, §28). Shader discards leave every pixel outside a ring
untouched; sampling is clamped to the source's world clip; overlapping bands read
the same snapshot and the last submitted band wins, without recursive
refraction. There is no extra copy or draw in a frame without visible active
rings or heat, and no new full-frame image. `BlastWaves` reports the submitted
ring count. The player's **Distortion** switch (§30) is its only control.

### 25.2 Dynamic ordinary blast

Authored Nanolathe design, not a retail behavioral claim. The purpose is to
differentiate ordinary explosions that share art, without shrinking existing
waves or changing the largest special explosions.

The central combat impact event copies the immutable weapon's authored
`AreaOfEffect` and `DamageDefault` into value-only presentation metadata, with a
separate presence bit distinguishing a known zero from a missing profile. The
session bridge, fixed effect pool and committed view preserve these values, and
the client forwards them with the existing art extent and age. No renderer lookup
follows a source unit or projectile handle. A unit's death blast uses the death
weapon the existing central impact path selects. Scripted fireballs and
water-crossing splashes without a profile keep §25's artwork-only wave.
Authoritative damage, RNG, effect admission and lifetime do not read the added
scalars.

Known profiles with artwork extent at least 128 world pixels retain the original
special-explosion treatment. For smaller art:

- Below 48 pixels: no wave, even with high authored damage.
- From 48 to below 64 pixels: admit only when authored AoE is at least 32 and
  default damage at least 80. This admits substantial medium shells while
  excluding small missile and laser hits. Existing art of at least 64 pixels
  keeps its admission regardless of the profile values.
- Baseline target radius is `max(2.5 × art extent, 160)` world pixels.
- Breadth is `sqrt(clamp((AoE − 48) / 208, 0, 1))`. Target radius is the greater
  of baseline and `min(baseline × (1 + 0.5 × breadth), 280)`; the baseline floor
  preserves the few ordinary art entries already larger than that cap.
- Force is `sqrt(clamp((default damage − 80) / 1120, 0, 1))`. Multiply the
  original displacement strength by `1 + 0.75 × force`.
- Start radius, band half-width, 15-tick lifetime, attack and decay, visibility,
  clipping, zoom transforms, strongest-32 selection and the shared draw remain
  §25's.

These two independent boosts use authored values as visual signals, not a
calculation of damage dealt: armor overrides, victim counts, falloff and overkill
do not affect the visual. This is the one blast shape; the player's Distortion
switch decides whether any wave is drawn. The admission rules, including the
artwork-only fallback for a missing profile and for art of at least 128 pixels,
are locked by the shape's own unit checks, and the device-only square roots are
listed explicitly in I2.

## 26. Coastal water (Enhanced)

An authored Nanolathe presentation treatment, not a retail behavioral claim.
Original water is painted terrain plus script-emitted strip-2 sprinkles
[03 R-WATER-01 §1]; simulation, script sprinkles, collision, LOS and RNG are
unchanged, and the original blue and white script particles remain the water
wakes. The treatment adds quiet drifting water, soft shoreline and building foam,
land hovercraft particles that lightly brighten the ground, and faint rippled
reflections of above-water model pieces and admitted projectiles. The player's
**Water** switch (§30) gates all of it: with it off the recorder marks no water
surface and admits no reflection site.

### 26.1 Public API and ownership

`frame.WindView { Heading uint16; Strength int32 }` and `Frame.Wind` copy the
session wind at publication [01 §7.3][I6]; reset clears the value, and the
renderer never reads the live wind service. `drawlist.WaterSurface { Enabled;
Tick; Fraction16; WindHeading; WindStrength; DriftX, DriftZ, Energy }` is the
value field `Terrain.Water`, enabled by the terrain recorder only for Enhanced
non-strategic world drawing, from committed tick and wind plus the presentation
fraction; paused captures retain their phase. `drawlist.SurfaceWake { X, Y, AxisX,
AxisY, CrossX, CrossY, Age, Alpha; Dust; Foam }` is a rotated quad — centre and
half-vectors in recording pixels, age and opacity in [0,1] — immutable until the
next list reset; `SurfaceWakes`, `List.RecordSurfaceWakes` and the optional
`SurfaceWakeSink` carry batches after terrain and before objects, `Clone` owns its
marks, and world transforms apply exactly once in the executor.

Client wake ownership is `water_wakes.go`, hooked from `world_draw.go` and
`trails.go`. The hooks observe every committed tick through the session
publication observer, **including catch-up ticks**, reset with trails at battle,
source and renderer changes, and record the batch immediately after terrain. The
producer uses authored `CanHover`, footprint and committed movement; hidden,
carried, airborne, unfinished and teleported units must not bridge wake history;
every emitted mark starts at a player-visible position, and existing fog
composites cover the batch. A bounded ring holds the recent path and zero movement
emits nothing. Grounded mode admits hovercraft without comparing model Y to the
centre terrain height, because the four-corner conform can differ from that sample
[04 R-MOV-01 §5]. Hot or damaging liquid receives no water foam.

GPU ownership is `water.go` and `water_reflections.go`. A conservative water/shore
mask is cached in painted map coordinates through the terrain inverse projection
[07 §8][03 §2.5]; a negative height sentinel is never water; painted colours are
preserved; the treatment draws before objects and fog; wake fragments clip to the
matching wet/dry mask. No postprocess displaces units, HUD or fog. Cache resources
are released on map replacement and disposal.

### 26.3 Surface treatment and cost bounds

**The mask** is one RGBA image per terrain identity: red ordinary water, green
inward shore distance, blue valid dry ground, **alpha the damp band's ring term**
(§32.3); excluded liquid and invalid terrain belong to neither medium. It starts
at one painted map pixel per texel and doubles
that step until its largest side is ≤2,048 texels (≤16 MiB of GPU pixels). Two
integer chamfer sweeps approximate distance up to 32 world pixels; a separable
nine-tap blur smooths the distance channel without changing wet/dry labels, its
sample spacing at least two world pixels so height-grid corners round even on the
finest level. Bilinear sampling softens the mask while conservative coverage clips
foam and dust. A future height-editing path must invalidate this cache as well as
the painted terrain sources.

A 128-pixel block index skips the water pass when no water intersects the view,
plus one block of slack a side so a coast just past the viewport edge still runs
the pass for the damp band (§32.3). Otherwise the scheduler copies the terrain
composite and applies a viewport water shader before objects. Every constant in
that shader is an authored presentation choice.

**The ripple.** Three smooth value-noise layers displace the terrain sample by up
to 3.4 world pixels per axis, plus a domain warp that deforms them. **None of the
three scrolls on a velocity of its own**: each travels only on the integrated wind
drift, because translation alone reads as a moving tile.

| Layer | Travel | Deformation |
|---|---|---|
| broad ripple | 6 × the integrated drift | offset up to 0.8 of its cells by the warp |
| fine ripple | 11 × the drift | offset up to 0.45 of its cells; lattice rotated 37° about the map origin — one fixed rotation, never a wind-following one, so its cell rows never coincide with the broad lattice's |
| coarse gust patch | 22 × the drift | where it passes, up to a fifth more ripple amplitude and displacement and up to 5% darker water at full wind energy |
| domain warp | the only term that advances on time alone, ≈0.08 cell per second | two noise evaluations on a lattice about 4× coarser than the broad ripple; a deformation, so cells stretch, split and merge in place instead of marching past |

**The field is translated by the wind and never oriented by it.** Retail re-rolls
the wind heading to a fresh random value every 150 to 420 ticks
[05 "The wind phase, its draws, and the generator notification"], so crests
aligned to the heading would swing through a new angle every few seconds; and any
rotation about a fixed point sweeps distant pixels in proportion to their distance
from it.

Moving brightness and blue highlights make the motion readable against fine
painted texture, and bilinear terrain sampling keeps displacement from snapping
between original pixels. Shore fronts travel toward the coast along the blurred
distance field on a ≈4-second cycle with spatially varying phase and opacity;
broad crests fade across the last seven world pixels before the wet/dry boundary,
so its grid is not outlined. Surface brightness varies between −16.5% and +11.5%
of the painted colour at full wind strength inside a gust and between −6.7% and
+6.7% in a dead calm; the blue highlight blend is bounded to 8% at full wind
strength. A wet pixel costs six value-noise evaluations — gust, two warp, broad,
fine and the shore patch. No new pass, texture, uniform or allocation.

**Wind response.** `water_motion.go` observes committed wind through its negative
sine and cosine components [R-WIND-01]. Strength is normalized against 5,000 and
clamped to [0,1]; target drift speed is 0.4–2 world pixels per second; velocity
and visual strength approach the target by 1/90 of the remaining difference each
tick. Integrating the velocity preserves pattern position across wind changes,
heading wrap and reversals included. Recording interpolates previous and current
visual values with the permitted presentation fraction. Repeated ticks do nothing;
source and renderer changes, tick rewinds and observation gaps over 300 ticks
reset the state. The value noise reduces its integer lattice coordinate onto a
289-cell period before hashing, because the drift scrolls that lattice without
bound and an unbounded hash argument leaves float precision, whereas a modulo on
time itself would make the pattern jump; the resulting tile is thousands of world
pixels across, wider than any viewport.

**Particles.** History is bounded to 8,192 marks and 4,096 tracked unit
identities. Hover-dust age and building-foam ring phase both add the presentation
fraction, as scorch and blast ages do, so they advance at display rate rather than
stepping at 30 Hz on a faster display.

| Mark | Admission | Emission and life |
|---|---|---|
| land hover dust | the producer rules of §26.1 | every six travelled world pixels, alternating around the rear skirt from near its outer edge; 45 ticks from initial opacity 0.45, spreading and drifting sideways with quadratic opacity decay; the soft lobed profile composites white at low opacity, so it brightens terrain without darkening it or needing another copy |
| building foam (`water_buildings.go`) | a visible completed floating building on wet terrain, its model top reaching the surface, its committed base height equal to sea minus authored waterline [05 "Geothermal requirement"] — **not** the FBI `Floater` flag, which stock water-yard buildings such as tidal generators do not set | broken elliptical ripples, bounded to 1,024 visible rings; two staggered rings expand and dissolve inside each quad so the footprint is not outlined as a square. This approximates displacement around the base, not the model's waterline intersection, and the shared mask clips it to water |

### 26.4 Above-water screen-space reflections

`ModelGeometry.ReflectWater` admits a visible body whose origin is over valid
ordinary water; `ReflectionSea` is absolute sea height in recording-scale pixels,
alongside `WorldHeight`. Vertex `Height` remains physical relative height,
supersampled geometry included. `Sprite.ReflectWater/ReflectionHeight` and
`Line.ReflectWater/ReflectionHeight0/1` carry signed above-sea height for admitted
projectile bodies and endpoints. Ground-shadow sprites do not opt in; classic
ignores these value fields; cloned lists retain them.

The model lane captures front-facing faces while preparing its atlas, and each
reflected vertex samples that resolved colour at its original atlas position; the
key plane and quad mapper reject source pixels belonging to an obscuring piece.
Physical height, independent of the retail comparison key, clips fragments at and
below sea. Only physical height is reflected, which preserves the hull's ground
footprint: the camera subtracts half height [03 §2.5], so reflected screen Y is
source screen Y plus above-water height, and equal-height points keep their
screen-space direction at every heading. Low hulls can obscure much of their own
reflection; the image is never moved or flipped to force it into view. No extra
model atlas or alternative scene camera is built, and a model omitted from the
atlas gets no fallback reflection. The atlas already applies materials,
construction reveal and waterline tint.

Projectile model pieces share this path. GAF projectiles are reflected billboards
about their anchor's water-plane projection, their art having no per-pixel
physical depth; beam and segment endpoints use committed heights and interpolate
the waterline clip across each stroke. No effect, UI glyph or projectile ground
shadow is inferred reflective from its brightness; §32.2 lifts this for named
explosion and impact art alone, which the recorder admits explicitly.

A retained viewport RGBA plane receives the reflection source, capped at 32,768
vertices a frame with 4,096 reserved for projectile sprites and strokes. A
water-only resolve adds two irregular horizontal ripple frequencies, a softening
filter, a cool tint and 25% opacity; wave phase uses the same committed time and
fraction as the water and freezes on pause. World zoom applies once, to
destination coordinates; source atlas positions are unchanged. The resolve runs
after painted water and before objects, wakes and fog, so reflections cannot paint
over foreground units, shore or UI, and black fog covers the result. It samples
the shared mask **bilinearly** in the surface and wake shaders' coordinate
convention, converts it to coverage with the same smoothstep and multiplies its
premultiplied result by that coverage, keeping the early-out where coverage is
zero. The source plane is allocated only when needed and released on source reset.
Nothing here reconstructs offscreen or hidden surfaces, traces rays or solves
inter-unit reflected depth ordering; overlapping reflected subjects remain an
approximation. Shore foam's opacity is reduced by one quarter beside it.

### 26.6 Aircraft and boat reflections

Four smooth height ramps, in world pixels above sea, shape a model source; every
term depends on physical height alone, so boat opacity is unchanged, no
classification is added and a tall non-aircraft model follows the same rule.

| Term | Ramp | Effect |
|---|---|---|
| source fade | 64–320 (sprite and beam sources keep 64–160) | aircraft at flight altitude leave a faint reflected image; covers every model source, tall pieces and model projectiles included |
| opacity | 64–160 | model opacity 1 down to 0.35, multiplied again by world-anchored horizontal transmission bands, 1 down to 0.7 at full ramp strength, so a high reflection is weakened and broken up rather than mirrored cleanly |
| horizontal displacement | the opacity ramp, floored at strength 0.35 after normalized height is clamped to 0–1, so a submerged corner cannot displace without bound | two world-anchored sine waves of amplitude 3 and 1.05 world pixels; at the floor, 1.05 and 0.3675 |
| blur strength | 64–200 | each source texel blends its narrow horizontal filter (centre 0.5, sides 0.25) with a filled cross kernel — centre 0.25, horizontal pairs at distances one to four weighing 0.12, 0.09, 0.06 and 0.03 each, vertical pairs at distances one and two weighing 0.06 and 0.015 each; both kernels sum to one |

The displacement moves the already recorded reflection vertices and retains their
original atlas coordinates, so it samples no neighbouring model region and needs
no second camera; shared corners follow the same continuous displacement, the
distortion uses committed water time and fraction, freezes on pause, and scales
once with zoom. The wide blur kernel uses nearest texels on a fixed
one-screen-pixel grid — four screen pixels horizontally, two vertically — which
keeps one-pixel details connected at every zoom while the narrow contribution
stays bilinear and scales with zoom; the wide contribution can advance by whole
pixels with water motion, an intentional sampling approximation.

The source shader also writes a viewport-sized RGBA8 height buffer from the same
admitted geometry, atlas samples and hidden-piece checks: red is blur strength
times source alpha, alpha is source coverage after fading, and normal
premultiplied composition preserves a weighted strength where reflections overlap.
The resolve recovers strength as red/alpha and weights each source texel before
interpolation, so adjacent boats cannot inherit an aircraft's blur strength. The
executor marks a coarse grid of 64×64 recording pixels from elevated triangle
bounds, expanded by the full filter reach, water displacement and sampling margin,
converting screen bounds back to recording coordinates before marking so
fractional zoom does not transform the grid twice. Only marked cells run the
heavier filter; adjacent same-kind cells share horizontal quads; cheap and soft
cells use separate compile-time shader variants in two non-overlapping groups, so
ordinary water avoids the larger shader's register cost as well as its samples.
Triangles entirely below 64 or above 320, and bounds outside the viewport, mark
nothing; height-range intersection is conservative. A frame without marked cells
skips the metadata render and retains the single resolve quad. The buffer
allocates lazily, is reused, is resized when next needed, and is retired on source
reset. Top-surface reuse remains an intentional approximation: no underside,
offscreen body or reflected depth is reconstructed.

## 27. Burning vegetation heat shimmer

A modern GPU presentation experiment, not a retail behavior claim. The recorder
tags the resolved body art of burning sprite features using the committed
`IsBurning` flag, with an additional anchor LOS check. No new fire, simulation
state, RNG calls or asset edits are introduced; classic ignores the metadata; and
missing art, hidden anchors and finished burning emit no shimmer. Time comes from
the committed tick modulo 3600, plus the presentation fraction when enabled, with
a stable cell-derived phase offset, and the shader's frequencies wrap at that
tick period, so replaying a list and pausing freeze it.

The plume starts 45% down the resolved body's art and extends upward. Its
half-width is 55% of the art width clamped to 14–38 world pixels; its height is
125% of the art height clamped to 56–112 pixels. Two upward-travelling waves, a
small sideways drift and a squared soft envelope create up to about 2.4 world
pixels of horizontal refraction. Recording scale and the final smooth zoom apply
to all lengths together. These numbers are artistic tuning.

The executor culls against the world viewport before admitting at most 128 plumes
in source order. Bilinear sampling is clamped to the source clip; outside the
soft plume envelope pixels are untouched; overlaps read the same snapshot and the
last plume wins rather than recursively amplifying displacement. `HeatPlumes`
counts the submitted plumes. The player's **Distortion** switch gates the plumes
beside the blast rings (§30). Preparation copies only scalar geometry, clip, time
and scale into its scratch buffer, so no GAF-frame reference escapes the borrowed
list and a retained heat source cannot keep decoded art alive across a map reset;
source reset clears that buffer.

### 27.2 Shared explosion and tree-heat pass

Tree heat **overwrites** explosion distortion wherever both occur — the user's
explicit choice, accepting the small overlap change. The two families append to
one retained vertex and index batch and use one shader, one immutable world copy
and one draw. All explosion quads precede all heat quads regardless of source
recording order. A heat fragment outside its plume discards, preserving the blast
beneath; a covered heat fragment replaces it with a heat-only sample of the
original world, so heat does not refract an already distorted explosion image.
The source-coordinate Y offset selects the shader formula: negative for a blast,
one plus scaled heat amplitude for heat (§28). X carries blast strength or heat
time. Each quad has one selector throughout, and both formulas retain their
arithmetic, clipping, bilinear sampler and independent budgets. A GPU fixture
checks overlap priority, unaffected blast pixels outside the plume, frozen
replay, and exactly two extra submissions (copy plus draw) with either or both
families, compared with neither. No extra render target is allocated.

## 28. Fresh wreck cooling

An Enhanced presentation experiment extending §27's shared heat pass; artistic
tuning, not a claim about retail temperature or damage. A successful violent
unit-death corpse placement records the returned feature instance's birth tick in
session publication state. **Only that exact live instance** publishes a known
birth: map features, restored features, nonviolent feature conversions and newly
discovered features do not acquire heat. The metadata is presentation-only and is
neither simulation input nor saved state.

The modern 3DO feature recorder samples committed age and the optional tick
fraction, so replaying a list cannot advance age. Anchor LOS is required;
underwater origins suppress both effects; removal and reclamation remove the
source with the feature. A birth tick of zero is valid and an unknown birth is
explicit. Packet operands are cleared before each visibility and age decision.

The material begins pale orange for six ticks, loses its pale component, then
cools through orange and red to its original texture by 180 ticks; a weaker
shimmer fades quadratically over 300 ticks. The emission is computed once per
wreck on the CPU and carried in unused vertex RGB lanes of its existing body
composite, and a screen blend preserves texture variation and premultiplied model
coverage. There is no extra material draw, render target, texture lookup, bloom
blur or dynamic light. The atlas-overflow fallback omits emission; normal atlas
bodies carry it.

Wreck plumes use the recorded model bounds, capped to 40 scaled pixels in half
width and 64 in height, with a 32-visible-plume budget applied after clipping.
They append after explosion rings and before tree plumes to the same distortion
mesh; trees still win overlapping pixels, and the tree budget remains 128. All
three families share the existing world copy and single distortion draw; a frame
containing only wreck shimmer still needs that copy and draw pair. Distortion
amplitude is encoded with a positive bias so a nearly cold source cannot round
into the explosion selector. No GPU readback or extra full-screen pass is added.
The **Distortion** switch owns it (§30), which is why a wreck emits no light
(§31.1) when Distortion is off even if Lighting is on.

## 29. Metal/paint finishes and fading scorch marks

All coefficients and texture annotations here are authored presentation choices,
not retail material or temperature evidence. Unit emission/halo and brief
dust/spark/water impact accents were rejected and are not part of this. Existing
glow, blast art, coastal wakes and §28's wreck treatment keep their own
contracts.

### 29.1 Materials

A curated texture-name table annotates textured unit faces as default, metal or
paint. It does not classify feature or wreck faces. Metal receives a broad cool
response beneath the existing glint; paint receives a weaker rough highlight. The
coefficients preserve authored dark seams and panel hue, and untagged faces
retain their existing shading. The team-colour panel textures `colorslt`,
`colorsmd`, `colorsdk` and `colordk2` use the metal finish for every player
frame, preserving the selected team hue; team logos retain their existing
classification.

The table is **authored data, not Go source**. `internal/client/materials/
materials.tdf` is a TDF file with one `[materials]` section whose keys are 3DO
texture names and whose values are `metal` or `paint`; it is embedded in the
binary and parsed once into a lowercase map, and an unrecognised value annotates
nothing. A mounted install may replace it wholesale by supplying the logical path
`nanolathe/materials.tdf` — a remaster pack or a loose gamedata directory — which
the command layer installs after mounting. A missing override is the ordinary
case; one that cannot be read is reported in the standard diagnostic shape and
leaves the embedded table in force, because presentation art never fails a load.
The retail executable carries no material classification for model textures, and
nothing here reaches authoritative state.

`ModelFace.Material` carries the annotation, resolved **once at texture bind**
rather than per face: `resolveModelTexture` stamps the annotation and the
generation it was read under onto the cached texture reference, and the compose
path reads a field instead of lowercasing a name and probing a map. A later
install bumps the generation, which makes the stored byte stale and sends that
face's name through a fresh resolution; a zero value is stale by construction,
because installing the embedded table leaves generation one. The colour vertex
lane retains its low 16 bits for palette index and glint, followed by two
material bits and three quantized normal-response bits: at most 21 bits, exactly
representable as a float32 integer. Shadow geometry carries no finish. The
existing colour shader applies the finish after palette lighting, sharing its
key, reveal and waterline verdicts; reveal replacements clear the finish. There
is no additional model pass, emission atlas or render target.

### 29.2 Cooling and fading scorch

The client observes every committed publication, including ticks between draws. A
nonzero effect ID paired with its event sequence deduplicates primary impact art
at birth; unresolved, hidden and pre-existing effects cannot later create a mark.
Both the event and its ground anchor must be visible. Valid dry terrain within 12
world-height pixels of the impact admits a mark, sized to 0.55 times the resolved
art extent and clamped to 8–64 world pixels.

A FIFO retains at most 256 world-space marks, with a separate 512-identity budget
bounding work within one publication. Marks reset on source or load change,
viewer change or tick rewind; they are not saved. Camera projection produces
screen-space `ScorchMark` records with radius, committed age plus the optional
draw fraction, and a stable variant; drawlist cloning owns a copy of the batch.
The warm centre cools through 90 ticks, and whole-mark opacity then fades
smoothly from tick 90 to 450 — fading starts at three simulated seconds and the
mark is gone at fifteen. Shared drawlist constants define these ages and the cap.
At expiry the CPU removes the mark and the GPU rejects it without submission, and
the GPU caps externally supplied batches to 256 marks per frame.

A smooth uneven procedural quad draws immediately above terrain, below objects
and fog. It reuses the coastal mask's dry channel, including when water animation
is disabled, and has no persistent GPU history or additional render target. The
**Marks** switch owns it (§30); `ModelStats.MaterialFaces` and `ScorchQuads`
report the submitted workload.

### 29.3 Verification

Device readbacks verify material key, reveal and waterline behavior, unchanged
model submissions and image allocation, and compatibility with §28's independent
wreck composite. Scorch checks cover monotonic fading, exact disabled-output
equality at expiry, zero expired submissions, object/HUD and wet masking, cloned
replay, view scale, reset and bounded public batches. Staged impacts observed
through 511 ticks must leave images byte-identical to the disabled output once
every mark expires.

## 30. Player controls for Enhanced effects

The Enhanced effects reached this point as prototypes with executor comparison
switches, environment variables, or nothing a player could reach. **Five
persisted switches** now cover them, beside the glow switch of §19.4. They are
Nanolathe presentation preferences, not retail evidence: the classic executor
composes identical pixels whatever they say, and nothing here is visible to the
simulation or to any committed frame [I6].

`settings.Presentation` stores them as `water`, `lighting`, `finish`,
`distortion` and `marks`, each defaulting to 1. They are integers for the same
reason the display bits are: a stored 0 is "off" and is kept, only a negative
value is repaired, and a file that omits a key keeps the default because the
loader decodes over the defaults [02 "Settings"]. `internal/drawlist.Effects` is
the value type both sides read, with one `bool` field per switch;
`drawlist.AllEffects()` is every family on. `cmd/nanolathe` converts the stored
integers, so `internal/settings` remains a leaf.

| Switch | Recorder gate | Executor gate |
|---|---|---|
| Water | the water phase (§26.1), the wake, foam and water-motion producers, and reflection site admission (§26.4) | the water surface and the screen-space reflections |
| Lighting | none — lighting kinds are always recorded | the battle light pass and its ground pools (§23, §31) |
| Finish | none — face material and normals are always recorded | the metallic glint (§23.7) and the metal/paint finishes (§29.1) |
| Distortion | blast ring metadata (§25), the burning-feature heat tag (§27), and the fresh-wreck emission and shimmer (§28) | the blast rings and the vegetation heat shimmer |
| Marks | the scorch observer and draw (§29.2) and the trail layer (§15) | the fading scorch layer |

These five are the whole **effect** surface. `Renderer.SetEffects` is its only
entry point: the per-family setters are package-internal, each is set from the
selection and from nothing else, and there are no environment overrides, no
window chords and no prototype comparison switches beside them — the package
reads no environment variable at runtime at all. Two exported switches sit
outside the five and are set beside them on every present: `SetGlow` (§19.4) and
`SetDisplayPalette`. Treatments with no switch of their own — the aircraft soft
shadows of §34, the trail layer's executor half — are on whenever the executor
that draws them is; §34's shadows soften a shadow the executor draws either way,
so they belong to the shadow rather than to a family. `New` applies the all-on
selection at construction, so a renderer is in the same state whether or not the
host has presented a frame yet, and a source reset preserves the selection.

`Client.SetEffects` retires the trail, wake, water-motion and scorch histories
whenever the selection changes, the way an executor swap does, so a switch that
was off leaves no stale marks and a switch turned back on starts from the current
tick. It also advances the paused-world revision, because a changed selection is
a different world raster (§13.10).

The host calls `Renderer.SetEffects` once per presented frame. Re-applying an
unchanged selection is a no-op by construction — every switch is assigned from
the argument — so no guard is needed and nothing else can move a switch between
calls. The window polls the shell's committed preference each update
(`RunOptions.Effects`) and hands the executor the client's selection on the
running, paused and benchmark paths; the benchmark keeps every effect on so two
runs measure the same work. A `--shot` capture reads the same settings file, so
it composes under the player's switches. The options page rows and the `+water`,
`+lights`, `+finish`, `+heat` and `+marks` chat commands are described in
[DESIGN_INTERFACE_HUD_INPUT.md](DESIGN_INTERFACE_HUD_INPUT.md) §3.4.1.

## 31. Fire, projectile and ground lighting (Enhanced)

Two extensions of §23, both Enhanced presentation design rather than retail
evidence: more of the world's light sources emit, and the light they emit now
reaches the ground. Classic composes the same pixels whatever this section says,
nothing here is visible to the simulation or a committed frame [I6], and the
player's Lighting switch gates every source and the ground pass with it.

### 31.1 The added sources — contract BL5

The five families that feed the budget are §23's two plus three more. Every one is
already admitted by its producer's own visibility gate, so an unseen event never
lights a visible receiver (BL1); the kind is recorded whatever the switches say
and the executor gates emission.

| Source | Recorder | Colour | Radius (world px at record scale) |
|---|---|---|---|
| burning feature | the feature's own burning art, tagged where the §27 heat tag is taken, behind the same extra LOS gate | measured from the art like an explosion (§23.2) × 1.6, with a warm fallback hue below measured peak 0.25 | `1.4 × art`, clamped 128–160 |
| flame stream | the flame and flame-trail strip families, after their one-point coverage gate [03 R-FX-02 §2] | as above | as above |
| projectile body | the keyed projectile sprite, never its ground shadow | measured from the art | `1.4 × art`, clamped 56–96 |
| beam / lightning | an `Emissive` stroke of the projectile renderer [06 R-WFX-01 §4] | `PAL[index] × 0.8` — a stroke has no art to measure, so its colour is the colour it is drawn in | `0.6 × length`, clamped 48–160 |
| fresh wreck | a model packet whose §28 cooling emission is non-zero | that emission × 0.6, so it fades on the wreck's own cooling curve | 80 |

Muzzle-flash art is an effect record the explosion path already counts, so nothing
is doubled; smoke stays a receiver and is never promoted (§23.1).

**Placement.** A burning feature's light sits at its frame **anchor**, recovered
from the authored offsets: feature art records the top-left the blitter writes
from [03 §5.3.1] while effect, projectile and strip art record the anchor, so every
emitter shares one position and one clip test. A stroke carries the committed
ABSOLUTE height of each endpoint in recording-scale pixels, in fields of its own —
the reflection heights beside them are relative to sea (§26) and cannot stand in
for a physical height — and its light sits at the stroke's midpoint at the mean of
the two heights.

**Flicker.** A fire's emitted strength is multiplied by
`0.75 + 0.25 × (0.5 + 0.5 sin(2π t / 15 + phase))`, a bounded [0.75, 1] wave of
about half a second, where `t` is the committed tick plus the presentation
fraction and `phase` is an integer hash of the recorded position. No wall clock and
no RNG stream is read, so a replayed or paused frame reproduces exactly and
neighbouring fires do not pulse together. Sources that do not flicker keep their
measured or authored strength.

**Fire energy.** Measured fire art peaks at 0.39–0.51 on the reference install:
above the 0.25 fallback threshold, so the installed art carries its own hue, but
far below an explosion's. A fire's measured colour is therefore multiplied by 1.6
before the flicker. The warm fallback `(0.95, 0.55, 0.18)` serves art too dark to
carry a hue, and scales by the measured peak so a dying fire fades.

### 31.2 Budget — contract BL6

The budget stays 64 sources. Each kind has a **cap**, the most it may hold, and a
**reserve**, the slots it cannot be evicted below:

| kind | cap | reserve |
|---|---|---|
| explosion | 64 | 24 |
| nanolathe | 64 | 12 |
| fire | 24 | 12 |
| projectile | 24 | 12 |
| wreck | 16 | 4 |

The reserves partition the budget exactly (a compile-time check holds them to it).
A kind at its cap competes only with itself, keeping its own strongest. When the
budget is full the slot is taken from the kind furthest ABOVE its reserve, and
within one kind the stronger source wins with stable, record-order ties — so a
field of burning trees cannot starve explosions, and a volley of explosions cannot
push selected fires below their reserve. A source whose reach misses the recorded
viewport never reaches the budget at all, and that extent comes from the world
record rather than the framebuffer, because gathering precedes replay and the
record extent is wider than the framebuffer below a rest factor (§16.3).
`ModelStats.BattleLightKinds` counts the selection by family in that order, and a
`--shot` capture prints it beside `GroundLights`.

### 31.3 Ground illumination — contract BL7

The terrain pass ends, after the water surface and the reflection resolve, with
one pass over the visible lights:

1. if no light is selected, or the Lighting switch is off, nothing happens at all
   — no copy, no batch, no cost;
2. otherwise the scheduler is submitted (one barrier), the region the batch
   samples is copied into the read surface (`readcopy.go`), and one clipped quad
   per light is drawn in ONE additive batch.

The fragment is **base × light**, not a flat wash: it samples the copied composite
under the pixel — the ground albedo already carrying the map's painted lighting —
and outputs `min(base × colour × falloff × 2.0, 1 − base)` with alpha zero. The
clamp against `1 − base` stops a bright source from flattening the ground to
white, and the additive blend makes overlapping pools sum as `base × (1 + Σ L)`.
The gain 2.0 stays well below §23.2's model-face gain, because terrain already
carries the map's own lighting.

The falloff is the radial law of §23.2 **with the square dropped** — a ground pool
is read as a shape and wants a body, where a model face wants a tight core — over
`distance² = dx² + dy² + h²`, `h` being the source's stored absolute height. The
pool is centred on the source's **projected** position, `Y − h/2`, the same
half-height projection as visible objects [03 §2.5], because the pass has no
terrain receiver height (SC20 explains why painted terrain cannot be inverted that
way on elevated ground); that correction applies to every family, nanolathe
clusters included, while model and smoke receivers retain their physical source
coordinates and response. The explosion source retains its quarter-frame lift
(§23.2), taken from the current animation frame, so its pool projects one eighth
of a frame above the event anchor, and
the per-animation reach of §23.2 is what lets it light the opening flash; no new
lift or art centroid is introduced. Air bursts retain absolute-height attenuation.
The quad covers the light's full radius rather than the smaller disc a lifted
light reaches, so the cover is conservative and the shader's own distance test
discards the difference without a square root [I2]. The world transform of §16.3
applies exactly once, here.

Placement is deliberate: the copy is taken AFTER the water and reflection resolve,
so the pools brighten the water surface too, and the pass runs BEFORE objects,
wakes and scorch, so units are drawn over it and the ordinary fog composite covers
it.

### 31.4 Cost and verification

Cost is two extra submissions — the barrier's copy and the batch — in a frame with
a light in view, and nothing in a frame without one. Optional
`NANOLATHE_EXPLOSION_SHOTS` captures a frame sequence for inspection. The
synthetic and opt-in real-device coverage this section carried, and the
measurements behind its constants, are in the history file (§31.4, §31.6).

### 31.5 Known limits

There is no occlusion: a fire lights the ground on the far side of a wall, and a
unit between a light and the ground casts no shadow into the pool. Terrain has no
normals here, so a hillside takes the same light as flat ground — the pool is a
screen-space disc, not a projection onto relief. The ground pass reads one copy of
the composite, so a light applies to whatever the terrain pass has already drawn,
water surface included, and never to the objects over it. The absolute-height
approximation can still suppress an entire small explosion on high ground, and
supplies no missing terrain receiver height. A fresh wreck's light borrows the §28
cooling emission, which the Distortion switch owns: with Distortion off a wreck
emits no light even when Lighting is on. The flicker's phase hash is a position
hash, so two fires at one recorded position pulse together and a moving fire
changes phase.

### 31.6 Short explosion terrain flash

Modern presentation tuning, not retail behavior. Only the **terrain** receiver
multiplies explosion RGB by 0.375 (effective peak gain 0.75 instead of 2.0), holds
that multiplier through age two ticks, then applies `(1 − (age − 2) / 10)²` until
age twelve ticks, when it omits the ground quad. At 30 Hz the hold is about 67 ms
and the whole terrain flash ends at 400 ms; at age seven ticks one quarter of the
new peak remains. Current-art colour still modulates this envelope, so it cannot
invent light from dark pixels. Radius, position, radial falloff and the albedo law
are unchanged, and water receives the same short flash through the existing pass.
Nearby models and smoke retain the prior full-colour response; fire, projectile,
nanolathe and wreck terrain lights keep their prior gain and timing.

The recorder supplies an independent, presence-tagged `LightingAge`: committed tick
minus published effect `StartTick` plus the presentation fraction when enabled,
populated regardless of the Distortion setting. Untimed detached sources receive
the lower peak gain but retain art-driven lifetime; missing age does not mean an
expired or newly restarted explosion, and the main production explosion recorder
always publishes age. Hidden or finished primary art still stops contributing
immediately. There is no new shader, texture, pass, clock, simulation state or RNG
consumer. This short flash is the one terrain response.

## 32. Reflected explosions, shoreline band, and the removed sun glitter

Additions to §26's coastal water, authored presentation choices rather than
retail behavioral claims: every constant is artistic, the classic executor
composes identical pixels, and the player's Water switch gates the surviving two.

### 32.1 Sun glitter

**Removed.** The surface shader briefly added a narrow specular lobe on wave
tops, driven by a pseudo-normal differenced from the drifting height field. It
landed at a peak of 0.35, was cut to 0.08 on the first review as too strong for a
surface that must stay subtle, and was removed entirely on the second: the user
judged it was not earning its keep and asked for motion that reads as living
water instead of a tiled sheet sliding across the screen. The lobe, the
half-vector it met and the shared two-layer height function it needed are all
gone, and the four value-noise evaluations they cost went with them. The
replacement is §26.3: no fixed scroll, three downwind speeds, a slow domain warp,
a rotated fine lattice, and coarse gust patches.

### 32.2 Reflected explosions and impacts

Named explosion and impact art records `ReflectWater` and `ReflectionHeight` like
a projectile billboard does, from the same `reflectionWaterAt`/`reflectionHeight`
pair, so the Water switch already governs admission. The reflection preparation
admits a billboard at height **zero**, where it previously required a strictly
positive height, and the source shader's waterline clip makes the same
distinction: a model face or beam stroke carries per-fragment physical height and
is still cut at and below sea, while a billboard's height is one constant for the
whole quad and its admission has already happened on the recording side.

Height zero is the case that matters. A surface impact's anchor sits at sea
level, so mirroring about that anchor sends the opaque upper half of the fireball
below the anchor, where the art itself is keyed out — which is where the
reflection becomes visible. Reflected fire is drawn before objects, so the
explosion composites over its own reflection afterwards; that ordering is
intended. The resolve keeps its cool tint and 25 percent opacity for fire as
well, so the reflection reads as water rather than as a second fireball, and
nothing about bright art is treated as a special case. A projectile billboard
resting exactly at the surface is admitted on the same terms. Nothing else
changes: the vertex reservations, the frame cap, the source plane, the passes and
the mask-clipped resolve are as §26.4 and §26.6 left them.

### 32.3 Damp shoreline band and shallow tint

Dry ground the water has just washed keeps a darker tone. How much ordinary
water surrounds a texel is the **ring term**, and it is precomputed into the
mask's alpha channel when the mask is built (`waterRingField`, §26.3): eight
bilinear readings on a ring of eight world pixels give a maximum and a mean, the
maximum converted with a 0.2-to-1.0 smoothstep as the band's admission and the
mean shaping its falloff through a 0.10-to-0.45 smoothstep, and the two are
multiplied once. The mean is needed because the red channel is binary and the
maximum alone would end the band on a whole texel; both readings are bilinear,
which is what makes the band resolution-independent, since on a large map where
one texel is eight world pixels the ring spans a single texel and only the
filtered reading still resolves the band's width. Water further from the shore
than the ring reaches has water on every tap, so its term is filled in without
sampling.

The shader reads the term out of the `wetMask` tap it already takes for the
other three channels, so the band costs **no fetch at all**. It used to run per
fragment, and the water quad covers the whole viewport whenever any water is
within a block of it, so every inland pixel paid eight bilinear readings —
thirty-two texture fetches on top of the twelve the other channels cost:
**forty-four mask reads per dry pixel, where there are now twelve.** The ring
depends on nothing but the terrain, so an inland pixel was paying every frame
for the same answer. The term is written on the wet side by the same formula so
the bilinear blend across the wet/dry boundary stays continuous (the dry gate
below removes it there), and because it is computed at texel centres and
filtered back, a fragment reads the per-fragment ring's own numbers blended
across one texel rather than exactly.

The result multiplies a dry gate — a 0.05-to-0.5 smoothstep on the mask's blue
channel, times one minus water coverage. The gate is deliberately low: its only
job is to exclude terrain that is neither medium, since invalid ground and
excluded liquid carry no dry flag at all, and a higher threshold would suppress
the band exactly at the waterline, where it belongs. The band darkens the painted
colour by up to twelve percent, half of that steady and half pulsing on the lap
phase the shore foam carries at the boundary, so the ground darkens as a wave
front arrives. All of this runs before the shader's dry early-out, because the
band lives on dry texels; a pixel with no water on its ring returns the painted
colour unchanged, and the mask's alpha says so without a second lookup.

On the water side, the result mixes eight percent toward a pale cyan
`(0.62, 0.80, 0.84)`, scaled by one minus a 0.0-to-0.35 smoothstep of the shore
distance, so water lightens as the bottom rises. Deep water is untouched.

### 32.4 Verification

Real-device fixtures cover the surviving additions. The surface fixture uses an
authored flat-grey terrain and a straight authored coast, drawn through the
shipped shader source **and** through variants that replace exactly one term with
a constant, so any difference is that term: the composed surface is
byte-identical on a held phase and different as the field advances; a gust patch
changes water texels and no dry texel, and with the gust held flat the surface
still differs between two phases, so the churn comes from the domain warp and not
from the gust alone; the damp band darkens only dry texels, never brightens,
leaves covered water untouched and leaves ground beyond the ring byte-identical;
the shallow tint changes near-shore wet texels only. A further variant restores
the retired per-fragment ring search, so the precomputed alpha field is held to
within two levels of the numbers it replaced across the coastal scene. No census
is pinned for the gust — its lattice is far coarser than the fixture, so how
much of the fixture one patch covers is an accident of the authored extent. The
reflection fixture adds a surface impact at height zero whose upper half alone is
opaque, and checks that it casts a reflection below its anchor, that the mask
holds it off dry ground, and that the same art standing on dry ground is never
admitted.

## 33. Cloaked model image commits

`ModelGeometry.Cloaked` is refreshed from the admitted committed unit on each
cached or staged image packet. It is not part of `ModelCacheKey`, retained face
colours or the atlas raster. The final atlas resolve scales both premultiplied
RGB and coverage by the ALP half-colour formula Enhanced already uses (§13.2), so
background details remain visible through the body. This adds no pass or texture
read, and it preserves the ordinary shadow immediately before the body
[03 R-RAST-01 §7][03 R-REN-03D §4].

A keyed carrier blends its complete staged image once, including children;
keyless direct live polygons keep their ordinary writer. The classic packet uses
the loaded ALP lookup instead of the RGB approximation. Cloak toggles therefore
need no raster rebuild, and one instance cannot recolour another's model.
`ModelPreviewOptions.Cloaked` supplies the same committed lane for static visual
review. Focused tests exercise source-major ALP, native and detail placement,
retained opaque/cloaked/decloaked transitions, direct live lanes, and real-device
pixels; existing visibility regressions retain owner bypass and foreign cloak
rejection. The atlas-overflow fallback remains a presentation approximation with
no staged image blend.

## 34. Aircraft soft shadows (Enhanced)

An Enhanced presentation treatment, not retail evidence. It does not change
authoritative flight or the retail shadow gates [03 R-REN-03D §1]. Airborne
mover-mode units supply **clearance** above the higher of the terrain under the
unit and sea level — one receiver height per aircraft, not a terrain-conforming
projection across the footprint. Ground units, structures, clipped silhouettes
and Original use their existing shadow route, and placement and source silhouette
remain the existing mobile path (§22).

The recording carries clearance and model scale independently of retained face
geometry, and each placement refresh clears old admission. The executor filters
body alpha at the shadow placement with a normalized 5-by-5 binomial kernel
(weights 1, 4, 6, 4, 1 divided by 16 on each axis) and bilinear taps in the
existing twice-resolution body atlas. The radius is `clearance/60`, capped at
three world pixels, plus an upper-altitude increment: a smoothstep over
clearances 120–200 from zero to 1.5 world pixels, so the final radius reaches 4.5
at clearance 200 and stays capped there. Lower flight retains the reviewed curve,
and scale is applied once after these world-space calculations. Preserving
integrated coverage away from clipping is what makes thin details fade as the
penumbra grows. No framebuffer colour is filtered. These are artistic constants,
not an optical calibration.

Each receiving pixel samples the ordinary-water mask with its existing 0.8–1
coverage ramp. Wet pixels interpolate shadow opacity from one half to one fifth,
add half a world pixel of filter radius, and displace the silhouette with bounded
world-anchored waves driven by committed water phase and integrated wind drift.
The motion freezes on pause, scales once with zoom, and cannot warp a dry shadow
pixel. This approximates water's weaker shadow response; it does not trace
refraction or separate sky reflection from direct lighting.

The command is one clipped, expanded rectangle per eligible aircraft, in its
ordinary shadow-before-body order, and the expansion includes filter and
displacement reach. The **whole colour page** binds, and the silhouette's
rectangle on it rides the four custom lanes — which is how the projected shadow
commit already samples a page (§22). The fragment clamps its own taps to that
rectangle, on the same half-open test, so a tap outside this subject is
transparent and an adjacent atlas slot can never be read. A sub-image view gave
that bound for free and is gone: Ebitengine allocates and caches one image per
distinct rectangle, a moving aircraft asks for a new rectangle every frame, and
a distinct image splits the batch, so each aircraft also cost an image, a cache
entry and a device draw of its own. Every aircraft whose body is on one page now
shares one device run (`TestAircraftShadowCommitsShareOneRun`). There is no
full-screen blur and no extra render target. The shader uses 25 bilinear
coverage taps plus a bilinear water-mask lookup and a palette lookup.

**The one uniform on a per-frame draw.** Giving the custom lanes to the
rectangle left nowhere on the vertex for the four water operands — committed
phase, the two integrated drift components and the mask step — so they ride a
Kage uniform, `AircraftWater`, installed on the scheduler's shared scene options
before the segment is submitted. This is the single exception to §11.2's rule
that every parameter rides the vertex, and it holds for the reason that rule
exists: the four are **frame constants**, written by the frame's one terrain
command, so every aircraft compiled into a submission was compiled against the
values in place. Their storage — a four-float slice and its one-entry map — is
retained and written in place, so the layer still allocates nothing in a
steady-state frame.

The treatment has no switch of its own: it softens a shadow the executor draws
either way, so in Enhanced it is always on (§30). A recorded clearance of zero is
what selects the ordinary silhouette route, and the paired capture fixture pairs
on that.

Verification: pixel relationships verify wider spread without extra integrated
shadow mass, water attenuation, dry stability, frozen replay, neighbouring atlas
isolation and fractional zoom; the normalized-coverage device test exercises
clearance 200, and the radius test checks low-flight preservation, the upper cap
and scale independence. Installed fighter and bomber models are inspected over
land, water and shore at native and twice scale with staged clearances, and a
shoreline sequence advances water while holding the aircraft fixed. Reproduce
with `NANOLATHE_GPU_DEVICE_TEST=1`, `NANOLATHE_RETAIL_ASSETS` pointing at the
install, `NANOLATHE_AIRCRAFT_MAP` naming a coastal map and
`NANOLATHE_AIRCRAFT_SHOTS` an output directory, then
`go test ./internal/platform/gpurender -run '^TestDeviceFixtureLoop$' -count=1`;
`NANOLATHE_AIRCRAFT_PROFILE=1` additionally runs the paired frozen profile under
the shared benchmark lock. These are staged visual fixtures, not simulated
flight.

## 35. Known limitations and owed work

**Owed human review.** Several layers were built by agents that could not inject
input, so their evidence is captures and frame arithmetic rather than a live
window: the wheel in motion and the snap's feel (§16); the half-pixel step at
refresh rate and whether the two-raster-pixel outline reads right on a rising
nanoframe (§17); the glow interpolated with its sources (§19); and whether the
water's churn reads as alive rather than as a slow wobble, and whether the gust
patches read as wind (§26.3, §32.4). The filtered terrain of §16.3 is a taste
call as well as a measurement: a player who wants the crunchy nearest-sampled map
back should be heard before the lane becomes permanent.

**Executor.**
- The two-surface alternation of §11.5 is unexplained and unlanded
  (`TODO(H1)`), and so is the model overflow barrier's sensitivity to segment
  merging (§13.8). Both change composited pixels for reasons the placement rules
  do not account for.
- The model lane's atlas is two 4096² pages; a frame that fills both takes the
  native painter-order fallback, which has no key plane, no supersample, no
  reveal and no children, and omits its shadows (§22.1). The parameter image
  caps at 174,762 entries, past which a frame's remaining faces interpolate
  linearly, and the retained-lane store caps at 2,048 entries, past which the
  least recently used lane is re-derived cold.
- Glow's two magnified adds could be one shader pass sampling both octaves, and
  the half plane could go if Ebitengine's mipmapped shrink proves cheaper than a
  pass; the glow's cost is resolution-dependent and was measured only at 1080p
  (§19.3).
- The terrain atlas stores one index per RGBA8 texel; storing it in one channel
  would cut the texture to a quarter (§14.5). A half-resolution tile set for the
  strategic range is unmeasured (`TODO(question)` in `terrain.go`, §16.10).
- A keyed source cannot take the filtered sampling of §16.3 without an
  antialiased sprite edge, which is a look to approve (`TODO(question)` in
  `schedule.go`).
- A model cross-fade at the strategic cut needs an alpha lane on the model
  commit (`TODO(question)` at `Client.markerAlpha`, §16.11).
- Aircraft soft shadows on one page now compile into one device run (§34), but
  dense overlapping formations and non-Metal backends are unmeasured, and the
  5×5 kernel's hundred bilinear taps per pixel over the expanded rectangle are
  unchanged.

**Recorder and content.**
- The dominant cold reason in the model lane is now the **first sighting of a key
  that never comes back**: a subject whose cached lane is re-stored every
  presented frame, which under interpolation is every turning unit, because
  retail rebuilds on an orientation delta **strictly greater than seven**
  [03 §5.2] (§2.2.1). That is retail's contract and is not changed here — a
  rebuild is a new revision of the same body, as it should be — but it means the
  store sits at its cap with inserts evicting one for one, and a cheaper key for
  a lane that turns is unexplored.
- The session publishes no feature `InstanceID`, so a 3DO feature's retained lane
  is keyed by its map position and pruned by age (§13.12). Publishing one would
  key the lane by identity and let it be pruned with the feature set, which is
  owed work on the publication boundary, not on the renderer.
- The recorder still builds the doubled packet's native faces (§22.4).
- A carried child texel its own reveal or clip erases stays a hole where retail
  shows the carrier through it (§22.4), and the modern executor omits a child
  packet that is keyless or itself has children (§22.3).
- `DontShadow` exists in the pose and is compared with it but the shadow
  projection does not consult it (§13.12).
- The strategic icon catalog's AA, fighter, artillery and heavy-role
  distinctions are unresolved, as are levels with neither a reachable build path
  nor one unambiguous authored token (§18.3).
- The Anti-Alias option's menu text still describes the retail structure
  behaviour; its Enhanced meaning is wider and the text is owed a line (§17).
- Render type 2's fixed global sprite binding is unresolved in classic as well
  (§10), and the `Lens` transparent key carries a `TODO(question)` for retail's
  uninitialized value under SPEC_CONFLICTS SC18 (§2.5).

**Measurement gaps.**
- No live capture of an explosion over water has been obtained: `--shot` runs a
  skirmish with no opponent, so nothing fires, and the battle benchmark places
  its armies on dry ground. The device fixture is the only evidence for the
  reflected effect (§32.2); an AI opponent reachable from a capture flag, or a
  staged coastal diagnostic, would settle it.
- The frozen-list capture route (`--shot-gpu-profile-frames`) fails on the
  reference host while acquiring the Metal drawable texture, on unchanged main as
  well as on branches, so several layers have live-benchmark evidence but no
  frozen-list timing. Resolving that host failure would settle those gates.
- Ebitengine's public API exposes neither GPU completion nor actual presentation
  timestamps, so no measurement in this repository establishes GPU execution time
  (§6).

## 36. Commander arrival prototype

User-requested artistic presentation, isolated on `prototype/commander-arrival`;
not a retail behavioral claim. `--arrival` opts fresh modern skirmishes into a
2.05-second opening. Saves, campaign entry, classic, and ordinary captures retain
their existing entry. A skirmish without a local commander skips the opening.

The shell requests one tick-zero publication of the loaded session through
`Session.PublishOpeningFrame`, using the ordinary publisher without a phase or
RNG draw. It selects the local published commander using the immutable
definition's Commander flag, frames the landing in the final viewport, and
holds gameplay input and the authoritative pump. Escape skips; losing focus
holds presentation time. Its clock waits for the first submitted GPU frame,
then holds a dim scene for 0.5 seconds before the 0.8-second map reveal. The
commander overlaps its final quarter-second, with no intervening pause.
Handoff rebases the host scheduler anchor so the intro
cannot become accumulated tick debt. The ordinary pause mechanism is untouched.

`Client` stores only commander identity, position, and elapsed presentation
seconds. The commander is hidden during the map reveal until 1.05 seconds,
then a local model-input copy accelerates downward with cubic easing, hitting
the ground at 1.33 seconds. Starting lift places the commander at the top of the
battle viewport, so his drop is visible throughout that interval;
its shadow is suppressed during descent. No committed pose, occupancy, weapon,
health, damage, or simulation RNG changes. Snapshot replacement retires the
intro, and every time change cancels speculative recording before invalidating
the presentation epoch.

The world-begin packet carries elapsed seconds, landing position, map-grid
origin, recording scale, starting lift, and explored reveal radius by value. The radius measures
reveal-chunk centres overlapping on-screen fog cells whose channel zero is not
15, including partial fog edges and gray explored terrain [03 §3.3]. Fully black
fog and off-screen tiles do not set the reveal speed: even a small starting
island fills the reveal interval and overlaps the descent. The short-lived scan
reads the published fog grid and needs no GPU readback; missing fog metadata
falls back to the viewport extent. The radius is in world pixels; positions and
scale pass once through
the world affine mapping. After fog and before interface, one viewport copy and
one shader draw reveals the map with an overlapping warm descent streak and a
strong warm impact flash, colourless distortion field, and damped screen recoil.
The expanding field uses the same bipolar compression/rarefaction profile as
explosion distortion (§25); it warps scene pixels without adding a coloured rim.
The map reveals in 32-world-pixel screen chunks with a
small rise and settle; sampling the completed scene carries terrain, trees,
water, and fog together. It is a sampled image effect, not moving terrain
geometry. Black source fog stays black. The ring finishes according to the
viewport's farthest corner; the final stage returns the exact source image.
The pass borrows the existing read surface and submits nothing when inactive.

The falling commander starts hot, reusing wreck emission, nearby lighting and
rising air distortion (§28) on outgoing model packets. A warm orange glow cools
to the ordinary texture over four seconds after impact; the heat follows the
same unit identity as it moves. Gameplay resumes at 2.05 seconds while cooling
continues on a small presentation clock, frozen on pause or focus loss. Both
glow and shimmer respect the existing Distortion switch. No texture is replaced
and no additional model shader is needed. The shared diagnostics count this
source with wreck heat/lights, an intentional prototype shortcut.

For repeatable visual inspection, use `--arrival --shot-ticks=0
--shot-arrival-time=0.75` with a modern `--shot`; useful stages are 0.2 (lead-in),
0.75 (reveal), 1.28 (descent), 1.33 (contact), 1.45 (distortion),
2.05 (gameplay with hot commander), and 5.33 (fully cooled).
Timings, tint, displacement and attenuation are authored prototype choices.
There is no dedicated arrival sound, landing joint animation,
terrain-specific impact treatment, or multiplayer start barrier in this version.

Verification locks clock/RNG preservation, handoff without catch-up, immutable
poses and slot identity, speculative-record invalidation, one world transform,
shader compilation, unchanged fog/interface pixels, repeatable GPU replay, and
inactive/completed pixel and draw-count identity. Real-map captures and the
live battle benchmark remain required for visual/performance review (§6).
