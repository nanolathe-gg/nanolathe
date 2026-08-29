# GUI — Menu and Screen Layouts (`.gui`)

## Overview

`.gui` files in the `guis/` directory define every menu and in-game panel:
the main menu, battle rooms, dialogs, and the side command bars used during
play. A normal GUI file is a text file in the general TDF syntax
([tdf.md](tdf.md)) describing a list of **gadgets** — buttons, listboxes,
text fields, scrollbars, labels, dynamic picture surfaces, fonts, and
picture boxes.

Graphics come from a GAF file with the same base name as the GUI
(`MAINMENU.GUI` ↔ `anims/MAINMENU.GAF`) plus the shared
`anims/commongui.GAF`; fonts come from `fonts/*.fnt`. Behavior is largely
hard-coded: a gadget's `name` is matched against engine-known event names
per menu (e.g. `SINGLE`, `MULTI`, `EXIT` in the main menu). Names with no
hard-coded event render but do nothing.

The retail corpus also contains a binary `ENDGAME.GUI` resource and a
truncated text `SCORE.GUI`. The binary resource is not the ordinary TDF
gadget format; tools may expose its extracted labels as a fallback. The
truncated file can be recovered by closing its final gadget section.

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

(This is the real start of `guis/MAINMENU.GUI` from `totala1.hpi`,
whitespace preserved.)

The numeric suffix in `[GADGETn]` is cosmetic — the engine accepts `[]` or
any bracketed name; order in the file is what matters. The first gadget
describes the whole interface (position, size, background); each later
gadget is one element of it.

## Reference

### `[COMMON]` fields (all gadget types)

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | int | Gadget type — dispatches everything else. Known: 0 header, 1 button, 2 listbox, 3 textbox, 4 scrollbar, 5 label, 6 blank surface, 7 font, 12 picture box. |
| `assoc` | int | Association key linking gadgets. Confirmed effect: a listbox and scrollbar sharing `assoc` are wired together (listbox drives knob size, scrollbar scrolls list). **Correction (2026-08-29):** "Most gadgets ignore it" was too strong — buttons with the radio attribute use it as their group, a slider's synthesised arrow buttons carry it, a listbox copies its selection to same-`assoc` listboxes and (attribute 8) to a same-`assoc` textbox; see the executable spec [07 R-WGT-01 §3, §5]. |
| `name` | string | Dual purpose: (a) graphic lookup — the name of a GAF entry in `<menu>.GAF` or `commongui.gaf`, falling back to default art for the type/size; (b) event binding — hard-coded per-menu event names attach behavior. `HELPTEXT` is a universal name: a label so named shows hover help text. |
| `xpos`, `ypos` | int | Position in pixels (640×480 space). The first gadget is clamped so the interface stays on-screen. |
| `width`, `height` | int | Size in pixels. Ignored by types whose art dictates size (buttons, labels, picture boxes). Scrollbar orientation follows the long axis. |
| `attribs` | int | Type-dependent. Confirmed: scrollbars need `1` = horizontal, `2` = vertical. **Correction (2026-08-29):** "Other observed values … have no confirmed meaning" — the executable's bit meanings for buttons (radio `0x10`, toggle `0x40`, cycle `0x100`, auto-repeat `0x2000`, keep-authored-quickkey `0x10000`), listboxes (text list `0x10`, fire-on-click `0x40`, heading-reject `0x200`) and the alignment bits are in the executable spec [07 R-WGT-01 §§3–5]. |
| `colorf`, `colorb` | int | GUI semantic foreground/background palette fields; retail resolves them through the GUIPAL→PALETTE nearest-RGB map before primitive/FNT writes |
| `texturenumber` | int | No observed effect |
| `fontnumber` | int | Partially understood: nonzero reverts labels to the default font when a custom font gadget is present |
| `active` | int | `1` visible, `0` hidden |
| `commonattribs` | int | No observed effect |
| `help` | string | Declared everywhere, effect unconfirmed |

Cavedog files fill unused fields with the sentinel `-51` (and `52685` =
0xCDCD, both uninitialized-memory patterns from their editor) — treat any
out-of-range value as "unset".

### Header gadget (`id=0`) extra fields

