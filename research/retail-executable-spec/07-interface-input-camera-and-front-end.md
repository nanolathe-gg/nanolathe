# Retail interface, input, camera, and front-end

This document specifies the retail interface contract from the current
static analysis of the retail executable. The primary
evidence is the retail executable's GUI, HUD, input, camera, and front-end
paths together with TDF/GUI, side-data, and mission data contracts described
in documents 01 and 02.

The terms below distinguish evidence levels:

* **Established fact** is directly supported by current retail decompilation
  or bounded static evidence.
* **Supported inference** combines several direct observations but should be
  kept behind a compatibility seam until verified.
* **Unknown** is deliberately left as a contract gap.

No executable addresses, memory offsets, raw decompiler names, or source-like
decompilation are included. Screen-space dimensions and named data fields are
included only where they are part of the observed interface contract.

## 1. Interface architecture

### Established fact

Retail has two related but distinct interface families:

1. A front-end shell built from `.gui` TDF files, PCX backgrounds, GAF
   artwork, and FNT fonts.
2. An in-battle interface built from side-data, interface GAFs, fixed HUD
   chrome, per-builder build pages, and live simulation values.

Both families use a logical 640 by 480 design space. The active display may
have another size, but GUI layout and battle chrome are authored in the
logical space and then presented to the display surface.

The front-end and battle interface share the following services:

* TDF parsing and named gadget construction.
* Indexed-color surfaces and one shared palette/LUT.
* GAF lookup by root and entry name.
* FNT text measurement and bitmap blitting.
* Mouse position polling and keyboard-state polling.
* Modal-window hit testing and an offscreen-to-window presentation step.

The battle frame is composed into an offscreen surface before the final
window presentation. World, HUD, text, modal overlays, and cursor are
separate conceptual layers even when they share the same indexed surface.

### Supported inference

The UI should expose a small common presentation context while keeping front-
end widgets and in-battle HUD controls separate. Treating every battle panel
as an ordinary `.gui` window would lose side-data anchors, live values, and
hard-coded battle layout behavior.

## 2. Win32 input translation and focus

### Established fact

The retail input layer polls Win32 keyboard state and asynchronous key state,
then converts them to an internal input token. A token of zero means no new
input.
The battle dispatcher consumes one token per input pass and uses separate
modifier queries for held Shift/Ctrl-like states.

Keyboard tokens are queued in a 30-position ring with separate producer and
consumer indices; at most 29 tokens can be pending because one position is
reserved. The window procedure does not inspect repeat count or previous-state;
repeated `WM_KEYDOWN`/`WM_SYSKEYDOWN`/`WM_CHAR` therefore enqueue each time
through the same producer paths. `WM_KEYDOWN` uses the virtual-key translator
in ordinary mode, `WM_SYSKEYDOWN` in system-key mode (where `A..Z` without Ctrl
produce lowercase `a..z`), and `WM_CHAR` enqueues the character value directly.

Held state is queried by `GetAsyncKeyState` with the low toggle bit removed:
`0x20` Space, `0xF4` Left, `0xF5` Up, `0xF6` Right, `0xF7` Down, `0xF9` Shift,
`0xFA` Ctrl, `0xFB` Alt. Special virtual-key translations are exact:
`VK_PAUSE` → `0xF8`, `VK_PRIOR` → `0xF2`, `VK_NEXT` → `0xF3`, `VK_END` → `0xF1`,
`VK_HOME` → `0xF0`, `VK_LEFT` → `0xF4`, `VK_UP` → `0xF5`, `VK_RIGHT` → `0xF6`,
`VK_DOWN` → `0xF7`, `VK_INSERT` → `0xEE`, `VK_DELETE` → `0xEF`; F-keys and
Ctrl composition are table-driven, with `VK_PAUSE` closing physical pause
provenance (pause token `0xF8` toggles the local pause bit and emits packet
`0x19`).

**The OEM aliases and the dispatch table are closed.** The translator first
queries Ctrl (toggle bit masked off) and dispatches through a byte table
indexed by `virtual-key − 0x13` whose values select Pause; PgUp/PgDn/End/Home/
Left/Up/Right/Down; Insert; Delete; F1..F12; or the default path. In ordinary
mode without Ctrl the default path returns zero for every key outside the
handled sets (Space, Enter, Escape, Tab, and friends produce no token through
this translator). Digits `0x30..0x39` pass through as themselves and with Ctrl
map to `0xC4..0xCD`; letters `A..Z` map to lowercase `0x61..0x7A` and with Ctrl
to `0xAA..0xC3`; F1..F12 map to `0xE2..0xED` and with Ctrl to `0xCE..0xD9`. The
OEM punctuation ranges are written as immediate values in the translator, not
copied tables: `0xBA..0xC0` map to `;` `=` `,` `-` `.` `/` `` ` `` (tokens
`0x3B`, `0x3D`, `0x2C`, `0x2D`, `0x2E`, `0x2F`, `0x60`) and `0xDB..0xDE` map to
`[` `\` `]` `'` (tokens `0x5B`, `0x5C`, `0x5D`, `0x27`). System-key mode
(`WM_SYSKEYDOWN`) bypasses the ordinary-mode zero gate so the same mappings
apply with Alt held. Enqueued tokens go through the 30-position ring with the
reserved-slot refusal (producer and consumer both unchanged when the ring is
full).

Mouse position is read from the window system. Focus is checked before edge
scrolling or other world-level pointer behavior. If the game window does not
have focus, edge-scroll behavior is suppressed. A small cursor-warp helper is
used by front-end display transitions and related presentation operations.

**Mouse records are closed.** The window procedure converts pointer messages
into timestamped six-dword input records. Each record carries the X coordinate
(low word of the message position), the Y coordinate (high word), the
message's key-state word, a scaled tick count (`GetTickCount()` multiplied by
the presentation object's time-scale integer divided by 1000), the original
message number, and an explicit double-click field. Left down/up (`0x201`/
`0x202`) and right down/up (`0x204`/`0x205`) produce records with the
double-click field clear; left/right double-clicks (`0x203`/`0x206`) produce
the same record family with the field set. `WM_MOUSEMOVE` (`0x200`) builds the
same six-dword record with the double-click field clear but feeds a separate
motion-state path instead of the button queue. Middle button (`0x207`) and
wheel (`0x20A`) have no dedicated case and fall through to default window
processing.

The button queue is a fixed-capacity ring of 24-byte (six-dword) records held
in the presentation object with separate base, capacity, producer, and
consumer fields. The producer computes its next position first; if that
position equals the consumer index the record is refused and neither index
changes (reserved-slot wraparound; the oldest entry is never overwritten).

**Motion consumers are closed.** The motion path copies the record wholesale
into the presentation object's fixed 24-byte current-pointer slot. Each host
frame the input pass polls the mouse: when the button ring is non-empty it
pops the next button record, otherwise it copies the motion slot; either way
the record lands in the game state's canonical pointer record, which the
pointer update (cursor shape, hover, placement validity), the GUI hit tests,
and the click/drag dispatch consume. The record's message-number field selects
the click (down/up/double) handling in the pointer update.

**Clipboard paste is closed.** Paste tokens `0xBF` and `0xEE`, handled inside
the focused text editor, open the window clipboard and request exactly one
format: `CF_TEXT` (format `1`). No Unicode format is requested and no
registered-format API appears in the reviewed path. Contents are copied
bounded into the editor's fixed 128-byte edit buffer: the buffer is zero-filled
first, the copy length is the smaller of the clipboard allocation size and
`maxchars - 1` (the authored maximum capped at 128), the copy proceeds in dword
then byte steps, and the clipboard is unlocked and closed. After pasting, if
the rendered width exceeds the control width, trailing bytes are removed one
at a time — re-measuring each iteration — until the text fits.

GUI quick keys are stored as character values in gadget data. Front-end
buttons can therefore be activated by their quick key as well as by a mouse
activation. The in-battle input dispatcher maps normalized tokens to speed,
selection, build, order, chat, pause, group, and display commands.

The observed input path has these conceptual phases:

1. Poll keyboard/mouse state.
2. Convert device state to the retail internal token set.
3. Check modal/focus state.
4. Give the active modal GUI first refusal.
5. Dispatch remaining tokens to the battle or front-end state machine.
6. Recompute dirty presentation state and redraw.

### Supported inference

Input tokens should be generated from a compatibility key map rather than
from platform-specific key constants. This permits the same normalized
commands to drive front-end widgets, battle hotkeys, and deterministic command
serialization.

### Unknown

The full Win32/internal key-to-token table beyond the proven held-key and
special-key submaps, all OEM aliases, unsupported-device census, and every
battle/front-end consumer are not established. The translator dispatch table,
the OEM punctuation aliases, the Ctrl composition ranges, and the ordinary-mode
zero gate are established above. The remaining unknown is consumer coverage:
which of the battle/front-end input paths handle which tokens beyond the hotkey
dispatcher's cases. Text-input code page, IME behavior, and keyboard repeat
policy are not established; the mouse motion-record consumers are closed (the
motion record lands in the presentation object's current-pointer slot, polled
by the frame input pass into the canonical pointer record — §2, mouse records). The
mouse button record model (including double-click fields and queue refusal),
middle-button/wheel default processing, drag/double-click capture into
timestamped records, and the `CF_TEXT`-only clipboard contract are established
above.

## 3. Modal windows, focus, and event ownership

### Established fact

The active GUI window has a hit-test/open test used by input and by world
interaction. Dialogs such as message boxes, load/save lists, CD checks,
options, and chat are child/modal windows. When an active modal covers the
pointer, world picking and camera edge behavior are gated out.

The front-end GUI pump processes widget events, updates control state, and
renders the panel. The battle input dispatcher runs before the battle frame
composer and can open a modal GUI, toggle a HUD state, issue a command, alter
game speed, or update selection/control groups.

**Input ordering and GUI consumption are closed.** The host-frame input pass
first peeks special tokens — `0x7E` (a chat-command alias honored when an
alias flag bit is set), `0xE3` (when the ESC-menu bit of the battle-interface
state byte is set), and `0xD6` (screenshot) — polls the mouse, dispatches the
active GUI, polls the mouse again, and only afterwards runs the battle hotkey
dispatcher. An active GUI therefore consumes queued keyboard tokens before
battle hotkeys run; this is an ordering contract, not a claim that every GUI
consumes every input.

Inside the GUI pass, when the top GUI's token-mode field is nonzero the pass
consumes one queued token; when it is zero the pass peeks without consuming
and suppresses tokens `0xE2..0xEB` inclusive for that pass (they are replaced
with zero); other peeked tokens are observed without the initial consumption.

The active GUI is the top object of a linked stack, and its event pass walks
authored gadgets in increasing index order (the declared count is inclusive:
N gadgets admit GADGET0..GADGETN). Hit testing is inclusive on both axes:
`gx <= x <= gx+w-1 && gy <= y <= gy+h-1`. Grayed and hidden gadgets reject
interaction: the hidden flag and the grayed attribute bit are tested before
activation effects. Callbacks and association handling can redirect which
gadget becomes active.

**Top-object close is closed.** Closing the active GUI object invokes its
registered callbacks, redraws under nest-counted cursor/display protection,
removes the object, reactivates the predecessor when one exists (the stack
head is replaced and the predecessor is marked for redraw), and applies one
extra redraw request selected by GUI flag `0x800`.

Escape, Enter, Backspace, and numeric/character input are observed in dialog
and chat paths.

### Supported inference

Event ownership should be hierarchical: modal child, active front-end panel,
battle HUD gadget, then world/camera. This matches the observed modal hit
tests and prevents a click intended for a dialog from selecting a world unit.

### Unknown

The user-facing naming of every GUI mode/flag bit, the complete per-dialog
Escape/Enter/focus-restoration matrix, default-control rules for every panel,
event bubbling between parent and child panels, whether keyboard focus can be
shared by a list and textbox, and complete overlap/capture/association
redirection precedence remain unknown. Two of the flag meanings are
mechanically named: `0x800` requests one extra redraw pass when the window
closes, and `0x1000` centers a modal window in the playfield right of the
128-pixel rail (§11). The per-dialog Escape/Enter/focus defaults are authored
data — each GUI file declares its own `escdefault`, `crdefault`, and
`defaultfocus` controls — so there is no hard-coded matrix to inventory; a
reimplementation must honor the authored fields. Top-object close, predecessor
reactivation, token suppression range, and hit-test bounds are established
above.

## 4. GUI file and widget model

### Established fact

GUI files are TDF panel descriptions. The complete authored grammar — section
naming, the `[COMMON]` subsection, every key, its accessor, its default, and
its stored width — is specified in document 02; this section covers only how
the interface layer uses it.

The observed control kinds are:

* Panel/header.
* Button.
* List box.
* Text box.
* Scrollbar.
* Label.
* Surface/blank panel.
* Font selector.
* Picture-box style control.

**Control-kind mapping is closed.** The parser's control-kind byte selects
among twelve handled cases: background/panel (with -1 centering and the
`BackTile` fallback chain), button (including staged buttons chosen by
best-fit frame size and `|`-separated multi-line labels), listbox, text input
(name capped at 127 bytes), slider (which synthesizes two scrollbar child
gadgets with derived knob travel), a text case, an unnamed zeroing case, two
embedded-file cases, and three further single-purpose cases. Gadgets live in
fixed 347-byte records; each kind resolves its art from its own named GAF
entry first, then the side-specific interface GAF, then the built-in fallback.

**Runtime control-kind dispatch is closed.** The active-GUI pass routes each
gadget's stored control-type byte to distinct runtime families:

| Stored type | Runtime family |
|---:|---|
| `1` | Clickable control path with callback result; can become the active gadget. |
| `2` | Distinct stateful control path. |
| `3` | Focusable text editor: drains queued edit tokens until empty or Escape; cursor movement, insertion, deletion, navigation, and clipboard paste (above). |
| `4` | Dedicated control update path. |
| `5` | Association-capable path that can redirect activation by comparing up to 16 bytes of a gadget name identifier across other gadgets during a fixed-record-stride scan. |
| `6` | Distinct callback-producing path. |
| `12` | Repeating/decrementing path: auto-repeat decrements a counter while a throttle predicate holds. |
| `13` | Timed/range path: animates a range value toward a maximum using per-gadget interval/threshold fields, then fires the path. |

Text-editor admission is bounded by the authored maximum (a stored 16-bit
value capped at 128): printable bytes are admitted up to `maxchars - 1`
subject to the gadget's input-filter attribute bit `0x02`, an
allowed-character-set test with exceptions for space, underscore, and
apostrophe, and the rendered-width rule `currentLen + newLen <= controlWidth
- 4`. Paste copies at most `maxchars - 1` bytes, maintains termination, then
removes trailing bytes until the rendered width fits the control.

Each interface has a name, logical rectangle, default focus, escape/default
actions, declared gadget count, and optional background/panel artwork. Each
control has a name, type, group, rectangle, attributes, text/link data,
font/texture references, quick key, and optional animation stages.

Observed attributes include left/center alignment, radio grouping, build
button behavior, toggle behavior, cycle behavior, disabled/hidden state, and
staged visual selection. Scrollbars can be associated with lists; the engine
updates their range, knob size, and position from the associated list rather
than trusting every authored value.

The GUI loader resolves named GAF entries, fonts, and panel backgrounds. A
missing background may use the known `BackTile`-style fallback; missing
required controls or side-data anchors have different error behavior.

GUI control geometry is interpreted in logical screen coordinates. A control
is hit-tested against its rectangle after runtime window placement. Text and
artwork are drawn at the control location relative to the active window. A
general display-scale conversion contract has not been established.

### Supported inference

The parser should preserve both authored control order and the runtime lookup
order. Retail stores sorted TDF structures in some parser paths, while GUI
behavior can depend on declared focus/group relationships and build-page
ordering.

### Unknown

Field lengths and control-specific defaults are now established and are given
in document 02, as is the mapping from the control-kind number to its
per-type parser. What remains incomplete is some *use* of those fields at
runtime: listbox item-height rules, picture-box binding, and the complete
widget **callback map** (which runtime events each widget receives).
Text-editor admission limits, clipboard paste bounds, and the control-type to
runtime-family dispatch are established above.

## 5. Front-end screen and state families

