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
execution contract is §9–§11; it supersedes the historical P1–P3 CPU model bridge
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
2. **GPU Classic** targets the original appearance and composition rules with
   modern GPU techniques. Small, visually unobtrusive differences in coverage,
   interpolation, texture sampling, and shade/key quantization are permitted.
   Exact retail raster arithmetic is not a requirement for this mode. Differences
   are measured and reviewed visually, including in motion; a pixel threshold
   alone is never approval. This mode remains an enhancement-disabled reference.
3. **Enhanced** shares the GPU executor and adds explicitly designed presentation
   changes (§5). Lighting and glow are deferred designs, not prerequisites for
   the model prototypes. No enhancement changes authoritative simulation.

One committed-frame ordering remains `drawCommittedFrame` [03 §1]. Neither
executor reads live pools or simulation RNG or writes authoritative state [I6].
A reusable frame packet owns transient model/command data and shares only
immutable resources. Classic model packets own their completed image planes;
modern model packets own geometry, as specified by C-G5.

The projection remains orthographic with the retail half-height shear
[03 §2.5]. GPU Classic stays in palette index space through the retail composite:
ALP, LHT, SHD, Gray and Blue remain table operations, not approximate RGB blends
[03 §4.3]. Enhanced may add auxiliary material/emission data and RGB passes;
material lighting cannot be reconstructed from the expanded image alone.

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

### 2.3 `internal/platform/gpurender` — the modern executor

Imports Ebitengine; joins `internal/platform/ebitenapp`, `internal/audiobackend`
and `cmd/nanolathe` in the architecture test's Ebitengine allowlist. Exposes:

* `New(pal *palette.Tables, w, h int) *Renderer` — uploads the tables
  once: `PAL` as 256×1 RGBA, `ALP` as 256×256, `SHD` and `LHT` as 256×32,
  `Gray` and `Blue` as 256×1, each storing indices in the red channel.
* `(*Renderer).Execute(list *drawlist.List, w, h int) *ebiten.Image` — the
  `Sink`; replays into the frame's indexed offscreen and returns the expanded
  RGB image for `Draw` to present, or for `--shot` to read back.
* Resource caches keyed by pointer identity: tile-set atlases per map, GAF
  frame atlases filled on first use, FNT glyph atlases, the 3DO texture atlas
  with its LOGOS frames, and reusable GPU model composition surfaces (§3 C-G5).

The indexed offscreen is an RGBA8 image whose red channel holds the palette
index. Every shader runs in Kage pixel mode and samples with nearest
filtering, so `index = int(r * 255 + 0.5)` recovers the byte exactly.

The batching target is to group consecutive commands of one family into one
Ebitengine draw where that is order-preserving (C-G3). Current destination
operations use per-command snapshots; batching is not yet implemented. The families that read the
destination pixel — `Tinted`, `Lit`, `LitRect`, `ShadeRect`, the gray and
checker fog fills, the model shadow commit and the waterline tint — cannot be
fixed-function blends because they are table lookups on the destination. The planned batching uses **layers**: a run of same-family commands whose screen rectangles are
pairwise disjoint is drawn into a scratch overlay, then one table pass folds
the overlay into the world image over the union rectangle. A command that
overlaps an earlier member of the run closes the layer and opens the next.
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
* **C-G6 Structure supersample.** Preserve the cached/all versus live gate,
  pre-shear doubled projection, ordered ALP color resolve, and top-left key
  resolve [03 R-REN-03A §6–§7]. Live pieces draw at native scale afterward.
  Mobile units are not supersampled in GPU Classic. A full subject-wide MSAA
  or SSAA replacement belongs to Enhanced and needs a separate design.
  The current GPU resolve preserves the existing classic all-piece
  approximation. Its cached/live-piece split remains a shared reconciliation
  gap (§10); the GPU pass does not independently invent that split.
* **C-G7 Fog composition.** The recorded fog ops are converted to a per-tile
  grid texture (kind, variant, frame, pattern parity) and may be applied by a combined
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

### 5.2 Enhanced zoom and strategic view (planned)

