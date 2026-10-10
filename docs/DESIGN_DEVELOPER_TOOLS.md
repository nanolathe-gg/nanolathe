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
| Commands that change the battle | [07 R-CAM-01 §6], [01 R-PLAT-01 §9] | Default unit-spawn handler implemented in §8; other mutating tools remain deferred |

This scope does not add multiplayer, restore competitive synchronization, or
turn a diagnostic snapshot into a save. Existing [diagnostic capture](DEBUG_CAPTURE.md)
and the Modern `+spawn` command keep their own documented contracts.

## 2. Tooling policy

### 2.1 Reconnect dormant tools

**Nanolathe host input policy — developer shortcut (user-authorized
2026-10-09).** `+dev` grants developer authorization in every rule set,
including Strict 3.1, Community 3.9 and Modern; it is case-insensitive,
takes no arguments and is idempotent. It does not enter film controls or
change a battle command, resource, or RNG. The existing online command
filter still refuses developer activation in multiplayer.
The historical `+Now Film Chris Include Reload Assert` remains available in
every rule set with its original argument case and authorization behavior
`[07 R-CAM-01 §9]`. `TestDevShortcutPreservesHistoricalAccessAndBattleState`
covers all three reserved modes, repeated use, argument rejection, historical
access and unchanged RNG/resource state. This is host input convenience and
adds no gameplay seam.

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
The Modern `+spawn` keeps its Strict bypass. The separate retail developer
default handler is available in both modes under §8.

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

In any rule set, enter `+dev` in TALK. The historical
`+Now Film Chris Include Reload Assert` also works in every rule set:
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

## 7. 3D unit viewer preview

**User-authorized Nanolathe presentation policy (2026-10-01).** The unit
viewer is an unfinished, hidden preview: there is no main-menu Tools button
or public shortcut listing. Ctrl+U at the main menu opens the viewer directly;
the shortcut is inactive in child windows, modal dialogs and text editors.
The Nanolathe screen's *Unit restrictions* card opens it as the restriction
editor below; that card is the visible route (DESIGN_MODS_MUTATORS §15.9).
This maintainer contract records the entry without advertising it in the UI.
The searchable catalog and textured, rotatable unit model are inspired by the
official Cavedog viewer. This is Nanolathe tooling, not a reconstruction of
that program's interface or behavior, and is available in every gameplay mode
without developer authorization.

The user-authorized refinement matches the Nanolathe settings screen
(DESIGN_INTERFACE_HUD_INPUT §3.17): its existing original metal button art,
dark textured surfaces, bundled display/body fonts, gold headings and green
active indicators. `screenkit` draws at device resolution. A detached
`ui.Panel` retains editor, list, scrollbar, button and keyboard-focus behavior;
input measurement uses the same typefaces and sizes as the painted controls.
The catalog, model and scrollable unit data retain distinct regions with
consistent spacing. Library rows carry each unit's build picture. The unit
data column has Stats, Weapons and Build tabs drawn as the settings screen's
text tabs; its rows have gold section headings, dimmed labels, right-aligned
values and a dimmed unit column, and every row is one native list row so
wheel, scrollbar, painting and hit testing share one geometry. The selected animation has a visible active indicator;
unsupported callbacks keep disabled controls. A subdued drafting grid and
circular guide decorate the model bay; they remain fixed to the display and
represent neither terrain nor a battle shadow. No GUI artwork is copied into
the repository and no settings-screen state is changed by opening the viewer.

The catalog is the running content set's immutable preview catalog, with the
same directory layout and content limits as the game's loader. It lists every
retained definition, including units outside build menus and records hidden by
duplicate lookup names. Display order is name, internal unit ID, definition
number, then catalog ordinal to break ties. Search is case-insensitive; each
whitespace-separated token must match a name or internal ID field. Filtering
preserves order and selection when the selected record remains present. No
match clears the preview. Switching content rebuilds the screen from the new
mount, so stale definitions cannot cross a content boundary.

Click a list row or use Up/Down and Page Up/Page Down to select. Drag inside
the model stage to rotate horizontally and vertically, including the underside.
Wheel over the stage zooms from 35% to 300%; wheel over either sidebar scrolls
that sidebar. The model turns once every eighteen host seconds by default,
pauses while dragged, and resumes on release. **Rotate** toggles this;
**Reset view** restores orientation and fit. Ctrl/Cmd+F focuses search,
Ctrl/Cmd+A arms replacement of its text; the native editor supplies caret,
Home/End, Delete and Backspace behavior and its existing byte and width limits.
Tab traverses the native controls;
outside search, Space toggles rotation, R resets, Left/Right turn and +/- zoom.
Esc or Back returns directly to the main menu, or to the Nanolathe screen
when its card opened the viewer. Closing consumes held keys
and buttons until release. These constants are viewer preferences, not retail
gameplay arithmetic.

Clicking a Build-tab entry (a press and release on the same entry) selects
that unit. Every selection — library click, keys, search or link — records
the unit it replaces. The `<` control, Alt+Left, or Backspace while the search
is not being edited returns to the previous unit; `>` and Alt+Right go
forward again. Alt+arrows never turn the model or reach the editor. The
history keeps 64 units and is discarded on close. A unit reached through a
link or the history that the current search excludes clears the search, so
the library always shows the staged unit. The selected tab persists across
selections, so the build tree can be walked.

The turntable applies yaw before display-space tilt, converted to the model
renderer's existing body orientation order [03 §2.4], C21. This keeps its vertical
axis upright throughout a turn instead of making the unit wobble as it rotates.
At a vertical orbit pole the equivalent orientation uses zero bank. The default
tilt is 45 degrees, using 94% of the conservative projection fit; both are
presentation preferences. Stable placement and final-resolution projection
below keep the orbit from amplifying game-resolution rounding.

