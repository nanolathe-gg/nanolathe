# Mod engine-package compatibility

## Purpose and scope

This document compares the three inspected non-retail engine packages —
ProTA 4.8, TA: Escalation Gold 10.2.0, and TA Zero Alpha 5 — and records where
their behaviors agree, where the same input does different things, and where
the differences are content vocabulary rather than engine exclusivity. It
covers implementation packaging, configuration identity, input mappings,
script interfaces and content-key vocabulary. It does not establish retail
behavior and approves no Nanolathe behavior; per-package details live in
[ProTA](prota-engine.md), [Escalation](taesc-engine.md) and
[TA Zero](ta-zero-engine.md).

**Established — artifact set.** The three packages were installed and
inspected as downloaded in September 2026; identifying hashes are in the
per-package documents.

## Package architecture

**Established — all three replace the executable's runtime dependencies, by
two different mechanisms.**

| | ProTA 4.8 | Escalation 10.2.0 | TA Zero Alpha 5 |
|---|---|---|---|
| Executable | byte-identical to retail 3.1 | retail image patched in place | retail image patched and extended |
| Draw/renderer | `ddraw.dll` (cnc-ddraw) + `tdraw.dll` loaded by the DirectPlay shim | `TAESC.dll` imported directly | `zdraw.dll` imported directly |
| Session/recorder | `dplayx.dll` forwarder re-exporting `tplayx.dll` | `eplayx.dll` imported directly | `zplayx.dll` imported directly |
| Music | `wgmus.dll` (WGMUS by MnHebi, BASS-based) plus Audiere-based `tmusi.dll` | `emusi.dll` (TA Music Player by Rime, winmm-based) | Audiere-based `zmusi.dll` (identical file to ProTA's `tmusi.dll`) |
| Registry root | stock Cavedog path for the executable; recorder under `Software\ProTA` | `Software\TA Esc` | `Software\TA Zero` |
| Preferences | `ProTA.ini` | `TAESC.ini` | `TAZero.ini` |
| Content trees | retail names inside `ProTA.gp3` | renamed `unitsE`, `weaponE`, `gamedatE`, `unitpicE`, `downloadsE`, `guie`, `aE` | renamed `ZUnits`, `ZWeapon`, `ZGameDat`, `ZUnitPic`, `ZBuildMenu`, `zgui`, `zi` |
| Savegame | retail `.sav` (stock executable) | retail `.sav` | `.zsv` |
| Replay suffix | `.pro` (4.3 release note) | not established | not established |

**Supported inference.** The replacements are not interchangeable: each
patched executable imports its own DLL names, so DLLs and executables are
coupled per package. A shared executable would have to avoid exposing one
package's DLL names to another's runtime.

**Established — the three renderer/engine DLLs are builds of one community
lineage.** Shared strings and structures include megamap drawing, whiteboard
and circle-select helpers, `IsLost`, `MegamapRadarColor`, the factory
identity-recycle fix, start-position assignment, wind synchronization and
"Swedish Eye ver 0.8" diagnostics (`tdraw.dll`/`TAESC.dll`). Feature coverage
diverges by build: Escalation's is the largest (unit-definition extension
keys, build-preview, rotation, challenge/response verification, vote handling,
explosion-cap telemetry), ProTA's is intermediate (limit adjusters,
verification, construction-unit options, ten-player funnel), TA Zero's 2013
build is the smallest and carries neither the extension-key registry nor the
construction-unit options. Exact source provenance and licensing are not
established.

The shared renderer interface these builds expose — the megamap, the
whiteboard, the selection filters and their preference-key differences — is
owned by [Shared draw-DLL interface](draw-engine-interface.md).

## Input mappings

### Retail baseline

**Established — retail mechanism.** Stock TA implements `CTRL`+letter
category selection by formatting the tag `CTRL_<letter>` and selecting own
units carrying that authored category
([07 R-INT-...](../retail-executable-spec/07-interface-input-camera-and-front-end.md)).
Stock content authors only `CTRL_V`, `CTRL_W` and `CTRL_F` (census over 278
definitions), while the engine provides fixed behaviors for the letters
documented in [07](../retail-executable-spec/07-interface-input-camera-and-front-end.md).

### Authored category vocabulary per package

**Established — authored census.** Unit definitions carrying each `CTRL_x`
category tag, by package (one unit counts once per tag):

