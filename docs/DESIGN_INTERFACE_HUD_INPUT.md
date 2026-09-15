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

The platform boundary owns native message history, timestamp scaling, and
double-click recognition. The current Ebiten producer polls all modifier and
button states before it constructs one snapshot, retains observed left/right
transitions without representing their retention order as native chronology,
updates the motion fallback, and publishes once before `Client.Step`. Its
timestamp is the existing scaled 30-Hz host-clock value. Polling does not
recover native ordering among changes that arrived between polls, native key
repeat/history, or double-click identity; it does not derive a double-click
timing heuristic.
`TODO(T25): establish an Ebiten event source that exposes native ordering,
repeat, and double-click identity without a heuristic.` [07 §2] [01 R-PLAT-01 §6]

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
`clampInsets` is the one definition of the insets. `Scroll` is the scroll pass:
`magnitude = setting × rawDelta` capped at 128, a signed comparison with no
absolute value, zero delta meaning no movement, where `rawDelta` is thirtieths
of a second and not milliseconds `[07 §10]`. `Pan`, `Clamp`, `JumpTo`,
`BattleViewCenterOrigin` and `JumpToBattleViewCenter` are the jump family; a
jump writes the origin, clamps, and copies the clamped result into the desired
origin so no glide survives it `[07 R-CAM-01 §12]`. `WorldToScreen` applies the
half-height shear `wz − (wy >> 1)` with arithmetic shifts; `ScreenToWorld`
inverts it at ground height for pixel-level questions only.

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
precedes phase 10. `Step(*Camera, dx, dy)` writes each origin as
`(trunc(delta / 4) + anchor) × 16`, clamps it, copies current into desired, and
updates the anchor; it does not clear follow during later steps. Entry also
refreshes the pending host sample so neither a tracked target nor an earlier
glide can resume after capture in the same sub-tick batch. Its battle
adapter supplies successive pointer deltas. The camera primitive quantizes the
retail beam origin through `BattleViewOrigin`, never framebuffer `X`/`Z`,
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

**The minimap** (`minimap.go`). `LayoutMinimap` is the letterbox: the longer map
dimension occupies `MinimapLongSide = 126` pixels, the other is scaled by
integer division, and the unused axis is centred by truncating the half
padding. `PlayRight`/`PlayBottom`/`PlaySize` are the playable extents the radar
lens is built on `[03 §3.4]`. `WorldToRadar` and `RadarToWorld` are the lens
conversions, `WorldToRadarWithY` the one that carries the height term
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
surface hook, and returns after the first fired record. `WidgetHooks.Change`
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

`TODO(question)`: Nanolathe has no identified authored battle-root
`MAIN2.GUI` opener. Retail builds that root before the successful transition;
its integration must preserve that timing when the root owner is implemented.
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
repeat cadence or a native order among simultaneous physical edges
`TODO(T25)` `[07 R-WGT-01 §2]`.

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

**Anchors and bars** (`anchors.go`, `bars.go`). `Anchors [30]Rect` is the side's
mandatory anchor block, stored verbatim as authored `x1,y1,x2,y2` corners and
never normalised `[02 §6]`. `AnchorNames` is the fixed name list and
`AnchorIndex` its reverse. The bar helpers fill horizontally or vertically from
an anchor and a fraction; `HealthFraction` and `ResourceFraction` are the two
clamped ratios `[07 R-HUD-03 §4]`.

**Chrome** (`chrome.go`). The layout rules for a surface larger than the design
space: `ChromeRailX = 129` is the x origin of both horizontal strips,
`StripStamps` is retail's "advance by the frame width while `x < width`" stamp
loop, `BottomStripY` is `surfaceHeight − 32`, `RailGap` is the band of the left
rail that no chrome covers and that stays palette index 0 for the whole battle,
and `ModalPlacement` centres a modal in the surface width to the right of the
128-pixel rail and in the full surface height, both by truncating divides, at
the live size `[07 R-HUD-05]` `[03 §4.1]`.