`cmd/nanolathe/unit_viewer*.go` owns the screen and detached view state.
The control adapter uses a private panel and shared immutable menu assets;
it does not change the underlying menu panel.
`frontendScreens` routes it and the settings screen through the existing
`ebitenapp.FullScreen` boundary (DESIGN_INTERFACE_HUD_INPUT §3.17), which
suppresses input to the underlying menu. Catalog compilation runs in a host
worker; closing joins it before the content can unmount. The same worker
derives the build tree once per catalog load. GPU resources and model caches
belong to this screen and are released on close.

**Restriction editor.** The editor that DESIGN_MODS_MUTATORS §15.9 owns
lives in `unit_viewer_restrict.go`. Its controls are native panel controls,
so they share focus, Tab and hit testing with the rest. The data column's
list keeps 20 rows, and under it a *Restriction* block level with the stage's
rows holds the selected unit's state, one note line, the On/Off switch, the
count stepper with its readout, and *Reset*. The note says why a
`norestrict` unit or a commander is limited (with a padlock), that the entry
covers all N records of a duplicated name, or that Shift steps by ten. The
*Restricted only* box sits on the library's count line, each row draws its
name's state on its ID line (a cap as a chip, *Removed* in red with the row
and picture dimmed, a padlock with *Norestrict* or *Commander*; the ID shrinks
a little beside a state before it would clip), and the library's footer
counts the names removed and capped and the entries the content leaves out.
The keyboard hint joins the stage's. The names' locks come from
`Catalog.CheckRestrictions` over every name at 0, run once by the catalog
worker, so the editor locks exactly what battle entry refuses. Choices the
design left to the viewer: the switch reads On for a cap and switches it
Off; Shift with a step button or the wheel over the stepper moves to the next
multiple of ten (No limit down gives 100 either way, and up gives 10); and a
single unit's edit keeps the filtered list, so the selection never jumps,
while *Reset*, the box and the search filter again. Opened from the card the
heading reads *Unit restrictions* and Back hands the draft back; opened with
Ctrl+U it starts from the running content's saved set, names it lacks
included, and *Apply* with the changed-name count appears beside Back once
the draft differs. Apply selects the set,
saves the settings and refreshes the main menu's chip; Back discards. The
editor tests (`TestUnitViewerRestrict*`) lock the row states, the stepper
ends, Reset emptying the set, *Restricted only*
and both routes' draft, Apply and Back.

**Build pictures.** Library rows and build-tree entries show
`unitpics/<unit name>.pcx` from the running content through the settings
screen's asynchronous picture loader (DESIGN_INTERFACE_HUD_INPUT §3.17),
wrapped with an already-resolved empty plan so none of that screen's card
planning runs. Only visible rows request pictures; the loader's worker decodes
them and Draw uploads the decoded pixels without waiting. A picture still
decoding, or absent from the content, shows a neutral empty frame; no
substitute art is drawn. Closing halts and joins the picture worker, waiting
for the file in hand, and deallocates the uploaded images before the content
can unmount. No unit
allocator, simulation clock or authoritative RNG is used.

**User-authorized animation preview policy.** Each selected action owns a fresh
isolated presentation VM and first presents a completed unit as the battle's
already-built creation makes one [04 R-CB-01 §4]: authored `Create` with its
immediate drain; for each of the three weapon slots the synchronous `Query*`,
then `AimFrom*` with a second `Query*` when AimFrom leaves its −1 seed; the
deferred `SetMaxReloadTime` carrying the longest compiled reload across all
three definition pointers [04 R-CB-01 §2]; the creation-time extractor
`SetSpeed` when `extractsmetal` is positive [04 R-CB-01 §5]; and last the
already-built activation of an `activatewhenbuilt` definition through the edge
machine [04 R-SPEC-01 §12][05 R-SHARE-01 §8]. The deferred starts first run in
the first preview tick's drain. On that tick's general update a wind generator
receives `SetDirection`, then `SetSpeed` with the speed shifted left by four,
once, as one wind re-roll would issue them [04 R-CB-01 §5]. Every preview tick
follows the established unit-visit order: general update, weapon update, normal
drain, order and construction work, movement, then slot-end death handling
[04 §5.4].

The action row shows the actions that apply to the selected unit's class and
disables those whose callbacks are absent; the buttons share one width when the
row has room and otherwise take their caption widths:

- **Idle** is the completed unit at rest.
- **Move** (mobile ground units) runs the ground speed word along a straight,
  level route to a distant goal: each tick adds `Acceleration` up to
  `MaxVelocity`, the level pitch-table cap [04 R-MOV-01 §4], and the
  movement-rate classifier issues `StartMoving` then `MoveRateN` on leaving tier
  0 and `MoveRateN` on later tier changes, each with its wake barrier
  [04 §5.2][04 R-MOV-01 §6].
- **Fly** (`canfly`) runs the battle's flight integrator
  (`movement.IntegrateFlight`) over the preview's flat world. The first order
  visit is `VTOL_Move`'s takeoff preamble: activation rises (`Activate`, the
  takeoff hook), mover mode 2, and a climb marker at half the cruise altitude
  [04 R-AIR-01 §6][04 R-AIR-02]. When the climb arrives the next visit installs
  a cruise goal straight ahead; the classifier issues whatever tier changes the
  integrated speed produces, so an aircraft whose `MoveRate1` lies below its
  `MaxVelocity` reaches `MoveRate2` in flight. The cruise is held once the tier
  equals `MaxVelocity`'s and the climb has ended, or after 60 seconds. While
  airborne the button reads **Land**: `VTOL_LandIfCan`'s preamble, then
  `EndTransport` with its wake when authored, a landing marker on the ground
  below and the falling activation edge (`Deactivate`, the landing hook); at
  arrival the mover-mode setter grounds the aircraft and the classifier issues
  `StopMoving` [04 R-AIR-01 §3][04 R-AIR-01 §6]. The preview position is always
  landable, so the landing search and its random bearing are not taken. A zero
  `MaxVelocity` disables Fly: retail's integrator faults on it [04 §10.1].
