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
`0x19`). OEM ranges `0xBA..0xC0` and `0xDB..0xDE` remain unknown table aliases.

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
special-key submaps, all OEM punctuation aliases, user-remappable controls,
keyboard repeat policy, text-input code page, IME behavior, and the internal
consumers of mouse motion records are not established. The mouse button record
model (including double-click fields and queue refusal), middle-button/wheel
default processing, drag/double-click capture into timestamped records, and
the `CF_TEXT`-only clipboard contract are established above.

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

The user-facing naming of every GUI mode/flag bit (including the
extra-redraw flag `0x800`), the complete per-dialog Escape/Enter/
focus-restoration matrix, default-control rules for every panel, event
bubbling between parent and child panels, whether keyboard focus can be
shared by a list and textbox, and complete overlap/capture/association
redirection precedence remain unknown. Top-object close, predecessor
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

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

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

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

### Retail palette contract

The retail renderer has one active 256-entry display palette for the indexed
surface: `PALETTE.PAL`. Frontend backgrounds, GAF widgets, FNT glyphs, HUD
art, terrain, minimap pixels, and direct indexed primitives are eventually
presented through that table and the current 256-byte logical-to-physical
lookup. `GUIPAL.PAL` is a frontend semantic color-field palette retained by
the GUI bootstrap;
it is not installed as a second physical display palette. This distinction is
material because the two retail files have different color ordering.

**Publication omission:** Historical executable-analysis detail omitted from this public edition.

### Retail frontend control activation and raster rules

The following rules are established for the frontend controls in this slice:

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

The stock common frame dimensions used by these controls are stable in the
retail resource set: `BUTTONS0` groups are `16×16`, `321×20`, `120×20`,
`96×20`, `112×20`, `80×20`, and `96×31`, each repeated as a four-frame group;
`LISTBOX` has nine `16×16` tiles; and `SLIDERS` has ten vertical frames and ten
horizontal frames. The staged common entries used by this menu are
`stagebuttn2` (five `120×20` frames) and `stagebuttn3` (six `120×20` frames).

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

Campaign and map controls also retain data-driven display behavior. The
campaign list is rebuilt from the discovered campaign documents and filters
the `HEADER campaignside` value to the selected side, accepting `ALL`. For
New Campaign, when the installed campaign set has two or fewer entries, the
campaign and mission list controls are hidden and the side-specific
`Arm Campaign` or `Core Campaign` file is selected directly. Play Any uses the
same `newgame.gui` but exposes the campaign and mission lists and applies the
retail compressed list rectangles at runtime.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

The main-menu `EXIT` callback is a direct close transition. The separate
`YESORNO.GUI` text `Close Windows CD Player?` belongs to frontend
initialization cleanup, not to the main-menu quit button. [07 §5]

#### The loading screen

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

The repainted composition, in the order it is drawn:

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

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
dword is nonzero. The `+` command vocabulary itself is not inventoried.

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
its GAF offsets are not added. The command-panel
GUI window `guis/<prefix>main.gui` is the underlying battle/root window, not
the empty-selection command page. When the selected-unit count becomes zero,
the command-window switch formats and opens `guis/<prefix>gen.gui`. A
non-builder selection uses that same general page; a selected builder uses its
authored `guis/<unit>1.gui` page. Named page art is resolved from
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
truncated integer `K` suffix outside the inclusive `-99999..99999` range;
metal production and consumption use one fractional digit. Consumption is
shown with a negative sign. Normal text uses logical palette entry 15,
production entry 10, and consumption entry 12. Production/consumption values
are latched every 30 simulation ticks; the current-over-capacity bars remain a
live presentation of committed stock.

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
The minimap viewport rectangle thickness varies between 6 and 4 pixels with
latch-flag bit `0x40`.

### Supported inference

Battle chrome should be data-driven from side-data, while semantic values and
command availability remain runtime state. A generic GUI implementation may
share drawing primitives, but must retain the side-data anchor contract.

### Unknown