The following state families are directly evidenced by GUI names, strings,
asset roots, and call paths. A reimplementation should retain explicit state
names rather than treating the front-end as one undifferentiated menu.

### Established fact

**The front-end controller mechanism is closed.** The shell runs as an
explicit controller: a two-level phase byte selects the broad controller
family while separate current and requested substate bytes carry the active
screen; event callbacks write the requested substate and a later controller
pass reconciles it into the current value and performs the transition work.
Screen-family entry points are proven for the main shell;
single-player/campaign/new-game; options; save/load (which opens with pause
set in battle); exit; end mission/game; and multiplayer setup. The
post-battle controller itself is an eight-phase machine whose phases include
CD check, last-frame copy, click-to-continue, statistics animation, and the
GUI pump.

#### Startup and main menu

The main shell provides single player, multiplayer, intro/movie, credits, and
exit actions. It loads the main menu GUI, applies the active font and palette,
and runs the GUI event/presentation loop.

**Main-menu background shimmer (SPARKS) is closed.** Presentation-only
effect active only while `MAINMENU.GUI` is shown. The shell allocates a
100-entry particle buffer tagged `SPARKS` (100 × 13 bytes) on entry, zeroed,
and frees it on exit when the menu window closes. Two window callbacks are
installed for this shell: a destroy handler that frees the buffer and a
per-frontend-frame tick that drives the shimmer. The tick is invoked through
the frontend window pump (the same pump that services input polling and
`GUI` drawing) once per frontend frame with no internal wall-clock throttle;
presentation is via the shared indexed offscreen and palette present path
(see document 03 §1). All draws use the CRT `rand()` stream only, never the
simulation RNG, and no authoritative state is touched.

Each record is 13 bytes (stable iteration `0..99`, stride 13, no map
iteration; record count and size are exact because `100 × 13` matches the
allocation):

| Off | Size | Field |
|---|---|---|
| `+0` | `int16` | x 0..639 |
| `+2` | `int16` | y 0..479 |
| `+4` | `uint8` | active 0/1 |
| `+5` | `int8` | dx (-3, 0, 3) |
| `+6` | `int8` | dy (ditto, exactly one of `dx/dy` zero at any time) |
| `+7` | `uint8` | life countdown, decremented each active frame |
| `+8` | `uint8` | twinkle/direction timer |
| `+9` | `uint32` | linear framebuffer offset `y*640+x` |

The framebuffer is the logical 640×480 indexed surface (pitch 640, `off =
y*640+x`). Two surfaces are used each tick: a background backup (the static
menu art as captured when the offscreen was selected) as source and the
onscreen offscreen as dest. All draws are single indexed bytes to the dest;
no `ALP`/`SHD` blend participates.

Per record per frontend frame, in order `0..99`:

1. If active, restore the previous pixel from the backup to the dest at the
stored offset before any other test. This erases the prior frame's spark.

2. If inactive, attempt to spawn: pick `x = rand() % 640` and
`y = rand() % 220` (220, not 480 — spawn band is the upper portion of the
menu), form `off = y*640+x`, and test the dest pixel's low nibble
`pixel & 0xF`. If `<= 0xC` (≤12) remain inactive; only bright background
(`>= 0xD`/13) may spawn — dark menu bar areas never sparkle. On success set
`active=1`, `off`, `life = (rand() low byte)+1` wrapping (0 allowed, dies
next frame with probability 1/256), `timer = (rand() & 0x1F)+1` (1..32), and
an initial orthogonal direction `±3` chosen by parity of `life` and the
spawn position (if `life` odd the choice branches on `y` parity, otherwise on
`x` parity; exactly one axis is zero). No pixel is drawn in the spawn frame;
the spark becomes visible on its next active pass.

3. If active after erase: decrement `life` — if already zero deactivate
(the particle lives for `life` additional frames after spawn); advance
`x += dx, y += dy` with signed 8-bit steps; if `x<0 or >=640 or y<0 or
>=480` deactivate; integrate `off += dx` and if `dy != 0` add `dy*640`; test
the dest pixel at the new offset `& 0xF >= 0xD` else deactivate (sparks that
wander onto dark art die); otherwise write palette index `0xAA` (170) at
`off` as the sparkle.

4. Twinkle timer: if `timer != 0` then `--timer` and go to the next record.
If zero, pick a new orthogonal direction `±3` by parity of the current
coordinate (`x & 1` when previously moving horizontally, `y & 1` when
vertically; the other axis zeroed) and reset `timer = (rand() & 0xF)+1`
(1..16). The just-drawn `0xAA` remains for this frame.

After all 100 records the tick marks the menu window dirty so the next
present copies the modified offscreen to the display.

The shimmer is therefore presentation-only. A reimplementation must isolate
it to a CRT-style `rand()` stream (do not consume the simulation RNG), use
pitch 640, threshold low-nibble `>= 0xD` against the *background* (not the
previously drawn spark), orthogonal steps `±3` with exactly one zero axis,
life/timer ranges as above, and must restore the background byte before each
move. Spawning over dark `MAINMENU` art (low nibble `<= 0xC`) must remain
suppressed — retail never sparkles over the grey menu bar.

This section absorbs the earlier standalone menu-spark note in full; that note
added no behavior beyond the above — its buffer size,
pitch-640 offset, `0xAA` sparkle index, CRT-only `rand()` stream, spawn band
and low-nibble threshold, life/timer ranges, and parity-driven orthogonal
steps are already stated here.

#### Single-player and campaign

The single-player family includes campaign selection, arbitrary mission
selection, campaign side/difficulty selection, mission lists, briefing,
briefing continuation, help, and mission start. Campaign and mission lists
are populated from `camps\\*.tdf` and mission `MISSION` records, not hard-coded
unit lists.

Briefing screens consume mission description, planet, briefing, narration,
hint, glamour, and rotation/panorama data. The planet value selects a set of
named text and GAF resources.

**Planet vocabulary is closed.** The `Planet` OTA key selects from a
fifteen-value vocabulary that drives three parallel fixed tables consumed
together: planet names map to briefing keys (`Greenbrief`, `Archibrief`,
`WDesertbrief`, `Desertbrief`, `Lavabrief`, `Marsbrief`, `Lunarbrief`,
`Metalbrief`, `Lunar2brief`, `Icebrief`, `Lushbrief`, `Slatebrief`,
`Waterbrief`, `Acidbrief`, `Crystalbrief`), to panorama images
(`GreenPan…CrystPan`), and to rotation animations (`GreenRotate…CrystalRotate`);
the fetched artwork pokes the `PANORAMA` and `PLANET` GUI controls. When a
Lunar override flag is set, the briefing name is rewritten through the
`Lunar`/`Lunar2` string constants. Per-mission wind variance is seeded at
briefing entry as `rand() % (maxWind-minWind+1) + minWind` with a second
`rand() & 0x3F` draw. The briefing flow has explicit previous,
continue/start, more-text, and text-region states.

#### Skirmish

The skirmish family includes player/side selection, map selection, map view,
alliances, visual settings, game speed, restrictions, resource sharing, and
chat selection. The lobby displays up to ten player rows with side, color,
team/ally, ready, map, resource, and status information.

### Retail closure for the single-player menu slice

The implemented single-player path is bound to the retail resources and
callbacks below. These are not replacement layouts or a map-first skirmish
wizard:

| State | Retail GUI | Retail background/art | Entry/callback behavior |
|---|---|---|---|
| Main | `mainmenu.gui` | `frontendx.pcx`, `mainmenu.gaf`, `commongui.gaf` | `SINGLE` opens `single.gui`; `EXIT` enters the frontend close state |
| Single-player chooser | `single.gui` | `singlebg.pcx`, `single.gaf`, `commongui.gaf` | `NewCamp` opens `newgame.gui`; `Skirmish` opens `skirmish.gui` directly |
| Campaign/mission | `newgame.gui` | `newcampaign4.pcx` or `newcampaign4x.pcx`, `newgame.gaf` | Campaign and mission list gadgets are populated from discovered `camps` data; `Side0/Side1`, `Difficulty`, `Start`, and `PrevMenu` retain their authored callbacks |
| Map selection | `selmap.gui` | `dselectmap2.pcx`, `commongui.gaf`, plus the TNT minimap surface | The map-selection callback opens the window through the window-open routine with flags `0x980`, then hands `DSELECTMAP2` to the bitmap cache, which installs it on the open window through the bitmap-install path. The authored `494×420` record at origin `(84,12)` is a panel window composed over the screen it was reached from; `MAPNAMES`, `SLIDER`, `MAPPIC`, `DESCRIPTION`, `SIZE`, `LOAD`, and `PREVMENU` remain window-local records placed at that origin |
| Skirmish setup | `skirmish.gui` | `skirmsetup4x.pcx`, `skirmish.gaf`, `commongui.gaf`, `textures/logos.gaf` | `Player%d`, `Side%d`, `Color%d`, `Allies%d`, `Metal%d`, and `Energy%d` are appended by the runtime builder; their row geometry is `step=200/n`, `y=(180-(n-1)*step)/2+79` |

`selectgame2x.pcx` is not part of this path: the multiplayer `SELGAME.GUI`
lobby path loads it (verified: the selmap callback fetches `DSELECTMAP2` from
the bitmap cache, and the SELGAME opener fetches `selectgame2x`), and that lobby is out of scope for the single-player
slice. `dselectmap2.pcx` is a `640×480` file whose panel art occupies only the
top-left `494×420`, which is exactly the `selmap.gui` window rectangle: the
window fill copies the bitmap into the window's own surface at `(0,0)`, so the
art lands at the window origin and the rest of the file is outside the window
and never presented.

#### Frontend asset failure boundaries

The single-player screen openers use one common `.GUI` window-open path and do
not test the returned window before installing callbacks or reading its gadget
list. A missing GUI file therefore does not select a second authored GUI, and
a parser rejection does not enter a caller-level recovery branch. The exact
process-level symptom of either case is not established; in particular, this
is not evidence that the screen proceeds with an empty layout. **Unknown** for
the final missing/malformed-`.GUI` outcome; the absence of a caller check is
**Established**. [07 §4]

The single-player PCX backgrounds in this table — `frontendx`, `singlebg`,
the `newcampaign4`/`newcampaign4x` choice, `dselectmap2`, and `skirmsetup4x` —
go through the shared bitmap loader. A missing file or a decode failure enters
the common fatal content diagnostic and terminates the process; no `BackTile`,
other screen, or caller-level error transition is selected. The same contract
applies to the `loadgame2bg` background in the loading-screen transition. The
bitmap path is therefore distinct from the panel-art `BackTile` fallback below.
**Established; high confidence.** [01 "Error and diagnostics"] [02 §6]

GUI-attached GAF roots are loaded by a separate optional binding path. If a
screen root such as `mainmenu.gaf`, `single.gaf`, `newgame.gaf`, `skirmish.gaf`,
or `commongui.gaf` is absent, its handle remains null; a decode failure is
stored as the same null result rather than sent through the common fatal GAF
loader. Named controls then try their authored support-root and stock fallback
entries (including `BackTile` for panel fills and `BUTTONS0`/staged button
families). When no entry is found, the control remains present with no art and
the surface renderer uses its background fill. **Established for the shown
lookup paths; medium confidence for malformed files whose decoder does not
return null.** [07 §4] [07 §5]

`textures/logos.gaf` is a texture-set resource rather than a required GUI root.
The skirmish `Color%d` surface uses its selected frame when present; with no
resolved frame, the generic no-entry surface path supplies the context
background rather than a screen-level diagnostic. **Established for the
no-entry path; medium confidence for all malformed texture payloads.** [07 §5]

The physical palette loader first requests `palettes/PALETTE.PAL`. If that file
is absent, it explicitly tries the authored `palettes/PALETTE.PCX` fallback. If
the selected PAL file is present but cannot be decoded, the PCX fallback is not
attempted; a failure of either selected input reaches the common fatal content
diagnostic and terminates the process. **Established; high confidence.**
[01 "Error and diagnostics"] [03 §4.3] [fmt pal] [fmt pcx]

Startup preloads `fonts/COMIX.FNT` (and the separate `SMLFONT.FNT` slot) and
checks the decoder result. A missing file or decoder-rejected input therefore
uses the same fatal diagnostic path; there is no authored font fallback for
COMIX. The two HATTFONT GAF slots are different: a missing HATTFONT file is
accepted as a null slot, and GUI text falls back to the active FNT (COMIX in
the frontend context). A malformed HATTFONT whose parser returns null is not
cleanly handled by the loader's subsequent metric read, so its exact outcome
remains **Unknown** rather than being specified as either a fallback or a
clean abort. **Established for missing COMIX and missing HATTFONT; high
confidence.** [02 §6] [07 §4]

The skirmish callback dispatcher identifies a row before it identifies a
control. It copies the activated gadget's name, converts its last
character to an integer and stores that as the current row in the session's
game-data record, then truncates the digit and compares the remainder against
the bare control names `Start`, `PrevMenu`, `Player`, `Side`, `Allies`,
`Color`, `Metal`, and `Energy`. `Allies3` is therefore row 3 plus control `Allies`, and no callback
parses the row out of the name a second time.

The `Allies` callback advances `players[row].allyGroup` by one modulo six and
then calls the alliance-icon refresher, which rewrites the frame index of
*every* row's `Allies%d` surface — the alliance number is never itself the
frame index. For each row the refresher counts the configured rows, meaning
controller not `Open`, whose alliance number equals that row's, then selects the `TEAMICONSx`
frame: a count of zero gives frame `10`, exactly one gives `group*2 + 1`, and
two or more give `group*2` (re-verified: the refresher counts the matching
configured rows and writes the frame index into the row's surface field with
exactly that three-way branch). The entry's twelve frames are six symbols in that
order, the odd frame of each pair split in half and the even frame whole, so a
row alone in its alliance shows the broken symbol and a row sharing it shows
the joined one. Alliance `5`, the unassigned sentinel of
[08 "Skirmish configuration"], maps to frames `10` and `11`, which are both
blank — that is what makes it read as "no allegiance" rather than a sixth
symbol. The count is over the alliance and not over the row being drawn, so an
`Open` row whose alliance a live row shares still resolves to that alliance's
symbol; its surface is hidden, so this is never on screen.

The skirmish row controller values are numeric and distinct from the session
API's compatibility mapping: `0` is `Open`, `1` is `Player`, and `2` is
`Computer`. The row initializer makes slot 0 `Player` and ally group 2 when
every controller row is zero. The row controller cycles `0→2`, `1→0`, and
`2→1` only when no other row is `1`, otherwise `2→0`. An open row keeps only
`Player%d` visible; side, ally, resource, and color gadgets are hidden. The
stock dynamic art is `skirmname`, `SIDEx`, `32xlogos`, `TEAMICONSx`, and
`skirmmet` (both resource controls select its frame 0; its frame 1 is the
armed/pressed state), with the runtime help strings authored by the
executable. There is no authored opponent-count, round-settings, or map-first
control in `skirmish.gui`; the row count comes from the `NumSkirmishPlayers`
registry value and the remaining setup values are the authored staged gadgets.
[08 "Skirmish configuration"]

GAF rendering uses each selected frame's authored dimensions at the `.GUI`
control origin; `XOffset/YOffset` remain animation-anchor metadata and are not
added to ordinary frontend gadget placement. Retail's GUI initializer replaces
button runtime width and height with the chosen stock/owned frame dimensions.
The indexed output surface uses the shared `PALETTE.PAL` display table; GUI
semantic color fields are translated into active indices before primitive/FNT
writes, while GAF image and GAF-font bytes are copied directly.

Frontend initialization installs `anims/hattfont12.gaf` as GAF-font slot 0 and
`anims/hattfont11.gaf` as slot 1. The GUI text routine prefers slot 0; only a
null GAF-font slot falls back to the active FNT. At GAF-font load, the
font-loader routine finds the capital-I frame — the hattfont frames are
indexed by character, so this is the frame at the ASCII position of `I` — and
subtracts its height from
every frame's runtime `YOffset` (re-verified: the loader reads the `I` frame's
height and subtracts it from every frame's stored YOffset in one pass). The
glyph blitter then places a frame at
`penX-XOffset, penY-normalizedYOffset`; the text loop passes the pen directly
and does not pre-add either offset. Consequently, stock `hattfont12` glyphs
whose raw `YOffset` is 11 rasterize one pixel below their pen because the
capital-I height is 12. TODO(question): the exact text-pen arithmetic
`y + trunc((height - 1 - fontHeight) / 2) + (stages != 0)` with the capital-I
height plus two as the metric is not re-verified; the pen site that applies it
was not located in this pass. The startup context also selects `fonts/COMIX.FNT`
(the active slot of the font registry, chosen by the font-selection routine)
for the fallback path; `SMLFONT.FNT` is a separate preloaded font slot. [07 §4]

