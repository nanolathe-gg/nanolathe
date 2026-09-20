# Shared draw-DLL interface

## Evidence scope and sources

This document records the interface and behavior of the community "draw DLL"
family — the renderer/extension DLLs that ProTA, TA: Escalation and TA Zero
each ship — as far as the inspected builds establish it. It does not establish
retail behavior and approves no Nanolathe behavior. Per-package identity and
the rest of each package live in [ProTA](prota-engine.md),
[Escalation](taesc-engine.md) and [TA Zero](ta-zero-engine.md).

**Established — inspected builds.** ProTA 4.8 `tdraw.dll`
(`6b46046a…`), Escalation Gold 10.2.0 `TAESC.dll` (`61783aca…`) and TA Zero
Base `zdraw.dll` (`34993ee8…`), plus the two optional Zero `zdraw.dll`
replacements. All are builds of one lineage; this document states per-build
differences where they exist. No third-party implementation source was read.

## Megamap

**Established — the view is held, not latched.** The window handler tests the
configured `MegamapKey`'s current down state throughout; the "view active"
flag is set on a left click while the key is down and cleared on release when
it is up. The built-in default key is Tab; the value is a preference
(`MegamapKey`). TA Zero's controls page documents F4 as its megamap key, but
the inspected preferences do not set `MegamapKey`, so the shipped effective
binding is **Unknown** (a registry or options-menu default could replace the
built-in one).

**Established — zoom and movement.** A step counter with eleven steps (0–10) is
clamped on every change; wheel-forward and Page Up step in, wheel-back and
Page Down step out, and both are ignored unless the megamap key is held.
`WheelZoom` (default on) enables wheel zoom; `WheelMoveMegaMap` (default on)
enables wheel view movement; `DoubleClickMoveMegamap` (default off) the
double-click variant.

**Established — redraw limiting.** `MegamapFpsLimit` (default 60, `0` =
unlimited) gates the megamap surface's *redraw rate* only, never the game tick.
It exists in `tdraw` and `TAESC`; the 2013 `zdraw` has no such key, and the
Escalation 10.2 INI comments it out as disabled in that release.

**Established — layer toggles.** Nine preference booleans, all default on,
gate what the megamap draws: `DrawBackground`, `DrawMapped`, `DrawProjectile`,
`DrawUnits`, `DrawMegamapRect`, `DrawMegamapBlit`, `DrawSelectAndOrder`,
`DrawMegamapCursor`, `DrawMegamapTAStuff`.

**Established — under-attack flash.** `UnderAttackFlash` (default off) hides a
damaged unit's megamap icon on the phases where the engine's global blink bit
is clear; the cadence is that engine phase, not a DLL timer (**Unknown** exact
rate).

**Established — icon choice.** Each unit definition carries a numeric category
identifier. The loader walks the unit-type table and loads
`<type-name>.PCX` per type from the game directory, plus the fixed names
`COMMANDER`, `MOBILECOMBAT`, `CONS`, `FACTORY`, `BUILDING`,
`AIRCRAFTCOMBAT`, `AIRCONS`, `NONE`, `UNKNOWN` and `NUKEICON`. At draw time the
icon list is walked in order and the first entry whose category set contains
the unit's category wins; a unit matching nothing draws `UNKNOWN`, and `NONE`
means draw nothing.

**Established — icon configuration.** `MegaMapConfig` names an icon-config
INI (ProTA and Escalation ship `Icon/iconcfg.ini`, TA Zero `ZIcon/iconcfg.ini`),
resolved relative to the working directory; `UseDefaultIcon` (default true)
suppresses it. Its `[Option]` keys with defaults are `FillColor` 0,
`TransparentColor` 9, `SelectedColor` 89, `HoverColor` 84, `UseCircleHover`
false; `[Icon]` lines are `name=file.PCX`, where the name is a category name
resolved by the engine's category lookup. The reserved names `nothing`,
`unknow` and `nukeicon` map to `NONE`, `UNKNOWN` and `NUKEICON.PCX`.

**Established — sensor rings are clutter filters.** `MegamapRadarMinimum`,
`MegamapSonarMinimum`, `MegamapRadarJamMinimum`, `MegamapSonarJamMinimum`
(default −1 = no filter) and `MegamapAntiNukeMinimum` (built-in default 512)
are thresholds: a ring is drawn only when the unit's range for that sensor
*exceeds* the value. Antinuke rings need an antinuke-flagged weapon slot and
are drawn dashed while the unit is armed.

**Established — colours.** `MegamapRadarColor`, `MegamapSonarColor`,
`MegamapRadarJamColor`, `MegamapSonarJamColor`, `MegamapAntinukeColor` and
`MegamapWeapon1/2/3Color`; absent values fall back to the engine's own minimap
colours. Player dots are `Player1DotColors` … `Player10DotColors`, defaults
227, 212, 80, 235, 108, 219, 208, 93, 130, 67. The per-player marker graphics
use `PlayerMarkerPcx`, `PlayerMarkerBackground`, `PerPlayerMarkerWidth` and
`PerPlayerMarkerHeight`.

