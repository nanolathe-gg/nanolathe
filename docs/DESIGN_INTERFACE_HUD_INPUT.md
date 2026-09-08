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
* **What does the session hear about it?** Exactly one call:
  `Session.EnqueueHumanCommand`, taking a typed value that carries handles and
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

**Follow and bookmarks** (`follow.go`, `bookmarks.go`). `FollowState` is the
rest of the retail camera block: the desired origin, the tracked object handle
and four bookmark slots. `DesiredOrigin` and `FollowTo` step the origin toward
the target; `GlideTo`/`StepGlide` are the message-source and next-unit glides;
`SetTracked`/`ClearFollow` are the tracked-object writers, each named by the
jump family's table; `StoreBookmark`/`RecallBookmark` are Ctrl+F5..F8 and
F5..F8 `[07 R-CAM-01 §12]` `[07 R-CAM-01 §14]`.

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

`retail_menu_input.go` is the gadget service pass, in retail's order: the
scrollbar drag update and the held-arrow repeat, the wheel, then press — which
takes the capture and gives the gadget focus before any callback, so a
following Return or Space activates the same control — then release, which runs
the list, scrollbar or callback arm and **returns immediately**, because a
callback may close the window it was invoked from; then the skirmish
right-button rows, then Escape, then Enter/Space against the focused gadget or
the window's `crdefault`, then the authored quick keys, then the list arrows
`[07 R-WGT-01 §1]` `[07 R-WGT-01 §2]` `[07 R-WGT-02 §5]`. `activateGadget`,
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
`retail_menu_draw.go` the screen painter; `window_panel.go` shares authored
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
the site check and `commitBuild`. `battle_commands.go` and `battle_dispatch.go`
are the command boundary of §3.4. `battle_menu.go` drives the modal chain and `battle_options.go` the in-battle
options window `ARMOPT`'s `PREFS` opens over it;
`battle_settings.go` the damage-bar and message-line options;
`battle_cursor.go` the pointer update and the footer hover;
`battle_status.go` the game-speed and pause hotkeys, posting the announcement
to the shared message ring;
`battle_queue_overlay.go` the overlay's art resolution and draw.

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
slide strip is raised, `--shot-zoom` and `--shot-focus` stage the presentation
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

**C5 — input queues.** The keyboard ring has 30 entries with 29 usable and the
mouse ring 24 records. A full ring refuses the new event and never overwrites an
older one `[07 §2]` `[01 R-PLAT-01 §6]`.

**C6 — selection modifiers.** With the modifier clear, units inside the
rectangle are set and those outside cleared; with it set, units inside toggle
and those outside are preserved. Owner slots are visited in stable ascending
order `[07 §9]` [I1].

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
opens settings on a keypress. The option has no authored options-page gadget;
the local chat command that can alter it is outside this unit's chat scope.
The page number lives in unit-flag bits 23–25 with bit 22 marking paged, guarded
by the builder's page count. Generated menu records author `PAGE` and `BUTTON`
explicitly, and the generated `<unit>N.GUI` pages determine page existence and
placement. Stock full pages carry six 64×64 product gadgets and the last page
may be partial, so no runtime path may infer an eight-slot grid `[07 §9]`
`[07 R-CAM-01 §4]` `[07 R-HUD-03 §6]`.

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

Three persisted values are stored and re-shown but not yet consumed, each with
its consumer named at the write site: `Sound Mode`'s `Mono`-versus-`3D`
distinction (the output device's 3-D flag — this build pans positionally either
way), `Interface Type` (`internal/orders` holds the word behind a `TODO(T23)`
and exports no setter), and the per-track music category array (retail persists
it in the `CDLISTS` ring keyed by the drive's volume serial, which this build
has no analogue for). `gamespeed` is consumed only from the in-battle arm: the
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

### 3.5 The pointer and latch state machine

One press/release pair is routed through exactly one path, and the path is
chosen on the **press** edge. In order:

1. **Over the fitted minimap lens.** Under the default `Interface Type 0`
   polarity, a right down edge sets the minimap camera capture. The frame that
   set it does not jump; each following host frame tests the already-set
   capture before new clicks and re-runs the camera jump from the live pointer
   record until the matching right up edge. The signed lens conversion runs
   before camera clamp even after the drag leaves the lens. Left down issues
   the armed order or world click at the lens point. `Interface Type 1` swaps
   those two minimap buttons. The canvas letterbox bars suppress a viewport
   drag but are not lens/world-pointer input; cursor, footer and command paths
   share that classification `[07 R-CAM-01 §5]` `[07 R-CAM-01 §11]` `[07 §8]`.
2. **Right button, anywhere else.** Right is deselect and cancel only: a factory
   product button is the one exception, subtracting one or five from the
   matching tail node; otherwise an armed placement disarms, then an armed latch
   returns to idle, then a non-empty selection clears. No right-button path
   queues an order `[07 §9]` `[04 §3.4]` `[07 R-P0-11 §1]`.
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
5. **Otherwise the world drag.** A held left starts the rubber band and tracks
   it; the release classifies by size. A rectangle smaller than three pixels on
   both axes is a click: with a latch armed it dispatches that latch's code and
   then returns to idle unless Shift keeps it; with the idle latch it selects
   when the picked unit passes the shared eligibility predicate, issues the
   contextual code 1 when a selection exists, and otherwise clears the selection
   unless Shift is held. A larger rectangle is a drag selection, replacing or
   toggling by the modifier `[07 §9]` `[07 R-CAM-01 §14]`.

Two rules cut across the machine. A latch held by Shift retires on the live
Shift-up whatever the order family `[07 R-P0-11 §4]`. Escape returns an armed
latch or placement to idle, and an already-idle latch deselects everything
`[07 R-CAM-01 §2]`.