### Retail palette contract

The retail renderer has one active 256-entry display palette for the indexed
surface: `PALETTE.PAL`. Frontend backgrounds, GAF widgets, FNT glyphs, HUD
art, terrain, minimap pixels, and direct indexed primitives are eventually
presented through that table and the current 256-byte logical-to-physical
lookup. `GUIPAL.PAL` is a frontend semantic color-field palette retained by
the GUI bootstrap;
it is not installed as a second physical display palette. This distinction is
material because the two retail files have different color ordering.

For frontend GUI color fields, the bootstrap copies the 256 four-byte
`GUIPAL.PAL` entries into the window's GUI palette record. It then builds the
window's semantic color lookup by comparing each GUI entry's RGB triple with
all 256 `PALETTE.PAL` entries using the sum of absolute per-channel
differences. The lowest-distance destination wins; a tie keeps the lowest
destination index. The GUI draw routine uses this lookup for color fields
before it stores primitive colors or FNT foreground/background values in the
indexed surface. The GAF blitter instead copies opaque frame bytes directly through the frame
decode-and-copy path, and PCX backgrounds/TNT minimap pixels also remain direct
active palette indices. No frontend path substitutes
`GUIPAL.PAL` or a PCX trailer palette for the active `PALETTE.PAL` table, and
no GUI lookup is performed again during indexed-to-RGB presentation. [03 §4.3]
[fmt pal] [fmt pcx] [fmt gaf]

### Retail frontend control activation and raster rules

The following rules are established for the frontend controls in this slice:

* The panel background is drawn first. Controls are then visited in authored
  order, with the panel record itself skipped. Hidden and grayed controls do
  not receive focus or callbacks. A control's screen rectangle is its local
  `.GUI` rectangle translated by the panel header origin; hit testing includes
  every integer pixel from the origin through `origin + size - 1`.
* An ordinary clickable control arms on a left-button press inside its
  rectangle. Its pressed frame is shown only while the left button remains
  held and the pointer remains inside. Releasing outside clears the armed
  state without invoking the callback; releasing inside invokes the callback
  once. Focus is assigned on the press edge, before a later keyboard
  activation. Quick keys and the panel's default/escape controls enter the
  same callback path without a mouse rectangle.
* Button art is selected from the named panel GAF first and the common
  `BUTTONS0` fallback otherwise. `BUTTONS0` is grouped as four frames per
  stock size: normal, armed, disabled, and spare. For a staged entry the
  current status selects the stage frame and a held press selects the
  penultimate frame. The text-pen routine computes the text pen Y as
  `y + trunc((height - 1 - fontHeight) / 2) + (stages != 0)`, where the GAF-font
  metric is capital-I height plus two. The added pixel belongs to staged
  controls, not the held/pressed state; pressing changes the selected art but
  does not move the text pen. The authored alignment bits select a three-pixel
  left inset, a three-pixel right inset, or centered text, in that priority
  order. Text is measured and clipped through the selected GAF font, with the
  active FNT used only when the GAF-font slot is null; it is never drawn with a
  platform font.

The stock common frame dimensions used by these controls are stable in the
retail resource set: `BUTTONS0` groups are `16×16`, `321×20`, `120×20`,
`96×20`, `112×20`, `80×20`, and `96×31`, each repeated as a four-frame group;
`LISTBOX` has nine `16×16` tiles; and `SLIDERS` has ten vertical frames and ten
horizontal frames. The staged common entries used by this menu are
`stagebuttn2` (five `120×20` frames) and `stagebuttn3` (six `120×20` frames).

* Surface controls are not drawn by the button/label renderer. The surface
  renderer reads the surface's own GAF entry and frame index, then branches on
  the frame's `Compressed` byte: an RLE frame is stamped once at the gadget origin,
  while a raw frame is texture-mapped through the four-corner blitter with the
  destination spanning `(x, y)..(x+w-1, y+h-1)` and the source spanning
  `(0, 0)..(frameW-1, frameH-1)` — that is, resampled onto the authored gadget
  rectangle. Skirmish's `Color%d` is the visible case: `textures/logos.gaf`
  holds raw `32×32` frames that retail resamples into the authored `20×20`
  record, while `anims/skirmish.gaf`'s RLE `TEAMICONSx` icons are stamped 1:1.
  A surface with no entry and no raw bitmap is filled with the context's
  background color index. [fmt gaf]
* A window is opened by the window-open routine, which pushes it in front of
  the window already open and allocates a `SAVE UNDER` surface for it, so the
  chain is composed back to front and a panel window leaves the screen beneath
  it visible around its edges. A companion routine then gives the window a
  drawing surface of exactly its own width and height, positioned at the window
  origin,
  and copies the current screen into it before anything is painted; the
  `SAVE UNDER` copy is what is put back when the window closes. Everything the
  window draws is therefore confined to its rectangle, and the four full-screen
  frontend screens cover the display only because they are authored
  `(0,0,640,480)`. If the window has a background bitmap it is copied into that
  surface at `(0,0)` — the window origin on screen — and clipped to the
  rectangle. Without one the fill is the tile-fill routine: it tiles the
  window's art entry — the stock fallback entry is `BackTile`, whose frame
  offsets the initializer zeroes — across the window rectangle, then draws a
  two-pixel raised bevel through the bevel routine `(surface, rect, c1, c2,
  c3)`. The three colors are the GUI context's semantic color fields `0`,
  `17` and `20`, resolved through the nearest-color map the bootstrap builds.
  The first four edge runs use color `17` and the next use color `0`; where
  color `20` is used is `TODO(T23)`. Flag `0x80`, which `selmap.gui` passes,
  suppresses that fill for the open pass, so a window that loads its bitmap
  after opening shows the saved screen until its first repaint.
  Child gadget records are shifted by the window origin at open time, unless
  the open window contains a `PANEL` gadget, in which case the new window's
  gadgets are centered inside that rectangle instead.
* A gadget is resolved by name with a forward scan that stops at the first
  match. When a `.GUI` authors several gadgets under one name
  — `skirmish.gui` has five separate `TEXT` labels — only the first is ever the
  target of a by-name text, status, or activation set; the rest keep the values
  in their own records.
* List controls retain a selected item and a top visible item. A row hit is
  resolved against the list's two-pixel inner origin and the runtime font
  height. The row pitch is the authored `itemheight` when it is non-zero and
  the GAF-font metric plus one otherwise; the row-pitch initializer raises a
  zero authored value to that same default. The selected row is not painted
  with art: the row renderer draws the row's text first and then hands the row
  rectangle to the light-level remapper at level `+30`. A non-negative level
  there indexes the 32-row
  `PALETTE.LHT` brightening table and a negative one the `PALETTE.SHD`
  darkening table (`level+32` selects the row, clamped to `-32..31`), and the
  operator remaps the pixels already in the rectangle, so the selected entry
  reads as a lit bar carrying lifted glyphs rather than a painted block.
  Associated scrollbars use the common `SLIDERS` entry: the vertical
  family is frames `0..9` and the horizontal family is `10..19`; each family
  has three track pieces, three thumb pieces, and two normal/armed arrow
  pairs. The thumb pieces are a one-pixel cap, a repeatable three-pixel middle
  and a one-pixel cap, so the knob is a computed run and never the sum of those
  frames: the thumb-length routine stores its length as
  `round(visibleRows / itemCount * (barLength - 3))`, clamped up to ten pixels.
  An arrow changes the associated list by one row. A left press inside
  the computed thumb captures the pointer and maps held pointer displacement
  through the thumb travel/range, truncating integer division toward zero.
  A click on the track beside the thumb does not invent a page step.
* Frontend GAF frame offsets are animation-anchor metadata. The GUI blitter
  places the frame's indexed pixels at the translated gadget origin without
  adding `XOffset` or `YOffset`. The selected frame dimensions become the
  runtime button dimensions used by both drawing and hit testing. GAF and FNT
  output uses active `PALETTE.PAL` indices: GAF bytes are copied directly,
  while FNT receives the active index produced by the GUI semantic color map
  before rasterization. PCX backgrounds and TNT minimap pixels use their
  active `PALETTE.PAL` indices directly.

Campaign and map controls also retain data-driven display behavior. The
campaign list is rebuilt from the discovered campaign documents and filters
the `HEADER campaignside` value to the selected side, accepting `ALL`. For
New Campaign, when the installed campaign set has two or fewer entries, the
campaign and mission list controls are hidden and the side-specific
`Arm Campaign` or `Core Campaign` file is selected directly. Play Any uses the
same `newgame.gui` but exposes the campaign and mission lists and applies the
retail compressed list rectangles at runtime.

The `MAPNAMES` items are the OTA file stems the map-census routine collects
from the `Maps\*.ota` census, kept with the case the archive records; the
language-prefixed accessor replaces a stem only when it returns something
different. No map file is opened to build the list — only the selected map is
read, by the map-OTA reader — and the packed list is sorted before it is
installed: the map-selection callback passes it through the `SORTED LIST1`
sort routine, a bubble sort whose comparison is the ASCII-only case-insensitive
string compare (the same comparator the order-descriptor sort and lookup use,
and proven case-insensitive because the order-kind switch names such as
`VTOL_MOVE` match descriptor records named `VTOL_Move`), so
the list is ascending and case-insensitive rather than archive order. The
map-description filler fills the two text controls from the selected map's
OTA: `DESCRIPTION` receives
`missiondescription` verbatim (read by the map-OTA reader into a bounded buffer
with a `No description available` fallback) — the authored string already
carries its own
`16 X 17 ` size prefix — and `SIZE` is formatted `"%s  %s: %s"` from `memory`,
the localized `Players` label, and `numplayers`, giving `16 mb  Players: 2, 4, 8`.
The wrapping decision compares the label's inclusive height against twice the
capital-I metric plus two and sends the taller case to the wrapping renderer;
`selmap.gui` authors `DESCRIPTION` `235×31` for the wrapped case
and `SIZE` `235×18` for the single-line one. The map preview preserves the
selected TNT radar image's aspect ratio inside the authored surface. If the map
census is empty, the map-selection callback uses the stock message
`There are no multiplayer maps to choose from`.

The main-menu `EXIT` callback is a direct close transition. The separate
`YESORNO.GUI` text `Close Windows CD Player?` belongs to frontend
initialization cleanup, not to the main-menu quit button. [07 §5]

#### The loading screen

Leaving the frontend for a battle goes through the loading-screen
transition, which is the loading screen and is not a `.GUI` window at all. It
closes the open GUI windows, forces the display to `640x480`, reloads
`palettes/guipal.pal` into the GUI context's semantic color map, and hands
`DSELECTMAP2`'s sibling `loadgame2bg` to the bitmap cache with no window open,
so `bitmaps/loadgame2bg.pcx` becomes the global background rather than a
window's. It then starts the loader on its own thread — the thread-create call
with the load-thread entry point, which runs the battle-entry load routine and
fails with `Unable to start the loading thread!` — and repaints the screen
while that thread works, sleeping 200 ms at the end of every repaint so the
loader keeps the machine.

The repainted composition, in the order it is drawn:

* The background blitted whole at `(0,0)`.
* The map line, before any row. It is
  `sprintf("%s: %s", "Map", mapName)` through the localized string table,
  centered on the surface width and with its pen at
  `trunc(surfaceHeight - fontHeight * 1.5)`, drawn in GUI color 15. The
  loading-screen routine gates it on the mission type being other than
  1, so a campaign mission draws no map line and a skirmish does. The name is
  the map's list entry, uppercased first on a non-English install.
* Six progress rows, each drawn label first, then fill, then grille. The
  labels are `Textures`, `Terrain`, `Units`,
  `Animation`, `3D Data`, and `Explosions`, drawn at `x=0x5a` with their `y`
  authored one per row rather than stepped: `0x87`, `0xb1`, `0xda`, `0x106`,
  `0x130`, `0x15b`. Each row's percentage is a byte, `0..100`, stored in six
  consecutive per-row percentage bytes, written by six separate loaders inside
  the load thread.
* Each row's bar is a solid rectangle through the bar-fill routine, spanning
  `(0xcd, y)` to `(0xcd + percent*7/2, y+20)` inclusive, so a finished bar is
  351 pixels wide. The common `LIGHTBAR` entry's frame 0 is then stamped over
  it at `(0xcd, y)` with its own offsets zeroed. That frame is a 351x21 metal
  grille whose slots are transparent, so the fill shows through as lit
  segments rather than as a plain bar.
* The fill color is the GUI semantic color the row's state selects: index 12
  while the row is under 100 and index 10 once it reaches 100. These are the
  GUI context's semantic color fields `12` and `10` — resolved through the
  nearest-color map the bootstrap builds — so they are ordinary GUI color
  fields, not raw palette indices.
  The same color is handed to the text state through the text-state setter
  before the row's label is drawn, so the label and its bar always agree.
* A completion flash. When a row's percentage byte first reads 100 and the
  remembered value from the previous repaint did not, its counter is set to
  30; every repaint drops each non-zero counter by two. The counter is passed
  as the text renderer's sixth argument, which routes each glyph to the
  brightened-glyph blitter instead of the plain glyph blitter; the brightened
  blitter indexes the same `PALETTE.LHT` brightening table the light-level
  remapper uses for a non-negative level, so the label is lifted through
  `PALETTE.LHT` and fades back over
  fifteen repaints — three seconds at the screen's own cadence.

`LIGHTBAR` frame 0 carries authored offsets of `(19, -27)`, and the GUI frame
blitter subtracts a frame's offsets from the pen it is given, so the
loading-screen routine zeroes both before stamping it; without that the grille
would miss its bar. The `3D Data` row stamps the grille twice, the second time
at `rect + (frame.XPos, frame.YPos)`, which those zeroes have already made the
same place — a redundant repaint with no visible effect.

The load thread itself is the battle entry: it seeds the
performance-counter RNG, resolves the mission or skirmish schema, places
commanders, and finishes by opening `MAIN2.GUI`, the in-game HUD — the
`<side>main2.gui` window whose name is stored as the battle root's command-
window name (see §6). The transition that starts the thread also fixes the
battle viewport rectangle to `(0, 32, W-1, H-33)` and initializes the
player-slot ready table; the map line, the mission-type-1 gate, the row colors,
and the thread entry are all verified above.

#### Multiplayer

The multiplayer family includes new-game setup, lounge/battleroom, second
lounge state, TCP/modem/serial setup, new-game/options panels, connecting and
timeout messages, chat, map selection, and game options. DirectPlay lobby and
direct-connection setup are separate session paths.

#### Options and display

Options include visual detail, scroll/speed controls, gamma, sound/music,
volume, CD mode, controls, and runtime display preferences. The exact option
panels are GUI-driven but some settings update global runtime state directly.

#### Load/save and dialogs

Load/save screens enumerate save slots and use list/scrollbar controls.
Message-box, yes/no, CD-check, restart, exit, and generic status dialogs use
the same GUI/widget machinery and indexed presentation surface.

#### Endgame and score

End-mission, end-multiplayer, report, score, credits, victory, defeat, and
outcome screens are distinct states. Endgame displays player statistics,
resource/economy results, outcome text/art, and a continue/close flow.

#### Chat

**The chat contract is closed.** Character token `0x0D` (Enter) opens chat
through the battle hotkey dispatcher and plays the `SmallButton` cue. The
dialog is `TALK.GUI`, except that the expanded `TALK2.GUI` form opens when a
persistent expansion-flag byte has bit `0x01` set and the mission-mode word
equals `3` (battle); the expanded form opens with its placement parameter
reduced by `0x80` (`0x800` instead of `0x880`). Opening installs the dialog
callback, sets chat-active state bit `0x04` in the battle-interface state
byte, binds the persistent text storage into the `TALK` control, moves
keyboard focus there, configures `SENDTO`, and in the expanded battle form
also configures `SENDTYPE`; in non-battle modes `SENDTO` is hidden. The first
open zero-fills the persistent storage exactly once; later opens reuse
whatever text survived close/reopen.