**Established — full-screen replacement.** `FullScreenMinimap` (default off)
enables the DLL's full-screen megamap replacement, and `MegamapFpsLimit`
applies only while it is active; `MenuWidth`, `MenuHeight` and `MenuResolution`
size the minimap-menu replacement, and `ShareDialogExpand` the expanded share
dialog. `MegamapDither` (default on) and `EnhancedBuiltInMinimap` (default on)
exist only in the Escalation build; the first dithers the megamap and built-in
minimap, the second patches the engine's built-in minimap.

## Whiteboard

**Established — key and modes.** `WhiteboardKey`, default `\`, is a hold key:
the whiteboard's modes are live only while it is down.

**Established — interactions.** Left-drag on empty space paints a freehand
stroke; left-click on one of your own text markers grabs it and makes it follow
the mouse; right-drag while grabbing erases everything in a small box at the
cursor with a delete animation; middle-click erases at the cursor;
middle-click release on empty space adds an empty text marker; right-click on
your own marker opens its edit box, where Enter commits, Escape cancels, and
Backspace edits. Holding the whiteboard key with Ctrl moves the camera to the
newest marker's stored map position, clamped to the map.

**Established — distribution.** Markers arrive through the ordinary game chat
stream as a control-prefixed message (a leading control byte, then position,
colour/player index and text); the DLL parses it, creates the marker and echoes
the chat line. Marker visibility therefore follows chat reach — allies in team
games. The local creation paths create markers directly; no packet builder for
announcing a locally created marker was found, so how a marker reaches peers is
**Unknown** (a two-client test would settle it). Text markers announce as
`*<player>: <text>` and dot markers as `*<player> added a new marker`.

**Supported inference — markers do not persist.** Nothing serialises the marker
store, which is created once at engine init.

## Selection

**Established — preference.** `DoubleClick` (default on) is read by the
selection object at startup; the same-type identity rule itself is engine-side
and not in the DLL.

**Established — drag filters.** The DLL precomputes three per-unit bitmaps
from the authored categories `CTRL_W`, `CTRL_B` and `CTRL_F`. When an authored
category matches nothing, `CTRL_B` falls back to units with one flag bit set
and another clear and a non-zero per-unit byte, and `CTRL_F` to the same flag
pattern with that byte zero; both fallbacks exclude the `CTRL_W` set. The
meaning of those unit flag bits is **Unknown** from the DLL alone.

**Unknown — the remaining selection rules are engine-side.** No DLL-side rule
was found for circle select, the `CTRL+S` on-screen-weapons predicate, or the
idle-builder/idle-factory cycle order, camera behavior and shift variants; the
DLL contributes only the filter masks.

## Preference-key census

**Established — keys read from the `Preferences` section.** All three builds
read: `UnicodeSupport`, `UnicodeSupport_Background`, `UnicodeSupport_Color`,
`DoubleClick`, the megamap block above, `FullScreenMinimap`,
`ShareDialogExpand`, `MenuWidth`/`MenuHeight`/`MenuResolution`,
`UseVideoMemory`, `DisableDeInterlaceMovie`, `DisplayModeMinHeight768`,
`AISearchMapEntries`, `SfxLimit`, `UnitLimit`, `MegaMapConfig`,
`PlayerMarkerPcx`, `PlayerMarkerBackground`, `PerPlayerMarkerWidth`,
`PerPlayerMarkerHeight`, the player dot colours, and the option keys
`ClickSnapOverrideKey`, the autoclick key (stored in the registry, default
`X`), `WhiteboardKey` and `MegamapKey`.

| Key | ProTA `tdraw` | Escalation `TAESC` | Zero `zdraw` |
|---|---|---|---|
| `MegamapFpsLimit` | yes | yes (disabled in 10.2's INI) | — |
| `MegamapDither` | — | yes | — |
| `EnhancedBuiltInMinimap` | — | yes | — |
| `NanoframePreviewFill` | — | yes | — |
| `UnitType` / `X_CompositeBuf` / `Y_CompositeBuf` | yes | yes | yes |
| `WeaponType` / `MultiGameWeapon` | — (weapon tables pinned by patch code) | — (same) | yes |

## Unknown

- **Unknown — the megamap flash cadence.** The rate of the engine blink phase
  that hides attacked-unit icons.
- **Unknown — local marker distribution.** How a marker created locally is
  announced to peers.
- **Unknown — `CTRL+S` and cycle rules.** The on-screen-weapons predicate and
  the idle-builder/factory cycle order live in the executable or another
  component and are not established here.
- **Unknown — the drag-filter fallback flag bits.** What the filter fallback's
  unit flag bits denote.
- **Unknown — TA Zero's effective megamap key.** Its controls page documents
  F4; the DLL's built-in default is Tab and the inspected preferences do not
  override it.
