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
logical space and then presented to the display surface. The front end always
runs at 640×480 ([R-FE-02 §2]); the battle chrome is neither scaled nor
letterboxed at a larger mode but extended by the per-element rules of
[R-HUD-05].

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
at a time — re-measuring each iteration — until the text fits or only one byte
remains. **Established:** that final byte remains even when its glyph is wider
than the control. Paste does not change the caret index; a shorter replacement
can therefore leave it beyond the new visible text. The subsequent widget
repaint measures the caret prefix but does not relocate the index. This differs
from focus setup, which places the caret at the text end [R-WGT-01 §12].

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

### The host frame: where input becomes simulation state [R-CAM-01 §1]

**Established fact — one host frame, in order.** The battle frame handler
(the pointer/cursor update of §8, installed as the mode's frame function)
runs the following, in this order, once per host frame:

1. **Pointer classification** — the canonical pointer record (§2 "Mouse
   records") is classified against the minimap rectangle and the view
   rectangle, producing the pointer's world position and the region bits
   consumed by the click paths (§10 [R-CAM-01 §11]).
2. **Click dispatch** — the record's message number (`0x201`/`0x202` left
   down/up, `0x204`/`0x205` right down/up) selects the world-click, drag-
   rectangle, minimap-latch, or cancellation path (§9, and [R-CAM-01 §5] for
   the `LEFTCLICK` polarity). World clicks that produce orders enter the order
   dispatcher **here**, before this frame's simulation ticks.
3. **Outer frame** (the battle host pump): the tick-budget step of
   [01 §4.3] runs first and, as a by-product, stores the **raw wall-clock
   delta** — the scaled `GetTickCount()` reading of §2 (`GetTickCount() ×
   timeScale / 1000`) minus the previous frame's reading — that the scroll
   pass below consumes. Then every runnable sub-tick of [01 §4.4] executes,
   phase 10 (follow camera and shake, §10) included, once per sub-tick.
4. **Hotkey dispatch** — exactly **one** keyboard token is popped from the
   30-slot ring and dispatched (the census in [R-CAM-01 §2]). A second token
   waits for the next host frame.
5. **Scroll pass** — the host-frame camera writer of [R-CRD-006 §1] runs
   once, with the delta from step 3 ([R-CAM-01 §10]).
6. **Hover refresh, then HUD composition** — the pointer update recomputes
   hover state, and the composer draws the frame.

Keyboard auto-repeat needs no policy of its own: the window procedure
enqueues one token per repeated `WM_KEYDOWN` (§2) and step 4 drains one per
host frame, so a held key repeats at the OS repeat rate bounded above by the
frame rate, with at most 29 tokens backlogged.

Steps 4 and 5 are skipped entirely while the in-game options window is open
(the battle-interface ESC bit of §11); steps 3–5 are skipped in favour of
the GUI pump while a modal front-end window has focus. In single-player
(non-network) mode, step 3 also skips the budget step — and therefore does
**not** refresh the raw delta — while the pause bit is set: edge and keyboard
scrolling while paused reuse the last delta computed before the pause, which
is the delta of the frame in which pause was pressed.

**Established fact — the presentation/simulation seam.** Everything above
the line is presentation: the camera origin, the follow target, the shake
state, the pointer record, the hover list, the chat overlay, the message
ring, and the interface options are host-frame state that no simulation
phase reads back into unit state. The inputs that *do* become simulation
state are: orders produced by world clicks and by the command palette
(dispatched in step 2 of the frame in which the click record is consumed —
so they are visible to the **next** frame's sub-ticks, never to sub-ticks of
the frame that produced them); the hotkey actions of [R-CAM-01 §2] that call
the order dispatcher (self-destruct, group assignment writes the per-unit
group word, selection writes the per-unit selected bit — selection and the
selectable/alive status bits are the same words the trigger system tests
[08 R-TRIG-01 §5]); pause and game-speed changes, which write the speed
state of [01 §4.3] in step 4 and are therefore consumed by the following
frame's budget step; and chat `+` commands, which run their handler in the
chat commit callback of §5 (a GUI-pump callback, i.e. before step 3 of the
next frame). The raw wall-clock delta feeds only the tick budget and the
scroll pass; the only wall-clock value that leaks into authoritative state
is the hover bob of [01 §7.4], which is a separate sampler.

### The battle hotkey census [R-CAM-01 §2]

**Established fact.** The battle hotkey dispatcher pops one token (§2 token
model) and switches on it. Shift, Ctrl and Alt are **held-key queries**
(`0xF9`, `0xFA`, `0xFB`) made at dispatch time, not part of the token, except
that Ctrl composition is already folded into the token by the translator
(`Ctrl+A..Z` → `0xAA..0xC3`, `Ctrl+0..9` → `0xC4..0xCD`, `Ctrl+F1..F12` →
`0xCE..0xD9`) and Shift reaches the dispatcher as the shifted `WM_CHAR`
character for printable keys. "Own selectable unit" below means a unit in the
local player's slot range whose status word has the selectable bit set, whose
build-progress fraction is `0.0`, whose post-capture grace counter is zero
([R-WGT-01 §10]), and whose carrier reference is either null or itself
marked as a visible carrier — the same predicate the rectangle selection of
§9 uses and the trigger system's eligible-unit predicate [08 R-TRIG-01 §3]. "Cue" means
the named sound cue played through the interface sound path.

| Token | Key | Action |
|---|---|---|
| `0x09` | Tab | In battle mode (mission-mode word `3`) with chat inactive: toggle `TABMENU.GUI` (§11). In any other mode Tab falls through to the F2 case below. |
| `0xE3` | F2 | Shift not held: if the options window is not open, open `ARMOPT.GUI` and set the ESC bit (§11). Shift held: retain the hovered unit for the **Unit Builder Probe**, or disable that probe when no unit is hovered. This arming path has no developer gate; drawing is a separate, unrooted path ([R-CAM-01 §9]). |
| `0x0D` | Enter | `SmallButton` cue; open chat (§5 "Chat"). |
| `0x1B` | Escape | Options window open: close it and clear the ESC bit. Otherwise, if the armed-order latch is idle (`1`): deselect everything (the `deselect all` path also runs the selection-changed refresh); if a latch is armed: return it to idle, clear the Shift-latch persistence bit, and reset the palette's default control. |
| `0x21` `0x23` `0x2A` `0x60` `0x7E` | `!` `#` `*` `` ` `` `~` | Toggle the persistent "label every unit" bit (interface-flags byte bit 0) and write all settings to the registry. The composer reads it: with the bit set every on-screen own unit gets its unit marker and its group digit; with it clear only grouped units get the digit. |
| `0x2B` `0x3D` | `+` `=` | Game speed up by one ([R-CAM-01 §3]); refused in developer film mode, for a watching player (the same player-record bit that gates `SHARE.GUI` and the Tab menu's diplomacy gadgets — *Supported inference* on the bit's name), and when the speed is already `20` (`> 19` test on the unsigned target word). |
| `0x2D` `0x5F` | `-` `_` | Game speed down by one; refused under the same gates and when the speed is below `2`. |
| `0x2C` | `,` | Previous build page of the current build-menu unit (`nextbuildmenu` cue) [R-P0-11]. |
| `0x2E` | `.` | Next build page (`nextbuildmenu` cue). |
| `0x31..0x39` | `1..9` | Build-page / group-recall mux under the `SwitchAlt` option ([R-CAM-01 §4]); group recall takes Shift as its additive argument and plays `SelectSquad`. |
| `0x54` `0x74` | `T` `t` | Set the follow-camera tracked object to the **previous** (`T`, Shift held) or **next** (`t`) selected unit after the current tracked object in unit-slot order, wrapping within the local slot range; with nothing selected the tracked object becomes null ([R-CAM-01 §12]). |
| `0x5C` | `\` | Developer mode only: re-run the last `+` command ([R-CAM-01 §9]). |
| `0x68` | `h` | Battle mode, non-watcher: open `SHARE.GUI` (resource sharing, doc 05). |
| `0x6E` | `n` | Find the next own unit not yet visited by this cycle (per-unit visited bits `0x40`/`0x80` of the status word), glide the camera to it ([R-CAM-01 §12]), record it as the current unit word (a HUD word — *Supported inference* on its reader) and mark it and every on-screen own unit visited; it does **not** change the selection. When every unit has been visited, clear the visited bits on all units and restart. |
| `0xAA` | Ctrl+A | Select every own selectable unit (additive over the current selection), clear the current build-menu unit, `selection changed` refresh. |
| `0xAC` | Ctrl+C | Select the own selectable units whose definition is in the authored `CTRL_C` category set (replacing the selection unless Shift is held), then set the follow-camera tracked object to the last own unit in the `Commander` category set — the camera follows the commander ([R-CAM-01 §12]). |
| `0xAD` | Ctrl+D | Self-destruct: resolve the `SELFDESTRUCT` order descriptor; for every selected unit that carries that button, fire the button's script path; if no selected unit had the button, issue the order through the order dispatcher for the selection. |
| `0xAB` `0xAE..0xBB` `0xBD..0xC2` | Ctrl+B, Ctrl+E..Ctrl+R, Ctrl+T..Ctrl+Y | Category select: format `CTRL_%c` with the uppercase letter and select the own selectable units whose definition is in that category set (Shift adds to the selection, otherwise units outside the set are deselected). Stock content authors `CTRL_B`, `CTRL_C`, `CTRL_F`, `CTRL_M`, `CTRL_P`, `CTRL_R`, `CTRL_V`, `CTRL_W` (asset census, 278 FBI files); every other letter selects nothing. |
| `0xBC` | Ctrl+S | Select every own selectable unit in the on-screen list (§8) — replaces the selection; no-op refresh when the list yields none. |
| `0xC3` | Ctrl+Z | Select every own selectable unit whose definition id matches any currently selected unit's definition (a 256-bit definition mask built from the selection). |
| `0xC5..0xCD` | Ctrl+1..Ctrl+9 | Assign group `1..9` to the selection (§9 "Control groups"); `CreateSquad` cue. Ctrl+0 (`0xC4`) has no case. |
| `0xD2..0xD5` | Ctrl+F5..Ctrl+F8 | Store camera bookmark `0..3` = current camera origin, mark it valid; `SelectSquad` cue. |
| `0xE6..0xE9` | F5..F8 | Recall bookmark `0..3` ([R-CAM-01 §12]); `SelectSquad` cue. Recalling an unwritten bookmark loads whatever the (zero-initialised) slot holds. |
| `0xD6` | Ctrl+F9 | Screenshot — consumed by the input pass before the dispatcher (§2 "Input ordering"). |
| `0xD7` | Ctrl+F10 | Developer mode only: start/stop the movie capture series ([R-CAM-01 §8]). |
| `0xE2` | F1 | Shift not held: open `UNITINFOx.GUI` for the hovered unit (or the build button's product when a build button is hovered) — the unit-info panel of §6. Shift held: retain the hovered unit for the **Unit State Probe**, or disable that probe when no unit is hovered. This arming path has no developer gate; drawing is a separate, unrooted path ([R-CAM-01 §9]). |
| `0xE4` | F3 | Clear the "last jumped-to" bit (`0x20`) on all thirty message-ring records, then glide the camera to the first message whose source unit is alive and whose visited bit (`0x10`) is clear, setting both bits on it; if none, clear the visited bits and retry once ([R-CAM-01 §12], [R-CAM-01 §14]). |
| `0xE5` | F4 | Toggle interface-flags bit `0x80`. Its two readers are presentation: the HUD side-panel slide treats the bit as "Space held" (the panel stays extended while it is set), and the kill announcement path arms two 30-frame counters (killer's player index, victim's side) on each kill only while the bit is set. The counters' visible effect is closed in [R-CAM-01 §14] (the killer's `Kills` and the victim's `Losses` number flash bright and fade to row 0 over half a second on the pinned panel). **Unknown:** the user-facing name alone — no string in the image names the bit, and nothing observable turns on it. |
| `0xEC` | F11 | Developer mode only: toggle film mode ([R-CAM-01 §9]). |
| `0xED` | F12 | Clear the message ring (producer and display indices both reset to zero). |
| `0xF8` | Pause | Toggle the local pause bit and emit packet `0x19` with sub-kind `0` and the new bit ([01 §4.3]). |

Tokens with no case (including `0x20` Space, digits with Ctrl+0, and every
`0xF0..0xF7` navigation token) are dropped by the dispatcher; the arrow
tokens are never dispatched at all — scrolling uses the held-key queries
in the scroll pass, not the ring.

**Established fact — Escape versus F2.** Token `0xE3` is **F2** under the
translator table of §2 (F1..F12 → `0xE2..0xED`); Escape reaches the
dispatcher as the `WM_CHAR` value `0x1B`, whose case is the cancel/close
chain above. The options window is therefore opened by F2 (or Tab outside
battle mode) and closed by either F2 or Escape. The "ESC bit" of the
battle-interface state byte is so named because it is the bit Escape clears.

### The game-speed hotkey and its announcement [R-CAM-01 §3]

**Established fact.** Speed changes from the hotkeys, the `GAME` slider of the
interface options ([R-CAM-01 §7]) and the `+`-command path all go through one
setter taking the requested speed and a "send" flag:

```
s = requested; if (s > 20) s = 20; if (s < 1) s = 1      (signed compares)
if (s != targetSpeed) {
    text = (s == 10) ? translate("Game Speed Normal")
                     : sprintf("%s  %c%d\n", translate("Game Speed"),
                               (s - 10 > 0) ? '+' : ' ', s - 10)
    post text to the message ring, kind 2, no unit, silent ('\n')
}
targetSpeed = currentSpeed = s          (both 16-bit words)
if (send) emit packet 0x19 sub-kind 1 with byte s
```

The hotkey path passes `send = 1` and computes `requested` as the target
word ±1; the announcement therefore prints the **offset from normal**
(`Game Speed  +3`, `Game Speed   -2` — a space precedes negative values,
whose sign comes from `%d`). The speed state and its adaptation are
[01 §4.3]; this section supplies the clamp bounds and the announcement.

**Established fact — the slide strip's speed line.** The HUD's own speed
line is a separate formatter in the composer: `%s %s` of the translated
`Game Speed` key and either `Normal` or `%+d`, with ` (%+d)` appended while
the adapted current speed differs from the target — no colon follows the key
([R-HUD-04 §4] has the exact formats of all three strip lines). Both `%+d`
arguments are the **offset from normal**, formed the same way the
announcement forms its own: the 16-bit speed word is widened without sign
extension and 10 is subtracted, and the 32-bit difference is the argument.
So `Game Speed +3` at target 13 and `Game Speed -2` at target 8, never
`+13`/`+8`. The two words are read separately — the `Normal`-versus-`%+d`
branch tests the **target** word against 10, and the suffix tests the
**adapted current** word against the target and, when they differ, appends
` (%+d)` of the current word minus 10 to the string the branch already
produced. The suffix is appended after the `Normal` branch joins, so
`Game Speed Normal (+2)` is a reachable string while the adaptation is above
a target of 10.

### `SwitchAlt` [R-CAM-01 §4]

**Established fact.** `SwitchAlt` is a persistent interface option: registry
value `SwitchAlt` (DWORD, absent → `0`; bit 0 kept) stored as bit 8 of the
interface-flags word. The digit-key gate of §9 tests this bit, not a
battle-mode flag; the gate is

```
switchAlt = SwitchAlt option bit
alt       = Alt held (0xFB)
if (switchAlt == alt) -> build page (digit - 1) of the current build-menu unit
else                  -> group recall(digit, shiftHeld) + SelectSquad cue
```

i.e. by default digits pick build pages and Alt+digit recalls groups; with
`SwitchAlt` set, digits recall groups and Alt+digit picks build pages. The
build-page branch is a no-op (no cue) when there is no current build-menu
unit or the digit exceeds its page count. The bit has no other reader in the
image. It is set by the registry loader, by the interface options' write-all
path, and by the chat command `+SwitchAlt` — with no argument it toggles the
bit and writes the registry; with an argument it stores `arg & 1` without
writing ([R-CAM-01 §6]).

### `LEFTCLICK`: mouse-button polarity [R-CAM-01 §5]

**Established fact.** The interface options' `LEFTCLICK` two-stage button
(`Left Click|Right Click`, `SPEEDS.GUI`; the `Button Interface` label) writes
a dword, persisted as registry `Interface Type` (absent → `0`), also set by
`+IFace n`. Its value gates the click dispatch of the frame handler and the
world-click cursor resolver:

* **`0` (`Left Click`, default)** — the polarity §9 documents:
  left down over the world starts the drag rectangle; left down over the
  minimap issues the armed order / world click at the minimap's world point
  (the hover conversion of [R-CAM-01 §11]); right down with an order armed
  returns the latch to idle wherever the pointer is; right down over the
  world view with **Ctrl** held starts the cursor-warp drag-scroll mode
  ([R-CAM-01 §11]), without Ctrl it deselects; right down over the minimap
  sets the minimap-latch bit (a right-drag on the minimap pans the camera).
  The latch is released by right up (`0x205`).
* **`1` (`Right Click`)** — left down over the world still starts the drag
  rectangle and left down over the minimap sets the **minimap latch** (camera
  jump every frame while held, released by left up `0x202`); left click on
  empty ground with the latch idle **deselects** (the world-click handler's
  extra case). With the latch idle, right down over the world with the
  pointer's region bit 2 set issues the contextual order at the pointer's
  world point through the order dispatcher. Armed orders still fire on left
  down; right down while a non-idle order is armed cancels it and returns the
  latch to idle.

**Established (direct-static) — the Type-1 cursor column is closed.** The
alternate column changes only the idle-latch row; every armed latch uses the
same capability-gated shape row as Type 0. For each selected acting unit, an
own selectable finished target gives `cursorselect`, a hostile unit gives
`cursorred`, and any other unit gives `cursorgrn`. With no unit target, a
reclaimable feature gives `cursorgrn` when the actor has either
`canresurrect` or `canreclamate`; otherwise the result is `cursornormal`.
These tests are ordered as written. The
hostile/friendly colour rows do not test whether the actor can perform the
contextual order; the order dispatcher applies its own capability gates when
the right click arrives. With no selected acting unit, the chooser retains
its ordinary idle fallback: `cursorselect` over an own selectable finished
unit, `cursornormal` otherwise. Section 8 gives the shared reduction and the
armed-latch rows.

### The chat `+` command vocabulary [R-CAM-01 §6]

**Established fact — the vocabulary.** The message-builder registers three
AI tuning commands (`plan`, `weight`, `limit`, mask 8) and the **battle entry
orchestrator** registers three more tables into the same sorted command
vector and installs the default handler; 83 commands are dispatchable from
chat. The inline `+<digit>`/`+a`/`+e` mini-language of §5 "Chat" runs
**after** the command dispatch on the same text.

**Established fact — dispatch mechanics.** A `+` line is copied (at most 79
bytes) into a persistent last-command buffer, tokenised into up to 20
whitespace-separated words (a `#` ends parsing even inside a word, a semicolon
is ordinary word content, words keep their case, and the shared word storage
is 126 bytes including terminators), and the first word is looked up in the
command vector by **case-insensitive** binary search. The entry's route mask is ANDed
with the caller's route word: on a nonzero result the entry's handler runs and
the entry's mask is returned; otherwise, if a default handler is installed
and its mask matches, the default handler runs and its mask is returned;
otherwise `0`. The chat route word carries bit 1 always, bit 2 when the
entry-time cheat word is set — skirmish `1`, campaign `0`, multiplayer the
host's `Cheat Codes` bit ([08 R-OOS-01 §2] for the word's one writer and one
reader, [08 R-OOS-01 §5] for the gate stated per kind; `Cheat Codes` as a
game option is a multiplayer lobby word [08 R-SKIR-01 §11]) — and **both
bits 2 and 4** in developer mode (route word `7`, [R-CAM-01 §9]). Mask-1 commands are therefore live in every session
kind; mask-2 commands do **not** dispatch in campaign outside developer
mode. After dispatch the line — including the `+` — is still sent as
ordinary chat; when the returned mask has bit 2 the outgoing recipient mode
is forced to `0` (everyone), so a cheat is broadcast to all players.
**Unknown (out of scope):** how the multiplayer receive path applies the
lobby bit before re-dispatching a received `+` line.

Handlers read word *n* as text or as its integer value: the longest signed
decimal prefix is converted with 32-bit `atoi` semantics, trailing bytes are
ignored, and an absent word or one without a digit prefix reads as `0`.
`flags` below means the mode-flags word that also
holds the developer bit; `interface flags` the word of [R-CAM-01 §4]; `render
flags` the terrain-render flags word; "write settings" the registry write-all
path. Player-slot arguments are valid when `0..9`, the slot is occupied, its
controller kind is `1..3` and its side byte is not `10`.

**Mask 1 — settings and information (43):**

| Command | Effect |
|---|---|
| `NoShake` | toggle flags bit 4 (camera shake suppression) |
| `Contour` | word 1, word 2 as floats × 256, truncated, into the two contour-line parameters |
| `ScrollSpeed n` | scroll setting byte = `n` (low byte); write settings ([R-CAM-01 §10]) |
| `IFace n` | `Interface Type` = `n`; write settings ([R-CAM-01 §5]) |
| `Give p n metal` / `Give p n energy` | valid slot `p`: transfer `n` (as a float) of the named resource from the viewing player to slot `p` through the sharing transfer of doc 05 (word 3 compared case-insensitively) |
| `CDPlay n` / `CDStop` | CD audio track play / stop (doc 03 audio) |
| `Sound3D` | toggle the live 3D-sound state of the audio device; invoke settings write-all, which serializes the unchanged packed `SoundMode` (see below) |
| `Shading` `AntiAlias` `Shadow` | toggle interface bits `0x20`, `0x02`, `0x04`; rebuild the terrain renderer; write settings |
| `Dither` | toggle interface bit `0x40`; write settings |
| `SwitchAlt [n]` | [R-CAM-01 §4] |
| `TShadow` `FShadow` | toggle interface bits `0x08`, `0x10` (no write) |
| `LOSType` | toggle render-flags bit 2; refresh the visibility presentation |
| `Light a b c` | replace the global model light vector from three signed integers, then invalidate shared model images ([03 §2.4.1]); no settings write |
| `RCache` | invalidate shared model image allocations; subsequent draws rebuild as required without resetting retained pose ([03 §2.4.1]); no settings write |
| `Selectable` | set the selectable bit on every unit whose status word has bit 28 (alive) set |
| `MusicMode n` | store the signed desired music category through the category-change fade/delay path, without changing playback mode or writing settings [03 R-AUD-01 §4] |
| `Logo n p` | valid slot `p` and `0 ≤ n <` logo count: slot `p`'s logo byte = `n`; renderer rebuild; otherwise post `Invalid logo setting` |
| `ScreenChat` | toggle the screen-chat dword; write settings |
| `Gamma n` | gamma = `n × 0.1` into the display; store `n`; write settings |
| `Clock` | toggle flags bit 6 (clock display); write settings |
| `NetStats` | reset the network statistics block |
| `Sing` | toggle the "sing" flag read by the unit-chat voice path ([R-CAM-01 §7]) |
| `NoMetal` / `NoEnergy`; `NoMetal p n` / `NoEnergy p n` | command-only form writes `0` to the local player's metal / energy stock; otherwise the first argument is player `p` (low byte) and the second is integer `n`, converted to a float for the stock assignment |
| `BigBrother` | toggle a camera-flags bit; when set, write `1` to a companion camera word; when cleared, cancel the follow target ([R-CAM-01 §12]). The companion word is the 90-tick cycle counter of the unit sweep tail, paused while Shift is held ([R-CAM-01 §12], [04 R-MOV-03 §1]). |
| `Now Film Chris Include Reload Assert` | exactly six words: command name matched case-insensitively, the five arguments matched case-sensitively as shown; set the developer bit on a match, otherwise clear it ([R-CAM-01 §9]) |
| `Drop n` | flags bit 0 = (`n == 0`) |
| `ShootAll` | toggle flags bit 10 |
| `ShareMetal` `ShareEnergy` `ShareMapping` `ShareRadar` | network mode only: toggle the local player's share bits (`2`, `4`, `0x20`, `0x40`), post `Toggled ShareX to: ON/OFF`, resend the player record (doc 05 [R-SHARE-01]) |
| `ShareAll` | the four toggles in sequence |
| `ShowRanges` | toggle the range-ring overlay dword (§6 [R-P0-11 §3]) |
| `SetShareMetal n` / `SetShareEnergy n` | network mode only: share threshold = `n` when `n` is not above the storage capacity, else the capacity; post `OK.  Will share metal if above %d` |
| `Compression` | network mode only: toggle outgoing packet compression; post `Ok.  Outgoing packet compression turned ON/OFF` |
| `BPS` | toggle the bytes-per-second display dword |
| `SFX` | toggle the sound-effects debug byte |

**Established fact — `Clock` persistence and stand-alone painter.** The
command ignores extra words, flips only bit 6 of the session option word and
immediately invokes the common settings writer. Startup reads the `clock`
DWORD with a missing-value default of zero and copies only its low bit into
that option bit. No battle-entry reset, session-kind gate or battle-save field
intervenes, so this is a persistent presentation preference shared by campaign
and skirmish battles.

The master battle composer tests only that option bit and redraws the clock on
every composed host frame. It reads the unsigned 32-bit global tick and forms
`hours = tick / 108000`, `minutes = (tick % 108000) / 1800`, and
`seconds = (tick % 1800) / 30`; hours are cumulative and do not wrap. The
translated `Game Time` key is formatted as `%s : %02d:%02d:%02d` and drawn
without backing art at absolute `x = 130`, `y = screenHeight - 34 - fontHeight`,
in `dcb[15]`, with the current skip colour, no outline and no control-width
limit. The painter runs after the bottom slide strip and the optional developer
rate overlay, and before the network indicator and linked GUI-window painter;
those later windows may cover it.

The FNT is the stateful current selection at that point. With a nonzero
`textlines` value, the earlier message-column pass has selected the primary
`fonts/COMIX` FNT even when the ring is empty. When that value is zero the pass
returns before selecting a font, so the clock inherits the local side's
console FNT (unless an enabled developer overlay has selected COMIX in
between). This painter is independent of §6's Space-held bottom strip: that
other clock is written in GAF slot 1 over `LIGHTBAR`, at the clip-relative
strip coordinates, and only while the slide offset is nonzero. Both may draw
in the same frame.

**Established fact — `Sound3D` device state and settings.** The command reads
the audio device's live 3-D flag, flips that flag, and then invokes the common
settings write-all. It does not change the separate packed `SoundMode` setup
field, so the write serializes that field unchanged rather than persisting the
new live device flag.

**Established fact — resource-setter argument forms.** `NoMetal` and
`NoEnergy` inspect the total token count. A vector containing only the command
name selects the local player; every longer vector reads the first argument,
narrows it to its low byte, and uses that byte as the player slot. Both forms read the second argument as the amount, so it is
the absent-value `0` in the command-only form and also when a line supplies
only a player. The shared integer reader above provides signed decimal-prefix
conversion, after which the amount is converted to single precision and
assigned directly to the stock. The narrowed player byte is accepted only
when below `10`, with an occupied record, controller kind `1`, `2`, or `3`,
and side byte other than `10`; a failed check leaves the stock unchanged.

**Established fact — `Logo` arguments and failure.** The first argument is a
signed integer and must be nonnegative and less than the unsigned frame count
of the `32xlogos` entry in `textures/logos.gaf`. The second argument is narrowed
to its low byte before the player-slot validation above. Success stores the low
byte of the first argument in that player's logo field, rebuilds the renderer,
and performs no settings write or success diagnostic. Every failed check posts
the exact text `Invalid logo setting` with class 2, source unit 0, and speaker
slot 10. Missing arguments retain the shared zero default and extra arguments
are ignored.

**Established fact — viewing and control.** `View` changes the viewing slot,
without changing the true-local command owner or requesting a visibility
refresh. Its player argument is narrowed to the low byte before the common
player-record validation. `Give` uses that viewing slot as its source, narrows
its destination argument to the low byte, converts the second integer argument
to single precision, and delegates to the resource transfer of doc 05. Negative
amounts retain that helper's signed behavior. Neither command writes settings.
The top resource strip retains its previous displayed stocks and rate latches
across a viewing change; it does not reset them. Cursor actor selection remains
true-local, while visibility and status-caption ownership use the viewing slot
([R-HUD-03 §4], [R-HUD-03 §14.1], [04 R-P0-08-B §1]).

**Mask 2 — cheats (10):**

| Command | Effect |
|---|---|
| `Radar` | toggle flags bit 9 (full radar) |
| `ATM` | local player: metal += `1000.0`, energy += `1000.0` (float adds, no cap) |
| `View p` | valid slot `p`: the viewing player index = `p` |
| `LOS` | toggle live render-flags bit 1; refresh visibility presentation; invoke settings write-all, which serializes the unchanged setup record (see below) |
| `Mapping` | toggle live render-flags bit 0 (mapped); refresh; invoke settings write-all, which serializes the unchanged setup record (see below) |
| `DoubleShot` | toggle flags bit 7 |
| `HalfShot` | toggle flags bit 8 |
| `NowISee` | clear render-flags bits 0 and 1; refresh |
| `Meteor [n]` | one word: force-arm a storm immediately; `Meteor n`: set the scheduler's enable word to one when parsed `n ≠ 0`, otherwise zero, without changing the current storm (doc 06 §6.5) |
| `MakePoster …` | write a `BIGSHOT` capture into `<install>\screenshots` and reset the wall-clock base (argument grammar not traced — **Unknown**, static trace; developer tooling) |

**Established fact — visibility commands and setup persistence.** The four
visibility commands above change the live render-flags word and refresh
visibility presentation without copying those changes into the separate
skirmish setup record. `LOSType` and `NowISee` perform no settings write. `LOS`
and `Mapping` each invoke the common settings write-all after changing the live
word. That writer obtains `Mapping`, `LineOfSight`, and `LOSType` from the
unchanged setup record. The write can therefore preserve or restore the
authored setup triple rather than persist the command's new live visibility
state.

**Mask 4 — developer (30, plus the default handler):** `AI p` (toggle slot
`p` between AI and human control), `Control p q` (viewing/controlling
indices), `Kill [p]`, `IWin`, `ILose` (set the outcome bits and end the
battle), `Film name` (film recording flag and name), `FilmSpeed n`, `Assert` (no-op),
`Assign order x y` (issue a named order at a point), `BurnAll`, `BurnOne`,
`DebugBreak [1|2|3]` (allocation-exhaustion / divide-by-zero / break), `DPrint` (no-op),
`Edge w h` (play-area extents), `Include name` (run `debugdat\name.txt` as a
command script, one command per line), `Mem` (no-op), `MemDump` (creates or
truncates and closes an empty `memdump.txt`), `Move x y` (camera-jump family), `PrintWeights p file`,
`Profile` (toggle profiler display), `Reload unit` (reload one unit definition), `ReloadAIProfiles`,
`Save name` (write `savegame\name.sav` with the description `Generic Game
Description`), `SeaLevel n`, `Search x y r`, `SelBoxes` (flags bit 2),
`TreeDeath` (flags bit 3), `Feature name` (spawn a feature at the pointer),
`ZBuffer`. The **default handler** (mask 4) treats an unrecognised first word
as a unit definition name and spawns one unit per matching definition for the
viewing player at the pointer's world position, stepping the spawn point by
32 world units per unit and wrapping at the play-area edge. `+syncerr` is a
separate string with a network-only reader. None of these run outside
developer mode. The release-build stubs and profiler routing are detailed in
[01 R-PLAT-01 §9]; a registered name does not establish that its advertised
diagnostic exists. The remaining commands above are a vocabulary census,
not a complete implementation contract for their deeper effects.

### Interface options (`SPEEDS.GUI`) and their consumers [R-CAM-01 §7]

**Established fact — controls and storage.** The interface options screen
opens `SPEEDS.GUI` (or `SPEEDSRT.GUI` from the in-battle options) with the
`optinterface4x` art and binds:

| Gadget | Kind | Runtime max | Stored value | Registry key (absent →) |
|---|---|---|---|---|
| `GAME` | slider | `21` | game speed (target and current words) | `gamespeed` (`10`) |
| `SCREEN` | slider | `65` | scroll setting byte | `scrollspeed` (`32`) |
| `TXTSCROL` | slider | `20` | text-scroll seconds dword; label written to a gadget named `TEXTSCROLLTEXT` = `%d secs` — no such gadget is authored, see below | `textscroll` (`10`) |
| `MAXLINES` | slider | `30` | message-line count dword; label written to a gadget named `MAXLINESTEXT` = `%d`, or `None` when `0` — no such gadget is authored, see below | `textlines` (`10`) |
| `LEFTCLICK` | 2-stage button `Left Click|Right Click` | — | `Interface Type` dword | `Interface Type` (`0`) |
| `UNITCHAT` | 3-stage button `Off|Medium|Full` | — | unit-chat **text** level byte = stage × 5; displayed stage = byte ÷ 5 | `unitchattext` (`5`) |
| `RESTORE` | button | — | speed `10`, scroll `32`, text-scroll `10`, lines `10`, `Interface Type 0`, voice level `10`, text level `5` | — |
| `UNDO` | button | — | every value above restored from the copies taken when the screen opened | — |

The registry `unitchat` value (absent → `10`) is the unit-chat **voice** level
byte; it is edited from the sound options screen's `SPEECH` gauge, not here
([03 R-AUD-01 §2]). `SwitchAlt` has no gadget ([R-CAM-01 §4]).

The two slider read-outs are written by name to gadgets called
`TEXTSCROLLTEXT` and `MAXLINESTEXT`. Neither `SPEEDS.GUI` nor `SPEEDSRT.GUI`
authors a gadget of either name, so the setter finds nothing and the values are
never shown; the page's `GAMETEXT` label is authored empty and nothing writes
it. The screen therefore draws four unlabelled slider tracks.

**Established fact — slider value mapping.** Every slider callback computes
its value from the slider's knob position word `pos` and range word `range`
(the widget model of §4); the "range" word is the slider synthesiser's
**computed track length** (`travel`, [R-WGT-01 §5]), not the authored
`range` key ([R-FE-01 §6] traces the read-out and the position writer):

```
value = (range < 2) ? 0 : trunc( float(pos) / float(range - 1) * max )     x87 division then multiply, __ftol
GAME:     value < 1  -> 1 ; then the speed setter of [R-CAM-01 §3] (which clamps 21 -> 20)
SCREEN:   value <= 1 -> 1 ; stored as a byte (1..65)
TXTSCROL: stored as is (0..20)
MAXLINES: value < 0  -> 0 ; stored (0..30)
```

and the screen opener places each knob at
`ceil( float(value) * float(range - 1) * (1/max) )` where `1/max` is a
single-precision reciprocal constant (`1/21`, `1/65`, `1/20`, `1/30`): the
product is truncated, and one is added when the truncated value differs from
the product (an exact-integer test against `0.0`). The `GAME` slider is
inert for a watching player.

**Established fact — consumers.**

* **`textlines` (MAXLINES).** The message poster: when the count is `0` the
  message is **dropped** (no ring write, no `MessageArrived` cue). Otherwise,
  when `(producer + 1) mod count == displayIndex`, the display index advances
  first (dropping the oldest visible line); then the text is copied (64 bytes,
  forced terminator), stamped with the current tick, kind nibble, source unit
  and the speaker's player slot (`10`, which renders as `'\n'`, is the
  no-speaker sentinel, [R-HUD-03 §14.3]); the producer advances mod 30;
  `MessageArrived` plays only when the speaker slot is a real slot. The
  composer draws at most `count − 1` lines walking back from the producer to
  the display index ([R-HUD-03 §14.4]) — `count` is a modulus, not the
  on-screen budget. This is the ring of §11 "Status scrollback".
* **`textscroll` (TXTSCROL).** Once per host frame **after** the sub-tick
  loop (so it runs whether or not any tick ran), if the ring is non-empty and
  `messageTick + (seconds + 1) × 30 < currentTick` for the oldest displayed
  line, the display index advances by one (mod 30). A line therefore stays
  at least `(seconds + 1)` seconds of game time, measured in simulation ticks
  — game speed changes stretch it.
* **`unitchat` / `unitchattext` (UNITCHAT).** The unit acknowledgement path
  (order acknowledgements, doc 04 [R-ORD-01 §1]) plays the voice line only
  when `10 - voiceLevel < ackPriority` (signed), a voice exists, the voice
  argument is set and the sound-flags byte has bit `0x40`; it posts the text
  line, kind 1 with the unit's id, only when `10 - textLevel < ackPriority`
  and the unit is alive. With the `Sing` toggle set the voice path
  substitutes one of two fixed sound names on `tick / 30 mod 8`. Levels are
  bytes: `Off` = `0` (only priorities above 10 pass — none in stock content),
  `Medium` = `5`, `Full` = `10`. The producer is the **shared status
  emitter** of [04 R-ORD-01 §1], which owns the three-clause producer gate
  and the kind → (sound name, default text) table of 23 kinds whose
  priorities these two levels arbitrate.
* **`scrollspeed`** — [R-CAM-01 §10]. **`gamespeed`** — [01 §4.3].

### Movie capture series [R-CAM-01 §8]

**Established fact.** Ctrl+F10 in developer mode toggles the capture series.
Starting: scan `<screenshotDir>\MOVIE*` and take the largest numeric suffix
found (the scan parses each name's digits), add one, format
`<screenshotDir>\MOVIE%03i`, create that directory, capture one frame
through the screenshot writer, and set the next-capture tick to the current
tick. Stopping (the series counter is nonzero) clears the counter. While the
counter is positive, the outer frame captures a frame whenever
`nextCaptureTick <= currentTick`, then adds `30 / rate` (integer division;
`rate` is registry `Movie Output Rate`, absent → `10`, also set by
`+FilmSpeed`) to the next-capture tick and **resets the wall-clock base** so
the capture time does not enter the next raw delta. `<screenshotDir>` is the
`%s\%s` path built at startup from the install directory. The `Film`/
`FilmSpeed` developer commands write the same rate and a recording flag.

### Developer mode [R-CAM-01 §9]

**Established — activation is independent of film mode.** Developer access is
set by either the settings loader finding `DisplaymodeDepth = 256` and
`Games = 1`, or the ordinary mask-1 `Now` command accepting exactly six
parsed words. The command lookup is case-insensitive, so `+now Film Chris
Include Reload Assert` succeeds. The five arguments must have precisely the
shown case. Additional words, missing words, or changed argument case clear
developer access. Tokenisation happens first ([R-CAM-01 §6]): extra whitespace
is immaterial, a trailing `#` comment is ignored, and a semicolon is ordinary
word content. The settings loader clears access when its pair does not match;
the command changes only the live access flag and does not write settings.
The password is accepted through ordinary TALK in a stock installation;
there is no prerequisite developer registry edit.

**Established — three separate controls.** Developer access gates mask-4
commands, the default unit-spawn handler, `\` command replay, Ctrl+F10 capture,
and the F11 toggle. Film mode is a separate flag toggled by F11. The film
information flag is a third flag, toggled by lowercase `i` while film mode is
active. F11 entry disables GUI quickkeys. F11 exit enables them, clears film
information, and resets the viewport diagnostic mode to zero. The quickkey setter does
not hide or show the HUD. The battle-screen initializer clears film mode and
film information; the password handler changes neither of them. Consequently,
clearing developer access while film mode is active leaves the film controls
and their display gates active, but prevents F11 exit until access is enabled
again. Other dialog close paths can re-enable quickkeys [R-WGT-01 §3].

**Established — film input is a second dispatch.** After the ordinary battle
hotkey switch finishes, the same token is passed through this table if the
film flag is still set; developer access is not re-tested. A token is not
consumed exclusively by the film switch. In particular `=` is rejected by
the speed handler first, then performs the film action; `+` has no film action.
F11 entry reaches the second switch, which has no F11 action; F11 exit skips
it because film mode has just been cleared.

| Token | Film action |
|---|---|
| `=` | Visit player slots 0 through 9; for each occupied slot with controller kind 1, 2 or 3 and side other than 10, replace metal and energy stock with that player's respective storage capacities. |
| `P` / `p` | Request pointer capture / release respectively. |
| `]` | If the hovered unit is present, set its recorded attacker-side snapshot to 10 (no attacker), clear its recorded-attacker reference, and mark it dying ([06 R-WPN-04 §2]). This is a direct diagnostic mutation, not an ordinary queued order. |
| `i` | Toggle film information. Uppercase `I` has no case. |
| `m` | Increment viewport diagnostic mode and wrap exactly 5 to 0. Uppercase `M` has no case. The viewport presentations are [03 §3.12]. |

`DebugBreak` additionally requires both developer access and film mode.
The runnable display gates, including film information, are described in
[03 §2.4] and [R-HUD-03 §1]; film mode is not synonymous with either probe.

**Established — replay is not another console window.** `\` re-tokenises
and dispatches the retained last-command text with every route bit enabled.
The text excludes the leading `+`. Replaying does not submit another TALK
message and does not replace the retained command. The mask-8 AI-profile
commands are therefore eligible through replay although ordinary developer
TALK uses route 7. The dispatcher does not fall back when tokenisation yields
zero words. For a missing command, or a registered command excluded by the
route, the mask-4 default handler is eligible; command existence alone does
not suppress the default handler ([R-CAM-01 §6]).

#### Unit probes: retained targets and dormant painters

**Established — hotkey state.** Shift+F1 and Shift+F2 maintain independent
State and Builder probe enable flags and retained unit references. With a
nonzero hovered-unit reference the respective hotkey enables its probe and
copies that reference. With no hover it clears only the enable flag, leaving
the old reference stored but inactive. Repeating a hotkey over the same unit
does not toggle it off. Moving the pointer does not retarget an armed probe.
Neither arming path tests developer access, film mode, selection, ownership,
controller type, completion, or build capability. Escape and ordinary F1/F2
do not explicitly clear these flags.

**Established — reachability boundary.** The retail image contains separate
State and Builder painting routines. The recovered call/reference census
has no incoming reference to either, a full-image pointer search finds no
stored reference to either, and the battle composer calls neither. The
hotkeys and painting routines must not be conflated with the rooted
diagnostic footer. **Supported inference:** these are dormant diagnostic
painters in the examined release, rather than accessible overlays awaiting
another ordinary toggle. **Unknown:** a reachable indirect invocation or
other retail-build wiring; a rooted caller or manual observation identifying
the exact build and activation sequence would settle this. The contracts
below describe the dormant routines' actual behavior, not proof of display
in an unmodified retail session.

**Established — painter lifetime and invalidation.** Each painter returns
without drawing unless its own enable flag and retained reference are both
nonzero. State checks that its retained unit is live and not dying; on
failure it clears its own enable flag and reference, but still completes the
current draw from the already resolved unit. Builder also rejects a unit
whose definition has no build-option list. Its failure branch instead clears
**State's** enable flag and reference, leaving Builder armed, and likewise
continues its current draw. This cross-probe clear is an observed retail
quirk. There is no owner or visibility check in either painter. A retained
reference identifies a pool slot; the painters do not verify a generation
number. **Unknown:** the cross-battle lifetime of these retained references
and the shared panel-bottom cache; the hotkey and painter writers alone do
not establish a battle reset. A bounded reset/save-load ownership trace is
the decider.

**Established — shared layout.** Both painters select `COMIX`, set text to
`dcb[15]`, and use line pitch `p = fontHeight + 3`. State begins at
`(134, 7*p+3)`; Builder begins at `(134, 3*p+3)`. Before drawing text, each
shades a rectangle from x 131 to 401 using the darken operation with argument
−24 (shade row 8), then outlines it in `dcb[5]` with its right and bottom
edges extended by one. Its top is `7*p` for State and `3*p` for Builder; its
bottom is a **shared retained** previous text-end y, or `20*p` when that
cache is zero. At draw completion it overwrites that same cache with the
new text-end y. Text and backdrops therefore need not have matching heights
on the first draw or after switching probes. This is a shared cache, not
independent automatic panel sizing. Each explicit line advances by one
pitch; format strings retain their authored newline bytes. Neither routine
provides scrolling or pagination.

**Established — State fields, in draw order.** Quoted formats below are the
retail diagnostic text; `\n` denotes an authored newline.

| Line | Source and formatting |
|---|---|
| Title, rule | `Unit State Probe`, then `================` |
| Identity | `uid: %03d/%04x '%s'\n`: the same unit identity in decimal and lowercase hex, then the definition display name. Widths are minima. |
| Owner | `playerno: %d '%s' %s - %s\n`: owner slot, registered player name, `LOCAL` when that player is occupied with controller kind 1 or 2 and `REMOTE` otherwise, then `BUILDING` when definition `bmcode` is zero or `MOBILE` otherwise. |
| Controller | `controller: %d\n`: the owner's controller kind. |
| Construction | `buildtimeleft: %1.3f\n`: current remaining construction fraction, not seconds. |
| Health | `damage: %d\n`: current signed hit points, despite the label. |
| Occupancy | `occupy: %s\n`: `NONE`, `GROUND`, or `AIR` for occupancy classes 0, 1, or 2. No valid label is defined for class 3. |

Only an occupied owner with controller kind 1 or 2 gets the remaining lines:
`autotarget w[pri:sec:spe]: w[%c:%c:%c]\n` in primary/secondary/special order,
using `X` for a set per-weapon autotarget flag and `-` for a clear flag. Then
print `Mission Q:` only if the primary order queue is nonempty, visit it in
linked queue order, and do the same for `Background Mission Q:` and the
secondary queue. Each order uses its descriptor name and state:
`    '%s' state: %d\n`, or `    '%s' state: %d  tgt: '%s'\n` when it has a
unit target, appending that target's definition display name. No coordinates,
queue count or summary replaces those per-order lines.

**Established — Builder fields and score bars.** Its title and rule are
`Unit Builder Probe` and `==================`. It uses `uid: %03d '%s'\n`,
the same owner line as State, then `controller: %d\n\n`. Only an occupied
owner with controller kind 1 or 2 gets `Units I can build, and the
probabilities:\n` followed by one row per authored build option, in build-list
order. Each row obtains the ordinary candidate score for the **unit's owning
player** ([08 R-P0-05 §3], [08 R-P0-05 §4]), and formats the raw result as
`       %3d %% - '%s'\n` with the product's internal definition identifier
(the name used for unit lookup), rather than its display name. These are
individual AI scores displayed with a percent sign; they are not normalized
probabilities, and the painter does not run the random weighted selection.

At each row's y, outline `(136,y+1)..(162,y+p−5)` in `dcb[15]`. Let
`q = min(score,100)`. When `q > 0`, fill inclusively from x 136 through
`136 + trunc(26*q/100)` over the same y range, also in `dcb[15]`.
A score above 100 retains its full numeric text but saturates the bar;
a nonpositive score has only the outline. The title's claim of probabilities
must not be used to normalize or clamp the numeric readout.

### Supported inference

Input tokens should be generated from a compatibility key map rather than
from platform-specific key constants. This permits the same normalized
commands to drive front-end widgets, battle hotkeys, and deterministic command
serialization.

### Unknown

- Which front-end screen paths handle which key tokens (the battle
  dispatcher's cases are the census of [R-CAM-01 §2]) · §5 · static trace.
- The unsupported-device census · §2 · static trace.
- Text-input code page and IME behavior · §2, §7 · presentation-level
  platform detail; no retail contract observed beyond the ASCII token set
  (`TODO(T23)`).


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

**Established — command-window downs precede battlefield cancellation.** The
GUI mouse fetch peeks the queued pointer record. With an active window, it
consumes the record when its pointer lies inside that window's inclusive
rectangle or no button is held. This admission precedes gadget hit testing:
blank window space and hidden or greyed gadgets do not make an inside down
available to the battlefield. An outside held record remains queued. The
outer input pass compares the message kinds before and after GUI service.
Equal kinds pop the next available record into the battlefield input. Different
kinds retain the earlier record only when it was a release; otherwise they use
the later record. Thus a down consumed by the command window does not itself
reach battlefield cancellation; a subsequent queued record may still do so. The
factory product's signed subtraction remains its GUI activation callback
([R-P0-11 §1]), not a battlefield right-down order. This window admission is
independent of the battle interface's button polarity.

Inside the GUI pass, when the top GUI's token-mode field is nonzero the pass
consumes one queued token; when it is zero the pass peeks without consuming
and suppresses tokens `0xE2..0xEB` inclusive for that pass (they are replaced
with zero); other peeked tokens are observed without the initial consumption.

The active GUI is the top object of a linked stack, and its event pass walks
authored gadgets in increasing index order (the declared count is inclusive:
N gadgets admit GADGET0..GADGETN). Hit testing is inclusive on both axes:
`gx <= x <= gx+w-1 && gy <= y <= gy+h-1`. Hidden gadgets are skipped before
the hit test; a greyed button (bit 0 of its own grey word — `attribs` has no
greyed bit) refuses the press at press time, before its own hit test, and
never at hover ([R-WGT-01 §13]). Callbacks and association handling can
redirect which gadget becomes active.

**Top-object close is closed.** Closing the active GUI object invokes its
registered callbacks, redraws under nest-counted cursor/display protection,
removes the object, reactivates the predecessor when one exists (the stack
head is replaced and the predecessor is marked for redraw), and applies one
extra redraw request selected by GUI flag `0x800`.

GUI flag `0x800` requests one extra redraw pass when the window closes, and
`0x1000` centers a modal window in the playfield right of the 128-pixel rail
(§11). The per-dialog Escape/Enter/focus defaults are authored data — each
GUI file declares its own `escdefault`, `crdefault`, and `defaultfocus`
controls — and the fixed matrix that consumes them is [R-WGT-01 §2]. There
is no parent/child bubbling (one flat gadget array, first firing gadget in
index order wins), the default-control rules are the Enter/Escape/Space rows
of the matrix, focus is single (one focused gadget, one capture), and
association is a post-change synchronisation, not a redirection
([R-WGT-01 §1, §2, §5–§7]).

Escape, Enter, Backspace, and numeric/character input are observed in dialog
and chat paths.

### Supported inference

Event ownership should be hierarchical: modal child, active front-end panel,
battle HUD gadget, then world/camera. This matches the observed modal hit
tests and prevents a click intended for a dialog from selecting a world unit.

### Unknown

- User-facing naming of every GUI mode/flag bit · §3 · static trace.
- The meaning of the window key-navigation flag's *clear* state in the
  front end — which screens deliberately leave Tab/Enter/Escape to their
  own key callback rather than the matrix · §3 · per-screen static trace.

### The gadget service pass: order, capture, hover help, and who closes the window [R-WGT-01 §1]

**Established.** One routine services the top window once per host frame
(the "GUI pass" of §2). Its order is fixed:

1. The scaled timer ([R-CAM-01 §10], 30 units per second) is sampled; the
   delta since the previous pass is stored. A separate once-per-tick latch
   (`0 < now − lastStamp`) gates every piece of timed widget work below
   (flash decay, auto-repeat, list auto-scroll); a pass that lands inside
   the same timer unit does none of it.
2. The mouse sample is fetched. A sample taken while a button is held **and**
   the pointer is outside the window rectangle is discarded — the pass keeps
   the previous position. So a drag that leaves the window freezes at its
   last inside position, and the release (buttons zero) is the first sample
   accepted again. The "last mouse message" word (button-down /
   double-click identity) and the held-button bits (1 left, 2 right) come
   from the same fetch.
3. The keyboard token is taken as §3 states (pop when the window's token
   mode is non-zero, else peek with `0xE2..0xEB` zeroed). When the token
   mode is non-zero, the token is non-zero and the window's key-navigation
   flag is set, the **window key matrix** (§2) runs first; a token it does
   not consume is uppercased, pushed onto the window's 15-entry key history,
   handed to the window's key callback, and the fired-gadget result is reset.
4. The window-rectangle hover test of §3 runs (cursor swap).
5. Gadgets `1..N` are visited **in index order**. Each visit performs the
   inclusive hit test of §3 (hidden gadgets are skipped before it, so the
   hovered gadget is the **last** hit in index order) and dispatches on the
   stored kind (the table of §4). The loop stops at the first gadget that
   reports a *fired* result; later gadgets are not visited that pass.
6. If the hovered gadget changed, the gadget named `HELPTEXT` (16-byte name
   compare) receives the hovered gadget's localized `help` text — the empty
   string when nothing is hovered — and a redraw is requested. This is the
   whole "hover help" mechanism; it is not per-kind.
7. The window's per-pass callback runs.
8. If a gadget fired: it becomes the focused gadget (a text input additionally
   gets its colour, font and caret set up), then the window's *fired*
   callback runs with the result still visible. If the callback leaves the
   result set, the window is **closed** by the top-object close of §3. Every
   screen handler that wants to stay open therefore clears the result; the
   "callback result" of §4's kind table is this word.

**Capture.** Exactly one gadget can hold the pointer capture (a context
word, `−1` when free). A press inside a gadget takes it; the take is refused
while another *non-text* gadget holds it (a text input's capture is
released implicitly by any other take). The captured gadget keeps receiving
the pass while a button is held, whatever the pointer does; release inside
fires, release outside restores (per kind, §3–§8). There is no parent/child
bubbling: a window has one flat gadget array, and a gadget that fires does
not forward anything to another gadget except through the **link**
redirection of §7 and the **assoc** synchronisation of §5.

**Flash decay.** A button's `colorf` word is not a colour: the button
painter passes it as the light-table row of the keyed blitter
([03 R-FONT-01 §6]), and the pass decrements it by 2 per timer tick
(clamped at 0) for buttons and by 1 for picture boxes (kind 12, repainting
each step). Screens set it through the gadget-colour setter to make a
button flash and fade; the window builder zeroes it for every button and
label at open.

### The key matrix: Tab, Enter, Escape, Space, arrows, and focus order [R-WGT-01 §2]

**Established.** The window key matrix runs before any gadget sees the
token, and only when the window's token mode is non-zero and its
key-navigation flag is set (screens that want keyboard navigation set the
flag; battle windows leave it clear, so in battle none of this applies and
tokens reach the hotkey dispatcher after the gadget loop). A consumed token
is replaced by zero for the gadget loop.

**Established — the token-mode arm alone settles battle.** The two gate words are tested together, and the token-mode word is
the one whose value for a battle window is not in doubt: only the front-end
shell and the in-battle options root set it ([R-WGT-02 §2]), and the
in-battle window openers — `UNITINFOx.GUI`'s among them — set nothing. With
it zero the pass takes the peek branch of [R-WGT-01 §1] step 3 (token left in
the queue, `0xE2..0xEB` zeroed for that pass only) and skips the matrix
outright. Thus the matrix consumes no token for an ordinary battle child,
and `escdefault`/`crdefault` do not fire from Escape or Enter through that
matrix. Indexed gadget quickkeys and an editor can still claim tokens under
their own rules; remaining tokens reach the caller. A caller-specific key
transition, such as the YESORNO No row in [R-FE-01 §7], is separate. The separate navigation switch is identified below; it does not change
this token-mode exclusion.

**Established — navigation enable is separate from the fired result.**
The interface owns a keyboard-navigation switch, initialized enabled and
explicitly enabled or disabled by screen transitions. The key matrix tests
that switch together with a non-zero window token mode and a non-zero token.
The same switch gates the focus/default marker pass after a full repaint.
When it is disabled, the service skips the matrix and its unconsumed-key
history/key-callback arm; the ordinary gadget pass still runs. Gadget
accelerators retain their separate quickkey-enable and capture gates.

The fired result is instead a gadget index, with `−1` meaning no result.
Window opening initializes that index to `−1`; a gadget or the matrix writes
the selected index. A screen callback keeps the window open by clearing the
index back to `−1`. The close decision tests the surviving index, not the
navigation switch. These are independent state, correcting the former
boolean-result description in [R-WGT-02 §2].

The in-battle options root explicitly enables navigation and consuming token
mode; its close callback disables navigation. General battle entry disables
navigation after closing the old windows. These traced transitions do not
establish every screen's enable lifetime; that remaining census stays in §3's
Unknown list. Ordinary zero-token battle children remain excluded from the
matrix regardless of the inherited navigation switch.

| Token | Rule |
|---|---|
| Tab | Focus moves to the next gadget in reading order (below); Shift+Tab to the previous. |
| Enter | If the captured gadget is a text input the token is **not** consumed (the editor fires on it, §6). Otherwise the `crdefault` gadget (name lookup, [R-FE-01 §12] for the empty-name fallback) fires when it is active and not a greyed button; when it is absent or unusable the Space rule is applied to the focused gadget instead. |
| Escape | The `escdefault` gadget fires when it exists and is active; otherwise the token is not consumed (the text editor then sees it, §6). |
| Space | Fires the focused gadget when it is a button, listbox or surface, active, and (for a button) not greyed. A button with the radio attribute (`0x10`) is set down and its group cleared; a staged button advances its stage. A focused text input does not consume Space. |
| Left / Right | Focused text input: not consumed. Focused **horizontal** slider (width > height): knob −1 / +1 with the slider's own clamp, repaint, assoc sync and change callback. Otherwise focus moves left / right. |
| Up / Down | Focused listbox: selection −1 / +1 (§4) and the list's change callback. Otherwise focus moves up / down. |

Firing through the matrix sets the same fired-gadget result the mouse path
sets, so the window callback and close rule of §1 apply unchanged. There
is no per-kind key table beyond this: buttons additionally answer their
quickkey (§3), labels theirs (§7), and text inputs drain the token stream
themselves (§6). Every other token passes through.

**Focus order (Established by direct static trace for windows with at most
49 controls).** No focus (`−1`) returns immediately without changing focus or
capture. Header index 0 retains canonical X zero, independent of its authored
rectangle X; the population walk begins at control index 1. Otherwise traversal
constructs a canonical-X value for every control in file order, including
controls later rejected as candidates. For
each control, scan the prior canonical values from index 1 until the first
zero. The first value within nine pixels of the control's raw X donates its
value: the signed difference must be strictly greater than −10 and strictly
less than 10. If none matches, keep the raw X. Thus columns can inherit an
already clustered X; the scratch does not group Y rows. Zero terminates the
donor scan rather than donating a coordinate. A first control at X=0 prevents
all later clustering; a later zero cuts off itself and subsequent entries
from later donor scans.

Candidates are active controls of kinds button, listbox, text input, slider
and surface whose attribute `0x400` is clear. Exclude greyed buttons, locked
sliders, vertical sliders (`width < height`, so a square remains eligible),
and listboxes with attribute `0x100`. Surfaces have no `hotornot` test here.
Left and Shift+Tab move backward in raw `x + y·5000` order; Right and Tab
move forward in that order. Up and Down use `y + canonicalX·5000`, backward
and forward respectively. The keys and their adjustments use signed 32-bit
arithmetic.

Backward selection starts with a best key of `currentKey − 25,000,000`.
For each eligible control in ascending index order, subtract 25,000,000 once
when its key is at least the current key, then select it only when the
adjusted key is strictly greater than the best key. Forward selection starts
at `currentKey + 25,000,000`, adds 25,000,000 once to candidate keys at or
below the current key, and selects only strict improvements below the best
key. Equal candidate keys retain the first indexed winner. A candidate tied
with the current key maps to the initial boundary and cannot replace the
current focus. If no candidate improves the boundary, focus remains unchanged.
The previous row-grouping and larger wrap-distance description was incorrect.

After an attempt with a nonnegative focus, capture is released and the chosen
index is stored, even when unchanged. Text-input focus initializes its colour,
font and caret. Opening with empty `defaultfocus` seeds header index 0 and
runs forward traversal; a nonempty field instead installs the exact bounded
lookup result. Disabling the focused control also invokes forward traversal.

**Unknown — focus scratch beyond 49 controls.** Only the first 50 canonical
entries, including header index 0, are initialized before the control walk.
There is no corresponding count guard, so the donor scan for later controls
can depend on uninitialized temporary values. All GUI provider copies in the
reference install have at most 43 controls; stock content does not settle
that larger authored-input behavior. A bounded initialization/lifetime trace
or manual observation of such a custom window is required before assigning
a deterministic result. No implementation should invent that residue.


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

**Control-kind mapping.** The parser reads a per-kind key table selected by
the control-kind byte ([R-WGT-01 §11]); the window builder's switch then has
eleven keys over ten arms ([R-WGT-01 §12]): background/panel (with -1
centering and the `BackTile` fallback chain; kind 11 shares the arm), button
(including staged buttons chosen by best-fit frame size and `|`-separated
multi-line labels), listbox, text input (`maxchars` capped at 127), slider
(which synthesizes two scrollbar child gadgets with derived knob travel),
label, font file, raw file, picture, and score bar. Gadgets live in fixed
347-byte records; each kind resolves its art from its own named GAF entry
first, then the side-specific interface GAF, then the built-in fallback.

**Runtime control-kind dispatch is closed.** The active-GUI pass routes each
gadget's stored control-type byte to distinct runtime families:

| Stored type | Runtime family |
|---:|---|
| `1` | Clickable control path with callback result; can become the active gadget. |
| `2` | Distinct stateful control path. |
| `3` | Focusable text editor: drains queued edit tokens until empty or Escape; cursor movement, insertion, deletion, navigation, and clipboard paste (above). |
| `4` | Scrollbar/slider: knob, travel and assoc synchronisation ([R-WGT-01 §5]). |
| `5` | Label whose `link` names the gadget the pass redirects to, found by comparing up to 16 bytes of the gadget name across the other gadgets during a fixed-record-stride scan ([R-WGT-01 §7]). |
| `6` | Surface with a per-pass callback and the `hotornot` click ([R-WGT-01 §8]). |
| `12` | Repeating/decrementing path: auto-repeat decrements a counter while a throttle predicate holds. |
| `13` | Timed/range path: animates a range value toward a maximum using per-gadget interval/threshold fields, then fires the path. |

Text-editor admission is bounded by the authored maximum (a stored 16-bit
value capped at 128): printable bytes are admitted up to `maxchars - 1`
subject to the gadget's input-filter attribute bit `0x02`, an
allowed-character-set test with exceptions for space, underscore, and
apostrophe, and the rendered-width rule `currentLen + newLen <= controlWidth
- 4`. Paste copies at most `maxchars - 1` bytes, maintains termination, then
removes trailing bytes until the rendered width fits the control or only one
byte remains (the exact paste behavior is in §2).

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

- Who fills a listbox's `maxTop` word for each screen (the widget code only
  reads it) · §4 [R-WGT-01 §4] · per-screen static trace.
- The record-list (`0x20`/`0x80`) item structures beyond the height word the
  hit test and knob arithmetic read · §4 [R-WGT-01 §4, §5] · static trace of
  the save/load screens.

### Buttons: art resolution, press semantics per attribute, quickkeys, cue sounds [R-WGT-01 §3]

**Established — the record.** The authored `status` is stored as the
button's **down-state word** (0 up, non-zero down); `stages` is a byte and
the *current stage* is a second byte cycling `0..stages−1`; `grayedout`
sets bit 0 of the grey flag; `quickkey` is stored as one byte — an
alphabetic first character verbatim, otherwise the decimal value of the
string. The window builder then resolves art and **overwrites the
quickkey** (below).

**Established — art and frame base.** The builder looks the gadget name up
in the window's own GAF, then in the common interface GAF; only when both
miss does it fall back: attribute `0x80` → `CHECKBOX`; `stages ≠ 0` →
`stagebuttn%d` with the stage count, except that a label exactly `Off|On`,
`stages == 1`, or attribute `0x4000` forces `stages := 2`, entry
`stagebuttn1` and sets `0x4000`; no stages → `BUTTONS0`. On the fallback
path the *frame base* is the best-fit frame: over frames `0, 4, 8, …` of
the entry, the one minimising `|h − frameH| + |w − frameW|` (first wins on
ties, initial best 1000); on the named path the base is frame 0. The
gadget's width and height are then **replaced** by the base frame's size
(this is [fmt gui]'s "size dictated by art"). A staged label is split at
`|` into per-stage strings, each localized, stored NUL-separated, and the
attribute word becomes `(attr & 0x4000) | 1` — staged buttons are always
left-aligned. So the retail frame layout is: *base* (rest), *base+1*
(pressed), *base+2* (greyed) for plain buttons, and for staged art frame
`k` is stage `k` directly with `frames−2` the pressed look and `frames−1`
the greyed look.

The staged-label split and alignment-attribute write are one common builder
tail after every art choice, including a named entry and `CHECKBOX`; selecting
a checkbox prevents only the fallback's stage-count forcing. The completed
record retains the selected entry itself, so another bank with the same entry
name cannot replace it during later input or painting.

A found entry ends the provider search even when it has no frames. Fallback
selects exactly one of the checkbox, staged, or ordinary button families;
an absent entry in that family remains absent. It does not cause a second
fallback to another family. Missing art or a zero-frame entry leaves the
authored geometry unchanged; the shared staged tail still runs.

The fallback scan begins with base frame zero already selected; only a score
strictly below its initial bound replaces that selection, so a window with no
closer group still keeps frame zero.

**Established — installed runtime record.** The builder completes that art
choice before the screen service is created: the record then holds the chosen
entry, base frame, resulting rectangle, stages and attributes. Later input and
painting use that one record, while only the current-stage value remains
mutable in the screen state.

**Established — the painter's frame choice.** Not greyed and down-state set
with `stages < frameCount` → `base + downState` when `stages == 0`, else
`frames − 2`; not greyed otherwise → `base` (`stages == 0`) or the
current-stage byte (`stages ≠ 0`, base ignored); greyed → `frames − 1`
when attribute `0x100`; else `stages == 0` and not an arrow (`0x1800`
clear) → `base + min(downState + 2, frames − 1)` darkened by 20 palette
steps unless attribute `0x80`; arrow → `base`, darkened; staged → the
current stage, darkened. With no art at all the button is a bevel in
window colours 0/17/20 (raised when up, sunken when down; greyed 0/19/19).
The label pen is [03 R-FONT-01 §6]; the "second string" that section
leaves to this document is the **quickkey character**. For a label
containing the key (case-insensitive search) the text is drawn in three runs
— the part before the key, the key character, the remainder — and the two
branches differ. The **build-attribute** (`0x20`) branch draws the key
character in window colour entry 10 and the other two runs in the gadget
colour, whether or not the button is greyed. The **centred** (attribute 2)
branch draws all three runs in the gadget colour and **underlines** the key
instead: a one-pixel line on row `penY + metric − 1` spanning the key
character's width, in window colour entry 2 (entry 0 when `stages` is
non-zero); a greyed button skips this branch and draws the whole caption as
one run. The "gadget colour" of both branches is the window colour-table
entry the button's `colorf` selects when `stages` is zero, and entry 0 when
it is not ([03 R-FONT-01 §6]).

**Established — quickkey assignment.** Assignment operates on the current
records of the whole window, in builder record order. For a button the helper
first preserves the current key when attribute `0x10000` is set. Otherwise a
nonzero stage count clears the key and returns. A non-staged button with an
empty caption returns without clearing its key. For other nonempty button
captions the key is cleared before searching. A label with an empty link is
not assigned; a linked label clears its key before searching, even with an
empty caption.

The search walks caption bytes until the terminator, skipping only the literal
space byte. For each candidate it compares lowercase forms against the current
key of **every button and every label** in the window, including later records
and inactive records. A collision advances to the next caption byte. The first
free candidate is stored with its original case, not its lowercase form. If
none is free, the cleared key remains zero. This is not a reservation pass over
only previously assigned controls: a later button's existing authored key can
exclude a candidate before that later button is rebuilt.

The button builder calls assignment before resolving art and before splitting
and relocalizing staged captions. The parser has already localized the initial
caption (§11). Buttons marked as slider arrows, or with the authored per-gadget
GAF flag below set, bypass this assignment call. Linked labels invoke it in
their own builder arm. Thus ordinary nonempty, non-staged buttons normally
replace their authored key, but the empty-caption, preservation and bypass
cases must not be collapsed into that rule.

**Established — the authored per-gadget GAF flag.** This flag is exactly bit 0
of `[COMMON].gaffile`; it is not a result of resource lookup or a marker that
button art has already been resolved. New window record storage starts zeroed.
For every record with a `[COMMON]` subsection, the parser reads `gaffile` as an
integer with default zero and replaces only this bit with the integer's low bit.
Whole-record moves used while appending a child GUI preserve it, while the
builder's newly synthesised slider-arrow records start with it clear. No traced
runtime setter changes the bit: the common-field parser is its only targeted
writer, and the value then lasts for the record's lifetime. The GUI serializer
also emits only this low bit.

At the start of the build record loop, a set bit selects the per-gadget resource
path. The builder clears the record's installed GAF and entry, tests
`anims\\<gadget-name>_gadget.GAF`, and, when that resource exists and loads,
looks up an entry with the gadget name in that GAF. Neither existence nor load
success changes the flag. For a button, the same set bit closes the later gate
that contains quickkey assignment and the ordinary button-art pipeline. It
therefore also bypasses the window-GAF/common-GAF/fallback search, best-fit base
selection, geometry replacement, and staged-caption split and attribute rewrite.
The external entry uses base frame zero when present; if the resource or entry
is absent, the button remains art-less with its authored geometry. Slider-arrow
attributes close this same later gate independently, after their owning slider
has installed their art and geometry (§5).

**Established — open, assignment, and caller mutation order.** An ordinary
window open performs its initial build, including button-key preclear and
assignment, before returning the new window to its screen-specific caller.
That caller may then relabel, hide, or grey gadgets and request a repaint; the
repaint does not repeat the build prelude or key assignment. In particular,
the in-battle options window assigns `MISSION` from its initially localized
authored caption before the skirmish/multiplayer caller relabels it to the
translated `Settings`. The relabel does not assign a new accelerator.

**Established — whole-window key preclear lifetime.** The process-lifetime
interface context initializes preclear enabled. The first loading-to-battle
transition clears it; no later writer restores it, and returning to the
frontend reuses the same context. Before each window build's record loop,
enabled preclear clears every button key while retaining label keys. A repaint
alone does not run this prelude. Thus later authored button keys participate
in collision checks after the first battle, but are already zero before the
first battle. The preserve attribute retains the post-preclear value, which
can be zero; it does not recover the originally parsed byte. This state is
separate from accelerator service enablement [R-WGT-02 §2].

The loading worker opens and builds the side's root `MAIN2.GUI` before it
reports completion, so that root still sees the enabled startup preclear. The
successful transition then finishes the loading draw, selects the running
battle host mode, and clears the latch. Selection-driven command pages and
the in-battle options, exit, confirmation, Tab, and post-battle result windows
are opened only by the running or post-battle paths, after that clear. Parsing
or caching those resources early must therefore remain separate from their
one-time runtime build: eagerly building them with the loading candidate would
apply the wrong first-battle preclear state.

**Established — caption-byte case conversion is process-wide ASCII only.**
Every candidate byte and stored key reaches the collision comparison as a
signed byte widened to an integer. The fold changes only `A` through `Z`, by
adding `0x20`; every other input, including bytes `0x80` through `0xFF`, is
returned unchanged. Consequently extended caption bytes collide only with the
same byte value, while ASCII letters collide without regard to case.

The process starts with the locale selector in its zero state and retains that
state throughout supported ordinary single-player execution. The complete CRT
startup initializer sequence was traced: it selects the system ANSI code page
for multibyte classification, but does not select a locale for the character
case mapper. The one locale-selection path can replace the selector through an
indexed category update, including the character category, but it is absent
from the startup tables and has no reachable application caller or callback in
this scope. Therefore the system ANSI code page does not affect quickkey
collision folding. This closes the caption-byte contract without assuming an
ASCII or Windows-1252 code page.

**Established — press semantics.** Greyed buttons ignore everything. A
press (left or right button-down message) inside takes the capture and
saves the down-state. While captured, by attribute:

| Attribute | Behaviour |
|---|---|
| `0x10` radio | Fires **on press** while the button is held and the pointer is inside: down-state := 1, every other button with the same `assoc` byte and a non-zero down-state is cleared and repainted. Pointer outside → the saved down-state is restored, nothing fires. |
| `0x40` toggle | Held: inside shows down (1), outside shows up. Release inside → down-state := `(saved == 0)` (flip), group clear, fires. Release outside → restore. |
| `8` | Release inside → down-state flips between 0 and 1 (other values unchanged), fires. Nothing while held. |
| `0x100` cycle | Each fresh **left press** inside advances the down-state through `0..frames−1` (wrapping), fires immediately and retires capture. Held and release passes never apply the plain-button mutation. |
| plain | Held: when the down-state is zero and the pointer is inside, down-state := 1 and the auto-repeat delay is set to 15; pointer leaving clears the down-state. Re-entering while held therefore restores the pressed state and restarts the full delay, without decrementing it in that same pass. Release: capture freed, down-state := 0, group clear; release inside a non-arrow fires and, if staged, advances the stage `(stage+1) mod stages`; release outside repaints only. |
| `0x2000` auto-repeat (with plain) | While held inside, every distinct timer tick with a positive delay decrements it and returns, including the tick that reaches zero. Later timer ticks repaint the button; a plain button does not fire its window callback. Synthesised slider arrows additionally step the associated slider and invoke its change callback (§5). |
| `0x1800` slider arrow | On each trigger (first press and every repeat) the kind-4 gadget with the same `assoc` byte moves its knob by −1 (`0x1000`) or +1 (`0x800`), clamped to `0..travel−1`, is repainted, synchronised (§5) and its change callback runs. The arrow itself never fires. |

**Established — button quickkey admission.** The button first rejects the
low bit of its grey word; other bits alone do not reject it. Hidden gadgets
are skipped by the outer service pass. When this button itself holds capture,
it follows its pointer state machine and does not also take the quickkey path.
Otherwise, a captured text input blocks the quickkey unless Alt is held. A
different non-text gadget's capture does **not** block the key: the earlier
blanket no-other-capture rule was incorrect. With the window's quickkey flag
exactly 1 and a nonzero token equal to the stored key in either case, toggle
buttons flip, radio buttons go down and clear their group, the token is popped,
and the button fires. The screen's fired callback cannot tell a quickkey from
a click. These capture rules concern accelerator admission; they do not change
the separate rules for taking pointer capture in §1.

**Cue sounds (Established, bounded negative).** No widget handler or
painter plays a sound. `BigButton`, `SMLBUTTON` and their case variants
(`bigButton`, `smlButton`, `Smlbutton`, `SmlButton`, `smlbutton`) are
alias names that the per-screen fired callbacks pass to the interface cue
player when they act on a result, so which cue a button plays is authored
per screen in code, not per gadget kind or size. A reimplementation should
play the cue in the screen handler that consumes the result.

### Listbox: rows, hit rows, selection, scrolling, double-click, headings [R-WGT-01 §4]

**Established — geometry.** `metric` is the current font's line metric
([03 R-FONT-01 §6]: GAF font `height(I) + 2`, FNT font header height).
`rowH = itemheight` when authored non-zero, else `metric + 1`. The
click handler's visible-row count is `rows = trunc((h − 2) / rowH)`; its
interior is `x ∈ [gx, gx+w−1]`, `y ∈ [gy+2, gy+h−4]`, all inclusive. The
list state is: `selected`, `top` (first visible item), `maxTop` (the
largest allowed `top`, maintained by whoever fills the list), `count`, and
the item text block (items separated by newline or NUL; item `n` is the
text after the `n`-th separator).

**Established — the click.** A press inside takes the capture (left or
right). While captured and the pointer is inside, the list becomes the
focused gadget and, for a text list (attribute `0x10`):

```
sel = top + trunc((y − (gy+2)) / rowH)
if sel < 0: keep the old selection
else: sel = min(sel, top + rows − 1); sel = min(sel, count − 1); sel = max(sel, 0)
```

then, when attribute `0x200` is set, an item whose text begins with the two
bytes `&G` (a heading row) **rejects** the selection and the old value is
kept. Every other listbox with the same `assoc` byte receives
`min(sel, itsCount − 1)`. A changed selection repaints the list and calls
its change callback; attribute `0x40` makes a single click **fire**;
otherwise a click only requests a redraw. For record lists (attribute
`0x20` / `0x80`, the picture rows the save-game list uses) the hit row is
found by walking item heights (`itemheight` if authored, else each item's
own height `+ 2`) from `top` until the pointer Y is exhausted.

**Established — drag auto-scroll.** While captured with the pointer below
the interior, every 2 timer ticks `top += 1` (while `top < maxTop`) and
`selected := top + rows − 1`; above the interior, every 2 ticks
`selected := min(selected, top) − 1` and `top −= 1` (while `top ≥ 1`),
or, at `top == 0`, `selected := 0`. Heading rows are rejected here too.

**Established — double-click.** A left double-click message inside a
non-empty list fires the gadget (the preceding click already selected the
row); with attribute `0x200` the row under the pointer is recomputed and
a heading row fires nothing. Fired means the screen's callback sees the
list as its result — the "open" action of the load/save/map lists.

**Established — keyboard.** Up/Down (§2) move `selected` by one and drag
`top` along (`top := top − 1` when the selection rises above it,
`top += 1` when it passes `top + rows − 1`), clamp to `count − 1`, reject
heading rows, repaint and synchronise. The keyboard handlers compute
`rows = trunc((h − 2) / (metric + 1))` — **`itemheight` is ignored** on
this path, a retail inconsistency with the click path. Programmatic
*scroll-to* uses that same `rows` count. Only a selection outside the
current visible interval changes top: `top := selected`
when `maxTop ≠ 0`, `top := min(top, maxTop)`, and the assoc'd slider knob
becomes `trunc(travel × top / maxTop)`.

**Established — the painter.** Restore the list's authored rectangle from
the window's installed background surface, or the context's alternate
background when the window has none. Only when both are absent and window
placement bit `0x80` is clear does the painter draw the common `Listbox`
tile. Otherwise existing background pixels remain. Both `SELMAP` openers
set that bit, and its installed bitmap already carries the decorative frame;
painting an additional `Listbox` tile produces an incorrect inner border.
The painter draws the first row
once the nonempty text-list branch is admitted. After each
row it subtracts `rowH` from remaining height and admits the next row only
when the remainder is at least `metric` and another item remains. Thus row
index `i > 0` requires `h − i·rowH ≥ metric`, alongside
`top + i < count`; row zero has no additional fit test. The earlier
`h − (i+1)·rowH` reading moved a post-draw test before the current row and
incorrectly dropped a row. Admission caches the current font metric and
row height before selecting the gadget’s FNT; selection leaves the active
GAF font unchanged. A later metric query for wrapped versus single-line
text does not replace that cached admission metric. The row rectangle is
`(gx+2, gy+2+i·rowH) – (gx+w, gy+2+(i+1)·rowH)`; text pen X by attribute
1 / 2 / 4 = `gx+2` / centred / `gx+w−textW`, Y the row top; a row taller
than `metric + 6` uses the word-wrapping drawer, else the single-line
drawer; text colour = the window colour-table entry the gadget's `colorf`
selects. A text heading with the `&G` prefix is drawn without that prefix; a
per-row flag byte of 1 also marks a heading but preserves its text bytes. Both
forms are darkened four times (19, 20, 21, 22 palette steps);
the selected row (attribute `0x100` clear) is lightened by 30 steps whether
or not the list has focus. Record lists draw each item's frame and lighten
the selected one by 20.

**Established — text-row pen and remap details.** Define the inclusive row
edges as `left = gx + 2` and `right = gx + w`. Alignment tests left bit 1
before right bit 4 before centre bit 2. Left uses `x = left` and width
`right - left + 1`; right uses `x = right - textW` and width `textW`;
centre uses `x = max(left, trunc((left + right - textW) / 2))` and width
`right - x + 1`. The width measurement precedes prefix removal. Without a
per-row heading flag, a leading ampersand skips two bytes; only `&G` makes
that row a heading. A row marked by a flag equal to one is a heading without
removing its text prefix. Heading shading uses four successive signed levels
`-19`, `-20`, `-21`, `-22` over the inclusive row rectangle and excludes the
ordinary selected-row brightening branch. That rectangle includes both its
right edge and bottom edge, so its extent is `w - 1` by `rowH + 1`.

**Unknown — unaligned text-list pen.** With none of alignment bits 1, 2 or 4,
the retail painter does not initialize its local pen position/width on that
branch. The native scratch history is not modeled; the current host retains
its prior four-pixel inset for this malformed/authored edge case (`TODO(T25)`).

### The kind-4 gadget: scrollbar, slider, knob and travel arithmetic, synthesised arrows, value read-out [R-WGT-01 §5]

**Established — one kind.** `SCROLLSLIDER`, `VIDSLDR`, `SLIDER%d` and the
`SHARE` sliders are all kind 4. The record holds: `travel` (authored
`range`), `knobpos` (`0..travel−1`), `knobsize`, a **read-out range**
(authored `thick`), a change callback with one argument, the `SLIDERS` GAF
and its frame base, and a *locked* word. Attribute 1 = horizontal. A
locked gadget, or one with attribute `0x10`, is inert and drawn darkened
by 20 steps.

**Established — synthesis at open.** The builder resolves `SLIDERS` from
the window's own GAF, then the common GAF. Frame base = 10 when
`w > h` (horizontal), else 0; the short axis is replaced by the base
frame's size. With no art, `travel := max(w, h) − 6`. With art, two
**button gadgets are appended** to the window (count grows by 2): frames
`base+6` and `base+8`, attributes `0x3400` (decrement arrow: auto-repeat,
not focusable, arrow-minus) and `0x2c00` (increment arrow), same `assoc`,
same `active`. Horizontal: the second arrow sits at `x + w − arrowW`, the
bar shrinks by `2·arrowW` and shifts right by `arrowW`, `knobsize :=
width(frame base+5)` and `travel := w' − knobsize − 4` (with the shrunken
`w'`). Vertical: the second arrow sits at `y + h − arrowH`, the bar shrinks
and shifts likewise, and **`travel` is left as authored** — a vertical bar
gets its travel from its list (below) or from the screen. `SHARE` overrides
both (`travel := w − h`, [R-HUD-03 §9]).

**Established — an arrow gadget's cross-axis extent.** `arrowW`/`arrowH`
above is each arrow's size on its *long* axis; **both** axes of each arrow's
rectangle are written straight from that arrow's own `SLIDERS` frame — width
and height alike, copied as a pair (in the builder's kind-4 arm, right after
the frame-base lookup) before the horizontal/vertical branch above runs. The
decrement arrow (frame `base+6`) takes its full rectangle from frame `base+6`;
the increment arrow (frame `base+8`) takes its from frame `base+8`. Neither
read touches the bar's own short axis (the `BaseExtent` frame `base` sets,
above) — the two are independent frame lookups, not a reuse. The
horizontal/vertical branch that follows only ever writes the second arrow's
position and the bar's own rectangle, knobsize and travel; it never revisits
either arrow's width or height. **Asset corroboration:** in the shipped
`anims/commongui.gaf` `SLIDERS` entry, frames `base+6` and `base+8` share the
same size on their cross axis as frame `base` itself, in both orientations
(16px for both the vertical set at base 0 and the horizontal set at base 10)
— so a renderer that reused the bar's `BaseExtent` for the arrows' cross axis
would draw pixel-identical arrows to one that reads each arrow's own frame,
for this one asset. The mechanism is still the per-frame read: nothing in the
executable ties the arrows' cross axis to `BaseExtent`, and a modified
`SLIDERS` entry with mismatched cap and arrow heights would expose the
difference.

**Established — the knob rectangle.** Horizontal: `(gx+1+knob, gy+1) –
(gx+1+knob+knobsize, gy+h−1)`; vertical: `(gx+1, gy+2+knob) – (gx+w−1,
gy+2+knob+knobsize)`; the track is the whole gadget rectangle.

**Established — pointer.** A press inside the track takes the capture; a
press inside the knob also starts a **drag**, remembering the pointer and
the knob. While captured: dragging → `knob := savedKnob + (pointer −
savedPointer)` on the bar's axis; not dragging → the knob steps by −1
when the pointer is before the knob rectangle and +1 when after, **every
pass** (unthrottled) while the button is held. Then `knob` is clamped to
`0..travel−1`; any change marks the window dirty, repaints the bar,
synchronises the assoc group and calls the change callback. Release frees
the capture and ends the drag. Keyboard: §2.

**Established — knob size and travel from an assoc'd listbox** (the bar's
painter recomputes them before every paint, so authored `knobsize` is
only honoured on a bar without a list): text list (`0x10`): `rowH =
max(metric + 1, itemheight)`, `rows = trunc((listH − 2) / rowH)`,
`knobsize = max(10, trunc(rows / count × (barH − 3)))` (single-precision
division then multiply), `travel = 0` when `count ≤ rows`, else
`barH − knobsize − 3`. Record list `0x20`: `knobsize = trunc(listH × barH
/ (firstItemH × count))`; `0x80`: `knobsize = trunc(trunc(listH /
itemheight) × barH / count)`; for these two `travel = w − knobsize`
(horizontal) or `h − knobsize` (vertical).

**Established — assoc synchronisation** (runs after any change of gadget
`g`, over every other gadget with the same `assoc` byte):

* list → list: copy `top` and `selected`.
* list → bar (`count > 1`): `knob := maxTop == 0 ? 0 : trunc(top × travel /
  maxTop)`; repaint when it changed.
* bar → list (`itemheight ≠ 0`): `top := trunc((count − trunc(listH /
  itemheight)) × (knob + e) / (travel − 1))` where `e = trunc(travel /
  (maxTop + 1))` for a `0x20` record list and 0 otherwise.
* list (attribute 8) → text input: the selected item's text replaces the
  input's text (the "type or pick" pattern of the save-name box).

**Established — painting.** With art, vertical: frame `base` (top cap) at
`(gx, gy)`, frame `base+1` tiled down while it fits, frame `base+2` (end
cap) at the bottom; knob = frames `base+3/4/5` (cap, body tiles, cap),
X centred on the track (`gx + capW/2 − knobW/2`), Y `gy + 3 + knob`,
length `min(knobsize, h − 6)` clipped to `gy + h − 4`; horizontal is the
transpose with the knob at `gx + 3 + knob`, capped at `right − knobW − 2`.
Without art the track and knob are two bevels. Attribute 4 adds a **value
label** at `(gx + w + 2, gy + 4)` in window colour entry 15: the gadget's
`text` if non-empty, else the number `trunc(knob × readoutRange / (w −
knobsize))` when the read-out range (authored `thick`) is non-zero, else
`knob` (`knob + 1` with attribute 8). This painter's divisor is `w −
knobsize`, **not** the `travel − 1` of the `SHARE` read-back helper
[R-HUD-03 §9] and of [R-FE-01 §6]'s option sliders — those screens
compute their own values and only *display* them through labels.

### Text input: focus, Enter, Escape, caret [R-WGT-01 §6]

**Established.** A press inside (left or right) sets the input's colour
from the window table, selects its font (the `fontnumber`-th font gadget,
or the window default), takes the capture **and** the focus, and re-lays
the text: kept when its length is `≤ maxchars`, else emptied. While
captured the token editor of §4 drains the queue (admission rules there).
The editor's return value drives two exits: **Enter** frees the capture and
**fires** the input (the screen sees it as the result); **Escape** empties
the text, frees the capture and fires as well — a screen cannot distinguish
"entered" from "cancelled" except by the now-empty text. Any other token
marks the window dirty. The painter fills the rectangle with colour 0 when
attribute 1 is set (else restores the background), draws the text at `(gx,
gy + 3)` limited to the gadget width, and, while captured, a one-pixel
caret at `gx + width(text[0..caret))` from `gy + 3` to `gy + 3 + metric`
in window colour entry 9. Focus arriving by Tab or by a label link (§7)
performs the same setup without a press. Quickkeys are suppressed while an
input holds the capture unless Alt is held (§3, §7). A list and a text box
cannot share focus — the window has one focused gadget and one capture; the
list→text `assoc` copy of §5 is what the save-name screen uses instead.

### Labels, links, and label quickkeys [R-WGT-01 §7]

**Established.** A label with attribute `0x10` and no quickkey is inert.
Otherwise a press inside takes the capture and a release inside fires; a
linked label also answers its quickkey (assigned by the builder from its
own text as in §3, only when `link` is non-empty; the label's authored
`quickkey` is never read). The pass then resolves `link`: the named gadget
is found by a 16-byte name compare; a **button** target that is active and
not greyed advances its stage `(stage+1) mod stages` (a plain button's
stage stays 0), is repainted, and becomes the fired result **in place of
the label** — a linked label click is indistinguishable to the screen from
a click on the button; an inactive or greyed target swallows the click.
A non-button target that is active (and, for a slider, not locked)
receives the **focus** instead (text-input setup as §6) and nothing fires.
`HELPTEXT` labels are written by the pass (§1), not by their own handler.
The label painter is [03 R-FONT-01 §6]; the builder sets attribute
`0x10` on every label whose `link` is empty, which is why plain caption
labels never react.

### Surfaces (`hotornot`), picture boxes, lines, and the focus halo [R-WGT-01 §8]

**Established.** A surface (kind 6) calls its per-pass callback every pass
with the context and the gadget (this is how map previews and save
screenshots repaint), then, only when `hotornot` is 1, behaves as a plain
button without art: press inside captures, release inside fires. Its hit
test compares the **raw screen pointer** against the gadget rectangle
without subtracting the window origin — the only handler that does — so a
hot surface in a window placed away from the origin hit-tests at the
wrong place (retail quirk; stock hot surfaces sit in full-screen windows
whose origin is 0,0). Painting: the bound GAF frame, else the bound
surface copied, else a fill with window colour entry 7. A picture box
(kind 12) blits its frame (keyed by `colorf` while it decays, §1) and
darkens by 28 steps when its own flag bit is set; a "line" gadget draws a
horizontal (attribute 1), vertical (2) or outlined (4) line in the
gadget's colour (mechanics and the outline's second coordinate below).
After every full repaint, when the window's key-navigation flag is set,
the builder paints a **focus halo** around the focused gadget: for a
button or surface six one-pixel frames growing outward with palette
lightening 31, 28, 24, 19, 13, 6; for a text input it sets `colorf` to 30
instead (lists and labels get none). Kind 8 has no runtime behaviour.

**Established — the kind-10 line painter, exactly, and `nuttin`'s runtime
role.** The painter reads only the gadget's own rectangle, its `attribs`
word, and `colorf` (as a row index into the window's colour table); it never
reads `nuttin`'s slot (§11) at all. Computed once,
unconditionally, before the attribute test: `x2 := x + w − 1` (the gadget's
own rectangle, right edge). Then, gated on the paint pass carrying the redraw
bit: attribute 1 (horizontal) draws `(x, y)–(x2, y)` and returns; attribute 2
(vertical), attribute 1 clear, redefines the second endpoint's X to `x`
(collapsing to a vertical run) and draws `(x, y)–(x, y2)` where `y2 := y + h
− 1`; attribute 4 ("outlined"), both 1 and 2 clear, draws `(x, y)–(x2, y2)` —
the `x2` from the unconditional computation above, never reassigned on this
path. That is a **single diagonal line** across the gadget's rectangle, not a
four-sided rectangle outline — "outlined" is this attribute bit's name, not a
description of the shape it draws. With none of attributes 1, 2 or 4 set,
nothing is drawn. So: the outline case's second X coordinate is `x + w − 1`,
identical to the horizontal case's, not `nuttin` — and `nuttin` has no traced
reader anywhere in the painter. `nuttin` remains parsed and retained
losslessly (§11); no runtime consumer of it has been found.

### Selection presentation: the shared eligibility predicate and the footprint quad [R-WGT-01 §9]

**Established.** The rectangle selection and the category/select-all
paths of [R-CAM-01 §2] test, per unit: status bit 5 (*selectable*) set,
construction remaining `== 0.0`, the post-capture grace counter `== 0`, and
carrier null or the carrier's status bit 30 (*cargo-selectable*) set —
byte-for-byte the predicate the trigger system shares ([08 R-TRIG-01 §3];
that section's account of the grace counter and of bit 5 applies here).
The selected unit's on-screen mark is the **footprint quad** of
[03 R-WATER-01 §1]
(root-piece bounds on the `y = min` plane, projected, four lines in
logical entry 10, always on): document 03 owns its drawing; nothing in
the widget or footer code draws a selection count ([R-HUD-03 §12]).


### The eligibility float is the remaining-build fraction, and no order state feeds it [R-WGT-01 §10]

The single-precision unit-record word that the eligibility sites of §8 and
§9 compare with `0.0` is not an order-guard float, and no standing order
keeps it nonzero. This section records the census that identifies it.

**Established — identity.** The single-precision unit-record word every
eligibility site compares with literal `0.0` is the unit's **remaining-build
fraction**: the word the construction step of [05 R-WORK-01 §1] stores, the
capture sentinel of [05 R-WORK-01 §10] tests, the constructor seeds
([04 R-COB-03 §4]) and COB port 17 (`BUILD_PERCENT_LEFT`, [04 R-COB-03 §2])
reads. The identification rests on a whole-executable census of every load
and every store of that word — the readers below are the complete load set,
the writers below the complete store set, and no pointer to the word is ever
taken — so no store site outside the list exists.

**Established — writers, exhaustively.** Five events write the word; two
whole-record copies carry it unchanged.

1. *Unit creation* [04 R-COB-03 §4]: `1.0f` when the unit is created as a
   nanoframe (the creation call's under-construction argument), integer zero
   (`+0.0f`) when it is created complete. The same branch seeds health to 0
   for the nanoframe and to the low sixteen bits of `maxdamage` for the
   complete unit.
2. *The shared construction step* [05 R-WORK-01 §1]: the clamped `0..1` new
   fraction — on the forward arm only after the two-resource admission
   succeeds, on the reverse (deconstruction/decay) arm unconditionally. The
   step returns without writing when the fraction is already `0.0`. The
   fraction is a build-work quantum divided by `buildtime`, not an
   order-state ratio.
3. *Construction completion* [04 §3.8][05 R-PROD-01 §2]: zero, together with
   the product's presentation-dirty bit; the step invokes it synchronously
   when its stored fraction reaches `0.0`.
4. *Resurrection completion* [05 R-WORK-01 §7]: zero, with health 1.
5. *Save-game restore* and *capture ownership transfer*: the value is copied
   from the saved or source record unchanged.

No routine of the order subsystem stores to the word: not the order-record
constructor, not the primary or secondary pump, not the handler
return-code epilogue, not cancel-all, not the single-record expiry helper,
and not the idle-queue refill of [04 §3.3]. The only order-related zeroing is
the build order's completion on its *product*, item 3.

**Established — readers beyond the two gates.** Every other load compares the
same word with `0.0` in one of two senses. *Finished* (`== 0.0`): point-click
selection and every bulk-selection walk — select-all, category select,
select-on-screen, control-group recall, and the next-eligible-unit loops of
[R-CAM-01 §2]; the build-menu opener, which opens no menu on a nanoframe;
`SelfRepair` and `RepairUnitNoMove`'s admit test [04 R-ORD-01 §5];
the damage-intake reaction, which only a finished unit runs [06 R-WPN-04 §2];
the per-player round-robin acquisition scan and the target-registry rebuild
[06 §3.1–§3.2]; the targeting-upgrade registry rebuild [04 R-SPEC-01 §8]; the
`BuildUnitType`, `AllUnitsKilled` and `MoveUnitToRadius` trigger visitors
[08 R-TRIG-01 §3–§4]; and the kill bookkeeping that credits a kill only for a
finished victim [08 R-SKIR-01 §3]. *Unfinished* (`!= 0.0`): the shape
chooser's `cursorrepair` rows and the command resolver's help-build routing
(§8, [04 §3.4]), `RepairUnit`'s entry branch, `GetBuilt`, the repair-candidate
filter of the repair-patrol family (which accepts a target whose health is
below `maxdamage` **or** whose fraction is nonzero), the `VTOL_HelpBuild`
phase exits, the renderer's nanoframe-versus-model choice [03 R-RAST-01 §7],
and the death bookkeeping's refund scale, which multiplies a definition cost
by `(1.0 − fraction)`. COB port 17 is the one arithmetic reader: `0` when the
fraction is exactly `0.0`, otherwise `1 − trunc(...)` per [04 R-COB-03 §2].

**Established — the idle queue is irrelevant to the predicate, and what an
idle unit's queue holds.** Because nothing in the order subsystem touches the
word, a finished unit is eligible whatever its queue contains. What it does
contain is authored data: the idle-queue refill of [04 §3.3] parks a
`defaultmissiontype` record on an empty queue. A census over
the 278 unit definitions of the reference install gives `Standby` 122,
`VTOL_Standby` 30, `Standby_Mine` 12, `Guard_NoMove` 26, and no key on 88 —
every one of the 88 a structure (factories, extractors, generators, storage,
sensors, walls, silos). So an idle stock **mobile** unit's front queue holds a
standing `Standby`-family record, an idle stock structure's queue is empty
unless it authors `Guard_NoMove`, and both are selectable and inspectable.
A build that models the standing record is modelling retail; a build that
derives an eligibility word from the queue's emptiness is not.

**Established — the two gates, restated for implementation.** Let `E(u)` be:
`u`'s status bit 5 (*selectable*) set; `u`'s remaining-build fraction compares
equal to `0.0` (an exact single-precision compare: `−0.0` passes, a NaN would
fail, and no writer produces one); `u`'s post-capture grace counter is zero
(armed only by a capture whose new owner is a remote controller — always zero
in single-player, [08 R-TRIG-01 §3]); and `u` has no carrier, or its carrier's
status bit 30 (*cargo-selectable*) is set. Clause order in the code is as
listed, short-circuiting.

* *Rectangle selection (§9):* walk the local player's unit slice in ascending
  record order; for each record with `E(u)` true, apply the inclusive
  rectangle test and the set-or-toggle of §9's truth table. A record failing
  `E(u)` is neither written, toggled, nor counted, regardless of position.
* *Idle-latch inspect (§8):* with the idle latch armed and the candidate set
  empty, the hovered unit is inspectable — `cursorselect`, index 15 — when it
  is non-null, its owner-slot byte equals the local player's, and `E(u)` is
  true. Under interface type 1 the same test runs first in the idle case and
  precedes the red/green divert. Immediately before the inspect test on the
  empty-candidate path, a hovered unit that the selection's repair-target
  predicate accepts **and** whose fraction is nonzero yields `cursorrepair`
  (index 6) instead — the same word read in the opposite sense, and the
  "friendly target needing assistance" row of §8's idle-latch list.

The "finished" clause of the build's inspect predicate — the remaining
fraction is zero — is therefore the entire "empty current-task" gate, and a
build carrying a separate order-derived guard word has one clause too many.
Every claim here is Established: the store and load census plus the
authored key census.


### The kind byte and the parser's per-kind key table [R-WGT-01 §11]

**Established** (static trace of the panel parser: the `[COMMON]` reader,
the per-kind reader it dispatches to, and the kind-0 header reader).

**The caption-localization boundary.** Every `text` field called localized in
the table below is read from the authored gadget section, passed through the
already-loaded translation table, and copied into the runtime gadget record
during parsing. This finishes before the window builder runs, so button and
linked-label quickkey assignment in §3 scans the current localized record
caption. A missing table or missing source entry supplies the authored text
unchanged under [02 "Translation table"]. For a staged button the whole
authored field first crosses this parser boundary; the later builder splits
the stored field at `|` and localizes each stage fragment again. Staged buttons
do not receive an assigned quickkey (§3).

**The kind byte.** `id` is read as an integer and stored as **one byte** —
the low eight bits — so the byte the builder and the service pass dispatch
on is `id mod 256`. After `[COMMON]` (read only when the subsection exists,
[02 R-MALF-01 §5]) the parser switches on that byte and reads exactly these
keys, with the stored width and cap:

| Kind | Keys read after `[COMMON]` |
|---:|---|
| 0 | `totalgadgets` (16-bit, later overwritten by the section count), `panel`, `crdefault`, `escdefault`, `defaultfocus` (16 bytes each); then, when a `[VERSION]` subsection exists, `major`, `minor`, `revision` (one byte each) |
| 1 | `status` (16-bit down-state word), `text` (128 bytes, localized), `quickkey` (at most 18 authored bytes, then an alphabetic first byte verbatim or the low byte of the decimal-prefix value), `grayedout` (bit 0 of the grey word, §13), `stages` (byte) |
| 2 | `itemheight` (16-bit); the list's change-callback word and its per-row flag pointer are zeroed here |
| 3 | `maxchars` (16-bit, capped at 128 here; the builder caps it again at 127, §12), then `text` (128 bytes, localized) |
| 4 | `range` (16-bit `travel`), `thick` (16-bit, widened to the 32-bit read-out range), `knobpos`, `knobsize` (16-bit each); the change-callback word zeroed; then `text` (128 bytes, localized) |
| 5 | `link` first byte and the label quickkey byte zeroed, the text buffer zeroed; `text` (128-byte buffer, localized, copied back with a 127-byte bound), `link` (16 bytes) |
| 6 | `hotornot` (bit 0 of the surface's flag word) |
| 7 | `filename` (32 bytes) into the text field |
| 8 | `filename` (32 bytes) into the text field — the same key and the same slot as kind 7 |
| 10 | `nuttin` (integer, stored as a 32-bit word at the start of the text field) |
| any other value (6 aside: 9, 11, 12, 13, 14 …) | `[COMMON]` only |

The declared count is then replaced by the number of top-level sections
minus one, as [02 R-MALF-01 §5] states. Nothing in the parser rejects an
unknown kind; the record keeps its `[COMMON]` fields and the builder (§12)
decides whether any build work follows.

**Established — quickkey reader boundary.** Its text copy retains at most
18 bytes before classification and conversion. ASCII letters take the verbatim
branch; other ASCII input uses the decimal scanner, including zero for no
digits. **Unknown:** the platform alphabetic classification of extended first
bytes and its host locale; this is separate from the builder's established
ASCII-only collision comparison. `[fmt gui]` records the deterministic host
placeholder for that residual.

### The window builder's kind switch, exactly [R-WGT-01 §12]

The whole-window quickkey preclear of §3 runs before this record loop on a
build/open operation, not during repaint-only service. Its process-lifetime
state must therefore be sampled for each build, including a return to the
frontend after battle (Established).

**Established — the table.** The builder (the routine that resolves art,
synthesises the slider arrows and paints the tree — the "control-kind
switch" of §4) dispatches on the kind byte through a **fourteen-entry jump
table** indexed 0–13 after an unsigned `> 13` bounds test. Entries 6, 9 and
10 and every value above 13 do **no build work** (the gadget keeps its parsed
record and is serviced by the pass as usual — kind 6's surface callback and
kind 10's line painter need nothing built). Eleven keys select ten arms;
kind 11 shares the panel arm with kind 0:

| Key | Arm |
|---:|---|
| 0, 11 | panel: a negative window `ypos` receives the centring offset (§4's "−1 centering"); when the window has no GAF yet, `anims\<name>` is opened as the window's own GAF; the `panel` entry is resolved own GAF → common GAF → common `BackTile` (every frame's origin zeroed) and stored as the window background |
| 1 | button (§3) |
| 2 | listbox: `LISTBOX` from the common GAF (frame origins zeroed); then every **other** kind-2 gadget with the same `assoc` byte and this one take the larger of their two `itemheight` values (both records are written) |
| 3 | text input: `TEXTINPUT` from the common GAF (frame origins zeroed); `maxchars` capped at **127**; the 128-byte text buffer zeroed |
| 4 | slider (§5) |
| 5 | label: an empty `link` sets attribute `0x10` (inert, §7), otherwise the quickkey assignment of §3 runs; `colorf` zeroed |
| 7 | font: the whole file `<font directory>\<filename>.FNT` is loaded into the gadget's file slot — the directory is the interface context's font directory, set to `fonts` when the interface starts, joined with a backslash; the extension is appended verbatim to the authored name |
| 8 | raw file: the authored `filename` is opened **verbatim** (no directory, no extension) and loaded whole into the same slot; nothing reads it afterwards (§8's "kind 8 has no runtime behaviour") |
| 12 | picture: `colorf` and the frame pointer zeroed, then frame 0 of the entry named by the gadget's name, own GAF first, then the common GAF |
| 13 | score bar: the next-due stamp ← scaled timer + the gadget's `interval` ([R-HUD-03 §11]) |

No arm only zeroes — the label and picture arms zero `colorf` (and the
picture arm its frame pointer) before their own work. The name field is 16
bytes for every kind. `[fmt gui]` carries the file-side consequences.

### The grey flag: one word, its writers and every test site [R-WGT-01 §13]

**Established.** "Greyed" is bit 0 of a per-gadget word that is **not** the
`attribs` word — `attribs` has no greyed bit. The word is written by the
parser from `grayedout` (kind 1 only; on a kind-4 record the same slot holds
the `thick` read-out range, which is why the grey/lock helper of [R-FE-02 §5]
locks a slider through its *locked* word instead) and at run time by the
grey/lock helpers (by index, by name, and the per-kind form). Within the
widget layer it is read at exactly these sites:

- the **button handler**, as its first statement: a greyed button returns
  before its own hit test, so it neither captures nor fires, whether the
  press is a click or a quickkey (§3 "greyed buttons ignore everything");
- the service pass's link resolution — a greyed link target swallows the
  click (§7);
- the key matrix — Enter's `crdefault` and Space refuse a greyed button, and
  the focus order excludes greyed buttons (§2);
- the painter's frame choice (§3);
- a gadget-dump debug printer, which prints the bit and does nothing with it.

So the test is at press/fire time, not at hover: the pass's hit test (§1
step 5) skips only hidden gadgets, so a greyed gadget still becomes the
hovered gadget and still feeds `HELPTEXT`. A reimplementation therefore needs
one boolean per gadget carrying `grayedout` and the helpers' writes; nothing
derives it from `attribs`.

### The bitmap cache, window-record words, gadget appenders and small gadget contracts [R-WGT-02]

#### The front-end bitmap cache [R-WGT-02 §1]

**Established.** Every front-end screen and the in-battle options root
request their backdrop by name through one cache (`FrontendX`, `options4x`,
`dhelp`, `drestart`, `GameSettings`, … — the names in [R-FE-01]). The
request carries the name (or null), a *clear-first* flag, an *apply
palette* flag and a *keep-window* flag:

1. With *clear-first* set, the display is cleared through the framebuffer's
   clear and presented before anything else.
2. A null name skips the cache: the image is null.
3. Otherwise the cache — **ten** entries, each an image, a palette and a
   name, most-recently-used first — is searched by exact (case-sensitive)
   name. A hit is moved to the front (the entries above it shift down by
   one).
4. A miss decodes `bitmaps\<name>.pcx` with a fresh 1024-byte palette
   buffer; a decoder failure is fatal through the modal status channel
   ([01 R-PLAT-01 §8]) with the composed path as the message. Unless the
   host mode word is 6 (the in-battle briefing of [R-FE-01 §4] — the one
   request made from inside a battle is decoded but not cached), the last entry's image is freed with its palette, every entry
   shifts down by one and the new image, palette and name take entry 0.
5. With *keep-window* clear: when **no** window is open the image becomes the
   global background image and the name is copied into the background-name
   field (an empty name when the request was null); when a window is open
   the image is handed to the **top window's background-image slot** (the
   window record's backdrop pointer) and, if *apply palette* is set, the
   decoded palette is installed as the display palette. The return is 1
   when an image was resolved (or the name was null), else 0. With
   *keep-window* set nothing is installed and the return is 0.

So the ten most recent backdrops stay decoded across screen changes, a
name is never decoded twice while it remains in the ten, and the front-end
never evicts during a load. The `.pcx` decoder itself is [02 "PCX"] /
[fmt pcx].

#### Window-record words and their setters [R-WGT-02 §2]

**Established.** The interface object owns a stack of *window records*
(one per open `.GUI`, head = top window; each record holds the gadget
array, the rectangle, the backdrop image and the words below). The
screen-side helpers that every screen calls set single words:

| Word | Meaning | Setters |
|---|---|---|
| fired-gadget index (interface object) | `−1` means none; a surviving selected index requests closure after the fired callback ([R-WGT-01 §1] step 8) | gadget/key service writes the selected index; *stay open* clears to `−1`; window opening also initializes it to `−1` |
| keyboard-navigation switch (interface object) | enables the key matrix and its unconsumed-key history/callback arm when token mode is non-zero, and enables focus/default markers after repaint ([R-WGT-01 §2]) | initialized enabled; screen transitions explicitly enable or disable it; independent of the fired index |
| redraw-request word (interface object) | 1 = repaint the top window this pass | *request redraw*; set by every text/stage/grey mutation of [R-FE-02 §5] and by the unfold of [R-HUD-04 §2] |
| dirty word (window record) | 1 = the gadget painter re-lays the whole window | *mark dirty* (through the top record; a no-op with no window); the painter and every gadget mutation set it |
| token-mode word (window record) | non-zero = the GUI pass pops keyboard tokens, zero = it peeks ([R-WGT-01 §1] step 3; with zero the text editor pops for itself) | *set token mode*; the front-end shell and the options root set 1 |
| quickkey-enable word (interface object) | exactly 1 = button and label quickkeys are honoured | initialized to 1; dialog/list close paths write 1; only the F11 developer film-mode toggle disables it; ordinary window opening does not write it |
| fired-button word (interface object, mirrored into the window record) | 1 = the fired gadget was pressed with the left button, 2 = the right button; the button, label, link and list handlers write it when they fire; a screen reads the mirror to distinguish a right-click on a row (`SKIRMISH` uses 2 for its row actions, [R-FE-01 §5]) | written by the gadget handlers only |
| held-button bits (interface object) | the mouse sample's held-button mask of [R-WGT-01 §1] step 2 (1 left, 2 right) | the sample fetch; the *held-button test* helper masks it |
| top-window backdrop pointer (window record) | the image the window painter blits behind the gadgets | the bitmap cache (§1) |

**Established — accelerator enable lifetime.** The only disabling caller is
the F11 developer film-mode toggle [R-CAM-01 §2]. Within the supported
single-player scope, accelerator service therefore stays enabled. Editor
focus does not clear this word: text capture suppresses accelerator admission
through the separate Alt rule [R-WGT-01 §3][R-WGT-01 §7]. The previous account
that ordinary window opening enables the word and typing disables it was
incorrect. Do not confuse this service state with the one-way build preclear
state [R-WGT-01 §3].

**Established — the two mouse-message predicates.** The "last mouse
message" word of [R-WGT-01 §1] step 2 holds the Win32 message identity.
*Pressed* with mask 1 is true for `WM_LBUTTONDOWN` or `WM_LBUTTONDBLCLK`,
with mask 2 for `WM_RBUTTONDOWN` or `WM_RBUTTONDBLCLK`; mask 1 is tested
first and a mask with both bits tests only the left button. *Double-clicked*
is the same with only the `…DBLCLK` identities. These are the press and
double-click tests every kind handler of [R-WGT-01 §§3–8] uses.

**Established — the window stack repaint.** Repainting walks the window
records from the **bottom** of the stack to the top (recursion before
work). A record is repainted when its dirty word is 1, or — when a clip
rectangle is supplied — when its rectangle (`x, y, x + w − 1, y + h − 1`)
overlaps the clip rectangle by the inclusive overlap test of
[03 R-COMP-01 §2]; a repaint clears the dirty word and blits the record's
backdrop image at the record's origin. Records that are neither dirty nor
overlapped are skipped, so a top window that moves leaves the windows below
untouched unless the clip rectangle says otherwise.

**Established — top-window name test.** "Is `<name>` the top window" is a
16-byte compare of the top record's name (false with no window). The page
close of [R-HUD-04 §3] and the options-close path use it.

**Established — the fired-gadget name test.** The callback-side "which
gadget fired" predicate reads the record selected by the fired-gadget index,
not the mutable focus field. With no top window or fired index `−1`, it is
false. Otherwise it compares that record's name with the wanted string using
full NUL-terminated byte equality: case and whitespace matter, and there is
no 16-byte comparison bound. This is distinct from the bounded named-lookup
helpers of [R-FE-02 §5]. Normal loaded names terminate within their name field,
so the two predicates agree for ordinary stock names.

#### Gadget appenders for the dialog builders [R-WGT-02 §3]

**Established.** Beside the *append record* of [R-FE-02 §5] (forces kind 1)
there are two more appenders with the same 200-gadget refusal: *append
text region* copies a 204-byte template into the next 347-byte record,
forces kind **6** (the text-region gadget that `MOREBAR` pages,
[R-HUD-03 §10]) and zeroes its text word, its two scroll words and its
16-bit page word; *append stat bar* copies a 214-byte template and forces
kind **13** (the score bar of [R-HUD-03 §11]). `MSGBOX`, `CDCHECK` and the
report screen use them; the three appenders are the only way a window grows
after the `.GUI` parse.

#### Small gadget contracts [R-WGT-02 §4]

**Established — set text by index, refined.** The *set text* of
[R-FE-02 §5] has two additions. For a kind-1 button whose `stages` byte is
non-zero, after the 128-byte copy and the label-fit the text is split at
every `|` into NUL-separated pieces (the multi-line / per-stage labels of
§4), each piece is re-localised and the pieces are re-packed
back-to-back into the 128-byte field — `stages` pieces are read. For a kind
3 input a non-zero fourth argument replaces the input's `maxchars` word.
Kind 5 labels get the copy and the label-fit only. Every kind sets the
redraw-request word.

**Established — the text-input caret and the length clamp.** The interface
object's caret word is the insertion index the editor of §4 uses. The
*re-lay* of [R-WGT-01 §6] is: with the *force-empty* flag clear and
`len(text) ≤ maxchars` the caret becomes `len(text)`; otherwise the text is
emptied and the caret is 0; the gadget is then re-laid.

**Established — the edit-token loop, refined.** The kind-3 editor of §4
runs as a loop: with the window's token-mode word zero it pops its first
token itself, otherwise it takes the token the pass hands it; after each
token it pops the next until the ring is empty ([01 R-PLAT-01 §6]) or an
Escape token (`0x1B`) stops the loop; it returns the last token seen, and
re-lays the gadget once if any token was processed. Per token: Backspace
(`0x08`) with the caret above 0 moves the caret back one and closes the
gap; Delete (`0xEF`) with a non-empty text and the caret below the length
closes the gap at the caret; Home (`0xF0`) and End (`0xF1`) move the caret
to 0 / the length; Left (`0xF4`) and Right (`0xF6`) move it by one within
`0..len`; the paste tokens (`0xBF`, `0xEE`) are §2's; a printable token
(`0x20..0x7F`) is inserted at the caret — shifting the tail right — when
the current length is not already `maxchars`, the attribute-`0x02` filter
admits it (alphanumeric, or space, underscore, apostrophe), and the rendered
width rule of §4 holds, and the caret advances. Other tokens are ignored.

**Established — font by `fontnumber`, gadget parse, basename.** *Select
font by gadget*: the n-th kind-7 record of the window (n = the gadget's
`fontnumber`, counting from 0 in index order, so 0 is the window's first
font record) selects its font and returns that record's index; with no such
record the default (common) font is selected and −1 returned ([R-FE-02 §5]
"focus a text input"). The `fontnumber` byte is read **signed**: a value of
128..255 is negative, never equals the non-negative walk counter, and so
selects no record — the stock 132 (`LOUNGE`, `SSIDESEL`) and 205 (`TALK2`)
are common-font gadgets even in a window that authors records, as is
`MSNBRIEF`'s 9 against three records. The four gadget painters inline the
same walk; which of them keep its result is [03 R-FONT-01 §5]. *Gadget
parse of the common keys*: `status` → the 16-bit status word; `text` →
128 bytes, then re-localised in place; `quickkey` → the byte is the first
character when it is a letter, else the decimal value of the text
(`83` → `S`); `grayedout` → bit 0 of the flag word (the other bits are
preserved); `stages` → the stages byte — the grammar is [fmt gui]. *GUI
basename*: a loader path is reduced in place to the text after its last
backslash before it is used as the window name (the `.GUI` opener of §4).

#### The keyboard-ring flush [R-WGT-02 §5]

**Established.** The flush called by the front-end controller at each screen
change, by the window open path and by the report and end-mission screens
zeroes both indices of the 30-slot key-token ring of §2 ([01 R-PLAT-01
§6]): pending tokens are discarded and the ring is empty. Nothing else is
touched — the button ring and the held-key table keep their state.

### Art-less bevels: geometry, the three colour fields, and the window fill [R-FE-02 §4]

**Established fact — geometry.** Every art-less control (a button whose name
resolves to no GAF entry, the score bar, the slider track and knob without
`SLIDERS` art) and the art-less window fill draw their edges through one
family of bevel routines that take a surface, an inclusive rectangle
`(x1, y1, x2, y2)` and colours. All lines are one pixel wide and go through
the Bresenham line primitive. The *two-pixel bevel* is eight runs:

| Run | From | To |
|---|---|---|
| 1 | `(x1, y1)` | `(x2, y1)` |
| 2 | `(x1, y1+1)` | `(x2−1, y1+1)` |
| 3 | `(x1, y1)` | `(x1, y2)` |
| 4 | `(x1+1, y1)` | `(x1+1, y2−1)` |
| 5 | `(x2, y1+1)` | `(x2, y2)` |
| 6 | `(x2−1, y1+2)` | `(x2−1, y2)` |
| 7 | `(x1+1, y2)` | `(x2, y2)` |
| 8 | `(x1+2, y2−1)` | `(x2, y2−1)` |

Given two colours `A`, `B`, the *raised* form draws runs 1–4 (top and left)
in `A` and runs 5–8 (bottom and right) in `B`; the *sunken* form swaps them
(top/left `B`, bottom/right `A`). Two wrappers fill the rectangle with a
third colour `F` first: *fill + raised* and *fill + sunken*. The *one-pixel
bevel*, used only by the art-less slider, fills with `F` and draws four
runs — top `(x1,y1)–(x2,y1)`, left `(x1,y1)–(x1,y2)`, right
`(x2,y1+1)–(x2,y2)`, bottom `(x1+1,y2)–(x2,y2)` — the track form with the
first colour on top/left, the knob form with the second.

**Established fact — who passes what.** The colours are the GUI context's
semantic colour fields (the nearest-colour map of "Retail palette contract"),
always in the order `A` = field `0`, `B` = field `17` (field `19` when
greyed), `F` = field `20` (field `19` when greyed):

| Caller | Routine | top/left | bottom/right | interior |
|---|---|---|---|---|
| art-less button, down-state word 0, not greyed | fill + sunken | 17 | 0 | 20 |
| art-less button, down-state word ≠ 0, not greyed | fill + raised | 0 | 17 | 20 |
| art-less button, greyed | fill + raised | 0 | 19 | 19 |
| score bar frame ([R-HUD-03 §11]) | fill + raised | 0 | 17 | 20 |
| window / `PANEL` fill **without** a tile entry | fill + sunken | 17 | 0 | 20 |
| window / `PANEL` fill **with** a tile entry | tiles only, **no bevel** | — | — | the tile |
| art-less slider track / knob ([R-WGT-01 §5]) | one-pixel | 0 / 17 | 17 / 0 | 20 |

So an un-pressed art-less button reads: interior field 20, top/left field
17, bottom/right field 0; pressing swaps the two edge colours.

**Established fact — the window fill.** The tile and bevel forms are
exclusive, not sequential: when the window resolved a tile entry (`BackTile`
in the window's own GAF or the common one) the fill tiles it across the
rectangle and draws **no** bevel; only when no tile entry exists is the
rectangle filled with field `20` and bevelled — runs 1–4 (top/left) in
field `17`, runs 5–8 (bottom/right) in field `0`. Field `20` is therefore
the interior of every art-less button and of the score bar, and the art-less
window/panel fill; it is never an edge colour. (A listbox background is the
same routine with the `Listbox` entry and the rectangle inset by 3,
[R-WGT-01 §4].)

**Established fact — how the tile fill tiles.** With the resolved entry `E`, the panel's
rectangle `(x1, y1)–(x2, y2)` inclusive (`W = x2 − x1 + 1`, `H = y2 − y1 +
1`), and the origin `(0, 0)` of the window's own surface for the panel itself
(gadget 0) or `(x1, y1)` for any other gadget:

* an entry with **fewer than two frames** is stamped once at the origin and
  is not tiled;
* otherwise frame 0's width and height are the tile pitch `tw × th`, and
  every tile picks its frame as `band + column`, walking rows `y = 0, th, 2th,
  …` while `y < H` and columns `x = 0, tw, …` while `x < W`:
  * `band` is `0` on the first row (`y == 0`); on later rows `6` when the
    tile would overflow the rectangle (`y + th > H`), else `3`;
  * a row that would overflow is pulled flush: `y := H − th` (the tile
    overlaps the previous row rather than being clipped);
  * `column` is `2` when the tile reaches or passes the right edge
    (`x + tw >= W`), in which case `x := W − tw` likewise; else `1`, except
    `0` when `x == 0`.

  The two edge tests differ in strictness: a row ending exactly on the bottom
  edge is a *middle* band (`3..5`), while a column ending exactly on the right
  edge is the *right* column. Frame indices `0..8` are therefore a nine-slice
  — top-left, top, top-right, left, centre, right, bottom-left, bottom,
  bottom-right — and a rectangle smaller than one tile draws a negative-offset
  top band, clipped by the surface.

*Asset census.* The stock `anims/commongui.gaf` **does** author `BackTile`:
nine 64 × 64 frames, a bevelled metal frame with a dark interior. So the
art-less bevel branch never fires for a stock window whose `panel` resolves
nowhere — `EXITMENU.GUI` (empty `panel=`), `YESORNO.GUI` (unusable bytes),
`MSGBOX.GUI` and `ARMOPT.GUI` all fill from `BackTile` as a nine-slice plate:
corner and edge frames along the border, the centre frame across the
interior. A retail capture of the in-battle exit and confirmation windows
shows exactly that plate.

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
| Campaign/mission | `newgame.gui` | `playanygame4.pcx`, `newgame.gaf` | Campaign and mission list gadgets are populated from discovered `camps` data; `Side0/Side1`, `Difficulty`, `Start`, and `PrevMenu` retain their authored callbacks. Both `NewCamp` and `AnyMsn` open the play-any layout whose background is `playanygame4.pcx`; the two `newcampaign4` files belong to the unreachable campaign layout ([R-FE-01 §4]) |
| Map selection | `selmap.gui` | `dselectmap2.pcx`, `commongui.gaf`, plus the TNT minimap surface | The map-selection callback opens the window through the window-open routine with flags `0x980`, then hands `DSELECTMAP2` to the bitmap cache, which installs it on the open window through the bitmap-install path. The authored `494×420` record at origin `(84,12)` is a panel window composed over the screen it was reached from; `MAPNAMES`, `SLIDER`, `MAPPIC`, `DESCRIPTION`, `SIZE`, `LOAD`, and `PREVMENU` remain window-local records placed at that origin |
| Skirmish setup | `skirmish.gui` | `skirmsetup4x.pcx`, `skirmish.gaf`, `commongui.gaf`, `textures/logos.gaf` | `Player%d`, `Side%d`, `Color%d`, `Allies%d`, `Metal%d`, and `Energy%d` are appended by the runtime builder; their row geometry is `step=200/n`, `y=(180-(n-1)*step)/2+79` |

`selectgame2x.pcx` is not part of this path: the multiplayer `SELGAME.GUI`
lobby path loads it (the selmap callback fetches `DSELECTMAP2` from the
bitmap cache; the SELGAME opener fetches `selectgame2x`), and that lobby is
out of scope for the single-player slice. `dselectmap2.pcx` is a `640×480` file whose panel art occupies only the
top-left `494×420`, which is exactly the `selmap.gui` window rectangle: the
window fill copies the bitmap into the window's own surface at `(0,0)`, so the
art lands at the window origin and the rest of the file is outside the window
and never presented.

#### Frontend asset failure boundaries

The single-player screen openers use one common `.GUI` window-open path and do
not test the returned window before installing callbacks or reading its gadget
list. A missing GUI file therefore does not select a second authored GUI, and
a parser rejection does not enter a caller-level recovery branch. **Established
— the absence of a caller check.** The `MSGBOX`, `YESORNO` and HUD build-page
openers do check the result; every front-end screen opener does not
([R-FE-01 §12]).

**Established — the process-level outcome.** The window opener does not test
its own result either. Its first act after building the search path is to ask
the content layer for the file; when that reports nothing, it jumps over the
**entire** allocate-and-parse block — the only block that assigns its window
pointer — and lands on the shared tail, which copies the window's name
through the still-null pointer: the process takes an access violation. There
is no diagnostic, no fallback layout, and no empty screen — the screen does
not proceed at all. The malformed case differs in shape: the parse is
attempted, and when it is rejected the opener frees the record it has just
allocated and then runs the same tail through the freed block — a
use-after-free rather than a null store, so its symptom is not fixed.
**Established for the missing file; Established that no recovery branch
exists for either.** A reimplementation cannot clone this; report the failure
and refuse the screen instead.

The single-player PCX backgrounds in this table — `frontendx`, `singlebg`,
`playanygame4`, `dselectmap2`, and `skirmsetup4x` —
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
two or more give `group*2` (the refresher counts the matching configured
rows and writes the frame index into the row's surface field with exactly
that three-way branch). The entry's twelve frames are six symbols in that
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
every frame's runtime `YOffset` (the loader reads the `I` frame's height and
subtracts it from every frame's stored YOffset in one pass). The
glyph blitter then places a frame at
`penX-XOffset, penY-normalizedYOffset`; the text loop passes the pen directly
and does not pre-add either offset. Consequently, stock `hattfont12` glyphs
whose raw `YOffset` is 11 rasterize one pixel below their pen because the
capital-I height is 12. The button text-pen arithmetic
`y + trunc((height − 1 − metric) / 2) + (stages ≠ 0)`, with the metric the
capital-I height plus two, is verified at the button painter's pen site
([03 R-FONT-01 §6]). The
startup context also selects `fonts/COMIX.FNT`
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
active palette indices. `GUIPAL.PAL` is never substituted as a second physical
UI palette, and no GUI lookup is performed again during indexed-to-RGB presentation.
**Established exception:** the results sequence replaces the active display
palette with the glamour image's decoded palette and then with the ENDMSN
background's palette [08 R-CAMP-01 §6]. [03 §4.3]
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
  rectangle. Without one the fill is the tile-fill routine: when the window
  resolved a tile entry — the stock fallback entry is `BackTile`, whose frame
  offsets the initializer zeroes — it is tiled across the window rectangle as
  a nine-slice and no bevel is drawn; only with no tile entry is the
  rectangle filled with the GUI context's semantic color field `20` and given
  a two-pixel bevel through the bevel routine `(surface, rect, c1, c2, c3)`,
  the first four edge runs in field `17` and the next four in field `0`, all
  three resolved through the nearest-color map the bootstrap builds
  ([R-FE-02 §4]). Flag `0x80`, which `selmap.gui` passes,
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
  height. Filling a text list raises an authored `itemheight` at or below the
  metric-plus-one floor to that floor; the GAF-font metric is capital-I height
  plus two and the FNT metric is its header height. The selected row is not painted
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
  An arrow moves the associated knob by one step, then synchronises the list
  [R-WGT-01 §3][R-WGT-01 §5]. A left press inside
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
the `HEADER campaignside` value to the selected side, accepting `ALL`. Both
`NewCamp` and `AnyMsn` open the play-any layout of `newgame.gui` — both lists
shown, the compressed list rectangles applied at runtime, background
`playanygame4`; the campaign-only layout, which hides the lists and selects
the side's `Arm Campaign` / `Core Campaign` file directly, is never reached
([R-FE-01 §4]).

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

**Established — the loading screen is 640×480 and the battle resize is the
transition's second half.** The `640x480` force above is the full routine of
[R-FE-02 §2]: the logical size is written, and only when the window's current
size differs is the `OFFSCREEN` surface freed, the presentation surface
dropped, the window moved to `(0,0)` at `640×480`, the presentation surface
re-created and `OFFSCREEN` re-allocated. So the loading screen composes at
`640×480` whatever `DisplaymodeWidth`/`Height` hold, and its background is
blitted whole at `(0,0)`; nothing scales, stretches or centres the picture.
Immediately after the force the transition copies `DisplaymodeWidth`/`Height`
into the *battle viewport* extent pair and derives the subrect `(128, 32)` to
`(W−1, H−33)` from them ([03 §4.1]) — the viewport is at the chosen mode
while the window is still at `640×480`. The window catches up in the
transition's second half, taken on the pass after the load thread signals
completion: the same compare-and-resize runs against `DisplaymodeWidth`/
`Height`, `OFFSCREEN` is re-created at the extent pair already holding the
mode size, and only then is `MAIN2.GUI` opened. A resize therefore never
happens while the loading screen is on screen.

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
  the load thread. The loading transition zeroes all six on entry, as two
  dword stores over the first and the fourth byte.
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

**Established — what each of the six loaders writes.**
Each bar is its own loader's own division; there is no shared progress helper,
and two of the six misbehave in ways a faithful screen would show.

| Bar | What the loader writes |
|---|---|
| `Textures` | Walking the texture directory listing, `index × 100 / (count − 1)` per entry, where `index` advances on every entry (including the `logos.gaf` entry the loader skips) and `count` is the listing's file count. After the walk, `100`. |
| `Terrain` | The map file is read in ten chunks of `size / 10` bytes and the byte is written `9, 18, 27, … 90`, one per chunk; the remainder read after the ten writes nothing. `100` is written later, by the world-setup routine, once its sort and eyeball allocations are done. |
| `Units` | `index × 100 / unitCount` per unit record, from index 1, then `100` at the end of the pass. |
| `Animation` | `index × 100 / entryCount` per entry — and **nothing else**. No writer anywhere sets this byte to 100, so the bar stops one entry short of full and its completion flash never fires. |
| `3D Data` | A fixed 32-slot page table: a counter starts at 100 and is raised by 100 *before* each live slot's write, so for `n` live slots the writes are `200 / n, 300 / n, … , (n + 1) × 100 / n` — the last **exceeds 100** and the bar overruns its grille by `100 / n` percent for one repaint. `100` is written after the loop. |
| `Explosions` | Three flat milestones with no proportional term: `20` after the first precomputed explosion table, `50` after the second, `100` after the third. |

A seventh consumer sums all six and divides by six; that aggregate is not
drawn by this screen.

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
player-slot ready table.

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

Chat lines are posted to the local message ring after the `+` dispatch in
every session kind; the `0x05` chat packet built first is dropped unsent in
single player ([08 R-OOS-01 §1]).

**The chat contract is closed.** Character token `0x0D` (Enter) opens chat
through the battle hotkey dispatcher and plays the `SmallButton` cue. The two
single-player session kinds always open `TALK.GUI`; its installed rectangle
keeps the authored left edge at 128 and is aligned to the live surface bottom.
The expanded `TALK2.GUI` form is confined to multiplayer session kind 3 and
opens only when a persistent expansion-flag byte has bit `0x01` set; its
placement parameter is reduced by `0x80` (`0x800` instead of `0x880`). Opening
installs the dialog callback, sets chat-active state bit `0x04` in the battle-interface state
byte, binds the persistent text storage into the `TALK` control, moves
keyboard focus there, configures `SENDTO`, and in the expanded battle form
also configures `SENDTYPE`. The single-player form hides `SENDTO` and
synthesises no player-recipient rows. The first open zero-fills the persistent
storage exactly once; later opens reuse
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
alias flag byte has bit `1` set, additionally OR'd with `2` when the
entry-time cheat word is nonzero. The `+` command vocabulary — 83 commands
over the mask-1, mask-2 and mask-4 tables plus the default handler — and the
dispatch mechanics are [R-CAM-01 §6]. After the command dispatch the chat
commit callback handles the mini-language inline on the same text:
`+<digit>` (occupied slot) sets the per-player custom-recipient byte for that
digit, `+a`/`+e` (case-insensitive) set the recipient-mode byte to allies or
enemies with the matching label, and any other `+...` sends the whole text as
plain chat.

**Established fact — the single-player local post.** The post uses the
registered local player name without
a fallback and composes `<name> text` (literal angle brackets followed by one
space) before the message ring's own 63-character bound. It appends routing
class 4, source-unit value 0 and speaker-slot sentinel 10 at the current
published tick. Campaign entry registers the local player as `Player` before
this path is reachable. The sentinel means this local chat line has neither a
player-logo prefix nor a `MessageArrived` cue ([R-HUD-03 §14.3]).

Activating the `TALK` control commits: the callback closes the dialog
(clearing chat-active bit `0x04` when the gadget association is generic),
re-focuses, and clears the 129-byte persistent storage (32 dwords plus one
terminator byte) plus the `SENDTO` toggle bit on every exit path. While
`TALK.GUI` is present, held-arrow camera movement is suppressed; pointer-edge
scrolling is never suppressed by chat.

### The front-end controller: phases, substates and the pump [R-FE-01 §1]

**Established fact.** The shell controller is one function called once per
front-end frame by the host-mode-2 pump. At the top of every pass, if the
*requested* substate byte differs from the *current* substate byte, the
requested value is copied into the current one; the pass then switches on
the *phase* byte and, inside most phases, on the current substate. Screen
callbacks never touch the phase directly: they write a requested substate,
and the next pass does the transition work. Two helper shapes exist: *set
phase* (phase := n, both substates := 0) and *set substate* (both := n). The
phases and their substate vocabularies are:

| Phase | Family | Substate meanings (current byte) |
|---:|---|---|
| 0 | startup | one pass: in full-screen, play the logo movie `1.zrb` when `PlayMovie` is set (then clear it and persist the preferences) or when the once-per-process replay flag is clear; then → phase 2 |
| 1 | intro | play `2.zrb`, → phase 2 |
| 2 | main menu | 0 open `MAINMENU.GUI` (a pending DirectPlay lobby launch diverts to phase 0x10 first; a command-line multiplayer flag diverts to substate 6) then → 1; 1 idle (clear/present); 5 `SINGLE` → phase 7 (sets the single-player session bit); 6 `MULTI` → mission type 3, phase 0xf; 7 → phase 0; 8 `EXIT` → process quit; 9 `Credits` → phase 3 |
| 3 | credits movie | play `5.zrb`, → phase 2 |
| 4 / 5 | campaign-complete movies | 1: play `3.zrb` (Arm) / `4.zrb` (Core) then `5.zrb`, clear the session bit, → phase 2 with host mode 2 |
| 7 | `SINGLE.GUI` | 0 open, → 1; 1 idle; 3 `PrevMenu` → phase 2; 10 `NewCamp` → open `NEWGAME` (play-any layout), phase 8; 0xb `Skirmish` → reload preferences, mission type 2, phase 9; 0xd → phase 10 and open the options root (no callback requests 0xd: phase 10 is unreachable, and `Options` opens the root as a child window over `SINGLE` instead); 0xe `AnyMsn` → open `NEWGAME` (play-any layout), phase 8 |
| 8 | `NEWGAME.GUI` | 1 idle; 3 `PrevMenu` → phase 7; 0xf `Start` (campaign layout) → mission type 1, phase 0xb; 0x10 `Start` (play-any layout) → mission type 1, phase 0xc |
| 9 | `SKIRMISH.GUI` | 0 open → 1; 2 `Start` accepted → raise the battle-start bit; 3 `PrevMenu` → phase 7 |
| 10 | options (front-end) | 1 idle; 3 → phase 7. Unreachable: nothing requests substate 0xd of phase 7, and the only writer of substate 3 for this phase has no caller |
| 0xb / 0xc / 0xd / 0xe | briefing (`MSNBRIEF.GUI`) reached from the campaign layout / the play-any layout / the end-mission screen / a between-missions save | 0 open → 1; 2 `Start` → raise the battle-start bit; 3 `PrevMenu`: 0xb → reopen `NEWGAME` (campaign layout), 0xc → reopen `NEWGAME` (play-any layout), 0xd → reopen `ENDMSN` under host mode 7, 0xe → host mode 2, phase 7 |
| 0xf / 0x10 / 0x11 / 0x14 | multiplayer setup, lobby, network battle host, connection dialogs | out of scope; the edges that enter them are `MULTI` (phase 2 → 0xf) and the lobby-launch detector (phase 2 or 0xf substate 0 → 0x10 substate 0x12) |

The pump that drives this is the host-mode-2 frame handler: it runs the
controller, then tests the *battle-start bit* of the session word; when set
it selects host mode 4 for mission type 1 (campaign load) or host mode 5 for
type 2 (skirmish load) — these are the loading-screen transitions of
§5 "The loading screen" — and otherwise leaves the front end running. Host
mode 7 is the post-battle controller (§10). Every phase or substate write is
preceded by a code-segment checksum probe whose failure text is
`Code segment checksum error found when switching FE states.` followed by
the source line and file; the message is a `MSGBOX` and does not abort.

Three helpers recur below. The *CD check* takes 0 (a disc whose
`TOTALA_ID` `[Contents]` names `Campaign`, reported as "Disc 2") or 1
(`Multiplayer`, "Disc 1"), walks the drive letters, and returns the drive
letter or zero; a no-CD build flag short-circuits it. On success every
caller runs the *archive mount pass* (`rev*.gp3`, `*.ccx`, `*.ufo`, the
first ten `*.hpi`, then the disc's `*.hpi`). The *mission record* is
re-allocated with a type word — 1 campaign, 2 skirmish, 3 multiplayer,
0 none — whenever a family is entered; the loading screen and every
in-battle menu branch on that word. The cursor is switched to the busy
shape while a screen opens and back to the arrow when a callback leaves.

### The single-player transition table [R-FE-01 §2]

**Established fact.** Gadget names are authored; the callback association is
by name through the forward scan of §5 (first match wins). "MSGBOX" means
the message box of §9 with the quoted text; it returns to the screen it was
raised over. Every listed cue is the GUI sound played through the sound-cue
table before the transition.

| Screen | Gadget / key | Effect | Successor |
|---|---|---|---|
| `MAINMENU` | `SINGLE` (S) | cue `BigButton`; requested 5 | `SINGLE` |
| `MAINMENU` | `MULTI` (M) | mount pass; needs the `multiplay` content root, else MSGBOX `Please insert the Multiplayer CD (Disc 1) and try again`; requested 6 | multiplayer (OOS) |
| `MAINMENU` | `INTRO` (I) | cue `smlButton`; windowed → MSGBOX `Debug:  You must be in full-screen mode to play a movie.`; no disc (check 0 and 1) → MSGBOX `Please insert a Total Annihilation CD and try again`; else latch Shift-held (repeat until a character key cancels), drain input, phase 1 | intro movie, then `MAINMENU` |
| `MAINMENU` | `EXIT` (E) | requested 8 | process quit |
| `MAINMENU` | `Credits` | cue `smlButton`; same full-screen and disc gates; requested 9 | credits movie, then `MAINMENU` |
| `SINGLE` | `NewCamp` (N) | disc check 0 else MSGBOX (Disc 2); mount pass; cue `BigButton`; requested 10 | `NEWGAME` (play-any layout) |
| `SINGLE` | `AnyMsn` | disc check 0; mount pass; cue `bigButton`; requested 0xe | `NEWGAME` (play-any layout) |
| `SINGLE` | `Skirmish` (S) | disc check 1 else MSGBOX (Disc 1); mount pass; cue `skirmish`; requested 0xb | `SKIRMISH` |
| `SINGLE` | `LoadGame` (L) | cue `BigButton` (it shares `NewCamp`'s alias); opens the load dialog over `SINGLE` | `LOADGAME` (load mode) |
| `SINGLE` | `Options` (O) | cue `options`; opens the options root as a child window over `SINGLE` (phase 7 unchanged; `PREV`/`CANCEL` pop it) | `STARTOPT` |
| `SINGLE` | `PrevMenu` (P) | cue `Previous`; requested 3 | `MAINMENU` |
| `NEWGAME` | `Start` (S), or a click in `Missions`, or in `Campaign` (campaign layout) | cue `bigButton`; disc check 0 else MSGBOX; mount pass; select the campaign (§4); load the mission; failure leaves the screen; success: side records fixed (player 0 → 0, player 1 → 1), preferences saved, requested 0xf (campaign layout) / 0x10 (play-any) | `MSNBRIEF` |
| `NEWGAME` | `PrevMenu` (P) | cue `Previous`; requested 3 | `SINGLE` |
| `NEWGAME` | `Difficulty` | cue `SmlButton`; cycles the difficulty word 0→1→2→0 | — |
| `NEWGAME` | `Side0` / `Arm`, `Side1` / `Core` | cue `SideSelect`/`SideSelect2`; side word and the two side records rewritten; campaign (and mission) lists rebuilt | — |
| `MSNBRIEF` | `Start` (S) | disc check 0 else MSGBOX; mount pass; stop narration; requested 2 | loading screen → battle |
| `MSNBRIEF` | `PrevMenu` (P) | stop narration; cue `Previous`; requested 3 | `NEWGAME` / `ENDMSN` / `SINGLE` by phase (§1) |
| `MSNBRIEF` | `SHUTUP` | stage 0 stops narration; stage 1 replays it | — |
| `MSNBRIEF` | `TextRegion` / `MOREBAR` | cue `More`; next text page | — |
| `SKIRMISH` | `Start` (S) | preflight of §5; requested 2 | loading screen → battle |
| `SKIRMISH` | `PrevMenu` (P) | requested 3 | `SINGLE` |
| `SKIRMISH` | `SelectMap` | opens the map chooser over `SKIRMISH` | `SELMAP` |
| `SELMAP` (from skirmish) | `LOAD` (S) / `MAPNAMES` | copies the selection into the skirmish map name and loads its OTA; `MapName` label refreshed | `SKIRMISH` (default close) |
| `SELMAP` | `PREVMENU` (Esc) | cue `Previous` | `SKIRMISH` |
| `STARTOPT` / `PREFS` | `SOUND` / `MUSIC` / `SPEEDS` / `VISUALS` | cue `Options`; the page is merged into the open options window (§6) | same window |
| `STARTOPT` / `PREFS` | `PREV` ("OK", Esc/Enter) | cue `Options`; preferences saved to the registry | previous screen |
| `STARTOPT` / `PREFS` | `CANCEL` | cue `Previous`; every audio, interface and visual value restored from the entry snapshot | previous screen |
| `ARMOPT` (Esc in battle) | `LOADGAME` / `SAVEGAME` / `PREFS` / `HELP` / `MISSION` / `EXIT` / `OK` | §7 | `LOADGAME` / `LOADGAME` / `PREFS` / `HELP` / `BRIEFING` or `GAMEOPTIONS` / `EXITMENU` / battle |
| `TABMENU` (Tab) | `OPTIONS` / `SHARE` / `CONTROL` / `ALLIES` / `CANCEL` | §7 | `ARMOPT` / `SHARE` / `CONTROL` / lobby allies (OOS) / battle |
| `EXITMENU` | `MAINMENU` / `EXITGAME` / `RESTART` / `CANCEL` | §7 | `YESORNO` / `YESORNO` / `RESTART` / battle |
| `YESORNO` (surrender) | `CHOICE1` "Yes" (Y) | stop sounds; teardown; main-menu variant → host mode 1 → front end; exit variant → quit | `MAINMENU` or process quit |
| `YESORNO` | `CHOICE2` "No" (N, Enter, Esc) | — | battle |
| `RESTART` | `RESTART` | disc check by mission type else MSGBOX; mount pass; difficulty := stage; restart flag raised | battle restart (consumer: doc 08) |
| `RESTART` | `CANCEL` (authored Esc/Enter defaults; skipped by the zero-mode matrix) / `Difficulty` | — | battle |
| `HELP` | `Page` | page loaded (§7) | — |
| `HELP` / `BRIEFING` / `GAMEOPTIONS` / `MSGBOX` / `CDCHECK` | `OK` | default close | caller |
| `LOADGAME` (save) | `LOAD` ("OK"/Enter), `GAMES`, `GAMENAME` | cue `smlbutton`; writes `SAVEGAME\<name>.SAV` when the name box is non-empty (§8) | — |
| `LOADGAME` (save) | `DELETE` | cue `SmallButton` (a different alias from `SMLBUTTON`); deletes the selected `.SAV`, re-enumerates, rewrites the summary panel | — |
| `LOADGAME` (load) | `LOAD` / `GAMES` | disc check by the save's game type else MSGBOX; cue `SMLBUTTON`; validates and applies the save (§8); battle-start bit | loading screen → battle, or `MSNBRIEF` for a between-missions save |
| `LOADGAME` | `CANCEL` | cue `Previous`; the four enumeration allocations are freed | caller |
| `ENDMSN` | `Start` (S) / `Missions` | disc check 0 else MSGBOX; mount pass; load the chosen mission; phase 0xd | `MSNBRIEF` |
| `ENDMSN` | `MainMenu` (M) | phase 2, host mode 1 | `MAINMENU` |
| `ENDMSN` | `LoadGame` / `SaveGame` | dialogs over `ENDMSN` | `LOADGAME` |
| `ENDMSN` | `Difficulty` | cycles the difficulty word and the skirmish difficulty | — |

Asset failure: every opener in this table goes through the common window
open of §5 "Frontend asset failure boundaries"; the `MSGBOX` opener and the
four `YESORNO` openers are the exception and test the returned window for
null (a missing `MSGBOX.GUI`/`YESORNO.GUI` silently shows nothing and the
caller continues). The backgrounds named below go through the fatal bitmap
loader of that section. **Established.**

### Startup, the movies and `MAINMENU` [R-FE-01 §3]

**Established fact.** Movies are files `Data\<n>.zrb`; a missing file is
skipped silently (the player is entered only when the file exists). `1.zrb`
is the logo, `2.zrb` the intro, `3.zrb`/`4.zrb` the Arm/Core campaign
endings, `5.zrb` the credits. The logo plays only in full-screen and only
when the `PlayMovie` preference is set (it is then cleared and the
preferences persisted, so it plays once per install) or when the
process-level replay flag is clear; the intro after it is unconditional in
that pass. `INTRO` samples Shift once when the button activates. If held,
playback reopens the movie after each ending until a character key cancels;
releasing Shift alone does not change that latch. The callback is installed
by the live main-menu loader. The earlier claim that only a dead debug
routine writes the repeat latch was incorrect. Playback drains the input
queue before returning. Detailed playback, sizing and sound contracts are
[08 R-OOS-01 §4] and [03 §9].

The shell loader closes every open window, clears the display, opens
`MAINMENU.GUI` with the deferred-fill flag, installs the callback and the
per-frame shimmer tick (§5 SPARKS), loads `bitmaps\FrontendX.pcx`, starts
the `BGM` front-end loop ([03 R-AUD-01 §5]), rebuilds the semantic colour
map from `palettes\guipal.pal`, selects the `COMIX` font, and writes the
version string `v3.1` into the `DebugString` label. **Established:** this is
a literal built into the executable, copied unchanged by the menu loader;
it is not read from `gamedata/version.tdf` or formatted from GUI version fields.
The loader activates the authored inactive label, replaces its text, then
subtracts half the measured text width from its authored horizontal position,
using integer division. Measurement uses the primary GAF font when present
(the sum of glyph frame widths), otherwise the active FNT. The label keeps its
other authored geometry and drawing attributes. Reopening starts from the
authored position again. Four once-per-process
prompts follow, in order: `YESORNO` `Close Windows CD Player?` (only when a
CD-player process is detected; `CHOICE1` closes it), a `MSGBOX` warning
about the installed DirectX version, `No sound driver is available for
use.` when the sound device failed, and the `Revision.GPF` version check of
[02 §6].

`MULTI` requires the `multiplay` content root (a directory/archive probe)
rather than a disc; `INTRO` and `Credits` require full-screen and either
disc. The `EXIT` substate quits through the post-shutdown path directly; no
confirmation is asked.

### `SINGLE`, `NEWGAME` and the briefing screens [R-FE-01 §4]

**Established fact — `SINGLE.GUI`.** Opened with no flags over a cleared
display, background `singlebg`; the mission record is re-allocated as type 1
on entry. The registry `side` value (0 Arm, 1 Core) is written into the
local player's side record and its inverse into the next slot. `AnyMsn` is
authored inactive and is shown only when the `AllMissions` preference bit
is set. On a Spanish install the `Skirmish` button's quickkey byte is set
to `s` (`0x73`) through *set quickkey by name* ([R-FE-02 §5]), so the
Spanish label keeps the English hotkey.

**Established fact — `NEWGAME.GUI` has two layouts, and the shell reaches
only one.** The opener takes a flag: 0 selects the *campaign* layout
(background `newcampaign4` when more than two `camps\*.tdf` exist, else
`newcampaign4x` with the campaign list hidden and the side's `Arm Campaign`
/ `Core Campaign` document selected directly), 1 selects the *play-any*
layout (background `playanygame4`; `Campaign` and `CampaignKnob` moved to
y = 308 with height 48, `Missions` height 62; both lists shown and filled).
Both `NewCamp` and `AnyMsn` reach the opener through controller substates
whose call sites pass 1. The only call with 0 is the `PrevMenu` return edge
of phase 0xb, and phase 0xb is entered only by a `Start` issued from the
flag-0 layout; the campaign layout is therefore unreachable in this
executable and `newcampaign4`/`newcampaign4x` are never loaded. What differs
between `NewCamp` and `AnyMsn` is only the sound cue and the substate
number; the screen, background and lists are identical.

The campaign list is rebuilt for the current side (the campaign-side filter
of [08 R-CAMP-01 §2]); selecting a `Side0`/`Arm` or `Side1`/`Core` button
rewrites the side word, both side records, and both lists. `Difficulty` is
a three-stage button cycling the difficulty word 0→1→2→0, initialised from
the registry `Difficulty`. `Start` (or a click in `Missions`) loads the
campaign named by the `Campaign` selection and the mission at the
`Missions` selection index through the mission loader; failure keeps the
screen open with no dialog; success writes the two side records (player 0
side 0, player 1 side 1 — the side *bytes* of the lobby records, distinct
from the side word), saves the preferences, and requests the briefing.

**Established fact — `MSNBRIEF.GUI`.** Background `mbrief<planet>`, the
planet tables of "Single-player and campaign" above, the per-mission wind
draw, `SOLARSYSTEM` hidden, `SHUTUP` stage 1 (narration on), the wrapped
briefing text paged through `TextRegion`/`MOREBAR` ([R-HUD-03 §10]), and
narration started through the mission's narration key (skipped under host
mode 6). `PrevMenu` returns to the screen the briefing was reached from
(§1, phases 0xb–0xe). The in-battle `BRIEFING.GUI` (`ARMOPT` → `MISSION`
in a campaign) is the same text and pager over background `igmbrief`, with
`OK` closing it.

### `SKIRMISH` start preflight and `SELMAP` [R-FE-01 §5]

**Established fact.** The row controller, alliance icons, resource steps and
colour rules are closed in "Retail closure for the single-player menu
slice" and [08 R-SKIR-01]; this section adds the edges. On entry the
difficulty word is loaded from the skirmish record's difficulty, and a map
name that no longer resolves is replaced by the first map of the census.
`Start` runs, in order: cue `BigButton`; disc check 1, whose failure raises
the Disc 1 `MSGBOX` **and then continues** (the check gates nothing here —
a retail quirk, not a Nanolathe contract to copy); the mount pass; the map
terrain must load (`The terrain for the selected map does not exist.`); at
least one `Computer` and at least one `Player` row (`There must be at least
one player and one computer opponent`); the map's start-position count must
be at least the row count (`There are too many players enabled for this
map`); the alliance preflight of that section (`All players may not be in
the same allied group.`); then the row count is stored, the slots written,
the preferences saved, and the battle-start substate requested. Each
failure `MSGBOX` returns to `SKIRMISH`. `LineOfSight` is a three-stage
cycle over the record's two words (lineofsight, lostype): (0,·)→(1,1)
"Terrain elevations affect a unit's view.", (1,1)→(1,0) "…do not affect…",
(1,0)→(0,1) "All mapped terrain is visible."; `CommanderDeath`,
`StartLocation` and `Mapping` toggle their word and write the matching help
line into `HELPTEXT`.

`SELMAP.GUI` opened from skirmish uses placement flags `0x880` (the lobby
opener uses `0x980` and keeps the previous name for `PREVMENU` restore);
an empty census raises `There are no skirmish maps to choose from` and does
not open. The map census reads `Maps\*.ota`, keeps maps that pass the
multiplayer-capable filter, and switches the cursor to busy while scanning.

**Established — map preview.** Clear the entire authored `MAPPIC` canvas to
palette index zero before drawing the selected minimap. Its aspect comes from
the usable map extents `EW = TNT.Width*16 - 32` and
`EH = TNT.Height*16 - 128`, rather than the stored minimap raster's aspect.
For stored size `SW × SH` and canvas size `CW × CH`, when `EW < EH`,
crop the source to its top-left `trunc(EW*SW/EH) × SH` and fit it to
`trunc(EW*CW/EH) × CH`; otherwise crop to
`SW × trunc(EH*SH/EW)` and fit to `CW × trunc(EH*CH/EW)`.
Centre the fitted short axis using half the unused canvas extent, truncated.
The crop excludes authored padding [fmt tnt]. The shared quad mapper receives
source corners through `(srcW-1, srcH-1)` and destination corners through
`(x+dstW, y+dstH)`. The clear leaves black bars around the centred picture.
The mapper divides the 16.16 source corner spans by the destination extents
before accumulating each step, with no half-pixel bias. Exclusive spans
clipped to the surface's inclusive last coordinates leave its final canvas
row and column clear [03 R-RAST-01 §1].
The surface gadget then maps that canvas a second time: source corners
`(1,1)` through `(CW-1,CH-1)` map to gadget corners `(gx,gy)` through
`(gx+CW-1,gy+CH-1)`. The same fixed-step, exclusive fill leaves the gadget's
last row and column as backdrop. Thus the first displayed pixel samples
canvas `(1,1)`, and the horizontal source step is
`trunc(((CW-2)*65536)/(CW-1))`, with the corresponding vertical expression.

### The options family and the slider arithmetic [R-FE-01 §6]

**Established fact — window.** The options root is `STARTOPT.GUI`
(front end, background `options4x`) or `PREFS.GUI` (in battle: the
options-open bit is set and, outside a multiplayer game, the pause bit).
Entering copies the current window surface to a `FLIPSURFACE` backup and
allocates a 300×480 `BKUPSURFACE`; it snapshots the 83-byte preference
block, the two session mapping/LOS bits, the game speed, the scroll speed,
the mixer state and the 100-entry CD list — the `CANCEL` and per-page
`UNDO` sources. `MUSIC` is grayed when no CD device exists. The four page
buttons open their `.GUI` with the *merge* flag (`0x200`): the page's
gadgets are appended to the open window (centred inside its `PANEL` when
one exists); in battle the `…RT.GUI` variants are used, the window is
widened by 150 and a `PANEL` gadget synthesised, and gadgets whose names
begin `MAP` or `VID` are hidden. `PREV` ("OK") saves every preference to
the registry (§11); `CANCEL` restores the snapshot and re-applies gamma and
volumes. Leaving through the tab-close path writes requested substate 3.

**Established fact — `VISUALS` / `VISUALRT`.** `ANTI`, `BSHADOWS`, `SHADING`
are two-stage buttons bound to bits 1, 4, 5 of the display option word
([02 §3]); `BSHADOWS` also copies bit 4 into bit 3 and bit 3 into bit 2,
so one control drives `FeatureShadows`, `VehicleShadows` and `Shadows`. In
battle each change relights the terrain. `RESTORE` sets bits 1–5, gamma 12
and (front end only) 640×480 with `DitheredFog` cleared; `UNDO` restores
bits 1–6, gamma, and (front end only) the display size from the snapshot;
both reopen the page. `VIDSLDR` indexes the display-mode table (sorted
ascending by width then height, modes below 640×480 dropped) and writes
`DisplaymodeWidth`/`Height`; `VIDVAL` shows `%d X %d`. `SELVMODE.GUI` is
the stand-alone form of the same slider; its opener branch has no live
caller (every call site passes the merged-page flag), so it is never shown.

**Established — the page background, the page names, and where the merge
puts a page's gadgets.** Each page draws one full-screen plate —
`OptSound4x`, `Optmusic4x`, `OptInterface4x`, `OptVisual4x`, with the
in-battle `Igopt…x` family beside them; `OptVisual4x`'s middle column lines
up with `VISUALS.GUI`'s authored rectangles.

*The background.* The page opener picks it, not the window opener and not a
repaint of the root: each of the four page routines calls the merge-flag
window open and then, in the very next statement, hands its own bitmap to
the shared bitmap cache. The in-battle arm of the same four
routines opens the `…RT.GUI` variant and hands **no** bitmap at all, leaving
the battle visible behind the page.

*The page files.* The root's `SOUND` button opens `SOUNDS`, not `SOUND.GUI`.
`SOUND.GUI` (7 gadgets, 517×268, authored at (49, 182)) is a different file
this family never opens. The four merged pages are
`SOUNDS` (authored at (0, 1)), `MUSIC` (0, 0), `SPEEDS.GUI` (0, 0) and
`VISUALS.GUI` (0, 0); the in-battle variants are `SOUNDSRT.GUI`,
`MUSICRT.GUI`, `SPEEDSRT.GUI`, `VISUALRT.GUI`.

*The placement.* The merge arm parses the page into the slot one past the
open window's last gadget, then searches the open window's gadgets — from
index 1, comparing the full 16-byte name field — for one named `PANEL`.
With **no** `PANEL` it adds the *page's own* window-header x and y to every
one of the page's gadget rectangles, as 16-bit adds. With a `PANEL` it
zeroes that gadget's own active byte, raises the centring flag in the open
flags, and offsets each page gadget by `(panel.w − page.w) / 2 + panel.x`
and `(panel.h − page.h) / 2 + panel.y` — a signed C divide, truncating
toward zero rather than an arithmetic shift, so an odd difference biases
toward the origin on both signs. It then adds the page's gadget count to the
open window's and copies the page's gadgets down over the page-header slot,
discarding that header. `STARTOPT` authors no `PANEL` (its gadgets are
`SOUND`, `SPEEDS`, `VISUALS`, `PREV`, `CANCEL`, `MUSIC`, `RESTORE`, `UNDO`),
so the front-end family always takes the origin-add arm, and `SOUNDS`'s
(0, 1) shifts that whole page down one pixel. The in-battle arm is the one
that reaches the `PANEL` branch, since it synthesises the gadget.

**Established — the options family's cue column.**
Every control the four page callbacks and the root callback recognise plays
`Options`, and `CANCEL` alone plays `Previous`. That includes `RESTORE` and
`UNDO`, which the transition table of [R-FE-01 §2] does not list: both arms
on every page converge on a shared tail that repaints and plays `Options`.
It also includes the two-stage and list buttons (`ANTI`, `SHADING`,
`BSHADOWS`, `MODE`, `SPEECH`, `LEFTCLICK`, `UNITCHAT`, `TRACKMODE`,
`TRACKTYPE`, `NOTRAK`, `CDPLAY`, `CDNEXT`, `CDPREV`, `CDSTOP`) and the
video-mode button.

Two kinds of control are silent. The sliders are driven by their own value
callbacks, and none of them — `VIDSLDR`, `GAMMA`, `FXVOL`, `MUSICVOL`, `GAME`,
`SCREEN`, `TXTSCROL`, `MAXLINES` — plays anything, so a knob move, a drag or an
arrow step makes no sound. And the sound page's `TEST` plays
`sounds\explode.wav` and returns without reaching the shared tail, so it plays
no family cue either ([03 R-AUD-01 §2]).

**Established — `VIDSLDR`'s maximum.** It is the
mode table's count minus one, written into the slider record by the page
opener out of the table it has just built — not a stored constant like
`GAMMA`'s literal 20 beside it. The slider's change callback and a pointer
to the table it indexes are installed in the same breath.

**Established fact — `SPEEDS` / `SPEEDSRT`.** `GAME` (max 21) → game
speed, applied at once through the speed setter of [R-CAM-01 §3];
`SCREEN` (max 65) → scroll speed byte; `TXTSCROL` (max 20) → `textscroll`,
label `%d secs`; `MAXLINES` (max 30) → `textlines`, label `%d` or the
`0`-text `None`; `LEFTCLICK` two stages → the `Interface Type` word
([R-CAM-01 §5]); `UNITCHAT` three stages → `unitchattext := stage × 5`.
The two labels are written by name to gadgets called `TEXTSCROLLTEXT` and
`MAXLINESTEXT`, and **neither `SPEEDS.GUI` nor `SPEEDSRT.GUI` authors a gadget
of either name**: the text setter finds nothing and the two read-outs are never
drawn on the stock files. The page's own `GAMETEXT` label is authored empty and
nothing writes it, so it is blank too.
`RESTORE` sets textscroll 10, textlines 10, game speed 10, scroll speed 32,
`LEFTCLICK` 0, unitchat 10, unitchattext 5; `UNDO` restores the snapshot.
After the page opens every slider's value callback runs once so the labels
match. Clamps: `GAME` value < 1 → 1; `SCREEN` value ≤ 1 → 1; `MAXLINES`
value < 0 → 0; the others none.

**Established fact — slider arithmetic.**

* Read-out, on every knob move: `value = trunc(pos / (travel − 1) × max)`
  where `pos` is the knob position, `travel` the knob travel length the
  scrollbar synthesiser computes — exact forms per [R-WGT-01 §5]:
  `max(w, h) − 6` with no art, `w' − knobsize − 4` with `w' = w − 2·arrowW`
  for horizontal `SLIDERS` art, and the authored `range` left as is for
  vertical art — and `max` the per-slider maximum above; `travel < 2` reads
  0.
* Position, on page open: `x = min(value, max) × (travel − 1) × (1/max)`
  with the reciprocal stored as a single-precision constant (1/20, 1/21,
  1/65, 1/30; `VIDSLDR` divides instead); then `pos = trunc(x)`, and if
  `x − pos ≠ 0` the position is `trunc(x + 1)` — a ceiling for non-integral
  `x`, not a rounding.
* `GAMMA` (max 20): the stored integer `g` is applied as the palette factor
  `0.5 + g / 24` (12 → 1.0, 0 → 0.5, 20 → 1.333), and the wave/CD volumes
  are re-pushed (`v << 10`) whenever it changes.

`SOUND`/`MUSIC` pages: gadget maps and effects are closed in
[03 R-AUD-01 §2] and [03 R-AUD-01 §4]; nothing here re-traces them.

### The in-battle menus [R-FE-01 §7]

**Established fact.** `ARMOPT.GUI` (Escape; flags `0x800`) grays `SAVEGAME`
and `LOADGAME` in a multiplayer game, relabels `MISSION` to the translated
`Settings` for skirmish and multiplayer (session kind 2 or 3; the button is
relabelled through the gadget text setter, never hidden or greyed — a retail
skirmish capture shows `Settings` in the `Briefing` slot),
sets the pause bit outside multiplayer and pauses
audio; its close clears both and the options-open bit. Buttons: `LOADGAME`
/ `SAVEGAME` → the two `LOADGAME.GUI` modes (§8); `PREFS` → the options
root (§6); `HELP` → `HELP.GUI` (flags `0x1881`, background `dhelp`) whose
`Page` three-stage button loads page `p` of `gamedata\help.tdf` section
`[Help]`: keys `Line<n>` for `n = 17p … 17p+16`, each split at `|` into a
key column (x 40, width 78) and a description column (x 125, width 300),
rows from y 50 in steps of 18, both localised; `MISSION` → `BRIEFING.GUI`
in a campaign, else `GAMEOPTIONS.GUI` (flags `0x1881`, background
`GameSettings`): read-only rows *Commander Death* (`Game Continues` /
`Game Ends` / `Deathmatch`), *Starting Locations* (`Random`/`Fixed`),
*Mapping Mode* (`Mapped`/`Unmapped`), *Line of Sight* (one of three
labels selected by the session's two LOS bits), then in multiplayer
*Cheat Codes* and *Watching* (`Disallowed`/`Allowed`) and otherwise
*Difficulty* (`Easy`/`Medium`/`Hard`), then *Map*, *Starting Metal*,
*Starting Energy* (lobby words × 100 in multiplayer, the skirmish record
otherwise) and *Max Units*; `EXIT` → `EXITMENU.GUI`; `OK` → close.

#### Tab options menu and manual exit

`TABMENU.GUI` (Tab; the in-battle menu bar of §11): its `OPTIONS`
sets the options-open bit and opens `ARMOPT`, `SHARE` opens the transfer
dialog [R-HUD-03 §9], `CONTROL` opens `CONTROL.GUI` (host-only player
control: `WATCHING` and `GAMEOPEN` toggles of the lobby word, `LIVEPLYR<n>`
→ a `YESORNO` "Reject: <name>" that kicks the peer — multiplayer only, edge
recorded, semantics out of scope), `ALLIES` the lobby alliance panel
(out of scope).

`EXITMENU.GUI` (flags `0x1800`, centred) shows `RESTART` (authored
inactive) only for campaign and skirmish, and hides `MAINMENU` when the
session was launched from a DirectPlay lobby. `MAINMENU` and `EXITGAME` set
the exit-kind word (0 / 2), close, and open `YESORNO` (flags `0x1000`)
titled `Surrender this battle and return to main menu?` / `Exit the Battle`
(lobby-launched) or `Surrender this battle and exit to Windows?`, with both
Enter and Escape bound to `CHOICE2` ("No") and focus on it. `Yes` stops all
sounds and, for kind 0/1, tears the battle down, closes every window down
to the HUD, clears, and selects host mode 1 — the *return to front end* handler:
phase 2, session bits 0/2/3 cleared, mission record type 0, victory bits
cleared, then host mode 2 — so the next frame opens `MAINMENU`; for kind
2 it raises the surrender bit and quits.

`RESTART.GUI` (flags `0x1000`, background `drestart`) wraps the map name
into `MISSIONNAME`/`MISSIONNAME1` and focuses `Difficulty`; `RESTART` does
the disc check for the mission type, the mount pass, stores the
`Difficulty` stage and raises the restart request. The battle host pump
consumes it through teardown and normal campaign/skirmish re-entry
[08 R-CAMP-01 §8 "In-battle restart request and re-entry"]. **Established
(direct static caller trace).** The exit menu closes before this dialog opens.
Cancel closes only the restart dialog: it does not reconstruct the exit menu,
and the surviving root options window continues to own pause/audio pause.
The same close-before-open rule applies to `YESORNO`; its No action, including
Enter/Escape, returns to the surviving options window [R-WGT-01 §1].

### Save and load [R-FE-01 §8]

**Established fact.** One `LOADGAME.GUI` serves both directions. *Save*
(flags `0x880`, background `DSAVEGAME2`, from `ARMOPT`/`ENDMSN`): the pause
bit is set, `SAVEGAME\` is created, the list is filled with descriptions,
`DELETE` is hidden when the list is empty, `GAMENAME` becomes the text
entry with focus, `LoadGame` is hidden. `LOAD`/`GAMES`/`GAMENAME` with a
non-empty name writes `SAVEGAME\<GAMENAME>.SAV` through the save writer of
[08 R-SAVE-02] (the name, not the description, is the file stem); `DELETE`
deletes the selected file and re-enumerates. *Load* (flags `0x980`,
background `DLOADGAME2`, from `SINGLE`/`ARMOPT`/`ENDMSN`): an empty
`SAVEGAME\*.SAV` set closes the window and raises `There are no saved games
to choose from`; `DELETE`, `GAMENAME` and `SaveGame` are hidden. The
enumerator reads every `.SAV`'s `Description` and drops files without one,
so the list index is over described saves only. Selecting a row fills
`GAMENAME` (description), `RADAR` (the `Radar Image` block when present),
`GAMETYPE` (`???` when `Players` is 0, `Single` for type 1, else
`Skirmish (%d players)`), `CAMPAIGN` + `CAMPTEXT` (type 1 only),
`MISSION` (`Mission` or `Map`), `TIME` (`%02d:%02d:%02d` of `Game Time`),
`SIDE` and `DIFF` (`Easy`/`Medium`/`Hard`).

`LOAD` validates before applying: `Gametype` 1 needs disc 2, 2 needs disc
1 (each else the matching `MSGBOX`), anything else is `Invalid savegame
file`; then the mount pass, the arrow cursor, the `summary` section applies
`Gametype` (mission record), `Campaign`, `Side`, `Difficulty`, the two side
records (type 1), `Mission` (the map must load — else `Invalid savegame
file`), `Thumbs` into the 25-byte mission-thumb string (or the `U` reset),
and for type 2 `Players`, `CommanderDeath`, `Location`, `Mapping`,
`LineOfSight`, `LineOfSightType` into the skirmish record; the battle-start
bit is raised under host mode 2. A campaign save flagged `BetweenMissions`
instead sets the between-missions bit, drops the handle, clears the
battle-start bit and enters phase 0xe — the briefing of the next mission.

`SAVELIST.GUI` / `LOADLIST.GUI` are the unit-restriction list files
(`SAVEGAME\*.LST`) of the lobby `RESTRICT2` screen (its `Save`/`Load`
buttons); they share the enumerator shape and the `There are no saved
lists to choose from` refusal. Edge recorded; the restriction semantics are
out of scope.

### Dialog primitives [R-FE-01 §9]

**Established fact — `MSGBOX`.** The opener takes (text, width, showOK,
autoWidth). The text is localised, wrapped to `width`, and split at `\n`
into one centred `TEXT` label per line at `y = 20 + n × (fontHeight + 5)`;
the panel width is `max(line widths) + 20` when `autoWidth`, else `width`;
the height is `lines × 25 + titleHeight + 40`; the window is centred at
`((W − w) / 2, (H − h) / 2)`; every label is widened to the panel; `OK`
(authored Esc/Enter default) is placed at `(w − okW − 15, h − okH − 15)` or
hidden when `showOK` is 0 (the box then closes only through a caller's
close). One GUI pass runs immediately so the box paints before the caller
continues. The call sites in this document use widths 200 (disc prompts),
320 (map/save refusals), 480 (skirmish preflight), 500 (checksum and sound
warnings), and the checksum text width `+ 20` for pending lobby errors.

**Established fact — the labels are runtime gadgets, and what the constants
above name.**

*The authored file holds no text control at all.* `MSGBOX.GUI` is two gadgets:
the `HEADER` panel (116,82,372,272; empty `panel=`, so the `BackTile` fallback
of §4 fills it) and the `OK` button (264,208,80,42, text `OK`, quickkey 13). Its
`crdefault`, `escdefault` and `defaultfocus` are all already `OK`, so the
opener's write of the Enter/Escape defaults is a no-op on the stock file. The
`TEXT` labels are **appended to the window at run time**, one per wrapped line,
through the same append-a-gadget helper the rest of the interface uses: kind 5
(label), name `TEXT`, local x = 0, height 15, colour-foreground 15, and the
attribute word 2 — which is the centring bit the label painter of §4 tests — and
the line's text in the gadget's own text field. They are appended before the
panel is resized, and the widening pass afterwards sets every kind-5 gadget's
width to the finished panel width and re-stamps attribute 2.

*`titleHeight` is the second gadget record's height field, read after art
resolution, not the authored byte.* The opener addresses it as a fixed
displacement from the start of the window's gadget array, landing on gadget
1's height — for `MSGBOX.GUI` the `OK` button. This document previously took
that height to be the file's authored 42 (giving `lines × 25 + 82`), reading
the term as the raw record field. It is not: the generic window builder that
resolves every button's art and **replaces** its width/height with the
resolved frame's size (`"Buttons: art resolution..."` above, [R-WGT-01 §3])
runs before the MSGBOX opener's own code, so by the time this height field is
read it already holds that resolved value. `OK`'s name matches no entry in
any GAF MSGBOX has access to, so it falls to the generic BUTTONS0 best fit for
its authored 80×42 rectangle — the stock `commongui.gaf` on the reference
install resolves that to an 80×20 frame — giving `titleHeight = 20` and
`lines × 25 + 60` for the stock file, not 42 and `+82`. A retail capture of
the empty-save-list box (`OTA_Menu_Skirmish`, the "There are no saved games to
choose from" dialog) shows a box and an `OK` button both visibly shorter than
the `+82`/height-42 reading predicts, and matching the resolved `+60`/height-20
one; `okW`/`okH` in the position formula above are the same already-resolved
gadget-1 dimensions, so `OK` sits flush 15px from the resolved box's edges,
not 15px short of a box sized for the unresolved 42. There is no title gadget
in the file; the name is descriptive only. **Correction, capture + [R-WGT-01
§3] order of operations; supersedes the earlier "42"/"+82" reading.**

*`fontHeight` is the FNT's, not the GAF font's.* The line advance comes from the
active FNT's height byte even though the same routine measures the line
**widths** through the GUI's GAF font when one is loaded (§4). The two font
paths are genuinely mixed here.

*The wrapper.* It is a distinct routine from the label painter's wrapper. It
copies the source byte by byte and measures the current line only when the
**next** byte is a space, a newline or a hyphen; when the measured width has
reached the wrap width it rewinds over the word just copied — in the output and
the source together — to that separator, overwrites the separator with `CR` and
inserts `LF` after it, and resumes from the byte after the separator. Splitting
the finished buffer at `\n`, as the opener does, therefore yields a **trailing
`CR` on every broken line**; control bytes have no glyph and measure zero (§4),
so the carriage return neither draws nor advances the pen. The scratch buffer is
sized `len + 2 + (len / (width / w("...")))·3`, which is an allocation bound and
not a layout term.

*Argument census.* Over all 73 call sites, 63 pass literal arguments the static
reduction resolves: `showOK` is 1 at every one of them but a single site, and
`autoWidth` is 1 at every one but a single (different) site, so the panel is
essentially always sized to its content and always carries `OK`. The widths are
500 (25 sites), 200 (18), 320 (12), 480 (5), 250 (1), 150 (1) and 400 (1). The
remaining 10 sites compute their width.

**Established fact — `YESORNO`.** Four openers: the CD-player question
(§3), the surrender/exit question (§7), the lobby reject question
(`%s: %s` from `Reject` and the player name, flags `0x100`), and the
multiplayer `You're out!  Continue Watching?` (flags `0x900`, `Yes` clears
the watching bit and refreshes the HUD, `No` raises the surrender bit).
Each writes `Yes`/`No` into `CHOICE1`/`CHOICE2`, tests the window for
null, and sets the Enter/Escape defaults itself (§12).

`CDCHECK.GUI` is raised only by the post-battle machine (§10) when a
campaign ends and disc 2 is absent; `OK` re-checks and advances, else the
Disc 2 `MSGBOX`. `TIMEOUT.GUI` (a lobby peer silent for `timeout × 30`
ticks) and `REPORT.GUI` (score reporting through `reporter.dll`, with the
`Unable to initialize scores reporting.` `MSGBOX` over background
`ReportError`) are multiplayer-only; edges recorded, semantics out of scope.

### The post-battle machine and `ENDMSN` [R-FE-01 §10]

**Established fact.** Host mode 7 is the results controller: its eight
states, the darkening fade, the glamour fade-in, the `Click to continue.`
prompt, the outcome-art preparer and the campaign-complete branch are
closed in [08 R-CAMP-01 §6], and the `ENDMSN.GUI` population, control set,
next-mission selection and progress write in [08 R-CAMP-01 §8]; nothing
here re-traces them. This section adds the edges into and out of that
machine as the transition graph sees them:

* State 4 opens `CDCHECK.GUI` (flags `0x101`, `OK` bound to Enter) only
  for a campaign whose disc 2 is absent; `OK` re-runs the disc check and,
  on success, sets state 5, else raises the Disc 2 `MSGBOX` (§9).
* The campaign-complete branch of state 5 hands control back to the shell
  controller: windowed → phase 2 (`MAINMENU`); full-screen → phase 5 (Core)
  or phase 4 (Arm), the ending-movie phases of §1, which play `4.zrb` /
  `3.zrb` and then `5.zrb` before phase 2. Both leave host mode 7 for host
  mode 2.
* `ENDMSN` `Start` / `Missions` enters phase 0xd (substates 0): the next
  pass opens `MSNBRIEF` for the chosen mission under host mode 2; that
  briefing's `PrevMenu` (phase 0xd substate 3) reopens `ENDMSN` and returns
  to host mode 7 at state 7 (the statistics rows re-animate). `MainMenu`
  enters phase 2 under host mode 1, the *return to front end* handler of
  §7. `LoadGame` / `SaveGame` open the §8 dialogs over `ENDMSN`.
* `Difficulty` cycles the difficulty word and the skirmish record's
  difficulty together, with the `SKirmish` cue.
* Closing `ENDMSN` frees the palette and fade tables and the frame copy,
  restores the saved gamma factor, and, when the session was launched from
  a DirectPlay lobby, returns to it (out of scope).

The `ENDMSN` gadget set is authored entirely inactive; the opener's control
set of [08 R-CAMP-01 §8] is the only thing that shows any of it. The
"route" predicate there — campaign and (has a next mission or the mission
was not won) — is the same test the opener and the gadget-set helper both
evaluate.

### Registry write census and readers [R-FE-01 §11]

**Established fact — who persists.** The preference saver (every DWORD and
string name of [02 R-KEYS-01 §5], plus `FixedLocations`, which the loader
never reads and no screen writes — inert) runs at: the logo-movie pass
(after clearing `PlayMovie`), options `PREV`/"OK", `NEWGAME` `Start`,
`SKIRMISH` `Start`, the lobby `START` (out of scope), and shutdown. No
screen writes a value directly; every screen edits the global and relies on
one of those saves. Consequently `CANCEL` on the options root discards
unsaved edits made on any page, while edits made on the in-battle pages and
left by `PREV` persist at the next save point.

**Established fact — screen → value → reader**, for the rows [02 R-KEYS-01
§5] lists as Unknown:

| Value | Written by | Read by |
|---|---|---|
| `DisplaymodeWidth` / `DisplaymodeHeight` | `VISUALS` `VIDSLDR`, `RESTORE` (640×480), `UNDO` | the skirmish/campaign load transitions compare them to the current window size and, when different, resize the window, re-select the mode and re-create the offscreen; the lobby copies them into the session record | 
| `DisplaymodeDepth` | nothing | loader only: equality with 256 gates the `Games` read ([02 R-KEYS-01 §3]) |
| `Gamma` | `VISUALS` `GAMMA`, `RESTORE` (12) | applied as the palette factor `0.5 + g/24` at battle init and at every slider move |
| `DitheredFog` | `VISUALS` `RESTORE` clears it (front end); `+Dither` toggles it and writes settings; no gadget sets it | loader copies the stored DWORD's low bit into interface bit `0x40`; the fog presenter uses that bit to select plain or patterned current fog ([03 R-RR16-A §2], [03 R-RR16-A §8]) |
| `SwitchAlt` | nothing | [R-CAM-01 §4] |
| `screenchat` | nothing | the **message column**'s class filter ([R-HUD-03 §14]): `screenchat` 0 draws only the lines whose routing class is 1, 4 or 8, and any other value draws every class; the filter is not in the footer |
| `textlines` | `SPEEDS` `MAXLINES` | the message line ring: a line is stored only when `textlines ≠ 0`, and when storing one would exceed `textlines` visible lines the oldest visible line is dropped (`(head + 1) mod textlines == tail` advances the tail; both indices wrap at 30). `textlines` is the **modulus**, not the on-screen budget: the drawer shows `textlines − 1` lines ([R-HUD-03 §14.3], [R-HUD-03 §14.4]) |
| `textscroll` | `SPEEDS` `TXTSCROL` | line ageing: the oldest visible line expires once `(textscroll + 1) × 30` ticks have passed since it was stored |
| `mousespeed` | nothing | **nothing** — no reader in the whole export beyond the loader; persisted and inert |
| `gamespeed` | `SPEEDS` `GAME` (the mirror word too) | the speed setter of [R-CAM-01 §3] |
| `unitchat` | `SOUND` `SPEECH` gauge × 5 ([03 R-AUD-01 §2]); `SPEEDS` `RESTORE` (10) | voice crowding threshold [03 R-AUD-01 §3] |
| `unitchattext` | `SPEEDS` `UNITCHAT` stage × 5 | the caption presenter admits a unit caption when `10 − unitchattext < priority` — the same crowding form as the voice gate. The presenter is [R-HUD-03 §14]: the admitted caption is appended to the message line ring, not drawn directly |
| `side` | `NEWGAME` side buttons; load-game `summary` | `SINGLE` opener, briefing planet override, briefing font index |
| `Difficulty` | `NEWGAME` / `ENDMSN` / `RESTART` / `SKIRMISH` `Difficulty` | [08 R-CAMP-01 §3] |

**Established fact — gamma load, factor and palette application.** A missing
`Gamma` registry value loads as 12. A loaded DWORD equal to 10 is changed to 12
in memory only; every other DWORD is retained without a slider-range clamp.
The `Gamma n` command computes and stores the binary32 display factor as
`binary32(n × binary32(0.1))`, with `n` the signed parsed integer and
the product retained at working precision. Slider changes and initial
application instead compute `binary32(0.5 − g × binary32(−1.0/24))`, with
the product and sum retained until the single final store. The exact forms
are `binary32(n × 13421773) × 2^-27` for the command and
`binary32(g × 11184811 + 2^27) × 2^-28` for the slider; their integer
intermediates fit signed 64 bits. This avoids prematurely rounding the
working sum even for saved values outside the slider range.

Rebuilding the display palette starts from the preserved 256-entry source
palette. Each source red, green and blue byte is treated as unsigned and
multiplied by that binary32 factor at working precision. A product greater
than 255 is replaced by 255; there is no lower clamp and no power curve. The
remaining value is truncated toward zero and its low byte is retained. The
fourth output byte of every entry is zero.

`WindowPositions\` is not a game key: it belongs to the Cavedog library's
developer overlay windows (`Performance status`, `Memory Status`), under
`Software\Cavedog Entertainment\Cavedog library\WindowPositions\<title>`
with `LeftEdge`, `TopEdge`, `Width`, `Height`, `Zoomed`; no game screen
reads or writes it. `totala.ini` carries only the two `[Preferences]` sound
switches of [02 §3]; no front-end screen touches it.

### Never-opened GUIs, default bindings and misfiled names [R-FE-01 §12]

**Established fact (bounded negative, whole image).** `LOGOSEL.GUI` has no
reference. `BUILDER.GUI` is only *tested for* by the build-completion path
(to request a repaint when it is open) and has no opener. `SELVMODE.GUI`'s
opener branch has no caller (§6). `<side>GEN.GUI` (`ARMGEN`/`CORGEN`) is
the HUD's general build page selected by the selection-to-page routine of
[R-HUD-03 §6], not a front-end screen.

**Established fact — Enter/Escape defaults.** The panel-header parser
stores `crdefault`, `escdefault` and `defaultfocus` as three gadget names.
When a `.GUI` authors an empty `crdefault`, the window-open routine binds
Enter to the first button whose name begins `OK` or `NEXT`
(case-insensitive prefix compare); when `escdefault` is empty, Escape to the
first button beginning `PREV` or `Cancel`. Openers may overwrite both after
the fact (the `YESORNO` and `MSNBRIEF` openers do). This closes the
`crdefault` behaviour left unconfirmed in [fmt gui].

**Established fact — which openers test the returned window.** The
`MSGBOX` opener (returns 0), the four `YESORNO` openers, and the HUD
build-page opener test it; every front-end screen opener in this document
does not (§5 "Frontend asset failure boundaries").

### Multiplayer screen edges (out of scope) [R-FE-02 §1]

**Established fact — the boundary.** Multiplayer is out of Nanolathe's scope
([08 R-OOS-01]). The screens below are reached only through the multiplayer
phases of the shell controller ([R-FE-01 §1]) or from a network battle; their
internals are not traced. Each row records the one edge that reaches the
screen so the boundary is explicit; "provider" is the DirectPlay service
provider the player picked, identified by its GUID (TCP/IP, IPX, modem,
serial).

| Screen | Reached by | Notes |
|---|---|---|
| `SELPROV.GUI` | phase 0xf substate 0 (`MULTI` on `MAINMENU`, [R-FE-01 §2]), unless a pending DirectPlay lobby launch diverts to phase 0x10 substate 0x12 | lists the providers as `SERVICE<n>` rows plus a `DPLAY` list; `Options` (substate 0xd) opens the options root exactly as `SINGLE` does |
| `TCP.GUI`, `SERIAL.GUI`, `MODEM.GUI` | phase 0xf substate 2 when the chosen provider is TCP/IP, serial or modem: phase 0x14 substate 1 with the matching dialog | when the dialog reports its connection bit the session opens and the controller enters phase 0x10 substate 0; an open failure stores `An error occurred trying to use the selected service provider` as the pending error text and returns to phase 0xf substate 0 |
| `NEWMULTI.GUI` | the `STARTNEW` branch of `SELGAME` | the new-game name/password form |
| `SELGAME.GUI` | phase 0x10 substate 0 (every provider except a modem/serial *create*, which goes straight to substate 0x11) | the game list; `JOINGAME` → substate 0x12, `WATCH` → substate 0x13 (sets the watching bit), `STARTNEW` → substate 0x11, `PREVMENU` → substate 3 (session closed, back to phase 0xf) |
| `LOUNGE2.GUI` | phase 0x11 substate 0 (after a create or a successful join, substate 0x15) | the lobby; its per-frame tick is substate 1; `START` raises the battle-start bit (substate 0x11 destroys the lobby record and enters the loading transition); leaving is substate 3 (session closed, network layer reset, lobby record destroyed, back to phase 0xf for TCP/IP and modem providers, phase 0x10 otherwise, or the lobby-exit path when launched from a DirectPlay lobby) |
| `ALLIES.GUI` | the lobby callback, and in battle `TABMENU` → `ALLIES` ([R-FE-01 §7]) | alliance panel over the shared player-row builder (`PLAYER<n>`, `LOGO<n>`, `ALLY<n>`, `LIVEALLY<n>`, `LIVEPLYR<n>`, `TEAMICONS<n>`) |
| `RESTRICT2.GUI` | the lobby callback | unit-restriction editor; `Save`/`Load` open `SAVELIST.GUI` / `LOADLIST.GUI` ([R-FE-01 §8]) |
| `VIEWMAP.GUI` / `viewmap.gui` | the lobby callback and the lobby tick | map preview |
| `TALK2.GUI` | the chat opener when the expansion flag and mission type 3 hold (§5 "Chat") | the recipient rows (`PLAYER<n>` / `LIVEPLYR<n>`) exist only in this form |
| `GAMEOPTIONS.GUI` | `ARMOPT` → `MISSION` outside a campaign ([R-FE-01 §7]) | **shared** with skirmish; only its multiplayer rows (`Cheat Codes`, `Watching`) are out of scope |
| `TIMEOUT.GUI` | the lobby time-out monitor (a peer silent for `timeout × 30` ticks, [R-FE-01 §9]) | `will be rejected in <n> seconds` countdown; `REJECT` kicks the peer |
| `REPORT.GUI` | phase 0x10 substate 0x12 when launched from a DirectPlay lobby, and the post-battle machine for mission type 3 | score reporting through `reporter.dll` (`_RIInitializeEx`, `_RIReport`, …); a modal pump runs the GUI while the box is open |
| `CONTROL.GUI` | `TABMENU` → `CONTROL` ([R-FE-01 §7]) | host player control |

Everything those screens call — DirectPlay session create/open/enumerate/
close, player create/destroy, the packet layer (send rate `1000 / n` ms for
`n` clamped to `2..30`, eleven channels), the alliance/chat/ping packets, the
peer content-sync check, the rejection-reason exits (`The game is closed`,
`The game is full`, `You did not have the correct password`, `You have lost
connection with the host`, `You need a unit you don't have …`, `You need a
newer version of the game`, `No watching is allowed for this game`, `The
creator has left the game`, else `You were rejected from the game`), the
`online.dll` service loaded by the `-c` command-line switch, and the lobby
game record (restriction lists and the player-shared map) — is out of
scope, with this section as its edge. Two pieces of
this code do run in single player and are stated where they belong: the
network half of the surrender teardown is a no-op when the session's network
bit is clear (§3), and the lobby-launch detector is what diverts phase 2 /
phase 0xf when a lobby connection is pending ([R-FE-01 §1]).

### The pump's per-frame residue: catalog reload, cursor visibility, 640×480, and the checksum stub [R-FE-02 §2]

**Established fact — catalog reload step.** Before the controller runs, the
host-mode-2 pump ([R-FE-01 §1]) tests two words: when the unit catalog is
empty (definition count 0) or the catalog reload flag is set, the whole unit
catalog is (re)loaded through the loader of [02 R-MALF-01] and the flag is
cleared. The flag's only other writer is the end of the battle-entry
definition finalisation ([08 R-ENTRY-01]), which raises it unconditionally,
so the first front-end frame after **every** battle rebuilds the whole unit
catalog — with or without the surrender teardown of §3 — and every front-end
frame runs with a loaded catalog. Nothing in the front end loads definitions
by any other path.

**Established fact — software-cursor visibility.** The presentation object
carries a cursor-visible word tested by the cursor blitter (the cursor is
composed only when it is nonzero). The controller writes 0 in phase 0
(startup) so the logo/intro movies play without a cursor, and 1 when phase 2
substate 0 opens `MAINMENU`; the `ENDMSN` `MainMenu` button and the movie
player write it the same way. The post-battle controller also hides it when
arming the darkening fade and shows it at the completion of the statistics
reveal; the campaign CD-check dialog explicitly shows it earlier
[08 R-CAMP-01 §6].

**Established fact — 640×480 enforcement.** The shell loader ([R-FE-01 §3]),
the post-battle controller ([R-FE-01 §10]) and the multiplayer join path
call one routine that sets the logical display size to `640×480` and, only
when the presentation window's current size differs, frees the `OFFSCREEN`
surface, drops the presentation surface, moves the window to `(0,0)` at
`640×480` (frame-change flag set), re-creates the presentation surface in
the current mode (windowed or full-screen, doc 03) and re-allocates
`OFFSCREEN` at the new size, selecting and clearing it. The front end
therefore always runs at 640×480 regardless of `DisplaymodeWidth`/`Height`.
So does the loading screen, whose transition calls this same routine on
entry; the resize the other way is that transition's second half, taken only
after the load thread completes (§5 "The loading screen"). The results
controller of [R-FE-01 §10] calls it too, at the end of its fade-to-black,
so the glamour image and `ENDMSN` are 640×480 as well.

**Established fact — the checksum probe is a stub.** The code-segment
checksum probe that precedes every phase/substate write ([R-FE-01 §1]) is a
function that returns 0 (success) in this executable; the wrapper that
formats `Code segment checksum error found when switching FE states.` and
raises it as a `MSGBOX` of width 500 is therefore unreachable. Nanolathe
needs no equivalent.

### The surrender teardown: what returning to the shell frees [R-FE-02 §3]

**Established fact.** `Yes` on the surrender question ([R-FE-01 §7]) stops
all sounds and runs one teardown routine before the windows are closed and
host mode 1 is selected; the exit-to-Windows variant runs the same routine
after clearing the display and the network half, then quits. The routine
runs, in this order: the audio stop and the sound-engine flush; a
feature/projectile step stub (empty in this build); the *sensor teardown* —
every live unit is de-registered from the sensor tables, then the `HOT
UNITS` and `HOT RADAR UNITS` lists ([R-REV-01 §5]) and the unit pool are
freed; the *effect-system teardown* (the ten effect lists run their
elements' destructors, doc 03 [R-FX-01]); the multiplayer elimination record
(§1); the *per-player teardown* over the ten slots (unit-slice bounds
zeroed, the per-player ten-entry table, the AI planner record with its ten
planner objects, and the score buffer freed); the three minimap surfaces;
the *map teardown* (feature definitions — every non-flagged definition's GAF
frames on both instance lists — the instance arrays, the height/type/LOS
word grids, the visibility grids, the path caches and the occupancy
bitmaps); three further buffers; the builder page-record
table; the *unit-catalog teardown* (per definition the model tree, the
build picture and the per-definition buffers, then the weapon-name list and
the summary/model tables); the *weapon-catalog teardown* (all 256 weapon
records: the sound-name buffer and the projectile-model vector); the
per-side logo table and surface; one more presentation buffer; the three
order-descriptor tables ([R-P0-11 §3]); the unit-category name vector
([R-CAM-01 §2]); and finally the DirectPlay close when the session's network
bit is set (a no-op in single player). The mission record itself is
re-allocated, not freed, when the next family is entered ([R-FE-01 §1]); its
own teardown (the AI mission record, the start-position and census lists,
the OTA parse tree) runs then.

Nanolathe impact: after a surrender every catalog is gone; the next battle
entry reloads units (the pump step of §2), weapons, features and the map
from scratch, so nothing survives across battles except the session globals
and the preferences. Every free is null-checked and zeroes its pointer;
there is no reference counting.

### Gadget lookup, mutation and synthesis helpers [R-FE-02 §5]

**Established fact — name lookup.** Every screen in this document finds its
gadgets by name through one family of helpers, all of which scan gadget
records 1..count in index order and compare the authored 16-byte name field
with `strncmp(name, wanted, 16)` — **case-sensitive, first match wins**.
The callback-side "which gadget fired" predicate instead compares the
fired-index record's complete terminated name [R-WGT-02 §2]. The lookup
variants differ only in what they return and in what a miss does:

| Helper | Returns | On a miss |
|---|---|---|
| index by name | the index | `−1` |
| index by substring | the first gadget whose name *contains* the text | `−1` |
| record by name (non-fatal) | the record | null |
| record by name (fatal) | the record | writes `Error in GUI layout` through the fatal-error path (process exit) |
| `active` byte by name | the byte | `0xFF` |
| stage by index | the button's current stage | `−1` for a non-button |

The fatal form is used by every slider and list opener and by the options
pages, so a `.GUI` that lacks a gadget those openers expect terminates the
process with that text; the non-fatal forms are used where the gadget is
optional (`AnyMsn`, `DELETE`, the `RESTART` button, …).

**Established fact — mutation helpers.** *Set text* (by name, or by index)
copies up to 128 bytes into the record's text field for kinds 1, 3 and 5 and,
when the gadget is the focused text input, moves the caret to the end of the
new text; the by-name form on the *parent* window is what `SELMAP` uses for
`MapName`. *Rename* replaces the 16-byte name (17 with the terminator). *Set
quickkey* writes the button's quickkey byte. *Grey/lock* (by name or index)
is per kind: a button's greyed bit; a listbox's attribute `0x100` (the
"no highlight" bit of [R-WGT-01 §4]); a slider's locked word **and** the
same bit on its two synthesised arrow buttons (found by equal association
id and the arrow attribute bits, [R-WGT-01 §5]); a label's lock bit; a
picture's darken bit. *Set text by name and repaint* additionally requests
the window redraw and repaints just that gadget (the `DebugString` version
label of [R-FE-01 §3]). *Focus a text input* by index sets the drawing colour
from the gadget's `colorf`, selects the window font at the gadget's
`fontnumber` (the n-th kind-7 font record; the common font when there is
none), captures, sets the window focus and re-lays the input with its
`maxchars` ([R-WGT-01 §6]). *HELPTEXT refresh* copies the hovered gadget's
localised help (or the empty string when nothing is hovered) into the gadget
named `HELPTEXT` and requests a redraw ([R-WGT-01 §1]); the skirmish rows
use it for their runtime help lines.

**Established fact — listbox fill.** The list filler takes a gadget name,
the item text, its count and an optional per-row flag array. It copies those
rows, enables row selection, and computes metric `m` — capital-I height plus
two for a GAF font, otherwise the FNT height. An authored item height at or
below `m + 1` becomes `m + 1`. It resets `top`, selection and the associated
knob to zero. It initializes `maxTop` to `count − 1`, then walks backward from
that row while subtracting the row height from the gadget height; it replaces
the candidate only while the remainder is non-negative. Thus a row ending
exactly at the bottom is retained. An active list with overflow activates its
same-association kind-4 control and its synthesized arrows; a fresh fill with
no overflow leaves those controls inactive. A row flag value exactly `1`, as
well as the text prefix `&G`, marks a heading; the flagged form leaves its
text bytes intact. The variable record-list payload remains unknown
([R-WGT-01 §4], §5).

**Established fact — synthesised gadgets.** Screens append gadgets at run
time by two helpers. *Append label* adds a kind-5 record named as given at
`(x, y)` with width `panelWidth − x − 5` when the caller passes `−1`, height
15, `colorf` 15, the attribute word given, active, and the text (127 bytes);
`GAMEOPTIONS` rows, `HELP` lines, the text pager ([R-HUD-03 §10]), `MSGBOX`
lines and the `UNITINFOx` values are all made this way. *Append record*
copies a caller-built record whole and forces its kind to 1 (button);
it refuses when the window already holds 200 gadgets — the only gadget-count
cap in the executable, and the reason the skirmish row synthesis (§8) stays
under it. *Label fit* measures a label's localised text (GAF font: the sum of
the glyph frame widths; FNT: the font width routine) and, while it exceeds
`w − 6`, drops the last character.

### The word-wrap routine [R-FE-02 §6]

**Established fact.** `MSGBOX`, `RESTART`'s mission name and the briefing
text share one wrapper `wrap(text, width, font)`. It allocates
`len + 2 + 3 × (len / (width / spaceWidth))` bytes (`spaceWidth` measured on
the wrapping font, or on the current font when `font` is `−1`), zeroed, and
copies the input byte by byte, stopping at NUL or `0xFF`. After copying a
byte whose *successor* is a space, a newline or `-`, it measures the current
line (from the last break to the copy position) and, when the measured width
is `≥ width`, walks back over the copied output to the nearest earlier space or `-`
(clearing the bytes it passes; there is no line-start guard), writes `CR LF`
there, and restarts the line after it. A
literal newline in the input also restarts the line. Consequences an
implementer must keep: the test is `≥`, not `>`; breaks happen only at a
space or hyphen (a word longer than `width` is never split — the walk-back
runs to the previous space or hyphen, even one before an earlier break); the hyphen or space that breaks the line is
consumed; the emitted separator is `\r\n`, which the label splitter of
[R-FE-01 §9] and the pager treat as one line end; `0xFF` ends the text.

The walk-back rewinds the **input** cursor alongside the output one and
tests the *input* byte, so it is the input's separators it looks for. That
is why the missing line-start guard is a latent hang rather than a
formatting quirk: a word wider than `width` whose line has no separator of
its own sends the walk-back past the earlier break to the previous line's
separator, and the re-copy of the same word then reaches the same measure
and the same walk-back for ever. Stock text never reaches it. A
reimplementation must terminate; stopping the walk-back at the line start
does, and leaves the documented consequence intact — the over-wide word is
emitted whole and the next break lands after it.

When the caller passes a gadget rather than `−1`, that gadget's font is
selected first and every measure in the routine — the allocation's
`spaceWidth` included — is the **FNT** width sum of that font, not the GUI's
GAF font. The briefing's call passes the `TextRegion` gadget and the
gadget's authored width ([R-HUD-03 §10]).

### Briefing blink words [R-FE-02 §7]

**Established fact.** The text pager of [R-HUD-03 §10] does more with a
`&X…&` run than choose a colour: when it lays a page it registers every
bracketed run as a *blink word* in a 15-entry table the briefing/help
window allocates on open (`BRIEFING`, `MSNBRIEF`, `HELP`) and frees on
close. An entry holds the run's text (≤ 127 bytes), its pen position — the
label's `x + 5` plus the measured width of the line so far, and the label's
`y` — two colours and two periods: colour A is the side's text-colour entry
selected by the letter (`G` 1, `Y` 2, `R` 3, anything else 3), colour B is
palette index 94, period A is 1.0 s and period B 0.25 s. The window's
per-frame surface hook draws every live entry with the window font stored
for the table: an entry starts in phase A with a deadline of
`now + 30 × 1.0` (the scaled 30 Hz timer), and each frame whose timer
exceeds the deadline flips the phase and sets the next deadline to
`now + 30 × period` of the phase entered (the deadline arithmetic is single
precision, the timer an integer); phase A draws in colour A, phase B in
colour B, with no width limit. Turning a page clears the table. A run that
spans an input line break is pre-split before paging: the pre-pass closes
the run before the newline and reopens it after (`&` + `CR LF` + `&` +
letter), so each line blinks on its own.

**Established fact — the label under the blink word draws the run too.** The
pager's copy loop consumes only the marker bytes; every byte between them is
appended to the label as well as copied into the blink entry, so the blink
word overdraws the same text in place rather than filling a gap.

### Skirmish row synthesis geometry [R-FE-02 §8]

**Established fact.** `SKIRMISH.GUI` authors no per-player rows; the opener
synthesises them for `NumSkirmishPlayers` rows through the append helpers of
§5. With `n` rows: `step = 200 / n` (signed truncation) and the first row's
`y = (180 − (n − 1) × step) / 2 + 79`, the rows `step` apart. Per row `i`
(0-based), six gadgets in this order, all active:

| Gadget | Kind | `x` | `w × h` | Art / notes |
|---|---|---|---|---|
| `Player<i>` | button | 45 | 112 × 20, or the `skirmname` entry's frame size | own GAF entry `skirmname`, frame 0 |
| `Side<i>` | button | 163 | 45 × 20, or the `SIDEx` frame size | `SIDEx`, stages 2 |
| `Color<i>` | picture | 214 | 20 × 20 | the colour swatch |
| `Allies<i>` | picture | 241 | 40 × 20 | help `Click to select an allegiance symbol …` |
| `Metal<i>` | button | 286 | 45 × 20, or the `skirmmet` frame size | `skirmmet`; attribute bit 16 set; help `Left click to increase metal …` |
| `Energy<i>` | button | 337 | 45 × 20, or the `skirmmet` frame size | `skirmmet`; help `Left click to increase energy …` |

The button records are zero-initialised then set: attribute word `2`
(centred text) for `Player` and `Side`, `2 | 0x10000` (centred, keep the
authored quickkey) for `Metal` and `Energy`; colour fields 0; the frame size
comes from the entry's frame 0 when the window's own GAF has the entry. Six gadgets per
row keep ten rows (60) far under the 200-record cap of §5. The row
controller, colours, alliance icons and resource steps are closed in §5
"Retail closure for the single-player menu slice" and [08 R-SKIR-01].

### The display-mode source list [R-FE-02 §9]

**Established fact.** The mode table the `VIDSLDR` slider indexes
([R-FE-01 §6]) comes from one routine: in the GDI (windowed) presentation
it is the fixed list `640×480`, `800×600`, `1024×768`, then `1280×1024`
only when the desktop is at least `1280×1024`, then `1600×1200` only when
the desktop is at least `1600×1200` (`GetSystemMetrics` screen size, both
axes inclusive); in the DirectDraw presentation it is the driver's
enumeration of 8-bit modes, each appended as `(w, h)`. The options page then
sorts and filters that table as [R-FE-01 §6] states.

### `DRDEATH`, and the `AllMissions` toggle [R-FE-02 §10]

**Established fact.** `SINGLE.GUI` installs a per-frame hook that compares
the last seven bytes of the window's key history (the 15-byte upper-cased
ring the service pass shifts on every unconsumed key, [R-WGT-01 §1]) with
`DRDEATH`. On a match the `AllMissions` preference bit is toggled, `AnyMsn`
is shown or hidden accordingly, the `AllMissions` DWORD is written to the
registry at once (not deferred to the save points of [R-FE-01 §11]), and the
window is redrawn. Because the comparison runs every frame while the seven
bytes still match, typing any further key is what stops the toggle from
re-firing: in practice one keystroke after the `H` leaves the bit in the
state the first match set. This is the only way to set `AllMissions`; no
options page exposes it. (`AnyMsn` opens the play-any layout of `NEWGAME`,
[R-FE-01 §4].)

### The developer contour overlay [R-FE-02 §11]

**Established.** `+Contour spacing offset` is a mask-1 settings command and
does not require developer activation. Each argument is parsed as a float,
multiplied by 256 and truncated into a signed integer: these are
**1/256-height** units, not 16.16 world positions. Zero spacing disables the
overlay. Nonzero spacing causes the main viewport composer to draw contours
immediately after terrain tiles; this is not a minimap lens. Negative spacing
is not rejected and can make the descending-level loop fail to terminate.

Each visible terrain quad is split into four clockwise triangles about its
centroid. Corner heights are multiplied by 256; centroid height is the sum of
the four unscaled heights multiplied by 64. Its screen position is the
componentwise truncated `(sum of four projected coordinates + 2)/4`.
For each triangle, sort vertices by height. Starting at
`L = trunc(top/spacing)*spacing + offset`, subtract spacing while `L > top`,
then draw while `L > middle` and afterwards while `L > bottom`, subtracting
spacing after each line. The offset is not normalized upward: a negative
offset can skip upper contour levels. The middle-height equality belongs to
the lower segment, and the bottom equality is excluded. Edge interpolation
truncates `(p1*(d−t) + p2*t)/d` per screen coordinate.

The line uses the fixed 32-colour lookup indexed by
`((L >> 8) − seaLevel + 256) >> 4`, followed by the ordinary clipped line
primitive. These are raw palette entries, in index order:

```
0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 111, 110, 109, 108, 107, 106,
88, 87, 86, 85, 84, 83, 82, 81, 80, 255, 255, 255, 255, 255, 255, 255
```

The complete projection, visible-cell iteration and painter ordering are
owned by [03 §3.12]. This section establishes the
command's activation and units; it makes no Nanolathe implementation-policy
exclusion.

### The chat line composer [R-FE-02 §12]

**Established fact.** Chat text leaves the `TALK` dialog through one
composer: it formats `<name> text` — the local player's name (three name
fields) in angle brackets, one space, the committed text — into a 200-byte
buffer, builds
the type-5 chat packet from it (dropped unsent outside a network session,
[08 R-OOS-01 §1]), and posts the line to the local message ring with the
routing class its caller passes (4 for an ordinary commit, [R-CAM-01 §6]).
The ring's `textlines`/`textscroll` behaviour is [R-FE-01 §11].

### Supported inference

Front-end state transitions should be represented as explicit named states
with modal substate, not only as a stack of filenames. This is required for
campaign briefing progression, multiplayer lobby readiness, load/save
validation, and endgame continuation.

### Unknown

- Whether the label under a briefing blink word also draws the run (so the
  blink overdraws it) or elides it · §5 [R-FE-02 §7] · static trace of the
  pager's copy loop.
- Process-level outcome of malformed HATTFONT and malformed GAF payloads
  whose decoders return null · §5 "Frontend asset failure boundaries" ·
  static trace.


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
panel starts visible when a session mode byte has bit `0x04` set. What
slides is the Space-held **readout strip** at the bottom edge of the view —
not the side rail: `PANELSIDE` has one final origin, `(0,0)`, stamped by the
first paint and nowhere else, and every rail window and gadget rectangle is
fixed in authored coordinates ("Panel asset binding and draw origins" below;
[R-HUD-05]). The strip owns a signed pixel offset advanced on a
15-millisecond wall-clock throttle (a step whose timestamp is early is
skipped); each accepted step eases by remaining-distance/3 with a minimum
step of one pixel in both directions so it always converges; detents are 0
(parked: the strip sits at the surface's bottom edge, off screen, and is not
drawn) and -31 (fully raised, its rows on screen over the bottom strip).
Crossing into a detent plays cues: leaving -31 upward and leaving 0 downward
play `Panel`; reaching 0 and reaching -31 play `Options`. The strip is
stepped unconditionally in every session kind ([R-HUD-03 §1]); the bottom
strip's own draw is the offset's one consumer.

Space polarity: with Space held the strip slides toward -31 unless a latched
typed gadget whose authored record type equals `3` (the text-editor family)
holds focus, in which case it slides toward 0; with Space released it always
slides toward 0.

Whenever the offset is nonzero, the band — frame index 1 of the common GUI
GAF's `LIGHTBAR` entry — is blitted at `(x, yBottom + offset)`, with `x` the
left edge and `yBottom` the bottom edge of the battle view `(128, 32)–(W−1,
H−33)`, and three translated strings are written on one line at
`yBottom + offset + 10` in GAF font slot 1 (`hattfont11`), light-table row 0:
`Game Time` at `x + 25` as `%s : %02d:%02d:%02d`, `Total Units` at `x + 190`
as `%s : %d  (Max %d)` (two spaces before the parenthesis), and `Game Speed`
at `x + 380` as `%s %s` — the `Normal`-or-`%+d` text of [R-CAM-01 §3] with
` (%+d)` appended while the adapted speed differs from the target. A
space-colon-space follows the first two keys and nothing follows the third
([R-HUD-04 §4]).

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
show the eased display value of [05 R-ECO-01 §6], repainted only when the
strip's snapshot changes ([R-HUD-03 §4]).

**Unit health color thresholds are closed.** The retail health primitive uses
the active logical-to-physical table entries `dcb[10]`, `dcb[12]`, and
`dcb[14]`: above two-thirds health selects entry 10, above one-third selects
entry 14, and the remaining positive-health range selects entry 12. The
outer health rectangle uses entry `dcb[0]`; the inner fill is inset before
the current/max fraction is truncated toward zero. This is separate from
the side `DAMAGEBAR` anchor, whose placement remains data-authored.

#### Footer state and formatting boundary [R-HUD-02R]

The bottom/status readout's sources, priority, redraw and formatting are
[R-HUD-03 §1]–[R-HUD-03 §3]; this subsection keeps the boundary facts they
build on. The footer has a rooted film-information diagnostic branch
([R-HUD-03 §1]). The separate `Unit State Probe` painter has no established draw invocation
in the examined release ([R-CAM-01 §9]); it is neither that branch nor the ordinary
unit-information footer.

**Established (direct-static).** Selection and world hover are separate state.
The pointer update publishes the hover result once per host frame, and click
and cursor targeting consume that same result [R-SEL-02B2]. A hovered unit can
therefore coexist with a selected primary or selected group, and the footer
never reads the selection ([R-HUD-03 §1]). The side loader establishes the
available semantic anchors: `UNITNAME`, `DAMAGEBAR`, `UNITMETALMAKE`,
`UNITMETALUSE`, `UNITENERGYMAKE`, `UNITENERGYUSE`, `MISSIONTEXT`,
`UNITNAME2`, `DAMAGEBAR2`, `NAME`, and `DESCRIPTION`, along with the reload
anchors; which footer state writes each is [R-HUD-03 §5].

**Established (content and text primitives).** A unit display name and
description come from the language-prefixed authored-field accessor: it tries
`<language>name`/`<language>description` and then the plain field [02 §6]
[fmt fbi]. The returned authored capitalization is preserved; no uppercasing
or canonical-identifier fallback is established for the footer. The bitmap
text path measures glyph advances, truncates to a supplied maximum width
before clipping, and then clips to the destination [07 §7]; the footer passes
no maximum width ([R-HUD-03 §1]).

**Established (content meanings).** `EnergyMake` and `EnergyUse` are
authored active-state rates, and metal production is `MetalMake`; retail has
no `MetalUse` key, so metal upkeep is represented by a negative `MetalMake`
[fmt fbi]. The top-strip production and consumption values are a different
presentation record — latched every 30 simulation ticks, integer energy or
one-decimal metal formatting (§6 above) — and the footer's four rate fields
are the archived settlement slots of [R-HUD-03 §2], not that record.

**Frame composition passes.** The master battle frame runs ten ordered layer
passes: terrain tiles → features/wrecks → soft units → hard units → shadows
→ selection brackets/health → projectiles → explosions → UI gadgets → squad
numbers (medium confidence on the projectile/explosion order — a swap would
still match the observed call count). The squad-number pass draws `'0' +
squadId` digits when a squad-overlay bit is set or the squad id is nonzero.
The composer's five-pixel crosshair — two one-pixel Bresenham lines crossing at
a projected point plus the `(128,32)` view origin, in color-map entry 15 — is a
**world** figure and a film-mode diagnostic, not the minimap's viewport
indicator [03 §3.12]. The minimap's viewport indicator is a separate figure
with a separate producer: a rectangle outline one pixel wide, in color-map
entry 14, stroked by the HUD's minimap-presentation routine onto the
destination surface after the radar picture is copied there ([03 R-MM-01 §1]
is the owning statement). The drag-selection rectangle's outer color-map
entry is 6 or 4 while the armed latch is MOBILEBUILD — chosen by the
pointer-flags *site-valid* bit, 6 when set and 4 when clear ([R-CAM-01 §14]
step 1) — and entry 15 otherwise, with inner entry 0.

### The ordinary footer: sources, priority, redraw and clearing [R-HUD-03 §1]

**Established — the writer.** The footer is drawn by the routine the master
composer calls immediately after the top resource strip and before the
minimap, once per host frame. Its leading branch is the developer overlay
that prints `PFSTATE`, `DELTATIME`, `GAMETIME`, `PACKETS` and friends when
film mode and film information are both enabled; developer access itself
is not re-tested. The ordinary footer is the remainder of the same routine.

**Established — diagnostic footer layout and data.** This branch repaints
the side's `PANELBOT` backdrop every call, selects `COMIX`, uses palette
index 83, and returns after its diagnostic lines; it does not update or
consult the ordinary footer's change-detection snapshot. Let
`lower = H − fontHeight − 1` and `upper = lower − 16`. All formats below
end with an authored newline.

| Position | Format | Values |
|---|---|---|
| `(130,lower)` | `PFSTATE %d, PFABLE %d` | A display-state flag and display-availability word; precise platform meanings remain Unknown below. |
| `(264,lower)` | `MOVEORD: %d FIREORD: %d` | Hovered unit's movement and fire stance values, each a two-bit field; omitted when no unit is hovered. |
| `(400,lower)` | `DELTATIME: %d` | Current runnable tick budget, not elapsed wall-clock milliseconds. |
| `(520,lower)` | `GAMETIME: %d` | Current absolute simulation tick. |
| `(130,upper)` | `X: %d  Y: %d` | Camera origin in map pixels. |
| `(264,upper)` | `UNITS %d\%d` | Current live-unit sweep count and on-screen unit count, separated by one literal backslash. |
| `(400,upper)` | `PACKETS: %d %d %d` | Three fixed counters associated with player slots 1, 2 and 3; their producer semantics remain Unknown. |
| `(520,upper)` | `XYH: %d %d %d` | Pointer terrain-cell x and z, followed by that cell's unsigned height byte. |

**Unknown — footer platform fields.** The exact meaning and update cadence of
`PFSTATE`/`PFABLE`, and whether the three `PACKETS` counters represent received,
queued or processed data, require their producer traces. Their labels do not
justify substituting pathfinding state, packet rates, aggregate network totals
or the current viewing player. The painter reads the indicated current values
without smoothing or unit conversion.

**Established — the three sources and their fixed priority.** The footer
reads exactly three inputs, in this order, and the first that applies wins
outright (replacement, never supplement):

1. **Hovered gadget** — the battle window tree's hovered-gadget index
   (`-1` when none). The gadget-tree pointer pass sets it, for button and
   text-entry gadgets, to the index of the gadget whose authored rectangle
   contains the pointer; it is reset to `-1` when the battle window tree is
   built and whenever a window closes. When it is not `-1` the footer shows
   the **build card** ([R-HUD-03 §3]) — or nothing, if the gadget is not a
   product button.
2. **Hovered world unit** — the pointer record's unit word. The battle
   pointer handler rewrites it every host frame **only while the pointer is
   inside the view rectangle with no drag-selection rectangle armed, or over
   the minimap**: in the view it is the HOT UNITS winner of [R-REV-01]; over
   the minimap it is the unit whose minimap dot lies within squared pixel
   distance `< 4` of the pointer, nearest first, else `0`. While the pointer
   is anywhere else (the side rail, the top or bottom strip) the word keeps
   its previous value, so a unit hovered on the way to the panel stays in
   the footer until a product button takes over or the pointer re-enters the
   view. A nonzero word selects the **unit readout** ([R-HUD-03 §2]).
3. **Hovered feature** — the pointer classification's feature word
   (`0xFFFF` when none), the feature occupying the ground cell under the
   pointer's lens position [03 §3.11], recomputed every frame wherever the
   pointer is. A present feature selects the **feature readout**
   ([R-HUD-03 §3]).

With none present the footer draws nothing but its backdrop. **The footer
never reads the selection**: neither the primary selected unit nor the group
is a footer source.

**Established — redraw and clearing.** The routine builds a fifteen-word
snapshot every frame — the current order caption pointer, the hovered unit id
and its health word, its kill count, three per-weapon reload words (below),
its four archived rate values, the secondary unit id and health
([R-HUD-03 §2]), the hovered feature id, the hovered gadget index, the panel
slide offset and the strip-art pointer — and compares it with the copy kept
from the last draw. Nothing is drawn when they are equal. When they differ
the copy is replaced and the whole bottom strip is repainted: first the
side's `PANELBOT` entry is stamped from `x = 129` rightward, each stamp
advancing by its frame width until `x` reaches the screen width, at
`y = height − 32` (plus the frame's own GAF offsets, the [07 §6] blitter
contract), which erases the previous text; then the winning source draws.
So "no target clears" is true by construction — the backdrop stamp is the
clear — and a change of hovered unit, of that unit's health or kills, of the
order caption, or of a build-card hover redraws the strip in the same host
frame. The three reload words are, per weapon slot 1..3, `-1` unless the
slot's weapon definition has a reload time of at least 31 ticks and the
slot's flag bit 1 is set, in which case the slot's own countdown word; they
enter the snapshot (forcing a repaint each time a qualifying weapon's
countdown changes) but **nothing draws them** — the `RELOAD1`..`RELOAD3`
anchors are loaded and never read ([R-HUD-03 §5]).

**Established — placement rules shared by every footer draw.** Every footer
anchor's `y1`/`y2` is offset by `dy = screenHeight − baseheight`, where
`baseheight` is the `[GENERAL]` integer of `sidedata.tdf`, default 480
[02 §6]; `x` is never shifted. Text uses the side's `font` face (the console
face of §6) and, unless a rule below says otherwise, the **raw palette index
83** as its foreground — not a `dcb[]` entry — with the window's current
background. Text is drawn with no maximum width (the FNT truncate-before-clip
step of [07 §7] is bypassed; only the destination clip applies), so no footer
field wraps, ellipsises or truncates. "Centred at anchor A" below means
`x = A.x1 − trunc(textWidth / 2)`, `y = A.y1 + dy`, where `textWidth` is the
glyph-advance sum of the string in the side font (signed division, truncating
toward zero).

**Established — the panel-slide gate.** The session-kind gate belongs to the
Space-held **Kills/Losses score panel** ([R-HUD-04 §1]): the composer draws
it only when the session kind is 2 or 3 (skirmish or multiplayer in doc 08's
vocabulary) and never in a campaign mission (kind 1). The §6 slide strip
(`Game Time` / `Total Units` / `Game Speed`, 15 ms throttle, −31/0 detents)
is stepped unconditionally in every session kind; it is not a side rail but
the strip that slides up from the bottom edge of the view when Space is
held.

### The unit readout: name, damage bar, logo, rates, kills, caption and the secondary field [R-HUD-03 §2]

**Established — admission.** The hovered unit must be alive (its definition
index word nonzero). Then the viewing player's direct-visibility predicate
[03 §3.2] is queried with the viewing player's record: when it returns false
the footer draws the localized caption `Unidentified object`, centred at
`UNITNAME`, and **nothing else** ([03 R-VIS-01 §4] owns the predicate; own
units always pass it).

**Established — the name (`UNITNAME`).** The string is the owning
**player's name** (the 30-byte lobby name in the player record) when the
session kind is 3 (multiplayer, doc 08) **and** the definition's word B has
bit 17 (`showplayername`) or bit 18 (`commander`) set; otherwise it is the
definition record's leading name field — the FBI `name` after the
language-prefixed accessor of [02 §6], stored at load — drawn **verbatim**,
without a second localization lookup. Centred at `UNITNAME`, colour 83.
This routine is a reader of `showplayername`.

**Established — the damage bar (`DAMAGEBAR`).** Drawn when the unit's owner
slot equals the viewing slot **or** the definition lacks `hidedamage`
([04 R-SPEC-01 §6]; allies see no bar either). With `hp = clamp(health16,
0, maxdamage)` and `w = x2 − x1`:

```
fill  := x1 + (w × hp) / maxdamage           // signed idiv, truncating
[x1 .. fill] × [y1+dy .. y2+dy]  filled with dcb[10]
if fill ≠ x2:  [fill+1 .. x2] × [y1+dy .. y2+dy]  filled with dcb[4]
```

Both fills are the inclusive rectangle filler of [R-P0-19-P], so a dead-level
`hp = 0` still paints a one-pixel column of `dcb[10]` at `x1`; there is no
threshold colouring here (that is the world bar's rule, [03 R-FX-01 §6]).

**Established — the owner's logo (`LOGO2`).** After the bar, unconditionally
for an identified unit, the owner's logo is blitted at `LOGO2` (`y + dy`): the
frame of the `logos` GAF indexed by the owner's lobby colour index, at the
frame's full size (the same primitive draws the viewing player's logo at
`LOGO` in the top strip, [R-HUD-03 §4]).

**Established — the four rate fields, own units only.** The rest of the
readout is drawn only when the unit's owner slot equals the viewing slot or
the F11 developer overlay is on ([R-CAM-01 §2]). The four values are the
unit's economy sub-record **archived** slots of [05 R-ECO-01 §5] — the
production and requested totals of the most recent settlement pass, copied
there by the apply-back and re-written every pass (30 ticks); no other
cadence, no smoothing, and not the definition constants. Each is clamped
below at zero before formatting (`max(v, 0.0)`, the compare is
"less-or-equal → use 0"):

| Anchor | Value | Format | Colour |
|---|---|---|---|
| `UNITMETALMAKE` | archived metal production | `+%.1f` | `dcb[10]` |
| `UNITENERGYMAKE` | archived energy production | `+%.0f` | `dcb[10]` |
| `UNITMETALUSE` | archived metal requested | `-%.1f` | `dcb[12]` |
| `UNITENERGYUSE` | archived energy requested | `-%.0f` | `dcb[12]` |

Each is drawn at its anchor's `(x1, y1 + dy)`, left-aligned, in that order.
`%.Nf` is the C runtime's round-half-even conversion of the double the single
was widened to. The sign characters are literal, so an idle unit shows
`+0.0`, `+0`, `-0.0`, `-0`.

**Established — the kills line.** When the unit's status bit 31 is set (the
*armed* bit — the definition resolved at least one weapon, [04 §3]) and its
credited-kill counter is nonzero: `"%d %s"` with the localized word `kill`
when the count is exactly 1 and `kills` otherwise; when the count exceeds 4
the form is `"%d %s - %s"` with the localized `Veteran` appended. Drawn at
`(DAMAGEBAR.x1, DAMAGEBAR.y2 + 2 + dy)` in `dcb[15]`. Only the single word
`Veteran` exists; the experience tiers of [04] are not shown.

**Established — the order caption (`MISSIONTEXT`).** The current order's
caption is the localized caption column of the order-kind table
([R-ORD-01 §1]) for the unit's current order kind, or the table's row-0
caption when the unit has no current order; centred at `MISSIONTEXT`,
colour 83. It is drawn only inside the own-or-overlay block, so an enemy's
order is never shown.

**Established — the secondary field (`UNITNAME2` / `DAMAGEBAR2`).** Two
mutually exclusive uses, decided after the caption:

* **Stockpile.** If the unit's stockpile percentage ([06 §11.1]:
  `progress × 100 / reloadtime` of the first stockpiling weapon, `0` when
  none) is nonzero **and** the unit is own: the localized word `Weapon` is
  centred at `UNITNAME2` in colour 83 and `DAMAGEBAR2` receives the bar of
  the `DAMAGEBAR` rule with `hp := clamp(percent, 0, 100)` and
  `maxdamage := 100`.
* **Order target.** Otherwise (percentage `0`), when the hovered unit is own
  (the target lookup runs only for own units) and its current order carries a
  target unit id whose unit is alive and passes the visibility predicate: the
  target definition's leading name field is centred at `UNITNAME2` (verbatim;
  no player-name substitution), and `DAMAGEBAR2` receives the target's bar
  under the same own-or-not-`hidedamage` test.
* Neither: `UNITNAME2`/`DAMAGEBAR2` stay blank (the backdrop stamp).

This closes the "which pair is primary" question: `UNITNAME`/`DAMAGEBAR` are
always the hovered unit; the `2` pair is its stockpile or its order target.

**Established — the logo entry.** The GAF handle the `LOGO2` draw indexes by
the owner's lobby colour byte is the entry named `32xlogos` of
`textures/logos.gaf`, bound during battle-data initialization; the score
panel of [R-HUD-04 §1] reads the same handle. Rule:
`frame = logos.gaf["32xlogos"].Frames[lobbyColour]` ([R-HUD-04 §4]).

### Feature and build-card readouts: `NAME` and `DESCRIPTION` [R-HUD-03 §3]

**Established — feature hover.** With no hovered unit and a hovered feature,
and the feature definition's `nodisplayinfo` bit clear (or the F11 overlay
on): the text is the localized `description` field of the feature definition
(the 20-byte field, [fmt tdf]), or under the overlay the feature's internal
name. When the definition's `indestructible` bit is clear the line is
`"%s %s%s"` with the second argument `" M:%d"` when the authored `metal` is
nonzero (else empty) and the third `" E:%d"` when `energy` is nonzero (else
empty), both values truncated toward zero from the stored singles; an
indestructible feature shows the name alone. Drawn at `(NAME.x1, NAME.y1 +
dy)`, colour 83, left-aligned, no width limit.

**Established — build card.** With a hovered gadget: the gadget's 16-byte
authored name is looked up (binary search, case-insensitive) in the
definition summary table; a miss draws nothing. A hit whose name is not
`CORBUILD` (case-insensitive) draws `"%s  M:%d E:%d"` — two spaces; the
definition's display name, then `buildcostmetal` and `buildcostenergy`
truncated toward zero — at `(NAME.x1, NAME.y1 + dy)`, and the definition's
`description` field verbatim at `(DESCRIPTION.x1, DESCRIPTION.y1 + dy)`,
both colour 83, both without a maximum width. No wrapping, ellipsis or second
line exists: `NAME` and `DESCRIPTION` are single-line, left-aligned, and clip
only at the destination surface. The `CORBUILD` exclusion is a literal name
test with no other reader (Unknown why; decider: asset census for a gadget
of that name).

### The top strip: redraw condition, bar arithmetic, share marker and alignment [R-HUD-03 §4]

**Established — displayed stock.** The current-over-capacity bars and the
current numbers are not a live presentation of committed stock: the displayed
stock is the eased display value of [05 R-ECO-01 §6] (an eighth of the
integer gap per host frame, minimum step one, clamped to capacity), and the
strip is repainted only when its snapshot changes.

**Established — initialization and lifetime.** Battle entry clears both
displayed stock values to zero before the first frame. Loading a saved battle
rebuilds the world through the same entry reset, so restored live stocks also
ease from zero. A viewing-player change retains both displayed values and
continues easing toward the newly viewed player’s stocks; it does not perform
this battle-entry reset.

**Established — rate-latch lifetime.** Initial process storage clears the
four latched production/request values to zero. The battle-entry top-strip
reset clears displayed stocks and capacities but leaves those four rates
unchanged; loading another battle likewise retains them until the restored
viewing player's display deadline becomes due. Each active player's deadline
is seeded from the battle's current tick and save loading restores its saved
value. A viewing-player change uses that player's own deadline without
resetting the shared rate latch [05 R-ECO-01 §1, §6].

**Established — redraw condition.** The composer keeps a 33-byte snapshot —
the viewing slot byte, then eight singles: displayed energy, latched energy
produced, latched energy requested, displayed metal, latched metal produced,
latched metal requested, energy capacity, metal capacity — and repaints the
strip only when the freshly computed snapshot differs from the stored one.
The displayed-stock easing and the 30-tick rate latch of [05 R-ECO-01 §6]
run before the compare, so the strip repaints whenever the eased value moves
(every frame while stock is changing) and otherwise stays.

**Established — the repaint.** The side's `PANELTOP` entry is stamped once
at `(129, 0)`; the strip is then extended rightward with the side's
`PANELBOT` entry, each stamp advancing by its frame width, until `x` reaches
the screen width. The viewing player's logo (the `logos` GAF frame at the
player's lobby colour index) is drawn at `LOGO`. Then, energy first and metal
second, with `S` the displayed stock, `C` the capacity and the bar's
`w = x2 − x1`:

```
if C > 0:
    fill := ftol( x1 + w × S / C )                     // single-precision product/quotient
    [x1 .. fill] × [y1 .. y2]  filled with the side's energycolor / metalcolor (raw index)
    if 0 < shareThreshold < liveStock:
        m := ftol( x1 + w × shareThreshold / C )
        [m .. m+2] × [y1 .. y2]  filled with dcb[12]
"%d" ftol(S)                 at (NUM.x1, NUM.y1)           colour dcb[15]
"0"                          at (ZERO.x1, ZERO.y1)         (ENERGY0 / METAL0)
"%d" ftol(C)                 at (MAX.x1 − textWidth, MAX.y1)   (ENERGYMAX / METALMAX; right-aligned to x1)
produced / consumed          at PRODUCED / CONSUMED (x1, y1)  dcb[10] / dcb[12], formats per §6
```

`shareThreshold` is the player's `SetShareEnergy`/`SetShareMetal` value
([05 R-SHARE-01]); the two-pixel marker is drawn only while the threshold is
strictly positive and strictly below the **live** stock (not the displayed
one). The capacity number is the only right-aligned text on the strip:
retail's `ENERGYMAX`/`METALMAX` anchors are authored at the bar's right end
and the number ends there.

### Side-anchor consumer census [R-HUD-03 §5]

**Established.** The side record's HUD anchors have exactly two readers, the
top-strip painter ([R-HUD-03 §4]) and the footer ([R-HUD-03 §§1–3]); no
other routine addresses the anchor block. Per anchor:

| Anchor | Consumer | Use |
|---|---|---|
| `LOGO` | top strip | viewing player's logo frame |
| `ENERGYBAR` `METALBAR` | top strip | fill + share marker |
| `ENERGYNUM` `METALNUM` | top strip | displayed stock, `%d` |
| `ENERGY0` `METAL0` | top strip | literal `0` |
| `ENERGYMAX` `METALMAX` | top strip | capacity, right-aligned to `x1` |
| `ENERGYPRODUCED` … `METALCONSUMED` | top strip | latched rates (§6) |
| `LOGO2` | footer | hovered unit's owner logo |
| `UNITNAME` `DAMAGEBAR` | footer | hovered unit (or `Unidentified object`) |
| `UNITMETALMAKE` `UNITMETALUSE` `UNITENERGYMAKE` `UNITENERGYUSE` | footer | archived rates, own units |
| `MISSIONTEXT` | footer | current order caption |
| `UNITNAME2` `DAMAGEBAR2` | footer | stockpile or order target |
| `NAME` `DESCRIPTION` | footer | feature text / build card |
| `TOTALUNITS` `TOTALTIME` | **none** | loaded, never read — the running display uses fixed offsets on the slide strip (§6) |
| `RELOAD1`..`RELOAD3` | **none** | loaded, never read (the reload words only feed the repaint snapshot) |

Only `x1`/`y1` of the text anchors are read; `x2`/`y2` matter only for the
bar anchors. There is no slide/modal anchor combination and no per-side
fallback: a missing anchor is the loader's diagnostic (§6), never a borrowed
rectangle.

### Build pages: name composition, the `DL` template, `NEXT`/`PREV`, and command-button state [R-HUD-03 §6]

**Established — page window names and the page cycle.** The page state of a
builder is its status word's paged bit (22) and page field (bits 23–25, §9).
Page 0 — the paged bit clear — is the **orders** state: the command-window
switch opens `"%sGEN.GUI"` (the side's `nameprefix`) unless the definition's
word A bit 31 is set. That bit is written at definition load by probing
`guis/<internal name>0.GUI` for existence (name format `"%s0"`); no stock
unit ships such a file, so in stock content it is never set. For a page
`N ≥ 1` (or page 0 with bit 31) the switch composes `"%s%d.GUI"` from the
definition's internal name and the page number — `ARMCOM1.GUI` is page 1 —
and, if that window is not already open for the same unit, asks the page
opener to open it. The opener requires the builder to be complete (remaining
construction fraction exactly `0.0`), resolves `guis/<name>` through the VFS,
and when the file is **absent** opens the window `"%sDL"` with the side's
`nameprefix` instead (`ARMDL` / `CORDL`), the download-page template whose
`IGPATCH` product slots the generated-page assembly of §9 then patches with
every build-menu entry whose builder matches and whose authored `PAGE` byte
minus one equals the page number. The definition's page-count byte is the
maximum authored page plus one, so valid pages are `0 .. count−1`.

**Where the page-count byte comes from (Established).** It is the
probe of `guis/<internal name>N.GUI` the catalog compiler runs per record,
[02 R-CAT-01 §5] step 5 — not the length of the builder's `CANBUILD` list
divided by the six product gadgets a full stock page carries. The two agree
for 39 of the reference install's 45 builders and disagree for six:
`ARMCA`/`ARMCK`/`ARMCV` author nineteen `CANBUILD` products and
`CORCA`/`CORCK`/`CORCV` twenty, while all six author only three page windows,
so the division claims a fourth page that no window backs. Retail cannot
select it — its count byte is 4, not 5 — and those builders' last one or two
authored products are simply unreachable, which is the data's own state, not a
defect to repair. This is what §9's "generated `<unit>N.GUI` pages are
authoritative for page existence and placement" means in arithmetic. A count
of 0 (no numbered window and no `<n>0.GUI`) is a valid state and not malformed
state: the switch opens the side's `%sGEN.GUI` and the stage/grey table below
greys `BUILD` and `ORDERS` on its own "page count 0" arm. Eight stock builders
are in it — `ARMASP`/`CORASP`, `ARMCARRY`/`CORCARRY`, `ARMDECOM`/`CORDECOM`,
`ARMFARK` and `CORNECRO`.

**The state a builder is first selected in (Established — manual retail
observation).** Selecting a builder that has not yet had a page
selected shows its **first build page**, not the orders state; the order
palette is reached by clicking `ORDERS`. Because `ORDERS` selects page 0,
which clears the page-shown bit and leaves the page field alone, the choice is
remembered per unit from then on and the default fires only once. A builder
whose page-count byte is below 2 has no build page to open and stays on the
orders state.

**Established — the first-page writer.** The writer that puts a builder on
its first build page is **unit creation**: the unit initializer seeds the
status word with page field `1` and the paged bit set (bits 22–23 both set)
for every definition whose page-count byte is `2` or more, and leaves the
field and the bit clear otherwise. The `BUILD` and `ORDERS` clicks only set
or clear the paged bit through [R-P0-11 §1]'s deferred bits; no click writes
the field, and the only field writers are the `.`/`,` keys, the
`NEXT`/`PREV` gadgets and the digit keys in the table below. So a `BUILD`
click always re-shows the remembered page, which is `1` until the unit
pages, and "the paged bit set over a zero field" is unreachable for a
builder that has pages ([R-HUD-04 §4]).

The page-cycle keys and buttons ([R-CAM-01 §2]) move as follows, every
change setting battle-interface dirty bit `0x10` and playing `nextbuildmenu`:

| Input | From page 0 | From page `p ≥ 1` |
|---|---|---|
| `.` key (next, keyboard) | page 1 | `p+1`, or page 0 when `p == count−1` |
| `,` key (previous, keyboard) | page `count−1` | `p−1`, or page 0 when `p == 1` |
| `NEXT` button | page 1 | `p+1`, or page 1 when `p == count−1` (never page 0) |
| `PREV` button | page `count−1` | `p−1`, or page `count−1` when `p == 1` |
| digit `d` ([R-CAM-01 §2]) | page `d−1` when `d−1 < count`, else nothing | same |

The gadgets `"%sPREV"` and `"%sNEXT"` (prefix-named, `ARMPREV`/`ARMNEXT`)
are hidden (`active := 0`) after a page opens when the page-count byte is
below 2. `ONOFF` on a page shows the builder's on/off bit as its stage when
the builder is a building (status bit 29). On a page above 0, every gadget
with attribute bit `0x04` (a product slot) is greyed when its name does not
resolve to a definition. Queue counts on product buttons are [R-P0-11 §2].

**Established — command-button stage and grey state.** After a page opens,
the command buttons are set from the selection-aggregate words the refresh
computed. The fold that produces those words — its sentinels, its
disagreement values, and the identity of every capability bit named "—" below
— is [R-HUD-03 §13]:

| Gadget | Stage | Greyed when |
|---|---|---|
| `BUILD` | builder's page-shown bit (status bit 22) | no builder, or page count 0 |
| `ORDERS` | inverse of that bit | same |
| `CLOAK` | aggregate cloak pair (bits 3–4 of the second aggregate word) `>> 3` | pair == 3 (not applicable) [R-HUD-03 §13] |
| `ONOFF` | aggregate on/off pair (bits 5–6) `>> 5` | pair == 3 (not applicable) [R-HUD-03 §13] |
| `MOVEORD` | aggregate move-stance field (bits 0–2) | field == 4 (not applicable) [R-STANCE-01 §1] |
| `FIREORD` | aggregate fire-stance field (bits 12–14 of the first word) `>> 12` | field == 4 |
| `MOVE` `STOP` `ATTACK` `DEFEND` `PATROL` `RECLAIM` `CAPTURE` `REPAIR` | — | **no** selected unit carries that command's capability key [R-HUD-03 §13] |
| `LOAD` / `UNLOAD` / `BLAST` | — | transport bit (`canload`) clear: `LOAD` hidden, `UNLOAD` greyed, `BLAST` greyed unless the blast bit (`candgun`); transport bit set: `BLAST` hidden |

For `CLOAK` and `ONOFF`, `3` is the not-applicable sentinel and `2` is the
mixed value; the greying condition is `3`.

A stage write goes to the gadget's stage word; greying sets bit 0 of the
gadget's grey word ([R-WGT-01 §13]) and marks the tree dirty. The button
painter then chooses the GAF frame by the rule of [R-WGT-01 §3]: the authored
`status` is the **down-state word**, the frame base comes from art
resolution, the pressed frame is drawn while the mouse is held, and a greyed
button draws `base + min(downState + 2, frames − 1)` (or `frames − 1` when
the gadget's attribute bit `0x100` is set) and then passes its rectangle
through the rectangle shader of [03 R-COMP-02 §5] at level `−20` —
PALETTE.SHD darken row 12 (`−20 + 32`) — after the frame blit, skipped when
the button carries attribute `0x80`. The painter never reads the label for a
staged button.

### The `damagebars` option is the "label every unit" bit [R-HUD-03 §7]

**Established.** The registry value `damagebars` ([03 R-FX-01 §6]) and the
"label every unit" bit of [R-CAM-01 §2] are the same bit — bit 0 of the
interface-flags word — toggled by the `!` `#` `*` `` ` `` `~` key tokens and
written back immediately. Doc 07 names it `damagebars` from here on; the
composer's consumer (own units get a world health bar; grouped units their
digit) is [03 R-FX-01 §6].

### The unit information screen `UNITINFOx.GUI` [R-HUD-03 §8]

**Established — trigger and subject.** F1 ([R-CAM-01 §2]) opens the screen
when the options window is not open. The subject is the hovered gadget's
product when the hovered-gadget index is not `-1` (resolved by the same name
lookup as the build card, [R-HUD-03 §3]); otherwise the hovered world unit,
if alive and passing the visibility predicate; otherwise nothing opens.

**Established — content.** The screen's `HOTR` gadget receives the picture
`unitpics/<internal name>.PCX` and a click callback; the `NAME` gadget's text
is the definition's display name (a 128-byte limit argument). Eight label gadgets are appended
at fixed positions (x, y): `Cost` (130, 32), `Energy` (140, 47), `Metal`
(140, 62), `Build Time` (140, 77), `Statistics` (130, 92), `Max Velocity`
(140, 107), `Acceleration` (140, 122), `Turn Rate` (140, 137) — every label
localized. The value column at `x = 240` is the property list, a run of
NUL-separated strings at the same rows (a `"\n"` string occupies the two
header rows): `%d` of `buildcostenergy` (truncated), `%d` of
`buildcostmetal`, `%d` of `buildtime`, then for a **building** (`bmcode`
0) three localized `N/A`, and for a mobile unit:

```
tps      := 30                                  // the runtime's ticks-per-second word
velocity := f32(maxvelocity16.16 × 2^-16) × tps × 0.4          "%.1f m/s"
accel    := f32(acceleration16.16 × 2^-16) × tps × 0.4         "%.2f m/s/s"
turn     := turnrate16 × tps × 0.0054931640625                 "%.0f deg/s"
```

`0.4` and `0.0054931640625` (= `360 / 65536`) are literal double constants;
the first two products narrow to single after the `2^-16` scale and are then
widened; the unit words `m/s`, `m/s/s`, `deg/s` are localized. The `0.4`
factor is retail's world-unit-to-metre convention for this screen only; no
other reader uses it.

**Established — portrait palette.** The opener requests an image without a
palette result. The PCX loader copies decoded indices into a surface and
releases the decoded trailer palette; the portrait painter copies those
indices into the window without palette installation or remapping. The active
display palette therefore supplies the portrait colors `[fmt pcx]`.

### `SHARE.GUI`'s `METAL#` / `ENERGY#` [R-HUD-03 §9]

**Established.** `METAL` and `ENERGY` are gadget-kind-4 sliders
(`attribs=1`, horizontal), not text fields. On open each slider's range word
is set to `ftol(live stock)` of that resource (metal from the metal stock,
energy from the energy stock), its knob to position 0, and its change
callback installed; the slider helper places the knob at
`ceil(min(value, range) × (travel − 1) / range)` and reads it back as
`ftol(knob / (travel − 1) × range)` — with `travel = width − height` of the
authored gadget (the knob is a square of the gadget height) and a travel
below 2 reading back as 0. `METAL#` and `ENERGY#` are
label gadgets whose text is `"%d"` of that read-back value, written on open
(so both start at `0`) and again by each slider's change callback. The
confirm handler reads the same two read-back values ([05 R-SHARE-01 §5]:
the amount semantics — truncate, clamp to live stock, zero is a no-op — are
that section's; the 64-bit result it reads is the slider read-back's
`ftol`).

### `MOREBAR` / `MORE...` text-region paging [R-HUD-03 §10]

**Established.** The shared paging routine serves every screen with a
`TextRegion` gadget and a `MOREBAR` button (briefings, the end-of-mission
text and the front-end help pages). Lines per page is
`regionHeight / (fontHeight + 2)` (signed, truncating; `fontHeight` from the
current font). The routine keeps a page counter and the text pointer; a
`MOREBAR` click advances the counter and re-lays the region; opening a screen
resets it so the first call lands on page 0. It scans the text for the
`(page+1) × linesPerPage`-th newline: found → the `MOREBAR` caption is the
localized `MORE...`; not found on page > 0 → the caption is the localized
`BACK TO START` and the next click wraps to page 0; not found on page 0 →
the caption is empty. The page's lines are then emitted as one label gadget
per line at `(regionX + 5, regionY + (fontHeight+2)/2 + i × (fontHeight+2))`,
using the side's four-entry text-colour table: plain text is entry 0 and a
run bracketed as `&G…&`, `&Y…&` or `&R…&` is drawn in entry 1, 2 or 3 (any
other letter after `&` also reads as entry 3); the caption colour is entry 1. A line ends at `\n`; `0xFF` or NUL ends the
text. There is no thumb: `MOREBAR` is a plain button.

The marker bytes never reach a label: on an opening `&` the walk steps past
both the `&` and its letter, on the closing `&` past the `&` alone, and the
copy that follows resumes with the next byte. The run's own bytes **are**
copied into the label, so the blink word of [R-FE-02 §7] overdraws the same
text the label already carries. The open/closed marker state is initialised
once per page, not per line — the pre-split of that section is what keeps a
run from leaking across a line end.

**Established — the side text-colour table.** It is four bytes per side,
indexed `side × 4 + entry`, and the bytes are **physical palette indices**,
not GUI semantic colours: the emitted labels are kind 5, and a kind-5
painter installs its colour word raw ([03 R-FONT-01 §6]). Side 0 (Arm) is
`53, 51, 64, 208`; side 1 (Core) is `117, 86, 82, 212`; the rows past Core
repeat the Core row.

**Established — `fontHeight` and the wrap width are the region gadget's own
font.** The `fontHeight` the divide and the line step use is the **FNT**
height byte of the font the `TextRegion` gadget's `fontnumber` selects
([R-WGT-01 §12]), and the wrapper the text is passed through before paging
([R-FE-02 §6]) is called with that same gadget, so it measures through that
FNT and wraps to the gadget's authored **width**. On `MSNBRIEF` the font
index is the local side plus one, which is `armfont` for Arm and `corefont`
for Core ([08 R-CAMP-01 §2]).

### The score-bar gadget (kind 13) and its `value / 15` step [R-HUD-03 §11]

**Established.** The end-of-mission score rows ([08 R-CAMP-01 §7]) create
kind-13 gadgets carrying: `current := 0`, `target := value`,
`max := column maximum`, `step := max(1.0, value × 0.06666667)` (single),
`interval := 1`, `animating := 1`, `showNumber := 1`. The gadget-tree
service pass steps every kind-13 gadget whose `animating` flag is set and
whose `current < target`: when the scaled timer ([R-CAM-01 §10]; 30 units
per second) has passed the gadget's next-due stamp, `current += ftol(step)`;
a step that strictly overshoots `target` is clamped to it and clears
`animating` (landing exactly on the target leaves the flag set), and the
next-due stamp becomes `timer + interval`. The painter draws the bevelled frame, insets
by 2, fills the inner rectangle with the gadget's background colour, then
`[x .. x + ftol(current / max scaled to the inner width)]` with the gadget's
foreground colour, and when `showNumber` is set centres `current` as decimal text in the
rectangle. So a bar fills in about fifteen 1/30-second steps whatever its
value (one step per tick for values below 15); the per-gadget `value / 15`
float is the animation step, not a fill fraction.

**Established — geometry.** The runtime kind-13 record keeps the
authored dimensions `width=67,height=18`, but its inclusive painted footprint
is `(x,y)..(x+67,y+18)` (68×19). The standard raised two-pixel bevel is the
`fill + raised` primitive of [R-FE-02 §4]: semantic fields 0 on the top/left
runs, 17 on the bottom/right runs, and the record's inner span is
`(x+2,y+2)..(x+65,y+16)` (64×15). ENDMSN supplies dcb[8] as the inner
background and dcb[4] as the foreground; the latter is painted through the
inclusive endpoint `x+2 + trunc(63*current/max)`. The displayed decimal is
centred at `x+33-textWidth/2`, `y+9-fontMetric/2` (the stock text origin is
therefore y+2 for its font metric). These are semantic palette fields, never
literal RGB values.

The service examines only active, visible bars and enters its body only while
`current < target`. A bar is then due only when `nextDue < presentationUnit`;
it advances once and then stores
`nextDue = presentationUnit + 1`, even if the sampled clock jumped. The
result rows'
player surface is 91×21 at x=16 and uses the source slot's frame from
`textures/logos.gaf:32xlogos`, stretched by the established surface painter;
the name is centred in the 90×15 text area at x=16 with foreground field 15.
The row ordinal is not a logo-frame selector. The reveal deadline comparison
is also strict (`deadline < presentationUnit`), with the inherited deadline
expired so Kills can reveal on the first pass; each group then schedules
`deadline = presentationUnit + 10`. In the single-player result surface, a
keyboard edge activates all seven groups and plays `ActivateAllStatBars`, then
the same pass performs the ordinary one-group reveal and cue. Mouse input does
not skip the reveal.

### Selection-count and group displays [R-HUD-03 §12]

**Established — there is no selection-count readout.** Nothing in the battle
composer, the footer or the gadget painters prints the number of selected
units. The only count on screen is `Total Units: %d (Max %d)` on the slide
strip (§6), which is the player's live unit count against its limit, not the
selection. Group membership is shown only as the `'0' + group` digit under
own units ([03 R-FX-01 §6]) and, on the footer, not at all.

### Unit captions: the presenter, the message-line ring, and the column that draws it [R-HUD-03 §14]

The footer draws no message line at any setting; the `screenchat` filter
belongs to a separate column that the master composer paints near the end of
the frame, long after the footer ([R-HUD-03 §14.4]).

**Established — a raised caption is not the footer's order caption.** Two
different mechanisms, with no connection between them:

* The footer's `MISSIONTEXT` field ([R-HUD-03 §2]) is *derived, per frame*:
  the localized caption column of the order-kind table for the **hovered**
  unit's current order kind. It has no history, no lifetime and no producer —
  it is recomputed from current state whenever the footer repaints, and it is
  gone the moment the pointer leaves the unit.
* The captions of [05]'s census are *transient events*: raised once, at the
  instant an order handler reaches the state that names them, and routed
  through the unit voice/caption queue of [03 §8.3] into the shared
  message-line ring, where they persist for a wall of ticks set by
  `textscroll` regardless of hover, selection or pointer position.

The footer never reads the ring and the ring's drawer never reads the
order-kind table. A reimplementation that only draws `MISSIONTEXT` shows the
player nothing about a blocked build.

#### The raiser and its gates [R-HUD-03 §14.1]

**Established.** The order handlers call one *raise unit caption* helper with
three arguments: the unit, the sound **event slot** of [03 §8.3]'s static slot
table, and an optional caption string. It does nothing at all unless all three
hold:

1. the unit's owner slot equals the viewing player's slot (so an enemy's
   blocked build is never captioned);
2. the unit's state word carries the live bit (bit 28, [04 §8]);
3. the unit's death-pending bit (bit 14) is clear.

A null caption argument is replaced by the slot's default caption from the slot
table; the caption — given or defaulted — then goes through the localization
table before it is queued. Two further variants of the helper exist that
additionally require the unit to be, or not to be, in the current selection;
only the *not selected* variant has a caller, and the *selected* variant is
unreachable code. ([03 R-AUD-01 §3] names these same two gates "the unit's
chat-enable status bit" and "the silenced bit"; the predicate is identical.)

**Established — the `Slot` column of [05 "the build-order caption census"]
is an event slot, not a priority.** The number is the **sound event slot** of
[03 §8.3]'s static slot table, i.e. which `[SOUNDS]` event of the unit's
sound category the caption rides on. The priority the gate
`10 − unitchattext < priority` compares is a separate column of that table,
looked up by the slot, and the two numbers move in opposite directions here:
the *lowest*-numbered slot the census uses carries the *highest* priority.
For the three slots the census uses:

| Slot | Key | Priority | Cooldown |
|---:|---|---:|---:|
| 7 | `cant` | 8 | 1 s |
| 8 | `unitcomplete` | 3 | 3 s |
| 9 | `build` | 4 | 2 s |

**Established — what `UNITCHAT` therefore does to the census.** The caption
gate is `10 − unitchattext < priority` ([03 §8.3] step 4, signed byte compare),
and `unitchattext` is `0` / `5` / `10` for `Off` / `Medium` / `Full`
([R-CAM-01 §7]), default `5`:

* `Off` — no caption of any slot appears (the highest stock priority is 10 and
  the compare is strict).
* `Medium`, the shipped default — only priority above 5 passes, so **only the
  slot-7 captions appear**: `Waiting for target area to clear`,
  `Target area was blocked`, `Unable to create any more units`,
  `Construction stopped`, `Construction terminated`,
  `Construction terminated by hostile action`, and
  `I can't reach the construction site`.
* `Full` — everything passes, adding `Starting construction` (slot 9) and
  `Building complete` (slot 8).

**This is a contract, not a nicety, and it is the practical answer a
reimplementation needs.** At the shipped default (`UNITCHAT = Medium`,
`unitchattext = 5`) `Waiting for target area to clear` and
`Target area was blocked` **do** appear on screen, and `Starting construction`
and `Building complete` **do not** — they require `Full`. A build that stalls
for want of energy raises no caption at all at any setting (no handler raises
one for it), so at the default the presence of the slot-7 line is exactly what
distinguishes a blocked build from a starved one. An implementation that gates
all seven or nine captions alike, or that shows the slot-8/9 pair at Medium,
is wrong in the one place a player can see.

**Established — the per-slot cooldown is not a text throttle.** [03 §8.3]
establishes that the slot's next-allowed frame gates *insertion*, and that it
is re-armed only on an **audible** resolve. So with voice suppressed — sound
off, `unitchat` low, or a unit whose sound category has no variant for the slot
— the cooldown is never armed and every raise queues. Slot 7's cooldown is 1
second, and `MobileBuild`'s blocked-site retry is 30 ticks
([05 "the build-order caption census"]), so the blocked-build line repeats
roughly once per retry either way.

#### From the queue to the ring: the presenter [R-HUD-03 §14.2]

**Established.** [03 §8.3]'s resolve step 4 does not draw. It composes the line
as `"<name>: <caption>"` (a two-`%s` format with a colon and a space), where
`<name>` is the acting unit's **definition display-name field** — the same
string `UNITNAME` draws for an own unit ([R-HUD-03 §2]), taken verbatim with no
second localization pass — and `<caption>` is the entry's override text or the
slot row's caption for the drawn variant. It then **appends that line to the
message-line ring** (§14.3) with:

* routing class **1**;
* the source-unit word set to the acting unit's id;
* the speaker slot set to the no-speaker sentinel, so no arrival cue plays and
  no logo is stamped.

Nothing is appended when the composed caption is empty or when the unit's live
bit has cleared since the entry was queued.

#### The message-line ring [R-HUD-03 §14.3]

**Established.** There is exactly one ring, shared by unit captions, chat and
the announcement lines. It is 30 fixed slots of 72 bytes: a 64-byte text field
copied with a bounded copy and a terminator forced at index 63 (so **63
characters survive**, and a longer composed line is silently truncated), the
tick it was stored, a source-unit word, a speaker-slot byte and a class byte
whose low four bits are the routing class. A 16-bit producer index and a 16-bit
display index both wrap at 30.

Append (`text`, `class`, `sourceUnit`, `speakerSlot`):

1. an empty text is dropped; `textlines == 0` drops the line outright — no
   store, no index movement, no cue;
2. when `(producer + 1) mod textlines == display`, the display index advances
   first, wrapping at 30 — this is the drop of the oldest visible line;
3. the five fields are written at `producer`; the class byte is a read-modify-
   write that touches only its low four bits;
4. the producer index advances, wrapping at 30;
5. the `MessageArrived` cue plays **only** when the speaker slot is not the
   sentinel;
6. if a window named `TIMEOUT.GUI` is open it is repainted.

**Established — lifetime.** Every battle's common world rebuild, including a
save load, resets the producer and display indices to zero
([08 R-ENTRY-01 §3]). F12 performs the same cursor reset ([R-CAM-01 §2]).
Neither operation erases the stored records or changes the message settings.
The empty displayed span makes old captions and their source-unit ids
unreachable to both drawing and F3. A later append replaces the five fields
above but preserves the upper class-byte flags, including F3's visited and
destination marks ([R-CAM-01 §14]). The message column belongs to the battle
composer (§14.4), including its frozen-frame message boxes; front-end menus
and mission briefings do not paint that column. The post-battle `ENDMSN`
screen also omits it: after the frozen-frame message-box stage, result setup
clears the surface and installs its authored outcome background, and the
result reveal/service stages paint the result window without the column.

Ageing is [R-CAM-01 §7]'s rule unchanged: once per host frame after the
sub-tick loop, the oldest visible line expires when
`storedTick + (textscroll + 1) × 30 < currentTick`, and the display index
advances by one.

**Established — the fourth append field is the speaker's player slot.** The
value 10 is a sentinel meaning "no speaker" — `'\n'` is simply what 10
renders as, and no real slot reaches 10 — so the cue rule is "plays only for
a line with a real speaker". The poster compares the byte only against
`'\n'`; the drawer is the second reader, and there the same byte selects the
owner logo stamped ahead of the text (§14.4), which is why the field exists
at all. Unit captions and chat lines both pass the sentinel — the chat
composer already embeds `<name>` in the text itself ([R-FE-02 §12]) — so in
a single-player session no line ever carries a speaker and none draws a
logo.

**Established — the on-screen budget is `textlines − 1`.** The walk-back
runs at most `textlines − 1` steps before drawing forward (§14.4), so
`textlines` lines are never on screen: `textlines = 1` draws **nothing at
all**, and the default 10 shows nine. The append rule agrees — the drop test
`(producer + 1) mod textlines == display` keeps at most `textlines − 1` lines
between the two indices. `textlines` is a **modulus**; the budget is one
less.

#### Where the lines are drawn [R-HUD-03 §14.4]

**Established — the owner.** The **master composer** draws the column itself,
onto the same composed surface as everything else, and only when its
"draw the interface" argument is set (it is clear for movie capture). Order
within the frame: the footer and minimap are painted early ([R-HUD-03 §1]);
the message column is painted much later, after the Space-held slide strip
(§6) and the network meter, and before the developer overlays and the
in-battle options unfold ([R-HUD-04 §2]). The same column is repainted by the
frozen-frame path that puts a `MSGBOX` over the last game frame.

**Established — geometry.** All coordinates are absolute screen pixels; unlike
the footer there is **no `baseheight` adjustment**, so the column does not move
with screen height.

1. The start index is the producer walked back at most `textlines − 1` times,
   decrementing with a wrap to 29 and stopping early if it reaches the display
   index. Lines are then drawn forward from that index up to, but not
   including, the producer.
2. The primary UI font (`fonts/COMIX`, [03 R-FONT-01 §5]) is selected; let `h`
   be that font's glyph height.
3. The first line is drawn at `y = 52`; each drawn line advances `y` by `h`.
4. The ordinary foreground is `dcb[15]`; the F3 destination uses `dcb[10]`
   by the flag rule below.
5. A line whose speaker slot is not the sentinel first stamps that player's
   owner logo — the same primitive that draws `LOGO2` ([R-HUD-03 §2]), keyed by
   the owner's lobby colour index — stretched into the square
   `(138, y) .. (138 + a, y + a)`, where `a = trunc(h × 0.8)`; the text then
   starts at `x = trunc(138.0 + 1.5 × a)`, the multiply and the add done in
   doubles and truncated toward zero. A line carrying the sentinel — every unit
   caption, and every chat line — draws no logo and starts at `x = 138`.
6. The text goes through the shared glyph drawer with an **unbounded** maximum
   width and no outline colour, so a long line is bounded only by the
   destination surface.

**Established — the second colour marks the F3 destination.** The drawer
uses `dcb[10]` when bit 5 of the entry's class byte is set and `dcb[15]`
otherwise. F3 clears bit 5 across the ring, then sets it on the live-source
message it chooses; its separate bit-4 visited marker controls cycling. The
append operation's low-nibble write preserves both upper bits. The complete
writer/reader chain is [R-CAM-01 §14], which supersedes the earlier negative
writer census. Ring initialization is not a prerequisite for this branch's
reachability.

**Established — the class filter, and the `screenchat` polarity.** The drawer
selects on a presenter-mode word. Process init sets that word to 3 and nothing
else in the image writes it, so the other two branches (mode 1: draw only class
2; mode 2: draw everything except class 8) are unreachable. In mode 3:

* `screenchat ≠ 0` — the shipped default is 1 ([02 R-KEYS-01 §5]) — **every** class
  draws;
* `screenchat == 0` — only classes **1, 4 and 8** draw.

**Established — the routing-class census** (every producer in the image):

| Class | Producers |
|---:|---|
| 0 | one announcement producer that passes 16, which the writer's low-nibble mask folds to 0 |
| 1 | unit captions (§14.2) — the only producer |
| 2 | the score announcements (`… has taken the lead with %d kills`) and the resource-share line |
| 4 | the ordinary chat commit ([R-FE-02 §12]) and the player-scoped announcements, which are the only lines that carry a real speaker slot and therefore the only ones that draw a logo and play `MessageArrived` |
| 8 | two session/network lines |

So `screenchat = 0` hides the score and share announcements and the class-0
producer, and keeps captions and chat.

#### What a reimplementation must do to put `Target area was blocked` on screen [R-HUD-03 §14.5]

**Established — the whole chain, in order.** Every step is required; skipping
the ring is why the string can be produced and still never be seen.

1. `MobileBuild`'s site test fails with the retry count already above 10
   ([05 "the build-order caption census"]); the handler raises event slot 7
   with the literal caption.
2. The raiser drops it unless the builder is the viewing player's, live, and
   not death-pending (§14.1).
3. The caption is localized and offered to the voice/caption queue: dropped if
   slot 7's next-allowed frame is in the future, dropped if a slot-7 entry is
   already queued, otherwise inserted in descending-priority order, evicting
   and silently resolving the tail if the queue already holds eight
   ([03 §8.3]).
4. Once per host frame the queue's head is resolved. Text is emitted when
   `10 − unitchattext < 8`, i.e. at `Medium` or `Full`.
5. The emitted line is `"<builder display name>: Target area was blocked"`,
   appended to the message ring with class 1, the builder's id, and the
   no-speaker sentinel — truncated to 63 characters, dropped entirely if
   `textlines` is 0, and evicting the oldest visible line if the ring is at
   `textlines − 1` (§14.3).
6. The master composer draws the last `textlines − 1` ring lines as a
   left-aligned column at `x = 138`, first line at `y = 52`, one font height
   apart, in `dcb[15]`, filtered by class as above (§14.4).
7. The line ages out `(textscroll + 1) × 30` ticks after it was stored.

**Unknown — what a mission scripting layer can put in this ring.** None of the
producers above is the campaign/objective text path; whether mission scripts
reach this ring or a separate one is not established here. *Decider:* trace the
mission-event text producers of doc 08 for a call into the ring append.

### The Kills/Losses score panel, the options-window unfold, and the latch-to-idle group reset [R-HUD-04]

#### The Space-held Kills/Losses score panel [R-HUD-04 §1]

**Established — what it is and when it runs.** Holding Space in a skirmish
or multiplayer battle slides a score panel in from the **right** screen edge
listing every player's kills and losses. It is drawn by the battle frame
composer ([03 R-COMP-01]) after the world and chrome and only when the
session kind is 2 or 3 (skirmish / multiplayer — doc 08's kind vocabulary);
a campaign mission (kind 1) never calls it, so its slide word is inert
there. It is separate from the bottom *slide strip* of §6 (`Game Time` /
`Total Units` / `Game Speed`), which the composer steps unconditionally in
every session kind ([R-HUD-03 §1]).

**Established — show/hide polarity.** The panel is *showing* when the F4
interface bit ([R-CAM-01 §2]) is set, or when Space is held (held-key query
for token `0x20`) **and** the focused gadget of the top window is not a
kind-3 text editor. Otherwise it is *hiding*: a text editor with the focus
takes Space for itself and the panel retracts. (The bottom slide strip uses
the same "Space unless a text editor has focus" test, §6.)

**Established — the slide arithmetic.** One signed slide word `s` in
`0..125` is the number of panel pixels on screen; it is stepped once per
composed frame (no wall-clock throttle — unlike the §6 strip's 15 ms gate):

* hiding: if `s < 1` nothing is drawn and the routine returns; if `s == 125`
  the cue `Panel` plays (leaving the open detent); `step := trunc(s / 4)`
  (signed, toward zero), `max(step, 1)`; `s := s − step`; if `s < 1` then
  `s := 0` and the cue `Options` plays (reached the closed detent);
* showing: if `s < 125`: if `s == 0` the cue `Panel` plays; `step :=
  trunc((125 − s) / 4)`, `max(step, 1)`; `s := s + step`; if `s > 124` then
  `s := 125` and the cue `Options` plays;
* the panel is then drawn at the new `s` in the same frame, including every
  intermediate position, so a press shows one frame of a 31-pixel sliver
  (`125/4`).

The cues are the same two aliases the §6 strip plays, through the sound
alias cue of [08 R-CAMP-01 §6]. Because the step is a quarter of the
remaining distance with a floor of one pixel, both directions converge in a
bounded number of frames: 18 composed frames from fully closed to fully
open, and 18 back.

**Established — geometry and painting.** With `W` the composer surface
width, `x0 = W − s`, `x1 = x0 + 125`, `y0 = 32`, `y1 = 40 × playerCount +
46` (the session's player-count word): the rectangle `(x0, y0)–(x1, y1)` is
darkened through the rectangle shader at level `−24` ([03 R-COMP-02 §5]).
The localised `Kills` heading is written at `(x0 + 2, 32)` and `Losses`
right-aligned at `(x1 − textWidth − 2, 32)`, both with a text width limit of
119 and light-table row 0 (the panel text writer's brightness argument).
Rows start at `y = 47` and advance by 40 per row drawn.

**Established — row order and content.** Rows are emitted in **rank order**
`r = 0 .. playerCount − 1`. For each rank the ten player slots are scanned
in slot order for the first that qualifies: record present; controller byte
1, 2 or 3; side byte ≠ 10; live-unit count ≠ 0 **or** the slot's auxiliary
word == 0 (a word with no writer, which therefore reads zero,
[08 R-CAMP-01 §7]); the lobby record's watcher bit
(`0x40`) clear; and the slot's **rank byte equals `r`** (the rank byte and
its maintenance on every credited kill are [08 R-CAMP-01 §9]). The first
match draws the row and the scan stops; if **no** slot holds rank `r`, every
qualifying slot whose rank is greater than `r` has its rank byte
decremented by one (compaction of a vacated rank) and the next rank is
tried — so a vacated rank collapses in the same frame and the row count on
screen equals the number of qualifying slots. For the drawn row, with `y`
the row's top:

* when the slot is the **local** player, the rectangle `(x0 + 4, y − 1)–
  (x1 − 4, y + 38)` is lightened twice, at levels `31` then `20`;
* the side logo is the frame numbered by the lobby record's logo byte in
  entry `32xlogos` of `textures/logos.gaf` (§4), drawn by the quad-mapped blitter
  ([03 R-RAST-01 §1]) from source corners `(1,1) (w−1,1) (w−1,h−1)
  (1,h−1)` (the frame interior, `w`/`h` the frame size) onto the destination
  quad `(x0+7, y+1) (x0+119, y+1) (x0+119, y+37) (x0+7, y+37)` — i.e.
  stretched to 112 × 36;
* the player name is written at `(x0 + 9, y + 6)`, width limit 119, row 0;
* the kill count is formatted `%d` and written at `(x0 + 9, y + 21)`; the
  loss count `%d` right-aligned at `(x0 + 119 − textWidth − 2, y + 21)`;
  both with width limit 119 and their **flash brightness** (below).

**Established — which counters, and the flash.** The kills value is the
slot's kill counter, or its **commander-kill** counter when the
commander-death option word is 2 (*Deathmatch* — [R-FE-01 §7] names the
values); losses likewise select the loss counter or the commander-loss
counter. These are the same words the report screen's `Kills`/`Losses`
columns and the kill-lead line read ([08 R-CAMP-01 §7, §9]); the save
account of the same name persists the first pair ([08 "Player records"]).
Two ten-entry byte arrays hold a per-slot flash for kills and for losses:
the kill-record finalize ([08 R-SKIR-01 §3]) sets the crediting slot's kill
flash and the victim slot's loss flash to **30**, but only while the F4
interface bit is set ([R-CAM-01 §14]; with it clear the finalize skips the
arm and both arrays stay zero, so a Space-held panel shows steady numbers —
the flash is the F4 bit's second visible effect); the score panel routine
decays every non-zero entry by **2** once per unit of the scaled timer
([R-CAM-01 §10], 30 units per second — the same once-per-unit latch as
[R-WGT-01 §1] step 1) whether or not the panel is showing, and passes the
byte as the light-table row of the number's text ([03 R-FONT-01 §6]): a
fresh kill draws its number bright and fades to row 0 over half a second.
Battle entry zeroes both arrays.

**Established — the rank byte's initial value, and the watcher bit's
writers.** *Rank:* the per-slot registration helper — the "registers the slot as human / computer /
inactive" step of [08 R-SKIR-01 §2]'s row-to-player conversion, also run by
the campaign entry for its two seats and by the multiplayer player creation
— writes the slot's **rank byte = the slot index** (and two neighbouring
slot-index bytes) beside the controller byte. Battle entry itself never
touches the byte, and its only other writer is the kill-lead shift of
[08 R-CAMP-01 §9]. So before the first credited kill the ranks are
`0..9` in slot order, distinct, and the panel's rank walk draws the
qualifying slots in ascending slot order with vacated ranks compacted.
**Established.** *Watcher:* the lobby record's watcher bit (`0x40`) has
exactly two setters, both multiplayer-only: the battleroom's `SIDE%d`
control, which turns a human slot into a watcher when the side is cycled
past the last side (and back when clicked again), and the kind-3 branch of
the elimination handler (`You're out!  Continue Watching?`), which sets the
eliminated slot's bit so the player stays in the session as a spectator.
The skirmish elimination branch, the registration helper and battle entry
never set it (registration and the lobby screens only clear neighbouring
bits or clear this one). In every single-player session the bit is
therefore constantly clear, and it is **not** derived from the settlement
gate's observer byte ([05 "Authoritative settlement order"]), which is a
different field. What reads it: this panel's row gate, the score helper's
row gate and the statistics rows' flag bit 3 ([08 R-CAMP-01 §7, §10]), the
kill-lead scan's "non-watcher" filter ([08 R-CAMP-01 §9]), the elimination
and participant filters, the multiplayer camera placement at battle start
([08 R-ENTRY-01 §5] "when the local player is watching") and the lobby's
`Watching:` label. **Established** (bounded census of the bit's writers over
the recovered function set).

#### The in-battle options window unfold [R-HUD-04 §2]

**Established.** Opening the options root in battle (`PREFS.GUI`,
[R-FE-01 §6]) snapshots the top window's surface into the `FLIPSURFACE`
backup, wraps it as a one-frame sprite, and arms an *unfold* animation:
`counter := 0`, `limit := windowWidth − 1`, `top := windowY` (the window
record's y), `skew := 0`, and the options-open word. While that word is set
the composer runs the unfold once per frame, after the score panel and the
chat/diagnostic overlays and before presentation:

1. If `counter < 277`: `counter := counter + 21`; if it is now `> 276` the
   cue `Options` plays and `counter := 277`. Then, if `counter > limit` and
   the **previous** counter was `< limit`, frame 2 of the `LIGHTBAR` entry of
   the common GUI GAF is blitted once into the snapshot at the frame's own
   hotspot (a one-time stamp on the backdrop).
2. `skew := skew + 6` while `counter < limit`; otherwise `skew := max(skew −
   6, 0)`.
3. If `limit < counter < 277`: `counter := counter + 1` (one extra pixel per
   frame after the width is passed).
4. The snapshot is drawn onto the composer surface by the quad-mapped
   blitter ([03 R-RAST-01 §1]) from source corners `(1,1) (w−1,1) (w−1,h−1)
   (1,h−1)` (the snapshot interior) onto a destination quad whose bottom is
   pinned to screen row **479** and whose top edge is skewed by `skew`:
   * while `counter ≤ limit`: corners `(counter, top − skew) (127, top)
     (127, 479) (counter, 479)` — the left edge sweeps right from `x = 0`
     while the right edge stays at the side rail's edge `x = 127`;
   * once `counter > limit`: corners `(limit, top) (counter, top − skew)
     (counter, 479) (limit, 479)` — the left edge parks at the window width
     and the right edge continues to `x = 277`.
   Whether an inverted quad (left corner past the right corner) paints is the
   two-chain rule of [03 R-RAST-01 §1] step 7, not a separate test here.
5. The top window's redraw word is set, battle-interface dirty bit 2 is set,
   and the "full chrome repaint" word is set to 1 — so the HUD repaints every
   frame while the options window is open, not only during the sweep.

The counter, skew and limit words have no other reader; the only observable
effects are the `Options` cue when the counter saturates, the one-time
`LIGHTBAR` stamp, the per-frame quad and the per-frame repaint requests.
Closing the options root clears the options-open word and frees the
snapshot ([R-FE-01 §6]).

**Supported inference.** With the stock in-battle `PREFS` layout (the
window widened by 150, [R-FE-01 §6]) `limit` equals the 277 saturation
value, so the "counter > limit" branch — the `LIGHTBAR` stamp, the extra
one-pixel steps and the second quad form — is unreachable, and the skew
rises for the whole sweep (13 frames, to 78) and then decays. Decider: the
authored `PREFS.GUI` panel width plus 150 (asset census).

#### Command-panel page close and the latch-to-idle group reset [R-HUD-04 §3]

**Established — the page close.** The command-panel *page close* is called
by every selection change ([R-CAM-01 §2]'s deselect, the `n`/`t` cycles,
the `+BigBrother` sweep tail [04 R-MOV-03 §1], the build-page switch of
[R-HUD-03 §6] and the front-end teardown) with one argument, *force*:

1. When *force* is 0 and any of: battle-interface flag bit `0x800`, any of
   its bits `0x65` (bits 0, 2, 5, 6), or any of bits 5–7 of the session-shell
   byte (set while `TABMENU` is open, [R-FE-01 §7]) is set, the close is **deferred**: battle-interface dirty bit
   `0x10` is set and the routine returns 0 without closing anything — the
   interface pass that clears those bits re-runs the close.
2. Otherwise the current-page word is zeroed; if no window is open the
   routine returns 0.
3. Then, while a window is open: if the **top** window's name equals (first
   16 bytes) the command-window name the HUD recorded at battle entry, the
   routine returns 1 — the command window is on top and the pages above it
   are gone; else the top window is closed through the top-object close of
   §3 ([R-WGT-01 §1]) and the test repeats. Returns 0 when the stack empties
   without meeting the command window.

**Unknown — implementation ownership.** The engine currently closes unit info
for selection changes. Mapping the force-zero deferral flags and current-page
word to its shell remains unresolved; establishing those owners is required
before replacing that limited close with this full stack operation.

**Established — the latch-to-idle reset.** The *return the command latch to
idle* step named by [R-CAM-01 §2] (Escape with a latch armed) and §8
(right-click) is one routine: the armed-order latch byte becomes 1 (idle),
bit `0x20` of the latch flags byte (the Shift-latch persistence bit) is
cleared, and — the "palette's default control" phrase of [R-CAM-01 §2] —
the gadget named `STOP` is looked up by index (non-fatal; a miss skips the
rest) and every kind-1 gadget with the same `assoc` byte as `STOP` (the
radio group of [R-WGT-01 §3]) whose status word is non-zero has that word zeroed and is repainted, then
the window redraw word is set. The status word is the button's authored
`status` ([fmt gui]) — the frame base of [R-HUD-03 §6] — so this returns
every button of the order palette's radio group to its up frame. The
group-reset helper takes any gadget index and is shared with the other
latch writers.

#### HUD markers: the logo entry, the slide strip's text offsets, the greyed-button darken row, the first-page seed, and the F4 flash gate [R-HUD-04 §4]

* **Side logo (Established).** Both logo draws — the footer's `LOGO2`
  ([R-HUD-03 §2]) and the score panel's row logo (§1) — read one GAF handle:
  entry `32xlogos` of `textures/logos.gaf`, bound during battle-data
  initialization; the frame index is the owner's lobby colour byte. Rule:
  `frame = logos.gaf["32xlogos"].Frames[lobbyColour]`.
* **Slide strip text (Established).** [07 §6]'s strip: with `x` the left
  edge and `yBottom` the bottom edge of the composer surface's clip rectangle
  (below) and `off` the slide offset (`−31..0`, drawn only while non-zero),
  the strip art is blitted at `(x, yBottom + off)` and the three strings on
  one line at `yBottom + off + 10`: `Game Time` at `x + 25`, `Total Units`
  at `x + 190`, `Game Speed` at `x + 380`; GAF font slot 1, light-table
  row 0.
* **Greyed art buttons (Established).** After the frame blit the gadget
  rectangle goes through the rectangle shader ([03 R-COMP-02 §5]) at level
  `−20` — darken row `12` — unless the button carries attribute `0x80`.
  Frame choice is [R-WGT-01 §3]; the authored `status` is the down-state
  word, not a frame base.
* **First build page (Established).** Unit creation seeds page field `1`
  with the paged bit set when the definition's page-count byte is `≥ 2`,
  else clears both; no click writes the field ([R-HUD-03 §6]).
* **F4 (Established).** The kill/loss flash arms only while interface-flags
  bit `0x80` is set (§1, [R-CAM-01 §14]).

**Established — the slide strip's band, rectangle, font and formats.**

* *Band.* Battle-data initialization looks up entry `LIGHTBAR` of the common
  GUI GAF (`anims/commongui.gaf`, the window record's common GAF of
  [03 R-FONT-01 §5]), takes **frame index 1** (the second frame; 507 × 32 in
  the stock file — index 2 is the 149 × 354 stamp of §2), zeroes that frame's
  two hotspot words in place, and caches the frame pointer; the composer
  blits it through the plain frame blitter at `(x, yBottom + off)`. With the
  hotspot zeroed the blit lands exactly there. Zero frames in the entry
  leaves a null cache and nothing draws.
* *Rectangle.* `x` and `yBottom` are the left and bottom edges of the
  composer surface's **clip rectangle**, which battle entry sets to the view:
  left `128`, top `32`, right `W − 1`, bottom `H − 33` (screen height less
  the 32-row bottom strip, less one). At 640 × 480 the fully raised band
  therefore covers rows `416..447` of columns `128..634` and the text line
  is `y = 426`, with `Game Time` at `x = 153`, `Total Units` at `318` and
  `Game Speed` at `508`.
* *Font.* Before the first string the composer writes the window record's
  current-GAF-font word from **slot 1** (`hattfont11`) and after the last it
  restores slot 0 (`hattfont12`); the strings go through the GAF pen of
  [03 R-FONT-01 §6] with no width limit and mode 0 (glyph bytes copied, no
  light-table remap) — slot 1 and the plain blitter. The side FNT is reached
  only through the pen's null-slot fallback.
* *Formats.* Literal, with the translated key as the first `%s`:
  `%s : %02d:%02d:%02d` (`Game Time`; hours, minutes, seconds of the tick
  count at 30 per second), `%s : %d  (Max %d)` (`Total Units`; the local
  player's live count and the unit limit; two spaces before the
  parenthesis), and `%s %s` (`Game Speed`; the `Normal`-or-`%+d` text of
  [R-CAM-01 §3], with ` (%+d)` appended afterwards while the adapted speed
  differs). No colon follows `Game Speed`; a space-colon-space follows the
  other two keys. With no translation table loaded every key is returned
  verbatim [02 "Translation table"].

`campaignside = ALL` needs no retail contract beyond [R-FE-01 §4]: the side a
campaign battle uses when the campaign names none is the local player's side
record, written from the registry `side` word before the campaign was chosen;
carrying that word into battle entry is plumbing, not an open question.

#### The `ORDERS` and `BUILD` stage buttons' cues [R-HUD-04 §5]

**Established** (the command-window click handler that also owns the
factory product click of [R-P0-11 §1]). The handler tests the clicked
gadget's name against four
literals, in this order, before it reaches the product path: `PREV`, `NEXT`,
`ORDERS`, `BUILD`. Each test is a **substring** search, not a whole-name
compare, which is what admits the side-prefixed stock names (`ARMORDERS`,
`CORBUILD`). Each of the four raises one deferred bit and returns; only the
two stage buttons also **play a named cue**, through the ordinary interface
cue helper (the same helper `addbuild`, `Panel` and `Options` use, with the
local-player argument):

| Gadget name | Deferred bit | Cue |
|---|---|---|
| `ORDERS` | the bit whose consumer clears the unit's page-shown bit | `ordersbutton` |
| `BUILD` | the bit whose consumer sets it | `buildbutton` |

The cue is played on the click, before the deferred bit's consumer runs, and
neither button plays `nextbuildmenu` — the `PREV` and `NEXT` rows above are
silent at the click, and `nextbuildmenu` is played by the page-switch routine
their deferred bits reach (§9, [R-P0-11]).

### The battle chrome at display modes larger than 640×480 [R-HUD-05]

When the negotiated surface is one of the larger modes of [R-FE-02 §9],
applied at the load transition of [R-FE-01 §11], the battle interface is
neither scaled nor letterboxed. Every element follows one of a handful of
rules, each reading the
surface width `W` and height `H` that the presentation state holds, and the
authored 640×480 art is reused as-is. `W×H = 640×480` is the degenerate case of
every rule below, which is why nothing in this section changes the 640×480
picture.

**Established — the surface and the world viewport.** The battle presenter
re-creates its offscreen composition surface at exactly `W×H` when the battle
shell starts, and the battle loader rebuilds the world viewport subrect as
`(128, 32)..(W−1, H−33)` inclusive, a span of `W−128 × H−64` [03 §4.1]. The
composer's viewport extents in cells are those spans shifted right by four
(floor), and the minimap viewport rectangle of [03 R-MM-01 §1] uses the cell
count shifted back left, so the 536-row span of 800×600 projects as 528.

**Established — the first paint.** Before the first frame the presenter fills
the whole surface with palette index 0 and stamps the three panel entries at
their final origins — `PANELTOP` at `(129, 0)`, `PANELBOT` at `(129, H−32)`,
`PANELSIDE` at `(0, 0)` — through the offset-cancelling contract of §6's
"Panel asset binding and draw origins". `PANELSIDE` is stamped here and
nowhere else: the loader and this first paint are the only readers of its
handle in the image.

**Established — what the per-frame composer repaints, and where it stops.**

1. The top strip, when the resource snapshot changes ([R-HUD-03 §4]):
   `PANELTOP` at `(129, 0)`, then the side's `PANELBOT` frame at each
   successive `x += frameWidth` while `x < W`, every stamp at `y = 0`. The
   stock `PANELBOT` frames are 33 rows tall against the strip's 32, so on the
   frames that repaint the strip each extension stamp's last row lands on the
   viewport's first row (`y = 32`), and the world composition — which runs
   first, every frame — takes that row back on the frames that do not. At
   640×480 the stock `PANELTOP` (513 wide for ARM, 511 for CORE) reaches the
   edge in one stamp and no extension is drawn.
2. The footer's backdrop ([R-HUD-03 §1]): `PANELBOT` at `(x, H−32)` for
   `x = 129, 129 + frameWidth, …` while `x < W`. The 33rd row falls off the
   surface.
3. The GUI window pass repaints a window only when it is marked dirty or when
   its rectangle intersects the viewport subrect. The rail windows — the root
   `<prefix>MAIN2.GUI` and every command page, all authored at `(0, 128)`
   with size `128×352` — therefore persist from their own paints, and no
   per-frame path touches the rail's columns `0..128` below row 479 or the
   bottom strip's columns `0..128`. **On a surface taller than 480, the band
   under `PANELSIDE` stays palette index 0 for the whole battle.**

**Established — what follows the surface, and what stays put.**

| Rule | Elements |
|---|---|
| Anchored to the bottom edge `H` | the footer's `y` anchors through `dy = H − baseheight` ([R-HUD-03 §1]); the §6 slide strip; the `Send` throughput meter at `(129, H−95)`; the frozen-frame `Click to continue` line at `y = H−20` |
| Anchored to the right edge `W` | the Space-held score panel, `x = W − slide` ([R-HUD-04 §1]); both strip stamp loops; the pointer clamp and edge-scroll tests at `W−1` / `H−1` (§10) |
| Centred on the surface | a window opened with the "centre" placement flag: `x = (W − w) / 2`, `y = (H − h) / 2` |
| Centred in the view | a window opened with the "centre in the view" flag — `EXITMENU`, `YESORNO` ("Tab options menu and manual exit"): `x = (W − 128 − w) / 2 + 128`, `y = (H − h) / 2`; the in-game title frames `igpaused` / `igvictory` / `igdefeat` (§11), whose draw origin is the view centre `((W + 128) / 2, H / 2)` less the frame's authored offsets; the camera clamp's maximum, `PlayRight − (W − 128)` and `PlayBottom − (H − 64)` ([R-CAM-01 §13]) |
| Fixed in authored coordinates | the top strip's anchors (`LOGO`, the bars and their numbers — no `dy`); the radar canvas, a 126-pixel square at the surface's top-left corner letterboxed by map aspect, with `RADAR FINAL` blitted at its pad offsets; the message column at `x = 138`, first line `y = 52` ([R-HUD-03 §14.4]); every rail window and gadget rectangle; the unit information screen ([R-HUD-03 §8]) |

All divisions above are truncating integer divides.

**Established — the window initializer's sentinel rules.** The two placement
flags write sentinels into the root gadget's `xpos`/`ypos` before placement:
"centre" writes `−1` into both, "centre in the view" writes `−2` into `x` and
`−1` into `y`. Placement then tests **`x` alone**: `x = −1` centres both axes
on the surface, `x = −2` centres `x` in the view and `y` on the surface; `y`'s
own value is never consulted. Afterwards, an `x + w > W` recentres `x` and a
`y + h > H` recentres `y`, and a window wider or taller than the surface is
refused. `W` and `H` are the live surface size, so a sentinel-placed window
moves with the display mode while an explicitly placed one does not.

**Nanolathe impact.** The build reads the surface size at draw time
and applies the rules above: `hud.StripStamps` / `hud.BottomStripY` /
`hud.RailGap` / `hud.ModalPlacement` carry the arithmetic and the battle
composer stamps the strips, paints the rail band and re-places the two modal
windows from them. Two deliberate presentation choices, each recorded at the
site: the top strip's extension stamps are clipped to the strip's 32 rows,
because this composer repaints the strip every frame and would otherwise show
the 33rd-row overlap permanently rather than only on retail's repaint frames;
and the rail band is measured from the art's authored 480 rows rather than from
the slid panel, so the panel slide's own uncovered rows remain the 640×480
matter they were. The paused title follows the established view-centre anchor
and subtracts its authored GAF offsets at every display size. GUI window
placement is a separate operation; the two battle modals use the view-centred
window placement above.


### Supported inference

Battle chrome should be data-driven from side-data, while semantic values and
command availability remain runtime state. A generic GUI implementation may
share drawing primitives, but must retain the side-data anchor contract.

**Battle-rail minimap destination is closed (Established).** The battle composer
copies the FINAL radar surface to the origin of its fixed 126×126 logical canvas.
The aspect-dependent `originX/originY` values belong to the picture's internal
letterbox and are not an additional screen placement. Consequently the canonical
destination rectangle is inclusive `(0,0)..(125,125)`; drawing and input both
apply the same letterbox inside that canvas [07 §6][07 §10].

### Unknown

- The reader, if any, of the `CORBUILD` gadget-name exclusion in the build
  card · §6 [R-HUD-03 §3] · asset census for a gadget of that name.
- Whether any stock or third-party content authors a `<unit>0.GUI` page (the
  authored-page bit of [R-HUD-03 §6] is never set by stock content) · §6
  [R-HUD-03 §6] · asset census.
- Whether a mission script can post to the message ring, or whether campaign
  objective text has its own path; none of the ring's producers in
  [R-HUD-03 §14.4]'s class census is a mission-event producer · §6, doc 08 ·
  static trace of the mission-event text producers.
- The authored width of the in-battle `PREFS` window, which decides whether
  the options unfold's second quad form and `LIGHTBAR` stamp are reachable ·
  §6 [R-HUD-04 §2] · asset census.


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

**There is no drop-shadow switch.** The glyph rasterizer's extra argument is
the **skip colour** — the palette index
the rasterizer treats as transparent, set once to 254 at front-end
initialisation — and font data carries no shadow either. Every shadow or
outline seen in retail is a caller composition: the label painter draws the
string twice with the shadow pass at `(+1, +3)`; the report prompt draws a
four-way outline. Arithmetic, truncation and clip rules are in
[03 R-FONT-01 §3–§4]; the GAF-font pen, wrapper and button pen in
[03 R-FONT-01 §6].

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

- Text wrapping beyond the two closed wrappers ([R-FE-02 §6],
  [03 R-FONT-01 §6]) — whether any other caller wraps · §7 · static trace.
- Code-page behavior for extended bytes · §7 · static trace (`TODO(T23)`).
- Font fallback order beyond the closed missing-HATTFONT-to-active-FNT path
  · §7 · asset census.
- Translation-table missing-key rules and the complete translation lookup
  fallback · §7 · static trace.
- Malformed HATTFONT (no `I` frame) handling · §5, §7 · undefined by
  construction in retail ([03 R-FONT-01 §6]); asset census decides whether
  it ever occurs.


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
19 `cursornormal`, 20 `cursorhourglass`, 21 `pathicon`; slot 0 is the zero
word before the first handle, which no producer selects and which would fault
if bound ([03 R-FX-01 §5]). Slot 10 is filled last by the init sequence, out
of the otherwise ascending order (`docs/SPEC_CONFLICTS.md` records the
transcription that once shifted every later index by one). Index 19 is the
idle default the pointer
update falls back to, and 20 is the loading shape the front end installs
around blocking transitions. The index writer diffs and swaps shapes, so
re-selecting the shape already shown does not restart its animation. The
armed-order latch decides authorization while the index decides shape; they
coincide numerically only by table offset and must not be conflated. During
mobile-build placement, site validity picks `cursorfindsite` when placement is
valid else `cursortoofar`; the ghost preview uses `cursorred`/`cursorgrn`.

**Mouse polarity depends on `Interface Type`.** Armed orders fire on left
click in both modes, and a right click while an order is armed returns the
latch to idle. With the latch idle, Type 0 uses left click for the contextual
order and right click to clear selection; Type 1 uses right click for the
contextual order and left click to select or clear selection ([R-CAM-01 §5]).

**The shape chooser is closed.** One pointer update resolves the shape in four
steps. First, two region bits record whether the pointer is over the world
viewport or over the minimap; when neither is set the index is forced to
`cursornormal` and nothing else is consulted. Second, the mobile-build latch
with a live placement ghost takes the site-validity branch above. Third, the
selected units of the local player — walked over the owner's inclusive range at
the fixed 280-byte stride, admitted by the membership bit `0x10` — together
with the hovered unit are
each asked for a shape, and **the lowest index wins**, so the table's numbering is
also its shape priority order (the chooser collects the selected units plus
the hover id, evaluates each through the per-latch shape table, and keeps the
smallest index with a strict `<` reduction). Fourth, when that
candidate set is empty the
idle latch over an own, selectable, finished unit gives `cursorselect`
and everything else gives `cursornormal`. The tests behind "selectable,
finished" are the shared eligibility predicate of [R-WGT-01 §9]: status bit
5, remaining-build fraction exactly `0.0`, post-capture grace counter zero,
and carrier null or cargo-selectable. The predicate reads no order state at
all ([R-WGT-01 §10]): an implementation that tested a separate order-queue
emptiness would gate `cursorselect` off for every idle unit whose queue
holds its `defaultmissiontype` standing record (the idle-queue refill of
[04 §3.3]).

Per selected unit the Type-0 shape is dispatched on the armed latch and gated
on the same authored capability flags the order predicate reads, so the
advertised action and the performed action cannot disagree. The armed rows
also apply unchanged to Type 1:

* Idle (latch 1, Type 0) rewrites itself to ATTACK over a hostile target the unit can
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

**The Type-1 idle row is relationship-coloured, not action-shaped.** It tests
an own selectable finished target first and returns `cursorselect`; otherwise
a hostile unit returns `cursorred` and any other unit returns `cursorgrn`,
without an actor-capability test. With no unit target, a reclaimable feature
returns `cursorgrn` when the actor has `canresurrect` or `canreclamate`, in
that order; failing those, the answer is `cursornormal`. Thus this row can
return only `cursorselect`, `cursorred`, `cursorgrn`, or `cursornormal`. The
chooser still reduces all per-actor answers by lowest cursor index, and an
empty acting selection still uses the shared idle fallback described above.
No armed-latch row branches on `Interface Type`.

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
an error to correct.

The triple is stored for the frame, and its cell index (`>> 20` per axis) is what
the order dispatcher, the build-site validator, and the hover/status readout all
consume.

Queued orders and path previews use separate cursor/indicator artwork. The
cursor validity state is therefore a presentation of command legality, not
just a pointer shape.

#### Selection overlay and picking evidence [R-SEL-02A]

**Established (direct-static).** The authored 3DO selection primitive is not
the retail selection wireframe. Model loading moves a declared selection
primitive to primitive zero, and the model draw walk skips that slot. No
selection primitive read occurs in the bulk drag selector or in the traced
hover path; no authored plate is reused by any selection consumer.

The four consumers have the following established split:

* The wireframe visible during box selection is the composer's two solid
  one-pixel rectangular frames, not a 3DO polygon. Their outer/inner palette
  entries and fog ordering are specified in [03 §2.4.1] and [03 §1].
* Overlap/drag selection projects each eligible unit's origin point and tests
  that point against the normalized rectangle. It does not project a piece,
  selection primitive, or model extent. Both rectangle boundaries are
  inclusive, and all eligible units at the same point are admitted in the
  stable unit-pool walk order.
* Point click uses the hover id computed before the click. The hover search
  walks the `HOT UNITS` list, projects each unit's transformed root-piece
  bounds as a four-corner screen polygon, and retains the smallest score with
  a strict `<` comparison. Equal scores therefore retain the earlier list
  member (the lower stable pool position). The list is rebuilt by a
  frame/presentation producer rather than by the sensor phase, its
  membership test is not the §9 eligible-unit predicate, and the projected
  box comes from the selected root piece alone [R-REV-01].
* Cursor targeting consumes that same hover/visibility result for its
  target-dependent cursor branches; no authored selection primitive is read.

The drag boundary rule is therefore `min <= coordinate <= max` after
independent endpoint sorting. The projected-hull edge convention is the
opposite: the polygon helper requires a strictly positive signed
cross-product at every edge, so equality on an edge is rejected
([R-SEL-02B2]; the sign convention and the bounds-extrema-to-corner mapping
are [R-REV-01 §2, §4]). The strict rule must not be confused with the
inclusive drag-rectangle rule.

**Established layer order and clipping.** World terrain, features, units,
projectiles, and effects are composed first; the world fog/LOS overlay is
then applied; the selection rectangle follows fog; and HUD/interface gadgets
are composed later. The selection solid-frame writer consumes the inclusive
clip rectangle copied into the active surface descriptor from presentation
state. Its projected rectangle coordinates carry the separate beam-space
`+128/+32` offsets; those offsets are not the clip rectangle's left/top
values. The later HUD rail can overwrite pixels in its own interface pass.
No separate plate clipping rule exists because no plate is drawn.

**Viewport coordinates.** The `(0,32,W-1,H-33)` battle viewport rectangle
of §5 and the `(128,32,W-1,H-33)` subrect of [03 §4.1] describe different
coordinate records, not one universal selection clip. The static
call chain establishes that selection consumes the runtime surface-descriptor
clip, but does not establish its left value for every visible/hidden-panel
state. That selection-left value is therefore **Unknown** until a focused
mode/panel capture records the descriptor at the selection draw. Neither
tuple may be used as a universal canonical value.

**Established palette/remap.** The rectangular outline's outer color is
logical map entry **15** for an ordinary drag-selection rectangle; the 6/4 pair
replaces it only while the armed latch is MOBILEBUILD — entry 6 when the
site-valid bit is set, entry 4 when it is clear. Its inner frame is entry 0.
Each is looked up once through the logical-to-physical palette map before the
solid indexed writer. The outline is neither fog-remapped nor blended; raw GAF
image bytes and this semantic map path must not be conflated. The
conditioning term is the armed latch, never the existence of a drag (logical
4 resolves to a dark red, so the ordinary case is visibly entry 15), and the
bit that picks 6 over 4 is the pointer-flags site-valid bit of
[R-CAM-01 §14] step 1, not the latch-flags helptext bit.

**Unknown.** The static evidence does not show a per-selected-unit plate
pass, an extra primary-selection wireframe/chrome rule, or an authored plate
used for cursor targeting. Whether the UI adds primary-only information in a
separate HUD page is outside this wireframe path. A focused capture with two
selected units, a single/primary selection, overlapping hover hulls, and
pointer samples around the hull corners would settle those remaining questions;
edge equality itself is already closed by R-SEL-02B2.

#### Hover hull arithmetic and publication boundary [R-SEL-02B2]

**Established (direct-static).** The hover finder is a separate point-pick
pass from the drag selector. In the viewport it walks the `HOT UNITS` list,
which is rebuilt by a frame/presentation producer from an ascending unit-pool
walk and retains that order ([R-REV-01 §5] states the producer's admission);
neither the producer nor the hover consumer evaluates the §9 eligibility
predicate — those gates belong to their own selection consumers. The list
contains units only; feature contacts are not candidates in this pass. A
non-empty unit definition/model reference is required before the hull helper
is called. The visibility source is mode-selected: the local player's bit in
the shared word coverage, or a nonzero byte in the selected per-viewer
coverage grid. Visibility is resolved when the list is built, so a hidden
unit cannot win merely by having a nearer hull.

**Established (direct-static).** For an admitted viewport candidate, retail
asks the unit's model/piece-hierarchy bounding-box helper for its model-space
box, builds four extrema-derived corner records, and sends each through the
same hierarchy transform used by the unit's current orientation. The helper
does not read the authored 3DO selection primitive or the footprint cells.
For each transformed corner `(x,y,z)`, the screen coordinates are:

```text
screenX = int16((x + unitX - cameraX) >> 16) + 128
screenY = int16(((unitZ - cameraZ) - z) >> 16)
         - (int16((y + unitY) >> 16) >> 1) + 32
```

The transformed Z is **subtracted** from the camera-relative unit Z, not
added: the subtraction is the transient negation of Z that [03 §2.4]
describes inside the projection helpers, folded into the same expression.
The narrowing occurs before the half-height shift and before the view-origin
addition. The unit's committed position supplies `unitX/unitY/unitZ`, and its
three committed orientation accumulators supply the hierarchy transform;
camera X/Z are the current presentation camera values, scaled to 16.16 before
the subtraction. This is the exact projection used by the four-point hover
path and is distinct from the origin-only drag projection in §9.

The helper's outputs, its vertex source, the disabled hierarchy walk, and
the exact four-corner component mapping are [R-REV-01 §1–§2]. A production
replacement must not substitute `FootprintX/Z`, the authored selection face,
or a generic bind-pose box.

**Established (direct-static).** The four projected corners are passed to a
four-point polygon hit helper. The helper's return value is the sole viewport
hull admission test. It rejects fewer than three vertices, then visits each
edge in order and admits the point only when the signed cross-product test is
strictly positive. Each product is evaluated as a signed 32-bit value (the
low signed product is retained); equality therefore rejects the point. The
inclusive rule documented for drag rectangles does not apply to hover hulls.
This strict edge rule is shared by the precomputed hover id consumed by click
and cursor paths.

The clean-room form of the tested predicate is:

```text
containsStrictPolygon(point, vertices):
    if len(vertices) < 3:
        return false
    for i in 0 .. len(vertices)-1:
        prev = vertices[i]
        next = vertices[(i + 1) mod len(vertices)]
        lhs = signed32((next.y - prev.y) * (point.x - prev.x))
        rhs = signed32((next.x - prev.x) * (point.y - prev.y))
        if lhs <= rhs:
            return false
    return true
```

The operand order fixes which winding of the quad counts as inside; the
reversed order would admit the complement of the retail hull.

For the retail hover path `vertices` has four entries. `signed32` means that
the low signed 32-bit product is the value compared; it is not a floating
point cross product, has no epsilon, and has no overflow guard, so a
replacement must keep the 32-bit wrap rather than widening silently. The
corner order and the mapping from the box's six extrema into those four
entries are established in [R-REV-01].

**Established (direct-static).** Among candidates whose polygon admits the
pointer, the hover reduction computes the following fixed-point score, using
the model-height and horizontal-span terms in the order shown by the retail
helper:

```text
score = ((((modelHeight * 32768) >> 16) + zSpan) * xSpan) >> 16
```

`modelHeight` is the compiled model total-height term, while `xSpan` and
`zSpan` are the compiled horizontal box spans — three compiled definition
words read directly by the reduction, unrelated to the six hull extrema
([R-REV-01 §7] names their writers). Both multiplications are
evaluated as signed 64-bit products and each `>> 16` is an arithmetic shift
of that 64-bit intermediate before the result is taken as a signed 32-bit
score; they are fixed-point narrowing steps, not floating-point rounding
(**Established**, direct-static). The winner is replaced only when
`score < bestScore`.
Equal scores retain the earlier `HOT UNITS` member, hence the lower stable pool
position. The minimap branch uses `HOT RADAR UNITS` instead and admits a
contact only when planar squared distance is strictly less than four; it also
retains the first member on an equal distance.

**Established (direct-static).** The click path consumes the hover id already
computed by the pointer update; it does not recompute the hull. Cursor target
shapes consume that same hover/visibility result. Thus cursor and click agree
for every admitted interior point and reject an edge point under the same
strict polygon test; what cursor and click share is one computed hover
result.

**Presentation publication gap (supported inference from [03 §2.4] and
[03 §2.5]).** The committed `frame.UnitView` publishes the unit pose (`X/Y/Z`
and heading/pitch/bank), model name and definition id, flags, footprint
(`FootX`/`FootZ`), and per-piece rotation/translation/hidden state. The hull
corners and the three score terms are reproducible from that
([R-REV-01 §7, §10]); what the frame does not publish is the `HOT UNITS`
membership/order and the producer's viewport/visibility admission result.
Reading live simulation state or treating the frame slice as the hot list is
forbidden at the presentation boundary [03 §2.4–§2.5][07 R-SEL-02A], so a
pixel-for-pixel replacement of the producer needs a committed ordered
candidate record ([R-REV-01 §6]). `TODO(question)`: publish that record at
the committed-frame boundary.

#### Hover hull extrema, corner mapping, projection sign, polygon predicate, and HOT UNITS producer [R-REV-01]

This section derives the whole viewport hover chain — list producer,
foreign-visibility helper, per-candidate hull test, model bounds helper,
orientation transform, four-point polygon helper, and score reduction.
Nothing here changes the drag-rectangle or minimap contracts.

**1. Model bounds helper — Established (direct-static).** A wrapper
zero-fills two output triples and a zero base triple, then calls a recursive
body with the selected model node, the base triple, both outputs, and a
walk flag. For each visited node the body forms `translated = base + node
translation`. If the node's vertex count is **strictly greater than two**,
every vertex contributes `translated + vertex` componentwise: the first
output keeps the componentwise minimum (replaced when a candidate component
is strictly less than the stored one) and the second the componentwise
maximum (replaced when strictly greater). Nodes with two or fewer vertices
contribute nothing. Because the outputs are zero-seeded rather than
first-vertex seeded, an axis whose translated vertices are all positive keeps
zero as its minimum, and an axis whose vertices are all negative keeps zero
as its maximum; a selected node with two or fewer vertices leaves all six
values at zero. When the walk flag is nonzero the body recurses into the
node's child with the translated base and then follows the sibling chain
with the unchanged base. **The hover caller passes the disabling value**, so
on this path only the selected root node contributes vertices — children and
siblings are never visited. The six values are therefore minima and maxima of
translated authored vertex coordinates, taken from the 3DO node translation
and vertex arrays described in [03 §2.4]; they are not footprint cells, the
authored selection face, or sprite bounds.

**2. Four-corner mapping — Established (direct-static).** With
`min = (minX, minY, minZ)` and `max = (maxX, maxY, maxZ)` from the helper,
the hover caller writes four contiguous three-component records in this
order, then transforms and projects them in the same order:

```text
corner[0] = (minX, minY, minZ)
corner[1] = (maxX, minY, minZ)
corner[2] = (maxX, minY, maxZ)
corner[3] = (minX, minY, maxZ)
```

`maxY` is computed by the helper but **never loaded by the hover caller**.
The hull is therefore a horizontal rectangle in the XZ plane at the model's
minimum Y — a ground-level quad, traversed in a consistent winding — not a
diagonal slice through the box and not a full projected AABB. When the helper
returns all zeros (see §1) the four all-zero records still enter the normal
transform and projection loop; no separate caller-side invalid-model fallback
exists on this path. The mapping is directly readable from the caller's
record initialization: each of the four records is written from named helper
output components before the transform loop starts.

**3. Projection — Established (direct-static).** Each corner record is fed
through the same three-axis orientation transform the renderer uses, driven
by the unit's three committed orientation accumulators, and the result is
projected as:

```text
screenX = int16((x + unitX - cameraX) >> 16) + 128
screenY = int16(((unitZ - cameraZ) - z) >> 16)
         - (int16((y + unitY) >> 16) >> 1) + 32
```

where `x/y/z` are the transformed corner components, `unitX/unitY/unitZ` the
unit's committed position, and the camera X/Z values are scaled to 16.16
before the subtraction. Each `>> 16` narrowing and its truncation to a signed
16-bit value happen **before** the half-height shear and before the viewport
bias is added. The transformed Z is subtracted, not added: the same trailing
Z sign [03 §2.4] describes for the projection helpers.

**4. Four-point polygon predicate — Established (direct-static).** The helper
returns false for fewer than three points. Otherwise it visits every directed
edge once — retail indexes `previous = p[i-1]`, `next = p[i mod n]` for
`i = 1 .. n`, which enumerates the same edge set as `prev = p[i]`,
`next = p[(i+1) mod n]` for `i = 0 .. n-1` — and admits the point only when
every edge satisfies

```text
(next.y - prev.y) * (point.x - prev.x) > (next.x - prev.x) * (point.y - prev.y)
```

evaluated as two low signed 32-bit integer products compared with a signed
comparison. Equality rejects, so every exactly collinear point — edge
interior or vertex — is outside. There is no tolerance, widening, saturation,
alternate edge path, or overflow guard, and the index wrap is signed integer
division and remainder.

**5. `HOT UNITS` producer and consumer — Established (direct-static), with
the caller context a Supported inference.** The list is rebuilt from scratch
by one producer: it takes the stored list base, zeroes its running count,
walks unit memory from its lower to its upper bound inclusive at the fixed
unit stride, and appends the stable unit identity of each accepted candidate
in that ascending order, writing the final count when the walk ends. There is
no duplicate filter and no producer-side capacity branch. Its admission is
exactly three tests, in this order:

1. **Non-empty definition/model reference.** The candidate's definition/model
   index word must be nonzero. This is the same word the hull path uses to
   select the model, and it is the only per-candidate slot test.
2. **Projected definition bounds intersect the viewport.** The producer
   combines the unit's committed position with six compiled definition extent
   words, applies the same `+128` horizontal and `+32` vertical biases and the
   same half-height shear as the hover projection in §3, and compares the four
   resulting bounds against the four viewport bounds. All four comparisons are
   inclusive, so a candidate whose projected bound exactly touches a viewport
   edge is retained. Before that comparison, when the candidate's two low
   movement-mode status bits are not equal to 1, the producer queries the
   terrain record under the unit's position and, if that query returns a
   record whose height byte is smaller than the accumulated vertical term,
   clamps the term to that byte. That the tested bits are the movement-mode
   bits described in [04] is a **Supported inference**; what would settle it
   is a writer census of that status word.
3. **Ownership or foreign visibility.** A candidate whose owner byte equals
   the local player's owner byte is appended without any visibility query.
   Otherwise the shared foreign-visibility helper decides: it returns true
   immediately for a candidate owned by the queried player record; returns
   false when the candidate's hidden/cloak bit is set; returns false when the
   candidate's movement flags lack the exempting bit and its derived vertical
   sample is below the global sea-level byte scaled to 16.16; and otherwise
   tests up to four samples derived from the unit position plus compiled
   definition extents. Each sample is resolved through the mode-selected
   coverage representation — a per-viewer byte grid with bounded, half-open
   cell coordinates, or the shared word grid tested with the local player's
   bit — and the **first** sample that reads visible admits the candidate;
   later samples are evaluated only after an earlier one fails.

The producer body evaluates **no** selectable-bit, remaining-build-fraction,
grace-counter, carrier-state, or health predicate. The viewport hover
consumer walks the stored identities in producer order, repeats only test 1,
calls the hull path of §§1–4, applies the score reduction, and replaces the
winner only on a strictly smaller score — so an equal score retains the
earlier producer member. The minimap branch reads a separate radar-contact
list and is not part of this record.

That this producer runs in the frame/presentation update rather than in the
sensor phase is **Supported inference**: the producer reads only unit memory,
camera, viewport, and coverage state, and holds no sensor list, but the
bounded caller census that places it in the frame update was not re-derived
here. What would settle it is a caller census of the producer taken from the
frame-update and front-end refresh roots. Either way no sensor product is
read.

**6. Remaining boundary — Unknown.** The committed `frame.UnitView` cannot
represent this result: its `Units` slice is the published unit set, not the
producer's viewport- and visibility-filtered list, and it carries no list
membership or rank, no producer admission result, no resolved hull corners or
extrema, and no score terms. Recomputing them at presentation time would mean
guessing those inputs or reading live simulation state, which
[03 §2.4–§2.5] forbids. The smallest evidence-backed publication is one
committed ordered candidate record whose slice order **is** the producer's
rank and whose entries carry the stable unit identity, the producer's settled
admission, the four resolved hull corners (or the six extrema plus the §2
mapping), and the three score terms or the reduced score. This is a data
boundary, not a prescribed API. Whether the existing authoritative
presentation pass can publish it without a new cross-package API is
**Unknown**; the decider is a design pass over the publication boundary, not
a further trace. The authored provenance of the three score terms is §7; the
reduction itself is reproducible at the presentation boundary with no new
record (§10).

#### The three hover score terms, their definition-compiler writers, and what decides an overlapping click [R-REV-01 §7–§10]

The three compiled definition words the hover reduction reads are written by
the definition compiler and the catalog loader. Nothing here changes the hull
geometry, the projection, the polygon predicate, or the producer's admission
tests.

**7. The three words and their writers — Established (direct-static).** Each
compiled unit definition carries a six-word model box — a per-axis minimum and
maximum — followed by three extent words and a derived mean. The extents are
differences of the box:

```text
xExtent = maxX - minX
yExtent = maxY - minY
zExtent = maxZ - minZ
```

Two separate passes fill them, in this order:

* The **FBI unit-record compiler**, having read the authored `FootprintX` and
  `FootprintZ` keys into two definition halfwords, writes the horizontal box
  purely from the footprint and centres it on the model origin:
  `minX = -(footprintX << 20) / 2`, `maxX = (footprintX << 20) / 2`, and the
  same pair for Z from `footprintZ`. It then writes the three extents as the
  differences above and a fourth word `(zExtent + xExtent) / 3` that the hover
  path never reads. So `xExtent = footprintX << 20` and
  `zExtent = footprintZ << 20` exactly — a footprint cell is sixteen world
  units, and `<< 20` is that sixteen expressed in 16.16, so each horizontal
  extent is the footprint cell count times sixteen world units in 16.16. These
  two words are **not** hull-helper outputs and are unrelated to the six hull
  extrema of [R-REV-01 §1]; nothing later overwrites them.

* The **catalog loader**, immediately after loading the definition's 3DO and
  binding its textures, zeroes the definition's minimum-Y word, calls the
  **model-height helper** on the loaded node tree, stores the answer as the
  maximum-Y word, and rewrites `yExtent = maxY - minY`. Because the minimum was
  just zeroed, the Y extent the reduction reads **is** the model total height.
  This second pass runs after the FBI compiler's write, so the footprint-derived
  Y extent the compiler produced never survives; the X and Z extents do.

**Model-height helper — Established (direct-static).** It returns one signed
16.16 vertical value for a node and takes no arguments beyond the node. Its
running maximum is seeded at **zero**, not at the first vertex. It walks the
node's sibling chain; for each node it takes the maximum of
`vertexY + nodeTranslationY` over that node's own vertex array, and, when the
node has a child chain, takes the maximum against `helper(child) + nodeTranslationY`.
Two consequences follow from the zero seed: a model whose vertices all lie below
the origin has height zero rather than a negative height, and a subtree lying
entirely below its parent contributes zero rather than lowering the result. The
helper applies **no** minimum-vertex-count gate — that gate belongs to the hull
bounds helper of [R-REV-01 §1] and not to this one — reads no orientation, and
runs once per definition at load time on the authored bind pose.

**8. The reduction, restated with its terms named — Established
(direct-static).** With the writers above, the reduction of [R-SEL-02B2]
reads:

```text
score = ((((yExtent * 32768) >> 16) + zExtent) * xExtent) >> 16
      = ((((modelTotalHeight * 32768) >> 16) + (footprintZ << 20)) * (footprintX << 20)) >> 16
```

Both multiplications are signed 64-bit products; each `>> 16` is an arithmetic
shift of that 64-bit intermediate whose result is taken as a signed 32-bit value
before the next step, including the addition, which wraps in 32 bits. There is
no overflow guard. The running best is seeded above every reachable score and
replaced only on a strictly smaller score, so an equal score retains the earlier
`HOT UNITS` member — the lower stable pool position [R-REV-01 §5].

**9. What decides a click that hits more than one unit — Established
(direct-static), with one consequence a Supported inference.** The whole of
retail's overlap rule is the §8 reduction over the candidates whose hull admits
the pointer. Enumerated against the plausible alternatives, none of which appear
anywhere in the producer or the consumer:

* **Not draw order or depth.** The hover consumer walks the `HOT UNITS` list in
  the producer's ascending pool order [R-REV-01 §5]. It reads no bucket, row,
  pass or depth key, and the painter's two-pass split of [03 R-RAST-01 §7] has
  no counterpart here.
* **Not altitude.** No unit Y, terrain height, or airborne test is read by the
  consumer. The producer touches a terrain record only to clamp one projected
  viewport bound before its bounds/viewport intersection [R-REV-01 §5 test 2];
  that clamp cannot reorder candidates because it feeds an inclusion test, not a
  rank.
* **Not a mover-class or unit-category priority.** The consumer repeats exactly
  one per-candidate gate, the non-empty model reference; it evaluates no
  movement-mode, building, or category predicate.
* **Not list order alone.** List order is only the tie-break, reached when two
  scores are exactly equal.
* **Not a bounding radius.** Admission is the four-point strict polygon test of
  [R-REV-01 §4]; the only distance comparison in the function is the minimap
  branch's separate squared-distance test against four, over a different list.

The score is therefore a pure definition-size measure — half the model's height
plus its Z footprint, scaled by its X footprint — and **the smallest definition
wins**. The airborne-unit precedence a player observes when clicking a plane
that is flying over a plant is this rule and not an altitude rule: an aircraft's
definition is smaller than the plant's, so it scores lower. The **Supported
inference** is only the generalization, not the mechanism: because stock
aircraft carry small footprints and short models, the size rule reproduces
airborne precedence over stock buildings across the stock unit set. It does not
hold by construction — a click landing inside both hulls of a large aircraft and
a 2×2 building resolves to the building — and what would settle the stock-set
claim is a census of `FootprintX`/`FootprintZ` and model heights over the stock
definitions, not a further trace.

**10. Presentation boundary — Established consequence.** `frame.UnitView`
already publishes `FootX`/`FootZ` — the same authored `FootprintX`/`FootprintZ`
the compiler read — and the authored model the hull path already resolves
supplies the model total height by the §7 walk. The reduction is reproducible
from what is already committed, so no new pick record is needed for the
ordering. The producer's viewport/visibility admission and its list rank are
still not published ([R-REV-01 §6]), and the `TODO(question)` for the ordered
candidate record stands for anyone wanting a pixel-for-pixel replacement of
the producer.


### Supported inference

Picking should return a typed hit result with ownership/visibility metadata,
then let the active command mode choose whether a unit, feature, terrain
cell, minimap, or GUI control consumes the action.

#### Idle-latch divert, eligibility compare, and the active-state bit

The idle-latch branch is closed. The interface-type option (a runtime word the
options and settings loaders write from the `Interface Type` registry value,
clamped to 0/1) selects the two rows established above: value `0` uses the
action-shaped contextual table, while value `1` uses the relationship-coloured
`cursorselect`/`cursorred`/`cursorgrn` row with its feature-capability gate and
plain-normal fallback. The option word's writers are the option loaders, and the
gate is the option value itself. Armed rows do not read it.

The shared eligibility predicate's exact single-precision compare is **equal to
`0.0`**, not `1.0`: all eligibility sites compile to an exact compare against
the constant `0.0`. The compared field is the **remaining-build fraction** —
the word the construction step drives from `1.0` toward `0.0`
[05 R-WORK-01 §1] — so "eligible" means *construction complete*
([R-WGT-01 §9, §10], [08 R-TRIG-01 §3]); no routine of the order subsystem
writes the word, and there is no separate "empty current task" gate. The
other half of the predicate, status bit 5 (`0x20`), is the *selectable* bit
of [08 R-P0-04 "Runtime eligibility bit lifecycle"]; both have counterparts in
the build (`Remaining` and the eligibility status bit).

The visibility gate is the word-grid bit `1 << (localPlayer & 0x1F)` versus the
per-viewer byte grid selected by a visibility-mode bit. The named-entry index
table, the hotspot convention, the four-step shape chooser with its
lowest-index-wins reduction, the per-latch shape table, the cursor-to-ground
resolver, and the build-site validity/ghost cursor selection are established
above. The world overlays' palette entries are GUI semantic indices resolved
through the GUIPAL-to-display map [03 §4.3]; §9 lists the ones each overlay
uses.

### Unknown

- The committed pick record: the producer's list rank and its
  viewport/visibility admission result · §8 [R-REV-01 §6] · a design pass
  over the publication boundary. A pixel-for-pixel replacement of the
  *producer* is blocked on it; the hull test and the score reduction are not.
- Whether a stock aircraft always outscores the stock buildings it can fly over
  · §8 [R-REV-01 §9] · a census of `FootprintX`/`FootprintZ` and model heights
  over the stock definitions. The mechanism is Established; only the
  generalization over the stock set is a Supported inference.
- The selection rectangle's clip-left value for every visible/hidden-panel
  state · §8 [R-SEL-02A] · a focused mode/panel capture recording the surface
  descriptor at the selection draw.
- Feature-versus-unit pointer priority; features are absent from the unit
  hover list, and reclaim families resolve features separately at the pointer
  · §8 · static trace.
- Whether the `HOT UNITS` producer runs in the frame/presentation update
  rather than the sensor phase (Supported inference) · §8 [R-REV-01 §5] · a
  caller census of the producer from the frame-update and front-end refresh
  roots.
- Whether the two low status bits the producer tests before its terrain
  clamp are the movement-mode bits of doc 04 (Supported inference) · §8
  [R-REV-01 §5] · a writer census of that status word.


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
fields: the *selectable* status bit `0x20` (bit 5) of unit runtime flags; the
remaining-build fraction compared exactly equal to `0.0` — construction
complete; the post-capture grace counter equal to zero; and either no carrier
reference or a carrier whose runtime flags carry the *cargo-selectable* bit
`0x40000000` (bit 30) [R-WGT-01 §9][R-WGT-01 §10]. Selection membership is bit
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

**Mouse-button assignment is closed (for `Interface Type 0`; the `1` polarity is [R-CAM-01 §5]).** Every world action — single-unit picking, rectangle drag selection, building placement, and issuing every order including the contextual code 1 — is performed with the **left** mouse button. The **right** mouse button performs only deselection and cancellation: it cancels an armed order or build placement (returning the command latch to idle) or, when the latch is already idle, clears the current selection. The battle input pump routes left-button press and release through the single-click and drag-rectangle paths and the order dispatcher, while a right-button press left available by the earlier GUI service (§3) is routed exclusively to the cancellation path that returns the latch to idle and, when idle, clears selection; no battlefield right-button path queues an order. The cursor table shows the same polarity: every latch shape fires its order on left-click; the right-click column is empty or a transition back to the normal cursor [04 §3.4][07 §8].

Control groups store one group value per unit rather than membership bits in
several groups. Ctrl+digits (tokens `0xC5..0xCD`) assign groups with the
`CreateSquad` cue: the assignment scans local unit slots with a nonzero
catalog definition id; selected units (flag `0x10`) receive the requested
group value, and unselected units already carrying that group have it zeroed.

Digits `1..9` (tokens `0x31..0x39`) route between build-page selection and
group recall under an exact `SwitchAlt`/Alt gate ([R-CAM-01 §4]) — Alt is
held-key token `0xFB`, not Shift:

```
modeBit = SwitchAlt option bit (interface-flags bit 8)
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
a fixed chain, writing the parsed value only when the button's runtime gate
value is nonzero, else writing `1`. Every matched arm clears latch-flag bit
`0x08` and plays one of two cues.

**The chain and the cue column.** `MOVE` is the first test and there is
**no default**: a name matching none of the eleven returns "not handled",
writing no latch and playing no cue, so the button falls through to whatever
the panel handler tries next; there is no `PICKUP` compare, only `LOAD`.
Each test is a substring search (case-sensitive) over the
gadget's 16-byte name, so `UNLOAD` must precede `LOAD` — and does. The chain
in order, with the value written when the gate is nonzero and the cue:

| # | Name tested | Latch (gate ≠ 0) | Cue |
|---:|---|---:|---|
| 1 | `MOVE` | `2` | `immediateorders` |
| 2 | `STOP` | `1` | `immediateorders` |
| 3 | `ATTACK` | `3` | `immediateorders` |
| 4 | `BLAST` | `4` | `immediateorders` |
| 5 | `DEFEND` | `7` | `immediateorders` |
| 6 | `REPAIR` | `8` | `specialorders` |
| 7 | `PATROL` | `9` | `immediateorders` |
| 8 | `RECLAIM` | `0xC` | `specialorders` |
| 9 | `CAPTURE` | `0xD` | `specialorders` |
| 10 | `UNLOAD` | `5` | `specialorders` |
| 11 | `LOAD` | `6` | `immediateorders` |
| — | anything else | — | none; not handled |

Two consequences worth stating because they are easy to get wrong. The cue
belongs to the **arm**, not to the value: a button whose gate is zero writes
`1` instead of its own value and still plays its arm's cue, so the split
cannot be reimplemented as a function of the latch byte alone. And the split
does not follow the numbering — `UNLOAD` (5) is special while `LOAD` (6)
beside it is immediate. `STOP` is the one arm that ignores the gate: it
always writes `1`, builds its own order descriptor from the name and
transmits it immediately, then plays `immediateorders`. On stock content both
aliases resolve to the same sample (`immediateorders` and `specialorders` are
both authored as `button5` in `allsound.tdf`), so the distinction is
inaudible in the shipped install and audible only under replaced sound data.

Latch-flag bit `0x40` selects immediate-versus-special helptext, bit `0x20`
marks placement-valid pending, and bit `0x08` additionally gates placement
drawing; the dispatcher clears bit `0x08` while the Escape cancel path clears
bit `0x20`. This word is not the one the drag rectangle's colour reads: the
bit that picks the drag box's outer entry is bit 6 of the **pointer-flags**
byte — the site-valid bit of [R-CAM-01 §14] step 1 — a different byte whose
bit is also written `0x40`; nothing in the build-button handler touches the
helptext word. The latch writers, census-complete: the order-button
dispatcher arms `1..9`, `0xC`, `0xD`; the battle-HUD build-button handler arms
`0xE` (MOBILEBUILD) when the product's `BMcode` byte is zero, storing the
product id in the pending-build word and playing `addbuild` — the click arms
it, not the ghost show; idle resets write `1`. **Latch value `0xB` (TELEPORT)
has no writer anywhere in the image** — it is a consumer-only switch key
(order dispatch, shape table) — MOBILEBUILD is armed by the build-button
click, TELEPORT never by retail's own code.

**The stance buttons are a separate producer from the order latch.** The
`MOVEORD` and `FIREORD` gadgets do **not** arm the command latch above: they
are handled by a different battle-panel handler, which resolves the pressed
gadget by substring match against the chain `MOVEORD`, `FIREORD`, `STATUS`,
`ONOFF`, `CLOAK` and, for a stance gadget, cycles a **three-bit** field of
interface state and immediately transmits `STANDING_MOVEORDER` or
`STANDING_FIREORDER` to every eligible selected unit, then plays
`setmoveorders` or `setfireorders` (the on/off and cloak arms of the same
handler play the already-documented `specialorders`) and marks the panel dirty.
The two three-bit fields live on **two different** sixteen-bit engine-root
words — the fire stance in bits 12–14 of one, the move stance in bits 0–2 of
the next, the latter also carrying the cloak pair (3–4), the on/off pair (5–6)
and the command-capability enable bits (7–15, plus one bit of a third word —
the full map is [R-HUD-03 §13]). Values `0`, `1`
and `2` are the three stances, `3` means the selection disagrees, and `4` means
no selected unit accepts that stance, in which case the repaint grays the
gadget instead of writing its status word. Stock gadget names are
`ARM`/`COR` prefixed with quick keys `f` (fire) and `v` (move); the labels are
button artwork, not `text=` fields. The unit-side fields, their two-bit masks,
the acceptance flags, and the simulation consumers are
[04 §3.4a][R-STANCE-01 §1][R-STANCE-01 §2]. The `CLOAK` link of the same chain
runs the same four steps over the two-bit cloak pair, transmitting `CLOAK_ON`
only from a pair of `0` and `CLOAK_OFF` from every other value; it is spelled
out in [04 R-STANCE-01 §2].

**Established — the battle-panel handler's own chain, and where the two
dispatchers above sit inside it.** One callback receives
every click on the command window and tests the gadget's name against a
substring chain of its own before it reaches either producer described above:

1. `PREV` — raises a bit of one interface byte; **no cue**.
2. `NEXT` — raises a different interface bit; **no cue**.
3. `ORDERS` — raises a third bit and plays the **`ordersbutton`** cue.
4. `BUILD` — raises a fourth bit and plays the **`buildbutton`** cue.
5. A build-product gadget whose name resolves to a definition id whose
   `BMcode` byte is zero: arms MOBILEBUILD and plays `addbuild`
   (the branch already described under "Build placement is closed").
6. The stance / on-off / cloak handler above.
7. The order-button dispatcher above.
8. If neither of those consumed the click and the selected unit's flag bit 4
   is set, the counted factory-queue producer ([R-P0-11 §1]) with the signed
   count from the button identity and the live Shift query.

So the stance handler is consulted **before** the order-button dispatcher,
which is why `MOVEORD` and `FIREORD` never reach the latter's `MOVE` test
even though their names contain it. `ordersbutton` and `buildbutton` are
ordinary authored aliases; on stock content both resolve to `butnmbl1`.

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
enabled/disabled/mixed paths across the selected set — the fold that produces
it is [R-HUD-03 §13].

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

**Established — generated slot patch and build.** For each matched product,
the selected gadget index is the authored `BUTTON` byte plus four. The patch
clears only the low grey bit, sets `commonattribs` to four, copies the product
name into the gadget name, and sets the low per-record GAF flag bit. It leaves
the separate art-name field alone. After all matching product patches, the
ordinary window builder runs over the resulting records. **Established —
activation identity:** the battle-panel callback resolves this installed gadget
name, as in step 5 of its chain above. `BUTTON+4` determines where a generated
product is installed; it does not turn a click into an index into the builder's
CANBUILD sequence or the appended download list. An ordinary physical page
retains its authored gadget names wherever no generated patch replaces them.
The odd-GAF prepass therefore resolves each product's named gadget resource and retains both hits
and missing entries before any painter uses the record [R-WGT-01 §3].


**Build placement is closed, and the branch into it is on the product.** The GUI
build-button handler resolves the gadget to a definition id and, when that id is
nonzero **and the product's authored `BMcode` byte is zero**, arms the
MOBILEBUILD latch (`0xE`), stores the id in a pending-build word and plays the
`addbuild` cue. Nothing is queued; the click that follows on the world is what
issues the order. A product whose BMcode is nonzero never reaches that branch and
falls through to the counted factory-queue producer of the order-producer block
below ([R-P0-11 §1]) — a signed counted add/subtract that never purges.

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
per-order-kind mask byte values come from the registration tables
([R-P0-11 §3] has the full census): MOBILEBUILD marker+dashes+rings (`0x13`),
VTOL_MOBILEBUILD marker+dashes (`0x03`), MOVE and PATROL dashes+rings
(`0x12`), QMOVE/QPATROL dashes (`0x02`), and every attack-family kind
icon-only (`0x08`) — the circle bit is set by no stock record. No stock
helper draws a plain connecting segment; the dash chain is the only
connector.

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
health bars (whose visibility to other players is the `hidedamage` gate of
[04 R-SPEC-01 §6]), unit name, and queued-order cursor indicators.

#### UI order producers [R-P0-11]

Nanolathe impact: battle-HUD product clicks are counted ±1/±5 producers that
never purge; the queue-count labels, the five-bit overlay walker, Shift-latch
persistence and the COB signal-mask seed 1 follow.

#### Factory product click producer [R-P0-11 §1]

**Established.** The battle-HUD click handler resolves the clicked build-page
toy's name to a product definition id, loads the single-selected unit from the
runtime pool, and branches on the product's BMcode byte exactly as the
build-placement paragraph above records: zero arms MOBILEBUILD placement;
nonzero (every factory product) falls through to a signed counted add/subtract
against the builder's production queue. The count is derived from the click's
button identity and the live held-key query for token `0xF9` (Shift): +1 for a
plain left-click, +5 for Shift+left-click, -1 for a plain right-click, -5 for
Shift+right-click. The path is a counted producer that never purges — the
Shift axis scales the count, not the queue mode.

The counted-add routine plays the `addbuild`/`subbuild` cue (below), routes
the stockpile buttons `MAKENUKE`/`MAKEANTI` to the
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

**The cue's gate.** The routine's first statement is the local-player
test — the selected builder's owning-player byte against the local-player
byte — and inside it a **single signed test on the count**: one or more plays
`addbuild`, anything else plays `subbuild`. There is no third arm and no
silent case; the cue runs before the descriptor routing and before the queue
coalesce, so a click that ends up changing nothing is still audible. Zero is
not reachable from a click (the count is always ±1 or ±5) but it is on the
`subbuild` side of the test.

#### Queue-count display [R-P0-11 §2]

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

**Established — which flag field, and what each format prints.**

*The flag field is the toy's authored `commonattribs` byte* — the `[COMMON]`
key of that name, which the loader keeps in the runtime toy record immediately
after `active`, in the same order the authored block lists them
`[02 §6 "COMMON"]`.
The writer walks the open page window's toys from index 1, skips anything whose
kind is not the button kind, and tests `commonattribs & 4` first, then
`commonattribs & 8`. An asset census over the reference install's 375 `guis/`
files settles what authors them: **all 480 build-product buttons author
`commonattribs = 4`** (`ARMAAP1:ARMACA`, `ARMVP1:ARMFAV`, `ARMCOM1:ARMSOLAR`, …,
alongside `attribs = 32` and an **empty** `text=`), and **the eight stockpile
buttons author 8** (`ARMAMD1:ARMMAKEANTI`, `ARMEMP1:EMPMAKENUKE`,
`Armscab1:ARMMAKEANTI`, …). Every other side-panel button — the command buttons,
`PREV`/`NEXT` — authors 0. A count therefore never appears on an authored
caption, because the toys that carry a count author no caption; an
implementation that gates the counter on the toy having authored text writes no
count at all.

*Bit `0x04` prints one number, not two.* The bit-4 branch resolves the toy's
**name** to a unit definition id, and prints nothing when the name resolves to
zero. The count query it then calls walks the builder's primary list and then
its secondary list, admitting nodes whose flag word carries the
counted-production bit and whose id field equals the requested id, and returns
**one running total across both lists**. That single total is formatted `+%d`, so
a queue of five in the primary list and two in the secondary shows `+7`, not
`5 +2`. A zero total clears the slot. A format that prints the two lists as
separate numbers is not a retail shape.

*Bit `0x08`'s two numbers are two different quantities.* The bit-8 branch prints
`%d` of a **byte on the builder unit** — the stockpile count — clearing the slot
first and printing nothing when that byte is zero, and then appends ` +%d` of
the same count query run with id **0**, which is the pending build-weapon queue
(the `BUILDWEAPON` nodes the stockpile buttons enqueue through, §1). So the
stockpile button reads "held +pending". The second number is not the secondary
order list.

*Whose queues.* The writer is handed the single selected builder the click
handler resolved (§1), so only that unit's two lists are counted.

#### Order-queue overlay helpers [R-P0-11 §3]

**Established, gate and ordering.** The world composer draws the selected
units' queue quads after the unit band and again after the projectile/impact
flash; that quad pass is gated on bit 2 of the interface-options word (toggled
from the options handlers, seeded at startup). The Shift-gated overlay walker
then runs once per frame over the local player's unit slice — after the effect
strips, before the drag-rectangle and GUI frames — consistent with the overlay
paragraph above. The walker passes a caller mask to a per-unit dispatcher:
mask `0x1F` for four privileged units and marker-only `1` for the remaining
local units, the latter only when some builder context exists. The four
privileged units, and the exact Shift gate, are enumerated in "Who gets the
full mask, and is the marker Shift-only" below. Per order node the
effective mask is `table[kind].drawMask & callerMask`; the five bits
dispatch:

| Bit | Helper |
|---:|---|
| `1` | Build-site marker — the eight lines of the marker paragraph above. Age is the global tick minus the order's birth tick, clamped `0..10`, driving the `(w·age)/10` and `(h·age)/10` offsets; the color pair is that paragraph's 3/10 vs 1/9, outer/inner. |
| `2` | Travelling-dash chain between the order's previous position and its anchor (target tracking and the cached-position flag live in the anchor getter). The distance is a float square root truncated toward zero; segments under one world unit draw nothing. The artwork is **not procedural**: sprites come from a GAF sprite chain (a frame table with a ticks-per-frame field), blitted through the ordinary GAF byte-copy blitter, with frame index `(age/tpf) % nFrames` where age is the global tick minus the order's birth tick — the cadence is driven by the order's **creation tick**, not the global phase. The chain's phase offset starts at `((age mod 30) * 3 << 20) / 30` (integer division) and advances `3 << 20` per sprite until the segment end; the 30-tick wrap of the phase is what makes the dashes march. Each sprite's position interpolates the segment linearly with a 16.16 fraction `(phase << 16) / distance`, so sprites sit three cells apart. |
| `4` | A sixteen-segment circle at the order point. The radius is the truncated product `src * 0.9` where `src` is the target unit definition's radius field for unit targets and a constant 32 otherwise; chords are drawn through the line primitive in color-map entry 12. |
| `8` | Queued-order icon: an animated cursor-GAF frame at the order point, index `tick/(tpf*2) % nFrames` into the cursor handle array at the descriptor's icon byte, drawn with the half-height shear; in battle the attack icons (1/2) first draw weapon AOE/coverage/attack-length rings using color-map entries 12/4 on the low tick bit, then use the ordinary GAF icon blit without that GUI colour argument. |
| `16` | Once per unit: labeled range rings — sight/radar/sonar/jammer/build-distance/maneuver/kamikaze radii read straight from the unit definition's fields (the kamikaze radius is the trigger distance of [04 R-SPEC-01 §1]), weapon ranges from the weapon definition's authored range field — in color-map entries 15/12/14 per branch; the compact kamikaze pulse uses half the resolved explosion area of effect, as detailed below. |

**Established — `ShowRanges` selection.** The command toggles a process-only
word, initially zero, without reading an argument or writing settings. It does
not bypass the Shift gate. The bit-16 helper runs once per unit, at the first
order whose effective descriptor mask contains that bit, after that node's
other helpers. Marker-only fallback units never reach it. The helper reads
unit-definition radii regardless of visibility.

With the switch off, a cloaked unit draws its nonzero signed-16 minimum-cloak
radius in GUI colour 15. A kamikaze unit with a resolved explosion definition
then draws a GUI-12 pulse: `h = uint16(areaOfEffect) >> 1`, with radius
`min(h, max(8, ((tick % 60) * h * 2) / 60))`. It next draws unsigned-16
kamikaze distance for a mover (`BMCode == 1`), or signed-16 sight distance
otherwise, in GUI colour 12. A resolved explosion with zero area still reaches
this second ring.

With the switch on, nonzero unit fields draw in this order in GUI colour 14:
`mincloak`, `sight`, `radar`, `sonar`, `radarjam`, `sonarjam`, `build distance`,
`maneuver`, `kamikazedistance`. The first six radii are signed-16; the last
three are unsigned-16. Label ordinals count preceding nonzero fields. Authored
weapon ranges then use GUI colour 12 on even ticks and 4 on odd ticks, with
labels `weapon1 range` through `weapon3 range` and fixed ordinals 0 through 2.
Slot three's range tests slot one's enabled bit; the other slots test their
own. Definition activity is not an additional gate.

With the switch on, each attack icon (descriptor icon 1 or 2) first draws each
enabled weapon slot's nonzero unsigned-16 area of effect and authored coverage,
then the unit's nonzero unsigned-16 attack length. The labels are
`weapon N: area of effect`, `weapon N: coverage` (zero-based slot N), and
`attack length`; their ordinals are 0, 1, and 2 respectively. The line colour
uses the same even/odd tick pair.

These range helpers use terrain-following chords, not the bit-4 target circle.
For radius `r`, the chord count is
`trunc(r * 6.28318530717958 * 0.125)` and the integer angle step is
`65536 / count`; the inclusive loop emits `count + 1` chords from angle zero.
Each endpoint uses the greater of centre height and sampled terrain height,
then the ordinary projection and clipped integer line drawer. A label uses the
second endpoint of chord `ordinal * 3`, falling back to the final endpoint
when both saved screen coordinates are zero. Its pen is four pixels below
that point, using the active battle FNT in GUI colour 15. The owning unit
behaviour detail is [04 R-SPEC-01].

Descriptor-table facts: the table is runtime-built with base and end pointers
installed at startup; the records are fixed-stride with stride 25, proven by
the binary-search divisor in the kind lookup. Each record
carries the order-kind id, the draw-mask word, the icon byte, the
order-flags
word (copied onto each node the order issues), and a name pointer. Three
static record tables feed the registry — the ground-state table (22 records:
Move_Ground, Follow_Ground, Suppress, Attack_Chase, Attack_Kamikaze,
AttackSpecial, Park, Patrol, Ground_Pickup, Ground_Unload, Teleport,
MobileBuild, HelpBuild, RepairPatrol, RepairUnit, Capture, Resurrect, Reclaim,
ReclaimUnit, RepairUnitNoMove, Standby, Standby_Mine), the ground-special
table (23 records: Stop, Attack_NoMove, Activate/Deactivate/Cloak pair,
Standing_MoveOrder/Standing_FireOrder, BuildingBuild, BuildWeapon,
SelfDestruct pair, Paralyze, GetBuilt, BeCarried, MakeSelectable, Wait,
WaitForAttack, AttackUType, Guard_NoMove, SelfRepair, QMove, QPatrol), and the
VTOL table (22 records: VTOL_Standby through VTOL_LandIfCan) — and their
draw-mask words are listed in the helper table above. The icon bytes equal the
cursor index table's values, an independent confirmation of the table in §8.
The
always-on selected-unit connecting quad uses a single GUI-context color-map
color; there is no owned-vs-other color pair in the overlay itself —
differentiation is by mask width (full `0x1F` vs marker-only `1`), not color.

##### Who gets the full mask, and is the marker Shift-only

**Established.** The build-site marker is drawn **only while Shift is held**.
There is no non-Shift path to it. The reachability is a single chain with no
branch points: the battle world composer is the one and only caller of the
overlay walker, the walker is the one and only caller of the five-bit per-node
dispatcher, and the dispatcher is the one and only caller of the build-site
marker helper. None of those entry points is ever taken as data anywhere in the
image, so there is no indirect route either (see the vestigial descriptor field
noted at the end of this subsection for the one apparent exception, which is
not a call). The composer's guard is a **live asynchronous key-state query for
the Shift key**, asked afresh every battle frame at draw time — the same
real-time source §4 contrasts with the click message's modifier word, not a
latched or remembered modifier. With Shift up, retail therefore draws **nothing
at all** at a queued build site: no rectangle, no sweep, no dash chain.

The evidence, whole-image:

* The call census is whole-image, not neighbourhood: the walker, the dispatcher
  and the marker helper each appear as a call target exactly once, and a byte
  scan of the entire file for their entry values finds only the two vestigial
  descriptor fields described at the end of this subsection.
* The vestigial field really is never read. Every load through the runtime
  descriptor table's base pointer was enumerated and classified: the reads are
  the state handler, the draw-mask word, the icon byte, the order-flags word
  and the name pointer. The spare helper slot is read by nothing.
* The guard gates the **whole** walker, not one branch inside it: the
  conditional the key query feeds skips exactly the walker call and resumes at
  the composer's next pass.
* The key really is Shift. The held-key query is a small dispatch over eight
  modifier/arrow tokens; the token the composer passes selects the arm that asks
  the operating system for virtual key `0x10`, which is Shift. The other arms
  cover Control, Alt, Space and the four arrows.

**Unknown — the play observation.** A player reports seeing the sweeping
build-site lines at a queued site with Shift *up*. Nothing in the overlay can
produce that, and the two things the composer does draw without Shift are drawn
elsewhere: the armed placement ghost (at the **cursor**, only while a build
latch is armed, two nested outlines in one colour, no sweep) and the always-on
selected-unit footprint quad (at the **unit**, gated on the interface-options
word). Neither projects a footprint at an order's position and neither sweeps.
The likeliest reconciliation is that the observation was of the overlay under
Shift, and that it looked wrong rather than absent: for a MOBILEBUILD order the
overlay draws three helpers, and the two beside the marker — the travelling-dash
chain and the range rings — were both missing from Nanolathe when the comparison
was made. Settling it needs a retail session with the key state observed rather
than recalled. `TODO(question): does any retail path draw the sweeping
build-site lines with Shift up? A retail session with the Shift key state
observed, not recalled, would settle it [07 R-P0-11 §3].` Recorded here rather
than changed in the gate, because inventing a second trigger would be inventing
behavior.

There is no "acted-on" recency mark — a unit id plus a timestamp, or a bit on
the unit — written when an order is issued: issuing an order writes nothing
that this overlay consults.

The walker admits a local unit only when it is alive and not death-marked, then
picks its caller mask by the first of these that matches:

1. the unit the follow camera is currently tracking (the tracked-unit slot of
   the camera block, cleared when the follow is cancelled, [R-CAM-01 §12]);
2. the unit whose command page is currently open — the command-page subject id,
   written when a page opens for a unit and zeroed both by the page-close
   routine ([R-HUD-04 §3]) and by the select-all sweep;
3. the hovered unit id — the same hover word the footer's first hover source
   reads ([R-HUD-03 §1]);
4. any unit carrying the selected flag.

All four get the full mask `0x1F`. Every other local unit gets marker-only `1`,
and that fallback runs **only when a builder context exists**, defined
precisely as: at least one of (1), (2), (3) resolves to a live unit whose
definition carries the builder capability. When none of them does, the
remaining local units are skipped entirely and only the four privileged units
draw anything. Note the asymmetry: the builder-context test gates the
marker-only fallback alone — the four privileged units draw their queues
regardless of whether anything is a builder.

Two details. First, **every** selected unit gets the full mask, not only a
lone one; the command-page subject (2) is the separate notion that
is single-unit-ish, and it is an id the page machinery owns, not a count of the
selection. Second, the walker passes the dispatcher one further byte that
distinguishes the selected-unit branch from the other three; no helper reads
it, so it changes nothing that is drawn. In particular the marker's colour pair
is still chosen from the **order node's owning unit's** selected flag (3/10 when
set, 1/9 when clear), exactly as the marker paragraph in §9 states, and never
from which branch admitted the unit.

What *is* drawn without Shift, and is easy to mistake for this marker, is the
always-on selected-unit footprint quad: gated on bit 2 of the interface-options
word, built from the unit's own root-piece bounds, and drawn **at the unit** —
never at an order's position. Nothing else in the world composer projects a
build footprint at an order position.

Vestigial descriptor field: each order-descriptor record carries, besides the
draw-mask word, a spare helper pointer that always holds the helper for that
record's **lowest set draw-mask bit** (so the two MOBILEBUILD-family records
hold the marker helper). Nothing reads that field at runtime — every runtime
read of a descriptor record uses the state handler, the draw mask, the icon
byte, the order-flags word or the name pointer. It is dead metadata, not a
second dispatch table, and it is the only place the marker helper is named
outside the dispatcher.

##### The dash chain's artwork, and the anchor getter that doubles as the icon

**Established.**

*The dash chain's sprite is the authored GAF entry `pathicon`.* It is resolved
by name at start-up by the same loader and into the same handle array as the
twenty-one named cursors — `pathicon` is loaded in that run of names, between
`cursorhourglass` and `cursorrevive` — so it comes from the cursor GAF root
(`anims/cursors.gaf`, §8) and not from a separate animation file. In the stock
install the entry has **one** frame with a frame duration of 3, so a stock
queue line is one small sprite repeated along the segment and the chain's
apparent motion comes entirely from the phase seed, not from frame cycling.
The helper reads the entry's frame count and the **first frame reference's
duration field** as its ticks-per-frame — it does not consult per-frame
durations — and blits through the ordinary GAF frame blitter, which subtracts
the frame's authored placement offsets, so those offsets are the sprite's
hotspot exactly as they are for the software cursor [fmt gaf "Placement
offsets"]. The frame index the helper table gives, `(age / tpf) mod nFrames`,
is the index of the chain's **first** sprite; each later sprite along the same
segment takes the next frame index, wrapping at the frame count. With a
one-frame entry that increment is invisible, which is why a stock chain reads
as a static sprite marching rather than an animating one.

*The bit-8 helper is also the anchor getter, and the bit-2 helper calls it.*
The helper the table lists under bit 8 does two jobs: it resolves the order's
anchor — the target unit's position when the node carries a target, the node's
own stored position otherwise, with the cached-position flag maintained there —
into the running point the dispatcher threads through the helpers, and *then*,
only if the order descriptor's icon byte is nonzero, draws the queued-order
icon at it. The travelling-dash helper's first act is to call it with its own
arguments, because it needs the segment's far end. The consequence matters for
Nanolathe: an order kind whose mask sets bit 2 but not bit 8 still runs the
icon helper, and whether an icon appears is decided by the descriptor's icon
byte alone. For MOBILEBUILD and VTOL_MOBILEBUILD that byte is **0**, so a
queued build site draws no per-order icon — the sprites a player sees strung
between queued build sites are the `pathicon` dash chain, not order icons.

*The per-kind census.* The two static tables, each record as draw mask /
icon byte. Ground-state table, in table order — Standby `0x10`/15, Standby_Mine `0x10`/15, Move_Ground `0x12`/14,
Follow_Ground `0x12`/5, Suppress `0x08`/1, Attack_Chase `0x08`/1,
Attack_Kamikaze `0x08`/1, AttackSpecial `0x08`/1, Park `0x00`/14,
Patrol `0x12`/7, Ground_Pickup `0x08`/12, Ground_Unload `0x08`/13,
Teleport `0x08`/9, MobileBuild `0x13`/0, HelpBuild `0x18`/6,
RepairPatrol `0x12`/7, RepairUnit `0x12`/6, Capture `0x08`/4,
Resurrect `0x12`/11, Reclaim `0x12`/11, ReclaimUnit `0x12`/11,
RepairUnitNoMove `0x18`/6. VTOL table — VTOL_Standby `0x00`/15,
VTOL_Move `0x02`/14, VTOL_Landing `0x08`/14, VTOL_Pickup `0x08`/8,
VTOL_Unload `0x08`/9, VTOL_Follow `0x02`/5, VTOL_Patrol `0x02`/7,
AirStrike `0x08`/2, AirToAir `0x08`/1, AirToGround `0x08`/1,
AirToGroundHover `0x08`/1, VTOL_MobileBuild `0x03`/0, VTOL_HelpBuild `0x08`/6,
VTOL_RepairPatrol `0x02`/7, VTOL_RepairUnit `0x02`/6, VTOL_Reclaim `0x02`/11,
VTOL_ReclaimUnit `0x02`/11, and VTOL_Evade, VTOL_SeekAttack, VTOL_SeekGuard,
VTOL_GetRepaired, VTOL_LandIfCan all `0x00`/19. Notes: the `0x18` kinds
(HelpBuild, RepairUnitNoMove) draw an icon plus range rings and no connector;
a `0x00` mask draws nothing at all however privileged the unit; the circle bit
is still set by no stock record; and no stock ground record and no stock VTOL
record uses icon byte 0 except the two MOBILEBUILD-family records. The
ground-special table (Stop, QMove, QPatrol and the rest) was read in an
earlier pass only; its `0x02` values for QMOVE/QPATROL are the ones §9 gives.

*The marker arithmetic, from the helper itself.* It matches the §9 marker
paragraph exactly: the
rectangle's two corners are the definition's own corner-offset fields added to
the order node's stored position; **both** corners take the same height, which
is what flattens the marker onto the ground plane; the age is the global tick
minus the node's birth tick, clamped into `0..10`; both offsets divide by ten
truncating toward zero; and the eight lines are the four sweep positions drawn
once one pixel out in the outer colour and once flush in the inner colour, with
the colour pair chosen from the owning unit's selected flag. A node whose build
definition id is zero draws nothing.

#### Latch persistence under Shift [R-P0-11 §4]

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

#### Engine-started COB thread signal mask [R-P0-11 §5]

**Established.** The COB thread-slot allocator initializes each new
engine-started thread slot with a fresh state word, code pointer, return
sentinel (−1), parameter cell (0), and a **signal mask of 1** — engine-started
threads begin accepting signal group 1, not group 0. (An engine-side finding
whose natural home is the COB VM contract of doc 04; recorded here as well.)

#### Queued world orders: a repeat click at an already-queued point removes it [R-P0-11 §6]

**Established.** Every world order the interface issues goes
through one producer that takes the order's canonical kind, the click's
**queue flag** (the Shift bit of the click record's key-state word, [R-P0-11
§4]), the acting unit, an optional target handle, and an optional goal point.
Its first act is a **duplicate test that runs only when the queue flag is
set**:

* Walk the acting unit's **primary** order list from the front.
* A node matches when *all* of these hold:
  * its order kind equals the kind being issued;
  * the issued target handle is absent (zero), or equals the node's target;
  * the issued goal point is absent, or lies within **one map cell on each
    of the X and Z axes** of the node's goal — the test is
    `|issued − queued| ≤ 0x100000` in 16.16 world units on X and on Z
    independently, inclusive at the boundary. Y (the site height) is not
    compared.
* On the **first** match the producer unlinks that node from whichever
  segment holds it (the rear segment when the node carries the rear-segment
  bit, the primary segment otherwise), marks it tombstoned unless it was the
  list head, frees it, and **returns without issuing anything**.
* With no match — and on every non-queued (Shift-up) click, which does not
  run the test at all — control falls through to the ordinary insertion,
  which purges the existing unprotected orders first when the queue flag is
  clear and appends when it is set.

So a Shift-click that repeats an already-queued order at (or within one cell
of) the same point is a **toggle that removes exactly one queued order**, not
a second enqueue: one node per repeat click, front-most match first, and the
click produces no order of its own. Two consequences are worth stating
because they are easy to get wrong:

* **The product identity is not part of the match.** For the build-placement
  producer the product definition id travels in a separate argument that the
  duplicate test never reads. Shift-clicking a site where *any* queued
  building of the same order kind (`MOBILEBUILD`, or `VTOL_MOBILEBUILD` for a
  flying builder — the two are distinct kinds and do not match each other)
  already stands removes that queued building, whatever it was going to be.
* **The tolerance is a whole cell, not an exact site match.** Queued sites
  one cell apart are within tolerance of each other, so the front-most of the
  two is the one that goes.

The build-placement click ("*The click*" above) reaches this producer once
per selected builder, so one repeat click clears the queued site from every
selected builder that has one there. The queue-count label writer of
[R-P0-11 §2] runs after cancels as well as enqueues, and the Shift-gated
overlay walker of [R-P0-11 §3] redraws from the live queues every frame, so
the removed site stops being drawn on the next frame with no separate
invalidation.

Nanolathe impact: the presentation may not implement this as "coalesce a
repeat build click into the tail node" — that adds a second building where
retail removes the first. The removal belongs at the authoritative
order-insertion boundary, before the queued build is constructed, and must
search the primary queue front-to-back; the removal frees the whole node —
there is no count decrement on this path, unlike the factory producer's
negative-count subtraction of [R-P0-11 §1]. The overlay colour map is the
boot-installed 256-byte logical-to-physical table, and every helper's entry
is pinned — marker 1/9 and 3/10, circle 12, rings 12/14/15, attack-ring
alternation 12/4; the dash helper reads no map entry (it blits GAF frame
bytes directly).

### The selection-aggregate command-state fold and its two sentinel shapes [R-HUD-03 §13]

The command buttons of [R-HUD-03 §6] do not read the selection at paint time.
A single **aggregate command-state refresh** folds the selection into three
adjacent sixteen-bit interface words, and §6's repaint only reads those words.
This section is that fold; the greying *conditions* are §6's. The three-bit
stance half of the same fold is [04 R-STANCE-01 §1].

**Established — when the fold runs, and when it is skipped.** The refresh
first raises the battle panel's "refresh in progress" flag. It then tests the
**selected-builder single-select id** (§9): when that id is nonzero the whole
fold is skipped and the aggregate words keep whatever they already hold — the
refresh only resolves that one unit for §6's build-page step, and when that
unit no longer carries a definition index it clears the id and returns having
touched nothing else. Only when the id is zero does the refresh walk the local
player's inclusive unit range in ascending pool order at the fixed unit-record
stride — §9's walk — folding every unit with a nonzero definition index whose
status word carries selection bit `0x10`. The aggregate words are then written
unconditionally, so an empty or all-ineligible selection deposits every
field's starting value.

**Established — four folded state fields, in two shapes.** Each field is
gated on a per-definition capability; a unit without the gate does not
participate in that field at all.

| Panel field | Aggregate bits | Definition gate | Per-unit value folded | Starts at | Disagreement |
|---|---|---|---|---|---|
| fire stance | first word, 12–14 | `firestandorders` (capability bit 1) | status word bits 20–21 | `4` | `3` |
| move stance | second word, 0–2 | `mobilestandorders` (capability bit 0) | status word bits 18–19 | `4` | `3` |
| cloak pair | second word, 3–4 | can-cloak, i.e. `cloakcost > 0` (capability bit 13) [05 R-PROD-01 §7] | the cloak-requested status bit [05 R-ECO-01 §9] | `3` | `2` |
| on/off pair | second word, 5–6 | `onoffable` (capability bit 2) | the state byte's activated bit (bit 0) [04 R-SPEC-01 §12] | `3` | `2` |

All four folds have one shape: the accumulator starts at a value the per-unit
field cannot hold; the first gated unit **replaces** that value with its own;
a later gated unit moves the accumulator to the disagreement value one below
it. The starting value therefore survives only a walk that folded nothing, and
it is the **not-applicable** sentinel — "no selected unit carries this
capability". The three-bit fields need two spare values because their unit
field holds `0..3`; the two-bit pairs need two spare values because their unit
field holds `0..1`.

**Established — the two sentinel shapes.** `3` is the value each two-bit
pair starts from, so it means **not applicable** — no selected unit is
cloak-capable, or none is `onoffable` — and `2` is the mixed value; `3` greys
both gadgets (§6). The two shapes are parallel, not identical: the stance
fields grey at `4` (not applicable) and show the generic plate at `3`
(mixed); the two-bit pairs grey at `3` (not applicable) and show the generic
plate at `2` (mixed).

**Established (asset census) — the artwork confirms which value is "mixed".**
The `anims/commongui.gaf` entries `ARMCLOAK`/`CORCLOAK` and
`ARMONOFF`/`CORONOFF` carry **five** frames each (the stance entries of
[04 R-STANCE-01 §8] carry six), rendered through the repo's own GAF and
palette decoders:

| Frame | `ARMCLOAK` / `CORCLOAK` | `ARMONOFF` / `CORONOFF` |
|---:|---|---|
| 0 | `VISIBLE` | `OFF` |
| 1 | `CLOAKED` | `ON` |
| 2 | `CLOAK ORDERS` | `OFF/ON ORDERS` |
| 3, 4 | unlabelled plate | unlabelled plate |

Frame `2` is the generic "orders" plate — exactly the role frame `3` plays on
the six-frame stance entries — and it is the frame the panel stages when the
selection disagrees. A pair value of `3` never selects a labelled frame,
because §6's repaint greys the gadget instead of writing its stage.

**Established — the on/off and cloak folds are not symmetric.** The on/off
fold compares: a later `onoffable` unit whose activated bit *equals* the
accumulator leaves it alone, and only a differing one takes the pair to `2`.
The cloak fold does not compare at all — once the pair is off its `3`
sentinel, **any** second cloak-capable unit sets it to `2`, agreeing or not.
The difference is visible in the panel: two units that are both on (or both
off) still show `ON` (or `OFF`), while two units that are both cloaked still
show the generic `CLOAK ORDERS` plate. A reimplementation must reproduce the
asymmetry. Whether it is deliberate is **Unknown**, and it does not need
deciding to clone the behavior: no observation can separate "intended" from "a
missing compare", so it is recorded here rather than carried as an open item.

**Established — the capability folds are a disjunction, and the bits are
named.** With the aggregate word each is deposited in:

| Definition key (capability word bit) | Aggregate bit | Command button |
|---|---|---|
| `canmove` (7) | second word, bit 7 | `MOVE` |
| `canstop` (3) | second word, bit 8 | `STOP` |
| `canattack` (4) | second word, bit 9 | `ATTACK` |
| `canguard` (5) | second word, bit 10 | `DEFEND` |
| `canpatrol` (6) | second word, bit 11 | `PATROL` |
| `canload` (8) | second word, bit 12 | `LOAD` / `UNLOAD`, and `BLAST` visibility |
| `canreclamate` (10) | second word, bit 13 | `RECLAIM` |
| `cancapture` (12) | second word, bit 14 | `CAPTURE` |
| `canreclamate` via the parser's derived copy (9) | second word, bit 15 | `REPAIR` |
| `candgun` (14) | third word, bit 0 | `BLAST` |

Each fold is a plain OR: the refresh **sets** the aggregate bit for any
selected unit whose definition carries the key, and never clears one during
the walk. §6's repaint then greys the button when its aggregate bit is clear.
So a button is greyed only when **no** selected unit can perform the command;
one builder plus one tank offers both `RECLAIM` and `MOVE` — a disjunction,
not an AND across the selection. The key-by-key record is doc 02's
([02 "Unit record"]).

**Established — `REPAIR` reads the derived bit-9 copy of `canreclamate`, and
bit-9 readers do exist.** The unit-definition parser stores `canreclamate` in
capability bit 10 and, in the same instruction sequence that stores
`canresurrect`, writes bit 9 as a copy of bit 10 ([02 R-KEYS-01 §1]). The
`REPAIR` aggregate above reads that derived copy; the `RECLAIM` aggregate
reads the original. The bit-9 reader census is not empty and not confined to
this panel — the command
resolver's patrol case tests bit 9 alone to choose the repair patrol over the
plain patrol ([04 §3.4], code 9), the `VTOL_RepairPatrol` handler tests it as
its own entry gate, and the repair-target predicate tests it on the repairer.
Because the bit is a verbatim copy of bit 10, none of these can behave
differently from testing `canreclamate`: `REPAIR` and `RECLAIM` are enabled
and greyed together for every authored definition.

### Supported inference

The command system should retain a canonical semantic order object from input
through local execution, cursor validity, HUD help, and network serialization.
This avoids divergent behavior between mouse clicks, keyboard shortcuts, AI
orders, and multiplayer packets.

### Unknown

- Per-window census of the authored gadget association ids that resolve each
  widget's callback target · §9, doc 02 §6 · static trace.
- Whether any retail path draws the sweeping build-site lines with Shift up
  (a play observation contradicts the traced Shift gate) · §9 [R-P0-11 §3] ·
  a retail session with the Shift key state observed, not recalled.


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

##### In-flight camera move: the weapon `holdtime` hold

**Established fact — the "in-flight camera move" is the weapon `holdtime`
hold, and the only thing that starts one is a projectile retirement.** No
camera, input, or minimap path ever stores a nonzero remaining count or writes
the 16.16 anchor; the camera-jump family (bookmark recall and the other
absolute repositions) only ever *clears* the count, and clears the
followed-projectile and tracked-object references with it, so a camera jump
cancels a hold in progress. The five writers that start one are the
projectile-retirement paths of `[06 §7.3]`: the direct-expiry retirement, the
two central impact retirements, the collision retirement, and the
shooter-death anchor sweep. Each of them first compares the record being
retired against the followed-projectile reference; only when they are the same
record does it copy that record's current position triple (X, Y, Z, each
16.16) into the anchor, store the retiring weapon definition's `holdtime` —
already converted to whole ticks by the catalog's seconds-times-30 truncation
`[02 "Weapon record"]` — into the remaining count as a 16-bit word, and clear
the followed-projectile reference. Retiring any other projectile leaves all of
this untouched. `holdtime` has no other reader anywhere in the image
`[fmt tdf]`.

**Established fact — how the camera comes to follow a projectile.** The
followed-projectile reference is set in exactly one place: the projectile
initializer, which sets it to the record it is initializing when the firing
unit is the camera's current tracked object **and** that unit's status word
carries the building-class bit `[04 §2]`. Tracking a mobile unit therefore
never hands the camera off to its shots; tracking a structure — a silo, a
long-range battery — does, for every projectile that structure spawns, and the
hold above is what the camera does when that projectile dies. The projectile
pool's compaction pass rewrites the reference when the followed record is
relocated, so the handoff survives compaction `[06 §5]`.

**Established fact — the hold branch's selection and decrement.** Phase 10
opens by testing the remaining count against zero, and the two arms are
exclusive:

* **Nonzero** — the count is decremented by one as a 16-bit store, and the
  target point for this pass is the frozen anchor. The followed-projectile and
  tracked-object references are not consulted at all while a hold is running.
* **Zero** — the followed projectile is used when its reference is non-null
  (the point is that record's own position triple); otherwise the tracked
  object is used when its reference is non-null **and** its alive bit is set
  (the point is that unit's position triple); a tracked object whose alive bit
  is clear instead clears the remaining count, the tracked reference and the
  followed-projectile reference, and no point is selected this pass.

Because the decrement happens on the same pass that uses the anchor, an
authored `holdtime` of `N` freezes the camera on the retiring projectile's last
point for exactly `N` phase-10 passes — `N` simulation ticks, since phase 10
runs once per runnable sub-tick — and the pass after that resumes ordinary
following. The count is signed and only ever compared for equality with zero,
so a negatively authored `holdtime`, which the catalog's truncate-then-store
path turns into a negative 16-bit word `[06 §7.3]`, counts *away* from zero and
wraps, holding for 65,536 passes less its distance to zero rather than
releasing immediately. No stock weapon authors a negative `holdtime`.

**Established fact — the desired origin from the selected point.** Whatever the
source of the point, the desired origin is formed identically:

```
desiredX = sext16( pointX             >> 16 ) - viewportWidth  / 2
desiredZ = sext16( (pointZ - (pointY >> 1)) >> 16 ) - viewportHeight / 2
```

`pointY >> 1` is an arithmetic shift (a floor halving, not a truncation toward
zero); `sext16` takes the low sixteen bits of the shifted value and
sign-extends them, so a world coordinate beyond a signed 16-bit map-pixel range
wraps rather than saturating. The half-viewport terms are signed divisions of
the presentation extents. The desired origin is then clamped per axis in this
order: a value below zero becomes zero, otherwise a value above
`mapPixelExtent - viewportExtent` becomes that limit. Because the low clamp is
tested first, a viewport wider than the map clamps to zero rather than to the
negative limit. Selecting a point also clears the view-dirty bit that the step
below re-tests.

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

Retail evidence does not establish a keyboard/edge “target” that is advanced
by phase 10. A sample-once/hold-through-the-next-pump seam is a valid
Nanolathe determinism choice, not a retail contract; if used, it must keep
host-frame sampling, phase-10 intent application, follow-target stepping, and
shake as named stages, and it must not be documented as retail's input
algorithm. Retail's host-frame writer remains one direct movement pass
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
picture and draws start positions (the viewport rectangle is not on FINAL:
the HUD composer strokes it onto its own destination surface after copying
FINAL there, [03 R-MM-01 §1]). Radar/sonar
contact blips use dedicated palette entries distinct from terrain colors —
the sensor circles drawn beside them take colour-map entry 10 (radar and
sonar coverage) and entry 12 (both jam circles), with the weapon/interceptor
ring in entry 15 ([03 R-MM-01 §2]) — and
the radar surface is wiped and rebuilt each tick while the picture persists.

Per-axis camera clamp order is:
`maximum = mapSize - viewSize; if camera < 0 then 0 else if camera > maximum then maximum`,
giving inclusive `[0, mapSize-viewSize]` in the normal `viewSize <= mapSize`
domain; ordered form controls negative-maximum domains. The clamp refreshes the
camera-to-radar rectangle. Camera persistence uses `Camera` `X_Position` /
`Z_Position` and reapplies the clamp on load. **`viewSize` here is the battle
viewport subrect's span and the bounds are in retail's own camera frame, whose
origin is the world point at that subrect's top-left corner — see
[R-CAM-01 §13] below, which is what a reimplementation with a different origin
frame has to convert.**

Radar layout fits the map aspect inside a 126×126 square with signed integer
division and centers the shorter dimension:
`radarHeight=126, radarWidth=floor(mapWidth*126/mapHeight)` when
`mapWidth < mapHeight` else the transpose, with `padX = trunc((126-radarWidth)/2)`
or `padY` accordingly. The rectangle is inclusive
`right=padX+radarWidth-1, bottom=padY+radarHeight-1`. The direct radar branch
converts
`worldX=(mouseX-padX)*PlayRight/radarWidth`,
`worldZ=(mouseY-padY)*PlayBottom/radarHeight` — the play-area pixel extents
`PlayRight`/`PlayBottom` are the scale numerators, matching [03 §3.11] — which
is the pointer's world position; the camera jump subtracts half the viewport
from it and follows the standard movement/clamp path ([R-CAM-01 §11]). The
alternate drag/current-camera branch is the cursor-warp drag-scroll mode of
[R-CAM-01 §11]. No zoom/rotation mutation occurs in the reviewed edge-scroll,
direct-radar, clamp, or save/load paths. Minimap rendering and visibility
masks are separate concepts.

### The scroll pass: inputs, units, and what it cancels [R-CAM-01 §10]

**Established fact — inputs of the host-frame writer.** The scroll pass of
[R-CRD-006 §1] runs once per host frame, after that frame's sub-ticks and
hotkey dispatch ([R-CAM-01 §1]). Its inputs, with their sources:

* **Scroll setting byte** — an unsigned byte; registry `scrollspeed` (absent
  → `32`), the `SCREEN` slider of the interface options (`1..65`,
  [R-CAM-01 §7]), the chat command `+ScrollSpeed n` (low byte of `n`), and
  `RESTORE` (`32`). It is the only settings byte preserved across the camera
  block reset at battle entry (the reset zeroes the tracked object, follow
  target, bookmarks, hold state and both origins, and restores the byte —
  [R-CAM-01 §14]).
* **Raw delta** — a signed 32-bit host value: this frame's scaled
  `GetTickCount()` reading minus the previous frame's, as stored by the
  tick-budget step ([R-CAM-01 §1]). The scale is the presentation object's
  time-scale integer, which the battle boot path sets to **30**, so the
  reading is `floor(GetTickCount() × 30 / 1000)` and the delta counts
  **thirtieths of a second** elapsed since the previous outer frame — not
  milliseconds. It is refreshed
  only when the budget step runs (never while paused in single-player), and
  the movie writer resets its base after each capture ([R-CAM-01 §8]).
* **Pointer position** — when the presentation object has not captured the
  mouse (its capture bit clear), the pass reads the live cursor position
  through `GetCursorPos` (not the pointer record) and applies the §10 strip
  rule: a cursor at or beyond the right/bottom edge but less than `100`
  pixels beyond, while the game window has keyboard focus, is treated as
  `W-1` / `H-1`; a cursor further out, or without focus, keeps its raw
  coordinates and matches no edge. When the mouse is captured, the pass
  uses the pointer record's coordinates clamped to `W-1` / `H-1`.
* **Held arrows** — the four `GetAsyncKeyState` queries of §2, suppressed
  while `TALK.GUI` is open.

The magnitude is `min(128, scrollByte × rawDelta)` in **map pixels per host
frame**, where `rawDelta` is the thirtieths-of-a-second delta above. The
whole pass — cursor read, edge tests, direction tests and all — is skipped
when the magnitude is zero, so a host frame that lands inside the same
thirtieth as the previous one scrolls nothing at all. With the default byte
the sustained rate is therefore `32 × 30 = 960` map pixels per second and is
**independent of frame rate**: at 60 fps every other frame contributes `32`
pixels, at 30 fps every frame contributes `32`, and at 10 fps every frame
contributes `96`. The `128` cap bites only once a single frame spans four or
more thirtieths at the default byte (a frame interval of about 133 ms, or a
proportionally shorter one at a higher setting), which degrades the rate
rather than raising it. The
product is a signed 32-bit multiply of the zero-extended byte and the raw
delta; a negative delta (a wrapped tick count) yields a negative magnitude
that the `> 128` test does not cap (it is a signed comparison) and the
`!= 0` test does not skip, so it
scrolls the opposite way for one frame. The direction tests and the
sequential opposing-direction behaviour are as [R-CRD-006 §1] states.

**Established fact — the raw delta is thirtieths of a second, not
milliseconds.** The scroll pass and the tick-budget step read the same stored
delta word — the budget step writes `scaledNow − previousAnchor` into it and
the scroll pass multiplies it by the scroll byte — and the scaled reading
both share is the one helper that returns `GetTickCount() × timeScale /
1000`, whose time-scale integer is set once, to `30`, on the way into the
battle mode. That is the `floor(ms × 30 / 1000)` timebase [01 §4.1]
establishes for the budget, and it has to be: the budget's runnable-tick
count is `delta × speed + carry` truncated and clamped to `0..5` [01 §4.2],
which only yields a 30 Hz simulation if `delta` is already in simulation
ticks. A millisecond delta would run five sub-ticks on every 16 ms frame — a
150 Hz simulation — and would make the scroll cap fire on every frame at
every playable frame rate. Nanolathe impact: the battle screen's keyboard and
edge scroll must derive `rawDelta` from a 30-per-second scaled clock; feeding
it milliseconds scrolls eight times too fast at 60 fps.

#### The scroll pass while paused [R-CAM-01 §10]

**Established — the single-player pump skips the budget but not the scroll
pass.** The battle host pump's single-player arm evaluates
`pauseBitClear && (budgetStep(), runnableTicks != 0)`. The C short-circuit is
the whole contract: with the pause bit set the budget step is **not called at
all**, so neither the stored raw delta nor the scaled-time anchor is touched.
The hotkey dispatch and the scroll pass then run from a block placed *outside*
that arm, gated only on the in-battle options-window bit — never on pause. So
while single-player is paused the scroll pass still runs once per host frame
and still multiplies the scroll byte by the **frozen** pre-pause delta.

A whole-image census of the raw-delta word settles that nothing else can
disturb it: exactly one writer (the budget step) and two readers (the budget's
own runnable-tick product, and the scroll pass). The anchor's other writers are
battle entry, the screenshot hotkey and the movie-capture writer
([R-CAM-01 §8]); none of them is a pause or unpause path.

**Established — what the player sees.** Three consequences follow, and all
three look like defects to a reader who has not traced them:

* **A paused camera scrolls**, by keyboard or by edge, at `scrollByte ×
  frozenDelta` map pixels per **host frame**. Unlike unpaused scrolling this is
  frame-rate *dependent*: at the default byte and a frozen delta of 1 it is
  `32` pixels per frame, about `1920` map pixels per second at 60 Hz.
* **The rate depends on which frame the pause landed on.** The frozen value is
  the delta of the frame in which pause was pressed ([R-CAM-01 §1]) — the
  budget step runs before hotkey dispatch, so that frame is still budgeted.
  At 60 Hz that delta is 0 about half the time, and a pause that lands on such
  a frame leaves the paused camera **completely immobile** until unpause.
* **Unpause takes one capped step.** The anchor did not move either, so the
  first unpaused budget spends the entire pause in one delta. That is the
  single-player unpause burst of [01 §4.3] seen from the scroll pass: with an
  arrow held, one frame at the `128`-pixel cap, then the ordinary rate.

**Multiplayer differs** and is out of scope here: its arm calls the budget step
every iteration even while paused, so anchor and delta keep tracking wall
clock and paused scrolling behaves exactly as unpaused scrolling does
([01 §4.3]).

The in-battle options window does not reach any of this: opening it sets the
pause bit *and* the options-window bit, and the latter skips hotkey dispatch
and the scroll pass outright ([R-CAM-01 §1]). The reachable case is the pause
hotkey.

Nanolathe impact: a paused-scroll rate that looks like a runaway is correct;
zeroing the delta on pause would be a divergence, not a fix.

**Established fact — what a scroll cancels.** When the pass changes either
origin coordinate it: writes the origin, sets the view-dirty bit, runs the
per-axis clamp, copies the origin into the phase-10 **desired** origin (so
the follow step has nothing to close), clears the render-flags minimap cache
bit (*Supported inference* on that bit's role: it is the bit every camera
writer clears and the minimap composer re-tests), and
**zeroes the hold count, the tracked object and the followed projectile**.
Any keyboard or edge scroll therefore ends `t`/Ctrl+C tracking and a
`holdtime` hold ([06 §7.3]; the hold's own statement is in §10 above). The
same triple clear is performed by the bookmark recall (F5–F8), the minimap
camera jump and the drag-scroll entry ([R-CAM-01 §11]) and `+BigBrother`
off; it is **not** performed
by the glide writers (`n`, F3) or by the battle-start placements, which only
write the desired origin ([R-CAM-01 §12]).

### Minimap click, latch, and drag-scroll arithmetic [R-CAM-01 §11]

**Established fact — pointer classification (step 1 of the frame).** With
`RadarW`/`RadarH` the letterboxed radar extents and `padX`/`padY` its origin
within the minimap canvas (§10), `PlayRight = Width·16 − 32` and
`PlayBottom = Height·16 − 128` [03 §1]:

* Pointer inside the inclusive minimap rectangle and no drag rectangle
  active → **minimap region** (region bit 0 set, bit 1 clear) and the
  pointer's world position is the lens conversion
  `worldX = (ptrX − padX) · PlayRight / RadarW`,
  `worldZ = (ptrY − padY) · PlayBottom / RadarH` (signed 32-bit
  multiply, then signed division truncating toward zero) — **no**
  half-viewport term.
* Otherwise → clamp the pointer into the view rectangle, region bit 0
  clear, bit 1 = "pointer inside the view rectangle", and the world
  position is `camera + (clampedPtr − viewOrigin)` per axis.
* Region bit 2 = bit 0 OR bit 1. The world position then goes through the
  ground resolver of §8, and its truncated `>> 20` (16.16 → 16-pixel cell)
  coordinates index the occupancy map for the hovered feature.

This is the "lens branch" of [03 §3.11] — it produces the **pointer's world
position** (orders given on the minimap under `Interface Type 0`, the
hover target), not the camera.

**Established fact — the minimap latch (camera jump).** Region-bit-0 clicks
set a latch bit in the pointer-flags byte ([R-CAM-01 §5] for which button
under which polarity); while it is set, every host frame writes the camera origin from the pointer record (the first jump lands on the frame **after** the one that set the bit, because the frame handler tests the bit before the click paths write it):

```
cameraX = (ptrX − padX) · PlayRight  / RadarW − trunc(viewWidth  / 2)
cameraZ = (ptrY − padY) · PlayBottom / RadarH − trunc(viewHeight / 2)
```

(signed truncating divisions, the half-viewport terms signed), then sets the
view-dirty bit, clamps per axis, copies current to desired, clears the
minimap cache bit, and clears the hold count, tracked object and
followed projectile. The clicked map point becomes the **centre** of the
view. The latch is released by the matching button-up message, and the
pointer record's coordinates — not `GetCursorPos` — are used, so dragging
across the minimap pans continuously. The pointer-classification conversion
above has no recenter; the camera jump does.

**Established fact — the drag-scroll mode (the "alternate drag branch").**
Under `Interface Type 0`, right-down over the **world view** (region bit 1)
with **Ctrl** held (key-state bit `0x08` of the pointer record) enters a
cursor-warp drag mode:
the frame handler stores the pointer record, sets the drag-mode dword, saves
`anchorX = trunc(cameraX / 16)`, `anchorZ = trunc(cameraZ / 16)` (signed,
truncating toward zero), computes the screen centre `(displayW / 2,
displayH / 2)` and warps the OS cursor there (`SetCursorPos`). While the
mode dword is set the frame handler runs, instead of any click path:

```
dx = ptrX − centreX ; dz = ptrY − centreY               (pointer record)
cameraX = (trunc(dx / 4) + anchorX) · 16
cameraZ = (trunc(dz / 4) + anchorZ) · 16
dirty; clamp; desired = current; clear the minimap cache bit; (hold/tracked/followed untouched here)
anchorX = trunc(cameraX / 16) ; anchorZ = trunc(cameraZ / 16)
warp the cursor back to the centre
if the record's right-button key-state bit (0x02) is clear:
    leave the mode, warp the cursor to the stored record's position, re-show the cursor
```

So each frame moves the camera by `16 · trunc(delta / 4)` map pixels per
axis — four map pixels per screen pixel of mouse travel, quantised to 16 —
relative to the previous frame, and the origin is always a multiple of 16
while the mode is active. The mode's entry clears the hold count, tracked
object and followed projectile.

**Established — discarded motion has no remainder owner.** The frame stores
only the clamped, quantized origin as the next anchor and recenters the
pointer on every visit, including a visit with displacement smaller than four
pixels. Repeated signed displacements from −3 through 3 contribute zero to
the motion term; origin quantization and clamping still apply. There is no
stored remainder that could combine such samples into a later full step.
This is a host-frame rule, not a 30-Hz simulation-tick gate. Its outcome can
depend on how the same physical travel is divided among host frames; the
arithmetic alone establishes no fixed display refresh rate.

### The camera-jump family and what breaks a follow [R-CAM-01 §12]

**Established fact — the follow state.** The follow camera's state is the
current origin, the desired origin, the 16-bit hold count with its frozen
16.16 anchor, the tracked-object reference, the followed-projectile
reference, four bookmark origins with valid bytes, and the camera-flags
byte whose bit 1 is the view-dirty bit. Phase 10 ([01 §4.4]; the hold
arithmetic in §10 above, its writers in [06 §7.3]) is the only stepper.
"Glide" below means writing only the **desired** origin (clamped per axis,
minimap cache bit cleared) so that phase 10 closes the gap at the 320-per-tick /
half-remaining rate of §10; "jump" means writing the current origin and
copying it to the desired origin. Every writer:

| Writer | Kind | Clears hold / tracked / followed? |
|---|---|---|
| Scroll pass (keyboard, edge) | jump by delta | yes ([R-CAM-01 §10]) |
| Minimap latch, drag-scroll entry | jump | yes; the per-frame drag step itself does not |
| F5–F8 bookmark recall | jump to the stored origin | yes |
| Load game (`Camera` account) | jump | no (the origin only; the follow references are not in the account, §10 "save ownership") |
| Battle-start placement | jump: a skirmish player's commander stamp position minus half the viewport [08 R-SKIR-01]; the `Camera` account for a loaded game; otherwise the first start-position record of kind `1` minus half the viewport | no |
| `Ctrl+C` | tracked object = last own `Commander`-category unit | sets tracked; hold and followed untouched |
| `t` / `T` | tracked object = next / previous selected unit, or null | as `Ctrl+C` |
| `n` (next unit) | glide to the unit's position | no |
| F3 (message source) | glide to the source unit's position (its map-pixel X/Z words) | no |
| Phase 10 itself | steps current toward desired; a dead tracked object (alive bit clear) clears all three | — |
| `+BigBrother` off, `+Move x y` (developer) | cancel / jump | yes / (developer, untraced) |

**Established — the `+BigBrother` companion word.** The word `+BigBrother` writes `1` into is the 16-bit cycle
counter of the unit sweep tail ([04 R-MOV-03 §1]): while the camera-flags
bit is set and **Shift** (held-key query for token `0xF9`, §2) is not held,
the tail decrements it each tick and, when it falls below 1, resets it to 90
and re-picks the selection and the tracked object as `t` does. Shift held
pauses the cycle. The tail is after the phase-2 unit sweep, before phase 3.

**Established — cycle lifecycle.** Process initialization starts the flag off
and the signed 16-bit counter at zero. Each battle-entry world rebuild,
including in-place save restore, resets follow state and clears the cycle
flag; it does not write the counter. The Camera save account restores only
origin. Teardown does not write either cycle field. Enabling always overwrites
the counter with one; disabling clears the flag and follow triple while
retaining the counter. The dormant counter retained between battles is never
read while disabled and is overwritten before the next enabled read.

Unit positions enter the desired origin through one conversion:
`desiredX = sext16(unitX >> 16) − trunc(viewWidth / 2)`,
`desiredZ = sext16((unitZ − (unitY >> 1)) >> 16) − trunc(viewHeight / 2)` —
the same shear-and-recenter phase 10 applies to a followed point (§10). The
message-source glide instead reads the unit's map-pixel X/Z words directly
and subtracts the half viewport without the height shear. `Ctrl+C` and
`t`/`T` do **not** move the camera themselves; the first phase-10 pass
after them begins the glide toward the tracked unit, and the glide continues
every tick until a scroll, a minimap jump, a bookmark recall, the unit's
death, or a `holdtime` hold started by one of that unit's projectiles when
it is a building ([06 §7.3]) intervenes. A hold ends by count expiry back
into ordinary following; a hold is cancelled early only by the writers
marked "yes" above.

**Established fact — bookmarks.** Ctrl+F5..F8 store the **current** origin
(both axes) into slot `0..3` and set the slot's valid byte; F5..F8 recall
the slot unconditionally (the valid byte has no reader in the recall path —
its only reader is the save/restore census, doc 08), jump, clamp, and clear
the follow triple. Bookmarks are not in the `Camera` save account.

### The clamp's frame of reference: what "camera 0" and `viewSize` mean [R-CAM-01 §13]

**Established — a retail camera origin is the world point at the battle
viewport's top-left corner, not at the display's.** [R-CAM-01 §11]'s pointer
classification gives the world position of a pointer outside the minimap as
`camera + (clampedPointer − viewOrigin)` per axis, where `viewOrigin` is the
battle viewport subrect's origin — `(128, 32)` at every mode [03 §4.1]. A
pointer resting exactly on that origin therefore reads the camera's own
coordinates, which is the definition of the frame: the camera origin is drawn
at framebuffer pixel `(128, 32)`, and the 128 columns of command panel and the
32 rows of resource bar above it are *not* part of what the origin measures.
Every camera arithmetic in this section is expressed in that frame: the clamp's
`0` floor, the clamp's `mapSize - viewSize` maximum, the half-viewport
recenters of [R-CAM-01 §11] and [R-CAM-01 §12], and phase 10's desired origin
([R-CRD-006 §1], which names the same quantity `viewportExtent`).

*What the floor means, concretely.* At `camera = 0` the map's column and row 0
sit exactly on the viewport's leading edges, so the whole playable area west and
north of the start position is reachable. A capture of the retail build on
*Great Divide* scrolled hard west confirms it: the map's only geothermal vent,
anchored at map pixel `(104, 152)`, appears at framebuffer x ≈ 232 — that is
`104 + 128`, the vent's map pixel plus the viewport's left inset, with the
camera at its floor of 0.

**Established — the maximum's extent operand is the same subrect span.**

The battle-setup routine that arms the viewport writes six words in one block,
in this order: the negotiated display width and height, copied from the mode
record (they are initialised to `640 × 480` a few lines earlier and then
overwritten); the subrect's left `128` and top `32` as literals; the subrect's
**inclusive** right as `displayWidth − 1` and its **inclusive** bottom as
`displayHeight − 33`; and finally the span pair as `right − left + 1` and
`bottom − top + 1`. So the span pair is `W − 128` by `H − 64`, held in globals
distinct from the display size, and it is that *span* pair — not the display
pair — that the clamp's maximum subtracts. The same span pair, halved, is the
operand of every recenter and camera-jump site (the minimap latch, the
camera-jump family, the follow recenters), so one span definition serves the
clamp, the jumps and the recenters, exactly as this section assumed.

The two bounds are therefore symmetric (`512 × 416` at 640×480, `W-128 × H-64`
generally [03 §4.1]): the floor puts the playable area's first pixel on the
viewport's leading edge and the maximum puts its last pixel on the trailing
edge. That is what [R-CRD-006 §1]'s wording ("subtracting half the viewport
span", `mapPixelExtent − viewportExtent`) says, and it is the only reading
under which the playable extents `PlayRight = Width·16 − 32` /
`PlayBottom = Height·16 − 128` [03 §1] are fully visible. The rejected
alternative — the clamp reading the negotiated display width and height — would
have left the map's last 128 playable columns and last 64 rows permanently off
screen.

**Established, same block — the insets are the ones [03 §4.1] gives.** The
literal left `128` and the inclusive right `displayWidth − 1` make the X insets
leading 128 / trailing 0; the literal top `32` and the inclusive bottom
`displayHeight − 33` make the Z insets leading 32 / trailing 32 (rows
`H−32 … H−1` are chrome), corroborating [03 §4.1] from the viewport arming
itself.

See [R-HUD-05] for the rest of the display-mode layout.

**Nanolathe impact.** This build's camera
origin is the world point drawn at the *framebuffer's* top-left corner, not the
viewport's: the world is composed across the whole framebuffer and the chrome
is painted over it, so every draw site takes the projection of [03 §2.5] and
subtracts `(128, 32)` back out, and picking re-adds them. Retail's bounds
therefore have to be converted, by substituting `retailCamera = camera +
leadingInset`:

```
minimum = -leadingInset
maximum = mapSize - viewportSpan - leadingInset
```

with the subrect's insets — leading 128 / trailing 0 on X, leading 32 /
trailing 32 on Z [03 §4.1]. On X the maximum is numerically unchanged
(`mapSize - framebufferWidth`, because the trailing inset is zero) and on Z it
gains the bottom inset.

"The presentation width `W` and height `H`" of the scroll predicates above
*are* display extents and are not the operand here: a clamp that used retail's
bounds unconverted left the westmost 128 map pixels, the northmost 32 and the
southmost 32 of every map off screen. The clamp, the battle-start jump
([R-CAM-01 §12]) and the phase-10 desired origin share one conversion.

The **ordered** form is unchanged — the floor test still runs before the
maximum test — and the Unknown in the tail still stands: converting the frame
does not close the viewport-larger-than-map domain, it only moves the
degenerate condition from `viewSize > mapSize` to `viewportSpan > mapSize`,
which is the same condition correctly transposed.

### The battle-screen marker cluster: reset origin, saved-camera load, the start jump, the key-token producer, `n`/`N`, F3's two bits, F4's flash, and the minimap click paths [R-CAM-01 §14]

Static trace of the frame handler, the world-click handler, the key
translator and the camera writers; the HUD items are [R-HUD-04 §4].

**Established — the camera-block reset leaves both origins at (0, 0).** The
reset the world rebuild runs ([08 R-ENTRY-01 §3] step 12) zeroes
twenty-three consecutive 32-bit words of the camera block and writes the
scroll-setting byte back. The block spans the tracked-object and
followed-projectile references, the four bookmark origins with their valid
bytes, the **current origin**, the **desired origin**, and the hold count
with its anchor — so after the reset `current = desired = (0, 0)`. The
camera-flags byte (view-dirty bit 1) and the render-flags word lie outside
the block. A campaign without a start-position special therefore keeps
`(0, 0)` as both origins ([08 "Campaign camera"]).

**Established — a saved-camera load is a jump; the "state bits" are two.**
The load reads `X Position` / `Z Position` of the `Camera` account with the
current origin as each default, writes them to the current origin, sets the
view-dirty bit, clamps, copies current to desired, and clears the terrain
cache-valid bit. No glide is set up: desired equals current after a load.
The two bits every camera writer touches are the camera-flags byte's bit 1
(view-dirty — the composer redraws the view) and the render-flags word's bit
3 (terrain view cache valid — its only reader is the terrain view builder,
which rebuilds its per-view cache when the bit is clear and then sets it;
every camera writer clears it). Neither is authored or saved; a
reimplementation that rebuilds the view every frame needs nothing beyond
`desired := current`.

**Established — the battle-start jump has no height shear.** Both
battle-start writers (the skirmish spawn and the world-rebuild tail) call
the jump with `x = stampX − trunc(viewWidth / 2)`,
`z = stampZ − trunc(viewHeight / 2)`, where `stampX`/`stampZ` are the
whole-pixel halves of the commander stamp's 16.16 X and Z; the Y word is not
read. The `(z − y/2)` shear of §12 belongs to the unit-position glide
conversion only. For a watcher slot (the player-record bit §2 names for the
watching player) the world-rebuild tail instead jumps to
`(trunc(viewWidth / 2), trunc(viewHeight / 2))` and clears render-flags bits
0 and 1 — the mapping and LOS masks ([03 R-MM-01 §3]) — so a watcher's view is
unmasked from its first frame.

**Established — the key-token producer, and why Shift+digit never recalls
a group.** The window procedure feeds the key ring from three messages. A
*character* message pushes the translated character verbatim: Escape is
`0x1B`, `1` is `0x31`, Shift+1 is `!` (`0x21`) on the US layout the retail
install assumes, `n` is `0x6E`, `N` is `0x4E`. A *key-down* message goes
through the translator: with Ctrl held (asynchronous key state) a letter
pushes `0xAA + (letter − 'A')`, a digit `0xC4 + digit`, F1..F12
`0xCE..0xD9`; without Ctrl an ordinary key pushes **nothing** from the
key-down message (its character message carries it), while F1..F12 push
`0xE2..0xED`, Home/End/Page/arrow keys `0xF0..0xF7`, Insert/Delete
`0xEE`/`0xEF` and Pause `0xF8` regardless of Ctrl. A *system key-down*
message (Alt held) pushes a letter as its lowercase character and a digit as
the **raw virtual-key value `0x31..0x39`** — the same token as the
unshifted digit character; Alt produces no character message, so Alt+digit
is exactly one token. Consequences:

1. The digit case of §2 is reached by an unshifted digit character or by
   Alt+digit. The Shift argument it passes to group recall is live only for
   **Shift+Alt+digit**. Under the default `SwitchAlt = 0` (§4: Alt+digit
   recalls) additive recall is therefore Shift+Alt+digit. With
   `SwitchAlt = 1` a plain digit recalls, but Shift+digit yields the shifted
   character instead — `!` `#` `*` toggle the label bit, the other six
   shifted digits have no case — so additive recall is unreachable from the
   keyboard in that setting.
2. `!` `#` `*` are ordinary reachable tokens (Shift+1, Shift+3, Shift+8);
   the label toggle is the only thing they do, and the two rules the digit
   row seemed to contradict never meet.
3. `N` (`0x4E`) has no case: Shift+n does nothing at the dispatcher. A
   stockpile round is enqueued only by the palette's `MAKENUKE`/`MAKEANTI`
   gadgets ([07 §6]).

The character values are those of the US layout; on another layout the
character message decides the token — *Supported inference* for non-US
keyboards.

**Established — F3's leading clear is a different bit from the visited
bit.** Each message-ring record carries a flag byte. The F3 case first
clears **bit `0x20`** of every record, then scans from the display index
toward the producer index (wrapping at 30 — oldest displayed message first)
for a record whose source unit id is non-zero, whose **bit `0x10`** is
clear, and whose unit is alive; on a hit it sets both bits (`|= 0x30`) and
glides to the unit's map-pixel X/Z minus half the viewport (no shear). If
the scan finds nothing it clears bit `0x10` on every record and scans once
more. The two writes are different bits, so "not yet visited" is a real test
and the retry runs once every live-source message has been visited. Bit
`0x20` marks the record most recently jumped to.

**Established — the jumped-to line is the highlighted line.** Bit `0x20`'s
reader is the **message column painter** of [R-HUD-03 §14.4], and the bit
decides the line's colour. Walking the visible records in drawing order, the
painter tests
bit `0x20` on each admitted record and installs the FNT display context's pair
as (colour-map entry **10**, skip colour 254) when the bit is set and
(colour-map entry **15**, skip colour 254) when it is clear, immediately before
that line's text call — so exactly one line at a time, the one F3 last jumped
to, is drawn in the highlight entry and every other line in the ordinary
battle-text entry. It installs the pair per line rather than once for the
column, so the highlight cannot leak onto the following line. The same walk
carries the `screenchat` class filter and the speaker-sentinel test that
decides the line's `x` (138 for sentinel 10, otherwise a logo-width offset), so
one routine owns colour, filter and pen together. Nothing else in the recovered
image reads the bit.

**Established — F4 pins the score panel open and arms the kill/loss
flash.** Interface-flags bit `0x80` has exactly two readers: the score
panel's showing test ([R-HUD-04 §1] — the panel shows while the bit is set
as if Space were held), and the kill-credit finalize, which sets the
crediting slot's kill flash and the victim slot's loss flash to 30 **only
while the bit is set**. With F4 on, every kill draws the killer's Kills
number and the victim's Losses number bright, fading to row 0 over half a
second on the pinned panel; with F4 off the flash arrays are never armed
and Space shows a panel with steady numbers. The bit's user-facing name
remains **Unknown** — no string in the image names it.

**Established — idle viewport click classification.** When an idle left press
starts a viewport drag, save a fresh reading of the scaled wall clock and
initialize both drag endpoints from the current ground-resolved world point.
Each coordinate is the signed whole part of its 16.16 value. On a subsequent
non-release drag pass, replace the moving endpoint with the current
ground-resolved point. On left release, clear drag-active and classify using
the stored endpoints; the release pass does not first refresh the moving
endpoint.

Take a fresh scaled wall-clock reading during release processing. Form the
saved press reading plus **25**, retaining the low 32 bits. A click requires
the current reading to be **strictly less** than that deadline under a signed
32-bit comparison, and the absolute differences of the two endpoints'
whole-world **X and Z** coordinates each to be **strictly less than 32**.
Equality at 25 or on either dimension at 32 is a box release. Y does not
participate in this classification. A failed click test follows the existing
box-selection path. The accepted world-click handler uses the release input
and current resolved pointer; its command position is therefore distinct from
the stored endpoint used for classification.

**Established — timing identity and wrap.** Both press and release sample the
live scaled clock when the battle handler processes them; they do not use the
timestamps carried by the input records and do not use the authoritative
simulation tick. With a 32-bit unsigned millisecond reading, the clock is
`floor(((milliseconds × 30) modulo 2^32) / 1000)`: multiplication wraps before
the unsigned division. The deadline comparison is an absolute signed
comparison, not an unsigned elapsed-time subtraction. Consequently a clock
reading lowered by multiplication wrap can still satisfy the deadline test.
The clock's own result range is 0 through 4,294,967, so reachable clock
readings and their 25-unit deadlines remain positive signed integers.

**Established — the world-click handler is region-agnostic, and its branch
order.** Under `Interface Type 0` the frame handler routes a left-down to
the world-click handler whenever the armed-order latch is not idle, whatever
the region. With the latch idle, a left-down over the **minimap** (region
bit 0) goes to the handler at once — no box drag starts on the minimap —
while over the view it starts a box drag whose release counts as a click
when the strict clock deadline and stored whole-world endpoint tests below
pass. (Under `Interface Type 1` an idle-latch left-down
over the minimap sets the minimap latch of §11 instead.) The handler then
tests, in this order:

1. **Latch `MOBILEBUILD`.** If the pointer-flags byte's **site-valid bit
   (bit 6)** is clear → `notoktobuild` cue, nothing else. If set → issue
   the build: for every selected own unit whose definition has the builder
   bit, resolve `MOBILEBUILD` (`VTOL_MOBILEBUILD` for a flyer) against the
   armed product and the pointer's world point snapped to the product's
   footprint grid, queued when Shift is held; `oktobuild` cue; Shift keeps
   the latch (sticky bit), otherwise the latch returns to idle. The
   site-valid bit has **one** writer — the in-view placement preview, which
   the frame handler runs only when region bit 1 (view) is set and the latch
   is `MOBILEBUILD` — and is cleared by the world rebuild. Over the minimap
   the preview does not run, so the bit holds the verdict of the last
   in-view hover: a minimap click while placement is armed **sites the
   building at the minimap-resolved world point** if the last view position
   was valid, and plays `notoktobuild` if it was not. This bit is also the
   "special latch flag" that picks the drag-box colour in §9.
2. **Cursor kind `0x0F`** — the resolver's "select" answer: latch idle and
   the hovered unit is an own selectable unit (own slot, selectable bit,
   remaining-build fraction `0.0`, post-capture grace zero, carrier null or
   itself a visible carrier) → the select branch. With Shift: toggle the
   unit's selected bit, acknowledge if it is now selected, clear the current
   build-menu unit, mark the HUD dirty, done. Without: clear the selected
   and both visited bits on every unit, run the selection refresh, mark
   every on-screen own unit visited, set the unit's selected bit,
   acknowledge, mark the HUD dirty. The hovered unit over the minimap is the
   blip-dot winner within squared distance 4 ([R-HUD-03 §1]), so **clicking
   a blip selects that unit**.
3. **Cursor kind below `0x11`** → issue the resolved order (the latch code,
   or the contextual code with the latch idle) for the selection at the
   pointer's world point; Shift keeps the latch as in 1.
4. Otherwise, under `Interface Type 1` with the latch idle → deselect all.

### Supported inference

Camera and minimap conversion should remain an explicit compatibility
boundary. The retail paths do not share one conversion routine: the minimap
lens does not reuse the main view's cursor-to-world projection [03 §3.11], and
the ground resolver is a distinct search (§8). The minimap's viewport
indicator is a one-pixel rectangle **outline** in colour-map entry 14,
stroked onto the HUD destination surface after the radar picture is copied
there ([03 R-MM-01 §1]); the world composer's film-mode crosshair (§6) is a
different figure.

### Unknown

- Camera clamp behavior in unusual domains — a map whose viewport span
  exceeds the map size on an axis, where the ordered clamp form is the only
  established behavior · §10 [R-CAM-01 §13] · static trace.
- Whether any transient follow-target or shake state is reconstructed from a
  non-`Camera` save account · §10, doc 08 · static trace.
- Start-position marker art and placement · §10, doc 03 · static trace.
- Whether the character values of the key-token producer hold on a non-US
  keyboard layout (Supported inference) · §10 [R-CAM-01 §14] · manual retail
  observation.


## 11. Running display, pause, chat, options, and outcomes

### Established fact

The running display includes live resource bars and numbers, game time/speed
text, a unit hover/selection information region (its footer sources and
their priority are [R-HUD-03 §1]), damage and queue indicators,
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
opener hides the diplomacy gadgets for non-diplomatic contexts. F2 (token
`0xE3`, [R-CAM-01 §2]) while the battle-interface ESC bit is clear opens the
hard-coded `guis/armopt.gui` window with `anims/armopt.gaf`; this name is not
side-prefixed. Opening it sets both the battle modal bit and the
single-player pause bit, and pauses the runtime audio path. Closing the root
window clears the modal and pause bits and resumes audio. A second F2, or
Escape, while the root options window is active therefore closes it and
resumes the battle.
The authored root controls are `LOADGAME`, `SAVEGAME`, `PREFS`, `MISSION`,
`HELP`, `EXIT`, and `OK` (`Resume`). Network mode takes a separate path and
does not set this local pause bit.

Activating `EXIT` pushes `guis/exitmenu.gui` over the options window. Its
authored controls are `MAINMENU` (`Exit to Menu`), `EXITGAME` (`Exit Game`),
`RESTART`, and `CANCEL`. `MAINMENU` opens `guis/yesorno.gui` with the title
`Surrender this battle and return to main menu?`; `EXITGAME` uses
`Surrender this battle and exit to Windows?` unless launched from a lobby,
where it uses `Exit the Battle` [R-FE-01 §7]. Before either confirmation opens,
the exit callback closes `EXITMENU`. `CHOICE1` (`Yes`) commits the requested
transition; `CHOICE2` (`No`), including Enter or Escape, closes the confirmation
and exposes the still-open options window. The root options window remains
beneath either child, but the exit window does not remain beneath confirmation
or Restart. Closing a child therefore preserves root-owned pause/audio pause;
closing the root resumes the battle. **Established (direct static caller
trace)** [R-FE-01 §7][R-WGT-01 §1].

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
states later transition to end-mission/endgame report screens. The
options-window pause path is established above; the Pause key toggles the
same local pause bit and emits the pause packet ([R-CAM-01 §2], [01 §4.3]).

Game-speed changes are clamped to the retail range and displayed as localized
messages. In multiplayer, speed changes are represented as networked semantic
commands rather than purely local UI changes.

**End-mission resources.** Retail supplies `guis/endmsn.gui` and
`anims/endmsn.gaf`; there is no generated message box, literal header string
or `Continue` callback.
The GUI's named controls include `Start`, `LoadGame`, `SaveGame`, `MainMenu`,
and `Difficulty`; the result action is attached to the authored `Start`
control, not a control named `Continue`. The GAF contains `outcdivider`,
`victory`, and `defeat` entries. **Established:** the results title uses frame
0 of `igvictory` or `igdefeat` from `anims/igtitles.gaf`, not those endmsn
copies. Its draw anchor is `(surfaceWidth/2, 28)` and the ordinary frame
blitter subtracts that selected frame’s authored offsets. The earlier claim
that ENDMSN drew its own copies at surface center was incorrect; the loader
and results composer establish the shared title source [08 R-CAMP-01 §8]
[fmt gaf].

The file's end-mission controls are initially inactive in the retail asset.
The end-mission initializer chooses the outcome resource from campaign
progression and activates the established route: `Start` when a campaign has a
discovered next mission, or `MainMenu` when there is no next mission; the
opener's control set is [08 R-CAMP-01 §8] and the screen's edges are
[R-FE-01 §10]. Missing GUI/GAF resources are a degradable unsupported result,
not a reason to generate a centered rectangle, button set, title text, or
fallback labels [08 "Progression"]. The statistics rows are
[08 R-CAMP-01 §7] and their bar animation [R-HUD-03 §11].

### Supported inference

Modal state should be authoritative for input routing, while pause/victory/
defeat should be represented in the battle presentation state so the world
can remain composed under the appropriate overlay.

### Unknown

- Chat commit-versus-cancel semantics on every send route, including whether
  the terminator is included per route · §11 · static trace.
- Outcome transition timing · §11 · static trace.
- Pause authorization and forwarding authority for chat, pause, and speed
  packets in multiplayer · §11 · static trace. Out of implementation scope.


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

- Serial and modem UI validation, lobby timeout progression, and the complete
  ready/start protocol · §12 · static trace. Multiplayer-only, out of
  Nanolathe's implementation scope.
- Map-preview camera behavior · §12 · static trace.
- Role separation of shared player-word bit `0x20` between READY display and
  map-control authority; both consumers are proven and the semantics are not
  separable statically · §12 · manual retail observation.


## Missing and unknown

Open items only. Each bullet states what is unknown, the section that owns it,
and the decider that would close it.

- Focus traversal for windows with more than 49 controls depends on incompletely initialized canonical-coordinate scratch · [R-WGT-01 §2] · bounded initialization/lifetime trace or manual custom-window observation.

### Input and text

- A rooted invocation of the State/Builder probe painters and the cross-battle
  lifetime of their retained targets/shared panel cache · [R-CAM-01 §9] ·
  indirect-call and reset ownership trace, or manual observation identifying
  the exact retail build and activation sequence.
- Producer semantics of diagnostic footer `PFSTATE`, `PFABLE`, and the three
  `PACKETS` counters · [R-HUD-03 §1] · bounded producer/update trace.

- Which front-end screen paths handle which key tokens (the battle census is
  [R-CAM-01 §2]), and the unsupported-device census · §2, §5 · static trace.
- The user-facing name of the F4 toggle; the `+MakePoster` argument grammar ·
  §2 [R-CAM-01 §2, §6] · static trace / manual retail observation (developer
  tooling, low priority; no implementation decision turns on either).
- How the multiplayer receive path applies the lobby `Cheat Codes` bit before
  re-dispatching a received `+` line · §5 [R-CAM-01 §6] · out of scope.
- Text-input code page and IME behavior · §2, §7 · presentation-level platform
  detail; no retail contract observed beyond the ASCII token set
  (`TODO(T23)`).
- Portable clipboard text-to-byte mapping · §2 · the retail `CF_TEXT` paste
  operation is established, but its code page must be identified before a
  Unicode host clipboard can reproduce it (`TODO(T25)`).
- Whether the character values of the key-token producer hold on a non-US
  keyboard layout (Supported inference) · §10 [R-CAM-01 §14] · manual retail
  observation.
- Chat commit-versus-cancel semantics on every send route, including whether
  the terminator is included · §11 · static trace.
- Translation-table missing-key rules and the complete translation lookup
  fallback · §7 · static trace.
- Text wrapping beyond the two closed wrappers ([R-FE-02 §6],
  [03 R-FONT-01 §6]) — whether any other caller wraps · §7 · static trace.
- Language-specific font fallback order beyond the closed
  missing-HATTFONT-to-active-FNT path · §7 · asset census.
- Malformed HATTFONT (no `I` frame) handling · §5, §7 · undefined in retail
  ([03 R-FONT-01 §6]); asset census.

### Widgets and screens

- User-facing naming of every GUI mode/flag bit; `0x800` (extra redraw on
  close) and `0x1000` (modal centering) are mechanically named · §3 · static
  trace.
- The meaning of the window key-navigation flag's clear state per front-end
  screen · §3 · per-screen static trace.
- The record-list item structures beyond their known height path · §4
  [R-WGT-01 §4, §5] · static trace.
- Whether the label under a briefing blink word also draws the run, or
  elides it · §5 [R-FE-02 §7] · static trace of the pager's copy loop.
- The per-window census of authored gadget association ids · §4, §9, doc 02 §6
  · asset census.
- Process-level outcome of malformed HATTFONT or malformed GAF payloads whose
  decoders return null · §5 "Frontend asset failure boundaries" · static
  trace.
- Battle HUD optional-asset fallback beyond the closed `intgaf` panel entries,
  side fonts, authored GUI page, page GAF, support GAF, and common-button
  resolution · §6 · static trace.
- The `CORBUILD` gadget-name exclusion in the build card · §6 [R-HUD-03 §3] ·
  asset census.
- Whether any content authors a `<unit>0.GUI` page for the authored-page bit ·
  §6 [R-HUD-03 §6] · asset census.
- The authored width of the in-battle `PREFS` window (decides whether the
  options unfold's second quad form and `LIGHTBAR` stamp are reachable) · §6
  [R-HUD-04 §2] · asset census.
- Whether the message ring's backing memory is zero-initialised at session
  start (decides whether the drawer's second text colour, `dcb[10]` on
  class-byte bit 5, is reachable) · §6 [R-HUD-03 §14.4] · static trace of the
  session-init clear.
- Whether a mission script can post to the message ring, or whether campaign
  objective text has its own path · §6 [R-HUD-03 §14.4], doc 08 · static
  trace of the mission-event text producers.

- Extended-byte alphabetic classification in authored `quickkey` values ·
  [R-WGT-01 §11] · establish the platform classification and locale used by
  that parser call. ASCII parsing and the later collision fold are settled.

### Picking, selection, and orders

- The committed pick record: the `HOT UNITS` producer's list rank and its
  viewport/visibility admission result · §8 [R-REV-01 §6] · a design pass
  over the publication boundary (`TODO(question)`). A pixel-for-pixel
  replacement of the producer is blocked on it; the hull test and the score
  reduction are not.
- Whether a stock aircraft always outscores the stock buildings it can fly
  over (Supported inference) · §8 [R-REV-01 §9] · a census of
  `FootprintX`/`FootprintZ` and model heights over the stock definitions.
- Whether the `HOT UNITS` producer runs in the frame/presentation update, and
  whether the two low status bits it tests are the movement-mode bits
  (Supported inferences) · §8 [R-REV-01 §5] · a caller census of the
  producer; a writer census of that status word.
- The selection rectangle's clip-left value for every visible/hidden-panel
  state · §8 [R-SEL-02A] · a focused mode/panel capture recording the surface
  descriptor at the selection draw.
- Feature-versus-unit pointer priority; features are absent from the unit
  hover list and reclaim families resolve them separately at the pointer · §8
  · static trace.
- Remaining command-specific cursor validity rules · §8 · static trace.
- Manual unit and point target encoding, command-fire replacement, and the
  manual-versus-autonomous latch callers · §9, doc 06 §3.2 · static trace.
- Whether any retail path draws the sweeping build-site lines with Shift up
  (a play observation contradicts the traced Shift gate) · §9 [R-P0-11 §3] ·
  a retail session with the Shift key state observed, not recalled.

### Camera, minimap, and session UI

- Camera clamp behavior when the viewport span exceeds the map size on an
  axis; the ordered clamp form is the only established behavior · §10
  [R-CAM-01 §13] · static trace.
- Whether any transient follow-target or shake state is reconstructed from a
  non-`Camera` save account · §10, doc 08 · static trace.
- Start-position markers · §10, doc 03 · static trace.
- Outcome transition timing · §11 · static trace.
- Campaign continuation timing · §5, doc 08 · static trace.
- Role separation of shared player-word bit `0x20` between READY display and
  map-control authority; both consumers are proven and the semantics are not
  separable statically · §12 · manual retail observation.
- Multiplayer pause authorization, speed UI synchronization, chat/pause packet
  forwarding authority, serial/modem/TCP setup semantics, lobby timeout
  progression, and the complete ready/start protocol · §11, §12 · static
  trace. Out of Nanolathe's implementation scope (no multiplayer).
- Map-preview camera behavior · §12 · static trace.