All invoked retail anchors and their tuple order are established above; slide/modal
combinations and per-side fallback for optional presentation remain incomplete.

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

**The shape chooser is closed.** One pointer update resolves the shape in four
steps. First, two region bits record whether the pointer is over the world
viewport or over the minimap; when neither is set the index is forced to
`cursornormal` and nothing else is consulted. Second, the mobile-build latch
with a live placement ghost takes the site-validity branch above. Third, the
selected units of the local player — walked over the owner's inclusive range at
the fixed 280-byte stride, admitted by the membership bit `0x10` — are each
asked for a shape, and **the lowest index wins**, so the table's numbering is
also its shape priority order. Fourth, when that candidate set is empty the
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

Queued orders and path previews use separate cursor/indicator artwork. The
cursor validity state is therefore a presentation of command legality, not
just a pointer shape.

### Supported inference

Picking should return a typed hit result with ownership/visibility metadata,
then let the active command mode choose whether a unit, feature, terrain
cell, minimap, or GUI control consumes the action.

### Unknown

Exact geometric picking hulls, object priority in overlap cases, fog-edge
behavior, cursor handle slot 0 identity, and queued-line palette-entry color
values are not completely recovered. The named-entry index table, the hotspot
convention, the four-step shape chooser with its lowest-index-wins reduction,
the per-latch shape table, and the build-site validity/ghost cursor selection
are established above.

One branch of the idle latch remains unresolved. A global byte, distinct from
the latch and from the region bits, diverts the idle path to a two-shape
answer: `cursorred` over a hostile target and `cursorgrn` over a friendly one,
after the same own-finished-unit test that otherwise gives `cursorselect`. The
byte's writer has not been located, so the condition that arms this branch is
unknown and it is not implemented.

The runtime active-state bit `0x20` and the empty-current-task field that the
own-unit inspect predicate also tests are established for retail but have no
counterpart in the current runtime flag word (see `docs/SPEC_CONFLICTS.md`).

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
single-precision value compared equal to `1.0`; no disqualifying state
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
definition id — changes which matching units remain selected.

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
bit `0x20`. Latch values `0xB` (TELEPORT) and `0xE` (MOBILEBUILD) are armed
by other reviewed paths, not by the order-button dispatcher.

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

Queued build indicators use unit/build GAF artwork and numeric queue state.
Selection changes can update the side panel, build page, command palette,
health bars, unit name, and queued-order cursor indicators.

### Supported inference

The command system should retain a canonical semantic order object from input
through local execution, cursor validity, HUD help, and network serialization.
This avoids divergent behavior between mouse clicks, keyboard shortcuts, AI
orders, and multiplayer packets.

### Unknown

Repeated group-recall centering behavior and the writer lifetime of selection
flag `0x80000000`, hull geometry and jammer versus radar-contact picking,
page rebuild timing versus factory completion,
and the exact arming trigger for the two off-button latch values remain
incomplete. The drag-rectangle toggle truth table, eligibility predicate,
overlap pick order with strict `<` tie-break and inclusive `min <= x <= max`,
fog word versus byte gate, toggle versus held-Shift styles,
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
* Cursor-to-world picking.
* Minimap interaction and camera state.
* Fog/visibility presentation.

The minimap has a fixed square logical canvas with aspect-letterboxing. If a
pre-baked radar image is unavailable, the engine builds a temporary higher-
resolution image, rescales it, and composites it into the minimap region.
The input path reads the minimap/edge region and updates camera state.

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
`worldX=(mouseX-padX)*mapWidth/radarWidth`,
`worldZ=(mouseY-padY)*mapHeight/radarHeight`,
`cameraX=worldX-viewWidth/2`, `cameraZ=worldZ-viewHeight/2` and then follows the
standard movement/clamp path. An alternate drag/current-camera branch exists
when the direct predicate fails or a battle-mode bit is set; its boundary
vectors are not reduced to a standalone truth table. No zoom/rotation mutation
occurs in the reviewed edge-scroll, direct-radar, clamp, or save/load paths.
Minimap rendering and visibility masks are separate concepts.