Recipient selection has exactly four modes driven by a persistent recipient-
mode byte: `0` enables a row for every eligible other player (all), `1` uses
the local player's per-peer relation byte directly (allies), `2` uses its
logical inverse — enabled when the relation byte is zero (enemies) — and `3`
uses persistent per-player custom-recipient bytes written by the chat
`+digit` mini-language. Row building walks all ten fixed-size player-slot
records in slot order, skipping empty slots (slot type `0x00`), blocked slots
(`0x04`), and the local player's own slot. Activating `SENDTYPE` cycles the
mode byte through `0..3` (values of 4 or more wrap to 0) and refreshes the
rows; activating `SENDTO` toggles word-flag bit `0x0100`.

Ordinary commits stage the text through a 79-byte-bounded copy and send it
with ownership/routing bit value `4`; the message dispatcher selects handlers
by masking its command-table entries against that route word. If the first
non-space byte of the committed text is `+`, the remainder enters the command
path instead of chat display, routed with bits `1`, or `7` when the chat-
alias flag byte has bit `1` set, additionally OR'd with `2` when a referenced
dword is nonzero. The `+` command vocabulary is closed: the message-builder's
dispatch table registers exactly three commands — `plan`, `weight`, and
`limit` — each with route mask 8 that no chat route (1/7/2) matches, and the
default-handler slot is never installed, so the dispatch never consumes a chat
`+` message. The chat commit callback itself handles the mini-language inline:
`+<digit>` (occupied slot) sets the per-player custom-recipient byte for that
digit, `+a`/`+e` (case-insensitive) set the recipient-mode byte to allies or
enemies with the matching label, and any other `+...` sends the whole text as
plain chat.

Activating the `TALK` control commits: the callback closes the dialog
(clearing chat-active bit `0x04` when the gadget association is generic),
re-focuses, and clears the 129-byte persistent storage (32 dwords plus one
terminator byte) plus the `SENDTO` toggle bit on every exit path. While
`TALK.GUI` is present, held-arrow camera movement is suppressed; pointer-edge
scrolling is never suppressed by chat.

### Supported inference

Front-end state transitions should be represented as explicit named states
with modal substate, not only as a stack of filenames. This is required for
campaign briefing progression, multiplayer lobby readiness, load/save
validation, and endgame continuation.

### Unknown

The exact numeric transition-graph edges for every screen family
(successor/cancel/error), movie/intro state machine, credits timing, campaign
transition rules, load-failure restoration, all error dialogs, and front-end
persistence after aborted transitions remain incomplete. The controller
phase/substate mechanism, post-battle phase machine, planet-driven briefing
tables, and chat contract are established above.

## 6. Battle HUD and side-data interface

### Established fact

Side-data selects the battle interface root and palette colors. The HUD is
composed from named side assets and fixed semantic regions:

* Top resource/time strip.
* Side rail containing minimap, commands, and build controls.
* Bottom unit/status/chat strip.
* Unit information, damage, queue, reload, logo, mission, and production
  labels.

The battle frame composer renders terrain and world objects, then interface
chrome, status/resource values, selection/health/queue indicators, fog and
modal overlays, text, and the cursor. The pause, victory, and defeat title
images come from named GAF entries. The hourglass and queued-command/path
icons also come from cached cursor/interface GAF roots.

The HUD uses side-specific fonts and energy/metal color indices. Missing
mandatory side anchors are an error; the loader does not invent anchor
positions from a neighboring side.

The loader enumerates contiguous `SIDE%d` sections and stops at the first gap.
For each invoked anchor it performs the same required lookup; a missing lookup
enters the same diagnostic path. All four values are read in the order `x1`,
`y1`, `x2`, `y2` and are preserved verbatim as authored corners, including cases
where `x2 < x1` or `y2 < y1`. The loader does not normalize them to width/height.

The complete invoked required anchor list is: `LOGO`, `ENERGYBAR`,
`ENERGYNUM`, `METALBAR`, `METALNUM`, `TOTALUNITS`, `TOTALTIME`, `ENERGY0`,
`METAL0`, `ENERGYMAX`, `METALMAX`, `ENERGYPRODUCED`, `ENERGYCONSUMED`,
`METALPRODUCED`, `METALCONSUMED`, `LOGO2`, `UNITNAME`, `DAMAGEBAR`,
`UNITMETALMAKE`, `UNITMETALUSE`, `UNITENERGYMAKE`, `UNITENERGYUSE`,
`MISSIONTEXT`, `UNITNAME2`, `DAMAGEBAR2`, `NAME`, `DESCRIPTION`, and `RELOAD1`
through `RELOAD3`.

**Panel slide.** On entering battle, a flip surface is allocated at the
negotiated video-mode dimensions with the static `PANEL` backdrop blitted
into it, together with a cleared 300×480 backup scratch strip whose strip
header words are saved; the in-game gadget tree is enabled, and the command
panel starts visible when a session mode byte has bit `0x04` set. The side
rail slides with an exact animation contract: the panel owns a signed pixel
offset advanced on a 15-millisecond wall-clock throttle (a step whose
timestamp is early is skipped); each accepted step eases by remaining-
distance/3 with a minimum step of one pixel in both directions so it always
converges; detents are -31 (parked) and 0 (fully visible). Crossing into a
detent plays cues: leaving -31 upward and leaving 0 downward play `Panel`;
reaching 0 and reaching -31 play `Options`.

Space polarity: with Space held the panel slides toward -31 unless a latched
typed gadget whose authored record type equals `3` (the text-editor family)
holds focus, in which case it slides toward 0; with Space released it always
slides toward 0.

**Panel asset binding and draw origins are closed.** The side loader opens the
GAF named by the selected SIDE's `intgaf` field and caches the named entries
`PANELTOP`, `PANELSIDE`, and `PANELBOT`. Their final framebuffer origins are
`PANELTOP` at `(129,0)`, `PANELSIDE` at `(0,0)`, and `PANELBOT` at
`(129,H-32)` for the negotiated 640×480 surface. At each battle-shell call
site, TotalA passes that final origin plus the frame's `XOffset/YOffset`; the
raw blitter then subtracts the same fields before writing pixels. The offsets
therefore cancel and do not translate the decoded panel artwork. A renderer
that directly stamps decoded pixels must either stamp them at the final origin
or reproduce both halves of this contract; applying only the call-site
addition is not retail behavior. This is distinct from ordinary `.GUI` gadget
art: a gadget frame is copied at the translated authored gadget rectangle and
its GAF offsets are not added. **Command-window switch is closed.** The underlying battle/root window is
`guis/<prefix>main2.gui` — the only "main" GUI name the executable ever
composes (`%sMAIN2.GUI`, opened by the battle entry and stored as the root's
command-window name) — not `main.gui`; the asset `<prefix>main.gui` (OPTIONS/
SHARE/ALLIES) is never opened by any image path, and `blank.gui` is an
authoring template with no opener. When the selected-unit count becomes zero,
the command-window switch closes the command windows down to the root and opens
nothing; the root `MAIN2.GUI` shows through. A multiple selection or a single
non-builder selection formats and opens `guis/<prefix>gen.gui` (the general
command page); a single builder on page 0 whose definition has no authored page
falls to that same general page, and otherwise opens its authored
`guis/<unit>N.gui` when the definition's authored-page bit is set (the page-
existence probe that sets it checks the file), with the generated page assembly
patching the builder's products into gadget slots `buttonByte + 4 .. 9` — six
product slots, chosen by the authored button byte — when the authored page is
absent or stale. Named page art is resolved from
that page's `<unit>1.gaf`, then the side/main support GAFs, then the common
`BUTTONS0` stock-size groups. A left-button hold inside a gadget selects its
armed frame; pointer hover alone does not tint or change an ordinary button.

The stock battle resource set binds `fonts/<font>.fnt` as the side console
font and `fonts/<fontgui>.fnt` as the side GUI/button font. The frontend
`COMIX.FNT` selection is not a battle-HUD fallback. `energycolor` and
`metalcolor` are active `PALETTE.PAL` indices for their inner resource bars;
raw GAF/PCX/TNT bytes are copied as active indexed pixels. Only semantic GUI
color fields and FNT colors use the GUI-source-to-active lookup built from
`GUIPAL.PAL`; `GUIPAL.PAL` is never installed as the physical display palette.
The resource primitive fills the authored `ENERGYBAR`/`METALBAR` rectangle to
the current-over-capacity width with the side-authored active color; it does
not synthesize a GUI-colored frame or a second palette layer.

**Resource text formatting and cadence are closed.** Energy is the first
resource display and metal is the second. Current and capacity values use
integer text, and the authored `ENERGY0`/`METAL0` anchors receive a literal
`0`. Energy production and consumption use integer text, switching to a
truncated integer `K` suffix (`value/1000`) outside the inclusive
`-99999..99999` range — the production form when the value exceeds `99999` and
the consumption form when it falls below `-99999`; in the normal range the
consumption magnitude is shown with its sign stripped. Metal production and
consumption use one fractional digit (`%.1f`; consumption is absolute-valued).
Normal text uses the color-map entry 15,
production entry 10, and consumption entry 12, resolved through the
logical-to-physical map. Production/consumption values are latched every 30
simulation ticks — the master composer holds a per-resource-record next-due
tick and re-samples the four rate values only when it is due, advancing the
due tick by 30 — while the current-over-capacity bars and the current numbers
remain a live presentation of committed stock, drawn every frame. An earlier
corpus reading that tied the step-30 pair to the scrollback ring concerned
different state (the chat/status ring indices) and does not contradict the
composer's own 30-tick sample latch.

**Unit health color thresholds are closed.** The retail health primitive uses
the active logical-to-physical table entries `dcb[10]`, `dcb[12]`, and
`dcb[14]`: above two-thirds health selects entry 10, above one-third selects
entry 14, and the remaining positive-health range selects entry 12. The
outer health rectangle uses entry `dcb[0]`; the inner fill is inset before
the current/max fraction is truncated toward zero. This is separate from
the side `DAMAGEBAR` anchor, whose placement remains data-authored.

Whenever the offset is nonzero, the moving strip is blitted at y+offset and
three translated strings are drawn onto it: `Game Time:` as `hh:mm:ss`,
`Total Units: %d (Max %d)`, and `Game Speed: %s%s` with a `(+/-n)` suffix
when the requested speed differs from the active speed and the localized
normal-speed word at value 10.

**Frame composition passes.** The master battle frame runs ten ordered layer
passes: terrain tiles → features/wrecks → soft units → hard units → shadows
→ selection brackets/health → projectiles → explosions → UI gadgets → squad
numbers (medium confidence on the projectile/explosion order — a swap would
still match the observed call count). The squad-number pass draws `'0' +
squadId` digits when a squad-overlay bit is set or the squad id is nonzero.
The minimap viewport indicator is not a thick rectangle: in minimap mode the
composer draws two one-pixel Bresenham lines crossing at the camera point plus
the `(128,32)` view origin — a five-pixel horizontal run at the projected row
and a five-pixel vertical run at the projected column — in color-map entry 15
[03 §3.12]. The "thickness varies between 6 and 4 pixels with latch-flag bit
`0x40`" reading of an earlier revision was a mis-transcription: the 6/4 values
are the drag-selection rectangle's outer color-map entries chosen by that same
latch bit while the armed latch is MOBILEBUILD (outer entry 6 when the bit is
set, 4 when clear, else entry 15; inner entry 0), not a viewport thickness.

### Supported inference

Battle chrome should be data-driven from side-data, while semantic values and
command availability remain runtime state. A generic GUI implementation may
share drawing primitives, but must retain the side-data anchor contract.

### Unknown

All invoked retail anchors and their tuple order are established above; slide/modal
combinations and per-side fallback for optional presentation remain incomplete.

**Battle-rail minimap destination is closed (Established).** The battle composer
copies the FINAL radar surface to the origin of its fixed 126×126 logical canvas.
The aspect-dependent `originX/originY` values belong to the picture's internal
letterbox and are not an additional screen placement. Consequently the canonical
destination rectangle is inclusive `(0,0)..(125,125)`; drawing and input both
apply the same letterbox inside that canvas [07 §6][07 §10].

## 7. Fonts, text, palette, and localization use

### Established fact

FNT text rendering uses bitmap glyphs and an indexed-color destination. The
text system measures glyph advances, supports newline termination, clips to
the destination, truncates when a maximum width is supplied, and uses a
transparent palette sentinel for non-written pixels.

**Truncate-to-width is closed.** When a maximum width is supplied and the
total glyph advance exceeds it, the string is copied through a bounded copy
into a 300-byte buffer and trailing bytes are removed until the measured
advance fits — this happens before any clipping test occurs.

**Drop-shadow switch is closed.** The shadow argument passed to the glyph
rasterizer is a presentation-context field consulted on every call, stored
beside the foreground/background color pair in that context; it is not baked
into font data.

The active font is selected by the presentation context. Front-end text uses
preloaded/common or GUI-selected FNT assets; battle text can use side fonts
and compact status fonts. Some title/control artwork uses GAF fonts or GAF
text-like assets in addition to FNT.

The palette is loaded once into an indexed-color/LUT path and shared by GUI,
HUD, GAF, cursor, fog, PCX, and FNT rendering. Side energy/metal color
settings select palette entries rather than arbitrary true-color values.

Localized strings originate from two distinct mechanisms that must not be
conflated. The translation-table path compares the complete supplied key
byte-for-byte; a hit returns the translated value and a miss returns the
original input. Separately, the authored-field accessor used for localizable
record fields first tries `<current-language><key>` and falls back to the plain
`<key>` when the prefixed field is absent. The unit-definition parser uses this
language-prefixed accessor for `name` and `description`, so per-language unit
names and descriptions are honored. Several runtime messages use the translation
table before drawing, but complete caller coverage, code-page interpretation,
and missing-key fallback are not fully established.

### Unknown

The full text wrapping/line-breaking policy, drop-color defaults, code-page
behavior for extended bytes, font fallback order, and exact translation
fallback are not fully established. Truncate-before-clip and the
presentation-context drop-shadow switch are established above.

## 8. Software cursor and world picking

### Established fact

Cursor artwork is loaded from a cursor GAF root. Named cursor entries include
normal, red/green validity, attack, move, airstrike, too-far, hourglass,
path, and revive-style indicators. Cursor frames use the shared palette/LUT.

**The cursor index table is closed.** A hardware-style cursor index byte
selects the active shape from a handle array of twenty-two slots resolved at
init from named entries: index 1 `cursorattack`, 2 `cursorairstrike`,
3 `cursortoofar`, 4 `cursorcapture`, 5 `cursordefend`, 6 `cursorrepair`,
7 `cursorpatrol`, 8 `cursorpickup`, 9 `cursorteleport`, 10 `cursorrevive`,
11 `cursorreclamate`, 12 `cursorload`, 13 `cursorunload`, 14 `cursormove`,
15 `cursorselect`, 16 `cursorfindsite`, 17 `cursorred`, 18 `cursorgrn`,
19 `cursornormal`, 20 `cursorhourglass`, 21 `pathicon`; slot 0 is unused/gray
overflow. Slot 10 is filled last by the init sequence, out of the otherwise
ascending order — a revision of this document that transcribed the sequence
rather than the slot offsets dropped it and shifted every later index by one
(see `docs/SPEC_CONFLICTS.md`). Index 19 is the idle default the pointer
update falls back to, and 20 is the loading shape the front end installs
around blocking transitions. The index writer diffs and swaps shapes, so
re-selecting the shape already shown does not restart its animation. The
armed-order latch decides authorization while the index decides shape; they
coincide numerically only by table offset and must not be conflated. During
mobile-build placement, site validity picks `cursorfindsite` when placement is
valid else `cursortoofar`; the ghost preview uses `cursorred`/`cursorgrn`.

**Every world order fires on left-click; right-click never fires an order — it only returns the command latch to idle (`cursornormal`) and, when idle, clears selection.**