| Tag | Escalation | ProTA | TA Zero |
|---|---|---|---|
| `CTRL_B` | 63 | 24 | 39 |
| `CTRL_C` | 2 | 2 | 3 |
| `CTRL_E` | 4 | 2 | — |
| `CTRL_F` | 50 | 34 | 32 |
| `CTRL_G` | 96 | 48 | 41 |
| `CTRL_H` | 38 | 10 | 9 |
| `CTRL_I` | — | — | 32 |
| `CTRL_J` | 10 | 8 | — |
| `CTRL_K` | 45 | 24 | 18 |
| `CTRL_L` | 25 | 16 | 23 |
| `CTRL_M` | 4 | 12 | 3 |
| `CTRL_N` | 31 | 12 | 5 |
| `CTRL_O` | 8 | 6 | 18 |
| `CTRL_P` | 32 | 16 | 8 |
| `CTRL_Q` | 6 | 4 | 5 |
| `CTRL_R` | 55 | 22 | 34 |
| `CTRL_T` | 18 | 6 | 14 |
| `CTRL_U` | 12 | 5 | 3 |
| `CTRL_V` | 51 | 24 | 18 |
| `CTRL_W` | 193 | 95 | 8 |
| `CTRL_X` | 38 | 24 | 1 |
| `CTRL_Y` | 8 | 2 | 5 |

TA Zero authors `CTRL_I` on 32 definitions although its own controls list
assigns no category to `CTRL+I`; whether the Zero engine or the retail
interface-menu binding wins there is **Unknown**.

