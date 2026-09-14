# The GPU renderer's measurement record

This file is the record of what was measured, when, and against which build,
for the work [DESIGN_GPU_RENDERER.md](DESIGN_GPU_RENDERER.md) describes. It
was split out of that document on 2026-09-14, when the design document was
rewritten to state only what the code does now.

**It is history, not a contract.** Nothing here is a statement about current
behaviour. Numbers were taken on the hosts and builds named beside them,
mostly a darwin/arm64 Apple M3 Pro with the Metal backend, mostly on a shared
machine under other load; almost every one of them says so. Some of the
mechanisms measured below — the index-exact composite, the model slot atlas
and its residency table, the per-frame lit point plane in the modern lane, the
vegetation wind prototype — no longer exist. Read the design document for what
the executor does; read this for why it got there.

Artifact paths under `/private/tmp/...` name directories on the machine that
ran the comparison. They were never committed and are long gone; they are kept
because a measurement without a named artifact is harder to re-run, not
because the files can be retrieved.

## Section numbering

`DESIGN_GPU_RENDERER.md` **keeps every section number it had**: code comments
cite more than three hundred of them and each still resolves to the same
topic. Nothing was renumbered by the split. Sections whose whole content was a
record — §8 (the P0–P3 prototype sequence), §12 (the work units for §11) and
§24 (the removed vegetation-wind prototype) — were removed from the design
document and live here; every other section kept its number and its subject
and lost its measurements to this file.

| Design section | What moved here |
|---|---|
| §2.2, §2.3 | the cached/live and closeout capture comparisons |
| §5.1 | the slot-atlas placement-sensitivity measurement |
| §8 | the whole section: the P0–P3 prototype sequence and staged packet API |
| §11.1, §11.4, §11.5 | why the first executor was slow; the G5 and H1–H3 measurements; the second round's full text |
| §12 | the whole section: the G1–G5 and H0–H3 work units |
| §13.1, §13.5–§13.12 | the third to eighth rounds' full text and their measurement tables |
| §14.8, §14.9 | the detail view's work units and outcome |
| §16.5, §16.12 | the zoom validation runs and the verification record |
| §17.7 | the supersampling benchmark |
| §18.6, §18.7, §18.8 | the acceptance checklist and accepted fallbacks; the strategic icon validation and live benchmarks |
| §19.5 | the glow verification record and cost |
| §20.3, §21 | the range-guide and all-zoom validation runs |
| §22.2 | the model lane's measurements |
| §23.3, §23.4, §23.6, §23.7 | the lighting, nanolathe and glint outcomes |
| §24 | the whole section: the removed vegetation-wind prototype |
| §25.1, §25.2 | the distortion prototype verifications |
| §26.3–§26.6 | the coastal water and reflection iterations |
| §27.1–§27.3, §28.1, §29.3 | the heat, wreck, material and scorch verifications |
| §31.3, §31.4, §31.6 | the ground-pool gain and falloff tuning; the lighting test coverage and cost measurements |
| §32.4 | the water and reflection verification record |
| §34 | the aircraft shadow verification, cost and closeout |

Each heading below names the design section the text came from. The text under
it is verbatim, with only its original heading line removed where the heading
above already names it. A second pass on 2026-09-14 compressed §18, §26 and §31
and moved their remaining rejected alternatives and test enumerations here; the
headings added by that pass say so.

---
## §2.2 — cached/live model lane acceptance comparisons (2026-09)

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

## §2.3 — bitmap, clipping and shadow closeout comparison (2026-09)

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


## §5.1 — device raster sensitivity to slot-atlas placement (slot stage, retired 2026-09-11)

The device raster is also sensitive to where a subject lands in the slot atlas:
moving a subject's slot origin by one pixel, with no other change, moves a few
hundred silhouette-edge pixels across a full battle frame (measured: 452 of
2,073,600 on the seeded benchmark capture, one to three pixels per unit). The
shader arithmetic is translation-invariant; the residue is the device's edge
and varying interpolation at absolute positions, and it is part of this
approximation. A capture comparison between two revisions is therefore only
byte-exact when their slot placement is identical; a placement change is
reviewed on the model preview captures and by inspection, not by pixel count.


## §8 — historical prototype work sequence P0–P3 and the staged packet API

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


## §11.1 — why the first executor was slow

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