**The shape chooser is closed.** One pointer update resolves the shape in four
steps. First, two region bits record whether the pointer is over the world
viewport or over the minimap; when neither is set the index is forced to
`cursornormal` and nothing else is consulted. Second, the mobile-build latch
with a live placement ghost takes the site-validity branch above. Third, the
selected units of the local player — walked over the owner's inclusive range at
the fixed 280-byte stride, admitted by the membership bit `0x10` — together
with the hovered unit are
each asked for a shape, and **the lowest index wins**, so the table's numbering is
also its shape priority order (re-verified: the chooser collects the selected
units plus the hover id, evaluates each through the per-latch shape table, and
keeps the smallest index with a strict `<` reduction). Fourth, when that
candidate set is empty the
idle latch over an own, active, finished, untasked unit gives `cursorselect`
and everything else gives `cursornormal`.

Per selected unit the shape is dispatched on the armed latch and gated on the
same authored capability flags the order predicate reads, so the advertised
action and the performed action cannot disagree:

* Idle (latch 1) rewrites itself to ATTACK over a hostile target the unit can
  attack, and to RECLAIM over a hostile target a `canreclamate` unit could
  strip; otherwise it yields `cursorrepair` over a friendly target needing
  assistance, `cursorselect` over an own finished unit, `cursorrevive` or
  `cursorreclamate` over a reclaimable feature depending on `canresurrect`
  versus `canreclamate`, `cursormove` for a `canmove` unit, and
  `cursornormal` otherwise.
* MOVE (latch 2) requires `canmove` and then yields, in order, `cursorrevive`
  over a reclaimable feature for a `canresurrect` unit, `cursorcapture` over a
  hostile target for a `cancapture` unit, `cursorreclamate` over a hostile
  target for a `canreclamate` unit, `cursorrepair` over a friendly target
  needing assistance, `cursorunload` when a flyer targets an `isairbase` unit,
  the transport pair below over a carriable target, `cursordefend` over a
  friendly target for a `canguard` unit, and `cursormove` otherwise.
* ATTACK (latch 3) requires `canattack` and yields `cursorairstrike` when the
  unit's primary weapon is authored `dropped`, `cursorattack` otherwise.
* BLAST (latch 4) requires `candgun` and is gated on **affordability, not
  range**: the command-fire weapon's `energypershot` and `metalpershot` are
  compared against the owner's stocks, giving `cursorattack` when both are
  covered and `cursortoofar` when they are not.
* UNLOAD (latch 5) requires `canload` and gives `cursorunload`; PICKUP
  (latch 6) requires a carriable target and gives `cursorpickup` for a `canfly`
  transport, `cursorload` for a ground one.
* FOLLOW (latch 7) requires `canguard` and a friendly target, and refuses when
  a ground guard is pointed at an air target; otherwise `cursordefend`.
* REPAIR (latch 8) requires a target the unit can assist and gives
  `cursorrepair`; PATROL (latch 9) requires `canpatrol` and gives
  `cursorpatrol`; TELEPORT (latch 0xB) gives `cursorteleport`.
* RECLAIM (latch 0xC) requires `canreclamate` and a reclaimable feature or a
  hostile target, and gives `cursorreclamate`; CAPTURE (latch 0xD) requires
  `cancapture` and a target of another owner, and gives `cursorcapture`;
  MOBILEBUILD (latch 0xE) requires a non-empty build list and gives
  `cursorfindsite`.
* Any gate that fails yields `cursornormal`, which is also the value the
  reduction starts from.

The cursor is rendered after the offscreen battle/front-end surface is
prepared. Cursor position is read from the window system and translated into
surface/logical coordinates before drawing. **The hotspot is the GAF frame's
own authored `x_offset`/`y_offset`:** that pixel lands on the pointer, so the
blit origin is the pointer position minus the offset, and no separate hotspot
table exists.

Picking distinguishes GUI/modal controls from world space. In world space,
unit, feature, and terrain/radar tests use the camera transform and visibility
state. Hover/status selection gives priority to visible eligible objects and
does not expose hidden/fogged objects through the UI.

**The cursor-to-ground conversion is closed, and it is a search, not an
inverse.** One routine per pointer update turns the pointer into a world point.
It first picks a branch: over the minimap the pointer scales through the radar
rectangle (§10), otherwise the pointer is clamped into the battle viewport
rectangle and rebased to map pixels as `camera + clamp(pointer, vpLeft, vpRight)
− vpLeft` on X and the same on Y against `vpTop`/`vpBottom`. Three bits of the
placement flag byte record the branch: bit 0 the minimap branch, bit 1 whether
the pointer was inside the viewport, and bit 2 their OR.

The map-pixel pair then goes through the ground resolver, which returns a 16.16
world triple `(X, Y, Z)`. That resolver exists because the two halves of the
presentation disagree: terrain tiles are blitted flat — the tile blitter never
reads a height — while every world object standing on the ground is drawn with
the half-height shear `screenRow = Z − (height >> 1)` [03 §2.5]. Inverting the
projection at height zero would land an order north of the pixel the player
clicked, by half the terrain height there. The resolver instead searches:

1. Clamp both axes into the map rectangle, `0 .. Width·16 − 1` and
   `0 .. Height·16 − 1`. A pointer beyond the map edge therefore resolves to the
   edge, and the build ghost stays live there rather than going out of bounds.
2. Round the clicked row down to a whole cell and start **eight cells south** of
   it, with a budget of 128 that steps down by 16.
3. Probe at most nine candidate rows, walking north one cell (16 map pixels) at a
   time. Each probe takes `h = max(terrainHeight, seaLevel)` — so the water
   surface picks like ground — and computes that candidate's projected screen row
   `Z − (h >> 1)`, both terms narrowed to `int16`. The search stops at the first
   candidate projecting at or above the clicked row. Exhausting the budget
   returns the ninth candidate unrefined.
4. Probe once more one cell further south to bracket the answer, then interpolate
   `Z += ((clickedRow − projNorth) << 20) / (projSouth − projNorth)` and sample
   the height at the interpolated row for the returned `Y`. The unrefined
   candidate is kept only on budget exhaustion or when the clicked row lies
   south of the far bracket (`clickedRow > projSouth`). A non-increasing
   projection pair (`projNorth >= projSouth`) is **not** a guard: retail falls
   through into the division, whose divisor is then zero when the pair is equal
   — the degenerate bracket.

The division is unguarded in retail, so a degenerate bracket faults; nothing in
the stock corpus reaches it. The interpolation is linear in the projected rows,
not in the terrain, so on steep ground the returned point can still project a
few pixels off the clicked row — that residue is retail's own approximation, not
an error to correct. The probe loop, budget, start offset, clamp bounds, and
the `max(height, sea level)` water pick were re-verified constant-for-constant
against the resolver.

The triple is stored for the frame, and its cell index (`>> 20` per axis) is what
the order dispatcher, the build-site validator, and the hover/status readout all
consume.

Queued orders and path previews use separate cursor/indicator artwork. The
cursor validity state is therefore a presentation of command legality, not
just a pointer shape.

### Supported inference

Picking should return a typed hit result with ownership/visibility metadata,
then let the active command mode choose whether a unit, feature, terrain
cell, minimap, or GUI control consumes the action.

### Unknown

Picking hulls and object priority are closed: the hull is the unit's
transformed piece-hierarchy bounding box projected as a four-corner quad with
the half-height shear and a polygon hit test, and the pick walks the
sensor-built hot-unit lists (units only — features win nowhere in the pick;
the reclaim-family paths resolve features on demand at the point). The
visibility gate is the word-grid bit `1 << (localPlayer & 0x1F)` versus the
per-viewer byte grid selected by a visibility-mode bit. Cursor handle slot 0
identity (the unused/overflow slot) is the only remaining item. The
named-entry index table, the hotspot convention, the four-step shape chooser
with its lowest-index-wins reduction, the per-latch shape table, the
cursor-to-ground resolver, and the build-site validity/ghost cursor selection
are established above. The world overlays' palette entries are no longer open:
they are GUI semantic indices resolved through the GUIPAL-to-display map
[03 §4.3], and §9 lists the ones each overlay uses.

One branch of the idle latch is resolved. The red/green divert exists and is
gated on the interface-type option (a runtime word the options and settings
loaders write from the `Interface Type` registry value, clamped to 0/1):
when the option equals `1`, the idle latch answers `cursorred` over a hostile
target and `cursorgrn` over a friendly one, after the same own-finished-unit
test that otherwise gives `cursorselect`, and the can-reclaim/can-attack
branches also resolve to the green validity shape; when it equals `0` (the
default), the specific per-latch shapes of the table above apply (including the
idle ATTACK/RECLAIM rewrite over hostile targets). The earlier claim that the
diverting condition was "not implemented because its byte's writer was not
located" is superseded: the writers are the option loaders and the gate is the
option value itself.

The runtime active-state bit `0x20` and the empty-current-task field that the
own-unit inspect predicate also tests are established for retail but have no
counterpart in the current runtime flag word (see `docs/SPEC_CONFLICTS.md`).
The shared eligibility predicate's exact single-precision compare is **equal to
`0.0`**, not `1.0`: all eligibility sites compile to an exact compare against
the constant `0.0`, and the compared field is a per-unit order guard float —
zeroed at unit creation and at order completion, written with a clamped `0..1`
ratio while an order is being processed — so eligibility means the unit is not
mid-order. An earlier reading of the constant as `1.0` (and the corpus note
that endorsed it) misread the compared value; the machine code and the
constant's bytes are unambiguous.

## 9. Selection, control groups, orders, and build pages

### Established fact

Selection supports:

* Rectangle/rubber-band selection.
* Single-unit selection.
* Category/type filtering.
* Additive and replacement selection modes.
* Control-group creation, selection, clearing, and toggling.

Selection operates on the runtime unit pool. Bulk-selection paths walk the
owning player's inclusive unit range in ascending address order at a fixed
280-byte unit-record stride, so iteration stays stable regardless of writes
made during the walk. The shared eligibility predicate tests authoritative
fields: the active-state bit `0x20` of unit runtime flags; an exact runtime
single-precision value compared equal to `0.0` — a per-unit order-guard float
zeroed at unit creation and at order completion, nonzero while an order is
being processed; no disqualifying state
reference (zero); and either no parent-unit reference or a parent whose
runtime flags carry state bit `0x40000000`. Selection membership is bit
`0x10` of unit runtime flags. Bulk changes also clear the selected-builder
single-select id, refresh aggregate command/UI state, and set battle-
interface dirty bit `0x10` (a different field that coincidentally shares the
value). Selection and hover use separate state so the footer can report a
hovered unit while retaining the selected group.

**Drag-rectangle conversion is closed.** Drag endpoints recorded in world
coordinates are converted to presentation coordinates by subtracting the
camera position and adding the fixed view-pane origin offsets (128
horizontally, 32 vertically); each axis is then sorted independently
(`if right < left swap`, `if bottom < top swap`) and both boundaries are
tested inclusively (`min <= x <= max && min <= y <= max`). The toggle
modifier is bit 2 of the drag parameter word, giving this truth table for
eligible units:

| Modifier | Eligible unit inside rectangle | Eligible unit outside rectangle |
|---|---|---|
| Clear (`0`) | Set selected (`flags \|= 0x10`) | Clear selected (bulk pre-clear `flags &= 0xFFFFFF2F` across the whole pool before iteration) |
| Set (`1`) | Toggle selected (`flags ^= 0x10`) | Preserve prior selected state (no write) |

When the modifier is clear, the pre-clear also runs the single-select reset.
After selection, one selected unit takes the single-unit presentation path
and multiple units take the multiple-unit path; any change sets the dirty bit
above and plays `SelectMultipleUnits` or the single select cue.

**Mouse-button assignment is closed.** Every world action — single-unit picking, rectangle drag selection, building placement, and issuing every order including the contextual code 1 — is performed with the **left** mouse button. The **right** mouse button performs only deselection and cancellation: it cancels an armed order or build placement (returning the command latch to idle) or, when the latch is already idle, clears the current selection. The battle input pump routes left-button press and release through the single-click and drag-rectangle paths and the order dispatcher, while right-button press is routed exclusively to the cancellation path that returns the latch to idle and, when idle, clears selection; no right-button path queues an order. The cursor table shows the same polarity: every latch shape fires its order on left-click; the right-click column is empty or a transition back to the normal cursor [04 §3.4][07 §8].

Control groups store one group value per unit rather than membership bits in
several groups. Ctrl+digits (tokens `0xC5..0xCD`) assign groups with the
`CreateSquad` cue: the assignment scans local unit slots with a nonzero
catalog definition id; selected units (flag `0x10`) receive the requested
group value, and unselected units already carrying that group have it zeroed.

Digits `1..9` (tokens `0x31..0x39`) route between build-page selection and
group recall under an exact battle-mode/Alt gate — Alt is held-key token
`0xFB`, not Shift:

```
modeBit = battle-mode flag & 1
alt     = held-key query for token 0xFB (Alt)
if (!modeBit && !alt)  -> build page for digit - 1
else if (modeBit && alt) -> build page for digit - 1
else                   -> group recall(digit, shiftHeld) + SelectSquad cue
```

where `shiftHeld = held-key query for token 0xF9` (Shift) is the preserve/
toggle argument. Recall selects eligible members whose stored group matches
and clears nonmembers when that argument is clear; a secondary branch keys on
a matching unit that also carries runtime flag `0x80000000`, where the
authored `CTRL_F` type-filter bitset — a 256-bit category mask indexed by
definition id — changes which matching units remain selected. The flag has
readers (the recall filter branch, the unit-info panel, and an AI-side path)
but **no writer anywhere in the image** — the filter branch is unreachable
from retail's own code; a save file or external write is the only way to arm
it. Group recall never centers the camera: a whole-image census of camera
writes covers only the camera/scroll family, and neither the recall export nor
the hotkey dispatcher writes the camera.

Order names are mapped into canonical order classes before local execution or
network transmission. Observed classes include move, attack, blast, defend,
repair, patrol, reclaim, capture, load/unload, build, and special orders.
The command mode distinguishes immediate orders from special orders and
updates cursor/help text accordingly.

**The command latch is closed.** The armed-order latch byte holds values that
are literally the order-dispatcher switch keys (a 14-entry table indexed by
value minus one):

| Value | Order family |
|---:|---|
| `1` | normal/idle (also the generic immediate-order table) |
| `2` | MOVE |
| `3` | ATTACK |
| `4` | BLAST (attack-special) |
| `5` | UNLOAD |
| `6` | PICKUP/LOAD |
| `7` | FOLLOW/GUARD (defend) |
| `8` | REPAIR/HELPBUILD |
| `9` | PATROL |
| `0xB` | TELEPORT |
| `0xC` | RECLAIM/RESURRECT |
| `0xD` | CAPTURE |
| `0xE` | MOBILEBUILD |

The GUI order-button dispatcher arms the latch by parsing the button name in
a fixed chain — STOP into the generic immediate table, then ATTACK, BLAST,
DEFEND, REPAIR, PATROL, RECLAIM, CAPTURE, UNLOAD, LOAD/PICKUP alias, and
default MOVE — writing the parsed value only when the button's runtime gate
value is nonzero, else writing `1`. Each armed write clears latch-flag bit
`0x08` and plays the `immediateorders` cue (ATTACK/BLAST/FOLLOW/PATROL/MOVE
families) or the `specialorders` cue (REPAIR/RECLAIM/CAPTURE families).
Latch-flag bit `0x40` selects immediate-versus-special helptext, bit `0x20`
marks placement-valid pending, and bit `0x08` additionally gates placement
drawing; the dispatcher clears bit `0x08` while the Escape cancel path clears
bit `0x20`. The latch writers are now census-complete: the order-button
dispatcher arms `1..9`, `0xC`, `0xD`; the battle-HUD build-button handler arms
`0xE` (MOBILEBUILD) when the product's `BMcode` byte is zero, storing the
product id in the pending-build word and playing `addbuild` — the click arms
it, not the ghost show; idle resets write `1`. **Latch value `0xB` (TELEPORT)
has no writer anywhere in the image** — it is a consumer-only switch key
(order dispatch, shape table), so the "exact arming trigger for the two
off-button values" question closes as: MOBILEBUILD armed by the build-button
click, TELEPORT never armed by retail's own code.

Build pages are driven by `CANBUILD`, `BUILDER.GUI`, per-builder GUI files,
and side/build GAF assets. A builder’s available products are patched into
named build buttons. Pages support previous/next navigation, product slots,
queue counts, on/off controls, and command enable/disable state. The build
button name and unit definition remain data-driven; the GUI is not allowed to
invent a product absent from the builder’s authored build list.

