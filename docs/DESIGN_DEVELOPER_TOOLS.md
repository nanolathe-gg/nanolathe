# Developer tools

## 1. Status, scope and evidence

**Status: first inspection delivery implemented; validation is described below.** The
2026-09-15 user request brings retail developer views into Nanolathe's
scope and explicitly authorizes enabling tools whose painters survive in the
executable but whose activation/rendering paths are disconnected. This document
is the implementation contract; the delivery boundaries below distinguish
working inspection from deferred capture and command work.

Retail behavior belongs to the owning research sections below. In particular,
an existing handler, a reachable hotkey and a reachable painter are different
claims. A disconnected painter is eligible for implementation here. Why retail
disconnected it remains Unknown; a production build setting is a possible
explanation, not an established fact.

| Surface | Retail contract | Nanolathe delivery |
|---|---|---|
| Developer authorization, film controls, repeated command | [07 R-CAM-01 §6], [07 R-CAM-01 §9] | Explicit developer activation and the recovered controls; retain ordinary input ownership |
| Terrain/search/occupancy/metal/visibility display modes | [03 §3.12] | All five selector states, with their documented content and palette behavior |
| Height contours | [07 R-FE-02 §11] | Terrain contours with the recovered conversion, intersection and draw order |
| State and builder probes | [07 R-CAM-01 §9] | Reconnect both documented painters; do not reproduce their missing incoming calls |
| Runtime footer and selected-unit movement drawing | [07 R-HUD-03 §1], [03 R-COMP-01 §5] | Published diagnostic fields and route/target geometry |
| Battle profiler and frame counter | [03 R-COMP-01 §5] | Useful host measurements, with their sampling scope identified |
| Library memory/performance windows | [01 R-PLAT-01 §9] | Portable host diagnostics; do not require retail's disabled helper thread |
| Screenshots and movie frame series | [01 R-PLAT-02 §6], [07 R-CAM-01 §8] | Planned capture tools using the existing host capture boundary |
| Commands that change the battle | [07 R-CAM-01 §6], [01 R-PLAT-01 §9] | Separate later unit, gated on complete command-specific research |

This scope does not add multiplayer, restore competitive synchronization, or
turn a diagnostic snapshot into a save. Existing [diagnostic capture](DEBUG_CAPTURE.md)
and the Modern `+spawn` command keep their own documented contracts.

## 2. Tooling policy

### 2.1 Reconnect dormant tools

**Nanolathe Modern policy — developer shortcut.** `+dev` grants developer
authorization in the central `gameplay.Modern` mode; it is case-insensitive,
takes no arguments and is idempotent. It does not enter film controls or
change a battle command, resource, or RNG. Strict 3.1 ignores the shortcut.
The historical `+Now Film Chris Include Reload Assert` remains available in
both modes with its original argument case and authorization behavior. Tests
cover the Modern shortcut, Strict bypass, repeated use and historical access.

**User-authorized Nanolathe developer tooling policy.** Make the recovered
State and Builder Probes accessible even where the researched retail image
retains their hotkey state but never calls their painters. Likewise, a portable
memory/performance view need not reproduce the disabled Windows helper-thread
startup. These controls are available with developer tools enabled in both
Modern and Strict 3.1: observing the battle is independent of gameplay mode.
The UI and documentation must identify restored dormant tools and host-specific
measurements honestly; neither is a newly discovered retail activation path.

Retain retail labels and arithmetic when established and meaningful. Do not
expose host memory addresses as unit identities or reconstruct executable record
layouts. Unresolved fields remain explicitly unavailable until their owning
research is settled; do not give an unknown value a plausible label or zero.
The dormant retail frame-counter accessor is constant, so any useful current
frame count must be labelled as a Nanolathe host measurement.

### 2.2 Observation cannot change the battle

All read-only views consume detached data published by the simulation owner.
Painting, switching a view, pinning a subject, resizing, profiling and exporting
must not enqueue gameplay commands, advance either authoritative RNG, perform
extra ticks, or alter resource accounting. I4 and I6 remain binding.

Retail's search-grid diagnostic consumes CRT randomness for goal-cell colors
[03 §3.12]. Recreating that draw on Nanolathe's authoritative CRT would make
inspection change the battle. As a **Nanolathe presentation policy**, use
presentation-owned color variation and never advance the authoritative stream.
The research retains the original draw behavior. Visual parity excludes an
exact historical sequence of these random colors; geometry and marker meaning
remain testable. This is not a Modern gameplay rule and does not vary with
Strict 3.1.

Host profiler and memory counters describe Nanolathe's Go/runtime/renderer
workload. They cannot claim to reproduce retail allocator statistics or timing
values. Reuse `internal/debugcapture` projections and existing phase timing
boundaries where appropriate; unknown or omitted measurements stay visible as
such. Keep wall-clock reads in the host boundary, never simulation packages.

