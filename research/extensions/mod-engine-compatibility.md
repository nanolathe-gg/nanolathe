# Mod engine-package compatibility

## Purpose and scope

This document compares the three inspected non-retail engine packages —
ProTA 4.8, TA: Escalation Gold 10.2.0, and TA Zero Alpha 5 — and records where
their behaviors agree, where the same input does different things, and where
the differences are content vocabulary rather than engine exclusivity. It
covers implementation packaging, configuration identity, input mappings,
script interfaces and content-key vocabulary, with an authored-asset audit and
bounded observations of the Nanolathe host. It does not establish retail
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
| Content trees | `units`/`ai` retain retail names; renamed `gamedatP`, `weaponP`, `guiP`, `unitpicsP`, empty `downloadP` | renamed `unitsE`, `weaponE`, `gamedatE`, `unitpicE`, `downloadsE`, `guie`, `aE` | renamed `ZUnits`, `ZWeapon`, `ZGameDat`, `ZUnitPic`, `ZBuildMenu`, `zgui`, `zi` |
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
established for those historical DLLs. The current TADR source has an MIT
license; its pinned revision and per-profile matrix are recorded in
[community patch engine behavior](community-patch-engine.md). That source
does not identify the revision of an older DLL.

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

**Established — authored subset and current shared interface.** Escalation
content reads `32` and `69`–`75`, Zero reads `70` and `74`, and ProTA reads no
extended ports. The pinned recorder source gives those numbers one shared
meaning; it also handles the wider command/setter surface in
[Extended script ports](script-ports.md). Its mod-id gates affect supporting
plugins, and individual commands have distinct locality/playback gates.
**Unknown — historical equivalence outside the recorded subset:** the older
DLLs' wider interfaces and edge behavior need version-matched evidence; one
source tree does not prove identical answers under every historical build.

## Verdict

- **Asset data is portable only in the retail vocabulary.** All three packages
  compile from retail unit keys and retail script opcodes. New unit-definition
  keys are Escalation-only; new weapon keys are package-specific names;
  extended script ports overlap by number and need version-scoped semantics.
- **Engine builds are not interchangeable.** Executables import package-named
  DLLs, and the renderer/session DLLs carry package-specific feature sets and
  configuration names.
- **A single engine could host all three content sets** if it (a) keeps the
  retail key/opcode vocabulary, (b) accepts each package's non-retail keys
  under their own names, (c) implements the shared recorder port extensions
  once, and (d) does not adopt any one package's keyboard overrides as
  universal. No inspected artifact does this; the packages each ship their own
  build. **Established — current shared source, separate builds.** One
  MIT-licensed source tree builds the family once per package
  profile (seven at the pinned revision, including `prota`, `escalation`,
  `tazero` and `ota`), each with its own preference-file and registry identity
  and its matched recorder DLL
  ([community patch engine behavior](community-patch-engine.md) §3); it
  implements three of the four non-retail weapon keys (not `toaironly`) and the
  shared recorder ports once. This establishes shared implementation with
  profile-specific choices, not binary interchangeability or equivalence to
  each older package's engine. Input condition (d) remains a per-profile
  behavior to preserve.
- **Engine-level mutual exclusions are small and enumerable**: the keyboard
  overrides in the table above (especially `CTRL+F`, `Q`, `B`, `W`, `O`, `L`,
  `U`, `F4`), the differing AI difficulty multiplier values, and the
  package-specific registry/INI/save identities. They are configuration
  dimensions, not algorithmic incompatibilities. The extended script ports
  are *not* on this list: they are one shared recorder contract.

## Completeness boundary and contract owners

