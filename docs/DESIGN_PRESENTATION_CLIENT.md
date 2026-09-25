# Design — Presentation and the client

`internal/client`, `internal/render`, `internal/palette`, `internal/audio`,
`internal/audiobackend`, `internal/platform/ebitenapp`, and the consumption
side of `internal/frame`. One window, one software framebuffer of palette
indices, one ordered pass over the committed frame, one expansion to RGBA at
the very end, and one audio service consuming committed presentation events.
Acknowledgement pop cadence follows the explicit host policy in C18.

This is one of the design documents listed by [ARCHITECTURE.md](ARCHITECTURE.md);
that document owns package boundaries, the tick, and the citation routing that
makes `[C-1]` and `[C-3]` in `internal/client` resolve to §3.1 below. Rules
every diff is reviewed against are in [INVARIANTS.md](INVARIANTS.md); places
where the reference install disproves the written contract are in
[SPEC_CONFLICTS.md](SPEC_CONFLICTS.md).

The CPU/Original contracts below remain the retail reference. GPU Classic may
use visually reviewed raster approximations; Enhanced is planned to add strategic
zoom and optional interpolation under [DESIGN_GPU_RENDERER.md](DESIGN_GPU_RENDERER.md)
§5. Modern is the default executor with a 60 FPS cap. The Nanolathe options
page persists executor and cap choices (DESIGN_INTERFACE_HUD_INPUT §3.4.1);
classic retains 30 Hz presentation and the authoritative tick remains 30 Hz.

## 1. Purpose and boundary

Presentation answers one question: **what did the world look like at the tick
the simulation last committed?** Not "what does it look like now", and not
"what will it look like part-way to the next tick". Retail draws the committed
state and nothing else — there is no previous frame, no timing fraction, and no
interpolation seam anywhere in the draw path `[03 §2.4]` [I6]. The window's
presentation cadence is 30 Hz, matching the simulation timebase; a paused
simulation retains the same pixels indefinitely.

That gives the boundary its shape, and the shape is a one-way valve.

* **The committed frame is the only input.** `internal/frame.Buffer` publishes
  one immutable `Frame` at the end of each sub-tick; the client samples
  `Buffer.Current()` once per window update and reads unit, feature,
  projectile, effect, strip, visibility, fog, selection and event views out of
  it `[03 §2.4]` `[03 §1]`. Nothing in these packages holds a pointer into a
  live pool, a `*Session`, or a mutable world.
