# Design — The frame draw list and the GPU renderer

`internal/drawlist`, `internal/platform/gpurender`, and the recording side of
`internal/client`. One committed-frame walk records one ordered list of draw
commands; two executors replay it. The **classic** executor is the software
composer writing palette indices into a byte surface. The **modern** executor
replays the same list through Ebitengine, composing in true colour on the GPU.
The public choices are `--renderer=classic|modern`, default modern, also
selectable on the Nanolathe options page and with F10. The simulation cannot
tell which executor is selected. On macOS, `--metal` plays one battle in an
experimental native Metal renderer instead; it draws the world from retained
meshes rather than replaying this list, and
[DESIGN_METAL_RENDERER.md](DESIGN_METAL_RENDERER.md) describes it.

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
| `Fog` | the op list `render.BuildFogOpsWindowWithArtInto` produced, already clipped, plus the gray and black GAF entries | the three fog fills and the fog GAF blit `[03 §3.3]` |
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
their bound source lifetime: GAF frames and entries, PCX, FNT, the palette tables,
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
* `(*Renderer).PrepareTerrain(drawlist.Terrain)` prepares the native and detail
  terrain atlas keys during loading (§14.8), without replaying a frame, and
  `PrepareSprites([]*formats.GAFFrame)` places the battle's feature rest art
  in the scene atlas at the same boundary. `SetSparseTerrain(true)` instead
  fills tile atlases on demand, a tile the first frame it is in view (§14.8).
* `(*Renderer).SetEffects`, `SetGlow`, `ResetSources`, and the diagnostics
  `DeviceDraws`, `ModelStats`, `AtlasUploads`, `FogContentError`,
  `DebugSnapshot`, `DebugLastFrame`.
* Resource caches keyed by pointer identity: tile atlases per (tile set, detail
  set, scale), GAF frame atlases filled on first use, FNT glyph atlases, the 3DO
  texture atlas with its LOGOS frames.

Draw art arrives as recorded commands; the executor caches by the pointer
identity the record carries. Loading can supply the same immutable terrain
identities early through `PrepareTerrain` (§14.8).

**Two surfaces, both true colour.** `surfaces[0]` is the composite the whole
frame is drawn into and the image `Execute` returns: every source index is
resolved through `PAL` at the fragment that writes it, so there is no expansion
pass (§13.3, C-G8 as amended). `surfaces[1]` holds the read copy the fog run and
the `readcopy.go` layers sample in its top-left frame-sized region; beyond that
region it holds the ground-light field (§31.8), so every reader keeps its
samples inside the frame, and one that clamps clamps to the frame, never to the
read surface's own size. Index-carrying *sources* — tiles, GAF frames,
glyph strips, the model pages' key plane — stay in the red channel of RGBA8,
sampled nearest in Kage pixel mode, so `index = int(r*255 + 0.5)` recovers the
byte exactly (C-G4).

Batching is specified by the compiled executor of §11: the scheduler groups
commands into phases wherever that is order-preserving (C-G3). The families
whose result depends on what is already there — `Tinted`, `Lit`, `LitRect`,
`ShadeRect`, the model shadow commit, the trail marks, the lit discs and the fog
composite — are device blends rather than destination reads since §13.3, but the
order they impose is the same, so the placement rules are unchanged. Those whose
blend is source-over — `Tinted` and the model shadow commits — evaluate in the
opaque scene shader and ride the opaque class, so they share runs with the
writes around them in record order (§13.3). Two
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

Device images that hold nothing between frames also survive: the model lane's
4096² key and colour pages and the water reflection planes, each cleared
before every use, and the flash disc page with its staging bytes, whose
frames are keyed by generated table and frame and rewrite their region and
border before upload. Fixed-size source pages — the scene atlas's shared 2048²
pages, every model texture page and the pages of a terrain atlas filled on
demand (§14.8) — and the water mask, replaced wholesale per map, go to a pool
of up to 128 MiB (`page_pool.go`) and come back cleared to whoever asks for
that size next. A 3DO texture frame is written straight into its model texture
page region; only a frame too large for a page keeps a standalone texture. On
unified memory every fresh texture is wired before the first frame that uses
it can start: a settings-preview scene change measured about 100 ms of driver
wiring on an M3 Pro, 56 ms of it for the model lane's three 64 MiB planes,
inside one frame, and a standalone texture per model texture frame added
about 150 small allocations to every scene's first frame. With these kept, a
new battle or preview scene packs into memory the renderer already holds.

**Shared pages.** Renderers that execute one after another on one goroutine
may draw through one `SharedPages` set (`shared_pages.go`): the model lane's
page planes and the page pool. Every `Execute` clears the model page rows it
uses and finishes reading them before it returns, and the pool hands a page to
one owner at a time, so the device order Ebitengine keeps is all sharing
needs. The settings screen attaches one set to all of its preview renderers,
so a renderer made for a new card size or the first compare does not allocate
its own 128 MiB of planes, and the compare twin packs into pages the primary
retired (DESIGN_INTERFACE_HUD_INPUT §3.17). A device fixture interleaves two
renderers sharing a set across resets and compares every frame with an
unshared renderer's. Renderers that may execute concurrently must not share a
set; a battle's renderer has its own.

**Shared programs.** Every Kage program the package compiles goes through one
process-wide table keyed by its source text (`shader_cache.go`). Programs are
immutable and no renderer deallocates one, so a second renderer — each
settings-screen preview scene, its compare twin, a film or capture — reuses
the first one's programs. The backend compiles a new program on the render
thread inside a frame, and a renderer carries about thirty. Ebitengine's
Direct3D backend runs `D3DCompile` at optimization level 3 for every new pixel
shader and caches only vertex shaders, so a renderer per preview scene paid
the whole set at every scene change on Windows; the per-program cost there is
expected to be tens of milliseconds but has not been measured on Windows. On an
M3 Pro (Metal) the worst scene-change freeze fell from 485 to 168 ms with this
change alone. A source that fails to compile keeps its error, so `NewChecked`
reports it for every renderer. This is host resource sharing; no pixel changes.

#### On-demand effect uploads

**Nanolathe host storage policy.** Large effect frames marked `Transient` use
a separate 256 MiB / 256-image cache, including the duplicated edge border.
They never enter the permanent scene atlas or the emitter-color memoization
map. Small effects admitted to the client's bounded durable tier retain their
ordinary shared-atlas identity. Both paths upload the same palette-index and
opacity bytes and use the same keyed/tinted/LHT shaders.

Transient uploads are pinned for a complete `Execute`, including preparatory
passes, scheduler submission and glow/reflection work. On a miss, old uploads
unused by that execution are evicted first. A simultaneous working set larger
than the cache allowance remains submission-owned; its excess is deallocated
after submission. It is never accumulated across an animation's lifetime.
Page tokens preserve the existing absent-page sentinel, so reflections retain
source identity as well. One local padded upload buffer avoids growing the
permanent scene scratch planes to giant-effect dimensions. Recorded CPU frames
remain immutable if their cache ownership disappears. Source reset releases
all transient images alongside the existing resource owners.

The renderer's retained-cache bound excludes the current draw list, backend
queued commands, temporary upload planes and driver overhead. It is not a
claim about total process/GPU memory. Tests exercise submission pinning,
eviction across generations and eager/transient device pixel equality.

This is a host resource-lifetime correction, not a retail rendering claim.
Previously the one process-lived renderer retained each newly loaded terrain and
immutable source identity forever, so memory grew with the number of battles.
The terrain reference also retains its movement-service binding.

### 2.4 `internal/platform/ebitenapp` — the switch

The adapter owns one `client.Client` and, lazily, one `gpurender.Renderer` built
on modern loading preparation or the first modern Draw, so a classic run never
allocates device textures.
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
  Independent factory occupants join only when their cloak and Digger state
  match the factory's (DESIGN_PRESENTATION_CLIENT §5). A mixed Digger pair
  records two independent bodies, preserving each subject's own key base and
  final erase; actual attachments retain the ordinary carrier composition
  [03 R-REN-03A §4]. This admission is shared with the classic executor.
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
  patterned fills and masked checkers select the even phase of final destination
  coordinates plus camera parity, matching the byte writer across clipped
  edges, and fog GAF frames sample their frame `[03 §3.3]` `[03 §4.3.3 R-RR16-A §1]`. The
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

Original preserves the established keyless composition texture fallback:
raw texels for either shading selection, with the width-128 stride exception
[03 R-RAST-01 §1 step 6], including diagnostics. Direct framebuffer targets
retain normal stride. The anti-alias composition scratch always has a key
plane, even when the resolved image is color-only [03 R-REN-03A §6]. Enhanced
continues to sample its texture atlas with authored dimensions and its existing
geometry lighting/depth policy under this raster approximation contract; it
does not reproduce the software texture fallback.

Two properties keep the approximation bounded. A four-corner face — flat or
textured — evaluates retail's own two-chain span mapping per fragment from the
parameter image (§11.2 "Textured quads without strips"), rather than a generic
inverse-bilinear map, so a non-parallelogram quad does not develop the diagonal
bend that motivated the original strip path. And the key pass and the colour
pass evaluate the same mapping, so they agree about a texel's key except where
the shader compiler rounds the two contexts differently: one key apart, at a
fraction of a percent of a mapped face's texels (§22.4 "Owed", item 4). Rings
that are not four-corner faces interpolate their lanes linearly.

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

The detailed view is designed in §14: native (1×) and detail (2×) record steps,
2× terrain and feature art synthesized from the map's own pixels at load time,
and model geometry rasterized at output scale, with every world-space layer,
picking, fog and the minimap sharing the one transform.

Continuous zoom between the steps, and the strategic view below 1×, are **§16**,
and they are modern-only: the classic executor uses §14's two steps.
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

### 5.5 Enhanced unit overlap order

Retail and Original draw units in 16-pixel world-Z rows, with ascending slot
order inside a row `[03 R-RAST-01 §7]`. Two overlapping units can reverse
which one covers the other when just one crosses a row boundary, even if their
front-to-back positions never reverse. Enhanced keeps the same row admission,
grounded/airborne passes, feature tail, and per-subject height planes, but
orders units within each row by their full 16.16 world Z, then by slot on an
exact tie. Adjacent rows already have that Z order, so an unrelated row
boundary cannot reverse an overlapping pair. `RecordModernFrame` selects
this order while recording the modern list. Classic composition retains the
retail order even in a two-renderer comparison capture where Enhanced art is
enabled for the modern half. This is a presentation policy only:
it does not change occupancy, collision, selection, RNG, or the simulation
frame. Units that genuinely pass one another in Z can still exchange visual
priority. The Classic list preserves retail's slot tie.

The boundary fixture draws the same overlapping pair just before and after a
plot-row edge in both executors. The enhanced bucket check also holds equal-Z
slot ties, feature order, repeated walks, and warm-frame allocation behavior.

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

   **Known gap — `both` records twice, from the same CRT.** "Record once" is
   the requirement, not today's behavior. The `both` route composes the classic
   image and then calls the modern recorder, so the frozen committed frame is
   walked twice. The pair is deliberate today: the classic-inclusive list omits
   the geometry-only model packets the GPU consumes, so a single list cannot yet
   feed both executors. Closing the gap properly means one list both executors
   can replay.

   What the two walks no longer differ in is the presentation RNG. The classic
   compose is wrapped in the pre-record path's CRT save/restore
   (`Client.SnapshotPresentationCRT`), so the modern recording starts from the
   stream state the classic compose found rather than the state it left. Before
   that, a scene with segmented lightning projectiles gave `both` a modern half
   that differed from `--shot-renderer modern` at the same seed, and `framediff`
   attributed presentation RNG jitter to executor divergence; the segmented
   pass of [03 §5.4] draws from that stream in both walks [I4]. The two modern
   halves are now byte-identical, which is what makes `both` usable as the
   parity tool while the double walk stands.
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
then the Digger erase. The recorder uses the software path's shared threshold
helper: positive depth admits the pass, then the threshold narrows to an
unsigned byte. A wrapped zero remains an active cutoff; the packet's separate
waterline mode distinguishes it from no pass. Keyless subjects skip both
clipping passes. The final body coverage punches its shadow, as in classic. No software image planes are
allocated during recording. [03 R-COMP-01 §3][03 R-WATER-01 §2]

### Attached-unit composition

A keyed carrier owns an ordered list of child geometry and signed height deltas.
Each child keeps its own reveal and outline on its **own** key. Its raw
composition joins the carrier before waterline and Digger processing, so only
the carrier's passes act on the child's **shifted** key. Its shadow-only
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
them. The model shadow commits join it for the same reason (§13.3). The
remaining destination-compositing families (`FillLitRect`, `FillShadeRect`,
`PointLit`, the trail marks, the lit discs, the strategic marker layer) draw with
one destination shader (`sceneDest`) whose custom attributes select the table and
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
passes evaluate that two-chain mapping per fragment from a per-frame parameter
image, then floor before sampling. This is not a generic inverse-bilinear map,
which differs from the retail mapping on a non-parallelogram. It removes the
diagonal bend the strip path was introduced for (§5.1) without CPU scanline
work. Rings that are not four-corner faces interpolate their lanes linearly.

The parameter image is made of twelve-texel RGBA8 slots, two 16-bit values a
texel. A subject's verdict entry takes one slot (§22.1); a mapped quad takes two
(`model_quads.go`):

* The **corner entry**: the corners' positions, texel coordinates, keys and
  shade rows, rotated so the top corner comes first, with the bottom corner's
  index. The colour pass and the water reflection walk it: each chain's edges in
  turn, every edge's slope divided out at the fragment.
* The **key entry**: the span writer's edge setup for the key, made once per quad
  on the CPU — the corner rows and the bottom corner's index, the corners' keys
  and columns, and each ring edge's 16.16 slope in the direction its chain walks
  it. The key pass reads it. The fragment chooses each chain's edge by comparing
  its row with the corner rows: an edge that descends owns the rows from its top
  corner down, and a later edge overrides an earlier one, so the edge a row
  belongs to is the last descending edge that starts at or above it — the "later
  edge wins" rule of the span writer's edge table. It walks the column with the
  stored slope, with the same truncation, +65535 bias and half-open rows, and
  interpolates the key along two edges rather than up to six. That is eight
  texel fetches instead of twelve, and no integer division.

The colour pass keeps the walk because the shader compiler rounds by the shape
of the code. With fast math, it rounds the product of each chain's first edge
separately and adds it to the top corner, and it fuses a later edge's
interpolation into one multiply-add. Where a shader reads all four lanes, as the
colour pass does, it rounds some of them differently again, for example the
right chain's first edge. The key entry's code keeps the walk's shape: each
chain starts from the top corner, its first edge updates that value, and a
later edge overrides it. So it gives the walk's key bit for bit where a shader
reads the key alone. No shape tried gave the colour pass's four lanes bit for
bit.

The same rounding means the walk's key in the colour pass can differ by one from
the key its key pass wrote. In a probe of 512 random quads that happened at
0.28% of texels, the colour pass's key lower at three quarters of them. Where
it is lower, the colour pass rejects that texel of its own face. The two passes
behaved this way before the key entry, and the key entry keeps that behaviour
(§22.4 "Owed").

At the Moon Quartet Survival capture (`--benchmark-capture`, 30 draws a second,
GPU timestamps from a locally instrumented Ebitengine), the key pass's fragment
stage fell from 1.07–1.12 ms to 0.86–0.88 ms (median). The colour pass is
unchanged. `battle.png` is byte-identical at both zoom levels and both draw
rates.

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
borrowed from per-frame scratch. It also reaches into Ebitengine: each
destination image converts a draw's vertices into a buffer of its own that is
reallocated at exactly the draw's size whenever a draw outgrows it, so a batch
growing by a few vertices a frame reallocated hundreds of kilobytes every few
frames. The executor's draws hand over a vertex slice rounded up to a size class
of at most an eighth more (`deviceVertexSpan`); the extra vertices are storage
no index references. §11.2 "Allocation policy" states the standing
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
  writers do [03 R-REN-03D §4–§5]. Source-over is also the opaque blend, so all
  three ride the OPAQUE stream and the scene shader (§11.2): a shadow commit
  reads the model pages the body commit already binds. Until 2026-09-26 the
  shadow commits kept the destination shader, and every body drawn over a
  shadow opened a phase: a 1,600-unit battle compiled 34 phases and 89 device
  draws, and a coastal battle 37 and 113, where they now compile 8 and 24, and
  11 and 47, and the render thread's offscreen draw work fell from 1.31 to
  0.84 ms a frame. At rest scales the composite is byte-identical. At a
  fractional world scale a commit's texel fetch then still depended on the
  origin of whichever image the run bound in slot 0, so the move shifted a
  sub-texel rounding in 150 pixels of one unit of a 0.75× Ashap Plateau frame;
  the renderer's images are now unmanaged, at origin zero, so run grouping no
  longer reaches a fetch (§22 "Page passes beside Replay"). The other ALP
  families that bind a shader of their own — the strategic
  marker layer, the aircraft shadow, the scorch marks and the water reflection
  resolve — stay in the destination class.
* *Row families* — `FillLitRect`, `FillShadeRect`, `PointLit`, the lit discs and
  the trail marks — scale the destination: LHT row r by `1 + r/30`, SHD row r by
  `0.06875·r`. One blend serves both: source factor destination-colour,
  destination factor source-alpha, so `out = dst · (src.rgb + src.a)`; the
  fragment carries `(min(k,1), min(k,1), min(k,1), max(k−1, 0))`. A factor above
  2 clamps at 2 (SHD row 31 is 2.13; the difference is below the quantization
  floor). Repeated scaling of one picture compounds the difference. The results
  darkening is the case that shows it: ten `FillShadeRect` steps, rows 13 down
  to 4, over the retained battle picture `[08 R-CAMP-01 §6]`. The classic
  chain re-quantizes to the palette after every step, while the composite
  keeps the product. On the retail tables, one row's lookup already differs
  from its formula by a mean of about 10 and up to 22–35 levels per channel at
  rows 4–8. Mid-sequence a battle view differs by up to about 30 levels per
  channel between executors, and both converge to black by the last step. This
  is the intended consequence of the true-colour composite, not a parity
  defect, and it predates the in-battle end titles.
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
blit and the model shadow commits, which is the same blend and so the same class
and the same run.
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
Under the asynchronous simulation of §13.13 the pair is the one before the
latest release, pinned for the frame.
Everything below is an Enhanced presentation rule; Original keeps committed-tick
sampling.

**Cadence — Nanolathe host presentation policy.** Ebitengine runs Update once
per display frame (`SyncWithFPS`), refreshing its portable input snapshot before
Draw. Update only polls and buffers input until a separate 30 Hz host step is
due. The follow-camera glide [07 R-CAM-01 §12], scroll pass [07 §10], client
step and sub-tick budget [01 §4.2] retain their existing host cadence. The host
accumulator initializes once before the first Draw, carries a signed remainder
against the ideal 30 Hz instants so the long-run rate is exact, and limits
catch-up to five steps per frame, retaining the previous window scheduler's
jitter/stall policy. Each step lands on an Update, one per refresh, and the
choice has hysteresis (`hostClock`): a step takes the Update that keeps the
cadence — every second at 60 Hz, every fourth at 120 Hz — while that is
within three quarters of a refresh of its ideal instant, and moves by one
Update only past that. Rounding each step to the nearest Update instead let a
step whose ideal instant sat near the midpoint of two Updates follow the
jitter: two steps a refresh apart, then a gap a refresh too long, repeatedly
while the phases stayed close, and the world and camera blends paced by the
step jumped and held at each pair. It does not select simulation ticks or
consume RNG; the session still owns that budget. Original presents only after a host step; Enhanced may present on every
Draw. `--fps N` caps how often Enhanced presents: a Draw the cap does not
present returns without recording and the retained screen keeps the last
frame. Draw still sits on the display's vsync grid. Where the cap's interval
is a whole number of refreshes, a Draw that arrives sooner than the interval
(less an eighth of it, the vsync jitter allowance, and at most half a
refresh) is skipped, so the cap lands on that multiple; where it is not, the
present schedule below chooses the refreshes. The refresh is measured, not
assumed — half
the median spacing of two consecutive Draw arrivals — because a ProMotion panel
changes it mid-battle, dropping from 120 Hz to 60 Hz and back. When the
measured refresh is no faster than the cap every refresh is due, and only a
Draw within half a refresh of the last present, one of a burst, is skipped:
at 60 Hz the eighth-interval test skipped the refresh after any present more
than 2 ms late, so one hitch cost two refreshes and a quiet battle counted
55–59. A present is timed by the refresh it belonged to: when the Draw after
a present arrives well inside one refresh of it (a quarter to three quarters
of a refresh), the present's own Draw was late — the loop was held up — and
it still reached the screen at the refresh after its own, so the present is
booked that much earlier. Timed by its late arrival it pushed the cap's clock
forward and the next present waited a third refresh; on a 120 Hz panel under
host load that was about half of a heavy save's late frames (3.1% → 1.1% and
2.7% → 1.3% in two recorded window traces). Original ignores the cap.

**Present schedule — Nanolathe host presentation policy.** Holding the whole
number of refreshes above the cap's interval fell well short of the cap
wherever the refresh does not divide it. On a 144 Hz display (6.9 ms) a 120
cap's threshold of 7.3 ms passed over every other refresh and presented 72
frames a second, a 60 cap every third refresh (48) and a 30 cap every fifth
(28.8). A tester with a 144 Hz display on Windows reported 79, 49 and 24;
another's overlay read a median frame of 13.7 ms under the 120 cap, and 2,497
frames in 30 seconds. The macOS pacer's slot rule was the same test and fell
as short on a 144 Hz external display. The allowance could also overshoot: an eighth of a 30 cap's
interval is a whole refresh at 240 Hz, and that cap presented 32 times a
second.

The window's cap and the pacer therefore share one schedule
(`frame_pacer.go`). While the cap's interval is within a thirty-second of a
whole number of refreshes (`evenCadence`) nothing changes: every n-th refresh
presents, timed from the present before. Otherwise each present is due one
cap interval after the last present's **deadline**, not after the present,
and is made at the refresh nearest that deadline. The refreshes between
presents then take the two whole numbers either side of the ratio in turn,
spread as evenly as whole refreshes allow, and the rate is the cap's: 1, 1,
1, 1, 2 refreshes for a 120 cap at 144 Hz, 2, 2, 3, 2, 3 for a 60 cap, and
5, 5, 5, 5, 4 for a 30 cap. The schedule never presents faster than the cap
to catch up: a present that lands a refresh or more after the refresh its
deadline chose — a refresh with no Draw, a stall — starts the schedule again
from itself, as the even cadence always did. The refresh a present plans for
the next is chosen when the present is made, and held as a time: chosen
afresh at every Draw, a deadline half-way between two refreshes followed the
wobble of the measured refresh from Draw to Draw, skipped the earlier refresh
and restarted the schedule from the later one. The window counts a Draw in
refreshes from the last present: it belongs to the refresh it arrived after,
unless it came within a quarter of a refresh of the next, the quarter the
late-present booking above uses. A present booked earlier by that rule leaves
a deadline the schedule kept where it was.

The price is judder. No gate presents 120 evenly spaced frames a second on a
display that refreshes 144 times: one frame in five stays on screen for two
refreshes (13.9 ms) and the others for one (6.9 ms), 24 times a second. Each
frame still shows the world at the instant it is presented (the blend
below), so motion is where it should be; what alternates is how long each
frame stays. Two even alternatives were rejected. Presenting every refresh
while the refresh is under twice the cap is perfectly even, but presents 144
frames a second for a cap of 120 — a fifth more work than the cap allows, and
bounding that work is what a cap is for. Holding the largest whole number of
refreshes is even too, and is the reported defect: 72 frames for a cap of
120. A player who wants an even cadence on such a display can choose a cap
the refresh divides, or present at the display's own rate with `--fps 0`.

The thirty-second covers the display and the cap running on different
clocks. A 60 cap is 1.998 refreshes of a 119.88 Hz panel; a schedule held
exactly to the cap would correct that drift with a frame one refresh short
every eight seconds, where the even cadence presents 59.94 times a second. It
also covers the window's refresh estimate, which moves by a percent or two
when Draws jitter by a third of a refresh. The nearest ratio the schedule must
still hold to the cap is a 30 cap at 144 Hz, 4% from five refreshes. A display
within an eighth of the cap's interval of the cap itself still presents every
refresh, as above: that can exceed the cap by up to a seventh, and the band
holds only displays whose rate is the cap's.

While the cadence is uneven the schedule also plans the spacing to the next
present, and the pre-record extrapolates over that plan (§13.10,
`observeDraw`). Its interval otherwise — the last spacing or the cap, the
longer — predicts neither spacing of an uneven cadence: on a synthetic 144 Hz
refresh train 40% of the presents at a 120 cap and 80% at a 60 cap missed
their pre-record, and the ones that hit showed an instant up to 1.4 and 2.8 ms
from their own. Over the plan every one hit, exactly. An even cadence keeps
the old interval, and the pre-record's tolerance stays half a refresh.

Presents per second on synthetic refresh trains, after the first second, with
the Draws on time and then up to 1.5 ms late for their refreshes; the pacer's
column drives the macOS pacer with the link's display times
(`present_cap_test.go`, `frame_pacer_test.go`):

| Display | Cap | Before | After | After, 1.5 ms late | Pacer before → after |
|---|---|---|---|---|---|
| 144 Hz | 120 | 72.0 | 120.0 | 120.0 | 72.0 → 120.0 |
| 144 Hz | 60 | 48.0 | 60.0 | 60.0 | 48.0 → 60.0 |
| 144 Hz | 30 | 28.8 | 30.0 | 29.9 | 28.8 → 30.0 |
| 165 Hz | 120 | 82.5 | 120.0 | 120.0 | 82.5 → 120.0 |
| 165 Hz | 60 | 55.0 | 60.0 | 60.0 | 55.0 → 60.0 |
| 165 Hz | 30 | 33.0 | 30.0 | 30.0 | 33.0 → 30.0 |
| 100 Hz | 60 | 50.0 | 60.0 | 60.0 | 50.0 → 60.0 |
| 100 Hz | 30 | 33.3 | 30.0 | 30.0 | 33.3 → 30.0 |
| 75 Hz | 60 | 37.5 | 60.0 | 60.0 | 37.5 → 60.0 |
| 75 Hz | 30 | 25.0 | 30.0 | 30.0 | 25.0 → 30.0 |
| 240 Hz | 30 | 32.0 | 30.0 | 30.0 | 30.0 → 30.0 |

At 120 Hz under caps of 120, 60 and 30 and at 60 Hz under 60 and 30, every
Draw was decided as before with the Draws up to 1.5 ms late, and at 240 Hz
under a 60 cap with them up to 0.5 ms late. Late by a third of a refresh or
more, the measured refresh can wobble out of the thirty-second, and some
Draws are then decided by the schedule instead, at about the same rate.

Before the schedule, the 144 Hz train with its Draws up to 1.5 ms late
presented 82 times a second under the 120 cap, some presents falling a single
refresh apart, beside a median spacing of two refreshes — close to the
second tester's overlay, 83 frames a second at a median of 13.7 ms, whose
display's rate was not reported. The first tester's 24 under a 30 cap is below
the train's 28.8. Draw arrivals on Windows were not measured, and no Windows
window has run the schedule.

**Present slots — Nanolathe host presentation policy (macOS).** Ebitengine
presents whenever it is given a drawable, whether or not the Draw drew: a Draw
the cap skipped puts the retained screen on the display again. Under a 60 cap
on a 120 Hz panel the window therefore presented 120 times a second to show 60
frames. macOS shows a drawable two refreshes after it is presented and the
layer has three, so at one present per refresh the queue has no refresh to
spare. In fullscreen, where the display scans the drawables out directly,
anything that then occupied the window server — the pointer moving, another
application drawing in the background — put a present on the display a
refresh after its own, the presents behind it followed, and with all three
drawables held the display link missed a refresh as well. Timed at the display
by Instruments in a traced fullscreen battle, 5.6% of presents were shown late
with the pointer still and 57% with it moving, and the frames that carried
content reached the display at the wrong interval 5 and 36 times a second.
The window's own trace saw a fraction of that, since a present that is late
at the display was still made on time. Presenting only the frames that carry
content, with every other refresh left free, none was late in either case.

`framePacer` therefore chooses the refreshes the window presents at. Where
the display link reports to a delegate (macOS 14 and later), the pacer's
delegate is installed in front of Ebitengine's through the link's own
`setDelegate:`. It passes on the refreshes the present schedule chooses,
counted on the link's own display times, and keeps the others, so Ebitengine
runs one frame per presented frame and every Draw presents; `presentDue` is the cap elsewhere,
and when the display is no faster than the cap. Slots are spaced from the
display time of the slot before, so a late frame does not move the ones after
it. The refresh period is the shortest recent spacing of the link's reports:
the link's own figures differ between a window scanned out and one
composited. The cap applies to Enhanced alone, so Original is not paced.