## §11.4 — compiled execution measured after G5

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

## §11.5 — render passes, not draws (second round, 2026-09-08)

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


## §12 — work units for §11 (G1–G5, H0–H3)

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


## §13.1 — why the index-exact executor cannot get there (2026-09-08)

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


## §13.5 — per-frame model differences are the blend, not the atlas

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


## §13.6, §13.7 — third-round work units and outcome

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


## §13.8 — the lit point plane (fourth round)

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


## §13.9 — two-stage unit recording (fifth round)

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


## §13.10 — the record/submit pipeline (sixth round) and paused world reuse

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


## §13.11 — flash quads and same-stream phases (seventh round, 2026-09-10)

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


## §13.12 — persistent model slots (eighth round; retired 2026-09-11)

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


## §14.8, §14.9 — detail-view work units and outcome (landed 2026-09-09)

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


## §16.5 — zoom interpolation validation and benchmark runs (2026-09-13)

Validation (2026-09-13): the camera/recorder regression tests, independent
review and actual-device GPU fixtures passed. A frozen Ring Atoll scene,
seed 7, 90 ticks, 1024×768, with explicit half-update presentation fractions
was captured through 1×→2×→0.25×→1×. The commander at the cursor rocks in the
baseline and stays anchored in the corrected capture; both zoom directions
were visually inspected. Captures are outside the repository under
`/private/tmp/nanolathe-zoom-visible-{before,after}`; the side-by-side video is
`/private/tmp/nanolathe-zoom-comparison.mp4`.

Sequential Great Divide scene-4 battle runs compared baseline `74b9ba21` with
the integrated fix, seed 7, 1920×1080, native scale, 300 preticks, 60 warmup
and 180 measured draws at 30 TPS, factories on, automatic remaster off.
Both renderers have identical metadata, every frame's census (including
features and construction), and byte-identical final battle captures; the
captures were visually inspected. Classic Record median/p95 changed from
14.058/18.304 ms to 16.727/50.253 ms, with 1.585 MB/frame in both runs.
Modern Record median changed from 4.244 to 3.746 ms and Submit median from
5.574 to 4.748 ms; allocation was 1.875 versus 1.601 MB/frame. Other tasks
were building and testing on this host during these runs, so the timings are
not an isolated performance comparison and establish no speedup or regression.
Artifacts are `/private/tmp/nanolathe-zoom-bench-{base,fixed}-{classic,modern}`.
The separate motion capture and anchor tests exercise zoom interpolation;
the battle benchmark holds the camera fixed.

## §16.12 — smooth zoom and strategic view verification record

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


## §17.7 — subject-wide supersampling benchmark (item 4)

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


## §18.7 — strategic icon validation and live benchmarks (2026-09-11)

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

## §18.8 — strategic icon play-test correction benchmarks

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


## §19.5 — glow verification record and cost (items 3–5)

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


## §20.3 — tactical range guide validation (2026-09-11)

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