- **Fire** (one action; the user merged the former separate Aim action into
  it on 2026-10-05) uses the selected active weapon's `AimPrimary`,
  `AimSecondary` or `AimTertiary` with a fixed heading of 45 degrees and pitch
  of approximately 15 degrees, and needs only that Aim callback. It waits for
  an explicit nonzero Aim return, invokes the matching Fire callback followed
  by authored `RockUnit` when present, waits at least the selected weapon's
  compiled base reload interval, then starts a fresh aim. A script without the
  Fire callback misses that start, as the battle's spawner does, and the loop
  continues [04 R-CB-01 §2]. The minimum repeat interval is one preview tick;
  this timing is viewer policy, not predicted battle cadence.
- **Build** (builders) takes the mobile work handlers' path when `bmcode` is 1:
  the slot-form `StartBuilding` carrying the relative bearing to the work target
  [04 R-CB-01 §3], then the script-owned build-stance wait, then one synchronous
  `QueryNanoPiece` on each tick that carries accepted work
  [05 R-P0-06 §1][05 R-P0-06 §2]; an aircraft builder polls the stance and
  discards the verdict. A `bmcode` 0 builder takes the factory production state
  machine: raise activation, wait for the stance, query `QueryBuildInfo` for the
  pad, raise the building edge (`StartBuilding`, edge form), then the same
  per-step nano query [05 "Factory production lifecycle"]. Every work step is
  admitted. A factory builds its real products on the pad and both paths draw
  the nanolathe spray (below). While engaged the button reads **Stop**: the
  mobile path issues the slot-form `StopBuilding` with four zero cells
  [04 R-CB-01 §2], and the factory lowers the building edge and then
  activation, as a completed final product does, and discards an unfinished
  product. Build restarts the same order; a factory then rebuilds the
  interrupted product from a fresh nanoframe.
- **Hit** issues the normal-kind damage pair: the health falls first, then
  `HitByWeapon` with cos and sin of the direction at radius 400 through the
  shared table and the independent `TakeDamage` with the clamped post-hit
  percentage [04 R-CB-01 §2][04 §5.1].
- **Death** plays the unit's real battle death in the field (below): the
  stage leaves the turntable for a tiny real battle in which the selected
  record stands alone and, after a one-second settle, dies through the
  battle's own death path at the selected severity. Its `Killed` script,
  death explosion, debris with their smoke and fire trails, and corpse are
  the battle's own [06 §12.1][06 §12.2]. The field restarts seven seconds
  after the death. The status names the corpse the death left, its depth in
  the corpse chain and its reclaim metal, or says it left none.
- **Wreck** plays the same field death and, once its hold has passed, hands
  the stage back to the turntable with the corpse that death left: explosion
  first, then the inspectable wreck. Depth 1 is the authored `Corpse`, and
  each further step follows `featuredead`, so depth 2 is the heap
  [06 §12.2]. The feature resolves through the immutable catalog's feature
  table and its 3DO loads through the unit model path; it is drawn as the
  battle draws a 3DO feature, through the structure path with the height plane
  and its own fixed fit [03 R-REN-03A §2]. The status names the feature, its
  depth and its reclaim metal. A death that left no corpse, a sprite-only
  feature or an unloadable model is reported in the status with an empty
  stage; nothing is substituted.
- **On/Off** (definitions with `Activate` or `Deactivate`) drives the
  activation edge machine as an order does: `Activate` on the rising edge,
  `Deactivate` on the falling edge, nothing when the bit is unchanged
  [04 R-UNIT-06 §2]. It acts on the current preview at once and records an
  explicit choice that each later action applies after the creation sequence.
  Fly and factory Build raise and lower the same bit as the battle does; the
  button's indicator and the status show the live state.

A factory's action row adds **Speed** after Build. It cycles 1×, 4× and 16×:
the number of construction steps the product cycle applies per preview tick,
because a large product takes minutes at normal speed. It changes nothing
else: scripts, nano queries, the spray and the pulse keep the 30 Hz preview
tick. The choice is a viewer preference for the open screen, kept across
unit selections and reset when the viewer opens; it is never written to
settings.

The control row keeps Pause, which stops animation and the field
independently of rotation, Rotate, Reset view, Weapon and **Severity**, which
cycles the Death and Wreck severities and replays either in a fresh field.
Choosing an action again restarts it, except Fly and Build, whose buttons land
or stop and resume within the same preview. Changing the selected unit
restores Idle, weapon 1, the first severity and the creation activation state.

**User-authorized field preview policy (2026-10-06).** Death and Wreck stage
"In the field", a small real battle on the viewer's stage, through the
Nanolathe screen's live-preview machinery (DESIGN_INTERFACE_HUD_INPUT §3.17,
`nlPreview`); `cmd/nanolathe/unit_viewer_field.go` owns it. Staging runs on
that machinery's worker while the stage reads *Staging...*; each choice of
Death or Wreck, and each Severity press, stages a fresh run. The battle is an
ordinary skirmish composition on a stock map with no armies: the two start
commanders stand at their own start positions out of frame, fog is lifted as
the settings scenes lift it, the computer player is passive, no mutator or
restriction applies, and the lobby's commander-death rule is "game
continues" (rule 0), so a commander's death plays alone instead of
eliminating its owner [08 R-SKIR-01 §3]. It runs under the player's gameplay
mode and is drawn with the player's presentation choices through the same
renderer path as the settings previews, at the stage's device size. Trees and
other destructible features are cleared about the site's anchor, and the
selected record is created by the ordinary allocator on the nearest spot the
placement validator accepts, owned by the viewing player and holding
position. An aircraft stands where that allocator creates it, on the ground.
A record whose placement profile asks for water under it tries the water
site first; whichever site comes first, a refusal moves the field to the
other, and a refusal at both is reported with both reasons and nothing in the
unit's place. A record hidden by a duplicate name is refused, since no battle
creates it by name.