**Established — the same letter selects different classes in different
packages.** Each package documents its own meaning for the letters it uses,
and the meanings disagree (controls lists: Escalation readme §9, TA Zero
author's controls page; ProTA changelogs name individual letters only):

| Key | Escalation | TA Zero | ProTA |
|---|---|---|---|
| `CTRL+V` | vehicles with weapons | mobile air units | not documented; authors `CTRL_V` |
| `CTRL+K` | kbots with weapons | kbots | not documented |
| `CTRL+T` | transports & teleporters | vehicles | not documented |
| `CTRL+X` | defensive units | experimental units | not documented |
| `CTRL+G` | mobile ground units with weapons | mobile ground units | not documented |
| `CTRL+L` | LRPC, silo and anti-missile | frontline mobile ground | not documented |
| `CTRL+O` | fighters | supporting mobile ground | not documented |
| `CTRL+W` | mobile units with weapons | mobile water units | not documented |
| `CTRL+E` | gunships | — | not documented |
| `CTRL+Q` | metal extractor (build) | long-ranged structures | not documented |

**Established — engine-level overrides of retail behavior.** The following
letters are changed from retail selection semantics by the packages' engine
DLLs/executables; the first three agree between Escalation and TA Zero (and
are documented for ProTA in part), the last is a ProTA-only divergence:

- `CTRL+F`: cycles idle factories instead of selecting all factories;
  `CTRL+SHIFT+F` selects all factories (Escalation and TA Zero documentation).
  ProTA instead centres the camera on the selected factory (4.6 changelog).
  ProTA's release notes state the override keys prefer the authored `CTRL_F`
  and `CTRL_B` category tags over heuristics, so the tags drive both selection
  and the overridden behaviors.
- `CTRL+B`: cycles idle construction units; `CTRL+SHIFT+B` selects all
  construction units (Escalation and TA Zero). ProTA documents only that
  `CTRL+B` excludes aircraft carriers.
- `CTRL+S`: selects on-screen units with weapons; `CTRL+SHIFT+S` selects all
  on-screen units (Escalation and TA Zero). ProTA documents the same intent
  through its category-tag preference rules.
- `CTRL+D`, `CTRL+A`, `CTRL+C`, `CTRL+Z` retain their retail meanings in the
  Escalation and TA Zero lists; ProTA does not list them.

### Order and build keys that collide across packages

**Established — context-dependent collisions.** All three packages keep the
retail order letters (`A` attack, `C` capture, `D` special, `E` reclaim,
`F` fire orders, `G` guard, `M` move, `P` patrol, `R` repair, `S` stop,
`T` track, `V` move orders) and add context-sensitive build hotkeys. The
documented sets overlap on the same physical keys, with different meanings
depending on selection and package:

| Key | Escalation build hotkey | TA Zero | Notes |
|---|---|---|---|
| `D` | use special ability / disintegrator | use special ability; on an idle factory, direct the build plate | same letter, different machinery per selection |
| `Q` | select metal extractor to build | select top-left build-menu slot | collision |
| `B` | select heavy/ballistic defence to build | not in the controls list (the stale in-game help keeps `B` as build menu) | collision; Zero's current mapping not established beyond the help file |
| `W`, `I`, `L`, `O`, `Y`, `U`, `J`, `Z` | select named build categories | `L` load, `U` unload, `O` orders (retail letters) per the controls list | Escalation rebinds several retail order letters when a builder is selected |
| `F4` | not documented (retail: scorecard) | toggle megamap | collision with retail meaning |
| `F10` | developer mode | developer mode in single player | agreement; ProTA not documented |
| `INSERT` | repeat previous command/cheat | repeat previous command/cheat | agreement; ProTA not documented |
| `\` | whiteboard modifier | whiteboard modifier | agreement; ProTA shares the engine family |
| `X` | build-in-line/surround while dragging | build-in-line/surround | agreement; ProTA not documented |

**Established — shared community selection additions.** Escalation and
TA Zero both document double-click same-type selection (configurable),
drag-selection filters (`W` armed mobiles, `B` construction units, `Y`
factories), `CTRL+SHIFT`-click for a hundred units, `PrintScreen`
screenshots, and ally resource bars with spectator view switching. ProTA's
documentation names its own variants of several of these (double-click
selection setting, `+noshake`, minimisable ally resource bar, the 4.7
PrintScreen fix); its drag-filter letters are not documented. The recorder
DLL's help text describes the shared hundred-unit queue and idle-constructor
finder. These additions are consistent where documented and do not conflict.

**Unknown — ProTA order/build key table.** ProTA's bundled documentation does
not list its extended hotkeys, so its overrides cannot be compared letter by
letter. Its 4.3–4.6 changelogs establish only individual letters (`CTRL+B`,
`CTRL+F`, `CTRL+S`/`CTRL+W`, build hotkeys, hotkey overlays) and that
`CTRL+F2` moved its advanced menu to `Settings.ini`/`ChatMacro.ini`.

## Executable-level behavior differences

**Established.** Escalation and TA Zero both patch the shipped executable;
ProTA ships it byte-identical to retail and applies its changes at runtime
through the engine DLL. The two patched executables touch largely disjoint
areas, and where they overlap the behavior either converges or is
parameterized differently:

- **Self-repair.** Both make the repair period data-driven through the
  definition's `HealTime` value and stop repair before construction completes;
  the mechanism is essentially the same in both.
- **AI difficulty economy.** The executable's per-difficulty resource
  multipliers are changed in Escalation from retail's 0.5×/0.7×/1× to
  4×/2×/1× with the difficulty name slots swapped (index 0 selects Hard), so
  the release notes' mapping (Easy 1×, Medium 2×, Hard 4×) holds; ProTA
  documents Hard 4×, Medium 1×, Easy 0.5×. The values differ for the middle
  and low difficulties; a shared engine must choose per package or per player
  setting.
- **Unit limits.** Escalation documents a per-player limit settable up to
  6553; TA Zero's executable raises the setting cap to 5000; ProTA documents
  20–1500. The shipped defaults are 1000 (Escalation), 1500 (Zero) and 1500
  (ProTA).
- **Order/weapon-slot handling (Escalation only).** The special order keeps
  other weapons acquiring targets, and other order states select a single
  weapon slot; no counterpart was found in TA Zero.
- **Build-point arithmetic (TA Zero only).** The sweet-spot-derived build
  point is computed at twice the box centre with an inverted vertical term; no
  counterpart was found in Escalation or in ProTA's stock executable.
- **Command classes.** TA Zero moves `+ai` and `+control` into the
  skirmish/multiplayer class and rewrites `+atm` to fill resources; Escalation
  documents `+ai`/`+control` reactivation and the same `+atm` behavior, so the
  packages converge on the user-visible result.

## Build-facing mechanisms

**Established — the same user need is implemented three different ways, and
they do not carry over.**

- **Escalation**: a command-fire third weapon (`LAB_DIR`, "Factory Buildpad
  Direction", range 768) on twenty-six factory definitions directs an existing
  factory's build plate; separately, a placement-rotation feature cycles the
  facing of a structure under construction with a configured key (default `/`),
  constrained by the unit's `Rotations` letters. Neither the four-frame
  rotation overlay asset nor any `Rotations` authoring ships in Gold 10.2, so
  the placement-rotation restriction is dormant there.
- **TA Zero**: the same command-fire ability concept as `FactoryDir` (range
  800) on sixteen factories, with the plate snapping to two valid positions in
  the script and AI factories steering it through the recorder ports; there is
  no placement-rotation key.
- **ProTA**: rotated exits are separate authored shipyard units (east, north
  and west variants), not an engine facing control.

Content written for one mechanism is inert under another package's engine; an
engine hosting all three content sets must implement the direction ability as
ordinary content (it is), honour the `Rotations` key and the rotation GAFs, and
accept the rotated-variant units as ordinary content.

## Content-key vocabulary

**Established — the packages disagree on non-retail weapon keys.** Escalation
authors `nottoair`, `surfacefire` and `nottounderwater` and its engine
resolves them; ProTA authors `toaironly` with no established reader; TA Zero
authors none. No inspected package authors another package's key. Because the
keys are separate names, one engine build that implemented all four would not
misinterpret content, but no inspected engine build implements all four (see
[Non-retail weapon target keys](weapon-target-keys.md)).

**Established — extended unit-definition keys are Escalation-only.** The
extension-key registry (veterancy, preview pieces, rotations) exists only in
Escalation's engine DLL; ProTA's and TA Zero's builds register no such keys,
and their content authors none. A Nanolathe content profile can therefore
accept the Escalation keys without changing the other two packages' meaning.
The unit-upgrade wiring is likewise Escalation-only: ProTA's and TA Zero's
`SIDEDATA.TDF` `[CANBUILD]` sections contain no upgrade entries.

**Established — resurrection keys are retail; several others are not.**
`canresurrect` and `resurrect` are retail keys and stock content authors them
(the Necro); Escalation's use of them is ordinary retail key usage. Unit
upgrades are also retail content wiring (the SIDEDATA `[CANBUILD]` list and
per-unit named GUI gadgets — see [Escalation](taesc-engine.md)); `canbuild`
itself has no reader. `canrepair`, `canland` and the misspelled
`ActivateWhenBuild` remain authored with no established reader; the same names
in another package would be inert until that is settled.

## Script opcodes and ports

**Established — no opcode conflict.** Scans of all compiled unit scripts in
all three packages found no non-retail opcode in executable position; the
apparent anomalies are trailing compiler banner text. Reused-opcode
divergence does not occur between these packages.

**Established — extended ports are a shared recorder interface, not a
collision.** All three recorders install the same COB-extensions handler over
the executable's port reader, expose the same port set (`32`, `69`–`75`) and
resolve each port to the same value; the 3.9.2.0 and 3.9.2.416 builds differ
only in implementation route. Escalation content reads `32` and `69`–`75`, TA
Zero content reads `70` and `74`, and ProTA content reads none; a script from
any package would see the same answers under any recorder build. The port
semantics are recorded in [Extended script ports](script-ports.md). ProTA
scripts use only retail ports.

## Verdict

- **Asset data is portable only in the retail vocabulary.** All three packages
  compile from retail unit keys and retail script opcodes. New unit-definition
  keys are Escalation-only; new weapon keys are package-specific names;
  extended script ports overlap by number and need per-package semantics.
- **Engine builds are not interchangeable.** Executables import package-named
  DLLs, and the renderer/session DLLs carry package-specific feature sets and
  configuration names.
- **A single engine could host all three content sets** if it (a) keeps the
  retail key/opcode vocabulary, (b) accepts each package's non-retail keys
  under their own names, (c) implements the shared recorder port extensions
  once, and (d) does not adopt any one package's keyboard overrides as
  universal. No inspected artifact does this; the packages each ship their own
  build.
- **Engine-level mutual exclusions are small and enumerable**: the keyboard
  overrides in the table above (especially `CTRL+F`, `Q`, `B`, `W`, `O`, `L`,
  `U`, `F4`), the differing AI difficulty multiplier values, and the
  package-specific registry/INI/save identities. They are configuration
  dimensions, not algorithmic incompatibilities. The extended script ports
  are *not* on this list: they are one shared recorder contract.

## Unknown

- **Unknown — the remaining port questions.** The unit-id iteration fields,
  the relation table's meaning and the visibility class's exact test stay
  open in [Extended script ports](script-ports.md).
- **Unknown — ProTA's exact key table.**
- **Unknown — recorder version behavior differences.** See
  [TA Demo Recorder](ta-demo-recorder.md).