**Selection and pages** (`selection.go`, `build.go`). `NormalizeDragRect` and
`DragRect.Contains` are the rubber band. `NextSelected`/`NextFlags` are the
modifier truth table; `ApplyDragSelection` and `ApplyDragSelectionFlags` apply
it over the owner's slots in stable ascending order with the membership bit and
the GUI dirty bit `[07 §9]`. `AssignGroup`, `RecallGroup` and
`TypeFilterPasses` are the control groups and the `CTRL_F` filter.
`EncodePageBits`/`DecodePage`/`IsPaged`/`RememberedPage` are the page bits 23–25
with bit 22 marking paged. `RoutesToPage`, `DigitToPage`, `DigitToGroup` and
`HandleDigit` are the digit gate. `BuildProductsFor`, `ProductsForPage`,
`BuilderPageCount`, `NextPage`/`PrevPage` and the button/key variants are the
page cycle; `RetailBuildButtonsPerPage = 6` is the authored full-page size, and
no runtime path may infer a different grid `[07 R-HUD-03 §6]`.

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
pick result and `CursorSelection` the acting side, including the stocks the
command-fire affordability gate reads.

**The footer** (`footer.go`). `BuildFooter` composes the bottom readout from the
committed frame, the catalog and a `FooterHover`: the build-card line for a
hovered build button, the feature line, and the unit readout with its name,
damage bar, logo, metal and energy rates, kills line and secondary field
`[07 R-HUD-03 §1]` `[07 R-HUD-03 §2]` `[07 R-HUD-03 §3]`. The output is a value
— texts, bars and logos with a `FooterColor` that records whether the byte is
raw or logical — so the composer performs the palette lookup and this package
performs none.

**The minimap** (`minimap.go`). `MinimapHUD` binds the camera layout to the
side's anchor rectangle; `ViewportRect`/`MinimapViewportRect` are the
camera-to-radar rectangle stroked as a one-pixel outline in colour-map entry
`ViewportMarkerLogicalColor = 14` `[03 R-MM-01 §1]`.

**Status and the score panel** (`status.go`, `scorepanel.go`). `SnapshotStatus`
reduces a committed frame to the values the rails display: resources with their
formatted strings, the construction and factory readouts, the current order and
the selection summary. `ScoreShowing` is the Space-held panel's gate — the
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
five-bit mask, every other local unit draws marker-only, and the marker-only
fallback runs only when one of the first three resolves to a live builder.
`queueDescriptors` is the per-kind census of the order descriptor's two overlay
bytes — the draw-mask word (marker 1, dash 2, circle 4, icon 8, range 16) and
the icon byte that indexes the same twenty-two-slot cursor array the software
pointer uses. An icon byte of 0 is the "no icon" encoding rather than cursor
slot 0. Helpers run in mask-bit order, and the bit-8 helper is also the anchor
getter, so a kind that sets bit 2 without bit 8 still draws its icon first.
`DashSprites` places the authored sprite chain along a world-space segment —
the dash is a sprite chain, never a line to rasterise. `BuildMarkerSegments` is
the eight-segment build marker with its ten-tick sweep.

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

`activateGadget`,
`activateEscape`, `activateSkirmishGadget` and `activateDynamicSkirmishGadget`
are the callbacks; `openMissionMenu`, `retailSkirmishStartError` and
`retailAllPlayersSameAlliedGroup` are the `SKIRMISH` start preflight
`[07 R-FE-01 §5]`. `retail_menu.go` owns the panel refreshes and the authored
data flow (campaign options, map data, skirmish rows, ally icons, hover help);
`retail_menu_list.go` the listbox and the scrollbar geometry, knob sizing and
drag; `retail_menu_options.go` the options family — both roots and all four
merged pages (`SOUNDS`, `MUSIC`, `SPEEDS` — whose root button is captioned
`INTERFACE` — and `VISUALS`), the display-mode list, the per-page `RESTORE` and
`UNDO`, the entry snapshot `CANCEL` restores, and the slider arithmetic
`[07 R-FE-01 §6]` `[03 R-AUD-01 §2]` `[03 R-AUD-01 §4]` `[07 R-CAM-01 §7]`.
Rebuilding a merged page restores the selected category's down-state in the
replacement panel, including the Nanolathe category, while releasing the old
pointer capture. Direct opens and per-page reopens therefore show the same
radio selection as pointer activation `[07 R-WGT-01 §3]`.
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
`LOADGAME.GUI` serving both save and load, with the summary field mapping
`[07 R-FE-01 §8]`; `postbattle.go` and `result.go` are the post-battle machine,
its glamour fade and the score bars `[07 R-FE-01 §10]`.

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
`SetMessageFNT`; `SetFNT` retains the side console for group digits
`[03 R-FX-01 §6A]`, and the HUD passes its own font operands explicitly. The
column advances by the COMIX glyph height. Each line resolves logical colour
15 (ordinary) or 10 (the F3 destination) through the active palette map before
recording its glyph command, so classic and modern share the same foreground
`[07 R-HUD-03 §14.4]` `[07 "Retail palette contract"]` `[03 §4.3]`.

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
selection (`<prefix>gen.gui` for an empty selection, the per-unit window
otherwise, the generated `<unit>N.GUI` numbered pages) and the gadget art and
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