### 2.3 Mutating commands are a separate contract

Developer inspection is not permission for a painter to alter a unit. Commands
such as stock refill, spawn, kill, owner/control changes, reload and forced
orders require typed input routed through the existing owner/command boundary.
Their execution ordering, ownership, resource effects and RNG effects must be
established before they are implemented. No unit records may be mutated from a
widget, renderer or background profiler.

Do not silently turn retail stubs into claimed retail functionality. `Mem`,
`Assert` and `DPrint` are no-ops in the studied executable, while `MemDump`
creates an empty file [01 R-PLAT-01 §9]. Useful replacements should be named and
documented as Nanolathe tooling. Deliberate crash/allocation-exhaustion commands
are recorded as retail evidence; they are not required for the inspection UI.
The existing Modern `+spawn` remains subject to its Strict bypass until a
separately designed retail developer-spawn command is implemented.

## 3. Ownership and public boundary

No new engine or second simulation owner is needed. Extend existing packages:

| Owner | Responsibility |
|---|---|
| `cmd/nanolathe`, `internal/input`, `internal/hud` | Developer activation, shortcut routing, probe target selection, view state and text layout |
| `internal/session`, `internal/frame` | Produce immutable diagnostic projections at the existing publication boundary |
| `internal/movement`, `internal/path`, `internal/world`, `internal/construction` | Supply named diagnostic data through their existing owners; no renderer dependency |
| `internal/client`, `internal/drawlist` | Compose terrain markers, contours, route geometry and text in the researched pass order |
| `internal/platform/ebitenapp`, `internal/debugcapture` | Host counters, portable capture/export and presentation resources |

**Public API contract for implementation units:** agree the named projection
fields and ownership before dispatching implementation. A view request names
its mode and inspected subject; the session owner returns an immutable snapshot
stamped with committed tick and the existing publication identity where
available. Consumers distinguish unavailable, stale and valid observations.
No API returns a live unit pointer, search heap or mutable terrain array. Query
order and enabled-view state cannot affect authoritative traversal or admission.

Nanolathe currently publishes `Session.DebugDisplayMode` through
`frame.Radar.MarkerMode`; the name does not make this a minimap view. Its existing
reset/cycle helpers have tests but no user activation. Extend or migrate that
boundary deliberately rather than introducing a competing selector. The
terrain and movement views need additional published data; the selector alone
is not an implementation of any view.

Use opt-in, bounded projections for expensive search/route data. Define what is
captured while paused: a stale committed observation must not be refreshed by
stepping the simulation. Reusing a pool slot must not silently make a pinned
inspector describe a different published occupant. Historical searches may be
unavailable; avoid retaining every expansion solely for the UI.

### 3.1 First delivery API

- `frame.Frame.Developer *frame.DeveloperView` is nil unless diagnostics were
  requested for that publication. The value and every nested slice belong to
  the frame slot. `Tick` identifies the captured observation; no request forces
  a tick while paused. `internal/frame/developer.go` defines the shared fields.
- `Session.SetDeveloperDiagnostics(bool)` opts in to the next ordinary
  publication. Publish map attributes/occupancy, the first selected local
  movement class, available shared search state, true-local current coverage,
  and supplemental unit/probe/follower fields. Existing `UnitView`, `Players`
  and `OrderQueues` remain the source for their already published fields.
  Unavailable search history or builder scores remain unavailable.
- `client.DeveloperOptions` carries `Mode uint8`, `Information bool`,
  `ContourSpacing, ContourOffset int32` in 1/256 height units, and
  `PickX, PickZ int32`, `PickValid bool` in whole world coordinates.
  `Client.SetDeveloperOptions(DeveloperOptions)` invalidates retained
  presentation when these options change. The existing session mode helpers
  remain the sole mode writer; the host mirrors their result to the client so
  a paused display can change without another simulation publication.
- `developerProbeTarget { Slot pool.Handle; InstanceID uint64; Enabled bool }` and
  `developerProbeSelection` with `State, Builder developerProbeTarget`, a COMIX
  `Font`, and shared previous-bottom `Layout` live in
  `cmd/nanolathe/battle_developer_probe.go`.
  `drawDeveloperProbes(c *client.Client, f *frame.Frame, selection
  developerProbeSelection)` draws only these detached observations and reports
  missing or stale subjects. The host owns hotkeys and target changes.

Nanolathe presentation policy uses a deterministic tick/cell-based goal color
sequence instead of advancing any random-number generator. This keeps repeated
rendering and paused-frame caching stable while retaining varied goal colors.
Reject malformed/non-finite parameters or negative contour spacing and spacing outside signed 32-bit
conversion range. Zero disables contours. This host input policy avoids retail's
nonterminating negative-spacing case; it does not redefine retail arithmetic.