**Page encoding is closed.** Page switching validates the selected-builder
identity first (nonzero single-select id and nonzero definition id) and guards
against the builder definition's page-count byte. The page number is encoded
in unit-flag bits 23–25 (`(page & 7) << 23`, cleared by mask `0xFC7FFFFF`)
with bit 22 as the paged indicator (`(page > 0) << 22`, cleared by mask
`0xFFBFFFFF`); page 0 clears bit 22 and leaves bits 23–25 alone. Switching
sets battle-interface dirty bit `0x10` and plays the `nextbuildmenu` cue.
Generated side-specific build GUIs and selected-builder identifiers are
asset/catalog driven, and aggregate command state has distinct
enabled/disabled/mixed paths across the selected set.

**Product-page assembly is closed.** The generated-menu input records carry
both an explicit `PAGE` byte and an explicit `BUTTON` byte. Assembly first
matches the builder definition and page, then patches the product into the
named gadget slot selected by that authored button byte. The maximum authored
page populates the builder definition's page-count byte used by the guard
above. Stock Cavedog full builder pages expose six 64×64 product gadgets (the
final page may be partial); the stock `CANBUILD` sequence consequently maps
entries 1–6 to page one, 7–12 to page two, and so on. Generated
`<unit>N.GUI` pages are authoritative for page existence and placement, so a
replacement engine must not infer an eight-slot grid or synthesize missing
pages.

**Build placement is closed, and the branch into it is on the product.** The GUI
build-button handler resolves the gadget to a definition id and, when that id is
nonzero **and the product's authored `BMcode` byte is zero**, arms the
MOBILEBUILD latch (`0xE`), stores the id in a pending-build word and plays the
`addbuild` cue. Nothing is queued; the click that follows on the world is what
issues the order. A product whose BMcode is nonzero never reaches that branch and
falls through to the counted factory-queue producer of the order-producer block
below ([R-P0-11 §1]) — a signed counted add/subtract that never purges. The
"immediate queue path" phrasing of an earlier reading is superseded there.

BMcode zero is the structure class — the same test that decides whether a
definition carries a yard map at all [04 §6.2] — so the discriminator is "is this
product a building", not "is this builder a factory". The two agree across the
stock corpus only by coincidence of the data, and the builder's mobility is not a
usable substitute: stock factories author `CanMove=1`
(see `docs/SPEC_CONFLICTS.md` SC21).

Three things then run once per pointer update, all of them gated on the pointer
being inside the battle viewport — over the side rail or the minimap the ghost is
not updated and not drawn.

*The site.* The ground point under the pointer (§8) is snapped to whole cells
with the footprint's half-extent removed first and a round-to-nearest term
added:

```
cellX = (pickedX - (footprintX << 19) + (1 << 19)) >> 20
cellZ = (pickedZ - (footprintZ << 19) + (1 << 19)) >> 20
```

The footprint here is the movement profile's, copied into the definition at
compile time from the named `movementclass` or, for a class-less building, from
the definition's own authored footprint. The `+ (1 << 19)` is a rounding term,
not a floor: an even footprint straddles a cell boundary and follows whichever
cell the pointer is nearer to, while an odd one is centered on a cell and
behaves like a plain floor.

*The height.* The footprint validator writes the site's ground height to a
global on its way out, and the ghost reads it. That height is the minimum of the
per-cell low heights over exactly those footprint cells whose yard byte carries
bit 3; when no cell carried the bit — the aggregate minimum is still 255 and the
maximum still 0, so the maximum compares below the minimum — the height is
`SeaLevel - waterline` instead. The same two aggregates feed the gates that can
reject the site: `maxHigh - minLow` against the profile's MaxSlope, the bit-4
maximum against the site height, and the pair against `SeaLevel - MaxWaterDepth`
and `SeaLevel - MinWaterDepth`. A rejected site leaves the global at whatever a
separate scan of the same two height fields produced, so the ghost still has a
height to draw at.

*The drawing.* The two cell-aligned corners are projected with the ordinary
half-height shear [03 §2.5], **both with the site height**, so the ghost lies
flat on the ground the building will stand on rather than tilting with the
terrain under the cursor:

```
left   = cellX*16 - cameraX + 128        top    = cellZ*16          - (h >> 1) - cameraZ + 32
right  = (cellX + footX)*16 - cameraX + 128
bottom = (cellZ + footZ)*16 - (h >> 1) - cameraZ + 32
```

Two one-pixel outlines are drawn, the second inset by one pixel on all four
sides, **both in the same color**: validity is a color change, not a shape
change. The color is a GUI semantic index — 10 when the site is legal, 4 when it
is not — resolved through the GUIPAL-to-display map [03 §4.3]. GUIPAL's first
sixteen entries are the familiar sixteen-color set, so those are bright green
and dark red. The drag-selection rectangle shares this drawing path and takes
15 (white) outer and 0 (black) inner. No text is drawn beside the ghost. The
pointer shape carries the same validity independently: `cursorfindsite` when
legal, `cursortoofar` when not (§8).

*The click.* A left-click on a legal site walks the local player's unit range in
pool order and issues an order to every selected unit whose definition is
authored `builder` (capability bit 6) — MOBILEBUILD, or VTOL_MOBILEBUILD when
the unit is authored `canfly` (bit 11). One click therefore tasks a whole pack
of construction units onto one structure. The order's stored position is the
**footprint's center**, `((foot + 2*cell) << 19)` per axis, with the site height
as Y — not the raw cursor point. The `oktobuild` cue plays. If Shift was held
the placement-valid-pending bit is set and the mode stays armed for another
placement; that bit is cleared, and the latch returned to idle, as soon as Shift
is released, whether or not another click arrived. A left-click on an illegal
site plays `notoktobuild`, queues nothing, and leaves the mode armed. Escape and
right-click both return the latch to idle (§9, mouse-button assignment).

The builder then paths to the site: build-site generation enumerates perimeter
candidates around the footprint, filters by build distance and placement
validation, and hands a selected point goal to path search [04 §7.4].

**The order-queue overlay is closed, and it is gated on Shift.** Once per battle
draw, after the effect strips and before the GUI frames, the engine tests whether
Shift is held; only then does it walk the local player's units and draw their
order queues. Each order's descriptor carries a **five-bit draw mask** that
selects which of five helpers run for it: a build-site marker, a travelling-dash
chain, a circle at the order point, a queued-order icon, and a per-unit pass that
draws range rings once (per-bit dispatch in R-P0-11 §3 below).
The hovered/selected unit is drawn with the full mask and other local units with
the marker-only subset, and only when some builder context exists. The
per-order-kind mask byte values are now closed from the registration tables
[R-P0-11 §3]:
MOBILEBUILD marker+dashes+rings (`0x13`), VTOL_MOBILEBUILD marker+dashes
(`0x03`), MOVE and PATROL dashes+rings (`0x12`), QMOVE/QPATROL dashes (`0x02`),
and every attack-family kind icon-only (`0x08`) — the circle bit is set by no
stock record. An earlier reading of this paragraph — a four-bit mask, a plain
connecting line, a line-only subset for other selected units, and no stock order
using the circle bit — was superseded: no stock helper draws a plain connecting
segment (the dash chain is the only connector), the bit-4 helper is a circle,
and the icon helper (bit 8) was missing from the enumeration. A later revision
that re-added the circle bit to the attack family was itself wrong: the attack
family is icon-only, and no stock order uses the circle.

**The build-site marker is eight lines with a ten-tick sweep.** For an order
carrying a nonzero build definition id, the footprint rectangle is projected
exactly as the ghost is — the definition's own corner offsets added to the order
position, then the half-height shear. The order's age in simulation ticks is
clamped into `0 .. 10` and drives two offsets:

```
dx = (right - left) * age / 10        dy = (bottom - top) * age / 10
```

Four lines are drawn at `left + dx`, `right - dx`, `top + dy` and `bottom - dy`,
each twice: once one pixel outside the rectangle on the perpendicular axis and
one pixel back along its own in the outer color, once flush with the rectangle in
the inner color. At age 0 the four lie on the footprint's own edges; over the
sweep the two verticals cross each other and land on the opposite sides, as do
the two horizontals, so the marker closes inward and comes to rest as the same
rectangle it started from — a third of a second at 30 Hz. The color pair is
chosen by whether the owning unit is currently selected: GUI indices 3 (outer
lines) and 10 (inner lines) when it is, 1 (outer) and 9 (inner) when it is not.

Queued build indicators use unit/build GAF artwork and numeric queue state.
Selection changes can update the side panel, build page, command palette,
health bars, unit name, and queued-order cursor indicators.

#### UI order producers (R-P0-11)

The findings below are folded from addendum R-P0-11. The headings keep the
addendum's section numbers so `[R-P0-11 §N]` citations still resolve; the
addendum's contract table is not repeated. Nanolathe impact: battle-HUD product
clicks become counted ±1/±5 producers that never purge, the queue-count labels,
the five-bit overlay walker, Shift-latch persistence, and the COB signal-mask
seed 1; the regression fixtures that lock these cover the counted coalesce,
tail-most cancellation, and the dash cadence.

##### Factory product click producer (R-P0-11 §1)

**Established.** The battle-HUD click handler resolves the clicked build-page
toy's name to a product definition id, loads the single-selected unit from the
runtime pool, and branches on the product's BMcode byte exactly as the
build-placement paragraph above records: zero arms MOBILEBUILD placement;
nonzero (every factory product) falls through to a signed counted add/subtract
against the builder's production queue. The count is derived from the click's
button identity and the live held-key query for token `0xF9` (Shift): +1 for a
plain left-click, +5 for Shift+left-click, -1 for a plain right-click, -5 for
Shift+right-click. The "immediate queue path" phrasing of the build-placement
paragraph above is superseded by this: the path is a counted producer that
never purges — the Shift axis scales the count, not the queue mode.

The counted-add routine plays the `addbuild`/`subbuild` cue (local player;
positive counts only), routes the stockpile buttons `MAKENUKE`/`MAKEANTI` to the
`BUILDWEAPON` order descriptor and everything else to the
`MOBILEBUILD`/`BUILDINGBUILD` descriptors from the product definition, then
coalesces the signed count into the queue:

* Positive count: the queue head is chosen by a descriptor flag that selects
  the secondary order list when set and the primary order list otherwise. If
  the tail node of the chosen list matches the descriptor's kind and product
  id, the count is added to that node's signed 32-bit count field — a plain
  click therefore accumulates into the tail-coalesced node instead of
  restarting the queue. Otherwise a new node is inserted with the no-purge mode
  hard-set: the purge branch that unlinks unprotected nodes is unreachable from
  clicks.
* Negative count: matching nodes are scanned without stopping at the first
  match, so the tail-most match is consumed first. If its count exceeds the
  remaining magnitude the node is subtracted in place; otherwise the node is
  unlinked (tombstoned unless it is the list head), freed, and the scan repeats
  with the reduced remainder.

Counted nodes keep their descriptor flags, including the counted-production
flag the button counter filters on; the world-order shift-chain flag does not
participate on this path.

##### Queue-count display (R-P0-11 §2)

**Established.** After every enqueue or cancel the click handler runs the
count-label writer over the page's toys; for each build-product toy it sums
the count fields of nodes in **both** the primary and secondary order lists
whose kind/id matches the product id and which carry the counted-production
flag. The toy's flag bits select the format: bit `0x04` gives `+%d` (for
example `+5`); bit `0x08` gives `%d` with a ` +%d` second count appended. A
zero total clears the label. There is no numeric clamp in the counter or the
formatter — the toy's text buffer is the only bound. The label is written into
the toy record's text slot and rendered by the ordinary window text pass (the
window's font handle and foreground/background GUI color fields, anchored by
the toy's authored rectangle); there is no separate count primitive and no
display cap.

##### Order-queue overlay helpers (R-P0-11 §3)

**Established, gate and ordering.** The world composer draws the selected
units' queue quads after the unit band and again after the projectile/impact
flash; that quad pass is gated on bit 2 of the interface-options word (toggled
from the options handlers, seeded at startup). The Shift-gated overlay walker
then runs once per frame over the local player's unit slice — after the effect
strips, before the drag-rectangle and GUI frames — consistent with the overlay
paragraph above. The walker passes a caller mask to a per-unit dispatcher:
mask `0x1F` for the acted-on/hovered/single-selected units, marker-only `1`
for the remaining local units, and only when some builder context exists. Per
order node the effective mask is `table[kind].drawMask & callerMask`; the five
bits dispatch:

| Bit | Helper |
|---:|---|
| `1` | Build-site marker — the eight lines of the marker paragraph above. Age is the global tick minus the order's birth tick, clamped `0..10`, driving the `(w·age)/10` and `(h·age)/10` offsets; the color pair is that paragraph's 3/10 vs 1/9, outer/inner. |
| `2` | Travelling-dash chain between the order's previous position and its anchor (target tracking and the cached-position flag live in the anchor getter). The distance is a float square root truncated toward zero; segments under one world unit draw nothing. The artwork is **not procedural**: sprites come from a GAF sprite chain (a frame table with a ticks-per-frame field), blitted through the ordinary GAF byte-copy blitter, with frame index `(age/tpf) % nFrames` where age is the global tick minus the order's birth tick — the cadence is driven by the order's **creation tick**, not the global phase. The chain's phase offset starts at `((age mod 30) * 3 << 20) / 30` (integer division) and advances `3 << 20` per sprite until the segment end; the 30-tick wrap of the phase is what makes the dashes march. Each sprite's position interpolates the segment linearly with a 16.16 fraction `(phase << 16) / distance`, so sprites sit three cells apart. |
| `4` | A sixteen-segment circle at the order point. The radius is the truncated product `src * 0.9` where `src` is the target unit definition's radius field for unit targets and a constant 32 otherwise; chords are drawn through the line primitive in color-map entry 12. |
| `8` | Queued-order icon: an animated cursor-GAF frame at the order point, index `tick/(tpf*2) % nFrames` into the cursor handle array at the descriptor's icon byte, drawn with the half-height shear; in battle the attack icons (1/2) alternate color-map entries 12/4 on the low tick bit and additionally draw the weapon AOE/coverage/attack-length rings. |
| `16` | Once per unit: labeled range rings — sight/radar/sonar/jammer/build-distance/maneuver/kamikaze radii read straight from the unit definition's fields, weapon ranges from the weapon definition's authored range field — in color-map entries 15/12/14 per branch; the weapon-range pulse grows from 8 to the authored range over `tick mod 60`. |

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

Corrections to the overlay paragraph above: the mask is **five bits, not
four** — the icon helper (bit 8) was missing from that enumeration, and the
bit-4 helper is a circle, not a plain connecting line; no stock helper draws a
plain connecting segment, so the dash chain is the only connector. The
per-order-kind mask byte values in the runtime-built table are now
**established** from the three static registration tables (the full census is
in the helper table above), with exact bit identities.

##### Latch persistence under Shift (R-P0-11 §4)

**Established.** The world-order commit handles every armed latch on
left-click: it issues the order (MOBILEBUILD via the build-order issuer plus
the `oktobuild` cue; every other latch via the general order issuer reading the
resolved ground point), then tests the click record's key-state word for the
Shift bit — the message-time modifier, not the live key state. When Shift was
held at the click, latch-flag bit `0x20` is set and the latch stays armed;
otherwise the armed-latch byte is written back to `1` (idle) and bit `0x20` is
cleared, with a command-palette refresh. The same Shift test governs the
MOBILEBUILD branch and every other latch — move, attack, patrol, and reclaim
alike — so the placement-valid-pending behavior of the build-placement
paragraph above is the general rule, not a MOBILEBUILD special case. A
per-frame closer then implements release-to-idle: while bit `0x20` is set and
the **live** held-key query for token `0xF9` reports Shift up, it writes the
latch to idle, clears bit `0x20`, and refreshes the command palette. The
arming test therefore uses the click message's Shift bit while the disarm test
uses the real-time key state — two distinct sources.

##### Engine-started COB thread signal mask (R-P0-11 §5)

