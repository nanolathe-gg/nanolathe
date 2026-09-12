# GUI — Menu and Screen Layouts (`.gui`)

## Overview

**Established** `[02 §6 "Interface panel files"]`, `[07 R-WGT-01]`.
Ordinary `.gui` files in the `guis/` directory define menu and in-game panels:
the main menu, battle rooms, dialogs, and the side command bars used during
play. A normal GUI file is a text file in the general TDF syntax
([tdf.md](tdf.md)) describing a list of **gadgets** — buttons, listboxes,
text fields, scrollbars, labels, dynamic picture surfaces, fonts, and
picture boxes.

Graphics come from a GAF file with the same base name as the GUI
(`MAINMENU.GUI` ↔ `anims/MAINMENU.GAF`) plus the shared
`anims/commongui.GAF`; fonts come from `fonts/*.fnt`. Behavior is largely
hard-coded: a gadget's `name` is matched against engine-known event names
per menu (e.g. `SINGLE`, `MULTI`, `EXIT` in the main menu). Generic widget
behavior (selection, scrolling, linked labels and radio groups)
still applies without a screen-specific event binding `[07 R-WGT-01 §§3–7]`.

**Established — implementation policy.** The installed corpus includes a
binary `ENDGAME.GUI` and truncated text `SCORE.GUI`. `formats.LoadGUI`
extracts fallback labels from the recognized binary signature and repairs one
missing final brace when an unterminated text section ends in a semicolon.
These are Nanolathe recoveries, tested in `formats/source_test.go`; they do
not establish that retail accepts either file through its ordinary panel
loader. That reader uses the fatal TDF syntax path `[02 R-MALF-01 §5]`.

## Format at a glance

```
[GADGET0]                  ← first gadget = the interface itself (id=0)
    {
    [COMMON]               ← fields every gadget has
        { id=0; name=Mainmenu.GUI; xpos=0; ypos=0; width=640; height=480; ... }
    totalgadgets=6;        ← gadget-specific fields follow COMMON
    [VERSION]
        { major=-51; minor=-51; revision=-51; }
    panel=; crdefault=; escdefault=; defaultfocus=SINGLE;
    }
[GADGET1]                  ← subsequent gadgets = the interface's elements
    {
    [COMMON]
        { id=1; name=SINGLE; xpos=139; ypos=393; width=96; height=20; ... }
    status=0; text=SINGLE; quickkey=83; grayedout=0; stages=0;
    }
```

(Abbreviated illustration based on `guis/MAINMENU.GUI`; ellipses and
annotations are explanatory, not literal file contents.)

The numeric suffix in `[GADGETn]` is cosmetic — the engine accepts `[]` or
any bracketed name; order in the file is what matters. The first gadget
describes the whole interface (position, size, background); each later
gadget is one element of it.

## Reference