One continuous camera scale should support native 1× through detailed 2× zoom,
using remastered terrain/features when available and model geometry rasterized
at output scale. Below 1×, reduce detail progressively until units become readable
dots or icons in a full-screen strategic battlefield view. Render the visible
world directly into a bounded viewport target, not a huge native-scale whole-map
image. The remaster work remains independently owned; do not alter its assets
or branches as part of these prototypes.

Camera anchoring, cursor-to-ground, selection, orders, fog, radar contacts, and
minimap mapping must share the view transform. HUD/cursor scale is independent.
Strategic markers show only player-known information. Marker thresholds, asset
selection/fallback, icon aggregation, and filtering need design and human review
before implementation. Classic zoom [F-P1-008] is unchanged by this milestone.

### 5.3 Enhanced interpolation (planned, not implemented)

Target at least 60 presented frames/s (16.7 ms frame budget) while retaining the
30 Hz authoritative simulation. Rendering more often without interpolation repeats
committed poses. Optional Enhanced interpolation may use two immutable committed
snapshots; it must never write interpolated values back or consume simulation RNG.
Before implementation define object identity across slot reuse, spawn/death,
teleportation, child attachment changes, piece animation, input latency and pause
behavior. Original/GPU Classic retain committed-tick sampling. I6 permits this
future design only; this milestone changes neither cadence nor frame publication.

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
The geometry packet owns its vertex and face slices and survives the next frame.
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
deep-copies all classic plane slices. `ModelGeometry` owns ordered
`[]ModelFace`, each owning its ordered `[]ModelVertex`; a vertex holds projected
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

GPU model shadows, reveal/outline, waterline/digger processing, attached-unit
staging, and structure resolve are implemented. The capture audit below checks
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

This milestone targets the existing classic image. It inherits classic's
explicit use of the structure rerasterization technique for mobile and Digger
shadows; implementing the retail silhouette branches is separate work. The
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
The current publisher proves a running event with `EventSeqName`; it does not
carry independent live-record and runtime-shadow-enable state when that cursor
is absent. `TODO(EC-P5)` at the draw site tracks that publication boundary; this
raster correction does not complete the feature arena/cursor lifecycle.

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
sees that narrowed key. Finally the group commits once. The current classic
per-subject waterline policy is retained; this work does not change classic's
cached/live-piece or group-waterline research gaps. [03 R-REN-03A §4]

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

This preserves the existing all-piece classic approximation: separating cached
and live pieces into distinct raster scales remains a shared classic research
reconciliation, not a new GPU behavior. Model-only `both` captures can continue
using their explicitly logged native-scale comparison recipe; the scene matrix
and AA-enabled preview API cover the normal structure option.

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

## 11. Performance milestone: local model images and reuse

Implementation policy, not additional retail behavior. The first measurement
target is the development Mac at 1920×1080. The gameplay acceptance target is
still 16.7 ms per presented frame; a frozen draw-list microbenchmark does not
establish that target for simulation, camera motion or a complete battle.
Screenshot readback and PNG encoding are excluded from gameplay timings.

The modern executor rasterizes each body into its composition-sized color and
height-key planes. It packs the completed result into one GPU image (red index,
green key, opaque alpha), then reuses that image while its resolved raster input
is unchanged. Camera placement is applied only when composing the scene. This
also permits sharing identical resolved bodies across units. It does not skip
recording, texture cursor registration or presentation event processing.

Cache identity compares all ordered face/vertex/material inputs, resolved
immutable texture-frame identities, dimensions, scale, key-plane mode,
supersample geometry, reveal bands, outlines, waterline and Digger decisions.
It excludes the outer anchor and origin, shadows and attached children. Exact
serialized key comparison avoids treating a hash collision as equal content.
Turning, aiming, opening, animated/team texture changes and construction or
clipping changes therefore rebuild the affected body. Palette tables are
immutable for a renderer's lifetime. Future zoom, lighting or other material
inputs must extend this identity when introduced.

Shadows are cached as independent silhouettes and blended against a fresh,
bounded destination snapshot on every draw. The current body punches the shadow
using their separate placement rectangles. Fog, selection, terrain and other
scene overlays remain outside the cached body. A carrier's children merge in
order into a local union rectangle. Cached keys survive beneath transparent
pixels; signed child comparison, byte storage and later-child ordering remain
as specified in [03 R-REN-03A §4]. The completed scene or a crowd of units is not
flattened into one persistent sprite, since intervening objects and effects must
retain their normal ordering.

