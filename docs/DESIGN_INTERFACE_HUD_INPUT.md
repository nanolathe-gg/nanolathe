# Design — Interface, HUD and input

`internal/gui`, `internal/input`, `internal/camera`, `internal/ui`,
`internal/hud`, and the screens and dispatch of `cmd/nanolathe`. The authored
`.GUI` window files and the control kinds they compile to, the front-end screen
graph and its panel stack, the battle HUD's anchors, rails, pages, footer and
overlays, the platform-neutral key and mouse vocabulary, the orthographic
camera and its minimap, the pointer and latch state machine that turns a click
into a command, and the one boundary at which a human gesture becomes
authoritative state.

This is one of the design documents listed by
[ARCHITECTURE.md](ARCHITECTURE.md); that document owns package boundaries, the
tick, and the citation routing that names this document as the home of
`[P0-I14]` — picking, selection and HUD commands — and, jointly with
DESIGN_PRESENTATION_CLIENT, of `[F-P1-008]`, defined in §3.5 below.

## 1. Purpose and boundary

These packages answer three questions, and nothing else:

* **What is on screen, and where?** One parser for the authored `.GUI` window
  file, one compiled `Window` per screen, one 30-entry side anchor block per
  battle, and one set of layout rules that extend the 640×480 design space to a
  larger surface without scaling it `[07 §4]` `[07 R-HUD-05]`.
* **What did the player just do?** One host-frame input sample, one pointer and
  latch state machine that owns a whole press/release gesture, one keyboard
  table whose every row is a traced retail token, and one camera that scrolls,
  jumps, follows and converts between screen, world and minimap space
  `[07 R-CAM-01 §1]` `[07 R-CAM-01 §2]`.
* **What does the session hear about it?** One typed input queue:
  `Session.EnqueueHumanCommand` (or its receipt-returning form
  `EnqueueHumanCommandWithSequence`), taking a value that carries handles and
  numbers and no pointers. Everything above is presentation; nothing above
  writes a unit, an order or a resource `[07 §9]` [I6].

The single most dangerous mistake here is to let the interface *decide*
something the simulation owns. The HUD computes no build legality, no order
result, no selection membership; it proposes, and the command drain in the
tick's first phase disposes. A page number, a group recall, a stance cycle and
a build placement all cross the same boundary as data. Presentation state that
looks authoritative — the armed latch, the drag rectangle, the camera origin,
the panel offset — is never read back by a simulation phase, and the parity
guards in `internal/architecture` enforce that the packages here reach no
random stream and no clock of their own.

The boundary runs at six places:

* **The tick belongs to `internal/session`.** Commands are queued at any point
  of a host frame and drained in phase 1 of the next sub-tick, in enqueue
  order, each stamped with the sequence it was accepted at and the tick it is
  due `[01 §4.4]` `[08 "Soft pacing — no per-tick input barrier"]`. There is no
  input-delay constant. A pause is the one exception: it is applied
  synchronously at the scheduling boundary, because a paused session publishes
  no newer tick for the UI to observe the transition on `[01 §4.3]` `[07 §11]`.
* **Pixels belong to DESIGN_PRESENTATION_CLIENT.** `internal/hud` and
  `internal/gui` hold geometry, state and verdicts; `internal/client` and
  `internal/render` turn them into bytes in the indexed framebuffer, resolve
  GAF art and palette entries, and draw the software cursor. Where this
  document names a colour or a font it is naming the *choice*, not the blit
  `[07 §6]` `[03 R-FONT-01 §6]`.
* **Authored bytes belong to `formats` and `internal/content`.** `internal/gui`
  compiles a parsed `.GUI` section list into control records; it never invents
  a rectangle, a frame, a default or a fallback geometry `[fmt gui]`
  `[02 §6]`. The 30 side anchors are stored verbatim as authored corners
  `[02 §6]`.
* **The world point belongs to `internal/world`.** Resolving a pointer to
  ground is `Terrain.CursorToWorld`'s bounded search along Z, not the algebraic
  inverse of the projection (SC20) `[07 §8]`. `Camera.ScreenToWorld` keeps its
  pixel-level meaning and is not a ground-order conversion.
* **The order vocabulary belongs to `internal/orders`.** The latch byte is
  literally the order dispatcher's switch key; this side chooses the key and
  never the handler `[07 §9]`.
* **Nothing here draws from a random stream.** Menu and cursor animation are
  driven by the renderer's delta; the front end has no simulation clock at all
  [I4].

## 2. Packages, files and key types

### 2.1 `internal/gui` — the authored window

**The record** (`types.go`). `Kind` is the stored control-type byte. The
builder's switch has fourteen arms indexed `0..13` after an unsigned `> 13`
bounds test; kinds 6, 9 and 10 and every value above 13 do no build work, and
their records are still parsed and still serviced `[07 R-WGT-01 §12]`. Named
kinds are panel `0`, button `1`, listbox `2`, text box `3`, scrollbar/slider
`4`, label `5`, surface `6`, font `7`, raw file `8`, line `10`, panel alias
`11`, picture `12`, score bar `13`; kind 9 has an id and no traced behaviour,
so it gets no name `[07 R-WGT-01 §11]`. `RuntimeFamily` maps the stored kind to
its runtime dispatch family `[07 §4]`. `Gadget` carries the common header —
name, association, rectangle, the 32-bit attribute word, the two colour fields,
texture and font numbers, the active byte, help text — plus the per-kind fields
each arm reads. `Rect` keeps the authored `xpos`/`ypos` alongside the placed
position, so the `-1` centre and `-2` far-edge sentinels stay auditable
`[02 §6]`. `Window` is one `COMMON` block plus its gadgets and the focus index;
`PlacedRect` resolves a gadget's local rectangle against the window origin.
`ArtSources` is the resolution order a painter walks: the gadget's own named
GAF entry, then the side-specific interface GAF, then the built-in fallback
`[07 §4]`.

**The loader** (`load.go`). `Load` parses one logical path into a `Window`.
`HitTest` is inclusive on both edges and runs after runtime placement; hidden
and greyed controls reject `[07 §3]` `[07 R-WGT-01 §13]`. `Fires` reports
whether a gadget's kind produces a callback at all. `BuildSlider` is the
kind-4 synthesis: a scrollbar or slider gadget expands into the bar plus two
arrow children whose art is frames `base+6` and `base+8` of the `SLIDERS`
entry, with the knob travel derived rather than trusted `[07 R-WGT-01 §5]`.
`SliderFrameBase` is that base choice. A list-associated scrollbar takes its
range, knob size and position from the list `[07 §4]`.

**Association** (`dispatch.go`). `Name16Equal` is the fixed-stride 16-byte name
comparison the association-capable family uses to find its peers `[07 §4]`.

### 2.2 `internal/input` — the vocabulary

**Keys and buttons** (`keys.go`). `Key` names the letters, digits, function
keys, arrows, editing keys, modifiers and the four punctuation keys the battle
dispatcher has cases for: `-`/`=` for game speed, backquote for the label bit,
and `,`/`.` for build-page paging `[07 §2]` `[07 R-CAM-01 §2]`. `MouseButton`
is left, middle, right. Nothing here names a platform virtual key.

**The latch** (`latch.go`). `Latch` is the armed-order byte, and its values
*are* the order dispatcher's switch keys: normal `1`, MOVE `2`, ATTACK `3`,
BLAST `4`, UNLOAD `5`, PICKUP/LOAD `6`, FOLLOW/GUARD/DEFEND `7`,
REPAIR/HELPBUILD `8`, PATROL `9`, TELEPORT `0xB`, RECLAIM/RESURRECT `0xC`,
CAPTURE `0xD`, MOBILEBUILD `0xE`; `0xA` is unused `[07 §9]`. `IsValid` is the
gate that keeps a malformed gadget name from leaking an arbitrary index into
the command adapter.

**The sample** (`state.go`). `State` is the one canonical host-frame value the
client edge fills: a `MouseState` with per-button held, pressed-edge and
released-edge arrays plus wheel and moved bits, and a `KeyboardState` with held
and edge arrays. `Sample` is the flattened copy taken before dispatch, so no
mutable client model is shared with command handling [I6].
`SampleFromState` clamps the pointer to the **live** surface at `W−1`/`H−1`,
not to the authored 640×480: at a larger display mode the chrome extends by
rule rather than scaling, and clamping to the design space made every point
past `(639, 479)` unreachable `[07 §1]` `[07 R-HUD-05]`. `SurfaceWidth` and
`SurfaceHeight` are the authored design space and are the fallback for a caller
that does not yet know the negotiated surface, never a clamp applied to a
larger one.

**I02 pointer-record queue contract.** `PointerEvent` is the semantic button
record: its kind is one of the already classified left/right down,
double-click, and up messages; it retains the event coordinates, modifier
snapshot, event-time `Buttons`, and caller-supplied scaled host `Timestamp`.
The button snapshot belongs to the record even when later live button state
has advanced; alternate drag consumes that record state [07 R-CAM-01 §11]. `PointerRing` has 20
records with one reserved slot, so `Enqueue`, `Dequeue`, `Len`, and `Flush`
operate on at most 19 pending button records. `Enqueue` refuses a full ring
without changing either index. It is not a motion queue.

`State.EnqueuePointer(PointerEvent) bool` updates the live pointer/button
sample and attempts to retain that button record. Its result says whether the
record entered the ring; it does not report the live-state update. An invalid
button kind is rejected before changing either live state or queue state. The caller
uses `State.UpdatePointerMotion(PointerEvent)` for a motion record: it updates
the live pointer sample and replaces the one latest-motion fallback, without
adding a button record. `State.PopPointer() (PointerEvent, bool)` is called
once for a host service: it returns the oldest queued button record with
`true`, or the latest motion record with `false` when the button ring is empty.
`State.FlushPointers()` discards queued button records. Once per host service,
the producer calls `State.PublishPointer()`: it consumes exactly one oldest
button record, or the latest motion fallback when no button record is pending,
and caches that canonical result. `State.CurrentPointer() (PointerEvent, bool)`
is the read-only fetch every widget and battle consumer shares for that service;
it never consumes another record. `SampleFromState` carries the cached value as
`Sample.Pointer` plus `Sample.PointerValid`, and `StateFromSample` restores only
that already-published record. A manually built `Sample` that leaves
`PointerValid` clear remains the legacy live-edge form and does not claim native
history. The event modifier snapshot is never reconstructed from later live key
state. The existing `MouseState.SetButton`/`Sample` edge APIs remain live-state
samples and do not claim to reconstruct native event history; producers must
call the pointer methods above to retain a same-interval down/up pair.

The platform boundary owns native message history, timestamp scaling,
double-click recognition and key repeat. The current Ebiten producer polls all
modifier and button states before it constructs one snapshot, retains observed
left/right transitions without representing their retention order as native
chronology, updates the motion fallback, and publishes once before
`Client.Step`. Its timestamp is the existing scaled 30-Hz host-clock value.
Polling does not recover native ordering among changes that arrived between
polls, nor native key repeat/history.
`TODO(T25): establish an Ebiten event source that exposes native ordering and
repeat.` [07 §2] [01 R-PLAT-01 §6]

**Held editing keys repeat.** Retail enqueued one token per operating-system
repeat of a held key, and the battle frame drains one token per host frame, so
a held key repeated at the OS rate bounded by the frame rate `[07 §2]`
`[07 R-CAM-01 §1]`. Ebiten's character batch already carries the repeats of
printable characters, but it reports the non-printing keys only as held state.
`internal/platform/ebitenapp/keyrepeat.go` therefore re-issues the token of a
held Backspace, Delete, arrow, Home, End, Page Up or Page Down key after the
host delay and then at most once per 30 Hz service, translated with the
modifiers of that service. The delay and rate are host policy (§5). The toggle
keys — Enter, Escape, Tab, Insert (paste), Pause, the function keys and the
Ctrl/Alt compositions — keep one token per press; retail would have repeated
them too, and that remains part of the T25 gap. Repeat state belongs to one
keyboard state and never reaches the simulation except through the tokens an
ordinary press would also produce.

The producer reconstructs **left and right double-clicks**,
because its inputs are host settings rather than retail behaviour: retail read
the operating system's double-click message, whose interval and rectangle were
the user's OS settings. `internal/platform/ebitenapp/doubleclick.go` owns that
policy and both constants; §5 records the values and the rationale. The second
press of a pair carries `LeftDoubleClick` or `RightDoubleClick` **instead of**
the corresponding plain down event, the way
the operating system replaced the second press message — one observed
transition stays one record, publication still takes a single record per host
service, and the widget pass acts on a left double-click as a press that also
fires `[07 R-WGT-01 §4]`. Each button retains its own candidate; interleaved
left and right presses never pair with one another. The optional Community
selection consumer uses either double-click identity (§3.13).

`cmd/nanolathe` uses one `pointerFrame` adapter whenever it services a
`ui.Panel`. It projects the already-published record into the frame and passes
that exact one event through, including double-click kind, modifier snapshot,
button snapshot, coordinates, and timestamp. Command, cursor, hover, and
pressed-art consumers use the same published projection. A manually constructed
sample without `PointerValid` keeps the legacy live-edge fallback; it does not
claim native message history. Middle-button camera drag, wheel, and keyboard
held queries remain live samples [07 §2] [07 R-WGT-01 §1].

### 2.3 `internal/camera` — origin, scroll, zoom, minimap

**The camera** (`camera.go`). `Camera` is the integer orthographic camera:
origin `X, Z` in map pixels, view size, map extents, the presentation `Scale`
of §3.5, and the follow block. `OriginX, OriginY` are the battle viewport's
insets `(128, 32)`; this build's origin is the world point drawn at the
**framebuffer's** top-left corner, while retail's is the world point at the
**viewport's** top-left corner, so every bound converts by the leading inset —
`clampAxis` takes `minimum = −leading` and
`maximum = mapSize − viewportSpan − leading`, with the floor test before the
maximum test exactly as retail orders them `[07 §10]` `[07 R-CAM-01 §13]`
`[03 §4.1]`. `BattleView` is the one definition of the visible span;
`clampInsets` is the one definition of the insets. `Scroll` is the scroll
pass's magnitude arithmetic only: `magnitude = setting × rawDelta` capped at
128, a signed comparison with no absolute value, zero delta meaning no
movement, where `rawDelta` is thirtieths of a second and not milliseconds
`[07 §10]`. The pass's *shape* — one exclusive direction test per axis (Left
before Right, Up before Down), a single origin commit after both axes, and the
jointly gated beyond-edge forced strip — lives at the caller in
`cmd/nanolathe` with host-coordinate adaptation in `internal/client`, and is C2's contract `[07 §10]` `[07 R-CRD-006 §1]`; the
caller's extra focus/modal gating and host-coordinate adaptation of the edge
disjuncts are presentation policy (C2). `Pan`, `Clamp`, `JumpTo`,
`BattleViewCenterOrigin` and `JumpToBattleViewCenter` are the jump family; a
jump writes the origin, clamps, and copies the clamped result into the desired
origin so no glide survives it `[07 R-CAM-01 §12]`. `WorldToScreen` applies the
half-height shear `wz − (wy >> 1)` with arithmetic shifts; `ScreenToWorld`
inverts it at ground height for pixel-level questions only.

Battle installation applies the entry jump after adopting the client's actual
surface dimensions. Detached preparation uses the authored 640×480 size, but
its origin is provisional: fresh missions must center their authored start
position in the installed battle viewport, and skirmishes center their local
commander there. A loaded battle instead reapplies the original saved origin
and clamps it against the installed viewport; it never takes the fresh-entry
center. Both current and desired origins are written by the jump. Later
display changes use ordinary resizing, without replaying battle entry
`[07 "The loading screen"]` `[07 R-CAM-01 §12–§14]`.

**I03 command-palette service (implemented).** Ordinary
ARMOPT/EXITMENU/YESORNO/RESTART/ENDMSN children and each command-window
identity use retained `Panel.ServiceFrame` state with zero token mode. The
command palette resolves its current window, including the selection-open
flush, before it peeks the original client token ring; it then runs before the
controller turns input into a value sample. Its dynamic hidden/grey/capability
verdict updates the shared service for that pass, and its indexed fired result
is the sole callback input for both pointer and quickkey actions. A topmost
UNITINFO child performs its own peek first and prevents service of the page
underneath. Eligible quickkeys consume only their claimed prefix; the key
matrix remains disabled. Pointer capture and staged mutation are therefore
shared with the release path, so a held button cannot also fire from its
quickkey. YESORNO's explicit Enter/Escape caller row routes to No independently
of the generic matrix [07 R-WGT-01 §§1-3,6-7] [07 R-WGT-02 §5]
[07 R-FE-01 §7]. Panels are built at the owning transition before their first
input or draw.

**The Ctrl-right drag-scroll primitive** (`drag_scroll.go`).
`camera.DragScroll.Begin(*Camera)` captures `trunc(origin / 16)` and clears the
tracked follow and its pending host sample once, because pointer dispatch
precedes phase 10. Entry preserves the desired origin and unfinished glide,
so runnable sub-ticks in that frame can continue it; paused and zero-tick
frames do not move it. `Step(*Camera, dx, dy)` writes each origin as
`(trunc(delta / 4) + anchor) × 16`, clamps it, copies current into desired, and
updates the anchor and pending host sample; it does not clear follow during
later steps. The first later step therefore uses the entry-captured anchor,
regardless of intervening glide movement. Its battle adapter supplies
successive pointer deltas and does not defer a second glide cancellation.
A later hotkey retains its ordinary post-batch request ownership. The camera
primitive quantizes the retail beam origin through `BattleViewOrigin`, never
framebuffer `X`/`Z`,
because the two coordinate frames differ by the viewport inset `[07 R-CAM-01 §11]` `[03 §4.1]`.

Small signed displacements are discarded on each input service; there is no
residual accumulator. The ordinary desktop adapter currently services input
at its configured 30 Updates per second. Display refresh can cause additional
Draw calls, but those calls do not independently poll or step drag input;
modern deferred updates still consume each scheduled Update once. Slow motion
below four pixels per serviced sample can therefore leave the camera still,
including while paused. That quantization is the established retail rule;
neither a residual accumulator nor a simulation-tick throttle is implied by
it `[07 R-CAM-01 §11]`.

`battleSession` admits this capture only for an idle Type-0 Ctrl-right event
inside the world view. It spends motion before release and suppresses the
ordinary camera scroll branch during that frame. `Client.SetPointerCaptured`
is presentation-only; Ebitengine captured mode supplies cumulative virtual
pointer positions, and its desktop adapter restores the native capture-start
position on release. The software cursor is hidden while captured. T25 remains
explicit: host polling cannot reproduce the historical native event's restore
point or warp chronology; focus loss and screen ownership changes release
host capture. The projectile-hold camera owner is still absent, so entry clears
tracked-unit/glide state but has no projectile hold state to clear; the pending
owner connection remains at the code site.

**Follow and bookmarks** (`follow.go`, `bookmarks.go`). `FollowState` is the
rest of the retail camera block: the desired origin, the tracked object handle
and four bookmark slots. `DesiredOrigin` and `FollowTo` step the origin toward
the target; `GlideTo`/`StepGlide` are the message-source and next-unit glides;
`SetTracked`/`ClearFollow` are the tracked-object writers, each named by the
jump family's table; `StoreBookmark`/`RecallBookmark` are Ctrl+F5..F8 and
F5..F8 `[07 R-CAM-01 §12]` `[07 R-CAM-01 §14]`. The `n` and F3 glide
writers preserve the tracked object; its next follow pass can replace the
desired origin written by the glide `[07 R-CAM-01 §12]`.

The `t`/`T` input adapter scans selected slots relative to the tracked slot,
even when that unit has been deselected. Its inclusive owner range comes
from the committed player row's `UnitSlotStart` and strip `UnitLimit`, so
player permutation does not change the owner boundary. Null or out-of-range
tracking starts at the first actual slot; forward search starts strictly
after it and reaches it only on wrap. Backward search wraps from that first
slot to the range's end. An empty selection yields null; an absent snapshot
or range leaves tracking untouched `[07 R-CAM-01 §12]` `[I6]`.

**Shake and its return** (`follow.go`). `Camera.Shake` finishes each completed
sub-tick's camera pass after the follow step: it adds that sub-tick's share of
the session's published shake offset to the current origin and ends with the
camera clamp, as retail's phase 10 does on every pass. Retail's shake never
writes the desired origin, and its current-to-desired step runs on every pass
whether or not a follow point is selected, so a shaken camera settles back to
within the half-step's one-pixel stall of where it was `[07 §10]`
`[07 R-CAM-01 §10]` `[03 §5.6]`. This build keeps no live desired origin for an
idle, untracked camera — the scroll pass and the other host writers move the
current origin alone — so when the first jolt arrives with nothing tracked and
no glide in flight, `Shake` records the pre-jolt origin as the return target
and steps back toward it on later passes at the same bounded half-step. The
target persists between shakes, so the stall never becomes the next shake's
start and nothing creeps per jolt. A tracked follow or a glide in flight owns
the desired origin instead and damps the jitter toward its own target. Any
host writer that moves the view between passes — scroll, middle or Ctrl-right
drag, trackpad pan, minimap or megamap jump, bookmark recall, zoom — ends the
return where it put the view, which is what retail's jump writers do by
copying the current origin into the desired origin `[07 R-CAM-01 §12]`; the
zoom and the non-retail pans follow the same rule so the return never pulls
the view back toward where the player moved it from. It is presentation state
only; the session's shake draws and published offsets are unchanged `[I6]`.

**The minimap** (`minimap.go`). `LayoutMinimap` is the letterbox: the longer map
dimension occupies `MinimapLongSide = 126` pixels, the other is scaled by
integer division, and the unused axis is centred by truncating the half
padding. `world.PlayInsets` derives the playable extents the radar lens is
built on, and map loading writes them onto the terrain `[03 §3.4]`.
`WorldToRadar` and `RadarToWorld` are the lens conversions, `WorldToRadarWithY` the one that carries the height term
`[03 §3.9]` `[03 §3.11]`. `ToWorldPlay` is the pointer conversion the click
path uses: `worldX = (ptrX − padX) · PlayRight / RadarW`, a signed multiply and
a truncating divide with no half-viewport term `[07 R-CAM-01 §11]`.

### 2.4 `internal/ui` — screen-level state

**I06 common widget service contract.** `input.PointerEventKind` carries only
the six already-classified left/right down, double-click and up messages;
platform event history and double-click timing remain I02. `ui.WidgetFrame`
is one host-pass sample (`PointerX`/`PointerY`, held bits, ordered pointer
events, ordered tokens and caller-provided `TimerAdvanced`). `Panel.ServiceFrame`
visits runtime records in increasing index order, retains one indexed capture
and button bit, freezes held samples outside the window, runs the gated key
matrix before the gadget walk and services a captured editor at its own
indexed visit when the matrix leaves its token available, updates
hover/`HELPTEXT`, invokes each reached
surface hook, and tests the surviving fired result after each record. A
matrix result still allows record 1's ordinary visit; a hidden record 1
stops the walk without advancing to the next active control. A pointer result
can replace the pending matrix result, while a rejected label link clears it.
Focus follows the surviving result. `WidgetHooks.Change`
is synchronous, while the screen consumes `ServiceResult.FiredIndex` only
after the pass returns. `WidgetHooks.ArtFrames` returns the resolved button
entry's frame count after named, common and fallback lookup; it is the modulo
for an attribute-`0x100` down-state cycle, never the authored stage count. The
panel exposes indexed down/stage/cycle, slider knob, list top/max-top, hover,
dirty and capture state for painters and later keyboard work. `StatusAt` is the
down-state word and `StageAt` is the independent current-stage byte;
`SetStageAt` restores a released staged selection without setting it down.
Buttons use their documented radio/toggle/bit-8/cycle/plain and staged paths;
slider dragging and track paging are unthrottled; timed button
repeat and list edge scrolling advance only when `TimerAdvanced` is true. The
adapters sample the existing monotonic millisecond source through
`clock.ScaledNow`, retaining one panel-local prior stamp; battle uses its
existing source and menus retain the shell source. Record-list variable row
payload beyond its known height remains `TODO(question)` in the service
boundary [07 R-WGT-01 §§1-8] [07 R-WGT-02 §2] [07 R-FE-02 §4].

The host window builder resolves each button's own/common/fallback art before
calling `ui.NewPanel` and installs the effective `Gadget` record there:
effective stages and attributes, the selected art entry identity and base
frame, and the base frame's geometry. The authored lookup name remains intact.
Service, painting and captions read that installed record; `Panel.StageAt`
alone carries the mutable current stage [07 R-WGT-01 §3].

**I13 text-list contract.** `Panel.FillTextListAt` copies text rows and
optional raw row flags, enables row selection, resets selection/top and
computes the screen-owned `maxTop`; `SetListTopAt` stores any supplied bound.
The reverse walk begins at `count − 1`, subtracts the normalized row height
from the gadget height, and retains a candidate only while the remainder is
non-negative. Filling an active overflowing list activates its associated
scrollbar and synthesized arrows and resets the scrollbar knob. Service,
drawing and scrollbar movement read `ListMaxTopAt` rather than deriving or
mutating a second bound. A row flag of exactly `1` is a heading without removing an
ampersand from its text; `&G` retains its existing heading form. A row height
at or below the metric-plus-one floor becomes that floor. `drawRetailList`
uses the selected font metric and painter stopping predicate; tall rows are
strictly taller than metric plus six and use space/CR wrapping, with equality
fitting, a metric-plus-two line step and an `H−1` list budget. The click/scroll
row count is not the painter's row limit. Attribute `0x100` suppresses
selection brightening. Record-list payload beyond the known height path and
unaligned pen scratch remain unresolved [07 R-WGT-01 §4].

**The panel** (`panel.go`). `Panel` is one authored window's mutable state:
per-record active, status, text, help and list state; named operations find the
first exact 16-byte name match after the window header `[07 R-FE-02 §5]`.
Indexed painters, editor capture and list associations preserve duplicate
record identity. The remaining state includes the focus index, the left and
right press latches, the list models, and the scrollbar drag capture. `List`
holds items, selection and scroll top. `PanelStack` is the window chain with
its retail shape: `Replace` swaps the screen, `Push` is a save-under open,
`PushModal`/`Modal`/`CloseModal` are the message layer, `Under` is the panel a
modal sits over — the one input and drawing still apply to when the modal
closes `[07 §3]` `[07 R-WGT-02 §2]`. `Press`/`Release`/`ReleaseAction` are
release-inside activation: a press captures a gadget, and a release activates
only when the same gadget is still under the pointer `[07 R-WGT-01 §1]`.
`FlashRow`/`SetFlashRow`/`DecayFlash` are the row highlight decay. `HitTest`
and `PressTest` are the two hit shapes; a greyed gadget returns before its own
hit test and never captures `[07 R-WGT-01 §13]`.

The compiler and panel share `gui.Window.GadgetIndex` for exact names.
Enter/Escape default resolution uses that same lookup for a nonempty authored
default; an empty default takes the separate case-insensitive prefix scan,
without trimming or filtering inactive records `[07 R-FE-01 §12]`.
I16 remains open for screen callback comparisons and options slider identity;
I04 owns the complete key matrix and default-versus-focus admission order.

**I04 bounded Enter/Space selection contract.**
`Panel.DefaultKeyAction(enter bool) Action` in `internal/ui` replaces the
old pointer-predicate `Panel.Activate` keyboard surrogate: `enter=true`
selects the usable Enter default first and otherwise applies the focused
Space-kind rule; `enter=false` applies only that focused rule. The result keeps
the selected record index and exact authored name. Missing/inactive records
and a greyed button produce no target; default admission does not borrow the
pointer-kind or surface-hotness predicate [07 R-WGT-01 §2]. A captured text
editor retains these tokens through the existing editor path. Selection does
not mutate button stages or radio state; those state machines remain I06.

The frontend and active in-battle options handlers both consume this selector
through `activateDefaultKey`, which installs the selected index as focus
(including text-editor setup) before the name callback [07 R-WGT-01 §1 step 8].
It performs no second first-name lookup. Ordinary
battle children keep their current zero-token path. Preserve Escape, ordered
editor service and existing callback ownership. This bounded correction does
not implement traversal, navigation lifetimes, token-history ordering or the
remaining per-kind directional matrix; those stay explicitly open under I04.
Production tests must distinguish focus from default, Enter from Space,
unusable defaults, grey-bit polarity, indexed duplicates and excluded focused
kinds in both callers. A helper tested without the two live callers is not
accepted.

**I05 bounded button accelerator admission (implemented; remaining service open).**
The runtime builder runs per open after dynamic gadget append and before panel
construction. Its process-lifetime preclear starts enabled, clears button keys
before each build, and is permanently disabled only by the first successful
loading-to-battle hand-off; a failed load and a later frontend return do not
reset it. `gui` supplies the pure preclear and per-record assignment helpers:
buttons preserve on `0x10000`, staged buttons clear, empty ordinary captions
retain, and linked labels clear and search while unlinked labels remain
unchanged. Caption scanning stops at NUL, skips only space, compares every
current button and label key including later/inactive records with ASCII-only
`A`..`Z` folding, and stores the first free original byte. The odd low bit of
`gaffile` and `0x1800` arrows bypass the ordinary button arm; an odd `gaffile`
uses `anims/<name>_gadget.GAF` and retains that resolved entry identity,
including a resolved absence. Button flash state is reset before either gate
[07 R-WGT-01 §3] [07 R-WGT-01 §7]. [07 R-WGT-02 §2] establishes that quickkey
service stays enabled throughout supported single-player scope, so a new
mutable enable flag is unnecessary here. I03 owns ordered token service; the
shared I06 toggle/radio mutation path is implemented.

Battle HUD entry loads and builds the side-prefix `MAIN2.GUI` root before
the first successful transition disables startup preclear; later entries
retain that process-lifetime state.
Its resolved header panel is retained for the empty-selection painter
`[07 §6]` `[07 R-WGT-01 §3]`.
Actual command, options, confirmation and result windows build at their runtime
open, after the transition. The options caller relabels the already-built
MISSION caption, retaining the originally assigned accelerator. Results
population establishes its panel before the first input pass. Generated
product slots set the per-record GAF bit so the ordinary builder resolves the
product image; the generic external prepass does not replace a nonbutton
kind's own image slot `[07 R-WGT-01 §3]` `[07 §9]` `[08 R-CAMP-01 §8]`.

The builder consumes the caption already held in `Gadget.Text`. Retail
localizes kinds 1, 3, 4 and 5 while parsing a GUI, before this assignment pass.
The modeled startup loads `gamedata/translate.tdf` with literal lowercase
`english`, including an English-table override, and applies it at that parser
boundary. This scope is GUI captions only. `TODO(T25)`: integrate the traced
host command-line/registry selection for a non-default language; do not add a
local case-map or language flag at this seam [07 R-WGT-01 §3][07 R-WGT-01
§11][02 §3].

`Panel.ButtonQuickKeyAction(index, capture int, alt bool) Action` checks a
matching button in `internal/ui`, retaining its indexed identity. `capture`
is the current owning pump's record index, or -1. It rejects inactive records,
non-buttons, absent keys, low-bit grey, the captured button itself, and a
captured text editor without Alt. Another non-text capture does not reject a
button [07 R-WGT-01 §3]. This distinction was checked against both the handler
and its outer service pass before implementation.

The frontend and battle-options handlers share `activateButtonQuickKey`,
which walks authored indexes, performs their existing key matching, and sets
the fired index as focus before the unchanged screen callback. The options
pump passes its existing pointer owner rather than installing a second owner
in Panel. Tests use both production input handlers, low-bit versus upper-bit
grey, same-button versus other capture, text capture with/without Alt, changed
keys and duplicate records. I03 supplies the command-page equivalent through
the same service and callback identity. This unit preserves existing
callback-owned preference mutations and cues.

**I12 shared button painter (implemented).**
Frontend windows, shell-owned battle options and MSGBOX share the same button
painter. Missing art uses the shared ordered eight-run bevel with the exact
up/down/low-grey-bit colour triples [07 R-FE-02 §4]. Its down word comes from
the indexed widget service. The caption is a single-line button pen whose
position depends on stage count, never held-pointer state; build-key colour
and centred-key underline follow [03 R-FONT-01 §6][07 R-WGT-01 §3]. Authored
raster fixtures exercise missing art, upper grey bits, stationary pressed text,
case-insensitive first-byte key decoration and the grey build-key exception.
Battle modal and command-page painters use the same frame, bevel and caption
rules. Modal grey art shades the installed rectangle within its private
surface. The sidebar reads the retained input panel's down, stage and flash
state; painting never creates a panel or reconstructs a second pointer latch.
FNT glyph commands carry the private-surface clip into both renderer sinks.

**I04 focus traversal API and lifecycle contract (implemented).**
`internal/ui` owns `FocusDirection` (`FocusForward`, `FocusBackward`,
`FocusUp`, `FocusDown`) and `Panel.MoveFocus(direction FocusDirection) bool`.
The method applies [07 R-WGT-01 §2] focus order to indexed runtime activity,
releases capture and installs the selected record with text-editor setup.
Its boolean reports an attempted supported traversal, including an unchanged
winner; nil windows, absent focus, invalid direction and the unresolved
more-than-49-control case return false without mutation. The latter retains
an explicit `TODO(question)` and the existing research Unknown; this is a
bounded host fallback, not a claim about retail temporary memory.

`NewPanel` invokes forward traversal from header index 0 when authored
`defaultfocus` is empty, after initializing runtime gadget state. A nonempty
field retains the compiler's exact lookup, including its missing-name result.
`SetActiveAt` invokes forward traversal when disabling the focused record.
These production lifecycle consumers are the acceptance scope of this unit;
keyboard token dispatch and navigation-enable lifetimes remain separate I04
work. Tests must exercise actual opening/disabling as well as strict donor,
wrap, tie, admission, capture and text-setup contracts. Existing authored
fixtures that relied on unspecified default focus should state their intended
focus explicitly rather than weakening production opening behavior.

**I04 keyboard matrix and ordered-token service (implemented; explicit platform
limits remain).**
`WidgetFrame.Tokens` is the producer-ordered keyboard-ring snapshot and
`ServiceResult.ConsumedTokens` identifies only the serviced prefix. A
consuming, navigation-enabled front-end or battle-options root passes Tab,
Enter, Escape, Space and arrow tokens through the matrix before pointer
gadgets, regardless of an editor capture. Tab uses `MoveFocus` and held Shift;
Enter selects the usable
`crdefault` then the restricted focused fallback; Escape uses an active
`escdefault`; Space is limited to button, listbox and surface. Horizontal
sliders step and clamp through their ordinary knob/change path, while a focused
list changes its selected row without wrapping and computes keyboard rows from
`metric + 1`, independently of authored `itemheight`. It changes `top` by one
row only and clamps against the screen-provided `maxTop`; keyboard service does
not recalculate that list-owner value. Other directional keys traverse from the
focused control. An unusable Enter default takes the Space fallback, including
radio/staged mutation; a usable default only fires. Every firing matrix row
returns the indexed record through `ServiceResult`, with `StageAdvanced` set
only by the gesture that actually changed a staged value, so callbacks preserve
duplicate identity and slider write ownership `[07 R-WGT-01 §§1,2,4-6,8]`
`[07 R-FE-01 §12]`.

The two consuming callers and `MSNBRIEF` pass the result directly to their
existing callback paths. Residual button and linked-label accelerators are
checked during each indexed gadget visit, independently of the matrix gates;
they can mutate radio/toggle state and claim a peeked battle-child token. The
UNITINFO child performs that peek pass before battle hotkeys, but remains
outside Enter/Escape defaults. Screen/window open paths flush the token ring;
pointer and held-key state survive `[07 R-WGT-01 §§1-3,7]` `[07 R-WGT-02 §5]`.
`flushWindowTokens` is called at save/load, message, preferences-page,
information and results opens. Battle child cancellation preserves the surviving
options window's input; it is a close, not an open. Command-window identity and
selection changes mark actual opens, while cached draw/input lookups preserve
fresh tokens `[07 §6]` `[07 R-HUD-04 §3]`.
A captured editor then drains its available prefix only when the index walk
reaches that text record, so an earlier indexed gadget fire wins the pass.
Left/Right leave a focused text input token for that editor; Enter does
likewise, while an active `escdefault` consumes Escape before the editor can
see it.

The navigation-enable word is initialized enabled for the traced shell,
options and briefing adapters. `TODO(question): complete the per-screen
transition census for every explicit enable/disable write; current code must
not be read as a universal navigation lifetime.` Ebiten does not provide native
event chronology between its text batch and physical navigation edges. The
adapter retains each observation after the producer queue, but does not invent
a native order among simultaneous physical edges `TODO(T25)`
`[07 R-WGT-01 §2]`. Held editing and cursor keys repeat at the host cadence of
§2.2 and §5; the widgets see those repeats as ordinary tokens.

Quickkey comparison folds only ASCII letters. A `TokenText` rune in the byte
range `0x80..0xFF` compares to the same stored quickkey byte unchanged; a rune
outside the byte range is not mapped. The control-byte edit tokens Backspace,
Tab, Enter and Escape normalize to `0x08`, `0x09`, `0x0D` and `0x1B` before
the same comparison. Other edit keys use the established special-key token
bytes of `[07 §2]` before that comparison. Text-editor admission remains its
separate ASCII-only contract until the codepage/IME question is resolved.

**The front end** (`frontend.go`). `Mode` is the screen: `ModeMain`,
`ModeSingle`, `ModeMission`, `ModeMap`, `ModeSkirmish`, `ModeLoading`,
`ModeBattle`. `Frontend` owns the mode and the panel stack; `Open` decides
replace-versus-save-under from the caller's authored-rectangle test;
`ActivePanel` is the modal-aware accessor; `Navigate` resolves only the fixed
authored edges (`SINGLE`, `SKIRMISH`, `PREVMENU`), leaving every
configuration-dependent transition to the composition root
`[07 "Retail closure for the single-player menu slice"]` `[07 R-FE-01 §2]`.
`InstallSkirmishDynamicGadgets` synthesises the skirmish setup rows from the
authored row geometry `[07 R-FE-02 §8]`.

**The battle screen** (`battle.go`, `result.go`). `BattleState` is the single
owner of mutable battle interface state: the modal chain, the modal press
capture, the pause truth, the panel slide, and the public `BattleInputState`.
`BattleInputState` deliberately keeps every gesture latch together — the armed
`Latch`, the drag rectangle, `HUDCaptured` with its press point,
`PlaceCaptured`, `ShiftHeld`/`ShiftLatchSticky`, the pointer position, and the
placement block (`BuildDef`, footprint, `BuildOK`, the resolved cell and site
height, `BuildSticky`) — because a HUD press, a placement press and a world
drag each own a *whole* button gesture, and separate owners would both observe
the same release and act twice. `BattleModal` is the modal chain state:
`Closed → Options → Exit → ConfirmMain`/`ConfirmExit`. `BattleModalAction` is
what activating a control means to the composition root — main menu, exit game,
save, load — never a navigation the UI performs itself.
`BattleScheduleIntent` is the pause/speed value the session applies
synchronously. `ResultAction` and `ResultActionForControl` are the post-battle
screen's control vocabulary.

### 2.5 `internal/hud` — battle geometry and verdicts

**Anchors** (`anchors.go`). `Anchors [30]Rect` is the side's mandatory anchor
block, stored verbatim as authored `x1,y1,x2,y2` corners and never normalised
`[02 §6]`. `AnchorNames` is the fixed name list and `AnchorIndex` its reverse.
There is no shared bar-fill helper: the top-strip stock bars and the footer
damage bar are inclusive fills with their own arithmetic — `FooterBarFill`'s
truncating integer divide `[07 R-HUD-03 §2]` and the composer's
single-precision `drawResourceBar` `[07 R-HUD-03 §4]` — and a third, clamped
fraction helper would only be a fourth statement of neither.

**Chrome** (`chrome.go`). The layout rules for a surface larger than the design
space: `ChromeRailX = 129` is the x origin of both horizontal strips,
`StripStamps` is retail's "advance by the frame width while `x < width`" stamp
loop, `BottomStripY` is `surfaceHeight − 32`, `RailGap` is the band of the left
rail that no chrome covers and that stays palette index 0 for the whole battle,
and `ModalPlacement` centres a modal in the surface width to the right of the
128-pixel rail and in the full surface height, both by truncating divides, at
the live size `[07 R-HUD-05]` `[03 §4.1]`.

**Selection and pages** (`selection.go`, `build.go`). `NormalizeDragRect` and
`DragRect.Contains` are the rubber band. The rectangle itself is
`client.WorldSelectionBand`: both drag endpoints are recorded as whole
three-component *world* points and converted by the ordinary projection at the
moment the band is used, each carrying its own half-height shear, exactly as the
unit points tested against it are `[07 §9]`. One band serves both consumers — it
is returned in the record space unit points project into and in the surface
pixels the overlay draws in — so the accepted handles and the drawn box cannot
disagree, and neither slides off the terrain when the camera scrolls or zooms
mid-drag. The drag's membership writes are not here:
`client.SnapshotUnitHandlesInBand` walks the committed frame and the
session's `HumanSelectionReplace`/`Toggle`/`Clear` commands own the modifier
truth table, which is the one path a shipped build takes `[07 §9]`. Selection
commands apply to local interface state immediately in offline and online
battles (DESIGN_MULTIPLAYER §7.3). The ordinary viewport and minimap point
selectors request the clicked unit's voice only if it remains selected;
a rectangle chooses its cue from the resulting eligible selection, including
units outside the box `[07 §9]`. These are requests to the existing audio
service, whose playback admission and throttling remain independent.
`AssignGroup`, `RecallGroup` and `TypeFilterPasses` are the control groups and
the `CTRL_F` filter.
`EncodePageBits`/`DecodePage`/`IsPaged`/`RememberedPage` are the page bits 23–25
with bit 22 marking paged. `RoutesToPage` and `DigitToPage` are the digit gate,
which `battleSession.routeDigit` drives — the group arm takes the digit itself. `BuildProductsFor`, `ProductsForPage`,
`BuilderPageCount` and the button/key variants `NextPageButton`/`PrevPageButton`
and `NextPageKey`/`PrevPageKey` are the index page cycle; `RetailBuildButtonsPerPage = 6` is the authored full-page size, and
no runtime path may infer a different grid `[07 R-HUD-03 §6]`. Authored builder pages page through
`PageState` instead: retail's `.`/`,` key, NEXT/PREV and BUILD operations on the page-shown bit and the
three-bit field, modulo eight, so a builder with nine or more pages follows retail's field arithmetic
`[07 R-HUD-03 §6]`. The adaptive sidebar keeps the index cycle for its own page ranges.

The authored GUI's named mobile-product buttons enqueue counted production
for the selected owned unit without requiring its definition's `Builder`
flag. `paletteContext` retains that actor and `DispatchFactoryBuildDelta`
checks the committed actor's ownership; site-placement admission remains
separate `[07 R-P0-11 §1]`. The synthetic click contract covers both flag
values, and the asset-backed Aegis case follows its real button through
completed construction and extended shield coverage. No content-profile
exception or rewritten unit definition is needed.

**The command latch** (`commands.go`). `ParseButtonLatch` is the button parse
chain in its corrected order — MOVE, STOP, ATTACK, BLAST, DEFEND, REPAIR,
PATROL, RECLAIM, CAPTURE, UNLOAD, LOAD — matched case-insensitively by
substring, with MOVE first and **no default**: a name matching none of the
eleven returns `NotHandled`, writes no latch and plays no cue. `UNLOAD` is
tested before `LOAD` because it contains it, and retail has no `PICKUP`
compare at all `[07 §9]`. `LatchToCode` maps the latch byte to the resolver's
command code.

`resetOrderLatch` returns the semantic latch to idle, clears Shift persistence,
and releases the retained buttons in STOP's authored association. World/minimap
dispatch, cancellation, Escape, Stop and Shift release share that transition;
Shift-queued commands retain their down-state until the latch retires
`[07 R-HUD-04 §3]`.

**The cursor** (`cursor.go`). `ChooseCursor` is the four-step shape chooser:
outside the world and minimap gives `cursornormal`; live mobile-build placement
gives `cursorfindsite` or `cursortoofar`; an empty selection gives
`cursorselect` over an own finished unit and `cursornormal` otherwise;
otherwise the per-unit shapes are reduced by **lowest index wins**, so the
table's numbering is also its priority order `[07 §8]`. `CursorHover` is the
pick result, including the resolved ground point, and `CursorSelection` the
acting side, including the stocks the command-fire affordability gate reads.
An immobile attacker's ATTACK shape is range-tested through
`CursorSelection.WeaponAdmits`, which the shell binds to
`session.CursorAttackAdmits`: slot 0 is rebuilt from the definition's first
weapon link on the committed-view copy and asked the combat service's own
unit-to-unit or point admission gate, so an out-of-range tower shows
`cursortoofar` `[07 §8][06 R-WPN-05 §9]`.

The idle Type-0, MOVE and REPAIR assistance shapes use
`CursorSelection.RepairAdmits`, bound to `session.CursorRepairAdmits`. A
private queue on the committed actor copy supplies the immutable sea level to
the order resolver's shared repair admission. Thus a construction aircraft
does not advertise assistance on a wholly submerged structure. Idle additionally
requires a nonzero remaining-build fraction. MOVE uses the shared admission
without code 2's extra unsigned health comparison, preserving retail's shape
for an over-full or death-latched live target `[07 §8][04 R-ORD-01 §7]
[04 R-ORD-02 §7]`.

The same chooser is the armed click's front door. `battle_commands.go`'s
`orderSelected` issues an armed order only when the reduced shape is an action
shape — an index below `cursorred` — judged on the unit that click itself
picked, so the advertised action and the performed action are the same action
`[07 R-CAM-01 §14]`. A refused click does nothing at all, which includes
leaving the order armed: only an issued click retires the latch, or keeps it
under the Shift sticky rule. The world-region bit is the caller's business
rather than the gate's — every caller has already classified the sample as a
world click, and the Modern area drag (§3.11) dispatches its own target list
and never reaches the producer; its short release does, and is judged, and
leaves the latch, like any other armed click.

The session command boundary carries the hovered unit identity and clicked
ground point independently, for both single clicks and area-list entries.
Revalidating a target does not replace the captured point with the unit's
origin; only the applicable formation adjustment changes an ordinary click's
point `[04 R-STANCE-01 §5][04 R-ORD-01 §13]`. An armed ground unit with a
non-AA primary weapon resolves an attack on an own or friendly unit to
`Suppress` and fires at that clicked point
`[04 R-ORD-02 §1][04 R-ORD-01 §3]`. The fast command tests preserve this in
local and online sessions, and the stock LLT/solar regression compares
single and batch friendly attacks with a ground-point control in all modes.

**The footer** (`footer.go`). `BuildFooter` composes the bottom readout from the
committed frame, the catalog and a `FooterHover`: the build-card line for a
hovered build button, the feature line, and the unit readout with its name,
damage bar, logo, metal and energy rates, kills line and secondary field
`[07 R-HUD-03 §1]` `[07 R-HUD-03 §2]` `[07 R-HUD-03 §3]`. The output is a value
— texts, bars and logos with a `FooterColor` that records whether the byte is
raw or logical — so the composer performs the palette lookup and this package
performs none. Survival may supply `FooterHover.UnitNamePrefix` from committed
attacker ownership; it is applied only after direct visibility admits the unit
name. Build cards and unidentified contacts ignore it. See
[DESIGN_SURVIVAL §5.2](DESIGN_SURVIVAL.md#52-infected-attacker-appearance).

**The minimap** (`minimap.go`). The compiled-in radar canvas the camera layout
letterboxes inside is `camera.LayoutMinimap`, with `camera.Minimap.HitTest` as
its inclusive hit test; this package adds `MinimapViewportRect`, the
camera-to-radar rectangle stroked as a one-pixel outline in colour-map entry
`ViewportMarkerLogicalColor = 14` `[03 R-MM-01 §1]`.

**Resource text and the score panel** (`status.go`, `scorepanel.go`).
`status.go` holds the resource strip's text: `FormatEnergyRate` with its
truncated `K` suffix outside the inclusive `-99999..99999` window, the
`FormatMetalRate` single fractional digit, and the produced/consumed pairs whose
consumed arm is a magnitude because the panel artwork owns the minus glyph
`[07 §6]`, alongside the `PaletteNormal`/`PaletteProduction`/`PaletteConsumption`
logical entries. The readouts themselves are read out of the committed frame by
the composer in `cmd/nanolathe`, not reduced into a second status model here.
`ScoreShowing` is the Space-held panel's gate — the
interface bit, Space held, and no focused text editor; `ScoreSlide.Step` is its
slide with its cues; `ScoreRowOrder` compacts the qualifying slots; `ScoreFlash`
is the kill/loss flash, armed only while the interface bit is set
`[07 R-HUD-04 §1]`.

**The queued-order overlay** (`queueoverlay.go`, `buildmarker.go`).
`QueueOverlay` is a pure snapshot consumer: it walks the committed order queues
while Shift is held and returns immutable draw instructions, retaining no
pointer into simulation state and mutating nothing, so holding Shift cannot
move the partial state fingerprint `[07 R-P0-11 §3]` `[07 R-P0-11 §4]`. The walker's
privileged sources are the follow camera's tracked unit, the unit whose command
page is open, the hovered unit, and every selected unit; all four draw the full
five-bit mask, but selection alone suppresses travelling sprites while retaining
icons and anchor advancement. The walk visits only the primary order list.
Every other local unit draws marker-only, and the marker-only
fallback runs only when one of the first three resolves to a live builder.
`queueDescriptors` is the per-kind census of the order descriptor's two overlay
bytes — the draw-mask word (marker 1, dash 2, circle 4, icon 8, range 16) and
the icon byte that indexes the same twenty-two-slot cursor array the software
pointer uses. An icon byte of 0 is the "no icon" encoding rather than cursor
slot 0. Helpers run in mask-bit order, and the bit-8 helper is also the anchor
getter, so a kind that sets bit 2 without bit 8 still draws its icon first.
`DashSprites` places the authored sprite chain along a world-space segment —
the dash is a sprite chain, never a line to rasterise. Each connector runs
directly from the preceding anchor to the order destination; published movement
route points are not overlay vertices `[07 R-P0-11 §3]`. `BuildMarkerSegments` is
the eight-segment build marker with its ten-tick sweep.

The battle adapter `queueAnchorFrame` supplies targeted-order icons/connectors
with the target's committed position through a presentation copy. An owned
target-only assist therefore needs no stored point [07 R-P0-11 §3]. Remaining
integration gap: the visibility-dependent cached horizontal anchor needs an
immutable publication binding; the adapter marks it with `TODO(question)`.

### 2.6 `cmd/nanolathe` — the front-end screens

`frontend.go` is the shell: `gameShell` holds the mounted asset set
(`menuAssets`: the common and logo GAFs, the FNT, the two GAF fonts, the
palette, one `retailPanelAssets` per screen, the message window, the loading and
mission backgrounds), the map and campaign lists, the skirmish setup, the
persisted display and message settings, the briefing controller, the shared
audio owner, the loading state, and the `ui.Frontend`. `openMenu` is the one
screen transition: it clears the outgoing panel's gesture state *before*
replacing it, applies the `NEWGAME.GUI` layout mutation for the Play-Any branch,
installs the skirmish dynamic rows, decides save-under from
`panelWindowNeedsUnder` — true only for a window smaller than the display, which
among the single-player screens is `SELMAP.GUI` alone — and forces the surface
back to 640×480, because the front end always runs at that size whatever
`DisplaymodeWidth`/`Height` hold `[07 R-FE-02 §2]` `[07 §4]`. `applyDisplaySize`
is the resize step in both directions and `applyDisplayMode` its load-transition
half `[07 R-FE-01 §11]`. `step` is the per-host-frame pump: battle, loading, or
the menu pass, each installing its cursor shape — the hourglass across the
blocking load transition and the idle shape everywhere else `[07 §8]`.

The demo save/load fallback uses its authored `SAVELIST.GUI` / `LOADLIST.GUI`
rectangles and the existing panel/common-art fill when `LOADGAME.GUI` is
absent. Those resources originally serve restriction lists; their reuse for
game saves is Nanolathe presentation policy, owned by
[DESIGN_SESSIONS_AI_SAVE §5](DESIGN_SESSIONS_AI_SAVE.md#5-divergences).

**Demo campaign fallback (user-authorized 2026-10-03).** When
`bitmaps/playanygame4.pcx` is absent, the shell selects the observed demo
campaign layout `[07 R-FE-01 §4]` / `[08 R-CAMP-01 §3]`. Count every
root-level `camps/*.tdf` before filtering by side: two or fewer selects
`newcampaign4x`, hides both lists, and selects the literal `Arm Campaign` or
`Core Campaign`; more than two selects `newcampaign4`, shows the side-filtered
campaign list, and hides the mission list. Both keep the authored NEWGAME
rectangles; a new campaign action enters mission zero. Selecting a visible
campaign row starts its briefing; otherwise Start does. Initial focus is Difficulty for the fixed
campaign and Campaign for the visible list.

This is Nanolathe content presentation compatibility in every gameplay mode,
not a change to retail 3.1's reachable layout or to battle rules. A present
Play Any background retains both lists and the existing compressed layout.
Malformed backgrounds still fail; only a missing Play Any file selects the
fallback, and the chosen campaign background remains required. Unused campaign
backgrounds are not preloaded. `TestMissionBackgroundFallback` locks the
selection, the strict two-file boundary and decode failures;
`TestCampaignFallbackUsesAuthoredLayout` locks visibility, literal side
selection, retained mission identity and authored rectangles. Panel refreshes
preserve the mission selected by ENDMSN or a continuation load: the first-mission
reset belongs to New Campaign's Start/row action, not to population. Otherwise
opening the return panel before MSNBRIEF would replay mission zero after victory.

The supplied demo unconditionally greys the Core portrait (`Side1`) and caption
(`Core`) [07 R-FE-01 §4]. As user-authorized content presentation policy
(2026-10-03), Nanolathe's fallback instead greys each side's portrait and caption
when its layout offers no campaign with a mission, and greys Start when the
selected side has no mission. Added playable content re-enables that side; the
Play Any layout retains its existing controls.
`TestCampaignFallbackDisablesUnavailableSides` locks pointer and keyboard refusal, empty descriptors and re-enabling.
Visual acceptance uses the extracted demo alone to open MAINMENU, New Campaign, briefing and battle.

**Unavailable menu actions (user-authorized host presentation policy,
2026-10-03).** On each fresh MAINMENU or SINGLE window, grey entry controls
whose required content is absent. Use the existing gadget grey bit
`[07 R-WGT-01 §13]`; preserve authored restrictions, visibility, rectangles,
and the rest of the grey word. These checks apply in every gameplay mode and
add no gameplay selection or capability registry. Reopening or rebuilding the
shell from expanded mounted content recomputes availability.

- SINGLE requires its cached child window. Intro and Credits require their
  respective movie files; their existing load boundaries still diagnose a
  present but malformed movie.
- Skirmish and Survival require both setup/chooser windows and at least one
  map from the existing Network-schema census with a paired TNT file. Do not
  decode every terrain or build a catalog to decide a menu entry.
- New Campaign requires NEWGAME, MSNBRIEF and a campaign offering a mission
  under the selected layout. Play Any additionally requires the Play Any
  layout; the demo's authored hidden control remains hidden.
- Options requires its GUI and background. Load Game requires the currently
  selected retail or missing-only fallback dialog and its required background.
  An empty save list retains its established message; the save/load feature
  remains available when its presentation resources are present.

A missing child backdrop, like a missing child GUI, is retained as an
unavailable screen rather than preventing its usable parent from opening.
MAINMENU remains required at startup. Malformed backgrounds retain their
existing failure, and file-presence checks never substitute for decoding at
an action's load boundary. Missing optional gadget art, narration, result art
or CD tracks does not disable an otherwise usable screen.

Options categories are greyed when their required page GUI/backdrop is absent,
using the existing page table and its distinct frontend and in-battle sources.
The Music page remains usable without tracks, but CD previous/stop/play/next,
track mode and per-track category require music enabled and a nonzero track
count. Its volume and enable controls keep their existing meaning. This extra
availability gate is Nanolathe host policy; retail's music-enabled tests remain
`[03 R-AUD-01 §4]`.

`TestFrontendAvailabilityFollowsMountedContent` locks input refusal, paired
terrain, reopening, authored grey bits and supported neighboring actions.
`TestFrontendLoadAvailabilityUsesMissingOnlyFallback` locks dialog selection.
`TestFrontendMissingBackdropIsAnUnavailableChild` locks the missing/malformed
boundary. `TestOptionsAvailabilityUsesRequiredPageAssets` and
`TestMusicTransportNeedsAvailableTracks` lock options dependencies and the
zero-track transport gate. Visual acceptance uses the extracted demo to
inspect MAINMENU, SINGLE and Music, then enters Arm's briefing.

The supplied demo's cursor bank also predates `cursorrevive` while supplying
every other required named shape `[07 §8]`. As host presentation compatibility,
an absent revive entry uses that bank's normal pointer; the logical cursor
index and command chooser remain unchanged. A present revive entry retains
its authored art. Empty entries, missing normal art and other missing required
shapes remain errors. `TestOlderCursorBankUsesNormalForMissingRevive` locks
these boundaries. This policy enables no resurrection capability or rule.

`retail_menu_input.go` adapts the host sample to `Panel.ServiceFrame`, which
owns pointer capture, held repeats, selection and keyboard dispatch. A
user-requested host extension routes wheel and two-finger scrolling over a
text list or its associated scrollbar/arrows to that list. One normalized
wheel unit moves one row; fractional units accumulate per panel and target
list, and clamping discards outward fractional motion. This changes `top`
without changing the selected row. It is host presentation policy: retail has
no dedicated wheel-message case `[07 §1]`.

List scrollbar painting consumes the service's retained knob and derived knob
size from the already-built track rectangle. Runtime arrow children paint
separately; the painter must not remove their extents from the track a second
time. A list refresh with unchanged rows preserves `top`, and an external
change to `top` synchronizes the associated knob before pointer service
`[07 R-WGT-01 §4]` `[07 R-WGT-01 §5]`.
`openMenu` finishes associated list scrollbars with `BuildSlider` before
creating the panel, retaining the selected arrow entry and frame bases.
The window background supplies a list's decoration when present; `SELMAP`
also suppresses the fallback tile through its opening flags. Its preview
caches an indexed canvas per map and canvas size, with the authored padding
cropped and usable terrain centred on palette zero `[07 R-FE-01 §5]`.

SKIRMISH screen entry copies the lobby selector into the campaign/session
selector before building the controls. Its Difficulty callback cycles the lobby selector and writes the same
value to the campaign/session selector, including the Hard-to-Easy wrap
`[08 "Skirmish configuration"]`. The coupling belongs to this screen; it
does not make every preference load or NEWGAME write update both fields.

The screen's typed-key history accepts `*III` through `*X` (with `*` entered
by Shift+8) and sets the visible row count to 3 through 10. The matching
count is saved immediately and the runtime rows are rebuilt at their new
spacing; `*V`, `*VI` and `*VII` keep the prefix so the longer numerals can
complete. The setup still requires a map with enough start positions and at
least one computer opponent `[07 R-FE-02 §10]` `[08 R-SKIR-01 §1]`.
Lowering the count hides rows without clearing their controllers, so
`skirmishConfigForStart` reads only the shown rows: a hidden live row never
becomes a player, matching the row-to-player conversion `[08 R-SKIR-01 §2]`.
The controller cycle and the row build's all-Open fallback (row 0 Player,
row 1 Computer, applied when the rows are built, not at settings load) also
read only the shown rows `[08 R-SKIR-01 §1]`.

**Computer AI (Nanolathe divergence, user decision 2026-09-25).** Each
computer row plays the Classic or the Modern AI
([DESIGN_SESSIONS_AI_SAVE](DESIGN_SESSIONS_AI_SAVE.md#modern-ai-computer-player)
"Per-player selection"), chosen with the row's existing name button: no new
gadget, no new art, and the retail layout unchanged. The button's caption
names the row's AI, `Modern AI` or `Classic AI`, where retail writes
`Computer`; both fit the 112-pixel `skirmname` button. The controller cycle
splits retail's Computer stage in two: a row that becomes Computer (from
Open, or by the all-Open fallback) starts as the Modern AI — the user's
default for a newly added computer slot — the next click makes it the Classic
AI, and a click on a Classic row leaves the Computer stage exactly as retail's
Computer row does (to Player when no shown row is a human, otherwise to Open).
A right click on the name button still does nothing. A computer row's hover
help names its AI and what the next click does. The start conversion carries
each computer row's AI through the compaction (`SkirmishPlayer.AI`) and gives
a human row none. The choice persists with the row's other choices: the
settings file's skirmish row stores `"ai": "classic"` for a Classic computer
row and nothing for a Modern one, so a row without the word — every row of
a file written before this encoding — loads on the Modern AI (user decision
2026-09-25); the Survival screen keeps its buddy rows, choice included, for
the session ([DESIGN_SURVIVAL §9](DESIGN_SURVIVAL.md#9-front-end), where a
buddy row walks Open, Modern AI, Classic AI and back to Open). The row alone
chooses: the options page's gameplay control chooses rules only, and no
gameplay selection overrides a row. Tests: `TestComputerRowsCycleModernThenClassic`,
`TestAClassicRowLeavesTheComputerStageAsRetail`,
`TestTheLobbyCarriesEachRowsAIToTheBattle`, `TestTheSettingsFileKeepsEachRowsAI`,
`TestSurvivalBuddiesCycleOpenModernClassic`,
`TestTheAIPlayerFlagMarksComputerRows` and, on the retail art,
`TestSetupScreensChooseEachComputerRowsAIRetail`, which also composes each
screen's battle under Strict 3.1 and Modern and, with `NANOLATHE_SHOT_DIR`
set, writes both screens as PNGs.

`activateGadget`,
`activateSkirmishGadget` and `activateDynamicSkirmishGadget`
are the callbacks — the authored escape default is resolved by the widget
service's key-navigation frame, not by a separate front-end handler; `openMissionMenu`, `retailSkirmishStartError` and
`retailAllPlayersSameAlliedGroup` are the `SKIRMISH` start preflight
`[07 R-FE-01 §5]`. `retail_menu.go` owns the panel refreshes and the authored
data flow (campaign options, map data, skirmish rows, ally icons, hover help);
`retail_menu_list.go` the listbox and the scrollbar geometry, knob sizing and
drag; `retail_menu_options.go` the options family — both roots and all four
merged pages (`SOUNDS`, `MUSIC`, `SPEEDS` — whose root button is captioned
`INTERFACE` — and `VISUALS`), the display-mode list, the per-page `RESTORE` and
`UNDO`, the entry snapshot `CANCEL` restores, and the slider arithmetic
`[07 R-FE-01 §6]` `[03 R-AUD-01 §2]` `[03 R-AUD-01 §4]` `[07 R-CAM-01 §7]`.
The interface page retains `UNITCHAT`'s byte-sized setting independently of
the button's current stage. Its `SCREEN` entry conversion preserves the
stored reciprocal and the resulting callback read-back, including a seeded
knob one position beyond ordinary pointer travel. The visual pages seed
their sliders without running value callbacks, so opening them does not
quantise stored gamma or display size. Explicit slider changes still commit
through the existing callbacks. `retail_options_audit_test.go` locks these
boundaries with authored controls; other page behavior and host display
choices remain separate `[07 R-FE-01 §6]`.
The sound page likewise keeps `SPEECH`'s live stage separate from its stored
byte-sized level: reopening derives the stage from storage. Its cue request
precedes the setting write, while `MODE` requests its cue after applying the
new audio gates and control state. Sound-page Restore and Undo request their
cues after reopening. These are request-order contracts; the host's cue gain
policy and actual playback remain separate `[07 R-FE-01 §6]`.
Entering Repeat, or opening its page while Repeat is selected, restores the
screen selection from the controller's retained request without starting
playback. Both input adapters poll the displayed track after widget service
and before dispatching an action, including passes with no action. They use
the controller's logical next track, which remains meaningful while stopped,
and a narrow track-detail refresh that preserves already-advanced control
stages. That refresh writes the selected track back as the request in Repeat
`[07 R-FE-01 §6]`. The ordinary music tick still owns subsequent playback.
For a missing or wrong-kind `TRACKNUM`, the host skips polling: retail's
uninitialized text has no established stable value, so this is an explicit
host fallback. `retail_music_options_audit_test.go` locks entry, both adapters,
edited text, disabled music and stage-preserving refresh with authored pages.
The controller now exposes `SelectTrack` for stopped/paused selection and
playing-track submission, plus `UpdateNow` for an explicit update using the
existing fresh device query without servicing timers. A fresh `Open` seeds
the retained request while preserving its separate next-track boundary
`[07 R-FE-01 §6]`. `music_options_lifecycle_test.go` locks these controller
contracts and the production service's one-time initialization. Repeated
`Open` and `Close`/reopen retain the existing host policy; their mapping to
retail's already-open no-op needs a complete lifecycle caller audit before
changing it. The frontend transport now adopts the selector's result and
refreshes only track details; Play performs no detail refresh. The options
root retains its selection across ordinary close/reopen, independently of
next and requested track, while the existing new-shell content reload starts
fresh. Its entry snapshot includes the requested track and restores it only
within the controller's accepted upper bound.
Music-page departure runs once: a fresh-query update in battle or stop/reset
in the front end. Root actions depart before their cue and action; Undo and
Restore apply their state first, then depart and rebuild. Cancel restores its
snapshot after departure. Restore changes the stored Custom/enable preferences
without applying the controller mode or enable setter. Undo and Cancel apply
the saved mode, conditionally update before restoring the enable preference
and request, and also avoid the enable setter `[07 R-FE-01 §6]`.
`retail_music_lifecycle_audit_test.go` exercises authored roots and pages through
both production input adapters, including query-time state, selection before
the next poll and distinct stored/live mode and enable values. These immediate
callback contracts make no promise about subsequent audible playback.
`retail_sound_options_audit_test.go` exercises these boundaries through
authored page loads and widget callbacks, including cue-time output state.
Rebuilding a merged page restores the selected category's down-state in the
replacement panel, including the Nanolathe category, while releasing the old
pointer capture. Direct opens and per-page reopens therefore show the same
radio selection as pointer activation `[07 R-WGT-01 §3]`.
A page's staged buttons show their stored value in the current-stage byte and
never in the down-state word, because the stage selects both the caption and
the art. `NOTRAK` works the same way: the `MUSIC` page shows `musicmode` bit 0
as the switch's stage, and a click stores the stage it selects. The bit used
to go into the down-state word. The switch then always opened showing `Off`,
so with music on, a player who set it back to `Off` turned the music on again.
`TestMusicSwitchShowsAndSetsTheMusicBit` and, on the retail page,
`TestMusicSwitchRetailPage` lock it `[03 R-AUD-01 §4]`.
Every routine there takes one of two arms, chosen by the `inBattle` word on the
options state: the front end opens `STARTOPT.GUI` over `options4x` and merges
`SOUNDS`/`MUSIC`/`SPEEDS`/`VISUALS` with their own full-screen plates; the
battle opens `PREFS.GUI` with no bitmap at any step, widened by 150 columns with
a `PANEL` gadget synthesised over them, `MAP`- and `VID`-prefixed gadgets
hidden, and merges the `…RT.GUI` variants through the merge's centring branch
instead of its origin-add branch. The stored audio block, game speed and
`Interface Type` word live on `gameShell` beside the display and message
blocks, so one options session can write them whichever arm it took;
`retail_menu_message.go` the
`MSGBOX` layer with its word wrap `[07 R-FE-02 §6]` `[07 R-FE-01 §9]`;
`retail_menu_draw.go` the screen painter, resolving each saved-under window's
resource set by its logical GUI name because `openMenu` clones the parsed
definition before building runtime controls; `window_panel.go` shares authored
panel resolution, the clipped nine-frame fill and the art-less bevel with
battle modals `[07 R-FE-02 §4]` `[07 R-WGT-01 §12]`.

`skirmish_menu.go` is the setup rules (opponent count, line of sight, the
resource steppers); `settings.go` reads and writes the persisted preference
block; `briefing.go` is the campaign briefing controller — planet resolution,
panorama and rotation frames, paged text, the wind line and the narration
effects `[07 R-FE-01 §4]` `[08 R-CAMP-01 §2]`; `loading.go` is the loading
screen and the loader goroutine, whose progress and result the render goroutine
alone reads `[07 "The loading screen"]`; `loadgame.go` is the one
`LOADGAME.GUI` serving both save and load, with the summary field mapping and
the `RADAR` preview `[07 R-FE-01 §8]`; `postbattle.go` and `result.go` are the
post-battle machine, its glamour fade and the score bars `[07 R-FE-01 §10]`.

`briefing_render.go` retains each side's decoded immutable background for the
current content set, including an absent optional background. Replacing the
content set invalidates both entries. The cache changes no authored pixels or
briefing state; it removes archive reads and PCX decoding from each draw.
`TestBriefingBackgroundCacheTracksContentAndSide` locks the source boundary.

The campaign briefing adapter passes the named `SHUTUP` button's resulting
stage to `DispatchNarrationStage`: zero requests a stop, and every nonzero
stage requests narration when its mission key exists. `NarrationOn` reports
that stage decision, not audible playback. An authored three-stage button can
therefore request another start directly from its opening stage; the audio
owner handles delayed requests [07 R-FE-01 §4][03 R-AUD-02 §1]. The in-battle
briefing reuses only the pager and does not dispatch narration.
The same adapter requests `BigButton` before Start validation and requests
`Options`, the narration action, then `SmallButton` for `SHUTUP`. Prev stops
the stream before requesting `Previous`. Successful Start and Prev stop any
current stream even without a narration key; a failed Start leaves it running
[07 R-FE-01 §4]. Cue attenuation and audio admission follow retail
[03 R-AUD-01 §1][03 R-AUD-01 §2].

`briefing_render.go` paints wind and gravity from the panorama's custom path,
using the hidden `SOLARSYSTEM` gadget's rectangle and selected FNT. It uses
the controller's existing wind display without new random draws, translates
both labels and converts authored gravity for the readout only
`[08 R-CAMP-01 §2]`. Successful text installation assigns the side font to
both text-region and conditions gadgets; missing text retains their authored
font numbers. Each briefing visit records successful current-GAF loading and
panorama callback installation independently of the retained artwork. A failed
load or missing named panorama does not admit condition text; after binding,
the text still precedes missing-sequence and frame guards. Condition text
precedes panorama art and uses its own clip. The retail tests
`TestBriefingConditionsRequireCurrentArtBinding` and
`TestBriefingConditionsUseHiddenGadgetFontAndClip` lock these boundaries.

Selecting a row fills the summary panel's text fields and its `RADAR` surface.
`RADAR` shows the selected file's Summary `Radar Image` box — an 8-byte
width/height header and then the rows of palette bytes — and nothing at all
when the box is absent or short, which covers a continuation save, a save
written before the box had a producer, and a truncated file
`[08 R-SAVE-02 §3]`. The box is read once per selection, from the selected file
only: the slot-list enumeration deliberately drops box payloads, so a directory
of saves is listed without carrying one raster per file. The producer is the
battle save arm of the same file, which encodes the rail's composed radar
surface (docs/DESIGN_SESSIONS_AI_SAVE.md, "The `Radar Image` preview box").
Retail's placement of the picture inside the authored 121x113 rectangle is
unestablished and carries a `TODO(question)`; this build resamples it with its
own aspect preserved and centres it, so nothing of the saved battle is cut
away.

Below the authored summary fields the window gains one Nanolathe label,
`NLSIDECAR`, a copy of `TIME` moved beneath it and widened. It names the
selected save's mod and active mutators from the save's Nanolathe sidecar
(*ProTA 4.8 - Health x2*, *(not installed)* after a mod the library lacks),
so the player knows before loading that the game will switch; a save with no
sidecar leaves it empty. This is a Nanolathe divergence from the authored
window ([DESIGN_MODS_MUTATORS §8.4](DESIGN_MODS_MUTATORS.md#84-the-load-dialog)).
A restored battle opens without the loading screen, so the sidecar's load
warnings (§7.3) are posted to the battle message line instead of the loading
screen's lines.

The GAF-font text path is `retail_font.go`. Retail's interface text has two
pens: the side `.FNT` and the GAF fonts loaded as window font slots.
`retailGAFGlyph` indexes a `formats.GAFEntry` by character code;
`retailGAFTextWidth`, `retailGAFTextHeight` and `retailGAFBaselineHeight` are
its metrics; `drawRetailGAFText` and `drawRetailGAFTextClipped` write glyph
bytes with no light-table remap (pen mode 0), and `drawRetailGAFTextLit` is the
lit variant that resolves through the palette's light table. Where a window
selects a GAF slot it restores slot 0 afterwards, and a null slot falls back to
the active FNT with the width limit dropped `[03 R-FONT-01 §6]`
`[07 R-HUD-04 §4]`.

The active FNT is per gadget. `gui.Window.FontRecord` is the walk every text
painter opens with — the n-th kind-7 record of the window, n the gadget's
`fontnumber` read as a signed byte, so 0 is the window's first record and
9/132/205 select none — and `gui.Window.Font` loads and caches that record's
FNT through the VFS, nil when no record matches or the file is missing (the
common font stays) `[07 R-WGT-01 §6]` `[03 R-FONT-01 §5]`. The painters apply
it as retail does: a label whose number matched draws through the FNT drawer
directly (`drawRetailLabelFNT`, `drawModalLabelFNT`); a button, listbox or
text-input string, and a label that matched nothing, go through the GAF pen,
where the selected FNT is reached only on the null-slot fallback
(`drawRetailStringSelected`, `drawProductButtonCaptionSelected`). A side
page's build count therefore stays `hattfont12` although every product button
selects `armbutt`/`corbutt`; `MSNBRIEF`'s `MOREBAR` caption is `smlfont` and
its paged lines carry the text region's `localSide + 1` `[08 R-CAMP-01 §2]`.

Label X belongs to the mutable window record. The GUI loader preserves its
negative local value; options-page placement translates it before initial
painting. The frontend and battle builders perform the initial label-position
writes before their callers replace captions, including an empty caption.
Later painters test the current X and retain the signed-16 result in that
record, so changing `MapName`, hover help or a modal title does not repeat
centering from `RawX`. A result still equal to the sentinel is tested again on
the next paint. Ordinary alignment and the window origin are applied after
that stored position is resolved [07 R-WGT-01 §7] [03 R-FONT-01 §6].
The message-box builder also preserves positions across its authored paint
and resized rebuild. Its resize pass gives every label the final panel width
and centre attribute, then restores the final builder's inert bit for labels
with empty links [07 R-FE-01 §9], [07 R-WGT-01 §7].

**Pending caption fitting.** Shell and battle button painters currently draw
their selected caption without the stored-text shortening required by
[07 R-FE-02 §5]. The ordinary unique single-caption case is established:
resolved artwork width 26 with three ten-pixel glyphs must retain only two
caption bytes after its first paint, even if the rectangle later widens.
Staged-caption termination and duplicate-name lookup are now established in
the owning research section. Their adaptation and the ordinary fitting fix
are deferred until a concrete stock-content example establishes practical
impact. Widths below six and invalid-text memory behavior remain unknown.
No stock occurrence is claimed.

### 2.7 `cmd/nanolathe` — the battle session

`battle.go` holds `battleSession`: the session and catalog, the camera, the HUD,
the surface size, the shell hand-off and the teardown. `battle_composition.go`
turns a menu choice into a `freshBattleRequest`; `battle_controller.go` is the
host-frame driver, taking one `input.Sample` and a millisecond source and
running the pass in order.

`battle_input.go` is the pointer and latch state machine — the subject of §3.1
and §3.3 — plus the keyboard table of §3.2, the digit routing gate and the
Ctrl+letter category table. `battle_selection.go` owns picking and eligibility:
`pickTarget` is the shared committed-frame picker used by both selection and
targeting, so fog, radius, tie-breaks and viewer rules cannot disagree between
them; `ownSelectableUnit` is this build's one copy of the shared eligibility
predicate `E(u)` — own slot, selectable bit, remaining-build fraction zero,
post-capture grace zero, carrier null or itself visible `[07 R-WGT-01 §9]`
`[07 R-WGT-01 §10]`; `eligibleHandlesInRect` and `selectedHandlesInSlotOrder`
keep slot order.

`battle_camera.go` owns the battle-start placement (the campaign start-position
special, the skirmish commander, the watcher case), the committed shake apply,
the scroll setting and its raw delta, the follow camera, the `t`/`T` cycle, the
`n` next-unvisited glide and the F3 message-source glide `[07 R-CAM-01 §14]`.
`battle_minimap.go` owns the minimap pointer paths: the admitted camera-button
down edge sets a presentation capture that first re-jumps on the next host
frame and continues from live pointer records through its matching up edge; the
other polarity's order button uses the lens point, and the minimap hover unit
shares that usable-region classifier with command and cursor consumers
`[07 R-CAM-01 §11]` `[07 R-CAM-01 §5]` `[07 §8]`.
`battle_placement.go` owns `cursorWorld` (the SC20 resolver), the build ghost,
the site check and `commitBuild`. The placement marker renders the adjacent
same-colour strokes of [07 §9] as one solid border. Its full two-pixel width
uses `ViewScale.Px(2)` so magnification preserves continuity; the half-step
scale encoding must never be used as a pixel inset. `battle_commands.go` and `battle_dispatch.go`
are the command boundary of §3.4. `battle_menu.go` drives the modal chain and `battle_options.go` the in-battle
options window `ARMOPT`'s `PREFS` opens over it;
`battle_settings.go` the damage-bar and message-line options;
`battle_cursor.go` the pointer update and the footer hover;
`battle_status.go` the game-speed and pause hotkeys, posting the announcement
to the shared message ring;
`battle_queue_overlay.go` the overlay's art resolution and draw.

The cursor's hostile-target prediction reads the selected actor's directional
alliance row from `frame.PlayerRow`, copied at tick-end publication. It uses
the actor row indexed by the target owner, exactly as the command resolver
does; it neither consults a snapshot unit's unbound order queue nor combines
the reverse declaration. Thus an allied different-owner target, an enemy, and
an asymmetric declaration all retain the authoritative answer across the
presentation boundary `[04 §3.4]` `[05 R-SHARE-01 §1]` `[I6]`.

**One message ring.** Retail has a single 30-entry message ring, fed alike by
unit captions, chat and the game-speed announcement `[07 R-HUD-03 §14]`. The
presentation client (`internal/client`) owns the one instance: it is what
`drawMessageLines` paints and what the INTERFACE options page's `TXTSCROL`/
`MAXLINES` sliders configure through `ConfigureMessageLines`. `battleSession.
messageRing()` (`battle_status.go`) returns that same instance through the
`*client.Client` `installBattleClient` wires onto the battle shell (field
`battleSession.cl`), rather than holding a second ring — so F3's glide, F12's
clear and the speed announcement's post all share the 30 entries and the one
pair of visited/jumped cursors the composer's highlight and the caption
producer both depend on. The announcement (`setGameSpeed`) posts to this ring
as kind 2, silent, no source unit `[07 R-CAM-01 §3]`; its only drawn
representation is the ring line the message column paints at its usual
position, so nothing else renders it a second time. This is a different
readout from the slide strip's own always-on `Game Speed` line below, which
recomputes from the live clock every frame it is drawn and is not ring-backed
`[07 R-HUD-04 §4]`.

`installBattleClient` binds the HUD's primary COMIX FNT for this column through
`SetMessageFNT` and GAF-font slot 0 (`hattfont12`, the HUD's `modalFont`)
through `SetMessageGAFFont`; `SetFNT` retains the side console for group digits
`[03 R-FX-01 §6A]`, and the HUD passes its own font operands explicitly. The
column advances by the COMIX glyph height, but its text goes through the GAF
pen: each glyph frame is a plain keyed blit of its authored bytes, so the lines
keep `hattfont12`'s outlined face on any terrain and the F3 destination looks
like every other line `[07 R-HUD-03 §14.4]` `[03 R-FONT-01 §6]`. Only with
`hattfont12.gaf` missing does a line fall back to COMIX, resolving logical
colour 15 (ordinary) or 10 (the F3 destination) through the active palette map
before recording its glyph command, so classic and modern share the same
foreground `[07 "Retail palette contract"]` `[03 §4.3]`.

Successful battle installation clears the ring's producer/display cursors,
including on save load, so captions and source handles from an earlier battle
cannot reach the column or F3 `[08 R-ENTRY-01 §3]`. This is the same cursor-only
operation as F12: hidden records and configured limits remain, and appending
replaces the message fields while retaining the slot's visited/jumped flags
`[07 R-HUD-03 §14.3]`. `drawInterface` records the column only with a committed
nonterminal battle frame. The front-end's unpublished snapshot therefore
suppresses old captions on menus and briefings; terminal results use ENDMSN's
separate outcome surface. Paused nonterminal battle frames still draw the
column `[07 R-HUD-03 §14.3]` `[07 R-HUD-03 §14.4]`.

The column shares the HUD's loaded `32xlogos` entry through
`SetMessageLogos`. A real speaker selects the committed roster's `Logo` byte;
the scaled logo precedes the text using the geometry in `[07 R-HUD-03 §14.4]`.
The retained announcement drain posts the finished session line and plays
`MessageArrived` only after the ring accepts a real speaker. This preserves
the eliminated owner on class-4 announcements, including with `screenchat=0`,
and leaves sentinel captions, local chat and score lines text-only and silent.
Repeated composition and draw-list replay cannot repeat the cue. The open
localized possessive-tail question remains owned by `[08 R-CAMP-01 §9]`.

### 2.8 `cmd/nanolathe` — the battle HUD

`battle_hud.go` builds `retailBattleHUD` from the mounted side data: the anchor
block, the rail art, the GUI windows, the fonts, the radar surface and the
display size. The rest is split by concern: `battle_hud_assets.go` is the asset
resolution and its diagnostics; `battle_hud_pages.go` the command-window
selection (closed for an empty selection, `<prefix>gen.gui` for generic orders
or multiple selection, and the generated `<unit>N.GUI` numbered pages) and the gadget art and
frame choice; `battle_hud_siderail.go` the rail draw, the command-button
verdicts (staged, greyed, hidden) and the product captions and queue counts
`[07 R-HUD-03 §6]` `[07 R-P0-11 §2]`; `battle_hud_input.go` the rail's click
pass, where a hidden button is skipped before the hit test and a greyed one is
hit-tested but fires nothing `[07 R-WGT-01 §1]` `[07 R-WGT-01 §3]`;
`battle_hud_footer.go` the footer draw; `battle_hud_resources.go` the top strip;
`battle_hud_minimap.go` the radar rebuild and draw;
`battle_hud_scorepanel.go` the Space-held Kills/Losses panel;
`battle_hud_slidestrip.go` the bottom slide strip; `battle_hud_modal.go` the
modal draw, its placement, the nine-slice `BackTile` fill and the paused title,
and the last composition layer — the child windows `ARMOPT` opens over the
battle: the options root (`battle_options_draw.go`) and the save/load dialog,
with the shell's `MSGBOX` above them `[07 R-FE-01 §6]` `[07 R-FE-01 §8]`.
`unitinfo.go` is `UNITINFOx.GUI`, a child window that services a release before
the command page does `[07 R-HUD-03 §8]`.

**Build-product count labels.** Human world placement constructs ordinary
mobile-build orders with a zero production count, for both ground and aircraft
builders. Shift selects queue insertion without turning a site into counted
production. The published count therefore leaves commander construction
buttons blank; factory products and stockpile rounds keep their counted
producer paths and labels [07 R-P0-11 §2].

**Displayed resource stocks.** `Client` owns the two displayed singles and
passes them by value in `UIFrame.Resources`. `BeginPresentationFrame` advances
them once per host presented frame using `[05 R-ECO-01 §6]`; both stock bars
and current numbers consume this pair, while capacities remain live.
Each bar's fill is inclusive on both spans `[07 R-HUD-03 §4]` `[03 R-P0-19-P]`:
`fill = ftol(x1 + w·S/C)` with the product and quotient in single precision,
and the painted rectangle is `[x1..fill] × [y1..y2]`. A bar therefore covers
`ftol(w·S/C)+1` columns and `y2−y1+1` rows, a zero stock still paints the one
column at `x1`, a full stock paints `w+1` columns, and a capacity at or below
zero paints nothing because retail's fill sits inside the `C > 0` branch.
`drawResourceBar` takes the stock pair rather than any clamped ratio, which
would divide before multiplying by `w`.
`drawShareMarker` then runs in the same `C > 0` branch, painting the player's
automatic-sharing threshold `T` `[05 R-SHARE-01 §3]` as the three columns
`[m..m+2] × [y1..y2]` in `dcb[12]` at `m = ftol(x1 + w·T/C)`, but only while
`0 < T < live stock` — both bounds strict, and the gate reads the committed
live stock, not the eased displayed stock the fill uses `[07 R-HUD-03 §4]`.
`EconomyView` carries both thresholds so presentation never reaches into the
economy ledger for them.
`UIFrame.Resources` also carries the four unscaled settlement-rate latches.
`Client` binds each viewed player's saved `EconomyView.DisplayTimer` once per
battle buffer. On each presented frame, unsigned `deadline < tick` samples
the four rates and advances the previous deadline by 30 exactly once. Equality
does not sample; an overdue deadline can catch up across repeated presented
frames at one committed tick. The shared rate latch survives `View` and buffer
replacement; only the deadline bindings and displayed stocks reset on a new
buffer `[05 R-ECO-01 §1, §6]` `[07 R-HUD-03 §4]`.
Multiple presented frames at one committed tick each step
the pair. Composition and replay do not step it. Only the viewing player's
committed economy row is admitted; a missing row retains the previous pair.
Binding a different snapshot buffer at battle installation or save restoration
resets both values to zero `[07 R-HUD-03 §4]`. `View` publishes into the same
buffer and retains the pair. The audio-only `TickPresentationAudio` API remains
an audio drain; the host boundary calls it after advancing displayed stocks.

The modern pre-record worker draws a pure prediction of the next stocks and rates and
includes it in its presentation digest. The real host boundary computes the
same step before consuming that list; a miss or discarded prediction cannot
advance or restore the retained pair. See DESIGN_GPU_RENDERER §13.10.
`--shot` advances one presentation frame before choosing its executor; the
`both` route composes both images from that same retained pair.

The save host supplies `Client.ResourceDisplayTimers()` as detached metadata
to `RetailSaveInputs.DisplayTimers`. Projection overlays only those initialized
player deadlines on copied save rows; unviewed players retain the session's
seeded or restored values. Presentation never writes stocks, ledger fields, or
the authoritative session's timer copies [I6].

**The slide strip.** The panel offset has exactly one consumer. It is not a side
rail: it is the strip that slides up from the bottom edge of the *view* when
Space is held, and neither `PANELSIDE` nor any rail window or gadget rectangle
moves with it `[07 R-HUD-03 §1]` `[07 R-HUD-05]`. With `x` the composer clip
rectangle's left edge (128), `yBottom` its bottom edge (`H − 33`) and `off` the
slide offset, the `LIGHTBAR` band is blitted at `(x, yBottom + off)` and the
three readouts are written on one line at `yBottom + off + 10` — `Game Time` at
`x + 25`, `Total Units` at `x + 190`, `Game Speed` at `x + 380` — in GAF slot 1.
The formats are literal: `%s : %02d:%02d:%02d`, `%s : %d  (Max %d)` with two
spaces, and `%s %s` with no colon after the speed key. The strip is drawn only
while the offset is non-zero `[07 R-HUD-04 §4]` `[07 §6]`.

The host update passes the battle's existing `clock.MillisSource` to
`BattleState.AdvancePanelWithClock`. This is the process-relative monotonic
source used by the battle, not low Unix milliseconds. The service follows
the signed strict-deadline admission and separate admitted-visit sample of
`[07 §6]`; it refreshes the deadline before updating the Space/editor target,
even at a resting detent. `AdvancePanel` is an equal-sample convenience for
deterministic callers. Painting never reads this clock. Tests expose both
source reads and retain the established easing and cue-request sequences.

**Host boundary.** The deadline remains owned by each battle's UI state and
starts at zero with that state. Complete retail deadline retention through
every reset/rebuild is still unresolved in `[07 §6]`; no process-global timer
ownership is inferred. The process-relative host origin is also distinct
from retail's system-uptime origin. This boundary does not alter the
simulation clock or its scheduling state.

### 2.9 Capture tooling

`shot.go` and `flags.go` own `--shot`: compose one battle frame after
`--shot-ticks` authoritative ticks and exit without opening a window.
`--shot-select` runs the select-all first so the command page is open,
`--shot-modal` opens one of the modal layers, `--shot-space` holds Space so the
slide strip is raised, `--zoom` and `--shot-focus` stage the presentation
zoom of §3.5, and `--shot-size` composes at a surface other than the authored
640×480. A capture is the evidence for any visual change in these packages.

`--shot-modal` takes a window name and presses the authored buttons that reach
it, in order, through the same `ui.BattleState.Activate` the pointer drives, so
a capture can open no window the game cannot: `options` (`ARMOPT` itself),
`exit` (`EXITMENU`), `confirm` (`YESORNO`), `settings` (`GAMEOPTIONS.GUI`),
`help` (`HELP.GUI`) and `briefing` (`BRIEFING.GUI`). The last three are the
read-only children of C18.1. `settings` and `briefing` press the same
`MISSION` button, whose child is the session kind's, so `briefing` needs a
campaign session (`--mission <selector>`) and `settings` a skirmish; the other
session is refused rather than captured as the window the reviewer did not ask
for.

Two capture-path facts are worth knowing before reading one of those pictures.
A single composed frame samples the briefing's blink pre-pass once, and that
first step always lands on phase B — palette index 94 — so an `&`-coded run
prints in the blink colour rather than the letter's own `[07 R-FE-02 §7]`; the
window alternates it, a capture cannot. And the rows are read at open, so a
`GAMEOPTIONS` capture shows the session the capture composed, not a fixed set
of words `[08 R-SKIR-01 §11]`.

## 3. Contracts

Two contract numberings reach these packages from their source plans, and a
bare `Cn` in a Go comment is resolved by the citation it stands next to: beside
`[07 §10]`, `[03 §2.5]`, `[07 §2]`, or written `[PLAN_04A Cn]`, it is a shell
contract of §3.1; every other bare `Cn` under `internal/gui`, `internal/hud`,
`internal/ui` or `cmd/nanolathe` is an interface contract of §3.2–§3.4. The
shell list's C7–C13 — indexed colour, FNT metrics, the frame boundary,
presentation randomness, the backend and draw order — are carried by
DESIGN_PRESENTATION_CLIENT, not here.

### 3.1 Shell — camera, input and picking (C1…C6)

**C1 — projection.** World-to-screen is the integer orthographic projection:
subtract the camera, one map pixel per whole world unit, and the half-height
vertical shear with arithmetic shifts. The active viewport supplies the origin;
consumers do not copy path-specific constants `[03 §2.5]`.

**C2 — scroll.** Scroll magnitude is the authored setting byte multiplied by the
raw time delta and capped at 128; a zero delta makes no movement. The cap is a
signed comparison with no absolute value, so a negative delta keeps its sign.
The delta is thirtieths of a second, so at the default setting byte 32 the
sustained rate at native 1× is 960 map pixels per second at any frame rate
(§3.8 defines the zoom conversion), and the cap is a low-frame-rate limiter `[07 §10]` `[07 R-CAM-01 §10]`.

Magnitude is only half the contract; the pass is also a **shape**. It makes
one exclusive test per axis, Left before Right and Up before Down, so a
satisfied Left (or Up) predicate skips its opposite entirely and two opposing
directions move the camera once toward Left/Up rather than twice or not at
all; and it commits the origin **once**, after both axes
`[07 §10]` `[07 R-CRD-006 §1]`. This build applies each axis as its own
clamped pan, which lands on the same origin because the clamp is per axis.
Retail's beyond-edge forced strip is jointly gated by focus and both
coordinate bounds [07 §10]. Its trailing-edge-only clamp assumes the retail
presentation surface: negative top/left coordinates never reach an edge, and
a letterbox bar wider than 100 logical pixels can put even the host's right or
bottom edge outside the strip.

**Nanolathe host presentation policy:** `Client.EdgeScrollPosition` includes
letterbox padding calculated from the adapter's actual outside dimensions and
extends the same 100-logical-pixel overshoot strip to all four host edges. The
pointer must be strictly inside both expanded bounds, and the window focused,
before it is clamped to the logical canvas for the camera predicates. This lets
a player reach an edge even when a sampled motion skips the exact boundary
pixel, or when fullscreen aspect fitting adds bars. Picking, HUD hit tests and
cursor rendering retain the original pointer coordinates. This is host policy
in both gameplay modes and both renderers, independent of simulation rules.
The adapter refreshes the outside dimensions from `Layout`; a client without
that adapter uses its logical size. Regression cases drive the battle pass
with overshoot, wide/tall hosts, focus loss and out-of-strip coordinates, and
assert both camera displacement and unchanged picking coordinates.

Retail's pass has no minimap-region test and no modal test, and it consults
window focus only inside that forced strip [07 R-CAM-01 §10]. This build
additionally suppresses the **edge** disjuncts when the window is unfocused and
when a modal is open; that much is host behaviour, recorded here so the
divergence is not mistaken for the traced pass.

The pass no longer tests the minimap. It once did, and because the radar canvas
is anchored at the screen's top-left corner (C4) that test cost the whole
top-left corner plus the first 126 pixels of the top edge and of the left edge
— the pointer positions a player uses to pan up and left. A captured minimap
camera drag that reaches those canvas edges does not fight the restored edge
disjuncts: the latch jumps the camera to the lens point of that same edge
(canvas column 0 is the map's leftmost column, canvas row 0 its topmost row),
so C3 has already pinned the axis, and the edge disjunct then pushes further in
the direction the clamp holds. The camera does not move at all
`[07 R-CAM-01 §11]`.

**C3 — clamp.** Per axis, compute the maximum from the map size and the battle
viewport's own span, clamp the negative side first, then clamp above the
maximum. The ordering is preserved even when the viewport is larger than the
map. Both bounds are expressed in this build's frame of reference by
substituting the leading inset, so the whole playable area is reachable
`[07 §10]` `[07 R-CAM-01 §13]` `[03 §4.1]`.

**C4 — minimap.** The longer map dimension occupies 126 pixels, the other is
scaled with integer division, and the unused axis is centred by truncating the
half padding. A click converts through the lens with a signed multiply and a
truncating divide, subtracts half the viewport so the clicked point becomes the
view centre, and then applies C3. There is no separate drag branch: the latch
re-runs the same jump every host frame `[03 §3.6]` `[07 §10]`
`[07 R-CAM-01 §11]`.

**C5 — input queues.** The keyboard ring has 30 entries with 29 usable; the
pointer button ring has 20 records with 19 usable. A full ring refuses the new
event, never overwrites an older one, and leaves both indices unchanged. When
the pointer button ring is empty, host service uses the latest separate motion
record `[07 §2]` `[01 R-PLAT-01 §6]`.

**C6 — selection modifiers.** With the modifier clear, units inside the
rectangle are set and those outside cleared; with it set, units inside toggle
and those outside are preserved. Owner slots are visited in stable ascending
order `[07 §9]` [I1]. Replacement must restore an inside unit after the bulk
pre-clear even when it was already selected; reporting unchanged membership
must agree with the resulting flags and selected count. The two HUD drag
helpers currently have test consumers only; live selection is applied at the
session command boundary.

### 3.2 The GUI file and window model (C1…C7)

**C1 — the parser's kind arms.** The builder handles the fourteen-entry switch
indexed `0..13` after an unsigned bounds test: the panel arm with its `-1`
centring and `BackTile` fallback chain, the button with staged frames chosen by
best-fit frame size and `|`-separated labels, the listbox whose rows, hit rows,
selection and scrolling are `[07 R-WGT-01 §4]`'s and whose associated peers
share the larger item height, the text input whose name is capped at 127 bytes,
the scrollbar/slider arm that synthesises two child gadgets with derived knob
travel, the label, the font arm that loads `<font directory>\<filename>.FNT`
whole, the raw-file arm that opens the authored filename verbatim, the picture
box, the score bar, and the panel alias. Kinds 6, 9, 10 and everything above 13
do no build work and are still parsed and serviced `[07 §4]`
`[07 R-WGT-01 §11]` `[07 R-WGT-01 §12]`.

**C2 — record identity and art resolution.** Retail gadgets have 347-byte record
identity; Go represents the named fields without packed-layout assumptions
[I13]. Art resolves in order: the gadget's own named GAF entry, then the
side-specific interface GAF, then the built-in fallback `[07 §4]`.

**C3 — runtime dispatch families.** The stored type byte routes to distinct
families: `1` clickable with a callback result, `2` stateful, `3` focusable text
editor, `4` dedicated update, `5` association-capable — comparing up to 16 bytes
of a name identifier across gadgets in a fixed-stride scan — `6`
callback-producing, `12` repeating under a throttle, `13` timed and animating
toward a maximum `[07 §4]` `[07 R-WGT-01 §5]` `[07 R-WGT-01 §7]`
`[07 R-WGT-01 §8]`.

**C4 — text-editor admission.** The authored maximum is a 16-bit value capped at
128. Printable bytes are admitted up to `maxchars − 1`, subject to the input
filter bit `0x02`, the allowed-set test with its exceptions for space,
underscore and apostrophe, and the width rule
`currentLen + newLen ≤ controlWidth − 4`. A paste copies at most `maxchars − 1`
bytes, keeps termination, then trims trailing bytes until the rendered width
fits `[07 §4]` `[07 R-WGT-01 §6]`.

The shared editor receives paste as an immutable `input.ClipboardText` on the
Insert or Ctrl+V token. A successful read replaces the entire text, including
successful empty text, bypasses ordinary printable/filter admission, and trims
to the full control width, stopping when only one byte remains even if that glyph
is still too wide. Paste retains the previous caret index, including when the
replacement is shorter. A failed read leaves text and caret unchanged
`[07 §2]` `[07 R-WGT-01 §12]`. As **Nanolathe safety policy**, the editor bounds
that retained index to the current string before each subsequent token, so an
edit following a shorter paste cannot slice beyond Go string storage. The
painter bounds its local prefix separately. This is host safety handling, not a
claim about retail's later edits with an index beyond the visible text.
The host reads its clipboard only on the initial paste key transition, and
queued tokens retain that snapshot. macOS reads the AppKit pasteboard's plain
text and Windows `CF_UNICODETEXT`. Linux and BSD desktops have no clipboard
the standard library reaches, so they run the session's clipboard program:
`wl-paste` under Wayland, then `xclip` or `xsel` under X11. A read keeps at
most 64 KiB and waits at most about a second across the programs it tries; a
missing program, a failure or `xsel`'s empty output (which cannot be told
from no text) reads as unavailable. Cmd+V is an authorized host shortcut alias
in both gameplay modes, with no synthetic Ctrl held state and no other Cmd
shortcuts translated into game keyboard tokens. The host removes any companion V character from paste.

The portable text boundary currently accepts ASCII bytes unchanged, including
control bytes, and stops at the first NUL. It rejects an entire non-ASCII payload
rather than guessing replacement characters. `TODO(T25): establish the host
Unicode-to-retail-codepage conversion` remains the research gap in `[07 §2]`.
Tests cover replacement, empty success versus failure, copy and rendered-width
bounds, retained caret and safe subsequent edits, filter bypass, native
private-pasteboard reads, Windows UTF-16 decoding to the first NUL within the
bound, the unix program choice from the session and PATH, its output and wait
bounds, queued snapshot ownership, and Cmd/Ctrl/Insert translation through
the common editor.

**C5 — hit tests and greying.** Hit tests are inclusive on both edges and run
after runtime window placement. Hidden gadgets are skipped before the hit test,
so one neither acts nor shields what lies behind it; a greyed gadget is
hit-tested, takes no capture and fires nothing, and the pass continues to the
gadgets after it `[07 §3]` `[07 R-WGT-01 §1]` `[07 R-WGT-01 §13]`.

**C6 — dispatch order and window close.** GUI dispatch precedes battle hotkeys.
In zero-token mode the peek suppresses tokens `0xE2..0xEB` — not all tokens.
Closing the top window runs callbacks, redraw, removal and predecessor
reactivation, plus an extra redraw under flag `0x800` `[07 §3]`
`[07 R-WGT-01 §1]`.

**C7 — associated scrollbars.** A scrollbar associated with a list takes its
range, knob size and position from the list; the authored values are not trusted
`[07 §4]` `[07 R-WGT-01 §5]`.

### 3.3 The battle HUD (C8…C15)

**C8 — the anchor block.** All 30 side anchors are mandatory and are stored
**verbatim** as `x1,y1,x2,y2` corners, never normalised. Their consumers are the
top-strip painter and the footer; `TOTALUNITS` and `TOTALTIME` are loaded and
never read `[02 §6]` `[07 R-HUD-03 §5]`.

**C9 — selection and groups.** Selection modifiers follow the truth table;
iteration over the owner's range is stable ascending; membership is bit `0x10`
with a GUI dirty bit. Group recall honours the preserve/toggle argument and the
`CTRL_F` filter keyed on flag `0x80000000` `[07 §9]`.

**C10 — digits and pages.** Digits 1–9 select a control group or a build page
under the `SwitchAlt` gate: the gate is `switchAlt == alt`, so by default a
plain digit pages and Alt+digit recalls, and with the option set the two swap.
`SwitchAlt` is a persisted low-bit preference: an absent value is clear, the
frontend shell carries its normalized bit into battle, and a direct battle
captures it at install time. Digit handling reads that captured bit and never
opens settings on a keypress. The option has no authored options-page gadget;
Nanolathe's Orders page adds one, *Digits: Pages / Groups*, beside Idle keys
and 2-click. It shares that page's options transaction (Undo, Restore Defaults
to the clear retail bit, Cancel, and OK persisting it), and a change made in
battle also updates the running battle's captured bit. Partial I10 implements
its local chat command through the shared TALK command path (§3.9), which
keeps working beside the control.
The page number lives in unit-flag bits 23–25 with bit 22 marking paged, guarded
by the builder's page count. Generated menu records author `PAGE` and `BUTTON`
explicitly, and the generated `<unit>N.GUI` pages determine page existence and
placement. Stock full pages carry six 64×64 product gadgets and the last page
may be partial, so no runtime path may infer an eight-slot grid `[07 §9]`
`[07 R-CAM-01 §4]` `[07 R-HUD-03 §6]`.

Build-button activation resolves the installed gadget name to its catalog
unit definition, the same identity used by artwork and the hover card.
Generated assembly patches that name at authored `BUTTON+4`; a physical page
keeps its authored name unless a generated placement replaces it. The published
`CommandPage.ProductKeys` membership union cannot be indexed by gadget ordinal:
download entries may be sparse, reordered or replace existing slots. Pointer
and accelerator activation share this name lookup `[07 §9]` `[07 R-HUD-03 §3]`.

**C11 — the latch is the switch key.** The command latch byte *is* the order
dispatcher's switch key, and the button parse chain is MOVE → STOP → ATTACK →
BLAST → DEFEND → REPAIR → PATROL → RECLAIM → CAPTURE → UNLOAD → LOAD with no
default. The latch-writer census is complete: the order-button dispatcher arms
`1..9`, `0xC` and `0xD`; the battle-HUD build-button **click** arms `0xE`
(MOBILEBUILD) when the product's `BMcode` byte is zero, storing the product id
in the pending-build word and playing `addbuild`; and nothing in the image arms
`0xB` (TELEPORT), which stays a consumer-only switch key. The latch-to-idle
reset is byte 1, the Shift-persistence bit `0x20` cleared, and the `STOP` radio
group's status words zeroed and repainted `[07 §9]` `[07 R-HUD-04 §3]`.

**C12 — the cursor table and the chooser.** The cursor index table is 0..21 with
slot 10 `cursorrevive`; every index from `cursorreclamate` up shifts by one
against the previously published twenty-entry table (SC15). `cursorprotect` is
present in the art and never resolved by retail. The software cursor is drawn
after the composed surface with the GAF frame's authored `x_offset`/`y_offset`
as its hotspot, and the index writer diffs before swapping so an unchanged shape
keeps its animation phase. Shape selection is the four-step chooser of §2.5
`[07 §8]` `[03 R-FX-01 §5]`.

**Nanolathe host presentation policy — placement feedback.** While build
placement is armed, `cursorfindsite` is drawn with its artwork centred on the
pointer used for site picking. The retail GAF offset puts that one reticle
down-right of the pointer; this display choice changes neither the chosen
cursor shape nor the site, click or order. Modern's late cursor positioning
(DESIGN_GPU_RENDERER, "Pointer latency") skips the cursor drawn with the ghost —
this reticle over a legal site, `cursortoofar` over an illegal one — so it stays
on the host-step pointer the ghost was snapped from and the two move together;
retail's separate cursor thread does not tie them. At rest the reticle sits on
the pointer, up to half a cell from the footprint centre along each map axis,
plus on uneven ground the screen shift of drawing the ghost flat at the site
height: that is the retail round-to-nearest site snap and site height [07 §9],
not an offset. When community click snap moves the ghost onto a deposit, only
the ghost moves; the reticle stays on the pointer (community patch engine
CP-CON-6). The green/red footprint border keeps
its retail validity colour and cell-aligned rectangle. The default preview is
the building's pulsing nanoframe wireframe, using the committed tick and the
construction colour ramp [03 §5.2]. The preview pauses with the committed
tick and consumes no RNG. These choices apply in every gameplay mode because
they are presentation only `[07 §8][07 §9]`.

**C13 — the panel slide.** On entering battle a flip surface is allocated at the
negotiated video-mode dimensions with the static `PANEL` backdrop blitted in,
plus a cleared 300×480 backup scratch strip whose header words are saved; the
command panel starts visible when the session mode byte has bit `0x04` set. The
offset advances on a **15 ms wall-clock throttle** — a step whose timestamp is
early is skipped — and each accepted step eases by `remaining/3` with a minimum
of one pixel in both directions, so it always converges. The detents are `−31`
and `0`. Leaving either detent plays `Panel`; reaching either plays `Options`.
The static shell's framebuffer origins are `PANELTOP (129,0)` and
`PANELBOT (129,H−32)`; the moving strip's origin is the view's bottom-left
corner. Retail passes each origin plus the frame's `XOffset`/`YOffset` to the
raw GAF blitter, which subtracts the same offsets: they cancel and must not
translate decoded panel pixels `[07 §6]` `[07 R-HUD-04 §4]`.

**C14 — Space polarity.** With Space **held** the strip slides toward `−31`
unless a latched gadget whose authored record type equals 3 — the text-editor
family — holds focus, in which case it slides toward `0`; with Space released it
always slides toward `0` `[07 §6]`.

**C15 — the gadget layer's place in the frame.** The battle frame draws ten
ordered layer passes: terrain, features, soft units, hard units, shadows,
selection and health, projectiles, explosions, gadgets, squad overlays. The
compositor is DESIGN_PRESENTATION_CLIENT's; this document owns the gadget
layer's contents `[07 §6]` `[03 §1]`.

#### Modern defeated players

**Nanolathe Modern policy** (user-authorized 2026-10-01, issue #61).
The Space-held/F4 score panel retains defeated players, dims their row artwork,
prints `Defeated` on a separate line, and adds `Remaining: alive/total` above
the Kills/Losses headings. Names, rank order, both counter pairs and kill/loss
flashes remain available. Selection uses the bound session's central
`gameplay.Mode` base: Modern, including sets derived from it, enables this
presentation; Strict 3.1 and Community 3.9 retain the retail panel.

**Retail baseline — Established.** The score-panel filter keeps a player whose
last unit was lost, because its auxiliary word remains zero; there is no defeat
label or remaining count `[07 R-HUD-04 §1]` `[08 R-CAMP-01 §7]`. The ordinary
skirmish defeat and victory predicates read the live-unit count
`[08 R-SKIR-01 §3]` `[08 R-TRIG-01 §6]`. The economy's distinct elimination
predicate also reads the created-unit count, which is rebuilt from surviving
units after load `[05 R-ECO-01 §12]`.

**Display contract.** Among the rows the existing filter/rank scan actually
draws, zero committed live units means `Defeated`; every other such row counts
as remaining. This is a display classification, not a stored elimination bit
or a change to any end condition. It works for an empty player restored from a
save even when its created count is zero, and clears if a subsequent committed
frame has live units again. Unused, inactive, neutral and watcher slots excluded
by the existing row filter contribute neither to the count nor the rows.
In Survival, the final configured seat is the commanderless wave attacker
(DESIGN_SURVIVAL §4.1): its existing score row is retained, but it contributes
neither to the survivor count nor to defeat labels during empty wave intervals.
Campaign sessions still have no score panel.

**Layout.** The panel retains its 125-pixel width and 40-pixel row advance.
Modern adds 15 pixels of heading space and sizes the body to the drawn rows,
so ten rows end at scanline 461 on a 640×480 surface. A defeated row darkens its
artwork at shade level −12 before writing readable text: name at row+1,
`Defeated` at row+13, counters at row+26. Active rows keep the retail text
offsets. These dimensions and the shade are presentation choices.

**Boundary and verification.** The painter reads committed player rows, the
bound mode and immutable slot setup, with no new persistent state, save fields,
RNG draws or resource effects. The existing `RuleSet` binds the reserved base into
`Session.Gameplay`; no gameplay algorithm asks a new question, so a new rules
interface would serve no owner. A mode switch selects the display afresh on
the next composition, with no marker state to migrate. This adds no selector
or renderer preference.
`TestModernScorePanelDefeatUsesOnlyDrawnPlayers` covers mode/default selection,
the row exclusions, revival and Survival's wave slot.
`TestModernDefeatedScoreRowDrawsMarkerAndKeepsCounters` checks the composed
marker/counter pixels, the Strict/Community bypass, and unchanged resources and
both RNG draw counts. The optional installed-art capture
`TestRetailDefeatedScorePanelCapture` exercises three and ten rows, a defeated
local player, and the retail layout at 640×480 for visual inspection.

#### Oversized authored build pages

**Nanolathe host presentation policy.** The authored-layout path fits oversized
command canvases below the minimap. Classic uses this
path; Modern expansion uses the flat layout below when supported. The ordinary GUI loader retains its retail
640×480 header clamp [fmt gui]; the battle page loader preserves an explicit
header origin at or below the minimap when its authored canvas exceeds that
height. Fitting stock pages keep their existing geometry in the authored-layout path.
Oversized-page fitting is independent of gameplay mode: it makes authored
controls accessible on the chosen host surface.

Generated placements retain `BUTTON + 4` record addressing [07 R-HUD-03 §6].
The target must be an authored button marked as a product or an empty `IGPATCH`
slot, including numeric artwork-family suffixes. There is no inferred six-slot
capacity and command records cannot be overwritten. ProTA 4.8's authored GUI
has a 128-pixel header origin with a 640-pixel canvas and mixed 16/32/64-pixel
shipyard controls. TA Zero base with Alpha 5 authors twelve `IGPATCH3` slots
and download records targeting the later slots. These are authored-content
observations supporting the host layout; they make no claim about retail or
third-party patch execution.

An oversized build page keeps its own fonts, artwork, controls and associations.
Vertically intersecting product rectangles and artwork form an indivisible
group, preserving unequal widths, heights and starting positions. The complete
navigation and command block stays at the bottom, with authored spacing
reserved between products and controls. Whole groups are partitioned into
visible pages; trailing empty slots do not create pages. A partition never
mixes different authored page scaffolds. The existing local sidebar pager owns
navigation and resize anchoring, without changing the committed page state.
Orders loads the authored side GEN or explicit custom unit-zero window and
moves its complete button block below the minimap. Empty IGPATCH template slots
are omitted from Orders; real product records retain their source, geometry
and normal click behavior. During fitting, a textless, unbound button can be
omitted only when its complete hit/art extent is covered by a later active
product or empty product slot. Commands, navigation, captions, links, shortcuts
and partially exposed controls are retained. This handles CORE's covered
placeholder records without a side-name rule. BUTTONS0 extent uses only the
selected size family's reachable states, not unrelated sizes in that GAF entry. Drawing, pointer activation, hover,
and keyboard service share this geometry. Changing pages or size releases
old retained pointer capture.

This fit supports button/font windows contained within the rail, with product
and command blocks separable vertically. Unsupported widgets, a product group
that cannot fit beside the complete command block, and oversized Orders blocks
retain the original path; arbitrary GUIs are not promised. No source window,
catalog, or committed frame is mutated. The synthetic overflow tests lock
extended-slot safety, mixed groups, product reachability and resize anchoring.
`TestRetailOversizedModMenus` optionally checks all installed commander products
and Orders for every authored faction at 480, 768 and 1080 pixels under both renderer
selections. Supply
`NANOLATHE_MOD_ROOTS_PROTA`, `NANOLATHE_MOD_ROOTS_ZERO` and
`NANOLATHE_MOD_ROOTS_TWILIGHT` as host path lists,
with the usual retail asset variable; `NANOLATHE_MENU_SHOTS` saves HUD
captures outside the repository. The capture is the common software HUD
composition with the selected Classic or Modern sidebar policy; it does not
execute the Modern GPU world replay. A 2026-09-22 run against the identified
ProTA 4.8 archive (`ba2ee5c…`) passed both factions and all six size/policy
combinations. The reviewed first and trailing pages showed the package's own
ARM/CORE build pictures, letter overlays, fonts and command chrome; Modern's
flat pages preserved the four directional shipyard buttons as one composite.
This establishes authored interface presentation only. It does not establish
the package DLL's gameplay, complete hotkey assignment table, or GPU model
parity. `TestRetailProTAPresentationAssets` separately locks the archive
provenance for those GUI pages, both 96×96 commander portraits and all six
authored palette/table overrides, checks the twelve-slot/shortcut records and
decodes the package's pink and slate 32×32 team-logo frames. Its optional
`NANOLATHE_PROTA_PRESENTATION_SHOTS` directory receives those four small art
captures for visual review.

#### Modern expanded sidebar

This is a user-approved host presentation policy, independent of the central
Modern / Strict 3.1 gameplay rules. The Modern renderer uses a flat list of
logical build cells. Classic retains the authored layout, including the fitted
oversized-page path above. The build-count and supplementary-orders preferences
retain preview, Cancel, Undo, Restore and persistence behavior. World zoom does not change the available UI pixels.

Compile each builder's list once from resolved numbered GUI windows, after
applying download placements. Read pages in authored page order and products in
vertical then horizontal reading order, with stable source-record tie breaking.
Real products on a custom page zero also belong to this list. Omit empty IGPATCH
slots and inactive records; retain duplicate real entries and separate directional
shipyard definitions. CANBUILD is neither a source of button identities nor a
button-state or placement gate: a physical page may name a product that differs
from CANBUILD (ARMPLAT's ARMCSA, CORCS's CORSY and CORLLT), and such a cell greys
only when its name resolves to no definition in the expanded view. Opening a
numbered source page above zero applies the retail product-resolution pass
before either sidebar copies its records. It visits the header and children
before the final loaded child, replacing their low grey bit after download
placement while preserving higher bits `[07 R-HUD-03 §6]`. The final child
keeps its incoming state, including any earlier download clearing. Custom
page zero retains its authored enabledness. Do not reconstruct this list from
membership or infer relationships from unit-name suffixes. Arbitrary building rotation is deferred.

Each list entry is one logical cell containing one or more product buttons.
The host lays cells out in two columns of 64-by-64 squares in the 128-pixel rail
below the minimap. A single product keeps the normalized presentation: its
artwork is centered and fitted without distorting its aspect ratio.

**Composite cells are an approved Modern presentation policy.** After download
placements and art resolution, group multiple active, resolved products only
when their positive hit rectangles completely tile a 64-by-64 square on the same
source page, without overlap, crossing products, or artwork extending outside
a child's rectangle. The candidate square starts at an authored child corner;
if multiple possible tilings share any child, leave those products separate
unless the source is a 128-pixel rail and its two-column grid uniquely covers
every product participating in a candidate tiling. That grid uses the window's
horizontal origin and the topmost resolved product row, with 64-pixel steps.
Keep only grid-aligned candidates when that cover is complete; a partial cover
retains the ambiguity fallback. This prevents shifted squares across adjacent
Twilight factory composites from competing with their own complete cells,
without grouping a partial strip run or consulting product names.
Do not group merely because four nearby icons exist, and do not consult unit
names, mod identities or gameplay relationships. Sparse, overlapping or otherwise ambiguous layouts retain individual normalized
cells; duplicate product records are never removed. Empty slots
are not products and cannot complete a composite. This conservative geometric
rule recognizes ProTA's directional shipyard artwork without changing which
unit any button builds. In the installed ProTA ARM and CORE commander menus,
the resolved shipyard controls tile one square as two 16-by-64 side strips and
two stacked 32-by-32 centre buttons; resolved art geometry matters because CORE's
GUI text gives different initial dimensions for the side strips.

A composite preserves each child's authored offset and size within the square.
Keep its children in source record order for shortcut/widget precedence, and
sort logical cells in source page and spatial reading order. Pagination never
splits a composite. Every child retains its product identity, shortcut, help,
source window, font, artwork, association and ordinary build action. Hit testing,
hover, queue counts and drawing share each child's rectangle; there is no parent
build action or new submenu. Source geometry still selects the original button
art family. Availability remains a draw/input decision and never repaginates or
regroups the list. Building rotation remains outside this policy.

**Build capacity before orders (user-authorized 2026-10-01).** Two independent
preferences choose build items (6, 12 or Free flow) and whether orders appear
below build items when space permits. There is no separate Original choice:
six items with Never supplies the compact build view.

Free flow first reserves **six logical build slots**, then includes the complete
supplementary Orders-page panel only if it fits alongside those slots and the
always-visible common command rows, then fills every remaining
complete two-column row. Fixed counts reserve their selected count before
considering Orders. If that count cannot fit even without Orders, use every
complete row that fits; never shrink the buttons. Free flow's adaptive path
requires capacity for at least six slots; the ordinary 640×480 minimum is enough
for the stock scaffold. A builder with fewer products has a partial page, not
invented products. Unsafe custom scaffolds retain the authored/fitted path.

The Orders source is the custom unit-zero GUI when present, otherwise the ordinary
side Orders GUI. It owns common commands and supplementary controls; deduplicate
matching named controls from the first numbered build page, retaining that page's
unique controls. Recognized commands and NEXT/PREV arrows match by the existing
dispatcher's action identity, so a foreign builder's native pages can combine
with the local side's generated DL and Orders windows. Other controls retain
their complete names. Use the same identities for numbered-page comparison,
deduplication and shared association mapping; preserve source names and artwork.
Move, Stop, Guard (`DEFEND`), Patrol, Attack and D-Gun (`BLAST`)
remain on every build view, alongside tabs, page navigation and build-only controls.
The preference governs the supplementary Orders-page controls above those common
rows, not the common rows themselves. Both compositions preserve authored command
row gaps and native capability greying/transport visibility. Hidden supplementary
controls remain available on the dedicated Orders page, which shows the complete commands
without build products; BUILD returns to the remembered build partition. When
orders fit inline, the existing combined Orders/build view remains. Non-builders
continue to use their ordinary orders GUI regardless of this preference.

Keep the authored row gaps and margins, including artwork extents. Compress only
gaps when necessary to fit the required build capacity, proportionally and with
at least one pixel per positive gap. Orders visibility, navigation and build
capacity are fixed across all build pages, including a short final page. The
shared `sidebarProductCatalog.sidebarLayout(height, limit, orders)` returns
capacity, supplementary-orders visibility, retained commands, gaps, panel height
and command-panel origin for
both the battle and the settings demonstration. Its arithmetic allocates whole
64-pixel rows; composite product cells remain indivisible.
All numbered sources must agree on command identities, stages and grouping so
flattening cannot hide a later-page-only control. Compare retained numbered
controls' shortcuts using the widget service's ASCII-only case equivalence;
extended bytes remain exact. Shared named commands replaced by Orders use the
Orders shortcut consistently, even if numbered pages author different keys (OTA's
advanced aircraft plant authors Guard as `q` on one source and `g` on another).
This canonical shortcut choice is Modern host policy; Classic keeps each source's
own shortcut. No source records are rewritten. Shared commands
connect source-local association groups; reject contradictory mappings rather
than silently changing radio-button behavior. Source fonts and artwork survive
composition. Unsafe widgets, incompatible scaffolds or panels that leave no
complete build row retain the authored/fitted fallback. Different product sizes
or sparse slots alone are not a fallback reason. Controls stay stationary across
all build pages, including the partial final page. Fixed counts place the controls
directly after the reserved build rows and authored gap, rather than at the rail's
bottom; spare height stays below the controls. Free flow fills the available rail
and retains the footer at its bottom. A dedicated Orders page uses the vacated
build area directly below the tabs. Hit regions and artwork must not overlap build cells.
Hidden commands retain their authored accelerators as inactive, zero-hit-area
records in the private composed window. The widget token pass enables those
records temporarily, retaining capability greying and command precedence, then
restores them before drawing. Pointer-only passes never enable them. This hides
buttons without disabling ordinary or rebound order shortcuts; source GUIs remain
immutable.

Page count is one Orders page plus the ceiling of logical cell count divided by
the resolved capacity. Fixed counts and Free flow both partition the same ordered
list across authored source-page boundaries, so two stock six-item pages combine
under twelve. There are no empty trailing pages; the final partition may be
short. Source identities, product order and shortcuts remain authored.
The pager remains host state: arrows cycle build pages, comma/period also visit
Orders, digit d selects visible page d-minus-one, and BUILD returns to the
remembered build page. Existing SwitchAlt and squad behavior remains unchanged.
These operations do not submit simulation commands or rewrite committed authored
page bits.

On resize, show the new page containing the previous first visible product. A
new builder, changed selection or external authored-page change seeds the view
from that source page. Renderer/preference changes discard local paging state.
Compile the immutable list independently of the composed-window cache so page
changes and resizes do not re-extract all source pages. Source windows, catalogs
and committed frames must not be mutated by expansion. Resize, selection/page
changes and renderer/preference switches retire stale retained pointer capture.

Numbered GUI files with zero bytes follow the absent-page DL fallback, matching
the catalog's probe and the TDF loader [02 R-MALF-01 §1]; nonempty malformed
pages retain their errors. A base-install download may raise the page count
even when a mod overlays that page with an empty file [07 R-HUD-03 §6].

Verification covers ordered exact product coverage across physical and generated
pages, empty slots, duplicates, mixed artwork dimensions, short and tall surfaces,
resize anchoring, source identity, factory button identity, custom Orders,
shared input/shortcuts, composite tiling ambiguity, indivisible child groups,
fallback and capture retirement. Installed OTA, ProTA, Twilight and
Zero checks exercise all factions and visually inspect normalized products and
controls. OTA coverage must also enter after the completed front-end transition
and select every builder with authored product pages, including foreign-faction
builders owned by the local player; testing only commanders, the player's own
faction or pre-transition windows misses command-prefix and shortcut differences.
Reject duplicated combined commands. Classic and unsafe
authored/fitted fallback use the existing layout tests. This is a
host UI extension, not a promise to support arbitrary replacement command GUIs.

#### Build page lock

**Nanolathe host presentation policy**, revised with user authorization on
2026-10-01. The UI's **Build items** offers 6 per page, 12 per page and
Free flow; **Orders below build** offers When space permits and Never. Six with
Never keeps the compact build view; the retired Original choice migrates to that
configuration.
Twelve means groups of twelve logical cells, combining short authored pages as
needed. Mod-authored twelve-slot pages naturally retain full-page boundaries
when their ordered lists align; short intermediate source pages no longer force
a short displayed page. Composites count as one logical cell.

ProTA 4.8, TA Zero Alpha 5 and Escalation document or author twelve-slot pages
([ProTA engine](../research/extensions/prota-engine.md),
[Extended build menus](../research/extensions/build-menus.md),
[TAESC engine](../research/extensions/taesc-engine.md)). That evidence supplies
content, not this pagination policy or a Community 3.9 presentation parity claim.

`presentation.buildMenuPageSize` stores 6 or 12 for fixed counts, zero for explicit
Free flow, and negative one for the default content recommendation. An explicit
choice, including Free flow, overrides the mounted mod's `buildMenuPageSize`
metadata and the content profile's `presentation.build_menu_page_size`. If the
player inherits, the mod recommendation wins over the profile; no positive
recommendation selects Free flow. Detached captures inherit content defaults.
Old stored positive values remain fixed sizes; old stored zero now means explicit
Free flow. Missing keys load the content-default sentinel. `sidebarOrders` defaults
to one (When space permits); zero selects Never. Legacy `expandedSidebar: 0`
loads as `expandedSidebar: 1`, six items and Never, including through presets.
Classic retains its authored/fitted layout. Apply, Cancel, Undo and Restore include both
preferences, and the preview uses the resolved content recommendation and the
selected game's logical resolution.

Verification locks six-slot boundary comparisons, whole-row capacity, hiding
orders before reducing a fixed count, cross-source pagination, explicit Free flow
over a mod recommendation, dedicated Orders accessibility, stable partial pages,
resize anchoring, source identities and persisted defaults. Existing stock and
oversized-mod tests check safe geometry and every product's reachability.

#### Rail backdrop

**Nanolathe host presentation policy.** Retail stamps the side's `PANELSIDE`
art once at `(0,0)` and leaves the rail below its 480 rows at palette index 0
[07 R-HUD-05]. That is correct while every rail control sits inside the art,
as every stock page does, and it remains the rule for Classic with stock pages.
When the host layout places rail controls below
the art — the Modern expanded sidebar above, or an oversized page fitted by the
authored-layout path — the art is instead resampled over the whole rail, `(0,0)`
to the surface's bottom edge, without its GAF offsets. Otherwise controls
straddle the edge between panel art and black.

The expanded sidebar selects the stretch whenever it is active, even before a
page is open, so the backdrop does not change with the selection. The
authored-layout path stretches only while the resolved page has an active,
drawable control ending below the art's bottom row. Stretching rather than
tiling or mirroring keeps the art's vertical gradient continuous and leaves
its bottom border at the surface edge, where retail shows it at 640×480;
stock CORE art ends in a purple row that a mirrored or tiled copy would repeat
mid-rail. A surface no taller than the art keeps the retail stamp. The radar,
strips and rail controls draw over the backdrop exactly as before.
With no selected units, the retained side-prefix `MAIN2.GUI` header panel
draws over that backdrop at its native authored origin and size. This reveals
the stock faction emblem without stretching it; the host backdrop remains
visible below the root at taller sizes. No command window is opened and no
command input is admitted for that state `[07 §6]` `[07 R-HUD-05]`.
`TestRailWindowReachesBelowArt` locks the extent test; seeded `--shot`
comparisons against the retail path must match outside the rail columns.

#### Modern UI scale

**Nanolathe host presentation policy** (issue #95), a renderer preference
independent of gameplay selection. Retail draws the chrome at one framebuffer
pixel per authored pixel `[07 R-HUD-05]`, which is small on a 1440- or
2160-row surface. `presentation.uiScale` magnifies the whole battle chrome —
rail, minimap and the top and bottom strips — together. It stores Auto (0,
the default) or a fixed 1 or 2; a larger stored value reads as 2, and a size
stored under the earlier sidebar-only key carries over. Auto is 2x from 1440
rows and 1x below, which always leaves the rail at least the 480 rows every
stock page and the side panel art are authored for, and Auto also stays at 1x
when 2x would leave the bars fewer than the 512 columns right of the rail
their art and readouts are authored for (`hud.MinBarScaleWidth`). A fixed 2x
applies as chosen even when it leaves less; the bottom of a page may then fall
off the rail (`hud.ChromeScale`), and a centred modal too wide for the space
beside the rail is kept on the surface. The scale is resolved at the joined
host presentation boundary, before camera blending and world recording.
Input until the next draw maps the pointer through that value, which is what
the player sees. It is always 1x when the Classic executor may replay the
recording, since that executor ignores the
region markers below, and for captures that crop the chrome at retail's fixed
insets: films, `--shot-renderer both` and Nanolathe screen previews. The
Nanolathe screen's Sidebar card ("UI scale") and the in-battle Nanolathe page
("UI scale: Auto/1x/2x") offer the choice, with the usual Undo and Restore;
`--ui-scale` sets it for one run, captures included.

At scale k the rail — backdrop, side page and the community rotation menu —
is laid out on a virtual surface of the framebuffer size divided by k and
recorded between a pair of world-space markers whose factor is k
(`Client.BeginChromeRegion`). The modern executor magnifies everything
between them with its existing world transform and nearest sampling, so the
art is integer-scaled and blocky by design. The markers are flagged as
chrome, so text inside them — build-queue counts, resource readouts — is
magnified too, where world text keeps native glyphs. Rail layout, including
the expanded sidebar's free-flow capacity, reads the virtual height, so a 2x
rail at 1440 rows has the build capacity of a 720-row surface. Rail input maps
the pointer onto the virtual surface before every gadget test, the widget pass
included. A region's virtual width rounds up, so strips reach the right edge.
At 1x the regions record nothing, so recordings and captures are unchanged.

The top strip, the resource readouts and the community income and weather
readouts under them use the same region as the rail, so PANELTOP's authored
column 129 lands on the magnified rail's edge. The bottom strip, the footer
and the other viewport overlays — slide strip, clock, titles, score panel and
build-power readout — use it moved down by the framebuffer height modulo k,
fixed with the scale at each draw, so the bottom strip ends on the last row.

The minimap is drawn outside the region, at k times its 126-pixel canvas, from
a second radar service whose terrain picture is generated at that size from
the map tiles, built once per battle and scale. Blips and projectile markers
are drawn k times larger on that picture and the viewport rectangle is k
pixels thick. The picture dimensions and letterbox offsets magnify the fitted
canonical rectangle: truncation happens at 126 pixels before magnification,
so every drawn edge pixel belongs to input's fitted radar rectangle.
Radar circles keep one-pixel lines. Minimap input and world
mapping keep the canonical 126-pixel layout through the magnified destination
rectangle, so the retail arithmetic of `[07 §10]` is unchanged.
Hover also converts the pointer to canonical radar pixels before the strict
squared-distance test `[07 R-SEL-02B2]`, so a magnified blip retains the
canonical pick radius.

The camera's chrome insets become 129k-1 on the left, the magnified 129-column
side panel less one column as retail's 128 is, and 32k top and bottom
(`camera.ChromeInsets`). They move the clamp floor, centring, the battle
viewport rectangle, picking clamps, wheel-zoom and on-screen tests, so every
playable pixel stays reachable beside the magnified chrome; changing them
keeps the point at the viewport's centre. A changed inset refreshes both camera
blend endpoints to that centred host camera. The pre-record and paused-world
validity digests include the insets, so neither can reuse a picture with old
viewport bounds. The message column moves with the insets. Centred modals,
the unit information screen and the chat window are
placed beside the wider rail. A save's radar thumbnail rebuilds the canonical
radar, so it is unchanged by the scale.

`TestChromeScale`, `TestChromeInsetWidensTheViewport` and `TestChromeRegion`
lock the scale rule, the insets and the region mapping.

### 3.4 Screens, dispatch and preferences (C16…C18)

**C16 — the front-end subset is resource-driven.** `mainmenu.gui`, `single.gui`,
`newgame.gui`, `selmap.gui`, `skirmish.gui` and `msgbox.gui` are drawn with
their retail PCX, GAF and FNT assets and the shared `PALETTE.PAL` display table
through the retail GUI-to-palette source mapping. Controls use the translated
authored rectangles, the stock staged button, list and scrollbar frames,
release-inside activation, and the retail campaign, map and skirmish callback
rules `[07 "Retail closure for the single-player menu slice"]`
`[07 "Retail frontend control activation and raster rules"]`
`[07 "Retail palette contract"]`.

Opening `MAINMENU` activates its authored `DebugString` label and supplies the
retail literal `v3.1`. Its fresh window rectangle moves left by half the primary
GAF text width (active FNT fallback), with integer division; the cached authored
window stays unchanged so returning to the menu cannot accumulate the shift
`[07 R-FE-01 §3]`. A content profile whose package replaces that literal names
the replacement as `presentation.main_menu_version`, and the same shift
measures it. ProTA 4.8's patch list configures `4.8` as that string, and its
window places the label at x 323 in its own title box
([ProTA engine package](../research/extensions/prota-engine.md#main-menu-version-label)).
The label is then drawn by the label painter below, so the visible text is
narrower than the width the shift measured: stock `v3.1` is drawn from x 307,
which a retail capture of the stock menu shows.

**Label painter.** A label whose `fontnumber` matches one of its window's
kind-7 font records is drawn with that FNT (`drawRetailLabelFNT`); every other
label — all labels of every stock window except `BRIEFING` and `MSNBRIEF` —
takes the painter's GAF branch (`drawRetailLabelGAF`) `[03 R-FONT-01 §6]`. The
branch measures, wraps and draws in GAF-font slot 1, `hattfont11`. With
that slot null it uses the common FNT with no width limit. The pen follows
the label rules shared with the FNT branch (`retailLabelPenX`): right
`gx + w - tw`, else centre `gx + trunc(w/2) - trunc(tw/2)`, else `gx`, with
no inset. An authored x of -1 centres on the panel, and the pen Y is the
label's own y. A label taller than two metrics goes to the wrapper, with a
line pitch of metric + 2 and the height spent per line. Any other label is
drawn on one line limited to its width, so a zero-width label draws nothing:
`NEWGAME`'s `SIDENAME` shows no text. The button painter's three-pixel insets
and vertical centring belong to buttons and the remaining kinds, not to
labels. Nanolathe screens built from authored labels (the Mods & Mutators
text, the option-page headings, the main menu's `MODSTATUS` line) draw
through the same branch, so the `MODSTATUS` centring and `fitDetail`'s line
budget measure in the label face. The in-battle modal windows draw their
labels through the same layout (`drawModalLabelGAF`, sharing
`drawRetailLabelLines`) in the battle HUD's slot 1, clipped to the window's
private surface: the `YESORNO` title and the `RESTART` mission lines are the
visible cases. The three background-bitmap children (`igmbrief`,
`GameSettings`, `dhelp`) use the same branch through `drawBattleInfoLabel`.

While a `MAINMENU` window is in the chain, `menu_sparks.go` runs the background
shimmer: one hundred single-pixel records that spawn in the top 220 rows over
indices whose low nibble is 13 or more, step three pixels along one axis per
frame, turn on a timer, and draw `PALETTE.PAL` index 170 (a dark green). The
records, draw order, parity rules and CRT recurrence follow `[07 §5]`; the
field owns a private copy of that recurrence, so the menu consumes no other
stream. A new `MAINMENU` window starts an empty field, as retail allocates it
zeroed per window. The sparks are recorded as one-pixel fills on that
window's layer, after its authored gadgets and before the Nanolathe-owned
`MODS` button and status line appended to the clone, so those two and any
window stacked above cover them. Two host choices are recorded in §5.

**C17 — preferences survive the process.** Retail reads its whole preference
block once at startup, installing a per-value default for anything absent, and
writes the whole block at its commit points; the per-slot
`Player%d Controller/Side/Color/AllyGroup/Metal/Energy` values live under the
skirmish subkey and every other skirmish value under the main key
`[02 R-KEYS-01 §3]` `[07 R-FE-01 §11]`. Nanolathe keeps the value set, the
defaults and the read-once/write-whole shape and swaps the registry for one JSON
file (`internal/settings`). Persisted: the campaign `Difficulty`,
`SkirmishMap`, `NumSkirmishPlayers`, the six skirmish rule scalars and the ten
skirmish rows, the display block, the message-column configuration, the audio
block (`Sound Mode`, `RestoreVolume`, `ackfx`, `buildfx`, `speechfx`, `fxvol`,
`musicvol`, `MixingBuffers`, `musicmode`, `cdmode`, `unitchat`), `gamespeed`
and `Interface Type` `[03 R-AUD-01 §2]` `[07 R-CAM-01 §7]` `[07 R-CAM-01 §5]`.
Not persisted: the networking identity fields, which are retail values this
engine has no owner for and are deliberately absent rather than written as
invented defaults.

Music category edits and undo snapshots update the audio service's live list;
there is still no cross-launch equivalent of retail's per-disc `CDLISTS` ring.
Sound Mode's Mono-versus-3D choice is applied to the audio device (see
DESIGN_PRESENTATION_CLIENT §2.6). `Interface Type` is consumed by the battle pointer and cursor
paths: a shell battle reads its live in-memory stage, and a direct battle copies
the loaded stage at entry, with no per-frame preferences read `[07 R-CAM-01
§5]`. Fresh campaign and skirmish battles copy the shell's live `gamespeed`
into both scheduler speed words at client installation; direct entry uses its
loaded preference block. The front-end slider therefore supplies the next
battle's speed, and the in-battle arm applies changes immediately. Save loads
retain their restored scheduler and the isolated Settings preview keeps its own
speed `[08 R-ENTRY-01 §3]`.

**C18 — the in-battle modal chain.** An empty selection closes the command
window and its input; the retained side-authored `<prefix>main2.gui` root
supplies the empty rail artwork `[07 §6]`. In a
non-network battle the options window sets the single-player pause state and
draws `igtitles.gaf:igpaused` at the live view centre using its authored GAF
offsets `[07 R-HUD-05 "Centred in the view"]`; closing it unpauses. `EXIT`
pushes `exitmenu.gui`, whose Main Menu and Exit Game choices push `yesorno.gui`
and commit only on `CHOICE1`, with both Enter and Escape bound to `CHOICE2` and
focus on it. The options window keeps its authored origin, while the `0x1000`
modal flag centres Exit and Yes/No in the playfield to the right of the
128-pixel rail. The `MISSION` gadget is **relabelled** to the translated
`Settings` for a skirmish; it is never hidden or greyed. It and `HELP` open the
three read-only children of C18.1. `PREFS` opens the options root as a child window over `ARMOPT`, which
stays on the chain underneath with its pause bit still set; the root's `PREV`
("OK") saves the whole preference block and `CANCEL` restores the entry
snapshot, and either one returns to `ARMOPT` rather than to the battle. Escape
takes the same route, because `PREFS.GUI` authors `escdefault=PREV`. Each modal
renders into an exactly sized clipped surface whose background resolves through
the common `BackTile` as a nine-slice fill, and labels use the primary GAF font
rather than the side FNT `[07 R-FE-01 §6]` `[07 R-FE-01 §7]` `[07 R-FE-02 §4]`
`[08 R-SKIR-01 §11]`.

**C18.1 — `MISSION` and `HELP`, the options window's read-only children.**
`MISSION` opens `BRIEFING.GUI` in a campaign mission and `GAMEOPTIONS.GUI` in
every other session kind; `HELP` opens `HELP.GUI`. All three take the same
place on the chain `PREFS` takes: they open over the surviving options root,
which keeps the pause bit it set, and each one's `OK` — and Escape, the chain's
back transition — returns to that root rather than to the battle
`[07 R-FE-01 §7]` `[07 R-WGT-01 §1]`. `ui.BattleState` owns the branch: the
composition root records the session kind on the state when the options window
opens, and `Activate("MISSION")` selects the child from it, so the routing is
testable without any asset.
The same owner requests `Options` for each child's recognised page control
and `OK`, before reporting a page action or closing the child. Unknown names
and cleanup without a fired gadget make no request. The audio service retains
its own playback gates; a request is not proof of audible output
`[07 R-FE-01 §7]` `[03 R-AUD-01 §2]`.

`battle_info_window.go` owns the shared half — the parsed records, the
retained widget state, the indexed pointer service and the painter — and the
three per-window files own their content. The windows print their rows as
appended kind-5 labels at the authored geometry of `[07 R-FE-01 §7]`, and each
refill first truncates the record set back to the authored count the loader
produced, which is what the `HELP` page filler's saved gadget count does. The
`Page` three-stage button advances its own stage and the window then reloads at
the page that stage names; `GAMEOPTIONS` reads its nine session words once, at
open, because the overlay never refreshes them `[08 R-SKIR-01 §11]`. The
in-battle briefing reuses the campaign briefing screen's own controller — the
wrapper, the blink pre-pass and the pager — over the same mission text, and
clears the inert-label attribute bit on `MOREBAR` and `TextRegion` so either
one pages it; it starts no narration, because the shared text installer's
narration request is gated on the host mode word a battle runs under
`[07 R-FE-01 §4]` `[07 R-FE-02 §2]`.

These three are the only battle children that install an authored background
bitmap — `igmbrief`, `GameSettings`, `dhelp` — which replaces the window's
panel fill, so they have their own painter rather than the shared modal one.
It blits the bitmap at the window origin clipped to the window rectangle and
otherwise uses the battle modal family's art chain.

Its labels go through the shared **kind-5 label painter**
(`drawBattleInfoLabel`, C16): a label has no 3-pixel inset and no
vertical centring — its pen y is the gadget's own y — and its branch is
decided by whether the `fontnumber` walk *matched* a kind-7 record, not by
whether that record's file loaded. A match draws through the FNT drawer with
the limit dropped; no match takes the GAF branch (`drawModalLabelGAF`): GAF
slot 1 (`hattfont11`) with the limit set to the gadget width
`[03 R-FONT-01 §6]`. Neither `GAMEOPTIONS.GUI` nor `HELP.GUI` authors a font
record, so every printed row takes the GAF branch, and a row wider than its
column is truncated at the column edge. In that face no stock `help.tdf` row
or `GAMEOPTIONS` name is wider than its column; in `hattfont12` four help
descriptions and three option names were. Their rows are
**left-aligned**: both openers rewrite every appended record's attribute word
to 1 after the append helper stored 2, and this build's helper stores the
value that survives that rewrite. Getting this wrong is visible — a centred
row overruns its column on the left and then loses its tail to the same width
limit — so a test locks the attribute word.

Both of this section's gaps are closed. Every `ARMOPT` button now plays the
`Options` cue before its route runs, `OK` included `[07 R-FE-01 §7]`:
`ui.BattleState` emits the alias from the one authored-state owner, before the
route, through the same cue sink the rail detents use, and the composition root
turns it into the ordinary interface cue request — no sound is reached from the
UI layer `[07 R-WGT-01 §3]`. And `--shot-modal` opens all three children
(§2.9), so a capture no longer needs the tests' own composition path.

The in-battle options window has its own pointer pass (`battle_options.go`)
rather than borrowing the front end's. The reason is the capture rule of
`[07 R-WGT-01 §1]`: a press goes to the first gadget in index order whose
handler accepts it, and a picture box has no handler at all — it blits its frame
and returns `[07 R-WGT-01 §8]`. `PREFS.GUI` authors its whole rail plate as a
picture box at index 1, in front of every button on the window, and each
`…RT.GUI` page authors another over its own column, so a press test that admits
every kind but the panel would hand every click to a plate. The pass owns only
the capture rule; every semantic action it reaches — the page merge, the slider
callbacks, `RESTORE`, `UNDO`, the snapshot restore, the cue column — is the same
routine the front-end pump calls. Its painter (`battle_options_draw.go`)
likewise exists for one reason: those plates are picture boxes whose art
resolves own GAF then **common** GAF `[07 R-WGT-01 §12]`, and the whole
in-battle family's plates (`IGOPT`, `SOUNDSRT`, `MUSICRT`, `SPEEDSRT`,
`VISUALSRT`) live in the common GAF, which the front-end picture path does not
consult.

A page write in battle reaches the running session as well as the stored block:
`GAME` goes through the session's speed setter at once, announcement included
`[07 R-CAM-01 §3]`; `SCREEN` re-primes the camera's cached scroll byte
`[07 §10]`; `TXTSCROL` and `MAXLINES` reconfigure the live message column
`[07 R-HUD-03 §14.3]`; the audio gauges and the display-option bits already went
straight to the backend and the client. `CANCEL` re-applies all of them from the
entry snapshot, the same way it re-applies gamma and the volumes.

#### 3.4.1 Nanolathe options

This is a Nanolathe extension authorized by the user, not a retail finding.
The fifth options category, **Nanolathe**, sits one authored category spacing
below Visuals in PREFS, the battle's options. The front end's STARTOPT leaves
it out: there the Nanolathe screen (§3.17) owns these choices, and the
Community pages move up one spacing. The engine adds its gadget and builds
a page from the VISUALS canvas, label style and control dimensions. The front
end uses the original options background; battle uses the game's tiled window background.
BUTTONS0 and stagebuttn2/3 from the game assets supply the buttons and controls.
No retail asset is copied into the repository or changed on disk.

The page contains a captioned Gameplay (Strict 3.1 / Community 3.9 / Modern)
row. Compact Renderer (Classic / Modern), FPS (30 / 60 / 120), Sidebar (6 / Flow),
Camera Zoom (Smooth / Steps / Off), Modern Icons (Modern / Community 3.9),
Radar dots (No dots / Visible dots / Attackable dots), Glow, Water, Lights,
Metal, Heat and Marks controls carry their own names.
The icon button abbreviates Community as Comm to fit the authored font; its
help spells out the full name. The captioned row uses a tight pitch and the
switches sit directly together at their authored height, so the
page fits the in-battle column as well as the front-end one. Restore Defaults
and Undo Changes retain their authored dimensions with a clear gap after the
preferences and between each other. Zoom, icons and radar dots preview live and share the
ordinary page Undo, defaults, Cancel and persistence transactions. Camera zoom
and icons are available under Modern and Community camera controls; radar dots
retain their rule boundary. DESIGN_GPU_RENDERER §16.6 and §18.7 own the camera
controls. The main-menu Controls screen's Mouse table scrolls when these and
the existing rows exceed its height.

Gameplay defaults to Modern, independently of the renderer, and follows
DESIGN_WEAPONS_PROJECTILES §2.3.1. The remaining controls default to Modern,
60 FPS and every effect switch on. These are presentation
choices; simulation remains 30 Hz. The cap bounds modern presentation on the
display's refresh grid; classic still presents at 30 Hz. Higher or refresh-following
values remain available through `--fps`; a value outside the presets is
displayed as stored.

The six buttons select the Enhanced effects the modern executor draws. Glow is
one switch; the other five are family shortcuts over the independent switches
listed with what each gates in [DESIGN_GPU_RENDERER.md](DESIGN_GPU_RENDERER.md)
§30, which the Nanolathe screen (§3.17) offers one by one. A shortcut is not a
stored value: it shows On while any switch of its family is on, and a press
writes every switch of the family to the new stage. Water is the surface,
motion, foam and reflections switches; Lights the model and ground light; Metal
the finishes only (the glint is its own switch and no shortcut's); Heat the
blast rings, fire shimmer, wreck glow and wreck shimmer; Marks the scorch switch
and the trail strength, which a press off sets to 0 and a press on restores to
the default when it is 0. The glint, the aircraft soft shadows and the model
supersampling (Smooth edges on the Nanolathe screen's Effects page,
DESIGN_GPU_RENDERER §17.5) belong to no shortcut, and no shortcut moves the ground light or blast ring strength.
Glow edits the display block, where §19.4 already put it, so it previews through
the same live path the Visuals rows use; the other five edit the presentation
block, which the host polls each update. Classic presents identically whatever
they say. The same five shortcuts run from the message line as `+water`,
`+lights`, `+finish`, `+heat` and `+marks`, beside the existing `+glow`, each
persisting the switches it wrote the way the display-bit commands do.

The legacy options page's Sidebar shortcut chooses six items with Never or
Free flow with When space permits. Both use the modern composition in §3.3;
Classic always uses the authored page.

Edits preview immediately; gameplay changes enqueue a typed command for the
next simulation boundary. OK saves gameplay and the presentation block with the existing
settings transaction; Cancel restores the entry values, Undo restores this
page — including its glow bit, every switch its shortcuts write and the trail
strength — and Restore Defaults chooses Modern / 60 with every effect on. F10 updates the shell and saves
only the renderer field, preserving other pending preferences. The adapter polls
the live shell preference and shares executor-swap cleanup with F10. A saved
renderer also controls subsequent battle loading and detail-art preparation.
Explicit `--renderer` / `--fps` override saved values at window startup;
captures and benchmarks retain deterministic command-line defaults without
reading preferences. Older settings files acquire Modern / 60 through decoding
over defaults, with no schema version bump.

### 3.5 The pointer and latch state machine

`BattleState.PlacementArmed` requires both MOBILEBUILD and a selected product.
Changing to another latch clears placement state. The same gate controls the
ghost, cursor and build dispatch. Page input updates local interface state
immediately, so multiple keys before a tick preserve page order. Authored-page
next/previous requests cue even without a live page owner; accepted digit-page
requests cue even when that page is already shown, while rejected digit
requests are silent `[07 R-HUD-03 §6]`. These are sound-service requests,
subject to playback admission. Expanded-sidebar navigation keeps its host
policy of cueing only actual local page changes (§3.3). Palette painting and
activation share hidden/grey product and arrow decisions, and hit rectangles
end at the authored last pixel. Feature commands read mapping memory at the
projected pointer, independently of current feature visibility.

The active command window's GUI service runs before the battlefield paths
below. A down inside its inclusive window rectangle belongs to that service,
even on blank space or a greyed/hidden control; gadget capture is not the
admission test. Right-down there preserves an armed latch or placement. An
admitted factory product activation still subtracts the signed batch. The
same ownership applies to both interface types and while paused `[07 §3]`
`[07 R-P0-11 §1]`.

For a pointer record left available by that service, one press/release pair
is routed through exactly one path, chosen on the **press** edge. In order:

1. **Over the fitted minimap lens.** Under the default `Interface Type 0`
   polarity, a right down edge sets the minimap camera capture. The frame that
   set it does not jump; each following host frame tests the already-set
   capture before new clicks and re-runs the camera jump from the live pointer
   record until the matching right up edge. The signed lens conversion runs
   before camera clamp even after the drag leaves the lens. Left down issues
   the armed order or world click at the lens point. `Interface Type 1` swaps
   those two **idle** minimap buttons. An armed order or placement remains a
   left-click action in either mode, so Type 1's idle camera button cannot
   capture it. The canvas letterbox bars suppress a viewport
   drag but are not lens/world-pointer input; cursor, footer and command paths
   share that classification `[07 R-CAM-01 §5]` `[07 R-CAM-01 §11]` `[07 §8]`.
   Its unit hover uses the admitted radar contacts in pool order and the nearest
   projected point within squared pixel distance `< 4`. Admission uses the
   committed contact's sensor/ownership flags and radar options, shared with
   the megamap; it excludes unknown enemies without dropping radar-only
   contacts. The regular blip's damage blink does not affect hover membership.
   The viewport's direct visibility gate does not apply here, so a radar-only
   contact can be the target of an armed attack `[03 §3.9]` `[07 R-SEL-02B2]`.
2. **Right button, anywhere else.** Under `Interface Type 0`, right is
   deselect and cancel only. Under `Interface Type 1`, an idle right-down in
   the viewport issues the contextual order; an armed placement or latch still
   cancels on right. A counted build-page button is the one exception — a
   factory product, or a `MAKENUKE`/`MAKEANTI` stockpile toy, which reaches the
   same counted producer and only routes to the `BUILDWEAPON` descriptor
   instead — subtracting one or five from the matching record (twenty for a
   factory product under the Alt extension in §5, which the stockpile toys are
   scoped out of). Thus only Type 1's idle viewport
   path queues a right-button order `[07 R-CAM-01 §5]` `[07 §9]` `[04 §3.4]`
   `[07 R-P0-11 §1]`.
3. **Left press that lands on chrome** takes the HUD capture and records the
   press point. The matching release activates only when the *same* authored
   gadget is under both endpoints, so a drag across the rail cannot fire a
   different control; either way the capture ends and the release never reaches
   the world `[07 §3]` `[07 R-WGT-01 §1]`.
4. **Left press while placement is armed** takes the placement capture and owns
   the button until release. An illegal site queues nothing, plays the refusal
   cue and stays armed; a legal site commits, plays the confirmation cue, and
   then either disarms or — under Shift — stays armed so the next click places
   another copy, dropping back to idle as soon as Shift is released. A rejected
   command is a failed commit, not an armed state `[07 §9]` `[07 R-P0-11 §4]`.
5. **Armed world press.** An armed latch enters the world-click handler on
   left press; it does not wait for idle drag classification. The handler
   dispatches the latch and returns to idle unless Shift keeps it
   `[07 R-CAM-01 §14]`.
6. **Otherwise the idle world drag.** Save the live scaled clock and both
   ground-resolved whole-world endpoints — three components each, height
   included — on press. Held passes re-pick the moving endpoint's world point;
   release classifies the stored endpoints without refreshing them. No screen
   pair is kept: the band is those two world points projected whenever it is
   needed, which is what keeps it over the terrain while the camera moves
   `[07 §9]`.
   A click requires each X/Z displacement strictly below 32 and the current live
   clock strictly below the wrapped press-plus-25 deadline under signed 32-bit
   comparison. Use the clock's wrapped multiply-before-divide arithmetic, not
   event timestamps or simulation ticks. A successful click acts at the current
   release position: select an eligible unit, issue contextual code 1 when a
   selection exists, or clear selection unless Shift is held. Type 1's idle
   left click only selects or clears; its contextual order uses right-down.
   Failed classification performs box selection, replacing or toggling according
   to the modifier `[07 R-CAM-01 §14]`.

Two rules cut across the machine. A latch held by Shift retires on the live
Shift-up whatever the order family `[07 R-P0-11 §4]`. Escape returns an armed
latch or placement to idle, and an already-idle latch deselects everything
`[07 R-CAM-01 §2]`.

### 3.6 The keyboard table

Retail folds Ctrl into letter, digit and function-key tokens, so a composed
token cannot reach an unmodified key's case. The platform producer queues those
compositions and all special keys alongside translated text. After GUI service,
the battle consumes one unclaimed token and retains its queued tail. The
controller sample carries that exact token independently of live modifiers:
literal case selects `n` versus `N` and `t` versus `T`, while group recall still
queries live Shift and Alt. A production pass with no token has no shortcut
press edges. Hand-authored controller fixtures may still supply physical edges.
The rows below are the battle hotkey census
`[07 R-CAM-01 §2]` `[07 R-CAM-01 §14]`.

| Keys | Effect |
|---|---|
| F2 | open and close the options window |
| Tab | the same, except on an already paused battle with no modal open, where host policy resumes it directly instead — see §5, "Tab resumes an already paused battle"; with the Megamap overview a released Tab toggles the megamap instead (§3.15) |
| Escape | close the options window, else cancel the latch, else deselect all |
| `` ` `` `~` and Shift+1/3/8 (`!` `#` `*`) | flip the "label every unit" bit `[07 R-HUD-03 §7]` |
| `+` `=` / `-` `_` | game speed up and down, with the ring announcement `[07 R-CAM-01 §3]` |
| `,` / `.` | previous and next build page, with the page cue |
| 1–9 | the `SwitchAlt` mux: build page or group recall, the recall playing `SelectSquad` |
| Ctrl+1–9 | assign the control group, playing `CreateSquad`; Ctrl+0 has no case |
| `t` / `T` | follow the next or previous selected unit; neither moves the camera itself |
| `n` | glide to the next unvisited own unit, changing no selection; `N` has no case |
| Ctrl+A | select every own selectable unit, additively |
| Ctrl+C | the `CTRL_C` category select, then follow the commander |
| Ctrl+D | toggle: cancel the selection's self-destruct countdowns, or start them when none is running `[07 R-CAM-01 §2]` |
| Ctrl+B, E..R, T..Y | the `CTRL_%c` category selects |
| Ctrl+S | select the own selectable units on screen, replacing |
| Ctrl+Z | select every own unit sharing a selected definition |
| Ctrl+F5..F8 / F5..F8 | store and recall camera bookmarks 0..3, both playing `SelectSquad` |
| F1 without Shift | open `UNITINFOx.GUI` for the hovered unit or the hovered build button's product |
| F3 | glide to the message source |
| F4 | the interface-flags bit that pins the score panel open and arms the kill/loss flash `[07 R-HUD-04 §1]` |
| F12 | clear the message ring |
| Pause | toggle pause |

Space and the arrows have no ring case: the arrows are the scroll pass's
held-key queries. The order latch keys `m a p r e c g d x o` have no row in the
census either — retail reaches them through the command palette's visible,
authored gadget quick keys. The palette consumes an admitted ordered text token
before this residual table and activates that exact gadget record through the
same callback path as a pointer release. The production viewer services the
original token ring and published pointer together in one indexed GUI pass,
then carries its pointer-ownership verdict into the controller. A claimed
token prevents residual shortcut dispatch for that pass; held modifiers and
unclaimed pointer work remain available. A linked-label shortcut consumes its
token before resolving its target. An admitted target fires after focus setup,
including a non-button; an absent target fires the label itself. A rejected
target clears the result and permits later gadget visits. Ctrl-composed quickkeys
retain their own authored byte identities, including through a battle child's
function-key suppression filter. UNITINFO ownership is captured at
frame entry, so closing it cannot service the underlying palette again in
that frame. Closing or changing the selected command window retires the old
panel's pointer capture, and cycle controls obtain the resolved art-frame
count through the same installed-art resolver used by painting. No held-key
fallback supplies a palette command `[07 §2]` `[07 §9]` `[07 R-WGT-01 §3]` `[07 R-HUD-03 §6]`.
`\`, `Ctrl+F10` and
F11 are developer mode, `Ctrl+F9` is a screenshot with no in-battle writer,
`h` is the multiplayer share dialog and remains out of scope. Unclaimed Enter
opens the single-player TALK editor through partial I10 (§3.9)
`[07 §5 "Chat"]` `[07 R-CAM-01 §9]`.

An absent `CTRL_%c` category supplies an empty membership set: its shortcut
clears the selection unless Shift requests an additive selection. Camera
capture still services a queued shortcut after its pointer work. Modern command
gestures treat a queued token as superseding input even when there is no new
physical edge. The plain F9/F10 presentation extensions leave their Ctrl
compositions to the currently unimplemented retail developer paths; their
planned activation is owned by DESIGN_DEVELOPER_TOOLS.

#### Rebinding

**Policy.** Keyboard rebinding is a Nanolathe host input preference, built as
a prototype on its own branch. It is presentation only: it adds no
`gameplay.Mode` term or `RuleSet` seam, never reads the gameplay mode or the
content profile, and enters no digest, fingerprint or save [I6]. The retail
profile with nothing rebound is the identity. Every token reaches the battle
unchanged, and `TestRetailKeyMapIsIdentity` locks that over the whole
catalogue.

**Chords.** A binding is an `input.Chord`: a key with Ctrl and Shift. Alt is
never part of one, because retail folds Alt+key into the plain key's token
(Alt's system key-down pushes the raw character) `[07 R-CAM-01 §14]`. Group
recall reads Alt live, as before. Ctrl composes only letters, digits and
function keys, and a composed token carries no Shift. So Ctrl+Shift+B is
Ctrl+B, and its Shift stays the live additive modifier the handler reads.
Shift is visible only as a character's case or shifted symbol. The producer
queues the translated character, which is read on the US layout the retail
install assumes: Shift+1 is `!`. Shift with a function key, an edit key or
Space is the same token as the plain key. `Chord.Normalize` folds a captured
key press onto the chord the token stream can tell apart. `ParseChord`
refuses the rest (`alt+a`, `ctrl+tab`, `shift+f5`, `ctrl+shift+a`). The
written form is lower case with `+` separators (`ctrl+a`, `shift+t`, `f5`,
`,`; `!` or `+` may stand for their shifted keys). The displayed form is
`Ctrl+A`, `Shift+T`, `F5`, `!`.

**Catalogue.** `input.Actions()` lists every key the rewrite knows, from
evidence only.

| Group | Actions and retail keys | Evidence |
|---|---|---|
| Orders | Move `m`, Attack `a`, Patrol `p`, Guard `g`, Stop `s`, Repair `r`, Reclaim `e`, Capture `c`, Load `l`, Unload `u`, D-gun `d`, Fire orders `f`, Move orders `v`, On/off `x`, Cloak `k`, Orders page `o`, Build page `b` | the command palette's quick keys, identical in stock `ARMGEN.GUI` and `CORGEN.GUI` (gadgets `MOVE`…`BUILD`, `DEFEND`, `BLAST`, `FIREORD`, `MOVEORD`, `ONOFF`); `TestRetailOrderKeysMatchAuthoredPalette` reads both windows from the install `[07 R-WGT-01 §3]` |
| Orders | Previous / next build page `,` / `.`, plus this build's Page Down / Page Up; Self-destruct Ctrl+D | census rows above; the page keys' host extras in `handleBattleShortcuts` |
| Selection | Select all Ctrl+A, commander Ctrl+C, on screen Ctrl+S, same type Ctrl+Z; `CTRL_B`, `CTRL_E`…`CTRL_R`, `CTRL_T`…`CTRL_Y` category selects on their Ctrl letters | census rows `[07 R-CAM-01 §2]` |
| Camera | Follow next / previous `t` / `T`; next unvisited `n`; message source F3; store bookmarks 1–4 Ctrl+F5–F8 and recall F5–F8; zoom step F9 | census rows; F9 is the host extension of §3.8 |
| Game | Options F2; options, resume or megamap Tab; Pause; speed `+` `=` / `-` `_`; label every unit `` ` `` `~` `!` `#` `*`; unit information F1; score panel F4; clear messages F12; chat Enter; switch renderer F10 | census rows; Enter opens TALK (§3.9); F10 is the host extension of DESIGN_GPU_RENDERER §14.6 |
| Fixed | Recall group or build page 1–9; assign group Ctrl+1–9; cancel or deselect Escape; scroll arrows; slide the rails Space (held) | shown for reference, never rebound |

Fixed keys are held-key queries (arrows, Space), modifier-read multiplexers
(the `SwitchAlt` digits with live Shift and Alt), or Escape. Escape is also
the options window's and TALK's own cancel key, so rebinding it on the
residual path alone would split its meaning. No action may take a Fixed key.
Options (F2) and Unit information (F1) act only with Shift up, which the
rewrite cannot change, so a shifted chord is never bound to them.
The developer keys (`\`, Ctrl+F9, Ctrl+F10, F11) and the out-of-scope `h`
are not catalogued. They pass through unchanged, and binding an action to
one of them takes the key.

**Translation.** `KeyMap.Translate` reads the token's chord.

* A chord bound to an action becomes that action's retail token. A press
  that is already one of the action's retail keys passes unchanged.
  Otherwise the token is the first default whose Shift matches the press,
  or the first default. So a speed key bound to `j` emits `=` and bound to
  `J` emits `+`.
* A chord that is some action's retail key, but reaches no action now, is
  dropped. Moving Attack from A to Q stops A from attacking.
* Any other token passes unchanged.

The palette matches its quick keys without regard to case and reads Shift
live `[07 R-WGT-01 §3]`. An order action is therefore marked `AnyShift`:
binding `q` also answers Shift+Q, that press emits `A`, and both A and
Shift+A are dropped once Attack leaves them. An explicit binding of the
shifted letter to another action outranks this.

A chord belongs to one action. `Rebind` takes each chord from its previous
owner and returns those owners. `Reset` restores the profile's keys and takes
them back from any action holding one. An empty binding leaves the action
unbound. A request made only of Fixed or indistinguishable keys changes
nothing.

**Where it applies.** Only in the battle, and only on the shortcut token
path. In `viewerStep`'s ordinary frame the hook `serviceKeyMap` rewrites the
queued head token in place (`KeyMap.TranslatePending`). It runs after
UNITINFO's authored keys and the Zero drag-filter key have seen the physical
key, and before the command palette, TALK's opener and the residual hotkey
table read it. A dropped token counts as claimed, exactly like a consumed
one, so the next queued token waits for the next pass. The head is always
consumed in that frame, so no token is translated twice. The hook is
skipped while a text editor has the keyboard: TALK owns its whole frame
before the hook is reached, and `textEntryActive` also covers a focused text
field.

Inside the options window only a key bound to the two options actions is
rewritten, and only while the window's own F2/Tab toggle would act on it.
The rebound key then closes what it opened, and every other key reaches the
authored controls as the physical key. As a consequence, an unbound F2 or Tab
still closes the window from inside. Front-end menus, the Nanolathe screen
and battles without a shell (captures, benchmarks, headless runs) play the
retail keys. Held-key queries are never remapped: arrow scrolling, the rail
slide's Space, live Shift, Ctrl and Alt, and the Zero scheme's W/B/Y drag
filters (§3.13). The Shift+arrow page keys therefore keep working whatever
the page actions are bound to.

**Profiles.** Retail is the retail keys. Community is the same keys: ProTA's
documented controls change what Ctrl+B, Ctrl+F and Ctrl+S do, not which
keys do it, and those behaviours are already the Community selection scheme
of §3.13
([ProTA hotkey audit](../research/extensions/prota-engine.md#shipped-selection-and-hotkey-audit)).
Zero adds `z` as a second previous-build-page key
([TA Zero documented behavior](../research/extensions/ta-zero-engine.md#documented-engine-level-behavior)),
so under Zero `z` emits `,`.

**Settings.** The shell holds an `*input.KeyMap` built from the settings key
`keyBindings` (`settings.KeyBindings`: `profile` and `bindings`, a map from
action to written chords). On load, unknown actions and unreadable chords
are dropped. A list whose chords all fail to read keeps the profile's keys,
while a list saved empty unbinds the action. On save only the actions that
differ from the profile are written, and a retail block with nothing rebound
is omitted from the file. A content reload carries the map through the same
capture and apply.

The controls presets gain a *Keyboard* row (Retail 0, Community 1, Zero 2).
Applying a preset selects its profile and keeps every rebound action, and
restoring from a preset returns the row to Retail the way every other row
returns. A settings screen edits `gameShell.liveKeyMap()` in place, captures
a key with `keyCaptureChord`, which maps it through `ebitenapp.PortableKey`
and `Chord.Normalize`, and can name a chord's current owner with
`KeyMap.Owner`.

**Limitations.** A content's own palette quick keys play under the retail
profile, because an uncatalogued letter passes through unchanged. A rebound
order emits the retail-authored letter. Under content that authors different
quick keys, that letter reaches whichever gadget the content binds to it (or
none), and the content's own letter still passes through unchanged. This is
an explicit limitation, not an inference about the content.

Rebinding the Tab action moves the megamap toggle's press, but the megamap
still waits for the physical Tab to be released (§3.15). A rebound key
therefore toggles on its press. Key capture reads key positions and tokens
read characters, so on a non-US layout a captured letter or symbol can
differ from the character the key types.

**Tests.** `internal/input` locks the identity over the catalogue,
`TestRebindMovesActionAndSuppressesOldKey`,
`TestRebindDisplacesTheChordsOwner`, `TestZeroProfileZPagesBack` and
`TestParseChordRoundTrip`. `cmd/nanolathe` locks
`TestKeyMapBypassedWhileTalkOpen`, `TestKeyBindingsSettingsRoundTrip`,
`TestControlsPresetSelectsKeyboardProfile`, `TestKeyCaptureChord` and the
retail-tier `TestRetailOrderKeysMatchAuthoredPalette`. The TALK test types a
retail order key and a rebound one into the chat line and checks that both
arrive unchanged. Outside TALK it checks that the unbound F12 is dropped and
the rebound Home clears the ring.

### 3.7 The command dispatch boundary

Every gesture that changes authoritative state becomes one or more
`session.HumanCommand` values and goes through `Session.EnqueueHumanCommand`
or its receipt-returning form. The value is immutable at the boundary: the enqueue copies handle slices and
strings, so a caller may reuse its buffers, and it assigns the sequence and the
due tick — the next session tick, because no input-delay constant exists
`[08 "Soft pacing — no per-tick input barrier"]`. The kinds are selection
replace, toggle and clear; order; stop; activation; mobile build; factory build;
cancel production; stockpile; build page; group assign and recall; stance; and
self-destruct, which needs its own kind because Ctrl+D resolves a descriptor by
name and the order kind's codes are the latch bytes, none of which is
self-destruct `[04 R-ORD-01 §2]` `[07 R-CAM-01 §2]`.

The consequences of that shape are the contract:

* **Presentation resolves, the session validates.** A build page arrives as an
  absolute zero-based page and the boundary clamps it against the compiled page
  count; a stance arrives as the next value computed from the published
  three-bit panel field and the boundary owns the broadcast and the definition
  gate; a group recall carries the `CTRL_F` mask when one has been published and
  an all-zero mask means no filter `[07 §9]` `[04 R-STANCE-01 §2]`.
* **A mobile build carries its validated site.** The world X, Z and the
  validated site height cross together, because the ghost's verdict and the
  command's site must be the same point `[07 §9]`.
* **A repeat click at an already-queued point removes it.** The world-click
  producer runs the match test — kind, target and goal — over the queue before
  appending, so a second Shift-click on the same target cancels rather than
  duplicating. The ground and air forms of an order are distinct and never match
  each other `[07 R-P0-11 §6]`.
* **The UI never mutates a live flag.** Deselect, group assign and page change
  are all typed commands; nothing in `cmd/nanolathe` writes a unit's selection
  bit directly [I6].

#### Modern submerged wreck picking

**Nanolathe Modern policy (user-authorized 2026-10-09, issue #108).** Retail
resolves a pointer against `max(terrain height, sea level)` and probes the
resulting attribute cell for a feature [07 §8]. A sinking wreck keeps its
stamped footprint but renders at its changing instance height [05 "Feature
sinking and water interaction"][03 R-RAST-01 §6]. Consequently its visible
body can lie south of its clickable water-surface footprint. Strict 3.1 and
Community 3.9 retain that ground-cell behavior.

Modern additionally picks a reclaimable 3D corpse below sea level against
the projected authored body faces at its committed position and orientation.
The existing `orders.Rules.PicksSubmergedWrecks` decision selects the policy;
unbound fixtures answer false. No renderer setting selects it. The model
source, parent translations, root angles, handedness and camera projection are
shared with drawing. Positive-area front faces include their edges; selection
plates, attachment points, missing models and degenerate faces do not admit a
hit. This is geometric picking, without texture-alpha or raster-edge sampling.

The policy is confined to the main viewport, outside the megamap and HUD.
A directly picked unit retains its ordinary unit and ground-feature results.
Otherwise the first admissible wreck in committed feature order wins a model
overlap, ahead of a different feature at the pointer's water-surface cell.
Admission requires the existing feature display-visibility predicate and
mapped history at the wreck's footprint centre. Hidden wrecks acquire no new
targeting path. At or above sea level, sprite features and non-corpse scenery
continue through the ordinary cell probe. The water-surface footprint remains
a valid fallback when no projected wreck wins. If the ordinary probe already
names that same wreck, it keeps its original point and command behavior.

Cursor, footer and command picking use `battle_feature_pick.go`'s shared hit.
Armed RECLAIM receives a corrected footprint centre at sea-level height. A
contextual click whose feature point needs that correction captures only the
selected actors whose ordinary resolver answers feature reclaim or resurrection
and gives them the centre; other selected units retain their existing orders,
as on a Modern area work gesture. A pure-mover contextual click and explicit
MOVE, attack and other ground orders retain the raw ground-resolved point.
The resulting typed command uses the existing resolver, order queue, feature
lookup and work/payout paths; it introduces no wire fields, per-tick state,
save state or RNG draws, and does not change costs, work cadence or rewards.

**Verification.** Authored fixtures lock submerged versus surface picking in
all reserved modes, both interface types, native/detail and fractional zoom,
queued reclaim, cursor/dispatch agreement, unit priority, mapping/visibility
refusal, omitted geometry, mixed contextual work selections, pure-mover clicks,
and unchanged explicit ground orders. Geometry
checks cover parent translations, rotated faces, selection-plate exclusion,
edge inclusion and front-face admission. Picking and paused command admission
preserve both RNG streams and resource stocks; ordinary completion from the
submitted footprint point pays the feature's authored pool once. A retail
close-up capture verifies that a rendered ship wreck is picked at its body.

### 3.8 `[F-P1-008]` — presentation-only zoom

`[F-P1-008]` is `camera.Scale`, and it is not a retail concept. Retail has one
world scale; this build adds a presentation zoom so a capture or an inspection
can magnify the composed frame. It changes no authoritative state, is never read
by a simulation phase, and is not saved [I6].

* **The two values.** `Scale` is a `camera.ViewScale`, the RECORD step:
  `ViewScaleNative` (2, also the zero value's meaning) and `ViewScaleDetail`
  (4, 2×), retaining the denominator-two encoding. `Zoom` is a `camera.Zoom`,
  the LIVE factor in 1/1024 units, free between the map-derived floor and 2×; a zero
  `Zoom` reads as the step's own factor. Both were once a fractional `float32`
  clamped to `[0.25, 4]`; DESIGN_GPU_RENDERER §14 made the projection an
  integer so it and its inverse are exact, and §16 put the free factor back on
  top of that integer projection rather than in place of it. `Project` is
  `ceil(v·f)`, its inverse `floor(v/f)`, and `Px` scales an extent with
  half-away rounding; at native and detail factors the free arithmetic and the
  step's own agree exactly.
* **What each one drives.** The step drives the RECORDING: `WorldToScreen`, the
  terrain record and the art variant selection. The factor drives everything
  that measures the view in world pixels — `EffectiveView`, `clampInsets`,
  `BattleView`, `Drag` and `ScreenToWorld` — because those describe what is on
  screen. `ScreenToRecord` bridges the two for the hover hull, which compares a
  pointer against corners projected at the step; the drag band needs no bridge,
  because it is projected from world points rather than from pointer pixels.
* **Arrow-key and edge scroll speed.** Both apply the existing setting, host-time
  delta and signed cap in screen pixels, then divide by the live zoom before
  moving the camera. Fractional map pixels carry per axis while zoom is steady;
  a zoom change or a clamped move clears the corresponding carry. This keeps
  the standard 1× screen speed at every zoom, including slow settings at 2×.
  Both use the current live factor, including intermediate continuous-zoom
  factors, rather than the recording step or zoom target. This is a Nanolathe
  presentation choice in every gameplay mode and renderer; at native 1× it
  retains the retail rate. Production scroll-pass tests cover all four
  directions, slow/default settings, free factors and a changing live zoom.
* **The native fast path is exact.** At factor 1 both conversions take the
  original integer path unchanged, so nothing composed at native scale differs
  by a pixel from a build without the feature; the same holds at 2×
  when the factor is on the step.
* **Its writers.** F9 in the battle, which in classic toggles the step
  1× ↔ 2× about the viewport centre and in the modern prototype toggles the
  full-map view and the saved combat view (DESIGN_GPU_RENDERER §16.8);
  **the mouse wheel over the battle viewport**, which in the modern prototype
  changes a target proportionally about the pointer (below); middle-drag through `Drag`, which converts the
  screen delta by the inverse of the factor before panning; `--zoom` with
  `--shot-focus`, which takes a free factor for modern and one of the two
  views for classic; and, with `--zoom` unset, native 1× at battle entry for
  both renderers at every window resolution
  (DESIGN_GPU_RENDERER §14.6, §16.8). `SetScaleAbout` and `SetZoomAbout` keep
  the world point under a given screen position fixed and then clamp.
* **The wheel binding.** Retail's wheel is not a camera control: it belongs to
  the GUI list under the pointer [07 §2][07 §10], and that is still where it
  goes first. What DESIGN_GPU_RENDERER §16.6 adds is a Nanolathe binding on
  the wheel the chrome did not want: over the battle viewport, outside TALK,
  with no modal open and the pointer off the minimap, and only while the modern
  executor presents, Smooth multiplies the target by 1.25 per upward
  notch or divides it by 1.25 per downward notch, clamped to full map..2×.
  Pinch and wheel share a configurable snap band: near targets and crossings
  land immediately on the preferred stop, defaulting to exactly 1×. Controls →
  Mouse owns Zoom lock (`presentation.zoomLockPercent`, default 100); 120
  chooses approximately 1.2× and replaces the native stop in both Smooth and
  Steps. Camera zoom also offers No zoom: fixed native 1×, no pinch, wheel,
  F9 or whole-map Tab zoom, with panning intact and Tab opening Options.
  The choice is host policy under Modern and Community camera controls; Strict
  and the separate Community megamap ignore it. Wheel input then needs 180 host
  milliseconds of quiet before a new burst can leave the stop; pinch needs a
  fresh gesture. The world point under the pointer stays put within the camera bounds. Camera zoom
  centres an axis while the whole map fits on it, then limits panning to its
  edges; those bounds take precedence over cursor anchoring and are applied
  continuously during zooming (DESIGN_GPU_RENDERER §16.7). Fractional wheel travel banks
  until it is worth a notch. The live wheel factor eases toward its target
  on the host Update grid; pinch follows the fingers directly. The
  classic executor takes no wheel zoom at all.

What the scale does to every world-space layer, the 2× art it selects and the
load-time remaster that produces that art are DESIGN_GPU_RENDERER §14. F10
toggles the executor at runtime (§14.6). The live factor, the wheel's steps,
the ease and the strategic view at and below half scale are §16.

**I13 text-list raster correction.** The frontend list painter measures
stored text before handling a heading prefix, applies the traced 1/4/2
alignment precedence and inclusive row bounds, and performs heading shading
as four successive table operations instead of selection brightening. Flagged
headings keep their stored ampersand. Tall rows use the closed list wrapper
and list-owned scroll limit. Admission caches the entry font metric before
the gadget selects its FNT; the first row draws before the remaining-height
test, and equality admits the next row. The unaligned authored case retains the previous
host inset with `TODO(T25)` because retail leaves that pen scratch unset.
Frontend windows and MSGBOX now establish one scoped child-surface clip.
Images, scaled map previews, text, shades and original outline edges retain
that clip in both executors; nested scopes restore their parent. Record-list
payload and variable geometry remain an explicit code/research Unknown
`[07 R-WGT-01 §4]`; no record layout is invented.

ENDMSN delegates its populated mission list and scrollbar to these same
frontend painters, preserving mark bytes, selection and scroll state. Initial
selection uses the fill-time scroll limit. Result player names are the
appended labels of `[08 R-CAMP-01 §7]`, drawn by `drawResultName` over each
colour logo with the label painter's GAF branch: the small `hattfont11` face
through the lit GAF pen (`drawRetailGAFTextLit`) at the label's colour word,
light-table row 15, so they read lighter than the mode-0 bar numbers. Only a
missing `hattfont11` reaches the COMIX FNT fallback; the metric that fallback
centres by carries a `TODO(question)`. Its outcome title reuses the loaded
`igvictory`/`igdefeat` frames at `(W/2, 28)` with ordinary authored offsets
`[08 R-CAMP-01 §8]`. The selector applies retail's watcher test: `igvictory`
only when the result was won and the local slot is not watching, otherwise
`igdefeat`. `Players[Selection.LocalPlayer].Watcher` is the watcher bit ORed
with this build's observer controller. A draw has no title.

**In-battle end titles.** The same two frames are drawn over the battle view
`[07 §11]`. They use the pause title's view-centre anchor
`((W + 128) / 2, H / 2)`, less the frame's authored offsets. They sit in the
pause title's layer: over the world and chrome, and under the clock line, the
open windows and the result overlay. One gate covers both titles: a watching
local slot gets neither. Otherwise a won result draws `igvictory` and a lost
one draws `igdefeat`. A draw, or an ending without an outcome, has no title.
The first frame that shows the latched result is composed before the results
controller exists. That frame is retail's one live battle frame after the
latching tick, so `drawResultOverlay` draws nothing on it. The title then
stays on the retained battle picture, and the post-battle darkening
(`applyPostBattleFade`) shades it with the view. It stays, including behind
the campaign CD-check dialog, until the glamour image or the ENDMSN background
replaces the picture. Retail's handler shades
the retained frame and was not found to clear it. That the title stays under
the darkening is therefore **Supported inference**. No retail capture in the
reference set shows a battle ending, and the Unknown entry in `[07 §11]`
stays open until one does. `drawEndTitle` and `endTitleOnPicture` live in
`result.go`. `TestEndTitleGateAndOutcome`, `TestEndTitleAnchorAndWatcherGate`,
`TestEndTitleStaysUntilEndMission`, `TestResultTitleFrameWatcherTakesDefeat`
and `TestResultOverlayWaitsForTheResultsController` lock the contract. Selected Core briefings use the installed `mbriefcor`
background key `[08 R-CAMP-01 §2]`.

### 3.9 Partial I10 and exclusions

**Partial I10 — single-player TALK.** After the active GUI and command palette
decline Enter, battle opens the installed `TALK.GUI`, plays `SmallButton`,
places its authored 512×33 strip at `(128, H−33)`, hides `SENDTO`, and focuses
the retained common-widget editor. The dialog owns keyboard, button and world
pointer input through its close frame. Simulation continues, pointer-edge
camera scrolling remains live, and held-arrow and drag camera movement are
suppressed. Enter posts and Escape cancels; either exit clears the editor.
Keypad Enter is Enter. A right-button press anywhere cancels as Escape does:
`serviceTalk` serves the editor one Escape in place of that frame's pointer
records and closes the line without committing even if the editor had lost
its capture. The press belongs to the dialog's frame, so it never cancels an
armed order or clears the selection. This is a Supported inference from a
retail maintainer's manual observation `[07 §5 "Chat"]`, applies in every
gameplay mode, and is locked by `TestTalkRightPressCancelsAndIsConsumed`
together with the unchanged right press while chat is closed.
Posting reads the registered local-player name without a fallback and appends
`<name> text` to the local message ring with class 4, source unit 0, speaker
sentinel 10 and the current published tick. Campaign entry registers the name
`Player` before TALK is reachable `[07 §5 "Chat"]` `[07 R-FE-02 §12]`.

The same path has a deliberately partial `+`-command implementation. It keeps
the 79-byte last-command copy separate from tokenisation, admits at most 20
words into 126 shared bytes including terminators, treats `#` as an inline end
marker and semicolon as ordinary content, and reads integer arguments with
signed decimal-prefix `atoi` semantics. Every typed `+` line is still posted
as local chat. The implemented handlers are:

| Command | Implemented effect |
|---|---|
| `Light a b c`, `RCache` | invalidate client model image/geometry products while preserving retained pose; `Light` first replaces the global shading vector; neither queues simulation work nor writes settings |
| `NoShake` | enqueue the authoritative toggle through `HumanCommand` |
| `ATM` | skirmish only; enqueue uncapped `+1000` metal and energy through `HumanCommand` |
| `SwitchAlt [n]` | no argument toggles and persists; an explicit argument applies `n & 1` without persisting |
| `ScreenChat` | toggle and persist the screen-chat word |
| `ScrollSpeed n` | store and persist the low byte, including exact zero |
| `IFace n` | store and persist the integer interface type |
| `AntiAlias`, `Shading`, `Shadow` | toggle the independent live display bit and persist immediately |
| `Gamma n` | apply the command factor to the shared output palette and persist the signed integer; startup and slider callbacks use their distinct factor conversion |
| `Clock` | toggle and persist the stand-alone battle-clock bit; draw the committed unsigned tick in the late HUD layer |
| `ShowRanges` | toggle detailed terrain-following range rings and labels inside the existing Shift-held queue overlay; Modern placement reuses its weapon-ring renderer (DESIGN_GPU_RENDERER §20); initially off unless the content profile opts in, retained by the shell across battles, without settings or simulation writes |
| `FPS` | toggle the process-local modern battle FPS counter (DESIGN_GPU_RENDERER §13.5); initially off, retained across battles, with no settings or simulation writes |
| `Dither` | toggle the live current-fog pattern selector and persist `0` or `1` immediately |
| `TShadow`, `FShadow` | toggle vehicle or feature shadows independently; persist on the next settings write |
| `MusicMode n` | set the signed desired category through the existing music controller; fade/delay timers use the busy presentation pump and do not write settings |
| `CDPlay n`, `CDStop` | use the existing music controller; argument zero runs its enabled music tick |
| `Sound3D` | toggle the live audio output mode; write settings while retaining the separate stored sound-mode preference |
| `Sing` | toggle the existing voice queue's audible alias override; preserve captions, arbitration and random draws; no settings write |
| `View p` | skirmish only; queue the low-byte viewing slot without changing command ownership or requesting a visibility refresh |
| `Give p n metal/energy` | queue a signed resource transfer from the own/controlling player, read at drain time, through the existing economy ledger `[07 R-CAM-01 §6]`; no settings write |
| `Logo n p` | validate the signed logo index against authored `32xlogos` frame count and the low-byte player argument against the current committed player row; enqueue the authoritative logo-byte assignment; no settings write |
| `NoMetal`, `NoEnergy` | command alone sets local stock to zero; otherwise the first argument's low byte selects the player and the second supplies the stock value, subject to the established player-record gates |
| `Selectable` | enqueue the alive-unit walk, setting only the selectable status bit |
| `LOS`, `Mapping`, `NowISee` | skirmish only; enqueue live visibility toggles or clear both history/current bits; LOS and Mapping write the unchanged setup preferences |
| `LOSType` | enqueue the terrain-ray visibility toggle in either single-player mode, without a settings write |
| `DoubleShot`, `HalfShot` | skirmish only; enqueue independent damage gates, applying signed doubling before halving in the existing weapon pipeline; no settings write |
| `Radar` | skirmish only; toggle the battle-local full-radar bit for unit contacts; no settings write |
| `Meteor [n]` | skirmish only; no argument queues a forced storm arm through the existing scheduler owner, while an explicit argument only sets its enable bit from `n != 0`; no settings write |

`ShowRanges` reads authored radii from the catalog and live weapon-enabled
bits from `UnitView`; no visibility-derived radius cache or live unit access is
needed. The existing queue walker emits range chords and labels in descriptor
order. Radii producing fewer than one chord are rejected before division, a
narrow malformed-input bounds departure under I11; no substitute radius is
invented `[07 R-P0-11 §3]` `[I6]`. The separate bit-4 target-circle
helper still needs its model-radius binding and replacement of the old flat
approximation with the established sixteen-segment world projection; it is
not enabled by the range adapter.

The resource strip retains its shared rate latch across `View`, while the next
refresh uses the newly viewed player's own display deadline. The presentation
owner and save-projection handoff are described in §2 above
`[07 R-HUD-03 §4]`.

The retained classic and native model-cache keys include the published team
selector, so a committed player-logo change rebuilds cached LOGOS pixels or
faces on the next presentation frame `[03 R-RAST-01 §3]` `[I6]`.

The stand-alone clock is presentation state owned by `battleSession`, with the
frontend shell retaining the persisted bit between battles. Its HUD helper
reads only the committed frame tick, formats cumulative hours at 30 Hz, and
draws at the fixed late-composer origin before linked battle windows. It is
independent of the Space-held `LIGHTBAR` strip. The HUD binds COMIX alongside
the side console FNT because the retail draw site inherits COMIX after a live
message-column pass and inherits the side console when `textlines` is zero
`[07 R-CAM-01 §6]` `[07 R-HUD-03 §14.4]` `[I6]`.
There is no live interface-suppressed battle-composition route in this build;
movie capture remains excluded below. If that route is implemented later, it
must supply the retail selector-inheritance condition at this painter rather
than adding a dormant mode flag now.

`BigBrother` now enters the session's ordered command stream. A command-owned
Shift latch pauses the phase-2 cycle; its published cycle/reset events reach
the camera on every completed sub-tick, including intermediate catch-up ticks.
The publication observer applies repick, follow/glide and shake in order, with
no movement on zero-tick host frames. Manual follow requests run after that
batch, preserving the pre-input tracked target until an automatic cycle
replaces it `[04 R-MOV-03 §1]` `[07 R-CAM-01 §12]` `[I6]`.
The cycle currently shares selection commands' unit-info-only page close.
`TODO(question)`: map the retail force-zero deferral bits and current page
owner into the shell before claiming complete page-stack close behavior
`[07 R-HUD-04 §3]`.

The cadence integration's sequential scene-3 Ashap Plateau check used seed 7,
factories, 1920×1080, zoom 1, remaster off, 30 TPS, 60 warm-up and 180 measured
draws. Against the accepted baseline, both renderers retained identical
per-frame censuses and byte-identical inspected captures, including four shake
frames. Classic record median/p95/max was 10.045/11.769/13.482 →
10.534/12.020/15.041 ms; modern was 5.259/6.994/15.000 →
5.166/6.896/10.998 ms. Cadence stayed 177/180 for classic and changed
105/180 → 107/180 for modern; allocations stayed 1.168 and 5.219 MB/frame.
These runs establish no performance improvement. The normal benchmark does not
enable BigBrother or exercise catch-up input; the focused controller fixture
checks its same-tick repick, two-tick follow and zero-tick behavior.

These commands are partial I10. Shell and direct-map entry use the same live
display bits, including the persisted low bit that selects dithered fog; a
later direct-entry settings write includes deferred shadow preferences. The remaining ordinary local
single-player command families stay in the parent I10 scope. The mask-4
developer table and default unit-spawn handler remain unimplemented with developer
mode; multiplayer `TALK2.GUI`, recipient controls and network chat remain
excluded with multiplayer `[07 R-CAM-01 §6]` `[07 R-FE-02 §12]`.

* **The multiplayer lobby shell.** Retail's `SELGAME.GUI` lobby and its
  transport remain out of scope; the single-player skirmish setup screen is a
  different surface and is implemented `[07 §12]` `[07 R-FE-02 §1]`. The
  authored `MAINMENU` `MULTI` button opens Nanolathe's own online screen
  instead ([Online games](#online-games)). Where the mounted content has no
  skirmish map, such as the browser's demo, it is greyed with Skirmish so it
  cannot be activated by pointer or key.
* **The front-end movie stage.** Normal launches play the original startup
  logo `Data/1.zrb` once, then open `MAINMENU` `[07 R-FE-01 §3]`. Playback
  is deferred to the first shell update so the platform PCM device is ready;
  the pending surface is black and menu music starts only after completion.
  Direct map and saved-game launches bypass the logo. The requested portable
  startup policy plays the logo on every normal launch, including windowed
  launches; it does not add retail's one-install `PlayMovie` preference or
  automatically append the full intro. Holding Shift at startup does not
  request repeat. The `CDCHECK` gate remains excluded; developer frame capture
  is planned in DESIGN_DEVELOPER_TOOLS and remains unimplemented.
  The authored main-menu `Credits` callback plays `Data/5.zrb` once.
  Campaign victory with no next authored mission and `nomovie=0` routes after
  the results fade to `Data/3.zrb` for local side zero (Arm), otherwise
  `Data/4.zrb` (Core), then `Data/5.zrb` [08 R-CAMP-01 §6]. These movies use
  the same portable windowed playback policy as Intro. Battle teardown and
  campaign progress retention precede the movie sequence; the menu appears
  only after the sequence. Missing reels skip individually. A character skips
  the current reel; Alt+F4 cancels the sequence and quits [08 R-OOS-01 §4].
  Main-menu `INTRO` resolves `Data/2.zrb` from the mounted content and plays
  it through `formats/zrb` and the existing desktop PCM device. The shell
  stops ordinary sound, narration and CD music, drains the activation input,
  hides the cursor and installs the movie palette. Original alternate-row
  scanlines are retained, at the native display size and vertical placement
  `[03 §9]` `[fmt zrb]`. Each focused update advances at most one sequential
  frame when due; audio playback position supplies the clock when available,
  with authored frame-duration scheduling for silent playback. No simulation
  ticks run. Character input (including Escape) skips; mouse clicks and
  untranslated navigation/function keys do not. Shift held at activation
  latches repeat until a character cancels `[08 R-OOS-01 §4]`.

  EOF, skip and decode failure close the soundtrack and restore the menu's
  palette, gamma, cursor and newly started `BGM`. Missing movies skip silently;
  invalid media produces a provenance-bearing message window. Portable host
  policy permits playback with sound in windowed mode, bounds movie input to
  256 MiB, pauses both clocks while unfocused and uses the device's reported
  playback position without proprietary cursor calibration. Exact retail
  audio calibration and focus-loss sound behavior remain a marked research
  gap `[03 §9]`. The pure Go decoder and bounded PCM conversion add no module
  dependencies and do not invoke an external decoder at runtime.
* **`SHARE.GUI` and `CONTROL.GUI`.** The resource transfer dialog and the
  host-only player control panel are multiplayer surfaces
  `[07 R-HUD-03 §9]` `[07 R-FE-01 §7]`.
* **Developer mode.** The repeated-command binding, contour overlay, in-battle
  screenshot and Unit State/Builder Probes are not implemented. They are now
  planned in [DESIGN_DEVELOPER_TOOLS](DESIGN_DEVELOPER_TOOLS.md), including the
  user-authorized connection of dormant probe painters. Research distinguishes
  hotkey state from actual painter reachability
  `[07 R-CAM-01 §9]` `[07 R-FE-02 §11]`.
  The front-end `DRDEATH` cheat sequence also lacks its token-history consumer
  `[07 R-FE-02 §10]`.
* **Clipboard portability.** Insert, Ctrl+V and the macOS Cmd+V alias read
  the host clipboard on the initial paste key transition: AppKit plain text
  on macOS, `CF_UNICODETEXT` on Windows, and the session's `wl-paste`,
  `xclip` or `xsel` on Linux and BSD desktops, where a desktop with none of
  those programs has no clipboard (§3.2). The browser build takes the text of
  the page's paste event (DESIGN_BROWSER_HOST §4 contract 9). Android, VM
  guests and other hosts lack a clipboard bridge. Translating non-ASCII
  Unicode text into the retail code page remains unresolved; an unavailable
  format, failed read or unmapped text preserves the current editor `[07 §2]`.
* **Never-opened GUIs.** The windows retail's own code never opens are not
  implemented, and implementing one would be inventing a screen
  `[07 R-FE-01 §12]`.
* **The label painter's quickkey underline and `colorb` fill.** The FNT and
  GAF label paths draw the caption only; the one-pixel underline under a
  label's quickkey letter and the map-entry `colorb` rectangle fill are not
  drawn (every stock label authors `colorb` 0) `[03 R-FONT-01 §6]`.

### Modern spawn command

**Nanolathe Modern policy.** The user-authorized testing command
`+spawn <unit>` creates one fully built unit owned by `Session.LocalOwner` in
single-player skirmish or campaign. The retail vocabulary remains defined by
`[07 R-CAM-01 §6]`; this is a Nanolathe extension. Strict 3.1 rejects it at
both the chat edge and the authoritative command consumer, without allocation,
resource changes or RNG draws.

The case-insensitive catalog name and world point under the cursor **at
submission** enter the ordinary human-command queue. The pointer must be over
the battlefield, outside the HUD and minimap. Moving the camera or pointer
later does not retarget the queued request.

**Placement is retail's.** The session places the unit exactly as retail's
developer spawn handler places each unit it creates: through the mission
spawner's position fixup — buildings snap to their footprint grid and take the
spawner's height probe, mobiles retain the terrain point `[08 R-ENTRY-01 §6]`
`[08 R-ENTRY-02 §1]`, which the allocator's creation-time post-move correction
then grounds or floats `[04 R-MOV-01 §5]` — and through nothing else. That
handler runs no placement validator `[07 R-CAM-01 §6]`, so the command does not
test terrain suitability, slope, depth, features, building yards or occupancy: a unit can
be spawned on top of another unit or structure, or on ground it could not be
built on. The session checks only the current local player, the catalog
definition, and that the captured point lies on the map — a Nanolathe guard
that retail's pointer, confined to the clamped view, could never trip.

Successful allocation uses the normal fully built creator — the allocator
arguments retail's handler and mission spawner also pass — including its unit
limits, COB initialization, activation, allocator RNG sequence and movement
registration. The normal observer and publication passes expose the unit. No
build resources are charged or granted; subsequent unit operation participates
in the ordinary economy. Rejections before allocation draw no RNG; allocator
or script failures retain the common creator's failure semantics. Usage errors,
unknown names, an off-map point and allocation failures produce chat feedback.

**Which parts are retail.** Retail: the position fixup, the absence of any
site validation, the fully built creator and its RNG effects, and the
allocator's own refusals. Nanolathe Modern policy: the command itself and its
Strict bypass, the exact case-insensitive name (retail matches `?`/`*`
patterns and spawns every match), one unit per line (retail steps 32 world
units between matches), `Session.LocalOwner` as owner (retail takes the slot
from a second word, slot 0 when absent), no developer access, the off-map
guard, and the feedback lines.

Verification locks submission-time coordinates, local rather than viewing
ownership, creation state, a second unit stacked on an occupied site (and a
structure on a structure), the off-map refusal, no resource charge, ordinary
creation RNG effects and the Strict bypass, including a mode switch after enqueue.

**Shorthand `+<unit>` (Nanolathe Modern policy).** A command line of exactly
one word that matches no registered command and names a catalog unit
(case-insensitive) queues the same request as `+spawn <unit>`, with the same
submission-time pointer capture, placement and feedback. Retail's analogue is
the developer-only mask-4 default handler, which treats any unmatched first
word as a name pattern, creates one fully built unit per matching definition
for the slot named by a second word (slot 0 when absent), and steps 32 world
units between footprints `[07 R-CAM-01 §6]` `[07 R-CAM-01 §9]`. The shorthand
deliberately keeps the `+spawn` contract instead: no developer access, one
unit, local ownership. Without developer access, a word naming no unit, a
line with arguments, and every Strict 3.1 line remain plain chat with no feedback, exactly as an unregistered
command would. With developer access, the separate retail default handler
owns unmatched words in both modes, including wildcard patterns and the
optional owner; see DESIGN_DEVELOPER_TOOLS §8.

**Repeating the last command.** Retail replays the retained last `+` command
only with `\` and only with developer access; Insert has no battle action and
pastes the clipboard in a focused editor `[07 R-CAM-01 §2]` `[07 R-CAM-01 §9]`
`[07 §2]`. The Insert replay that players remember is a later engine-patch
feature — documented for the Escalation and TA Zero engines
([taesc-engine](../research/extensions/taesc-engine.md),
[ta-zero-engine](../research/extensions/ta-zero-engine.md),
[mod-engine-compatibility](../research/extensions/mod-engine-compatibility.md))
and undocumented for ProTA's. It is not adopted: under AGENTS.md, evidence that
a patch implements a behaviour is not authorization to enable it, so an Insert
replay would need the user's approval as a new Modern policy.

To activate replay in the current battle, open TALK with Enter, submit `+dev`
or `+Now Film Chris Include Reload Assert`, then submit the command to retain
(for example `+atm`). Press or hold `\` to repeat it. Every rule set accepts
`+dev` as the activation shorthand (DESIGN_DEVELOPER_TOOLS §2.1).
Replay drains ordinary character tokens and adds no TALK echo.
`TestBackslashReplayThroughBattleInput` locks activation,
access gating, repeat order and editor ownership across all gameplay modes.

**Command history (Nanolathe host input policy, user-authorized 2026-10-08).**
Every battle keeps up to 64 submitted `+` command lines, in submission order,
with exact consecutive duplicates suppressed and the oldest line evicted at
the limit. The existing dispatcher's leading-space admission determines
whether a line is a command; unknown or refused commands remain recallable.
Plain chat and cancelled lines never enter history. This is a host input
extension in every gameplay mode, Strict 3.1 included; retail research does
not establish an Up/Down command history `[07 §2]` `[07 §5 "Chat"]`.

While TALK has editor capture, Up recalls older commands and Down recalls
newer ones. The first Up saves the current draft after any earlier tokens in
that frame. Down past the newest restores that draft exactly. Each recalled
line passes the ordinary editor's byte and font-width admission, with its
caret at the end; subsequent edits use the existing editor. Editing a recall
does not overwrite its stored line. Returning to the draft ends recall, and
closing or reopening TALK clears the recall position and draft. History
remains local to that battle and is never written to settings or simulation
state. Up/Down outside captured TALK keep their existing behavior.

Recall only fills the editor. Enter submits through the original command
dispatcher and access checks, and cancellation discards the edited line.
Ordered tokens keep the existing final-consumed-token Enter behavior and
Escape stop `[07 R-WGT-01 §6]`; TALK owns the entire closing frame as before.
Recall neither executes a command nor replaces the separate retail
`lastCommand` copy; only submitting a `+` line updates that copy.
`TestTalkHistoryOrderedRecallEditsAndDraft`, `TestTalkHistoryBoundDuplicateAndCancel`,
`TestTalkHistoryUsesCurrentEditorAdmission`, `TestTalkHistoryKeepsWorldInputOwnership`
and `TestTalkHistoryAccessAndCommandBoundaryAllModes` lock admission, ordering,
the bounded battle lifetime, world input ownership, access checks, backslash
replay and unchanged resource/RNG state before the command boundary.

**Retail cheat and visibility commands.** The mask-2 set (`Radar`, `ATM`,
`View`, `LOS`, `Mapping`, `DoubleShot`, `HalfShot`, `NowISee`, `Meteor`) and
the mask-1 settings commands are dispatched in
`cmd/nanolathe/battle_chat_commands.go` per `[07 R-CAM-01 §6]`; mask-2
commands are inert in campaign `[08 R-OOS-01 §5]`. `LOS` and `Mapping` are
pure toggles of live mode bits 1 and 0 starting from the skirmish setup record
`[03 R-VIS-01 §1]`: from a `Permanent` line-of-sight start the first `+LOS`
turns current-sight tracking **on** (unseen ground greys), and from `True` or
`Circular` it turns tracking off. The research command table records no posted text for either, so none is shown.
Neither rewrites the setup record; their settings write-all re-serializes the
unchanged triple. Not implemented: `MakePoster` (argument grammar Unknown),
`ShootAll` (needs a session-owned seam into `combat.Acquisition.ShootAll`),
the network-only share/compression/`NetStats` commands, `SFX`, `Drop` (its
flag's consumer is not documented), and the mask-4 developer commands such as
`Kill`, `IWin` and `ILose` whose deeper effects the census does not specify.

### 3.10 Modern resource double-click construction

**Established implementation policy (user-requested departure, not retail
behavior).** With the modern executor active, an idle selected mobile builder
accepts Shift-left-double-click on a metal deposit to build its strongest
available extractor, near a geothermal vent to build an available geothermal
plant, or on ordinary ground to build a solar collector. Every
Shift-double-click appends, preserving the normal queue-command modifier. The
typed mobile-build command carries explicit `Queued` and `AppendOnly` intent: existing work is
preserved; a repeated site is moved clear of queued footprints instead of
using the manual Shift-placement removal gesture. Candidates must occur both
in the rule-selected construction membership and as active, enabled product
buttons on the builder's human GUI pages, including generated download pages.
All pages participate, independent of the currently displayed page. CANBUILD
alone also contains products reserved for the computer player and cannot
establish human availability. Equal extractor strengths and multiple solar or
geothermal candidates retain construction-membership order. Missing or
unresolved human pages never fall back to the AI construction list. For a metal
deposit, prefer the strongest candidate that the ordinary placement and queued
footprint preview admits there; this preserves underwater products on water
without choosing them for land. If none fits, retain the strongest human
candidate and the existing refusal path. This changes input selection only,
never placement legality or the simulation's build admission.
Factories, unit targets, other features, armed commands, HUD and minimap input
keep their existing paths. Classic does not use this shortcut.

Extractor candidates are non-builder structures with `ExtractsMetal > 0`;
`MakesMetal` alone is a converter and does not qualify. Solar candidates are
non-builder structures with positive energy production (`EnergyMake > 0` or
`EnergyUse < 0`), no wind/tidal generation or extraction, and an explicit
`SOLAR` token in category or sound category. Token separators are punctuation
and whitespace. **Established asset observation:** the reference installation's
ARM/CORE collectors use `ARM_SOLAR`/`CORE_SOLAR` sound categories. Fusion and
geothermal have distinct sound families. There is no universal solar capability
field; a custom unit without either explicit token is deliberately unclassified.
This input policy uses the authored FBI fields [fmt fbi], not unit-name lists,
output thresholds or a generic ENERGY-category guess.

Geothermal candidates are non-builder structures whose parsed yard map has
the geothermal requirement bit (`G`); parsing uses the ordinary footprint-sized
yard parser, so an unused trailing `G` does not classify a building. These
products cannot also become solar or extractor ground shortcuts. Vents are
committed features with `Geothermal` in their immutable definitions
[05 "Geothermal requirement"][fmt fbi]. A direct vent click without a matching
build-menu product does not fall back to solar.

For this modern input policy, "near" means that the available plant's snapped
footprint around the clicked ground point overlaps a vent. Exact metal-deposit
clicks retain extractor precedence. When several vents qualify, choose the
nearest vent center in fixed-point world coordinates, retaining publication order on ties.
Center the plant on that vent, adjusting to the nearest cell-aligned anchor
where an actual `G` cell covers it; increasing Z then X breaks alignment ties.
This also handles custom yards whose `G` region is not central. Permanent vent
identity survives fog, while the normal placement checks still decide whether
the build is admitted.

Deposits are committed feature footprints whose immutable definitions
have nonzero Metal and Indestructible [05 R-FEAT-01 §7][08 R-AI-03 §1]. Clicking
in fog still identifies these permanent map deposits, independently of current
line of sight; a deposit must never fall back to solar, even when the builder
has no extractor in its menu. Other features retain their usual visibility gate.
Clicking any cell of the footprint selects its center; the product's own footprint is
snapped around that center using the ordinary placement arithmetic [07 §9].
The existing cursor preview validates visibility, terrain and occupancy, and
its site height and snapped center enter the shared mobile-build dispatch. Refused sites
play the usual refusal cue and enqueue nothing. This does not replace an
existing extractor or clear obstacles automatically. Featureless metal maps
have no deposit feature for this gesture to identify.

**Queued footprint spacing (modern input policy).** On the second click,
collect building footprints from both published order lists of every local
builder, including unselected builders, and from copied mobile-build input
commands awaiting their authoritative tick. Pending cancellations may leave a
conservative reservation until the next publication. Foreign queues do not
affect the gesture. An incomplete published queue refuses the shortcut because
it cannot establish that a site is clear.

An overlapping site moves to the nearest legal cell-aligned outside edge of
the connected group of queued footprints. Expand obstacles by the new
footprint to obtain forbidden anchors, collect every integer anchor along the
group's boundary edges, and consider them by squared distance from the original
anchor, breaking ties by increasing Z then X. Check each against all reserved
footprints and the normal placement validator. Half-open rectangles permit
touching edges without a gap, including mixed footprint sizes. A site already
clear of queues keeps its original position and validation behavior. This is
an input-time search, with no new per-frame or per-tick work.

An offset extractor must still cover at least one cell of the same deposit;
partial coverage is allowed and can reduce extraction. If no legal edge
retains deposit coverage, refuse rather than place off metal. Earlier queued
sites are never moved. The final adjusted position and height enter the
ordinary queued build command and therefore the existing queue animation.
Geothermal spacing additionally keeps a `G` cell over the same selected vent;
overlap with a different yard cell or another vent does not qualify. An already
reserved vent therefore refuses another overlapping plant.

The battle input layer recognizes a pair within 400 host milliseconds after
first release, at most six logical pixels apart on each axis, with Shift held
and no Ctrl/Alt. The second press must resolve to the same product and snapped
site. These are modern gesture choices, not native double-click identity; the
platform's retail event-history gap in §2.2 is unchanged.

In Type 0, the first Shift-click immediately enqueues a move for the captured
selection. This gesture's move appends without the ordinary repeated-position
toggle, so even an older move at the same location survives a double-click.
`EnqueueHumanCommandWithSequence` returns the input sequence as a receipt;
the issued move carries it as transient `HumanMoveSequence` metadata. On the
matching second click, a typed `HumanCancelQueuedMove` removes only that
sequence's still-live ground or air move from the captured local actors, then
an ordinary append-only build follows at the same input boundary. Cancellation
uses the actual matching node and ordinary cleanup, never coordinate matching,
a saved queue copy, or a stale pointer. It is harmless if the move has already
finished or been removed. An idle unit can begin walking between the clicks;
that movement is not rolled back. Refused builds consume the gesture's move
without removing older work. Receipts are not saved: loading or leaving a battle
ends input recognition.

In Type 1, Shift-left-click is selection input, so its empty-ground deselect
waits for expiry or Shift release to keep the builder available for the second
click. Type 1's right-button move input keeps its immediate dispatch. Ordinary
unmodified clicks and Type 0's Shift-click moves never wait for recognition.
Expiry, Shift release, or a different pointer press ends recognition without
reissuing a move. Keyboard commands other than Shift, selection changes, armed
modes, leaving the battle input pass, and renderer changes discard the gesture
receipt. They do not undo the already issued move. The second press owns its
release even on refusal and never arms persistent placement.

A successful double-click reveals the existing animated order-queue path and
queued footprint markers for 1.5 host seconds, restarting the interval for each
successful double-click. This is modern presentation policy. Input updates the
expiry; drawing remains a read of the committed queue inside the existing world
overlay transform. It does not set a live or authoritative Shift bit. Focus,
modal/chat/result ownership, selection changes and switching to classic hide
the feedback; ordinary held-Shift visibility remains unchanged. Refused builds
do not start feedback. A normal unshifted order click remains the escape hatch:
left-click in Type 0, right-click in Type 1, using the usual queue replacement.

Verification: `TestResource*` replays the production input/command seam with an
injected host clock, checking modern/classic behavior, Shift-only queue
preservation and feedback, immediate plain and Shift moves, replacement across
ticks, gesture expiry and modifier transitions, refusal, deposit centering in
and outside current LOS, builders without extractors, repeated/rapid queued spacing, unselected local
builders, mixed footprints, illegal edges, deposit coverage, geothermal menu
detection and yard alignment, nearby/fogged vents, and cancellation. Synthetic
fixtures define the new input policy; it is not attributed to retail evidence.

### Ordinary group-click destinations

Retail's selection broadcast preserves nearby actors' offsets from the
selection centroid for formation-enabled order descriptors. The exact
counting, rounding, cutoff and producer boundaries are [04 R-STANCE-01 §5].
This ordinary click behavior is separate from the explicit drag destinations
in §3.11. It belongs at the authoritative command boundary, where the current
actor positions and complete selection are available.

The `internal/session/commands.go` ordinary `HumanOrder` path computes the
centroid before per-actor resolution, excluding the designated target where
the numeric command requires it. Captured handles are treated as a set in pool
order. Formation-enabled descriptors apply the retail offset before queued
point matching and insertion. Strict 3.1 and Community 3.9 use it unchanged;
Modern adjusts the resulting ground destinations as described in
[Modern group destination slots](#modern-group-destination-slots).
The producer does not consume RNG or resources, and retains the supplied Y.

Explicit drag destinations set `HumanOrderCommand.AssignedPosition`; those
already assigned coordinates pass through without another offset. Ordinary
clicks, including the tracked queued move used by the resource gesture, leave
that field clear. Area target batches retain their existing separate dispatch.

Tests cover centroid truncation, separately truncated squared components at
the inclusive cutoff, outliers, per-actor command rejection after counting,
selected-target exclusion, non-formation rally descriptors, queued toggles,
and exact fractional drag destinations. Blocked-move completion and retaliation
retain their retail contracts [04 R-ORDER-02 §1][04 R-STANCE-01 §3].

### Modern group destination slots

**Nanolathe Modern policy.** An ordinary group move gives every ground actor
its own destination footprint. Outliers keep their bearing instead of sharing
the clicked point, and an actor whose destination is shared or held moves to
the nearest free footprint.

**Strict 3.1 behavior.** Actors within the cutoff keep their offset from the
selection centroid; every actor beyond it takes the clicked point
[04 R-STANCE-01 §5]. A line wider than the cutoff therefore sends both end
actors to one cell, and the first to arrive can park in front of the slots
the others still need. In the opt-in path benchmark a sixteen-flea row
ordered along its own axis ends with one flea at its goal and the rest queued
behind the one parked mid-row (`traffic/open_flea` 15, 16 and 17; 8 and 64
are unaffected). `movement.StrictRules.GroupDestinationSlots` answers false;
Community inherits it.

**Modern behavior.** In the ordinary `HumanOrder` path, before the per-actor
loop, `Session.groupDestinationSlots` takes each ground actor whose resolved
order carries the formation flag (aircraft keep the retail goal) and asks
`movement.System.AssignGroupDestinations`:

1. *Clamped outliers.* An actor inside the cutoff keeps exactly the retail
   goal. One beyond it keeps its offset direction, scaled to the cutoff
   radius `isqrt(3000 × count)` world units.
2. *Claim order.* Actors claim in travel order: the actor farthest along the
   centroid-to-click direction first, ties by lateral position. Each actor
   keeps its own goal where it is free, so retail pairing is preserved; a
   first version that re-paired actors to goals by travel order scrambled
   large formations and was rejected.
3. *Free footprint.* A footprint is free when no earlier actor claimed any of
   its cells, no stationary unit outside the selection holds one, and the
   actor's class passes it as the owner knows the ground — the route search's
   own read, where unexplored ground is passable and learned ground is read
   (DESIGN_MOVEMENT_PATH "Modern learned terrain"), so a destination never
   reveals hidden terrain. Class layers are built when a unit of the class
   is registered (DESIGN_MOVEMENT_PATH "Full-layer rebuild storage"); for a
   unit registered on this tick, whose layer is not built yet, the same
   mapping-word gate over the static footprint test answers. The query never
   allocates a layer.
4. *Nearest.* An unfree goal moves to the nearest free footprint, ring by ring
   up to twelve cells; with none, the actor keeps its goal. A moved goal is the
   footprint's centre, which the commit's quantisation maps back to its anchor.

The supplied Y, queued matching, drag destinations (`AssignedPosition`),
targeted orders, area batches and the AI's orders are untouched. Integer
arithmetic only; no RNG, resources or simulation state are touched.

**Measured effect.** Opt-in path benchmark, research branch
`proto/path-round3` (one-repeat outcomes, deterministic): near-goal arrivals
`open_flea` 15/16/17 1/1/1 → 15/16/17; `idle_blockers` 8/16 3/4 → 8/16;
`packed_goal` 16 12 → 16; `terrain-clutter-mixed` 17 5 → 17; `terrain-winding`
8 4 → 8; the `knowledge-*` 8-unit cases 4 → 8; open 64/256, dense groups and
drag formations unchanged. Destinations move 0–2 cells in most cases (the
collapsed outliers), at most 5 in a dense 64-unit head-on. Losses: slow
shallow-water ships keep their true formation width and so are still sailing
at the window end (`naval-shallow-surface` 8, 5 → 0 near), and
`lifecycle/patrol` and `dynamic-new-wreck` lose one arrival each. The landed
implementation, together with [Modern bounded path
work](DESIGN_MOVEMENT_PATH.md#modern-bounded-path-work), was re-measured over the
whole opt-in corpus against the previous Modern (one deterministic repeat,
excluding the position-dependent `traffic/waves`): near-goal arrivals
3,520 → 3,744 of 5,676, pending objectives 1,974 → 1,834, 36 cases better and
6 worse; the worse are the ships above, `lifecycle/patrol`, and dense 256-unit
crowds whose outcome depends on admission order in both directions.

**Boundaries.** One pass per command, over the selection only: later
commands, other players' units and moving units can still reach a claimed
footprint, and the ordinary follower and crowded-arrival policy handle them.
Nothing is saved. Strict and Community fingerprints are unchanged; the locked
Modern scenes issue no human group move, so their locks are unchanged too.

**Verification.** `session.TestGroupDestinationSlotsStrictAndModern` (a
sixteen-unit row: Strict and Community send both outliers to the click; Modern
keeps every inside actor's retail goal, gives the outliers their own bearing,
shares no cell, and consumes no RNG or resources),
`TestGroupDestinationSlotsAvoidHeldGround` (a stationary unselected unit's
cell), `movement.TestGroupDestinationSlotsAnswers`, and the unchanged
`TestHumanGroupMove*` retail producer tests.

### 3.11 Modern drag commands

**Established implementation policy (user-requested extension).** These gestures
belong to modern input; their geometry is not retail evidence. Classic retains
its existing click and selection handling. Authoritative construction, work,
and movement still receive ordinary typed commands and apply existing gates.

Left drag while placing a structure previews a straight row. Alt switches the
preview to an axis-aligned rectangular grid, taking precedence over Shift range
guides for the duration of construction capture. Anchors use the compiled footprint
in map cells, starting at the press anchor. A row advances by one footprint on
its dominant normalized axis; the other axis follows the straight segment,
rounded to the nearest cell, with half-cell ties toward the drag endpoint.
Normalized axis ties choose X. A grid walks rows from the pressed corner toward
the current corner. Touching edges are allowed. Invalid sites and reserved
queued footprints are red and skipped, unless a Shift row cancels them (below);
valid sites are green. On release the
first accepted build replaces orders unless Shift is held, and subsequent sites
append without duplicate-site toggle. If no site is valid, placement stays armed.
A click still uses the existing single-site path, on release in modern mode.
`presentation.buildDrag` (on by default; the Nanolathe screen's *Build drag*
card, §3.17) switches the construction row and grid off, so every press
places one site through that path; move, repair and reclaim drags are
unaffected.

**Cancelling queued sites with a Shift row (requested input policy,
2026-09-28).** A player sweeps a row over queued sites to take them off, as
the community line tool allows. The retail basis is the click: a queued
(Shift) build click at a point that already carries a queued record of the
same order kind, within one map cell on X and on Z, removes the front-most
such record and issues nothing; the test runs only with the queue flag and
does not compare products [07 R-P0-11 §6]. The community X line generator
feeds every candidate through that ordinary click with queue semantics forced
on ([TA Zero engine, X placement](../research/extensions/ta-zero-engine.md#x-placement-established-shipped-contract)),
so its line toggles queued sites off. That the line therefore removes them is
an inference from those two established contracts. Nanolathe does not
reproduce the X gesture; this rule gives the drag row the click's result.

While Shift is held, each row site that placement accepts is matched, in row
order, the way that click would be matched: against the drag builder's
committed primary queue, taking the front-most record of the builder's own
mobile-build kind (flying builders have their own kind) whose goal lies
within the cell tolerance and that no earlier site of this gesture took. When
that record is the dragged product, the site is a cancel site. It is drawn as
a crossed-out red box over the queued footprint, without the optional
Community model preview, and on release it receives
exactly the queued, non-append command a Shift-click there sends, so the
session's toggle removes the record. Other sites keep the rules above: free
valid sites append, overlapping ones stay red and are skipped. A row can
therefore both cancel and add, in row order, like Shift-clicks along it.

Boundaries:

* **Same product only.** When the front-most record in tolerance is another
  product, the toggle would remove that other building, so the site is not a
  cancel site; it overlaps the reservation and stays red. This deliberately
  narrows the click, which ignores the product, so a sweep cannot silently
  erase a different building. Until the press becomes a drag it is still that
  click, and its one-site preview uses the click's product-blind match.
* **Shift only.** Without Shift the first accepted site replaces orders, as
  before, and queued footprints stay reserved. Shift at release decides, so
  the preview follows the live Shift state while dragging.
* **Placement must accept the site,** as it must for the click. A started
  nanoframe occupies its own site, so started work is not cancelled.
* **One gesture, no re-planning.** A footprint the gesture cancels stays
  reserved for the rest of that gesture, so a new site overlapping it stays
  red. Only the command page's builder is examined, as the typed build
  command names only that builder.
* **Committed evidence only.** A truncated published queue, or any typed
  command still awaiting its tick, disables cancellation until the next
  publication: a pending command may change the queue first and turn a toggle
  into an addition. A record that completes between the publication and the
  command's tick is re-queued by the toggle, exactly as for a Shift-click.
* Classic is unchanged: drag commands exist only with the modern executor.

The optional Community queued-order drag (`presentation.queuedOrderDrag`,
Options → Placement) is a different gesture. It moves one queued build or move
order with Shift and no armed product; cancellation needs an armed product.

`TestCommandDragShiftRowCancelsQueuedSites` covers a mixed row (four cancels,
two additions), the unshifted preview and the resulting queue.
`TestCommandDragCancelBoundaries` covers another product in tolerance, the
product-blind undragged preview, and suppression by a pending command.
`TestCommunityPreviewSkipsDragCancelSites` keeps the model preview off cancel
sites.

Repair and Reclaim use rectangular world areas, with their command armed and
the left button dragged. Repair visits visible local damaged or unfinished
units. Reclaim visits visible reclaimable features whose authored `blocking`
flag is set, clearing movement and building obstacles such as trees and rocks
while leaving non-blocking grass and moss alone. That flag's movement and
placement meaning is established in [05 R-FEAT-01 §6]; using it to filter this
gesture is user-requested input policy. Reclaim avoids accidental unit
reclamation. Ordinary clicks preserve the existing target resolver. Targets
are captured from the committed frame at release, in publication order; orders
are queued per capable selected actor. An explicit target batch replaces only
at each actor's first admitted target, and never invokes repeat-click removal.
This is a one-time work list, not a persistent area task. Empty areas leave existing orders intact.

Move accepts a left drag with Move armed, or Alt-left-drag from the idle latch
in either mouse-interface mode. Alt is sampled on press; releasing it during
the gesture does not turn movement into selection. Alt-click gives an explicit
point Move, including over units or features. Armed commands take precedence:
Alt continues to choose a construction grid, and R/E followed by left-drag keep
their repair/reclaim areas. Ctrl prevents the idle formation shortcut. With
right-click interface mode, idle right drag on empty ground also draws a
formation. Ordinary left box selection and Shift-additive selection remain
available; Shift at a command's release appends instead and retains the ordinary
queue overlay, including enabled `+showranges` rings. Alt+digit keeps the squad
shortcut. Active command drags suppress placement ranges so their preview stays
readable; releasing or cancelling the drag restores them if placement remains
armed. The traced polyline accepts
curves and zigzags;
units receive individual destinations evenly spaced by arc length, including
both endpoints (a single unit receives the midpoint); whole world destinations
round nearest with halves away from zero. Selected mobile actors are matched
one-to-one to these destinations to minimize total straight-line travel across
the group. The presentation uses an epsilon-scaled auction assignment (the
classical algorithm described in [Bertsekas, §2](https://web.mit.edu/dimitrib/www/Bertsekas_Auction_Assignment_Algorithms_RICO.pdf)):
an actor displaced from a destination bids again, so internal slot order cannot
strand later actors at distant leftover spots. Costs are Euclidean distances;
the final epsilon is `1/(destination count + 1)`, bounding the whole group's
extra travel above the optimum to less than one world pixel, apart from
floating-point roundoff. Coarse passes start at one quarter of the largest
cost and divide epsilon by four, retaining prices between passes. This makes
large selections practical without pursuing imperceptible improvements.
Destination coordinates sort by X then Z before matching, so reversing an
identical destination set produces the same assignments. Equal bids prefer an
unused destination, then coordinate order; actors enter each pass in slot order.
The solver uses floating-point input geometry and quadratic memory, and runs
once on release. Commands still carry whole fixed-point destinations. The line
defines destinations, not mandatory paths around obstacles; existing
pathfinding chooses each route.

All gestures activate after six logical screen pixels of displacement, commit
once on matching release, and cancel on Escape, right cancellation, loss of
focus, modal ownership, or selection/latch changes. Shift at release appends.
Geometry is bounded to 1024 building sites and 2048 sampled path vertices per
gesture to keep input and preview work bounded. These are UI budgets, not
retail constants. Validation covers capture ownership, queue replacement versus
append, footprint spacing, curve sampling, fog filtering, and zoomed overlays.

The shell owns capture and preview in `battle_command_drag.go`; pure geometry
lives in `battle_drag_geometry.go`. An area's `HumanOrderCommand.Targets` slice
is copied at enqueue. The session resolves that work list in actor/target order
and purges only at the first admitted target for each actor. Explicit formation
handles give each mobile actor its own destination without editing selection.
`UIWorldLine` records one non-emissive segment per traced edge inside the same
world overlay used by placement, avoiding one recorded fill per line pixel.

Visual review used real modern GPU captures on Great Divide (seed 7), at
1024×768: mixed legal/illegal grid footprints, reclaim bounds and visible target
markers, and eight selected units distributed around a curved line at 0.75 zoom.
Capture-only input scripting stayed in an isolated QA worktree; assets and PNGs
remain outside the repository. Focused tests cover release without a held sample,
queue replacement/append, reservations, fog, cancellation, classic press behavior,
superseding keyboard commands, and a short release crossing into the radar.

The integrated fast and retail gates passed. The sequential live battle check
used Great Divide, seed 7, 1920×1080, 180 measured frames, 30 FPS, native zoom,
with the metallic glint off on both sides for matching scene metadata. Classic/modern median
cadence was 33.334/33.334 ms; median host draw work was 14.977/9.977 ms.
Frame-by-frame workload censuses matched, including 9–15 burning features and
four active factories per owner. Both captures were inspected. These are paced
host measurements of ordinary battle load, not isolated GPU timing or a claim
about maximum-size drag latency.

### 3.12 The paused-input boundary

**Retail parity, not a Modern departure.** Nothing here goes through
`gameplay.Mode`, and Strict 3.1 behaves identically.

While the single-player pause bit is set the battle host pump does not evaluate
the budget and runs no sub-tick, so the scaled-time anchor stalls and unpausing
yields the one capped burst `[01 §4.3]`. It still runs the host frame — input,
click dispatch and the interface — for as long as the in-game options window is
closed `[01 R-PLAT-01 §1 steps 2, 4, 5]`. In retail that frame *is* the
mutation: click dispatch enters the order dispatcher and the hotkey row writes
the per-unit selected bit and the group word, at dispatch time
`[07 R-CAM-01 §1]`. So a paused retail battle selects, changes build pages and
accepts orders; only their execution waits for the clock.

This build routes those gestures through the immutable input queue of §3.7,
whose sole consumer is phase 1 of a sub-tick. With no sub-tick there is no
consumer, so the queue needs a boundary of its own.

**The boundary.** `Session.stepPausedInput`, called from `Session.Step` in the
position the sub-tick loop would have occupied — the battle state, the pause bit
set, zero runnable ticks — so the once-per-pump executor tail still runs after
it `[01 R-PLAT-02 §7]`. It does three things and nothing else:

1. clears the per-tick big-brother notices, which is phase 1's own leading act;
2. drains the due, paused-applicable **prefix** of the queue through
   `applyHumanCommand`, the same path and the same order phase 1 uses;
3. republishes the committed tick once.

There is no second command path, no host-side predicted selection and no
presentation write into live units: `cmd/nanolathe` is unchanged, because every
dispatcher already resolves its actor from the committed frame (§3.7), and the
republication is what makes that frame current.

With nothing applicable queued the boundary returns without publishing, so an
idle-paused host loop produces no republication churn.

**The equivalence property.** The commands are applied with the tick they are
due for — `GlobalTick + 1`, the tick that has not run — not with the committed
tick. That is the choice that makes every creation stamp, deadline and
queue-insertion decision the one the unpaused run would have written. Because
phase 1 is the first mutation of a sub-tick and nothing runs between the paused
boundary and the resumed tick, applying the prefix at pause time leaves
authoritative state identical to letting tick `T+1` apply it: the same order
queues and stamps, the same resources, and the same position in both random
streams. Exactly-once follows from the queue itself — a drained command is gone
before the clock resumes.

The published frame keeps the committed tick `T`. The global tick, the scheduler
anchor and carry, and phases 2 through 12 are untouched.

**Which kinds apply while paused.**

| Kind | Paused | Why |
|---|---|---|
| Selection replace / toggle / clear, make-selectable | applied | status-bit writes `[07 §9]` |
| Group assign, group recall | applied | group word and selection bits `[07 §9]` |
| Build page | applied | the unit's own page field `[07 §9]` |
| Order (single and area batch), stop, cancel queued move | applied | queue edits stamped with the due tick `[04 §3.3]` |
| Mobile build, factory build, cancel production, stockpile | applied | queue edits through the ordinary construction producers |
| Activation, stance, cloak, self-destruct | applied | one record each, no draw `[04 R-STANCE-01 §2]` |
| Shift state, big brother, no-shake, gameplay mode | applied | session bookkeeping |
| Set resource, set logo, give, view, ATM, visibility, double/half shot, meteor **with** an argument | applied | player-row, visibility-mode and toggle writes, no draw |
| Meteor **without** an argument | **deferred** | enters the storm-arm body: four CRT scheduling draws and a live strike window `[06 §6.5]` |
| Spawn (Modern or developer default handler) | **deferred** | allocates a unit, which consumes creation draws |

A deferred command is not skipped. The drain **stops** at it and leaves it and
everything enqueued behind it in place, so enqueue order is exactly the order
the next real tick applies. Both deferred kinds are chat-console commands, not
battle input, and deferring them is what keeps the boundary's simplest
guarantee true: **a paused pump never moves either authoritative random stream
and never creates a world object**, which is retail's own reading of the pause
bit — it "suppresses simulation progress" `[07 §11]`.

**Acknowledgement voices need no rule of their own.** The producer insertion
arms a record's one-shot caption-pending bit; the `ok` voice is emitted by the
*pump*, when the record is first visited `[04 R-ORD-01 §1]` `[04 R-ORD-01 §13]`.
No pump runs while paused, so an order issued under pause speaks on the tick it
is first pumped — exactly when the unpaused run would have spoken.

**What the republication must not repeat.** `frame.Buffer.Republish` replaces
the committed view of a tick instead of advancing it. Every per-tick one-shot
the frame carries is reset by its own producer before the boundary runs, which
is why repetition is impossible rather than merely unlikely:

* **Presentation events** (audio, voice, message-ring lines, effect spawns, the
  single event cursor) are staged in a window the previous publication reset, so
  a republication retains only what the drained commands raised — and no applied
  kind raises any `[03 R-AUD-01 §7]`.
* **Big-brother notices** (cycle, reset-visited, cancel-follow) are cleared by
  step 1 above, in phase 1's position, so the last tick's notice cannot be
  delivered twice and a drained toggle's notice is delivered once
  `[07 R-CAM-01 §12]`.
* **The interpolation pair.** The superseded slot holds the *same* tick, so it
  must not become the previous one: `Republish` leaves the previous pair cleared
  and `Buffer.Previous` reports nil until the next ordinary publication supplies
  a real earlier tick. The Enhanced blend therefore holds the current pose
  instead of blending two copies of one tick, and no snap occurs
  (DESIGN_GPU_RENDERER §13.5). The paused world raster's digest keys on the
  committed slot, so each republication invalidates it and the new selection is
  drawn (DESIGN_GPU_RENDERER §13.10).
* **Retained channels** — effects, debris, fragments, strips, radar contacts,
  the result, economy rows, the shake offset — are snapshots of live state that
  no phase has advanced, so rewriting them writes the same values.
* **Developer diagnostics** keep their contract: enabling them never forces a
  publication while paused (DESIGN_DEVELOPER_TOOLS §3.1). A republication the
  *input* triggered carries the developer view exactly as the next ordinary
  publication would.
* **Publication counting** is a sub-tick diagnostic and is not incremented by a
  paused republication.

**Modality is preserved.** With the options window open the pump skips the host
frame entirely `[01 R-PLAT-01 §1 step 2]`, and this build matches that by not
reaching `Session.Step` at all from a modal host frame: input queued before the
window opened waits until it closes. The diagnostic capture path also pauses and
snapshots before any drain, so its `pending_human_commands` dump stays coherent.

**Tests.** `internal/session/paused_input_boundary_test.go` locks the tick, both
random streams, the published selection and command page, the empty-queue
no-op, the absent interpolation pair, the deferral prefix rule, the one-shot
audit, and the equivalence property — the last by comparing a
paused-then-resumed run against an unpaused run with the same script through
`PartialStateFingerprint`. `cmd/nanolathe/paused_input_host_test.go` locks the
host half: a mobile build dispatched while paused through the real dispatcher is
addressed to the unit selected while paused and lands on it exactly once, and an
open options window drains nothing.

### 3.13 Optional community selection controls

**Community host policy from the MIT patch source, with a documented Zero
scheme below.** These consumers are
presentation preferences and never depend on `gameplay.Mode` or the renderer.
`presentation.communitySelection` selects Retail (0), Community (1), or
Zero (2) input; the independent
`presentation.doubleClickSelection` enables same-type double-click selection.
Both default to zero; selection values outside 0–2 normalize to zero and the
double-click switch remains a low-bit boolean. With either option off,
the existing retail keyboard and pointer paths are unchanged. Evidence is the
[community patch engine behavior](../research/extensions/community-patch-engine.md)
§4.2 input-and-command surface and its pinned `ExternQuickKey.cpp` source.

With either non-retail selection scheme, unshifted Ctrl+B and Ctrl+F replace the
selection with the next admitted own mobile builder or factory. Nanolathe first
applies its shared host boundary of a completed selectable own unit. Within
that boundary, a factory is idle unless its primary order is `BuildingBuild`;
a mobile builder is idle only when its primary order is absent, `Standby`, or
`VTOL_Standby`, and its committed prior-window health sample is neither zero
nor one. The source's constructor scan itself has no common completion gate;
that is the explicit Nanolathe host boundary. Neither scan reads secondary
orders. Each shortcut owns a persistent unit-slot cursor. It scans committed
units in ascending slot order after that cursor, then wraps once; an empty full
pass clears the selection and resets the cursor. A hit centres the camera and
discards prepared placement. Authored
`CTRL_B` / `CTRL_F` membership wins whenever that category has any member.
Only an empty category uses the patch fallback: builders that are not air bases
or in the patch's commander/decoy class (`showplayername` and `hidedamage` both
set), split by nonzero BMCode for mobile builders and zero BMCode for factories.
Shift+Ctrl+B/F retain the retail additive category shortcuts.

In the Community scheme, unshifted Ctrl+S replaces the selection with completed
selectable own units inside the current battle viewport whose definition belongs to `CTRL_W` and
has its compiled `canfly` flag clear. The pinned source performs that flag test
and contains no `NOTAIR` or `NAIR` lookup; its release notes' claim that those
categories participate is therefore not implemented by that revision. The host
does not assign those category names a guessed capability meaning. The action
also discards prepared placement. Shift+Ctrl+S retains retail's on-screen
selection.

**Zero host scheme.** The author's [controls page](https://zero.tauniverse.com/controls/)
documents idle builder/factory cycling, on-screen armed selection and held
W/B/Y drag filters. Scheme 2 reuses the above B/F cycles and common completion,
ownership and selection gates. Ctrl+S selects on-screen definitions with
`CanAttack`; Ctrl+Shift+S keeps the ordinary on-screen selection. A selection
rectangle in the world view or megamap samples held keys at release: W admits
`CanAttack` definitions with nonzero `BMCode`; B admits the existing builder-cycle mask; Y admits the
factory-cycle mask. If several are held, W precedes B precedes Y. Shift keeps
the existing toggle semantics. While a normal selection rectangle is active,
the host consumes a leading W/B/Y text token without Ctrl/Alt before palette
shortcuts, preserving held-key state and Shift; Ctrl/Alt and other tokens keep their
ordinary route. This prevents a build quickkey from replacing the drag.
Outside an active rectangle, click selection and ordinary shortcuts are unchanged.
These capability predicates, simultaneous-key precedence and common gates are
Nanolathe host input policy implementing the documented categories, not a
claim about the historical DLL's undocumented edge cases. In particular Zero's
authored `CTRL_W` means water units and must not supply its armed filter.
The selector uses the existing options control (`NCYCLE`) with three stages.

`presentation.factoryHundredBatch` is a separate host input preference,
default off and normalized as a low-bit boolean. When enabled, Ctrl+Shift on a
factory product, including a mobile unit in a factory's menu, adds (left) or
subtracts (right) 100 through the existing signed command producer. Alt still
takes precedence with its existing batch of 20; other clicks retain 1 or
Shift's 5. Stockpile buttons retain their separate counts. This factory-only
scope and Alt precedence are explicit Nanolathe host policy. The gesture has
two sources. The Zero controls documentation describes it. The pinned source's
quick-key handler, the file the Community scheme above follows, sets the
counted click's Shift batch to one hundred while Ctrl is held
([community patch engine §4.2](../research/extensions/community-patch-engine.md#42-data-driven-switches-outside-totalaini)).
That source batch also reaches the stockpile toys, which Nanolathe leaves at
five. ProTA 4.8 ships the recorder whose interface upgrade lists "Queue 100
units" ([TA Demo Recorder](../research/extensions/ta-demo-recorder.md)), and a
ProTA 4.8 player reports the gesture. That the shipped 4.8 draw DLL has the
branch is a **Supported inference**. The options UI exposes this preference beside
selection (*100 batch*). The Community preset, which ProTA names, and the Zero
preset turn it on with their selection schemes; the Retail preset turns it
off, and Keep mine changes nothing. No input consumer tests a content-profile
name or gameplay mode. Tests preserve Retail/Community meanings, Zero
water-versus-armed membership, held filters, modifier precedence, signed
counts, persistence and preset refusal.
`TestCommunityPresetFactoryHundredBatchForMobileProduct` clicks a mobile product
in a structure factory's menu: Ctrl+Shift gives 5 under default preferences,
and +100 and −100 once the Community preset is applied.

With `doubleClickSelection` enabled, a platform-classified left or right double-click
strictly inside the battle viewport and over an own unit replaces the selection
with the on-screen selectable own units whose committed numeric definition ID
matches any ID in the committed selection. Shift does not make this additive. The consumer uses the
event kind already classified from native timestamps and rectangle policy; it
does not recognize a second click from simulation or host-frame time. Ctrl+Z
uses the same full-width compiled definition masks without the patch's old
512-definition truncation, but remains available independently of both options.
All four paths read the committed frame and enqueue ordinary typed selection
commands [I6]. The Orders options page exposes both default-off preferences.
Its Restore/Undo affects only that page; Cancel restores the whole entry snapshot.

### 3.14 Optional Community unit labels

The four Community unit-label switches are host presentation preferences. They
do not depend on the selected gameplay profile, do not enter its digest or a
simulation fingerprint, and read only the committed `frame.UnitView`. Counters,
reload bars and the `Vet<n>` footer label default off. Group digits retain the
existing default-on retail behavior; the host option can suppress them. The
client stores the compact `CommunityHUDOptions` value and replaces it through
`SetCommunityHUDOptions`. This implements the authorized §7 candidate without
creating another gameplay capability table
([DESIGN_COMMUNITY_PATCH §7](DESIGN_COMMUNITY_PATCH.md#7-host-and-presentation-features-out-of-the-profile)).

**Counters (Established, independently described from the pinned MIT Community
source).** The unit-label walk considers only a living unit owned by the local
human, at a nonzero projected centre, while the ordinary `damagebars` option is
on. The stockpile label is `<completed> +<queued>` when either value is nonzero:
completed is summed over all stockpile weapon slots, and queued is the positive
count on the one current rear-segment head. It neither sums later rear records
nor checks that head's descriptor. The transport label is `<loaded>/<capacity>`
only when capacity and loaded count are both nonzero. A flying transport whose
capacity is exactly one is excluded. When both labels exist, the stockpile line
sits immediately above the health bar and the transport line one font height
plus one pixel above it; a lone label uses the lower line. Both are centred and
use the source's black one-pixel outline with raw palette index 255 foreground.
The publisher supplies the four counts and the already resolved one-seat flying
classification; presentation neither walks live cargo links nor reads an order
queue [I6]. The transport count includes only cargo-list entries whose resolved
child still points back to this carrier.

**Nanolathe presentation policy — zoom.** The health-bar anchor follows the
live world transform. Counter glyphs, horizontal centering, spacing above the
bar and between lines, and all eight outline offsets remain native framebuffer
pixels at every zoom. The executor rounds the transformed anchor to the nearest
pixel before adding those offsets. Native and detail rest views retain their
existing pixels; this also keeps group digits readable during fractional zoom
(DESIGN_GPU_RENDERER §14.2, §16.3). Device fixtures compare the counter mask
relative to its anchor across native, fractional and detail factors, including
fractional camera translation, and check Classic/Modern parity at both rests.

**Reload bar (Established).** For a complete own unit under the same
`damagebars` gate, scan the three committed weapon slots in order. A slot is
tagged when either its authored definition or its current live weapon has
`reloadbar` bit 0 set. Exclude stockpile slots and zero authored reload times,
then retain the first slot having the largest reload time. The publisher uses
the authored slot definition's reload/type value when nonzero and the live
weapon's value otherwise, preserving a tag by weapon name across a changed
live slot. Progress is zero when remaining reload exceeds the selected maximum;
otherwise it is `maximum - remaining`. The outer inclusive rectangle spans
35×5 at ordinary scale and begins three rows below the health-bar centre. Its
32-unit inner progress width is `32*progress/maximum`; the inclusive fill makes
a positive half-width of 16 occupy 17 pixels. The fill starts at logical palette
entry 144 and darkens by `min((progress*100/maximum)/15, 6)` entries. This is
the presentation-only contract of
[community-patch-engine CP-WPN-7](../research/extensions/community-patch-engine.md#53-weapon-definition-tags-author-facing).
An inspection of the installed stock retail and ProTA4.8 catalogs found no
weapon with `ReloadBar` true. The option therefore draws no reload bar with
those two installed content sets, even when Health bars is on; content with an
authored `reloadbar` tag can exercise it.

**Veteran footer label (Established).** When enabled, an armed own unit whose
ordinary footer kill line is nonempty displays `Vet<n>` whenever its committed
bounded veteran level is above zero. Level zero keeps the retail kill-count
line. The level is the result already bound through `combat.Service.VeteranLevel`;
the HUD never recomputes thresholds from kills. This is the presentation half
of [community-patch-engine CP-UD-1](../research/extensions/community-patch-engine.md#58-unit-definition-extensions-and-spawned-schema-units).

The HUD options page persists counters, reload bars, veteran labels and group
numbers alongside optional allied-resource and weather overlays. It also
exposes the existing `damagebars` bit as **Health bars**, so the prerequisite
for counters and reload bars can be enabled on the same page. Changing that bit
joins pending presentation recording before the live write and advances the
presentation epoch. Each control's hover help states when it has an effect:
counters need stockpiles or loaded cargo, reload bars need tagged weapons and
Health bars, groups need an assigned group, veteran labels need a hovered
eligible unit, allied rows need another active ally, and weather reports appear
in battle. Restore/Undo is scoped to this page, including Health bars; Cancel
restores the entry snapshot, and OK persists the live `damagebars` bit through
the existing settings block. The source draw/admission gates remain unchanged.
The publisher sums completed stockpile bytes over admitted slots, copies only
the positive rear-head amount, and validates cargo back-links. Reload tag-name
membership is cached from the immutable catalog; authored reload/type fallbacks
are resolved before publication.

**Nanolathe host layout.** Allied bars default off and use the committed viewing
player's directional alliance row, excluding that player, in committed economy
order. They show name, stored metal/energy, capacity bars and positive income
with the source's compact-number formatting. The upper-right minus/plus widget
collapses the panel; rows clip above the bottom HUD. This substitutes local
committed records for the source's multiplayer shared-data transport. The
weather option defaults off, shows both wind and tidal rows, and adapts the
source's side-anchor/clamped layout with the retained retail wind hard limit
5000. If the report and clock do not fit beside the resource-strip anchors,
they occupy a backed panel just below the strip beside the left rail; compact
displays must not clamp the report over the energy readout. Current wind and
clock read the committed frame; bounds, tidal strength
and reference generators are immutable battle content. Source arithmetic is
recorded in [community patch engine §5.10](../research/extensions/community-patch-engine.md#510-optional-resource-and-weather-presentation).

### 3.15 Optional megamap

**Policy.** The megamap is a host presentation preference modelled on the
ProTA 4.8 draw engine's full-screen minimap
([ProTA 4.8 shipped megamap](../research/extensions/draw-engine-interface.md#prota-48-shipped-megamap)).
It enters no digest, fingerprint or save, and reads only the committed frame,
the immutable catalog and terrain, and host input [I6].
**Nanolathe Modern policy (user-authorized 2026-09-30):** the host selects the
overview from the selected set's existing base layer. Modern uses camera zoom
instead of this megamap (DESIGN_GPU_RENDERER §16.6–16.8); F9 fits the
whole map and returns, with F2 opening Options. Modern also honors the stored
Tab choice (user-authorized 2026-10-01): `presentation.overview = 0` (Options, default) opens and closes
Options; `1` (Overview) fits the whole map and returns on Tab release. Modern
No zoom keeps Tab for Options. Community 3.9 honors `presentation.overview`:
`0` (Tab: Options, default) enables the same host camera preferences as Modern,
including pinch, wheel, F9 and explicit battle-entry zoom; `1` selects the
separate megamap and disables those camera zoom bindings. ProTA's config
recommends `1`, while Escalation declares no overview preference. A Community
gameplay floor therefore does not impose ProTA's recommended presentation.
Both configuration surfaces let the player return to camera zoom by selecting
Tab: Options without changing the rules. They also keep Camera zoom selectable
while the megamap is active: explicitly selecting Continuous or Steps switches
Tab to Options in the same edit, even when reselecting the stored zoom style.
This lets saved base or per-mod overview preferences be cleared directly from
the zoom control (issue #99). No zoom leaves the selected overview intact.
The front-end draft treats the two preferences as one edit for mod locks,
Cancel, Restore and per-mod persistence, recording the actual Tab change so
a later rules selection does not drop it at Apply. The in-battle transaction
also remembers that the zoom selector changed Tab so Undo restores the entry
choice after a rules change. Modern's camera card retains its independent Tab
choice on Restore. Merely loading preferences changes
neither. Zoom lock and icon style still require the camera consumer.
Strict 3.1 retains the
`presentation.overview` preference: `0` (Options, default) keeps Tab/F2 options and
the earlier three camera presets; `1` (Overview) installs the megamap below.
Modern with the Classic renderer keeps its earlier Tab/F9 controls.
Both renderers draw the community megamap identically because it is one
indexed surface recorded after the world, not a camera factor: it is **not**
§16's strategic view and changes no zoom step, floor or picker there. The
simulation keeps running while it is shown.

**Settings.** All live in the presentation block and are host preferences,
with ProTA 4.8's `ProTA.ini` values as defaults except where noted:

| Key | Default | Patch key |
|---|---|---|
| `overview` | 0 (Tab opens Options); 1 selects the mode's overview: camera zoom under Modern, megamap under Community and Strict | `FullScreenMinimap` |
| `megamapWheel` | 1 | `WheelZoom` |
| `megamapWheelMove` | 1 | `WheelMoveMegaMap` |
| `megamapDoubleClickMove` | 0 | `DoubleClickMoveMegamap` |
| `megamapFlash` | 1 | `UnderAttackFlash` |
| `megamapRadarMinimum`, `megamapSonarMinimum`, `megamapSonarJamMinimum`, `megamapAntiNukeMinimum` | 0 | `Megamap*Minimum` (ProTA's INI sets all to 0) |
| `playerDotColors` | 227, 212, 80, 235, 108, 219, 208, 93, 130, 67 (the draw engine's own defaults) | `Player1..10DotColors` |
| `megamapWeapon1Color`, `megamapWeapon2Color`, `megamapWeapon3Color`, `megamapRadarColor`, `megamapSonarColor`, `megamapRadarJamColor`, `megamapSonarJamColor`, `megamapAntinukeColor` | −1 each (keep the ring's default) | `Megamap*Color` (ProTA's INI sets none) |
| `alliedDotSwatches` | 0 | none: the draw engine always draws the square (below) |

`MegamapRadarJamMinimum` has no setting: the shipped build reads it and never
uses it, and the radar-jammer ring compares against the radar minimum. Icons
come from the existing `strategicIconConfig` resolution (§18.7 of
DESIGN_GPU_RENDERER, including its mod-directory discovery), so a mounted ProTA
uses its own `Icon/iconcfg.ini`. A `Megamap*Color` value of −1 keeps its
ring's research default (below); any other value is the palette index the ring
draws in. The shipped build passes any value unchecked; a Nanolathe palette
index is a byte, so a value outside 0..255 normalizes back to −1 (host choice).

**Input (research contract unless marked host choice).**

| Input | Condition | Effect |
|---|---|---|
| Tab pressed | overview Megamap, battle frame, TALK closed | consumed; nothing else. F2 still opens options; Tab does not close them |
| Tab released | a consumed Tab press is pending | leaves the view if shown (camera unchanged), else enters it |
| Wheel back (negative notch) | `megamapWheel` on, view hidden | enters the view |
| Wheel forward (positive notch) | `megamapWheel` on, view shown | clears camera follow; with `megamapWheelMove` centres the camera on the pointer's map point (pointer clamped to the image) and clamps; leaves |
| Double-click in the image | `megamapDoubleClickMove` on, own-unit double-click below not taken | centres the camera on the point; leaves |
| Left press in the image | no prepared order | starts a box, clamped to the image |
| Left release | box extents both ≥ 9 pixels | own completed selectable units whose `(x, z − y/2)` lies strictly inside the converted rectangle: replace without Shift, toggle each with Shift |
| Left release in the image | selection nonempty and an order or placement prepared | the world-click handler at the megamap point, with Shift (host choice: in the margin, or with no selection, a prepared order issues nothing) |
| Other left release (not at the last double-click position) | — | with the select cursor showing: an own selectable, completed hovered unit is selected (Shift toggles); otherwise with a selection: right-click interface clears it, left-click interface sends the neutral order |
| Right release | — | a prepared order or placement is cancelled; else left-click interface clears a selection; right-click interface sends the neutral order, or Guard when the select cursor shows |
| Double-click on an own hovered unit | `doubleClickSelection` on | that unit's definition across the whole map (the Ctrl+Z set); view stays open |

Entering plays alias `Options`, leaving `Previous`. Entering clears the
hovered-unit word, any box, the world drag state and the placement site-valid
bit. While the view is shown every pointer record inside the battle viewport
belongs to it — except a release whose press began outside, which reaches the
HUD capture as before — so the world-click path never sees a hidden-world
point. Host choices: the edge/arrow scroll pass and middle-drag are held while
the view is shown, so leaving by key (or by wheel with `megamapWheelMove` off)
returns to the camera the player left, which is what ProTA's preference text
promises; §16's wheel zoom is not taken in Megamap mode while `megamapWheel` is
on, because the same notch would both zoom and enter; a notch is one
Ebitengine wheel unit of `ZoomScrollY` (precise trackpad scrolling excluded);
the chrome's own wheel consumers still see the wheel, as the research says the
patch never consumes it.

Pointer conversion is the research's: image pixels divided by the float scale
factors and truncated, with no half-height correction; the height is the
terrain height at that point, or sea level where terrain has none
(`Session.GroundPointAt`). The cursor, the footer hover, `pickTarget` and
`cursorWorld` all take this megamap branch while the view owns the pointer, so
the cursor shape, the footer and every order agree on one point and one unit.
The hovered unit is the research's first admitted contact whose reference
point `(x + footX·8, z + footZ·8)` lies strictly inside both the 22×22-pixel
search box and its picture box, each converted to world units. While the view
owns the pointer the feature lookup at the pointer cell reports no feature, as
the shipped build redirects it, so the cursor and footer see none; the order
resolver's own feature probe at the point is unaffected.

**Orders the view sends itself.** They bypass the world click and its
armed-click shape gate [07 R-CAM-01 §14] and go straight into the selection
broadcast [04 R-STANCE-01 §5]: the numeric code, the hovered unit as target,
the pointer's world point and Shift as the queue flag
(`megamapSend`). The broadcast's own rules apply unchanged, including the
target exclusion and the nearby offsets of positioned move and patrol results.

* The *neutral* send is code 1. Each selected unit resolves it separately
  through the Interface Type contextual rule [04 R-ORD-02 §1] ("Code 1 —
  contextual"): attack, assistance, resurrect or reclaim of a mapped
  reclaimable feature, or a move; a unit that resolves nothing gets no order.
* *Guard* is code 7 on the hovered unit: each selected `canguard` unit gets
  the ground or air follow order, the others nothing. After the send the
  latch-to-idle side effects run (Shift persistence cleared, Stop radio group
  reset) and the latch is restored to Guard, so the prepared order stays Guard
  until the next right release cancels it — the shipped build's quirk, kept.
* The *select-cursor* test in both interfaces is the ordinary cursor chooser's
  shape over the hovered unit with the prepared latch [07 §8], not an
  ownership test.

**Build placement.** A left release with a build armed and a selection reads
the site-valid bit as last written: set, each selected builder gets the
mobile-build order and `oktobuild` plays (the placement stays armed with
Shift, otherwise returns to neutral); clear, `notoktobuild` plays, no order is
issued and the placement stays armed. *Host choice, departing from a
Supported inference:* the shipped build revalidates only through the engine's
per-frame preview, which needs the engine's own pointer record inside the game
view; the research infers that record stays frozen at the last position the
game saw, so after the pointer crosses the build menu every megamap build
would play `notoktobuild` and an own-unit click would not select. Nanolathe
keeps its ordinary preview revalidating at the megamap point, so a building
chosen from the build menu can still be placed. A manual ProTA 4.8 test
settles it: open the view with the pointer over the battlefield, choose a
building by hotkey and click a legal site (predicted to build), then choose
one from the build menu and click a legal site (predicted `notoktobuild`).
The `TODO(question)` at `megamapWorldClick` carries it.

*Click snapping* on the megamap stays **Unknown**: whether the extensions'
mex and wreck snap finds a site from the megamap's point was not traced. The
megamap takes no click snap: the placement preview skips the build snap while
the view owns the pointer, and a megamap world click never takes the reclaim
snap. A ProTA 4.8 observation with a nonzero Mex-Snap radius would settle it.

**One shared frame (host choice).** Every layer — terrain, fog, icons,
projectiles, rings, the overlay — and the pointer conversion share one extent:
the retail play area [fmt tnt], `(TNT width − 2) × 16` by
`(TNT height − 8) × 16` world units (`camera.MegamapExtent`). The shipped
build fits and samples its terrain picture over the play area but scales
everything drawn over it, and the pointer, by the larger
`(width − 1) × 16` by `(height − 4) × 16`, so its overlays sit up and left of
the terrain by up to 16 and 64 map pixels; Nanolathe does not copy that
defect.

**Layers.** One indexed surface of the battle viewport, composed on change
(tick, hover, tracked unit, Shift, box, placement pointer, blink or layout) and
uploaded with a stable identity and revision; the patch's `MegamapFpsLimit` is
therefore not a setting (ProTA sets it to 0, unlimited). In order:

1. *Margins* in palette index 95. The image is fitted to the play area's
   aspect in single-precision floats as the shipped picture is: a wider extent
   keeps the view width and takes `trunc(w / cols × rows)` rows, a taller one
   keeps the height and takes `trunc(h / rows × cols)` columns; centred when a
   spare margin exceeds two pixels. Host choice: when the two aspects are
   exactly equal the shipped build makes a square of the smaller side;
   Nanolathe keeps the width branch, so the picture always has the play area's
   aspect.
2. *Terrain.* The shipped picture: the map's own tile art point-sampled over
   the play area, keeping raw palette indices — no averaging, colour matching
   or dithering — drawn through the battle palette. Column `c` samples source
   column `trunc(c × stepX)` with `stepX = extentW / imageW` in single
   precision (rows likewise), reading the tile grid at `(col/32, row/32)` and
   the byte `(col mod 32, row mod 32)` of that tile. It is built once per
   battle, and again only if the image size changes. Host choice: the shipped
   build never lets the step fall below one pixel, so a picture larger than
   the play area runs on into the excluded edge tiles and then reads past the
   tile grid (content undefined); Nanolathe keeps the fractional step, so the
   picture magnifies the play area and never reads past the grid.
3. *Fog.* The research's table from the committed grids: index 0 where the
   viewer's mapped bit is clear, the gray table where current LOS is absent.
   Sampling steps by float additions over the play area's span of the LOS grid
   (`extent / 32` cells, the shared frame), rows starting at
   `−(sea level / 20)` and clamped at zero. The grids themselves encode the
   mapping/LOS mode (a disabled mode fills them), so no separate flag test is
   needed.
4. *Projectiles.* The shipped gate (`render.MegamapProjectileAdmitted`). The
   cell is `x / 32`, `(z − y/2) / 32` in whole world units, signed divisions
   truncated toward zero with `y/2` first; a negative cell is rejected, and so
   is one **strictly greater** than the viewing player's LOS-grid width or
   height, so one equal to them passes. Then: the owner is the viewing player
   or in its alliance row; otherwise, with current sight on (mode bit 1), the
   viewer's current-sight byte at the cell; otherwise Unmapped (mode bit 0)
   admits every projectile; otherwise the viewer's bit in the mapping word. A
   cell equal to the width reads the next row's first cell, as the row-major
   index does; host choice: an index past the grid's end is not read and
   rejects. A weapon with `twophase`, `cruise` and `targetable` together draws
   `nukeicon` in the owner's dot colour, or the `nuclogo` frame when no such
   picture exists; any other draws a 2×2 block in the minimap's projectile
   colour.
5. *Unit icons* for the admitted minimap contacts [03 §3.9] with a
   definition. An identified unit (own, or passing the painter's visibility
   predicate) takes its configured `[Icon]` row, else `unknow`; an unidentified
   contact takes `nothing`. Selected art beats hover art; `FillColor` becomes
   `playerDotColors[logo colour]`; the centre is the projection of the
   position less half the footprint, with the half-height shear; left/top
   clipping shifts instead of cutting and the right edge clips at the
   four-aligned width. With `megamapFlash`, a unit whose damage-blink byte is
   nonzero loses its icon pixels while the committed radar blink phase is
   clear [01 R-CORE-03]; hover circles and rings still draw. Without a
   community configuration the §18 generated vocabulary is quantised to
   16-pixel indexed pictures (a quarter-pixel team contour, white glyph or
   halo, or a half-pixel black body, claims a pixel), `nothing` becomes
   the minimap's own `radlogo` frame, and `unknow` is the generated fallback.
6. *Rings* centred on the icon, radius `distance × rowPitch / extentW`: for a
   selected allied unit, radar/sonar and jammers above their minimums with no
   activation test; interceptor slots of an `antiweapons` unit above the
   antinuke minimum at `(coverage − 512)`, dashed by the published slot
   indicator and the blink phase; and, while Shift is physically held, the
   enabled slots 3, 2, 1 at raw `range` of the hovered allied unit and of the
   allied unit whose command page is open. Each ring takes its
   `Megamap*Color` setting, or at −1 its default: weapon slot 1 colour-map
   entry 6, slots 2 and 3 raw palette index 1, radar and sonar entry 10,
   jammers entry 12, interceptors entry 15.
7. *Selection box.* While a box drag is in progress and both screen extents
   exceed eight pixels, one outline in colour-map entry 15 joins the press
   point and the clamped pointer; there is no inner frame.
8. *Placement ghost.* With a build armed and the pointer on the image, the
   armed footprint (`footX × 16` by `footZ × 16` world units, scaled and
   truncated) centred on the projection of the pointer's world point with the
   half-height shear, moved back inside the image at an edge; entry 10 over a
   valid site, entry 4 over a refused one. The shipped row-building mode draws
   each queued row position instead (valid 234, or 240 while the
   construction-kickout selector is in its clearance state, refused 214
   [community patch engine CP-CON-6]). Nanolathe has no row-building mode
   over the megamap — the Modern command drag cannot start there — so no row
   ghost is drawn; one added later would take the same three physical
   entries by the same clearance test as the viewport preview.
9. *Queued orders*, only while Shift is physically held
   (`drawMegamapQueuedOrders`). The walk covers the local player's units in
   slot order, in play and not death-marked. *Focus* units are the hovered
   unit, the camera-tracked unit and the command-page subject; another unit
   is drawn only if selected, or if a focus unit allied with the local player
   has a build list (the builder context). From a running anchor that starts
   at the unit, each primary-list node's descriptor draw-mask selects: bit 1,
   the build-site outline with the ghost's geometry (entry 10 for a selected
   unit, entry 1 otherwise), a focus unit's dash chain to it, and the anchor
   moving to the site; bit 2, the resolved anchor — a target unit's committed
   position while the local player can see it, else the order position —
   with the order icon for focus and selected units (once per exact point per
   frame), a focus unit's chain, and the anchor advancing; bit 8, the icon
   alone; bit 16, for a cloaked focus unit, an entry-15 circle of radius
   `trunc(mincloakdistance × scaleX)`. Icons are the cursor-art entry of the
   descriptor's icon byte (1–20) at frame `(tick / (2 × ticksPerFrame)) mod
   frames`. The chain places `pathicon` sprites from anchor to anchor, both as
   `(x, z − y/2)`, spaced by the world length of a 20×20 image-pixel diagonal
   (`√(trunc(20/scaleX)² + trunc(20/scaleY)²)`), none unless the segment is
   longer than one spacing, the first `(age mod 20) × spacing / 20` along at
   frame `(age / ticksPerFrame) mod frames` and each later one the next frame;
   length and direction use the endpoints clamped to the extent, positions
   start from the unclamped anchor. Two `TODO(question)` sites remain: the
   unpublished cached position of a target the local player cannot see (the
   stored position stands in), and the cloak circle's centre, which the
   research does not name (the unit's projected position, where retail's own
   overlay centres its range rings [07 R-P0-11 §3]).

The software cursor stays the client's ordinary top layer. The megamap draws
no whiteboard: `PlayerMarkerPcx` is the whiteboard's dot-marker strip only,
the shipped megamap never draws the strip, markers or lines, and its blit
covers any whiteboard content inside the game view
([draw-engine-interface "`PlayerMarkerPcx` is whiteboard-only"](../research/extensions/draw-engine-interface.md#prota-48-shipped-megamap)).
Nanolathe has no whiteboard, so nothing reads it.

**Allied resource bars.** With `alliedDotSwatches` on, each allied row of the
optional allied income panel begins with an 8×8 filled square in that player's
`playerDotColors` entry, indexed by the player's logo colour (so a logo change
moves it; a player index above 9 gives palette index 0), 36 pixels right of
and one pixel below the row's origin; the name moves past it (host layout).
It is the table's only use in the panel
([draw-engine-interface "Allied resource bars"](../research/extensions/draw-engine-interface.md#prota-48-shipped-megamap)).
The draw engine always draws it; the setting keeps the retail default off, and
the ProTA controls preset turns it on
([DESIGN_MODS_MUTATORS §4.3](DESIGN_MODS_MUTATORS.md#43-selection-and-precedence)).

**Files.** `internal/camera/megamap.go` (lens and shared frame),
`internal/render/megamap.go` (fog, icon blit, rings, projectile gate, overlay
geometry) and `megamap_picture.go` (terrain picture),
`internal/client/megamap_icons.go` (indexed icon bank) and `megamap_draw.go`
(surface record), `internal/session/megamap_query.go` (ground point), and
`cmd/nanolathe/battle_megamap*.go` (state, input, composition, overlay);
`community_income.go` draws the allied swatch; `battle.go`, `battle_hud.go`,
`battle_placement.go`, `battle_selection.go` and `battle_cursor.go` carry
one-line hooks.

**Loading at battle entry — Nanolathe host policy.** The icon bank is built
when the battle is committed, if the Megamap overview is selected then, and
the viewing side's numbered build pages (`<unit><N>.GUI` for every page its
builders author, with their page art) are loaded into the side rail's caches
at the same point (`cmd/nanolathe/battle_first_use.go`). Built on first use,
the frame waited for them: a traced game froze 88 ms on the first Tab and
7-9 ms on each builder's first menu. Entry already spends a long frame
composing the battle, and the two take about 70 ms and 40 ms there. Nothing
else changes: the caches, their contents and every later lookup are those of
first use. The Megamap switched on mid-battle, and pages of a captured
builder of another side, still load on first use.

### 3.16 Optional victory cue

**Policy.** A host presentation preference modelled on ProTA 4.8's renderer,
which plays a victory sound on every local win
([ProTA engine, victory cue](../research/extensions/prota-engine.md#victory-cue-on-multiplayer-and-skirmish-wins)).
It is audio presentation, not gameplay: it reads only the committed frame and
host preferences, sends nothing and enters no digest, fingerprint or save
[I6]. `presentation.victoryCue` is `0` by default, which is retail: only the
campaign trigger cue plays, once per satisfied victory condition
`[08 R-TRIG-01 §8]`. It is the HUD page's *Victory cue* switch and a row of
the ProTA controls preset
([DESIGN_MODS_MUTATORS §4.3](DESIGN_MODS_MUTATORS.md#43-selection-and-precedence)).

**Behaviour (research contract).** With the switch on, each host frame that
shows a committed result the local viewer won (not a draw), with the local
slot not watching, runs the hook with the committed global tick. The hook
keeps one value for the process, zero at start and kept across battles and
content reloads. When the tick is lower than that value, or more than 300 ticks
after it, it plays the alias `Victory Condition` through the campaign cue's
by-name path (`Audio.PlayUICue`); in every case it then stores the tick.
Because the tick stops at the end, the cue plays once per shown win. A loss, a
draw or a resignation never reaches it. No session kind is excluded: skirmish,
Survival and campaign wins all play it. A campaign win therefore also plays one
cue when the result first shows, on top of the per-condition cues retail played
earlier. The hook's two edges are kept: a first win shown at tick 300 or
earlier in a process plays nothing, and a later battle plays only when its tick
is below the stored value or more than 300 ticks above it. With the switch off
the hook neither plays nor stores.

**The title gate.** The hook is reached only through the composer's gate
before both end titles `[07 §11]`. That gate skips both titles when the local
slot's lobby-record watcher bit is set, and it tests nothing else. Nanolathe
maps the gate to the committed frame: `Players[Selection.LocalPlayer].Watcher`,
which publishes the watcher bit ORed with this build's observer controller.
The result rows and the battle-start camera apply the same exclusion. When the
gate fails, the hook neither plays nor stores, as in the shipped hook. Retail
sets the bit only in multiplayer, so an ordinary campaign, skirmish or Survival
slot always passes. The retail title is composed into one live battle frame,
the one after the latching tick `[07 §11]`. Nanolathe instead runs the hook on
every frame that shows the result. The stored tick keeps that to one cue,
because the committed tick no longer advances. Nanolathe also draws the in-battle end
title (§3.8 "In-battle end titles"), but the cue does not depend on it
being drawn.

**The switch.** The HUD page's *Victory cue* row is an ordinary HUD switch.
A click writes `presentation.victoryCue` through the same options transaction
as the other rows: OK saves it, and Undo, Restore Defaults and Cancel take it
back. Like every row on the page, the click plays only the page's `Options`
cue. It does not preview the victory sound, because the hook plays only for a
shown win. The row used to be missing from the options dispatcher. A click
moved only its drawn stage, and the next click on any other HUD switch redrew
every row from the preferences and showed the Victory cue as Off again.

**Files.** `cmd/nanolathe/victory_cue.go` (the hook), a one-line call from the
result branch of the battle update in `battle.go`, and the switch in
`community_hud_options.go`, dispatched with the other HUD rows in
`retail_menu_options.go`. `TestVictoryCueTriggerRule`,
`TestVictoryCueWatcherGate` and `TestVictoryCueOffIsRetail` lock the rule, the
title gate and the retail default. `TestCommunityHUDEverySwitchCommits` locks
the switch: each HUD row writes only its own preference, and the click plays
only the `Options` cue.

### Community map downloads

The map selection screen offers **More maps** above its minimap, a user-authorized
2026-10-05 desktop content browser. It lists community maps with download
sizes and installed status, downloads a chosen map and its shared features,
and returns it to the ordinary map selector. Progress, cancellation and
failure messages belong to this frontend flow, with no network activity in
battle. Installation preserves the active mod and the live skirmish or
Survival setup. The catalogue shows a small authored minimap before downloading;
each removable locally downloaded map has an X inside its row in both lists.
The X confirms removal of that row without changing the map selection, and
its draw and pointer bounds follow list scrolling and clipping.
A downloaded package the mount left out shows as "not loaded" in the catalogue,
with its reason in place of the homepage line, and keeps its X so it can be
deleted ([DESIGN_CONTENT_VFS §5](DESIGN_CONTENT_VFS.md#5-divergences)).
An installed map whose package, or a feature package it requires, the
catalogue has republished shows "update available", and Load reads Update. A
map that the installed game or selected mod already supplies shows "in your
install", explains that it is already in the map list, and greys Load.
Retail, manual and active-mod maps stay protected. Catalogue trust, library layout and mount precedence are
owned by [DESIGN_MODS_MUTATORS §5.6](DESIGN_MODS_MUTATORS.md#56-community-map-catalogue).

### Online games

`MULTI` opens the online chooser of
[DESIGN_MULTIPLAYER §16.6](DESIGN_MULTIPLAYER.md#166-first-online-lobby), a
small popup over `MAINMENU` on the base install's message-box frame
(`guis/msgbox.gui`, its OK button cloned for every control), so a mod cannot
move it. It holds one sentence ("Play online with friends: create a game and
send them its code, or join with the code a friend sent you."), **Create
Game** and **Join Game** in the stock 96×31 button size, a status line, and
off the main path a small **Server** button with the current server beside it
and **Cancel** (or Escape).

**Join Game** replaces the chooser with the base install's address window
(`guis/tcp.gui`), captioned "Enter the room code your friend sent you", with
**Join** and **Cancel**. Its field takes focus at once; the code may be typed
or pasted (Ctrl+V, Cmd+V or Shift+Insert where the host has a clipboard
bridge: macOS, Windows, Linux and BSD desktops with a clipboard program
(§3.2), and the browser build, DESIGN_BROWSER_HOST §4 contract 9), is
shown in capitals, and ignores case,
spaces and dashes. Enter joins. Escape clears the field and a second Escape
cancels. Each refusal — no game with that code, a full or started game, a mod
that is missing or cannot be mounted, a server that cannot be reached —
keeps the window open with the reason in plain words under the field.
**Server** opens the same window captioned "Enter the server address", filled
with the current server (default `relay.nanolathe.gg`; a bare host means
`wss://host/relay`, `host:port` is native TLS, a `wss://` URL is taken as
typed, and a `ws://` URL is accepted only on a numeric loopback address, for
a relay on this computer). OK saves it as `onlineServer` and returns to the
chooser; an address that is not one is refused in the window, and OK on an
empty field restores the default.

Any build can create and join. Online games need the base game or an
installed mod: a `--root` stack or `--mod-config` content is refused, and so
is a mod installed from a folder, which has no archive identity to send.

**Create** opens a ten-seat room at once with the host's defaults: a skirmish
on its current skirmish map with its four skirmish options, its mounted mod,
mutators and unit restrictions, and a fresh seed pair. **Join** asks the relay
for the room's base configuration and adopts its mod, which is fixed for the
room. A mod that is installed but not mounted is mounted through the ordinary
content reload, which keeps the player's own mutators and resumes the join on
the new shell; a missing mod, or a different copy of it, is named and nothing
is joined. Compiling the catalog and opening the room run on a job goroutine;
While Create connects, the chooser says so with its choices greyed, and
Cancel abandons the job and closes any room it opens late; Cancel in the code
window does the same for a join.

The lobby is the authored `SKIRMISH.GUI` window, opened as the setup screen
with its ten runtime rows. Each present player has a row in seat order:
"Player n", marked "(You)" or "(Host)", their side in the setup screen's side
art (any side the catalog defines), their colour in the setup screen's
`logos.gaf` column, their team as the allegiance symbols (none, or teams 1–5,
joined when shared and split when alone; hidden in Survival) and "Ready" in
the metal column; the lobby's copy of the backdrop paints over the unused
Metal and Energy headings and draws a Ready heading. Players click their own
side and team to cycle them while not ready. Their own colour steps as the
setup screen's does `[08 R-SKIR-01 §1]`: a left click one colour on, a right
click one back, past every colour another present player holds. Each arrival
takes the lowest free colour, so every player's is their own.

The host's computer players
([DESIGN_MULTIPLAYER §6.6](DESIGN_MULTIPLAYER.md#66-computer-seats)) follow
the present players, in the order every seat composes them. Only the host
sees an **Add computer** row, as the name button of the row after the last,
while one more computer fits with room left for two humans: computers plus
the larger of the humans present and two stay within the map's start
positions and ten players, or Survival's three survivors. It is greyed while
the host is ready or checking. A new computer plays the Modern AI at the
host's skirmish difficulty, with no team, the side its row position
alternates to and the lowest colour no player or computer holds. Its name
button is captioned as the setup screen captions a computer row, `Modern AI`
or `Classic AI`, numbered among the computers when there are several, and
the host's click steps Modern AI, Classic AI, removed, as a Survival ally row
does. Its side, team (hidden in Survival) and colour cycle with the host's
own row controls; its colour steps past colours held by players and other
computers, and a player's own colour steps past the computers' too. The
energy column steps its difficulty Easy, Medium, Hard, drawn in the
headings' face over the button under a Difficulty heading shown while the
room has a computer; the Ready column is empty on computer rows. Each row
shows the colour composition will give the computer. Every computer change
replaces the base configuration and clears readiness like any host setting;
guests see the rows read-only, with help naming the AI. Switching to
Survival with more than one computer is refused ("Survival allows one
computer player. Remove the others first.").

The room code is drawn beside the title in `HATT14`, in two groups of three,
with **Copy** where the host has a clipboard bridge (macOS writes AppKit plain
text, Windows `CF_UNICODETEXT`, Linux and BSD desktops hand the code on stdin
to the first of `wl-copy`, `xclip` or `xsel` the session has, and the browser
build uses `navigator.clipboard.writeText` on secure pages; a host without a
bridge, or a desktop without one of those programs, hides it).

The host's settings stay open until Start: the game type (Skirmish or
Survival, in Difficulty's place), **Select Map** through the ordinary map
selector, which returns to the lobby, the four standard skirmish options
(Commander, Location, Mapping, Line of Sight) and, for Survival, the Survival
setup screen's Wave Pace, Air Waves and Naval Waves. Each change replaces the
room's base configuration, which every seat adopts as it arrives; the mod,
mutators, restrictions and seeds never change. A guest sees the controls
greyed, and a map it lacks is named on the status line ("The host chose
_map_, which you don't have.") with Ready greyed.

**Ready** composes the final configuration from the latest base and the
present seats in seat order, with their teams and sides, then the base's
computers, prepares this seat's
battle at its slot and runs the pre-start rehearsal of DESIGN_MULTIPLAYER
§16.7 on a job goroutine, then sends the configuration-identity and rehearsal
digests; **Not ready** withdraws them. While the check runs the status line
says so and Ready is greyed; a check that fails is reported. Any join, leave,
team, side, colour or settings change clears every seat's ready, and the
status line says which. Ready is greyed, and the status line explains, while fewer than
two players are present, a skirmish has more players, computers included,
than the map's start positions or ten, everyone — computers included — is on
one team ("Everyone is on one team. Someone needs another team, or none."),
or Survival has more than three players, computers included.
**Start** is the host's, enabled when everyone present is ready and the
digests agree; a mismatch is stated in plain words ("Your games simulate
differently. Every player needs the same version."). Hovering a control shows
its help in the status line instead. **Leave** (or Escape) leaves the room. A
relay failure — the host leaving, the 30-minute wait, a dropped connection —
returns to the chooser with its reason.

Started enters the prepared battle at the slot the relay reports, which must
be the slot this seat composed, through the paced lockstep driver, as the
command-line play test does after its dial, with no opening arrival. A
player's own result shows as soon as it is final, and a defeated player may
leave while the others play on. Leaving the battle or its result returns to
the chooser, which says whether the game ended, stopped (with the
transport's reason) or was left.

In an online battle `+net`, or the FPS display, shows a network overlay at the
top left of the world view, below the resource strip, in the side's console
face. Its first line gives the executed tick, the ticks executed in the last
second and the stalls (gaps of more than three tick intervals between
executed ticks). The second gives the grants received ahead of the executed
tick and the grant jitter: the 95th percentile, over the last five seconds,
of how far each grant's arrival gap strays from a thirtieth of a second.
Then come the relay round trip, from this seat submitting an order to the
grant that carries it, and the order latency, from submission to the
executed tick, each as the median and 95th percentile of the latest 64
orders; and the traffic in and out over the last second, in kilobytes and
messages a second, from the connection's own counters ("Traffic not
reported" on a connection without them).

With the relay's match report
([DESIGN_MULTIPLAYER §16.5.2](DESIGN_MULTIPLAYER.md#1652-continuous-grants-and-client-playout))
the next line reads "Checksums agreed through tick _n_", the last tick whose
battle-ended bits, and on every 30th tick unit checksums, the relay has
compared across every playing seat. A table of the human seats follows, one
row each: "Player _n_", marked "(You)"; the state; the relay's ping to the
seat ("--" until measured, in seconds from one second); and how many ticks
the seat is behind the newest grant this client holds. The state is playing,
defeated or won from the shared results, left once the relay no longer
compares the seat, or holding, in red, for the slowest playing seat once it
is 30 ticks behind — the relay's lead bound, where every other seat stalls —
unless every playing seat is as far behind, as while the opening holds them
all. Without a report (an older relay, or the loopback play test) the line
reads "No match report from the relay" and each seat shows only playing,
defeated or won, so a seat that left cannot be told from one still there.

With the FPS panel also shown the overlay stays beside the panel where it
fits and otherwise goes below it, its seat rows in two blocks when one is
too tall for the space left. Cells are two spaces apart, closing towards one
where the width is short. From 640×480 up it covers neither strip nor the
panel.

At the end the host prints one line to standard error: how the match ended,
its duration and ticks, the average and worst ticks per second, the relay
round trip and order latency (median and 95th percentile over the match),
the connection's traffic in and out (bytes and messages) and the stalls.
All of these are host timings; nothing reaches the simulation.

### 3.17 The Nanolathe screen

**Policy.** A Nanolathe-owned setup screen, user-authorized 2026-09-28 as a
prototype on its own branch. It is presentation and host preference only: it
writes the same settings the options pages, the Mods & Mutators screen
([DESIGN_MODS_MUTATORS §8](DESIGN_MODS_MUTATORS.md#8-presentation)) and the
controls presets already write, adds no gameplay rule and enters no digest,
fingerprint or save. The main menu's *NANOLATHE* button opens it; a shell with
no window keeps the Mods & Mutators screen.

**Host.** `ebitenapp.RunOptions.Screen` takes an `ebitenapp.FullScreen`.
While it is active it owns the window: `Layout` returns the display's own
pixel size rather than the authored 640×480 canvas, `Draw` hands it the
whole screen, it reads the pointer and keyboard itself, and the client is
fed an idle sample so nothing underneath reacts. The client keeps stepping,
so a content reload the screen requests still runs between host steps. The
screen draws the mounted game's software cursor last, with the authored GAF
hotspot and playback cadence. Normal is used over settings, and Hourglass while
the requested scene or content is loading. Its independent playback follows a
content reload without changing the cursor beneath the screen. The native
pointer is hidden while this art is available and remains the fallback when it
is not. Closing
waits until every key and button is up, keeping the last frame (the window
is not cleared each frame), so the Enter or Esc that closed it never reaches
the menu as a fresh press. `internal/platform/screenkit` holds its toolkit:
the film typefaces' glyphs as mip levels, paint helpers and hit regions. It
is a platform package, allowed to import Ebitengine.

**Control shapes.** The full animated background is retained. Control discs,
rings, strokes and convex polygons draw through one shader
(`screenkit/shape_draw.go`) that computes each pixel's antialiased coverage
analytically, as the fraction of the pixel square [x, x+1)×[y, y+1) inside the
shape. A disc of radius up to 8 takes its exact area in the pixel; a larger disc
treats its edge as straight across the pixel at the radius √(r²−1/12). A ring is
its outer disc less its inner. A butt-capped stroke is the product of exact
box-filtered slabs along and across its segment, and a polygon the product of
its edges' exact box-filtered half-planes. Shape-local coordinates, kind and
parameters travel in vertex attributes, so no shader uniform varies and
consecutive shapes merge into one draw command however they animate; nothing is
cached. Previously Ebitengine's antialiased vector path rasterized each shape
through stencil passes, and every animated lamp — the hero's slide-in, hover
fades, the staging blinkers — paid that cost: a page switch froze the screen for
up to 615 ms on an M3 Pro, now at most 15 ms. Geometry follows the vector
painter's conventions, with a stroke's width centred on its segment or radius.
Disc, Ring and Line keep its reading of their colour as alpha-premultiplied;
Poly keeps straight alpha. Degenerate input (non-positive size, non-finite
coordinates, or coordinates beyond float32's integer range) draws nothing. The
program compiles on first use, and construction and drawing stay on the game
goroutine. A hidden native fixture compares fractional, clipped, translucent and
overlapping controls with the vector painter: pixels clear of an edge blend
identically, edge pixels differ only by antialiasing method, and every shape's
total coverage stays within 2% of its area (5% for sub-pixel shapes). This is
Nanolathe host presentation policy, user-authorized 2026-09-30, and changes no
battle pixels, pacing or simulation state.

**Frame work and pictures.** The screen builds its page catalogue once and
rebuilds it only when the installed mods' names or versions change: they are the
one value its builders capture; every other value is read by the cards when they
run. Each frame reads the shell's live state once, for the Apply count and the
cards' changed lamps. A worker goroutine for each content set resolves the unit
pictures — card portraits, mutator examples and the sidebar demonstration's
products — through the content roster and decodes them. It starts once the
screen's catalog has compiled, normally while the main menu idles. The game
goroutine only uploads decoded pixels and never waits for a decode. A picture
not yet decoded draws nothing that frame, and a later name never stands in for
an earlier one still being read. The worker stops, and the game goroutine waits
for the file in hand, before anything that can close the content's archives: a
content switch, closing the screen, leaving the main menu, or a window opened
over it. The display's device scale is read at most four times a second, or
when the canvas size changes, and the host's own layout reads it likewise.
Hit-region names, labels, counts and wrapped paragraphs are kept rather than
rebuilt every frame. This is host presentation policy and changes nothing drawn.

**Layout.** One unit is `min(height/900, width/1560)` pixels, so a 4:3
window keeps the header on one line. A tab row (*Game*, *Mutators*,
*Graphics*, *Effects*, *Controls*) and *Back* / *Apply* (with the count of
changed cards) head the screen; under the wordmark one line names the
content, rules, renderer, mutators and unit restrictions a battle started now
would use, each part a jump to its card. The focused card fills the left: kicker, title, one
control of its kind (lamp meter, throw switch, stepper, the three rule
layers, side-by-side choices, or the content list), a description, a lock or
renderer note, chips and *Compare*. The page's cards run along the bottom;
the focused one lifts. Arrow keys choose and change, Tab pages, Space
compares, Enter applies, Esc goes back. The content list shows every
installed mod with its version, its rule lock and whether it brings its own
controls, and scrolls past five rows.
In the content list, Up selects the preceding row and Down the following row,
revealing the selection when needed. Wheel and two-finger scrolling, scrollbar
arrows, track clicks and thumb drags move the viewport without changing the
draft selection; redraw preserves that viewport even with the selection out
of view. Fractional wheel travel accumulates, and each whole unit moves one
row. The wheel target includes the scrollbar column. A track press centres
the thumb on the pointer; a thumb drag retains the original grab offset and
continues outside the track, clamped to the first and last viewports.
Action buttons and key caps borrow the base game's `BUTTONS0` raised and pressed
frames. Their borders retain their native proportions and their interiors fill
the settings rectangles; caption placement and the existing hit targets survive.
For this screen's darker gunmetal styling, the faces use 65% of their authored
RGB brightness and the three-pixel bevels use 85%; this preserves the texture and
raised/pressed geometry. Hover adds at most 8/255 white opacity. These are settings
screen styling choices, not a claim about retail's palette or button painter;
the game cursors keep their authored colours.
This is Nanolathe presentation policy, user-authorized 2026-09-29.

**Controls page.** Not a deck but a keyboard and mouse mapping view over
the key map of §3.6 "Rebinding". A profile bar offers Retail 3.1, Community
and TA Zero (the draft content's recommendation marked); choosing one shows
its keys at once and lists the other settings Apply will write. Tabs split
the actions into Orders, Selection, Camera, Game and Mouse. Each key is a cap:
clicking it waits for the next key press (Ctrl and Shift held are part of the
chord; Esc cancels, Backspace clears), `+` adds a second key, right-click
clears one, and a row that differs from the profile has a reset. A key taken
from another action is reported. A keyboard diagram colours every key by
the group that uses it, lights the selected action's keys with their
modifiers, and names a hovered key's actions plain, with Shift and with
Ctrl. The Mouse tab explains the two Interface Types on a drawn mouse (§3.5)
and holds the behaviour switches: mouse buttons, selection rules,
double-click, factory ×100, digit keys, order drag, build drag, the Tab key
and the snap-override key. It also owns camera zoom style (No zoom, Steps or
Continuous), Zoom lock and
strategic-icon style (DESIGN_GPU_RENDERER §§16.6 and 18.7). Zoom lock displays
the preferred factor, with one-percent minus/plus buttons, a draggable track
and Reset to 1.00×. The track handles broad changes while the buttons select
an exact percentage. The row has a 30-pixel minimum height so its value,
buttons and track remain separate at 640×480; the scrollable table keeps every
control reachable. Changes use the ordinary draft, Apply and Cancel transaction.
Controls presets include `presentation.zoomLockPercent`; unrelated graphics or
rules presets and selecting a controls profile preserve it. Tests cover draft
and saved-preset ownership, restart persistence, scoped presets and actual pointer
hits at 640×480, 1560×900 and 1920×1080.

**Live preview.** Behind everything a small real battle plays, staged off
the game goroutine the way the film route stages its shots
([FILM_CAPTURE](FILM_CAPTURE.md)) and stepped at 30 Hz with its own client
and renderer; it is never saved, networked or seen by the window's battle.
The background repaints at the draft's own Enhanced frame rate — the rate a
battle would present at — and the Frame rate card shows its selected value,
including Display; controls, animation and the cursor keep the host's display
cadence. A repaint lands on a whole number of display refreshes, its stride
(`nlCadence`): the preferred rate rounded to the median measured refresh (60 on
a 144 Hz display repaints every second refresh), slowed while the smoothed CPU
cost of a repaint exceeds 35% of a refresh, and slowed one refresh further for
each 90-frame verdict in which more than a tenth of the display frames arrived
over one and a half refreshes late — a GPU or host that cannot keep up — until
three calm verdicts in a row give it back. A repaint never falls below 30 a
second. Loading, arrival and the fade-in measure the refresh but do not vote.
Before this, ordinary backgrounds repainted at a fixed 30 FPS. Both compare
pictures refresh together; scene activation, a missing compare picture and an
edit to either picture's render parameters bypass the cadence. During an
outstanding scene load the previous picture holds and both its simulations and
renderers pause. The loading interval never accumulates for a catch-up on resume.
This is Nanolathe host presentation policy, not a gameplay or retail rule.
The scene depends on the focused card, on maps where units stand out (no
metal maps): an armour battle on Great Divide; Coast To Coast's shoreline with
submarines, underwater structures and sinking wrecks for the water parts; a
laser-tower defence with lightning kbots and working constructors on Crystal
Cracked for glow, and on Gasbag Forests for lighting, beside six small copses
set alight in turn; eleven copses on SHERWOOD, spaced so fire cannot jump
between them and each lit from its middle in turn, with no unit in frame, for
fire shimmer; fresh wrecks made every 40 ticks for the wreck parts; a Guardian
(accuracy 0) shelling the middle tank of a 5×3 Leveler block, first shell
0.67 s after the scene appears and then every three seconds, the block
healed to full between shots and a health bar over every tank, for blast
rings and blast size; constructors already in build range of their sites,
with the trees cleared round them, for the build mutators, and reclaiming
wrecks for salvage, with each scene's metal shown; nine hovercraft and tanks
circling on a Great Divide hillside that faces the key light of
DESIGN_GPU_RENDERER §23.7 at about 4.4×, for finish, glint and smooth edges
(on flat ground a deck faces straight up, which both finishes leave
unchanged); a snow march for trails; low aircraft for soft shadows; a fogged
march under Circular line of sight for Sight (the True raster stops at
sightdistance 256, which most units already reach); and the renderer compare
on Greenhaven at the detail view's 2×. Every camera is still, since a slow
pan moves pixel art in uneven one-pixel steps: a scene that frames a fight
follows it through the lead-in, which is silent and never shown, and holds
that frame from the first visible tick, and a twin compare copies its
primary's frame. The construction scene's hidden lead-in includes the
constructors' ordinary script readiness as well as their short approach to
each site's edge, so its first visible frame shows structures rising
([04 R-ORD-01 §5]). Both halves of a build-mutator comparison use the same
lead-in. Each scene has a fixed anchor, the
preview's computer player is passive (its units still shoot back), and a
scene of the viewer's units alone keeps one far enemy building, since a side
with nothing left has lost and the battle would stop. The main menu stages
the first card's scene in the background, so the screen opens onto it. The
preview's client draws no effect entry with a frame above 512 pixels
(`client.SetEffectArtLimit`): every stock entry is far smaller, but TA:
Escalation's explode2–4 are its 760–1140 pixel shield bubbles, which its
ordinary weapons also name, and a preview full of them covered the screen
and slowed it to a crawl
([escalation-shields](../research/extensions/escalation-shields.md)).
Battles draw every entry.
Rules and mutators are part of the scene's key, so a
change restages it with the draft applied; renderer and effect values are
applied per frame. *Compare* splits the background at a draggable line: the
executor renders the same recorded frame twice, once with the card's
alternative value; while a Classic picture is wanted the view is floored to
the whole world pixel and the camera blend snapped after each tick, so both
halves line up. A mutator changes the simulation, so its compare stages
a twin scene under ×1, stepped on the same clock and framed on the same
ground, and shows the two side by side, each cropped around the point the
camera aimed at, projected through the camera, since a camera clamped at
the map's edge does not hold the fight right of centre; a structure still
rising carries its build percentage from each simulation. Classic previews hold the camera on
the native or 2× step, the only factors the Classic executor draws. The
Metal and Smooth edges cards' compare blinks instead: the whole frame
alternates between the two values every 0.8 seconds under one tag naming the
value on screen, because those changes are too fine to find by looking from
one half to the other.

**Content-aware fixtures.** The named units above are the stock compositions.
Settings fixtures resolve those preferences against the mounted catalog's
SIDEDATA factions and authored capabilities. Missing names get deterministic
substitutes suitable for the scene; the film and benchmark fixtures retain their
pinned rosters. Selection reads the immutable authored catalog before rules and
mutators, so both halves of a comparison choose the same units. Construction
examples use a selected constructor's full authored build membership while
ordinary bound rules still admit each queued product. Commander build trees and
visible download placements identify the human roster when a mod's constructor
side tags also label another authored roster. Visible button zero participates
in that preference. Artillery,
worksite spacing and other unit-dependent geometry use the selected definitions.
Card portraits and mutator examples follow the same content selection. When a
capability is absent, the preview uses a compatible demonstration and explains
the limitation. This is settings presentation policy, user-authorized 2026-09-30.

**Game, Graphics and Effects pages.** Game holds content, rules, unit limit,
radar dots and the Sidebar card's Build items and Orders below build preferences.
Graphics holds the renderer, frame rate, game resolution and fullscreen.
Renderer, resolution and fullscreen work with both renderers; frame rate and
the adaptive sidebar require Enhanced. The Sidebar card's placement does not
change its saved preferences or the Graphics & effects preset scope.
Resolution offers monitor-derived presets
and editable dimensions through the host transaction in DESIGN_PRESENTATION_CLIENT
§2.1. Effects holds Enhanced's own looks, the player
switches of DESIGN_GPU_RENDERER §30, plus Commander arrival (§36) and Placement
weapon rings (§20), both default on and independent of gameplay mode. Their
previews use the existing opening recorder and prospective-building ghost/range
pass on a quiet Greenhaven skirmish; their compare renders each treatment on and
off without touching authoritative state. The placement example moves the
prospective tower through validated footprint-grid sites on an eight-second loop;
the ghost and its range overlay read the same site while the camera stays still.
Hovercraft land wash controls the dry-ground dust independently of Water's shore
and building foam. It defaults on, has an Enhanced-only On/Off compare using the
circling hovercraft scene, and belongs to graphics presets. It changes only
presentation; the existing wash history and shared terrain mask remain owned by
DESIGN_GPU_RENDERER §§26.3 and 30.
The grouped cards share one scene: Water
(surface, motion, foam, reflections), Lighting (unit light, ground light and
its strength), Metal (finish, glint), Smooth edges (the model supersampling
of §17.5), Glow (overall, weapons/projectiles, explosion/fire and nanolathe
amounts, plus the existing ground-light amount), Heat (blast rings and their strength, fire
shimmer, wreck glow, wreck heat wave), Marks (scorch, trails) and Soft
shadows (switch and blur-width percentage). The independent glow amounts and
shadow softness are user-authorized presentation preferences (2026-09-29), with
their renderer boundaries and defaults in DESIGN_GPU_RENDERER §§19.4, 30 and 34.
A grouped card has one row per switch or amount; the focused row picks the
scene and the compare. Every switch is independent: there is no family
master among the effect switches. The existing overall glow amount still scales
the bloom layer, and the separate source amounts multiply their own families.

**Demonstrations and numbers.** The Sidebar page groups Build items (6 per page,
12 per page, Free flow) and Orders below build as independent named
choices on one card. Sidebar and Fullscreen draw a small demonstration over
the preview (`nlscreen_demo.go`). The sidebar shows the draft's actual
resolved capacity and orders visibility at the selected logical game resolution.
Twelve combines authored pages; Free flow reserves at least six slots before
orders. Fullscreen scaling and world zoom do not alter that logical space.
The common command rows always appear; the optional panel contains the
supplementary Orders-page controls above them. Fixed counts put the native
controls directly after their reserved build rows.
The sidebar preview resolves the running content's GUI/download cells on the
picture worker, including duplicates and composite children, then shares the
HUD's `sidebarLayout` and adaptive page partitions. It never sorts CANBUILD
membership into fictional pages. The worker also resolves the mounted content's
native rail and command-button artwork through the battle's art/frame lookup,
decoding its unpressed build-page appearance with the content's physical palette.
Draw uploads those pixels without substitute button boxes or a separate list of
control names. Changing only the orders choice preserves an inherited or unusual
stored build count. Missing or unsafe pages show no demonstration.
Oversized authored/fitted pages show a layout description rather than a
normalized grid with an inaccurate page count. The worker honours retirement
between nested GUI and art reads. `TestNLSidebarPreviewPagePolicies` locks the
partitions and fitted boundary; `TestNLSidebarLoaderStopsBetweenAssetReads`
locks cancellation, and the retail builder sweep compares preview cells with
the live HUD.
The demonstration code also places marks on the
preview's real units — each unit's world position projected through the
preview camera, whose origin is the surface corner, less the viewport
origin, then through the background's crop. A mutator card lists three of the running content's
units with the scaled value before and after, read from a clone of the
catalog after `Catalog.ApplyMutators` with that one factor, so engine limits
show (a Krogoth's hit points stop at 32767). Choosing a controls profile
lists every row it would change, old then new.

**Unit restrictions card.** The Mutators page opens with the *Unit
restrictions* card, whose contract is
[DESIGN_MODS_MUTATORS §15.9](DESIGN_MODS_MUTATORS.md#159-editing-and-storage).
Its control summarises the draft against the running content's catalog: the
numbers removed and capped, the first four entries in key order with their
pictures and states, then *and N more*; *Edit...* and *Clear* sit beneath it,
and amber chips name the draft's entries the content leaves out, and saved
entries that do not read, over at most two lines. It has no Compare, and the
arrows and the wheel step nothing. The draft holds the set as its canonical
string, so the draft stays comparable. *Edit...* pauses the screen's picture
worker and opens the unit viewer's restriction editor over the screen
(DESIGN_DEVELOPER_TOOLS §7); the viewer's Back hands the edited set back as a
touched card, the screen's Apply writes it to the running content's layer
through the shell's restriction selection, and the screen's Back discards it.
While `--restrict` chose the run's set the card starts from that set and its
note says an applied edit replaces it. No preset part holds `restrictions`:
saving a preset leaves it out and applying one, an older preset that names it
included, never changes it. `TestNLRestrictionCardSummary`,
`TestUnitViewerRestrictCardRouteEditsScreenDraft` and
`TestNLPresetsExcludeRestrictions` lock these; `--nl-shot-only restrictions`
with `--restrict` entries captures the card.

**Preview and menu audio.** Retail menu sound must match retail exactly:
authored per-screen cue selection and aliases [07 R-FE-01 §2], ordinary cue
attenuation and effects-volume application [03 R-AUD-01 §1][03 R-AUD-01 §2],
and audio admission and the exclusive ambient-loop lifecycle [03 R-AUD-01 §5].
Menu playback uses the standard audio-service cue APIs without additional menu
gain. Retail's silent buttons remain silent. The additional 10% menu gain
introduced on 2026-09-29 was removed by user decision on 2026-10-08.

Every Nanolathe settings preview is silent, including its visible battle,
lead-in, restart and retirement. It has no playback binding and never changes
the shared backend's configuration or battle preferences. This separate preview
policy remains user-authorized (2026-09-29). Battle audio retains the player's
FX/music settings.

**Scene reuse.** The screen keeps recently viewed scenes paused, with their client
and last pictures, so a revisit avoids battle/client composition. The cache is
scoped to one content set and keyed by preset, rules, mutators, paired comparison
and surface size. Current and cached scenes retain at most three battles in total;
a paired comparison counts as two. The least recently used scene is retired first.
Cached scenes do not step; a revisit resumes the same scene clock. Exhausted loops
stage afresh, and closing the screen or reloading content retires every cached
client. First visits and uncached rule/mutator combinations still pay staging
cost; this is a bounded recent-scene cache, not a preload of all maps.

Scenes do not own renderers. The screen keeps one Enhanced renderer per surface
size (at most two: the common size and the closer-look scene's) and one for a
paired comparison's twin, for as long as it is open; GPU programs are shared
process-wide (DESIGN_GPU_RENDERER §2.3 "Shared programs"). When a different
scene — new, cached or restarted — takes a renderer, its predecessor's sources
are reset and the scene's own are prepared before its picture: the projected
water/shadow mask its staging worker built while exclusively owning the terrain
(DESIGN_GPU_RENDERER §26.1), kept with the scene for revisits, and the feature
rest art its terrain admits. The mask is a pure function of the map's static
terrain, so the screen keeps the last four built, keyed by a fingerprint of
exactly their inputs (`gpurender.WaterMaskInputs`: cell grid, sea level, lava
flag, each plot's height and void state); a restart, twin or revisit on the
same map binds the one already built instead of spending 220–440 ms of staging
on it. The projectile bank is loaded with the other battle art before any draw
(`Client.WarmProjectileArt`) and, for previews, decoded once per content set.
The renderer keeps its surfaces, layers, raster
pages and recycled source pages across the reset (DESIGN_GPU_RENDERER §2.3
"Source lifetime"), and fills its terrain atlas on demand (DESIGN_GPU_RENDERER
§14.8), so a scene change uploads the few
hundred tiles in view and allocates almost no device memory. Every renderer the
screen makes draws through one `gpurender.SharedPages` set — the model lane's
4096² planes and the recycled source pages — since they execute one after
another on the UI goroutine: a new card size or the first compare no longer
allocates its own planes, and an evicted renderer's pages return to the set.
Measured on an M3 Pro, a scene's first picture fell from 100–485 ms of frozen
frames (scene draw plus the backend's wait) to about 25–100 ms with the kept
renderers, and then to 15–37 ms on a renderer the screen already holds and
30–58 ms for one it makes (the first compare, the smaller card) once model
textures were written into their page, every recycled page actually returned to
the pool and the planes shared; shared programs matter most where the backend
compiles slowly (Direct3D). Nanolathe host presentation policy.

**Content reuse and asset scope.** A content set compiles the settings screen's
authored catalog once. Each scene composes from its own clone, including an
unmutated comparison scene; rule preparation and mutators never write to the
cached base. The same content set retains immutable decoded texture banks, model
geometry and reachable feature banks across scene loads. Each battle gets fresh
loaded-model identities and animation cursors. A content reload creates a new
cache.

Before loading art, each fixture declares every unit it can create: initial and
scheduled units, queued products, commanders, offscreen support and placement
ghosts. Only those models and their linked projectile/corpse/successor assets are
prepared, along with the terrain's admitted features and independent scene
weapons such as meteors. Ordinary battle entry keeps full preparation. Texture
banks still use the complete namespace to preserve entry precedence; authoritative
terrain, HUD art and detail synthesis retain their existing loading paths. This
scope reduces repeated catalog and art work without cropping the simulation map
or moving file reads into a simulation tick. The authored animation table (`content.SimArt`), which depends only on the
files and the feature definitions, is compiled once per content set and handed
to every scene's battle entry (`session.SkirmishEntryOptions.SimArt`) rather
than compiled from every feature bank per scene, about half of a scene's
staging before. The generated strategic icon atlas is a function of the icons'
art keys alone and is drawn once per key set (`internal/client`), although each
scene's catalog is a fresh clone.

**Input and persistence.** A grouped-effects wheel changes only the selected row
and stops at its endpoints. Controls wheel scrolling survives redraw; keyboard
selection scrolls its row into view. Preset lists scroll by wheel and keyboard.
Unavailable settings are dimmed, explain their requirement, and reject pointer,
drag, wheel and keyboard edits. Their saved values survive mode changes. Both
settings surfaces follow the existing runtime consumers: Enhanced effects,
sidebar and build drag require Enhanced; radar dots require the bound rule set's
main-view radar policy as well. Modern zoom preferences follow the rule set's
base mode. Classic retains its native zoom or No zoom, with the free Steps choice
disabled; Zoom lock and strategic icons require enabled Enhanced Modern zoom.
The Tab preference is disabled where the selected mode fixes its behavior.
Optional selection controls, queued-order dragging and mutators remain usable in
Strict. Legacy builder and rotation rows follow the resolved feature table;
snap controls follow their admitted radii and gesture consumers. Health-bar
counters and reload bars require health bars. The battle options refresh these
gates after committed rule changes.

Effect amounts require an active consumer: ground light, blast rings or soft
shadows for their respective amounts, and Overall glow for weapon/explosion
bloom. Nano amount remains usable through local lighting when bloom is off.
Soft shadows also require the existing master and vehicle shadow switches.
The independent family switches retain their existing behavior. Comparisons of
unavailable treatments are disabled, and the background keeps the chosen
renderer. Trail comparison hides the Off picture's recorded marks without
erasing the shared history. Every scene carries the saved ground-light and
ring strengths. Selectors may highlight the nearest preset notch, but exact
custom values are displayed and preserved through sibling edits, previews and
Apply, including custom frame caps, unit limits and registered rule-set names.

Every key mutation, including clear and reset, waits for a locked mod's approval.
The pending captured chord retains its action until that approval completes.
The selected content's locks apply before its reload too; Apply validates the
final composed settings so earlier edits cannot bypass a subsequently selected
mod's locks. A command-line asset stack disables content selection, and an
explicit command-line unit limit disables that preference for the run.
Apply records approval before reloading layers; startup reads it before layering
the player patch. Saving base content or applying a preset preserves the complete
mod-settings table and preset library. Graphics preset scopes include Smooth
edges, Commander arrival and Placement weapon rings.
Saving a preset composes the pending profile, presets and explicit card/key
edits in Apply order, including settings without cards, without changing live
settings. Complete presets explicitly encode Auto unit limit and default keys
so applying them can clear earlier choices. A profile immediately displays the
mouse values Apply will use, allowing an explicit subsequent choice to win.
Ordinary saves retain the selected content-profile path. Detached direct battles
use their installed host presentation settings for sidebar options too.

**Apply.** The screen edits a draft. Apply writes a chosen controls profile
first, then only the cards the player touched, over the live state, so a
profile keeps every row nobody changed afterwards. A different content is a
reload request carrying the mod, rules, mutators and the mod's recommended
controls (unless a profile was chosen here), exactly as the Mods & Mutators
screen makes it, except that the screen stays open: a loading plate covers
it while the content switches, and the draft is then applied to the new
shell and saved, so the changes made in the same Apply belong to the new
content. Otherwise the settings are saved at once. The rule-lock override is
[DESIGN_MODS_MUTATORS §4.3](DESIGN_MODS_MUTATORS.md#43-selection-and-precedence)
"Overriding a rule lock".

**Verification.** `--nl-shot <dir>` renders every card, each part of a
grouped card that has its own scene, its compare and the override dialog to
PNGs with no visible window, each at a fixed second of its scene's clock
(restaging a scene already past it), and logs each compare's share of
changed pixels. `TestNLScreenApplyKeepsTouchedCardsOverProfile`
(retail tier) and `TestModLockOverrideSurvivesStartupRaise` lock the Apply
order and the override.
`--nl-shot-only cache` captures a first visit, another scene and the revisit,
logging the cache activation and first cached-frame time. Placement captures
include a later sample to inspect the moving ghost and ring. Cache contracts
verify composition-key isolation, paused clocks, eviction of paired/exhausted
scenes and complete retirement; the placement contract validates each moved site
and the fixed camera.
`--nl-shot-only availability` captures disabled settings under Classic, Strict,
the Community megamap and No zoom, alongside enabled Modern and Community
camera controls. The configuration regressions cover inactive input, dependent
amounts, raw-value preservation,
pending preset export, target-content locks and detached sidebar preferences.
`TestCommunityContentCameraPreferences` loads the shipped Escalation and ProTA
configs to lock the separate gameplay-floor and overview recommendations.
`TestCommunityConfigurationCanLeaveMegamapForCameraZoom` and
`TestNLScreenCommunityCanRestoreCameraZoom` lock both configuration surfaces and
the saved host choice. `TestEscalationSavedMegamapCanEnableZoomDirectly` covers
inherited base and per-mod overview values, reselecting Continuous, selecting
Steps, per-mod save/reload and actual camera magnification;
`TestCommunityBattleOptionsCanEnableZoomDirectly` covers the in-battle selector.
`TestCommunityZoomApplyAfterModeSwitch` and `TestCommunityZoomUndoAfterModeSwitch`
lock the linked edit across later rules changes; `TestModernZoomCardRestoreKeepsIndependentTabChoice`
preserves Modern's separate Tab preference.
Camera mode/lock tests preserve Strict and megamap controls.

**Open.** Mouse buttons are not rebindable: the Mouse tab offers the
retail Interface Types and the existing switches. A mod's own key profile is
its controls preset's keyboard row; no content yet authors key bindings of
its own, so none is read. The blast rings change little of a still: a
ring lasts a fraction of a second.

## 4. Retail behaviour that is not a bug

* **The footer shows the *hovered* unit, never the selected one.** It persists
  while the pointer is over the rails. Selection and hover are separate state,
  and the pointer update publishes the hover result once per host frame for the
  footer, the cursor and click targeting alike `[07 R-HUD-03 §1]`
  `[07 R-SEL-02B2]`.
* **The right mouse button issues no order, ever.** Every world action —
  picking, drag selection, placement and every order including the contextual
  code 1 — is the left button. Right is cancellation only `[07 §9]` `[04 §3.4]`.
* **A greyed button is still hit-tested.** It takes no capture and fires
  nothing, and the pass continues past it; only a *hidden* gadget is skipped
  before the hit test, so a hidden one does not shield what is behind it
  `[07 R-WGT-01 §1]` `[07 R-WGT-01 §3]` `[07 R-WGT-01 §13]`.
* **Additive group recall is Shift+Alt+digit under the default option.** The
  digit case is reached by an unshifted digit character or by Alt+digit; Shift
  without Alt yields a shifted character instead, and only `!` `#` `*` have a
  case. With `SwitchAlt` set, a plain digit recalls and additive recall becomes
  unreachable from the keyboard. That is retail's behaviour, not a gap
  `[07 R-CAM-01 §4]` `[07 R-CAM-01 §14]`.
* **`Game Speed -9` is slow motion, not a pause.** The `-` key stops at speed
  1, which the budget runs at a tenth of nominal: three ticks a second
  `[01 §4.3]` `[07 R-CAM-01 §3]`. Original samples those ticks as retail did,
  so units step three times a second; Enhanced interpolation between committed
  ticks (DESIGN_GPU_RENDERER §5.3) draws the same slow motion smoothly. Neither
  changes the tick rate.
* **`N` does nothing.** `n` and `N` are separate character tokens and the
  dispatcher has a case only for `n`. The stockpile round is enqueued by the
  palette's `MAKENUKE`/`MAKEANTI` gadgets alone `[07 R-CAM-01 §14]`.
* **Nothing arms the TELEPORT latch.** `0xB` is a live switch key with no writer
  anywhere in the image; leaving it consumer-only is the traced contract, not an
  unfinished wire `[07 §9]`.
* **A button named `PICKUP` parses to nothing.** The chain has no `PICKUP`
  compare — only `LOAD` — and a name matching none of the eleven tests writes no
  latch and plays no cue `[07 §9]`.
* **`TOTALUNITS` and `TOTALTIME` are authored anchors nothing reads.** The
  running display belongs to the Space-held slide strip. Painting the clock at
  its anchor overlaps the energy readout on stock ARM data
  `[07 R-HUD-03 §5]` `[07 R-HUD-04 §4]`.
* **The slide offset moves one thing.** The bottom strip, and nothing else. No
  rail window, gadget rectangle or hit test follows it `[07 R-HUD-03 §1]`
  `[07 R-HUD-05]`.
* **`Normal (+2)` is a reachable speed line.** The suffix test is a plain
  inequality of the target and adapted speed words and runs after the `Normal`
  branch has joined `[07 §6]` `[07 R-CAM-01 §3]`.
* **The chrome does not scale at a larger display mode; it extends.** The strips
  stamp rightward, the bottom strip sits at `H − 32`, the rail keeps its
  authored 129×480 art with palette index 0 below it, and the authored rail
  pages keep their coordinates `[07 R-HUD-05]` `[03 §4.1]`.
* **A pointer past the map edge resolves to the edge.** The cursor-to-ground
  resolver clamps into the map rectangle first, so an off-map build ghost is
  legal rather than out of bounds (SC20) `[07 §8]`.
* **The front end always runs at 640×480.** Whatever display size is stored,
  every authored menu screen forces the surface back before it draws
  `[07 R-FE-02 §2]`.
* **`PREV` and `NEXT` refuse only a builder with no build page at all.** They
  are deactivated when the page-count byte is below 2, and page 0 counts, so any
  builder with one authored page has a count of at least 2
  `[07 R-HUD-03 §6]`.
* **A queued order's icon comes from the descriptor's icon byte, and zero means
  none.** `MOBILEBUILD` and `VTOL_MOBILEBUILD` are the only records carrying
  zero, which is why a queued build site shows its marker and dash chain but no
  order icon `[07 R-P0-11 §3]`.

## 5. Divergences

* **Main-menu shimmer cadence and gadget pixels.** Retail runs the shimmer
  once per front-end frame; the rate of that frame is not traced, and a
  60 Hz retail capture redraws it at 60 Hz, so Nanolathe steps it 60 times a
  second of presentation time (`TODO(question)` at `menuSparkRate`). Retail's
  spawn and move tests read the `MAINMENU` window surface, which also holds
  the gadget art; Nanolathe composes gadgets through the draw list each frame,
  so its tests read the window background plus live sparks. The two differ
  only where a spark reaches a button, and every retail `MAINMENU` button lies
  below the 220-row spawn band `[07 §5]`. The `MODS` button does lie in that
  band, so it is drawn above the sparks rather than beneath them.

* **Alt batches factory products by twenty.** This user-requested build-menu
  extension adds twenty on Alt-left-click and subtracts twenty on
  Alt-right-click. Alt takes precedence over Shift; Shift alone retains five
  and no modifier retains one. The optional Ctrl+Shift hundred-unit preference
  (§3.13) is also subordinate to Alt. The shared product callback uses the held
  modifiers at activation, including a product quickkey. Counts use the existing
  signed queue command, so addition coalesces and subtraction consumes matching
  queued products normally. Mobile building placement keeps its existing
  action, and the `MAKENUKE`/`MAKEANTI` stockpile toys are scoped out of the
  batch: they are counted producers too, but they keep retail's ±1/±5 under
  every modifier `[07 R-P0-11 §1]`. This is host input policy, not a retail
  behavior claim.

* **Tab resumes an already paused battle.** As user-requested host policy,
  Tab with no modal open resumes the battle directly; F2 still opens options.
  Child dialogs retain input ownership, so this shortcut cannot bypass a
  save/load or confirmation dialog. This is not a retail behavior claim.

* **Modern resource construction shortcut** is the user-requested input policy
  in §3.10. It produces ordinary typed commands, with no alternate simulation
  or placement rules.

* **The double-click interval and rectangle are host policy.** Retail never
  recognized a double-click: the operating system did, and the window procedure
  received the resulting message, so the interval and the rectangle were the
  user's own OS settings `[07 R-WGT-01 §4]` `[01 R-PLAT-01 §6]`. Ebiten polls
  devices and delivers no such message, so
  `internal/platform/ebitenapp/doubleclick.go` reconstructs the pair from the
  timestamps and positions the pointer records already publish, and holds both
  constants in that one place. The recorded values are the Windows defaults
  the OS shipped with: **500 ms**, expressed as **15** units of the scaled
  30-Hz host clock the pointer timestamp already carries (`500 × 30 / 1000`),
  and a **4×4 pixel** rectangle, applied as **±2 surface pixels** about the
  first press. Both bounds are inclusive. The clock unit quantizes the
  interval to roughly a thirtieth of a second; the edge deliberately does not
  invent a finer host time than it publishes. A second press of the same button
  inside both bounds is published as its double-click event in place of the
  plain down event; a press
  outside either bound becomes the new candidate, and a completed pair is
  consumed, so a third press starts a fresh pair instead of producing a
  triple. Left and right retain independent candidates under this host policy.
  The list open action still admits only the left button; the optional Community
  same-type selector admits both buttons, matching the licensed patch source.
  This is host input policy, not a retail behavior claim: no
  number here was measured from the executable.

* **The key-repeat delay and rate are host policy.** Retail had no repeat
  policy: the operating system repeated a held key's messages at the user's
  keyboard settings, and each repeat became one token `[07 §2]`
  `[07 R-CAM-01 §1]`. `internal/platform/ebitenapp/keyrepeat.go` reconstructs
  that repeat for the editing and cursor keys (§2.2) and holds both constants.
  The recorded values are the Windows defaults: keyboard delay setting 1, about
  **500 ms**, expressed as **15** units of the scaled 30-Hz host clock; and
  repeat speed setting 31, about **30 per second**, expressed as **1** unit, so
  at most one repeat per host service — the bound retail's one-token-per-frame
  drain already placed on the OS rate. A release re-arms the delay, and
  services that share a timestamp (a catch-up drain) repeat at most once. This
  is host input policy, not a retail behavior claim: no number here was
  measured from the executable.

* **SC15 — the cursor index table.** The previously published twenty-entry table
  was off by one from slot 10 up. The reference install's `anims/cursors.gaf`
  holds 22 named entries; the handle array has 22 slots and the init sequence
  fills slot 10 with `cursorrevive` last, after slot 21, so transcribing the
  sequence rather than the slot offsets dropped it. The corrected 0..21 table is
  what `internal/render` and the chooser carry, and `cursorprotect` is
  deliberately absent because retail never resolves it `[07 §8]`.
* **SC20 — cursor to ground.** The algebraic inverse of the projection at height
  zero is wrong wherever the ground is above zero: terrain is presented flat
  while world objects carry the half-height shear, so an order given at a pixel
  put the unit half the terrain height north of it. Retail resolves the pointer
  with a bounded search along Z — clamp into the map rectangle, start eight
  cells south of the clicked row, walk north up to nine cells comparing each
  candidate's `max(height, seaLevel)` projection against the clicked row, then
  bracket and interpolate. `Terrain.CursorToWorld` is that resolver and the
  single conversion the battle screen uses for ground orders `[07 §8]` `[03
  §2.5]`.
* **Preferences are a JSON file, not the registry (C17).** Two consequences
  follow from JSON's inability to distinguish an absent integer from a stored
  zero, where the registry query returns a status that does: the writer always
  emits all ten skirmish rows and all six rule scalars, and the loader defaults
  only rows missing from the array outright. A stored zero — ally group 0,
  colour 0 on a non-zero slot, Easy, LOS off — is therefore honoured, which is
  what retail does and what a per-field zero test would silently undo. A block
  whose every row is Open is repaired on load into the same state as retail's
  empty-controller initialisation: retail can reach the all-open state and
  simply refuses Start, and loading straight back into it would leave the screen
  with no way forward that is not a manual row cycle `[02 R-KEYS-01 §3]`.
* **One window backend.** Rendering goes through Ebitengine rather than the two
  retail display paths. The distinction between the fixed logical size and the
  negotiated outside size is preserved, because C13 and the chrome extension
  rules depend on it `[07 R-FE-01 §11]` `[07 R-HUD-05]`.
* **`[F-P1-008]` presentation zoom** is an addition with no retail counterpart,
  bounded by §3.8: presentation-only, exact at native scale, and absent from
  every authoritative path.
* **The panel-slide detent names.** The section's parenthetical labels call
  `−31` parked and `0` fully visible; the later closures establish that the
  strip is fully drawn at `−31` and invisible at `0`. The arithmetic is
  identical under either label, so the constant names are left as the section
  wrote them `[07 §6]` `[07 R-HUD-04 §4]`.

## 6. Research map

| Behaviour | Owning research |
|---|---|
| The interface object model, the authored 640×480 design space, redraw ownership | `[07 §1]`, `[07 §4]` |
| Win32 input translation, the rings, the OEM aliases, GUI quick keys | `[07 §2]`, `[01 R-PLAT-01 §6]` |
| The host frame: where input becomes simulation state | `[07 R-CAM-01 §1]`, `[07 R-CRD-006 §1]` |
| The battle hotkey census and the key-token producer | `[07 R-CAM-01 §2]`, `[07 R-CAM-01 §14]` |
| The game-speed hotkey and its ring announcement | `[07 R-CAM-01 §3]` |
| `SwitchAlt`, and mouse-button polarity | `[07 R-CAM-01 §4]`, `[07 R-CAM-01 §5]` |
| Interface options (`SPEEDS.GUI`) and their consumers | `[07 R-CAM-01 §7]` |
| Modal windows, focus, event ownership, and who closes a window | `[07 §3]`, `[07 R-WGT-01 §1]`, `[07 R-WGT-01 §2]` |
| Buttons, listboxes, sliders, text input, labels, surfaces | `[07 R-WGT-01 §3]`…`[07 R-WGT-01 §8]` |
| The shared selection-eligibility predicate and the footprint quad | `[07 R-WGT-01 §9]`, `[07 R-WGT-01 §10]`, `[07 R-SEL-02A]` |
| The kind byte, the per-kind key table, the builder's switch, the grey flag | `[07 R-WGT-01 §11]`, `[07 R-WGT-01 §12]`, `[07 R-WGT-01 §13]` |
| The bitmap cache, window-record words, gadget appenders, the keyboard-ring flush | `[07 R-WGT-02 §1]`, `[07 R-WGT-02 §2]`, `[07 R-WGT-02 §3]`, `[07 R-WGT-02 §4]`, `[07 R-WGT-02 §5]` |
| Art-less bevels, the `BackTile` chain and the window fill | `[07 R-FE-02 §4]`, `[07 R-FE-02 §5]` |
| The front-end controller, the transition table, startup and `MAINMENU` | `[07 R-FE-01 §1]`, `[07 R-FE-01 §2]`, `[07 R-FE-01 §3]` |
| `SINGLE`, `NEWGAME`, the briefing, and the blink words | `[07 R-FE-01 §4]`, `[07 R-FE-02 §7]`, `[07 R-FE-02 §6]` |
| `SKIRMISH` preflight, `SELMAP`, and the skirmish row synthesis | `[07 R-FE-01 §5]`, `[07 R-FE-02 §8]`, `[08 R-SKIR-01 §11]` |
| The options family and the slider arithmetic; the display-mode source list | `[07 R-FE-01 §6]`, `[07 R-FE-02 §9]` |
| The in-battle menus, `EXITMENU`, `YESORNO` and `RESTART.GUI` | `[07 R-FE-01 §7]` |
| Save and load, and the dialog primitives | `[07 R-FE-01 §8]`, `[07 R-FE-01 §9]`, `[08 R-SAVE-02 §4]` |
| The post-battle machine and `ENDMSN` | `[07 R-FE-01 §10]` |
| The registry write census, the 640×480 enforcement, and the per-frame residue | `[07 R-FE-01 §11]`, `[07 R-FE-02 §2]`, `[02 R-KEYS-01 §3]` |
| Never-opened GUIs and misfiled names; the surrender teardown | `[07 R-FE-01 §12]`, `[07 R-FE-02 §3]` |
| The single-player menu slice, its raster rules, its palette contract | `[07 "Retail closure for the single-player menu slice"]`, `[07 "Retail frontend control activation and raster rules"]`, `[07 "Retail palette contract"]` |
| The loading screen | `[07 "The loading screen"]` |
| The battle HUD, side data, panel slide, frame composition passes | `[07 §6]` |
| The footer's state, priority and formatting boundary | `[07 R-HUD-02R]` |
| The ordinary footer, the unit readout, feature and build-card readouts | `[07 R-HUD-03 §1]`, `[07 R-HUD-03 §2]`, `[07 R-HUD-03 §3]` |
| The top strip, the bar arithmetic, and the side-anchor consumer census | `[07 R-HUD-03 §4]`, `[07 R-HUD-03 §5]`, `[02 §6]` |
| Build pages, the `DL` template, `NEXT`/`PREV`, command-button state | `[07 R-HUD-03 §6]` |
| The `damagebars` option; `UNITINFOx.GUI`; `SHARE.GUI`; `MOREBAR` | `[07 R-HUD-03 §7]`, `[07 R-HUD-03 §8]`, `[07 R-HUD-03 §9]`, `[07 R-HUD-03 §10]` |
| The score-bar gadget, selection-count displays, the command-state fold | `[07 R-HUD-03 §11]`, `[07 R-HUD-03 §12]`, `[07 R-HUD-03 §13]` |
| Unit captions: the raiser, the presenter, the message-line ring, where lines are drawn | `[07 R-HUD-03 §14]`, `[07 R-HUD-03 §14.1]`, `[07 R-HUD-03 §14.2]`, `[07 R-HUD-03 §14.3]`, `[07 R-HUD-03 §14.4]` |
| The Space-held score panel, the options unfold, the latch-to-idle group reset | `[07 R-HUD-04 §1]`, `[07 R-HUD-04 §2]`, `[07 R-HUD-04 §3]` |
| The slide strip's art and text offsets, the greyed-button darken row, the first-page seed, the F4 flash gate | `[07 R-HUD-04 §4]` |
| The `ORDERS` and `BUILD` stage buttons' cues | `[07 R-HUD-04 §5]` |
| The battle chrome at display modes larger than 640×480 | `[07 R-HUD-05]` |
| Fonts, text and the GAF pen's modes | `[07 §7]`, `[03 R-FONT-01 §6]` |
| The software cursor, the index table, the shape chooser, the cursor-to-ground resolver | `[07 §8]`, `[03 R-FX-01 §5]` |
| Selection overlay and picking evidence; the hover hull and its publication boundary | `[07 R-SEL-02A]`, `[07 R-SEL-02B2]` |
| Hover hull extrema, corner mapping, projection sign, the polygon predicate, the hover score terms | `[07 R-REV-01]` |
| Selection, control groups, orders, build pages, and the latch-writer census | `[07 §9]` |
| UI order producers: the factory product click, the queue-count display, the overlay helpers, latch persistence, the thread signal mask, the queued-world-order match | `[07 R-P0-11 §1]`, `[07 R-P0-11 §2]`, `[07 R-P0-11 §3]`, `[07 R-P0-11 §4]`, `[07 R-P0-11 §5]`, `[07 R-P0-11 §6]` |
| The scroll pass, its units, and what it cancels; the scroll pass while paused | `[07 §10]`, `[07 R-CAM-01 §10]` |
| Minimap click, latch and drag-scroll arithmetic; the viewport rectangle and its colour | `[07 R-CAM-01 §11]`, `[03 R-MM-01 §1]`, `[03 R-MM-01 §2]` |
| The camera-jump family, what breaks a follow, and the clamp's frame of reference | `[07 R-CAM-01 §12]`, `[07 R-CAM-01 §13]` |
| The camera cadence seam: input scrolling and the phase-10 follower are two writers | `[07 R-CRD-006 §1]` |
| Running display, pause, options and outcomes | `[07 §11]`, `[01 §4.3]` |
| The projection, the battle viewport subrect, the minimap letterbox | `[03 §2.5]`, `[03 §4.1]`, `[03 §3.6]`, `[03 §3.9]`, `[03 §3.11]` |
| Draw order and the screen-Y buckets the gadget layer sits above | `[03 §1]` |
| The command drain's tick phase, and that no input-delay constant exists | `[01 §4.4]`, `[08 "Soft pacing — no per-tick input barrier"]` |
| The order codes the latch selects, and the self-destruct descriptor | `[04 §3.4]`, `[04 R-ORD-01 §2]` |
| The stance cycle the panel gadgets transmit | `[04 R-STANCE-01 §2]` |
| The `.GUI` grammar, the `COMMON` keys, defaults and stored widths | `[02 §6]`, `[fmt gui]` |
| Front-end cue classes and the interface alias table | `[07 R-FE-01 §2]`, `[03 R-AUD-01 §2]` |
| Glyph metrics, palette tables, and the art formats the screens resolve | `[fmt fnt]`, `[fmt pal]`, `[fmt gaf]`, `[fmt pcx]` |

## 7. Not implemented and open

Native event history, text code pages, and clipboard bridges for Android and
VM guests remain marked platform gaps in the input and editor paths (§2.2 and
§3.9). The other open questions these contracts carry follow, with the
observation that would settle each one.

* Whether the interface's non-world-click queued issues share that producer. The
  section scopes the test to "every world order the interface issues", and the
  two world-click boundaries are its only callers; the side panel's own buttons
  — Stop, the activation toggle, Ctrl+D, the two stance gadgets — issue no world
  point, and nothing says whether a Shift-held press of one runs the test, which
  would make a second Shift-press cancel the first. A trace of those button
  handlers settles it `[07 R-P0-11 §6]` (same site). The stockpile toys are no
  longer among them: §1 establishes that they reach the counted producer, where
  Shift scales the count and the shift-chain flag does not participate
  `[07 R-P0-11 §1]`.
* The user-facing name of the interface-flags bit F4 toggles. No string in the
  image names it. Both of its readers are closed and nothing reads a name, so
  this is a naming curiosity rather than a behavioural gap `[07 §2]`
  `[07 R-CAM-01 §14]`.

* The clamp's behaviour when the viewport is larger than the map — the negative
  maximum domain. The ordered form is reproduced exactly and the section still
  carries the domain as open `[07 §10]` `[07 R-CAM-01 §13]`.
* There is no general display-scale conversion contract, because retail has one
  logical size. The HUD is laid out at that size and the chrome extends by rule;
  a scale conversion would be invented `[07 §4]` `[07 R-HUD-05]`.

Two open questions belong to the in-battle options window and are carried as
`TODO(question)` markers in `retail_menu_options.go`:

* The rectangle the in-battle opener writes into the **synthesised `PANEL`**.
  `[07 R-FE-01 §6]` establishes that the window is widened by 150 and that the
  gadget is synthesised, but not where it is put. This build gives it the new
  columns beside the root's own plate — x at the authored width, and the plate's
  own y and height — because the traced centring divide then places every stock
  `…RT.GUI` page at exactly its own authored header origin on **both** axes,
  `(150−150)/2 + 128 = 128` and `(352−352)/2 + 2 = 2`, which is what the
  origin-add branch would also produce. Two coordinates agreeing exactly is the
  whole argument; the rectangle the opener writes would settle it.
* The **session mapping and LOS bits** the in-battle snapshot carries beside the
  preference block. No control on any of the four pages writes either bit —
  `GAMEOPTIONS.GUI` shows them read-only `[07 R-FE-01 §7]` — so nothing in this
  build can change them while the window is open and the snapshot would have
  nothing to restore. Copying them would be two dead fields. A writer reachable
  from the options family would settle it.

The exit opener enables the first named, authored-inactive `RESTART` control
for campaign and skirmish and installs the translated `Restart` caption before
creating the retained widget panel [07 R-FE-01 §7]. `battle_restart.go` owns its retained dialog state and request;
`RESTART.GUI` replaces `EXITMENU`, wraps the mission/map name to the authored
label width, focuses the difficulty stage, and uses the shared indexed pointer
service. It retains zero token mode, so Enter/Escape do not invoke header
defaults. Cancel exposes the surviving paused options root. The modal painter
reads this child's down/stage state and dynamic labels [07 R-FE-01 §7].

The shell remounts its original content root before consuming an accepted
request, tears down the old battle and uses normal fresh entry. Campaign
re-entry retains teardown W/L marks and loads the campaign's first entry before
its selected entry. Skirmish preserves its map, player count, rows and chosen
difficulty. The direct `--map` extension creates a fresh session on its existing
mount; its exit closure resolves the current battle after any replacement.
Session-less teardown still cleans presentation and shell state. No route uses
`Session.Retry` [08 R-CAMP-01 §8].

Opening `YESORNO` also replaces `EXITMENU`; No/Enter/Escape expose the surviving
paused options root. Closing that root resumes the battle. The ordinary
footer's sources and priority are closed by [07 R-HUD-03 §1]; selection is not a
footer source.

Not implemented: `[07 R-HUD-04 §2]` unfold. Opening the options root in battle
arms an unfold animation whose per-frame step draws the snapshotted window
surface through the **quad-mapped** blitter of `[03 R-RAST-01 §1]` onto a skewed
destination quad. `internal/client` exposes only axis-aligned UI blits
(`UIBlit`, `UIBlitClipped`, `UIBlitFrameScaledClipped`, …); the quad path lives
inside the model composer and is not reachable from the HUD, so the window
appears at once instead of sweeping in. What is left out is the sweep itself,
the one-time `LIGHTBAR` stamp and the `Options` cue at the counter's saturation;
the window's final position and every control on it are unaffected. The one
preparatory finding this build can confirm is that section's own Supported
inference: the authored `PREFS.GUI` panel is 128 columns wide, so widened it is
278 and `limit = windowWidth − 1 = 277` equals the saturation value, which makes
the `counter > limit` branch unreachable.

`TODO(T25)` boundaries remain at their owning code sites and in the feature
sections above. These include the unaligned text-list pen, whose unset native
scratch is not reproduced, and a parity fixture whose pinned retail/Nanolathe
screenshot pair does not record its original scenario. That fixture pins the
comparison rather than reproducible staging.

### Community builder preferences

The **Builders** options page has a Guard home and a Patrol work control for
Hold Position, Maneuver and Roam. Guard choices are Stay / Cavedog / Scatter;
patrol choices are Reclaim / Both / Assist. These are the per-player policies
of [DESIGN_COMMUNITY_PATCH §4.3](DESIGN_COMMUNITY_PATCH.md#43-construction),
not new unit stances. Defaults are Cavedog for every guard stance and
Reclaim / Both / Both for patrol under Community. Modern defaults to Both for
all three patrol stances and keeps those controls usable independently of the
Community filter flag (DESIGN_UNITS_ORDERS_COB "Modern patrol work"). Explicit
saved choices remain; Restore Defaults uses the selected rule set's base.
Strict ignores the preferences.

The settings `builderOptions` block holds two three-element arrays in that
stance order. Each choice is its zero-based position in the labels above.
Invalid values fall back to Cavedog or Both. Every battle entry and retail
save load copies the current human preference into session state; computer
players receive the bound orders rules' defaults. The retail save format is unchanged.

The page participates in the existing options transaction: a live edit sends
`HumanBuilderOptions` through the phase-1 boundary, Undo and Cancel send the
restored values through the same boundary, and OK persists the settings.
The command validates the local human owner and all six values. All existing
and future queues read the acting owner's record through their shared
binding, including after an ownership change. The guards and patrol handlers
continue to ask their bound gameplay rules, so switching to Strict preserves
the selected values without using them. Tests cover player isolation,
boundary timing, preference reload, and the page's Undo/Cancel/OK behavior.


### Prepared-build accelerator status

CP-CON-4 is supplied by `orders.Rules.PreserveBuildToggle` through the existing
session rule set. At the command palette's keyboard accelerator, Community
with `ReclaimToggleKeepsBuild` retains the current toggle status when its low
byte is nonzero and the prepared latch is MOBILEBUILD. Strict and a disabled
feature use the ordinary toggle. The generic widget still clears its radio
group and fires its callback; pointer gestures are unchanged. This follows
the hook's actual generic-widget operands rather than the source comment's
“reclaim-active” name (extension engine reference CP-CON-4). No separate
reclaim boolean is introduced.

The transient `+bps` switch preserves the two retail throughput lines and
outlined bars in both world and strategic views. Since this single-player
engine has no network transport, both rates are zero. It neither changes nor
persists the independent `+clock` preference ([07 §3]).

### Community placement input

Structure rotation is presentation state selected through the construction
rule already bound to the session. Strict 3.1 therefore exposes south only;
Community and Modern expose the current definition's authored facings when
their resolved feature table enables rotation. The retained cursor facing is
not reset when placement ends or when the player selects a definition that
does not allow it. Preview and order issue independently clamp that choice for
the current definition. Quarter turns transpose the placement footprint, and
the mobile-build command carries the clamped facing so the session validates
the same oriented yard and footprint that the cursor showed.

The configured rotation key defaults to `/`. It acts only during mobile-build
placement, Ctrl blocks it, Shift does not, and a definition with fewer than two
allowed facings leaves the key unconsumed. A successful press advances through
allowed facings in S, E, N, W order and plays the ordinary interface click.
Holding the click-snap override modifier (Alt by default) and scrolling cycles
in either direction; the modifier owns and consumes the wheel even when no
current definition can rotate. Build-menu edge selection and its GAF overlay
use the same retained choice. These are sourced extension behaviours
[community patch engine CP-CON-5].

Build-menu structure buttons expose each authored facing in a 13-pixel
nearest-edge band: bottom selects S, right E, top N and left W, with N, S, E,
W tie precedence. A centre click and every keyboard accelerator retain the
cursor's prior choice. Disallowed edges also fall through to ordinary product
selection. The host's `BuildRotationOverlay` preference gates both the edge
gesture and its art. The painter accepts the optional four-frame
`anims/buildrotate.gaf` and `anims/buildrotateclick.gaf` sequences in S, E, N,
W order, otherwise draws built-in chevrons. It reuses the resolved visible
command page and is composed before the existing later popup and modal layers,
so those windows occlude it without a second GUI-stack model [community patch
engine CP-CON-5].

The options root adds a **Placement** host page after **HUD**. Its compact
staged controls expose nanoframe preview (off/full/wire), rotation art,
queued-order drag, team-coloured nanolathe effects, mex and wreck snap radii,
and the Alt/Ctrl/Shift snap
override modifier. This control geometry and wording are Nanolathe host
mapping, not extension behaviour. Outside battle each radius offers Auto, Off
and 1..9 so a profile-independent preference can be stored. In battle the
active profile caps that list, shows the resolved Auto value, and disables a
radius whose resolved maximum is zero. The rotation key stays hand-editable in
settings because this compact page has no general text editor. The ordinary
options snapshot owns Cancel; Restore and Undo copy only these seven fields.

Click snap changes the command point at the host boundary. Its mex and wreck
radii come from host preferences bounded by the resolved community feature
table: negative selects the table default, zero disables, a value above the
table maximum returns to the default, and defaults and maxima are capped at
nine cells. Strict's resolved table is empty, so it bypasses snapping without
a host mode test. Holding the override modifier suppresses snap for that
click.

The search visits the square window from negative to positive X offsets and,
inside each X, negative to positive Z offsets. Candidates with non-positive
counts are discarded; the largest count wins, then the shortest squared
distance from the raw cursor with the source's half-cell bias, then the first
scan entry. An extractor counts footprint cells whose metal byte is strictly
above surface metal and accepts the chosen point only when a second search of
radius `max(footX, footZ)` leaves it unchanged (or it was already the raw build
cell). A geothermal definition uses the ordinary oriented placement predicate
as its candidate count and arms only after moving off the raw cell. Reclaim
snap first refuses a raw cell occupied by a unit, then searches for a
reclaimable feature with positive metal or energy, orders its footprint centre,
and carries the terrain-derived click height raised to sea level when needed.
All mutable terrain and feature reads occur through a read-only session query;
the host owns only the deterministic scan and command substitution
[community patch engine CP-CON-6][I6].

When construction kickout is enabled, the placement rectangle follows the
patch's two drawers. `communityBuildSnap` reports when the patch's click-snap
preview owns the site — an extractor centred over a deposit found within the
snap radius, moved or not, or a geothermal the snap moved — and
`BuildInputState.BuildSnapPreview` carries that verdict to the drawer. Such a
site uses the preview's physical palette entries: 234 for a clear accepted
site, 240 for an accepted site needing own-unit clearance, and 214 for a
rejected site; these indices bypass `GUIColor`, and 240 is black in the
stock palette (yellow only under a mod palette such as ProTA's), exactly as
the patch draws it. Every other site is the engine ghost, whose index retail
forms as the illegal entry 4 plus an offset masked in by the site-valid bit
[07 §9]: GUI entry 10 for a clear site, 4 for a rejected one, and entry 14
(yellow, physical 194 in the stock install) for a site accepted only because
the player's own units occupy it, the patch's clearance offset
(`hud.GhostColorClearance`). The ordinary ghost previously used physical 240
too and drew black over own units (issue #33). Without kickout the retail
logical legal/illegal colours remain in use whatever the clearance and
preview flags hold. Regression checks exercise all six community states,
the retail bypass, the resolved RGB of each under the installed palette,
the unmoved-extractor preview, and a snapped Coast to Coast mex with the
installed palette [community patch engine CP-CON-1, CP-CON-6].

The authored instructions mention Shift+Q/E alternation and using `v` before a
patrol route, but neither the pinned source nor those instructions settle a
separate extension input rule for those keys. `TODO(question)`: determine
whether they rely only on authored gadget accelerators or need host dispatch by
observing the pinned patch with a palette whose quick keys differ. Until then,
Nanolathe does not add a second keyboard table.

### Community order-position gestures

Queued build and movement-order dragging is an optional host input preference,
`presentation.queuedOrderDrag`, default off. It is independent of renderer and
gameplay mode. With the preference enabled, Shift-left press over an eligible
marker in the committed primary queue captures that marker. The host records
the unit slot, published unit instance, primary index, descriptor, creation
tick, target, build product, facing and old position; it never retains or
mutates the live record. While the pointer moves, host-only state draws the
candidate destination marker or oriented build rectangle; invalid build sites
use the ordinary invalid-ghost colour. The patch rewrites the retained record
on each mouse-move message, while Nanolathe commits one typed command on
release so authoritative mutation remains at the input boundary. A committed
selection change, unit-slot reuse, queue-index change or payload change cancels
the gesture so stale input cannot move unrelated work. Leaving the idle latch,
pressing the snap-override modifier, or entering strategic view also cancels
and cannot later emit a command.

The session reproduces the sourced in-place rewrite without removal or
reordering. For an active head, the patch stages the unit's current position
and invokes its ground-move entry before validation. Nanolathe stages the same
position and calls `ReleaseGoalPayload`: that adapter cancels the path request,
detaches and destroys the record's goal, and clears route active/repath state.
The next accepted phase-zero visit installs the new destination; invalid build
placement restores the old position while leaving the interrupted route
released.

Targetless move, patrol and unload destinations copy the cursor position and
reset the record phase to zero. A mobile build retains its queued facing,
derives the oriented footprint and yard through the construction service, and
uses the ordinary cursor placement predicate; rejection preserves the old
position, while acceptance stores the oriented footprint centre and canonical
site height. Hit testing uses the source's projected half-open footprint with
the definition's ordinary authored extents; per-order rotation applies only to
destination centring, preview and validation.

CP-CON-1's manual override gesture uses the configured click-snap override key
(Alt by default) and takes precedence over both queued dragging and Modern
Alt-move. Press over a committed local unit captures its slot and published
instance without changing selection. Releasing while the override remains held
emits `HumanCommunityKickout` to the cursor's unvalidated world point; releasing
the key cancels. The command revalidates local ownership and the published
instance, then calls the construction service's feature-gated `KickoutMove`.
Strict and a disabled CP-CON-1 feature therefore cannot reach the rewrite. Both
gestures are sourced extension input [community patch engine behavior §5.11].

## TA Zero content presentation

The skirmish side selector uses the compiled `SIDEDATA.TDF` faction count
and order for both cycling and normal button stages. Retail remains Arm/Core;
Zero is GoK/Arm/Core. Extra art frames are not playable factions: Zero's
`SIDEx` also contains Watch, pressed and disabled images. Button raster rules
remain unchanged. The shell reads the count at composition, never during a
simulation tick. The profile-selected frontend PCX paths and logo GAF are
load-time presentation choices described in
[DESIGN_CONTENT_VFS §5](DESIGN_CONTENT_VFS.md#5-divergences).

The Zero recommendation is an optional host preset, with independent sensor
thresholds and its authored player palette, owned by
[DESIGN_MODS_MUTATORS §4.3](DESIGN_MODS_MUTATORS.md#43-selection-and-precedence).
The existing side definitions continue to supply each faction's command HUD,
font, colours and build pages. No Arm/Core index assumption replaces them.

Synthetic checks cover faction cycling, retail defaults and replacement team
banks. Installed-content checks cover Zero's backgrounds, all three side
buttons and HUDs, construction from authored menus, and team-colour model
captures. Their compatibility boundary is [TA_ZERO_SUPPORT](TA_ZERO_SUPPORT.md).

## Modern radar dots

**Nanolathe Modern policy (user-authorized 2026-10-01, issue #62).** The
Enhanced main view offers `presentation.radarDots`: 0 **No dots**, 1
**Visible dots** (the existing display-only default), and 2 **Attackable dots**.
The Nanolathe settings screen and battle options persist the choice through
ordinary presentation settings and mod layers. Strict 3.1 and Community 3.9
bypass the feature: the existing `visibility.Rules` seam answers whether the
main view may expose sensor-only dots, and that answer is published with the
committed frame. Modern answers yes. A mode change takes effect on publication;
changing the preference invalidates recorded presentation immediately.

**Strict baseline.** Main-view hover uses visible model hulls
[07 R-SEL-02B2]; minimap contacts have their own admitted picker [03 §3.9].
The Modern addition does not change minimap or Community megamap admission,
model visibility, strategic identification, targeting-facility behavior, weapon
accuracy, AI observations, save data, sensor timing or either RNG stream.

**Main-view boundary.** No dots removes only sensor-only main-view markers.
Visible dots draws them through the existing committed minimap blip gate,
including blink, at the existing fixed screen size and projection at every
Enhanced zoom. Attackable dots uses those same bounds, clipping and reverse
draw ordering for a separate contact-handle picker. Visible models/icons keep
priority. A contact hit never becomes an identified `UnitView` hit: it cannot
reveal a name, model, health, build state, weapon rings, unit-info panel or
selection target, nor offer repair, reclaim, capture, guard or load. Only a
hostile contact may supply a target for a contextual order or an armed Attack
click. The existing cursor weapon admission and ordinary typed command path
remain authoritative; left-click follows the configured interface convention,
and Shift retains normal queued-order behavior. An attackable contact prevents
the Shift resource-construction shortcut and idle right-click move drag from
treating the dot as empty ground. A contextual right press attacks immediately,
so pointer movement before release cannot turn it into a Move order; explicit
Alt-left movement keeps its existing gesture.
Losing contact admission,
changing the viewer, or reusing a slot cannot preserve a cached target.

**State and effects.** The preference is host state; the rule objects remain
stateless. Merely drawing, picking or changing styles changes no resources,
orders or RNG state. An accepted click issues the same ordinary attack command
as an admitted minimap contact, with the existing live-target validation.
There is no new simulation mechanic or extra targeting accuracy.

**Verification.** Lock all three styles, the Modern/Strict/Community answers,
committed rule publication and rebind behavior, normal/fade/strategic zoom,
projection/clip/overlap boundaries, lost admission and blink, and the absence of
identified hover metadata. Command tests lock contextual/armed/queued attacks
and the rejection of non-attack contact actions, including contextual right
presses followed by pointer movement, with unchanged RNG/resource
state before command application. Settings tests lock omitted/default/invalid
values, persisted No dots and Attackable dots, mod layers and settings Apply.