### Supported inference

Camera and minimap conversion should remain an explicit compatibility
boundary. The current evidence does not prove that world rendering, picking,
and minimap input all share one conversion routine.

### Unknown

Exact camera bounds, clamp behavior at map edges, zoom/rotation support,
terrain-height projection, click-vs-drag thresholds, and radar/visibility
update cadence remain incomplete. Minimap generation (126-pixel canvas, 2x
supersample, picture/temp/mapped/final surfaces) is closed above; per-contact
color rules beyond the dedicated palette entries are not.

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

**Tab options menu and manual exit.** In a non-network battle, the Tab key
opens the hard-coded `guis/armopt.gui` window with `anims/armopt.gaf`; this
name is not side-prefixed. Opening it sets both the battle modal bit and the
single-player pause bit, and pauses the runtime audio path. Closing the root
window clears the modal and pause bits and resumes audio. A second Tab while
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

Pause is represented by a runtime state that suppresses simulation progress
and causes an `igpaused` title overlay to be drawn. Victory/defeat overlays
come from the `igtitles` GAF family — handles `igvictory`, `igdefeat`, and
`igpaused` — gated by mode-word bits: victory on bit 5 of one mode word,
defeat on bit 6 of it, pause on bit 0 of the pause-mode word. Victory/defeat
states later transition to end-mission/endgame report screens. Tab's local
options-window pause path is established above; the separate Pause-key path
remains outside this closure.

Game-speed changes are clamped to the retail range and displayed as localized
messages. In multiplayer, speed changes are represented as networked semantic
commands rather than purely local UI changes.

**End-mission layout and statistics cadence are closed.** The end-mission
screen is built as a message-box-style surface: the last game frame is copied
beneath the literal header strings `"Copy of last game frame"` and `"Click to
continue."` (the latter drawn after a delay gate). Statistics rows are
formatted per player (58-byte rows, up to ten players) with the label strings
`Kills`, `Losses`, `EProduced`, `MProduced`, `EWasted`, `MWasted`, `Score`,
gated per player by an enable-byte table. Statistics animate seven categories
one at a time behind a +10-tick gate, advancing the category substate `0→6`
(Kills, Losses, EProduced, MProduced, EWasted, MWasted, Score) with the
`EndGameStatBar` cue per category and `EndGameScore` for score.

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
incomplete. The chat open/send contract, the scrollback ring, the overlay
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

* The complete Win32/internal token table beyond the proven held-key and
  special-key submaps, all OEM aliases, unsupported-device census, and every
  battle/front-end consumer.
* Mouse motion-record consumer internals and downstream ownership of button
  records beyond queue admission (button down/up/double-click records,
  reserved-slot refusal, and middle-button/wheel default processing are
  established).
* Text-input code page, IME behavior, chat commit-versus-cancel semantics on
  every send route including terminator inclusion, scrollback drain ownership
  in the in-battle HUD, and the `+` command vocabulary (CF_TEXT-only
  clipboard and paste/truncate bounds are established).
* GUI mode/flag-bit naming (including extra-redraw flag `0x800`), the
  complete per-dialog Escape/Enter/focus-restoration matrix, event bubbling,
  and overlap/capture/association redirection precedence (top-object close,
  predecessor reactivation, token suppression range, and hit-test bounds are
  established).
* Listbox, scrollbar, font, and picture-box behavior beyond the observed
  common paths, and the complete widget callback map. The `.gui` grammar,
  field lengths, control defaults, control-kind parsing, and runtime
  control-kind dispatch are established.
* Exact frontend transition-graph edges (successor/cancel/error per screen
  family), movie/intro/credits handling, load-failure restoration, and abort
  recovery (controller phase/substate mechanism, post-battle phases, planet
  briefing tables, and chat contract are established).