* **Nothing here writes authoritative state, and nothing here is read back.**
  No package below imports a simulation owner, no draw, resolve or audio path
  mutates one, and no authoritative phase calls into the client. The valve has
  no exception in either direction.

  The one place it used to: a burning feature's frame geometry and a
  die/reclaim/burn lifetime in visits `[05 R-FEAT-01 §10]`, and a smoke puff's
  last frame against its entry's frame count `[03 R-STRIP-01 §2]`, are
  properties of a GAF entry, and the client was the only holder of a GAF cache.
  The feature phase read them through `Client.FeatureSequence`, so a headless
  battle — which installs no client — timed feature transitions and retired
  smoke differently from a windowed one. That metadata now belongs to
  `internal/content` (`content.CompileSimArt`), compiled from the VFS before the
  session's features and strips exist and immutable thereafter; the client keeps
  only its pixel cache, walking the identical `max(delay, 1)` cadence so the
  frame the simulation timed a record from is the frame painted for it.

  Battle installation prewarms event pixels through
  `Client.WarmBattleFeatureSequences`. Its roots are the composed terrain's
  entire feature-definition table, including mission and restored admissions,
  plus every catalog unit's corpse. A growing walk follows dead, reclaimed and
  burnt successors, including definitions with no art and cycles. This covers
  later corpse placement, transitions and reproduction (which retains its
  parent's definition); it never depends on the camera, current unit census or
  build restrictions. This is a Nanolathe presentation loading policy: the
  full authoritative `content.SimArt` table and feature admission order remain
  unchanged. Event body/shadow hits and misses are ready before drawing. Banks
  named by reachable rest/shadow art are also loaded before drawing: an
  unrelated event may previously have loaded one incidentally, and narrowing
  must not move that decode into Draw. A requested bank still materializes all
  its entries. Any new runtime producer that can introduce an
  unrelated feature definition must extend the setup roots before using this
  warm policy.
* **Nothing here touches the simulation RNG.** Presentation randomness — the
  segmented-projectile jitter of render type 7, the audio variant pick and the
  music chooser — draws from private CRT copies taken when their owners bind.
  Nanolathe particles and other effect strips are already advanced in session
  phase 11 and reach this package as committed values. Retail interleaves all
  of those draws on its live main-thread CRT; the approved isolation policy is
  defined in DESIGN_RUNTIME_DETERMINISM §5 `[03 §5.4]` `[03 §8.3]` [I4].
* **The window is one adapter, and it is thin.** `internal/platform/ebitenapp`
  owns the `*ebiten.Image`, the window size, device input polling and the PCM
  device installation. It calls the client's injected `Step` and then uploads
  the bytes the client hands it. The client owns the logical size; the adapter
  follows it.
* **Palette indices stay indices until the last moment.** Every pass writes
  bytes into one 8-bit indexed surface. The expansion to RGBA happens once, at
  the end of the frame, through `PALETTE.PAL` alone `[03 §4.3]`.

The sim side of the projectile boundary — what a `ProjectileView` carries and
who fills it — is [DESIGN_WEAPONS_PROJECTILES](DESIGN_WEAPONS_PROJECTILES.md)
§2.13. This document is the draw side of the same boundary: which art a render
type names, how a beam is stroked, and what happens when the art does not
resolve.

Two neighbours own things that look like they belong here and do not. The
**camera shake driver** is authoritative phase 10: it spends exactly two CRT
draws per active tick and publishes an offset on the committed frame, which the
battle camera adds — [DESIGN_RUNTIME_DETERMINISM](DESIGN_RUNTIME_DETERMINISM.md)
DET-04, `[03 §5.6]` `[01 R-CORE-01 §4.4.1]`. The **phase-7 model-texture
sequence advance** is likewise a session phase, but its `ModelTextureRegistry`
is battle-owned. Battle composition binds every qualifying primitive of each
loaded unit and projectile model before ticks begin, then installs that
registry as the session's phase-7 service. A cursor belongs to a loaded model
primitive, not a unit instance, frame, window or renderer: all instances of one
load share it, while distinct loads and primitives do not. Unit definitions use
one load per definition ordinal even when their filenames match; projectile
weapon records reuse the first earlier matching model-name load. The registry
precompiles candidate feature geometry at battle setup without binding cursors.
`features.Service.SetDefinitionAdmissionObserver` first visits the terrain's
existing feature-definition table in order, then reports each successful later
append; that callback binds a precompiled feature load once. A client receives
the immutable registry through `SetModelTextureRegistry`; drawing only reads its
current frames and can never admit a player. Bootstrap binds pre-restore terrain
definitions before tick one, follows every feature's dead/reclaim/burnt links
with a growing ordinal walk, and admits each unit corpse before that unit's
model. `features.Service` latches its definition-table length at the first
restore reset; bootstrap binds the saved suffix after the normal link walk and
then performs its own growing link walk. Repeated entries preserve their first
loaded model identity. All named geometry is strictly expanded before startup;
an unexpected provider or decode error rejects composition, while standalone
preview retains its explicit absent-model wrapper. The `--headless` composition
adapter installs the same registry before its session begins, without creating a
client. Replacing or removing a client therefore cannot pause phase 7, and
battle teardown clears the observer and resets the whole registry once `[03
R-CRD-005 §1]`.

**Standalone draws and the binder.** A cursor is keyed on the loaded model, so
the binder can only answer for a draw that carries one. Projectile and debris
draws carry the one-piece scratch model the standalone builder fills per call,
which no binder ever sees, so the walk asks about the loaded model the
scratch piece was copied from instead (`SourceModel`, below), keyed on the
piece's immutable loaded-model index so a parent and its child do not alias. That fallback used to be
unreachable whenever a registry was installed — which is every battle client —
and the primitive was dropped instead, so an animated-texture piece thrown off
one of the 39 stock unit models that reference a multi-frame entry (commanders,
metal makers and stores, air and vehicle plants, shipyards, radars, targeting
facilities, beacons) was simply missing. No stock weapon model references one,
so projectiles are unaffected in practice.

Which cursor a *detached* piece shares is now **settled** and is not the
per-subject one: retail hands the standalone entry the loaded model piece
itself and resolves the frame through the cursor living in that loaded
primitive record — the identical read the unit renderer performs, at the same
offset of the same record. A detached piece therefore shows the same frame as
the living unit at every instant, and the phase-7 walk advances that cursor
from model load onwards whether or not anything is drawing the model
`[03 R-COMP-02 §6]` "Which cursor a detached piece reads". The per-subject
standalone cursor is a *substitute*, not the contract: in a battle it is never
advanced, so it holds the loaded cursor's initial frame instead of its current
one.

**Resolved.** `UnitDraw.SourceModel` carries the loaded model across the
per-call scratch model: `buildProjectileStandalonePiece` records the model it
copied the piece out of, and the compose walk asks the binder about that model
rather than about the scratch one, resolving
`animatedFrame(SourceModel, piece.SourceIndex, primitive, ref)`. An ordinary
unit or feature draw leaves the field nil, because its `Model` already is the
loaded model. A battle standalone draw therefore follows the loaded primitive's
cursor and shows the frame its living parent shows. The per-subject cursor
survives only for standalone preview, where no registry exists and so no loaded
cursor is available to share; a battle draw whose model the binder does not hold
at all reads the entry's first frame, since no cursor for it exists anywhere and
a per-subject one could never advance.

**Wind** is read by the simulation's strip
producers, not by any draw here; `[03 R-WIND-01]` belongs to
[DESIGN_WORLD_VISIBILITY](DESIGN_WORLD_VISIBILITY.md).

## 2. Packages, files and key types

### 2.1 `internal/platform/ebitenapp` — the window

`app` adapts `client.Client` to Ebitengine. `Run` synchronizes Ebitengine's
Update with display refresh for portable input polling. A separate 30 Hz host
clock schedules the existing client work; the input buffer retains press edges,
text and scroll between these steps (DESIGN_GPU_RENDERER §13.5). Each host step
syncs the selected window size, publishes its buffered input, records focus, and calls
`Client.Step(1/30)` — the injected session step, which owns the clock, the
sub-ticks and the frame publication. Wall-clock time never crosses into the
simulation; the session converts the fixed delta with its own accumulator
[I6].

Original `Draw` consumes at most one pending presentation per host step. VSync callbacks
between 30 Hz host steps leave Ebitengine's retained screen untouched, avoiding
client composition, pixel conversion, upload, and device drawing. A consumed
presentation asks the client for its expanded bytes and does one `WritePixels`
into a device image recreated only when the logical size changes. `Layout`
pins the logical resolution, so Ebitengine letterboxes a resized window without
moving authored HUD coordinates.

Modern presentation follows DESIGN_GPU_RENDERER §13.5 and §13.10: Draw can
present between host steps and the deferred host step runs after submission.
Immediately before replay, it places only the recorded cursor at Ebitengine's
latest logical pointer position, preserving its authored hotspot [07 §8].
This removes the deferred step's extra frame of positional latency while
leaving cursor shape, hover, placement, orders and camera on the 30 Hz host
cadence. Ebitengine's cursor snapshot refreshes every display frame. The
capture-release restore point takes precedence over this newer sample, and
captured cursors remain hidden [07 R-CAM-01 §11]. Original and `--shot` retain
their existing composition. The submitted image captured by F11 includes the
late-positioned cursor.

`RunOptions.Stats` (`--stats`) opts into periodic and exit renderer pipeline,
cadence, input polling cost and paused-cache stderr readouts. Polling cost is
elapsed wall time, not process CPU time. Default sessions are quiet; the F11
bundle keeps these counters regardless of terminal logging.

**Nanolathe host presentation policy (user-authorized).** Windowed presentation
keeps the selected display dimensions throughout menus, loading, battle and
results. The logical front-end canvas remains the established 640×480
[07 R-FE-02 §2]; battle uses the selected dimensions [07 R-FE-01 §11].
Ebitengine scales the canvas proportionally into the host window or desktop,
letterboxing where needed, and reports pointer coordinates in that logical
canvas. `Layout` also publishes the actual outside dimensions to the client,
so camera edge scrolling includes letterbox bars and bounded overshoot on every
side (DESIGN_INTERFACE_HUD_INPUT §3.1). Only the camera uses that adapted
position; the raw pointer remains the source for picking and widgets.
When fullscreen scales the canvas down, Ebitengine's integer logical pointer
sample can stop short of the trailing logical pixel at the last physical
display pixel. The edge adapter treats the final physical pixel's logical
interval as the edge for camera scrolling.
Neither executor renders the world at desktop resolution merely
because the window is fullscreen.

`RunOptions.WindowSize` supplies the committed host size separately from
`Client.Size()`. The shell supplies its display preferences; direct battles
use the saved display dimensions for both the host and battle canvas. The
adapter checks the committed size before input and after the client step.
Resolution slider edits, UNDO and RESTORE only change the pending selection;
the options root's OK commits it and resizes the window once, after closing the
widgets. Cancel restores the entry selection without resizing. This prevents
resizing during a drag from moving the pointer relative to its captured widget.

The resolution slider retains the original modes and their desktop gates,
and adds 1280×720, 1600×900 and 1920×1080 as Nanolathe presentation choices.
The three 16:9 render sizes are always available, independent of monitor aspect
or device-independent desktop size. Fullscreen scales them to the display;
selecting a different aspect still letterboxes rather than stretching. The
combined list remains sorted by width then height, with no duplicates.

The user-requested monitor aspect options are also host policy. Each opening of
Options queries the monitor containing the window twice: `DesktopSize` reports
its device-independent size and `DesktopPixelSize` its physical pixel size
(the device-independent size times the device scale factor). The physical size
is offered directly, together with widths 1280, 1600 and 1920 whose heights
follow the physical ratio rounded to the nearest pixel, so a scaled desktop (a
3440×1440 monitor at 125% reports 2752×1152) still offers its native
resolution. The device-independent size stays offered beside it. Ebitengine
reports no pixel bounds, and on Windows and Linux its device-independent size
is truncated, so the physical size is reconstructed as the even integer in
`[dip·scale, (dip+1)·scale)`. The original desktop gates keep reading the
device-independent size. A monitor-derived size is available even when the
original list would gate that same size out on a smaller desktop. These are
render sizes, not enumerated hardware display modes. Missing monitor metrics
add no modes.
The current stored size stays in the table when moving between monitors, so
opening the page cannot silently replace it. All additions share the minimum
640×480 filter and sorting/deduplication. The list stays fixed during an options
session to preserve slider capture; reopening Options queries the monitor again.

The Visuals page shows the selected size's reduced aspect beside its read-out
and the detected monitor's aspect below that. Ratios use exact integer reduction
(with 8:5 displayed as the customary 16:10). These labels extend the authored
page using its label font and height; the resolution read-out retains its format.

Desktop fullscreen uses Ebitengine's fullscreen API without changing the
monitor mode. The top-level settings field `fullscreen` defaults to false
when absent. `--fullscreen[=true|false]` overrides startup; omission restores
the saved value. Alt+Enter toggles from any screen and is consumed before
game input publication, including the rest of that Enter hold, so it cannot
activate a menu default or submit chat. Observed fullscreen changes, including
native window controls, update only the saved fullscreen preference; they do
not save pending options edits. While fullscreen the
adapter continues tracking the selected window size for restoration on exit.
The native green fullscreen button is enabled on macOS with fullscreen-only
resizing. Windows/Linux enable window resizing to expose native maximize;
maximizing fills the desktop work area, while Alt+Enter enters fullscreen.
Manual resizing scales the selected logical canvas with aspect preserved and
does not change the saved resolution. The adapter only reapplies host size
when the selected resolution changes.

**Fullscreen pointer confinement (host presentation policy).** Retail's
exclusive display mode makes the whole display the canvas, so its pointer can
never leave it [03 §4.2]. Desktop fullscreen aspect-fits the logical canvas
inside the monitor, leaving bars on a display of another aspect that the
hidden pointer could wander into, carrying the software cursor out of sight.
On Windows the adapter confines the pointer to exactly the device pixels that
report a logical canvas coordinate (`presentedCursorRect`) while the window is
fullscreen, focused and not in drag-scroll capture, reconciling the clip every
host step and releasing it on focus loss, in windowed mode and at shutdown.
Windowed play is never confined. Other hosts are unconfined pending a manual
check on macOS and X11 (`cursor_clip_other.go`). Keypad Enter is folded into
the one Enter identity by the adapter, so it opens chat, commits a text edit
and toggles fullscreen with Alt exactly as the main Enter does.
On macOS, focused fullscreen hides the menu bar and Dock completely so moving
the pointer to the top edge scrolls the camera without revealing the native
window controls. The adapter reconciles AppKit's application presentation
options during fullscreen because transitions and Space activation can reset
them. It clears the incompatible auto-hide menu/Dock/toolbar flags, preserves
unrelated options, and restores its original windowed flags on exit and shutdown.
This is local to the application; system preferences are unchanged. Application
switching, Space gestures and Force Quit remain enabled. Alt+Enter also exits
fullscreen entered with the green window button in the current Ebitengine
backend, providing a keyboard exit when the rollover controls are hidden.
The native bridge is excluded from VM guests, whose host owns window policy.

AppKit contract: [NSApplicationPresentationOptions](https://developer.apple.com/documentation/appkit/nsapplication/presentationoptions-swift.struct?language=objc).
Manual macOS verification confirmed suppression at the top edge and continued
three-finger switching to other windows. Validate startup fullscreen, repeated
Alt+Enter entry/exit, green-button entry, and return from another Space; windowed
controls must return after exit.

Settings persistence also follows a host recovery policy: startup may use
in-memory defaults after a corrupt or unsupported settings file is reported,
but every save refuses to replace that unreadable file. The original bytes
remain available for recovery. A live toggle still changes the active session;
its persistence error is reported instead of erasing unrelated preferences.
Missing files and readable current-version files retain ordinary atomic saves.

These are host presentation choices, not additional retail behavioral claims.
Validate selected-size stability across logical canvas transitions, shortcut
consumption and settings preservation, plus live menu/battle input and both
executors in windowed/fullscreen modes.

`Run` installs the PCM device once for the life of the process, before the
window is shown, and calls `Backend.WarmUp` — see §5. It hides the window
system's pointer, because retail draws its own `[07 §8]`, and refuses to start
without one installed.

### 2.2 `internal/client` — the composer

`Client` holds the indexed surface, the RGBA scratch, the palette tables, the
camera, the terrain, the model and texture caches, the lazily loaded art banks,
the fog handles, the display option bits, the software cursor, the message
ring, and the bound audio service. `Options` injects `Step`, the frame
`Buffer`, the logical size and the title.

`BeginPresentationFrame` owns resource-display advancement. Stocks ease every
presented frame; the four rates copy settled totals only when the viewed
player's saved display deadline is strictly below the committed tick, advancing
that prior deadline by 30 once. `UIFrame.Resources` and the pre-record digest
carry the same six displayed values. Prediction copies the deadline state and
cannot advance it. New battle buffers reset stocks and deadline bindings but
retain the four rate latches `[05 R-ECO-01 §1, §6]` `[07 R-HUD-03 §4]`.
The client exports detached display deadlines for the host's save projection;
neither presentation nor that overlay mutates live economy state [I6].

Battle adoption binds the primary COMIX FNT with `SetMessageFNT`, preserving
the side font installed by `SetFNT` for group digits `[03 R-FX-01 §6A]`.
`SetFNT(nil)` clears both bindings on teardown. The message column's glyph
commands use that font's height for line spacing and resolve each logical
foreground through `paletteIndex` before recording. Both executors therefore
receive physical palette indices; the indexed surface needs no further GUI
colour lookup `[07 R-HUD-03 §14.4]` `[03 §4.3]`.

Battle adoption also binds the HUD's loaded LOGOS bank with
`SetMessageLogos`. Real-speaker lines select `32xlogos` by the current committed
player row's `Logo` byte and record the inclusive scaled square before their
glyphs. Teardown clears that binding. The accepted-announcement branch of
`enqueueStatusEvents` plays `MessageArrived` once for a real speaker; rejected
lines and sentinel lines stay silent. Class filtering belongs only to the
drawer, and draw-list recording and replay produce no audio effects
`[07 R-HUD-03 §14.3–§14.4]` `[I6]`.

`Frame` is one window frame: drain and tick audio, compose the indexed surface,
draw the cursor over it, expand to RGBA. `ComposeFrame` and
`ComposeFrameSnapshot` run the same composition without entering the window
loop, which is what `--shot` captures and what the parity fixtures digest.

The composition itself is one function, `drawCommittedFrame` in
`world_draw.go`, and it is the only production frame ordering in the tree. Its
files:

| File | What it owns |
|---|---|
| `client.go` | the type, its caches, options, size, present, exit |
| `frame.go` | `Frame`, the compose entry, fog draw, visibility predicates for units, features and projectiles |
| `world_draw.go` | the ten barriers, the plot-cell window, the screen-Y buckets, the two feature passes and the two unit passes |
| `terrain.go` | the tile blitter: source block plus intra-tile remainder, clipped at map bounds |
| `model*.go` | the model rasterizer — see §2.3 |
| `strip_draw.go`, `effect_draw.go` | the per-barrier strip walk and the fixed-effect pool draw |
| `nano_draw.go`, `flash_disc.go` | nanolathe particles; the three generated explosion tables |
| `projectile_draw.go` | render-type dispatch to pixels: sprites, beams, segments, shadows |
| `presentation_resolver.go` | art identity → GAF frame, for projectiles and effects |
| `healthbar.go` | the unit-label walk between strips 8 and 9 |
| `selection_quad.go`, `selection_overlay.go`, `hover_hull.go`, `select.go`, `snapshot_select.go` | the footprint quad, the drag rectangle, the pick hull, rectangle membership, the committed-frame visibility gate |
| `minimap_draw.go` | the radar surfaces onto the shell |
| `cursor.go`, `text.go`, `message_lines.go`, `ui_stage.go` | software cursor, FNT text, the caption column, the single UI adapter slot |
| `viewport.go` | the battle viewport rectangle and the transform between logical, beam and world coordinates |
| `feature_sequence.go` | the authored-animation accessor the feature phase reads |
| `audio.go` | binding and draining the audio service at the frame edge |
| `p28_parity_trace.go` | an opt-in, value-only renderer trace; nil on the normal path |

### 2.3 The model rasterizer

Sprite feature placement uses the terrain-height average around the feature's
anchor cell, independently of its center-sampled instance height; body and
shadow share that anchor. 3DO feature models retain their instance position
`[03 §5.1.4]` `[03 R-RAST-01 §6]`.

A unit is not blitted from a sprite. It is composed into its own indexed image
with a per-pixel **height key**, and that image is blitted. The split across
`model_*.go` follows the stages:

* `model.go` — the compiled 3DO in presentation form, the piece walk, the
  shaded/unshaded renderer selection.
* `model_compose.go` — per-primitive dispatch and the winding cull. Bit 0 of
  the authored `IsColored` field selects the flat filler, which draws any
  arity; with the bit clear a textured face must be a quad `[03 R-REN-03A §5]`
  `[fmt 3do]`.
* `model_raster.go` — the two-chain edge walk with its interpolated attribute
  lanes (U, V, key, SHD row), the key-plane admission test, and the 2×
  supersample resolve for structure anti-aliasing `[03 R-RAST-01 §1]`
  `[03 R-REN-03A §6]`.
* `model_spans.go` — the four span writers of `[03 R-REN-03A §5]`: flat and
  textured, each in an unshaded form that writes the byte raw and a shaded form
  that writes it through `SHD[row·256 + byte]`. Which pair runs is the
  *renderer* selection, not the flat/textured one.
* `model_textures.go` — texture-name resolution and the battle-owned loaded-model
  primitive registry. One frame is static; exactly ten frames is a `LOGOS` team texture
  indexed by a known `UnitView.OwnerColor` byte, never by its owner slot and
  never animated; unknown or out-of-range selectors yield no frame. Anything
  else is an animated sequence with per-frame holds `[03 §2.4.1]` `[03 §4.4]`
  `[03 R-CRD-005 §1]`. Decoded-frame adapters read `TexturePlayer.FrameIndex`
  directly; resolving a frame neither advances playback nor rebuilds asset IDs.
  Feature models use the committed player-zero row's colour in both classic
  composition and modern geometry recording, independently of the feature's
  placer and the viewer `[03 R-RAST-01 §3]`. An absent committed row remains
  an absent selector; an out-of-range colour contributes no team face. This
  safe missing-input boundary does not invent retail behaviour for a missing
  player record. The stock affected model is `corkrog_dead`; its three team
  quads are covered by the corpus census and focused capture test.
* `model_outline.go` — the nanoframe wireframe: not a polyline, but the edge
  walk's per-scanline extremes written into the composition image, admitted
  against the same key plane `[03 R-P0-19-N]` `[03 R-COMP-01 §3]`.
* `model_staging.go` — the carrier staging image. When the carrier's
  composition image has a key plane, its cargo is composed into a union box and
  resolved per pixel against one height plane, which is why a transport hull can
  stand in front of the unit it carries `[03 R-REN-03A §4]`.
  Classic staging computes child displacement at native raster scale before
  the final view-scaled group blit (`DESIGN_GPU_RENDERER` §14.2), so zoom applies
  once. A temporary image view changes only that staging anchor; cached child
  images and their separate shadow/trace anchors remain intact.
  The committed unit view copies each carrier's head-first cargo list from the
  linkage owner. Both executors receive children in that order, including after
  detach/reattach; pool-slot order never substitutes for attachment order.
  Piece-less children and children without models retain their existing draw
  gates `[04 R-UNIT-06 §3]` `[03 R-RAST-01 §7]` `[03 R-REN-03A §4]` [I6].
* `model_shadow_pass.go` — a second rasterization sheared 45°, every face
  filled with palette index 0, composited through the tinted blitter, which is
  an `ALP` blend `[03 R-REN-03D]`.

### 2.4 `internal/render` — the presentation pools and helpers

Presentation-only, and it writes no pixels: `internal/client` does that. It
owns `FixedEffectPool` (strip storage and the strip sweep itself are
`internal/session`'s), the render-type constants and the `ProjectileDraw` dispatch, `TexturePlayer`
and the GAF playback cursor, the cursor index table, `BuildFogOpsWindowWithArtInto`, the
`RadarSurface`/`MinimapService` pair, the nanolathe emitter, the nanoframe
reveal verdicts, the shade-row helpers, and the generated flash-table geometry.

### 2.5 `internal/palette`

`Tables` holds `PALETTE.PAL`, `GUIPAL.PAL`, `ALP` (256×256 blend), `LHT`
(32×256, brighten-only), `SHD` (32×256, the full signed ramp), the 256-byte
logical→physical map, and the gray table `[03 §4.3]` `[03 §4.3.3 R-RR16-A §1]`
`[fmt pal]`. The logical map is not a route for image pixels; see C7.

Gamma is a final colour-output transform. `Client.SetGammaFactor` retains the
display factor and rebuilds `Client.DisplayPalette` from the immutable
`palette.Tables.Base`; the classic indexed-to-RGBA conversion and the modern
PAL atlas row consume those same output colours. The logical and physical
index-remap tables do not change, and no gamma value reaches simulation state
`[07 R-FE-01 §11]` [I6].

Results retain the battle palette and gamma throughout darkening, then save
the current factor and force neutral gamma for outcome art. The glamour
entry installs its black palette before the first image composition; later
steps change only its display entries. Cleanup restores the saved factor.
The software cursor is hidden while darkening, displaying glamour and
revealing statistics, then returns as the normal arrow once every bar is
active and has reached its target.
The CD-check dialog explicitly restores visibility earlier
`[08 R-CAMP-01 §6]`.

### 2.6 `internal/audio` and `internal/audiobackend`

`Service` is the single audio owner: `Queue` (eight slots), `Registry` (the
alias table), `SampleCache`, the music controller, the positional viewport, and
one private CRT copy. `internal/audiobackend.Backend` is the PCM device behind
it — keeping the Ebitengine audio import there is what lets authoritative
packages import `internal/audio` without initialising a graphics platform [I6].
The music controller's `SetDesired(int32)` owns category transitions, the
raw volume/fade state, and a private ten-slot CD timer table. The host binds
`SetPresentationClock(func() uint32)` before commands and calls
`ServiceTimers()` only on the busy presentation pump. The callback returns
actual scaled host time (30 units per second), and registration samples it
again because registration services the live table before allocating.
`SetVolume(int)` retains its authored-slider API and performs the signed
shift and raw clamp internally. `SetPlaybackPoll` supplies a device query;
without a media device the existing controller models playback internally.
Only actual successful completion signals call `NotifySuccessfulCompletion`.
`DrainEvents` does not advance ordinary CD transitions. Explicit `Tick` calls
remain allowed during fades and delays [03 R-AUD-01 §4][01 R-PLAT-02 §4].
File-backed CD audio is supplied through `MusicOutput.NewMusicPlayer` on the
process output. The controller owns one player; stop, disable, replacement and
teardown close it and its VFS source. Music gain is the clamped raw CD level
divided by 65535, independent of wave effects gain, MODE Off and narration.
The host pump services timers and only dispatches successful completion after
both decoded EOF and device drain; pause and decode/device errors do not
advance tracks. Errors retain logical path/provider provenance and are drained
by the presentation host once.

The copied GOG soundtrack is recognized by `2.mp3..17.mp3`: logical tracks
1..16 use those files in physical order, with seven Battle then nine Building
categories. Duplicate extras `0.mp3` and `1.mp3` are excluded
`[03 R-AUD-01 §4 "Packaged MP3 media"]`. Nanolathe uses the fresh retail-disc
category layout; it does not reproduce the GOG adapter's undefined status
replies or import a Windows registry CDLISTS entry. For other file sets, numeric
basename order followed by lexical order is a Nanolathe directory policy.
Search priority remains `music`, `sounds/music`, then `cdaudio`; supported media
are MP3 and PCM WAV. No optional folder means silence.

Ebitengine's portable MP3 decoder (`go-mp3`, Apache-2.0) streams stereo float32
PCM on Windows, Linux and macOS. Decoding and resampling retain bounded PCM
buffers instead of caching whole decoded songs. WAV uses the existing sample
conversion with a 16 MiB encoded-file host limit. The per-track file and decoder
live only for playback; no soundtrack data is shipped in the repository.
Battle entry applies retained music preferences and arms Building playback
until the desktop output is installed. Front-end creation configures media
without starting it. Headless session construction never opens the device.
Damage intake and death accounting publish semantic intensity events with
captured local ownership and weights. The audio consumer retains all committed
cues in raise order, then evaluates the chooser using the local player's
published live-unit count and the scaled presentation clock. The local player
is `Selection.LocalPlayer`; viewport/viewing-player changes do not retarget it.
The committed `Result.Ended` flag suppresses chooser evaluation; the first
results presentation and battle teardown request category 4. The shell retains
the service and pumps its Custom-mode fade after releasing the battle, while
ordinary voices and narration stop. Existing focus gating still applies.
Non-Custom modes retain their established category-request behavior. Final
process shutdown closes immediately because no continuing shell can pump it.
Ring-only battle reset preserves the researched chooser history, including its
cross-battle retained-want quirk [03 R-AUD-01 §5].

Native CD-drive access and registry history remain T23 platform residuals.
The private CD table does not model retail slot competition and callback
ordering with delayed stream opening, which shares the retail timer table
[01 R-PLAT-02 §4][03 R-AUD-02 §1]. This remains a T23 platform residual.
The write-only outgoing-category history is omitted; its unbounded retail
write for unsupported categories is documented in the owning research.

The backend keeps base attenuation separate from its application-local FX
output gain, so slider changes affect already-playing cues and narration.
Zero FX mutes an existing buffer without restarting its timeline. MODE Off
stops and clears ordinary voice slots; narration follows its separate stream
start/stop lifetime. The stream opener has no ordinary MODE play gate
`[03 R-AUD-01 §1]` `[03 R-AUD-01 §2]` `[03 R-AUD-02 §1]`. Ebitengine player
gain stands in for the retail system wave-output mixer; no host-wide volume
setting is changed.

Opening a briefing initializes the narration toggle to stage 1 and resets
that visit's presentation clock before arming its delayed stream. The
MSNBRIEF adapter consumes the shared widget service's fired callback once;
the down edge only captures the button. Its painter uses the same independent
down and stage values as other frontend buttons, so the visible selection
agrees with the stop/restart request `[07 R-FE-01 §4]` `[07 R-WGT-01 §3]`
`[03 R-AUD-02 §1]`.

The shell's common presentation step pumps the backend in menus, loading and
paused battles. At intervals of at least 100 ms of monotonic wall time, it
releases finished cues and streams, including the final batch with no later
play request. This keeps playback retention independent of simulation ticks
`[03 R-AUD-02 §2]`.

The selected Sound Mode crosses the same output boundary independently of the
backend's stereo format capability. The default `Mono` branch uses the
inclusive beam rectangle's −585/−1585 pair. Exact `3D` uses the camera's battle
beam origin and extent, and terrain map extents, represented in 16-pixel cells:
the audio service applies the established planar inverse-distance factor between
its derived minimum and maximum distances before it submits PCM to the backend
`[03 R-AUD-01 §1]` `[03 R-AUD-01 §2]`. `Camera.BattleViewOrigin` converts this
build's framebuffer origin to retail's beam origin. At host zoom, which retail
does not have, the scaled beam span and inset are both truncating world-pixel
values before conversion to cells; this is the client-wide extension policy.

`audio.RegisteredOutput` extends the ordinary output seam only for loaded
mode-0 aliases. Its canonical PCM cache belongs to the immutable `Sample`, is
keyed by output rate and bounded per sample, and has no backend-global owner;
an active reader retains the bytes it needs after an alias or session goes
away. Each submitted reader holds independent pan state while backend base and
FX gains remain live output settings. Mode-1 voices and mode-2 streams use the
ordinary conversion boundary. §3.3 C20 states the API and fallback contract.

**REND-10 configured limit and tracking contract.** `audio.OutputConfig`
carries `MixingBuffers int` from the stored preference through delayed device
installation and subsequent option application. `Backend.ConfigureOutput`
retains it; changing it does not stop voices until the next admission. The
constructor defaults to eight [03 R-AUD-01 §2]. Nonpositive values keep the
existing settings-layer host recovery to eight; that is not a retail clamp.
Positive values have no upper clamp [02 R-SND-01 §2].

For non-looping producers, ordinary mixer admission compares the tracked count
to the configured limit, then steals in oldest-start order. The tracked count
may include completed buffers between explicit reaper calls: mode-1 loading
reaps before conversion/device creation, and the paced application pump reaps
at its deadline. Admission does not repeat that sweep after device creation.
A later stopped buffer therefore does not spare an older live tracked voice
from the mixer's steal [03 R-AUD-01 §1][03 R-AUD-02 §2]. The tracking table holds at most 32 voices. When it is
full and the configured limit exceeds that count, a new voice plays without a
tracking entry [03 R-AUD-01 §1 steps 2,7]. Such a voice is outside the ordinary
tracked stop-all/count. Separate host-only references let FX gain changes
reach these voices too, as the original system-wide wave-output setting did;
completion and application shutdown release those references. They never
participate in admission or MODE Off stop-all, including when the untracked
voice loops: the established stop-all visits only the fixed tracking table.

Registered aliases implement the four-instance restart and resolve configured
capacity before lazy device-player creation. `LoopingRegisteredOutput` is the
optional extension of `RegisteredOutput` for the front-end `BGM` alias; an
output without it is silent for that request and does not fall back to the
mode-1 path. A loop request first finds any tracked loop and returns before
capacity or static-instance work. Every tracked voice records its loop flag;
capacity stealing chooses the oldest non-loop only. If a configured limit is
occupied only by loops, the host drops the new request rather than inventing a
victim for the retail-unreachable edge. MODE Off, the paced pump and Close use
the same ordinary-voice ownership as non-looping cues. This does not change RNG
ownership or the separate narration stream. Tests exercise actual playback at
limits 1/3/32, live limit lowering, completion reaping, the
tracked/untracked boundary above 32, and loop exclusivity.

**REND-10 transient admission contract.** The concrete
`Backend.PlaySample` entry owns mode-1 unit voices and TEST previews. Production
alias playback uses `PlayRegisteredSample` because this backend implements
`audio.RegisteredOutput`; ordinary outputs preserve their existing one-shot
fallback. The new looping extension alone has no one-shot fallback. No public
signature change is needed.

Before creating a mode-1 device player, reap stopped transient references and
ordinary voices, then drop the request if all eight transient slots are busy
[03 R-AUD-01 §1]. This gate precedes ordinary mixer admission: a dropped ninth
transient must neither create a player nor steal a tracked voice. Conversion
and creation also precede capacity stealing. After a successful steal, the new
player is rewound once; rewind failure releases that new player and leaves no
gain, play, tracked or transient reference. Successful playback applies gain,
plays, tracks and then retains one transient reference until stopped/completed.
A voice stopped by global stealing frees its transient slot at the next reap.
Failed creation occupies no slot and cannot steal. Pump reaping uses the
existing presentation pacing [03 R-AUD-02 §2]. Transient bookkeeping never
adds to the global tracked count, changes host gain ownership, or extends MODE
Off beyond tracked voices.
Registered aliases and streams do not consume transient slots; application
shutdown releases existing player ownership and clears transient references.

Acceptance uses actual backend entry points with a configured limit above eight
to distinguish this gate from global stealing, completion and steal-then-reap,
registered/stream admission while transient slots are full, and failed creation.
Four-instance restart and registered-alias capacity-before-creation ordering
are implemented. Registered-alias eager reaping is corrected by keeping the
status sweep solely at the established mode-1 and pump entries.
Tests use a completed later voice at a full configured limit, distinguish
99/100 ms pump boundaries, and complete a voice during fake device creation
to prove there is no second transient sweep. No authoritative RNG or simulation
timing changes.

**REND-10 static-instance API.** The independently reviewed reaper repair
landed at `df6ccebe`. Keep `RegisteredOutput` and
`PlayRegisteredSample` unchanged. The backend owns a session-lifetime slice
keyed by `*audio.Sample` identity, with four instance slots holding the host
player and its pan reader. Replacement samples with identical aliases or PCM
have separate groups. Clear retained groups at `Backend.Close`.

Extend the private player boundary with `Position() time.Duration` and
`Rewind() error`; make `panReader` a synchronized `io.ReadSeeker` with `SetPan`
and `SetLoop`. A looping reader wraps its canonical PCM frames without EOF;
every static request sets loop state before its universal rewind, so a reused
looping instance can become one-shot.
Split global admission into capacity stealing before instance selection and
tracking after successful playback. Scan instance slots in ascending order:
first nonplaying wins immediately; otherwise remember the last null slot and
use strict greater cursor comparison, preserving the earlier slot on ties.
Lazy allocation fills 0, 3, 2, 1; a fifth all-busy request restarts the furthest
instance. The all-busy path first rewinds the selected instance before pan
setup, ignoring that reset's error. Every path then sets new pan and rewinds
again so buffered device data is discarded. A failure of this universal reset
returns before gain, playback or tracking. Otherwise apply current request/FX
gain, play, and append a new mixer reference even if
that player already has another reference. Preserve duplicate retail tracking:
stealing one reference stops the shared player but clears only that entry;
a restart before reaping makes remaining references live again.

Gain must have one current value per physical instance. Either retain it on the
instance or update every matching tracked/untracked reference on restart;
an older untracked reference must not overwrite a newer request's gain during
FX updates. Terminal cleanup must also release static instances whose ordinary
references were already reaped, without retaining departed-session samples.

**Platform residuals for this plan (`TODO(T23)`).** Ebitengine `Position` is an
audible-position estimate, not DirectSound's byte play cursor; an exact near-tie
decision needs a consumed-device-frame API. The output seam first sees a sample
at playback, so lazy creation of slot zero is explicit host recovery, not a
claim of registration-time allocation or equivalent failure timing. Rewind
can fail synchronously, but host Play has no error result; exact retail
SetVolume/Play failure timing remains outside that host boundary.
Retail aborts instance selection when a playback-status query fails, while
the host's `IsPlaying() bool` cannot report failure. Keep bool-based selection
as an explicit host residual; exact failure admission needs a status/error API.

Own only this design section, backend `player.go`, `pan_reader.go`, the shared
fake player in `live_settings_test.go`, `voice_limit_test.go`, and new
`static_sample_test.go` and `pan_reader_seek_test.go`. The registered configured-limit
fixture must use distinct sample identities so it continues to test the global
limit independently of the per-sample four-instance cap.
Acceptance locks fill order, first-idle reuse, strict ties, fifth restart,
sample identity, pan/gain updates including tracked/untracked aliases, both
all-busy pre-pan rewind and universal post-pan rewind ordering, continuation
after an early-reset error, and no playback/reference after a universal-reset error,
duplicate-reference steal/restart, and terminal cleanup. Independent review
accepted the implementation and its ordering regressions; source, integration
and post-merge synthetic/installed-asset gates passed at `0b2febe9`. Exclusive
loops and mode-1 creation/failure ordering remain separate work.

## 3. Contracts

Two contract series meet in these packages, and both keep the numbering their
source plans used, because Go comments cite them by bare number. A `Cn` in
`internal/render` or `internal/audio` is the composition/audio series of §3.2
and §3.3; a `Cn` in `internal/client` is resolved by subject — the shell series
of §3.1 for projection, palette, text and the frame boundary, the composition
series for strips, effects and models. `[C-1]` and `[C-3]` in `internal/client`
are C1 and C3 of §3.1.

### 3.1 The client shell — C1–C13

* **C1 Projection.** World-to-screen is the integer orthographic projection of
  `[03 §2.5]`: subtract the camera, one map pixel per whole world unit, and the
  half-height vertical shear, `screenY = (worldZ>>16) − ((worldY>>16)>>1) −
  camZ + originY`. Terrain sits at ground height, so its shear term is zero,
  but the tile blitter still goes through the same helper so no consumer copies
  a path-specific constant. Presentation zoom scales the same expression
  `[F-P1-008]`.
* **C3 Clamp and the battle viewport.** Per axis the camera clamp computes
  `maximum = mapSize − viewSize`, clamps negative to zero **first**, then above
  the maximum, and preserves that order even when the viewport is larger than
  the map `[07 §10]`. The rectangle it measures against is the drawn-chrome
  battle viewport, `[129,32]..[W−1,H−33]` in logical coordinates: retail's beam
  clip starts at 128 and the chrome's interactive region one pixel inside it,
  and the pointer region and the drawn rectangle must be expressed in the same
  space or a click lands somewhere other than where it looks `[07 §6]`
  `[07 §8]`. `internal/camera` owns the arithmetic; `viewport.go` owns the
  rectangle.
* **C4 Minimap surfaces.** Three surfaces with distinct lifetimes: the picture
  is immutable after map load, the mapped composite is rebuilt only when dirty,
  and the final surface is rebuilt each tick; the blink phase is a separate
  scalar and is never folded into the dirty word `[03 §3.6]` `[03 §3.7]`
  `[03 §3.8]`. The viewport rectangle is stroked as a one-pixel outline in
  colour-map entry 14 over the copied radar surface; sensor circles use entry 10
  for radar and sonar and entry 12 for jam `[03 R-MM-01 §1]` `[03 R-MM-01 §2]`,
  and the blip and ring gates are `[03 R-MM-01 §3]`. The 126-pixel letterbox
  and the click lens are `internal/camera`'s.
* **C6 Selection membership.** Drag endpoints convert to presentation
  coordinates by subtracting the camera and adding the view-pane origin (128,
  32); each axis is sorted independently and both boundaries tested
  inclusively. With the modifier clear, units inside the rectangle are set and
  those outside cleared; with it set, inside toggles and outside is preserved.
  Owner slots are visited in stable ascending order `[07 §9]`.
* **C7 Indexed colour.** There is **one** semantic map, not two: the
  logical-to-physical table *is* the GUI-to-base mapping, built at GUI bootstrap
  by matching every `GUIPAL.PAL` entry into the installed `PALETTE.PAL` by the
  sum of absolute channel differences, lowest destination index winning a tie.
  Primitive, FNT and `dcb[]` colours resolve through it **before** the byte is
  written into the indexed surface. GAF, PCX and TNT bytes are already physical
  indices and bypass it, as do the `LHT` and `SHD` lookups. RGBA expansion
  happens only at present time and consults `PALETTE.PAL` alone — no GUI lookup
  is performed again during indexed-to-RGB presentation `[03 §4.3]`
  `[03 §4.3.1]` `[07 "Retail palette contract"]`.
* **C8 FNT.** Apply the font descender to the baseline (the low byte of the
  control word, read signed); offset zero is an absent glyph, skipped in both
  measurement and drawing; a space advances seven pixels without drawing;
  advance stops at newline; the string is **truncated to the maximum width
  first and then clipped as a whole rectangle** `[03 §7.1]`
  `[03 R-FONT-01 §2]` `[03 R-FONT-01 §3]`. The control word's high byte is a
  base-character bias applied during measurement only when it is at or below
  the code; retail fonts store zero.
  Both executors admit the rectangle at the unadjusted baseline against the
  private surface's inclusive bounds. The signed baseline may then put glyph
  rows above that private clip; only host framebuffer bounds suppress those
  writes `[03 R-FONT-01 §4]`.
* **C9 Frame boundary.** Update calls the injected step, then reads the latest
  committed frame. Presentation samples that frame without interpolation; the
  client never advances the simulation clock and never writes authoritative
  state [I6].
* **C10 Presentation randomness.** No draw or audio path reads the simulation
  RNG. Presentation variants use the CRT stream where their contracts require
  it `[03 §5.4]` `[03 §5.6]` `[03 §8.3]` [I4].
* **C11 Backend.** There is one Ebitengine game loop and one window. Its fixed
  logical size is distinct from the negotiated outside size, so Ebitengine can
  letterbox without changing authored HUD coordinates.
* **C13 Draw order.** Units and comparable world objects reach the draw loops
  through per-row screen-Y bucket insertion, appended in enumeration order.
  Paint order is Y-sorted rows with in-row enumeration order, and there is **no
  depth test** `[03 §1]` `[03 R-RAST-01 §7]`.

C2 (scroll magnitude) and C5 (the 30-entry keyboard ring and 24-record mouse
ring) belong to `internal/camera` and `internal/input`; the interface design
document carries them.

### 3.2 Composition, models, effects and projectiles — C1–C12

* **C1 The ten barriers.** Retail stages the frame through ten fixed-order
  strips, each closed by a barrier carrying its pass number `[03 §1]`.
  `drawCommittedFrame` is that sequence: clear; terrain, minimap and clip
  preparation; strips 0–2; the first feature pass, which owns the never-seen
  admission; strips 3–4; unit pass A interleaved with that row's deferred tall
  features; strip 5; strip 6 (its committed nanolathe particles); the projectile pool; the fixed
  effect pool; strip 7; unit pass B; strip 8; the unit-label walk; strip 9; fog;
  selection; interface. Slots with no producer are kept as explicit no-ops so
  the barrier order survives — see C2's note on strips 0, 1, 3 and 8.
* **C2 Where projectiles, effects and fog sit.** Projectiles and effects are
  drawn **between strips 6 and 7** and are not strip objects. Fog covers
  everything world-drawn but is composed **after** strip 9 and **before**
  selection and interface, so it never darkens the selection overlays or the
  HUD `[03 §1]`. Strips 0, 1, 3 and 8 have no producer anywhere in retail and
  hold no object in any session; their walks are no-ops and nothing may be
  attached to them `[03 R-FX-02 §5]`.
  Weapon-smoke puffs pass the per-particle coverage gate before the shared
  composer records their sprites, so classic and modern both hide smoke outside
  sight. Geothermal steam skips that gate `[03 R-FX-01 §3]`
  `[03 R-FX-02 §3]`. Explosion art and calculated flashes also skip coverage
  admission and remain beneath the fog composite `[06 R-WFX-01 §2]`.
* **C2.1 Nanolathe publication.** Strip-6 emitter records own their particles
  and advance them during the phase-11 strip sweep. Publication copies every
  live particle into `Frame.Strips`; the client paints those copies as raw
  palette 2×2 rectangles after the per-particle coverage gate. A client does
  not create, seed, advance, expire, or recolour nanolathe particles from
  events, so any client renders the same committed snapshot identically
  `[03 R-STRIP-01 §2]` `[03 R-STRIP-01 §3]` `[03 §5.5]` [I6].
  The Community team-colour mapping reads committed owner metadata at draw
  time; its presentation-only exception is DESIGN_GPU_RENDERER §37.1.
* **C2.2 The debris draw's own two producers.** A whole-piece debris record
  carries two engine bits the simulation never reads: SMOKE and FIRE are draw
  inputs, and a frame that draws the piece makes one strip-9 smoke-puff
  container and/or one flame-stream trail container at it `[04 R-COB-04 §2]`.
  They ride the publication boundary as `frame.DebrisView.Smoke`/`.Fire`,
  because the arena that holds them is simulation state [I6].
  `internal/render.DebrisTrails` builds the pair — the strips-5/9 smoke puffer's
  init `(0, 1, 0, 0, 0)` on `smoke 1` at the piece, whose one spawn draw is the
  puff's own last frame `crtRand·(frameCount − 3)/0x8000 + 2`, and the
  flame-stream trail class on `flamestream` at the piece jittered per axis by
  `crtRand·3/0x8000 − 1` whole units with a container life of
  `crtRand·3/0x8000 + 1` ticks, four draws spent before the pool is consulted
  `[03 R-FX-01 §3]` `[06 R-WFX-01 §5]`. All five values come from the client's
  **private presentation CRT copy**, never the session stream (DET-01, §5), so
  frame cadence cannot reach the tick [I4]; with no stream bound neither
  container is built, rather than one being built with a substituted value [I9].
  **Persistence, and the barrier.** Both producers make CONTAINERS, and both
  reductions RT08 carried are now closed. `internal/client`'s debris trail store
  (`debris_trail_store.go`) holds them: the smoke container's one puff drifts by
  `windX·8` / `windZ·8` on the raw X and Z words and rises by
  `authoredGravity·4` on the raw Y word, walks its animation cursor after each
  authored hold (one presentation draw per advance) and retires when the cursor
  reaches its own drawn last frame; the fire container lays `lifetime + 1`
  coincident segments, one per tick, whose cursors advance every tick modulo
  `frameCount − 1`, and all of them go together the tick after its deadline
  `[03 R-FX-01 §3]` `[03 R-STRIP-01 §2]` `[06 R-WFX-01 §5]`. There is exactly ONE
  implementation of each of those expressions and it is not in this package:
  the puff's drawn last frame, its half-to-full animation hold and the flame
  trail's per-axis step are `internal/render`'s `SmokeLastFrame`,
  `SmokeFrameHold` and `FlameTrailStep`, called by BOTH the simulation's
  phase-11 strip table and this store. Each takes the already-drawn CRT value
  rather than a stream, so the session keeps spending the simulation stream and
  the store its private copy, in the same order and count as before [I4]. The
  two drift words are likewise the simulation's own derivation read back from
  the committed heading and strength (`world.WindVectors`, the FIRST word to
  world X and the SECOND to world Z), not a second copy of that trig
  `[R-WIND-01]`. The store draws at
  **barrier 9**, after strip 7, the airborne unit pass and the unit labels,
  following the published strip-9 objects — the order a later object in one
  vector has over an earlier one `[03 §1]`. Its records go through the same
  one-point coverage gate every other strip-9 smoke and flame record passes: a
  three-tick-old puff has drifted away from the point that produced it, so the
  producing frame's own admission no longer stands for it `[03 R-FX-01 §3]`.
  The store is a SEPARATE list from the simulation's published strip objects,
  because the producer is the draw pass: the simulation never makes these
  containers and has nothing to publish [I6].

  **Committed-tick keying (the pre-record and interpolation rule).** The store's
  sweep is keyed on the committed tick number, and so is its admission. This
  build composes the same committed tick more than once — every interpolated
  frame between two ticks, the pre-record's re-record after a miss (§13.10,
  `prerecord.go`, which rolls the presentation CRT back for exactly this class
  of hazard), and the `--shot-renderer both` route through
  `SnapshotPresentationCRT` — so a store stepped per rendered frame would
  animate and drift at the host's refresh rate instead of the tick rate. A
  repeat of a tick already stepped therefore does nothing, and a tick that moves
  backwards or forwards by more than the catch-up bound clears the store, the
  way the other retained presentation histories are retired when their
  producer's continuity breaks.

  **One stated divergence: the admission cadence.** Retail's producer runs per
  RENDERED FRAME — "one container is created per frame per burning piece, all
  overlapping" `[03 R-FX-01 §3]`. Admitting one per rendered frame here would
  scale smoke density with the host's refresh rate and with whether a pre-record
  hit, because this build renders several frames per committed tick while the
  containers are stepped by the tick. The producers therefore run once per
  burning piece per committed tick, stamped per debris slot. That is retail's
  density at a frame rate equal to its tick rate; at a higher frame rate retail's
  smoke is denser than ours. This is a presentation-rate decision, not a claim
  about retail.

  **Bound.** The store evicts its oldest container when a pre-insert count
  exceeds 400, so it holds at most 401 `[03 "Strip storage and lifecycle"]`
  `[03 R-STRIP-01 §1]`. Retail's strip 9 is one vector and its 1000-slot
  container pool is shared between these containers and every simulation-side
  strip-9 producer `[03 R-FX-02 §4]`; this build splits the two lists, so each
  carries the per-strip bound on its own and they cannot crowd each other out
  the way retail's single vector does.
* **C3 Buckets.** The plot-cell window is a pure function of the committed
  camera and the map extent, so the feature passes and the bucket build derive
  the same window; the bucket row is measured from the unclipped window origin
  even when clipping moves the first drawn cell. Pass A draws the grounded
  units of each window row; pass B draws the units whose committed mode mirror
  is not "grounded" — airborne aircraft, attached cargo, save-installed — at the
  end of strip 7 `[03 R-RAST-01 §6]` `[03 R-RAST-01 §7]`.
  Admission uses the committed hull visibility predicate, including ownership,
  cloak and submerged-unit rules `[03 §3.2]`. A unit's unsheared ground anchor
  must not add an unexplored-fog rejection: its hull may already reach sight,
  especially on elevated terrain. The later fog overlay obscures the covered
  pixels `[03 §3.3]`. Strategic icons and Enhanced building foam and hovercraft
  dust share this hull admission; nano particles keep their own point gate.
* **C3.1 A 3DO feature draws as a pseudo-unit.** The per-cell dispatcher fills
  it with "model pointer, position and the slot's orientation words" and hands
  it to the ordinary per-unit present `[03 R-RAST-01 §6]`, so `drawFeatureModel`
  passes the committed record's bank/heading/pitch into the same model build
  `drawUnitModel` uses for a unit. Only a corpse carries a nonzero triple
  `[05 "Feature instance and terrain cell"]`, so a wreck lies the way its unit
  fell and every map-authored 3DO feature draws at three zeros as before.
* **C3.1.1 A 3DO feature casts the structure shadow.** The pseudo-unit sets the
  structure class bit, so it takes the structure branch of `[03 R-REN-03D §1]` —
  the separate quarter-sheared rasterization, punched by the body and cached —
  and not a silhouette copy. Its gate is the master shadow bit alone: a feature
  definition authors no `noshadow` key, so that term of the common gate is
  false, and Digger, `canhover` and `floater` are clear for every feature draw,
  none of which the structure branch reads anyway. The branch's one extra
  predicate is the pseudo-unit's own — it is skipped when the definition ordinal
  is `0` **and** `hi16(unitY) < seaLevel` — and ordinal `0` is the feature
  pseudo-unit, so a 3DO wreck at or above the waterline casts a building-shaped
  shadow five pixels right of its body and a submerged one casts none
  `[03 R-REN-03D §1]` `[03 R-RAST-01 §4]`. That the ordinal is reserved for the
  pseudo-unit is the **Supported inference** stated in `[03 R-REN-03D §1]`; the
  branch and its predicate are Established. The shadow shears by the terrain
  height under the feature, as a unit's does `[03 R-REN-03D §3]`, so the feature
  draw supplies that ground height alongside the gate. Both executors read the
  one `CastsShadow` verdict, so classic and modern gain the shadow together.
* **C4 Strip lifecycle.** The update dispatcher evaluates removal **before**
  update for every object and stably compacts on a positive verdict, so
  survivors keep order; a terminal condition created during an update is noticed
  only on the next invocation. Producers append at the end; when the pre-insert
  count exceeds 400 the oldest is destroyed first, so steady state is at most
  401 `[03 §1]` `[03 R-FX-02 §4]` `[01 R-CORE-01 §4.4.1]`.
* **C5 The fixed effect pool.** Up to 300 fixed-size records; an append at or
  above the cap allocates nothing. The integrator advances velocity against
  gravity, can restore a prior position and invert and halve vertical velocity
  on terrain or water contact, single-steps both embedded animation players,
  clears non-looping sequences at termination, and removes emptied records by
  stable left compaction **within the same updater call** — unlike generic strip
  objects `[03 §1]` `[03 R-FX-02 §1]`.
* **C5.1 Published player liveness.** A record survives until **both** players
  are inactive, so the one that finishes first is still published every tick
  with its cursor reset to 0 — an index indistinguishable from a live first
  frame `[03 §4.4]`. `EffectView.ActiveA` / `ActiveB` therefore carry each
  player's liveness explicitly, written by the pool's snapshot and by nothing
  else; the two draw passes gate on them and never infer liveness from the
  durations, the art name or the cursor index. The rule is the sequence pointer
  and nothing else: a player is live until termination clears it, a layer with
  no player draws nothing, and admission activates a player only when authored
  timing resolved `[I9]`. Timing resolution is **per player** — the event's
  named art is the primary and whatever generated table the producer attached
  is the secondary, each with its own authored holds `[06 R-WFX-01 §2]` — so an
  impact's art owns a real player instead of being a static frame 0 held on
  screen by the calculated flash. Lifecycle stays in the pool: the client owns
  no expiry clock and decrements nothing.
* **C6 Render types 0–7** `[03 §5.4]` `[06 R-WFX-01 §4]`: 0 a line pair using
  the weapon's colour and secondary colour; 1 a base sprite plus a model; 2 the
  22×22 displacement frame through the lens blitter, whose failed screen-rect
  admission **returns from the whole renderer** so later records are skipped that
  frame; 3 base plus model on a distinct orientation path; 4 a selector over
  five fixed `fx` sequences with `−1` suppressing the draw; 5 a lifetime-scaled
  frame of `flamestream`; 6 the orientation stored verbatim in the record; 7
  randomized segmented lines with the `0x50000` denominator and
  `rand·11/0x8000 − 5` jitter. Types 1, 3, 4 and 6 all take frame 0 of the fixed
  `shadow` entry as their base. Frame counts for the two frame-selected families
  come from the shared `fx` entry, resolved through the same bank cache the blit
  uses, so the modulus and the blitted frame can never disagree.
* **C7 Beams.** `color2 == 0` draws one stroke; otherwise two, the secondary
  outer and the primary inner. No anti-aliasing and no distance-based width
  `[03 §5.4]`.
* **C8 GAF cursors.** A retail frame reference carries an `int32` duration in
  **whole ticks**, not milliseconds. The playback cursor is index, countdown and
  loop flag; a single-tick step advances when the countdown is below 2, wrapping
  to 0 or clearing; a delta step with a negative `int16` can cross several
  frames. A frame with hold `h` is shown for `max(h, 1)` advances. Every GAF
  entry in every retail file carries 1 in its loop word, and the weapon parser
  **clears** it for explosion art, which is what makes an impact play once
  instead of flashing forever `[03 §4.4]` `[06 R-WFX-01 §1]` `[fmt gaf]` [I13].
* **C9 Consumer-specific GAF dispatch.** The ordinary compositor expands ordered
  children and applies each alternate selector; tinted and nonzero-mode glyph
  composites expand every child through ALP. Glyph leaves retain the authored
  raw/RLE distinction and their own mode/key rule `[03 R-FONT-01 §6]`.
  Classic gray/dither walks all children in order with the raw gate before each
  recursion, without clipping to the parent canvas `[03 R-COMP-01 §2]`.
  Presentation resampling preserves that dispatch selector. The modern raw
  glyph uses the existing destination-light stream and its approved LHT colour
  approximation; compressed glyphs retain the source-remap stream. Unsafe raw
  glyph source rows are suppressed as host safety policy, not clamped.
  Modern fog retains the atlas for ordinary stock frames. A selected composite
  or frame extending outside its atlas tile sends the whole fog command through
  ordered child operations: repeated gray applications use separate destination
  snapshots, and black children retain ordinary/ALP dispatch. The shader uses
  the already approved modern desaturation and ALP colour approximations.
  Compressed gray/dither frames are gated before each recursion; ordinary black
  RLE remains drawable. `FogContentError` reports shader compilation failures.
  Projectile model spans select `GAFFrame.DirectRaster` before either classic
  rasterization or modern geometry recording. The visibility-mask compiler
  selects the same view and compares each storage byte to the authored key.
  RLE input therefore supplies encoded storage bytes to these direct consumers,
  never decoded coverage. Composites and file spans too short for the raster
  are rejected at this boundary `[02 R-MALF-01 §6]` `[fmt gaf]`.
  Remaining feature-mask and structure-texture consumers retain their marked
  compatibility raster pending traces. Calculated flashes currently have only
  generated leaf frames; their existing LHT path needs no composite dispatch.
* **C10 Palette, SHD and LHT.** Palette indices convert to RGBA at present time
  only. Model lighting selects an `SHD` row: a cleared shade bit (`DONT_SHADE`)
  pins the near-identity row **15**; otherwise
  `row = trunc(dot(N, L)·5.0) & 0x1F` with `L = (−0.8, 1, 0.25)`, interpolated
  gouraud-style across the span and sampled as `SHD[row·256 + texel]`. Rows 0–14
  darken and 16–31 brighten; there is no other row source, and row 16 is not a
  retail value anywhere. That path is reached only by a `BMcode = 0` unit with
  the `Shading` option on; every other unit maps textured faces with no `SHD`
  step `[03 §4.3]` `[03 §4.3.2]` `[03 R-RAST-01 §5]` `[03 R-RND-02A]`. The
  explosion and muzzle-flash ground halo uses `LHT[level·256 + index]`,
  brighten-only, admitted only over terrain coverage; row 0 is authored and
  near-identity, so a resolver reporting level 0 must still draw
  `[03 §4.3.1]` `[fmt pal]`.
* **C11 Impact presentation.** The named explosion art always composes **over**
  the calculated disc, which is why the pool is walked twice: every record's
  secondary (calculated) frame through the flash blitter first, then every
  record's primary (named art) frame through the ordinary blitter. One
  interleaved walk would let an early impact's art be brightened by a later
  impact's disc. Three calculated tables are generated once per battle with
  researched frame counts, sides and a hold of 2 ticks; a weapon with no art
  holder still shows the disc, which is why an impact is never nothing
  `[06 R-WFX-01 §2]` `[03 R-FX-01 §4]`.
* **C12 Committed sampling.** The client reads world positions, piece
  rotations, rotation accumulators and animation ticks from the one committed
  frame for the current tick. Retail does not interpolate between updates
  `[03 §2.4]` [I6].

### 3.3 Audio — C13–C20

* **C13 Orientation cache.** Per drawn unit the renderer compares its cached
  orientation triple against the committed bank, heading and pitch; when **any
  axis differs by more than 7 angle units** it refreshes the cache and schedules
  a rebuild. A dirty frame resets affected subtrees from pristine model vertices
  and reapplies transforms ancestor-after-descendant. Unit position never enters
  piece math — it enters only at final screen placement `[03 §5.2]`.
  `PieceView.DontCache` is the committed inverse of the render-piece cache bit,
  and `UnitView.CacheRevision` is a copied, monotonic Go representation of the
  script-driven cached-image invalidation. It is not a retail physical counter:
  the VM advances it for the exact invalidating setters, restore and rebind.
  `CacheValidityRevision` advances only for validity clears and full resets;
  cache/shade image discards leave it unchanged, preserving the mobile no-key
  direct-draw fallback. Frame publication consumes or clears neither revision `[03 R-COMP-01 §4]`.
* **C14 Sound categories.** Retail identity is a 64-byte name and 24
  twelve-byte event rows indexed by slot, where slot 0 is an unused sentinel and
  1–23 are the events; a row holds a variant count and two parallel arrays of
  64-byte strings, alias and speech caption. Go stores named fields without
  layout assumptions `[03 §8.3]` `[02 R-SND-01 §1]` [I13].
* **C15 The slot table.** The per-slot priority and cooldown table is **static
  and global across every category** — slot 1 `select` priority 10 cooldown 0,
  slot 2 `underattack` priority 9 cooldown 20 with default speech "Under
  Attack", and so on for all 23 rows `[03 §8.3]`.
* **C16 Insert.** Drop if the current frame is before the slot's next-allowed
  frame; drop if any queued entry already holds that slot; if the queue is full,
  resolve the **last** entry silently (printing its speech, playing no sound),
  free its text and shift it out; then insert so the queue stays sorted by
  **descending priority**, placing the new entry *after* equals — which makes
  equal priorities FIFO `[03 §8.3]`.
* **C17 Resolve.** Look up the acting unit's category and its row; draw the
  variant with the **fifteen-bit CRT draw** scaled by the row's count. The draw
  is **unconditional** — it happens on every resolve, including silent ones and
  including a row whose variant count is zero, before any gate. Then, if audible
  and the crowding gate `10 − audioThreshold < priority` passes and the row has
  at least one variant and the enable flag is set, play the alias and set the
  slot's next-allowed frame to `frame + cooldown × 30`; if showing text and
  `10 − speechThreshold < priority` passes, print the override or the caption,
  prefixed by the unit name when the unit is still alive `[03 §8.3]`
  `[03 R-AUD-01 §3]`.
* **C18 Drain.** Retail runs once per **rendered frame, outside the simulation**: an
  empty queue does nothing; within 30 frames of the base time the head resolves
  **silently**; otherwise the head resolves audibly and the base time resets.
  Then the head's text is freed and it is shifted out. The consequence to
  preserve is that **at most one voice is audible per 30 frames** `[03 §8.3]`.
  The events drained are the **retained** committed events, not only the current
  slot's: a session step can publish several frames before a rendered frame
  arrives — a hitch, or the 2×/3× speed setting — and reading one slot dropped
  every superseded tick's cues, making delivery depend on render cadence. The
  raise may cross the publication boundary only when each committed tick's
  events are applied exactly once in raise order `[03 R-AUD-01 §7]`
  `[03 R-AUD-02 §2]`.
  Nanolathe's host cadence policy admits at most one acknowledgement queue pop
  per 30 Hz client Update opportunity. Repeated modern draws between Updates
  do not consume additional entries, and missed opportunities do not cause
  catch-up pops. This service cadence continues while paused and is independent
  of game speed; the existing arbitration window and slot cooldowns still use
  the committed global tick. Retained status insertion and ordinary audio event
  delivery run on every presentation, independently of this pop gate. This is
  an explicit presentation policy, not a claim that retail drains per sim tick.
* **C19 No feedback and spatial placement.** Audio state never enters the
  simulation and no audio path draws from the simulation RNG [I4] [I6]. The
  selected Sound Mode, rather than a device stereo-capability probe, selects
  positional placement: Mono applies the inclusive −585/−1585 beam rectangle;
  exact 3D uses `(dx, 0, dy)`, `minDist = trunc((viewH+viewW)/2)×16`,
  `maxDist = (mapW+mapH)×16`, and the documented inverse-distance rolloff
  held after maximum distance `[03 R-AUD-01 §1]` `[03 R-AUD-01 §2]`.
* **C20 Samples.** `formats.LoadAudio` owns detection, metadata and payload
  bounds for raw PCM, DIGI and RIFF under `[fmt wav]`. `internal/audio.Decode`
  takes an owned PCM copy, derives playback frame alignment from channels and
  bit width, and applies the documented host-safety policy for unsupported or
  malformed PCM. No second decoder walks chunks or trims a DIGI prefix.
  Samples resolve through VFS provenance and cache by alias.
  There is **no eviction at all** — one decoded blob per alias, retained for the
  life of the session; the 255-alias cap is the registry's, not a cache size
  `[03 §8.2]` `[02 "Sound aliases"]` `[fmt wav]`.
  Registered aliases and mode-1 voice filenames have separate caches: the
  same name may designate different files in those two paths. Mode-2 stream
  opens read the exact resource path anew and never use an alias-cache hit
  `[03 R-AUD-01 §1]` `[03 R-AUD-02 §1]`.
  `RegisteredOutput` is an optional presentation extension of `Output` for
  those mode-0 registered aliases: `PlayRegisteredSample(*Sample, volume,
  pan)` may reuse sample-owned, canonical stereo float32 PCM at its output
  rate. The canonical bytes are unity-volume and centred; each play keeps its
  own reader and applies pan plus the ordinary backend gain outside those
  immutable bytes. `Sample` is immutable after decode or registry admission,
  and owns this bounded rate-keyed host cache, so replacing an alias with a new
  sample selects new PCM and a departed session retains no backend-global PCM.
  Outputs without `RegisteredOutput` use `PlaySample`; outputs without
  `LoopingRegisteredOutput` are silent only for a loop request. Mode-1 voice loads and
  mode-2 streams remain ordinary per-play conversion paths; this host cache is
  not a claim about retail device conversion or its mode-1 policy.

C9 of the source plan — screen shake — is not carried here. The shake driver is
authoritative phase 10 and the runtime document owns it; presentation only adds
the published offset to the camera.

### 3.4 Not implemented

* **Lens transparent-key initialization.** The generated render-type-2 lens
  uses the ordered `drawlist.Lens` destination reader in both executors. Its
  constructor arithmetic, relative displacement sampling and prior-output
  preservation are established [03 R-FX-01 §4]. Retail leaves the key byte
  uninitialized; the producer uses SC18 zero-initialization and retains the
  code-site question. Modern's RGB key comparison is the explicit approximation
  in DESIGN_GPU_RENDERER §2. Stock assets define MINDGUN without a unit binding;
  validation uses an authored projectile fixture.
* **The mobile and Digger shadow branches.** Retail selects one of three shadow
  branches: a Digger's buried-clip silhouette, a mobile unit's waterline-clip
  silhouette, and a structure's re-rasterized, punched, cached shadow. Only the
  structure branch's rasterize/punch/tint technique exists; it stands in for the
  other two, with the vehicle-shadow gate still applied to them
  `[03 R-REN-03D §1]` `[03 R-REN-03D §6]`.
* **Music and CD/MCI.** File-backed music plays through the portable desktop
  audio backend (§2.6). Native CD drives and per-disc registry history remain
  unavailable `[03 §8.4]` `[03 R-AUD-01 §4]`. The five playback modes and
  category fades and the damage/death-driven intensity producer are implemented
  `[03 R-AUD-01 §5]`. Native drive/history support remains the platform residual.
* **Empty established slots.** The key-controlled overlay before the unit
  labels, the auxiliary unit traversal at the end of strip 7, and the two
  optional overlays after strip 9 have no published draw record; they are left
  as empty passes rather than filled with an invented route `[03 §1]` [I9].
* **Startup, Intro, campaign endings and credits** use `formats/zrb` for indexed movie frames and PCM,
  `Client.SetMoviePalette` for a shared classic/GPU display palette, and
  `Backend.NewMoviePlayer` for finite soundtrack output with its own lifetime.
  PCM converts/resamples in bounded reads; full decoded video is never retained.
  DESIGN_INTERFACE_HUD_INPUT §3.9 owns the shell behavior `[03 §9]` `[fmt zrb]`.
  Movie capture remains excluded by ARCHITECTURE.

## 4. Retail behaviour that is not a bug

* **A structure paints over an aircraft in a later Z row.** Paint order is
  Y-sorted rows with no depth test, so a tall building drawn in a nearer row
  covers an aircraft behind it. This is the retail order `[03 R-RAST-01 §7]`.
* **The health bar sits below the unit's anchor row** — ten pixels below, at
  `sy0 + 42` — and is drawn only for units belonging to the viewing player,
  whatever the option, with no line-of-sight test beyond the label walk's own
  admission `[03 R-FX-01 §6]` `[04 R-SPEC-01 §6]`.
* **Fog is hard-edged.** Visibility culling is binary on 32-pixel tiles: there
  is no `ALP` blend at the LOS edge and no intermediate opacity `[03 §3.3]`.
  Fog and LOS are not applied at raster time — a tree is never culled by its
  anchor tile; the overlay composed after strip 9 darkens features, units,
  shadows and projectiles alike `[03 R-RAST-01 §6]` `[03 §5.1.5]`.
* **Strips 0, 1, 3 and 8 are always empty.** Their walks are real and their
  barriers hold the order; no producer exists in retail `[03 R-FX-02 §5]`.
* **At most one unit voice is audible per 30 frames.** Cues inside the window
  still print their speech; they simply play no sound `[03 §8.3]`.
* **The variant draw is spent even when nothing plays.** A silent resolve, and
  a row with zero variants, still consume one CRT value `[03 §8.3]`.
* **A carrier can occlude its own cargo.** With a key plane present the carrier
  and its attached children resolve per pixel against one height plane rather
  than by draw order `[03 R-REN-03A §4]`.
* **Nanolathe spray is a particle emitter, not a beam.** Each record spawns
  five particles a tick from the builder's nano piece into the middle of the
  target's bounding box; the cone comes from the narrowed target box, and every
  emitter passes the same colour — the variation is footprint jitter
  `[03 §5.5]` `[05 R-P0-06 §5]`.
* **Every placed copy of an animating feature is on the same frame.** Retail
  initialises one rest cursor per *definition*, not per instance
  `[05 R-FEAT-01 §1]`.
* **The authored selection primitive is never drawn.** It is swapped to
  primitive zero at model load and the face walk starts at primitive one, for
  completed and under-construction models alike; what appears under a selected
  unit is the footprint quad derived from the root piece's vertex bounds
  `[03 R-SEL-02A]` `[03 R-WATER-01 §1]`. The four corners rotate in place
  through the model package's shared per-axis arithmetic, without allocating
  a piece-state slice or transform chain; bounds already contain the root offset.
* **There is no wake rectangle.** Wakes are script-emitted strip-2 sprinkles;
  the rectangle earlier readings attributed to wakes was the selection frame
  `[03 R-WATER-01 §1]`.
* **Turning shadows off turns three bits off.** The bulk INI key fans one value
  across the feature-shadow, vehicle-shadow and master-shadow bits
  `[03 §5.3]` `[03 R-REN-03D §1]`.

## 5. Divergences

* **Completed units crossing factory yards retain per-pixel composition.**
  Retail detaches a product at completion `[04 R-FAC-02 §3]`. Nanolathe’s
  independent Z-row blit can then be overwritten by the factory plate. The requested host
  presentation policy extends the existing height-plane staging
  `[03 R-REN-03A §4]` to admitted, completed grounded mobiles whose authored
  footprint rectangles overlap an admitted, completed structure builder.
  `UnitView.IsFactory` copies `Builder && BMcode == 0` from the definition;
  mobility, unit names, selection and remembered construction history are not
  classification inputs. Ground units crossing any open factory yard receive
  the same treatment, including after save restoration. The current frame alone
  establishes the group, and strict rectangle overlap ends it when the edges
  touch. Actual cargo stays first in its published order; yard occupants follow
  in committed unit order. A unit overlapping multiple factories joins the
  first admitted factory in that order. Existing visibility and pass-A window
  admission apply independently to both subjects. Independent occupants join
  only factories with the same current cloak state: the combined image has
  just the carrier's final blend `[03 R-RAST-01 §7]`. Differing cloak states
  retain ordinary independent row painting, including its factory occlusion
  limits, so grouping cannot make a cloaked occupant opaque or tint an opaque
  one. This admission is recomputed each frame; actual attached cargo retains
  its established carrier composition. A grouped occupant has one
  body present, through the factory; geometry preparation skips its standalone
  packet. Neither positions, attachment links nor height keys change. The
  existing per-pixel height test keeps the factory walls able to occlude the
  unit while the lower plate cannot overwrite its higher surfaces. This policy
  applies to both executors and uses no depth bias.

* **The burning-debris fire effect is stepped by the tick, not by the frame.**
  Retail creates one fire container per rendered frame per burning piece, four
  CRT draws each `[03 R-FX-01 §3]`, and those draws come out of the one
  main-thread CRT stream that retail's wind interval, meteor scheduler and
  skirmish victory timer also read `[01 §7.5]` `[01 §7.6]`. Retail's own
  wind changes, meteor strikes and victory instant are therefore a function of
  its frame rate. Nanolathe does not clone that: the session steps debris once
  per committed tick, and the visual trail family that would follow retail's
  per-frame cadence lives in the client on a private CRT copy. This is
  a deliberate departure — cloning it would make our own runs unreproducible
  from their seed pair — and it is the determinism half of the admission-cadence
  divergence §3.2's debris-trail store states in density terms [I4] [I6].

* **One window backend replaces two.** Retail has a GDI windowed path and a
  DirectDraw fullscreen path `[03 §4.1]` `[03 §4.2]`; Nanolathe has one
  Ebitengine loop presenting a software framebuffer. The fixed logical size and
  the negotiated outside size stay distinct because the HUD arithmetic depends
  on that distinction. The displayless path composes the same session rather
  than running a parallel client.
* **Presentation draws private CRT copies.** The client copies state for
  segmented projectiles, while the audio queue and music owner each copy state
  at binding. Their cadence cannot perturb the session CRT. Briefing animation
  likewise stays in its front-end lifetime and never supplies a battle seed.
  DESIGN_RUNTIME_DETERMINISM §5 owns the complete approved divergence from
  retail's shared main-thread history [I4] [I6].
* **Battle cue state has a session lifetime.** The shell keeps one audio
  service across briefing, battles and save adoption. `Session.InitAudio`
  resets that service's pending category cues, unit references, drain base,
  per-slot deadlines and committed-event deduplication on a new service
  binding, then binds the new resolver and private CRT copies. Repeating
  initialization for the same session/service preserves its queue, cooldowns
  and random history. Fresh and restored battles have new session objects;
  assigning the shell service to a detached candidate does not reset it.
  The successful presentation adoption performs the binding. Sample caches,
  aliases, preferences, music and playback hooks retain their service lifetime.
  This is Nanolathe host ownership policy for independent session tick domains,
  not a claim about retail teardown. The queue still starts at base zero and
  uses the unchanged 30-tick drain window and slot cooldowns `[03 §8.3]`.
* **A missing art bank is not fatal.** Retail treats an unresolvable animation
  bank as fatal: a modal message box naming the constructed path, then exit. A
  presentation client cannot do that to a running battle, so an unresolvable
  bank or entry draws nothing and the identity stays published and unresolved
  [I9]. The consequence is worth stating plainly, because it is silent by
  design: **`DrawEffectViews` skips art it cannot resolve.** Effect identity is
  a *pair* — the entry name in `Graphic` and the bank that holds it in
  `AssetID`; a weapon authors them as `explosionart` inside `explosiongaf`, and
  a half-authored pair leaves retail's holder null and draws nothing too. An
  entry published with no bank comes from the engine's own fixed effect-slot
  table, which is bound from `fx`. A missing effect therefore shows up as a
  `Skipped` count and no pixels `[06 R-WFX-01 §1]`. F11 client diagnostics
  expose effect and strip counters for the latest recording pass, explicitly
  including a joined speculative pass. Each recording resets the counters.
  Bank failures retain their load error and remain negatively cached; missing
  or empty entries retain their authored identity. The serial resolver keeps
  at most 64 distinct failures per source installation, with a truncation flag;
  repeated recordings neither append duplicates nor log from workers. The
  snapshot copies this data and also exposes the shared projectile-bank error.
* **`Backend.WarmUp` has no retail counterpart.** Ebitengine's audio context
  only becomes ready once the host device finishes an asynchronous open, which
  measured from tens of milliseconds to low seconds on a cold process. Without a
  warm-up that wait lands on whichever cue plays first — felt in play-testing as
  "the first click takes a while". `Run` therefore plays one silent frame at
  zero volume at the platform boundary, before the window is shown. It is
  platform work and cites no retail contract.
* **SC10 — the trailing `-Z` is projection, not a second conversion.** The
  screen helpers negate only the transient projected Z and then compute the
  `Z − Y/2` shear; they do not store that negation back into model data. Model
  space is mirrored in Z against world space, which is why
  `Client.worldObjectScreen` and `camera.WorldToScreen` are two projections and
  not one `[03 §2.4]` `[03 §2.5]`.
* **SC14 — the muzzle query reuses pristine post-load vectors.** The piece
  transform rotates and translates the already-converted piece and centre
  vectors; there is no second sign fixup at query time `[03 §2.4]`. Same
  evidence as SC10, separated because SC10 is about the projection shear and
  SC14 about per-vertex flare reuse.
* **SC15 — the cursor table is 0..21 with slot 10 `cursorrevive`.** The retail
  init sequence fills slot 10 last, after slot 21, so transcribing the sequence
  rather than the slot offsets shifted every index from `cursorreclamate` up by
  one. `internal/render/gaf_cursor.go` carries the corrected table;
  `cursorprotect` is deliberately absent because retail never resolves it
  `[07 §8]`.
* **`render.PrimitiveRGBA` diverges from `[03 R-REN-03A §5]`, and is test-only.**
  §5 enumerates four span writers — unshaded and shaded × flat and textured —
  and the *renderer*, not the face kind, selects between them, so a flat polygon
  under the shaded renderer goes through `SHD` exactly as a textured quad does.
  `PrimitiveRGBA` still routes every flat primitive to the raw palette whatever
  row it carries, implementing the unshaded flat writer only; of its three
  terms, only `ShadeRow == NoShadeRow` matches the real split. The divergence is
  confined to this helper and its tests: the production draw path is the
  client's model raster, which interpolates `PrimitiveDraw.ShadeRows` itself and
  never calls this function. It is recorded rather than silently fixed because
  the helper is a reusable primitive with pinned tests.

## 6. Research map

| Behaviour | Owning research |
|---|---|
| The presentation boundary, the ten strips, the barrier order | `[03 §1]` |
| Strip producer census, object families, terminal state, the sweep's CRT draws | `[03 R-STRIP-01 §1]`, `[03 R-STRIP-01 §2]`, `[03 R-STRIP-01 §3]` |
| The wreck-smoke trigger; strip 5 has no combat producer | `[03 R-LAYER §3]`, `[03 R-LAYER §4]` |
| Feature changes restamp the movement classes (world's, not presentation's) | `[03 R-LAYER §2]` |
| The strip object pool, the flame family, the smoke-puff family, the pool cap, the four producerless strips | `[03 R-FX-02 §1]`–`[03 R-FX-02 §5]` |
| The phase-11 strip sweep: removal verdict before update, the 400 cap | `[01 R-CORE-01 §4.4.1]` |
| Named GAF banks and slots; projectile hand-offs; strip families strip by strip; the flash and lens blitters; cursors; `damagebars` and the health bar; `NoShake` and the scratch surfaces | `[03 R-FX-01 §1]`–`[03 R-FX-01 §7]` |
| The group digit's default colour is entry 15 | `[03 R-FX-01 §6A]` |
| Coordinate units, the TNT tile pass, height queries | `[03 §2.1]`, `[03 §2.2]`, `[03 §2.3]` |
| Committed sampling with no interpolation; the 3DO hierarchy and transform composition | `[03 §2.4]` |
| Face dispatch, shading selection, the excluded authored selection primitive | `[03 §2.4.1]` |
| The orthographic projection and half-height shear | `[03 §2.5]` |
| The composition image, the height key, piece order, cached body and attached units, the four span writers, structure anti-aliasing, waterline and digger clipping, order of operations | `[03 R-REN-03A]` |
| The red/purple fringe is the downscale blending with palette index 1 | `[03 R-REN-03A §7]`, `[03 R-REN-02R]` |
| Model shadows: the gate, the rasterization, placement, the `ALP` tint, the cached sprite, the silhouette branches | `[03 R-REN-03D]` |
| The polygon raster exactly; the vertex pipeline's floors; team colour from `LOGOS`; shadow/waterline/option corrections; lighting is `SHD` only; the feature window; unit draw order and the Z-row buckets | `[03 R-RAST-01 §1]`–`[03 R-RAST-01 §7]` |
| Which units reach the shaded renderer at all, and stock reachability | `[03 R-RND-02A]` |
| The tile pass; the frame blitter family and raster primitives; the nanoframe outline; image scratch records; composer-side diagnostics | `[03 R-COMP-01 §1]`–`[03 R-COMP-01 §5]` |
| Render piece state, the animated-texture cursor registry, circle rasterizers, the unshaded-renderer entries, the composition cache and strip-pool growth | `[03 R-COMP-02 §3]`–`[03 R-COMP-02 §7]` |
| Visibility storage, sight shape, fog and unexplored edges | `[03 §3.1]`, `[03 §3.2]`, `[03 §3.3]` |
| Radar and sonar; the minimap surfaces and lifecycle; the picture build; the mapped composite; contacts; sensor circles; the lens; the ground-pick crosshair | `[03 §3.4]`, `[03 §3.6]`–`[03 §3.12]` |
| The minimap viewport rectangle, the sensor-circle colours, the blip and ring gates | `[03 R-MM-01 §1]`, `[03 R-MM-01 §2]`, `[03 R-MM-01 §3]` |
| The gray table's construction | `[03 §4.3.3 R-RR16-A §1]` |
| The GDI and DirectDraw backends, and the viewport subrect | `[03 §4.1]`, `[03 §4.2]` |
| Palette tables and colour indirection; `LHT` brightening; `SHD` shading | `[03 §4.3]`, `[03 §4.3.1]`, `[03 §4.3.2]`, `[fmt pal]` |
| GAF sprites, animation, and the playback cursor | `[03 §4.4]`, `[fmt gaf]` |
| The phase-7 model-texture cursor registry and what it is not | `[03 R-CRD-005 §1]` |
| Terrain and features: definitions, placement, staging, the screen anchor, visibility and fog interaction, the burning lifecycle | `[03 §5.1]`, `[03 §5.1.4]`, `[03 §5.1.5]` |
| Units and 3DO models; the nanoframe reveal and its sweep | `[03 §5.2]`, `[03 R-P0-19-N]` |
| Projected shadows and feature shadows | `[03 §5.3]` |
| Projectiles and laser beams; the presentation hand-offs | `[03 §5.4]`, `[03 R-FX-01 §2]` |
| Effects, flashes, nanolathe and cursors | `[03 §5.5]` |
| Screen shake — the driver is the runtime document's; the CRT contract is here | `[03 §5.6]` |
| The follow-camera producer census (the camera is the interface document's) | `[03 R-CRD-006 §2]` |
| Construction and water wakes; there is no wake rectangle; water, lava and the blue table | `[03 §5.7]`, `[03 R-WATER-01 §1]`, `[03 R-WATER-01 §2]` |
| Water, lava and media-dependent impacts | `[03 §6]` |
| FNT glyphs: the record, the width measurer, the string drawer, the rasterizer, which fonts, the GAF-font pen | `[03 §7.1]`, `[03 R-FONT-01 §1]`–`[03 R-FONT-01 §6]`, `[fmt fnt]` |
| Audio backend selection and the sound device | `[03 §8.1]`, `[03 R-AUD-01 §1]` |
| WAV decoding and the cache | `[03 §8.2]`, `[fmt wav]` |
| Sound categories and the eight-slot arbitration | `[03 §8.3]` |
| Audio preferences, the unit voice pick, random draws and the throttle | `[03 R-AUD-01 §2]`, `[03 R-AUD-01 §3]`, `[03 R-AUD-01 §6]` |
| The status-cue sink: codes are slots, and the publication-boundary rule | `[03 R-AUD-01 §7]` |
| Streamed narration and the reaper that runs from the pump | `[03 R-AUD-02 §1]`, `[03 R-AUD-02 §2]` |
| Music, CD/MCI, the disc categories and the intensity chooser | `[03 §8.4]`, `[03 R-AUD-01 §4]`, `[03 R-AUD-01 §5]` |
| The sound-category loader and the audio preference values | `[02 R-SND-01 §1]`, `[02 R-SND-01 §2]` |
| Selection geometry, palette and the composition boundary | `[03 R-SEL-02A]`, `[07 R-SEL-02A]` |
| The hover hull and the pick geometry | `[07 R-REV-01]`, `[07 R-SEL-02B2]` |
| Presentation keys, explosion selection, impact and fire sounds, render types, the presentation RNG census | `[06 R-WFX-01 §1]`–`[06 R-WFX-01 §4]`, `[06 R-WFX-01 §6]` |
| Smoke-puff parameters per producer | `[06 R-WFX-01 §5]` |
| The authored animation the feature phase reads through this edge | `[05 R-FEAT-01 §1]`, `[05 R-FEAT-01 §10]` |
| Nanolathe segments and their CRT draws | `[05 R-P0-06 §5]` |
| Cursors, the pointer, and the retail palette contract on the interface side | `[07 §7]`, `[07 §8]`, `[07 §9]`, `[07 §10]` |
| Window creation residue and input queue capacities | `[01 R-PLAT-02 §3]`, `[01 R-PLAT-01 §6]` |

`[03 R-WIND-01]` — which wind table feeds which axis — is cited by
[DESIGN_WORLD_VISIBILITY](DESIGN_WORLD_VISIBILITY.md); no draw in these
packages reads wind. `[03 R-CRD-005 §1]` is cited on both sides: the runtime
document owns the phase-7 slot, this document owns the registry it advances.

## 7. Not implemented and open

Marked in the source:

* `internal/client/selection_overlay.go` — `TODO(T23)`: `[03 §4.1]` lists a
  hidden-panel *expansion* of the viewport subrect to `(0, 0, W−1, H−1)` as a
  prediction with no writer behind it, and `[07 R-SEL-02A]` separately records a
  `(0, 32, W−1, H−33)` description of the same record. Both would move only the
  clip's left edge; the placeholder is the one with a traced writer, and a
  retail capture of the descriptor at the selection draw with the rail retracted
  is the only thing that settles it.
* `internal/audio/music.go` — `TODO(T23)`: the missing-CD failure chain and the
  20-entry per-disc history ring keyed by volume serial are Established and
  written up `[03 §8.4]` `[03 R-AUD-01 §4]`; they are platform residuals here
  because there is no MCI device and no registry, so a port that acquires a CD
  backend implements them from research rather than re-tracing them.
* `internal/session/strips.go` — `TODO(T23)`, attributed here because it is the
  strip-5 flame spawner and the family it feeds is drawn by this client: a span
  under five world units — including the degenerate `from == to` teleport —
  makes `segLife` zero, which is an integer divide retail itself faults on, so
  there is no retail behaviour to clone. The placeholder gives such a span the
  family's minimum life of one tick, so the segment lays, expires the tick after
  the spawn, and its container dies with it instead of going immortal; the
  start-frame CRT draw is spent either way `[03 R-FX-02 §2]`.

Open on doc 03's side, with no marker in these packages because the implemented
behaviour is bounded rather than guessed:

* The purpose of the nanolathe particle word set at spawn and read by neither
  the advance nor the draw `[03 §5.5]`.
* Whether the fog cache's `1 = NW` corner-to-bit assignment holds; supported
  inference today, and an asymmetric fog probe settles it `[03 §3.3]`
  `[03 §4.3.3 R-RR16-A §1]`.
* Edge behaviour for unexplored in-map void cells and whether the backbuffer
  retains stale bytes beyond the play rect `[03 §2.2]` `[03 §4.1]`.
* Radar picture row orientation, the bar fill colour, and the minimap marker
  blit site `[03 §3.7]` `[03 §3.9]`.
* The identical-model six-variant shading matrix predicted by `[03 R-RND-02A]`
  has not been run.
* Portable direct-raster treatment of a composite's runtime-relocated child
  storage. File bytes cannot determine it, so visibility masks reject such a
  frame and projectile model faces omit it `[02 R-MALF-01 §6]`.
* Remaining feature-mask and structure-texture GAF readers need individual
  traces; their ordinary-only compatibility raster is not a retail contract
  `[02 R-MALF-01 §6]`.
* The exact PCM conversion for legacy WAV variants beyond the DIGI and raw rules
  `[03 §8.2]`.

## On-demand diagnostic capture

This is a Nanolathe host diagnostic feature, not retail behavior. Ctrl+Shift+F11
consumes one input edge before gameplay dispatch, joins the background recorder,
pauses through the session scheduling bridge, and writes a new capture directory
outside the repository. Success and failure both leave the session paused. The
capture reads one authoritative boundary and labels the separately committed and
presented ticks; it never advances a tick to refresh the published pause flag.

The bundle contains a versioned per-file manifest, Go runtime memory statistics
and sampled profiles, full goroutine stacks, explicit owner-provided unit,
order/script, session and client state, and device diagnostics when available.
Large collections are streamed; live object graphs and retail assets are not
serialized wholesale. Partial failures remain visible in the manifest. No
forced GC or full raw heap dump occurs by default. Profiling and disk access
remain outside simulation phases and do not consume authoritative RNG.

The client/window interface is `Client.SetDebugDeviceCapture(func(string) error)`
and `Client.WriteDebugDeviceCapture(directory string) error`. The callback runs
synchronously on the window owner after the recorder is joined. It writes
renderer counters and a readback of the last presented image, without composing
another frame. An unavailable device writer returns an explicit error, and the
bundle keeps all other successful files. The runtime capture and each engine
snapshot carry their own collection time/identity where they cannot describe an
identical instant. A diagnostic bundle is not itself a restore/save format.

Implemented capture details and the explicit coverage limits live in
[DEBUG_CAPTURE.md](DEBUG_CAPTURE.md). The host writes synchronously under
`~/Nanolathe/diagnostics`, with one unique directory per shortcut edge. Initial
runtime statistics and profiles precede the detached engine snapshots. macOS
OS memory tools inspect only this process, with independent five-second limits.
The device owner copies the retained image through one `ReadPixels` call before
PNG encoding; the PNG encoder never reads pixels individually from the GPU.
Client counters include each parallel recording worker's private scratch.
Arena capacities describe retained storage; idle offsets are not frame peaks.
The adapter marks exact presented tick identity unavailable until an explicit
presentation stamp exists. The window composition root installs the device callback after constructing
the Ebitengine app; an absent callback is a partial capture.

## Cloaked body composition

The admitted unit's current `Cloaked` state selects the image commit in
`ClassicModel`; its copied body pixels remain ordinary palette indices.
Classic applies the loaded ALP table in source-major order after the shadow
[03 R-RAST-01 §7][03 R-REN-03D §4]. Visibility admission remains upstream;
foreign hidden units produce no silhouette. Decloaking changes the next commit
without invalidating the cached body. Keyed cargo joins the carrier before the
carrier's single blend; direct live polygons retain their ordinary fill.