**Displayed resource stocks.** `Client` owns the two displayed singles and
passes them by value in `UIFrame.Resources`. `BeginPresentationFrame` advances
them once per host presented frame using `[05 R-ECO-01 §6]`; both stock bars
and current numbers consume this pair, while capacities remain live.
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

### 2.9 Capture tooling

`shot.go` and `flags.go` own `--shot`: compose one battle frame after
`--shot-ticks` authoritative ticks and exit without opening a window.
`--shot-select` runs the select-all first so the command page is open,
`--shot-modal` opens one of the modal layers, `--shot-space` holds Space so the
slide strip is raised, `--zoom` and `--shot-focus` stage the presentation
zoom of §3.5, and `--shot-size` composes at a surface other than the authored
640×480. A capture is the evidence for any visual change in these packages.

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
sustained rate is 960 map pixels per second at any frame rate and the cap is a
low-frame-rate limiter `[07 §10]` `[07 R-CAM-01 §10]`.

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
opens settings on a keypress. The option has no authored options-page gadget.
Partial I10 implements its local chat command through the shared TALK command
path (§3.9).
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

#### Modern expanded sidebar prototype

This is a user-requested presentation extension, not retail evidence. Classic
keeps the single authored command window and the unused lower rail described
by [07 R-HUD-05]. Modern can use that rail for additional controls when the
framebuffer is tall enough. World zoom does not change the available UI pixels.
The Nanolathe options page's **Expanded sidebar** switch enables this layout.
It defaults on, persists in `presentation.expandedSidebar`, and previews through
the live shell with the page's ordinary Cancel, Undo and Restore transactions.
Off restores the authored single page even when the surface has spare height.

The modern view partitions resolved build pages into complete authored rows.
Rows retain button identities, artwork, horizontal geometry, empty slots and
internal spacing. They form one sequence in authored page and vertical row
order, rather than a list reconstructed from build membership. Each visible
page takes as many complete rows as fit after reserving tabs, page navigation,
orders and the common command footer. There is no fixed six- or twelve-item
limit: an ordinary two-column GUI can show eight or ten items when four or
five rows fit. The last page stops at the sequence end without repeating rows
from its beginning. Trailing template-only rows from each source page do not
contribute to pagination; holes before or beside real product records retain
their authored positions. Navigation, orders and footer positions reserve the
same build-area height on every visible page, including a partially filled last
page. A builder whose entire row sequence fits uses only that sequence's height.
Additional orders remain above the normal bottom command
buttons, preserving the Orders page's authored gap before that block.

Visible page numbering is presentation state, independent of the authored GUI
page numbers and committed unit page bits. Page zero selects Orders while
displaying the remembered visible build page. Positive pages select partitions
of the row sequence; Count includes Orders. Arrow buttons cycle among visible
build pages, comma/period also visit Orders, and digit d selects visible page
d−1 if it exists. The existing SwitchAlt gate still chooses paging versus squad
recall. BUILD returns to the remembered visible build page. These navigation
actions update the view and its cue without submitting simulation commands.

Resizing repartitions the rows and selects the new page containing the previous
first visible authored row. A changed builder or externally changed authored
page seeds the view from that source page. Switching to Classic or disabling
expansion discards this local state and restores ordinary authored paging.
Layouts whose rows, navigation and bottom commands cannot be separated and
combined safely retain the original single page.

The mod boundary is explicit:

| Authored by assets | Owned by engine code |
|---|---|
| `.GUI` gadget names, rectangles, activity, labels, shortcuts, associations and font choices | Command-window selection, command interpretation, retained pointer service and page navigation |
| GAF button and panel artwork, including dimensions | Sidebar/minimap/world regions, screen-edge anchoring and modern block placement |
| Side-data interface names, fonts, colours and readout anchors | Dynamic selection, command availability, queue counts and resource text |
| Numbered GUI pages and download-menu placements | Resolving physical/generated windows and preserving their product identities |

