# TA Community Patch engine behavior (`tdraw.dll`)

## 1. Purpose, targets and scope

This document owns the behavioral specification of the **TA Community Patch
engine component** — the TADR `tdraw.dll` line — as the reference for
"community-patch-compatible behavior" in Nanolathe. It is a clean-room
description of what the patch does, independently worded from its MIT-licensed
source, with the evidence boundaries stated. It does not establish retail
behavior, which stays owned by
[retail-executable-spec](../retail-executable-spec/README.md), and it approves
no Nanolathe behavior; adoption belongs in the owning `docs/DESIGN_*.md`.

### 1.1 What "community-patch-compatible" means here

Five compatibility targets, in priority order. Each is separately testable;
"matches it exactly" is only claimed at the tier where the evidence supports
it.

- **T1 — Content compatibility.** Content authored for the community-patch
  line (unit FBI, weapon TDF, map OTA, COB scripts, preference INI) loads and
  means what its author intended, under the matching build profile. Covers all
  non-retail definition keys, the recorder-provided script ports, and the
  authored map-spawn schema.
- **T2 — Single-player/skirmish simulation behavior.** For each build profile,
  the patch's simulation-affecting changes are reproduced with the same
  arithmetic, comparison strictness and ordering that the source specifies, so
  that a scene run under Nanolathe and under the corresponding `tdraw` build
  produces the same outcome. This is the tier that must be exact where the
  source is exact.
- **T3 — Session and wire behavior (deferred).** Multiplayer-only mechanics
  (vote-reject, `.take` arbitration, share guard, lag guard, identity audit,
  chat-hijack packets, anti-cheat exchange, recorder protocol). Recorded here
  exhaustively so a future multiplayer effort has the contract, but not part of
  current Nanolathe scope, which excludes networking and replay.
- **T4 — Host presentation preferences (separate controls).** Renderer and UI
  features (megamap, whiteboard, chat drawer, counters, team colours,
  nanoframe preview). These are host preferences, not gameplay parity;
  Nanolathe's renderer keeps its own controls. They are catalogued here only to
  mark them out of T2 and to keep the boundary explicit.
- **T5 — Tooling (not engine behavior).** Installer, launcher, mods tool,
  inspector, visibility patcher, crash tooling. Out of scope except where they
  define packaging/identity facts.

"Exactly" is therefore defined as: **T2 arithmetic and ordering bit-for-bit
where the source states them; T1 semantics for every authored key; T3 recorded
but deferred; T4 and T5 explicitly excluded from gameplay parity.** The build
profile is part of the identity — the seven `tdraw-<profile>` builds disagree
on purpose, and compatibility requires naming the profile.

### 1.2 Related documents

Per-package material stays with its owning document:
[ProTA](prota-engine.md), [Escalation](taesc-engine.md),
[TA Zero](ta-zero-engine.md), [shared draw-DLL interface](draw-engine-interface.md),
[recorder DLLs](ta-demo-recorder.md), [script ports](script-ports.md),
[weapon target keys](weapon-target-keys.md), and the older docs-only
[community patch pathfinding](community-patch-pathfinding.md) note, which this
document supersedes where the source answers its open questions.

## 2. Evidence scope and sources