| Field | Meaning |
| --- | --- |
| `totalgadgets` | Declared element count; has no observed effect |
| `panel` | Background art: GAF entry name via the same lookup rule as `name`. Empty when the screen uses a PCX background. |
| `crdefault` | Button triggered by Return (name). **Correction (2026-08-29):** this row said "exact behavior unconfirmed"; the executable's window-open routine resolves the name by a forward scan and, when the key is empty, binds Return to the first button whose name begins `OK` or `NEXT` (case-insensitive prefix), and likewise an empty `escdefault` to the first button beginning `PREV` or `Cancel` — see the executable spec [07 R-FE-01 §12] |
| `escdefault` | Button triggered by Escape (name) |
| `defaultfocus` | Gadget name that starts focused |
| `[VERSION] { major=; minor=; revision=; }` | Required in all 368 retail GUIs, but **optional in the parser**: the executable's panel-header loader seeks the subsection and skips it silently when absent, leaving the three byte fields zero (see the executable spec doc 02 §6). Values are arbitrary in retail files. |

### Button (`id=1`)

| Field | Meaning |
| --- | --- |
| `status` | Starting frame within the button's GAF entry (multi-stage buttons must use 0) |
| `text` | Label text. Multi-stage buttons separate per-stage text with a vertical bar (e.g. `text=On\|Off;`) |
| `quickkey` | Keyboard accelerator as an ASCII code (`83` = `S`); a bare symbol also occurs in data. **Correction (2026-08-29):** this row implied the field is honoured; the executable overwrites it at window open with the first free letter of the label unless `attribs` bit `0x10000` is set — see [07 R-WGT-01 §3] |
| `grayedout` | `1` = visible but disabled |
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
| `maxchars` | Maximum text length |

No border art — backgrounds provide the visual frame.

### Scrollbar (`id=4`)

| Field | Meaning |
| --- | --- |
| `range` | Knob travel in pixels (`knobpos` runs `0..range−1`), not an item count; the engine overwrites it for assoc-driven bars and for horizontal bars with `SLIDERS` art [07 R-WGT-01 §5]. **Correction (2026-08-29):** the previous "Item count" was a guess. |
| `thick` | **Correction (2026-08-29):** this row said "No confirmed effect"; the executable reads it as the numeric range of the value label a scrollbar with `attribs` bit 4 draws beside itself (`trunc(knobpos × thick / (width − knobsize))`) — see [07 R-WGT-01 §5] |
| `knobpos` | Knob position within range (engine-driven) |
| `knobsize` | Knob size (engine-driven when assoc'd) |

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

Sets the label font for the whole interface.

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
- **Loader edges (2026-08-29, RWU-02-3, `[02 R-MALF-01 §5]`).** The panel
  loader walks the top-level sections **by index in file order** — the
  `GADGET<n>` names are not consulted — and `totalgadgets` is read and then
  overwritten with the number of sections minus one (inert as authored).
  `[COMMON]` is read only when present (an absent one leaves the gadget's
  common fields unwritten); kinds 0–8 and 10 read their extra keys, any
  other `id` reads only `[COMMON]`; a text box's `maxchars` is capped at
  128. Gadget records are 347 bytes in a fixed 69,463-byte window record
  with no count check (about 199 gadgets fit; more overrun the record). A
  syntax error is fatal like any TDF; a missing panel file returns failure
  to the screen.

## Retail corpus notes

A key survey of every retail `.gui` (base + patch + expansions, 368
interfaces, 5,840 gadgets) confirms the type-ID set is exactly
{0, 1, 2, 3, 4, 5, 6, 7, 12} — no other IDs occur — and the field lists
above are complete except for the rare `itemheight` (listboxes) and five
occurrences of `crtdefault`, which is a retail typo for `crdefault`
(parsers should tolerate unknown keys for exactly this reason). Buttons
dominate (4,421 of 5,840 gadgets).

## Unknowns and caveats

- The complete per-menu hard-coded event-name tables are engine-internal;
  the only way to enumerate them is inspection of the stock GUI files.
- `texturenumber`, `commonattribs` have no confirmed
  behavior (`crdefault`, `thick` and `help` were on this list until 2026-08-29; `thick` is closed above, `help` is the hover text the pass copies into `HELPTEXT` [07 R-WGT-01 §1]). `colorf`/`colorb` are confirmed semantic GUI palette
  fields, but their per-gadget defaults and every primitive consumer remain
  context-dependent.
- Exact numeric semantics of `attribs` beyond the values cited above are
  unknown.
- Listbox behavior: rows, selection, scrolling and headings are in the
  executable spec [07 R-WGT-01 §4].

## Sources

- *GUI File Format* v1.0 by Dark Rain, TA Design Guide — the primary
  description, from trial-and-error experiments:
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/guidesc.htm>
- Verified against `guis/MAINMENU.GUI` from `totala1.hpi`; OpenTA parser:
  `formats/gui.go`.