These responsibilities follow [02 §6], [07 §4], [07 §6] and
[07 R-HUD-03 §6]. The extension uses resolved windows through the existing VFS;
it neither replaces mod art nor derives pages by slicing the combined build
membership list. Sparse and reordered download placements retain their authored
slots. A layout the prototype cannot compose safely falls back to the original
single-page UI; this is not a promise of support for arbitrary replacement GUIs.
Adaptive rows require consistent row height and pitch, compatible column slots,
and matching navigation/footer geometry across the source build pages. A row
may omit a column or repeat a record without changing its placement. The first
source page supplies the spacing skeleton even when a visible page starts on
a shorter source page; each gadget still uses its own source art and font.
In particular, the prototype accepts button/font pages with controls and art
contained in the rail. Bounds include every artwork state's visible extent
without changing the authored hit rectangle; a border wider than the button
can still fit. Linked controls, editors, sliders, unsupported widget
kinds and art that spills into the world retain the original UI. Definitions
with a custom `<unit>0.GUI` also retain the original path: that pre-existing
orders-page support is outside this prototype.

The composed window keeps source artwork and font lookup per gadget. Matching
order groups share the single command latch; unrelated associations remain
separate per source block. Only repeated semantic command
and navigation controls are omitted; product records and empty slots are not
deduplicated. Quickkeys retain source record precedence among displayed
controls. The layout caches source definitions, surface, visible and authored
page state, and the transport capability that chooses LOAD versus BLAST.

Drawing, hit testing, hover cards and retained pointer/keyboard service must
share the composed geometry. Source-window identity must survive composition
for font and artwork resolution. Resize, page/selection change and renderer
switch must release stale pointer capture. No composed window may mutate cached
source windows, committed frames, catalogs or simulation state.

Verification: authored synthetic layouts exercise translated activation,
source identity, unsupported-shape fallback and capture changes; retail checks
exercise physical and generated pages. GPU captures review the expanded rail
and compare classic and short modern surfaces against their previous pixels.
The row-paging checks include partial source pages, non-overlapping navigation,
last-page behavior, resize anchoring and unchanged-frame persistence. GPU
captures verify intermediate capacities and source artwork across page
boundaries. Both options-panel captures fit the caption and Off/On control at
640×480; transaction checks cover live preview, persistence, Cancel, Undo and
Restore.

The row prototype's stock ARM captures show eight items at 1280×780, ten at
1280×844, twelve at 1280×908 and eighteen at 1920×1080. Advancing the eight-item
view shows the next eight records. A loose GUI override that swaps two build
buttons retains that order across the source-page boundary. Classic at
1920×1080 and short Modern at 640×480 match the pre-prototype baseline pixels.
These sizes describe the checked stock layout, not fixed engine thresholds.
The empty-page regression capture at 1280×844 verifies that Next wraps after
the populated pages. At 1280×908, the first and partially filled final page
render identical pixels throughout the navigation/orders/footer region.

