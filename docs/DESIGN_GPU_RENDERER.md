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
The simulation cannot tell which executor is selected.

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
immutable resources. Temporary same-frame CPU model references in Phase 2 are
an implementation bridge, not the final recording contract.

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
same package whose methods are the existing blitters. `Frame` records and replays through the classic sink, including cursor and expansion commands. The parity
fixtures that digest the indexed surface see nothing change.

The recorder never batches, reorders or culls beyond what the walk already
does. If two sprites overlap, the list says so in that order.

`ComposeFrameSnapshot` gains the recorded `List` beside `Indexed` and `RGBA`,
which is what the diff tool replays through the GPU executor.

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
  with its LOGOS frames, and the per-frame slot atlas for models (§3 C-G5).

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
* **C-G5 GPU model composition.** Ordinary face pixels use a per-subject
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
  outline retain their established stage order. The first prototype may use
  explicitly reported CPU fallback for unsupported subjects; no silent omission.
* **C-G6 Structure supersample.** Preserve the cached/all versus live gate,
  pre-shear doubled projection, ordered ALP color resolve, and top-left key
  resolve [03 R-REN-03A §6–§7]. Live pieces draw at native scale afterward.
  Mobile units are not supersampled in GPU Classic. A full subject-wide MSAA
  or SSAA replacement belongs to Enhanced and needs a separate design.
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
Large occlusion errors, missing subjects, incorrect stage ordering, and unstable
seams are defects, not covered by this allowance. Human review of captures and
motion is the acceptance gate; keep approved recipes and measured differences.

P3a uses two GPU-owned preparation paths. Simple clockwise projected rings use
conventional triangles. A folded projected ring is prepared as one-pixel-high
quads from the same ordered decreasing-index left chain and increasing-index
right chain as the established span walk; only rows whose right edge is strictly
right of the left edge become strips. The strips carry interpolated key, UV and
shade lanes to the GPU, which still performs the fragment key reduction and
colour write. This preserves the positive-span topology without uploading a CPU
body image. It is a prototype preparation cost and not a claim of exact
inside-face interpolation parity.

P3b keeps folded-row attributes in private float32 preparation vertices until
the device fragment stage. Each row uses the last applicable edge on the
ordered left and right chains, ceilings both fractional X intersections,
emits a one-pixel-high positive span, and keeps key, UV and shade lanes
constant through that row. A folded ring with no positive span is valid empty
input and is skipped; it does not force a CPU fallback. Folded faces still
require their SHD and resolved texture resources before GPU admission. The
shared GAF upload retains red for transparent-marked texels so model ownership
can see the physical texture index while keyed sprite families continue to use
their green coverage flag. The GPU model commit follows the researched
composition key for index 1; the current CPU model coverage path may retain a
covered index 1. Such pixels are a documented comparison exception pending a
classic coverage reconciliation, and are not accepted as visual parity.

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
   warm-up. Recording may consume private presentation RNG and drain audio, so
   never re-record that list inside the measured loop. Report one-time preparation
   separately (classic comparison preparation includes CPU composition and snapshot
   copying), and submission and synchronized render/readback distributions
   (median/p95/p99). ReadPixels includes GPU synchronization wait plus transfer;
   it does not isolate readback overhead. A synchronous
   readback is not normal presentation and its timing is not an isolated GPU
   timer. Existing headless `--profile-seconds` measures classic CPU composition
   only. Record hardware/backend, resolution, frames and scene. No universal
   60 fps claim from one idle scene or submission time alone.

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
retail images/assets stay local and uncommitted. Preserve the exact Phase 2
composition path as the fallback while GPU subject coverage grows. Unsupported
prototype cases must be listed in the handoff, not described as full GPU parity.

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

## 8. Prototype work sequence and public API contract

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

**P2 published API.** `drawlist.Model` retains its classic same-frame `Ref` and
adds `Geometry *drawlist.ModelGeometry`. `ModelGeometry` owns ordered
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