* Complete HUD side-anchor to draw/hit-test consumer mapping, provider/
  palette/font/art failure policy, and slide/modal combinations (anchor list,
  tuple order, slide animation, and frame-composition passes are established).
* Exact battle HUD optional-asset fallback behavior beyond the closed
  `intgaf` panel entries, side fonts, authored GUI page, page GAF, support GAF,
  and common-button resolution above.
* Full FNT text wrapping, drop-color defaults, code-page behavior, font
  fallback order, and translation-table missing-key rules (truncate-before-
  clip and the presentation-context drop-shadow switch are established).
* Complete translation lookup and missing-string fallback behavior.
* Exact cursor hotspots, remaining command-specific validity rules, cursor
  handle slot 0 identity, and queued-line palette-entry colors (latch-value
  table, cursor index table, and build-site validity cursors are established).
* Picking hull geometry, feature versus unit priority in exact overlap, and
  fog-edge versus jammer or radar-contact interaction beyond the local-player
  word versus per-viewer byte gate.
* Selection overlap pick order — drag endpoints sorted independently, inclusive
  `min <= x <= max` tested per axis, stable pool sweep with strict `<` distance
  tie-break favoring lower slot [P1-14] — and fog word bit versus byte
  distinction, toggle versus commit modifier styles (`GetAsyncKeyState`-style held
  query for world commit versus drag word bit 2), and mixed-selection gate as
  AND across the selected set [P1-14] are established; what remains is repeated
  group-recall centering behavior and the writer lifetime of selection flag
  `0x80000000`.
* Build cancellation and refund — tail-most matching walk with tombstone bit
  that skips `TargetCleared`, with `BuildWeapon` and `SelfDestruct` always
  tombstoned because the tombstone compares against the front anchor regardless
  of segment [P1-14] — and queue-modifier mapping (Replace purges unprotected,
  Append and Shift-queue both insert after the active marker without purging,
  Internal-Auto is the pump's head-insert) plus attack-ground versus
  unit-target discrimination (ATTACK picks unit when hit and hostile, otherwise
  ground; BLAST always ground) [P1-14] are established; what remains is the
  exact arming trigger for the two off-button latch values (TELEPORT and
  MOBILEBUILD placement preview versus click) and the producers for interrupt
  masks 2 and 8.
* Order-class semantics for immediate and special commands are established via
  the fixed dispatcher chain STOP into ATTACK, BLAST, DEFEND, REPAIR, PATROL,
  RECLAIM, CAPTURE, UNLOAD, LOAD or PICKUP alias, and default MOVE with gate
  check, and latch values for TELEPORT and MOBILEBUILD as off-button consumers
  of the same 14-entry table [P1-14]; what remains is the exact writer site
  that arms those two off-button values (placement preview region) and minor
  OEM aliases.
* Build-page patching and rebuild timing — page-number bit encoding
  `(page & 7) << 23` cleared by mask `0xFC7FFFFF` with bit 22 paged indicator
  cleared by `0xFFBFFFFF`, and page-count guard are established [P1-14]; what
  remains is dirty-scope versus factory completion and indicator animation
  details.
* Alternate minimap drag/current-camera branch boundary vectors and mode-bit
  truth table, unusual-domain camera bounds, terrain-height projection, and
  all dirty-field semantics (core clamp, edge predicates, direct radar
  conversion now established).
* Minimap/radar colors beyond the dedicated contact palette entries and
  visibility update cadence.
* Running-display refresh cadence and hover ownership (30-entry fixed-stride
  scrollback ring and heartbeat drain are established).
* Multiplayer pause authorization, speed UI synchronization, chat/pause
  packet forwarding authority, and the role separation of shared player-word
  bit `0x20` between READY display and map-control gate (both consumers
  proven; unified semantics not separable statically).
* TCP/modem/serial setup semantics, timeout rules, and map-preview behavior.
* Campaign continuation timing and the endgame per-value bar-fill animation
  mechanism (message-box surface, header strings, per-player stat rows, and
  the seven-category +10-tick statistics cycle are established).