### `[COMMON]` fields (all gadget types)

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | int | Gadget type — dispatches everything else. Known: 0 header, 1 button, 2 listbox, 3 textbox, 4 scrollbar, 5 label, 6 blank surface, 7 font, 12 picture box. **Stored as one byte** (the low eight bits of the integer), so the engine dispatches on `id mod 256`. The executable also knows `8` (raw file, below), `10` (line, below), `11` (built exactly like a header/panel) and `13` (the end-of-mission score bar, created by the engine, never authored); `9` and everything above 13 read only `[COMMON]` and get no build work. Full per-kind key table: [07 R-WGT-01 §11]; builder dispatch: [07 R-WGT-01 §12]. |
| `assoc` | int | Association key linking gadgets, and most gadget kinds do use it. A listbox and scrollbar sharing `assoc` are wired together (listbox drives knob size, scrollbar scrolls list); buttons with the radio attribute use it as their group; a slider's synthesised arrow buttons carry it; a listbox copies its selection to same-`assoc` listboxes and (attribute 8) to a same-`assoc` textbox. See the executable spec [07 R-WGT-01 §3, §5]. |
| `name` | string | Dual purpose: (a) graphic lookup — the name of a GAF entry in `<menu>.GAF` or `commongui.gaf`, falling back to default art for the type/size; (b) event binding — hard-coded per-menu event names attach behavior. `HELPTEXT` is a universal name: a label so named shows hover help text. |
| `xpos`, `ypos` | int | Position in pixels (640×480 space). The first gadget is clamped so the interface stays on-screen. |
| `width`, `height` | int | Authored size in pixels. Type-specific builders can replace dimensions from resolved art; scrollbar orientation follows the long axis [07 R-WGT-01 §§3–5, §12]. |
| `attribs` | int | Type-dependent bit field. Scrollbars need `1` = horizontal, `2` = vertical. The executable's bit meanings for buttons (radio `0x10`, toggle `0x40`, cycle `0x100`, auto-repeat `0x2000`, keep-authored-quickkey `0x10000`), listboxes (text list `0x10`, fire-on-click `0x40`, heading-reject `0x200`) and the alignment bits are in the executable spec [07 R-WGT-01 §§3–5]. |
| `colorf`, `colorb` | int | Stored as 16-bit values. Meaning depends on the painter: semantic GUI colors, direct FNT palette colors and GAF light-table rows are distinct. The builder clears `colorf` for buttons, labels and pictures; later service uses it as flash state. See [03 R-FONT-01 §6], [07 R-WGT-01 §12]. |
| `texturenumber` | int | No observed effect |
| `fontnumber` | int | Stored as a byte and read signed; selects the zero-based kind-7 font in file order. A negative value or missing indexed font selects the common font. Labels can use the selected FNT directly; button/list/input painters still use the GAF text path when present [07 R-WGT-02 §4], [03 R-FONT-01 §5]. |
| `active` | int | Stored as a byte; zero hidden, nonzero serviced [07 R-WGT-01 §1]. |
| `commonattribs` | int | Stored as a byte. Battle count labels test bit `0x04` for build-product counts, then bit `0x08` for stockpile counts [07 R-P0-11 §2]. |
| `gaffile` | int | Stored as 16-bit; parsed even though absent from the historical authored-key survey [02 §6 "COMMON"]. |
| `help` | string | Hover help text; the window-build pass copies it into the `HELPTEXT` label [07 R-WGT-01 §1] |

**Established.** Common integer keys default to zero and strings to empty
when the subsection exists `[02 §6 "COMMON"]`. Values such as `-51` and
`52685` occur in authored data, but there is no general "unset" conversion.
Retail narrows to the destination byte/word; positions are signed 16-bit and
have their own centering/edge rules. Preserve the authored value until that
boundary. `internal/gui/load_record_store_test.go` locks these stores.

### Missing `[COMMON]` subsection

**Established.** The panel loader retains every top-level section in file
order and reads common fields only when that subsection exists
`[02 R-MALF-01 §5]`. A missing subsection therefore does not make an otherwise
parseable panel fail.

**Nanolathe deterministic policy.** `formats.LoadGUI` preserves the raw
section and marks that it has no common record. `internal/gui.Load` starts the
compiled record zero-initialized and skips every common-field store in that
case. This deliberately does not claim a value for retail's uninitialized
record bytes.

### Header gadget (`id=0`) extra fields

| Field | Meaning |
| --- | --- |
| `totalgadgets` | Declared element count; has no observed effect |
| `panel` | Background art: GAF entry name via the same lookup rule as `name`. Empty when the screen uses a PCX background. |
| `crdefault` | Button triggered by Return (name). The window-open routine resolves the name by a forward scan and, when the key is empty, binds Return to the first button whose name begins `OK` or `NEXT` (case-insensitive prefix), and likewise an empty `escdefault` to the first button beginning `PREV` or `Cancel` — see the executable spec [07 R-FE-01 §12] |
| `escdefault` | Button triggered by Escape (name) |
| `defaultfocus` | Gadget name that starts focused |
| `[VERSION] { major=; minor=; revision=; }` | Present in the historical 368-file text-GUI survey, but **optional in the parser**: the executable's panel-header loader seeks the subsection and skips it silently when absent, leaving the three byte fields zero (see the executable spec doc 02 §6). Values are arbitrary in retail files. |