## §21 — modern resource construction input, all-zoom validation (2026-09-11)

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
Modern drag construction, rectangular area work, and free-form formation
commands are specified in [DESIGN_INTERFACE_HUD_INPUT §3.11](DESIGN_INTERFACE_HUD_INPUT.md#311-modern-drag-commands).
Their previews share the world-overlay transform and ordinary indexed line/fill
primitives. These are explicit input extensions; no renderer state enters
construction or movement. Alt grid capture takes precedence over tactical ranges.

## §22.2 — model lane measurements (battle benchmark, 1080p, 120 TPS)

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


## §23.3 — battle lighting radius-extension measurement

The 50% radius extension was visually checked against the preceding 3.25-gain
build in the matching 2× Great Divide battle (60 FPS, nearest-doubled art).
Median submission time was 3.592 → 3.689 ms, p95 3.931 → 4.138 ms; median
cadence was 16.694 → 16.775 ms. Median lit model faces rose from 1,089.5 to
2,038.5. Scene metadata and all frame censuses matched. Classic's matching
native capture remained pixel-identical. These are one pair of host timings,
not GPU timing or a guarantee for every battle. Build, renderer vet and the
existing GPU device fixtures passed. Captures and timing data are under
`/private/tmp/nanolathe-lighting-{radius-before-modern,radius50-modern,radius50-classic}`.

## §23.4 — battle lighting prototype outcome

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


## §23.6 — nanolathe glow and local illumination verification

The synthetic and real-device fixtures check visibility admission, palette
classification, physical height and scale, clustering, recorded-viewport
clipping, clone/reset, green illumination on a facing model surface, an unlit
back face, a halo beside the particle cores, and restoration when the effects
are disabled. Independent read-only review found no implementation issues;
the description was corrected to call the looping particle ramp a shimmer.

The factory-flash regression sweeps an unchanged spray through all seven
palette entries and replays entries out of order. Broad source energy stays
identical, while replacing the displayed palette updates its hue. A separate
sparse-spray sequence verifies proportional energy for one, two and three
particles and immediate removal at zero; count and geometry changes still
change illumination. The device fixture checks every pixel outside the particle
cores across the full ramp: armour and terrain remain byte-identical, with
nonzero ground illumination, while the cores retain their palette animation.
`NANOLATHE_NANO_SHOTS` optionally captures those seven frames during the existing
GPU device gate. This isolates palette flicker; it does not claim that particle
motion, changing cluster membership or light-budget contention are smoothed.

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


## §23.7 — metallic glint validation and measurements

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

## §24 — wind in vegetation (removed prototype)

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


## §25.1 — explosion distortion prototype verification

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


## §25.2 — dynamic ordinary blast verification and comparison

Prototype verification on the integrated branch passed `tools/check`,
`tools/check-retail` including lint, the affected packages and the real GPU
fixture loop. The float audit explicitly lists the device-only square roots
in I2. The six installed-art clips and the live modern/classic battle captures
were visually inspected. Tiny and expired paired captures are pixel-identical.

The sequential comparison used one binary with its dynamic formula disabled
and enabled: scene version 4, Great Divide, seed 7, 1920×1080, zoom 2,
auto-remaster off, factories on, 300 pre-ticks, 120 warmup draws and 180 measured
draws at 60 draws/s. Metadata and every census matched within renderer pairs.
Modern waves grew from 0–10 (mean 2.98) to 1–17 (mean 7.26), with identical
per-frame device draw counts. Submit median/p95/max was 3.650/4.011/4.184 →
3.665/4.037/4.126 ms; cadence median was 16.708 → 16.668 ms. This is a short
host-timing sample, not a GPU timing or guarantee. It isolates the renderer
formula, not the scalar-copy overhead relative to a pre-prototype binary.
Classic capture bytes match; its host cadence varied from 22.596 to 24.874 ms
median with a 192.509 ms outlier, so those samples do not establish a cost for
the modern-only formula. Captures retained moving and damaged armies, sprite
features and burning trees, projectiles, effects, construction and nanolathe
activity. Raw runs and profiles are outside the repository under
`/private/tmp/dynamic-blast-review`.

## §26.3 — coastal water validation record

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

## §26.4 — reflection storage and isolated device probe

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


## §26.5 — coastal water integration verification

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



## §26.6 — aircraft and boat reflection iteration measurements

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

## §27.1, §27.2 — tree heat prototype verification and shared-pass measurements

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


## §27.3 — tree heat landing review

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

## §28.1 — fresh wreck cooling prototype verification

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


## §29.3 — materials and scorch verification and live battles

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

## §31.4 — fire, projectile and ground lighting cost measurements

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

## §31.6 — short explosion terrain flash comparison runs

Sequential live battle pairs used the same binary with the terrain-flash
comparison off/on: Great Divide scene 4, seed 7, 1920×1080, zoom 2,
auto-remaster off, factories on, 300 lead-in ticks, 120 warmup draws and 180
measured draws at 60 draws/s. Every census and pairwise scene metadata matched.
Modern ground quads averaged 25.73 → 13.82; source lights, lit model faces,
blast waves and device draws matched on every frame. Submit median/p95/max
was 3.566/4.240/7.247 → 3.442/4.037/5.383 ms; cadence median 16.719 → 16.669 ms.
Classic capture bytes matched, with Submit median 0.465 → 0.472 ms and cadence
median 21.238 → 21.106 ms. These short host samples isolate the terrain formula,
not GPU time or a performance guarantee. Both scenes retained moving and
damaged units, projectiles, effects, sprite and burning features, construction
and nanolathe activity. Raw profiles and runs are under
`/private/tmp/ground-flash-review`, outside the repository.

## §32.4 — reflected explosions and shoreline verification record

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

## §34 — aircraft soft shadow prototype verification, cost and closeout

tools/check and tools/check-retail pass, as do the focused client/draw-list/GPU
checks and Metal device loop. Pixel
relationships verify wider spread without extra integrated shadow mass, water
attenuation, dry stability, frozen replay, neighboring atlas isolation and
fractional zoom. Independent read-only review verified source coordinates,
clearance normalization, filter weights and conservative expansion.

Installed ARM fighter and bomber models were inspected on Seven Islands and
Ring Atoll over land, water and shore, at native and twice scale, with staged
clearances of 32 and 200 world pixels. The 2x Ring Atoll shoreline sequence
advances water through 60 frames while holding the aircraft fixed. These are
staged visual fixtures, not simulated flight. Captures and profiles are outside
the repository. Reproduce with NANOLATHE_GPU_DEVICE_TEST=1,
NANOLATHE_RETAIL_ASSETS pointing to the install, NANOLATHE_AIRCRAFT_MAP set to
Ring Atoll and NANOLATHE_AIRCRAFT_SHOTS pointing to an output directory, then
run go test ./internal/platform/gpurender -run '^TestDeviceFixtureLoop$' -count=1.
NANOLATHE_AIRCRAFT_PROFILE=1 additionally runs the paired frozen profile under
the shared benchmark lock.

On the M3 Pro/Metal, 1280x960, 2x model scale, 60 warmup and 180 measured frames
per case, four alternating off/on/on/off runs with 64 installed fighters gave
median completed-frame times 5.668/6.265/6.307/5.656 ms on the land camera and
6.126/6.685/6.752/6.350 ms on the water camera. Paired increments are roughly
0.40–0.65 ms; averaged increments are 0.62 ms and 0.48 ms. Completion includes
CPU submission, driver synchronization and one-pixel readback, not isolated
GPU timestamps. Passes stay at seven; device draws rise from 11 to 74 because
bounded subimage sources split the aircraft runs. A lone fighter's small cost
is not established by these noisy comparisons. Dense overlapping formations
and other GPU backends remain unmeasured.

Live Great Divide battle comparisons against the prototype's exact starting
revision match scene metadata and every measured tick's census for both
executors, retaining moving armies, factory construction, sprite features and
fire. Classic's capture is byte-identical. Modern's changed pixels follow the
aircraft shadow footprints; captures were inspected. Median record/submit/
cadence in milliseconds: classic 13.517/0.485/33.333 before and
13.332/0.469/33.334 after; modern 2.998/4.817/33.333 before and
2.975/5.142/33.333 after. Modern's first measured frame retains 409 direct
subjects, 293 shadows, zero overflow and 18 passes; draws rise 95 to 118.
These measurements describe the initial three-pixel prototype. The user then
approved the upper-altitude increment and landing the treatment in main.

### Upper-altitude closeout

The final four-pixel treatment passed the focused tests and independent Metal
review. After integrating the current main, tools/check, tools/check-retail and
the Metal device fixture loop passed again. All twelve installed-art low-altitude captures remain byte-identical
to the initial prototype. The high bomber land, water and shore captures were
inspected; the outer silhouette softens further while the body stays unchanged.
The normalized-coverage device test now exercises clearance 200, and the
radius test checks low-flight preservation, upper cap and scale independence.

Final pre-landing Great Divide runs retain equal scene metadata and every
measured census, with byte-identical Classic captures and only aircraft-shadow
footprints changed in Modern. Classic median record/submit/cadence is
13.247/0.475/33.333 ms before and 13.424/0.460/33.333 ms after. Modern's first
pair is 2.048/3.657/33.333 ms before and 3.082/4.869/33.333 ms after; a repeated
baseline is 2.513/3.833/33.333 ms and its paired integrated run is
2.994/4.932/33.334 ms. Both pairs retain equal metadata and censuses; host
timing variation is material, so these timings do not isolate the filter.
Modern retains 18 passes, 409 direct subjects, 293 shadows and zero overflow;
first-frame draws rise from 95 to 118.

The final 64-fighter frozen profile retains the same sampling count and seven
passes. Its off/on/on/off completed-frame medians are
5.854/7.825/7.025/6.118 ms on the land camera and
7.210/8.107/7.985/7.556 ms on the water camera. Paired increments range from
about 0.43 to 1.97 ms in this run, greater and more variable than the initial
three-pixel profile; the expanded filter footprint does additional GPU work.
CPU submission increments in those pairs remain about 0.01–0.06 ms. These
synchronized completion measurements include readback overhead; the rendering
path performs no CPU readback. No isolated GPU duration is established.


## §18.6 — strategic icon acceptance checklist and accepted fallbacks (2026-09-11)

Moved out of the design document on 2026-09-14 when §18 was compressed. The
design document keeps the review-sheet command and the rule that every generic
fallback is resolved or explicitly accepted by a human reviewer.

Acceptance was: the sheet reviewed by a human, who resolves or explicitly
accepts every generic fallback; small authored fixtures locking radar-only
fallback, cloak and visibility loss, decoy appearance, immediate slot reuse,
classification traps, clip and zoom boundaries, retained list replay and
draw/pick agreement (retail tests skip without assets and assert
relationships, never a fixed catalog census); captures at 1×, 0.625×, inside
the fade, 0.5× and a lower allowed zoom, including buildings under
construction, aircraft and submarines, selected dense armies, radar-only
contacts and fog boundaries; and matching live benchmark runs for both
executors, plus modern before/after runs at 0.5×.

**Accepted fallbacks** on the reference mount: ten primary-role fallbacks
(ARMPEEP, CORFINK, ARMBEAC, CORBEAC, ARMDEV1, CORDEV1, ARMUWES, ARMUWMS,
CORBUILD, CORTRUCK), and ARMSCORP and CORTHOVR on a generic physical family.
AA, fighter, artillery and heavy-role interpretations remain explicitly
unresolved. The fallbacks are visible in the audit; no runtime name or
description guessing and no stock-name override table exists.

## §26.3 — the water treatment that slid as one sheet (rejected)

Moved out of the design document on 2026-09-14 when §26 was compressed. The
design document keeps the rule: none of the three noise layers scrolls on a
velocity of its own.

An earlier treatment added a fixed scroll on top of the integrated wind drift,
and in calm wind that scroll matched the drift itself, so the whole surface slid
as one sheet in a direction unrelated to the wind.

## §26.4 — why the reflection edge samples the shore mask bilinearly

Moved out of the design document on 2026-09-14 when §26 was compressed. The
design document keeps the rule: the resolve samples the shared mask bilinearly
and converts it to coverage with the same smoothstep the surface and wake
shaders use.

Rejecting a nearest sample below full coverage made the reflection end on a
whole mask texel, several world pixels wide on a large map, so the edge read as
a stepped line rather than a shore.

## §31.3 — ground pool gain and falloff tuning

Moved out of the design document on 2026-09-14 when §31 was compressed. The
design document keeps the gain 2.0, the albedo multiply, the `1 − base` clamp
and the falloff with the square dropped.

The gain has to be far enough above zero for the pool to read without
amplification, which 0.9 was not. Squared, the pool was a bright point inside a
wide invisible skirt; linear in `d²/r²` it carries light out to most of its
radius and still reaches zero at the edge. Multiplying by the albedo keeps every
painted detail — a dark rock stays darker than the sand beside it.

The per-animation explosion reach of §23.2 also fixed a temporal consequence of
the retained height attenuation: with a per-frame radius, the bright opening art
of a large explosion is too small for its reach to exceed the source height, so
ground illumination used to peak long after art emission peaked. Fixed entry
reach lights the opening flash and lets the measured art colour govern its
decay. The current frame still determines the quarter-frame lift. That addressed
radius-induced delay only; the absolute-height approximation can still suppress
an entire small explosion on high ground, and it supplies no missing terrain
receiver height.

## §31.4 — fire, projectile and ground lighting test coverage

Moved out of the design document on 2026-09-14 when §31 was compressed. The
design document keeps the cost (two extra submissions in a frame with a light in
view, nothing without one) and points here.

The synthetic tier checks admission and rejection per kind, the radius clamps,
the flicker's bounds, determinism, period and per-source phase, the warm
fallback's hue and energy, the stroke midpoint, height and colour, the wreck's
emission and its shadow packet, the anchor recovery for feature art, viewport
culling of sources, the per-kind caps and reserves in both arrival orders, the
ground quads' clipping to the viewport and the empty-batch cases. The opt-in
real-device fixture checks that a stroke brightens the terrain beneath it while
terrain beyond its reach is untouched, that a flame sprite lights a facing model
face but not a back-facing one, and that disabling Lighting restores the
composite exactly. The projected-pool regression additionally checks native and
2× recordings, a fractional final world transform, all five source families,
elevated explosion and nanolathe admission, unchanged physical sources, and zero
ground contribution at the height/radius boundary. The installed-art regression
compares a bright early frame against a trailing one at native and doubled
record scales, revisits the cached bright frame, and checks retirement; optional
`NANOLATHE_EXPLOSION_SHOTS` captures a sequence of frames for inspection.

## §34 — the sub-image binding and why it was removed (2026-09-14)

The layer originally bound the aircraft's silhouette as a sub-image view of the
model lane's colour page. The bound came free, but Ebitengine hands back a fresh
image for every distinct rectangle and keeps it in the page's sub-image cache for
sixty ticks, so a moving aircraft churned an image and a map entry per aircraft
per frame; a distinct image also splits the batch, which is what the closeout
profile above saw as device draws rising from 11 to 74 with 64 fighters. The page
now binds whole and the rectangle rides the custom lanes, freeing the four lanes
that carried the water operands onto the `AircraftWater` uniform. Every device
fixture render — dry and wet, both altitudes, both zooms, the neighbouring-subject
isolation case and the advanced water phases — is byte-for-byte what it was.

## §32.3 — the damp band's per-fragment ring (retired 2026-09-14)

The band's ring was computed per fragment: eight bilinear water readings,
thirty-two texture fetches, on top of the twelve the other mask channels cost.
The water quad covers the whole viewport whenever any water is within a block of
it, so every inland pixel paid for the same terrain-only answer every frame. The
two readings are now multiplied once into the mask's alpha at mask build, costing
about 120 ms once at map load for a 2,048-square map whose every block touches
water, and forty-four mask reads per dry pixel became twelve.

Visually unchanged: on a coast-to-coast frame at 1280×960 the band's own darkening
reaches 13 levels over 569 pixels, and the shipped frame differs from the
per-fragment ring by at most 2 levels on 261 pixels inside that region — the
field is sampled at texel centres and filtered back. The same frame at `--zoom 2`
is byte-identical.

## §22.1 — keying the cached lane alone (2026-09-14)

Before this change the recorder zeroed `ModelCacheKey` for every keyed subject
with a live piece, every nanoframe, and the whole packet of every 3DO feature,
and the executor refused any packet carrying a live lane or an outline, so the
retained store served far less of the frame than it could.

Great Divide (seed 7, 1080p, 120 TPS), medians per frame, 390 packets (371
subjects + 19 projected shadows):

* before (main `a2eed2c6`): `DirectRetained` 128, `DirectCaptured` 16; cold by
  reason: no key 122 (of which outline 8, live 15), first sighting (stub) 112 =
  store evictions 112, group 0.
* after: `DirectRetained` 153, `DirectCaptured` 17, replayed under a live lane
  14; no key 90, group 8 (the factory nanoframes are carried children), first
  sighting 112.
* Submit 3.611 → 3.566 ms, PreRecord 1.997 → 2.015, Cadence 8.488 → 8.469, Step
  1.255 → 1.233. A second before/after pair (Submit 4.255 → 4.142, Step 1.573 →
  1.645) puts the timing deltas inside run-to-run noise.
* `tools/framediff` before/after `battle.png`: 0 differing pixels.

The cold-reason counters were added for this comparison and are what settled
where the cold packets came from: the dominant one is the first sighting of a key
that never comes back, which under interpolation is every turning unit
(§35).

The first wreck-retention commit entered every committed 3DO feature into
`pruneCachedModelBodies`' per-frame live set, which was sized for the units
alone; the benchmark's allocation profile attributed +22.9 MB of the measured
window to that function (2.02 → 24.93 MB), the whole of a reported 0.536 → 0.683
MB/frame regression. Ageing feature bodies out after 30 ticks instead gave Alloc
0.520 → 0.531 MB/frame, Submit 3.576 → 3.541 ms, `DirectRetained` 128 → 153, and
an allocation profile of the measured window of 96.2 MB before against 87.0 MB
after.