The death goes through the battle's own path as the settings scenes' kills
do: the unit's prior health sample and health are set, then its death is
latched with no recorded damage kind, so the full pipeline runs the
synchronous `Killed` query, the `explodeas` death explosion and the corpse,
and nobody is credited [06 §12.1]. Severity is clamp(((−health·100) ÷
maxdamage + prior sample) ÷ 2, 1, 100), with an unsigned divide and a
truncating halving [06 §12.1]; the field takes the prior sample min(100,
2 × severity) and the least-negative health that completes the selection —
0 up to severity 50, then −⌈(2 × severity − 100) × maxdamage ÷ 100⌉ — so the
script receives exactly 25, 50, 75 or 100. The current sample takes the same
value, so a 30-tick sampling boundary on the death tick leaves the input
unchanged [04 §5.1]. Retail reads the health as a signed 16-bit word, so a
selection whose health would not fit (severity 100 above 32,768 hit points)
tries smaller samples and is otherwise reported as unreachable rather than
approximated. The corpse is read back from the battle: a feature of the
unit's corpse chain on the footprint where it died, whose chain position is
the depth reported. The field is presentation only: its battle is never
saved, networked or seen by any other battle, and no setting is written.
Another action, another unit, the history keys or Back close its battle so
it holds no CPU; a run still staging is closed when it arrives, and closing
the viewer waits for it. The field steps at the preview machinery's 30 Hz
with at most two ticks per display frame.

These explicit preview inputs stand in for battle and map state; none is a
retail value:

| Input | Preview value |
|---|---|
| Aim heading and pitch | 45 degrees; about 15 degrees |
| Build work target | 45 degrees off the builder's facing (the `StartBuilding` bearing); a mobile builder's spray target sits on the ground there, one fit radius from its origin, with the builder's own footprint and model-top box |
| Factory products | the Build tab's retail list in order, every step admitted; a completed product holds the pad for 60 preview ticks (two seconds) before the next nanoframe starts |
| Nanoframe identity | the product's sequence number in the open action (1, 2, …) and the preview tick, as the pulse's identifier and tick |
| Spray randomness | a private generator with a fixed seed, restarted with each action |
| Wind | one re-roll before the first tick: heading 45 degrees, speed 1050, the midpoint of the canonical fallback range 100–2000 [05 R-PROD-01 §3] |
| Extractor footprint | every covered cell holds metal byte 127, so `SetSpeed` carries footprint cells × 128 |
| Hit | direction byte `0x80`, from straight ahead [06 §9.1]; post-hit health half of `maxdamage` |
| Death severity | 25, 50, 75 or 100; the field's death takes the prior health sample min(100, 2 × severity) and the least-negative post-hit health that completes it [06 §12.1] |
| Field site | Greenhaven at (2308, 4386), the settings blast scene's flat ground, cleared of destructible features from 480 pixels west to 480 east and from 400 north to 480 south; Coast To Coast at (2284, 1188), the naval scene's deep water, for units the ground refuses; seed 7 |
| Field spot | the nearest spot to the site's anchor, within 24 rings of 16 pixels, that the placement validator accepts |
| Field camera | the battle's still camera, centred on the unit's footprint placed 12% of the picture's height below its middle; zoom makes the picture's width span six footprints, between 0.75 and the detail view's 2 |
| Field timing | one unseen lead-in tick, a one-second settle with the unit standing, the death, then a seven-second hold (by which stock debris has landed, burst and cleared) before Death restarts or Wreck hands over to the turntable |
| World | flat ground at height 0 with no air sector grid, an empty yard whose admission always passes, a straight level route; the cruise goal lies 30,000 world units ahead |

Engine ports read and write a detached copy of the unit's state: activation and
armor drive the edge machine, the in-build stance, busy, yard-open and
bugger-off flags read back what the script wrote, and health reads 100 until a
hit (then the post-hit percentage) or a death (then 0) [04 §4.4][04 §4.7].
Build percent left reads 0, except in a factory product's own script, where it
converts the product's remaining fraction as the battle's port does
[04 §4.4]; other unbound reads return zero and random requests
return their low bound without drawing. No world, allocator, authoritative RNG,
projectile, resource, sound, debris or effect sink is connected, and no medium
band is classified, so `setSFXoccupy` is never issued. The VM retains the
existing detached model flags. All of this describes the turntable's
presentation script; the field is a real battle and none of it applies there.

The animation exposes three read-only accessors: `building()` reports a
construction order that has reached its stance and carries work;
`nanoPiece()` is the model piece index of the latest `QueryNanoPiece` answer,
mapped from the script piece through the strict name link, or −1 while no
work step runs; and `padPiece()` is the factory's `QueryBuildInfo` piece as a
model index, or −1 for a mobile builder, a factory that is not building or an
answer that names no model piece.

**User-authorized product preview policy.** A factory's Build action
constructs its real products, never a stand-in. The cycle takes the Build
tab's entries in order and keeps the retail list only: a Modern-only entry,
a record another definition hides and a discovery-only record are left out,
and a product whose model will not load, or which the renderer refuses, is
dropped and never retried. The status names every omission with its reason;
nothing is substituted. When the building edge rises, the pad answer reaches
the product as one signed byte, so an index of 128 or more, or an unanswered
query, hangs it at the factory origin [04 R-FAC-02 §1]. A fresh nanoframe of
the next product is then composed as the record's attachment, posed by its
own presentation script: `Create` and the creation-time queries run as for a
completed unit, without the completion-time activation, and its
`BUILD_PERCENT_LEFT` reads its own fraction. That script runs once per
preview tick after the factory's, as a later unit in the sweep. The pad's
placement is resolved again for every record because a pad can turn
(DESIGN_GPU_RENDERER §22.5). An answer naming a script piece at or beyond the
model's piece count hangs the product at the factory origin, where the piece
locator answers the unit's own position for such an index [04 R-COB-01 §3]
[04 R-REV-02]. An answer below the model's piece count but past the script's
declared pieces addresses the record the link pass left in that slot; the
viewer's link map covers only declared pieces, so it places neither product
nor spray there (stock scripts answer a declared piece). A building-class product is drawn
at its pad like any other; the battle's cell snap of its unattached position
has no map grid to snap to in the preview.