A frame samples its input and its clocks as it begins, so the pacer also
holds the loop at the top of each frame, in `Layout`, which Ebitengine calls
before it takes the frame's input. The frame begins at the refresh before its
slot, which is where a presented frame began before and leaves its latency
unchanged (25 ms from the frame's beginning to the display at a 60 cap). A
frame that overruns that refresh presents after its slot arrived and is shown
late, by a quarter of a 60 Hz frame at a time on a panel that can refresh
between two of its nominal refreshes. When four of the last 64 frames took
more than seven tenths of a refresh from their beginning to the end of their
Draw — the rest is allowed for Ebitengine's flush — frames begin one refresh
earlier, as far as the cap's interval allows; when all 64 took less than half
of one, they begin a refresh later again.
The loop is never held for a link that has stopped reporting, as a hidden
window's does, nor while a slot already passed on is waiting for a frame.

**Composited fullscreen — Nanolathe host presentation policy (macOS).** A
fullscreen window the display scans out directly is shown from a queue too
short for a display of 120 refreshes a second. At a cap of the display's own
rate, or with none, every refresh carries content and the pacer has nothing
to keep back: in the same fullscreen battle at 120 FPS, 15% of presents were
shown late, and frames reached the display at the wrong interval 34 times a
second with the pointer moving and 13 with it still. Under a paced cap the
queue has a refresh to spare, which was enough on a quiet host and not on one
in ordinary use, where four 60 cap runs showed the wrong interval between 1
and 9 times a second. A window the window server composites is shown from a
longer queue, which absorbs the same disturbances, a refresh or two later.

`nativeScanout` therefore composites the window while it is fullscreen on a
display of 100 refreshes a second or more, under every cap and both
executors. The display scans out a fullscreen window directly only when its
layer is declared opaque, so the declaration is withdrawn for that time and
restored after it. A route is changed once four host steps in a row have
asked for it, since a fullscreen transition can report either state for a
step or two, and the display's period is the last a full set of the link's
spacings gave.

A layer that is not opaque is blended over the window behind it, and
Ebitengine clears the border around the game's picture to transparent black.
The window's background colour is therefore black while the layer is blended,
and its own again afterwards, so the border and the picture are the same on
either route.

Composited, in the same battle with the pointer moving, no present was late,
and frames reached the display at the wrong interval 0.1 times a second or
less at 120 FPS and 0.25 times or less at 60 and 30; with the pointer still,
about 0.5 times, most of those beside a frame the loop itself delivered late. Of
Original's presents 31% were shown late on the direct route and none
composited; its content cadence is unmeasured, since the live trace is
Enhanced's. The price is in latency, from a frame's beginning to the display:

| Cap | Direct | Composited |
|---|---|---|
| 120 FPS | 24 ms | 41 ms |
| 60 FPS | 25 ms | 33 ms |

The composited route depends on the window server composing at the
display's rate, which it does not always do. In one 120 FPS run it composed
about 93 times a second for twelve seconds of thirty while the loop presented
120, and a quarter of the frames were replaced before they were shown; six
other composited runs at 120 FPS whose loop was on time lost none. The
presents of that stretch came 0.9 ms after the refresh where they usually
come 0.2 ms after it, but a present that late is not what loses a frame:
handed their drawables 1.5 ms after the refresh, the frames of later runs
were all shown, and at the refresh they would have been shown at. What
occupied the window server is not in the trace, which profiles the game
alone; a trace of every process over such a stretch would show it. Neither
route helps a loop that other work on the host has starved: with another
session's tests running, frames were late at the loop itself.

A display of 60 refreshes a second keeps the direct route. With the panel
set to 60, on the same host in ordinary use, the direct route showed no
present late and the wrong interval 0.1 times a second; the composited route
showed the same a refresh later, 66 ms against 49 ms. Nothing between 60 and
120 refreshes a second was measured (`pacerFastDisplay`). Windowed play is
composited by the window server already. Before macOS 14, and with
`--live-unpaced`, the window is left as Ebitengine made it.

The default cap is 60 FPS. The Nanolathe options page offers 30 / 60 / 120 and
previews changes immediately; OK persists them, Cancel restores the entry value.
Explicit `--fps` overrides the saved preference at window startup, with zero
retaining display-refresh presentation. Captures and benchmarks use their
command-line settings independently of saved window preferences.

**FPS counter and frame graph — Nanolathe host presentation policy.** `+fps`
toggles a diagnostic overlay at the upper-right of the modern battle surface.
It starts off and retains its state across battles in the same process, without
changing saved settings. The host hides it when post-battle presentation begins,
so the title and score screens stay clear without clearing the
choice for the next battle. Its FPS number comes from the median presented-frame
interval over the most recent 500 ms, so cap-skipped Draw callbacks do not
inflate it and an isolated stall does not make the readout flicker. A
presented frame is timed by the refresh it belongs to, its Draw's arrival,
not by when its Draw returned: the work inside a Draw varies (a host step in
its tail, a synchronous record), and timing completions counted that variation
as late frames — in one traced game 426 of 433 such "late" frames reached the
display on their refresh.
The graph retains up to 30 seconds of presented-frame intervals and phase
durations in a fixed 8,192-sample ring (30 seconds through 273 FPS; faster
displays retain a shorter span). Its six aligned lanes show frame cadence,
whole Draw callback time, client/authoritative step time, committed-frame
interpolation, frame recording (including that interpolation), and renderer
`Execute` time (CPU preparation and GPU command enqueue). Each horizontal
pixel is an equal slice of 30 seconds and keeps the worst time in that slice,
so brief spikes stay visible at their approximate time. Lane labels show a
500 ms median and the 30-second peak. Frame and Draw medians include every
recent sample; Sim, Blend, Record and Submit medians include only frames where
that phase ran. With no such phase sample in 500 ms its live value is zero.
The graph, peak and late count remain unsmoothed. Cadence bars turn orange when
a frame missed a refresh: its interval exceeds the spacing the present
schedule meant it to have by half a refresh. While the cadence is even that
spacing is the cap or the display refresh, the longer, the rule the live
trace report uses. Where the refresh does not divide the cap it is the
spacing the schedule planned for that frame, a whole number of refreshes:
against the cap alone every frame a 120 cap holds for two refreshes at
144 Hz, one in five, counted late though it was on time. The live trace
records the plan (`plan_us`); its report counts late frames by the same rule
and its flight recorder measures spikes from the same spacing
(docs/BATTLE_BENCHMARK.md "Live window trace"). The overlay reports how many
intervals did so, the 30-second cadence peak and its amount over the target,
and the render passes the last presented frame issued (`ModelStats.Passes`):
on the development machine's Metal driver a frame past about 80 passes makes
the windowed present wait for the GPU, so that number is watched against the
§22 budget.
Changing the live FPS cap starts a fresh history, so the
graph and the late count describe one cap. A line in each lane marks the cap
interval. The Draw "room" is cap interval minus the median callback time and may be negative.
It is a partial wall-time margin: the sim step may run in Update or the Draw
tail, and an asynchronous pre-record can overlap other work, so lanes are never
added.
Paused foreground redraws report their own recording and renderer calls; Blend
is zero while the world is paused. Draw includes the overlay itself, so the
diagnostic has some cost. Draw excludes later GPU completion, display swap and
pacing; the interval includes those effects and scheduling but cannot
attribute a stall to one of them. Ebitengine does not expose a GPU execution
timer here, so `Submit` must not be read as GPU execution time. With `--fps=0`,
the overlay omits cap and room because the display refresh is not an explicit
budget.
Classic shows no counter. The overlay is applied after modern replay, outside
the recorded draw list and authoritative session; it has no RNG or resource
effects. It is absent from F11's GPU-image capture.

**Pointer latency — Nanolathe host presentation policy.** Modern positions the
recorded software cursor immediately before GPU replay, after joining the
recorder (`PositionPresentationCursor`), including the paused foreground path.
The adapter reads Ebitengine's newest logical cursor position, now refreshed
before every Draw on every platform. Ebitengine owns window coordinates,
letterboxing, DPI, capture and VM input injection; no native cursor bridge is
needed. Original retains its 30 Hz cursor. The cursor's shape and animation, hover, orders,
placement previews and camera continue using the ordinary host step; the fresh
position never publishes command input or consumes RNG. A cursor recorded in
the same list as the build placement ghost is pinned (`PinCursorToRecord`):
`cursorfindsite` over a legal site and `cursortoofar` over an illegal one keep
their recorded host-step position, because the ghost was snapped from that
pointer and moving only the cursor would carry it up to a couple of cells
ahead of the ghost while the mouse sweeps. While the ghost is shown the pointer
therefore moves at the 30 Hz host step, as in Original. The pin lasts one
recording, so every other cursor — and the placement cursor over the minimap,
where no ghost is drawn [07 §9] — keeps the fresh position. Retail draws its
cursor from a fresh OS position on its own 30 Hz thread [01 R-PLAT-01 §4]
[03 R-FX-01 §5], independently of the pointer record that places the ghost
[07 §9], so this pairing is host presentation policy, not a retail rule. The
GAF hotspot remains authored [07 §8]. Capture keeps the pointer hidden; the
release frame preserves its saved restore point [07 R-CAM-01 §11]. F11 reads the submitted GPU image,
cursor included. This removes the 30 Hz positional sampling limit;
cursor presentation still shares the selected FPS cap and any rendering stalls.

**Input buffering.** Refresh snapshots accumulate between host steps. A host
step receives the latest pointer and held state plus press edges retained from
the interval, so a brief click or key press cannot disappear between steps.
Wheel and pan deltas sum; text and pinch events retain batch order and drain
once. Catch-up steps receive held state without repeating these one-shot inputs.
The sample is owned by its scheduled host step, including when the ledger
defers that step to a Draw tail. This is host input policy; it does not claim
native event chronology, and the existing T25 polling limitations remain.
Multiple transitions of one key/button in a host interval still coalesce.
Text batches are filtered using the modifiers accompanying each refresh sample,
so later Alt/Cmd input cannot discard earlier plain text or leak a Ctrl+V
companion character. Shortcut translation otherwise retains its existing host
order and interval modifier state. One residual is explicit in `heldModifier`:
the public API cannot distinguish a modifier release followed by a repress in
one refresh snapshot. Catch-up holds conservatively treat that modifier as
released until the next refresh, while the current interval retains its chord.

Verification covers refresh-independent host cadence, bounded stall recovery,
buffered input and deferred sample ownership, plus the existing client tests
for hotspot placement, unchanged command input and capture/release. Performance
checks compare matching renderer/FPS settings: input polling changes, but Draw
frequency and the present cap do not increase. `--stats` reports polls/s and
elapsed polling cost alongside presented frames and 30 Hz host bodies; polling
elapsed time is not a process CPU measurement. Process CPU is measured separately
in an ordinary window, since the live battle benchmark uses its own scheduler.

Prototype check (2026-09-22, local macOS display): matched idle-menu windows,
12-second warmup and 25-second process-CPU samples, showed 29.1% → 28.4% of one
core at a 60 FPS cap and 31.8% → 30.7% at 120 FPS (native cursor baseline →
portable polling). These short samples show no observed CPU increase, not a
proven speedup. Both retained approximately 30 host bodies/s; portable polling
ran near 120/s and took about 1.5 ms/s elapsed, with presentation near the
selected cap. This is a local measurement, not a Windows/Linux performance
guarantee; their window targets were cross-compiled but not run here.

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
jumped two ticks forward. The modern window, which runs the simulation on its
own goroutine, takes the fraction together with the pair it presents from one
presentation clock instead (§13.13 "Presentation"), which reduces to this rule
at the nominal speed.

**Budget sample — Nanolathe host policy.** Holding the pose is still a
visible hitch when it happens often. The host steps at 30 Hz of wall time and
reads the budget's scaled timebase, a 30 Hz count of the same milliseconds, at
whatever phase the two clocks have; near a unit boundary a millisecond of
jitter decides whether a step reads the old unit or the new one, so a raw
sample released no tick on one step and two on the next. The presentation
then held the pose for a tick and skipped the next: in 2 of 3 traced 30 s
battles, every few seconds while the phases stayed close, at 60 and at 120
FPS alike, with every frame on time. On the wall clock the controller
therefore locks the sample to the host step (`BattleController.stepScaled`):
each step reads one unit past the last; a step that finds the raw timebase
behind the lock holds it, and one that finds it two or more units ahead — the
first step, or a stall longer than the host clock replays — takes the raw
value. The budget's arithmetic, carry and 0..5 clamp [01 §4.2] are untouched;
only the phase of the sample moves, by less than one unit, and its long-run
rate is the host clock's, which is the wall clock's. The sample is read at the
step's **ideal instant** — when the window's host clock meant the step for
(`hostClock.stepDue`), carried with the step's queued input and handed over as
`client.HostStepDue` — not when its body runs. A modern window runs a body at a
Draw's tail, a refresh or two after that instant when a cap skips the Draw in
between or the display drops a refresh (§13.10), and read there a late body
crossed a unit boundary and released two ticks. Shots, films, replays and
tests inject their own millisecond source and read it raw. The live trace's
`released` column counts the ticks each step released.

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

**Held walk axes — Nanolathe Enhanced policy (user-authorized 2026-10-06).**
The renderer and regular unit viewer share `internal/poseblend`. A complete,
unattached ground/surface mobile observed moving between the committed pair
may retain four distinct values per piece transform axis. Each value retains
its first tick, so repeated values are holds, not new endpoints. An axis that
exhibits a two- or three-tick hold samples `current tick + fraction
− 3`, linearly between known values, with the existing fixed-point truncation
and shortest signed angle arc. This is a three-tick (100 ms at normal speed)
visual delay relative to the current tick, two ticks beyond ordinary adjacent
tick interpolation. It is a host preference, independent of gameplay mode,
not a change to COB sleeps or simulation execution.

Axes that change every tick without short holds retain ordinary adjacent-tick
interpolation: aiming, spinning, and gait axes do not select each other's
sampling cadence. Once a short hold is observed, its axis keeps the delayed
phase through later dense keys until a longer hold or an existing lifecycle
reset. Switching back at every dense key would make variable-cadence gaits jump
backward. On first activation, sampling cannot precede the held endpoint
that selected the policy, so a two-tick hold cannot rewind earlier continuous
motion while the delayed clock catches up. A hold longer than three ticks lets the delayed view reach the last
known value before another key, and that new key returns to the ordinary blend; this bounded first release
does not predict a future keyframe or promise to smooth every authored cadence.
The temporal classifier does not identify callback names: a weapon axis with
the same short held cadence can qualify too. Projectile origins, damage,
weapon timing and all authoritative piece transforms remain unchanged. Cargo
composition uses its parent's presented pose through the existing attachment
path. There is no name-based unit allowlist.

Histories belong to presentation identities and are cleared on movement stop,
carrier/mover/build-state change, capture, teleport, instance replacement,
rollback, missing publications, or switching presentation modes. Removed units
release their history. Only available committed samples are recorded; repeated
presentation and paused frames cannot advance it. Piece membership, visibility
and render flags are discrete boundaries. Continuous and held outputs own their
storage; no committed frame is changed. The recorder prepares histories before
its parallel blend, and speculative/repeated recording remains idempotent.

Original keeps its committed poses and no history. The viewer enables this
policy for Move with Enhanced selected; other actions keep their existing
sampling. Tests cover per-axis separation, wrap/truncation, bounded storage,
owned endpoints, lifetime boundaries, missing ticks, repeat/pause and parallel
recording. Retail Fido captures verify intermediate poses; both renderer battle
benchmarks and the unchanged simulation fingerprint locks gate landing.

**Late model projection — Nanolathe Enhanced policy (2026-10-06).**
During Enhanced geometry recording, transformed model-relative fixed-point
vertices retain their fractions through the Z mirror, height shear and record
scale: `x = floor(s × rx)`, `y = floor(s × (−rz − ry/2))`. The doubled raster
projects independently with `2s`; it never doubles already-rounded corners.
Cached and direct live lanes share this local projection and the existing
subject placement, including its half-pixel offset. Extents and outlines use
the same coordinates. Height keys, nanoframe bands, waterline decisions and
structure shadow shear remain unchanged; mobile silhouette shadows follow the
body. Original software composition and the classic `RecordModel` reference retain
[03 R-RAST-01 §2]'s earlier rounding; modern-only `RecordGeometry` previews
inherit Enhanced projection at their selected record scale. This prevents that rounding from throwing
away the intermediate motion supplied by the pose smoother.

**Never interpolated.** Sprite and animation frame indices, the nanoframe reveal
band, damage flashes, palette rows, fog, visibility, selection, the cursor and
the HUD. A piece hidden in either tick is drawn as the current tick says. A COB
`turn` with no speed normally sweeps over one tick; a qualifying held walk axis
uses the longer presentation span described above.

**Benchmark.** `--benchmark-tps=120` runs one authoritative step every fourth
Draw and presents the four frames at fractions 0, ¼, ½ and ¾, so the report
measures the interpolated presentation; 30 and 60 keep one step per Draw.

**I6.** Amended for Enhanced only: the presentation may read the two most recent
committed ticks and the clock's carry, and retain bounded copied walk-axis history; it writes nothing back and consumes no
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
and a shared cursor balances better than a fixed stripe. A dispatch wakes only
the workers its work warrants — one per 32 jobs (`recordUnitsPerHelper`),
one per chunk past the first for the blend below — because every wake is a scheduler signal and a thread that spins
looking for work: a settings-screen preview with a few dozen units woke the
whole pool several times a frame for work the recording goroutine finishes
alone. Which participant takes a job never reaches the output.

**The blend rides the same pool.** The interpolated view of §13.5 blends every
unit of the battle, not only the ones in view, and each unit's blend reads its
own two views and writes its own slot and piece buffer. The recorder lends the
pool to the blend (`forEach`, 64-unit chunks from a shared cursor, sequential
below 256 units), so the 0.35–0.45 ms it took on the recording goroutine at
450–1,600 units is spread across the participants; the result is the
sequential blend's exactly, and a steady-state pooled blend allocates nothing.

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
allocates nothing per unit. The recording goroutine drains through a copy of its
own as well, so the recording client is not written from the first wake to the
last worker's return, and each worker takes its copy after its wake, in
parallel: the recording goroutine used to make all of them before the first
wake, about 0.15 ms of a 1,600-unit frame spent before any worker could start. Writes a worker makes to its own copy are dropped,
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

**Deferred pieces and retained lanes by reference.** A job builds only the
pieces its frame reads: `render.BuildUnitDrawDeferredInto` composes every
piece's transform but materializes a lane's world vertices on first read
(`UnitDraw.Materialize`), so a frame that only rebases a retained cached lane
builds the live pieces alone; a rebuild, a nanoframe and a structure-shadow
reprojection still build every piece. The rebase itself hands the packet the
retained faces rather than a copy when the offset is zero, and otherwise reuses
a rebased copy kept on the store while the box and half-pixel offset hold; a
rebuilt doubled lane is stored with its frame's half-pixel offset already
applied. This is safe because a recorded list is dead once the next frame starts
recording — the pipeline of §13.10 starts a pre-record only after `Execute` has
consumed the previous list — and a per-frame stamp on each retained store makes a
second write inside the same frame take fresh storage instead of moving faces a
packet of that frame already addresses.

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
goroutine joins that record before it touches client state again — in
Update before scheduled host work, and at the common entry to the next Draw, for both
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

- **the host's mutation epoch**, bumped once per scheduled host body and once per
  benchmark step, which is every point where the host writes client state: input
  and its selection, hover, command page, minimap viewport, pointer capture and
  focus; the simulation step and its publication; the camera the scroll pass and
  the follow glide moved; the executor toggle;
- **the committed frame**, by pointer identity and tick, as a cross-check on the
  epoch;
- **the two blend fractions of §13.5** and whether the camera's is set at all;
- **the camera origin, chrome insets and its two stepped samples**, the surface
  size, and the interpolation and Enhanced switches;
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
The window passes **half a display refresh**, computed per Draw as
`refresh / 2 × 30 × 65536` quanta from the refresh `presentDue` measures (the
`--fps` cap stands in before one is measured, and the last launch's own period
only without either). A frame that hitched measures a long period, so the
tolerance is deliberately not the interval this particular prediction was
extrapolated over, which would widen it by exactly the lateness it exists to
catch. So the window presents a matching list **at the fractions it was
recorded for**, and the pipeline's one presentation divergence is stated as
what it is: a pre-recorded frame is presented at the instant it was predicted
for, and the error is bounded by present jitter and capped at half a refresh.
A Draw a whole refresh late misses and records the frame it will show.

The tolerance used to be one nominal present interval, the cap or the
display's interval when that was wider. Under a cap slower than the display
that is two refreshes or more, so a Draw one refresh late still matched and
showed the world a refresh behind; the next frame made up the difference. In a
traced fullscreen session on a 120 Hz panel under the default 60 cap every one
of 96 presents that the panel held for three refreshes was such a match: the
world stood still for a third of the frame and leapt as far on the next.

**The host body runs in the Draw's idle window.** A pre-record can only serve a
frame if every client write that frame reads happened before the launch, and the
launch is at the end of the *previous* Draw. Refresh-rate input polling writes
only the adapter's buffer, so it may run while the recorder reads the client.
When the host clock issues a 30 Hz step, the adapter joins the recorder and
queues that step's input. The body normally runs at **the end of the modern
Draw**, after `Execute` and before the next launch. An `updateLedger` counts
scheduled host steps and guarantees one body per step: it defers when the modern executor's Draw tail is alive and
nothing is owed already, and otherwise runs every owed body inline, so the
simulation can neither step twice for one update period nor skip one however the
window behaves. The classic executor never defers; a Draw skipped by `--fps`
runs no tail, and the next scheduled host step runs the owed bodies inline. An exit request
seen from a tail cannot return a Termination, so it is recorded and the next
Update call returns it.

This deferral costs one presented frame of latency for host-dependent content.
A list recorded during the previous frame's flush cannot contain later input.
The cursor position alone is replaced immediately before replay using that
frame's fresh snapshot. The blend absorbs the host-body shift: the frame that used to present the new
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
viewport and map extents, chrome insets, scale and smooth zoom factor, dimensions,
world/asset binding revision, terrain, detail art, font, palette and display colours, the
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

**Measured and rejected (live window, 1,600 units, 120 FPS cap; 2026-09-26).**
Timed with the live window trace (docs/BATTLE_BENCHMARK.md "Live window
trace"); keep these out unless something below them changes:

- *Placing the model lane on the pipeline goroutine.* Running the renderer's
  CPU placement (§22) right after the pre-record, so Execute only submitted,
  cut Execute from 4.2 to 1.75 ms but put record and placement (5.4 ms, p90
  6.6) in series inside the loop: Execute N must finish before record N+1
  starts, and Execute N+1 needs both. The cycle grew past 8.33 ms and late
  frames rose from 6-17 to 60-82 a minute. Placement is instead parallelized
  inside Execute (§22).
- *A smaller §13.9 record pool.* Six or four participants instead of NumCPU
  lengthened the pre-record and its joins and lowered the on-time share.
- *A wider fraction tolerance after a hitch.* Two nominal intervals instead of
  one removed most synchronous re-records after a late frame (16 to 3 in three
  minutes) but did not reduce late frames; the re-record is rarely what makes
  the following frame late.

**What bounds the loop now (2026-09-26).** At 1,600 units the game goroutine's
`Execute` is about 4 ms and the pre-record about 3.2 ms, and the pre-record may
only start once `Execute` has consumed the list it would overwrite: the two run
in series around the render thread's flush, so a frame whose recording or
execution grows by a millisecond with the content in view is late. Running the
pre-record beside `Execute` needs the list, its arenas and every retained
store's rebased faces double-buffered, and the post-`Execute` host calls moved
behind the join; that is the next structural step. The pre-record's own largest
item is stage one (§13.9): about 11 ms of CPU a frame across ~7.5 effective
participants on a six-performance-core machine, most of it re-projecting the
cached lanes of units whose interpolated heading, pitch or bank moved since the
last presented frame (240 to 450 a frame in a 1,600-unit battle once the armies
meet).

**The browser host does not pre-record.** On js/wasm there is one thread and no
render thread: the pre-record ran inside the same browser task as the Draw that
launched it, so it hid nothing, and every predicted miss paid for the frame
twice. The browser records each frame synchronously in its Draw instead
(DESIGN_BROWSER_HOST §5); the ledger, digest and executor are unchanged.

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
  map rectangle in screen space and no executor needs a callback. Both readings
  are in RECORD pixels: the camera origin and the map's extent go through the
  record step's projection (§14.1), so at the detail step a map pixel is a 2×2
  block. Measured in world pixels, as the gate first was, its far edges sat
  halfway to the map's right and bottom edges, and every disc past that line
  was clipped — a straight cut through explosion light in a 2× view of that
  part of the map. A test locks both readings to the camera's projection at
  both record steps.
* `drawlist.Halo` carries the centre, the radius already taken through the view
  scale, the LHT row and the same rectangle.

Both are part of the `Sink` contract rather than an optional hook, because
unlike the trail marks every executor must draw them. The **classic recording
lane is unchanged**: it still emits the points, so classic output is
byte-identical by construction. The classic *sink* implements the two commands
by expanding them back into exactly those points — `Flash.Expand` and
`Halo.Expand` are the definition, and a test locks their output against the
points the classic lane records at both view scales — so a modern-lane list
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
exactly — and both were measured byte-identical. In the historical 1.5×
comparison, the loop's alternating one- and two-wide columns became
nearest-sample columns, which shifted some ramp columns by a pixel; every differing pixel lies inside a disc
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
  the same retained faces. A structure shadow whose reprojection comes out
  identical to the retained one keeps its revision (about three quarters of the
  coastal battle's reprojections), so the executor can replay it.
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
until an axis moves more than seven units, while the structure shadow projects
visible **cached** pieces from the **current** pose, including during
construction [03 R-REN-03D §2]. The two lanes remain retained side by side
and gated apart.

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

### 13.13 The asynchronous simulation

**Why.** Before this, the window ran a host step's sub-ticks inside the Draw
tail that deferred it (§13.10), so every other presented frame at a 60 cap
carried a whole tick besides recording, `Execute` and Ebitengine's present,
and every simulation spike became a dropped frame. Timed through the real
window at a Survival capture on Moon Quartet (1280x827, 120 Hz ProMotion
panel, 60 cap), the tick cost 4.0-4.3 ms per stepping frame at 803 units and
6.5-7.7 ms at about 1,600, with spikes past 20 ms. The modern window now runs
the sub-ticks on their own goroutine. A tick then has a whole host period,
about 33 ms at 1x, on a core of its own, and a presented frame carries only
presentation.

**Scope.** The modern (Enhanced) window only. Original presents the committed
tick after each update and stays on the synchronous path, as do `--shot`,
film, the benchmarks and headless runs; nothing but the window sets
`Client.SetAsyncSimulation`. Switching to Original (F10 or the saved
preference) calls the client's `JoinSimulation` hook, which joins and stops the
goroutine before presentation reads the committed buffer unpinned again.

**The order — Nanolathe host policy.** Every decision stays on the game
goroutine, in the order the synchronous step made it (`cmd/nanolathe/battle_sim.go`):

1. At the start of a host step (`gameShell.step`) the host joins the batch the
   previous step launched. With the session quiescent it then applies what the
   batch left for the client — the captions its audio inserts resolved and the
   executor tail's message-ring retire [01 R-PLAT-02 §8], in that order, and
   the feature definitions it admitted to the model texture registry — feeds
   each publication the batch made, oldest first, to the observers the
   synchronous batch ran inline — the published camera (follow and shake) and
   the Enhanced history layers (`Client.ObserveCommittedFrame`) — runs the
   follow hotkeys retail handles after the sub-tick batch, and drains audio.
2. Input is handled and human commands are queued exactly as before.
   `Session.PrepareStep` runs the session's state dispatch and the tick budget
   [01 §4.2] [01 §4.3] and stamps the release.
3. When the host step returns, `Session.ExecuteStep` runs the released
   sub-ticks, their publications and the executor tail
   [01 §4.4] [01 R-PLAT-02 §7] on the simulation goroutine, until the next
   host step joins it. The one exception is a pump with a gameplay switch
   queued (`HumanGameplay`): it rebinds rule state the HUD reads while it draws,
   so that pump runs inside the host step instead.

`Session.Step` is exactly `ExecuteStep(PrepareStep(scaled))`, commands reach the
same sub-ticks, and the authoritative sequence is unchanged; only where the
sub-ticks run moves. The retail test `TestAsynchronousSimulationMatchesSynchronous`
steps one seeded skirmish through the host step both ways, with a human order
and a commander self-destruct mid-run, and requires the same partial-state
fingerprint, global tick and both random-stream states. Each step also pins,
drains, digests and records a modern frame as the window's Draw does, beside
the running batch in the asynchronous run, so under `-race` the test is the
shared-state check below.

**Presentation — Nanolathe host policy.** The window presents one continuous
world time `T`, in ticks, that trails the tick budget's own clock by a lag
(`cmd/nanolathe/battle_present.go`):

    T(t) = F + carry + (t − t_F) × rate − lag − margin × rate

`F + carry` is the global tick after the last prepared host step's release plus
the budget's remainder [01 §4.2], rebased at every step so a speed change takes
effect from the step it does; `t_F` is that step's ideal instant (the budget
sample's, §13.5), below the millisecond; `rate` is 30 × the effective speed
ticks per second. A frame shows
the tick after `floor(T)` blended from the one before it at `T`'s fraction, and
never a tick the host has not joined: a late host step holds the newest joined
tick until its join. One call (`Options.PresentationTick`) names the pair and
the fraction together, so both always describe the same instant.

The lag is the smallest that keeps `T` at or below the newest joined tick.
A step joins the batch the previous step released, so during a step that
released K ticks the newest presentable tick is `F − K`, while the budget clock
runs from `F + carry` to `F + carry + k`, `k` being the ticks it accrues per step
(active ÷ 10). That holds when `lag ≥ 2k +` the carry the previous step kept,
and a speed's carries are multiples of gcd(active, 10) ÷ 10, so
`lag = 2k + 1 − gcd(active, 10) ÷ 10`: two ticks at 1x, with no carry — the pair
the window has always presented, the tick before the last release blended at
the §13.5 fraction — and four at 2x. Measured from one tick instead, as the
blend was until 2026-09-26, a step that released two ticks presented only the
second: at 2x the world eased across the older tick in half a step, held for the
other half and leapt the tick it never showed, a third of a tick and then one
and two-thirds, frame after frame (every speed above 1x did a version of it; a
traced 2x battle measured 229 world motion errors over 8 ms a minute, and 22
with the clock, the rest at late presents).

`T` is rebased at the step's ideal instant rather than when its body runs, so
it is a function of wall time alone. A body runs anything from a few
milliseconds to a refresh or two after its instant, and that lateness changes
from step to step: on a 120 Hz panel under the default 60 cap a body runs at
the tail of the Draw its step landed before, or one refresh later when that
Draw is one the cap skips, and a refresh the display drops moves the steps from
one to the other. Rebased at the body, each change moved the world by the
difference — it held for a refresh and then leapt one, again and again while
the panel dropped refreshes (a traced fullscreen session: 142 of 155 on-time
frames with a world motion error over 4 ms sat beside such a step). What the
lateness still decides is when the batch `T` needs is joined, so `margin` holds
`T` back by the lateness recently seen: a body later than the margin raises it
at once — the one hold that lateness costs — and the margin gives it back at
a refresh's worth every two seconds, the world running under one percent fast
meanwhile. It is capped at one 60 Hz refresh: anything later is a stall, which
holds the world at the newest joined tick until its join and must not leave it
a stall behind for seconds afterwards. Replayed against that session's timing
the clock's motion errors over 4 ms fell from 232 to 49 and over 8 ms from 143
to 13 in two minutes, for 3 ms more latency on average — the lateness the
body-rebased clock already carried, now held steady.

A speed-up that needs a longer lag holds the presented world still until the
clock reaches it again (the clock never presents earlier than the last Draw
did), a hold of the added lag — 33 ms from 1x to 2x — rather than replayed
ticks; a slow-down eases the lag back at three ticks a second instead of
skipping ticks. A pause returns the last presented sample, so the blended pose
stays where it was; a command applied at the paused-input boundary republishes
the committed tick, and that republication is presented at once, unblended
(DESIGN_INTERFACE_HUD_INPUT §3.12). A terminal publication is presented at once
after its join. A pre-record samples the same clock at the instant it predicts
(`StartPreRecordAt`), so the pair it pins is the one the next Draw names even
when the named tick moves on between the two, as it does part way through a
step above 1x (§13.10). Should a named tick ever have left the buffer, the
newest publication is presented unblended rather than waited for. Everything
the join applies is therefore in place before a publication is drawn. Unit
motion reaches the screen `lag` ticks behind the budget — two host steps at 1x
and 2x, a little more at speeds between; the camera, cursor, selection box and
interface are presentation state and do not move.

**The committed buffer.** `frame.Buffer` widens from two slots to an eight-slot
rotation (`SetConcurrentReaders`). A reader beside the writer pins what it
reads (`PinTick`, `PinLatest`), and the writer refills only the oldest slot that
is neither pinned nor committed, so a recording pass reads one publication
throughout. The host pins the pair a presented frame shows at the start of its
Draw (`Client.PinPresentation`), and `StartPreRecord` pins the pair its
pre-record will read before waking the worker, so the pipeline's digest and the
record name the same publication (§13.10). `PublicationsSince` feeds the
host's in-order observation. The two-slot default keeps synchronous routes at
their old footprint, and the unpinned `Current`/`Previous` rules are unchanged;
`Current` is an atomic load, since the synchronous recorders call it per drawn
object.

**Shared state.** Found by running the window under the race detector, or by
reading every presentation path the simulation reaches:

- The audio cue queue: the simulation inserts cues and presentation drains them,
  and the drain's resolver reads live units. Under the asynchronous simulation
  the drain runs in the host step with the simulation joined; queue pops were
  already gated to one per host step, so nothing is lost.
- The model texture registry: phase 7 advances its cursors while recording reads
  them, so a cursor's position and playing flag are atomic
  (`render.TexturePlayer`). Feature admissions write the registry's maps and
  wait for the join.
- Terrain heights: presentation samples heights while movement writes the
  occupant bytes of the same plot cells, so `PlotCell.Height` reads through the
  pointer instead of copying the cell.
- The Enhanced history layers (trails, scorch marks, hover wakes, water motion)
  are fed only by the in-order observation at the join; a recording pass places
  nothing itself.
- The message ring: recording reads it, and two simulation paths wrote it — the
  executor tail's retire and the caption a full audio queue's silent resolve
  posts. The batch notes the tail's tick and the client holds those captions
  (`Client.DeferCaptions`); the join applies both, so only the game goroutine
  writes the ring.
- The effect-bank cache: the session's effect-timing resolver loads banks on
  its first miss, from the simulation goroutine, while recording resolves art
  from the same cache, so the cache and the art diagnostics its misses record
  are locked.
- The HUD's world overlays (build ghost, drag previews, the nanoframe preview)
  are drawn inside the recording pass, so they read the presented publication
  (`Client.PresentedFrame`) and take the local player from it, not from the
  live session.

**Threads — Nanolathe host policy.** On macOS the main (render) thread, the game
loop, the simulation and the pre-record goroutine lock their threads and ask for
the user-interactive quality-of-service class
(`ebitenapp.RaiseCurrentThread`), which keeps them on performance cores while
other processes load the machine. The record pool's workers keep the default
class. Other hosts keep their default scheduling.

**Measured.** Interleaved runs of one binary with the simulation synchronous
and asynchronous, at the capture above:

| | synchronous | asynchronous |
|---|---|---|
| 803 units: host step on the critical path, mean | 4.0-4.3 ms | 0.4 ms |
| 803 units: Draw callback work, p95 / p99 / max | 10.1-10.6 / 10.8-11.5 / 28-34 ms | 6.2-6.5 / 6.6-7.4 / 13-14 ms |
| 803 units: presented per second | 59.6-59.9 | 59.7-59.9 |
| ~1,600 units: presented per second | 34.2-38.8 | 43.3-46.6 |

At about 1,600 units the frame is then bounded by Ebitengine's present waiting
on the GPU (13-15 ms), not by the tick. The frame graph's Sim lane adds the
batch's time on its goroutine to the host step's.

## 14. The detail view: native and 2× steps and load-time remaster

### 14.1 Decision

The record scale has two steps: 1× and 2×. `camera.Scale` [F-P1-008] is a
`camera.ViewScale`: `ViewScaleNative` (2) and `ViewScaleDetail` (4), with zero
reading as native. The encoding retains its denominator of two. Classic uses
these two views; modern adds a free live factor above this projection (§16).
Retail has one scale; the magnified view is Nanolathe presentation policy.
The projection is integer at each record step:

    screenX = Project(worldX − camX) + originX
    screenY = Project(worldZ − (worldY >> 1) − camZ) + originY
    Project(v) = v · (s / 2)

Here `s` is the encoded step, 2 or 4. The inverse used for picking is
`world = cam + floor(2·(screen − origin) / s)`: every screen pixel names one
world pixel, and every world pixel projects to the first screen pixel that
picks it. A 2× asset lands on the pixel grid one-to-one and a 1× asset doubled
by nearest sampling lands on the same grid. `Project` scales a position;
`Px` scales an extent or authored offset by the same exact integer multiply;
`Inverse` converts a picked pixel. The type is distinct from `int32` so that a plain
multiply against a pixel count does not compile.

### 14.2 The view transform — contract D1

Every world-space command is recorded in screen space by the recorder, and the
recorder is the one place the scale is applied. The executors replay recorded
coordinates directly at these steps; modern applies its additional live-factor
transform only when needed (§16). Both replay the same list at 1× and 2×, so
the parity gate of §6 applies at both steps. In the table below, `s` is the
linear magnification (1 or 2), rather than its encoded `ViewScale` value.

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

The laser's secondary stroke is offset after projecting its world endpoints:
its one-screen-pixel offset remains one pixel at both record scales, consistent
with the line-width policy above. Only the world endpoints scale
[06 R-WFX-01 §4].

Classic attached-unit staging uses the child-minus-carrier world projection at
native raster scale, then magnifies the completed union about the carrier anchor
once. It must not reuse the already magnified screen-anchor difference: the
completed union receives the scale once, including its child offsets. At 1×
this is the original staging placement [03 R-REN-03A §4].

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
cloud by nearly 64 pixels. Fill rectangles remain clipped. The producer expands
its viewport window by the authored leaf-frame bounds, including composite
children; neither executor rejects a GAF solely because its nominal cell is
offscreen. Regression fixtures
pan black, gray and dithered gray masks past both edges at 1× and 2× and
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

Only native and doubled art are retained. Modern fractional zoom samples its
recorded world through the live-factor transform (§16.3), without dedicated
fractional sprite, fog or terrain variants or caches.

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
* **Cache.** `$XDG_CACHE_HOME/nanolathe/upscale/<format version>/<key>`, defaulting
  to `~/.cache/nanolathe/upscale/<format version>/<key>` on every platform, in
  keeping with the settings and mod library layout. The key is a SHA-256 over
  the algorithm version and every input byte. One file per result has a magic
  and a version; a file that fails to parse is recomputed and rewritten. The
  cache is derived retail art and is never committed or shipped.
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

* **F9** toggles the view scale 1× ↔ 2× about the viewport centre
  (`ViewScale.Next`) in classic; modern's cycle is §16.8.
* **F10** toggles the executor between classic and modern. The client publishes
  the requested executor; the adapter switches at the next Update, turning
  interpolation and the synthesized art off when classic takes over (§14.3), and
  the retained screen bridges the swap. When classic takes over from modern,
  by F10 or the options page, the battle returns to native 1× about the
  viewport centre: classic cannot change or always present a modern free
  factor. Neither key is a retail binding. F10 also updates the shell preference and persists only the renderer field. The
  Nanolathe options page uses the same swap cleanup for live previews and Cancel
  restoration (DESIGN_INTERFACE_HUD_INPUT §3.4.1).
* `--zoom` sets the scale at battle entry and applies to captures too. Classic
  accepts only 1 or 2; modern accepts any factor in the free range (§16.8).
  Both renderers default to native 1× at every window resolution, including
  captures and the battle benchmark. A restart keeps the player's scale.
  `--shot-focus` stays. `--fps` is §13.5.

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

### 14.8 Loading-time terrain upload

Modern battle loading prepares both native and detail terrain atlases after
successful client binding, before returning to interactive presentation. The
client's `PrepareBattlePresentation` hook calls the window device owner; no GPU
calls run on the content loader goroutine. Fresh loads and save restoration use
the hook while the retained loading/front-end image is still on screen. Direct
map startup and a battle first entered with classic prepare on the first modern
draw instead. A failed load never invokes the hook.

`BattleTerrainSources` returns the installed immutable terrain and the same
complete detail provider the recorder would admit at 2×, without changing zoom,
recording a frame, advancing animation or consuming either random stream.
`Renderer.PrepareTerrain` calls the ordinary atlas cache for native with no
detail identity and detail with the provider identity (or nearest doubling).
The source-generation barrier retires the previous battle before preparation;
the first draw keeps those same pages. Teardown retires both scales through
`ResetSources`, and ordinary draws only check a readiness bit.

This is Nanolathe loading policy, not a retail claim. It moves CPU packing and
GPU upload submission earlier and retains the 2× atlas even when the player
never zooms. Its padded RGBA8 cells cost 66×66×4 bytes per detail tile, plus
unused cells at the end of each page; native remains 34×34×4 per tile. Device
allocation can add overhead beyond these source-image extents. Loading takes
longer; sustained rendering executes the same cached atlas path. Dynamic model
scratch and other first-use resources remain demand allocated; this change
does not promise every first-view cost is eliminated.

The same hook places the feature sprites a first sight of the map would draw.
`BattleSpriteFrames` lists, each once, every frame of the rest and shadow
sequences of the feature definitions the battle's terrain admits, resolved at
the current view scale exactly as the recorder resolves them, and
`Renderer.PrepareSprites` places them through the ordinary `sceneFrameFor`
cache. Placed on first use, a camera jump onto unseen ground packed and
uploaded dozens of sprites in one frame: a traced game spent 6 ms in Replay
there, and the render thread then blocked 43 ms in one texture upload.
Ebitengine sends one frame's writes to an image through a single staging
texture of their bounding box, so a burst scattered down a page costs the
region it spans; `Renderer.AtlasUploads` reports a frame's atlas uploads and
the largest such region for the live trace. Event sequences (burning, dying,
reclaim), unit corpses and the other view scale stay on first use: across a
catalog they are most of the art (61 megapixels on Town & Country against 2
for its rest frames, which place in about 8 ms) and appear a few at a time.

Verification checks exact cache identity with and without a provider, zero
allocation on the warm detail lookup, retirement of both scales, classic's
device-free loading, and lifecycle invalidation. Compare cold and prepared
first-detail submissions separately from repeated detail draws; upload
submission time is not a GPU completion timestamp (§6).

**Terrain filled on demand.** A renderer set with `SetSparseTerrain` builds
each tile atlas empty and packs a tile the first frame it is in view: before
the draw walks its pages, every visible tile without a cell takes the next
cell and uploads that one padded cell. Pages hold 32×32 cells (1,088 px
native, 2,112 px detail), capped by the device, allocated as cells are handed
out. A tile's cell is the only thing that differs from the complete atlas — the
cell's texels, padding and the draw's clipping and remainder are the same code
— so the pixels are identical (`checkSparseTerrainDevicePixels` walks a camera
over both scales against the complete atlas). The settings screen's previews
use it: their cameras hold still, so a scene packs and uploads the few hundred
tiles it shows instead of every tile of the map — 40–50 MB at the detail scale
on the preview maps, 200 MB on the largest — inside its first frame. Battles
keep the complete atlas `PrepareTerrain` uploads at loading, so a camera jump
mid-game never packs tiles. This is host presentation policy; no pixel changes.

## 15. Trails: Enhanced ground marks

Mobile ground units leave fading marks on the terrain: alternating footprints
for legged units, a pair of track segments for tracked ones. Retail leaves no
marks, so this is a Nanolathe presentation feature — on while the modern
executor presents, absent from Original, never a simulation input [I6]. The
marks are geometry, not art: an oval and a segment whose coverage the shader
evaluates, darkening whatever terrain is under them, so there is nothing to
author and the look is right on every tile set because the terrain's own colour
is what fades. The trail strength alone gates the layer: zero is off (§30).

### 15.2 Placement — contract T1

**Nanolathe presentation policy.** These marks are a modern-renderer effect,
independent of gameplay mode. Geometry supplies approximate contact dimensions;
committed travel supplies placement. None of the sizing or stride rules below
claims to reproduce retail footfalls. Retail provides the model hierarchy and
piece geometry [03 §2.4][fmt 3do]; the effect does not change simulation state,
RNG, resource consumption, or Strict 3.1 gameplay.

`internal/client/trails.go` retains at most 4,096 **individual quads** and one
tracker per unit, keyed by publication identity and pruned with the live unit
set. A track pair uses two ring slots; a footprint uses one. The oldest mark is
overwritten when full, so a busy scene can retire marks before their nominal
lifetime. Class is cached per definition and geometry per immutable loaded
model, including unsuccessful geometry lookups.

- **Class.** Aircraft and movement classes containing `HOVER` or beginning
  `BOAT` leave nothing. Fixed/flying editor classes remain excluded. `KBOT`
  and `COMMANDER` leave feet. `TANK` leaves feet if its model has a leg-named
  piece, otherwise tracks. This includes the stock spider, whose editor class
  is `TANK`. Other editor classes fall back to leg-name detection, then tracks.
  Leg-name clues are `leg`, `foot`, `thigh`, `knee`, `shin`, and `toe`.
- **Gate.** The unit must be mobile (BMcode), not a building, on the ground
  (mode mirror 1), complete, no more than two world pixels above the terrain
  under it, at or above sea level, and visible to the local player by the
  painter's own gate. A unit that fails the gate restarts its distance history
  where it next qualifies. A trail is memory of observed movement, not a sensor.
- **Foot geometry.** A leg assembly starts at its highest leg-named ancestor
  and includes its leg-named descendants. Compose their authored translations
  with no script pose. Take the assembly's lowest two world pixels, including
  polygon-edge intersections with the band ceiling, and bound those surfaces
  in X/Z. Selection polygons, unused vertices and primitive-free emit points
  are excluded. Combine sole and toe pieces in one assembly rather than
  counting them as separate feet. Average assembly widths, lengths and absolute
  X centres to obtain the representative print width, length and lateral
  spread. Width and length have a two-world-pixel visual minimum for pointed
  tips: a one-pixel oval at an integer centre can miss every native raster
  sample. This is a rest-geometry approximation, not a contact solver.
- **Vehicle geometry.** Conventional tanks need not have tread pieces; the
  base texture can depict them. Measure the root's actual surface width in X;
  if unavailable, try named `base`, `body`, or `chassis` pieces in model order.
  Turret/barrel children and selection faces cannot expand this width. Each
  track's full width is `bodyWidth × 3.5 / 32`. Its centre is one full track
  width inward from the body's side and its outer edge is half a track
  width inside the body edge. This allows for body overhang; visual review of
  Stumpy and Bulldog found body-edge placement too wide. Width and inset are
  presentation tuning retaining the old 3.5-pixel strip on a 32-pixel body,
  not a measured tread boundary.
- **Fallback.** If contact/body geometry is unavailable, retain the earlier
  8×4 print with lateral spread `2×max(FootX,1)`, or the 3.5-pixel track with
  spread `4×max(FootX,1)+2`. Footprint metadata never overrides usable geometry.
- **Stride.** Feet are laid every `max(10, measuredPrintLength)` world pixels;
  the ten-pixel floor bounds emission density for tiny leg tips and preserves
  the old small-walker cadence. Tracks remain eight pixels apart and eight
  pixels long so straight segments join. Feet still alternate between two
  sides: leg count, gait phase, individual wheel paths and turning tread motion
  are not modeled by this stage. The stride is an explicitly approximate size
  scale, not a measurement of foot swing or animation period. Several marks
  may be laid along a straight travel step. A distance above eight strides is
  treated as a relocation and bridges nothing.
- **Age.** A mark lives at most 300 committed ticks, fading linearly to zero.
  Ages are tick differences, never wall time.

### 15.3 Recording and drawing — contract T2

Each retained mark stores its final world-space contact centre, travel direction
and half dimensions at placement. A track pair becomes two marks immediately;
its count cannot expand during drawing. Project each centre at the terrain
height beneath that contact, cull using its scaled dimensions, and preserve
1/256-pixel precision in the two recorded screen-axis vectors; the executor
applies live fractional zoom afterwards (§16).
A later camera or model-cache change never changes the world dimensions of an
existing mark.

Record the whole frame as ONE `drawlist.Trails` batch between terrain and strip
0, so features, shadows, units and fog draw over it. The optional
`drawlist.TrailSink` hook leaves other sinks unchanged. The modern executor draws
the batch through the row families' scale blend (§13.3): each rotated quad's
fragment is `1 − strength × coverage`, with a soft oval for a footprint and a
soft-sided, hard-ended segment for a track. The tuned peak darkening is 0.4 for
feet and 0.3 for tracks. The player's `presentation.trailStrength` is a whole
percentage from 0 to 100, default 50; a mod may set independent `footprints`
and `tracks` percentages from 0 to 200 in `nanolathe/materials.tdf`'s
`[effects]` section. The two percentages multiply the tuned peak, so the stock
default is 0.2 for feet and 0.15 for tracks. Zero in either control hides that
trail family without changing scorch marks or simulation. The JSON preference
is decoded over defaults and clamped to 0..100; negative values restore 50.
Multiplies commute, so overlapping marks need no additional
phase ordering. `Client.ObserveCommittedTick` records every advanced capture
tick so a `--shot` observes the same history as the window.

### 15.4 Verification

`internal/client/trails_test.go` locks the classifier table, including the
spider and non-ground exclusions; a walker laying two
alternating footprints along its step; nothing on first sighting; nothing
re-recording the same tick; fading and expiry; nothing in Original; and no marks
for airborne, elevated or moved units.
`internal/client/trail_geometry_test.go` locks combined sole/toe bounds,
selection/emit exclusion, pointed-tip clipping, geometry-scaled stride and
quad dimensions, inset track placement, the actual-quad ring budget and
allocation-free warm lookup. Asset-gated checks compare stock walker sizes and
vehicle body widths.
`internal/platform/gpurender/trails_test.go` locks the optional-sink replay and,
on a device, that a full-strength footprint darkens its centre to near zero and
leaves the field untouched beyond its half-width and half-length, and that a
half-strength track halves the field along its whole length and not beside or
past it.

### 15.5 Animation-aware footfall experiment (2026-09-15)

**Decision:** ship the geometry-scaled distance placement above. Keep
animation-aware placement experimental until automatic contact selection and
calibration behave reliably across the model corpus. CPU cost alone does not
rule it out; a lift detector alone does not cover even these three walkers.

An independently authored probe replayed 360 committed poses each for Peewee,
Krogoth and Spider walking on Town & Country, seed 7. Contact assemblies were
explicitly selected: each Peewee foot, each Krogoth leg plus toe, and each
Spider leg. Proposed detection learned each assembly's minimum body-local
height over 60 samples, armed above that level plus two pixels, and emitted
once on return within one pixel. These are experimental presentation choices,
not authored contact flags or recovered retail footprint behavior.

In a subsequent 120-sample window, Peewee produced no lift events. Detecting
forward-to-back reversal of its stable foot-piece origin instead produced six
per foot, with a 19-tick same-foot period and alternating 9/10-tick events.
Krogoth produced two per foot with a 70-tick same-foot period; Spider produced
five per leg with a 24-tick period and some simultaneous plants. These are
observations of this probe, not constants to encode for the unit definitions.
They also demonstrate why foot swing distance cannot be equated with ground
stride and why an alternating pair cannot represent Spider's six-foot pattern.

Using the horizontal centroid of the currently lowest vertices for swing
reversal produced extra false Peewee events: the centroid changes abruptly as
sole corners rotate. Krogoth's walking minimum was also below its unanimated
resting sole. Thus a static rest-height threshold is insufficient, and a stable
reference is needed before interpreting apparent foot sliding. Spider's
candidate planted lateral offsets were approximately 14–24 pixels from the
centre, consistent in scale with the unposed geometry's representative spread.

A CPU-only replay benchmark on Apple M3 Pro, Go 1.25.0, used equal mixes of the
three models at staggered phases. Three one-second samples measured the
following **per tick over the entire population**, with reusable scratch:

| Work | 300 walkers | 600 walkers |
|---|---:|---:|
| Pose adaptation, contact transforms, vertex scan and plant detection (median) | 0.337 ms | 0.647 ms |
| Same work, observed range across three samples | 0.330–0.343 ms | 0.642–0.666 ms |
| Minimal squared-distance selector (median) | 0.0024 ms | 0.0048 ms |
| Warm allocations | 0 | 0 |

The distance comparator is deliberately minimal, not the full production
trail producer. Both measurements exclude visibility/terrain checks, mark
insertion, GPU recording and drawing. Cold preparation plus one pose took
about 2.3–3.7 microseconds and 5–10 KB per walker; loading assets and the
60-sample calibration were excluded. Calibration also introduces an observation
delay. These measurements establish a modest but measurable CPU cost, not a
whole-game frame-rate guarantee or GPU cost.

Before adopting this experiment: establish generic contact assemblies, choose
lift versus swing detection without unit-name switches, handle unrepresentative
calibration and posture changes, compose full orientation on slopes, and reset
history across visibility gaps and relocations. Stamp once at the plant event;
do not drag an existing print with a sliding animated foot. The current
geometry stage remains independent of those unresolved presentation choices.

## 16. Smooth zoom and the strategic view (modern)

### 16.1 Decision

The modern executor's view scale is a free factor. The **classic** executor
uses §14's native and detail steps. In the modern executor the factor is
continuous: the mouse wheel over the battle viewport moves it, it eases toward
its target on the
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
2×. It is the free generalization of `camera.ViewScale`; at the native and
detail factors (1024 and 2048), its `Project`, `Inverse` and `Px` agree exactly
with the matching record step. Fractional factors, including 1.5× (1536),
remain representable for explicit CLI zoom and smooth transitions.

`Camera` carries both. `Scale` is the **record step** the recorder projects at:
`WorldToScreen`, the terrain record, the detail-art selection and the fog op
builder read it and are unchanged. `Zoom` is the **live factor** the player sees:
`EffectiveView`, `clampInsets`, `BattleView`, `Drag`, `ScreenToWorld` and the
minimap's viewport rectangle read it, because they measure the view in world
pixels and the view is what is on screen. A zero `Zoom` reads as the step's own
factor, which is the classic executor's permanent state.

The record step follows the factor for the modern executor (`Zoom.Step`): the 2×
step above 1×, the native step at or below it. The classic executor does not
derive its step from a free factor: a factor handed to it is native or detail
and is set through `SetScaleAbout`, which writes the step and the factor
together (`ViewScaleForZoom` names the step).

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

**World text.** FNT runs follow the unscaled-glyph contract of §14.2. The
executor transforms only the recorded baseline anchor, rounds it to the nearest
framebuffer pixel, then adds `Glyphs.ScreenOffsetX/Y` in screen pixels. Counter
centering, vertical line spacing and the eight one-pixel outline stamps use
these offsets, so fractional zoom cannot collapse the glyph strokes or outline.
An explicit clip follows the world transform; text admission and baseline
storage clipping then use framebuffer bounds. The glyph quads enter the same
schedule with its transform held off for that command, preserving strip/fog
order without adding a submission. The region's transform is restored for the
next world command. Native and detail rest views preserve their previous text
pixels. These are presentation rules; no committed state or RNG changes.

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
exact. The fog atlas stays nearest.

**Fractional-zoom filtering (2026-09-30).** Between 1× and 2×,
keyed and tinted world sprites resolve each of four texels through PAL and
then interpolate premultiplied colour and coverage. A transparent texel
contributes zero colour and zero coverage, preventing colour-key fringes.
Model body commits, with Supersample enabled, interpolate four neighbouring
resolved 2×2 coverage blocks from the existing colour page, using sixteen
texel reads instead of four. The model atlas's two-texel margin isolates these
reads; sprite reads use the existing one-texel border. This trades a little
sharpness and additional sampling work for steadier fractional motion. It is
an Enhanced presentation choice, not retail behavior. Model alpha metadata is
decoded to full or zero coverage before filtering, so a submerged tag cannot
make an otherwise opaque hull translucent when refraction is disabled. Exact 1×,
exact 2×, zoom below 1×, screen-space UI, single-sample models, underwater
commits and model shadows keep their existing sampling. Terrain retains its
existing colour filter. This adds no render target, temporal history or
simulation state. The device fixture compares filtered submerged and ordinary
opaque hulls with the identity Blue table and refraction disabled.

Prototype validation: Great Divide, seed 7, 1280×720, 90 ticks, with the local
commander selected, produces byte-identical before/after PNGs at exact 1× and
2×. The short coastal benchmark (scene 5, seed 7, 1920×1080, 1.5×, 30 draws/s,
two runtime workers, 180 measured draws) has matching scene metadata and
190..236 moving units in both builds. Mean host DrawWork was 12.265 ms before
and 12.414 ms after; cadence remained 33.345 ms. These single runs measure
host work, not GPU execution, and are not a performance budget. The classic
renderer also completed the native-scale scene. Both Enhanced captures were
inspected; the benchmark had no burning features in view, so it does not
validate the standing-fire case. These are prototype measurements, preceding
the mode/configuration work below.

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
camera is clamped. The world point under the anchor does not move within the
camera bounds. The anchor is in **beam pixels** — the framebuffer point plus
the viewport offset (128, 32) —
which is the space `ScreenToWorld` takes, because the recorder stores a world
point at its beam position less that offset [03 §2.5]. The battle's zoom writers
convert the pointer, the viewport centre and `--shot-focus` from framebuffer
pixels at one seam. Anchoring on the raw framebuffer point instead holds the
world 128/f pixels left of and 32/f above the pointer fixed, and the map slides
under the cursor.

**Continuous Enhanced presentation.** The 30 Hz zoom controller owns input
targets and the ease, and its integer camera remains available to ordinary input
and clamp consumers. Beside that origin, zoom operations retain the precise
anchor using `origin + anchor/oldZoom − anchor/newZoom`; a clamped Modern axis
takes the continuous edge or centred-fit result (legacy retains its integer
clamp), and another writer changing the integer origin or factor
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

### 16.6 Pinch, wheel and ease — contract Z5

**Nanolathe Modern policy (user-authorized 2026-09-30).** Modern replaces the
community megamap with this camera overview. `presentation.zoomStyle` selects
`0` (Continuous/Smooth, default), `1` (Steps), or `2` (No zoom) for pinch and
wheel together. No zoom returns to fixed native 1×, cancels active zoom and
disables pinch, wheel, F9 and whole-map Tab zoom; ordinary panning remains,
and Tab keeps its Options binding. It does not enable the community megamap
or change the independently stored icon style. Community 3.9 honors the same
camera preferences when `presentation.overview = 0` (Tab: Options, default).
Selecting `1` (Overview) instead uses its separate megamap and disables camera
zoom (DESIGN_INTERFACE_HUD_INPUT §3.15). This preserves ProTA's authored
megamap recommendation without treating another content set's Community
gameplay requirement as a zoom restriction. The player can change the overview
through Controls → Mouse or Options → Orders without changing gameplay.
Strict 3.1 retains the earlier three presets, fixed 500 ms wheel cooldown and
0.30 ease; its overview preference
still selects the optional megamap. Registered rule sets inherit their base
layer's host policy through the existing registry. This is presentation only:
no new gameplay seam, RNG draws, resources, orders or save state [I6].

**Preferred zoom lock (user-authorized 2026-09-30).** Controls → Mouse owns
`presentation.zoomLockPercent`: an integer percentage, default `100` (1×),
with 1% choices from `1` to `200`. Old files and unsupported values adopt 100.
The active lock is the percentage rounded to the nearest 1/1024 and clamped to
the battle's full-map floor; the stored preference is retained when a small map
needs a higher floor. It replaces the native detent in smooth camera zoom and
the native stop in stepped camera zoom. It adds no independent stop at 1×.
For example, 120 selects approximately 1.2×, keeping the fractional filtering
of §16.3; it does not change the record scales or make that factor pixel-exact.
Strict and the separate Community megamap ignore this preference. It changes
neither the default battle-entry factor nor explicit `--zoom` framing (§16.8).

`camera.ZoomController` is the state machine, driven once per host Update from
the battle's camera pass. Easing uses host Updates and the lock's wheel hold uses the
supplied monotonic host milliseconds; neither reads simulation time [I6].

* **Smooth policy.** Pinch is continuous; the wheel makes small
  proportional changes. Both share `SnapZoom` and `ZoomSnapPercent`: targets
  within ±15% of the active lock, and input crossing the lock in either
  direction, snap immediately to it. At the default this is approximately
  0.85×..1.15× and snaps to exactly 1×. These are user-authorized renderer
  preferences, not retail findings. The previous preset list {0.25, 1, 2}
  remains available through `ZoomSteps` and `NextZoomStep`, but no longer drives
  Modern's wheel or F9.
* **Stepped policy.** The four usable stops are the full-map floor,
  max(floor, 0.25×), the active lock and 2×, sorted in increasing order.
  Duplicate stops collapse, and
  targets below the map floor are skipped. Each pinch spends one stop after
  0.12 net magnification, including a gesture spent against a limit. Each
  wheel notch advances a stop; a multi-notch event can move several stops but
  stops immediately at the lock and discards the rest of that burst. The lock's
  quiet hold and wheel easing are the same as Smooth. Selecting Steps from
  a free factor puts the camera on the nearest usable stop.
* **The wheel** banks `ZoomScrollThreshold` of travel (1000 thousandths, one
  Ebitengine wheel unit) for each notch. `n` signed notches multiply the target
  by `ZoomWheelRatio^n` (1.25); a negative notch divides by 1.25. Multiple
  notches in a host frame are spent together, clamped to the full-map floor and
  2×. Away from the lock there is no cooldown. Arriving at it puts the live camera
  there immediately, discards excess travel and holds until no wheel event has
  arrived for `ZoomScrollCooldownMillis` (180 host milliseconds). Each ignored
  event restarts that quiet interval; a long burst cannot skip the lock. A pause
  also clears fractional travel. Reversal discards the old remainder and glide,
  starting from the live view; cancelling inside the snap band reaches the lock
  with the same hold. Accepted wheel input clears follow. The ease stays
  anchored at the pointer. F9 and pinch clear pending wheel state.
* **macOS conventional wheel input** supplies camera zoom with the integer
  vertical count from `CGEventGetIntegerValueField` and
  `kCGScrollWheelEventDeltaAxis1`, rather than AppKit's `scrollingDeltaY` line
  distance. A line distance below one must not lose a deliberate notch, and
  a large line distance must not invent extra notches. Several ticks reported
  by one event or collected in one host poll retain their count. A nonzero
  AppKit line distance keeps its user-preference-adjusted direction; a zero
  distance retains the integer count's sign. If a conventional event has no
  integer count or backing CoreGraphics event, a nonzero vertical line distance
  supplies one signed notch. An event with
  both zero count and zero vertical distance supplies none. This normalization
  follows the native precision flag; it does not classify devices from delta
  magnitude or timing. GUI controls retain the original scrolling distance.
  Native synthetic-event checks in
  `internal/platform/ebitenapp/scroll_darwin_test.go` exercise small and large
  line distances, slow and batched clicks, multiple ticks, both directions,
  conflicting field signs, the absent-count fallback and touch/momentum
  exclusion. Physical mouse verification remains a manual acceptance check
  for issue #91.
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
* **macOS smooth pinch.** Pinch/spread continuously requests
  arbitrary factors from the full-map floor to 2×, anchored at the pointer
  sampled when the gesture began. Signed magnification moves a logarithmic
  position relative to the lock at `pinchSensitivity` (2.0); a flat interval
  of ±`pinchStickiness` (0.20) around zero resists leaving the lock. Factors
  within its ±15% snap band reach it directly, bypassing the ease. At the
  default the picture immediately uses the native projection. A gesture
  approaching the band from either side snaps to the lock and stays there
  until finger lift, including
  a single large event that crosses the whole band. A fresh gesture can leave
  the lock after overcoming the resistance and snap band; nearby factors are never
  final targets. A new gesture starts from the live factor, snapping nearby
  factors to the lock, and excess travel at either limit is discarded so reversal
  responds immediately. Pinch writes the live factor directly, without the
  wheel's ease, and clears camera follow. End and cancel events retire the gesture;
  cancellation does not undo an already accepted target. Ordered pinch events
  survive host batching and the semantic input copy. Blocked camera input cancels
  the active pinch.
* **The ease** closes `ZoomEaseFraction` of the remaining gap per Update, moves
  at least one unit so an integer factor cannot stall, and settles outright
  inside `ZoomSettleEpsilon`. The Modern fraction is 0.50, closing 87.5% of
  the gap in three 30 Hz Updates. Wheel input glides; pinch follows the fingers
  directly. The existing presentation transform interpolation remains §16.5.

Every one of those names is a **feel-tuning knob**, not a derived value; the
wheel and ease knobs live at the top of `internal/camera/zoomfeel.go` and
the pinch knobs in `cmd/nanolathe/trackpad.go`. The native monitor implements
these choices using Apple's gesture and scroll-event semantics and returns events
unchanged; empty native polls stay empty rather than replaying Ebiten's copy, and
other platforms retain Ebitengine wheel zoom with no device classification
guessed from delta magnitude or timing. The browser host now supplies canvas
two-touch pan/pinch and pixel-wheel pan through this same controller; its
Ctrl-wheel burst lifetime, DOM-unit policy and unavailable momentum metadata
are documented in DESIGN_BROWSER_HOST §4 contract 8.

The wheel binding is Nanolathe's, not retail's. Retail leaves the wheel to the
active GUI list under the pointer [07 §2][07 §10], and the UI boundary still
consumes it first: the camera pass sees only a wheel the chrome did not want, and
takes it only over the battle viewport, only outside TALK, only with no modal
open and the pointer off the minimap, and only in the executor that can present a
free factor. Wheel and trackpad controls also require window focus and no command
palette or unit-info ownership. Placement rotation retains its modified wheel.
Skipping the camera pass clears gesture state and wheel fractions/holds while
preserving an accepted glide. Changing the selected mode, zoom style or active lock cancels
the old gesture, glide and overview return before the new policy accepts input.
Turning Steps on in the Enhanced executor chooses the nearest configured stop.
Initial policy sync preserves battle-entry and restart framing; Classic keeps
its exact native/detail scale cycle instead of adopting fractional stops.

### 16.7 The minimum factor — contract Z6

**Modern full-map floor.** `Camera.MinZoom` is
`min(viewW/mapW, viewH/mapH)` over the battle viewport and the playable map,
rounded down and bounded to 1/1024..1×, so native remains reachable even when
the map already fits at 1×. Both playable axes fit at the floor.
For a camera with an explicit zoom, each axis that fits entirely inside the
viewport stays centred, with equal unavoidable margins and no panning on that
axis. Once the projected map fills an axis, its origin is bounded by the map
edges, with no extra border allowance. Cursor anchoring applies within those
bounds; an edge takes precedence when both cannot hold. Each zoom sample starts
from the previous bounded view, so it does not retain a displaced anchor that
would pull the camera back after leaving an edge. Both the fit transition and
the edge clamp use the continuous presentation factor before flooring the
integer camera, avoiding rounding jumps at fractional zoom factors. The host
also bounds every interpolated view: legal endpoints on opposite sides of the
fit threshold do not guarantee a legal intermediate projection. Non-interpolated
recordings also bound their integer fallback view and carry any edge correction
in the world transform, so rounding cannot expose a single border pixel.

This replaces the retained overview-margin allowance approved on 2026-10-04:
the user requested hard map edges with smooth cursor zoom on 2026-10-05.
It is Nanolathe presentation policy, with no authoritative effects. Unzoomed
cameras and legacy controls retain their existing bounds.
Targets below the floor are clamped at the controller and
camera. The viewport span is taken in framebuffer pixels, because the chrome
does not move with the zoom. The host sets `Camera.ViewportZoomFloor` for legacy
and the separate Community megamap: the greater axis ratio rounded upward,
bounded to 1/16..2×, with the earlier clamp. Modern and Community camera zoom
use the full-map fit. These presentation choices change no simulation, RNG or
resource behavior [I6].

**Overview margins (issue #90).** Border space outside the projected terrain
raster stays at PAL[0]. Features and effects can extend farther beyond the map
than the fog cache's border cells, so clearing the framebuffer before world
drawing alone leaves isolated sprite pixels there. The modern executor retains
the terrain command's raster bounds through the same precise affine transform
as its tile quads. After resolving the world layers, it clears the four exterior
rectangles in framebuffer pixels before the chrome and strategic markers.
Pixel-centre coverage preserves fractional map edges. The bounds use the full
tile raster, including authored void tiles, rather than the smaller playable
camera extent. Each region resets its bounds; world overlays without terrain
leave already composed UI intact. This is Nanolathe presentation policy, with
no changes to the retail fog contract, classic executor or authoritative state.
The authored GPU fixture locks all four margins, fractional translation, native
and detail record steps, repeated frames and the overlay boundary.

The camera also retains the requested factor before clamping. Wheel, pinch and F9
navigate that request, so the tactical and native stops remain distinct even if
the map floor gives them the same live factor; animation changes only the live
factor, direct jumps retain the request, and classic step changes replace it with
their own factor. In Enhanced presentation a sub-native request clamped by the
floor, or equal to it, becomes a full strategic view when the live factor
reaches that floor,
which makes the furthest-out stop usable on small maps and large framebuffers
without turning an ordinary native view into icons. Before arrival the usual fade
applies; at arrival models disappear, icons become fully opaque, and icon picking
and selection outlines take over together. Choosing native or detail clears the
exception immediately. The paused world cache includes this gate; resolution
changes refit the retained tactical request to the new floor.

The step writer deliberately does **not** apply this floor: a step is always at
least 1×. Modern presentation bounds are applied alongside `clampAxis`, leaving the
retail primitive intact. Camera tests cover direct and eased pointer anchoring on wide,
tall and square maps, edge-limited panning, centred fitted axes and the legacy bypass, alongside
full-map fit, collapsed stops, preferred-lock barriers, reversal, fractional
wheel travel and wrapping host milliseconds.
Host tests cover the three mode boundaries, both pinch styles, input ownership,
overview return and the retained legacy controls. `TestPreferredZoomLockBandAndCrossings`,
`TestPreferredZoomLockWheelHoldAndReset`, `TestCustomZoomLockDoesNotCatchNative`
and `TestPreferredZoomStopsOrderAndCollapse` lock the custom band, burst hold
and step ordering. Host tests exercise a 120% pinch, fresh-gesture departure,
free passage through native, settings changes, overview return and mode bypass.

### 16.8 Runtime switches

* **Modern Tab/F9.** With the Modern renderer, Modern saves the current factor
  and precise camera origin, then jumps to the
  full-map floor and centres the map. The next press restores that factor and
  position, clamped for the current viewport; a saved factor near the preferred
  lock snaps to it. It clears active pinch, wheel easing and follow. Bookmarks are not
  restored from the snapshot. Entering classic clears the saved return view.
  F9 always takes this action while zoom is enabled. Tab takes it on release,
  like the megamap, only when `presentation.overview = 1` (Overview); with
  `0` (Options, default), Tab opens and closes Options without changing the
  camera. Changing the choice to Options cancels a pending Tab release. F2
  opens Options. Community 3.9 with `presentation.overview = 0` keeps Tab for
  Options and honors camera zoom preferences, F9 and explicit battle-entry
  zoom. With `1` it takes Tab for its megamap and ignores F9 and explicit
  battle-entry zoom; Strict 3.1 retains its 1× → 2× → 0.25× F9 cycle.
  The Camera zoom selector remains usable with Community's megamap selected:
  choosing Continuous or Steps also selects Tab: Options, restoring the camera
  consumer without changing gameplay (interface §3.15, issue #99).
  Classic keeps its 1× ↔ 2× F9 cycle in
  Modern, Community camera zoom and Strict, and Tab's options binding in
  Modern and Community camera zoom. No zoom disables F9 in both renderers and
  retains Tab for Options.
  `TestModernTabOptionsPreference` locks the menu toggle without camera movement
  in Continuous and Steps, and F9's independent overview action;
  `TestModernTabOptionsCancelsPendingOverview` locks cancellation across a preference change.
* **The wheel** is §16.6.
* **`--zoom`** accepts any factor in the free range for the modern executor and
  only 1 or 2 for classic — the restriction is applied after parsing,
  because `--renderer` may follow `--zoom` on the command line. A capture follows
  `--shot-renderer` when one is given. The map-derived floor is applied at battle
  entry, where the map is known. Both renderers default to 1× at every
  resolution, including captures and the benchmark. A restart keeps the factor the player was on. Battle entry, a restart and a
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
  today, and is a follow-up (`TODO(question)` at `Client.markerAlphaAtZoom`).
* **No projectile markers.** A shot is an event, not a thing on the map.
* Markers are recorded **outside** the world region, already positioned through
  the live factor, because a fixed-pixel mark must not be scaled by the world
  transform. They draw through a destination op as a premultiplied flat index at
  the layer's alpha.

### 16.12 Verification

1. **The rest steps are untouched.** Classic captures at 1× and 2× and
   modern captures at 1× and 2× are byte-identical to the build before this
   section.
2. **The camera.** `internal/camera/zoom_test.go`: the free factor agrees with
   the native and detail steps; picking is the exact inverse at rest and the
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
— the recorder's exact doubled projection, or the native corners times two. The
player's **Supersample** switch (§17.5, §30) is what turns the coverage resolve
off.

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

### 17.5 The Supersample switch

A Nanolathe presentation preference, on by default: `drawlist.Effects.Supersample`,
stored as `settings.Presentation.Supersample` (`"supersample"`) with the other
Enhanced switches (§30). On is everything above. Off, the subject is drawn as the
native raster would draw it, with whole-pixel edges like the classic executor's
mobile units, by reusing two paths the lane already has rather than adding one:

* **The recorder** builds no doubled lane (`Client.supersampleGeometry` is false),
  which is exactly the recording the Anti-Alias option off makes: retail's anchor
  and native corners with no half-pixel offset, the shadow likewise. The geometry
  caches carry that result in their identities (§17.2), so moving the switch
  rebuilds the lane; the executor's retained store already refuses an entry whose
  doubled flag differs (§22).
* **The executor** keeps the 2× page, the key and colour passes, the verdicts and
  the outline, and changes only the resolve: every model commit takes the one
  texel at its pixel's block top left, at four times the weight, instead of the
  four under it. That texel is the native raster's sample, because the lane biases
  its corners half a texel so a texel is covered exactly when the span writer
  covers its corner point, and a block's top-left corner is the native pixel's own
  point; it is also the texel the verdicts already read as the pixel's key (§22).
  So with native corners the coverage is the native raster's exactly, and an edge
  pixel is covered or not. The switch rides a free lane of each commit: the body
  commit's Custom1, the projected shadow's Custom2, the silhouette shadow's
  Custom0, and the underwater commit's shading lane at weight two.

What it leaves alone: the doubled page is still allocated and drawn at 2× (the
cost is unchanged; the switch is a look, not a saving); the outline already draws
whole native pixel blocks; the live lane and a group's children are part of the
raster the commit reads, so they follow it; the fallback for a subject no page
holds was always native. Three treatments keep their own filtering because it
belongs to them rather than to the raster: the aircraft soft shadow (§34, its own
switch), the submerged part of a refracted hull, whose gather is displaced by the
water's fractional offset (§26.5), and the screen-space reflections (§26.4).
Classic is unaffected.

Either half alone still gives whole-pixel edges: a host that applies the
selection to the executor alone — the Nanolathe screen's preview keeps its
recorder on every effect — gets the single-sample resolve of the recorder's
doubled lane, which is the native raster at the subject's half-pixel position.

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
   `model_supersample_test.go` locks the switch of §17.5: with supersampling on,
   a subject covering one doubled texel resolves at a quarter over the field, a
   block split between two colours resolves their mean, and a one-texel projected
   or silhouette shadow composites an eighth; off, the top-left texel alone gives
   the face's exact colour, a texel elsewhere in the block gives none, and each
   shadow composites its whole half-blend.
   `TestSupersampleOffRecordsNoDoubledLane` locks the recorder half.
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
  Enhanced zoom including the fade band when the bound rules and Radar dots
  preference allow them. The separate attack-only contact picker follows
  [Modern radar dots](DESIGN_INTERFACE_HUD_INPUT.md#modern-radar-dots).
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
  contacts retain knowledge restrictions: the identified picker never hands
  hidden `UnitView` metadata to a tooltip. Modern Attackable dots uses a separate
  contact-handle picker for ordinary hostile attack commands only. Drag
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

### 18.7 Optional community icon configuration

**Nanolathe Modern policy (user-authorized 2026-09-30).**
`presentation.strategicIconStyle` selects `0` (Modern, default) or `1`
(Community 3.9) in Modern and Community camera zoom. Modern symbols ignore the
optional path and mod auto-discovery; Community icons use the authored mapping below. The Controls
screen's Mouse tab exposes this independently of Smooth/Steps. A live change
joins speculative recording, invalidates the paused world and accepted icon
projection, and rebuilds the battle catalog once. Missing community art retains
the generated fallback; no substitute historical art is invented. Community
3.9's separate megamap keeps its own icon bank regardless of this preference;
Strict retains its existing optional custom-icon resolution.

`settings.Presentation.StrategicIconConfig` is an optional host path to the
community draw engine's
[icon-configuration contract](../research/extensions/draw-engine-interface.md#megamap).
The path may name the exact INI or a package/config directory containing
exactly one case-insensitive `iconcfg.ini` at its root, `Icon/`, or `ZIcon/`.
No match or multiple matches reports the searched directory and retains the
generated catalog; an exact file path always remains the user's selection.
Empty discovers the running content's own configuration, silently: with a mod
from the Mods & Mutators library (DESIGN_MODS_MUTATORS §4) the mod's directory
is searched, and with a manual `--root` stack its roots are searched from the
last to the first, the order in which they win. The first root holding exactly
one configuration in the recognised places supplies it; a root holding none is
passed over without a diagnostic, and a root holding several reports the
ambiguity and keeps the generated catalog rather than trying the next root.
The base install alone is never searched. When nothing is found the generated
catalog above is kept. `UseDefaultIcon=true`, including its source default when the key
is absent, also keeps that catalog and does not open any PCX named by `[Icon]`.
An unreadable INI, malformed PCX or atlas outside the host bound reports the
config and art paths and falls back to the complete generated catalog; a partly
loaded mapping is never published. Authored relative PCX paths keep their
separator spelling portable. Each path component first takes an exact directory
entry, then a unique case-insensitive match so packages authored on a
case-insensitive host also load on a case-sensitive host. Multiple folded
matches are ambiguous and fail the whole custom mapping rather than selecting
one by directory order.

With custom icons enabled, `[Icon]` rows retain authored order. Each ordinary
name resolves through the final content catalog's category registry, and the
first membership containing a definition ID supplies its PCX. `unknow` supplies
the unmatched and stale-identity fallback. The reserved `nothing` and
`nukeicon` rows are parsed as source-reserved art and never compete as unit
categories: Nanolathe's identified strategic layer has no hidden-unit `NONE`
image or projectile icon, while unidentified contacts retain §18.4's generic
square. The draw engine's commander files are outside the INI row contract and
are not imported by this host option; generated commander appearances therefore
follow the same ordered category/`unknow` mapping as other identified units.

The PCX is compiled once into the shared strategic-mask atlas. `FillColor`
(default 0) becomes the live team lane, `TransparentColor` (9) becomes empty,
and `SelectedColor` (89) becomes the conditional halo lane. Selection draws
that lane with palette index 89 (or its configured replacement); hover draws it
with `HoverColor` (84). Other fixed PCX colours retain luminance between the
white and black lanes because the existing atlas deliberately carries masks,
not fixed palette indices. `UseCircleHover=false` uses the authored highlight
pixels. When true, a circle in the same fixed 24-pixel marker footprint replaces
those pixels for hover, preserving §18.4 drawing and picking bounds. These are
host presentation mappings, active only where generated strategic icons already
apply; they do not change definition records, hashes, visibility or gameplay.

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
| nanolathe spray | a palette-coloured quad two world pixels beyond each side of the particle core, at gain 0.16 (§23.5) |

The composite is bound as a source image while the glow plane is the destination,
so reading it there is legal and reads the frame as replayed so far. The emission
fragment's alpha is its largest channel, so the plane stays a valid premultiplied
image through the shrink and the blurs.

### 19.3 The resolve — contract L2

The batch is additive and order-free, so it rides no phase: sources append quads
(with the world transform of §16.3 applied to their vertices exactly as the
scheduler applies it) to runs keyed by image bindings, and the resolve runs at
most once per frame, at the first of: the fog command, before it compiles — so
the grey composite dims the glow and the black one hides it, exactly as they
treat the light sources themselves — or the close of the world region, so a frame
recorded without a fog composite still resolves before the chrome is painted over
the world. A front-end frame has no emissive commands and drops nothing.

Because the emission plane is a saturating sum of 8-bit contributions, draw order
does not reach a single texel, and a quad joins ANY run whose bound slots agree
with the slots it reads, not only the last one opened; callers bind exactly the
slots their op reads and leave the others nil, and a run adopts a slot a later
quad needs. Strokes, halos and flash discs share one run and each scene atlas
page of sprites another, so a battle frame's emission is two or three device
draws instead of one per change of source kind in record order.

The resolve submits the scheduler first, so it is a barrier costing one segment,
then draws five render passes, each into one image:

1. **Emission.** Clears the full-frame emission plane and draws the runs into it
   under `BlendLighter`.
2. **Shrink.** Shrinks it to a quarter of the frame, each texel the mean of the
   4×4 block under it — the two 2×2 halvings the layer was tuned with, in one
   pass.
3. **Blur across.** Blurs both octaves along x into one plane. The near octave
   takes the nine-tap Gaussian (`glowSigma` 2). The far octave's fragments fall
   on every other quarter column and read every quarter texel within reach
   through a Gaussian of `glowFarSigma` quarter texels, so the far octave
   shrinks to an eighth of the frame's columns as it blurs.
4. **Blur down.** Blurs both along y the same way into the octave plane, the far
   octave shrinking to an eighth of the rows. The octave plane is cleared in the
   same pass, so each octave sits inside a border of transparent texels.
5. **Composite.** Adds the quarter octave (×4, `glowNearWeight`) and the eighth
   octave (×8, `glowFarWeight`) onto the composite in one draw, each magnified
   with linear filtering and weighted, under a **screen** blend,
   `out = src + dst × (1 − src)`. The shader screens the two octaves together
   and the blend screens the result onto the composite once. Screen composes
   associatively, `1 − out = (1 − dst)(1 − near)(1 − far)`, so this is the two
   screen blends it replaces, without the rounding between them.

Screen rather than additive is what keeps a fireball's own art: an
already-white core stays white instead of clipping, and the halo shows where the
ground is darker. Every pass after the emission serves both octaves because the
render pass, not its fill, is the unit of device cost (§11.5), and the Metal
driver's stall past about 80 passes in flight makes it the budget the executor
shares (§22.1 "Merges run in waves").

**The halo is sized in world pixels, not in framebuffer pixels.** Everything a
halo surrounds — the beam, the sprite, the flash disc — is drawn at the frame's
**view scale**: the record step the recorder projected the world at times the
live zoom factor the scheduler applies (§14.2, §16.2). The terrain record
carries the step, so the executor reads it from the same place the aircraft
shadow does (`glowViewScale`); a frame with no terrain is the native view. A
halo fixed in framebuffer pixels is half as wide, *relative to the units it
comes from*, at the 2× step as at 1×, and changes size under the wheel. So
`glowNearSigmaWorld` states the near octave's blur radius in world pixels and
`glowBlurStep` converts it once, into the near kernel's tap spacing in quarter
texels; the far kernel's sigma, `glowFarSigma` quarter texels at the native view,
scales by the same factor (`glowFarKernelSigma`), and the stroke quad's
half-width takes the same view scale. At the native view the near spacing is
exactly one texel and the quad four screen pixels. Near fetches are nearest, so
a spacing below one texel folds taps onto the same texel: the kernel narrows
toward the octave's own resolution rather than aliasing, which is the right
failure at a zoomed-out view where the halo is already finer than the octave
can hold. The far kernel reads every texel within its cut, 2.5 sigmas, at every
scale, so a thin source cannot comb its halo when the scale spreads the taps;
the shader's loop is bounded (`glowFarReachMax`) and covers `camera.ZoomMax`.
The Gaussian's value at the cut is subtracted from every weight, so the kernel
ends at zero and changes smoothly as the zoom eases instead of gaining taps at
once.

That is five render passes per frame with something glowing. The fill is about
two frames: the emission plane (cleared, then drawn) and the composite are a
frame each, and the shrink and blur planes add under a quarter of one. A frame
with nothing emissive costs nothing. The planes are allocated once per frame
size. All four — emission, quarter, across and octave — are **unmanaged**, so
their texels never depend on an atlas placement and no placement can put two of
them on one texture or copy between the passes (§13.12 "Page planes are
unmanaged"). Steady-state frames allocate no options, no uniform map and no
geometry here.

The knobs (`glowLineWidth` 4 world px, `glowGain` 1, `glowThreshold` 0.65,
`glowSpriteGain` 0.6, `glowLightGain` 0.35, `glowNearWeight` 0.55,
`glowFarWeight` 0.425, `glowSigma` 2 over `glowTapCount` 4 taps a side,
`glowNearSigmaWorld` = `glowSigma` × `glowOctaveNear` = 8 world px, which is the
eight framebuffer pixels the layer was tuned at when the view scale is 1, and
`glowFarSigma` 4.65 quarter texels = 18.6 world px, cut at `glowFarCut` 2.5
sigmas) are presentation choices tuned by eye on
the battle benchmark capture; a first pass at 0.8/0.5 with an additive composite
and no sprite gain blew every fireball to a white blob, which is the case the
screen blend and the sprite threshold exist for.

The octave weights were tuned at 0.65 and 0.5. A play-test on a naval map
(2026-09-22) found the halo washing out a shipyard and the hulls around it; a
first response halved both weights, which dimmed every family alike. The
complaint was the nanolathe spray's glow and light, so the weights now stand at
about 85 percent of the first tuning (0.55 and 0.425) and the spray has its own
lower gains (§23.5 NL2–NL3): weapons and explosions stay close to the first
look while the spray reads as a soft tint. Measured against the same frame with
the glow off and the spray unlit, on the staged film scenes: the naval battle
adds 1.35 levels per channel in a crop around two ships against 1.59 at the
first tuning (85 percent); a crop around two factories under construction adds
1.33 against 3.62 (37 percent).

**Nine passes to five (2026-09-24).** The resolve used to be nine render passes
for 0.2–0.3 ms of GPU time: two linear 2×2 shrinks to a half and a quarter
plane, a two-pass separable blur of the quarter plane, a shrink of that blurred
plane to an eighth, a two-pass blur of the eighth, and two magnified adds. The
half plane is gone (one 4×4 shrink), the far octave rides the near octave's two
blur passes, and the adds are one draw. The near kernel is the one the layer
was tuned with. The far octave was the blurred near octave shrunk by two and
blurred again with the near kernel on its wider texel; it is now one Gaussian
over the quarter plane, and `glowFarSigma` is fitted to the old chain's
response to a point source. At the native view its spread, 17.0 pixels, agrees
within half a percent, and no pixel of the response moves by more than one
percent of its peak; at the 2× step five percent. The old chain's spread at
fractional view scales carried fixed framebuffer-pixel stages and folded taps;
the new one scales in proportion to the view, so its spread is 5 percent
narrower at a view scale of 0.5 and 14 percent at 0.25, where the halo is a
few pixels wide. Motion keeps the old granularity: the far octave inherits the
quarter plane's 4-pixel texel, as it did.

Measured at the Survival benchmark capture (Moon Quartet, 803 captured units;
docs/BATTLE_BENCHMARK.md "Replaying the scale of a diagnostic capture"), the
frame's passes fall from 24 to 20 at the native view and from 26 to 22 at the
1.5× and 2× views, and `GlowPasses` from 9 to 5. Against the nine-pass build,
`battle.png` differs by at most one level at the native and 2× views (two at
1.5×), 0.43–0.45 levels on average over the pixels the glow touches, and the
glow's total energy agrees within half a percent. With the glow off the frames
are byte-identical. On the M3 Pro's per-pass GPU timestamps the resolve costs
what it did: about 0.13 of the model lane's GPU time in both builds, the lane
doing the same work in each.

Four passes were built and measured first: the emission plane shrunk to a
quarter plane and to an eighth (the eighth by a 16×16-pixel tent, so the far
octave's motion stays smooth), then each octave blurred in one two-dimensional
pass. It cost 3.3 times the nine-pass resolve's GPU time. A two-dimensional near
kernel is 81 taps a quarter texel against the separable pair's 18, the tent is
256 taps an eighth texel from the full-resolution plane, and a far octave
blurred from its own eighth plane has to read every texel above a view scale of
one — 529 taps a texel at the 2× view — or it combs thin sources. One pass was
not worth that.

### 19.4 The switch

`settings.Display.Glow` (`display.glow`, default 1) is a Nanolathe option with no
retail bit. The shell and the capture route apply it with the other display bits
(`applyVisualOptions` → `Client.SetGlow`), the `+glow` chat command toggles and
persists it beside `+antialias` and `+dither`, and every modern executor site
copies the client's switch to the renderer (`Renderer.SetGlow`) before `Execute`,
next to the display palette and the effect selection. A settings file that omits
the key keeps the default because the loader decodes over the defaults
[02 "Settings"]. Off, no source appends and the resolve is a no-op. It is
deliberately **not** one of the `Effects` switches of §30.

**Strength.** `settings.Display.GlowStrength` (`display.glowStrength`, default
100, stored 0..200) is the player's halo strength as a percentage of the tuned look,
applied while the switch is on. `Renderer.SetGlowStrength(percent)` clamps it to
`0..GlowStrengthMax` (200) and multiplies both octave weights by
`percent / GlowStrengthDefault`; that is the only value it reaches, so the
halo's size, colour and threshold never change with it. 0 is off exactly as the
switch is: no source appends, the resolve spends no pass, and the frame is the
frame with the switch off. `New` starts at `GlowStrengthDefault`, so a host that
never sets the strength draws the default look. It is a separate key rather
than a new meaning of `glow` because `glow` is stored as 0 or 1 by its options
button, the `+glow` command and every file already written; read as a
percentage those would be a 1 percent halo. The loader repairs a negative value
to the default and caps a larger one at 200.

**Families.** A content pack scales three families of Enhanced light
separately, each as a percentage of its tuned look, 0..200, default 100, in an
`[effects]` section of the annotation file of §29.1 (`nanolathe/materials.tdf`):

| key | family | what it scales |
|---|---|---|
| `weapons` | beams and lightning, effect, projectile and strip sprites, explosion flash discs and halos | their glow emission |
| `nanolathe` | the nanolathe spray | its glow quad (NL2) and the light its clusters cast on models, smoke and terrain (NL3) |
| `ground` | every battle light | the terrain receiver alone (§31.3); models and smoke keep their light |

`Renderer.SetGlowFamilies(weapons, nanolathe, ground)` clamps each to 0..200 and
multiplies only its own family's gains by `percent / 100`. 0 turns that family
off: its sources append nothing (or, for the nanolathe light, gather no
cluster), and no other family changes. The ground percentage reaches the
terrain pass through the one per-source multiplier only the terrain reads, the
source's fade (§31.7), so the ground pass needs no switch of its own. The
player's `display.glowStrength` weights the whole glow layer, so it multiplies
the weapons and nanolathe glow on top of the pack's percentages; the nanolathe
light and the ground family belong to battle lighting (§30's model and ground
light switches) and are not weighted by the glow strength; the ground family is
weighted by the player's separate ground light strength instead (§30). The renderer's zero value is every family at
100, so a host that never sets them draws the tuned look.

**Player source amounts.** The Nanolathe Glow card has an Overall meter for
`display.glow` / `display.glowStrength`, plus independent `presentation` meters:

| key | source boundary | what it scales |
|---|---|---|
| `weaponGlowStrength` | beam and lightning strokes; projectile body sprites tagged by the recorder | glow emission |
| `explosionGlowStrength` | all other emissive effect and strip sprites; explosion flash discs and halos | glow emission |
| `nanoGlowStrength` | nanolathe spray | spray glow and local model, smoke and terrain illumination |
| `groundLightStrength` | every terrain light pool | the existing ground strength of §30, shared with the Lighting card |

These are authored presentation groupings, 0..200, default 100. The effect
boundary includes fire, sparks, muzzle flashes and unclassified effect/strip
art; it does not guess that every such sprite is an explosion. Projectile
classification uses the recorded source tag, never a sprite name or bright
pixels. The two weapon/effect player amounts both multiply the content pack's
shared `weapons` percentage. Nano multiplies the content `nanolathe` value;
ground keeps multiplying content `ground`. Overall further weights every bloom
source but leaves local and terrain lighting alone. A zero amount admits no
source in that family, and the other amounts are unchanged.

The three new source percentages travel in `drawlist.Effects` through
`presentationEffects` / `storeEffects` and `Renderer.SetEffects`, including the
preview's independent executor selection. The recorder keeps the source tags
and histories even at zero. Renderer storage is separate from the content
family storage, so the host's per-frame `SetGlowFamilies` cannot replace a
player amount. Source resets preserve both. Omitted settings keep 100,
negative settings repair to 100, explicit zero remains zero, and values above
200 cap at 200. `TestPlayerGlowAmountsStayIndependentOfContent` and
`TestPlayerGlowZeroAndGain` lock multiplier composition and source admission;
settings round trips, graphics preset scopes and mod-lock path discovery lock
the UI wiring.

`internal/client` parses the section beside `[materials]` when it installs the
file (`SetMaterialTable`) and holds the result with the texture table; hosts
read it through `Client.GlowFamilies()`. An override without `[materials]`
keeps the texture table in force and sets only the families (and any
`[glint]` strengths, §23.7); one without `[effects]` restores every family to
100; one with none of the three sections, or a family value that is not a
whole number, is reported and changes nothing. Unknown keys
are ignored so a later family does not make an older build reject the file.

The host carries it the way it carries the switch: `Client.SetGlowStrength` /
`Client.GlowStrength` beside `Client.Glow` (a changed strength advances the
paused-world revision, as the switch does), and every executor site that copies
`Client.Glow` to `Renderer.SetGlow` — the window, the paused world, `--shot`,
`--shot-debris` and `--film` — copies the strength beside it. The settings
loader (`attachSettings`) hands `display.glowStrength` to a client that already
exists when the file is read. The battle
benchmark keeps the renderer's default strength, so two runs measure the same
work. A film builds its own client and draws the default strength, so footage
stays reproducible whatever the player's preference.

`applyVisualOptions` copies `display.glowStrength` into the client, which is
what reaches the windowed shell's client (created after the settings load) and
the `--shot` routes.

Every site that copies the strength also copies the content pack's families,
`Renderer.SetGlowFamilies(Client.GlowFamilies())`: the window, the paused world,
`--shot`, `--shot-debris` and `--film`. The battle benchmark keeps every family
at 100, like its strength. Owed: an options-page control for the player's
strength.

### 19.5 Verification

1. **Device fixture.** `glow_test.go` under `NANOLATHE_GPU_DEVICE_TEST=1`: a flat
   field with one emissive stroke inside a rest-factor world region. Off, the
   frame is the exact classic expansion and the counters are zero. On, the
   resolve spends five passes, the stroke's own pixels are unchanged (screen
   leaves white white), the field beside it is clearly brighter, the brightening
   falls off with distance, and the far corner is the field to within the blur's
   last tap. The stroke's emission is centred on texel boundaries of both
   octaves, so the halo must be symmetric about it — a plane placed, or a tap
   phased, half a texel off breaks that — and twenty pixels out, where the near
   kernel has faded, the far octave must still light the field.
2. **CI tier.** Unit tests lock the normalized kernel, run-relative indices and
   run selection of the batch (conflicting bindings split, compatible ones rejoin), the stroke quad's geometry, and the recorder's
   emissive marks on beam and segment strokes. They compile the four resolve
   shaders, check that the far kernel's sigma follows the view scale unclamped
   from `camera.ZoomFloor` to `camera.ZoomMax` within the shader's loop, and
   check that the octave plane keeps a transparent border around each octave.
3. **Byte-identical off.** The modern battle benchmark with `display.glow` 0
   against the build without the layer.
4. **Look.** The benchmark capture with the switch on beside the same frame off,
   cropped 1:1 around an explosion cluster and around a laser: the fireballs keep
   their texture with a soft warm halo, the beam glows, burning trees glow, smoke
   does not, and the fogged half of the map shows the glow greyed.

## 20. Placement weapon ranges (Enhanced)

### 20.1 Presentation policy

General range rings start **off**. `+showranges` toggles the existing process-only
switch. Its Shift-gated queue overlay is the single general range display in
both renderers [07 R-P0-11 §3]. The former Enhanced overlay, category colours,
dashes, legend and separate selection/hover walk have been removed.

**Nanolathe Modern presentation policy:** an armed build product shows its
weapon ranges at the snapped site without requiring Shift or `+showranges`,
including an invalid site while the player repositions it. This is a Modern
renderer preference, independent of the simulation rule set; Classic has no
automatic placement guide. Each active authored weapon slot uses its `Range`,
including interceptors, matching the detailed `+showranges` weapon branch.
NOWEAPON links are omitted. Equal radii retain their separate weapon-slot labels.
Placement shows no sensor, jammer, build-distance or interception-coverage rings.

The Effects page's **Placement weapon rings** switch stores
`presentation.placementWeaponRanges` (default on), through ordinary host settings,
mod recommendations and presets. A mod config's `content.presentation.show_ranges`
(default false) seeds the shell or direct battle once; subsequent command toggles
survive battles within that shell without settings writes. The older
`content.presentation.placement_weapon_ranges` remains a recommendation only when
`settings.presentation.placementWeaponRanges` is absent. The player's effective
presentation preference controls the automatic exception; Shift with explicit `+showranges` still shows
the product's weapon rings. The hosted mods' configs use these defaults, which are
Nanolathe UI policy rather than historical mod or retail claims.

Placement uses the prospective definition, footprint centre and validated site
height. It never submits an order or changes RNG, resources or authoritative
state. Losing focus, switching to Classic, opening a modal or result screen,
entering chat, moving outside the world viewport, disarming placement or starting
a Modern drag suppresses it. Shift retains its normal selection and queue input.

### 20.2 Shared rendering path

`hud.WeaponRangeOverlay` and the detailed queue weapon branch call the same
weapon-ring helper. It supplies the established tick-parity GUI colour, the
`weapon1 range` through `weapon3 range` labels, terrain-following chord walk,
field narrowing and label placement [07 R-P0-11 §3]. The existing malformed-radius
bounds guards apply. Runtime enabled-bit quirks stay with the live queue branch;
a prospective product supplies its active authored slots independently.

The placement adapter appends these primitives to `drawQueueOverlay`, using its
projection, palette lookup, FNT labels and integer line rasterizer inside the
same world-overlay region (§16.3). The standalone tactical renderer and its
foreground-stage hook no longer exist. The product's ghost and ring therefore
share the queue overlay's camera transform at every zoom.

These are authored planning radii, not guaranteed coverage: actual firing also
tests terrain, target restrictions, arcs and ballistic feasibility [06 §3.3].
No retail behavior has been redefined by the placement exception.

### 20.3 Verification

Tests compare placement primitives with the existing detailed weapon branch on
both tick parities and uneven terrain, preserving slot labels even for equal
radii. Adapter tests cover the snapped invalid site, active-slot admission,
interceptor range, the per-mod default and explicit-command override, focus,
Classic, pointer bounds, disarming and drag suppression. Existing queue-overlay
tests own the chord-count arithmetic, terrain lift, label position and bounds.

`--shot-build <name>` captures placement; `--shot-shift --shot-select` with a
profile setting `presentation.show_ranges` true captures the ordinary queue
ranges. These switches apply after simulation setup and submit no build order.
Inspect matched captures at native, fractional and magnified zoom, plus disabled
placement and Classic, to verify the shared draw path.

## 21. Modern resource construction input

The modern-only resource double-click shortcut is specified in
DESIGN_INTERFACE_HUD_INPUT §3.10. The active executor gates input recognition;
ordinary session build commands and placement validation own all resulting
construction. Modern drag construction, rectangular area work and free-form
formation commands are specified in
[DESIGN_INTERFACE_HUD_INPUT §3.11](DESIGN_INTERFACE_HUD_INPUT.md#311-modern-drag-commands);
their previews share the world-overlay transform of §16.3 and ordinary indexed
line and fill primitives. Alt grid capture suppresses the placement range preview.
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
construction outline rings (`model_outline.go`) and their CPU row walk
(`model_prepare.go`), the parameter packing (`model_quads.go`), the
retained-lane store (`model_retain.go`) and the texture page
(`model_atlas.go`).

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

An ordinary attached-unit group composes in **one** region over the union of its bounds:
the carrier's faces, then each mergeable child's with its signed height delta
added to the keys, saturating at the byte's range where retail would wrap
[03 R-REN-03A §4]. A carried child that casts a shadow composes a **second** time
in a region of its own — its own keys, its own verdicts, no carrier clip — because
a shadow is cut from its subject's own finished image and the group region holds
the carrier's texels too [03 R-REN-03D §1]; `ModelStats.DirectCargoImages` counts
them, and the second composition is suppressed from the reflection source so the
same world geometry does not reflect twice. A shadow whose source region is
invalid is **omitted**, never cut from texels that are not the subject's.

Key and colour passes share one vertex batch. Packets without cached seeds
use one pair; seeded packets finish cached colour before a later outline/live
pair, as described under **Cached-plane seed** below. Groups requiring separate
child resolution then merge their finished children:

1. **Key.** Each face's height key, narrowed to a byte as the span writers narrow
   it, into a key plane under a MAX blend, so a texel holds the highest key drawn
   there. A four-corner face, flat or textured, takes its key from the span
   writer's two-chain mapping of its corners, evaluated per fragment from the
   quad's key entry in the parameter image (§11.2); any other ring interpolates
   its lanes linearly.
2. **Colour.** Each face's texel where its own key is not below the stored one —
   retail's `stored ≤ incoming` admission [03 R-REN-03A §2] — into a colour
   plane, faces in RECORDED order so a tie goes to the later-drawn face as
   retail's does. That tie is what puts a solar collector's base rim over its
   open panels, which lie at its height; a painter's sort by mean key loses it. A
   mapped face's key is the same two-chain mapping the key pass evaluated,
   walked from the quad's corner entry (§11.2), where the compiler rounds it
   differently at a few texels; at those the face can lose a texel to its own
   key (§22.4 "Owed"). Shadow silhouettes draw in their index with no key
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
   mapped faces, eight texels of a twelve-texel slot; its ninth texel carries
   the subject's **frame origin**, the atlas texel of the raster's local (0,0),
   because a parameter entry is packed **subject-local** — corners in the
   raster's own frame at the atlas scale, plus a bias that keeps the edge walk's
   operands non-negative — and the fragment shifts its atlas position by that
   origin. Every non-shadow subject therefore has an entry, negated when it
   carries the frame alone, so the verdict block still gates on the sign.
   Outline endpoints are the span writer's own row walk over the NATIVE
   packet, one pixel block each, key-tested once against the pixel's key,
   between the cached and live lanes in retail's order
   [03 R-COMP-01 §3][03 R-REN-03A §4]; a ring of three or four corners is one
   primitive whose fragment finds them ("Construction outlines" below).

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
| span fill inclusive on the left and top, exclusive on the right and bottom [03 R-RAST-01 §1] | body corners biased half a texel of the raster being drawn, so the device's centre test equals the span writer's corner test and a face covers exactly the texels its two-chain mapping has a span for (an earlier one-texel fattening drew a flat line under and right of every silhouette); a linear textured face still clamps its texel to its authored bounds |
| composition transparent index 1 | dropped at the fragment |
| reveal, waterline, Digger over the 1× image after the resolve | the same verdicts per texel on the pixel's nearest-sampled key |
| outline endpoints written at 1× after the resolve, key-tested once | each native row's two endpoints, found per pixel by one primitive over the ring's box and drawn as pixel blocks, key-tested once against the pixel's key |
| structure supersample, ALP downscale blending with index 1 | every subject at 2×, resolved in the commit fragment by coverage: an edge or a thin feature is a coverage alpha over what is beneath, never the red/purple fringe [03 R-REN-03A §7]; a mobile subject is supersampled too, where retail draws it at 1× |
| structure shadow punched by body coverage | same punch, both planes resolved from the pages |
| Digger and mobile shadow: the finished body image copied, flattened, clipped, blitted at the ground point five pixels right | the body's own raster read at that placement in the commit fragment; a mobile is never punched, as retail's is not |
| child composed alone, its erased pixels transparent, then `prior > key + delta` keeps prior, wrapped store | child faces in the group region under the same admission with the shifted key; the sum saturates instead of wrapping; construction groups merge finished child pixels (§22.4); other groups still leave a hole where a child clip erases its colour |

A subject no page can hold falls back to painter-order native triangles straight
on the composite (no key, no supersample, no reveal, no children) and its shadow
is omitted; the benchmark never overflows at either view. The fallback fragment
carries an opacity lane, so an overflowed cloaked subject draws the ALP
half-colour (§33) rather than an opaque body. The parameter image grows to what a
frame uses up to 2,048 rows (174,762 slots, a mapped quad taking two); a frame
past it draws its remaining faces linearly. The commit quads bind only the colour plane, so they
share a run with the sprites around them.

**Cached-plane seed.** Ordinary keyed cached unit packets carry an immutable
`ModelCachedSeed` with the source image's dimensions and origin before rebasing.
The final native group rectangle selects the raw or resized seed
[03 R-REN-03A §4]; doubled atlas rounding does not select it. A zero descriptor
keeps direct, keyless and standalone adapters on their existing path.

The key image's red channel remains the effective staging key. For seeded
cached faces its green channel retains the original winning key; resized red
maps key one to zero. This transform is monotone, so the two MAX results equal
resolving cached faces and then copying the key plane. Cached colour still
competes against green, preserving the original winner independently of colour
transparency. Reveal reads seeded red. Outline/live key and colour passes follow
without clearing either plane; live writes leave original green alone and are
never filtered through the resized seed. Packed retained vertices carry no
per-frame seed decision; run phase and uniforms are chosen on each replay.

Seeded group children use independent regions before ordered merges, including
children without construction reveal. Construction groups still skip transparent
children. Ordinary groups preserve their existing shared-key hole policy: a
transparent child with a higher key hides prior colour, while a transparent tie
does not overwrite it. Child key shifts retain saturation. Once the source
key has narrowed to a byte, seeded deltas are reduced to −255…255 consistently
for merging, carrier clipping and reflection admission. Every larger magnitude
has the same saturated result for all source bytes; this also represents the
full difference between two signed height words without a signed-word packing
fallback. Descriptor-absent arithmetic remains unchanged. No shadow source,
current-pose bounds policy, supersample, RGB shading or factory membership rule
changes here. The source origin is retained as immutable metadata; rebased face
placement already supplies its current offset.

This adds no key-plane image or readback. A page containing seeded outline/live
runs takes two additional passes; a page with only cached seeds retains two.
Seeded groups can need additional child regions and merge waves. The pass and
region accounting includes these costs; this contract makes no stock incidence
or performance claim.
The authored device fixture in `model_seed_test.go` measures two versus six
model passes, one atlas page in both cases, 424 versus 536 submitted vertices,
and zero versus 158,976 bytes of merge scratch against a fresh no-descriptor
renderer. This bounds that fixture's added work, not a battle workload.

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
the same operands. A finish or glint switch change (§30) or a source reset drops
the store. This is **CPU-side** retention of preparation work; the atlas regions
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

**Construction outlines.** A nanoframe overdraws every primitive of every
visible piece at its per-row extremes: on each row of the edge walk where the
right chain's column is past the left's, exactly the two pixels at those
columns [03 R-COMP-01 §3][03 R-RAST-01 §1]. The lane used to walk those rows
on the CPU and append two one-pixel quads a row. At the Moon Quartet Survival
capture, with about twenty factories building, that was most of the lane's
vertices, each converted twice by Ebitengine. A ring of three or four corners is
now one primitive: its box in native pixels, drawn at 2× about the native origin
as the endpoint quads were. Its fragment runs the ring's edge walk and keeps
only its row's two endpoint pixels. The ring's data is the key entry of a mapped
quad (§11.2 "Textured quads without strips"), one slot, with the ring's own
keys. A three-corner ring repeats its last corner: the repeated edge has no
rows, so the chains walk the triangle's edges in the triangle's order. The
native origin and a group child's key delta ride the primitive's vertices, so a
carried child's solo image reuses the entries its group composition made.

What must match the CPU walk, and how:

* **Rows and chain choice.** The key entry's edge choice is the walk's: the
  later matching edge of a folded chain wins, and so does the edge table
  [03 R-RAST-01 §1].
* **Skipped rows.** A row whose right column is not past its left draws
  nothing.
* **The packet-box clip.** The box is clipped to the packet's box, which is the
  clip the walk applied to each endpoint.
* **One pixel per endpoint.** Every texel under a native pixel evaluates that
  pixel, so a block's texels agree, and the colour pass tests the pixel's key
  once as before.
* **The key.** The walk's key is a float32 mix along the edge, truncated
  toward zero before the delta applies. The fragment computes the truncated
  exact rational instead, in integers: (key_from·dy + (key_to − key_from)·m) /
  dy, where dy is the edge's rows and m the row's offset down it. No compiler
  rounding can move that. The two agree wherever the rational is not a whole
  number, because the float's error is under (2|Δkey| + max|key|)·2⁻²⁴ with or
  without a fused multiply-add, and such a rational lies at least 1/dy from a
  whole number. The lane requires dy·(2|Δkey| + max|key|) ≤ 2²³. On the rows
  where the rational is a whole number, the float can land either side of it,
  so the lane runs the walk's own float arithmetic there (`outlineEdgeKey`, the
  same float32 operations as the walk) and compares.
* **Order.** Rings append in ring order, each primitive where its endpoint
  quads were, so a key tie with another face or ring resolves as before.

A ring the device cannot draw exactly keeps the CPU walk: five or more
corners, a key that fails either test, corners or keys the entry cannot hold,
or a full parameter image. `DirectOutlineRings` counts the rings drawn as
primitives, `DirectOutlineTexels` the 2× texels their boxes cover (the fragment
work they cost), and `DirectOutlineWalked` the rings walked on the CPU. A solo
image counts its rings again.

At the capture (30 draws a second), the lane's submitted vertices fell from
184.5k to 111.0k a frame at 1× and from 241.5k to 88.1k at 2×. About 977 rings
a frame at 1× (885 at 2×) became primitives covering 0.37M (1.25M) texels, and
`Submit` fell from 3.85 to 2.97 ms at 1× and from 4.64 to 2.46 ms at 2× (means
of three alternating runs against the base). The only rings walked on the CPU
had five or more corners, about seven a frame. The primitives cost fragment
work: at 1× about a quarter of their texels belong to rings that draw no
endpoint and only one in twelve is an endpoint. In GPU-timestamp runs on a
contended host, they added roughly 0.05–0.2 ms to each model pass's fragment
stage and removed some vertex work; the frame's GPU busy time moved by less
than the runs' spread of about ±0.3 ms. Primitives that hug each edge instead
of covering the ring's box would cut most of that fragment work. `battle.png` is
byte-identical at both zoom levels and both draw rates.

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

#### Placement workers

Placement — every subject's regions, its faces fan-triangulated into the batch
and its parameter entries — was the executor's largest CPU term after the
retained store: about 3.3 ms a frame at the 1,600-unit Town & Country field at
zoom 0.75 in the live window. Most of it is per-face arithmetic that depends on
its own subject alone, so the lane places a frame on a pool of workers whenever
it can prove the result is the sequential lane's, byte for byte
(`model_place.go`, `model_place_pool.go`).

**The split.** A frame's placement is a list of jobs — a subject with its group,
or one shadow — each allocated (`allocSubjectJob`, `allocShadowJob`), then each
of its packets **decided** and **filled**. Deciding is the ordered work: the
retained store's and the body index's lookups, inserts, evictions and LRU
order, a key's priming, the append path (replay, capture or plain append), the
warm body and the body a cold append records, and every texture the appends
will reach, resolved through the texture page in the sequential lane's order
together with the page each lane finds (a cold lane can open a new texture page
part-way, and that page decides which faces are standalone). Filling is the
rest, into a placement context (`modelPlaceCtx`): the batch, the parameter
entries, and the per-subject state an append carries from face to face — the
key delta, the lights, the solo pass, the outline plan, the capture and
body-recording slots, the doubled lane's slot box. The lane's own context is the
frame's batch; filling a job into it, deciding inline, is the sequential lane.

**The pipeline.** A frame goes to the pool only when a pre-check proves its
appends cannot interact:

- no two keyed top-level packets share a `(Body, Lane)`, so each store entry
  and body entry a job writes is its own;
- at most 1,024 keyed packets (`modelRetainBodyCap`): touched entries move to
  the front of their LRU, so an insert that evicts never evicts one this frame
  reads or writes;
- the frame's parameter slots — per non-shadow packet one verdict entry, two
  slots for every face of the larger raster and one key entry per outline ring
  — fit the image with two to spare, so every capacity test an append makes
  passes wherever it runs, and a context's slot numbers differ from the batch's
  only by a shift;
- at least 32 jobs, a timing floor.

The placing goroutine then wakes one helper per 32 jobs
(`modelPlaceJobsPerHelper`), at most the pool, and runs the pre-pass,
allocating and deciding the jobs in the sequential order and publishing each
as it is decided;
the participants fill published jobs as they appear, claimed from a shared
cursor, each into its own context, so the fill runs beside the pre-pass. A
sequential layout lays the jobs' output end to end in job order, joining a job's
first run to the batch's last exactly where `colourRun` would, and the pool
scatters each job into the frame's batch while the placing goroutine appends
the recorded face reflections in job order. A frame is one dispatch. A frame
the pre-check rejects is placed sequentially. A frame whose batch exceeds half
of `schedRunVertexLimit` (about four times the heaviest measured) is filled
again, sequentially, from the same decisions: below that bound no run can reach
the limit (a retained segment is at most half of it), so a run's joins depend
on its images and page alone.

**What the scatter rewrites.** A context's parameter slots are one-based over
its own entries, and a job's slot s is the frame's s − q0 + q. The lanes that
name a slot take that shift and nothing else does: a vertex's verdict entry in
`Custom2`, of either sign; `ColorB` of a mapped face (modes 2, 3, 5 and 6, live
or not) and of an outline ring's primitive (mode 7 with `ColorB` above one
half) — the shaders' own tests — and a recorded reflection's quad entry. A
joined first run's indices move by the vertices between the run's first vertex
and the job's. Every slot is an integer far below 2²⁴, so the float sums are
exact. Parameter bytes carry no slot, and a retained entry keeps none
(`finishCapture` stores the block-relative index and clears the verdict entry),
so an entry captured on one context replays on any other.

**Reflections.** The water reflection batch is ordered — its runs join the last
one and its vertex cap depends on every reflection before — so a worker records
each reflected face's call (the face, its landing on the atlas, its centroid,
its quad entry and page, the reflecting subject, its region, a construction
child's group region and its key delta) instead of appending it, and the
placing goroutine appends the records in job order, each quad entry shifted
with its job: the sequential lane's calls with the sequential lane's arguments.

**The worker contract.** A fill reads the recorded list, its packet's decision
and the renderer's read-only state — the lighting sources, the finish
switches, the table atlas, `walkOutlines` — and writes only its own context,
the store entry and body entry its job owns, and, in the scatter, disjoint
ranges of the batch. It resolves no texture and reads no texture page (both
come with the decision), and makes no Ebitengine call. A context counts only
`modelPlaceStats`, the counters an append may touch, so a counter added without
a place there does not compile rather than being lost from the participants'
sum. Anything an append would read that depends on an earlier subject — the
packer, the store, the texture page, the reflection batch — must be decided in
the pre-pass or recorded and replayed in order, or the pre-check must exclude
it. The differential test runs under the race detector.

**The pool.** One participant per `GOMAXPROCS` up to six, the placing
goroutine one of them and the rest parked on one-slot wake channels between
frames — the recorder's unit pool (§13.9). The cap is measured: the pre-pass
publishes the jobs, so participants beyond what keeps up with it only wait,
yielding, and at 1,600 units six placed as fast as twelve (1.29 against 1.26 ms
at 0.75×) for about a quarter of a core less, while four fell behind (1.58 ms). Tallest-first placement order puts the largest
jobs first. Every participant's context is emptied before the wakes, so a
participant woken too late to claim a job writes nothing the placing goroutine
reads. The pool belongs to the `Renderer` and is started on the first frame
placed in parallel. A cleanup stops it when the renderer is collected, which
works because an idle pool holds no reference back to it, so a host that drops
a renderer, or a test that makes hundreds, leaks no goroutine.

**Runs follow their region's page.** A run is stamped with the page of the
region its packet's faces land in (`fillJob`), the page its commit samples. A
construction group's separate child region can open a page after its group
region was placed on the one before, so one job's runs can change page between
packets. Until 2026-09-26 a job's runs took the packer's page once all of its
regions were allocated: such a group drew its carrier into the second page at
the first page's coordinates while its commit sampled a cleared region of the
first, and the factory vanished for that frame. Parallel placement reproduced
it byte for byte before the fix. `TestModelPlaceRunPageIsTheRegionPage` locks
both paths, and the device fixture `checkFactoryPageTurnDevicePixels` draws a
factory building a revealed product alone and straddling the page turn and
requires the same composite (the old stamping drops the factory's plate).

**Page passes beside Replay.** The two page passes hand Ebitengine every vertex
of the frame's batch twice, and converting and copying those vertices was the
largest single cost of a battle frame's `Execute` (about 1.2 ms of 4 on the
coastal battle, 2 ms at 0.75× with 1,600 units). Once placement returns, they
run on a goroutine of their own, with the group merges after them, while the
executing goroutine compiles `Replay` (`prepareModelDirect`,
`drawModelPages`). Their device accounting goes to a `deviceAcct` of their
own and is added to the frame's when they are joined. Every reader of what they
draw joins them first (`joinModelPages`): a scheduled batch that binds a page
(the body, shadow, underwater and aircraft-shadow commits), the atlas fallback,
which rebuilds its faces in the lane's batch, the water reflection draws, the
debug snapshot, source retirement and the end of `Execute`. Ebitengine then has
the passes enqueued before any command that reads them, which is the order the
sequential lane gave it, so the composite is the same; the device fixture
`checkModelPagesConcurrentDevicePixels` draws a frame both ways and compares
pixels and accounting, and the race detector passes over the fixtures and over
live coastal and Survival battles. The water reflection draws, which sample
the pages and the parameter image, are compiled at the terrain pass but issued
when the schedule is next submitted (`flushWaterReflections`), before any batch
that resolves them, so a water battle's compile overlaps the passes too.
`Execute` fell by about 0.3 ms in both battles (3.95 to 3.64 ms at 0.75×,
4.03 to 3.74 on the coast).

Running device calls from two goroutines exposed a dependency the sequential
executor had hidden. A managed Ebitengine image lives at an origin inside a
shared texture, and Ebitengine moves it when a draw into one of its
neighbours finds that texture already read this frame; the scene shader reads
its sources at positions interpolated in that shared texture and floors them.
So at a fractional world scale the ORDER of unrelated device calls rounded
texel fetches differently — interleaving the passes with `Replay` moved about
9,000 pixels of a 0.75× frame, and moving the model shadow commits into the
opaque runs above had moved 150 by changing which image a run bound in slot 0.
Every image the renderer owns is now unmanaged (`newRendererImage`), at origin
zero on a texture of its own, so a fetch depends on its command alone: rest
scales are byte-identical, a 0.75× frame moved once by sub-texel roundings
(93% of the changed pixels within 8 levels), and the concurrent and inline
page passes now give the same pixels at every scale. Ebitengine issued the same
63–64 commands and 31 render passes a frame on the coastal battle either way.

**Verification.** `TestModelPlaceGoldenHash` hashes the lane's whole output —
batch, runs, parameter bytes, regions, merges, accounting, the store and body
index in LRU order with their contents, the reflection batch and the fallback's
triangles — over an eight-frame scenario that takes every path the lane has,
with a coverage check that fails when an edit to the scenario stops taking one.
The hash was committed on the sequential lane before the split, is the same on
arm64 and amd64, and has not moved since. `TestModelPlaceParallelMatchesSequential`
places the scenario on pools of one, two, four and sixteen participants, claiming
jobs as they are published and in a shuffled order, and compares every frame's
output with the sequential lane's; removing any one of the scatter's shifts or
joins fails it. The guard tests cover each pre-check refusal and the vertex
refill, and a flooded scenario fills the reflection batch to its cap part-way
through a parallel frame. `battle.png` is byte-identical to the sequential
lane's at both zoom levels of the coastal scene, every frame of which is now
placed in parallel, and at the 1,600-unit Moon Quartet capture.

**Measured.** In the live window (`--live-trace`, Town & Country
`--live-scene=field:534`, about 1,600 units, 120 fps cap, an M3 Pro with
`GOMAXPROCS` 12), alternating two 40-second runs of each build against the
sequential lane:

| zoom | `place` sequential | `place` parallel | fps | presented within 5% of refresh |
|---|---|---|---|---|
| 0.75 | 3.50, 3.65 ms | 1.32, 1.32 ms | 95.7 → 117.4, 95.5 → 117.6 | 72.6 → 81.5%, 72.7 → 78.1% |
| 1 | 2.95, 2.96 ms | 1.05, 1.05 ms | 113.1 → 119.9, 112.2 → 119.9 | 73.0 → 99.4%, 72.8 → 99.1% |

`execute` fell from 5.1 to 3.4 ms at 1× and from 6.3 to 4.1 ms at 0.75. At 0.75
the pre-pass takes about two thirds of the placement (0.8–1.2 ms in instrumented
runs), the fill finishing after it about 0.05 ms, the layout 0.01 ms and the
scatter about 0.3 ms. The pre-pass is now the floor: most of it is the store
lookups and the warm bodies' topology proof (`modelRetainedBody.matches`), which
are ordered and so stay on the placing goroutine.
Frames under the 32-job floor (the empty opening seconds) stay sequential. In the
coastal battle benchmark every frame is placed in parallel, at both zoom
levels.

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
packer and its page turn. `model_quads_test.go` locks the parameter packing, and
checks the key entry's chain choice, columns and keys at every row of 1,632
rings against a port of the walk and against the CPU outline walk. It also has a
device check that renders 512 quads over tiles larger than themselves, where
the rows neither chain owns are included. Against the walk as it was, it
compares the bits of every lane of the corner entry, read alone and read
together, and of the key entry's key. `model_outline_test.go` checks that the validator's float is the walk's, that
wherever an edge is accepted the device's key is the walk's on every row of a
grid of 734,440 edges, and that a port of the device ring draws exactly the
walk's endpoints for 3,000 random rings. Its device check draws one list twice,
once with the rings on the device and once with every ring walked, and
compares both planes of every atlas page and the composites byte for byte. The
list covers folded, thin, back-facing and clipped rings, rings that tie the
body's key and are tied by a live face, a five-corner ring and a ring whose
float key truncates below a whole number, group children under a raised and a
saturating delta with a solo image reusing their rings, and packets at twice
the size, one with a doubled lane. `model_retain_test.go` holds the retained
store to its contract, with a device
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
2. Non-construction groups retain their shared-key hole behavior: a child
   texel erased by its own clipping can leave a hole where retail shows the
   carrier. Seeded packets resolve separately but preserve that behavior at
   merge. Construction groups use the transparent-child admission below. The earlier claim that only
   completed transport cargo uses attachment was stale: factory products attach
   to the build piece too (`Client.attachedChildren`).
3. The recorder still builds the doubled packet's faces. A packet with a doubled
   lane has its native faces read only by the fallback and the bounds; the doubled
   corners of a direct projection are exact rather than native × 2 plus an offset,
   so the offset alone cannot replace them.
4. The key and colour passes can disagree by one key at a mapped face's texel,
   because the shader compiler rounds the two-chain walk differently where all
   four lanes are read (§11.2 "Textured quads without strips"). A texel where the
   colour pass's key is the lower one is rejected by its own face. Evaluating the
   colour pass's key the key pass's way, or rounding both exactly, would close
   this. Either change moves pixels, so it needs its own visual review.

#### Construction-child composition

**Established (implementation).** A group with a mergeable child carrying a
reveal on either its native or doubled packet gives every mergeable child an
independent atlas region. Cached seeds also require independent regions, with
the separate ordinary-group admission described above. Each child finishes its own reveal, outline and live
lanes before its non-transparent pixels can compete with the carrier
[03 R-REN-03A §4]. This prevents the erased nanoframe interior's key from
rejecting the factory plate. Every child in the affected group takes this path
so a later transparent child cannot erase an earlier sibling, and ties still
admit the later recorded child. Ordinary groups retain the existing path.

The construction path keeps the existing shifted-key saturation and own/carrier clip
verdicts. After all atlas pages finish, each child's expanded rectangle merges
in recorded order under `parentKey <= childKey`, only where the child's
finished colour has nonzero coverage. A child's merged key is written back
only when something still reads it: a later sibling, a silhouette shadow or a
water reflection. The last child of an ordinary land factory with one product
therefore writes colour alone. Child reflection geometry samples its
own finished colour and additionally tests the final group key, preserving both
reveal holes and factory occlusion. No GPU readback or additional full atlas page
is required by the merge itself. Independent child regions can increase normal
atlas packing. The rectangle includes the existing two-texel margin and is
intersected with the parent's allocation, preventing neighbour-slot reads.
Allocation failure retains the existing whole-group overflow fallback.

**Merges run in waves.** Passes are what the device pays for (§11.5). Merging
one child at a time evaluated each merge into a small scratch pair and copied it
back onto its page: two passes per child, or four when its key was kept, half of
them over a whole 4096-square page. A Survival base with about twenty factories
building spent more of a frame's passes on its merges than on everything else
together. On the Metal driver where this was measured (an M3 Pro, macOS 26.6),
presenting blocks until the GPU drains once a frame has more than about 80
render passes in flight, and the windowed present then waits for most of the
frame's GPU work, so CPU and GPU time add up instead of overlapping. Treat about
64 passes a frame as the budget the whole executor shares.

A wave evaluates every one of its merges into a slot of its own in one reusable
scratch pair, then copies every slot back onto its carrier's page in merge
order. It opens one pass for each scratch plane and one for each page plane it
writes, so a wave on one page costs four passes however many merges it holds.
Within a wave every merge reads the pages as the wave found them. That is what
merging in order gives, as long as no merge reads a rectangle that an earlier
merge of the same wave writes. A merge whose parent or child rectangle overlaps
one that an earlier merge of the wave writes therefore starts the next wave:
above all a carrier's next child, which must see what the child before it
wrote. A merge that only writes where an earlier one read stays in the wave,
because in order that read came first too. Two merges of a wave never write the
same texel, and the result is texel for texel the one-at-a-time merge for any
list. Factories that each build a single product share one wave while their
slots fit the scratch, and a carrier's children whose boxes overlap take a wave
each. Slots are shelf-packed about 2048 texels wide, and a wave closes before
its shelves would pass about 2048 texels of height. A larger slot takes a wave
of its own and the scratch grows to hold it. The scratch only grows; source
reset releases both of its planes and preserves the merge shader. At the Moon
Quartet Survival capture (`--benchmark-capture`, 30 draws a second), the last
measured frame went from 64 passes to 24 at 21 merges, and `battle.png` stayed
byte-identical at both zoom levels.

`DirectGroupMerges`, `DirectGroupPixels` (2× texels) and
`DirectGroupScratchBytes` report the extra work and retained logical scratch
storage, and `DirectPasses` includes the passes the waves open. The device
fixture locks preserved plate pixels, visible reveal and outline, factory
occlusion, sibling admission and ties, native/doubled reveal selection, and both
atlas pages. A second device fixture compares every texel of both planes of two
pages with a CPU model of the one-at-a-time merge. Its merge list takes every
branch of the wave rule: independent merges spread over both pages, a read after
a write, a carrier's consecutive children, a write after a read, merges without
a key, boxes that do not meet and a wrapped shelf. A unit test locks the rule
itself, including the height bound and an oversized slot. Optional
`NANOLATHE_FACTORY_CAPTURE` captures
ARM and CORE factory products at early and halfway construction, using actual
committed session/COB poses and relative attachment positions at scales 1 and 2.
Resources are replenished for those diagnostic scenes; retail art stays outside
the repository. This renderer-only correction was visually reviewed with ARM
and CORE factories; gameplay and the retail baseline are unchanged.

### 22.5 Isolated unit viewer surface precision

**Established (implementation); user-authorized viewer policy.** The hidden unit
viewer calls `Renderer.DrawModelPreview` explicitly after composing its backdrop.
No battle command selects this path. Its `ModelPreviewGeometry` retains floating
screen positions and fractional oriented model-relative heights separately from
ordinary `ModelGeometry`, so battle packets, per-vertex storage and the key lane
above are unchanged. The shared collector still supplies texture admission,
palette/team colour, shade rows, normals and material annotations.

The device rasterizes the fractional coordinates at 2× output size. Depth is
the triangle-interpolated model-relative height, normalized over the complete
record's depth range before narrowing to the device float. It is never the
rounded two-chain mapper's key. The quad mapper still supplies texture/shade
lanes, and existing glint and material response apply. There is no name-based
geometry change or bias.

RGBA8 storage holds a non-wrapping 24-bit depth. Three MAX passes select bytes
lexicographically: highest byte first, then the middle byte only from fragments
matching that high byte, then the low byte only from fragments matching both.
Two depth images alternate; maximizing all channels independently in one pass
would produce depths belonging to no surface. One compiled shader and vertex
batch serve all three passes and the colour pass, selected by a uniform, so
their depth expressions cannot acquire different compiler arithmetic. Index-1
texture holes discard in every pass.

The colour pass admits only fragments within 1/1024 world unit plus two code
points of the stored depth. Faces submit in reverse recorded order, so the
first recorded face wins this tiny near-coincident band consistently. This
compensates for independently rounded 16.16 transformed vertices; it does not
claim exact ordering for sub-band separations or arbitrary grazing angles.
The coverage resolve averages four premultiplied samples over the backdrop.
There is no GPU readback, battle state or simulation interpolation.

The preview lazily allocates two depth planes and one colour plane at twice
the bounded canvas dimensions; no full battle atlas is allocated for it.
It reuses the renderer's palette and texture cache, plus private quad parameters
and CPU batches. `ResetSources` releases its planes/parameters and drops source
references, retaining only compiled shaders. Closing the viewer owns this call.
Its real-device fixture runs in the existing GPU gate and checks close sloped
planes, stable near-coincident priority, shared-edge coverage, depth byte carries,
negative/large heights and texture holes. The screen and record contracts are
owned by DESIGN_DEVELOPER_TOOLS §7.

#### Attached model and nanoframe

**Established (implementation); viewer policy.** A projected record can carry
a second model, such as a factory's product under construction on its pad, in
the same output-pixel and depth frame as the previewed model. The API:

```go
type ModelPreviewPlacement struct {
	Position             [3]numeric.Fixed // root-local attachment origin
	Heading, Pitch, Bank uint16           // added to the attachment's root, Y/X/Z
}
type ModelPreviewAttachment struct {
	Model                       string
	Structure, KeyPlane         bool
	PiecePoses                  []frame.PieceView
	HiddenPieces                []string
	Placement                   ModelPreviewPlacement
	BuildRemaining              float32 // 0 complete; (0, 1] nanoframe
	NanoframeID                 uint16  // pulse identity
	NanoframeTick               uint32  // pulse tick
}
type ModelPreviewPiece struct {
	Attachment bool
	Name       string
}

// ModelPreviewOptions.Attachment *ModelPreviewAttachment
func (r *ModelPreviewRenderer) PiecePlacement(model string, poses []frame.PieceView, piece string) (ModelPreviewPlacement, error)
func (r *ModelPreviewRenderer) ProjectedPieces(opts ModelPreviewOptions, projection ModelPreviewProjection, pieces []ModelPreviewPiece) ([]drawlist.ModelPreviewPosition, error)
```

Only `RecordProjectedGeometry` and `ProjectedPieces` accept an attachment;
`RecordModel` and `RecordGeometry` reject it, and the previewed model still
rejects construction, children, cloak, Digger, waterline and `Scale`. The
attachment takes the previewed model's owner. The record's
`Projected.Attachment` (`drawlist.ModelPreviewAttachment`) holds its faces,
`Reveal` and `Outline`; its ordinary packet follows the parent's in `List`
only so list consumers bound both. Without an attachment the record, the
shader source, the vertex batch and the passes are unchanged, byte for byte.

*Frame.* The placement is in the parent's root-local model space, the root
piece's frame before its own transform. The attachment's loaded model is
grafted below a proxy piece holding the parent root's translation and its
folded state, so the shared transform chain carries the attachment with the
parent's root transform and per-axis rounding [03 §2.4] C21, and shaded
normals see the final orientation. `PiecePlacement` returns a piece's
locator chain below the root [04 R-REV-02] and its own turns, which the
battle adds to the factory orientation without folding an ancestor
[04 R-FAC-02 §2]; the root piece places at the root origin with no turn. At
zero view orientation the attachment therefore lands where the battle draws
a product of a factory at heading zero; ARMLAB/ARMPW and CORAP/CORVENG
sessions reproduced the published product offset and heading exactly. Any
other view orientation turns the assembly rigidly, as an orbiting camera
would, so the attachment's own root offset turns with it rather than staying
axis-aligned as a separately oriented battle unit's does. Composing under the
root also applies a script turn of the root itself, which the battle's
orientation copy would not; a census of the reference install's 21 scripts
answering `QueryBuildInfo` found no turn, spin or immediate turn of a root
piece. The parent's pivot, fit and `PixelsPerUnit` frame both models. The
floater sea-level clamp has no water to clamp to, and the signed hang-byte
edge [04 R-FAC-02 §1] belongs to the caller that resolves the script's piece
index to a name.

*Reveal.* The client reuses the battle's reveal and pulses
(`unitNanoframeReveal`, from `NanoframePulse(NanoframeID, NanoframeTick)`)
[03 R-P0-19-N], including the §37.1 host team-colour mapping, which the
viewer's private client leaves off: the viewer shows the stock ramp. Each
attachment corner's key is its whole height above the attachment origin plus
the bias, in the battle's own frame (the local composition at the placement
angles), so the reveal never follows the orbit [03 R-COMP-01 §3]. The device
interpolates keys through the quad mapper's two-chain walk on quads, as the
battle does, and linearly on other faces with a 1/64 bias that keeps a
constant whole key whole; a lane-less face carries its key as
`-(key + 32768)` in the quad-index channel. The key wraps to the stored byte.
The attachment finishes its own image first, as a carried child does: three
depth passes find its top surface and the colour pass applies the verdict
there per 2× sample. Erase leaves the sample uncovered, so the parent shows
through and never the attachment's far side; an index replaces the colour
unshaded and without glint or finish; keep leaves the composed colour.

*Outline.* Every ring of every visible piece, last piece first and without
the selection plate, takes the battle's edge walk [03 R-COMP-01 §3]
[03 R-RAST-01 §1] translated to the viewer raster: retail samples rows at
integer corners and covers columns `ceil(xL)` to `ceil(xR)`, exclusive; the
device samples pixel centres, so each output row is sampled at its centre and
covers `ceil(xL-1/2)` to `ceil(xR-1/2)`. Where `xR-xL` is strictly positive
the two bounding columns are written, as whole output pixels in the outline
pulse; back-facing and empty rows write nothing. The outline is one output
pixel wide at every `PixelsPerUnit`. Each pixel carries the depth plane of the
face's fan triangle at its edge, clamped to the ring's span; it joins the
attachment's depth passes, so it stores its depth as a retail endpoint stores
its key, and is drawn after the reveal.

*Device passes.* The attachment's three depth passes and colour pass, then
the parent's as an isolated record draws them, then one pass that merges the
two per sample by depth, the attachment winning ties as a carried child wins
its carrier's key ties [03 R-REN-03A §4], and resolves the coverage. A
separate compiled shader adds the key lane and verdict uniforms; the merge
pass is its own shader. Two attachment planes are allocated at the planes'
size for the first record with an attachment, and `ResetSources` releases
them with the rest. Battle renderer preference does not select this path: the
viewer shows this look under Classic and Enhanced alike. The look is the
battle reveal's: Classic and Enhanced battles differ only in raster detail.

`ProjectedPieces` returns piece origins of either model through the same
transforms and projection, for an overlay such as nanolathe spray from a
`QueryNanoPiece` piece to the product. Client tests lock the unchanged
isolated record, the battle placement at zero view orientation, the placement
helper, product-local keys, the fraction boundaries (0 complete; 1 erases a
low body leaving the outline; a vanishing fraction keeps it), piece points
equal to drawn corners and the outline's row-extreme rule; with retail
assets, a running ARMLAB session's ARMPW offset and heading match the
composition on the pad its script names. The real-device
fixture locks crossing-plane occlusion, attachment-wins ties, erase showing
the parent rather than the attachment's far side, wrapped keys through both
key lanes, outline depth tests and both fraction boundaries.

## 23. Battle lighting (Enhanced)

### 23.1 Scope and inputs

A user-authorized presentation design, not retail evidence. It adds coloured
diffuse light to model faces and soft illumination to smoke around visible
explosions and weapon impacts, keeping the projection, existing shade,
silhouette, composition key, fog, effect lifetime and simulation unchanged.
Shadows from point lights, terrain relighting from geometry, material masks and
reflections are not part of it. The player's model light switch (§30) is its only
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

**Submerged emission.** A nano particle strictly below sea level over wet terrain
records `NanoSubmerged`; Enhanced skips both its glow and its light contribution.
Its original palette core still draws. This prevents underwater construction from
projecting broad green light through the surface over painted metal deposits.
Particles at the sea plane, above it, or over dry ground keep normal emission.
Admission uses the particle position and terrain, independently of the Water
switch and gameplay mode. No particle lifetime, RNG call or classic pixel changes.

**NL2 — spray glow.** The existing glow source pass receives a palette-coloured
quad extending two world pixels beyond each side of the particle core, at gain
0.16 (first tuned at 0.45 over heavier octave weights; §19.3). The existing two blur octaves resolve it beneath fog and interface. No
shader, render target or extra blur pass is added, and the existing glow switch
controls it.

**NL3 — local lighting.** Before model preparation, visible particles join the
nearest existing cluster within 24 world pixels in unsheared physical space.
Clusters follow the arithmetic mean of their particles' positions, with no
screen-grid snapping. Each particle adds 0.018 times the mean displayed RGB of the
seven-entry nano palette ramp [03 §5.5]; the completed cluster is uniformly
scaled down if its peak exceeds 0.21, and its radius is 80 world pixels. (First
tuned at 0.06 and 0.7, which washed factories and shipyards white; the
nanolathe family of §19.4 scales both.) At most
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
construction sources. The model and ground light switches remove explosion and
nano light from their own receivers; glow remains independent.

### 23.6 Community team-coloured nanospray

The Community patch's `TeamColorNanolathe` host preference selects the
stream and nanoframe palette lists in both renderers. The mapping and its
Enhanced lighting input are specified in §37.1.

### 23.7 Metallic glint

A small directional highlight using the existing outward face normals. One fixed
unit half-vector, `(-0.35, -0.15, 0.9246621)` in world X/Z/height axes, defines
an artistic overhead key; the clamped normal dot product is squared five times
(power 32), once per rendered face. There is no camera position, clock, RNG,
per-pixel normal, point-light loop or additional geometry: rotating panels change
their response and a stationary panel keeps its highlight.

The weight is **anchored at an overhead face**. An upward normal's own lobe,
about 0.08, is subtracted and what lies above it is rescaled to 0–1, so a face
pointing straight up catches nothing and only faces turned toward the key are
lifted. The palette colour is what the authored art already shows from above,
and the overhead camera mostly sees such faces: the unanchored lobe gave every
flat roof that 8% floor, 2–3 luma levels over a whole CORCA construction
aircraft on top of its real highlights (measured 2026-09-28).

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
player's **Glint** switch (§30) turns it on or off everywhere; it is independent
of the Finish switch, which owns §29.1's finishes alone.

Content controls it by texture name: the `[glint]` section of the annotation
file of §29.1 sets a textured unit face's glint as a whole percentage of the
tuned weight, 0..200, clamped; an absent texture, untextured faces and
features keep 100. `ModelFace.Glint` carries it, resolved at texture bind with
the finish class and invalidated by the same generation; zero is the tuned
glint and otherwise `Glint-1` is the percentage, so a zero value keeps today's
look. The executor multiplies the face's weight by it and clamps the product
to one before packing, so a strength above 100% saturates rather than
spilling into the material bits. The glint is independent of the finish
class: a pack may keep a texture's metal finish and remove its glint, or the
reverse. The embedded `[glint]` section is empty.

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
ring count. The **blastRings** switch (§30) turns the rings on and off, and the
player's **blastRingStrength** percentage (§30) multiplies every admitted ring's
displacement strength on top of the shape above — the read region's pad follows
the scaled strength, and 0 admits no ring.

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
do not affect the visual. This is the one blast shape; the player's blast ring
switch (§30) decides whether any wave is drawn. The admission rules, including the
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
surface and admits no reflection site. Its motion, foam and reflections parts
each remove their own share.

### 26.1 Public API and ownership

`frame.WindView { Heading uint16; Strength int32 }` and `Frame.Wind` copy the
session wind at publication [01 §7.3][I6]; reset clears the value, and the
renderer never reads the live wind service. `drawlist.WaterSurface { Enabled;
Tick; Fraction16; WindHeading; WindStrength; DriftX, DriftZ, Energy; TidalDriftX, TidalDriftZ }` is the
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
producer uses authored `CanHover`, COB wake routines and committed piece poses;
the wake routine's pieces are linked to the model exactly as the unit's own
script is `[04 R-COB-01 §4]`, so an emitter beyond the model emits nothing; hidden,
carried, airborne and unfinished units have no active visual wake script;
every emitted mark starts at a player-visible position, and existing fog
composites cover the batch. A bounded ring holds the recent spray. Land emissions
require changed X/Z positions across consecutive eligible committed observations;
stationary hovercraft emit nothing, while previously emitted specks finish fading.
Hover bob and turning in place do not count as travel. Grounded mode admits
hovercraft without comparing model Y to the
centre terrain height, because the four-corner conform can differ from that sample
[04 R-MOV-01 §5]. Lava receives no water foam; acid admits building rings but no shoreline foam. Building foam also excludes
cloaked units. Its surface test uses the canonical committed model transforms and
hierarchy visibility [03 §2.4], excludes selection faces, unused vertices and
non-drawing primitives, and requires one visible face to span the sea plane
inclusively. Static definition bounds can include hidden or retracted geometry;
they cannot prove a building intersects the surface. Missing models and entirely
submerged poses emit no foam. Mobile units do not feed building foam; Enhanced
hover dust remains restricted to dry ground, and terrain waves depend only on
terrain, tidal strength and wind direction. Retail script sprinkles keep their existing draw and lifetime.


GPU ownership is `water.go` and `water_reflections.go`. A conservative water/shore
mask is cached in painted map coordinates through the terrain inverse projection
[07 §8][03 §2.5]; a negative height sentinel is never water; painted colours are
preserved; the treatment draws before objects and fog, except admitted seabed
sprites are part of its terrain input; wake fragments clip to the
matching wet/dry mask. No postprocess displaces units, HUD or fog; the submerged
part of a blue-tinted hull is refracted inside its own commit (§26.5). Cache resources
are released on map replacement and disposal.

`BuildWaterMask(*world.Terrain) *PreparedWaterMask` runs the existing mask
construction entirely on the CPU and returns an opaque immutable result bound
to the terrain pointer. `WaterMaskInputs` fingerprints everything the mask
reads — the cell grid, the sea level, the lava flag and each plot's height and
void state — and `ForTerrain` binds a built mask to another load whose
fingerprint matches, sharing its pixels; feature placement never moves the
fingerprint, since only the void sentinels of the feature field are read. A staging worker must exclusively own the terrain while
building it, after any lead-in that changes its plots. The result may cross to
the render thread, where `Renderer.PrepareWaterMask` only installs its geometry
and block index and uploads its pixels. The renderer retains no prepared pixel
storage; the caller may release the result after upload. Matching terrain
identities reuse the installed mask, replacement releases the previous image,
and `ResetSources` drops the mask and terrain reference (§2.3). Draws still record
the current projection and use the ordinary lazy construction when preparation
was not supplied. Preparation leaves all mask channels, resolution bounds and
shoreline calculations unchanged (§26.3, §32.3).

### 26.3 Surface treatment and cost bounds

**The mask** is one RGBA image per terrain identity: red liquid coverage,
green inward shore distance, blue valid dry ground (1) or void liquid (0.5),
**alpha the damp band's ring term** (§32.3). Water and acid require terrain below
sea; lava also admits terrain exactly at the liquid level, including flat
height-zero pools on zero-level maps. Invalid terrain belongs to neither medium.
Shore foam is suppressed for damaging water, lava and topologically void liquid;
this does not depend on the selected unit's depth or slope limits. Lava retains
painted colors without blue highlights, cyan shallow tint or wet-edge darkening,
and gains neither model reflections nor building rings. Acid retains water tint,
damp edge, reflections and building rings. These are renderer policies only;
damage, pathfinding and all gameplay remain unchanged.

The mask starts at one painted map pixel per texel and doubles
that step until its largest side is ≤2,048 texels (≤16 MiB of GPU pixels). Two
integer chamfer sweeps approximate distance up to 32 world pixels; a separable
nine-tap blur smooths the distance channel without changing wet/dry labels, its
sample spacing at least two world pixels so height-grid corners round even on the
finest level. Bilinear sampling softens the mask while conservative coverage clips
foam and dust.

**The distance is measured from a rounded coast, not from the strict wet set**
(`roundShoreline`). Terrain height is a sixteen-pixel grid, and where a beach is
steep the sea-level contour of its bilinear surface hugs the cell edges, so the
strict boundary is a staircase; the nine-tap blur is far too narrow to round
sixteen-pixel steps, and the wave fronts and the shallow tint drew them. Three
box passes approximate a Gaussian of about eight world pixels over the binary
water reading, and water reading under three quarters is dropped from the
distance source. Three quarters is the reading at the tip of a square dry
corner, so the rounded coast passes outside every such corner and about six
world pixels off a straight shore. Red stays the strict wet set, because wakes,
reflections, the ring term and aircraft shadows gate on it. Every near-shore
term in the shader rises from zero at the rounded coast, so the strict boundary
is never the edge of anything drawn; the painted water between the two is left
as authored and reads as the shallows. A future height-editing path must invalidate this cache as well as
the painted terrain sources.

A 128-pixel block index skips the water pass when no water intersects the view,
plus one block of slack a side so a coast just past the viewport edge still runs
the pass for the damp band (§32.3). Otherwise the scheduler copies the terrain
composite and applies a viewport water shader before objects. Every constant in
that shader is an authored presentation choice.

**The selected surface.** The fixed hybrid treatment combines current-driven
translation with bounded in-place deformation. The chosen values are pattern
size 0.5, surface opacity 0.5, current multiplier 3, ripple deformation 5,
shore/building foam opacity 0.6, and an 11.2-map-pixel inward edge fade (the
selected 0.7 factor in 16-pixel units). Distortion, contrast, tint, damp edge,
foam width, building ring size and both animation clocks retain their original
unit multipliers. There is no tuning command, mode selector or preset file;
the water surface, motion and foam switches (§30) control the treatment, each
its own lane of the one pass.

The edge fade uses the existing rounded distance field and does not expand
water onto dry terrain. It softens disagreement between height and painted
shorelines without claiming to reconstruct the painted boundary. Surface opacity
scales the surface treatment and damp edge, but not reflections or foam. Shore
foam retains its original world-space pattern, clock and wind-energy response,
then applies its own opacity and the common shoreline fade. Building rings retain
their size and original phase with only their opacity scaled.

| Layer | Travel | Deformation |
|---|---|---|
| broad ripple | 6 × the scaled integrated tidal drift | bounded local warp |
| fine ripple | 11 × the drift | same warp, lattice rotated a fixed 37° to avoid aligned grid rows |
| coarse patch | 22 × the drift | spatially varying ripple contrast and displacement |
| local warp | no directional scroll | two stationary noise phases drive sine deformation at 0.65 and 0.83 radians/second, amplitude 2.5 per component |

All noise coordinates use map position divided by pattern size before drift is
subtracted, so size 0.5 also halves apparent travel in map pixels. Keeping this
order preserves the selected appearance. Surface energy is fixed at 0.5 and
ignores wind strength; wind energy still affects the original foam and reflection
paths. Six value-noise evaluations contribute to a wet pixel (coarse patch,
two warp phases, broad, fine and shore patch). Bilinear terrain sampling prevents
subpixel displacement from snapping. The edge fade reuses the distance sample;
no extra texture or full-screen pass is added.

**Current response.** `water_motion.go` observes committed wind heading through
its negative sine/cosine direction [R-WIND-01]. Base current speed is
`max(0, tidal)/20`, with non-finite tidal values treated as zero. The shader's
current multiplier 3 gives `0.15 × tidal` before layer multiples and pattern size.
Direction eases along the shortest arc by 1/90 of the remaining angle per tick:
a roughly three-second response, about 95% settled after nine seconds. The unit
direction preserves tidal-derived speed through turns and reversals. Integrating
it preserves pattern position; the lattice never rotates with a wind reroll.
Tidal zero retains local ripples with no directional current.

The original wind velocity and energy accumulator remains separate for foam,
reflections and aircraft shadows, with its existing 1/90 response. Recording
interpolates previous/current presentation drift. Repeated ticks do nothing;
source/renderer changes, tick rewinds and gaps over 300 ticks reset the state.
The value-noise hash wraps lattice corners at a 289-cell period to avoid
unbounded hash arguments; neither time nor integrated position is periodically
reset, which would jump the pattern.

**Authored tidal range checked locally.** Of 54 installed ordinary-water maps
with skirmish schemas, 49 use tidal 15–30; the outliers are Evad River Confluence
(0), Lake Shore (3), Trout Farm (7), Brilliant Cut Lake (10), and Metal Isles
(32). Across campaign and skirmish ordinary-water maps the observed range is
0–48; the high extreme is cc09. Installed active-acid maps use 20 except Acid
Pools (23); lava maps use 0 or 20. This is a local content census, not a promise
about all mods. No minimum-current floor or maximum tidal clamp is imposed.

**Submerged ground sprites.** Fully submerged, nonblocking static sprite features
with authored height below 10 are recorded as `Sprite.SubmergedGround`, including
their shadow/body pair. Burning/runtime features, models, tall/blocking objects,
partially submerged objects, lava, Original and disabled Water keep their normal
ordering. This admits the installed short AquaOre decals while excluding metal
towers. The executor gathers the admitted sprites into reused storage and draws
them after terrain, before the existing water pass, skipping their later replay.
The input already passed normal feature visibility; fog still composites afterward.
No additional screen-sized target or water pass is needed. It adds a sprite-list
scan and may change batching. Replay clears borrowed frame references afterward;
a view with no active water pass retains normal sprite replay.

**Particles.** History is bounded to 8,192 marks and 4,096 tracked unit
identities. Hover-spray age and building-foam ring phase both add the presentation
fraction, as scorch and blast ages do, so they advance at display rate rather than
stepping at 30 Hz on a faster display.

| Mark | Admission | Emission and life |
|---|---|---|
| land hover spray | the producer rules of §26.1 | authored COB wake timing and two-vertex emitter directions; two scattered 2×2 white specks per emission, half a world pixel of travel per tick, 48–64 ticks from opacity 0.45 with quadratic decay; each sample projects onto its current dry terrain height, and the shader clips fragments to dry ground |
| building foam (`water_buildings.go`) | a visible completed floating building on wet terrain, a visible posed polygon touching or crossing the sea plane, its committed base height equal to sea minus authored waterline [05 "Geothermal requirement"] — **not** the FBI `Floater` flag, which stock water-yard buildings such as tidal generators do not set | broken elliptical ripples, bounded to 1,024 visible rings; two staggered rings expand and dissolve inside each quad so the footprint is not outlined as a square. This approximates displacement around the base, not the model's waterline intersection, and the shared mask clips it to water |

**Nanolathe Modern renderer policy — land air cushion.** The stock scripts
suppress wake emissions in land occupancy band 4; retail water sprinkles also die
on land [03 R-WATER-01 §1][04 §9.1]. `SetHoverScripts` installs immutable catalog
programs at battle binding. Each visible, complete grounded hovercraft on dry
terrain gets an isolated presentation VM, capped at 4,096 instances. It invokes
only `setSFXoccupy(2)` and `StartMoving`, then advances once per committed tick.
It runs neither `Create` nor other engine callbacks and binds no gameplay ports,
explosion sink, transport mutations or simulation RNG. Only SFX 2–5 are collected
(up to 256 cues per VM visit); all other output is discarded. The isolated
`NewPresentationVM` also stops a thread after 4,096 instructions without yielding,
so a mod routine awaiting `Create` initialization cannot block the renderer.
Authoritative VMs keep their established uncapped execution [04 §4.2]. Script timing,
emitter identity and reversed types 4/5 remain authored. The twelve ordinary
installed hover scripts sleep 300 ms between wake bursts; ARMAMPH sleeps 250 ms.
This narrow visual use does not claim to reproduce arbitrary mod wake routines
that depend on `Create`, random values or gameplay queries.

The committed unit pose supplies each emitting piece's two transformed vertices;
the visual VM's transforms never alter the displayed unit. The two endpoints
follow [04 R-COB-03 §6], including model-Z negation. A zero-length segment or
missing script/model/piece produces nothing. The isolated wake routine starts
on first eligible observation to establish its position and authored cadence;
that first observation emits nothing. Subsequent cues are admitted only on ticks
with actual horizontal movement. At rest, cues are discarded rather than queued
for a later burst; the routine retains its cadence, since stock `StopMoving`
callbacks do not necessarily stop it. It retires when hidden,
carried, airborne, incomplete or over water. Viewer/source changes and tick
discontinuities clear all history. No distance-based fallback remains. Scatter,
life and opacity are artistic constants; variation uses only a cue-local integer
pattern. The independent **Hovercraft land wash** switch controls this existing
dry spray, separately from the four water treatments, and the renderer control
applies independently of gameplay Mode, so selecting Strict 3.1 does not
change this renderer preference. Neither simulation stream, live COB state, resources nor retail water particles change.

**Independent dry wash preference.** `presentation.hovercraftLandWash` is an
On/Off renderer preference, default On. Its Effects card uses the dry hovercraft
scene shared with Metal and compares the same recorded frame with the dry wash
on and off. `Effects.HovercraftLandWash` is mapped by
`presentationEffects` / `storeEffects` and applied by `Renderer.SetEffects`.
Only this switch admits and records dry hover marks; the executor independently
rejects the `SurfaceWake.Dust` lane with it off. WaterFoam continues to own shore
and building foam and the `SurfaceWake.Foam` lane. Ordinary authored wet spray
keeps its existing draw and lifetime (§26.1). The Water shortcut includes only
its four water switches and leaves land wash unchanged.

Off observes no dry-wash history. `Client.SetEffects` retains the existing
selection-change retirement, so re-enabling starts the visual routine at the
current tick without replaying old specks. Wash alone enables the shared static
wet/dry mask without observing water motion; even an entirely dry map with no
visible water retains that receiving mask. No particle geometry, shader tuning,
script cadence, simulation state or RNG changes, and the default appearance and
Classic pixels remain the same. Omitted settings keep On and explicit zero stays
Off; a negative value repairs to On. The retired `water: 0` umbrella also turns
wash off during migration, preserving that earlier explicit choice; current
`waterFoam: 0` leaves an omitted land-wash field On.

Tests cover foam Off/wash On and the inverse, history retirement on re-enable,
the dry-only mask and device rendering, unchanged default pixel restoration,
settings round trips, graphics preset scope, mod-lock paths and saved Apply.

Verification covers script timing, moving/stopped/resumed output, authored/reversed emitter
geometry, eligibility and reset boundaries, bounded storage, and fractional fade.
The device water fixture checks that land specks never darken terrain or cross
onto water. Installed-script fixtures exercise both hover script families.

### 26.4 Above-water screen-space reflections

`ModelGeometry.ReflectWater` admits a visible body whose origin is over valid
water or acid; `ReflectionSea` is absolute sea height in recording-scale pixels,
alongside `WorldHeight`. Vertex `Height` remains physical relative height,
supersampled geometry included. `Sprite.ReflectWater/ReflectionHeight` and
`Line.ReflectWater/ReflectionHeight0/1` carry signed above-sea height for admitted
projectile bodies and endpoints. Ground-shadow sprites do not opt in; classic
ignores these value fields; cloned lists retain them.

The source pass draws in record order, one device draw per run of faces that can
share bindings: the model faces of one atlas page (a face that tests no group key
joins a run binding one), and billboards and strokes together per scene atlas
page. A billboard samples its frame's scene atlas placement (§11.2), with the
frame's own rectangle carried in its vertex lanes; a sample outside that
rectangle reads transparent, exactly as a texture of the frame alone did, so the
page's padded edge is never reflected.

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
approximation. Shore foam has its own selected opacity (§26.3).

### 26.5 Underwater refraction

Retail tints the part of a model at and below the waterline through BLUE TABLE
when the viewer holds sonar contact, and erases it otherwise
[03 R-WATER-01 §2]; the tinted part then sits on top of a water surface that
refracts the seabed around it. In Enhanced with the water motion switch on,
that part is refracted by the same field, and shaded by it while the water
surface switch is on too (§30). This is authored presentation: the tint, the
erase, the key test and every simulation value are unchanged.

**Field.** `water_field.go` owns one Kage definition of the surface field —
the noise lattice, `waterField` (broad and fine lattices and the gust
envelope), `waterOffset` (the displacement in world pixels) and `waterShade`
(the ripple shade and crest highlight). The water pass and the underwater
commit both splice it in, so a hull moves in phase with the water around it.

**Marker.** The model lane's colour pass writes a texel the blue clip claimed —
the subject's own on its key or the carrier's on the shifted key (§22) — at
alpha 254/255 instead of 1. Every coverage reader tests alpha against 0.5, so
nothing else changes. The colour pass draws with a copy blend and discards its
transparent fragments; every fragment it keeps is opaque, so this stores what
source-over stored, and keeps the marker exact where two faces tie on a key.

**Commit.** A subject whose waterline mode is blue, with no wreck emission, on a
non-lava map whose water phase is recorded, while the water motion switch is on,
commits
through scene op `sceneOpUnderwaterCommit` instead of the plain resolve. Its
page binds slot 2 as the ordinary commit does and the water mask binds slot 3,
which no model command uses, so the subject joins the open opaque run and adds
no device draw. The frame's phase, tidal drift and mask step are the
`UnderwaterWater` uniform in the scene runs' one shared map (with the aircraft
shadows' `AircraftWater`, §34). Per pixel:

- texels at full alpha resolve in place exactly as the ordinary commit;
- marked texels are gathered at the pixel's position plus the water offset,
  scaled by the water pass's own depth ramp and coast fade and by
  `modelRefraction` = 0.5 (hull silhouettes read the field far more strongly
  than painted terrain), with an area-weighted two-texel box over three
  texels per axis so the hull slides rather than stepping a texel at a time;
  taps outside the subject's page rectangle are transparent;
- the gathered colour takes the water's shade and crest at the surface
  opacity while the water surface switch is on (the vertex's third custom
  lane), and fills whatever the above-water texels leave uncovered.

The quad is padded by the bounded offset (`underwaterMaxOffset` × 0.5, 1.32
world pixels) plus one, so displaced edges are not clipped. Erased hulls, lava,
Original and water motion off take the ordinary commit. Shadows of submerged subjects
and glow do not follow the displacement.

**Verification and cost.** `checkUnderwaterDevicePixels` locks, on a device,
that the marked part changes with the phase, the above-water quarter is
byte-identical to the ordinary commit, every change lies inside the padded
bound, an erased hull is untouched and the draw count is unchanged. In the
battle benchmark (about ten underwater subjects a frame) device draws stayed
at 120 and Submit, Record and DrawWork medians stayed inside run-to-run noise.

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
skips the metadata render and retains the single resolve quad. The metadata
render draws only the triangles, of any source, whose bounds widened by the same
reach touch a marked cell, in record order: only marked cells read the buffer and
their reads stay within that reach, so the omitted triangles cannot change a
pixel. In the coastal benchmark that is about an eighth of the mesh. The buffer
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
counts the submitted plumes. The **fireShimmer** switch gates
the plumes, independently of the blast rings (§30). Preparation copies only scalar geometry, clip, time
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
Two switches own it (§30): **wreckGlow** the cooling emission colour, and with
it the wreck light that borrows the colour (§31.1), so a wreck emits no light with
that switch off even if the light switches are on; and **wreckShimmer** the plume.
The recorder writes the emission only under the glow and the plume's strength and
clock only under the shimmer; the view scale both read (the plume's size, the
light's reach) is written under either. The executor gates both as well: its
plume admission follows the shimmer switch, and with the glow off it composes no
recorded emission on the body and lends the battle light none
(`Renderer.wreckEmission`). The executor half matters because a host may keep
its recorder on every effect and apply the player's selection to the executor
alone — the Nanolathe screen's preview does, so its histories survive a compare —
and a recorder-only gate left the preview's fresh wrecks red with the switch off.
The explosion and fire art of the death itself is the content's own and follows
no wreck switch.

## 29. Metal/paint finishes and fading scorch marks

All coefficients and texture annotations here are authored presentation choices,
not retail material or temperature evidence. Unit emission/halo and brief
dust/spark/water impact accents were rejected and are not part of this. Existing
glow, blast art, coastal wakes and §28's wreck treatment keep their own
contracts.

### 29.1 Materials

A curated texture-name table annotates textured unit faces as default, metal or
paint. It does not classify feature or wreck faces. Untagged faces retain their
existing shading. The team-colour panel textures `colorslt`, `colorsmd`,
`colorsdk` and `colordk2` use the metal finish for every player frame; team logos
retain their existing classification.

Both finishes are **anchored at an overhead face**, like the glint of §23.7. A
face's response `r` is its outward normal's dot product with the §23.7
half-vector, clamped to 0–1 and rounded to sevenths; an upward normal gives 6/7,
and a face at that response keeps its palette colour exactly. The palette colour is
what the authored art shows from above, so a finish moves light between
orientations instead of adding it: faces turned toward the key brighten and
faces turned away darken.

* **Metal** scales the colour by `1 + 0.42·(r⁴ − (6/7)⁴)`, from about 0.77 on a
  wall to 1.19 facing the key. A metal's reflection takes its own colour, so the
  scale keeps hue and saturation and a team panel keeps its team shade. Above the
  anchor, a neutral face — the glint's saturation mask of §23.7, one minus a
  smoothstep from 0.2 to 0.65 — tints the brightening with the cool sky colour
  (0.72, 0.84, 1.0) scaled to unit luminance (0.876, 1.021, 1.216), so bare
  steel catches a cool highlight without it adding light.
* **Paint** scales by `1 + 0.12·(r² − (6/7)²)` and adds a 0.075 white sheen
  times `r² − (6/7)²` only above the anchor; authored hue stays legible.

The first finish added its cool lobe on top of the palette colour, largest on
the faces the camera sees most. It raised the metal faces of the stock
commanders and CORSOLAR by 15–27 luma levels on average and pushed the light
`colorslt` team shade toward white: an ARMCOM strip the classic executor draws
as [145, 172, 212] read [223, 255, 255] with the glint. Anchoring replaced it on
2026-09-28 after a tester reported washed-out units. The metal faces of the
same models now average from 16 luma levels darker to 9 brighter than classic,
darker the more of them turn away from the key.

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

The same file carries each texture's glint strength in a `[glint]` section of
whole percentages (§23.7). Like `[materials]`, a `[glint]` section replaces
the glint table whole and a file without one keeps it; the embedded file
carries an empty section, so every load starts from the tuned glint.

The same file carries a content pack's effect strengths: an `[effects]` section
of `weapons=`, `nanolathe=`, `ground=`, `footprints=` and `tracks=` whole percentages, 0..200, default 100
(§19.4). A file may carry any of the three sections alone: one without
`[materials]` keeps the texture table in force, one without `[glint]` keeps
the glint table in force, and one without `[effects]` keeps every family at
100.

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
**scorch** switch owns it (§30); `ModelStats.MaterialFaces` and `ScorchQuads`
report the submitted workload.

### 29.3 Verification

Device readbacks verify material key, reveal and waterline behavior, unchanged
model submissions and image allocation, and compatibility with §28's independent
wreck composite. The anchor has its own readback: an overhead saturated metal
face, an overhead paint face and an overhead neutral face under the glint keep
their exact pixels, a metal face turned away from the key darkens untinted, and
a neutral one turned toward it brightens with a cool cast. Scorch checks cover
monotonic fading, exact disabled-output equality at expiry, zero expired
submissions, object/HUD and wet masking, cloned replay, view scale, reset and
bounded public batches. Staged impacts observed
through 511 ticks must leave images byte-identical to the disabled output once
every mark expires.

## 30. Player controls for Enhanced effects

The Enhanced effects reached this point as prototypes with executor comparison
switches, environment variables, or nothing a player could reach. **Sixteen
persisted switches** now cover them, with seven amount percentages — the
trail strength of §15, ground light and blast ring strengths below, three glow
source amounts of §19.4 and the aircraft shadow softness of §34 —
beside the glow switch and strength of §19.4. They are Nanolathe presentation preferences, not retail
evidence: the classic executor composes identical pixels whatever they say, and
nothing here is visible to the simulation or to any committed frame [I6].

**Every switch is independent and toggles exactly one visible treatment.** No
switch contains another, and none is off because some other switch is. An
earlier round had four family masters (`water`, `lighting`, `distortion`,
`marks`) that each turned off their own treatment *and* all their parts; that
read as confusing next to the parts themselves, so the masters are gone. The
master's own treatment became a switch of its own where it had one (`water` →
`waterSurface`, `lighting` → `modelLight`) and vanished where it had none
(`distortion`, `marks`). The combined `hotWrecks` switch split the same way,
into `wreckGlow` and `wreckShimmer`. The family is kept only as a shortcut on the battle
options page and the message line, described below; it is never stored.

`settings.Presentation` stores each switch as an integer defaulting to 1. They
are integers for the same reason the display bits are: a stored 0 is "off" and
is kept, only a negative value is repaired, and a file that omits a key keeps
the default because the loader decodes over the defaults [02 "Settings"].
`internal/drawlist.Effects` is the value type both sides read, with one `bool`
field per switch, plus integer source amounts and shadow softness;
`drawlist.AllEffects()` turns every switch on and sets those amounts to 100. `cmd/nanolathe`
converts the stored integers one field for one field (`presentationEffects`,
and `storeEffects` back), so `internal/settings` remains a leaf.

| Key | Recorder gate | Executor gate |
|---|---|---|
| `waterSurface` | seabed decal promotion (§26.3), shared with `waterMotion` | the surface pass's shading lane: ripple shade and crest tint with their depth ramp, the damp shoreline band and the shallow tint (§26.3, §32.3); the water's shade over a refracted hull (§26.5); the seabed decal replay, shared with `waterMotion` |
| `waterMotion` | seabed decal promotion, shared with `waterSurface` | the moving variant of the surface shader instead of the still one (field, drift, displacement); the underwater refraction (§26.5); the reflection ripple and vertex waves (§26.4, §26.6) and the wet aircraft shadow's waves (§34), held at phase zero when off |
| `waterFoam` | building foam (§26.1, §26.3); ordinary wet spray keeps its authored path | the surface pass's shore foam lane and wet foam marks |
| `hovercraftLandWash` | dry hover spray admission, history and batch (§26.3) | the existing dry `SurfaceWake.Dust` mark lane |
| `waterReflections` | reflection site admission for models, projectiles and explosion art (§26.4, §26.6, §32.2) | the reflection source pass and resolve |
| `modelLight` | none: lighting kinds are always recorded | the battle light on models and smoke (§23, §31.1) |
| `groundLight` | none | the ground pool pass (§31.3) with the short terrain flash (§31.6) |
| `groundLightStrength` (a percentage) | none | multiplies every pool's terrain gain; 0 skips the ground pass as `groundLight` off does |
| `weaponGlowStrength`, `explosionGlowStrength`, `nanoGlowStrength` (percentages) | none: source tags and spray histories stay recorded | independent source multipliers of §19.4 |
| `finish` | none: face material and normals are always recorded | the metal/paint finishes (§29.1) |
| `glint` | none | the metallic glint (§23.7) |
| `blastRings` | blast ring metadata (§25) | ring admission to the shared distortion batch |
| `blastRingStrength` (a percentage) | none | multiplies every admitted ring's displacement strength; 0 admits no ring as `blastRings` off does |
| `fireShimmer` | the burning-feature heat tag (§27) | vegetation plume admission to the same batch |
| `wreckGlow` | the fresh-wreck cooling emission colour (§28), which the wreck light borrows (§31.1), and the arriving commander's glow (§36) | a recorded emission is neither composed on the body nor lent to the wreck light (`wreckEmission`), so the switch holds for a host that gates the executor alone |
| `wreckShimmer` | the fresh-wreck plume's strength and clock (§28), and the arriving commander's rising air (§36) | wreck plume admission to the same batch |
| `scorch` | the scorch observer and draw (§29.2), the arrival landing scar that shares the layer included (§36) | the scorch layer |
| `softShadows` | the aircraft clearance of §34; off, it records zero | the soft shadow commit; off, the ordinary silhouette route |
| `shadowSoftness` (a percentage) | none: clearance stays recorded | scales the aircraft filter radius and its expanded bounds; zero takes the ordinary silhouette route (§34) |
| `supersample` | the doubled lane of §17.2 (`supersampleGeometry`); off, none, as with the Anti-Alias option off | the single-sample resolve of every model commit (§17.5); on, the coverage resolve |
| `trailStrength` (a percentage, not a switch) | the trail layer (§15): its observer and draw run while it is above zero | none: the layer draws what was recorded |

**Shared inputs.** Two things several switches read are produced while any of
their readers is on. They are named by `Effects.WaterPhase` and
`Effects.SeabedTreated` so the recorder and the executor agree; neither is a
switch.

* **The water phase and the water-motion history** (§26.1) are recorded while any
  water switch is on. Every water treatment reads them — the surface shading's
  damp-band pulse, the moving field, the shore foam's clock and wind energy, the
  reflections' ripple and softening — so with all four off no dynamic phase
  is recorded and no water pass opens. Land wash alone records
  `WaterSurface{Enabled: true}` for the shared receiving mask, with no tick,
  wind, drift or motion observation. The mask is built for valid terrain
  even on entirely dry maps with no visible-water blocks.
* **Seabed decal promotion** (§26.3) runs while the surface pass shades or moves
  the seabed: `waterSurface` or `waterMotion`. Promotion is an ordering, not a
  treatment — it puts short submerged decals beneath the pass so the pass treats
  them with the terrain they lie on — so it follows the switches whose lanes treat
  them. Foam alone leaves the decals in their ordinary order.
* **The battle light gather** (§23, §31) runs while `modelLight` or `groundLight`
  is on. Model light off hands models and smoke no source; ground light off draws
  no pool. With both off the gather is skipped.

**Strengths.** `groundLightStrength` and `blastRingStrength` are percentages of
the tuned look, 0–200, default 100, stored beside the switches and repaired like
the trail strength: a negative value takes the default and anything above 200 is
capped. They are Nanolathe presentation choices, and the curve is **linear**: the
executor multiplies by `percent / 100` (`effectStrengthOffset`, with
`EffectStrengthDefault` = 100 and `EffectStrengthMax` = 200). The ground strength
multiplies each pool's terrain gain after the family share and envelope of §31.6
and §31.7 and the content pack's ground family (§19.4), so models, smoke and the
glow layer are untouched. Section 31.8 applies a bounded nonlinear response
after the linear energy multiplier; doubling strength need not double the
visible brightness increase. The ring strength multiplies each admitted ring's displacement
strength after the shape of §25 and §25.2 — radius and width are unchanged, and
the read region's pad and the 32-ring budget both use the scaled value, whose
order a common factor does not change. At 0 either is off exactly as its switch
off is (the ground pass is skipped; no ring is admitted), and at 100 either
composes the tuned look byte for byte. The switch stays the on/off; the strength
is not a switch, is not part of `drawlist.Effects`, and does not retire any
history. The client holds both (`SetGroundLightStrength`, `SetBlastRingStrength`)
and the host passes them to the executor's setters of the same names beside
`SetEffects` on the running, paused and benchmark paths and in every capture,
the way the glow strength travels (§19.4). The renderer stores each as its scale
less one, so a renderer never handed a strength draws the tuned look, and a
source reset keeps both.

**One pass, independent lanes.** None of the splits restructures a pass; each
switch is a gate that already existed, a lane that already carried a zero, or a
lane added beside them:

* **The surface pass** runs while shading, motion or shore foam has something to
  draw — with the surface and motion switches off and foam either off or refused
  by the medium (lava, damaging water), it does not open at all. Inside it:
  * The **shading lane** is the pass's fourth custom lane: 0 with the surface
    switch off, 1 for water, 2 for a liquid that takes no water colour (lava, which
    keeps the ripple shade but not the crest tint, damp band or shallow tint). With
    it at 0 the ripple shade, crest, damp band and shallow tint are all skipped and
    the refracted painted colour is used as it is.
  * The **moving field** is the shader's compile-time motion constant, as before:
    the moving variant, or the still variant — the same source with the constant at
    zero, which evaluates the field at phase zero with no drift and takes no
    displacement. The damp band's pulse and the shore foam keep the real clock, so
    a still surface with foam on still laps at the shore. The motion switch also
    holds the reflections' ripple and the wet aircraft shadow's waves at phase zero,
    the shape a paused frame holds at its phase.
  * The **shore foam lane** writes zero when the foam switch is off, the lane that
    already turned foam off for lava, damaging water and void liquid.
* **Motion with the surface shading off** is drawn as the displacement alone: the
  moving field refracts the painted seabed and the result is mixed at the
  surface's usual opacity over the painted colour, with no ripple shade, crest,
  damp band or shallow tint. That is the moving surface with nothing recoloured —
  the nearest faithful reading of "motion on, surface off", since the ripple shade
  is colour and belongs to the surface switch. A submerged hull likewise refracts
  without the water's shade (the underwater commit's shading lane).
* **The surface shading with motion off** is the still surface as before: tint,
  ripple shade and crest as a fixed image, the field the moving surface would show
  at phase zero, and the seabed sampled in place. The underwater commit takes the
  ordinary resolve.
* **Foam alone** leaves open water and the dry shore exactly the painted frame and
  draws only lapping shore foam, building foam and wet foam marks. Dry hovercraft wash follows
  its independent switch.
* **Reflections** read the shared coastal mask and the recorded phase, not the
  surface pass's output, so they draw over painted water with every surface lane
  off.
* **The three heat switches share one refraction batch** (§27.2). Each admits its
  own sources into it, so any one can be off alone; a frame with only one kind
  admitted still pays the one copy and draw, as before.
* **The wreck glow and shimmer** are one recorded prototype with two switches
  (§28). The glow writes the emission colour, and the wreck light borrows it, so
  with `wreckGlow` off a wreck emits no light even with both light switches on
  (§31.5); the shimmer writes the plume's strength and clock. The view scale both
  read is written under either, so the light keeps its reach with the shimmer off.
  The arriving commander (§36) uses both parts and follows each switch alone.
* **Trails** are governed by `trailStrength` alone (§15): zero is the layer's off,
  laying no marks, and a change to or from zero retires the trail history the way
  a switch retires its own. A change between two non-zero strengths keeps the
  marks and changes only their recorded opacity.
* **Soft shadows** switch the existing treatment of §34 only. The recorder writes
  zero clearance, which is what already selects the silhouette route; the
  executor gate also refuses the soft commit, so a renderer handed a clearance
  with the switch off still draws the hard shadow.
* **Supersampling** switches between the two resolves the model commits already
  had the inputs for (§17.5). The recorder builds no doubled lane, the path the
  Anti-Alias option off already takes; the executor reads each pixel's top-left
  texel instead of the four. Each half alone still draws whole-pixel edges, so a
  host that gates only the executor gets them too.

**Settings migration.** The retired keys are read once, by
`Presentation.UnmarshalJSON` after the ordinary decode over the defaults, and
are never written again: they have no field, so the next save omits them. A
stored 0 is honoured by turning every switch of that family off — `water` the four
water switches plus land wash (which the old umbrella covered), `lighting` both
light switches, `distortion` the four heat
switches (`blastRings`, `fireShimmer`, `wreckGlow`, `wreckShimmer`), and `marks`
`scorch` together with `trailStrength` set to 0. The retired `hotWrecks` key is
read the same way: a stored 0 turns `wreckGlow` and `wreckShimmer` off. Any other
value was "on" (a negative one was repaired to on) and changes nothing, so the
parts keep what the file says. A file that omits the new keys keeps their
defaults. An old file with a master at 0 therefore composes exactly what the old
build composed with it. `supersample` postdates every retired key: a file written before
it omits the key and so keeps it on, and no retired key turns it off.

These switches are the executor **effect** surface. `Renderer.SetEffects` is its
only entry point: the per-switch setters are package-internal, each is set from
its own switch and from nothing else, and there are no environment overrides, no
window chords and no prototype comparison switches beside them; the package reads
no environment variable at runtime at all. Other exported setters sit outside and
are set beside them on every present: `SetGlow` and `SetGlowStrength` (§19.4),
`SetGroundLightStrength` and `SetBlastRingStrength` (above), and
`SetDisplayPalette`.
The trail layer's executor half has no switch of its own and is on whenever the
executor that draws it is. `New` applies the all-on selection at construction, so
a renderer is in the same state whether or not the host has presented a frame
yet, and a source reset preserves the selection.

`Client.SetEffects` retires the trail, wake, water-motion and scorch histories
whenever the selection changes — any one switch — the way an executor swap does,
so a switch that was off leaves no stale marks and a switch turned back on starts
from the current tick. Each history's observer runs only under its own gate:
trails under a non-zero trail strength, scorch under its switch, dry hover
spray under land wash, water motion while any water switch is on. It also
advances the paused-world revision, because a changed selection is a different
world raster (§13.10).

The host calls `Renderer.SetEffects` once per presented frame. Re-applying an
unchanged selection is a no-op by construction — every switch is assigned from
the argument — so no guard is needed and nothing else can move a switch between
calls. The window polls the shell's committed preference each update
(`RunOptions.Effects`) and hands the executor the client's selection on the
running, paused and benchmark paths; the benchmark keeps every effect on so two
runs measure the same work. A `--shot` capture reads the same settings file, so
it composes under the player's switches; `--shot-debris` keeps every switch on and
its `--shot-debris-lighting` flag turns both light switches together.

**Family shortcuts.** The battle options page's Water, Lights, Metal, Heat and
Marks buttons and the `+water`, `+lights`, `+finish`, `+heat` and `+marks` chat
commands are shortcuts over these switches, described in
[DESIGN_INTERFACE_HUD_INPUT.md](DESIGN_INTERFACE_HUD_INPUT.md) §3.4.1: a family
shows On while any of its switches is on, and pressing it writes every one of
them to the new value. Water is the four water switches, Lights the two light
switches, Metal the finishes alone (not the glint), Heat the four heat switches
(the rings, the fire shimmer and both wreck switches), and Marks `scorch` with the trail strength — off sets the strength to 0, on gives
a zero strength the default and leaves a chosen one as it was. The glint, land wash,
soft shadows and supersampling belong to no family, and no shortcut moves the ground light or blast
ring strength, whose effects keep switches of their own. The shortcuts live in one table
(`effectFamilies`, `cmd/nanolathe/nanolathe_options.go`) that both the page and
the commands read.

**Verification.** `TestEachSwitchOwnsOneGate` locks that each switch, flipped
from all on or from all off, moves exactly its own executor gate;
`TestFreshWreckLights` that the wreck glow off lends no light;
`TestEffectStrengthsScaleClampAndPersist` and
`TestEffectStrengthsReachRingsAndPools` lock the strengths' linear scale, clamp,
persistence across a selection and a source reset, and their reach into the
rings and pools;
`TestWaterPhaseFollowsAnyWaterSwitch`, `TestWaterSwitchesGateOnlyTheirProducers`,
`TestHeatPartsGateTheirOwnMetadata` (the wreck emission, plume and shared scale
included), `TestSoftShadowsSwitchRecordsNoClearance`,
`TestTrailStrengthZeroGatesTrailHistory` and `TestSwitchChangesRetireHistories`
lock the recorder side, `TestRetiredEffectMastersMigrate` the migration of each
retired key, and `TestEffectFamiliesShowAnyAndSetAll` with the options and chat
tests the shortcuts. On a device, the water fixture checks that a still surface
still shades, holds still across phases away from the shore, and keeps its shore
foam moving; that foam removes shore/building foam while dry hover wash keeps
its independent switch and works with water treatments off on an entirely dry
map; that the surface switch alone removes the shading and the damp band while
the water keeps moving and foaming; that motion alone moves open water, foam alone
leaves open water and the dry shore exactly painted, and all three off draw
nothing. The underwater fixture checks that motion off takes the ordinary commit
and surface off keeps the refraction without the shade; the reflection fixture
that reflections draw with every surface lane off; the aircraft fixture that
motion off freezes the wet waves and that soft shadows off is byte-identical to a
zero-clearance shadow; and the lighting fixtures that ground light off removes the
pool while the light is still gathered and model light off removes the light from
a model face while the pool stays. The ground fixture also checks that strength
200 brightens the pool, 0 draws none and 100 restores it exactly; the blast
fixture that ring strength 0 is the ring off, 200 differs from the default, and
100 restores it; and the wreck fixture that shimmer off keeps the glow exactly
with no plume while a plume without emission still draws, and that the glow off
composes a recorded emission exactly as the cold hull, with or without its
plume.

## 31. Fire, projectile and ground lighting (Enhanced)

Two extensions of §23, both Enhanced presentation design rather than retail
evidence: more of the world's light sources emit, and the light they emit now
reaches the ground. Classic composes the same pixels whatever this section says,
nothing here is visible to the simulation or a committed frame [I6]. The
player's model light switch removes the light from models and smoke and the
ground light switch removes the ground pass; the gather runs while either is on
(§30).

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

The terrain pass ends after the water surface and reflection resolve. With no
selected visible light, ground light disabled, or strength zero, it does no
lighting work. Otherwise it submits the scheduler, copies the affected
composite rectangle into the read surface, accumulates the visible lights into
a bounded half-resolution field beside that copy in the same pass, then
resolves the rectangle once. Section 31.8 owns the bounded response and the
field's storage.

The previous per-source `min(contribution, 1 - base)` and additive composition
could flatten even a single bright pool, and overlapping pools could each spend
the same headroom. The clamp prevented numeric overflow per contribution; it
never guaranteed preservation of painted texture. Section 31.8 replaces it
with a joint response whose brightness approaches its limit smoothly.

The falloff smoothsteps `f = 1 − distance²/radius²` as `f²(3 − 2f)` (§31.7),
with `distance² = dx² + dy² + h²`. Here `h` is the nonnegative difference
between source height and the ground height its producer carries; producers
without that metadata still measure from the sea datum (§31.7). The pool is
centred on the source's **projected** position, `Y − sourceHeight/2`, the same
half-height projection as visible objects [03 §2.5]. Projection uses the
absolute source height, independently of the attenuation height. The pass has
no per-pixel terrain receiver height (SC20 explains why painted terrain cannot
be inverted that way on elevated ground). This placement applies to every
family, nanolathe clusters included, while model and smoke receivers retain
their physical source coordinates and response. The explosion source retains
its quarter-frame lift (§23.2), taken from the current animation frame, so its
pool projects one eighth of a frame above the event anchor. Its per-animation
reach still lights the opening flash; no new lift or art centroid is introduced.
The quad covers the light's full radius rather than the smaller disc a lifted
light reaches, so the cover is conservative and the shader's own distance test
discards the difference without a square root [I2]. The world transform of §16.3
applies exactly once, here.

A content pack scales every pool through the **ground** family of §19.4: its
percentage is folded into each source's terrain-only fade when the source is
selected, so the pools brighten or dim together and model and smoke receivers
keep their light. The nanolathe family scales the spray's cluster energy before
selection, so its pools follow it as well.

Placement is deliberate: the copy is taken AFTER the water and reflection resolve,
so the pools brighten the water surface too, and the pass runs BEFORE objects,
wakes and scorch, so units are drawn over it and the ordinary fog composite covers
it.

### 31.4 Cost and verification

The §31.8 response costs two passes in a lit frame, as the original additive
pass did: one into the read surface carrying the clipped composite copy, the
field clear and the field batch, and the resolve back onto the composite. The
field has no image of its own; it occupies space the read surface's texture
usually already has (§31.8 "Field storage"). No visible light means no copy,
clear or draw. Device fixtures cover source placement, flash fade, independent
switches, strength, the highlight response and the field's frame edges. The
battle benchmark compares the same scene before and after on both executors.

### 31.5 Known limits

There is no occlusion: a fire lights the ground on the far side of a wall, and a
unit between a light and the ground casts no shadow into the pool. Terrain has no
normals here, so a hillside takes the same light as flat ground — the pool is a
screen-space disc, not a projection onto relief. The ground pass reads one copy of
the composite, so a light applies to whatever the terrain pass has already drawn,
water surface included, and never to the objects over it. Terrain receiver height
reaches this pass from the producers as of §31.7, so a source resting on high ground
is no longer suppressed by the sea datum; a producer that carries none still is.
A fresh wreck's light borrows the §28
cooling emission, which the wreck glow switch owns: with it off a wreck emits no
light even when both light switches are on. The flicker's phase hash is a position
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
Nearby models and smoke retain the prior full-colour response. The other
families' terrain gain and timing are defined in §31.7–§31.8.

The recorder supplies an independent, presence-tagged `LightingAge`: committed tick
minus published effect `StartTick` plus the presentation fraction when enabled,
populated regardless of the Distortion setting. Untimed detached sources receive
the lower peak gain but retain art-driven lifetime; missing age does not mean an
expired or newly restarted explosion, and the main production explosion recorder
always publishes age. Hidden or finished primary art still stops contributing
immediately. The short-flash change introduced no shader, texture, pass, clock,
simulation state or RNG consumer. Its envelope also governs the bounded
terrain response of §31.8.

### 31.7 Sparks, the family share of the terrain gain, and the rim

Modern presentation tuning, not retail behavior. §31.6 gave the explosion source
its own terrain response and left every other family at the full gain with no
timing, on the reasoning that a fire is a standing thing. Three consequences of
that showed up in play, all on open ground, and all of them read as a drawn
circle rather than as light:

1. **A burning fragment was treated as a burning place.** Both flame families —
   the standing flame-stream segment and the flame-stream TRAIL a burning debris
   piece drags behind it — classified as fire, so a spark the size of a few
   pixels took the standing fire's radius FLOOR of 128 record pixels, times the
   view scale. A death shower also filled the fire reserve, evicting the
   treeline it landed in.
2. **Terrain took the full gain from every family but one**, so a fire's pool was
   `base × colour × 2` where an explosion's was `× 0.75`, and on saturated
   ground the clamp against `1 − base` drove one channel to its ceiling.
3. **The radial law reached zero with a slope**, and a brightening that stops at a
   slope is a rim the eye reads as the outline of a disc.

The response is three changes and no new pass:

**The spark family.** `SpriteLightingSpark` is the flame-stream trail, split out
of `SpriteLightingFire` at the producer, and `lightSpark` is its executor family.
The trail is not only debris: the strips-7/9 family also carries the COB
`emit-sfx` 0/1 VTOL and thrust wakes [03 R-FX-01 §3], so every flying unit's
exhaust flame moves with it, from the standing fire's reach and energy to the
spark's. That is intended — a thrust flame is a moving fragment of fire, not a
place that is burning — but it is a visible change to aircraft, not only to
deaths.
Its reach comes from its own art clamped to 20..56 record pixels — its widest is
inside the standing fire's floor, which a test holds — at `sparkEnergy` 0.9
rather than fire's 1.6, and it takes no flicker at all: §31.5's flicker phase is
a POSITION hash, so a moving source re-rolls it every frame. Its budget is its
own: the reserves are re-partitioned to explosion 24, nano 12, fire 8,
projectile 12, wreck 4, spark 4, still a partition of the 64, with caps fire 16
and spark 16. Eviction takes from whichever kind is furthest over its reserve, so
this does not make a standing fire un-evictable: it means a death shower can no
longer take fire below EIGHT slots where it could previously take it below twelve,
and can no longer fill fire's cap at all. Standing fire pays for that — its
reserve falls 12 → 8 and its cap 24 → 16 — so a forest fire with no debris
anywhere now lights sixteen fires where it lit twenty-four. The reserves are
named constants that the table is built from, so the partition assertion
constrains the same symbols the table uses.

**The family share.** `groundKindScale` declares every family's share of the
terrain gain in one table: explosion keeps §31.6's 0.375 and its envelope, fire
takes 0.3 and spark 0.15. Section 31.8 subsequently sets nanolathe to 0.45
and projectile and wreck to 0.65. Fire and spark were tuned AFTER the receiver height below landed — measured from the sea
datum a pool on high ground carried a large standing attenuation, and a share
chosen against that reads bleached once the attenuation is gone. Model and
smoke receivers are untouched and still read the source's own colour, exactly as
§31.6 left them.

**The source's own fade.** A strip sub-record now publishes `Remaining`, the
committed ticks left before its expiry, as presentation metadata at the ordinary
publication boundary; no phase reads it back [I6]. The presentation-owned debris
trail containers carry the same hint from their own particles. The producer turns
it into a presence-tagged `LightingFade` over a THREE-tick tail and the terrain
receiver multiplies its family share by it, so a pool leaves with its flame
instead of switching off with the particle. The animation cursor cannot serve as
the clock: the flame families take it modulo their frame count, so it is a
looping phase and not a lifetime [03 R-FX-01 §3][03 R-FX-02 §2].

The tail is short, and a record's LAST drawn tick still emits a third of its
pool, because these records are short: a burning debris piece lays flame segments
that live one to three ticks, and a longer tail — or counting the final tick as
spent — takes the pool to nothing while the flame is still on screen. Sources
whose producer carries no fade are unaffected, which today includes the burning
FEATURE: its light comes from the feature blit, not from a strip, so its pool
still ends with its art rather than ahead of it.

**The hue.** The earlier response mixed 75% of the base colour's luma into its
per-source colour product to restrain excessive surface tint. That was artistic
colour tuning, not a physical reflectance reconstruction. Section 31.8 replaces
both that mix and the clipped display-space addition with a linear-light
response; the painted map still supplies the surface colour.

**The receiver height.** The original ground pass attenuated a pool by the source's height above the
SEA DATUM, because the pass has no terrain receiver height, and §31.5 recorded the
consequence: a small pool on high ground is suppressed outright. A spark's reach is
small by construction, so that limit discarded every spark pool on any map that
rises — on Great Divide, ground at 80 and pieces at 245, the family's terrain share
would never have been seen. The producers that own a world position now carry
`LightingGround`, the terrain height under the source, and the ground pass
attenuates by the difference. A source at rest on a plateau is at height zero to
this pass, as it always should have been; one lifted above it still attenuates, and
air bursts are unchanged in kind. Model and smoke receivers keep the physical
source of §23.2 exactly as before.

Three producers carry it: effect art (`effect_draw.go`), every strip family
(`strip_draw.go`) and the burning feature (`world_draw.go`), and each takes the
sample only for a kind that emits, so the terrain is not sampled for smoke, for a
non-emitting effect, on the classic executor's behalf, or with both light
switches off. Four do NOT carry it and still measure from the datum: projectile body
sprites, emissive strokes (`drawlist.Line` has no such field), cooling wrecks and
nanolathe clusters. A plasma shell over a plateau is therefore still suppressed —
the projectile clamp is 56..96 record pixels, the same order as the elevation that
suppresses it. Closing that is the obvious follow-up; it needs the field on
`drawlist.Line` and a world position at the wreck and nano gather sites.

**The rim.** The ground fragment smoothsteps its falloff — `f²(3 − 2f)` over the
same `1 − d²/r²` — which lands at zero with zero slope at the rim and at full
with zero slope at the core. The pool keeps the body the linear law of §31.3 was
chosen for and ends in nothing at all. One multiply-add per fragment; no change
to radius, position, the albedo law, or the distance term.

Every constant here is artistic, and both the gain and the hue mix reach the
fragment by formatting the Go constants into the shader source once at package
init, so there is no second hand-written copy of either number to drift. The
classic executor composes identical pixels — no field it reads changed value — the
player's light switches still gate the gather and pass. This earlier tuning
added no shader, texture or pass; the bounded field of §31.8 now does. Neither
change adds a clock, RNG consumer or authoritative state.

One wording caution, since the paragraphs above are read separately: "model and
smoke receivers are untouched" is a statement about the family SHARE, which is a
terrain-only multiplier. A source that was reclassified from fire to spark does
reach those receivers differently — a smaller radius, a lower energy, no flicker —
because it is a different family now, not because the receivers changed.

### 31.8 Bounded terrain illumination

**Nanolathe presentation policy**, user-approved 2026-10-05 after reviewing
matched terrain captures. The field and resolve are retained; the same day the
field moved to half resolution inside the read surface ("Field storage"). This
is a presentation choice, not a retail finding or gameplay policy, and applies
through the existing Enhanced ground-light control in every gameplay mode.
Original, GPU Classic, Enhanced with ground light disabled, model/smoke lighting,
glow, simulation, RNG streams and content remain unchanged. No map-name or biome
special cases.

**Response.** Keep the source budget, projection, height attenuation, smoothstep
spatial falloff and per-source fade. Normalize each source colour by its largest
channel, decode that hue with the sRGB transfer function, and restore its peak
and ground gain. This treats source strength as authored emission energy while
converting hue separately; the palette is not a radiometric measurement.
For each source's resulting nonnegative linear energy `E`, write `1 - exp(-E)`
to a cleared field with screen blending. In exact arithmetic overlapping sources
produce `Q = 1 - exp(-sum(E))`. RGBA8 storage rounds intermediate blends, so
reversed source order is checked within two display-byte units, not claimed
bit-identical. Source ordering itself remains the existing deterministic order.
The field uses valid premultiplied alpha; its alpha is not a coverage mask for
terrain.

Decode the copied opaque terrain colour to linear `B`. Its surface response is
`R = mix(B, luma(B), 0.25)`, using linear luminance weights
`(0.2126, 0.7152, 0.0722)`. Lift that response to `H = R * (2 - R)` so light on
darker painted surfaces remains legible. Resolve once as
`C = B + (1 - B) * H * Q`, then encode to display colour. These are artistic
constants: the source textures already contain painted lighting and are not
unlit albedo. The response remains bounded for every number of overlapping
lights. Under neutral light the grayscale ramp stays monotonic; even a full
field retains midtone texture instead of making a white plateau. The lift was
added after the first matched metal/desert captures showed too little light.
A zero field returns the original pixel directly, preserving unlit bytes. The
resolve preserves alpha and runs before objects and ordinary fog as before.

**Field storage.** The field is stored at half resolution in the read surface,
beside or below the frame-sized read copy (`groundFieldLayout`). Every pool
ends in the smoothstep of §31.7, so the field is smooth at the scale of its
texels: the resolve reconstructs it bilinearly, and on synthetic one-, 17- and
64-light scenes at 1920×1080 the composite stays within one display byte of a
full-resolution field. Single pools of 24 device pixels' radius and more stay
within two; smaller pools, which appear when zoomed out, differ by up to two,
three, four and six display levels at radii of 16, 12, 8 and 6 pixels. Each disc is drawn at half scale (centre, height and
radius together, so the ratio test is unchanged) with its quad rounded outward
to whole texels, so a disc clipped at an odd frame's right or bottom edge still
writes the last texel. Every bilinear tap is clamped to the field's own
rectangle, so the frame's edges never blend with the read copy or with the
texture beyond the field. Before the batch, the pass zeroes every field texel
the resolve rectangle's taps can reach. The copy, that clear and the batch share
one destination, so the whole step is two passes.

Ebitengine stores each image in a texture whose extents are rounded up to
powers of two. The read surface takes whichever placement keeps that texture
smaller, below the copy on a tie. At 1280×720, 1920×1080, 2560×1440 and
3840×2160 one placement fits in space the copy's texture already has, so the
field costs no GPU memory. Where neither fits (1366×768, 1440×900) the read
surface's texture doubles in one dimension, which is the memory a separate
frame-sized field would have taken. A placement must also fit the device's
largest texture side (`ebiten.MaxImageSize`); a frame for which neither does
keeps a frame-sized read surface and receives no terrain pools, while models
and smoke keep their light. Because the read surface now extends past
the frame, a layer that samples the read copy clamps to the frame: the water
refraction clamps to its destination's size, which is the frame, rather than to
the read surface's (§26).

**Family tuning.** Ground optical-density gain remains 2.0. The family shares
are explosion `0.75 / 2.0` before its existing short envelope, nanolathe 0.45,
fire 0.3, projectile 0.65, wreck 0.65 and spark 0.15. Existing strength and
content-family multipliers scale the input energy; the visible response is
nonlinear. Models and smoke still read the unchanged source. The terrain art,
sampling and zoom controls are unchanged: texture filtering is a separate
question outside this lighting change.

**Acceptance.** `checkGroundHighlightResponseDevicePixels` renders the real
field and resolve over a grayscale ramp with one and a full budget of lights.
It checks retained highlight detail, monotonicity, neutral hue/opaque alpha,
black/white anchors, empty-frame identity, clearing between frames and bounded
colour-order sensitivity. Its clearing check samples a pixel more than four
pixels outside the new pool, beyond the reach of the half-resolution field's
taps; a second corner, on the rectangle's even right and bottom edges, fails
without the clear's one-texel outer margin on either axis.
`checkGroundFieldEdgesDevicePixels` lights odd and even frames in both
placements with one frame-wide light and requires the last row and column to
match the centre within one byte; removing the tap clamp or the outward
rounding each fails it. `TestGroundFieldLayout` locks the placement, the
no-growth frame sizes and the texture-limit fallback, and
`TestReadCopyShadersClampToTheFrame` rejects any of the listed read-copy shaders
sizing its samples by the read surface. Existing device fixtures preserve projected centring,
flash decay and independent ground/model controls. Matched retail film captures
cover desert, snow, ice, metal, grass, forest, rock and lava, with construction
and combat, using the same fixed scripts and camera in both builds. Artifacts
and measurements remain outside the repository; no retail bytes are committed.
The renderer still has no terrain normals or light occlusion (§31.5); this
change adjusts colour response, not geometric illumination.

**Visual and performance validation, 2026-10-05.** The fixed 30 fps, three-second film pairs use
Painted Desert, Polar Range, Ice Scream, Metal Heck, Greenhaven, Gasbag Forests,
Comet Catcher and Lava Run. Snow/ice retain their painted texture through large
flashes; metal/desert retain softer coloured pools. A supplemental Painted
Desert builder/factory sequence compares both builds with a ground-light-disabled
diagnostic: the candidate still casts blue light. The existing elevated-nano
receiver limitation in §31.7 is unchanged; the first elevated construction-only
ice view had no terrain light in either build, so it is not evidence for the
new response. The main ice comparison was reframed on active combat.

Fast, retail short-tier, amd64 fingerprint and real-device gates passed. A
read-only specialist review checked the bounded-response arithmetic, Kage
coordinates/alpha and lifecycle; its stale-field test correction was applied
and rerun on device. The coastal live benchmark ran before/after on both
executors, plus a reverse-order Modern repeat, with identical metadata and
per-frame simulation census. Classic's final PNG was byte-identical. Modern
added exactly one pass, one draw and one quad. Median total host work was
12.62→10.81 ms on the first pair and 11.04→11.34 ms on the repeat; median
submission was 4.24→3.82 and 3.87→3.87 ms. These short runs do not establish a
speedup, and GPU execution timing is unavailable. Total measured allocation
was 0.928→1.054 MB/frame initially and 0.913→0.926 on the repeat; the extra
pass has a small allocation cost and the first pair had substantial run
variance. That separate field was a 2048×2048 RGBA8 texture at 1080p, 16 MiB
rather than the 7.9 MiB first recorded here, since Ebitengine rounds texture
extents up to powers of two (32 MiB at 1440p, 64 MiB at 4K).

**Field storage validation, 2026-10-05.** A device micro-benchmark timed the
ground-light step alone, including GPU execution: interleaved rounds of 30
repetitions per variant, each followed by a one-pixel readback, over synthetic
scenes with one, 17 and 64 visible lights. On an otherwise idle M3 Pro at
1920×1080 the separate full-resolution field cost 77, 79 and 130 µs per lit
frame more than the original additive pass (121, 118 and 192 µs at
2560×1440). Most of that was the third pass itself: even one small light paid
it, and a half-resolution field in its own image still did. A prototype of the
packed field with the same draws measured +14, +21 and −34 µs at 1920×1080
(+21, +55 and +50 µs at 2560×1440). The final code was measured while another
process shared the GPU, which inflated every time two- to fourfold; across two
repeats the separate field measured +95 to +120, +333 to +354 and +416 to
+424 µs at 1920×1080, and the packed field −5 to +12, +39 to +114 and −82 µs.
The remaining cost over the original pass is the resolve's per-pixel transfer
functions; with the full budget the quarter-size field is cheaper than the
original full-resolution discs. In the coastal live benchmark (Modern, seed 7,
1920×1080) every frame carried 7 to 32 pools; passes fell from 22 to 21, host
draw work and submission were unchanged (11.82→11.70 and 4.06→4.07 ms), and the
final frame matched the separate field's except for rounding speckle inside one
large explosion pool (12,094 pixels, 11,545 of them by one level, at most five).

## 32. Reflected explosions, shoreline band, and the removed sun glitter

Additions to §26's coastal water, authored presentation choices rather than
retail behavioral claims: every constant is artistic, the classic executor
composes identical pixels, and the player's water switches gate the surviving two —
the reflected explosions the reflections switch, the damp band and shallow tint
the surface switch.

### 32.1 Sun glitter

**Removed.** The surface shader briefly added a narrow specular lobe on wave
tops, driven by a pseudo-normal differenced from the drifting height field. It
landed at a peak of 0.35, was cut to 0.08 on the first review as too strong for a
surface that must stay subtle, and was removed entirely on the second: the user
judged it was not earning its keep and asked for motion that reads as living
water instead of a tiled sheet sliding across the screen. The lobe, the
half-vector it met and the shared two-layer height function it needed are all
gone, and the four value-noise evaluations they cost went with them. The
replacement is §26.3: tidal current with eased wind direction, bounded local
ripples, a rotated fine lattice, and coarse patches.

### 32.2 Reflected explosions and impacts

Named explosion and impact art records `ReflectWater` and `ReflectionHeight` like
a projectile billboard does, from the same `reflectionWaterAt`/`reflectionHeight`
pair, so the water reflections switch already governs admission. The reflection preparation
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
colour by up to twelve percent before the selected 0.5 surface opacity,
for a final maximum of six percent. Half is steady and half pulses on the
original lap clock; the surface pattern size scales its spatial phase. All of this runs before the shader's dry early-out, because the
band lives on dry texels; a pixel with no water on its ring returns the painted
colour unchanged, and the mask's alpha says so without a second lookup.

On the water side, the result mixes eight percent toward a pale cyan
`(0.62, 0.80, 0.84)`, rising over a 0.0-to-0.10 smoothstep of the shore distance
and falling over a 0.10-to-0.40 one, so water lightens as the bottom rises
without the tint ending on the strict wet boundary (§26.3). The selected
surface opacity and inward edge fade further attenuate it; lava disables both
tint and damp edge. Deep water is untouched.

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

Each receiving pixel samples the non-lava liquid mask with its existing 0.8–1
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

The player's **softShadows** switch (§30) turns the treatment off; it belongs
to no effect family. It gates this existing treatment only: off, the recorder
writes zero clearance and the executor refuses the soft commit, so an aircraft
takes the ordinary silhouette route every other mobile subject takes — the hard
shadow the executor drew before this section existed. A recorded clearance of
zero is what selects that route, and the paired capture fixture pairs on that.
The player's **shadowSoftness** amount multiplies the altitude-dependent filter
radius by `percent / 100`, 0..200, default 100. It travels in `Effects` and is
applied by `SetEffects`; omitted settings keep 100, explicit zero remains zero,
negative settings repair to 100, and larger values cap at 200. The expanded
rectangle uses that same scaled radius, retaining the water displacement pad.
At 100 the existing radius and pixels are identical; at zero the executor
refuses the soft commit and takes the ordinary silhouette route. The switch
still owns admission, so an amount never enables a switched-off treatment.
Water opacity, wave displacement and the fixed 25-tap filter budget stay as
above. `TestAircraftShadowPlayerSoftness` locks radius and bounds together;
the device fixture compares 50, 100 and 200 and checks the zero bypass.

The wet waves above are water motion: with the water motion switch off
they hold phase zero and no drift, and the rest of the wet treatment — the weaker
opacity and the wider filter — stays.

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
  caps at 174,762 slots — a mapped quad takes two, a verdict or outline ring
  one — past which a frame's remaining faces interpolate linearly, and the
  retained-lane store caps at 2,048 entries, past which the least recently used
  lane is re-derived cold.
- The glow's cost is resolution-dependent and was measured only at 1080p and at
  the Survival capture's 1280×827 (§19.3).
- The terrain atlas stores one index per RGBA8 texel; storing it in one channel
  would cut the texture to a quarter (§14.5). A half-resolution tile set for the
  strategic range is unmeasured (`TODO(question)` in `terrain.go`, §16.10).
- A keyed source cannot take the filtered sampling of §16.3 without an
  antialiased sprite edge, which is a look to approve (`TODO(question)` in
  `schedule.go`).
- A model cross-fade at the strategic cut needs an alpha lane on the model
  commit (`TODO(question)` at `Client.markerAlphaAtZoom`, §16.11).
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
- Outside construction groups, a carried child texel its own clip erases can
  leave a hole where retail shows the carrier through it (§22.4), and the modern executor omits a child
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

User-requested artistic presentation, enabled by default for modern battle
entry; not a retail behavioral claim. Fresh skirmishes and missions use the
1.95-second commander arrival. The Effects page's **Commander arrival** switch
stores `presentation.arrival` (default on), including the scene-only reveal on
load. Explicit `--arrival=true` or `--arrival=false` overrides the saved preference
at startup; an omitted flag preserves it. This is a host preference in every
gameplay mode. Classic
and ordinary captures retain their existing entry; an explicit
`--shot-arrival-time` stages an arrival capture.

Loading a save uses only the 1.2-second scene fade/bounce, including tiles,
sprites and units. Its origin is the saved camera view's centre, with no camera
recentering or changes to saved unit poses. It has no commander drop, descent
trail, impact flash, distortion wave, landing sound, heat or new scar. A fresh
mission without a local commander uses this same scene-only reveal. Save entry
is identified by the load path, including tick-zero saves; it consumes the
already-published restored frame without republishing. Input/simulation holding,
first-frame readiness, focus handling, and anchor rebasing apply to both
openings, and reveal-only hands control back as soon as tiles settle. Paused
world caching is bypassed while an opening is active, so a paused save still
reveals and then remains paused. World UI
overlays do not repeat the completed-scene effect.

The shell requests one tick-zero publication of the loaded session through
`Session.PublishOpeningFrame`, using the ordinary publisher without a phase or
RNG draw. It selects the local published commander using the immutable
definition's Commander flag, frames the landing in the final viewport, and
holds gameplay input and the authoritative pump. Escape skips; losing focus
holds presentation time. Its clock waits for the first submitted GPU frame,
then holds a dim scene for 0.5 seconds before the 0.7-second map reveal. The
commander overlaps its final quarter-second, with no intervening pause.
Gameplay hands off at ground contact (1.23 seconds), while the impact pass
continues through 1.95 seconds and model heat continues cooling. Handoff
rebases the host scheduler anchor so the intro cannot become accumulated tick
debt. The ordinary pause mechanism is untouched.

Online lobby battles use the same local opening and renderer/arrival preference
in every gameplay mode. This is user-authorized presentation and transport
policy (2026-10-09), not retail behavior: the relay grants no tick until every
human seat reports opening-ready (DESIGN_MULTIPLAYER §16.6.1). A client reports
at ground contact, at the scene-only reveal boundary, or immediately when its
renderer/preference omits the opening. Escape skips only that client’s opening;
focus loss holds its clock and keeps the relay waiting. Device preparation and
first-frame submission still precede its clock. Once locally ready the player
may issue orders or leave through the menu while another seat finishes; queued
orders take effect only after the shared barrier releases tick one. The
command-line transport probes have no presentation opening.

`Client` stores only commander identity, position, and elapsed presentation
seconds. The commander is hidden during the map reveal until 0.95 seconds,
then a local model-input copy accelerates downward with cubic easing, hitting
the ground at 1.23 seconds. Starting lift places the commander at the top of the
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
Its expansion uses 95% of the post-impact interval, with a later fade, so the
wave travels about a quarter slower while gameplay starts at the same time.
The map reveals in 32-world-pixel screen chunks with a
stronger vertical bounce, a small sideways wobble, and a damped rebound; sampling the completed scene carries terrain, trees,
water, and fog together. It is a sampled image effect, not moving terrain
geometry. Black source fog stays black. The ring finishes according to the
viewport's farthest corner; the final stage returns the exact source image. A `RevealOnly` packet ends at
the reveal boundary and never submits the descent or impact effects.
The pass borrows the existing read surface and submits nothing when inactive.

The falling commander starts hot, reusing wreck emission, nearby lighting and
rising air distortion (§28) on outgoing model packets. A warm orange glow cools
to the ordinary texture over four seconds after impact; the heat follows the
same unit identity as it moves. Gameplay resumes at 1.23 seconds while the
remaining impact effects and cooling continue on a small presentation clock,
frozen on pause or focus loss. The
arrival uses both wreck parts, each under its own switch (§30): the orange glow
and the light it gives follow **wreckGlow**, the rising air **wreckShimmer**. No texture is replaced
and no additional model shader is needed. The shared diagnostics count this
source with wreck heat/lights, an intentional prototype shortcut.

Impact also leaves one small dry-ground landing scar. A 38-world-pixel radius
quad reuses the scorch layer beneath objects and fog, with a ragged charcoal
patch and short trailing burn. It cools with the arrival, then stays at the
original landing point for the rest of the battle, outside the fading blast
mark FIFO. It respects the scorch switch (§30) and the dry
terrain mask. This is a
cosmetic ground mark: no height, collision, pathing, damage or reclaim value.
It resets on terrain/session replacement and is not serialized in saves in
this prototype. The existing scorch shader's landing variant keeps the scar
without extra images or a new render layer.

For repeatable visual inspection, use `--arrival --shot-ticks=0
--shot-arrival-time=0.75` with a modern `--shot`; useful stages are 0.2 (lead-in),
0.75 (reveal), 1.08 (descent), 1.23 (contact), 1.35 (distortion),
1.95 (gameplay with hot commander), and 5.23 (fully cooled).
Timings, tint, displacement and attenuation are authored prototype choices.
The stock `sounds/xplosml3.wav` plays once when presentation time crosses
impact, at 75% of the ordinary in-view cue gain and centred pan. The existing
backend applies master mute and FX volume. Missing audio stays silent; Escape
skips without playing the impact, and GPU redraws cannot retrigger it. No
simulation sound event or RNG draw is introduced.
There is no landing joint animation or terrain-specific impact treatment in
this version.

Verification locks clock/RNG preservation, handoff without catch-up, immutable
poses and slot identity, speculative-record invalidation, one world transform,
shader compilation, unchanged fog/interface pixels, repeatable GPU replay, and
inactive/completed pixel and draw-count identity. Real-map captures and the
live battle benchmark remain required for visual/performance review (§6).

## 37. Community placement-model preview

The placement-model preview is a host presentation feature built on the
Community patch's CP-UD-2 model selection and facing contract.
`NanoframePreview` selects Pulse (0, the default), Full (1), Wire (2), or Off
(3). Existing stored Full and Wire values retain their meanings; stored zero
now shows Pulse. It is independent of gameplay mode: Strict, Modern and
Community sessions see the same host choice, and changing it never changes
placement admission, an order, simulation state or either RNG stream. The
ordinary green/red build rectangle remains the steady placement verdict in
every mode.

While placement is armed and the pointer is over the world viewport, the host
places the immutable catalog definition's model at the resolved build-cell
centre and validated site height. Pulse records the construction reveal at
its initial, empty-body stage, using the normal nanoframe edge walk and the
two colours from `NanoframePulse(0, committedTick)` [03 §5.2]. The fixed zero
phase reflects that the preview has no unit identifier. Wire records projected
authored primitive rings as world-overlay lines, now coloured with the same
committed-tick outline pulse. Full records the production model-body draw.
Both executors therefore consume the same model hierarchy and world transform
as live units. Off records no model work. The preview has no shadow, COB
execution or authoritative construction state.

`PreviewPiecesS/E/N/W`, when non-empty for the selected facing, takes precedence
over non-empty `PreviewPieces`; a selected list is a case-folded whitelist split
on whitespace, commas and semicolons. With no list, pieces whose names contain
`flare`, `flash`, `muzzle`, `fire`, `flame` or `wake` are omitted. Omission
removes only that piece's faces: children remain in the hierarchy and are still
visited. `PreviewObject3D` names a substitute model in `objects3d`. The client
loads it lazily and caches both success and failure per mounted VFS. A failure
adds one bounded host art diagnostic and falls back to the definition's base
model; the piece rules still apply to whichever model is drawn.

`PreviewFaceOpponent` starts with the placement input's facing, then may snap to
the nearest cardinal toward an opponent. The host applies it only when at least
one handle in the committed selection resolves to a completed own unit with a
positive authored build distance and the build-rectangle centre lies within
that distance by an inclusive squared comparison. It scans player slots in
ascending order, requiring an active, non-watcher, directionally hostile row,
and considers exactly the start handle of that player's fixed unit-pool slice.
An absent or dead first record rejects that player; the scan does not substitute
a later survivor. Nanolathe additionally requires this first record to pass the
committed-frame visibility predicate. It never reads the live visibility
service. The nearest admitted unit wins by strict squared distance; ties retain
the earlier player. X dominance chooses east/west and Z dominance, including
ties, chooses south/north. No admitted opponent leaves the player's chosen
facing unchanged. An opponent-derived facing deliberately bypasses the authored
`Rotations` set, matching the key's script-owned-heading purpose.

Focused tests lock facing-list precedence, child traversal under an omitted
parent, substitute fallback and negative caching, the inclusive selected-builder
gate, first-unit selection, committed fog exclusion, player filtering and the
`Rotations` bypass. Visual review uses a software/headless capture with retail
models; the preview remains within the existing world-overlay region described
in §16.3.

### 37.1 Team-coloured nanolathe and nanoframes

`TeamColorNanolathe` is an independent host presentation switch, off by
default. Its ten stream-list and frame-list strings use the parser, bounds,
per-list fallback and stock-palette defaults established in
[community-patch-engine.md "Team-coloured nanolathe and nanoframe colours"](../research/extensions/community-patch-engine.md#512-team-coloured-nanolathe-and-nanoframe-colours).
It is not a gameplay-rule choice and never consumes a simulation or CRT draw,
changes an event count, or writes authoritative state [I6].
It is the sole team-colour control for nano particles and construction frames
in both renderers. A disabled switch preserves the stock palette bytes;
Community particles feed their fixed configured colour into Enhanced
illumination.

The strip publisher freezes the nano particle's resolved owner-logo colour,
creation sample and per-colour creation sequence beside its ordinary stock fill
byte. The per-colour creation sequence advances for every eligible particle
even while the host switch is off, so a later toggle observes the same sequence
as a host that kept it on and cannot feed presentation cadence back into the
simulation. Recording maps those operands to one configured stream byte. The
stock fill remains the disabled and unresolved-owner result. Mapping therefore
does not depend on how often a frame is recorded, submitted or replayed. Both
executors receive that one physical palette index in the shared `Fill` command,
so Classic and Enhanced cannot disagree.

An unfinished `UnitView` already carries the owner-logo colour. The client maps
only `0xa0..0xaf` reveal and outline bytes before either the classic composer or
the geometry recorder sees them; completed model material and every colour
outside that ramp remain unchanged. The §37 full placement preview keeps its
production model body and adds the same model-ring outline when team colour is
enabled; wireframe keeps its existing rings. Pulse and Wire map the current
outline colour through the selected owner's frame list. Full uses frame-list
entry zero for its static host outline. Unknown or out-of-range owner colours
keep the corresponding stock colour in every path.

## 38. Unit model skins

**Nanolathe presentation feature (user-authorized 2026-10-04).** An immutable
palette-indexed bank may replace unit-face textures by their original
case-insensitive texture names. Static GAF replacements and prepared infection
composition are two ways to prepare that bank. This is a host presentation choice in every
rule set, including Strict 3.1. It changes no unit definition, model identity,
mesh, hierarchy, COB, weapon, statistic, ownership, or simulation state. It
implements no infection or capture mechanic. Retail model binding and phase-7
ownership remain as established in [03 R-CRD-005 §1]; ordinary raster and team
selection remain [03 R-REN-03A §5] and [03 R-RAST-01 §3].

`Client.LoadModelSkin(name, logicalGAFPath)` reads the current model VFS and
installs a validated bank outside `textures/`, for example
`skins/infected.gaf`. Each named entry must be one nonempty indexed leaf frame;
empty banks, duplicate names, composite frames, animated entries and explicit
overrides of resolved team or animated textures fail before installation.
Missing replacement names keep the base texture. Unresolved original names
remain unresolved. The material walk independently guards static originals, so
a static bank prepared for different content cannot replace a team or animated frame.
Pixels keep the ordinary model mapper's indexed semantics; GAF sprite alpha is
not a separate unit transparency mechanism.

`ModelTextureRegistry.PrepareModelSkin` performs the same validation during
detached battle preparation. Its immutable `ModelSkin` can be shared, and
`Client.InstallPreparedModelSkin` binds it without I/O or failure after the
corresponding model registry is installed. The convenience loader installs only
after successful validation, so a failed reload preserves the previous bank.

**Prepared infection composition (user-authorized 2026-10-05).**
`ModelTextureRegistry.PrepareInfectedModelSkin(name, palette, overlay, overrides)`
combines original sparse RGBA infection art with the active registry's resolved
unit textures and the active `palette.Base`. This supports arbitrary mod names
and mod replacements of familiar names. It visits the catalog's loaded unit
models, resolves their non-team textures with the ordinary precedence, and
prepares every source frame, including animated entries. It never assumes a
retail texture catalog. An optional explicitly supplied static bank takes
priority for its named textures; its team/animation restrictions remain intact.
No static override is implied by the generic preparation API.

Composition samples only a 64×64 straight-alpha tile of the art. Art of any
other size is box-filtered into that tile first; art exactly 64×64 is taken as
an already reduced tile and read without resampling. The engine ships the
reduction of its 1254×1254 original as the 10 KB `infection-overlay-tile.png`,
so battle preparation neither embeds nor decodes the full-size source. A test
compared composites from both before the source left the tree and found them
identical.

This is original presentation tuning: an 18% warm substrate stain and an RGBA
blend capped at 75% locally, modulated by source brightness and reduced on
highlights, keep panel recesses and recognizable substrate. Coverage-aware box
filtering retains broad infection marks on tiny textures. Half-tile crops
enlarge the masses, and a coverage curve expands partially covered pixels while
retaining clear gaps. This makes diseased patches readable at ordinary unit
distance. Strengthening the original art's rose/olive chroma before blending
prevents palette quantization from collapsing those marks back onto gray ramps.
A stable texture-name hash varies the overlay placement without any random
stream. Palette quantization is cached by RGB value during preparation. A
256-entry flat-colour remap gives units whose
models have no textures a stronger 30% warm/rose stain, retaining source
brightness while surviving palette steps. Missing-texture diagnostic colours
retain their ordinary value, and remapped flat/textured pixels still take the
ordinary SHD path. Team textures remain untouched.

The generated lookup is keyed by texture name and selected original frame
pointer. The material walk first performs ordinary cached/live/direct frame
selection, then substitutes prepared pixels; source frame identity remains
available to renderer traces. Source entries, frame durations, phase-7 cursor
registration and advancement never change. Preparation copies pixel and
transparency planes, preserves composite header/offset/child/alternate-blitter
metadata, and places the overlay in one coordinate space for a composite's
plain raster and children. Malformed decoded geometry, child counts, or cycles
fail with texture/frame context before returning a bank. Nil/empty palettes,
empty names, and absent, empty or wholly transparent overlays fail similarly.
Preparation neither installs nor changes an existing bank, and old recorded
commands retain their immutable frames. Drawing performs only prepared lookups;
there is no draw-time synthesis or additional allocation for transformed pixels.

`SetOwnerModelSkin(owner, name)` supplies a default for the unit's current
committed owner. `SetUnitModelSkin(instanceID, name)` overrides that default;
its identity is exactly `frame.UnitView.InstanceID`, never a reusable pool
slot, and zero is rejected. Both setters reject unknown names without changing
the previous selection. Empty clears the relevant mapping: clearing an
instance override exposes its current owner's default. Two instances of one
shared model can therefore select different banks, and an ownership change
selects the new owner's default on the next draw. No state is added to frame
publication or authoritative units.

The primitive material walk applies the selected bank after base texture
resolution. The shared per-model texture-reference table remains an immutable
base table. Classic composition, native geometry packets, cached and live
lanes, direct drawing, construction and attached units all receive the same
selected frame pointers. Each retained body records the selected immutable
bank identity beside its existing memoization inputs. Changing a mapping,
reloading its bank or clearing skins rebuilds affected cached pixels/faces;
modern retained geometry receives its ordinary new raster revision. Existing
recorded commands keep their old immutable frames. Original team/animation
bindings and phase-7 cursors are never changed or registered by skin drawing.

Load, install, selection and clear operations belong to the presentation owner
between joined frames, never concurrently with recording or its workers.
Workers share only immutable banks and read-only selection maps during a frame.
The setters advance the paused-world presentation revision so paused recordings
also refresh. `ClearModelSkins`, `SetModelFS`, a changed model-registry binding,
and `Client.Close` clear the banks and selections. Reinstalling the same
registry retains them. Hosts bind model content first, then install their
prepared banks and selections. Instance mappings remain until explicitly
cleared or battle teardown; departed identities cannot select recycled slots.

Focused `TestModelSkin*` tests cover shared-model isolation, warm texture and
body caches, ownership changes, owner defaults and instance overrides, bank
reload/failure/clear, content boundaries, protected bindings and missing-name
fallback, classic pixels, modern geometry and cached/live/direct routes.
`TestModelSkinInfection*` additionally checks active mod pixels and palettes,
animated timing and source trace identity, flat SHD and diagnostic preservation,
composite structure, transactional failures and concurrent preparation. The
opt-in `TestModelSkinRetailProbe` accepts `NANOLATHE_INFECTION_OVERLAY` for an
original RGBA PNG (and optionally `NANOLATHE_INFECTED_SKIN` for named overrides).
It compares healthy/infected peers across walkers, tanks, aircraft and ships,
and writes native-size and nearest-enlarged captures to `NANOLATHE_SHOT_DIR`.
The probe compiles active unit definitions for model aliases and shading/key-plane
flags, and uses the normal COB piece linker and Create pose. A separate retail
palette check locks a visible warm shift across dark, middle and light neutral
flat colours. The prepared draw contract also checks zero allocations with warm
presentation scratch.

### Infection spray

The Modern infection capture policy publishes `NanoInfected` on its ordinary
reverse-nanolathe emitter. This is emission-time presentation metadata, like
owner colour: it does not change geometry, lifetime, admission or either RNG
stream. A completed capture's owner-based model skin takes effect in the first
committed replacement frame. No duplicate unit/model definition is needed.

Both renderers receive a shared rose/burgundy/olive particle ramp, quantized once
against the active palette when `Client.SetPalette` installs it. This infection
ramp takes priority over the optional team-colour nanolathe preference; ordinary
construction/capture retains its existing preference. Rebinding a mod palette
rebuilds the ramp. The session tests compare identical tagged/untagged particle
states and RNG positions, and client tests verify palette rebinding and the
ordinary-spray bypass.