### Button (`id=1`)

| Field | Meaning |
| --- | --- |
| `status` | Stored 16-bit down-state word, not a general GAF frame index; stage and frame selection are [07 R-WGT-01 §3]. |
| `text` | Label text. Multi-stage buttons separate per-stage text with a vertical bar (e.g. `text=On\|Off;`) |
| `quickkey` | Keyboard accelerator as an ASCII code (`83` = `S`); a bare symbol also occurs in data. The authored value is normally discarded: the executable overwrites it at window open with the first free letter of the label unless `attribs` bit `0x10000` is set — see [07 R-WGT-01 §3] |
| `grayedout` | `1` = visible but disabled. Stored as bit 0 of the button's own grey word — **not** an `attribs` bit — and tested by the button handler at press time, so a greyed button still shows hover help [07 R-WGT-01 §13] |
| `stages` | Number of stages for cycle buttons (0 = plain). `stages=1`, or a label of exactly `Off\|On`, is promoted to 2 stages with the `stagebuttn1` art [07 R-WGT-01 §3] |

Stock button GAF entries have frame 0 = rest, frame 1 = pressed, frame 2 =
disabled. Example from `MAINMENU.GUI` above: the `SINGLE` button.

### Listbox (`id=2`)

Content is filled by the engine (games list, map list). Pair with a
scrollbar via `assoc`.

| Field | Meaning |
| --- | --- |
| `itemheight` | Row height in pixels (rare — 7 occurrences in retail data, undocumented historically); when 0 the row height is the font line metric + 1 [07 R-WGT-01 §4] |

### Textbox (`id=3`)

| Field | Meaning |
| --- | --- |
| `maxchars` | Maximum text length; 128 parser cap, then 127 builder cap [07 R-WGT-01 §§11–12]. |
| `text` | Localized caption read by the parser; the builder clears the text-input buffer [07 R-WGT-01 §§11–12]. |

No border art — backgrounds provide the visual frame.

### Scrollbar (`id=4`)

| Field | Meaning |
| --- | --- |
| `range` | Knob travel in pixels (`knobpos` runs `0..range−1`), not an item count; the engine overwrites it for assoc-driven bars and for horizontal bars with `SLIDERS` art [07 R-WGT-01 §5]. |
| `thick` | Integer narrowed to signed 16-bit then widened to 32-bit. Numeric range of the value label a scrollbar with `attribs` bit 4 draws beside itself (`trunc(knobpos × thick / (width − knobsize))`) — see [07 R-WGT-01 §5] |
| `knobpos` | Knob position within range (engine-driven) |
| `knobsize` | Knob size (engine-driven when assoc'd) |
| `text` | Localized text, read after the numeric scrollbar fields [07 R-WGT-01 §11]. |

Requires `attribs=1` (horizontal) or `2` (vertical) matching its shape.

### Label (`id=5`)

| Field | Meaning |
| --- | --- |
| `text` | Displayed text (engine may replace it for event-named labels) |
| `link` | Name of a button; clicking the label acts as clicking that button |

### Blank surface (`id=6`)

Dynamic picture area filled by the engine (minimap previews, save-game
screenshots) when its `name` matches the menu's expected event name
(e.g. `MAPPIC`).

| Field | Meaning |
| --- | --- |
| `hotornot` | `1` = can take the focus rectangle, `0` = not |

### Font (`id=7`)

| Field | Meaning |
| --- | --- |
| `filename` | Font resource name without extension (`filename=SMLFONT;` → `fonts/SMLFONT.FNT`) |

Supplies one indexed font for gadgets selecting it with `fontnumber`.
The loader reads `filename`
into a 32-byte field and the window builder opens
`fonts\<filename>.FNT` (directory prefix and extension added by the engine)
[07 R-WGT-01 §12].

### Raw file (`id=8`) — engine-known, never authored

| Field | Meaning |
| --- | --- |
| `filename` | Same 32-byte field as the font gadget; the builder opens the name **verbatim** (no directory, no extension) and loads the whole file. Nothing reads it afterwards — the kind has no runtime behaviour [07 R-WGT-01 §8, §12]. |

### Line (`id=10`) — engine-known, never authored

| Field | Meaning |
| --- | --- |
| `nuttin` | Integer, stored as a 32-bit word in the text field; the painter does not consume it. The line itself is drawn from `attribs`: `1` horizontal (`(x,y)–(x+w−1,y)`), `2` vertical (`(x,y)–(x,y+h−1)`), `4` a single diagonal line across the gadget's rectangle (`(x,y)–(x+w−1,y+h−1)`, not a four-sided rectangle outline — "outlined" is the bit's name, not the shape it draws), in the gadget colour (`colorf` as a window colour-table row) [07 R-WGT-01 §8]. |