### 3.6 The keyboard table

Retail folds Ctrl into the token itself, so a Ctrl-composed token can never
reach an unmodified key's case; every unmodified arm here is gated on Ctrl being
clear, which reproduces that. The rows below are the battle hotkey census
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
| F1 | open `UNITINFOx.GUI` for the hovered unit or the hovered build button's product |
| F3 | glide to the message source |
| F4 | the interface-flags bit that pins the score panel open and arms the kill/loss flash `[07 R-HUD-04 §1]` |
| F12 | clear the message ring |
| Pause | toggle pause |

Space and the arrows have no ring case: the arrows are the scroll pass's
held-key queries. The order latch keys `m a p r e c g d x o` have no row in the
census either — retail reaches those through the command palette's authored
gadget quick keys `[07 §2]` — and they are kept as direct bindings here because
the authored quick-key path belongs to the palette's owner. `\`, `Ctrl+F10` and
F11 are developer mode, `Ctrl+F9` is a screenshot with no in-battle writer,
`h` is the multiplayer share dialog, and Enter is chat: all out of scope
`[07 §5 "Chat"]` `[07 R-CAM-01 §9]`.

### 3.7 The command dispatch boundary

Every gesture that changes authoritative state becomes one
`session.HumanCommand` and goes through `Session.EnqueueHumanCommand`. The
value is immutable at the boundary: the enqueue copies handle slices and
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

* **The value.** `Scale` is a `float32` with `0` meaning 1. `EffectiveScale`
  clamps it to `[0.25, 4]`. This is one of the presentation-side `float64`/
  `float32` uses the invariant allowlist covers [I2].
* **What it scales.** `EffectiveView` divides the framebuffer view by the scale,
  so zooming in shows less world; `clampInsets` divides the viewport insets by
  it, so the clamp and every recentre stay expressed in world pixels;
  `WorldToScreen` multiplies the projected delta by it and `ScreenToWorld`
  divides, so picking and drawing agree at any zoom.
* **The native fast path is exact.** At scale 1 both conversions take the
  original integer path unchanged, so nothing composed at native scale differs
  by a pixel from a build without the feature.
* **Its writers.** The mouse wheel through `AddZoom`, which steps by a factor of
  1.1 per notch; middle-drag through `Drag`, which converts the screen delta by
  the inverse of the scale before panning; and `--shot-zoom` with `--shot-focus`
  through `SetScaleAbout`. `SetScaleAbout` keeps the world point under a given
  screen position fixed and then clamps.

The next-generation Enhanced camera is planned in DESIGN_GPU_RENDERER §5.2:
dynamic 1×–2× detail with remastered resources, then zoom out to a full-screen
strategic view with player-known unit markers. It must share camera anchoring,
selection/order picking and fog/radar transforms while keeping HUD sizing separate.
This does not alter the existing [F-P1-008] implementation. The current GPU
prototype milestone exposes only `--renderer=modern`; the three-mode runtime
selector and the Enhanced camera are deferred until human review.

### 3.9 Not implemented

* **Chat.** `TALK.GUI` and `TALK2.GUI`, the recipient modes, the `+`-command
  vocabulary and the chat line composer are not implemented. In single player
  retail drops the packet unsent, so the surface has no single-player behaviour
  to clone `[07 §5 "Chat"]` `[07 R-CAM-01 §6]` `[07 R-FE-02 §12]`.
* **The multiplayer lobby shell.** Out of scope for the whole engine; the
  single-player skirmish setup screen is a different surface and is implemented
  `[07 §12]` `[07 R-FE-02 §1]`.
* **The front-end movie stage.** Startup goes straight to `MAINMENU`; there is
  no video decoder and no `CDCHECK` gate `[07 R-FE-01 §3]` `[07 R-CAM-01 §8]`.
* **`SHARE.GUI` and `CONTROL.GUI`.** The resource transfer dialog and the
  host-only player control panel are multiplayer surfaces
  `[07 R-HUD-03 §9]` `[07 R-FE-01 §7]`.
* **Developer mode.** The `\` console, the contour overlay, the in-battle
  screenshot and the Unit Builder Probe are not implemented
  `[07 R-CAM-01 §9]` `[07 R-FE-02 §11]`.
* **Never-opened GUIs.** The windows retail's own code never opens are not
  implemented, and implementing one would be inventing a screen
  `[07 R-FE-01 §12]`.
* **The label painter's quickkey underline and `colorb` fill.** The FNT and
  GAF label paths draw the caption only; the one-pixel underline under a
  label's quickkey letter and the map-entry `colorb` rectangle fill are not
  drawn (every stock label authors `colorb` 0) `[03 R-FONT-01 §6]`.

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

No `TODO(T23)` or `TODO(question)` marker remains in `internal/gui`,
`internal/input`, `internal/camera`, `internal/ui` or `internal/hud`. The open
questions these contracts still carry are these, each with the observation that
would settle it.

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

Retail's exit menu enables the authored inactive `RESTART` control for campaign
and skirmish; that branch remains unimplemented [07 R-FE-01 §7]. The separate
confirmation mismatch is fixed: opening `YESORNO` replaces `EXITMENU`, and
No/Enter/Escape return to the surviving paused options root. Closing that root
resumes the battle. The ordinary footer's sources and priority are also closed
by [07 R-HUD-03 §1]; selection is not a footer source.

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

One `TODO(T25)` remains in `cmd/nanolathe`, on a parity fixture test: the pinned
retail/Nanolathe screenshot pair does not record the scenario that produced it,
so the fixture pins the comparison rather than a reproducible staging.
