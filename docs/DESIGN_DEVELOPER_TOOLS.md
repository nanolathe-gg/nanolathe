# Developer tools

## 1. Status, scope and evidence

**Status: researched and planned; the developer UI is not implemented.** The
2026-09-15 user request brings retail developer views into Nanolathe's planned
scope and explicitly authorizes enabling tools whose painters survive in the
executable but whose activation/rendering paths are disconnected. This document
is the implementation contract, not evidence that any UI has shipped.

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
  repository. This documentation-only research pass does not claim these
  future visual/performance acceptance checks have run.

## 5. Later extensions

Potential later work includes richer path-search inspection, unit-order and COB
views, construction-admission explanations, pinned comparisons and exporting a
selected subject into the existing diagnostic bundle. These are ideas, not
retail claims or committed feature behavior. Design them after the recovered
views work and prove that observation leaves the battle unchanged.