Each preview tick of work applies the shared construction step with the
factory's integer worker quantum and the product's `buildtime`, from a
remaining fraction of one [05 R-WORK-01 §1][05 "Construction arithmetic"]:
the fraction falls by quantum ÷ buildtime at working precision, clamps to
0..1 and narrows to single precision, so a product completes after exactly
`construction.WorkTicks` steps. A zero quantum or a stored zero commits
nothing, issues no nano query and draws no spray [05 R-P0-06 §1]. Accepted
work then queries `QueryNanoPiece` and emits one spray record, in the order of
[05 R-P0-06 §6]. At zero the product's own activation runs for
`activatewhenbuilt`, then the factory's building edge falls (`StopBuilding`)
while activation stays raised [04 R-FAC-02 §3][05 "Factory production
lifecycle"]. A battle product then leaves the pad under its own `GetBuilt`
order while the factory's next placement waits for its exit; the viewer has
no movement, so the completed product holds the pad for its hold interval
instead. The order then returns to its stance test with activation still up,
so no second `Activate` runs, queries the pad again, starts the next product
and raises the building edge. The status reads "Building ARMPW 43% / 9.2 s
left at 1x": the percentage is the one `BUILD_PERCENT_LEFT` implies, and the
time is the steps left from `construction.WorkTicks` divided by the speed,
truncated as the Build tab truncates; a zero quantum, a non-positive
`buildtime` or a product over a day of work says so rather than guessing.
During the hold it reads "Built ARMPW / next ARMROCK".

**User-authorized nanospray preview policy.** Every accepted work step emits
one emitter record as the battle's does [03 R-P0-19-P][03 R-STRIP-01 §2]: the
source is the `QueryNanoPiece` piece origin, the target is a unit target's
box — the product's position plus its footprint and model-top extents
[05 R-WORK-01 §8] — and both are narrowed per axis to their 4/11..7/11 span.
The record spawns five particles at once and five more on the next tick; each
particle picks a source and a landing point, flies four world units per tick
for `trunc(distance/4)` ticks (a zero-length hop is discarded) and shimmers up
`0xa1..0xa7`, its nibble starting at one plus its spawn index modulo seven.
A record is removed once its list empties; past 400 records the oldest is
evicted. The picks come from the viewer's own generator, never the
simulation or CRT stream; it is seeded with a fixed value for each action so
captures repeat. Positions are kept in the factory's root-local frame, the
frame of the attachment's placement, so the spray turns with the orbit as one
body with factory and product, and a root piece animated by its script carries
the spray as it carries the product. A mobile builder sprays into its
stand-in target. Particles already in flight finish after Stop.

Particles project through the record's own projection: the root piece's
current state with the view orientation folded in, the pivot and the raster
scale, as model vertices do; a source point lands within 0.01 output pixel of
`ProjectedPieces` for the nano piece. Each mark is the battle's two-by-two
pixel square at the preview's magnification — a battle pixel is one world
unit, so a mark spans two world units of the projection, from the particle's
point right and down — because that coverage is what makes the stream read as
a spray [03 R-P0-19-P]. Marks are drawn at device resolution over the stage
and clipped to it, in the stock palette the preview already uses. There is
no depth test: strip 6 draws over grounded units and structures in the
battle. An airborne builder's stream, which the battle draws beneath its hull,
does not arise because the preview builds on the ground. Neither the battle's
local-player coverage gate nor team-coloured nanolathe (DESIGN_GPU_RENDERER
§37.1) applies to the viewer.

**Walk smoothing (user-authorized 2026-10-06).** With Enhanced presentation,
the regular viewer's Move action uses `internal/poseblend`, the same bounded
per-axis policy as battle presentation (DESIGN_GPU_RENDERER §13.5). Each
completed detached-VM tick is recorded, including catch-up ticks, and fractional
refreshes sample held axes three ticks behind the current preview tick. Axes
without short holds retain adjacent-tick interpolation; once a held axis is
recognized its phase persists across dense keys until a longer hold or reset,
as the shared policy specifies. Other actions and Original presentation
retain raw poses. The VM and its 30 Hz arithmetic [04 §4.6] never receive
interpolated values or changed sleeps. The projected viewer geometry already
retains fractions through final projection.

Selecting a unit or restarting/changing an action resets history. Pause freezes
the clock. The regular viewer inherits its presentation choice from settings;
no new setting or unit-name allowlist is added. The diagnostic
`--walk-preview armfido` window starts Move and lets **I** compare Original and
Smooth without restarting the VM. `--walk-preview-original` starts raw. With
`--shot`, `--walk-preview-frames N` captures N numbered PNGs at deterministic
120 Hz after `--shot-ticks` warmup ticks (one output file when N is one).
This diagnostic never writes settings.

Verification covers owned endpoints, discrete boundaries, held/continuous axis
separation, angle wrapping, bounded history, and unchanged VM playback at
30/60/120 Hz. The installed Fido test checks changing poses per displayed frame;
actual Original/Smooth captures verify the intermediate leg poses.

The preview runs at 30 Hz with at most five ticks per host update, dropping
excess elapsed time. Each thread retains the existing 4,096-instruction
execution bound. An aim that has not completed after 300 preview ticks, a build
stance not reached in 300 ticks, and a landing that has not arrived in 60
seconds each stop playback with a visible status; zero returns and interrupted
aims never authorize firing. A `Create` that ends without a return,
diagnostics and instruction exhaustion fall back to labeled authored geometry;
other callbacks may be signalled by later ones as authored behavior. The fit
radius is calculated from the creation pose and retained across playback and
action changes. These inputs are an explicit Nanolathe preview context, not
historical game behavior.

`ModelPreviewRenderer.RecordProjectedGeometry` resolves the 3DO, piece
transforms, textures, team-color bank and palette using the same source as
battle rendering [03 §2.4][03 §2.4.1]. The definition selects
structure shading (`BMCode == 0`); no unit-name special cases
are added. A fixed fit radius encloses the centered bounds of the visible
hierarchy's drawable vertices and parent translations, excluding hidden pieces,
selection plates, undrawable primitives and attachment-only points. No terrain,
waterline or battle shadow is claimed. The viewer uses its isolated GPU model path under
either battle renderer preference, without changing that preference. A missing
model reports an error while its catalog entry and available statistics remain
browsable. `ModelPreviewOptions.Attachment` composes a second model, such as a
factory's product under construction on its `QueryBuildInfo` pad, into the same
projected record, and `PiecePlacement` and `ProjectedPieces` place it and
report piece points (DESIGN_GPU_RENDERER §22.5).

**User-authorized smooth preview projection.** The viewer supplies
`ModelPreviewProjection{PixelsPerUnit, Pivot}` to the isolated projected-geometry
entry. `PixelsPerUnit` is the final raster scale. `Pivot` is an already-oriented
model-relative reference point, subtracted only from screen X/Y projection.
The projected record carries a separate durable `ModelPreviewGeometry` with
floating screen coordinates and oriented, fractional model-relative height.
The 2× antialiasing raster consumes these fractions directly; it must not double
already-rounded native coordinates. The shared material walk, textures, UVs
and shading remain the source of its appearance. The ordinary geometry packet
is retained alongside it for bounds and existing diagnostic consumers. Ordinary `RecordModel` and
`RecordGeometry` calls retain their existing arithmetic, as does every battle.

**User-authorized preview surface precision.** The viewer compares geometric
depth instead of the battle renderer's whole-unit, wrapping byte height keys.
Interpolating those keys through rounded scanline endpoints magnifies tiny
depth errors into sawtooth face intrusions; simply widening the stored key
does not fix the distorted interpolation. Screen coordinates and depth now
remain fractional through triangle rasterization. A model-relative 24-bit
depth image resolves negative heights and models taller than 256 units.
Texture and shade mapping retain the existing quad mapper independently of
the depth comparison. All units use depth, including assets whose retail
definition requests painter order.

Surfaces within 1/1024 world unit, plus two depth code points for device
rounding, use stable first-recorded priority. This is a viewer policy for
nearly coincident authored faces and fixed-point transform noise, not a
retail tie rule. The aircraft plant's separated pad/base surfaces retain their
geometric order; the vehicle plant's almost coincident pad/side surface uses
that stable priority. No unit or piece names select a bias. Transparent index-1
texels write neither colour nor depth, and reverse-winding faces remain culled.
The GPU implementation and its source lifetime are in DESIGN_GPU_RENDERER §22.5.

The viewer captures a Create-pose bounds center in root-local coordinates and
the reference root state once. Each view transforms that fixed point through
the current body orientation; animated poses never recalculate it. Current
composition bounds determine allocation only, while the canvas center remains
the anchor. This removes bounds-driven whole-model shifts and amplified
game-resolution rounding. Large subjects or canvases reduce the canvas and project again
at the reduced raster scale to fit the bounded GPU surfaces, preserving the
displayed zoom and aspect. Final raster quantization and authored 30 Hz COB
pose updates remain; this preview does not change simulation animation timing.

The projected-geometry entry is intentionally for complete isolated models.
It rejects explicit view scale, and attached children, construction, cloak
and special silhouette options for the previewed model itself, rather than
silently changing their ordinary preview contracts. Only
`ModelPreviewOptions.Attachment` composes a second model, which may be a
nanoframe, into the record (DESIGN_GPU_RENDERER §22.5). `Scale` must remain
zero; `PixelsPerUnit` supplies the scale. Each projected call also samples the
supplied orientation without retaining the battle cache's small-angle
threshold. No gameplay rule or new
renderer preference selects this projection.

Information comes only from the compiled definition, and every figure is a
base value before mutators. Rows that are zero or do not apply are omitted.
The Stats tab groups Overview (description, side, health, footprint and the
authored capability flags as tags), Economy, Mobility (mobile units only) and
Sensors. Costs and the mobile statistics reuse the established unit-info
conversions [07 R-HUD-03 §8]. Build work is authored `buildtime`, not elapsed
construction time [05 "Construction arithmetic"]. Energy and metal make and
use carry "/s" because the authored value is added once per settlement pass
and settlement runs every thirty ticks [05 "Authoritative settlement order"];
extraction, wind, tidal, storage and cloak values are shown as authored.
Ranges stay in world units (wu) [02 "Unit record"]. Discovery-only records
whose gameplay fields were not parsed say their statistics are unavailable
[02 R-CAT-01 §5].

The Weapons tab has one card per active weapon: display name, default damage,
the per-unit `DAMAGE` overrides whose first-match value differs (grouped by
value with their authored unit names) [06 §9.2], base reload, burst and burst
interval when burst exceeds one, range, blast radius and edge effectiveness,
velocity, energy and metal per shot, and the retail weapon flags as short
tags. The record already holds ticks and 16.16 world units per tick
[02 "Weapon record"]; the card shows seconds and world units per second. The
blast radius is the halved authored `areaofeffect` [06 §9.3]. `toairweapon`
reads "air targets only" because automatic acquisition admits only airborne
targets [06 §3.1]; inert community target keys are not shown as abilities.
"Death explosions" gives `explodeas` and `selfdestructas` the same damage,
radius and edge rows. Base reload is compiled ticks divided by 30, not
predicted firing cadence [06 §4.2]; the tab says so.

**Nominal DPS** is a labelled presentation figure: default damage ×
max(burst, 1) ÷ base reload, since an authored burst of N releases N pellets
and zero releases the root projectile [06 §4.3]. Its footnote states the
formula and that it ignores aiming, travel, damage overrides and falloff.
It is omitted for command-fire, stockpile and paralyzer weapons, zero damage
or reload, a negative burst, and a dropped weapon whose burst exceeds one:
the dropped creator writes no burst count, yet the burst-clone dispatcher has
a dropped arm [06 §6.4], so that pellet count is a `TODO(question)` at
`unitViewerNominalDPS`. No stock dropped weapon authors a burst.

The Build tab lists **Builds** (a builder's products) and **Built by** (every
builder whose list contains the unit), each entry with its build picture,
name and ID, in library order. The catalog worker derives both once per load
from the immutable `BuildMenus`: only `builder` definitions have a build list
[04 R-ORD-02 §1], builders are visited in sorted library order rather than map
order, names resolve through the catalog lookup a build button uses, and a
record hidden by a duplicate name is left out and says so [02 R-CAT-01 §§4–5].
The viewer is mode-independent. It shows the retail `Buttons` list and adds,
with a small Modern tag and a footnote, any entry that only `AuthoredButtons`
supplies (DESIGN_ECONOMY_CONSTRUCTION "Modern authored build membership"). The
lists are CANBUILD and download membership; an installed GUI page's buttons
can differ [07 R-HUD-03 §6].

Each Build-tab entry shows the unhindered construction work time at normal
speed. `construction.WorkTicks` repeats the shared step from a fresh frame
with the builder's integer worker quantum until the stored single-precision
fraction is zero [05 R-WORK-01 §1]; the step count is divided by 30. One step
per tick is established for unhindered mobile, factory and assist work, which
retry one tick after accepted work [05 R-P0-06 §1][05 "Factory production
lifecycle"]. The figure assumes every step is admitted. It excludes travel,
the factory's script-owned opening wait, the mobile build-stance wait,
placement and allocation retries, and resource stalls, and the tab says so.
A zero quantum reads "no work", a non-positive buildtime "n/a", and a stalled
fraction or more than a day of game time "over 24 h"; none becomes a guessed
number. Times truncate to the shown precision. The estimates are memoized by
quantum and buildtime for the open screen.

**Verification.** Focused tests cover identity-preserving search and selection,
definition immutability, stat and weapon conversion, omitted zero rows, the
nominal-DPS exclusions, the build tree's Modern tag, lookup and non-builder
rules, work-time labels from `construction.WorkTicks` (whose own tests lock a
hand-checked step count, a single-precision carry and the refusals), link
clicks measured in painted rows, history keys that leave search editing and
the orbit alone, the picture worker's join on release, input ownership and release, zoom
bounds, main-menu-only preview entry, isolated animation inputs, bounded
playback, callback return and reload ordering, pose-cache invalidation, stable
pivot placement, fractional projected positions/depth, retained record ownership,
independent supersample coordinates and unchanged ordinary preview calls. The
action contracts are locked by authored fixtures: the completed-unit callback
order and its preview arguments, the slot-form `StartBuilding` bearing and the
stance gate before any nano query, the factory's activation, pad, building-edge
and stop order, the Hit arguments, the `Killed` severity seed with the corpse
chain and the substitute depth, the movement-rate tiers along the speed ramp,
the flight takeoff and landing hooks, and the action row's availability.
Product fixtures lock the cycle's retail order and reported omissions, a step
count equal to `construction.WorkTicks` at every speed with the step's
refusals and clamps, the edge order around a completion and its hold (no
second `Activate`), the rebuilt interrupted product and the product's
`BUILD_PERCENT_LEFT`; spray fixtures lock five particles per spawn tick over
two ticks, their lifetime, colour shimmer and expiry, discarded zero-length
hops, thirty private picks per record, the 4/11..7/11 narrowing, and that
nothing sprays before the stance, after Stop, in Idle or without a target.
With retail assets, ARMLAB and CORAP lock the spray target to
`PiecePlacement` and the spray source to `ProjectedPieces`.
Real-device fixtures cover separated sloped planes, stable near-coincident
surfaces through a turn, shared-edge coverage, negative/large depth, byte
carries and texture holes. Visually inspect factory pads and roof/wall edges
through full orbits and animation poses. Capture the actual screen with
`--shot /tmp/viewer.png --shot-unit-viewer armcom --shot-size 1440x900`;
`armlab/build` or `armthund/weapons` selects a tab, and a capture waits up to
600 frames for visible pictures to decode. `/action=<name>` presses an action
before the capture and runs a fixed number of preview ticks without a clock
(`/ticks=N`, `/severity=N`, `/weapon=N` and `/speed=1|4|16` adjust it):
`armrad/action=off`, `armlab/action=build/ticks=300/speed=16`,
`armthund/action=land` or `armpw/action=wreck`. A capture never reads the
saved restrictions: `--restrict` entries give the editor's draft against an
empty saved set, so Apply shows its count; `/only` turns *Restricted only*
on, `/card` opens the editor as the card does and `/query=<text>` types a
search, as in `armpw/only` with `--restrict armpw=20 --restrict corak=0`. The
editor was checked on a capped unit, a removed unit, a norestrict commander
and *Restricted only*, on both routes, at 1440x900 and 1024x640. The
action round was checked on ARMRAD on and off, ARMSOLAR on, off and hit, ARMWIN
and ARMMEX idle, ARMCK and ARMCOM building, ARMLAB building and stopped,
ARMFIG and ARMTHUND flying, ARMTHUND landed, and ARMPW death at severities 25 and
100 and its wreck, at 1440x900 and 1024x640. The product round was checked on
ARMLAB early, mid-build, nearly complete and holding, CORAP building an
aircraft, ARMVP and CORVP, the ARMHP hovercraft platform, the ARMSY shipyard,
and ARMCK and ARMCOM spraying, at 1440x900, with CORAP at 1024x640; ARMLAB,
CORAP, ARMVP, CORVP, ARMSY, CORSY, ARMAP, CORLAB and ARMHP each went on to a
second product. Check a commander, a factory's
Builds, a unit's Built-by times, an aircraft with a bomb, a multi-weapon
unit, a building and a picture-less record (stock ARMSCORP) at 1440x900 and
1024x640.
`--shot-unit-viewer @tools` retains the unused tools-menu prototype for capture.
Captures write no preferences, and only a Death or Wreck capture creates a
battle: its field. Review narrow and wide
windows, buildings, mobile units and aircraft, plus search and drag states
through the native window or the Ebitengine VM host. Run the ordinary fast
and short retail landing gates,
plus matching classic and modern live-battle performance checks for changes to
the shared geometry collector.

**Field verification.** Tests lock the field's death inputs against the
battle's own severity arithmetic for maximum health from 1 to 40,000 (the
Peewee's 250 hit points give 0 at samples 50 and 100, then −125 and −250),
the refusal of an unreachable selection, Wreck's adoption of the field's
corpse over the presentation script's answer and a new generation for every
choice. With retail assets they lock the lifecycle — a severity-25 Peewee
leaving `armpw_dead` at depth 1 with inputs that give 25, a fresh battle for
the next severity, Death's restart after its hold, Idle closing the battle
and leaving the restart to close on arrival, Wreck handing `armpw_heap` at
depth 2 to the turntable, and the viewer's close joining a staging — and a
ship staged on water. A Death or Wreck capture stages its field synchronously
and runs `/ticks=N` field ticks from the field's first visible tick, so the
death falls on tick 30; it never restarts. By default a death is caught 20
ticks after it happens and a wreck just after the handover, as in
`armpw/action=death/ticks=75/severity=4` or `corlab/action=wreck/severity=1`.
The field round was checked on ARMPW at severities 25, 50 and 100 through the
settle, the blast, the debris bursts, the cleared ground and the restart, its
wreck at 25 and 100, CORLAB, ARMSOLAR, ARMCOM, ARMTHUND on the ground, ARMAH,
and ARMROY and ARMSUB on water, at 1440x900, with the Death loop and the
CORLAB wreck's handover also driven frame by frame through the live staging
path at 1440x900 and 1024x640.

## 8. Developer unit spawning

**Implemented retail contract (Established):** after developer activation,
an unmatched chat command `+<pattern> [player]` queues the default spawn
handler `[07 R-CAM-01 §6]` in every gameplay mode. `+arm*` therefore creates
one fully built unit for every loaded definition whose internal name begins
with ARM. `?` consumes one byte and `*` consumes any run, including empty;
ASCII literals compare case-insensitively and the whole name must match. All
retained definitions are visited in catalog order, including duplicate names;
the reserved sentinel is absent from `Catalog.UnitRecords`.

Developer access is required at submission (`+dev` or
`+Now Film Chris Include Reload Assert` in any rule set; see §2.1). Without
access the Modern exact-name shorthand retains its existing contract, and
Strict accepts no spawn shorthand. Registered commands retain priority.
The second word uses the existing signed-decimal-prefix reader and narrows
to an owner byte; absent means slot 0. Later words are ignored. Neither the
viewing owner nor the controlling owner chooses ownership. The allocator
silently refuses invalid owners and exhausted slices or definition limits;
the command does not test whether the player exists.

The host captures the submission-time battlefield point and enqueues a typed
`HumanDeveloperSpawn` request. All expansion and creation run at phase 1;
paused input holds the request and everything queued behind it. Access is
checked before submission, so a later host access change does not change the
accepted request. Single-player replay kind 46 stores that request (pattern,
owner and point); replay requires no unrecorded host developer state.
Online codecs and receivers refuse this replay-only kind even with cheats
enabled, and the local adapter admits no online input. A future online
developer-spawn contract must define its stream class, owner authorization and
work bounds through DESIGN_MULTIPLAYER before enabling it.

The first match starts at the captured point. Before each later match, X
advances by that definition's half footprint width (`footprintX × 8`). Mission
position fixup snaps buildings and computes their height; mobiles keep the
point. The fixed-up point becomes the running point. Each attempted creation
then advances X by 32 plus that definition's half width. X at or beyond the
play-area right edge wraps to 160 and advances Z by 160. Failed allocations
still advance the point and count as matches. These constants and operations
are retail, not new Modern policy `[07 R-CAM-01 §6]` `[08 R-ENTRY-01 §6]`.

There is no site validator, occupancy test or resource charge. Successful
creations use the ordinary fully built allocator, COB initialization, RNG
draws and movement registration; the normal visibility/publication passes
expose them. The host requires the pointer over the battlefield, outside HUD
and minimap, as for its existing spawn producer; this is a Nanolathe input
boundary. There is no per-match off-map guard or spawn feedback.

**Remaining implementation gap:** the no-match `debugdat` command-script
fallback is traced `[01 R-PLAT-01 §9]` but remains deferred with `Include`'s
typed command routing. Unknown patterns stay silent. This does not affect
matching unit-spawn requests. Case folding for bytes outside ASCII shares
the catalog’s existing Unknown code-page contract `[02 R-CAT-01 §3]`; those
bytes stay literal until the active retail comparison table is traced.

**Verification:** focused authored fixtures lock whole-name wildcard matching,
case, owner-byte narrowing, submission pointer/access capture, both gameplay
modes, paused queue order, duplicate definition order, a failed first match,
building snapping, the inclusive right-edge wrap, occupied-site stacking,
success-only allocator RNG, unchanged resources, replay dispatch and online
refusal. The ordinary fast and short retail gates cover integration; the quick
displayless simulation benchmark checks the unaffected ordinary tick path.