## 4. Implementation sequence and acceptance

1. **Input and snapshot boundary.** Add developer state and recovered controls;
   preserve modal/editor ownership and the distinct authorization/film/view
   states. Add the named diagnostic projections required by the first views.
2. **Terrain views and contours.** Reproduce the five display modes, unit tint,
   markers and contour geometry. Implement identically in the classic and
   modern executors through shared composition primitives where possible.
3. **Unit probes and movement inspection.** Connect the dormant painters to
   explicit controls, using the recovered fields that are Established. Mark
   unresolved content unavailable and test target lifetime. Do not interpret a
   diagnostic flag as permission to bypass ownership in gameplay commands.
4. **Host profiling and capture.** Add useful Go/renderer counters and portable
   exports, retaining the retail-versus-host distinction. Preserve
   Ctrl+Shift+F11 diagnostic capture. Review F11, Shift+F1/F2, Ctrl+F9/F10 and
   the existing F9/F10 presentation controls together before wiring shortcuts.
5. **Mutating console tools.** Implement only after the individual command
   contracts have been traced, reviewed and assigned. Existing vocabulary is
   an investigation index, not permission to guess command effects.

Each implemented unit needs focused tests for ordering, integer boundaries or
state isolation, not a test census of every label. Required acceptance:

- Identical seeded battles with diagnostics off/on agree on affected
  authoritative fields and both RNG states/draw counts. A partial state hash
  alone does not establish this; use explicit snapshots for the affected data.
- Rendering twice, switching display modes and resizing never step simulation.
  Paused views retain honest tick/availability information.
- Input tests cover authorization, film entry/exit, mode wrap, editor/modal
  ownership and collisions with existing shortcuts. Dormant tool activation
  works in both gameplay modes without changing their gameplay policy.
- Authored heightfield fixtures cover contour boundaries and mode-specific
  markers; graphics are visually inspected for both executors. Verify draw
  order, clipping, palette colors and tinted unit visibility from captures.
- Run `tools/check`, `tools/check-retail`, the owning design gates, and the
  sequential classic/modern live battle performance check for renderer or
  snapshot-storage changes. Keep benchmark and capture artifacts outside the
  repository. Record the checks actually completed in the delivery section below.

## 5. First inspection delivery

### Controls

In Modern mode, enter `+dev` in TALK. The historical
`+Now Film Chris Include Reload Assert` also works in either gameplay mode:
the command name is case-insensitive; the five arguments must match exactly.
Both grant access; press F11 to enter film controls. Then:

| Control | Effect |
|---|---|
| F11 | Enter/leave film controls; leaving clears information and mode |
| lowercase `m` | Cycle normal, movement/search, occupancy, metal, local coverage |
| lowercase `i` | Toggle the diagnostic footer and selected ground-follower route |
| uppercase `P` / lowercase `p` | Capture/release the pointer |
| Shift+F1 / Shift+F2 | Pin State / Builder Probe to the hovered unit; no hover disables it |
| `\` | Replay the retained supported local command without another TALK message |
| `+Contour spacing offset` | Independent terrain contours, no authorization needed; spacing 0 disables |
| `+HostProfile` | Toggle explicitly named Nanolathe Go memory/battle-update counters after authorization |

The ordinary HUD remains visible. Film entry disables GUI accelerators while
pointer controls and text editors continue working. F11 exit and ordinary
implemented TALK/options close paths re-enable accelerators. Speed changes are
blocked during film controls. Ctrl+Shift+F11 retains its existing diagnostic
bundle capture, and F9/F10 retain their view-scale/renderer controls.

Both probes are restored dormant tools, independently accessible without the
password, film mode, selection or ownership gates. A pin keeps its publication
identity across ticks. Missing, dying, reused or mixed-tick subjects show an
explicit unavailable/stale message; Nanolathe does not reproduce the dormant
Builder painter's erroneous clearing of the State enable flag. The shared
previous-bottom panel layout is retained across observations. Re-recording the
same observation and target pair reuses its starting bottom, so paused redraws
and the two executors do not progressively change the panel. This is the authorized restoration
lifetime policy, not a claim that retail used safe identities.

### Available data and current limits

Snapshots are requested when authorization, film, contours or either probe is
active. The next ordinary committed tick supplies them; enabling a view while
paused displays an awaiting-observation message until normal play publishes
one. Already captured observations remain inspectable while paused. The
existing session mode writer is mirrored to client options for immediate
paused mode changes, and the renderer invalidates its retained world accordingly.

Terrain modes, contour arithmetic, current true-local coverage, occupancy,
raw metal values, available shared path-search status, cached movement-class
tiers, committed follower footprints/routes, unit state and both order queues
are connected. Goal colors follow the deterministic presentation policy above.
An unallocated movement-class layer or uninitialized search table is reported
unavailable; inspection does not allocate/revise an authoritative path layer.
Undefined combined-status arrow colors are omitted as documented in [03 §3.12].

The occupancy cross and footer pick accept framebuffer mouse coordinates,
restore the camera's beam origin before the live-zoom inverse, and resolve the
detached heightfield. The cross uses the resolver's sea-level floor over water.
It marks the resolved ground point [03 §3.12], so steep terrain can retain the
ground resolver's interpolation residue rather than matching every cursor pixel.

Builder options preserve authored order. **Candidate scores are currently
unavailable**: the inspection publication does not yet provide the candidate
scorer's owning-player context, including for human-controlled builders. The
painter supports the researched raw score and saturated bar when a future
producer supplies them; it does not normalize or manufacture probabilities.

The footer exposes committed tick, camera origin, available hovered stances,
live-unit count and picked cell/height. A dash means unavailable for the
unresolved platform/packet fields and the not-yet-published runnable budget and
on-screen sweep count. The host profile separately names Go heap allocation,
objects, cumulative allocation, GC cycles, goroutines and battle-update time;
it does not claim retail allocator statistics or nine-bucket timing parity.

The existing diagnostic bundle and PNG capture route remain available. Retail
movie-series controls, portable dedicated memory windows, full nine-bucket
profiling, mask-specific AI console routes and mutating developer commands
remain separate later work. Replay here only reaches already implemented
local commands; it is not a complete mask-15 developer console.

### Reproducible visual checks

The normal `--shot` route accepts `--shot-developer=0..4`,
`--shot-probe=state|builder|both`, and `--shot-contour="16 0"`.
`--shot-select` supplies the usual selected-unit context. These are host capture
options: they request observation before the configured warmup ticks and pin
probes to the first committed local unit afterwards. Use the existing
`--shot-renderer=both` route to inspect both executors. Captures and performance
artifacts stay outside the repository.

A moving check — a camera move, an effect over its whole lifetime, a title —
is a film capture rather than a shot: `--film` composes a scripted sequence at
exact sub-tick blend fractions and writes it as frames or as a stream an
encoder reads ([FILM_CAPTURE](FILM_CAPTURE.md)). It is the promotional-footage
route as well, and it takes no host lock.

### Validation recorded for the first delivery

Focused publication tests compare both gameplay modes with observation off/on,
including both RNG states/draw counts, resources, terrain, visibility, movement,
wind and shake. They also verify detached storage and allocation-free warm
publication. Input tests cover password/case/lifetimes, quickkey ownership and
close paths, tiny negative contour spacing, replay, probe identity, and terrain
picking. Renderer fixtures cover mode markers, contour boundaries, palette
rules, pass ordering, paused invalidation and tint-cache reuse. Repeated probe
recordings produce identical indexed output.

`tools/check` and `tools/check-retail` passed on the integrated implementation,
including the real GPU fixtures. A separate input/lifetime review found and
verified fixes for dialog quickkeys and sub-quantum negative spacing. Classic
and modern real-asset captures of all four diagnostic modes, contours and both
probes were inspected, alongside authored contour fixtures.

Sequential live-battle runs used the same scene metadata: Great Divide, seed 7,
300 pre-window ticks, 180 measured draws at 30 Hz, native scale, Modern gameplay,
and factories enabled. Each run had 329–340 live units, 153–195 moving units,
8 active builds, and 9–17 burning features. End-state diagnostic bundles were
byte-identical between the base branch, diagnostics-off implementation and
occupancy-mode implementation, separately for each renderer.

| Mean host draw work | Base | Tools off | Occupancy + information |
|---|---:|---:|---:|
| Classic | 15.52 ms | 15.60 ms | 16.86 ms |
| Modern | 10.28 ms | 10.28 ms | 16.91 ms |

These are single-run host measurements, not GPU execution or scanout timing.
The disabled path showed no material regression in this sample; enabled
occupancy drawing has an explicit presentation cost. Both executors' battle
captures and feature censuses were inspected. Artifacts remain outside the
repository under `/private/tmp/devtools-bench-*`; each enabled run has a
`developer-options.json` sidecar because the existing benchmark scene metadata
does not include these new capture options.

## 6. Later extensions

Potential later work includes richer path-search inspection, unit-order and COB
views, construction-admission explanations, pinned comparisons and exporting a
selected subject into the existing diagnostic bundle. These are ideas, not
retail claims or committed feature behavior. Design them after the recovered
views work and prove that observation leaves the battle unchanged.