### Picture box (`id=12`)

Static picture; `name` selects the GAF entry to display. No specific
fields.

## Runtime notes

- The engine mutates gadget state at runtime (text, visibility, frames) for
  event-named gadgets; authored values are only initial state.
- In-game command bars (`ARMGUI.GUI` etc.) use the same format; the order
  and build buttons (`*MOVE`, `*STOP`, `*ATTACK`, unit build buttons) are
  event names resolved per side with the `nameprefix` from
  `gamedata/SIDEDATA.TDF`.
- Every unit's first command page carries the same command gadgets whether
  or not the unit can use them; availability is enforced by game logic, not
  by the GUI data.
- **Loader edges (`[02 R-MALF-01 §5]`).** The panel
  loader walks the top-level sections **by index in file order** — the
  `GADGET<n>` names are not consulted — and `totalgadgets` is read and then
  overwritten with the number of sections minus one (inert as authored).
  `[COMMON]` is read only when present (an absent one leaves the gadget's
  common fields unwritten); kinds 0–8 and 10 read their extra keys, any
  other `id` reads only `[COMMON]`; a text box's `maxchars` is capped at
  128 by the parser and at 127 again by the window builder (the effective
  cap). The complete per-kind key table with every width is
  [07 R-WGT-01 §11]. Retail does not check the gadget count against its
  fixed window capacity; an excessive count can overrun that storage. A
  syntax error is fatal like any TDF; a missing panel file returns failure
  to the screen.

## Retail corpus notes

**Established — bounded corpus observation.** A historical survey of 368
text interfaces (5,840 gadgets, base + patch + expansions) found the type-ID set
{0, 1, 2, 3, 4, 5, 6, 7, 12} — no other IDs occur — and the field lists
describe the observed keys, including rare `itemheight` (listboxes) and five
occurrences of `crtdefault`, which is a retail typo for `crdefault`
(parsers should tolerate unknown keys for exactly this reason). Buttons
dominate (4,421 of 5,840 gadgets).

## Unknowns and caveats

- **Unknown:** behavior for `texturenumber` and uncited `gaffile` consumers;
  a bounded consumer trace would settle it. Storing a key does not prove it
  affects rendering.
- **Unknown:** any screen event bindings or attribute bits not covered in
  doc 07; compare the screen dispatcher with the authored controls to close
  a specific gap. A GUI-file census alone cannot enumerate engine-only names.
- **Established — implementation mismatch:** `internal/gui.Load` currently
  reads `thick` directly into a 32-bit value, whereas [07 R-WGT-01 §11]
  specifies a 16-bit store before widening. Its nonnumeric `quickkey`
  fallback also accepts any first byte, whereas retail distinguishes letters
  from decimal conversion. These are code follow-ups, not format variants.

## Sources

- *GUI File Format* v1.0 by Dark Rain, TA Design Guide — historical
  community description from trial-and-error experiments:
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/guidesc.htm>
- Retail behavior: `[02 §6 "Interface panel files"]`, `[02 R-MALF-01 §5]`,
  `[07 R-WGT-01]`, `[07 R-WGT-02 §4]`, `[03 R-FONT-01 §§5–6]`.
- Nanolathe implementation and authored tests: `formats/gui.go`,
  `formats/gui_test.go`, `formats/source_test.go`, `internal/gui/load.go`,
  `internal/gui/load_record_store_test.go`, `internal/gui/font_test.go`.