**Established — evidence boundary.** Three independent targets must be named:
the authored release content, its distributed engine/recorder, and the current
TADR profile with the same mod name. The first two are identified in the
package documents; current source here means
[TADR revision dcff5ddeb6bd1030e3f452c0f16e5f005850f62f](https://github.com/tanvanman/TADR/tree/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f).
Source-established behavior at that revision does not settle older builds by
name alone. Catalog compilation proves neither scripts nor visual fidelity.

**Established — ownership index, not implemented-support status.** These
domains collectively bound a claim of full support. A claim must name the
release and the domains verified. Recording a domain outside the present
single-player implementation scope does not make it implicitly supported.

| Domain | Contract owner and boundary |
|---|---|
| Directory aliases, overlay, limits, LOS tables, maps | Package references and the asset audit below; the same mod name can denote different layouts. |
| Weapon/unit keys, targeting, damage, healing, veterancy, transport, wrecks | [Community engine](community-patch-engine.md), [target keys](weapon-target-keys.md), historical package contracts. |
| Construction, rotations, previews, membership, queue/placement | Community engine, [build menus](build-menus.md), [rendering](community-patch-rendering.md); display does not prove footprint or COB agreement. |
| Movement, path budgets, builder schedules | [Pathfinding](community-patch-pathfinding.md) and community-engine construction; a capacity raise does not establish a new path algorithm. |
| COB queries, commands, setters, effects, spawning, callbacks | [Script ports](script-ports.md); preserve locality, playback, caller and mod-id gates, not only port numbers. |
| Models, animated/team textures, composites, palette/SHD, nanoframes, projectiles | Rendering reference, authored audit below and retail format contracts. |
| Strategic view, contacts, icons, rings, whiteboard, selection | [Draw interface](draw-engine-interface.md) and rendering contracts; each visibility predicate matters. |
| Faction HUD, GUI pages, third side, controls | Package references and input mappings; do not assume ARM/CORE occupy side indexes zero/one. |
| Campaigns, schemas, AI, sound, music | Package references, recorder and community-engine schema contracts; compilation does not exercise a mission or its victory condition. |
| Saves, configuration identity, replay/session tooling | Package references, [recorder](ta-demo-recorder.md), community-engine session contracts; historic and current formats stay distinct. |
| Multiplayer claims, sharing, votes, verification, packet extensions | Community engine and recorder; researched surface, with networking/full replay outside the repository's present implementation scope. |

## Authored rendering audit

### Reproduction and admission

**Established — local host observation, 2026-09-22 UTC.** At Nanolathe revision
`857de633`, mount the reference retail install, followed by these roots in
order, and resolve the named built-in content profile:

| Profile | Additional roots |
|---|---|
| `prota` | ProTA 4.8 extracted distribution |
| `zero` | TA Zero Base's `TA Zero` directory, then TA Zero Alpha 5 |
| `escalation` | Escalation Gold 10.2.0 `Step_2_Install Main Files` directory |

The private bundle `~/ta-decompile/mods/notes-support-20260922/` retains audit
source, root/archive manifest, full JSON, definition-reference matches, test
logs and captures. The probe uses production VFS/profile detection, catalog
compilation, 3DO/GAF decoders and metadata validation. It deduplicates
case-folded logical paths and reads the overlay winner without donor files.
The corpus includes mounted but potentially unused assets; file identities
below hash decoded authored resource bytes with SHA-256.

**Established — admission at the audited revision.** ProTA compiles 317 unit
definitions and Zero 269. Escalation passed its enlarged definition/map/LOS
domains but stopped at `objects3d/armast_dead.3do`. The requested-feature
correction described under "Missing model and texture references" closes that
admission failure: `TestEscalationContentSetCompilesWithoutUnusedFeatureModels`
compiles the original package and separately requires an error when one of the
unused missing-model definitions is explicitly requested. Conversely,
`cmd/modinventory` deliberately inserts donors following missing-resource
errors; its tolerant report does not prove original art loads.

| Corpus | Decoded 3DOs | GAF banks decoded under default limits | Banks rejected by pixel budget | Unresolved textured-face names |
|---|---:|---:|---:|---:|
| ProTA 4.8 | 654 | 441 | 0 | 0 |
| Zero Alpha 5 | 1,145 | 932 | 1 | 2 |
| ESC Gold 10.2.0 | 1,189 | 722 | 3 | 27 |

**Established — observation scope.** Every enumerated 3DO decoded. Texture
checking compares nonempty textured-face names against nonempty entries of
the winning `textures/*.gaf` banks, case-insensitively. It establishes presence,
not duplicate-entry precedence, face visibility, or an original-engine lookup
rule absent from this probe.

### Large effect banks exceeded the eager host budget

**Established — authored content and original 2026-09-22 host observation.**
Four banks failed the production default GAF limit of 134,217,728 unique decoded pixels. Pixel-free
metadata validation succeeds when only the probe's aggregate/expanded pixel
budgets are enlarged. The counts sum `width × height` once per distinct frame,
including composite children: they are geometry budgets, not encoded sizes or
peak-memory estimates. That audit changed no production limit.

| Bank under `anims/` | Encoded bytes | Unique frame pixels | Largest frame pixels |
|---|---:|---:|---:|
| Zero `modfx.gaf` | 138,877,905 | 138,769,269 | 1,081,668 |
| ESC `esc_nuke_a_02.gaf` | 111,898,352 | 138,251,088 | 4,665,600 |
| ESC `esc_nuke_x_01.gaf` | 234,063,808 | 282,025,456 | 9,144,576 |
| ESC `esc_weap_x_01.gaf` | 238,541,376 | 288,556,992 | 10,497,600 |

**Established — resource identities**, in the same order:

- `modfx.gaf`: `c31dc7e72fb03595f58378ccbb2a6375d69d67eda930e1f0b7ea27d712614d1b`.
- `esc_nuke_a_02.gaf`: `955ae24a1f19f4f6e68d6a6b4fdc201f859f86b90b8d5d06e030358899b00c6b`.
- `esc_nuke_x_01.gaf`: `0f77fa777d7d86b47b52168b4b1bfa1032a899b99350584e90c4908bcceb6205`.
- `esc_weap_x_01.gaf`: `f46eec172d562f8d6fcfa7a5950e4703ea95ac2478b92a7038e7e4e28eaa28f3`.

**Established — authored dependencies.** Zero's `ZWeapon/ArmWeapons.tdf`
references `ModFX` for `Arm_ComDGun`, and `Death.tdf` for `Death_Commander`;
its other faction weapon files use the bank too. Escalation's
`T4ESC2.ufo:weaponE/TAESC4.tdf` uses `esc_nuke_a_02` for `NUKE_SUPER`;
`T5ESC.ufo:weaponE/TAESC5.tdf` uses `esc_weap_x_01` for `CANNON_BFG`,
`esc_nuke_x_01` for `CANNON_OLYMPUS`, and `esc_nuke_a_02` for `NUKE_SSILO`,
including water/lava variants. These files have active authored references.

**Established — original host consequence.** The eager loader's aggregate
limit made `internal/client.EffectBank` diagnose and cache a null bank. Catalog
success therefore coexisted with unresolved effect art.

**Established — bounded host closure, 2026-09-26.** The presentation path now
validates an immutable encoded `formats.GAFSource` and decodes only requested
root frames. Its source, decoded-frame, durable-atlas and transient-GPU caches
have explicit byte and entry budgets, with current-draw images pinned through
execution. Eager format callers retain their old limits. Every root in the
three identified Escalation banks decoded successfully (158 roots); the
largest materialized root occupied 47,962,888 bytes. Direct Classic and Modern
captures of representative large roots matched byte for byte. The same audit
of Zero's `ModFX`, `ModFX2` and `ModFX3` decoded every root and produced identical
Classic/Modern captures of each bank's largest root. An ordinary event-buffer
fixture also exercised Escalation `esc_nuke_a_02` through the effect service,
frame publication and client composition: its 64 frames lasted two ticks each,
were observed at frames 0, 32 and 63, and retired at tick 129. No sprite art was
missing or skipped. The middle, tail and retired Classic/Modern images matched;
the opening Modern ground-light effect deliberately differed. This proves the
existing event lifecycle for that bank, not weapon-impact creation or every
sequence. These are host observations against the identified authored resources, not historical engine
comparisons. Implementation and budgets are owned by
[GPU renderer, bounded effect residency](../../docs/DESIGN_GPU_RENDERER.md).

**Unknown — full presentation.** The decoder sweep and direct root captures
close admission, materialization and the exercised upload path. They do not
certify every authored effect's complete battle lifetime or all-frame visual
fidelity. Those claims require ordinary effect-event captures throughout the
sequence on both renderers, retaining the actual bank identities.

### Missing model and texture references

**Established — Escalation authored references.** Five absent models are named
by corresponding corpse sections:

| Missing model under `objects3d/` | File under `features/corpses/` |
|---|---|
| `armast_dead.3do` | `ARM_T3_corpses.tdf` |
| `armmanta_dead.3do` | `ARM_T2_corpses.tdf` |
| `corast_dead.3do` | `CORE_T3_corpses.tdf` |
| `corcapsub_dead.3do` | `CORE_T2_corpses.tdf` |
| `cortrog_dead.3do` | `CORE_T5_corpses.tdf` |

**Established — authored reachability, Gold 10.2.0 (2026-09-26).** The full
manual release (`TAESC_GOLD_10_2_0_FULL.rar`, SHA-256
`a9873e551d7fa72ad2f74ca37d8bbc7c873043d178979c842bbf8ed667eea2c3`)
contains these feature sections but no unit definition, unit corpse link,
incoming feature successor, terrain feature-table entry or mission placement
requesting any of the five missing-model definitions. The audit includes the
reference retail install beneath the seven primary Escalation archives and
checks authored FBI, feature TDF, TNT and OTA records. The Arm/Core advanced
shipyard names also occur in stale side build lists and weapon damage keys;
those occurrences do not request a feature model.

These are unused definitions, not a requirement to substitute wreck art.
Retail's feature parser is invoked with a requested section name
[05 R-FEAT-01 §1]; its fatal model policy applies when that feature is
compiled [02 "Cross-reference failure policy"]. Nanolathe's earlier check
requested every parsed feature model eagerly. Loading the requested feature
closure instead preserves the missing-model error for a unit corpse, a map
feature, a saved feature or any of their successors, without inventing art.

**Established — Zero unresolved texture names.** `armcolormeta4_1` appears on
`armcommander.3do` and `armt1aaturret.3do`; `goksphere2_5` appears in 22 models,
including `armt1sub.3do`, `coret2ewhover.3do`, `gokt2suphover.3do`. Neither
resolves in the audited namespace. **Established — Escalation unresolved
names:** `armhrk1_01` on `corfrig_dead.3do`; `dcom2` and `metal1` on
`cormkl.3do` and its two audited wreck variants; `s01` through `s24` on
`corfus_upgrade.3do` and `corsfus_upgrade.3do`.

**Established — authored piece/script follow-up.** The private audit bundle's
`textures/` directory records model/FBI/script identities, every matching
polygon and all inspected script visibility commands. None of the targeted
Zero polygons or Escalation `s01`–`s24` polygons is selection geometry.

| Missing name and model pieces | Shipped script evidence |
|---|---|
| Zero `armcolormeta4_1`: commander `head`, AA turret `socle` | The commander hides its head during arrival, then explicitly shows it. The turret has no hide/show/explode command for its pedestal. These are ordinary model surfaces requiring visual acceptance. |
| Zero `goksphere2_5`: wake-effect pieces | Of 100 matching quads, 93 are hidden in the straight-line beginning of `Create`, with no later show command in the inspected scripts, including the bound AI variants. Emitting an effect from a piece does not itself show its polygon. |
| Zero remaining seven wake quads | `coret1sub` has model piece `wake` but script piece `wake1`; the missing-name binding outcome is **Unknown**. `gokt1consub` and `gokt1sub` each have `wake1`–`wake3` in both model and script, but no hide/show/explode command for them. Their role as effect emitters does not establish invisibility. |
| Escalation upgrade models: `s01`–`s24` | Both scripts hide all 24 matching pieces during the straight-line start of `Create`, before waiting for construction. Neither script later shows them. |

**Unknown — intended visible appearance.** Script hiding does not settle
unscripted model displays, extension build previews or pre-script startup.
Actual projected pixels, occlusion, the unresolved piece binding, any further
documented texture resolver and matching historical captures remain to be
checked. The other Escalation names above have not received this piece/script
follow-up. Do not equate every missing name with a demonstrated visible defect
or choose substitute art from the census alone.

### Bounded model captures

**Established — host observation, not a retail comparison.** On the same
Nanolathe revision, macOS GPU captures used the roots above, empty temporary
settings, `--renderer modern --shot-renderer both --shot-size 800x600`, heading
zero and the default unanimated pose. Subjects were ProTA
`--shot-model armfndry` and Zero `--shot-model gokt3suprpod`; all four images
were opened and inspected. Both render recognizable textured geometry, but at
`--shot-model-scale 2` the classic image has approximately twice the modern
image's width and height. GPU telemetry reports one subject, no skipped body
and no lane overflow. This route does not exercise COB `Create`, build-preview
filtering, combat or a battle camera.

**Established — preview-scale discrepancy.** Repeating ProTA at scale 1 changes
the classic PNG; the modern PNG is byte-identical at scales 1 and 2 (SHA-256
`121511284f8ad8e7f7ed295b39f365568b5713f39cbce254509fa7e81711b476`). The default
comparison tolerance accepts these differences; a zero exit code is not a
visual parity assertion. **Unknown — scope and remedy:** trace the isolated
preview transform and recorded geometry against the battle executor before
calling this a general battle-scale defect. Large effects and Escalation
battle rendering are not visually certified by these model captures.

## Unknown

- **Unknown — the remaining port questions (since reduced).** The unit-id
  iteration fields, the relation test and port `75`'s exact test are settled
  from recorder source in
  [Extended script ports](script-ports.md); what stays open there is the
  `x100` kill scale on port `32` and the port-`73` out-of-range contract.
- **Unknown — ProTA's exact key table.**
- **Unknown — recorder version behavior differences (since reduced).** The
  version lineage and per-build command differences are recorded in
  [TA Demo Recorder](ta-demo-recorder.md), and the current recorder line's
  placement against the `3.9.2.x` versions in
  [community patch engine behavior](community-patch-engine.md) CP-SES-8; what
  remains open is the behavior of the old builds where it differs from the
  current source.

- **Unknown — full rendering acceptance.** Large-bank admission and bounded
  root presentation are closed above; resolve the remaining visible references
  and preview-scale observation, then compare matching
  battle scenes, animations, factions, underwater effects, team colours,
  fog/sensor states and construction on both renderers. Source contracts and
  parser success alone do not certify visual fidelity.