The image cache uses least-recently-used eviction with a 128 MiB payload budget
(packed pixels plus identity bytes), an implementation choice for this first
prototype. Driver/atlas overhead, Go metadata, uploaded assets and reusable
scratch images are additional memory. Images held by the current composition
are pinned until submitted; they may temporarily exceed the budget, then become
eligible for eviction. Oversized images therefore render correctly without
remaining resident beyond the budget. Eviction explicitly releases device
storage. Resizing the viewport preserves reusable model images.

`ModelStats` reports cache hits, misses, evictions, retained payload bytes and
native pixels rasterized. Existing face/strip/resolve counters measure work
actually performed, so cache hits do not increase them. GPU body/shadow counts
continue to count scene submissions, including cached submissions.

Retail's cached-body/live-piece partition is established in [03 R-REN-03A §4].
This first optimization caches the current complete-body approximation; it does
not implement that missing partition. An animated factory can invalidate its
whole body. Separating static and moving pieces requires preserving their key
ownership and tie ordering and remains a later measured optimization.

Geometry preparation is reused across the key and color passes on a cache miss.
Local coordinates exposed texture-boundary float residue in the translated-quad
device fixture; a one-16.16-unit UV guard replaces the earlier smaller guard.
This is a GPU sampling approximation, covered by that translation fixture.

Repeatable model microbenchmarks run with `tools/gpu-bench -count=100
-frames=120 -mode=record` (one command). Modes are `frozen` (executor only),
`record` (rebuild unchanged packets), `pan` (rebuild with changing placement),
and `turn` (rebuild changing headings, including reuse of earlier poses). The
crowd uses sixteen repeated headings of the requested installed `-unit`, at
1920×1080, with structure AA when the definition selects a structure. It omits
terrain, shadows, combat, simulation, input and audio; all poses are explicit
tool inputs. Report preparation, CPU submission and observed draw cadence
separately. `-shot=path.png` captures after sampling. The real-asset and device
fixtures separately cover shadows, construction, clipping and attached units.

Remaining performance work: measure repeatable stationary and changing-pose scenes, camera movement and
combat; separate presentation scheduling from the 30 Hz authoritative tick
without repeating audio or RNG consumption. Interpolation and enhanced visuals
remain deferred. Older prototype contracts in §8–§9 describe historical stages;
§10–§11 govern the current exclusive GPU path and performance work.

## 12. Renderer storage and batching

The renderer reuses frame-owned model state, transforms, polygons, image planes
and GPU preparation arrays. Every simultaneously live body, shadow and child
has a distinct borrowed slot; published snapshots still own deep copies. Reset
occurs at the next recording boundary, with classic shadows borrowing additional
slots during replay. Storage retains peak capacity; it is not a zero-allocation
contract or a bounded-memory cache.

Modern rendering packs immutable texture frames into 2048-square pages and
batches adjacent compatible faces without reordering source faces or scanlines.
Large textures use the existing per-face GPU route. Eligible model cache misses
are prepared in shared key/color passes and packed into reference-counted cache
pages. Entries pin pages through replay; eviction releases a page only after its
last entry leaves. Exact model identities still include pose and paint inputs.
These packing sizes are implementation policy, not retail behavior.

Classic rasterization skips unused interpolation lanes and uses ordinary flat
and textured writers when tracing and construction reveal are absent. General
writers retain those cases. Piece transforms reuse the same sine/cosine values
within one immutable node; rotation order and per-axis rounding remain intact.

The retained September 7–8 real-map prototype measured a mean CPU rendering
reduction from 14.33 to 12.28 ms at 1920×1080, excluding simulation, upload,
pacing and screenshots. GPU and CPU allocation reductions were measured in the
same seeded Ashap Plateau battle. These are development observations, not a
universal frame-time guarantee. Activated 2× solar captures and final battle
captures matched their respective pre-optimization baselines; CPU/GPU pixel
identity is not claimed. Full raw experiments remain outside the production
codebase on the retained profiling branches.