**Established.** The COB thread-slot allocator initializes each new
engine-started thread slot with a fresh state word, code pointer, return
sentinel (−1), parameter cell (0), and a **signal mask of 1** — engine-started
threads begin accepting signal group 1, not group 0. This supersedes the
earlier "mask 0" guess for engine-started COB threads. (Engine-side finding
folded here with the addendum that shipped it; its natural home is the COB VM
contract, and it is recorded here so the consolidation loses nothing.)

##### Unknowns carried from R-P0-11

The overlay color-map questions are closed: the map itself is the boot-installed
256-byte logical-to-physical table, and every helper's entry is pinned — marker
1/9 and 3/10, circle 12, rings 12/14/15, attack-ring alternation 12/4; the
dash helper reads no map entry (it blits GAF frame bytes directly). The
per-order-kind draw-mask bytes are established from the registration tables
(above). The dash-chain phase arithmetic is established (above); the only
remaining open cell is the text-pen formula of §5.

### Supported inference

The command system should retain a canonical semantic order object from input
through local execution, cursor validity, HUD help, and network serialization.
This avoids divergent behavior between mouse clicks, keyboard shortcuts, AI
orders, and multiplayer packets.

### Unknown

Repeated group-recall centering (never centers: negative-bounded over the whole
image), the writer lifetime of selection
flag `0x80000000` (reader-only, no writer in the image — the `CTRL_F` filter
branch is unreachable from retail's own code), hull geometry and jammer versus
radar-contact picking (hull = projected bounding-box quad over the sensor-built
hot-unit lists; the word-grid gate bit is the local player's index — both
established in §8),
page rebuild timing versus factory completion (completion sets the HUD dirty
when the completing or produced unit is selected),
and the exact arming trigger for the two off-button latch values (MOBILEBUILD
armed by the build-button click on a `BMcode`-zero product; TELEPORT has no
writer) are resolved above. The dash cadence, travelling-dash artwork, circle, icon, and ring radii of the
queue overlay are established above ([R-P0-11 §3]); the per-order-kind draw-mask
byte values in the runtime-built descriptor table and the overlay color-map
entries are established too. Factory product clicks and the queue-count
label are established as well ([R-P0-11 §1, §2]). The drag-rectangle toggle truth table, eligibility predicate,
overlap pick order with strict `<` tie-break and inclusive `min <= x <= max`,
fog word versus byte gate (word grid bit `1 << (localPlayer & 0x1F)` versus the
per-viewer byte grid, selected by a visibility-mode bit — exact test in §8),
toggle versus held-Shift styles,
group assignment and recall gating, digit routing, pagination bit encoding,
latch and dispatcher and cursor tables, mixed-selection AND gate, build
cancellation with tombstone (always tombstoned for BuildWeapon and
SelfDestruct), queue-modifier mapping, and attack-ground versus unit-target
discrimination (BLAST always ground) are established above [P1-14].

## 10. Camera, scrolling, projection, and radar/minimap

### Established fact

Camera state is shared by input, world rendering, picking, minimap clicks,
and save/load. Keyboard direction input and pointer edge scrolling update the
same camera coordinates. Scroll speed comes from the persistent setting and
is bounded by a retail clamp.

For one host-frame camera pass the scroll magnitude is
`delta = scrollSettingByte * rawTimeDelta`, capped at `128`; zero delta skips
movement. Direction predicates use the presentation width `W` and height `H` and
local pointer `x,y`:
Left: `(Left held AND TALK.GUI absent) OR (x == 0 AND y < H)`,
Right: `(Right held AND TALK.GUI absent) OR x == W-1`,
Up: `(Up held AND TALK.GUI absent) OR (y == 0 AND x < W)`,
Down: `(Down held AND TALK.GUI absent) OR y == H-1`.
Opposite held directions are evaluated sequentially. When the screen-cursor
branch is selected, a pointer outside the right/bottom edge but less than 100
pixels beyond it, with focus held, is forced to `W-1`/`H-1`, extending edge
scroll into that strip. `TALK.GUI` suppresses only held-arrow movement, not
pointer-edge movement.

Edge scrolling checks window focus and a small edge/minimap interaction region.
When a modal GUI such as chat or a message box is active, edge scrolling is
suppressed. Camera movement marks the world/fog/view state dirty so dependent
surfaces are rebuilt.

The camera transform is consumed by:

* Terrain tile projection.
* Feature and unit world rendering.
* Selection rectangle conversion.
* Cursor-to-world picking (see the ground resolver in §8: clamp, nine-probe
  search along Z, then a linear bracket interpolation).
* Minimap interaction and camera state.
* Fog/visibility presentation.

The minimap has a fixed square logical canvas with aspect-letterboxing. If a
pre-baked radar image is unavailable, the engine builds a temporary higher-
resolution image, rescales it, and composites it into the minimap region.
The input path reads the minimap/edge region and updates camera state.

#### CRD-006 camera cadence seam — two retail writers [R-CRD-006 §1]

**Established fact — input scrolling and phase-10 following are distinct.**
The host-frame input path described above writes the signed 32-bit camera
origin directly. Its state is a current map-pixel origin (`cameraX` and
`cameraZ`), a persistent unsigned byte scroll setting, and the signed
host-frame raw-delta value used to calculate one movement magnitude. For each
matching direction, the magnitude is `scrollSetting * rawDelta`, made
non-negative and capped at `128`; a zero magnitude performs no movement. The
direction tests run in the order Left, Right, Up, Down, so opposing held
directions can write the same origin sequentially. The input writer marks the
camera/view state dirty and wakes the normal clamp/refresh path. This is a
host-frame presentation/input cadence; it is not a phase-10 keyboard-target
step. [07 §10]

Phase 10 writes that same current origin from a separate follow-camera helper.
It does not read the held-arrow or pointer-edge predicates and does not use
the scroll setting or host raw delta. Its semantic target selection priority
is: an active in-flight camera move (whose remaining count is consumed when
selected), then the followed projectile, then the valid tracked object. An
invalid tracked object clears tracking; with no selected target, phase 10 does
not recompute or step a follow target. The selected target is converted to a
desired camera origin by subtracting half the viewport span (and applying the
ground object's half-height shear on the vertical axis), then the desired
origin is clamped to the map's normal camera range before stepping. [01 §4.4]

The phase-10 follow state is therefore two signed 32-bit map-pixel origins
(`currentX/currentZ` and `desiredX/desiredZ`), plus a signed 16-bit remaining
count for an in-flight camera move and a three-component 16.16 fixed-point
anchor for that move. Followed-projectile and tracked-object selections are
nullable object references; map dimensions and viewport spans are signed
integer extents. These are semantic types: an implementation may represent
the references as stable handles, but must preserve null and validity
behavior. [01 §4.4]

The phase-10 current-to-desired step is exact. For each axis let
`d = desired - current`: if `|d| > 320`, add `sign(d) * 320`; otherwise add
`trunc(d / 2)` with signed integer truncation toward zero. Thus `d = ±320`
uses a half-step of `±160`, `d = ±321` uses `±320`, and `d = ±1` stalls one
pixel short. A nonzero step marks the camera/view state dirty. The phase-10
order is target selection (including in-flight-count consumption), desired
origin calculation and clamp, current-origin step, shake consumption, then
the final current-origin clamp and view invalidation. [01 §4.4][03 §5.6]

Shake is applied after the follow step, in place to the current origin. It
never changes the desired follow target. Its integer state is duration,
remaining count, signed X/Y amplitudes, and an active flag. A request updates
duration to `trunc((requestedDuration + currentDuration) / 2)`, resets
remaining to that duration, accumulates the amplitudes, and is active only
when duration is positive. While the active counter is positive, each axis
first computes `s = trunc(amplitude * remaining / duration)`, then consumes
one CRT value as `trunc(rand * s / 0x8000) - trunc(s / 2)`; the two axis draws
occur before remaining is decremented. An invocation entering with no positive
counter only clears the inactive state and performs no draws. The final camera
clamp therefore also clamps a shaken origin. [01 §4.4.1]

This distinction corrects a tempting but unsupported reading of the CRD-006
handoff: retail evidence does not establish a keyboard/edge “target” that is
advanced by phase 10. The handoff's sample-once/hold-through-the-next-pump
requirement is a valid Nanolathe determinism seam, but it is a supported
implementation choice rather than a retail contract. If used, the seam must
keep host-frame sampling, phase-10 intent application, follow-target
stepping, and shake as named stages; it must not be documented as retail's
input algorithm. Retail's host-frame writer remains one direct movement pass
per host-frame invocation, while retail's follow/shake writer runs once per
runnable simulation sub-tick. [01 §4.4]

**Established fact — cadence probes.** The following probes separate the two
writers and are suitable for an implementation test harness. “Host pass” is a
single invocation of the input writer; “phase pass” is one phase-10 callback.

| Probe | Host-frame input writer | Phase-10 follow/shake writer |
|---|---|---|
| 0 phase passes | A host pass may still apply one direct delta if the outer frame ran; no phase-10 movement or CRT draws occur. | No target step and no shake consumption. |
| 1 phase pass | The same one host pass is not multiplied by the phase count. | One follow step, then one shake consumption/draw pair when active. |
| 5 phase passes | One host pass still applies one direct delta, with Left→Right→Up→Down ordering. | Five independent follow steps and five shake consumption/draw pairs when the counter remains active. |
| Held direction | `scrollSetting * rawDelta`, capped at 128, is recalculated only when the host writer is invoked. | No retail held-arrow read occurs. A deterministic Nanolathe seam may hold the sampled intent for the next pump and apply it once per phase pass; this is explicitly non-retail behavior. |

For the follow step, test `d = 0, ±1, ±2, ±319, ±320, ±321` and verify
respectively `0, 0, ±1, ±159, ±160, ±320` movement, with the sign preserved.
For input, test raw delta zero, a product below the cap, exactly the cap, and
above the cap; test both opposing pairs and verify the sequential order rather
than collapsing them into a single vector. For shake, test an active counter
of one (two draws, then zero) and the following invocation (no draws). These
tests assert arithmetic and ordering without depending on executable layout.

**Established fact — save ownership.** The `Camera` save account owns only
`X Position` and `Z Position`. Loading restores the camera origin in the
fixed account order after `Players` and before `Features`, then reapplies the
normal per-axis clamp. The persistent scroll setting is settings state, not a
`Camera` account entry. The save census does not show a serialized phase-10
desired target, in-flight camera-move countdown, or active shake counter;
whether any such transient state is reconstructed from another account is
not established and must not be inferred from the origin entries. [08
"Account inventory"][08 "Established fact — fix-up and partial-load order"]

**Supported inference — modern presentation controls.** Middle-button drag
may add a presentation-only origin/target adjustment and zoom/graphical
transforms remain presentation-owned. These controls must not mutate
authoritative simulation state or either RNG stream. No retail phase-10
keyboard-target contract or retail middle-drag save contract is established.

**Minimap generation is closed.** The internal canvas is 126 pixels on the long
side with the aspect-preserving letterbox arithmetic below; the generated
picture is produced at twice that size and downsampled (2x supersample) when no
baked terrain image exists — each supersample output maps back through floor
division to a source tile pixel. Four named surfaces participate: the generated
or baked *picture*, a temporary supersample buffer, the *mapped* composite that
carries contacts, and the *final* surface that merges mapped state with the
picture and draws start positions plus the viewport rectangle. Radar/sonar
contact blips use dedicated palette entries distinct from terrain colors, and
the radar surface is wiped and rebuilt each tick while the picture persists.

Per-axis camera clamp order is:
`maximum = mapSize - viewSize; if camera < 0 then 0 else if camera > maximum then maximum`,
giving inclusive `[0, mapSize-viewSize]` in the normal `viewSize <= mapSize`
domain; ordered form controls negative-maximum domains. The clamp refreshes the
camera-to-radar rectangle. Camera persistence uses `Camera` `X_Position` /
`Z_Position` and reapplies the clamp on load.

Radar layout fits the map aspect inside a 126×126 square with signed integer
division and centers the shorter dimension:
`radarHeight=126, radarWidth=floor(mapWidth*126/mapHeight)` when
`mapWidth < mapHeight` else the transpose, with `padX = trunc((126-radarWidth)/2)`
or `padY` accordingly. The rectangle is inclusive
`right=padX+radarWidth-1, bottom=padY+radarHeight-1`. The direct radar branch
converts
`worldX=(mouseX-padX)*PlayRight/radarWidth`,
`worldZ=(mouseY-padY)*PlayBottom/radarHeight`,
then uses those world coordinates directly as the camera coordinates and
follows the standard movement/clamp path; no half-viewport recenter is applied.
**Correction:** the earlier §10 wording used `mapWidth`/`mapHeight` as the
scale numerators; the executable uses the play-area pixel extents
`PlayRight`/`PlayBottom`, matching [03 §3.11].
An alternate drag/current-camera branch exists
when the direct predicate fails or a battle-mode bit is set; its boundary
vectors are not reduced to a standalone truth table. No zoom/rotation mutation
occurs in the reviewed edge-scroll, direct-radar, clamp, or save/load paths.
Minimap rendering and visibility masks are separate concepts.

**Correction (Established; [03 §3.11]).** The previous formula in this section
used raw map dimensions and a half-viewport recenter. That was a stale reading
of the direct branch. The clean-room reduction and implementation contract use
playable `PlayRight/PlayBottom` extents and direct-origin camera writes; only
the alternate drag branch applies a stored-camera delta.

### Supported inference

Camera and minimap conversion should remain an explicit compatibility
boundary. The retail paths do not share one conversion routine: the minimap
lens does not reuse the main view's cursor-to-world projection [03 §3.11], and
the ground resolver is a distinct search (§8).

### Unknown

The camera clamp order, the direct-radar conversion, the drag/current-camera
branch, the click-vs-drag gate, the terrain-height projection, and the
radar/visibility update cadence are established above (minimap generation:
126-pixel canvas, 2x supersample, picture/temp/mapped/final surfaces; the lens
indicator is two one-pixel lines in map entry 15, §6). What remains open is
the behavior of the camera clamp in unusual domains — maps whose view size
exceeds the map size on an axis, where the ordered clamp form is the only
established behavior — and the unresolved mapping of the three sensor callback
tables to the radar versus jammer palette entries. Unit, commander, feature,
and projectile art sources and their direct `PALETTE.PAL` indexing are closed
in [03 §3.9]; start-position markers remain doc 03's item.

The exact interleaving when a host-frame input pass and one or more phase-10
passes occur during the same outer frame remains **Unknown**; the two writers'
individual ordering is established, but their caller-level scheduling is not.
It is also **Unknown** whether any transient follow-target or shake state is
reconstructed from a non-Camera save account. The handoff's deterministic
held-intent seam is documented above, but whether a compatibility mode should
expose it alongside retail's direct host-frame input cadence is an
implementation decision, not a further retail finding.

## 11. Running display, pause, chat, options, and outcomes

### Established fact

The running display includes live resource bars and numbers, game time/speed
text, unit hover/selection information, damage and queue indicators,
scrollback/status messages, chat, minimap, panel chrome, cursor, and fog.

Chat opens the `TALK.GUI`/`TALK2.GUI` text-entry overlay specified in section
5; the open key, recipient modes, `+` command path, capacity bounds, and
camera-suppression rules are established there.

**Status scrollback is closed.** Chat/status output drains from a fixed-
stride output ring: 72-byte entries, 30 slots, with separate producer and
consumer display indices that wrap at 30. The lobby/battle heartbeat copies
rows into the `OUTPUT` gadget until the indices meet, advancing the display
side at most one row per pass under a `lineLen/(fps+2)` rate limiter.

In-battle options and message-box panels are modal. Load/save, restart, CD
check, and exit flows all use dialog GUIs and share text, button, list, and
scrollbar rendering.

**Tab strip and manual exit.** In a non-network battle, the Tab key
plays `SmallButton` and opens the hard-coded `guis/tabmenu.gui` window — a
510×33 top strip (authored origin `y=-33`) carrying `OPTIONS`, `SHARE`,
`ALLIES`, and `CONTROL`; this name is not side-prefixed. A second Tab while it
is open closes it (the Tab-menu word bit `0x20` toggles). In battle mode the
opener hides the diplomacy gadgets for non-diplomatic contexts. This supersedes
the earlier reading that Tab opens `guis/armopt.gui`: the ESC-menu path (token
`0xE3` while the battle-interface ESC bit is clear) opens the hard-coded
`guis/armopt.gui` window with `anims/armopt.gaf`; this
name is not side-prefixed. Opening it sets both the battle modal bit and the
single-player pause bit, and pauses the runtime audio path. Closing the root
window clears the modal and pause bits and resumes audio. A second ESC while
the root options window is active therefore closes it and resumes the battle.
The authored root controls are `LOADGAME`, `SAVEGAME`, `PREFS`, `MISSION`,
`HELP`, `EXIT`, and `OK` (`Resume`). Network mode takes a separate path and
does not set this local pause bit.

Activating `EXIT` pushes `guis/exitmenu.gui` over the options window. Its
authored controls are `MAINMENU` (`Exit to Menu`), `EXITGAME` (`Exit Game`),
`RESTART`, and `CANCEL`. `MAINMENU` opens `guis/yesorno.gui` with the title
`Surrender this battle and return to main menu?`; `EXITGAME` opens it with
`Exit the Battle` in the ordinary local skirmish path. `CHOICE1` (`Yes`)
commits the requested transition and `CHOICE2` (`No`) returns to the exit
window. These windows are a SAVE UNDER modal chain: the options window remains
beneath the exit window, which remains beneath the confirmation window.

The modal layout and raster contract is also established. `ARMOPT.GUI` is
created with flags `0x800` and retains its authored `(0,128,128,352)` root.
`EXITMENU.GUI` is created with `0x1800`, and `YESORNO.GUI` with `0x1000`; bit
`0x1000` makes the GUI initializer replace the authored root origin with its
modal sentinels and center the window in the playfield to the right of the
128-pixel rail:

```
x = (screenWidth - 128 - windowWidth) / 2 + 128
y = (screenHeight - windowHeight) / 2
```

At 640×480 this places the 150×155 exit window at `(309,162)` and the 400×100
confirmation window at `(184,190)`. The initializer allocates each window a
surface exactly equal to its root width and height; panel tiling, gadget art,
and glyphs are clipped to that surface before composition. Panel lookup tries
the file/context art and then the literal `BackTile`, so an absent/unusable
`YESORNO.GUI` panel field still produces a tiled background. A selected stock
button frame replaces the gadget's runtime width and height; the authored
95×20 `CHOICE1`/`CHOICE2` rectangles consequently use the selected 96×20
`BUTTONS0` frames. Ordinary modal labels use primary GAF-font slot zero,
installed from `anims/hattfont12.gaf`; glyph palette indices (including their
outline) are copied directly, with the active side FNT serving only as the
null-slot fallback.

Pause is represented by a runtime state that suppresses simulation progress
and causes an `igpaused` title overlay to be drawn. Victory/defeat overlays
come from the `igtitles` GAF family — handles `igvictory`, `igdefeat`, and
`igpaused` — gated by mode-word bits: victory on bit 5 of one mode word,
defeat on bit 6 of it, pause on bit 0 of the pause-mode word. Victory/defeat
states later transition to end-mission/endgame report screens. The ESC
options-window pause path is established above; the separate Pause-key path
remains outside this closure.

Game-speed changes are clamped to the retail range and displayed as localized
messages. In multiplayer, speed changes are represented as networked semantic
commands rather than purely local UI changes.

**Correction: authored end-mission resources supersede the earlier
message-box description.** The earlier wording about a generated message box,
literal header strings, and a literal `Continue` callback was not an authored
ENDMSN contract; it came from the synthetic presentation and is not a basis
for implementation. Retail supplies `guis/endmsn.gui` and `anims/endmsn.gaf`.
The GUI's named controls include `Start`, `LoadGame`, `SaveGame`, `MainMenu`,
and `Difficulty`; the result action is attached to the authored `Start`
control, not a control named `Continue`. The GAF contains `outcdivider`,
`victory`, and `defeat` entries. `victory` and `defeat` are direct outcome
copies rather than gadget references: the renderer blits the selected frame
using its authored anchor offsets at the negotiated surface center [fmt gaf].

The file's end-mission controls are initially inactive in the retail asset.
The end-mission initializer chooses the outcome resource from campaign
progression and activates the established route: `Start` when a campaign has a
discovered next mission, or `MainMenu` when there is no next mission. Other
controls remain inactive until their activation and callback contracts are
recovered. Missing GUI/GAF resources are a degradable unsupported result, not
a reason to generate a centered rectangle, button set, title text, or fallback
labels [08 "Progression"]. Exact statistics presentation remains Unknown.

### Supported inference

Modal state should be authoritative for input routing, while pause/victory/
defeat should be represented in the battle presentation state so the world
can remain composed under the appropriate overlay.

### Unknown

Exact pause authorization in multiplayer, multiplayer forwarding authority
for chat/pause/speed packets, chat commit-versus-cancel semantics on every
send route (including terminator inclusion per route), scrollback drain
ownership in the in-battle HUD (only the heartbeat drain is closed), outcome
transition timing, and the per-value endgame bar-fill animation mechanism are
incomplete. The chat open/send contract, the `+` command mini-language
(`+<digit>`, `+a`/`+e`), the scrollback ring, the overlay
gates, and the category-cadence statistics animation are established above.

## 12. Lobby and session shell

### Established fact

The lobby/battleroom shell updates:

* Player slot visibility and occupancy.
* Human/AI/blocked/unused status presentation.
* Side, color, ally/team, logo, resource, ping/memory, and ready controls.
* Map name and map availability.
* Chat output and start/ready button state.
* View-map and map-preview controls.

The lobby supports both lobbied and direct multiplayer connection setup, with
TCP/modem/serial panels in the front-end. Start/ready transitions feed the
mission/schema loader and synchronization barrier.

Map selection and player-count filtering use OTA schema/start-position data.
The lobby does not fabricate a playable map if no compatible map/schema is
available; it displays a not-selected or unavailable state.

**Battleroom slot contracts are closed.** Each of the ten fixed-size
player-slot records carries a slot-class byte: `0x00` empty, `0x01` human
host, `0x02` human join, `0x03` computer/AI, `0x04` blocked (`0xFF` is the
initial/unset data filler, not an executable slot class). The populated-row
path requires a non-null slot with class in {1, 2, 3} plus a marker/status
condition. Ready state is bit `0x20` (bit 5) of the shared per-player word:
the heartbeat XOR-syncs each human peer's bit 5 against the local player's
bit 5, propagating readiness to peers; the READY gadget displays that bit and
is grayed for every slot class except human host. The same bit position of
the local player's own word, captured once per pass, gates map-control
authority: SIDE editing is enabled when it is set or the slot is not human;
ALLY and PLAYER/MEM row enablement take it directly; otherwise the gadgets
get the disabled call. Statically one bit serves both READY display and
local authority, and retail may assign distinct per-role meanings that static
reading cannot separate.

Ineligible slots hide their row gadgets (`LOGO`, `SIDE`, `ALLY`,
`TEAMICONS`, `RES`, `PING`, `MEM`, `READY`), gray READY to value 0, and
substitute the `PLAYER%d` label with `UNUSED`, or the translated `BLOCKED`
truncated to 30 bytes when the slot class byte is `4`. While walking slots,
the heartbeat tracks the minimum ping value; when a host flag is set and that
minimum is below the host's stored value, the minimum is written back into
the host player record and a notify runs. The battleroom heartbeat runs while
`LOUNGE2.GUI` is up and is a front-end state synchronizer, not unit AI;
frontend wrappers handle View Map/COMMANDER/start-button flow and flip to
mission loading on Start.

### Supported inference

Lobby state should be modeled separately from active simulation state, with a
clear transition that freezes front-end editing, resolves the final map and
schema, and enters the multiplayer synchronization phase. Parts of the
battleroom slot-label table derived only from label string ownership remain
supported inference, not established fact.

### Unknown

Exact serial/modem UI validation, lobby timeout progression, map-preview
camera behavior, the role separation of shared player-word bit `0x20`
between READY display and map-control authority, and the complete
ready/start protocol are not established here. Slot classes, ready
propagation, map-control gating, UNUSED/BLOCKED substitution, and
minimum-ping write-back are established above.

## Missing and unknown

* The full Win32/internal token table and all OEM aliases are established
  (§2); what remains is the complete battle/front-end consumer census —
  which input paths handle which tokens beyond the hotkey dispatcher's cases —
  and the unsupported-device census.
* Mouse motion-record consumers and downstream button-record ownership are
  closed: the motion record lands in the presentation object's fixed
  current-pointer slot, the frame input pass polls it (queue record when the
  button ring is non-empty, motion slot otherwise) into the canonical pointer
  record, and the pointer update, GUI hit tests, and click/drag dispatch
  consume it. Middle-button/wheel default processing and the reserved-slot
  queue refusal are established.
* Text-input code page and IME behavior (`TODO(T23)`: presentation-level
  platform detail, no retail contract observed beyond the ASCII token set),
  chat commit-versus-cancel semantics on every send route including
  terminator inclusion, and scrollback drain ownership in the in-battle HUD
  (CF_TEXT-only clipboard, paste/truncate bounds, the chat `+` vocabulary —
  `+<digit>` custom recipients, `+a`/`+e` recipient modes, and the
  mask-8-only dispatch table entries `plan`/`weight`/`limit` that no chat
  route matches — are established).
* GUI mode/flag-bit naming: `0x800` (extra redraw on close) and `0x1000`
  (modal centering) are mechanically named; the complete per-dialog
  Escape/Enter/focus-restoration matrix is authored data (per-GUI
  `escdefault`/`crdefault`/`defaultfocus` fields), not a hard-coded matrix;
  event bubbling and overlap/capture/association redirection precedence remain
  unknown.
* Listbox, scrollbar, font, and picture-box behavior beyond the observed
  common paths, and the complete widget callback map. The `.gui` grammar,
  field lengths, control defaults, control-kind parsing, and runtime
  control-kind dispatch are established; the callback targets are
  data-driven through the authored gadget association ids resolved by each
  window's handler, and the full per-window association census is open.
* Exact frontend transition-graph edges (successor/cancel/error per screen
  family), movie/intro/credits handling, load-failure restoration, and abort
  recovery (controller phase/substate mechanism, post-battle phases, planet
  briefing tables, and chat contract are established). The final
  process-level outcome of a missing or parser-rejected required `.GUI` file
  remains unknown even though its screen callers do not check the open result;
  malformed HATTFONT and malformed GAF payloads whose decoders return null
  likewise remain open.
* Complete HUD side-anchor to draw/hit-test consumer mapping (the resource
  anchors' consumers are established in §6 — text, `0` literals, current
  values, production/consumption, and bar fills in the composer's side record
  — the remaining anchors' consumers stay open), and slide/modal combinations
  (anchor list, tuple order, slide animation, and frame-composition passes are
  established). The malformed non-null GAF payload outcome remains unknown.
* Exact battle HUD optional-asset fallback behavior beyond the closed
  `intgaf` panel entries, side fonts, authored GUI page, page GAF, support GAF,
  and common-button resolution above.
* Full FNT text wrapping, drop-color defaults, code-page behavior
  (`TODO(T23)`), language-specific font fallback order beyond the closed
  missing-HATTFONT-to-active-FNT path, and translation-table missing-key rules
  (truncate-before-clip and the presentation-context drop-shadow switch are
  established). The GAF-font loader's capital-I YOffset normalization is
  established; malformed HATTFONT handling and the button text-pen formula
  are `TODO(question)` (§5).
* Complete translation lookup and missing-string fallback behavior.
* Exact cursor hotspots (established: the GAF frame's authored offsets),
  remaining command-specific validity rules, and cursor
  handle slot 0 identity (latch-value table, cursor index table, build-site
  validity cursors, and the queue-overlay color pairs 3/10 and 1/9 are
  established; the overlay color-map entries are closed in [R-P0-11 §3]).
* Picking hull geometry and fog gates are closed: the hull is the unit's
  transformed bounding box projected as a four-corner quad with the half-height
  shear, hit-tested as a polygon; the pick walks the sensor-built hot-unit
  lists (units only, so units win over features by construction); the
  visibility gate is the word-grid bit `1 << (localPlayer & 0x1F)` versus the
  per-viewer byte grid. What remains is the per-viewer byte grid's writer
  semantics on the visibility side (doc 03's surface).
* Selection overlap pick order — drag endpoints sorted independently, inclusive
  `min <= x <= max` tested per axis, stable pool sweep with strict `<` distance
  tie-break favoring lower slot [P1-14] — and fog word bit versus byte
  distinction, toggle versus commit modifier styles (`GetAsyncKeyState`-style held
  query for world commit versus drag word bit 2), and mixed-selection gate as
  AND across the selected set [P1-14] are established; repeated group-recall
  never centers the camera (whole-image bounded-negative), and selection flag
  `0x80000000` is reader-only — no writer exists in the image, so the `CTRL_F`
  filter branch is unreachable from retail's own code.
* Build cancellation and refund — tail-most matching walk with tombstone bit
  that skips `TargetCleared`, with `BuildWeapon` and `SelfDestruct` always
  tombstoned because the tombstone compares against the front anchor regardless
  of segment [P1-14] — and queue-modifier mapping (Replace purges unprotected,
  Append and Shift-queue both insert after the active marker without purging,
  Internal-Auto is the pump's head-insert) plus attack-ground versus
  unit-target discrimination (ATTACK picks unit when hit and hostile, otherwise
  ground; BLAST always ground) [P1-14] are established; factory product clicks are a separate counted producer
  (+1/+5/-1/-5 with the no-purge insert hard-set, tail-most cancellation, and
  the `+%d`/`%d +%d` queue-count label) — [R-P0-11 §1, §2]. The off-button
  latch writers are closed: MOBILEBUILD is armed by the build-button click on a
  `BMcode`-zero product, and TELEPORT has no writer in the image (consumer-only
  switch key).
* Order-class semantics for immediate and special commands are established via
  the fixed dispatcher chain STOP into ATTACK, BLAST, DEFEND, REPAIR, PATROL,
  RECLAIM, CAPTURE, UNLOAD, LOAD or PICKUP alias, and default MOVE with gate
  check, and latch values for TELEPORT and MOBILEBUILD as off-button consumers
  of the same 14-entry table [P1-14]; the TELEPORT arming site does not exist
  (bounded-negative whole image) and MOBILEBUILD's is the build-button click;
  minor OEM aliases are closed (§2).
* Build-page patching and rebuild timing — page-number bit encoding
  `(page & 7) << 23` cleared by mask `0xFC7FFFFF` with bit 22 paged indicator
  cleared by `0xFFBFFFFF`, and page-count guard are established [P1-14];
  factory completion marks the HUD dirty when the completing or produced unit
  is selected; the product-slot count is six (gadget slots 4..9, chosen by the
  authored button byte).
* Alternate minimap drag/current-camera branch boundary vectors and mode-bit
  truth table are established (drag branch: camera plus viewport-clamped mouse
  minus origin; lens branch: `(mouse − origin)·play/radar`; branch bits 0/1 and
  the derived bit 2); unusual-domain camera bounds (view exceeding map on an
  axis) remain open; terrain-height projection and dirty-field semantics are
  established.
* Minimap/radar colors and art sources are established (regular unit blips use
  an owning-player frame selector into `radlogohigh`, commander markers use
  frame 0 of `nuclogo`, and feature contacts use the same selector into
  `h2oboom2`; selected GAF bytes are active `PALETTE.PAL` indices; circles
  entry 10; jam 12; rings 15; dot 14; contacts per tick, MAPPED per dirty,
  blink every 8 host frames); start-position markers remain doc 03's item.
* Running-display refresh cadence and hover ownership are closed: the pointer
  update (hover, cursor shape, placement validity) runs once per host frame as
  the battle frame handler, and the 30-entry fixed-stride scrollback ring and
  heartbeat drain are established.
* Multiplayer pause authorization, speed UI synchronization, chat/pause
  packet forwarding authority, and the role separation of shared player-word
  bit `0x20` between READY display and map-control gate (both consumers
  proven; unified semantics not separable statically).
* TCP/modem/serial setup semantics, timeout rules, and map-preview behavior.
* Campaign continuation timing and the endgame per-value bar-fill animation
  mechanism (message-box surface, header strings, per-player stat rows, and
  the seven-category +10-tick statistics cycle are established; the per-value
  easing may ride the type-13 timed/range gadget path — not separately
  closed).