Frozen-frame GPU timing was unavailable during prototype verification: the
baseline Metal profiling path failed on both attempts before the comparison
reached this prototype.

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
`[07 R-FE-01 §3]`.

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
§5]`. `gamespeed` is consumed only from the in-battle arm: the
front-end root has no session to apply it to, and battle entry is the reader
there.

**C18 — the in-battle modal chain.** An empty selection activates the
side-authored `<prefix>gen.gui`, not the underlying `<prefix>main.gui`. In a
non-network battle the options window sets the single-player pause state and
draws `igtitles.gaf:igpaused` at the live view centre using its authored GAF
offsets `[07 R-HUD-05 "Centred in the view"]`; closing it unpauses. `EXIT`
pushes `exitmenu.gui`, whose Main Menu and Exit Game choices push `yesorno.gui`
and commit only on `CHOICE1`, with both Enter and Escape bound to `CHOICE2` and
focus on it. The options window keeps its authored origin, while the `0x1000`
modal flag centres Exit and Yes/No in the playfield to the right of the
128-pixel rail. The `MISSION` gadget is **relabelled** to the translated
`Settings` for a skirmish and opens `GAMEOPTIONS.GUI`; it is never hidden or
greyed. `PREFS` opens the options root as a child window over `ARMOPT`, which
stays on the chain underneath with its pause bit still set; the root's `PREV`
("OK") saves the whole preference block and `CANCEL` restores the entry
snapshot, and either one returns to `ARMOPT` rather than to the battle. Escape
takes the same route, because `PREFS.GUI` authors `escdefault=PREV`. Each modal
renders into an exactly sized clipped surface whose background resolves through
the common `BackTile` as a nine-slice fill, and labels use the primary GAF font
rather than the side FNT `[07 R-FE-01 §6]` `[07 R-FE-01 §7]` `[07 R-FE-02 §4]`
`[08 R-SKIR-01 §11]`.

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
below Visuals in both STARTOPT and PREFS. The engine adds its gadget and builds
a page from the VISUALS canvas, label style and control dimensions. The front
end uses the original options background; battle uses the game's tiled window background.
BUTTONS0 and stagebuttn2/3 from the game assets supply the buttons and controls.
No retail asset is copied into the repository or changed on disk.

The page contains captioned Renderer (Classic / Modern) and Gameplay
(Strict 3.1 / Modern) rows. Compact FPS (30 / 60 / 120), Sidebar (Off / On),
Glow, Water, Lights, Metal, Heat and Marks controls carry their own names.
The two captioned rows use a tight
caption-plus-control pitch and the switches a
narrower one, so the page fits the in-battle column as well as the front-end
one without reaching Restore Defaults or Undo Changes.

Gameplay defaults to Modern, independently of the renderer, and follows
DESIGN_WEAPONS_PROJECTILES §2.3.1. The remaining controls default to Modern,
60 FPS and every switch on. These are presentation choices;
simulation remains 30 Hz. The cap bounds modern presentation on the display's
refresh grid; classic still presents at 30 Hz. Higher or refresh-following
values remain available through `--fps`; a value outside the presets is
displayed as stored.

The six switches select the Enhanced effects the modern executor draws, listed
with what each gates in [DESIGN_GPU_RENDERER.md](DESIGN_GPU_RENDERER.md) §30.
Glow edits the display block, where §19.4 already put it, so it previews through
the same live path the Visuals rows use; the other five edit the presentation
block, which the host polls each update. Classic presents identically whatever
they say. The same five toggle from the message line as `+water`, `+lights`,
`+finish`, `+heat` and `+marks`, beside the existing `+glow`, each persisting
its value the way the display-bit commands do.
Expanded sidebar selects the modern composition described in §3.3 and remains
independent of the renderer choice; Classic always uses the authored page.

Edits preview immediately; gameplay changes enqueue a typed command for the
next simulation boundary. OK saves gameplay and the presentation block with the existing
settings transaction; Cancel restores the entry values, Undo restores this
page — including its glow bit — and Restore Defaults chooses Modern / 60 with
every effect on. F10 updates the shell and saves
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
ghost, cursor and build dispatch. Page input projects pending commands for the
same builder through `Session.PendingBuildPage`, so multiple keys before a
tick preserve page order and cue only actual changes. Palette painting and
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
2. **Right button, anywhere else.** Under `Interface Type 0`, right is
   deselect and cancel only. Under `Interface Type 1`, an idle right-down in
   the viewport issues the contextual order; an armed placement or latch still
   cancels on right. A factory product button is the one exception, subtracting
   one or five from the matching tail node (or twenty with the Alt extension
   in §5). Thus only Type 1's idle viewport
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
   ground-resolved whole-world endpoints on press. Held passes update the moving
   endpoint; release classifies the stored endpoints without refreshing them.
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
| Tab, F2 | open and close the options window |
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
| Ctrl+D | self-destruct the selection |
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
unclaimed pointer work remain available. A focus-only linked-label shortcut
consumes its token and continues later gadget visits. Ctrl-composed quickkeys
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
compositions to the excluded retail developer paths.

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
  screen. `ScreenToRecord` bridges the two for the hover hull and the drag
  rectangle, which compare a pointer against corners projected at the step.
* **Arrow-key scroll speed.** Arrow keys apply the existing setting, host-time
  delta and signed cap in screen pixels, then divide by the live zoom before
  moving the camera. Fractional map pixels carry per axis while zoom is steady;
  a zoom change or a clamped move clears the corresponding carry. This keeps
  the standard 1× screen speed at every zoom, including slow settings at 2×.
  This is a Nanolathe presentation choice; edge scrolling retains its retail
  map-pixel rate.
* **The native fast path is exact.** At factor 1 both conversions take the
  original integer path unchanged, so nothing composed at native scale differs
  by a pixel from a build without the feature; the same holds at 2×
  when the factor is on the step.
* **Its writers.** F9 in the battle, which in classic toggles the step
  1× ↔ 2× about the viewport centre and in modern cycles
  1× → 2× → 0.25× → 1× as animated zoom targets; **the mouse wheel over the battle
  viewport**, which in the modern executor steps a zoom target through the
  fixed factors about the pointer (below); middle-drag through `Drag`, which converts the
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
  executor presents, one wheel notch moves the zoom target one step along
  the fixed list {0.25, 1, 2}, wheel-up zooming in
  and the world point under the pointer staying put; a trackpad's fractions
  bank until they are worth a notch. The live factor eases toward that target
  on the host Update grid, so a notch is a glide between two steps. The
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
selection uses the fill-time scroll limit. Its outcome title reuses the loaded
`igvictory`/`igdefeat` frames at `(W/2, 28)` with ordinary authored offsets
`[08 R-CAMP-01 §8]`. Selected Core briefings use the installed `mbriefcor`
background key `[08 R-CAMP-01 §2]`.

### 3.9 Partial I10 and exclusions

**Partial I10 — single-player TALK.** After the active GUI and command palette
decline Enter, battle opens the installed `TALK.GUI`, plays `SmallButton`,
places its authored 512×33 strip at `(128, H−33)`, hides `SENDTO`, and focuses
the retained common-widget editor. The dialog owns keyboard, button and world
pointer input through its close frame. Simulation continues, pointer-edge
camera scrolling remains live, and held-arrow and drag camera movement are
suppressed. Enter posts and Escape cancels; either exit clears the editor.
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
| `ShowRanges` | toggle detailed terrain-following range rings and labels inside the existing Shift-held queue overlay; retained by the shell across battles, without settings or simulation writes |
| `Dither` | toggle the live current-fog pattern selector and persist `0` or `1` immediately |
| `TShadow`, `FShadow` | toggle vehicle or feature shadows independently; persist on the next settings write |
| `MusicMode n` | set the signed desired category through the existing music controller; fade/delay timers use the busy presentation pump and do not write settings |
| `CDPlay n`, `CDStop` | use the existing music controller; argument zero runs its enabled music tick |
| `Sound3D` | toggle the live audio output mode; write settings while retaining the separate stored sound-mode preference |
| `Sing` | toggle the existing voice queue's audible alias override; preserve captions, arbitration and random draws; no settings write |
| `View p` | skirmish only; queue the low-byte viewing slot without changing command ownership or requesting a visibility refresh |
| `Give p n metal/energy` | queue a signed resource transfer from the viewing player at drain time through the existing economy ledger; no settings write |
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
developer table and default unit-spawn handler remain excluded with developer
mode; multiplayer `TALK2.GUI`, recipient controls and network chat remain
excluded with multiplayer `[07 R-CAM-01 §6]` `[07 R-FE-02 §12]`.

* **The multiplayer lobby shell.** Out of scope for the whole engine; the
  single-player skirmish setup screen is a different surface and is implemented
  `[07 §12]` `[07 R-FE-02 §1]`.
* **The front-end movie stage.** Normal launches play the original startup
  logo `Data/1.zrb` once, then open `MAINMENU` `[07 R-FE-01 §3]`. Playback
  is deferred to the first shell update so the platform PCM device is ready;
  the pending surface is black and menu music starts only after completion.
  Direct map and saved-game launches bypass the logo. The requested portable
  startup policy plays the logo on every normal launch, including windowed
  launches; it does not add retail's one-install `PlayMovie` preference or
  automatically append the full intro. Holding Shift at startup does not
  request repeat. Capture and the `CDCHECK` gate remain excluded.
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
* **Developer mode.** The `\` console, the contour overlay, the in-battle
  screenshot and the Unit State/Builder Probes are not implemented
  `[07 R-CAM-01 §9]` `[07 R-FE-02 §11]`.
  The front-end `DRDEATH` cheat sequence also lacks its token-history consumer
  `[07 R-FE-02 §10]`.
* **Clipboard paste.** Insert and Ctrl+V reach the editor, but the portable host
  has no clipboard byte bridge. They leave the current text unchanged. Retail's
  bounded `CF_TEXT` replacement operation is established; translating a host
  Unicode clipboard into the retail code page remains unresolved `[07 §2]`.
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
later does not retarget the queued request. Buildings use the existing mission
footprint snap and height probe `[08 R-ENTRY-01 §6]` `[08 R-ENTRY-02 §1]`;
mobiles retain the terrain point. The session checks the current local player,
catalog definition, map bounds, terrain suitability, features and occupancy
before allocation. Every footprint cell is checked even for open building
yards; there is no search for a nearby free site or displacement of blockers.

Successful allocation uses the normal fully built creator, including its unit
limits, COB initialization, activation, allocator RNG sequence and movement
registration. The normal observer and publication passes expose the unit. No
build resources are charged or granted; subsequent unit operation participates
in the ordinary economy. Rejections before allocation draw no RNG; allocator
or script failures retain the common creator's failure semantics. Usage errors,
unknown names, unsuitable sites and allocation failures produce chat feedback.

Verification locks submission-time coordinates, local rather than viewing
ownership, creation state, repeated-site rejection, no resource charge, ordinary
creation RNG effects and the Strict bypass, including a mode switch after enqueue.

### 3.10 Modern resource double-click construction

**Established implementation policy (user-requested departure, not retail
behavior).** With the modern executor active, an idle selected mobile builder
accepts Shift-left-double-click on a metal deposit to build its strongest
available extractor, near a geothermal vent to build an available geothermal
plant, or on ordinary ground to build a solar collector. Every
Shift-double-click appends, preserving the normal queue-command modifier. The
typed mobile-build command carries explicit `Queued` and `AppendOnly` intent: existing work is
preserved; a repeated site is moved clear of queued footprints instead of
using the manual Shift-placement removal gesture. The complete authored build
menu supplies candidates, independent of the currently displayed page. Equal
extractor strengths and multiple solar or geothermal candidates retain authored menu order.
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
point matching and insertion. This behavior applies in both gameplay modes.
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
queued footprints are red and skipped; valid sites are green. On release the
first accepted build replaces orders unless Shift is held, and subsequent sites
append without duplicate-site toggle. If no site is valid, placement stays armed.
A click still uses the existing single-site path, on release in modern mode.

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
available; Shift at a command's release appends instead. Shift also shows
tactical ranges; Alt+digit keeps the squad shortcut. Active command drags
suppress range guides so their preview stays readable; releasing or cancelling
the drag restores guides if Shift remains held. The traced polyline accepts
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

* **Alt batches factory products by twenty.** This user-requested build-menu
  extension adds twenty on Alt-left-click and subtracts twenty on
  Alt-right-click. Alt takes precedence over Shift; Shift alone retains five
  and no modifier retains one. The shared product callback uses the held
  modifiers at activation, including a product quickkey. Counts use the existing
  signed queue command, so addition coalesces and subtraction consumes matching
  queued products normally. Mobile building placement and stockpile toys keep
  their existing actions. This is host input policy, not a retail behavior claim.

* **Tab resumes an already paused battle.** As user-requested host policy,
  Tab with no modal open resumes the battle directly; F2 still opens options.
  Child dialogs retain input ownership, so this shortcut cannot bypass a
  save/load or confirmation dialog. This is not a retail behavior claim.

* **Modern resource construction shortcut** is the user-requested input policy
  in §3.10. It produces ordinary typed commands, with no alternate simulation
  or placement rules.

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

Native event history, text code pages and the clipboard bridge remain marked
platform gaps in the input and editor paths (§2.2 and §3.9). The other open
questions these contracts carry follow, with the observation that would settle
each one.

* Whether the world-click producer receives a goal point alongside a target
  handle. The match rule takes both arguments optionally, and the click is
  documented as issuing "at the pointer's world point", so this boundary
  supplies both and the goal term participates in a target-click match. If
  retail passes no goal there, a repeat Shift-attack-click on a target that has
  moved more than one cell since the order was queued would remove it where this
  build re-queues. A trace of the world-click handler's call into the producer
  settles it `[07 R-P0-11 §6]` (marked in `internal/session`, which owns the
  producer).
* Whether the interface's non-world-click queued issues share that producer. The
  section scopes the test to "every world order the interface issues", and the
  two world-click boundaries are its only callers; the side panel's own buttons
  — Stop, the activation toggle, stockpile, Ctrl+D, the two stance gadgets —
  issue no world point, and nothing says whether a Shift-held press of one runs
  the test, which would make a second Shift-press cancel the first. A trace of
  those button handlers settles it `[07 R-P0-11 §6]` (same site).
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

The exit menu enables the authored `RESTART` control for campaign and
skirmish. `battle_restart.go` owns its retained dialog state and request;
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