**Established — source identity.** The analyzed tree is
`https://github.com/tanvanman/TADR` at commit
`dcff5dd` (2026-09-20, the `dev-dcff5dd` developer release), with the recorder
distribution marked `2026.9.9`. The repository grants **MIT** for its three
components (`src/DDraw` = `tdraw.dll`, `src/Recorder` = `tplayx.dll`,
`src/Server` = `SERVER.EXE`), so the
[extension evidence policy](README.md#evidence-policy) permits describing the
implementation. All claims below are source-read behavior for that revision,
independently worded; no source code is copied into this reference.

Confidence labels follow the policy: **Established** means the pinned source
directly supports the claim; **Supported inference** names what is missing;
**Unknown** names what would settle it.

The shipped public feature list (`src/DDraw/tdraw.txt`, installed as
`tdraw.txt`) and settings file (`src/DDraw/totala.ini`) are treated as the
authors' documentation of the same revision; where they and the source
disagree, the source wins and the discrepancy is recorded.

## 3. Artifacts and build profiles

**Established — components.** The patch line comprises three separately built
components:

| Component | Built from | Artifact names | Role |
|---|---|---|---|
| Engine/renderer patch | `src/DDraw` (C++, MSVC x86; the `ReleasePublic` configuration is the one released) | `tdraw.dll` for every profile (the release archive is `tdraw-<profile>.zip`); `taesc.dll`, `zdraw.dll` and `mdraw.dll` are manual install renames documented in `tdraw.txt` for Escalation, Zero and Mayhem, paired with each mod's INI name | Loads `dplayx.dll` first (so the patch loader can relocate the registry path before settings are read), then the real `ddraw.dll`; replaces the DirectDraw entry surface, patches the running `TotalA.exe`, reads the preference INI |
| Session recorder | `src/Recorder` (Delphi 7) | `tplayx.dll`, `eplayx.dll`, `zplayx.dll`, `bplayx.dll` | DirectPlay replacement; recording/replay, console commands, COB extension ports |
| Standalone replayer | `src/Server` | `SERVER.EXE` | Plays back recordings outside the game |

**Established — seven build profiles.** `tdraw.dll` is compiled once per
content package from `src/DDraw/config_<profile>.h`; the profile is compiled
into the DLL (`TDRAW_CONFIG_NAME`) and selects the feature set. The seven
profiles at the pinned revision:

| Profile flag | Build identity | Intended package |
|---|---|---|
| `TDRAW_CONFIG_PROTA` | `prota` | ProTA ("all features enabled", mainline) |
| `TDRAW_CONFIG_ESCALATION` | `escalation` | TA: Escalation |
| `TDRAW_CONFIG_OTA` | `ota` | unmodified retail content |
| `TDRAW_CONFIG_TAZERO` | `tazero` | TA: Zero |
| `TDRAW_CONFIG_BTA` | `bta` | BTA |
| `TDRAW_CONFIG_MAYHEM` | `mayhem` | TA: Mayhem |
| `TDRAW_CONFIG_TWILIGHT` | `twilight` | TAF-Twilight |

The release workflow pairs each `tdraw-<profile>.zip` with that package's
recorder DLL so users install a matched pair.

### 3.1 Feature matrix at the pinned revision

The authoritative matrix is `config_<profile>.h`; values are compile-time and
have no runtime override. `—` means the profile leaves the library default
(referenced below). Only gameplay/session-relevant flags are listed; host and
UI flags are covered by the configuration surface in §4.

| Flag (compile-time) | prota | escalation | ota | tazero | bta | mayhem | twilight | Notes |
|---|---|---|---|---|---|---|---|---|
| `AREA_DAMAGE_OVERFLOW_ENABLE` | 1 | 1 | 0 | 0 | 0 | 1 | 1 | air-only splash overflow fix |
| `AREA_DAMAGE_OVERFLOW_FIX_DEDUP_CAP` | 1 | 1 | 1 | 1 | 1 | 1 | 1 | per-explosion victim de-dup (CP-DMG-1) |
| `AIR_CORPSE_FALL_ENABLE` | — | 1 | — | — | — | — | — | aircraft wrecks fall (default 0) |
| `GRID_CLAIM_TIEBREAK_ENABLE` | 1 | 1 | 0 | 0 | 0 | 1 | 1 | contested-cell claim tie-break |
| `REPAIR_RATE_FIX_ENABLE` | 0 | 1 | 0 | 0 | 0 | 0 | 0 | Escalation balance nerf |
| `REPAIR_RATE_FIX_*_MULTIPLIER` | 1/1 | 3/3 | 1/1 | 1/1 | 1/1 | 1/1 | 1/1 | repair/self-heal factors |
| `SHARE_ABUSE_GUARD` | 0 | 1 | 0 | 0 | 0 | 0 | 0 | Escalation share rate limit |
| `TAKE_CLAIM_ENABLE` | 1 | 1 | 1 | 1 | 1 | 1 | 1 | `.take` claim arbitration |
| `LAG_SWITCH_GUARD_ENABLE` | 1 | 1 | 1 | 1 | 1 | 1 | 1 | all profiles at this revision |
| `FIXED_POSN_GUARDING_CONS_ENABLE` | 1 | 1 | 0 | 1 | 1 | 1 | 1 | guarding builder hold |
| `PATROLING_CONS_RECLAIM_OR_ASSIST_ENABLE` | 1 | 1 | 0 | 1 | 1 | 1 | 1 | patrol filters |
| `CONSTRUCTION_KICKOUT_ENABLE` | 1 | 1 | 0 | 1 | 1 | 1 | 1 | build-under-own-units + kickout |
| `OFFMAP_AIRCRAFT_TARGETABLE_MARGIN_TILES` | 1 | 32 | 1 | 1 | 1 | 32 | 32 | band in map tiles |
| `DEFAULT_MEX_SNAP_RADIUS` / `MAX` | 3/3 | 0/0 | 0/0 | 3/3 | 1/1 | 3/3 | 3/3 | snap feature |
| `DEFAULT_WRECK_SNAP_RADIUS` / `MAX` | 1/1 | 1/1 | 0/0 | 1/1 | 1/1 | 1/1 | 1/1 | snap feature |
| `TDRAW_EXTENDED_WEAPON_IDS` | — | 1 | — | — | — | 1 | 1 | IDs ≥ 256 (default 0) |
| `WIND_SPEED_SYNC` | 1 | 1 | 0 | 1 | 1 | 1 | 1 | deterministic wind |
| `VISIBLE_MAP_DTS` | 1 | 1 | 0 | 1 | 1 | 1 | 1 | map DTs always drawn |
| `WEATHER_REPORT` | 1 | 1 | 0 | 1 | 1 | 1 | 1 | HUD overlay |
| `WEATHER_REPORT_WIND` / `_TIDAL` | 1/1 | 1/1 | 0/0 | 0/0 | 1/1 | 1/1 | 1/1 | per-row toggles |
| `TA_HOOK_ENABLE` / `USEMEGAMAP` / `MEGAMAP_FEATURES` / `USEWHITEBOARD` | 1/1/1/1 | 1/1/1/1 | 1/1/0/1 | 1/1/1/1 | 1/1/0/1 | 1/1/1/1 | 1/1/1/1 | host interface |
| `PLAYER_MUTE_ENABLE` | 0 | 1 | 0 | 0 | 0 | 0 | 0 | local mute command |
| `SHARE_PERCENT_ENABLE` | — | 1 | — | — | — | — | — | `%` share thresholds (Escalation only) |
| `COB_DISPATCH_TABLE_ENABLE` | 0 | 1 | 0 | 0 | 0 | 0 | 0 | Escalation-only jump table |
| `BUILD_WEAPON_SLOT_GUARD_ENABLE` | 0 | 1 | 0 | 0 | 0 | 0 | 0 | stockpile divide-by-zero fix |
| `WEAPONFIRE_DISPATCH_FROM_SLOT` | 1 | 1 | 1 | 1 | 1 | 1 | 1 | local-slot projectile kind |
| `UNIT_IDENTITY_AUDIT_ENABLE` | 1 | 1 | 1 | 1 | 1 | 1 | 1 | diagnostic digest |
| `TDRAW_2C_ENTRY_BAILOUT` | 0 | 0 | 0 | 0 | 0 | 0 | 0 | observe-only |
| `SOUND_INSTANCE_LIMIT_MS` | 50 | 50 | 50 | 50 | 50 | 50 | 50 | audio dedup window (ms) |

Flags shown as `—` fall back to the library default (`config.h`): 0 for the
feature flags, 50 for the sound window. `ALLIED_BUILD_QUEUE_ENABLE` is 0 in all
seven profiles at this revision (the overlay and its packet exist but compile
out).

**Established — documentation drift at this revision.** The shipped feature
text disagrees with the source in at least six places and is superseded by
the matrix above. `tdraw.txt` says lag-switch mitigation is disabled in the
BTA profile (it is 1 in the source, with a stale comment left behind) and
that extended weapon IDs are disabled (they are 1 for escalation, mayhem and
twilight); it describes tazero and bta as "prota minus" a few host rows when
both additionally turn **off** the two flags the source itself labels
Class B (`AREA_DAMAGE_OVERFLOW_ENABLE`, `GRID_CLAIM_TIEBREAK_ENABLE`); it
describes escalation with "that is all" when escalation also adds the COB
dispatch table, the build-weapon slot guard, player mute, share percent and
extended IDs, and mayhem as prota plus the off-map margin when mayhem also
adds extended IDs; and it has no twilight section at all (the "TAF-Twilight"
attribution rests on the source's own empty-download-file comment, CP-LIM-5).
The shipped preference file's own "TA patch default" comments also disagree
with the compiled fallbacks (`UnitLimit` 1500 versus 3663, `SfxLimit` 20480
versus 16000), which is why §4.1 keeps *code default* and *shipped* apart: a
user who deletes a line does not get the value the file claims. Beyond the
seven profiles there is an eighth, implicit developer configuration (no
profile macro defined) that selects prota and additionally enables
profiling, map dumps on error and a unit-table dump on load. Notation: the
matrix prints effective values; `WEAPONFIRE_DISPATCH_FROM_SLOT`,
`UNIT_IDENTITY_AUDIT_ENABLE`, `TDRAW_2C_ENTRY_BAILOUT` and
`SOUND_INSTANCE_LIMIT_MS` are defined in no profile header and come entirely
from the shared fallbacks, and `config_twilight.h` alone omits the player
mute, COB dispatch and slot-guard macros (landing on the fallback 0). The
shared configuration also refuses a build that omits either repair
multiplier or sets one other than 1 while the repair fix is off. The text's
"Start positions" paragraph (host and team on the odd positions in join
order) is a further drift, superseded by CP-SES-7. Reasons for the two
re-enables are recorded in the commit history.

### 3.2 Compatibility classes used by the patch authors

**Established — the source classifies its own patches**, and the classes
become compatibility rules:

- **Class A — bit-identical.** The change only affects code paths that are
  already degenerate (for example the stockpile reload-divisor clamp), or is a
  pure-speed rewrite with identical semantics (the COB dispatch table). Safe to
  enable without changing outcomes; mixed builds are not a lockstep risk by
  the authors' argument.
- **Class B — outcome-changing.** The change alters simulation outcomes; every
  player must run the same build, and demos recorded under an older build do
  not replay. In the pinned source the literal label is attached to
  `AreaDamageOverflow`, `GridClaimTieBreak` and the weapon-slot bounds check
  of `BuildWeaponSlotGuard` (the same module whose divisor clamp is the
  Class A example — its Class B half is justified by the DLL shipping with a
  new game version, so no mixed-version fleet exists). The pool expansion
  (CP-LIM-1) carries no class label; its fatal-abort rationale is the
  equivalent statement, and this document's B for it is an inference. The
  construction-behavior options and Escalation-only balance patches are gated
  per profile for the same reason. Where this document assigns a class the
  source does not state, the class is an inference.
- **Build-sync-required by construction.** Anything compile-time that changes
  *who can shoot what* (off-map aircraft margin) or *who holds a cell* is
  deliberately not a runtime setting: a per-machine override would create
  mixed fleets. The one exception to "no runtime override" is the pair of
  click-snap radii, whose profile constants are only the initial values of two
  registry-persisted dialog fields bounded by the profile's maximum and
  hard-capped at 9 (CP-CON-6) — a command-time preference, not a simulation
  rule, which is why the exception is tolerable. This precedent matters for
  Nanolathe's profile model.

## 4. Configuration surface

**Established — location and identity.** The DLL reads the preference file in
the game executable's directory, using the executable's own registered INI
name — a compiled string read out of the running `TotalA.exe`, not chosen by
the patch (retail: `totala.ini`); packages rename it (`TA.ini`, `ProTA.ini`,
`TAESC.ini`, `tazero.ini`, `Settings.ini`). The `[REG]` block and the
load-time consumers (limits, bug fixes, chat) are read on process attach,
before DirectDraw initializes; the megamap block is re-read on every map load
and the nanoframe style lazily on first use, so changes generally take effect
at the next game start. Boolean values are lower-cased and treated as true
when the string **contains** `true` anywhere (a substring test, which is what
makes the shipped file's trailing `;` comments harmless).

**Established — `[Preferences]` semantics.** Integer keys use the platform
profile API's decimal parse with a compiled default. The complete key
inventory and per-key scope is maintained in §4.1; the scope column is this
document's inference (the source classifies no key), and it decides whether a
mismatch between players matters:

- **engine capacity** (unit/weapon/type/effect/path budgets) — simulation
  buffer sizing; mismatched values between clients are a desync vector, and
  the patch treats failure to install the fixed pool expansions as fatal for
  that reason — while the other capacity keys (`UnitLimit`, `UnitType`,
  `SfxLimit`, `AISearchMapEntries`, the composite buffer) are written blind,
  with no stock-byte validation and no failure path;
- **host preference** — rendering/UI/audio only; safe to differ.

No `[Preferences]` key is per-client session behavior; the lag guard the
earlier text cited as an example is a compile-time flag.

**Established — `[REG]` registry overrides.** The parser scans the file for
the first section whose name begins with `REG` (case-insensitive), wherever
it appears, and reads entries up to the next section header or the end of
the file. Each entry is a **double-quoted** name (an unquoted name is
skipped), a `;` comment stripped first, and a value typed by the presence of
the literal `dword:` (numbers parse with C base auto-detection, so a `0x`
prefix is hexadecimal and a leading `0` octal) or a quoted string. Values are
written into the game's own registry branch at load, so settings that retail
reads from the registry (sound mode, mixing buffers, game speed, hotkey
modifier, skirmish player count, screen chat, music playback mode, display
mode) can be pinned by the INI; they are reapplied at every start, so the INI
overrides the last-used registry values, not the reverse.

**Established — the INI does not contribute to identity at this revision.**
The shared session block has a field for an INI CRC and the limits module
assigns an accessor's result into it, but the accessor's body is unreachable
(guarded by a condition that is always false, behind an archive-read path
that is commented out) and returns 0 unconditionally, and the field is read
nowhere in the tree. Nothing may treat the INI as part of session identity.

### 4.1 Key inventory

*Code default* is the value used when the key is absent; *shipped* is the value
in `src/DDraw/totala.ini`. SIM keys size simulation state and are
desync-relevant; HOST keys are local preferences.

| Key | Code default | Shipped | Scope and meaning |
|---|---|---|---|
| `UnitLimit` | 3663 | 1500 | SIM: per-player unit limit (four engine operand sites; file documents 20–1500) |
| `AISearchMapEntries` | 66650 | 66650 | SIM: path-search step allowance; retail 1333, patch x50 |
| `UnitType` | 16000 | 16000 | SIM: unit-definition capacity; <= 512 leaves stock |
| `SfxLimit` | 16000 | 20480 | SIM/HOST: special-effects vector count; backing buffer = value x10 |
| `X_CompositeBuf` / `Y_CompositeBuf` | 1280 / 1280 | 1280 / 1280 | HOST: model composite buffer (retail 600x600) |
| `UseVideoMemory` | TRUE | TRUE | HOST: draw surfaces in video memory (written back) |
| `DisplayModeMinHeight768` | FALSE | TRUE | HOST: minimum 768 height / 1024 width |
| `MegamapFpsLimit` | 60 | 60 | HOST: redraw-rate limit, 0 = unlimited (code spelling) |
| `DoubleClick` | TRUE | TRUE | HOST: double-click same-type selection |
| `ShareDialogExpand` | TRUE | TRUE | HOST: expanded share dialog |
| `MenuResolution` | FALSE | FALSE | HOST: menu at game resolution |
| `MenuWidth` / `MenuHeight` | 0 / 0 | absent | HOST: explicit menu size |
| `FullScreenMinimap` | FALSE | TRUE | HOST: megamap toggle |
| `EnhancedBuiltInMinimap` | TRUE | TRUE | HOST: re-render built-in minimap at display size |
| `MegamapDither` | TRUE | TRUE | HOST: dither instead of nearest-colour snap |
| `NanoframePreview` | WIREFRAME | WIREFRAME | HOST: FULL / WIREFRAME / DISABLED |
| `NanoframePreviewFill` | absent | absent | HOST: legacy boolean read only if `NanoframePreview` absent |
| `ShadingZeroFallbackLevel` | 5 | 5 | HOST: shade row for computed level 0 (0 disables) |
| `TeamColorNanolathe` | FALSE | FALSE | HOST: team-coloured nanolathe |
| `PlayerNStreamColors` (1–10) | built-in | built-in | HOST: 1–15 palette indices 0–255 |
| `PlayerNFrameColors` (1–10) | built-in | built-in | HOST: exactly 16 ordered palette indices |
| `ShowStockpileCounter` / `ShowTransportCounter` | TRUE / TRUE | TRUE / TRUE | HOST: counters above health bars |
| `StockpileCounterColor` / `TransportCounterColor` / `GroupNumberColor` | 255 / 255 / 255 | same | HOST: palette indices |
| `MegaMapConfig` | absent | `.\Icon\iconcfg.ini` | HOST: icon config path |
| `WheelZoom` / `WheelMoveMegaMap` | TRUE / TRUE | TRUE / TRUE | HOST: wheel behaviour |
| `DoubleClickMoveMegamap` | FALSE | FALSE | HOST: double-click to move |
| `UnderAttackFlash` | FALSE | TRUE | HOST: icon flash when attacked |
| `MegamapRadarMinimum`, `MegamapSonarMinimum`, `MegamapRadarJamMinimum`, `MegamapSonarJamMinimum`, `MegamapAntiNukeMinimum` | unset (-1) | 0 | HOST: configuration sentinel; effective defaults and the radar-jammer comparison are specified in [rendering contracts](community-patch-rendering.md) |
| `MegamapWeapon1..3Color`, `MegamapRadarColor`, `MegamapSonarColor`, `MegamapRadarJamColor`, `MegamapSonarJamColor`, `MegamapAntinukeColor` | unset (-1) | absent | HOST: ring colours (code spelling "Antinuke") |
| `PlayerNDotColors` (1–10) | per-player defaults | same | HOST: dot colours |
| `PerPlayerMarkerWidth` / `PerPlayerMarkerHeight` | 10 / 10 | 10 / 10 | HOST: whiteboard marker size |
| `PlayerMarkerPcx` | absent | `Icon\smallcircle.pcx` | HOST: marker icon strip |
| `PlayerMarkerBackground` | 9 | absent | HOST: marker background index |
| `ChatAnchor` | absent | absent | HOST: `topleft` / `topcenter` / `bottomcenter`; absent = engine chat untouched |
| `ChatPosX` / `ChatPosY` | 0 / 0 | absent | HOST: pixels or `%` of screen (-100..100) |
| `ChatBottomLines` | 8 | absent | HOST: lines reserved above the bottom bar |
| `ChatRenderer` | engine | absent | HOST: `engine` or `tadr` (TADR-owned drawer) |
| `ChatSplit` | 0 | absent | HOST: two-column chat (needs `ChatRenderer = tadr`) |
| `ChatSysGroups` | `unit,cmd,event,notice,other` | absent | HOST: kinds routed to the system column |
| `ChatGrow` | down | absent | HOST: `down` / `up` |
| `ChatLines` / `ChatSysLines` | 0 / 0 | absent | HOST: 0 = auto fit, 1..30 pinned |
| `ChatFontSize` | 0 | absent | HOST: 0 native bitmap, 1..128 TrueType cell height |
| `ChatFontColor` | blank | absent | HOST: `RRGGBB` mapped to nearest palette entry |
| `ChatFontOutline` | 0 | absent | HOST: 1 px black outline |
| `UnicodeSupport` | absent | absent | HOST: font name enables Unicode input/draw |
| `UnicodeSupport_Color` / `UnicodeSupport_Background` | 0xffffff / 0x000000 | absent | HOST: text colours |

Additional development toggles exist for megamap surfaces
(`DrawBackground`, `DrawMapped`, `DrawProjectile`, `DrawUnits`, `DrawMegamapRect`,
`DrawMegamapBlit`, `DrawSelectAndOrder`, `DrawMegamapCursor`, `DrawMegamapTAStuff`,
`UseSurfaceCursor`, `MaxIconWidth`, `MaxIconHeight`); they are not part of any
compatibility claim.

**Established — `[REG]` overrides.** The shipped file pins engine registry
settings through `[REG]`: sound mode (3D), mixing buffers (128), game speed
(10), `SwitchAlt` (1), `NumSkirmishPlayers` (10), `ScreenChat` (1), CD mode (2),
and optional `DisplayModeWidth`/`DisplayModeHeight`. The section is parsed
generically (name, `dword:` or quoted string value) and written to the game's
registry branch at load; it overrides last-used registry values, not the reverse.

### 4.2 Data-driven switches outside `totala.ini`

- **Author-facing content keys** (§6) are the per-content switches: weapon TDF
  tags, unit FBI keys, map OTA `[units]`.
- **Compile-time profile flags** (§3.1) have no runtime equivalent by design,
  with the click-snap radii as the bounded exception (§3.2).
- **Control options (ctrl-F2 dialog, in game only).** Registry-backed widgets,
  one persisted value each under a TADR-owned `Eye` subkey of the company's
  registry key — a sibling of the game's own branch, not the branch the
  `[REG]` section writes (CP-CON-5): virtual-key fields `ClickSnapOverrideKey`
  (default Alt; the widget exists only when a snap maximum is non-zero, so not
  in the ota build), `KeyCode` (the autoclick key, default `X`), `WhiteboardKey`
  (default `\`), `MegamapKey` (default Tab) and `RotateBuildKey` (default `/`);
  snap-radius integers `MexSnapRadius2` and `WreckSnapRadius2` (defaults and
  caps from the profile's snap constants, §3.1, both hard-capped at 9; the
  field is disabled and not persisted when the cap is 0); the three-way
  builder options `ConUnitsPatrolHoldPosOption` /
  `ConUnitsPatrolManeuverOption` / `ConUnitsPatrolRoamOption` (Reclaim Only /
  Both / Assist Only; defaults Reclaim Only for Hold Position, Both for the
  other two) and `ConUnitsGuardHoldPosOption` / `ConUnitsGuardManeuverOption`
  / `ConUnitsGuardRoamOption` (Stay / Cavedog / Scatter, all defaulting to
  Cavedog, the stock behavior) — both triples exist only in profiles that
  enable their feature; an F11 chat macro text field `ShareText` (default: a
  four-line macro setting energy and metal share to 1000, then share-all and
  shoot-all); and the switches `BuildMenuRotationOverlay` (default on),
  `ShowAlliedBuildQueues` (dead at this revision: its widget exists only with
  the allied-build-queue flag, 0 in every profile), `BackGround` (the
  resource-bar background: None / Text / Solid, default Text), `OptimizeDT`,
  `FullRings`, `ChatBackdrop` (default off) and `VSync` (default off).
  `DisableDeInterlaceMovie` is a further toggle under the same `Eye` key,
  created as true on first run so de-interlacing is off on a fresh install;
  `DialogPosX` / `DialogPosY` persist the dialog's position there too. The
  icon-config file named by `MegaMapConfig` carries its own `[Option]` keys
  and an `[Icon]` section; its contents are interface behavior and stay with
  [Shared draw-DLL interface](draw-engine-interface.md).
- **Input and command surface (Established from the pinned source, host-side).**
  Ctrl-F and Ctrl-B advance independent persistent cursors through the full
  owner slot block, skipping free records and wrapping once. Factory idle means
  no primary order or a primary order other than factory production. Constructor
  idle means no primary order or a ground/air standby primary order; its prior
  health percentage sample must also be neither zero nor one. Secondary orders
  do not participate, and the constructor scan has no separate completion gate.
  A hit replaces selection, centres the view and discards prepared placement;
  an empty full pass clears selection and resets the cursor. Authored `CTRL_F`
  or `CTRL_B` membership wins when nonempty. Otherwise the fallback takes
  builders excluding air bases and the class with both `showplayername` and
  `hidedamage`, split by `bmcode` into factories and mobile constructors. It
  does not subtract `CTRL_W`. Ctrl-S selects on-screen `CTRL_W` members with
  `canfly` clear; the release-note `NOTAIR`/`NAIR` description has no lookup in
  this pinned implementation. The Ctrl-Z / Ctrl-A / Ctrl-B / Ctrl-C
  selection truncation for definition IDs at or above 512 is fixed
  (CP-LIM-2); Shift with `q` and `e` alternates mex building and reclaim
  while snap is active; the `v` key sets a builder's movement option before a
  patrol route; a queued build or move order can be left-dragged to a new
  position; the share dialog and battleroom gain buttons that issue
  `+noshake`, `.ready`, `.autopause`, `+shootall`, AUTOTEAM, RANDOMTEAM and
  CRCREPORT (host-only); `+bps` keeps the throughput readout visible over the megamap;
  the separate clock option keeps game time visible there; and the F11 macro text is no longer relayed to
  other players. Source: `ExternQuickKey.cpp`, `tahook.cpp`, `dialog.cpp`,
  `sharedialog.cpp`; `tdraw.txt`.
- **Double-click selection (Established from the pinned source, host-side).**
  `ExternQuickKey::Message` handles both left and right double-click messages
  through the same branch when `DoubleClick` is enabled. The live-game gate
  must pass, the replacement megamap must not be displaying, and the key read
  from `Eye/KeyCode` (default `X`) must not be held. The nearby source comment
  calls this the whiteboard key, but the constructor reads `KeyCode`, not
  `WhiteboardKey`; those settings must not be conflated. The pointer must lie strictly inside the game viewport
  and identify an own unit. The branch replaces selection with on-screen own
  units matching the definitions already selected; it does not add the hovered
  definition or make Shift additive. Both mouse buttons are admitted regardless
  of interface type: the source's old interface-type filter is commented out.
  The input is an already-classified host double-click message; this function
  does not define its timing or pixel tolerance. Source: `src/DDraw/ExternQuickKey.cpp`,
  `Message` and `SelectOnlyInScreenSameTypeUnit`, pinned revision `dcff5dd`.
  This current-source contract does not establish the historical 4.8 DLL's
  behavior.
- **Legacy keys.** `WeaponType` and `MultiGameWeapon` in the shipped INI belong
  to a commented-out legacy weapon-ID patch: neither is read anywhere in the
  tree, and the class that read them survives only as a forward declaration.
  They do not control the current extended-ID module, which is compile-time
  (CP-WPN-6); `UnitType` is read and does drive the definition capacity.

## 5. Behavior contracts

Contract IDs are stable (`CP-<area>-<n>`). Each cites the source module and
carries a class: **A** = bit-identical where not triggered; **B** = changes
outcomes and requires a matched fleet; **P** = presentation/host only; **S** =
session/protocol (Tier 3). Arithmetic is exact where stated; when the source
states a value or comparison, implementers must reproduce it, not approximate.

### 5.1 Engine capacity and limits

**CP-LIM-1 (B, inferred). Fixed effect/projectile pools.**
`EngineLimits` raises four fixed pools to ten times stock: active projectiles
300 -> 3000, explosion effects 300 -> 3000, flying model-piece slots 100 -> 1000
(backing store 100000 -> 1000000 bytes, derived as stock bytes × new slots ÷
stock slots and pinned by a compile-time assertion), auxiliary debris/effect
records 300 -> 3000. The projectile cleanup routine's stack reservation grows
to fit the relocated index arrays (new limit × 4 + 16, with each intervening
page probed because the growth exceeds a page). Allocation above the new caps
is still silently discarded: the engine's own cap comparisons are retargeted
for projectiles and explosions, and the replacement auxiliary allocator
scans round-robin and returns nothing when full. This module validates every
stock byte image before writing, inside an exception guard; a mismatch aborts
the whole install, a partial write rolls back every applied patch in reverse
order, and a deferred check at two later start-up points shows a modal error
box naming desynchronisation as the reason and **terminates the process**
rather than running with mismatched limits, because the projectile cap is
simulation-visible and a mixed fleet can desynchronise after the stock cap
(the auxiliary pool's own comment calls it visual-only, so the rationale rests
on the projectile and explosion pools). Install is also refused once the
engine has allocated its projectile pool. Source: `EngineLimits.cpp`; author
documentation in `tdraw.txt`.

**Established — auxiliary owner mapping.** Matching the replaced allocation
loops and their callers to the retail shatter path identifies the auxiliary
records as the fragment-geometry pool of [04 R-COB-04 §3] and [04 R-COB-04 §4]: one eight-vertex,
six-face geometry record paired with each admitted shatter effect. They are
separate from the flying whole-model-piece slots. The enlarged geometry
allocator starts at its retained cursor, wraps at capacity, claims the first
free record, and advances the cursor to the following record; a full scan
returns no record. Battle initialization resets that cursor.

**CP-LIM-2 (B, inferred). Runtime capacity settings.** `LimitCrack` writes the
`totala.ini` values into engine constants on process attach, before the pool
module, with no byte validation and no failure path:
- `AISearchMapEntries` (code default 66650, shipped 66650) overwrites the path
  search's per-call step allowance with one four-byte data write; retail's
  1333 is the documented baseline (the shipped file's own comment). This
  settles the earlier open question in
  [community patch pathfinding](community-patch-pathfinding.md) as far as
  the source can: the adjuster writes a compiled step-allowance field and the
  shipped adjustment is exactly ×50; that the field is the one our retail
  research names is the retail-side identification, not something this tree
  can show.
- `UnitType` (default 16000) enlarges the unit-definition capacity at seven
  patch groups: two AI parsing stack frames, the name-lookup OR loop count,
  two AI bitmask clear counts, the ctrl-Z same-type selection frame, and the
  spot-finding site's pushed bitmask size and copy count. The derived frame
  size is `(value ÷ 8 ÷ 64 + 1) × 64` bytes and the iteration counts are that
  size ÷ 4. Values `<= 512` (non-strict) keep stock behavior. Selection, AI
  build-target parsing and category membership all scale with it.
- `SfxLimit` (code 16000, shipped 20480) sets the special-effects vector count
  at 20 sites and, through a twenty-first hook, its heap backing size =
  value × 10.
- `UnitLimit` (code 3663, shipped 1500) sets the per-player unit limit at four
  operand sites, including the multiplayer limit.
- `X_CompositeBuf`/`Y_CompositeBuf` (1280) size the model composite buffer
  through one patch rewriting both pushed dimensions (stock 600 × 600).

**CP-LIM-3 (P). Display-mode minimums.** With `DisplayModeMinHeight768`, five
patches install: the display-mode **enumeration** minimum (height only — any
width is still listed, as the shipped file states), the compiled default
height and width, and a strict-less-than raise-to-minimum clamp on the height
(768) and width (1024) read back from the registry. Nothing is ever lowered.
Host preference. Source: `TABugFix.cpp` (`MenuResolution.cpp` is the separate
main-menu resolution module and carries none of this).

**CP-LIM-4 (A, inferred). Can-build array overrun fix.** The can-build array
allocation grows from 60 to 72 bytes and the copy from 15 to 18 dwords,
removing an overrun for definitions with many build entries. Installed
unconditionally. (This is not the build-menu table of CP-LIM-5.)

**CP-LIM-5 (A, inferred). Empty download menu fix.** The build-menu table
allocated for `download/*.tdf` (one fixed block per file) comes from an
allocator that does not zero in release builds, and a block's section count
is written only when the file parses at least one section, so an empty file
leaves heap garbage the consuming loop walks out of bounds; the allocation is
routed through a zeroing wrapper so a never-written count reads 0 (the failure
mode is fatal under Wine and benign on native Windows, where the slot happens
to read 0; TAF-Twilight ships ~307 empty files).

**CP-LIM-6 (B, profile-gated). Weapon table capacity.** The weapon record
table is a fixed 256 entries in stock; with the extended-ID flag a heap-backed
overflow raises the usable ID range to 4096 (CP-WPN-6 owns the semantics).
This is the capacity the dead `WeaponType` key once controlled.

### 5.2 Simulation defect fixes

**CP-FIX-1 (A by the source; outcome-changing against retail). Resurrection
finalization.** The resurrection path snapshots the validated wreck
information before unit creation (it already replicates the engine's wreck
lookup to learn the wreck's stored orientation: it walks the order's target
position to the feature tile, follows a sub-tile marker back to the root tile,
requires the feature definition index to be in range and the definition to
carry the wreck flag, and keeps root tile, definition index and animation
index, plus the order's target-unit pointer as it stood before creation) and
re-checks the outcome afterwards: when the post-create wreck lookup returns
the failure sentinel (because creation consumed the wreck), the recovery
requires the snapshot to be valid, an order to be recorded, the order's
target unit to be non-null **and different from the pre-creation target**,
and the ordered type to be non-zero and equal to the created unit's type. It
then rewrites the root tile's animation index only — deliberately not the
definition index, so the wreck stays absent under the new unit — and supplies
the root tile and definition index to the engine as if the lookup had
succeeded, so the normal finalization continues instead of the engine
abandoning the new unit half-finished. Successful resurrections, invalid
wrecks and retryable creation failures are unchanged; a return thunk clears
the snapshot on every exit path. Ships in every profile (the module installs
unconditionally). Source: `unitrotate.cpp` (the rotation work owns the
pre-create wreck read); author text in `tdraw.txt`.

**CP-FIX-2 (B). Unit identity recycling holdback.** Unit slots freed by death
enter a per-player FIFO stamped with game time (the hook sits on the
unit-death receive path, cross-checks the packet's unit index against the
unit, and validates owner 0..9 and per-player index below the capacity
field) and are not handed out again until at least 150 ticks (5 s) have
passed — the gate is inclusive at exactly 150. A never-used high-water slot
is preferred first (a bump pointer walks never-issued slots; an occupied one
is consumed and skipped), then the oldest qualifying freed slot; because the
queue is age-sorted, a too-young head ends the search. **If no slot qualifies
the creation is refused** through the engine's own no-identifiers-available
outcome — stock would have reused a young free slot and succeeded, so this is
a refusal where stock creates, not "as stock". When the engine calls with a
requested non-zero index, the hook uses that slot if empty and otherwise
routes to a failure path, bypassing the FIFO. Slots occupied behind the
allocator's back (network creation writes the ID directly) are re-checked
and skipped, and a skipped slot rejoins the FIFO when its unit later dies.
All ten lists reset at game time 0 or when the capacity field changes; an
out-of-range player index falls back to the stock lowest-free scan.
Installed in every profile. Purpose: stale damage packets naming a recycled
slot cannot hit a live unit of the wrong type (the known "units exploding in
factories" defect and the weapon-fire divide-by-zero). Source: `TABugFix.cpp`.

**CP-FIX-3 (A). Ghost commander correction.** Two halves, both in
`TABugFix.cpp`. (a) *Receive side:* while game time in ticks is strictly
below the engine's per-player unit-slot capacity field (the block size the
`UnitLimit` setting sizes; the shipped 1500 gives the documented "first
50 sec"), a received movement packet from an active, non-watching, non-local
player (the packet's sender) whose **first** entry names unit index 0 (the
commander) and carries a movement payload snaps the local copy of that
commander to the entry's from-position. Only the first entry is examined, the
entry's type is not checked against the commander's, and the hook runs before
the engine's own handler. (b) *Assist side:* at tick 90 (3 s) exactly, each
locally controlled player (active, not watching, local human or local AI)
broadcasts, for every live land-move-class unit in its own block below the
lesser of the block size and the tick count, one dummy one-entry movement
packet carrying that unit's type and current position (the destination is the
unit's pending order position only when the order exists and both its
coordinates are strictly positive; otherwise the current position) plus the
end-of-list marker. **Unknown — the assist entry's unit-index field.** Every
such entry is written with unit index 0 (the commander) while the type and
position come from the iterated unit; the 2024 rework that introduced the
loop changed only the payload (the original fix sent one commander-only
entry per player). Two leads narrow it without closing it: the identity
module (CP-UID-2) describes the engine's entry reader as resolving each
entry's candidate unit from the entry's own index field and comparing the
entry's type against that unit's — which would make the broadcast
commander-only in effect — and the schema-unit spawner (CP-UD-3) deliberately
places non-commander initial units at per-player slots 90 and above, the
assist loop's upper bound, with the comment that the fix "only works for
commanders". **Supported inference:** the broadcast is commander-only in
effect and the other entries are ignored by type mismatch. What would settle
it: a read of the engine's movement-packet entry parser or a two-client
observation. Session-visible fix for the known "ghost com" defect (the
shipped changelog: remote commanders appear in the map corner during the
first 50 s).

**CP-FIX-4 (B). Deterministic wind.** With `WIND_SPEED_SYNC`, the wind update
is replaced by a deterministic sequence so all clients see identical wind. The
generator is a C++ standard-library `std::default_random_engine` created once
per process on the first wind update and **never re-seeded** — it persists
across games in one process — seeded from the hosting player's DirectPlay
identifier (the host is the player with player number 1 and a positive
identifier; otherwise a steady-clock reading truncated to 32 bits). Under the
Microsoft standard library that engine is the 32-bit Mersenne Twister — a
library fact the repository does not pin, so a bit-exact reimplementation
must take it from the library, not from this source. Each update draws in this
fixed order and adds the interval to the stored next-update tick (it does not
re-anchor it):

1. `r % 10` → the next interval is `30 * (5 + r % 10)` ticks (150–420);
2. only when `max > min`, one draw: the speed is `r % (max - min) + min`, i.e.
   uniform in `[min, max)` — **the maximum itself is unreachable** (the earlier
   `[min, max]` statement is falsified by the modulo); when `max <= min` the
   speed is `min` and no draw is taken;
3. only when the resulting speed is strictly positive, one draw: the direction
   is `r % 65536`.

The conditional draws are part of the contract: a zero speed consumes no
direction draw, so the sequence is only shared across clients whose wind
fields agree — and, because the generator is process-lifetime, only across
clients whose generators have consumed the same number of draws since
seeding; a client that played an earlier game in the same process diverges.
OTA profile disables the module. Source: `TABugFix.cpp`.

**CP-FIX-5 (A by the source; outcome-changing against retail). Antinuke
coverage and interceptor targeting.** The antinuke interceptor's target search
(weapon slots 0..2 with the slot's enabled flag) uses a circular
horizontal-distance test of radius equal to the weapon's coverage: a per-axis
bounding pre-test, then a 64-bit squared-distance compare in 16.16 map
coordinates, rejecting only when the squared distance is **strictly greater**
than the squared radius (a projectile exactly at the radius is accepted). It
skips the owner's own projectiles, non-targetable projectiles (no weapon, or
the weapon lacks the targetable bit), and projectiles already targeted by
another interceptor, and it returns the **first** qualifying projectile in
pool order, not the nearest. The minimap coverage ring is changed to match:
a fixed subtraction of 512 from the coverage value before scaling is removed,
both in the engine's ring draw and in the patch's own minimap module for all
three slots. Both patches are skipped with a log if the stock bytes do not
validate. Installed in every profile. Source: `TABugFix.cpp`, `UnitMinimap.cpp`.

**CP-FIX-6 (P/S). Radar jamming and observers.** Radar/sonar jammers owned by
allied units no longer suppress the contacts of the player whose point of
view is current; when a watcher is following one player's point of view, the
strict player-ally test is used instead so enemy jamming is still simulated
for that view. Installed in every profile. Source: `TABugFix.cpp`.

**CP-FIX-7 (A). Null/edge-case guards.** Installed guards: a null unit-death
victim (the routine is skipped), zero yardmap or footprint definitions (no
yardmap, or a zero footprint extent, skips the walk), unit-death prepared-order
reset (on death, before the interface update, the prepared order type is
forced to stop), display of the resource strip (its frame bottom clamped just
above the game-screen top), a multi-player departure announcement when the
departing player is already out of the table (suppressed with a log when no
slot with a non-zero controller matches), and chat text arriving through a
staging buffer that is not in fact a chat packet (the text is not printed).
Two guards the earlier text listed — a zero turn volume and a unit ID beyond
the live array in model drawing — exist as handlers but are **never
installed** at this revision (dead code). The unconditional comparison patch
previously recorded as a relaxed circle-radius test is a **divide-by-zero
guard in a circle-drawing routine**: the routine truncates a floating value to
a segment count and divides a full turn by it; stock skips the loop only for a
negative count, so a count of exactly zero divided by zero, and the patch
widens the skip to "count at or below zero" so a zero-count circle draws
nothing. It excludes zero rather than permitting it and is presentation only;
the earlier Unknown is closed. These remove crash paths without changing
valid simulation behavior. Source: `TABugFix.cpp`.

**CP-FIX-8 (TOOL). Order-dispatch validation.** The COB order-handler dispatch
validates the handler index against the table size and the function pointer
against the executable's code range, records breadcrumbs, and (when the
file-local compile-time switch `TDRAW_ORDER_DISPATCH_BAILOUT` is enabled; 0 at
this revision) bails out of a corrupt dispatch: the main and background
controllers to their epilogues, and a third site in the cancel-order teardown
skipping only one optional notification. The source describes a bail-out as
costing one tick of that unit's order processing and writing nothing to game
state; the default is observe-only because a client that survives never
produces the crash report the authors want. Source: `TABugFix.cpp`.

**CP-FIX-9 (A/P). Crash hardening and diagnostics.** Composite AABB clamping
(to the live enlarged composite-buffer size), GAF frame-pointer validation and
copy-screen-context bounds validation (both observe-only: they record and
never change flow), the bad-model size hunter (a model whose offscreen
extent exceeds 600 in either dimension is logged, reported in a message box
and not drawn that frame — presentation only), the vectored crash handler
with breadcrumb rings and process-exit instrumentation, and the
sound-instance limiter (50 ms window, keyed per sound-effect object, audio
only) are host-side hardening/diagnostics. No simulation effect. Source:
`TABugFix.cpp`.

**CP-FIX-10 (B). Extended weapon IDs.** See CP-WPN-6.

**CP-FIX-11 (P/S). Always-on platform and host patches.** Installed in every
profile with no switch: the CD-check path is disabled by three raw patches
(the game runs without the disc); the `+lostype` console handler's command
level is raised to the cheat level (`tdraw.txt`: "not available unless cheats
enabled"; whether use then marks the game as cheated is engine behavior the
source does not state); four GUI error-length operands are patched to 128;
one single-player start-button operand is patched — **Supported inference**
from the author's changelog ("Enable start button in multiplayer lobby if
only one player + AI are present"), not proven by code; metal/energy received
through sharing is subtracted from the displayed produced totals
(player-visible accounting only, and the same figures feed the replay-host
module); the victory-condition sound plays when the victory screen appears
(throttled to once per 300 ticks); the player logo colour is saved
unconditionally per player number and restored only while in game; a dead
host is placed in watch mode with an explanatory message rather than leaving
the session; the session-description password is cleared before the host
republishes it; and a campaign forces the cheat flag on while every mode
syncs the software-debug cheats bit to it so the cross-player hash sees one
source of truth. CD-music pause hooks install only when an `audiere.dll`
module is present in the process (audio only). Source: `TABugFix.cpp`.
### 5.3 Weapon definition tags (author-facing)

All tags are read from each weapon TDF section as integers, tested `& 1`
(absent = 0, so `=1` and `=3` enable and `=2` does not), through one shared
parser hook that runs every registered handler once per weapon definition on
every weapon load or re-load. `surfacefire`, `nottounderwater`, `notoverwater`
and `notoverland` are stored in a shared four-bit side table keyed by the
weapon definition, **re-assigned on every load** (so a re-parse without a key
clears it); `nomapweaponalert` keeps a definition set and `reloadbar` keeps
two (a definition set and a weapon-name set), both **insert-only and never
cleared on re-load**; `nottoair` keeps no registry at all and writes a bit
into the engine's own weapon word (CP-WPN-1). The key readers are registered
in every profile unconditionally. Hot-path installation differs per module:
`SurfaceFire` (two independent lazy groups, one for `surfacefire`'s five hooks
and one for `nottounderwater`'s single gate) and `TerrainFireGate` (one hook,
installed by either key) install only when a loaded weapon first carries the
tag with its low bit set, and `NotToAir`'s check hook likewise installs only
when a weapon's value is first seen **enabled** (`nottoair=2` installs
nothing); `ZeroDamageMapWeapons` (detonation path and native minimap
projectile draw) and `ReloadBars` (the unit-bars draw, shared with the status
counters) install eagerly at load whatever the content authors. Content that
uses none of these keys therefore pays a null check, a damage compare and an
attacker compare per detonation, and nothing else; only the authored data
decides what is active.

**CP-WPN-1 (B). `nottoair = 1`.** The weapon cannot select or fire at a unit
whose position-committed unit-side movement state is airborne (the low two
bits of the target's state word equal the airborne value, independently of a
new live mover mode that has not yet been published; a grounded aircraft reads grounded and flips
on lift-off, per a live observation the source records). At parse time the
handler ORs a not-to-air bit (bit 31, the one free bit of that word) into the
weapon definition's engine type-mask word — the only weapon-definition state
the key writes, and never cleared, so a re-parse without the key leaves the
bit set (a deliberate asymmetry: the side-table tags are assigned per load
precisely because no wipe site exists for this word). At runtime the shared
per-weapon target-eligibility check returns "no target" (through the
engine's own rejection exit) when the target is airborne and that bit is set.
The source establishes one check site; which callers reach it is an
inference, and the source does not claim manual-fire immunity (its
`surfacefire` text, by contrast, says both auto-aim and manual targeting are
affected). `SurfaceFire` deliberately lets `nottoair` win over `surfacefire`
at both of its bypass sites; `TerrainFireGate` does not consult it. Source:
`NotToAir.cpp`, `SurfaceFire.cpp`; author text in `tdraw.txt`.

**CP-WPN-2 (B). `nottounderwater = 1`.** Intended for `waterweapon = 1`
weapons (a plain weapon already rejects submerged targets natively, and the
gate sits inside the water-weapon branch): a target whose bounding-box top is
at or below sea level is rejected, where the top is the target's vertical
position plus the definition's vertical bounding extent, both **truncated to
whole world units** before the `<=` compare against the byte sea level. The
gate sits at the single convergence point every "allow" path of the
water-weapon branch funnels into before the range test, so it also rejects a
submerged target the stock path would have accepted for reasons unrelated to
`surfacefire`. Targets above the waterline remain eligible, so a water weapon
can hit surface ships while leaving submarines to torpedoes. The reject
deliberately routes to the engine's second rejection exit so that a weapon
carrying both `surfacefire` and `nottounderwater` cannot ping-pong between
hooked sites (the source documents that a hard freeze would otherwise
result). Source: `SurfaceFire.cpp`.

**CP-WPN-3 (B). `surfacefire = 1`.** With `waterweapon = 1`, extends the
weapon to surface targets: both stock "target is above sea level, reject"
checks are bypassed (the ordinary-target site and the hovercraft-class site,
both redirected to the range-check convergence); the can-aim depth sequence is
short-circuited to its success exit, skipping everything it would have
decided from the hook point on, not only the firer-depth rejection; the
**COB script-action** ATTACK gate for submersibles is bypassed — it consults
the tag on **weapon slot 0 only**, so a submersible whose first weapon lacks
the tag is still rejected whatever a later slot carries; and in-flight
guidance is restored **unconditionally for any tagged self-propelled
projectile** (stock kills guidance for a water weapon whose projectile is at
or above sea level — `>=`, not strictly above — and the redirect does not
test the target). `nottoair` still wins over `surfacefire` for airborne
targets. Author text: both auto-aim and manual targeting are affected.
Source: `SurfaceFire.cpp`; author text in `tdraw.txt`.

**Established — can-aim owner mapping.** The separate can-aim hook is the
shot-time point gate of [06 R-WPN-05 §9]. Its inclusive range comparison has
already passed. A tagged weapon takes success before the water-weapon branch,
skipping both the shooter-depth rejection and the subsequent ballistic
feasibility check. This hook tests the tag alone, so it also affects a tagged
non-water weapon; an ordinary water weapon already takes the same success
route. It does not bypass range or change the unit-target acquisition gate.

**CP-WPN-4 (B). `notoverwater = 1` / `notoverland = 1`.** The weapon does not
fire while the **firing unit** is over water (`notoverwater`) or over land
(`notoverland`): terrain height under the firer's horizontal position versus
the map sea level (height <= sea level is water, strictly above is land; a
negative height — the engine's off-map answer — falls through to stock
behavior, because it would otherwise read as water). Altitude is ignored, so
an aircraft is gated by the terrain it flies over rather than by being
airborne. The single hook sits in the per-tick weapon-slot loop and abandons
the rest of that slot's pass: it runs after the reload decrement and before
trajectory, aim script, fire callback and resource debit (the source chose
the site for exactly that ordering, rejecting the obvious alternative because
another module owns it and gating there would leave the turret tracking).
Code-established: the reload keeps counting and the four later stages are
skipped. Author-documented (`tdraw.txt`), not separately established by the
code path: the weapon keeps its target, its turret stops tracking and holds
its last angle, no ammunition or per-shot resources are consumed, and it
resumes with no reload penalty on crossing back. Whether every scripted or
commanded shot passes through that loop is an inference. A weapon with both
tags never fires. The tag is per weapon definition, so every unit mounting
that weapon is affected; the author's workaround is a per-unit copy of the
weapon under a new name and a spare ID. Source: `TerrainFireGate.cpp`;
author text in `tdraw.txt`.

**CP-WPN-5 (B). `nomapweaponalert = 1`.** For map weapons: when a projectile
has a weapon, that weapon's damage is 0, the projectile has no attacker unit
and the weapon carries this tag (evaluated in that order, the set lookup
last), the entire area-damage-and-broadcast call at detonation is skipped —
for a damage-0 weapon that removes the under-attack message, the alarm sound
and the area-damage broadcast side effects, and no damage, since nothing was
owed — and the projectile's minimap and megamap markers are not drawn (the
native projectile-draw path skips to the next projectile; the megamap drawer
returns early on the same predicate, suppressing the whole marker including
the nuke/interceptor icon path). The projectile itself still exists and
detonates. The intent is harmless weather/map effects (hail, storms). Source:
`ZeroDamageMapWeapons.cpp`, `ProjectileMap.cpp`; author text in `tdraw.txt`.

**CP-WPN-6 (B, profile-gated). Extended weapon IDs (0..4095).** With
`TDRAW_EXTENDED_WEAPON_IDS` (escalation, mayhem, twilight), a heap-backed
overflow array — allocated lazily and zero-filled on the first overflow ID,
aligned so the engine's own slot arithmetic lands inside it — provides weapon
slots above the stock 256. TDF `ID=` values 0..4095 load (0..255 take the
untouched base path; an absent `ID` keeps the engine's own negative default
and the base path) and are referenced by name: the extension hooks the
engine's name-not-found exit, so overflow slots are scanned only **after**
the base scan fails (base names win), case-insensitively over populated slots.
An `ID` of 4096 or more logs and its definition is then loaded into slot 0 —
the engine's permanent no-weapon sentinel — which the source notes is benign
only while no weapon is authored with `ID=0`. Overflow slots are stamped
`ID = 255` (the engine's own initializer sets the byte ID only for base
slots, and zero is the no-weapon sentinel several engine paths early-out on,
including the arming bit at weapon-script start and AI build scoring) with a
documented collision against a real base slot 255. A whole-array weapon
reload wipes the overflow population bitmap and buffer, so stale entries
cannot survive a mod reload, while per-slot sub-allocations are leaked across
it (source-stated). Fire events for overflow IDs are carried in a
recorder-safe chat-envelope packet (§5.9) because the native fire packet has
only a byte ID; the send side suppresses the native broadcast only for IDs at
or above 256, and a client without support silently drops the event. Source:
`WeaponIdOverflow.cpp`, `WeaponFiredExt.cpp`.

**CP-WPN-7 (P). `reloadbar = 1`.** Draws a reload-progress bar a fixed offset
below the health bar for a unit owned by the local human player; the tagged
slot with the largest reload time is shown, and the bar fills from empty to
full over the reload. It is drawn only when a specific game-option bit is set,
never for a nanoframe, never for a zero reload time, and **never for a
stockpile weapon**; tag membership matches by definition or by weapon name,
so a name match survives a reload into a different slot. Presentation only.
Source: `ReloadBars.cpp`.
### 5.4 Damage and claim mechanics

**Established — shared callback timing.** The patch's callback named “game
tick” is invoked in the outer game loop after its simulation catch-up work,
not inside the authoritative sub-tick. Its callbacks can run repeatedly at the
same game time or once after several sub-ticks. Among the adopted modules,
registration order is deferred schema spawning, transported-death capture
pruning, then area-overflow indexing. The area index alone suppresses a repeat
rebuild at the same game time. Its indexed footprint population is therefore
an outer-loop snapshot, not a lazy snapshot at the first blast and not an
index rebuilt immediately before each projectile phase. This source behavior
can depend on host batching. A deterministic Nanolathe projection requires an
explicit design decision; this reference does not authorize one. Evidence:
`GameTickHook.cpp`, registration in `ddraw.cpp`, and retail outer-loop call
ordering [01 §4.4].

**CP-DMG-1 (B). Area-damage overflow (air stacking).** Stock area damage
visits two occupant slots per map cell (unit slot zero and unit slot one
[06 §9.3]). With
`AREA_DAMAGE_OVERFLOW_ENABLE` (escalation, prota, mayhem, twilight) the victim
enumeration visits eight `(cell, selector)` pairs: the two stock selectors
return the stock reads byte-for-byte (then pass through the dedup step
below), and six DLL-owned overflow slots per cell (six 16-bit slots plus a
tick stamp) expose additional airborne units. Airborne units are indexed once
per distinct game-time value by walking the unit array in order and inserting
each eligible unit into the first free overflow slot over its claimed
footprint — the grid anchor plus footprint extent, **clamped to the map**,
so the index can cover fewer cells than a stock claim but never more; a unit
already present in a cell is not duplicated. Eligibility requires an occupied
pool slot, the airborne layer, alive and not pending death, not cargo, not
parked in the off-map bucket, a non-zero index below the dedup table size,
and a non-degenerate footprint. **There is no range test at indexing time**:
blast range is decided solely by the engine's unchanged cell walk. Victim
de-duplication uses a per-explosion generation counter bracketing each
area-damage call (saved and restored, so a nested explosion cannot clobber
the outer one): a unit whose stamp is **at or above** the current generation
is skipped on later cells, so a unit marked by a nested higher-generation
blast is skipped by the outer one too; generation 0 (no bracket active) fails
open. The dedup-cap macro is 1 in all seven profiles but takes effect only
where the module compiles in (the four enabling profiles): with it, duplicates
are suppressed on every selector including the two stock ones, which also
repairs stock overflow beyond its 20-entry victim list (the source's own
claim: past 20 distinct victims stock stops recording but keeps damaging, so
a later victim is hit once per cell it occupies); without it only the
overflow selectors are deduplicated and the stock cap bug stays. The blast
rectangle, falloff, edge effectiveness, damage values, firer self-exclusion
and broadcast are unchanged, and no floating point is added — though the
ground **victim set** does change past 20 victims when the cap is on.
Capacity is six overflow units per cell (occasionally seven when the vanilla
air slot is held by the race loser: the indexer inserts every airborne unit,
including the one already holding the stock air slot); additional aircraft
remain unreachable and the module counts saturation events (one per
(cell, unit) insert failure per rebuild; the author's log records a
26-aircraft stack producing five million events in one session) rather than
pretending otherwise. Class B: the source states every player must run the
same build and old demonstrations do not replay. Source:
`AreaDamageOverflow.cpp`.

**CP-DMG-2 (B). Contested-cell claim tie-break.** With
`GRID_CLAIM_TIEBREAK_ENABLE` (escalation, prota, mayhem, twilight) the
spatial-grid claim conflict rule becomes: the unit with the **lower
UnitInGameIndex wins** the cell (unsigned 16-bit compare, strict less-than to
evict the incumbent; a unit re-claiming its own cell keeps it, as in stock).
This replaces the stock client-relative rule (keep if the incumbent's player
is inactive; otherwise evict if that player is, from this machine's view, a
remote human; otherwise keep), so every client records the same owner for a
contested cell. The rule is patched at six sites: three cell kinds — the
yardmap/building path, the ground slot and the air slot — in each of two
functions, the re-claim-after-vacate path and the per-move stamp path; the
source stresses that patching one function alone would resolve the same
contest by two rules on alternating ticks, so installation is all-or-nothing.
It is used by every unit type and changes who direct-fire projectiles collide
with and VCRAFT landing decisions. The `PlayerActive` test is dropped — the
incumbent's player record is never dereferenced — so a stale owner can no
longer fault the claim path, and, as a consequence the source states, an
inactive player's lingering unit **can** now be displaced by a lower-index
claimant where stock never displaced it; on the owning client the rule
changes from "first claimer holds" to "lowest index holds". Class B. Source:
`GridClaimTieBreak.cpp` (the full scope warning is in its header;
`config_escalation.h` carries a short note pointing to it).

**CP-DMG-3 (B, content-driven). Transported explosion overrides.** Two optional
unit FBI keys replace the death explosion while the unit is transported:
`TransportedExplodeAs` and `TransportedSelfDestructAs` (independent, empty by
default; absent falls back to the stock key, and self-destruct versus kill is
read from the engine's own branch flag at the selection site). The override
applies when the unit is currently transported (carrier pointer non-null at
selection), was transported at the moment of death (captured by a pre-death
hook and cleared by the post-decision hook or consumed at selection), or was
captured at a transport's passenger-death pass (pointer, index, type and
health recorded; consumption matches pointer, index and type, not health).
The selector consumes a matching passenger capture even if the current carrier
or pre-death capture already qualifies, then clears the single pending
pre-death capture. The health sample is used only by pruning. Those captures are not
tied to an event or tick: they persist until a per-tick sweep prunes them
(identity changed, or the unit alive with changed health and not pending
death), so a captured passenger that survives untouched and later dies is
still treated as a transported death. Ground deaths are unchanged; an
unknown weapon name logs and falls back to stock, and an applied override
logs one line per death. Installed in every profile (no macro; four
byte-validated hook sites). Source: `TransportedExplosions.cpp`; author text
in `tdraw.txt`.

**CP-DMG-4 (B, Escalation balance). Repair-rate fix.** With
`REPAIR_RATE_FIX_ENABLE` (escalation only) the engine's whole repair/HealTime
helper is replaced at its single entry point, covering all five callers (four
active-repair order paths and the passive `healtime` regen path; the caller
class is discriminated from the return address — the passive site gets the
self-heal multiplier, every other caller the repair multiplier — because
"repairer equals target" would also match the self-repair order). Per call,
with `t` the caller's work quantum (a float that is integral at every caller;
the HP path truncates it toward zero, the energy path uses it as is):

- **HP owed** = `maxHP × t × multiplier` in signed 64-bit arithmetic, split
  into `whole = product / buildTime` (truncating) and a remainder banked per
  (repairer, target) pair; the bank adds a point whenever it reaches
  `buildTime` (at most one carry per call). A repairer holds two
  target-tagged banks: a lookup by target that misses claims the first empty
  slot, else overwrites slot 0; claiming starts the bank at zero.
  **Established — bank lifetime:** the repairer index selects the pair and
  the target's array-slot identity is its tag. The table is reset when the
  engine's unit array changes, not when an individual slot dies. Reusing the
  same repairer and target slots can therefore retain their matching fraction;
  a different target tag resets the claimed bank.
- **Energy** = `max(1, trunc(1 + (buildCostEnergy × t − 1) / buildTime))`,
  evaluated in the engine's own extended-precision sequence and its own
  truncating conversion helper (deliberately not reimplemented in C++ float
  arithmetic, for lockstep fidelity; this equals a ceiling only when the
  product is integral, so the truncating form above is the exact one). The
  multiplier is never applied to energy.

Guard order is load-bearing and fixed: full-HP check (refuses), `buildTime
<= 0` (refuses — stock has no guard there: its float divide yields infinity,
the truncating helper maps that to the minimum integer and the `max(1, ·)`
then heals exactly one point, without faulting), energy term, resource gate
**before** the bank is touched (a failed-energy tick cannot discard banked
fraction), `t <= 0` returns truthy 1 (two of the five callers gate the nano
beam on the return value), divide and bank, `whole <= 0` returns 1, HP
clamped to 65535 (the applied amount travels as 16 bits and the store
compares unsigned), then a raw kind-10 heal of `whole`. With no accumulator
available the fallback rounds the multiplied numerator up (a minimum of one
point) and counts a canary. The 3x/3x repair/self-heal multipliers are
Escalation balance (asserted to 1..100; other profiles carry 1x and the fix
off, and the shared configuration refuses a build with a multiplier other
than 1 while the fix is off).

**Baseline caution (this contract, unlike the source's comment).** The module
documents its energy term as reproducing stock; its model of the replaced
helper is `max(1, ceil(...))` on both terms, with stock "rounding each
repairer's fractional contribution up, per repairer, per tick". That model
does **not** match the retail repair helper, whose instruction-exact contract
is that each positive term is clamped **to exactly one** — retail repair
delivers one health point and charges one energy unit per accepted call
whenever the computed terms round to at least one, and heals nothing when
they do not ([05 "Repair"]). Two readings settle the gap: either the source's
"vanilla" is the Escalation image's own helper (a distinct executable whose
package documents a "repair-rate exploit fix" making cheap constructors less
effective — proportional contribution, exactly the module's model), or the
module's model of the shared helper is wrong. **Supported inference** (the
former): the module is Escalation-only and the documented exploit fix matches
its model term for term. Note that this module, alone among those here,
carries no byte-signature check and names no image — it was ported from an
external memory-editing reference — so the image identity rests on its
sibling modules, which name the Escalation executable. What would settle it:
a bounded manual comparison of repair contribution between retail and
Escalation play — which would also close the repair-rate open question of
[Escalation engine package](taesc-engine.md) — or a maintainer statement; the
extension evidence policy forbids analyzing the patch binary itself.
Nanolathe work that depends on "what stock does" must use the retail
contract, not the module's comment. Source: `RepairRateFix.cpp`; measured
effect summary in `config_escalation.h` (against the host image's helper).

**CP-DMG-5 (B, Escalation deployment). Build-weapon slot guard.** Two fixes
around the stockpile ("nanolathing") build path. (1) At the end of a weapon's
TDF load, a stockpile weapon whose stored reload-tick word is zero is clamped
to 1 (zero arises from an absent, zero or negative key or an exact multiple
of 65536 ticks); the source calls this **insurance, not the fix** — an
offline scan found no degenerate stockpile weapon in live Escalation data.
(2) The verdict path classifies an order's weapon slot as ok, zero-divisor,
bad index (above 2, unsigned) or unreadable order/unit/weapon. On the
simulation path a bad index or an unreadable state is redirected to the
engine's own corrupt-order exit, while the zero-divisor case is deliberately left
bit-identical to stock because that path has no integer divide and every
generic exit changes order state. **The HUD path returns "no display" for any
non-ok verdict, and that zero-divisor guard is the actual crash fix** (the
source's corrected root cause): an unarmed slot points at the weapon table's
permanent no-weapon sentinel entry, whose reload divisor is zero by
construction and which never passes through the TDF loader, so fix (1) cannot
reach it; a unit-identity divergence makes a legitimately issued order
reference such a slot on the diverged client only. All five sites are
byte-validated all-or-nothing, installation runs a self-test and aborts if it
fails, and every non-ok verdict is logged and recorded as a breadcrumb.
Source: `BuildWeaponSlotGuard.cpp`; only the escalation profile installs it at
the pinned revision, although the author notes the reviewer's finding that
the same stock signatures appear on all seven shipped executables, which the
project has not re-run itself.

**Established — corrupt-order result and queue scope.** The selected exit
returns result 7. The source comment describes the primary pump's cancel-all
interpretation, but stockpile records run in the secondary pump, where result
7 removes that record and returns from the secondary pass [04 §3.5]. Other
primary and secondary records survive. This correction follows the installed
exit and the retail pump contract, rather than the source comment. An unarmed
Nanolathe slot's nil weapon represents the readable no-weapon sentinel, not
the patch's unreadable-pointer verdict.

### 5.5 Unit identity and divergence handling

**CP-UID-1 (A). Weapon dispatch from the local slot (all profiles).** When a
remote fire event is processed, the projectile kind is taken from the firing
unit's own weapon slot rather than the weapon ID in the packet (when the
slot's weapon pointer is non-null; a null slot weapon leaves the engine's own
choice alone); the stock code takes the packet's byte ID while the projectile
constructors take the unit's slot, and the two can disagree on a client whose
copy of the shooter diverged (the documented ballistic divide-by-zero crash:
the constructor divides by the slot weapon's velocity, which is zero on the
no-weapon sentinel). The change is a no-op for an undiverged client, because
the local firing path already reads the slot. Compile-time only, because a
fleet split over which projectile is created is exactly the divergence it
prevents; with the flag off the hook still installs and only records the
mismatch breadcrumb. The site is byte-validated and skipped with a log on
mismatch. Source: `UnitIdentity.cpp`.

**CP-UID-2 (TOOL/S). Identity instrumentation.** With
`UNIT_IDENTITY_AUDIT_ENABLE` (1 in every profile), a per-player unit block
audit runs every 150 ticks on the same sample tick on every client (the
configuration comment's "~900 ticks" and the history-depth comment's
"4 x 900" are stale; 150 was chosen to sample a transient several times),
computing a live-unit count and a 32-bit FNV-1a digest of the per-unit
(index, type) identities in array order, and sends both in a chat-envelope
packet (only the sends are staggered, at audit tick + 1 + 3 × slot; watchers
and inactive players do not send); a peer's report is compared against the
local four-deep snapshot history for the same game time (no snapshot →
ignored; the sender must match its claimed slot), the first disagreement is
logged immediately as a transient observation, and a disagreement must
persist for three consecutive audits before it alarms because clients are
not lockstep. A count disagreement with the engine's own per-player unit
count is checked locally at the same time. Morph (a network create onto an
occupied slot with a different type; the same type counts as a recreate),
ghost, weapon-mismatch and 2C-dirty breadcrumbs are recording-only and not
gated. The dirty-entry bailout (`TDRAW_2C_ENTRY_BAILOUT`) is observe-only by
default because it would hide an active fault; when on it abandons the rest
of a packet's dirty list for bad-pointer or cursor-overrun reasons only.
Source: `UnitIdentity.cpp`.
### 5.6 Construction and builder behavior

**CP-CON-1 (B). Build under own units and automatic kickout.** With
`CONSTRUCTION_KICKOUT_ENABLE` (all profiles except OTA), a build order may be
placed on ground occupied by the ordering player's own units — the
permission test is "the occupant is a unit, not a feature, and its owner is
the local human player"; there is **no mobility test**, "mobile" being the
author's release-note wording, and the earlier cheat-flag clause is
falsified: the helper computes a cheats flag but returns the own-unit term
alone, and the changelog records the cheat path as deliberately removed. The
permission applies only while the prepared order is a build, and a one-byte
enable is written into the engine's placement test so it reaches that branch
at all. The placement preview's colour selector resets to the clear-site
state at the start of every placement test and changes to the clearance
state when a square is accepted only because own units occupy it. These
selector values are not GUI palette indices; the custom snap preview maps
them to green and yellow explicitly (CP-CON-6). The engine's
"target area blocked" wait limit is patched to 20 at the mobile and VTOL
sites (**Established** by matching the two replacement operands to the retail
mobile and VTOL blocked-site branches: each compares the current visit count
against 10, waits and increments while it is at or below the limit, and
abandons only when it is greater [04 R-ORD-01 §5]). The replacement changes
that comparison limit to 20 and retains the increment and 30-tick wait.
When the builder reaches the "waiting for the
target area to clear" branch, the candidate set is the occupant of each
footprint cell — cells from `(order position ÷ 16) − (footprint ÷ 2)` per
axis, truncating, out-of-range cells skipped, results de-duplicated, the
footprint read from the definition without a rotation lookup because
CP-CON-5's swap is in force for the whole handler — and each candidate is
considered:

- *Should it move?* A unit under a MOVE order is kicked only if the order's
  destination cell lies inside the target footprint (inclusive lower bound,
  exclusive upper). A unit that has an order with a work or attack target
  whose **target** is still under build (target nanoframe strictly greater
  than zero; there is no upper bound, so a fresh target with nothing invested
  is inside this case) and has less than 600 of the target's build energy
  invested (`target buildcostenergy × (1 − target nanoframe)`, in double
  precision, compared strictly below 600 — "20 seconds before it
  evaporates") is kicked only when another live unit of the same owner —
  any unit, not only a builder — has the same target and an order state of
  at least 2; otherwise it is left standing so the target cannot evaporate.
  Everything else is kicked: no order, an order without a target, a completed
  target, or 600 or more invested.
- *Where does it go?* A random bearing is drawn **first and unconditionally
  on every destination-search call** from the C runtime generator (an integer
  0..359 divided by 57.0, a coarse degrees-to-radians factor). The caller runs
  the should-move predicate first, so a rejected occupant never enters this
  search and spends no draw; within the search the draw is unconditional. The preferred bearing is then,
  in precedence: the random one when the unit is already executing a kickout
  move; else, when the unit has a work or attack target, the bearing to that
  target rotated ±45°, choosing the sign whose unit vector has the smaller
  dot product with the vector from the unit toward the build square (the side
  away from it), then re-based: the forward intersection of that ray with the
  circle of the nominal radius about the build square, if strictly inside the
  map (> 16 and < map size on both axes), replaces it with the bearing from
  the build square to that point. The intersection evaluates the ray tangent;
  when its absolute value is at most 1 it substitutes the line equation into
  the circle and solves the resulting quadratic in X, otherwise it uses the
  reciprocal tangent and solves in Y. The discriminant is `b*b - 4*a*c`, the
  roots are tested in `(-b + sqrt(discriminant))/a/2` then
  `(-b - sqrt(discriminant))/a/2` order, and a root is accepted when its dot
  product with the ray direction is nonnegative. Else, when the unit is not exactly at the
  build square, the bearing from the square to the unit ("away"); else the
  random one. The search then runs radii from `24 ×` the target footprint's
  X extent (world units) while **strictly less than** twice that, in 16-unit
  steps, and at each radius sweeps an offset from 0 while strictly less than
  a half turn in steps of `16 ÷ radius` radians, trying preferred-minus-offset
  then preferred-plus-offset — the whole circle, nearest the preference
  first, so the bearing is a preference rather than a restriction. A
  candidate world point must be ≥ 16 and < the map size on both axes, its
  cell index > 0 and cell + 1 < the cell count on both axes, the cell's
  ground occupant slot exactly zero, its feature absent, out of definition
  range or of definition height exactly 0, and its 2x2 height spread strictly
  below the kicked unit's maximum slope. Only that one cell is tested — not
  the unit's footprint, and no reachability.
- *What happens to its orders?* A build order on a fresh or absent target
  (target nanoframe exactly 1, or none) has its state reset to zero and the
  move pushed **in front of it**, so the build resumes after the move — it is
  not cancelled; a position-less order becomes a non-queued move **and the
  queued orders behind it are dropped**; a unit already being kicked out has
  its queue tail detached, the non-queued move issued and the tail
  re-attached; otherwise the engine's order-stop entry runs on the old order,
  a substitute is issued in its place keeping position and target (repair
  when the old order was a build, otherwise "no type" with the old order's
  script-handler index so it is recreated through its original handler), the
  queued orders are re-appended, and the move is pushed on top. The order
  type is not stored anywhere: it is recovered by probing twelve candidate
  types in a fixed order (move first) against the order's handler index,
  falling back to "stop". After every branch the issued position is recorded
  in a table keyed by the unit's in-game index; "already being kicked out"
means the current order is a move whose position still equals that record,
a move-position mismatch erases the entry, while a non-move order leaves it
alone, and the table is never pruned on death, so a recycled index can match a
stale record.

**Unknown — replication.** The kickout mutates the order list through the
engine's local order-creation entry points on the owning client only, with a
direction from the C runtime generator; whether peers converge is not
determinable from this module.

The override key (the click-snap override key, default Alt) held on
left-button-down over one of the local player's units **consumes the click**
(no selection change) and, if still held on button-up over a unit that is
still the player's, sends that unit through the same order-rewriting routine
to the cursor's map position with the cursor's height — with **no
validation** of the destination (no bounds, occupancy, feature or slope test,
unlike the automatic kickout) and no drag visual; releasing the key before
button-up abandons the move. The rotation envelope of CP-CON-5 and this
module's cancellation of another unit's order from inside the same build
handler re-enter each other; the source keeps an explicit envelope stack for
that (`TDRAW_UNITROTATE_RETURN_STACK`), retaining the earlier single-slot form
only to reproduce the crash it caused. Source: `ConstructionKickout.cpp`;
policy shape in `tdraw.txt` (2025.7.12, 2025.8.1, 2025.8.19).

**CP-CON-2 (B). Guarding builders hold position.** With
`FIXED_POSN_GUARDING_CONS_ENABLE` (all except OTA), a hook at a site the
author text describes as "what construction units that are guarding factories
do after completing each unit" (the code site carries no comment, so the
trigger is author-documented) rewrites the unit's home position to the
nearest diagonal offset from the guarded unit: stay-put uses
`7 × spacing ÷ 20` (multiply first, then truncating integer division),
scatter uses the full spacing, and the default option leaves stock behavior.
**Established — spacing and write shape.** Matching the hook to the retail
ground guard's follow-maintenance leg identifies the operand as its stored
follow radius: `(guard footprint X + ward footprint X + 2) × 16` whole world
units, initialized at guard admission [04 R-ORD-01 §8]. The patch overwrites
only the whole-unit halves of the two horizontal anchor offsets, preserving
their fractional halves and the vertical offset. The quadrant comparison
reads each unit's unsigned whole-unit horizontal coordinates. The source's
Stay multiplication and division operate on the unsigned radius before the
chosen offset is signed; ordinary admitted footprints keep it positive.
The hooked maintenance leg can be reached by any ground guard, not only a
builder or a guard of a factory; the narrower completed-build description
above is author text, not an additional code gate. Each axis's sign is taken from the guarding unit's quadrant
relative to the guarded unit (a zero difference goes positive), and the
order's position fields receive that signed per-axis offset. The option is
per movement setting (bits 18–19 of the unit's selection/flags word: 0 hold,
1 maneuver, 2 roam), chosen in the ctrl-F2 menu with labels Stay / Cavedog /
Scatter, default Cavedog for all three, and any out-of-range setting reads
as Cavedog. The older executable-byte opt-in described in `tdraw.txt` is not
read at this revision; the gate is compile-time. Source: `TABugFix.cpp`,
`dialog.cpp`.

**CP-CON-3 (B). Patrolling builders' reclaim/assist filters.** With
`PATROLING_CONS_RECLAIM_OR_ASSIST_ENABLE` (all except OTA), a patrolling
construction unit's per-movement-setting option selects "reclaim only" (jump
to the storage gates preceding feature reclaim, skipping the build/repair branch) or "assist
only" (return before the reclaim search); both mobile and VTOL patrol paths
are patched. Defaults: **Hold Position is Reclaim Only**; Maneuver and Roam
are Both (stock), as `tdraw.txt` also states. Source: `TABugFix.cpp`,
`dialog.cpp`.

**CP-CON-4 (A). Reclaim toggle does not cancel a prepared build.** While the
prepared order type is BUILD and the low byte of the current toggle gadget's
status is nonzero, the keyboard accelerator skips the status toggle. Radio-group
clearing and the fired callback still run. The hook does not test the gadget
name: “reclaim-active” is the source author's label for a generic widget status
byte, not a separate reclaim flag. Pointer toggles are outside this hook.
Installed in every profile. Source: `TABugFix.cpp`; retail widget context
confirmed against [07 R-WGT-01 §3].

**CP-CON-5 (B, content-driven). Structure rotation.** The `Rotations =` FBI key
(a string extension, default `S`) lists the cardinal letters a building
allows: rotation index 0..3 maps to S, E, N, W, a rotation is allowed when
**any character** of the value equals its letter case-insensitively
(unrecognised characters ignored, no separator or order required), and
rotation 0 is allowed before the string is read. While placing, the rotate
key (default `/`, rebindable) cycles the allowed facings S→E→N→W — in game,
on key-down, only with a build type selected and a prepared build order,
blocked by Ctrl, allowed with Shift so line-building can rotate, and refused
without consuming the key when fewer than two facings are allowed or the
next allowed facing is the current one; a success plays the interface click,
prints the facing and stamps the game time. Alt+wheel (the snap-override
modifier) also cycles and is consumed whenever that modifier is held, and a
per-cardinal margin band of a rotatable structure's build button selects a
facing (S bottom, E right, N top, W left; centre clicks select normally and
keep the facing; the overlay is switchable in ctrl-F2). The cursor's facing
survives switching to a non-rotatable type and back.

**Established — build-menu overlay.** The overlay preference gates both the
art and edge selection. Every active build button whose definition is a
building with at least two allowed facings shows a marker for each allowed
cardinal. The selectable band is the 13 pixels nearest an edge; equal edge
distances resolve north, south, east, then west, and the central area retains
the prior facing. A successful edge choice flashes that marker for 200 ms.
`anims/buildrotate.gaf` is optional: its first sequence is accepted only with
exactly four nonempty frames ordered S, E, N, W, and each frame's hotspot is
placed just inside its edge midpoint. `anims/buildrotateclick.gaf` has the same
contract and replaces the idle frame during the flash when both files are
valid. Missing or invalid idle art falls back to two black-outlined yellow
chevrons per allowed edge, with the selected pair flashing white; custom idle
art without valid click art has no flash substitution. Overlays are painted
after the command panel refresh and are covered by later visible panels.
Source: `unitrotate.cpp` at pinned revision `dcff5dd`.

The chosen rotation is
captured at order-issue time: the player's build-issue entry is wrapped so
every order record allocated inside it is tagged with the current rotation,
clamped to 0 when the selected type disallows it, in a table keyed by the
record's address that is never pruned (the queued-rectangle drawing keeps
reading it) — orders from any other producer (AI, campaign, squad) are never
tagged. Construction swaps the unit definition's footprint extents and
yardmap pointer to a rotated copy **for the entire duration of the builder's
build-order handler** (ground and VTOL), because the position snap, the
area-clear test and the occupancy stamp all read those fields; rotated
yardmaps are built lazily per (type, rotation) and cached for the session,
180° keeping dimensions and 90°/270° transposing them, refused for a
footprint of 0 or above 32 on either axis, with the rotation direction
following the engine's counter-clockwise heading convention so door cells
land on the side the exiting unit uses. Five live yardmap-reading engine
entry points are wrapped so an existing rotated building's occupancy update,
footprint rebuild, occupancy clear and yard open/close checks read the
rotated copy, and the queued build rectangle swaps the definition's bounding
pairs per order for the quarter facings. Because the shared definition table
is mutated in place, an unswapped snapshot is exposed to the anti-cheat hash
threads and all swaps are restored at teardown before the engine frees the
yardmap buffers. The persistent representation is the unit's normal heading
word: the default building facing is the half-circle heading, the rotation
index is `((heading − 32768 + 8192) mod 65536) ÷ 16384`, taken mod 4 —
the nearest quarter turn, tolerant of authored build-angle jitter — and
every consumer additionally requires the definition to be a building and the
`Rotations` key to allow the derived facing, which is what separates a
player-rotated structure from jitter. That heading is carried by the existing
creation and give packets: the local creation **adds** the quarter turn to
the heading the engine already assigned at the entry of the creation
broadcast (so the wire value carries jitter plus rotation, and the
post-creation hook leaves it alone to avoid double rotation); stock ignores
it on receive, so the receive side copies the packet heading onto a created
unit when its definition is a building; a give prefers the packet's heading
whenever a packet is present and falls back to the source unit's, buildings
only; resurrection reproduces it from the wreck's stored orientation (on the
state step that creates the unit, replicating the engine's cell lookup and
the single redirect to the root cell); and save/load reads the saved heading
word at the load path's creation call so the creation-time occupancy stamp
is rotated and leaves no stale cells. No capture-specific hook exists. **Established:** retail capture completion
calls the central ownership-transfer path [05 R-WORK-01 §15], which is the
same entry wrapped by the rotation give hook. Its local replacement therefore
receives the source building's heading-derived footprint before creation. No new script port or simulated field exists;
the only extra state is the per-order rotation table (local, transient, not
saved) and the yardmap cache. A scripting `Create()` that forcibly turns the
body runs after rotation and wins (author-documented limitation; the
preview key of CP-UD-2 exists for such units). Both peers in a session need
the patched build to see the rotated facing; an unpatched peer shows south
until a script-driven orientation broadcast corrects it. Source:
`unitrotate.cpp`; author text in `tdraw.txt`.

**CP-CON-6 (command-time). Click snap.** Per-profile default and maximum
radii for mex snap and wreck snap (§3.1) are clamped to 9, the default to the
maximum; a maximum of 0 disables the feature and greys its ctrl-F2 field
(`MexSnapRadius2`, `WreckSnapRadius2`; the override key is
`ClickSnapOverrideKey`), and a radius of 0 disables it at runtime. On mouse
move, while a build or reclaim order is prepared, a snap preview is computed
and, when armed, the engine's build rectangle is suppressed and the patch
draws its own; on left-button-down with a preview armed, the click is
**consumed and re-issued at the snapped position**. The search is a square
window of ±R feature-map **cells** around the engine's own build cell; each
candidate gets a count, only positive counts survive, the maximum count wins,
ties go to the smallest squared distance from the raw cursor (in cells, with
a half-cell bias) and then to scan order (dx ascending, then dy). Mex snap:
for a metal extractor the count is the number of footprint cells whose stored
metal is strictly above the map's surface-metal threshold (window
`−foot ÷ 2 .. +foot ÷ 2`, one less on even extents), and the snap is
all-or-nothing — it fires only when the chosen spot is well centred, meaning
it equals the engine's own build position or a second snap with radius
`max(footX, footY)` does not move it; for a geothermal building (detected by
a yardmap code character the author treats as diagnostic) the count is the
engine's own placement test at the candidate, armed only when it differs from
the engine's position. Wreck snap is refused when the cursor's own cell has
an occupying unit; the count is a single-cell test — the feature definition
in range, metal or energy above zero, and the reclaimable bit — and the
ordered position is the feature's footprint centre (following a multi-cell
redirect to the root cell). A snapped build temporarily overwrites the cursor
map position with the snapped cell × 16, runs the engine's build-spot test
and map click, then restores it (Shift queues). A snapped reclaim **forces
the prepared order type to reclaim**, sets the cursor to the centre (cell × 16
plus a half cell, rounded) and the height to the mean of that cell's 2x2
min/max, invokes the map click, and then raises the newly appended reclaim
order's height to sea level when it was below (the underwater-reclaim
correction). Snapping is skipped while the megamap is up or the override key
is held, and applies only when the cursor's screen X lies inside the game
rectangle (the vertical extent is not checked). Defect: an out-of-range
wreck radius in the settings-apply path assigns the parameter rather than
the member, so the previous value silently stays. The ordered position — and
for reclaim the order type and height — differ from what the raw cursor would
have produced; nothing in order execution is changed. Source: `tahook.cpp`,
`dialog.cpp`, `config_*.h`; author text in `tdraw.txt`.

**Established — snapped preview colours.** At the pinned revision,
`tahook.cpp`'s `VisualizeMexSnapPreview` reruns the build-spot test at the
snapped position. A rejected site draws with physical palette index 214
(red). An accepted site draws with physical index 234 (green), or 240
(yellow) when the construction-kickout test admitted own-unit occupants.
These indices bypass the GUI logical-to-physical map. The patch's internal
clear/clearance selector must not itself be treated as a GUI colour field.

### 5.7 Environment, visibility and climate

**CP-ENV-1 (B, compile-time). Off-map aircraft margin.** Aircraft outside the
map can be seen, targeted and killed within a configured band (1 tile in the
ota, prota, tazero and bta profiles; 32 in escalation, mayhem and twilight —
the source, not `tdraw.txt`, which omits twilight; 0 compiles the module
out). The margin is a square band measured in tiles from the unit's whole
stamped footprint rectangle to the map rectangle, so corners behave like
edges and a partially off-map unit measures 0. Four mechanisms:

(a) *Visibility.* For a unit in the flying move tier, when it sits in the
engine's off-map bucket **or** when the engine's own visibility index would
fall outside the array — which also happens to on-map aircraft near the upper
edge, because the visibility row carries half the altitude as a shear — the
substitute answer prefers the sheared row, falls back to the unit's true row
if that is off the array, clamps column and row into range, and reads the
per-player remembered-visibility array (or, in the fog variant, the shared
word using the **viewing** player's bit). This changes who can see and aim at
such units.

(b) *Off-map second chance.* A projectile that the engine's own grid test
calls off-map, whose age is at most 450 ticks (15 s, inclusive), that is
itself inside the margin (measured from its single tile), while the live
projectile count is below 270, is kept alive without calling the engine's
collision routine **whenever any reachable enemy aircraft exists in the
bucket** — hit or not; the candidate must be alive, not pending death, in the
flying tier, owned by a player other than the firer and within the margin,
and the bucket walk is capped at 4096 entries. The module then applies the
engine's direct-hit box test (projectile tile inside the victim's footprint
rectangle and height within the model box, inclusive at both ends) and a hit
calls the engine's own projectile-damage entry. `noexplode` rounds are **not**
excluded here: they are scanned, can hit, and the module marks them spent
itself because the engine's damage call does not.

(c) *Over-the-map second chance.* For a projectile over the map the engine
gets first refusal; if it did not mark the round spent, the module scans the
bucket for an aircraft bucketed off-map yet standing on on-map tiles and
detonates on it. **This** is the path that skips `noexplode` rounds (they
never set the spent flag, so acting would damage every tick). It runs after
the engine's collision pass.

(d) *Splash.* On **every** area-of-effect blast, wherever its centre is, when
the off-map bucket is non-empty, the bucket is walked before the engine's
own tile scan: radius = the authored area value halved by an integer shift (a
result of 0 or less skips the pass, so an authored value of 1 gives no
off-map splash) — the engine's diameter-to-radius convention [06 §9.3],
[fmt tdf]; the per-axis distance to the victim's model box is clamped at zero,
summed, square-rooted in double precision, truncated to whole world units
and clamped to 32767; admission is strict (`distance < radius`); falloff is
1.0 at zero distance, otherwise `(1 − edge) × (d ÷ radius − 1)² + edge` in
single precision with the authored `edgeeffectiveness` (default 0); the
attacking unit itself is skipped and there is no friendly-fire exemption.
There is no airborne filter, so any unit parked in the bucket within the
margin can take splash — while the visibility substitute and the direct-hit
scans require the flying tier, so a non-flying off-map unit stays invisible
and un-hittable yet splashable.

Off-map units have no tile stamp, so these victims are disjoint from the
tile-based paths — a property now *enforced* rather than inherited, since the
area-damage-overflow module (CP-DMG-1) explicitly skips bucketed units to
avoid double splash. The margin is compile-time only because it decides who
can shoot what. Source: `OffMapAircraft.cpp`.

**CP-ENV-2 (B, Escalation). Aircraft wrecks fall.** With
`AIR_CORPSE_FALL_ENABLE` (escalation only), a corpse created above terrain
(stored vertical position strictly above the terrain height) with all three
velocity components zero is seeded with the smallest downward fixed-point
unit in its vertical velocity — solely to defeat the engine's "retire any
all-zero-velocity node" test; the fall profile is the engine's own gravity
integration — so it falls and lands. Water corpses (terrain height at or
below sea level), corpses already at or below ground level, and corpses whose
height query answers negative (off-map) are untouched; the all-zero test is
precisely how an already-seeded water sink is recognised. The predicate is
about where the corpse was created, not what died — the hook reads only the
freshly placed feature node's own position and velocity, consulting nothing
about the unit or its movement class (the flag's name notwithstanding).
Landing and reclaim are the engine's ordinary wreck rules, not this module's.
The tag is compile-time; that a mixed fleet sees wrecks at different places
is the inference. Source: `AirCorpseFall.cpp`; author text in `tdraw.txt`.

**CP-ENV-3 (P). Map dragon's teeth always visible.** With `VISIBLE_MAP_DTS`,
a line-of-sight hook draws map-owned dragons' teeth and fortification walls
regardless of line of sight — but the hook fires only for features whose
owner index is 11, and the three raw patches that re-home map-owned features
from owner 10 to 11 have been commented out in the installer since the day
they were written (2024-01-03); nothing else in the tree assigns owner 11.
Unless an external executable patch supplies that rewrite, the option draws
nothing extra at this revision. Presentation of map features; no simulation
effect. Source: `TABugFix.cpp`.
### 5.8 Unit-definition extensions and spawned schema units

**CP-UD-1 (B). Per-unit veterancy.** Optional FBI keys `VeterancyThresholds`
(whitespace-separated unsigned integers; a token is accepted only when the
decimal parse consumes it whole, so `7abc` and `0x10` are dropped while a
leading minus sign is accepted by the unsigned parse and wraps to a very large
value; an all-invalid or empty list falls back to the default `5 10 15 20 25`;
the parsed list is memoised by the raw string) and `VeterancyAccuracyBuffRate`
(integer; default 12; a rate <= 0 substitutes 0 for the divided term rather
than disabling the hook) customize veterancy. The bounded level is zero for
an empty list or kills below its first threshold; otherwise it is the position
returned by an upper-bound binary search for kills. At each step, compare the
middle threshold: kills below it keeps the lower half, otherwise discard the
middle and lower half. For an ascending list this is the count of thresholds
<= kills, never above the list length. The source does not sort malformed
lists, so counting all qualifying entries would change their answers.
The unbounded level, used where the engine has no cap, equals the bounded
level up to the last threshold and beyond it extrapolates
`N + (kills - last) / (last - previous)` (unsigned integer division; a single
threshold gives `kills / threshold`). Eight engine sites are hooked
unconditionally in every profile and fed these values:

- **capture cost** is replaced by `10 + level`, where the level is the
  **unbounded level of the unit being captured** (its kill count, its own
  type's thresholds) — `10 + level`, not "+10 per level" (the earlier wording
  is falsified);
- **damage taken** receives the **bounded** level, then clamped to 25, as the
  defender's tier (the clamp is inert for a list of 25 or fewer thresholds);
- **damage dealt** receives the bounded level as the attacker's tier, no
  clamp;
- **the pre-fire lead gate** takes its buff branch when
  `kills > thresholds[0]` (strict);
- **the turret spread divisor** receives `kills / rate` (integer division; 0
  when the rate is <= 0);
- **reload** receives the **bounded** level, then clamped to 16;
- the two HUD kill displays (the normal one and a developer one) print
  `Vet<n>` with the bounded level for a level above zero and otherwise take
  the engine's untouched branch with the raw kill count (presentation only).

Malformed threshold lists are the content author's problem (not validated or
sorted; an unsorted list produces answers that are not levels, and equal
final thresholds divide by zero in the extrapolation — an integer fault, not a
wrap).

**Established — absent keys reproduce retail arithmetic.** With the keys
absent the registered defaults apply, and the substituted values then equal the
retail kill-count consumers term for term ([06 R-DMG-01 §2], [05 "Capture"]):
the capture factor `kills / 5 + 10` on the target's kills with no cap, the
damage and reload tier `min(kills / 5, 5)`, the spread divisor `kills / 12`,
and the lead gate `kills > 5` — the authors' own "OTA default" statement, and
an exact identity within this source (the default thresholds make the bounded
level `min(kills / 5, 5)` and the unbounded level `kills / 5`); that the retail
consumers are those exact terms is the retail specification's claim. Two
caveats: the HUD hooks do differ from retail (`Vet<n>` replaces the
kill-count/`Veteran` display), and the accuracy hook adds a fixed `0x800` to
its accumulator in both branches. **Established — spread operand identity**
by tracing the retail code surrounding the replacement site: the accumulated
low word is weapon accuracy minus the health term; the replaced block adds
the retail spread bound's `0x800` term and computes the kill-count divisor.
The continuation divides the unsigned low-word bound only when that divisor
is greater than one, then draws the two spread samples [06 §4.4]. The patch
substitutes `kills / rate` (or zero when disabled) and retains that same
constant addition. It introduces no rounding bias, and default rate 12 leaves
this complete arithmetic and draw cadence unchanged. Source:
`VeterancyHack.cpp`, `UnitDefExtensions.cpp`; author text in `tdraw.txt`.

**CP-UD-2 (P). Placement-preview keys.** All string keys with empty defaults,
registered only when the host's nanoframe-preview setting is not DISABLED.
`PreviewPieces` and the per-facing `PreviewPiecesS/E/N/W` whitelist model
pieces shown in the nanoframe preview: the per-facing key when set and
non-empty (that list only), else the global key, else every piece **minus a
substring filter** on flare, flash, muzzle, fire, flame and wake (children
of a dropped piece are still traversed); names are case-folded and split on
whitespace, commas or semicolons. `PreviewFaceOpponent` (integer, default 0)
makes the preview snap to face the nearest enemy commander: each frame it
considers players that are active, not watchers and not allied from the local
player's view, takes **each such player's first unit slot as the commander
stand-in** (the author acknowledges the recycled-slot edge case), picks the
nearest by squared distance from the build-rectangle centre and snaps to the
nearest cardinal, falling back to the chosen facing when none is found; it
**deliberately bypasses the `Rotations` restriction** (the author's signal
that the unit's heading is script-driven). It is gated to at least one
selected, live, completed own unit with a positive authored build distance
whose distance to the rectangle centre is within it (squared, inclusive).
**Established — source boundary:** this restricts the cursor's distance from
the builder; it does not test enemy visibility, mapping or cloak. An unseen
enemy's first slot can still determine facing inside that circle. The source
comment describes mitigation of remote scanning, not complete protection
against information from fog. `PreviewObject3D` substitutes a different 3DO
model, loaded from the archive's model directory on first use and cached;
a missing file is reported and the preview falls back, and the other keys
still apply to the substitute. Presentation only: the module renders and
reads, and writes no order, position or unit state. Source: `buildghost.cpp`;
author text in `tdraw.txt`.

**CP-UD-3 (B). Map `[units]` spawns.** A multiplayer/skirmish map's `.ota`
may carry a `[units]` section under its schema spawning extra units; the
module does not parse OTA or select a schema — it consumes the engine's
already-parsed mission-unit table, so the fields are the retail mission-unit
schema ([fmt ota]). It reads `UnitName` (a linear scan of the loaded
definitions with a byte-exact comparison, first match wins; a diagnostic line
is emitted for every attempt, and no match spawns nothing), `XPos`, `ZPos`
(authored in map units, held in the record as 16.16 and passed to creation
unchanged), `Player` (1..10 = start position, 11 = neutral; values outside
1..11 never match), `InitialMission` (the retail order mini-language, run
once after the initial spawns — the engine's parser is handed the mission-unit
index and the units spawned in this pass, which is how identifier
cross-references resolve; the module does not read `Ident` itself) and
`CreationCountdown` (seconds; a key the retail placement parser reads but
never uses [fmt ota], which the patch gives a reader): the initial pass takes
every unit with countdown **<= 0**, and the deferred queue takes only those
with countdown **> 0** and a non-empty name, spawning each when the countdown
is at or before elapsed ticks ÷ 30 (truncated seconds), draining all due
entries per tick. **Deferred units never have their `InitialMission`
parsed**, although the shipped author text shows a countdown unit with one.
`HealthPercentage` is ignored, as documented. `YPos` is not merely ignored:
the patch replaces it with the terrain height of the spawn cell before
creation (retail mission placement keeps an authored Y for mobile units
[fmt ota]; this does not) — unless that cell index falls outside the map, in
which case the authored value passes through. If a player receives any such
units, they do not receive a commander unless one is listed; the flag is set
for every **attempted** spawn, so a misspelled `UnitName` for a position costs
that player both the unit and the commander. The neutral recipient is the
player whose start position plus one equals the count of active, non-watcher,
non-empty players, whose inferred controller is local AI, active and not a
watcher; in skirmish with random start positions the module swaps the last
human's and last AI's positions so the AI takes the final one (only when the
AI's is below the human's), and in the battleroom, when the map has neutral
units and no local AI, it adds one by driving the player-slot control through
two synthetic clicks, prints three advisory lines and discards that press of
START, once per session. A deferred unit with `Player` below 11 whose position
resolves to the designated neutral AI is dropped. Initial units spawn **at
their authored coordinates** through the engine's own unit creation path, at
the moment the engine would create that player's commander (both the skirmish
and multiplayer commander-creation sites are the trigger, not the position —
the earlier "commander spawn point" statement is falsified); both bail out
when the map carries no mission units or a recording is being played back.
Each client spawns only for players whose inferred controller is local, so the
per-client creation sequence differs by design and the unit-number override
is what keeps identities aligned. **Unknown — the unit-number override.** The
creation call carries a unit-number override: the engine's default (zero) for
commander-named units (an exact, case-sensitive match against any of the five
sides' commander names; the match selects only the override) and for
countdown units, and `90 + iMissionUnit` offset by the player's block base for
other initial units; the source explains only that the ghost-commander
correction (CP-FIX-3) works for commanders and the fixed offset is a
workaround for other types. What would settle it: the engine's creation-entry
contract for that argument. **Determinism hazard:** the deferred queue is
sorted by countdown with a non-stable sort, so entries sharing a countdown are
ordered by the sort's internal behavior rather than by anything authored.
Placement-record fields the module does not read (authored angle, immunity
and the inert editor keys) are not applied by this path. Battleroom
`+spawnon`/`+spawnoff` set one boolean (default on) that the multiplayer
commander hook and the battleroom START hook test; the skirmish commander
hook and the deferred-spawn tick do not, so it does not suppress skirmish
spawns (with it off in multiplayer the initial pass never runs, so the
start-position table stays empty and deferred spawns find no owner). Source:
`MultiplayerSchemaUnits.cpp`; author text in `tdraw.txt`.

### 5.9 Session and protocol contracts (Tier 3 — recorded, deferred)

**CP-SES-1 (S). Chat-envelope extension channel.** Extension messages hide in
TA's chat sub-packet: an empty chat text (first text byte zero) turns the byte
after it into a message id, and the fixed 65-byte chat packet carries the
payload. The recorder and replayer treat these as opaque chat and round-trip
them, so the mechanism is compatible with both. Registrations at the pinned
revision: challenge-response `0x2b`, vote-reject `0x2c`, a declared
replay-toggle id `0x2d` with no sender or receiver (a placeholder — the real
ten-player-replay toggle is detected by matching a fixed ten-byte prefix in
received raw packets, a different envelope entirely), `0x2f` reserved for
parked work, weapon-fired extension `0x2e`, take-claim `0x30`, identity
digest `0x31`, and allied build queue `0x60`, which compiles out in every
profile at this revision. Ordinary chat takes the same path with a non-empty
text. Dispatch in live play is gated on the local player being active, having
a non-zero network identifier and not being a spectator; during replay only
handlers that opt in run (the weapon-fired extension and the identity digest
do; challenge-response, vote-reject and take-claim deliberately do not, and
ordinary chat handlers are suppressed). **Wire rule:** the final byte of every
extension packet is deliberately held at zero so the recorder's packet sizer
does not take its long-chat fallback and over-read the next sub-packet; the
vote and challenge packets assert this. Source: `ChatHijackIds.h`,
`PacketChatRouter.cpp`.

**CP-SES-2 (S). Vote-to-reject.** Rejection of a player during a game requires
a majority vote instead of an immediate action. Eligible voters are the active
players still able to vote, excluding the target (AI and dropped players are
not counted). Two shapes, one tally routine shared by the check, the HUD line
and the dialog so they cannot disagree:

- *Manual reject:* the proposer's yes is implicit in the proposal; the reject
  passes at `ceiling(2/3 x eligible)` yes votes (integer form
  `(2e + 2) / 3`), minimum one, **and** requires at least one able ally of the
  target to have voted yes when the target has one. A no majority (more `no`
  votes than `eligible - needed`, i.e. the threshold can no longer be reached)
  cancels immediately with a 90-second cooldown
  before another vote on that target; otherwise a yes majority passes and a
  vote left open expires after 60 seconds.
- *Timeout reject* (a player silent past the engine's network-dropout
  timeout, `NetworkDropoutTimeoutSec * 30` ticks of reference time — 30 s at
  the default setting): no implicit votes at all; the pass needs two yes votes
  (one when at most one voter is eligible) plus the same ally consent. A no
  majority closes voting early and the reject then fires when the 90-second
  vote timer expires. Simultaneous dropouts are proposed one vote per
  qualifying player by replicating the engine's own silence gap test (the
  engine auto-rejects nobody in that case). A timeout vote is never opened
  against the local player — a node never receives its own packets, so the
  auto-cancel could never fire and a recovered player would self-eject.

All three timers (60 s manual expiry, 90 s timeout expiry, 90 s cooldown) are
**local wall-clock**, not game ticks — unlike the share window of CP-SES-4 —
and each client executes the reject independently when its own tally crosses;
there is no leader. The engine's own reject is invoked with mask 1 for a
battleroom reject and 6 for a timeout reject. The dialog shows subject, tally,
countdown and yes/no (at most nine concurrent votes; failure notices sit on
the HUD for 10 s), plus `.take` for allies of a timed-out player. During
replay the hooks defer entirely to the engine. Source: `VoteReject.cpp`,
`VoteDialog.cpp`; the same rules are documented in `tdraw.txt`.

**CP-SES-3 (S). Take-claim arbitration.** `.take` carries an explicit target
and uses per-target claims with a deterministic election instead of a single
global take latch. The election rule, run identically on every client, is:
**the earliest claim wins, ties broken by the lower DirectPlay identifier** —
claims are ordered by the simulation tick they carry (a claim outside a
90-tick skew band around the local tick is clamped to the local tick — it
still competes, without the advantage — so a distant-past claim cannot win),
never by local wall time, and a winner that goes silent is excluded and the
election re-run. Claim collection settles over 45 simulation ticks, the winner
must execute within 3 seconds and an election is retained for 60 seconds.
`.takecmd` includes the dropped player's commander; plain
`.take` excludes it. A take requires the recorder's preconditions (allied
permission/`.give`, a target silent for 30 wall-seconds — the recorder's
message timestamps are wall-clock, not game time); "no take in progress" is
enforced by the election state rather than the per-target verdict (a second
claimant is told the units are already claimed and an announced winner stands
down). A take is refused outright, in the Escalation share-guard profile, when the
target's commander is already destroyed under "commander dies: game ends" —
detected as health <= 0 with the alive flag still set, identified by the
engine's Commander category. Transfer completion is detected by quiescence of
the target's unit block (sampled at 5 Hz, 4 s quiet, 60 s backstop) rather
than a fixed settle, because measured transfer durations span two orders of
magnitude (the source records forty production takes: median 1.4 s, longest
21 s, longest internal gap 2.6 s). `.give`/`.stopgive` replace the alliance
requirement. Gated by `TAKE_CLAIM_ENABLE`, 1 in every profile. Source:
`TakeClaim.cpp`, `ShareGuard.cpp`, `ESCALATION_SHARE_GUARD_DESIGN.md`.

**CP-SES-4 (S, escalation). Share rate limit.** Structure shares are allowed
up to 10 per rolling 30-second window; a share that would exceed the window is
held **whole** and released together 30 seconds later, after revalidating each
entry (recycled unit slots are dropped). Mobile units share instantly. The
window and release use game ticks. Source: `ShareGuard.cpp`, design document as
above.

**CP-SES-5 (S, all profiles). Lag-switch guard.** When every remote human
player has been silent for 500 ms or more, the local simulation freezes: the
per-tick delta time is forced to zero, with one tick leaked through every
500 ms so packet dispatch and liveness timestamps keep working. While frozen the
pause key is suppressed. Single-player, demos and no-remote-human games never
freeze; the guard is inert outside the in-game state, skips detection while
the game is paused (wiping its tracking, so silence accrued before a pause is
discarded — a September 2026 investigation cleared this of suppressing a real
outage), shows a transient "resumed" line for 5 s, is local only and sends
nothing. Source: `LagSwitchGuard.cpp`.

**CP-SES-6 (S). Version, identity and anti-cheat exchange.** The session
exchanges fingerprints in the battleroom and in game: a keyed SHA-256 over a
32-byte random nonce producing **two** replies, a modules digest and a
game-data digest. Coverage: on-disk snapshots of the executable, the draw DLL,
the outermost network-library wrapper (the patch loader if present, otherwise
the recorder) and one named archive whose name is built from a format string
and a version string inside the executable; the 256-entry weapon table, the
feature table, the unit-definition table (captured on the main thread before
the hash thread starts, so a rotation swap in flight cannot corrupt it), game
and lobby state (LOS type masked, unit-limit fields, and the masked
software-debug cheat bits: cheats enabled, invulnerable features, double
shot, half shot, radar), and a map snapshot that deliberately skips the last
row and column because the engine leaves height range uninitialised there and
lava maps propagate it into feature indices, diverging between Wine and
Windows. Schedule, in game time: challenges unicast at 6 s (spectators
excluded), replies sent until 15 s, verification once per second from 15 s to
2 minutes, one public chat line at 20 s naming how many players failed.
Hashing runs on a single pre-created below-normal-priority worker thread
(memory-bandwidth bound within a nine-second budget). Battleroom commands
request reports: `.exereport`, `.tdreport`, `.tpreport`, `.gp3report`,
`.crcreport`. The INI is **not** part of this exchange (§4). A per-file
provenance log records which content file came from which archive; it is a
diagnostic, not part of the exchange. That a TA 3.1 executable is required
for the recorder's hooks belongs to the recorder document, not this module.
Source: `ChallengeResponse.cpp`, `ta-demo-recorder.md`,
`prota-engine.md`/`taesc-engine.md` for the per-package commands.

**CP-SES-7 (S). Teams and start positions.** Teams/alliances assigned in the
battleroom drive start positions. Positions are handed out per team from a
running cursor that starts at the team index and advances by the **number of
teams** each time (for two teams, the even and odd sets; for three or more,
every third position — the earlier "odd or even" statement is the two-team
special case), with a second pass taking the first free position for any
assignment that ran past the end. The assignment order for fixed positions is
an ordered player-name list supplied by an external process over shared
memory (the lobby's autobalance) when every name resolves to a present
player, otherwise plain slot order — never join order (the earlier "join
order" text came from `tdraw.txt`, which the source supersedes; a further
drift for §3.1's list); for random positions the order is shuffled. The team
count defaults to 2 and is clamped to 2..5, so `+autoteam 7` becomes 5 and
`+autoteam 1` becomes 2; all three commands are host-only. `+autoteam` uses
the external ordering when available and random otherwise; `+randomteam` is
always random; in-game `+autoteam` derives alliances from actual start
positions (two players are allied when their positions are congruent modulo
the team count), refuses with fewer than two active players, and refuses
outright if any active non-spectator has a battleroom team selection.
Alliance offers are broadcast rather than unicast so the lobby client sees
them; teams are preferred over the alliance matrix because the per-player
team scalar survives the player-array compaction when somebody leaves. On
maps carrying neutral spawn units (CP-UD-3), solitary local AIs are sorted
to the end of the order, the stride is reduced by one when there are more
than two teams, and the last position is swapped to be held by an AI. Source:
`AutoTeam.cpp`, `StartPositions.cpp`; author text in `tdraw.txt`.

**CP-SES-8 (S). Recorder command surface.** The recorder DLL owns the console
command set, the COB extension ports, recording/replay and the take walk; its
contract stays owned by [TA Demo Recorder](ta-demo-recorder.md), and the
port arithmetic by [Extended script ports](script-ports.md). At the pinned
revision the recorder distribution is version 2026.9.9, built from the source
in this repository, while the draw DLL's own version resource at this
revision reads 2026.8.6.0 and the shipped text's newest changelog block is
headed by an undated placeholder — the pinned revision is an unreleased state
a little past the 2026.8.6 draw build; the older `3.9.2.0`/`3.9.2.416` binaries are a distinct
version lineage and remain separate contracts until evidence ties them to a
source revision. The repository's own version history narrows that gap: a
build numbered `416` appears in no version constant, project version or
release note (the recorded series runs `3.9.2.2` to `3.9.2.437`), so it cannot
be placed against this source at all, and `3.9.2.0` names both an early
recorder build and the standalone replayer's file version. Conversely the
shipped recorder source is behavior-current for the 2026.9.9 distribution
DLLs: apart from one 2026 range-check fix to a shared-memory structure, no
recorder source has changed since 2016 and the repository's later recorder
commits are rebuilds.

**CP-SES-9 (local, Escalation). Player mute.** With `PLAYER_MUTE_ENABLE`
(escalation only), `.mute <player>` / `.unmute <player>`, optionally scoped
to chat, ping or draw (or `all`), suppress that player's chat lines, map
pings and whiteboard drawings on the local display; the command is cancelled
upstream so it never goes out on the wire. Display only. Source:
`PlayerMute.cpp`.

**CP-SES-10 (local, Escalation). Percentage share thresholds.** With
`SHARE_PERCENT_ENABLE` (escalation only), `+setsharemetal` and
`+setshareenergy` accept a `%` suffix; the threshold is then re-derived from
the player's maximum storage every tick, and a plain integer clears the
percentage. Local per-client state, so it cannot desync; the expanded share
dialog's sliders are percentage-aware. Source: `SharePercent.cpp`.

### 5.10 Optional resource and weather presentation

**Established — allied income.** `cinomce.cpp` displays each allied player
in its shared-data order, with the player's name and colour, current metal
and energy, storage bars and income. Metal income has one decimal place;
energy income has none. Stored values below 10000 have no decimals; values
below 100000 are divided by 1000 with one decimal and `K`; larger values
use whole thousands. A minimise control collapses the panel to its widget.
The shared-data transport is multiplayer infrastructure, not an engine
simulation rule; a host using committed economy records can present the
same information without that transport.

**Established — weather.** `HardCodeFunctions.cpp` derives the reference
solar output from negated integer `ARMSOLAR.energyuse` and the reference
wind generator maximum from integer `ARMWIN.windgenerator`. If either is
missing or zero, both fall back to 20 and 30 respectively. For a positive
wind hard limit, displayed current/minimum/maximum power is
`(generator maximum * speed + hard limit / 2) / hard limit`, truncated,
then capped above by the generator maximum. A nonpositive hard limit gives
zero. Tidal strength is truncated to an integer. `cinomce.cpp` shows current
wind plus its range, tidal output, and game time from integer tick / 30;
wind/tidal rows are build-profile choices. It anchors those rows beside the
side's energy/metal bars, falls back to production anchors and then a fixed
reference, and clamps text to the screen. Watch mode omits current wind.
The source's solar reference is computed but not drawn in this revision.

**Established — bytes-per-second overlay.** `MegamapTAStuff.cpp` preserves
the retail `+bps` overlay in the strategic view and supplies the selected
side font and normal HUD colour. It delegates all readout contents to the
retail renderer; this source provides no independent definition of those
contents. The `BPS` console toggle is retail [07 §3].

### 5.11 Order-position input

**Established — queued build and movement-order drag.** While the prepared
order is idle, Shift is held, click-snap override is not held, and the
strategic overview is absent, a left press in the horizontal game area scans
the local player's unit slots in order. Only selected units participate; each
primary order list is visited from its head. The first targetless order whose
descriptor uses the build, patrol, unload or move cursor wins when the pointer
lies in its projected footprint: X is in `[goalX − 8×footX, goalX +
8×footX)` and projected map Y is in the analogous half-open interval around
`goalY − goalHeight/2`. A build uses the definition's ordinary, unrotated
footprint here; the per-order rotation is consulted later by destination
placement. Non-build orders use a 1×1 footprint. Source:
`tahook.cpp` (`Message`, `FindUnitOrdersUnderMouse`).

Mouse movement continues only while the prepared order remains idle, Shift
remains held, the record remains linked in the same unit's order list, it
remains targetless, and the strategic overview remains absent. If the record
is the list head, its active move is first interrupted at the unit's current
position. A build retains its recorded rotation; that orientation selects its
footprint, cursor centring and placement test. A valid site resets the order's
state byte to zero and replaces all three position coordinates with the
validated cursor position. An invalid site preserves the old position and
shows the invalid build rectangle. A non-build order performs no destination
test: it resets the same state byte and copies the cursor position directly.
No path reordering or remove-and-reinsert occurs. A release clears the retained
record; a release before any movement replays the ordinary click. Source:
`tahook.cpp` (`Message`, `DragUnitOrders`, `VisualizeDraggingBuildRectangle`).

**Established — manual construction kickout gesture.** In profiles containing
CP-CON-1, this handler runs before the queued-order handler. A left press while
the configured click-snap override key is held captures the unit under the
pointer only when it belongs to the local human and consumes that press without
changing selection. On release it rechecks the same captured unit and the
override key. If both remain valid, it invokes CP-CON-1's existing order
rewrite with the current cursor's three-coordinate map position and consumes
the release; otherwise it cancels. This manual path performs no destination
search or placement validation and consumes no random number. Source:
`ConstructionKickout.cpp` (`Message`, `IsKickoutOverrideKeyPressed`,
`KickoutUnitTo`) and `iddrawsurface.cpp` (message-handler order).

### 5.12 Team-coloured nanolathe and nanoframe colours

**Established — source-read host presentation contract.** In the pinned MIT
source, `TeamColorNanolathe.cpp` owns the preference parser, stream assignment,
construction-frame remap and renderer installation; `buildghost.cpp` reuses the
frame remap for both placement-preview styles. The feature is disabled by
default and affects only rendering. It does not alter a work event, particle
lifetime, random draw or session state.

Enabling the feature installs these ten stock-palette defaults, in player-logo
colour order. Each pair is `stream / frame`:

| player colour | palette indices |
|---|---|
| 1 | `224,225,226,227,228,229` / `224,224,225,225,226,226,227,227,228,228,229,229,230,230,231,231` |
| 2 | `249,201,202,203,204,205` / `201,201,201,202,202,203,203,204,204,205,205,206,206,207,207,207` |
| 3 | `81,82,83,84,85,86,87` / `80,80,81,81,82,82,83,83,84,84,85,85,86,87,88,89` |
| 4 | `233,234,235,236,237,238` / `232,232,233,233,234,234,235,235,236,236,237,237,238,238,239,239` |
| 5 | `103,104,105,106,107,108,109` / `103,103,104,104,105,105,106,106,107,107,108,108,109,109,110,111` |
| 6 | `217,218,219,220,221,222` / `216,216,217,217,218,218,219,219,220,220,221,221,222,222,223,223` |
| 7 | `208,193,194,195,196,197` / `192,192,193,193,194,194,195,195,196,196,197,197,198,198,199,199` |
| 8 | `89,90,91,92,93,94,95` / `88,88,89,89,90,90,91,91,92,92,93,93,94,94,95,95` |
| 9 | `129,130,131,132,133,134,135` / `128,128,129,129,130,130,131,131,132,132,133,133,134,134,135,135` |
| 10 | `65,66,67,68,69,70,71` / `64,65,66,67,68,69,70,71,72,73,74,75,76,77,78,79` |

**Established — list grammar and fallback.** A list contains decimal palette
indices `0..255` separated by commas. Spaces and tabs are accepted around a
token; the decimal conversion also accepts C whitespace before a number, while
only spaces and tabs are accepted between a number and its following comma. A
semicolon ends the parsed portion. A stream list must contain 1–15
indices and a frame list exactly 16. A missing or invalid list falls back
independently to that player's corresponding built-in list. Parsing happens at
installation, so source-side preference changes take effect at the next start.

**Established — stream ownership and mapping.** An emitter burst is tagged with
the player-logo colour of its builder. The currently dispatched order unit is
the authority when it is valid for the current tick. The fallback scans live
units for the smallest three-dimensional squared distance to the emission
point, keeping the first unit on a tie. A direct-mapped 64-entry cache keyed by
the exact three source coordinates retains only the resolved colour for 15
ticks; an empty, expired or coordinate-mismatched entry performs the scan. One
tag is then shared by every particle created by that emitter burst. An
unresolved owner or a logo colour outside `0..9` keeps the stock stream. For a
resolved owner, assignment chooses
`stream[(incoming sample + per-colour sequence) mod stream-count]`, then advances
that colour's sequence once. The incoming sample is the value already supplied
by the particle constructor; the feature consumes no additional random value.
The assigned byte stays fixed while the particle travels instead of following
the stock adjacent-colour advance.

**Established — construction and preview mapping.** For a resolved player-logo
colour, each stock construction-ramp byte `0xa0..0xaf` maps directly through
the corresponding position of the player's 16-entry frame list. Other bytes,
an unresolved owner and an out-of-range colour are unchanged. The same mapping
colours the animated construction surface and outline. The placement preview
uses the local player's logo colour for its fill, edge and sweep colours in
both full and wireframe styles; disabling team colour preserves its stock
colours. Source: pinned `TeamColorNanolathe.cpp` symbols `LoadColorConfig`,
`ParseColorList`, `SetPendingForSource`, `SetPalette`, `AdvancePalette` and
`MapNanoframeColor`, plus `buildghost.cpp` `RenderGhostAtCurrentBuildSpot`.


## 6. Content interface inventory

**Established — author-facing keys at the pinned revision.** Beyond retail
keys, the patch reads these, and no others: the engine's TDF getter is
reached from exactly two places, the unit-definition extension parser and the
shared weapon-TDF parse hook, so the inventory is closed. All are optional;
absent means stock behavior. The unit-definition parser reads every
registered key for every definition on load, storing the registered default
when the key is absent, keyed by definition ID; the module named in the
*Owner* column is the consumer.

| Interface | Owner | Key(s) | Values | Scope |
|---|---|---|---|---|
| Unit FBI | `unitrotate` | `Rotations` | string of S/E/N/W letters, default S | SIM |
| Unit FBI | `VeterancyHack` | `VeterancyThresholds` | whitespace ints, default `5 10 15 20 25` | SIM |
| Unit FBI | `VeterancyHack` | `VeterancyAccuracyBuffRate` | int, default 12, <= 0 off | SIM |
| Unit FBI | `TransportedExplosions` | `TransportedExplodeAs`, `TransportedSelfDestructAs` | weapon name, default empty; registered only after the module's hook-site validation passes | SIM |
| Unit FBI | `buildghost` | `PreviewPieces`, `PreviewPiecesS/E/N/W`, `PreviewFaceOpponent`, `PreviewObject3D` | piece lists / 0|1 / 3DO name; **not registered at all** when the host's nanoframe-preview setting is DISABLED | HOST |
| Weapon TDF | `NotToAir` | `nottoair` | integer, low bit | SIM |
| Weapon TDF | `SurfaceFire` | `surfacefire`, `nottounderwater` | integer, low bit | SIM |
| Weapon TDF | `TerrainFireGate` | `notoverwater`, `notoverland` | integer, low bit | SIM |
| Weapon TDF | `ZeroDamageMapWeapons` | `nomapweaponalert` | integer, low bit | SIM + HOST |
| Weapon TDF | `ReloadBars` | `reloadbar` | integer, low bit | HOST |
| Map OTA | `MultiplayerSchemaUnits` | the mission-unit records | see CP-UD-3 | SIM |
| Unit FBI categories | `ExternQuickKey` | `CTRL_F`, `CTRL_B`, `CTRL_W` (hotkey membership); `NOTAIR` / `NAIR` appear only in the release-note description, not the pinned consumer | authored category names; when no unit carries one, a derived heuristic set applies (§4.2) | HOST |
| Animation | `unitrotate` | `anims/buildrotate.gaf` (four frames: S, E, N, W in order; implausible sizes skipped), optional `anims/buildrotateclick.gaf` | rotation overlay artwork; built-in chevrons are the fallback | HOST |
| INI | `LimitCrack` etc. | §4.1 | see §4.1 | mixed |

`ID` is a retail weapon key; the extended-ID module widens its accepted
range and redirects the slot (CP-WPN-6) but parses nothing new. The
schema-unit module does not parse OTA and does not select a schema: it
consumes the engine's already-parsed mission-unit table (name, owning
player, creation countdown), so which schema those records came from is the
engine's decision.

Retail keys the community line authors beyond the executable's own vocabulary
(`toaironly`, `noairweapon`, etc.) remain owned by
[weapon target keys](weapon-target-keys.md).

## 7. Script interface

**Established — the COB ports are recorder-provided, not tdraw-provided.**
`tdraw` adds no script opcode, port, or VM instruction; rotation is expressed
through existing state and packets (CP-CON-5). Compatibility with community
content scripts includes the authored census's ports 32 and 69–75 alongside
retail ports. The current recorder additionally exposes getter/setter groups
through port 400, including commands with side effects, documented in
[Extended script ports](script-ports.md#current-source-dispatch-contract).
Its [script integration](ta-demo-recorder.md#current-source-script-integration)
also records callback payloads, mod-id-gated consumers, optional script-slot
capacity and map scripts. Those source contracts and remaining unknowns are
owned there; an eight-port census is not the full capability surface.

**Established — the COB VM dispatch patch is Escalation-only and claimed
bit-identical.** `CobDispatchTable` replaces the interpreter's opcode
compare ladder (28 decision nodes) with a 256-entry jump table over the same
dispatch key — one opcode-space bit tested first (clear → the unknown-opcode
path), then an **8-bit opcode field** (the byte above the low twelve bits) as
the table index — of which 57 entries land on 57 distinct real handlers and
the remaining 199 on the engine's own unknown-opcode path, exactly as the
ladder did. Handler behavior is identical; it is a performance change (the
author credits the old ladder with 19–23 % of main-thread time across three
captures) and cannot be observed in outcomes. Installation verifies the
original bytes, checks every non-error entry lies inside the VM's range,
reads the written window back, and restores the original on shutdown.
Source: `CobDispatchTable.cpp`.
## 8. Compatibility classes and fleet rules

**Established — deployment inference from the source's own classes.** For
Nanolathe the classes translate to three requirements:

1. **Fleet-equivalent behaviors.** Class B contracts (CP-LIM-1/2, CP-DMG-1..5,
   CP-CON-1..3/5, CP-ENV-1/2, CP-UD-1/3, CP-WPN-1..6, CP-FIX-2/4, CP-UID-1)
   must be selected identically on all participants of a shared session.
   Nanolathe's mechanism for that is a session rule set; nothing here approves
   a specific mechanism.
2. **Safe-to-always behaviors.** Class A contracts may be enabled
   unconditionally without changing valid outcomes (the fix only triggers on
   degenerate state or is provably identical otherwise).
3. **Identity.** A profile is `(build profile, recorder version)`; content
   checks that name a profile (for example the vote/reject and mod identity
   exchange) must see both. The patch's own version exchange hashes the
   executable, the draw DLL, the network-library wrapper and one named
   archive, plus the content tables and masked cheat bits (CP-SES-6); the INI
   is not part of it — a team identity surface Nanolathe must be able to
   answer for itself when session compatibility is in scope.

## 9. Mapping to Nanolathe (candidates only)

Not approved; recorded so the implementation work has a target. Nanolathe's
existing policy machinery (central `gameplay.Mode`, `session.RuleSet` registry,
load-time content profiles, separate renderer/host controls) is the intended
seam per [DESIGN_GAMEPLAY_RULES](../../docs/DESIGN_GAMEPLAY_RULES.md).

- **Content profile per build.** A load-time profile matching one of the seven
  `tdraw` profiles gives T1 content semantics; profile differences (mex/wreck
  snap, weather rows, off-map margin, extended IDs) are content/engine
  configuration, not Modern gameplay departures.
- **Class B behaviors belong behind the session rule registry** when
  multiplayer arrives; in single-player they are simply profile behavior and
  can be always-on for that profile, since no fleet needs agreement.
- **Class A fixes are candidates for always-on**, subject to the invariant
  review; they change no valid outcomes.
- **T4 features map one-to-one onto renderer/host preferences** (megamap, chat,
  whiteboard, counters, colours, preview) and need no gameplay rules. They
  should not be bundled into a "community mode" gameplay switch.
- **The recorder ports are an extension interface**, implemented once under the
  script-port contract (see [script-ports](script-ports.md)); they are not
  profile-specific.
- **Retail-vs-community coexistence:** the existing
  [mod engine-package compatibility](mod-engine-compatibility.md) analysis
  (keyboard collisions, package identities) still applies; a community profile
  does not change retail defaults.

## 10. Unknown

Unresolved questions, indexed here with their contracts (each contract keeps
its own detail beside it).

- **Author keyboard examples (CP-CON-3/6).** The release instructions name
  Shift+Q/E to alternate mex placement and reclaim, and `v` to change movement
  stance before patrol. The inspected extension handlers establish no separate
  dispatch for these keys; whether the examples require anything beyond the
  content's ordinary gadget accelerators remains unknown. A manual observation
  with a palette whose quick keys differ would settle it. Nanolathe retains
  authored gadget dispatch pending that evidence.

- **Saturation behavior under CP-DMG-1.** Beyond six overflow units per cell
  the extra aircraft are unreachable; the source counts saturation rather than
  defining a policy. What would settle it: a Nanolathe-side decision informed
  by measurements, or an upstream change to the slot count.
- **Exact `+spawnoff` scope (CP-UD-3).** Established that the source reads the
  spawn flag in the multiplayer and battleroom paths but not the skirmish
  path; whether that asymmetry is intended is unstated. Settled by the
  maintainers' intent or a test.
- **Deferred spawns and `InitialMission` (CP-UD-3).** The source never runs
  the order script for countdown units while the shipped author text shows
  one; whether that is a defect or the text is wrong is a maintainer question.
- **The assist entry's unit-index field (CP-FIX-3).** Narrowed to a Supported
  inference (commander-only in effect) by two leads recorded in the contract;
  settled by a read of the engine's movement-packet entry parser or a
  two-client observation.
- **The repair baseline (CP-DMG-4).** The repair module models its replaced
  helper as `max(1, ceil(...))`; the retail helper clamps positive terms to
  exactly one. Supported inference: the module's model describes the
  Escalation image's helper (the package documents a repair-rate exploit fix
  with that shape), not retail. Settled by a bounded manual comparison of
  repair contribution between retail and Escalation play, or a maintainer
  statement — which would also close the repair-rate unknown of
  [Escalation engine package](taesc-engine.md).
- **Unit-number override at creation (CP-UD-3).** What the engine's creation
  entry does with the overridden unit number (the `90 + iMissionUnit`
  workaround). Settled by the engine's creation-entry contract.
- **Kickout replication (CP-CON-1).** The kick mutates the order list locally
  with a C-runtime random direction; whether peers converge is not
  determinable from the module. Settled by a two-client observation.

- **The `+lostype` cheat marking (CP-FIX-11).** The command level is raised to
  the cheat level; whether use then marks the game as cheated is engine
  behavior the source does not state.
- **`VISIBLE_MAP_DTS` (CP-ENV-3).** The hook keys on owner 11 while the
  owner-rewrite patches are commented out; whether an external patch supplies
  them is a packaging question.
- **`TenPlayerReplay` funnel economy override (CP-SES-8).** The replay-host
  module rewrites income/expense display and from-player ids for 10-player
  replays; the full contract is not yet analyzed. Settled by a dedicated read
  of that module plus the recorder's replay path.
- **Remaining Tier-3 protocol detail (CP-SES-2/3).** The vote tally and the
  claim election are now recorded; the recorder-side take walk interplay and
  the remaining quorum edge cases still need their own contract pass before
  any Tier-3 implementation.
- **Historic 3.9.02 binary behavior (CP-SES-8).** This document describes the
  current source line. The 2013 `3.9.2.0`/`3.9.2.416` binaries inspected
  earlier are the recorder's version lineage; the repository's history cannot
  place a build numbered `416` at all (see CP-SES-8). Where an older binary
  disagrees with current source, the older build remains a distinct version
  until evidence pins it.

Closed since the previous revision: the circle-radius patch (CP-FIX-7) is a
divide-by-zero guard in a presentation routine; the start-button patch
(CP-FIX-11) is identified by the author's changelog; the INI CRC (§4) is dead
code.
