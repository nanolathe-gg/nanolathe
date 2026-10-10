# Design — Mods, mutators and match identity

How a player installs, selects and switches content mods such as ProTA,
TA Zero and TA: Escalation without command-line flags; how global
**mutators** (build speed, cost, health, damage, blast size, sight and radar
multipliers)
are selected and locked for a battle; how retail's multiplayer **unit
restrictions** are offered in skirmish and Survival (§15); and how a
Nanolathe save records all of it so that loading the save restores the same
match.

**Status: implemented (2026-09-24)** apart from the follow-ups in §13 unit 9.
The mutators, the mod library with drop-to-install, the remote catalogue
client and the hosted catalogue, the in-process reload, the screens, the save
sidecar and mod switching on load are in place (§13 units 1–8), and the
displayless command mounts an installed mod with `--mod`. A load that needs a
mod which is not installed is refused with a message naming it and saying
whether *Get more mods* offers it; the load does not start the download
itself (§7.3 step 2). The maintainer's decisions of 2026-09-23 are in §2. The
proposals this document made were confirmed the same day and are listed in
§12. **Unit restrictions (§15) are implemented (2026-10-06)**, apart from
the follow-ups at the end of §15.12; their proposals, approved on
2026-10-05, are in §15.10.

This document owns the mod library, the mod config every mod ships in its
own `nanolathe-mod.json` (§4.2), the remote catalogue, the mutator transform,
the unit-restriction transform and its editor (§15) and the save sidecar. It
builds on mechanisms owned elsewhere and restates none of them: the overlay and content profiles
([DESIGN_CONTENT_VFS §5](DESIGN_CONTENT_VFS.md#5-divergences) "Content
profiles"), the gameplay rule sets and their registry
([DESIGN_GAMEPLAY_RULES](DESIGN_GAMEPLAY_RULES.md)), the Community feature
table ([DESIGN_COMMUNITY_PATCH §3](DESIGN_COMMUNITY_PATCH.md#3-the-feature-table))
and the retail save bank
([DESIGN_SESSIONS_AI_SAVE §2.5](DESIGN_SESSIONS_AI_SAVE.md#25-internalsave--the-retail-bank)).

## 1. Purpose and boundary

Four features share one idea — the **match selection** (§3), the complete
set of choices that decides what a battle is:

1. **The mod library.** Mods live in a Nanolathe data directory, one extracted
   content root per mod version. Selecting one mounts it as the last content
   root, exactly as a second `--root` does today, and applies the Nanolathe
   config the mod ships in its own `nanolathe-mod.json` (§4.2). A catalogue
   hosted on nanolathe.gg lists downloadable mods; manual installs remain
   possible.
2. **Mutators.** A closed set of global multipliers applied to the per-battle
   catalog clone at battle entry and fixed for that battle.
3. **Match identity.** A Nanolathe sidecar file beside every save records the
   match selection. Loading the save restores it, switching mod if needed,
   and the loading screen shows it.
4. **Unit restrictions** (§15). Retail's multiplayer per-unit counts — remove
   a unit from the battle, or cap how many of it each player may have —
   applied to the per-battle catalog clone of a skirmish or Survival battle
   and fixed for that battle.

**Out of scope.** Multiplayer and lobby handshakes (the match selection is
designed to be the future handshake, not implemented as one). Stacked or
layered mods (D6). Per-player mutators and handicaps (D7). Running anything a
mod ships other than authored data and COB bytecode in Nanolathe's own VM.
Rule-level variations that data cannot express; those are Community
feature-table parameters under DESIGN_COMMUNITY_PATCH and remain ignored by
Strict 3.1 (§6.1).

**Terminology.** A *mod* in this document is a downloadable content package.
It is unrelated to the repository's `mods/` directory, which lists compiled-in
gameplay rule sets ([DESIGN_GAMEPLAY_RULES §8](DESIGN_GAMEPLAY_RULES.md#8-selection-by-name-the-registry-and-mods)).
To keep the two apart, code for this feature never uses the bare identifier
`mods`; its packages are `modlibrary` and `modfetch` (§9).

## 2. Maintainer decisions (2026-09-23)

| ID | Decision |
|---|---|
| D1 | Mutators work in **every** gameplay mode, Strict 3.1 included. They are a transform of compiled content, not a gameplay rule (§6.1). Strict 3.1 with no mutators remains the retail baseline. |
| D2 | Every save gets a sidecar recording the mod, content profile, rule set, Community table, unit limit and mutators active when it was written. Loading the save restores that selection, including switching mod. The loading screen shows it. |
| D3 | Redistribution permission is assumed. Nanolathe hosts the manifest **and** every download, so availability does not depend on the original sites and the hosted zips can be cleaned (§5.5). The manifest is on nanolathe.gg; the zips are assets of one GitHub release (`mods`) on the website repository, so the site's history does not carry them (revised 2026-09-23). |
| D4 | Trust is HTTPS to nanolathe.gg. Manifest signatures are a follow-up, not a v1 requirement (§5.4). |
| D5 | The client may use the network to fetch mod and community-map catalogues and their downloads (maps authorized 2026-10-05, §5.6) and, since DESIGN_MULTIPLAYER was adopted (2026-10-01, its §15 Q1), to connect to a multiplayer relay — and for nothing else. |
| D6 | One zip per mod version, containing the complete content root. No layers. |
| D7 | Mutators are global: every player, human and computer, plays the same catalog. |
| D8 | Gameplay mode and renderer stay orthogonal to the mod. A mod may declare a minimum gameplay mode, which the selector enforces visibly; nothing switches silently. |
| D9 | Downloads are extracted to disk and mounted as ordinary directory roots. There is no zip VFS provider. |
| D10 | Applying a different mod reloads the content in process, from the menu, with no restart (§4.4). |
| D11 | The main-menu entry is a Nanolathe-owned chip in a fixed position, not a gadget positioned against authored menu art (§8.1). |
| D12 | A selected mod passes its content profile explicitly, so a saved `contentProfile` preference cannot apply one mod's directory table to another. |
| D13 | A mod may recommend a controls preset (the Community host options). Superseded 2026-09-29 by D16: a mod's recommendations are its settings layer, not an offer. |
| D14 | The Mods & Mutators screen shows the installed mods and the mutators side by side. Mods that can be downloaded appear only in a separate *Get more mods* dialog (§8.2). |

| D16 | (2026-09-29) Each mod gets its own settings by default and the player can change them: the settings a battle plays are the base settings, then the running mod's recommendations, then the player's own changes for that mod, kept per mod. Saved presets let the player apply a set of settings to any mod (§4.6). |
| D15 | (2026-09-29) Each mod's own `nanolathe-mod.json` is the single source of its Nanolathe configuration — content layout, limits, front-end art, rules, recommended settings, keys and locks (§4.2). The engine carries no per-mod data: the built-in content profiles and per-mod Community tables are removed, and the base game's defaults stay in the engine because the base game is not a mod. A mod without a config mounts as plain content with a notice; nothing detects content. A mod may choose Strict 3.1, Community 3.9 or Modern as a whole and set Community 3.9 feature values; it may not switch off an individual Modern policy. |
| D17 | (2026-10-05) Retail restrictions, every mode: retail's multiplayer unit-restriction rules are implemented once and offered in skirmish and Survival beside the mutators, in every gameplay mode, Strict 3.1 included, edited in the unit viewer; the multiplayer lobby reuses them later. Strict 3.1 with no restrictions remains the retail baseline and every fingerprint lock runs with none (§15). |
| D18 | (2026-10-05) In Survival everyone obeys the restrictions, the wave attacker included, as retail binds computer players exactly as it binds humans (§15.6). |
| D19 | (2026-10-05) Classic computer players keep retail's behaviour — a capped product stalls with the allocator's 300-tick retry — while the Modern AI does not choose a unit once it has reached that unit's cap (§15.7). |

D9–D13 were proposed in review and accepted with the rest of the design
direction. D14 is the maintainer's layout for the screen. D13's offer was
extended on 2026-09-24: a mod that starts by any other route is offered its
preset once on the main menu, and the preset became the mod's full recommended
settings (§4.3). D15 supersedes D12's content profile: a mod's config is its
own, so no preference can apply one mod's table to another. D17–D19 are the
user's decisions for unit restrictions, recorded in §15.

## 3. The match selection

| Part | Chosen by | Fixed at | Recorded in |
|---|---|---|---|
| Mod (id, version, archive SHA-256) | Mods & Mutators screen, `--mod`, settings `mod` | process start (mount) | sidecar |
| Content config | the mod's own `nanolathe-mod.json`; with no mod, `--mod-config` or the saved `contentProfile` path | mount | sidecar (`contentProfile`, the config's id), existing reports |
| Gameplay rule set (name and base) | options control, `--gameplay`, settings `gameplay` | battle entry, and the phase-1 command boundary thereafter ([DESIGN_GAMEPLAY_RULES §5](DESIGN_GAMEPLAY_RULES.md#5-switch-timing)) | sidecar records the set bound when saving |
| Community sources and entry table | the config's `rules.communityFeatures`, settings, `--gameplay-feature` ([DESIGN_COMMUNITY_PATCH §3.2](DESIGN_COMMUNITY_PATCH.md#32-sources-and-precedence)) | battle entry | sidecar |
| Unit limit | settings `unitLimit`, `--unit-limit` | battle entry | sidecar |
| Mutators | Mods & Mutators screen, `--mutator`, settings `mutators` | battle entry, for the whole battle | sidecar, catalog hash, reports |
| Unit restrictions (skirmish and Survival only) | the unit viewer's editor and the Nanolathe screen card, `--restrict`, settings `restrictions` per mod (§15.9) | battle entry, for the whole battle | sidecar, catalog hash, reports (§15.5) |

Renderer, audio, display and other host preferences are not part of the
selection and are never recorded.

## 4. The mod library

### 4.1 On disk

Mods live under the XDG data directory, following the settings file's rule of
one documented layout on every platform:

```
$XDG_DATA_HOME/nanolathe/mods/      default ~/.local/share/nanolathe/mods
  <id>/<version>/                   one extracted content root, mounted as a root
    nanolathe-mod.json              the mod's metadata and Nanolathe config (§4.2)
    install.json                    receipt: archive SHA-256, size, source URL, time
  manifest.json                     last fetched catalogue, for offline display (§5.2)
  .downloads/                       <id>-<version>.zip.<sha256>.part: partial downloads
  .staging/                         extractions and removals in progress
  .replaced/<id>/<version>/          previous complete install during replacement
```

`.staging/` holds only work that ends with its process. The first opening of
the library in each process clears it, never while an install is extracting
into it; later openings leave it alone. Before this cleanup, opening the library
recovers interrupted replacements from `.replaced/`: restore a previous copy
when the target is missing, or remove it when the target is a complete install.
An unreadable target keeps the backup and reports a recovery error. An opening
during an install never performs this recovery underneath it.

The directory is the index. An installed mod is a `<id>/<version>/` directory
whose metadata parses and whose receipt exists; there is no separate index
file to drift out of step. Installing a new version leaves older versions in
place until the player removes them, because saves name a version (§7). A
catalogue update to the same version replaces that directory only after the new
package passes validation (§5.3); saves also record the archive hash, and the
compatibility limits of replacement are described in §7.3.

### 4.2 Mod metadata and config

`nanolathe-mod.json` sits at the root of every hosted zip and every installed
mod. It is the mod's own **Nanolathe config** (D15): everything Nanolathe
needs to know about the mod that the mod's authored content does not say.
The engine carries none of it. Schema 2:

```json
{
  "schema": 2,
  "id": "prota", "name": "ProTA", "version": "4.8",
  "summary": "One line for the mod lists.", "homepage": "https://…",
  "requires": ["<logical path the base install must supply>"],
  "content": {
    "detect": ["downloadP", "gamedatP", "guiP", "unitpicsP", "weaponP"],
    "layout": {"weapons": "weaponP", "gamedata": "gamedatP", "…": "…"},
    "limits": {"units": 16000, "weapons": 16000, "tnt_bytes": 67108864, "los_bytes": 8388608},
    "presentation": {"main_menu_version": "4.8"}
  },
  "rules": {
    "minimumGameplay": "community-3.9",
    "gameplay": "community-3.9",
    "communityFeatures": {"constructionKickout": true, "…": "…", "unitLimit": 1500}
  },
  "settings": {"presentation": {"overview": 1, "…": "…"}, "switchAlt": 1, "audio": {"soundMode": 2}},
  "keys": {"profile": "community", "bindings": {}},
  "locks": []
}
```

Every object is closed: an unknown key anywhere is refused with the standard
diagnostic, so a misspelling is reported rather than ignored.

- **Identity.** `id` is lowercase `[a-z0-9-]`, stable across versions.
  `version` is the original mod version, opaque and compared for equality only;
  the manifest's order is the display order. Nanolathe packaging adds no suffix.
  An archive SHA-256 distinguishes packaging updates within that version (§5.1).
  `requires` lists logical paths that the **base** install must resolve, for
  example a map from an expansion the mod overlays.
  A mod whose requirements fail is listed but cannot be selected, and the
  screen names the missing paths.
- **`content`** is the load-time content description
  ([DESIGN_CONTENT_VFS §5](DESIGN_CONTENT_VFS.md#5-divergences) "Content
  profiles"): `layout` is the directory table, `limits` the definition-table
  sizes and read caps (an omitted count keeps the retail value), and
  `presentation` the front-end art, main-menu version text, team logos,
  range-guide defaults and the `build_menu_page_size` build page lock
  (DESIGN_INTERFACE_HUD_INPUT §3.3 "Build page lock"; every explicit player choice, including Free flow,
  overrides it; omitted player preferences inherit the recommendation). `detect` selects nothing: the install check (§5.3
  step 4) refuses a package whose content lacks a directory it names, so a
  config paired with other content is caught. Omitted, the mod is
  retail-shaped. The section is kept apart from `rules` in code
  (`profiles.Profile`), because load-time content facts are not gameplay
  selection.
- **`rules`** are gameplay declarations. `minimumGameplay` and `gameplay`
  are reserved words only — a mod chooses Strict 3.1, Community 3.9 or
  Modern as a whole — and the recommendation is never below the minimum.
  `communityFeatures` is the complete Community 3.9 table the content was
  authored for, in the `community.Features` JSON shape, decoded over the
  mainline table (an omitted field keeps its mainline value), bounded as
  every source is, and the one place a snap-radius maximum may be declared
  ([DESIGN_COMMUNITY_PATCH §3.2](DESIGN_COMMUNITY_PATCH.md#32-sources-and-precedence)
  source 2). Strict 3.1 ignores it. No field can switch off an individual
  Nanolathe Modern policy; a mod that wants the retail rules chooses Strict.
- **`settings`** is a partial settings document in the settings file's own
  JSON shape: the preferences the mod recommends. It is decoded over the
  settings defaults exactly as the settings file is read, closed. It may not
  carry the match selection or bookkeeping: `gameplay` and
  `gameplayFeatures` (they are `rules`), `unitLimit` (a table field),
  `keyBindings` (the `keys` section), `mod`, `mutators`, `restrictions`
  (§15.9), `contentProfile`, `modernAI`, `controlsOffered`,
  `modLockOverrides` and `version`.
- **`keys`** is the settings file's `keyBindings` block: a keyboard profile
  (`retail`, `community` or `zero`) and rebound actions, each a catalogued,
  rebindable action with chords the battle can deliver.
- **`locks`** are dotted paths into the settings document (`gameplay`,
  `presentation.waterSurface`) the mod asks the player not to change; each must name
  a setting.

`modlibrary.Metadata.Config` carries the parsed config: `Content`, `Rules`,
the validated `Settings` document with `ApplySettings` (an overlay onto a
settings value) and `SettingsPaths` (the leaf paths it sets), `Keys` and
`Locks`. The mount applies the content section and the Community table;
layering the settings, keys and locks is the shell's. A mod's `Controls` is
its keyboard profile when that names a preset (§4.3); the config's settings,
recommended rules and keys are the mod's settings layer (§4.6), and its
`MinimumGameplay` and
`BuildMenuPageSize` are its config's. `--mod-config <path>` reads a
stand-alone config (`modlibrary.ReadConfigFile`) for a manual root stack or a
displayless run, or in place of an installed mod's own to try a config before
it is packaged; the settings key `contentProfile` is its saved form.

**Schema 1** (`contentProfile`, `minimumGameplay`, `controls` and
`buildMenuPageSize` beside the identity) stays readable, as identity only:
the profiles it named no longer exist, so a schema 1 mod mounts as plain
content like a package without metadata (§4.5), and its recommendation
fields select nothing. **A mod without a config** — schema 1 metadata, or the
generated description of a package that had none — mounts under the base
game's profile (retail layout, retail limits, no Community source), and the
main menu shows "*name* has no Nanolathe config file; it may not load
correctly. Re-download it from Get more mods."; the Mods & Mutators screen
marks the row. There is no detection or fingerprinting fallback.

**The hosted mods' configs** are authored in the repository, one per release,
as `modconfigs/<release>/nanolathe-mod.json`: `prota-4.8`,
`ta-zero-alpha5-20241224`, `escalation-10.2.0`, `mayhem-11.3.0` and
`twilight-2.0-beta98`. They are
packaging, not engine data: nothing embeds them, and future mod authors write
their own. The first four configs' content sections and tables reproduce the
removed built-in profiles and tables, whose evidence is the packages' own archives,
configuration files and the pinned patch source
([mod engine-package compatibility](../research/extensions/mod-engine-compatibility.md),
[community patch engine behavior](../research/extensions/community-patch-engine.md) §3.1
and §4.1, [ProTA engine package](../research/extensions/prota-engine.md),
[TA Zero engine](../research/extensions/ta-zero-engine.md),
[Escalation shields](../research/extensions/escalation-shields.md#passive-generator-healing),
[Total Mayhem package](../research/extensions/total-mayhem-engine.md)). A
test proves each table resolves to the value and digest the removed table and
profile produced (`internal/modlibrary/shipped_configs_test.go`). ProTA's and
TA Zero's `settings` and `keys` are exactly the `community` and `zero`
columns of the controls preset (§4.3), which a test locks against
`controlsPresetRows`; Escalation and Mayhem name no preset and carry
neither. All four keep the Community 3.9 minimum and recommend it, and none
locks a setting. They retain the original versions: ProTA `4.8`, Escalation
`10.2.0`, TA Zero `alpha5-20241224` and Total Mayhem `11.3.0`. Packaging changes
update the archive hash, never those version strings (§5.1). Legacy installed
versions with a `+nanolathe.N` suffix remain separate, selectable versions;
nothing renames or deletes them.

Twilight's new config uses retail tree names, authored identifier and battle
limits, and the pinned source's `twilight` Community profile. It recommends
Community 3.9, names no keyboard preset and locks no settings. Its version is
`2.0-beta98`; full historical runtime equivalence remains unverified
([Twilight package](../research/extensions/twilight-engine.md)).

### 4.2.1 Upstream installations that modify the engine

**Current policy — user-authorized 2026-10-07.** Mods whose upstream
installation modifies `TotalA.exe` or DLL files must be installed through
**Get more mods** in Nanolathe. The prepared download carries the researched
`nanolathe-mod.json` needed for its content layout, limits and supported
extension behavior. Nanolathe supplies the engine; it does not execute those
Windows binaries or discover their changes from their presence.

Automatic recognition of manually installed upstream packages is deferred.
D15's explicit config policy remains in force, and the catalogue stays schema
1 at its existing URL. Folder names, renamed resource directories, archive
names and version markers select no config or gameplay features. The existing
`--mod-config` path remains a development/diagnostic tool, not the supported
installation route for these mods.

The Escalation investigation established that its original content archives
are byte-identical to the prepared download, while the upstream package lacks
our config. Without that config, retail readers miss its renamed content
families even when startup succeeds. See
[ESCALATION_SUPPORT](ESCALATION_SUPPORT.md#unconfigured-upstream-installations)
for the observed comparison and its limits. This evidence does not establish
a general detection contract or authorize a catalogue change.

### 4.3 Selection and precedence

- **Flags and settings.** `--mod <id>[@<version>]` and `--mod none` on both
  commands; the settings key `mod` (`{"id": …, "version": …}`) is the saved
  choice, written by the Mods & Mutators screen. The flag wins over the
  setting. `--shot`, `--film`, `--battle-benchmark` and `--headless` never
  read the setting, so a capture or benchmark reproduces from its command
  line; `--mod` still applies to them. The displayless command
  (`nanolathe-headless`) takes `--mod` alone, never the setting, and never
  fetches. A missing version selects the newest installed version.
- **Roots.** The base install is resolved as today (discovery,
  `$NANOLATHE_TA_ROOT` or one `--root`). The selected mod's directory is
  appended as the last root, so it wins over the base
  (`vfs.MountGameDirectories`). The remaster override still mounts above
  everything.
- **Manual stacks keep working.** Two or more `--root` flags are a manual
  stack: the `mod` setting is ignored, `--mod` is rejected with the standard
  diagnostic, the chip reads *Custom content*, and the Mods & Mutators screen
  explains why it cannot switch. [PROTA_SUPPORT](PROTA_SUPPORT.md) remains
  valid for content-only overlays and explicit development diagnostics. Mods
  that modify the upstream engine use the installation route in §4.2.1.
- **Config.** A selected mod applies its own config (§4.2); a
  `--mod-config` file named beside it stands in for it. With no mod, the
  config is `--mod-config`, else the saved `contentProfile` path, else none.
  Content without a config mounts as plain content and says so: a mod
  without one, a manual root stack without one, and a saved `contentProfile`
  naming one of the removed built-in profiles (`prota`, `zero`,
  `escalation`, `mayhem`; `retail` is simply none). Nothing detects a
  layout, and mounting never writes the preference.
- **Gameplay minimum.** While a mod with a `rules.minimumGameplay` (§4.2) is
  selected, the
  options control skips the reserved sets below it in the derivation order
  Strict 3.1 → Community 3.9 → Modern. A registered set qualifies when its base
  does (`session.BaseModeOf`). If the current selection is below the minimum
  when the player switches mod, the Mods & Mutators screen states the change
  before applying it ("Escalation requires Community 3.9 or Modern; gameplay will
  change to Community 3.9"), and applying it writes the new gameplay setting.
  A command line that names both a mod and a gameplay mode below its minimum
  is rejected. The minimum is a visible constraint on the player's selection,
  never a hidden selector ([DESIGN_GAMEPLAY_RULES §9](DESIGN_GAMEPLAY_RULES.md#9-extending-the-existing-mechanism)
  "Content profiles are a separate input").
- **Overriding a rule lock** (user-authorized 2026-09-28). On the Nanolathe
  screen ([DESIGN_INTERFACE_HUD_INPUT §3.17](DESIGN_INTERFACE_HUD_INPUT.md#317-the-nanolathe-screen))
  the layers below a mod's minimum show a padlock. Choosing one asks first:
  the mod is built for its minimum, a lower layer may change how its units
  play, and a future network game may refuse the combination. Accepting adds
  the mod's id to the settings key `modLockOverrides`; from then on the
  start-up raise and a switch to that mod leave the selection where the
  player put it, and the screen says the lock is overridden. Returning to or
  above the minimum does not clear the entry; nothing else writes it. A
  command line that names a mod and a mode below its minimum is still
  rejected: the override is a saved choice, not a flag.
- **Controls preset.** A preset is a named assignment of existing host
  options: a mod's recommended settings. Each row writes a value the player
  can already change on an options page or with a chat command; a preset adds
  no behaviour and no setting. `community` is ProTA's recommended settings:
  the Community host options
  ([DESIGN_COMMUNITY_PATCH §7](DESIGN_COMMUNITY_PATCH.md#7-host-and-presentation-features-out-of-the-profile))
  plus the preferences ProTA 4.8's `ProTA.ini` pins through its `[REG]`
  block ([community patch engine §4.1](../research/extensions/community-patch-engine.md#41-key-inventory)),
  its draw-engine megamap keys
  ([draw engine interface](../research/extensions/draw-engine-interface.md#prota-48-shipped-megamap))
  and the victory cue its renderer always plays
  ([ProTA engine](../research/extensions/prota-engine.md#victory-cue-on-multiplayer-and-skirmish-wins)).
  `retail` writes each row's retail default. `zero` offers the documented
  Alpha 5 INI preferences listed below. The rows are defined once, in
  `controlsPresetRows` (`cmd/nanolathe/controls_preset.go`):

  | Setting | Where the player changes it | `community` | `retail` |
  |---|---|---|---|
  | `presentation.communitySelection` (idle unit keys) | Options → Orders | 1 | 0 |
  | `keyBindings.profile` (one row, *Keyboard*; Zero's is `zero`) | Nanolathe screen → Controls | community | retail |
  | `presentation.doubleClickSelection` | Options → Orders | 1 | 0 |
  | `presentation.factoryHundredBatch` (Ctrl+Shift factory batch of 100) | Options → Orders | 1 | 0 |
  | `presentation.queuedOrderDrag` | Options → Placement | 1 | 0 |
  | `switchAlt` (digits recall groups) | Options → Orders, `+switchalt` | 1 | 0 |
  | `presentation.communityCounters` | Options → HUD | 1 | 0 |
  | `presentation.reloadBars` | Options → HUD | 1 | 0 |
  | `presentation.veteranLabels` | Options → HUD | 1 | 0 |
  | `presentation.groupNumbers` | Options → HUD | 1 | 1 |
  | `presentation.weatherReport` (wind and tide readout) | Options → HUD | 1 | 0 |
  | `presentation.overview` | Options → Orders (*Tab: Options* / *Tab: Megamap*) | 1 (Megamap) | 0 (Zoom) |
  | `presentation.megamapWheel`, `megamapWheelMove`, `megamapFlash` | settings file | 1 each | unchanged |
  | `presentation.megamapDoubleClickMove` | settings file | 0 | unchanged |
  | `presentation.megamapRadarMinimum`, `megamapSonarMinimum`, `megamapSonarJamMinimum`, `megamapAntiNukeMinimum` (four rows) | settings file | 0 each | unchanged |
  | `presentation.playerDotColors` (one row, *Dot colours*) | settings file | ProTA: 227, 249, 18, 250, 67, 149, 208, 117, 210, 34 | Default: 227, 212, 80, 235, 108, 219, 208, 93, 130, 67 |
  | `presentation.alliedDotSwatches` (allied resource rows' player-colour squares) | settings file | 1 | 0 |
  | `presentation.victoryCue` | Options → HUD | 1 | 0 |
  | `clock` (stand-alone battle clock) | `+clock` | 1 | 0 |
  | `audio.soundMode` | Options → Sound | 2 (3D) | 1 (Mono) |
  | `audio.mixingBuffers` | settings file | 128 | 8 |
  | `audio.cdMode` | Options → Music | 2 (Random) | 4 (Custom) |
  | `skirmish.numPlayers` (skirmish rows shown, `NumSkirmishPlayers`) | the `*III`…`*X` selector | 10 | unchanged |

  Each `retail` value is also the setting's default in a fresh settings
  file, which a test locks. Group digits are drawn by retail
  [03 R-FX-01 §6] and the option only suppresses them
  ([DESIGN_INTERFACE_HUD_INPUT §3.14](DESIGN_INTERFACE_HUD_INPUT.md#314-optional-community-unit-labels)),
  so both columns turn them on.
  3D sound is the positional placement the audio device already implements
  (DESIGN_PRESENTATION_CLIENT §2.6). 128 voices exceeds the mixer's 32 tracked
  slots, so no sound is cut off for the voice limit, which is ProTA's
  "unlimited" [03 R-AUD-01 §1]. Ten skirmish rows only shows more rows: each
  row keeps its controller, so a skirmish starts with the same players, and
  the retail preset never removes rows. The megamap rows are the optional
  overview of [DESIGN_INTERFACE_HUD_INPUT §3.15](DESIGN_INTERFACE_HUD_INPUT.md#315-optional-megamap);
  the retail preset returns the overview to Zoom and leaves the megamap's own
  preferences as the player set them. The four ring minimums have separate rows, because Zero authors different
  thresholds. The ten dot colours remain one row naming the draw engine's
  defaults, ProTA's table or Zero's table; any other table reads *Custom*. The dot colours are read by
  the megamap's icons and, with `alliedDotSwatches` on, by the allied
  resource rows' squares (the draw engine's own two readers in Nanolathe): the
  table is not read by the retail minimap's contacts, so ProTA's minimap dots
  come from its content. The eight `Megamap*Color` ring colours are not in
  the preset, because ProTA's `ProTA.ini` sets none of them.
  The victory cue is
  [DESIGN_INTERFACE_HUD_INPUT §3.16](DESIGN_INTERFACE_HUD_INPUT.md#316-optional-victory-cue).
  The unit limit is not in the preset, because the Community feature table
  already sets it (§8.3).

  **TA Zero recommendation.** TA Zero's config names the `zero` keyboard
  profile and the Community 3.9 minimum, so a library install offers its own
  settings and requires Community 3.9 or Modern. Its `communityFeatures`
  carry the `tazero` build profile's table through the existing gameplay
  composition; it does not introduce another rule set or claim historical
  Alpha 5 parity.
  `TAZero.ini` in Alpha 5 and the author's controls page supply the preferences
  ([TA Zero engine](../research/extensions/ta-zero-engine.md#documented-engine-level-behavior)).
  The preset enables double-click selection, group digits, the megamap,
  wheel zoom, wheel camera movement and under-attack flashing; disables
  megamap double-click movement; sets radar, sonar, sonar-jammer and
  anti-nuke minimums to 0, 500, 0 and 512 respectively; and uses dot colours
  `227, 212, 80, 235, 198, 219, 208, 93, 36, 67`. It chooses 3D sound,
  128 voices, random music and ten displayed skirmish rows. It offers the Zero
  selection scheme and Ctrl+Shift factory batches of 100, with the host
  boundaries in [DESIGN_INTERFACE_HUD_INPUT §3.13](DESIGN_INTERFACE_HUD_INPUT.md#313-optional-community-selection-controls).
  The Community preset enables the hundred-unit batch too; the Retail preset
  disables it. All other Zero rows are unchanged, including options whose historical
  behavior is not established. Tests
  lock the independent thresholds, palette, unchanged preferences and
  persisted colour-table identity.

  A preset is a named column of these rows. A mod's config states the
  values its column recommends in its `settings` and `keys` sections (§4.2),
  and they reach the player as the mod's settings layer (§4.6): nothing is
  offered, and switching back to the original game plays the base settings,
  so no restore is needed. The presets remain the Controls page's profiles
  on the Nanolathe screen (DESIGN_INTERFACE_HUD_INPUT §3.17), which apply a
  column to the running content's settings. The settings key
  `controlsOffered`, written by the one-time offer this replaced, is kept
  and written back unchanged.
- **A missing mod at start.** If the saved mod's directory has gone, its base
  requirements (§4.2) are unmet, or it fails to open, build or bind, the game
  starts with no mod and the main menu shows one message naming the mod and
  the reason; the saved choice is kept, and start-up never fails for this.
  The direct battle view (`--map`) falls back the same way when the saved
  mod's battle cannot be built or bound, naming the mod and the reason on
  standard error. `--check-install` checks what a start would mount: a
  broken or missing saved mod is a warning on standard error, the base
  install is validated without it, and the exit status follows the base.
  The same failures of a mod named by `--mod` are errors, in every mode;
  one whose requirements are unmet names the missing paths.

### 4.4 Applying a switch

A different mod needs a different mount, so applying it **reloads the
content in process** (revised 2026-09-23; the first version restarted the
process). The window loop, and every callback of the window adapter, holds
the running shell through one indirection. A switch is requested from the
menu, carrying the whole pending selection (mod, mutators, any gameplay raise
and the controls preset), and performed after the current step returns,
never inside one. The reload:

1. mounts the base install plus the chosen mod, with the mod's own config
   (§4.2);
2. builds a fresh shell on that content from the running shell's
   preferences, applies the pending selection to it and enforces the mod's
   gameplay minimum (§4.3);
3. rebinds the client to it: model file system and caches, palette, font,
   cursors, visual options and UI stage;
4. only then writes the settings file from the new shell and releases the
   old shell: its dialogs, its voices and music, and its archive handles.

A failure at any step before the last changes nothing: the running shell and
the settings file stay as they were, the client's bindings are restored, the
new shell's audio and content are released, and the reason is shown on the
Mods & Mutators screen (on the main menu if the screen is closed).
Process-global state tied to the mounted content must reset on a remount,
and a failed reload must put the running content's back. One case is known:
the material table, which every `client.LoadMaterialTable` rebuilds from the
embedded table before applying the content's override, so no mod's table
outlives it, and which a failed reload reinstalls from the running content.
A save whose
sidecar names another mod switches the same way and then loads the save
(§7.3).

### 4.5 Manual installs

Dropping a `.zip` or a folder onto the window (Ebitengine's dropped-files
input) installs it through the same extraction and validation path as a
download (§5.3), with the content check against the base install; the
desktop command's `--install-mod` takes the same path from the command line.
Ebitengine reports the real path of each dropped item, so a folder is
copied and an archive hashed from disk. A drop is accepted on the menus and
the Mods & Mutators screen, one item at a time; a drop during a battle or
its loading screen is ignored. The install runs off the render thread as
the process's one mod install, so it never overlaps a catalogue download
(§8.2), and its outcome is shown on the Mods & Mutators screen when it is
open, whose list then includes the mod, else as the main-menu notice. The
installed mod is not selected. A package with `nanolathe-mod.json` installs
as that mod, with the config it carries (§4.2).
Without metadata, it installs as `local-<sanitized name>` with version
`local` and no config, and the Mods & Mutators screen marks it *Local*. It
mounts as plain content — the base game's layout and limits, no rules, no
recommended settings — with the no-config notice (§4.2): a ProTA package
dropped without metadata is not recognised as ProTA, and nothing detects its
renamed trees. The same holds for a package whose metadata is schema 1.
Copying a prepared directory into the data directory by hand also works; it
needs a metadata file and a receipt, which the screen can write with *Adopt*.

Escalation Gold 10.2.0's package and tested capability boundaries are recorded
in [Escalation support](ESCALATION_SUPPORT.md). Its catalog summary exposes the
known passive-healing limitation, and its config supplies the Community 3.9
minimum without naming the unrelated ProTA controls preset.

### 4.6 Settings per mod

**Layers.** The settings a battle plays are built in three layers, each a
partial settings document in the settings file's own shape
(`internal/settings/layers.go`): the base block, the file's top level, which
the original game plays as it is; the running mod's recommendations from its
config (its `settings` document, its `rules.gameplay` as `gameplay` and its
`keys` as `keyBindings`); and the player's own changes for that mod, the
settings key `modSettings.<mod id>`. Objects merge key by key and other
values replace; `keyBindings` and `gameplayFeatures` replace whole, so a layer
can drop a binding a lower one set. Only the mod-scoped paths
(`settings.ModScoped`: rules, the presentation block, glow, digit keys,
interface type, clock, sound mode, voices, music mode, keys, skirmish rows
and, because they name one content set's units, unit restrictions, §15.9)
take part; window, volume, mod and mutator choices are the player's alone.
A save while a mod runs keeps the base block's mod-scoped values, takes the
global ones from the live settings, and stores the difference between the
live settings and base-plus-recommendations as that mod's patch. A manual
root stack and the original game have no layers.

**Locks.** A config's `locks` name settings paths the mod asks the player not
to change. Until the player overrides the mod's locks (the rule-lock override
of §4.3, one per mod in `modLockOverrides`), a locked path plays the mod's
value whatever the player's patch says, and the player's own value there is
kept in the patch for when they override. The Nanolathe screen marks a locked
setting with a padlock and asks before changing it, with the network-play
warning; overriding unlocks all of the mod's settings. Controls profiles
check every setting their existing assignment table names, including digit
keys, audio and interface settings, before changing the draft. Canceling the
override leaves the profile and draft unchanged.

**Presets.** The settings key `presets` holds the player's named sets of
mod-scoped settings. The Nanolathe screen lists them beside the original
game's settings and each installed mod's recommendations; applying one can be
limited to its rules, its graphics and effects, or its controls and
interface, and writes to the running content's layer.

**On the screen.** Each card says whether the running mod's recommendation
sets its value (*Set by ProTA*) or the player changed it for that mod
(*Changed for ProTA*, with a link back to the mod's value).
Apply preserves the live configured unit-limit word carried by a restored
save when only other settings change ([08 R-SESS-01 §9]), including when
applying a preset or overriding a lock rebuilds the settings layers. Editing
the unit-limit field still replaces that word through the normal preference
and command-line precedence; the carried word is not itself a saved preference.

## 5. The remote catalogue

### 5.1 The manifest

One JSON file at `https://nanolathe.gg/mods/manifest.json`:

```json
{
  "schema": 1,
  "mods": [
    {
      "id": "prota", "name": "ProTA", "version": "4.8",
      "summary": "…", "homepage": "…",
      "archive": {
        "url": "https://github.com/nanolathe-gg/nanolathe-gg.github.io/releases/download/mods/prota-4.8.zip",
        "size": 12443167,
        "sha256": "…"
      }
    }
  ]
}
```

An entry contains only `id`, `name`, `version`, `summary`, `homepage`, and
`archive` (URL, byte size and SHA-256). The catalogue remains schema 1;
its schema does not select the ZIP metadata format. Unknown entry fields,
including old configuration fields (`schema`, `contentProfile`, `controls`,
`minimumGameplay`, `requires`, `buildMenuPageSize`), are ignored. Configuration
comes exclusively from the ZIP's `nanolathe-mod.json` (§4.2). Install validates
that file's supported schema and complete config, then matches only `id` and
`version` to the catalogue. Display text can differ between the catalogue and
ZIP. Direct install callers may still request the stricter metadata match.

An archive URL is either on the manifest's own origin (a relative URL
resolves against the manifest) or a release asset of a `nanolathe-gg`
repository, `https://github.com/nanolathe-gg/<repository>/releases/download/<tag>/<file>`.
The website repository keeps one release, `mods`, whose assets are the hosted
zips, one per mod version, named `<id>-<version>.zip`; its README describes
packaging and upload.

**Pre-release policy (user-authorized 2026-09-29).** Testing users update the
engine to read schema 2 ZIP configs. Keep the same manifest URL and one current
package per mod; no second catalogue, parallel archive fields or migration
flags are needed. Already installed schema 1 packages remain readable. A
packaging update now follows the hash policy below.

**Original-version packaging policy (user-authorized 2026-10-01).** This
supersedes immutable archives and Nanolathe version suffixes. A packaging change
keeps the original mod version and the `<id>-<version>.zip` asset name, and
publishes the new byte size and SHA-256 in the manifest. An installed entry is
current only when id, version and case-insensitive receipt SHA-256 all agree.
A different hash, including an absent receipt hash on a manual folder install,
offers the catalogue package again. Hashes identify bytes; they are not ordered,
so a cached catalogue describes only its last fetched package. Partial downloads
are keyed by the expected hash and never resumed into a different package.

A minimum engine version is deliberately absent: the build carries no
release version today (`internal/version` names a save profile, not a
release). Add `minimumEngine` once releases are stamped.

### 5.2 When the client talks to the network

- For mods, only when the player opens the *Get more mods* dialog (§8.2) or
  starts a download. Never at start-up, never in battle, never in the
  displayless command (D5). A multiplayer relay connection is the one other
  use of the network and belongs to DESIGN_MULTIPLAYER §12; nothing of it is
  implemented yet.
- The manifest only from `https://nanolathe.gg`. An archive from the same
  origin, where a redirect to another origin is refused, or from a
  `nanolathe-gg` GitHub release asset (§5.1). GitHub answers a release asset
  with a redirect to a signed, expiring URL on its asset host, whose name it
  has changed before, so that download may follow redirects to any `https://`
  URL. The entry's size and SHA-256, which come from nanolathe.gg, are
  checked before anything is installed.
- The last good manifest is cached in the data directory and shown, with its
  age, when the fetch fails. Installed mods never need the network.
- Requests carry a `nanolathe/<profile>` user agent and nothing identifying:
  no cookies, no identifiers, no telemetry.

### 5.3 Download, verify, extract, commit

1. Download to `.downloads/<id>-<version>.zip.<sha256>.part`, using the
   lowercase manifest hash, and resume with an HTTP range request when the
   server supports it. Older unqualified part files and parts with another hash
   are not reused. The screen shows progress and can cancel. A transfer that
   stops part-way, cancelled or stalled, keeps
   the part file so a later attempt resumes it.
2. Check size and SHA-256 against the manifest. Any mismatch deletes the file
   and reports it with the standard diagnostic.
3. Extract to a new directory under `.staging/`:
   - reject absolute paths, drive letters, `..` components and symlink entries;
     accept either slash style;
   - skip `__MACOSX/` and `.DS_Store`;
   - skip executables and libraries (`.exe .dll .com .bat .cmd .scr .msi .ps1
     .sh .dylib .so`), which Nanolathe never runs, even though hosted zips
     should contain none (§5.5);
   - reject two entries whose paths are equal case-insensitively, because the
     overlay folds case and the winner would depend on the host filesystem;
   - cap total uncompressed bytes and entry count (4 GiB and
     100,000). A cap violation refuses the install.
4. Validate: parse the metadata and config and match their `id` and
   `version` against the manifest; mount the base install plus the staged root in a scratch
   `vfs.FS`, apply the config's content section (the base game's profile
   without one), require every directory its `detect` list names, and
   require the products `openContent` requires. A mod that would not start,
   or whose config describes other content, is never installed.
5. Write `install.json`, then publish the staged directory to `<id>/<version>/`.
   Manual duplicates remain refused. Only an explicitly opted-in catalogue
   install with verified id, version, hash and size may replace a complete
   install: move the previous directory to `.replaced/<id>/<version>/`, then
   rename staging to the target. Both renames stay on one filesystem. A failed
   publication restores the previous copy; if restoring also fails, keep the
   backup for recovery on the next open (§4.1). Once the target is complete,
   remove the backup and downloaded zip. Validation failures never move the old
   directory. Publication and recovery are serialized within the process.

Both screens refuse an update to the mounted id/version, with a message to
switch mods first. The download job captures that identity and the base roots
on the render thread; the install worker checks the synchronized identity
before installation and immediately before publication. Content reload refuses
to mount an in-flight download's id/version until its install finishes, even
if its dialog has closed. Workers never read a mutable shell pointer.
The starter also compares the target directory with all mounted roots, including
filesystem aliases, so a manual `--root` stack must restart on other content
before replacing a directory it mounts.

### 5.4 Trust

v1 trusts HTTPS to nanolathe.gg (D4). The manifest's SHA-256 is what a
download is trusted by, wherever the bytes come from: it catches truncated,
corrupted and substituted downloads, including from a release asset host, and
it is the archive identity a save records (§7).

**Follow-up: signatures.** An Ed25519 signature over the manifest bytes
(`manifest.json.sig`), verified against a public key compiled into the
binary, using the standard library's `crypto/ed25519`. It protects against a
compromised host serving both a manifest and a matching malicious zip.
Recommended before the catalogue is advertised widely.

**What a malicious mod can do.** Mods are data. Nanolathe runs no mod code
other than COB bytecode in its own VM, and Go's memory safety limits a
malformed file to a crash or a stall rather than code execution. The format
readers already take explicit limits. Because they now see downloaded input,
this work adds fuzz targets for the readers most exposed to it: TDF, GAF,
3DO, COB and the HPI directory (§13 unit 9).

### 5.5 The hosted zip contract

For whoever builds the hosted zips:

- The zip root **is** the content root, the directory one would pass as
  `--root`: HPI-family archives (`*.hpi`, `*.ufo`, `*.ccx`, `*.gp3` and the
  other patch-tier extensions) and loose content directories. There is no
  wrapping top-level folder.
- `nanolathe-mod.json` is at the root, and it is the mod's schema 2 config
  (§4.2): the zip carries everything Nanolathe needs to run the mod.
- No executables, DLLs, installers, `ddraw` wrappers, launcher INIs or
  base-game archives.
- A new upstream mod version is a new file. Repackaging retains that upstream
  version and replaces its hosted file, publishing its new hash and size
  (§5.1). A previous package of the same version is not guaranteed to remain
  available for a save that recorded its hash (§7.3).
- **Merging packages is not a file copy.** A mod published as several packages
  (TA Zero is Base plus Alpha 5) is merged into one root, but precedence
  differs between the two shapes. As separate roots, the later root wins. In
  one root, archives of the same tier are ordered lexically
  ([DESIGN_CONTENT_VFS §5](DESIGN_CONTENT_VFS.md#5-divergences) SC3), which
  can invert the winner. A merged zip is accepted only when a check tool
  mounts the original root list and the merged root and finds identical
  winners (`vfs.Manifest`) and an identical compiled catalog hash (§13 unit 8).

**Total Mayhem 11.3.0.** Its one upstream ZIP contains authored archives
`mayhem.gp3` and `TADemoM.ufo`, icons and changelogs alongside the retail
`TotalA.exe`, engine DLLs and Windows renderer files. The hosted package keeps
the authored archives, icons and changelogs only. Its config maps the four
renamed content trees, carries the limits documented in `mayhem.ini`, and
carries the pinned source's `mayhem` build profile as its Community table. The catalogue
marks compatibility experimental because exact equivalence to its shipped
runtime DLL and all gameplay/controls paths is not established
([Total Mayhem package](../research/extensions/total-mayhem-engine.md)).

**TA: Twilight 2.0 Beta 98.** The TAF installation extracts Base Beta 91
followed by Beta 98 into one directory, replacing `rev31.gp3` entirely.
The hosted package keeps the replacement archive, the base's three companion
archives, icons, changelogs, license and original unit guide. It preserves the
original installed layout's winning archive bytes and catalog hash; retaining
the older `rev31.gp3` as a separate root would introduce unintended fallback
content. Its authored limits and current-source Community profile live only
in its config, and compatibility remains experimental
([Twilight package](../research/extensions/twilight-engine.md)).

**Older preservation candidates.** The requested next packages are TAUCP
Normal 2.3, then original Star Wars TA Advanced Fighter Pack v1.0. TAUCP's
intact distribution currently lacks an extractable authored payload; SWTA's
primary fifth-release download is inaccessible. Neither has passed content
or battle acceptance, and neither receives an unsupported catalogue entry.
The sourced release identities, requirements and evidence needed to proceed
are recorded in [legacy packages](../research/extensions/legacy-mod-packages.md).

### 5.6 Community map catalogue

User-authorized 2026-10-05. The map picker offers **More maps** above the minimap
preview to browse and install curated community maps individually. The download
list sorts by map name alphanumerically, ignoring capitalization, for both live
and cached catalogues. Retail maps are supplied by
the player's install and do not appear in the hosted collection. This is
host content management in every gameplay mode, not a gameplay rule or a mod
selection. Installing a map does not change the selected mod, rules, settings
or mutators.

The catalogue is `https://nanolathe.gg/maps/manifest.json`; its archives are
versioned assets of the website repository's separate `maps` release. The
schema-1 document has `maps` and `dependencies` arrays. Each record uses the
mod catalogue's identity, summary, homepage and `archive` fields. A map also
names one canonical `map` path (`maps/<name>.ota`) and a `requires` array of
dependency IDs. Dependencies are direct shared content packages, not another
mod or a dependency graph. IDs are unique across both arrays; an unresolved
reference refuses the catalogue. Archive size and SHA-256 are mandatory.
Map catalogue development uses `NANOLATHE_MAP_CATALOG` under the same origin
rules as the mod override. The shared `modfetch.Client` fetches, caches and
verifies both catalogues; a separate cache directory keeps them independent.

A map may also carry `preview: {url, size, sha256}`. This optional PNG uses the
same trusted origins as map archives, is at most 1 MiB, and has positive
dimensions no larger than 1024 × 1024. Its hash identifies a separate persistent
preview cache. The catalogue fetches only the selected map's picture, without
fetching its map ZIP; changing selection or closing the dialog cancels obsolete
requests. An old response cannot replace the current picture. Verified cached
pictures work offline. Missing or failed previews leave map downloads available.
`tools/map-previews` exports authored TNT minimaps through the reference palette,
using the chooser's source crop to omit padding, and adds their identities to
the manifest. These are presentation assets, not newly generated terrain.

The installed map library is `$XDG_DATA_HOME/nanolathe/maps`, falling back
to `~/.local/share/nanolathe/maps`. It reuses the mod library's atomic
extraction, identity checks and receipts, with identity-only schema-1
`nanolathe-mod.json` files. `internal/maplibrary` restricts the payload to
maps and their feature/art support; map packages cannot install gameplay
configs, units, AI replacements or executable code. A map install requires
its named OTA and paired TNT to parse. Shared features install once before
the requested map. A package's features must resolve from the base install,
the selected mod and its own declared dependencies alone: another installed
map package never completes it, though the package may not change that
package's existing feature definitions. A package whose map path the base
install or selected mod already supplies is refused as "already in your
install", since the base copy would win the path. A failed or cancelled
transfer never exposes a partial map, and a retry uses the existing
hash-bound resume mechanism.

On ordinary desktop startup, installed map roots mount in deterministic
order below the retail roots and the active mod. Existing retail/mod assets
therefore win any overlapping logical path. Validation also refuses differing
same-name feature definitions even when they live at different logical paths,
because feature compilation resolves names as well as paths. Packages exclude
the authoritative sight-mask archive. When a downloaded map root is mounted,
the terrain-file read cap is at least 64 MiB: a bounded host admission budget
for the inspected large maps, not a change to terrain interpretation. Other
profile limits remain as selected; with no downloaded roots all limits remain
unchanged. The base install identity and
mod selection exclude these map-library roots. Displayless runs, captures
and benchmarks do not discover the library implicitly; deliberate checks
supply explicit roots. No fetch occurs on startup, in battle, or during
simulation: the player opens the catalogue or requests a download.
Profiles that redirect map or feature-support directories omit the automatic
library mount; the download dialog explains that their layout is unsupported.
Unrelated directory redirects do not disable maps. Installed maps never stop
a start: the mount-time audit ignores file-manager clutter, and a package
that fails it, or no longer validates against the current base and mod
stack, is left unmounted with a notice naming its directory
([DESIGN_CONTENT_VFS §5](DESIGN_CONTENT_VFS.md#5-divergences)).

The picker shows download progress and failures, can cancel a transfer, and
uses a validated cached catalogue when offline. After a successful install,
it refreshes content at the existing frontend publication boundary, keeping
the player's live skirmish/Survival setup and selected mod. The map becomes
selectable immediately, without restarting. Mounted package versions are
never replaced while their archive handles are live. When the catalogue
republishes an installed id and version, or a dependency it requires, with
other bytes, the row offers an update. The verified archives install on the
frontend between remounts: mount without the old package, replace it through
the library's same-version replacement, and mount the result. A failed
replacement keeps the old package, which the final mount restores. The first release does
not promise multiplayer map synchronization or automatic save dependency
recovery; those remain with their owning workflows.

Each map row in the ordinary picker and community catalogue exposes an **X**
only when its winning provider belongs to a receipt-backed map package in the downloaded map library. Retail, manually
mounted and active-mod maps do not receive a delete action. Confirmation names
the clicked row's downloaded map without selecting or loading it. Row controls
follow the visible list bounds and scroll position; eligibility is cached for
the current list and mounted content, then revalidated before removal. Removal
runs only in the frontend with no active map download: prepare a valid mount without that package, publish it and close the
old readers before deleting the package through the library. If deletion fails,
restore its availability. Refresh the selection without changing the player's
other setup values. Shared feature packages remain installed for other maps.

Release preparation preserves author credits and readmes, records exact
source and package identities, verifies feature dependencies and map loading,
and publishes only inspected payloads. Package findings and unresolved
compatibility questions belong in
[community map packages](../research/extensions/community-map-packages.md).

## 6. Mutators

### 6.1 Policy

A mutator is a deterministic transform of compiled definitions. It is applied
to the per-battle catalog clone before a session exists. The result is a
catalog of compiled content values, including generated sight data (§6.5), and
every gameplay mode already accepts content packages, so mutators are
orthogonal to the gameplay mode in the same way content profiles are (D1).

A mutator is not a gameplay rule. It adds no seam, draws no random numbers,
keeps no per-tick state and is never consulted inside a tick. The retail
arithmetic that consumes a mutated value is unchanged in every mode. **Strict
3.1 with no mutators is the retail baseline**; every fingerprint lock runs
with no mutators. A variation that data cannot express, such as a
resource-sharing or fog rule, is not a mutator. It is a Community
feature-table parameter under
[DESIGN_COMMUNITY_PATCH](DESIGN_COMMUNITY_PATCH.md), and Strict 3.1 ignores
it.

### 6.2 Shape

```go
package content

// Factor is an exact rational multiplier from the fixed step list (§6.4).
// The zero value is the identity.
type Factor struct{ Num, Den uint8 }

// Mutators is the closed set of global multipliers one battle runs under.
// The zero value changes nothing.
type Mutators struct {
	BuildSpeed Factor
	BuildCost  Factor
	Health       Factor
	Damage       Factor
	AreaOfEffect Factor // shown as "Blast size"
	Sight        Factor
	Radar        Factor
	Income       Factor
	Salvage      Factor
	FireRate     Factor // shown as "Fire rate"
	UnitSpeed    Factor // shown as "Unit speed"
}

func (m Mutators) IsZero() bool
func (m Mutators) String() string // canonical, e.g. "buildSpeed=2,health=1.5"; "" when zero
func (m Mutators) Digest() string

// ApplyMutators transforms a per-battle clone in place. Like
// RestrictToCreatable, it is never called on a shared compiled catalog.
func (c *Catalog) ApplyMutators(m Mutators) error
```

The type is closed. A new mutator is a new field, a row in §6.5 and its
tests, not a plugin.

### 6.3 Where it applies

At the one catalog clone each battle entry already makes: fresh skirmish,
mission entry (after `RestrictToCreatable`) and save restore
(`internal/session` staging). Mutators travel in the battle-entry request
beside the unit limit and the gameplay selection. Nothing can change them
during a battle. The setting has two writers, the Mods & Mutators screen and a
save load (§7.3), and both act in the front end before a battle starts.

Mutators apply to campaign missions as well as skirmish. The
campaign progress bank does not record them; each save's sidecar does.

In a skirmish or Survival battle with unit restrictions (§15), the
restrictions are applied to the clone first and the mutators to the
restricted catalog, in fresh entry and in restore alike (§15.1).

### 6.4 Arithmetic

The transform starts from each field's compiled value, which is already in
the store domain retail gives it ([02 R-KEYS-01 §5]). For a value `v`:

- `v ≤ 0` is unchanged. Zero is the absent value for most of these keys, and
  negative values are malformed inputs with their own retail arms
  ([05 R-WORK-01 §11]); a mutator must neither create nor move one.
- `v > 0` becomes `max(1, (v·Num + ⌊Den/2⌋) div Den)`, computed in 64-bit
  integers (round half up), then saturated at the field's store maximum:
  65,535 for feature `metal`/`energy`; 32,767 for the sight, radar, sonar
  and jamming distances; 32,767 for `maxdamage` and weapon damage, because
  live health and a carried hit are signed 16-bit values downstream
  ([04 §4.4], [06 §9.2]); 65,535 for weapon `areaofeffect` and
  `reloadtime` and for unit `turnrate`; 2³¹−1 for `buildtime` and for the
  16.16 `maxvelocity`, `acceleration`, `brakerate`, `moverate1` and
  `moverate2`. The maximum is the narrowest
  store the value reaches, not only its FBI or TDF store.
  Unit costs are multiplied as integers and then stored as `float32`, which is
  exact below 2²⁴.
- The floating production keys Income scales are stored by retail as single
  floats ([02 R-KEYS-01 §5], [05 R-PROD-01 §1]); the compiler keeps the
  authored double, and consumers narrow it. A positive value becomes
  `float32(float64(float32(v))·k)`: the stored single times the step, which
  a double holds exactly, rounded once to single. It saturates at the largest
  single, and NaN and `v ≤ 0` are unchanged. The one exception is Income's
  negative-`energyuse` arm (§6.5).
- Where two mutators scale one field (Build cost and Salvage on a corpse-chain
  feature), or one scales a field by k² (Unit speed), the field is scaled once
  by the exact product. Every step's numerator and denominator is at most 4,
  so a product's are at most 16; the product is never a stored factor.

Saturating rather than wrapping is deliberate. Retail wraps an out-of-range
*authored* integer when it stores it, and content that authors one keeps the
wrapped value, because the mutator starts from the stored value. A mutator
never introduces a wrap.

**Identity tag.** The mutated catalog's identity hashes the tag
`mutators/3`, the base hash and the canonical set. The tag changes whenever
any mutator's transform changes meaning (it moved to 2 when Build speed
switched to `buildtime`, and to 3 when Sight began extending raster inputs).

**The step list** (initial): 0.25, 0.5, 0.75, 1, 1.5, 2, 3, 4. Each step is
an exact rational (a whole number of quarters), and its one spelling, in the
UI, settings, flags, reports and the identity, is that decimal: most players
read "1.5" more easily than "3/2". Settings, flags and sidecars refuse any
other value or spelling. A closed list keeps the test matrix finite and every
product exact.

### 6.5 The mutators

| Mutator | Fields | Effect | Couplings (retail arithmetic unchanged) |
|---|---|---|---|
| Build speed | unit `BuildTime`, scaled by the **inverse** factor: `max(1, (v·Den + ⌊Num/2⌋) div Num)` | every construction finishes in 1/k of the visits | Construction removes `worker / buildtime` per visit and spends cost in proportion to progress, so every builder finishes in 1/k of the visits at the same total cost ([05 R-WORK-01 §1]). `workertime` is deliberately untouched: the worker quantum is `workertime / 30` in integers, so scaling it is inexact (80→160 gives ×2.5) and stops builders below 30 ([05 R-WORK-01 §1]). The resurrection delay shrinks to about 1/k ([05 R-WORK-01 §7]). The abandoned-frame decay step is independent of `buildtime` ([05 R-WORK-01 §9]). Retail repair is unaffected: both repair terms are clamped to exactly 1 per visit ([05 R-WORK-01 §3]); only the Community repair rate, where enabled, divides by `buildtime`. Unit reclaim, capture and feature reclaim do not read `buildtime` ([05 R-WORK-01 §4], [05 R-WORK-01 §5], [05 R-WORK-01 §6]). The HUD's build-time readout shows the scaled value. |
| Build cost | unit `BuildCostMetal`, `BuildCostEnergy`; the `metal` and `energy` of every feature reachable from a unit's corpse chain | everything costs k× | Construction spend scales. The repair energy term follows `buildcostenergy` ([05 R-WORK-01 §3]). The unit-reclaim pulse divides by the target's metal cost, floored at 10 ([05 R-WORK-01 §4]). The capture timer reads the target's costs ([05 R-WORK-01 §6]). The decay of an abandoned frame shrinks as energy cost grows ([05 R-WORK-01 §9]). Wreck reclaim takes longer because its duration is seeded from the scaled pools ([05 R-WORK-01 §5]). Map-authored features (trees, rocks) are not scaled; a feature definition used both on maps and in a corpse chain is scaled everywhere. |
| Health | unit `MaxDamage` | k× hit points | Retail repair and self-heal restore exactly 1 HP per accepted visit ([05 R-WORK-01 §3]), so repairing to full takes about k× as long. The unit-reclaim pulse is proportional to `maxdamage` ([05 R-WORK-01 §4]). Feature `damage` (wreck hit points) is unchanged. |
| Damage | every weapon's `DamageDefault` and `Damage` entries (the default scaled from its stored 16-bit value) | k× damage | Includes death explosions, self-destruct, burn and meteor weapons, and damage to features, whose hit points are not scaled, so features die faster. Health and Damage at the same factor roughly cancel between units, apart from truncation and the thresholds. A hit of 30,000 or more skips the armored-state reduction ([06 §9.2]); the commanders' disintegrators already do at ×1. |
| Blast size | every weapon's `AreaOfEffect` above 16, from its stored unsigned 16-bit value, never scaled below 17 | every blast reaches k× as far | A projectile that meets a unit with an area of 16 or less damages that unit alone and skips the area sweep ([06 §9.1]), and Modern's reliable direct-fire class uses the same bound, so a direct-hit weapon is left alone and a splash weapon never scales into that class: a laser gains no splash, and the smallest stock splash (30) floors at 17 at ×0.25 instead of becoming a direct hit. Death, self-destruct, burn and meteor weapons are included. The radius is the area halved and the falloff reads distance over radius, so a recipient at the same fraction of the radius takes the same share ([06 §9.3]). The broad phase visits about k² times the cells ([06 §9.3]); the stock largest area, 950, is 3,800 at ×4. The sweep remembers twenty units and processes a unit met again after that ([06 §9.3]), so a wider blast re-hits large multi-cell units sooner. Interceptors catch within k× the distance, because their catch test uses the same field ([06 R-WPN-05 §10]). The kamikaze pulse ring and the area-of-effect range ring widen with it ([04 R-SPEC-01 §1], [07 R-P0-11 §3]). Explosion art is not scaled. |
| Sight | unit `SightDistance`; for factors above 1, larger per-battle LOS tables and circular masks | k× the authored sight distance, quantized to coverage cells | The definition saturates at 32,767. Factors at or below 1 retain the authored raster tables and their existing limits. Factors above 1 extend the compiled raster inputs as described below; terrain occlusion and publication arithmetic are unchanged in every mode. The fire-at-will opportunity scan and hold-position leash ([04 R-STANCE-01 §3], [04 R-STANCE-01 §4]) and patrol and VTOL work scans ([04 R-ORD-01 §4], [04 R-ORD-01 §7]) also read the scaled distance. |
| Radar | unit `RadarDistance`, `SonarDistance`, `RadarDistanceJam`, `SonarDistanceJam` by the same factor | k× radar and sonar | All four are plain search radii, so detection and jamming stay in proportion ([03 R-VIS-01 §5]). The emitter's height bonus is not scaled ([03 R-VIS-01 §4]); `mincloakdistance` is left alone. |
| Income | unit `MetalMake`, `ExtractsMetal`, `WindGenerator`, `TidalGenerator`; the magnitude of a negative `EnergyUse`; and `EnergyMake`'s surplus over a positive `EnergyUse`: with `sm`, `se` the stored singles, `sm > se` becomes `float32(se + float64((sm − se)·k))` (the conversion keeps the product from fusing into the sum) | every unit produces k× what it makes beyond its own upkeep while that upkeep is charged | Each settlement contribution is the field times something the mutator leaves alone — the wind scalar, the tidal strength, the footprint's metal sum — so output scales by k ([05 R-ECO-01 §2], [05 R-PROD-01 §3], [05 R-PROD-01 §4], [05 R-PROD-01 §6]). A negative `energyuse` is not a malformed value here: it is the production arm the stock solar collectors author (−20), whose negation is added to production, so its magnitude scales and its sign and arm are kept ([05 R-ECO-01 §2]). 126 stock units author `energymake` equal to `energyuse` (radar and sonar towers, jammers, most mobile units); scaling `energymake` alone would make them net drains below ×1 and generators above it, so only the surplus scales: a fusion plant makes exactly k×, a self-powered radar stays neutral while its upkeep is charged, CORCOM's 25 for an upkeep of 1 becomes 1 + 24k, and ARMCOM, with no upkeep, makes 25k. Positive `energyuse`, `makesmetal`, storage and costs are unchanged: a metal maker keeps its authored conversion ([05 R-PROD-01 §5]), and more energy runs more makers. Upkeep is charged only while a building is activated or a mobile unit is activated or moving, and `energymake` is paid whenever the unit is complete ([05 R-ECO-01 §2]), so a switched-off radar or a parked mobile unit keeps its unscaled make at every factor. Storage fills sooner and production beyond it is wasted at the settlement clamp ([05 R-ECO-01 §6]). The computer players' difficulty discount multiplies each contribution and composes with Income ([05 R-ECO-01 §3]). An extractor samples its rate at creation ([05 R-PROD-01 §6]), and a save carries it. The HUD rates are settlement values and show the scaled output. The Classic AI's class vector clamps `energymake` at 30 ([08 R-P0-05 §5]), and the Modern AI's integer summaries round small scaled values. |
| Salvage | feature `Metal`, `Energy` of every definition that is not `indestructible`: wrecks, heaps, rocks and trees, whether a map, a mission or a death places them. A corpse-chain feature takes Build cost × Salvage, rounded once | reclaim pays k× | Map features resolve to catalog definitions when the terrain loads, after battle entry has applied the mutators, so one transform reaches every placement. The deposit pass writes the low byte of an indestructible definition's `metal` into the extraction grid ([05 R-FEAT-01 §7]); scaling it would wrap that byte (250 × 2 is 244), so deposits are excluded. No stock indestructible definition is reclaimable. Feature reclaim counts down `trunc(15 + (energy + metal)/2)` work at a fixed rate and pays the whole pool on the removing visit ([05 R-WORK-01 §5]), so a builder earns at about the same rate for about k× as long. Repair patrol's reclaim scan ranks by value and admits a metal feature only when it fits under storage, inclusively ([05 R-FEAT-01 §6], [04 R-ORD-01 §4]), so large pools are passed over sooner when storage is nearly full. The HUD footer shows the scaled pools ([07 R-HUD-03 §3]). The computer players' discount applies to the payout ([05 R-ECO-01 §3]). Resurrection reads no pool ([05 R-WORK-01 §7]). The largest stock pool, 40,100, saturates at 65,535 from ×2. |
| Fire rate | every weapon's `ReloadTime`, from its stored unsigned 16-bit value, scaled by the **inverse** factor as Build speed scales `buildtime` | every weapon fires k× as often | The slot fires when its countdown reaches zero ([06 §4.2]), at most once per tick per slot, so short stock reloads round unevenly at ×4 (5 ticks becomes 1). Zero stays zero and the minimum of one never creates a free round ([06 R-WPN-05 §2]). `energypershot` and `metalpershot` are billed per shot, so drain grows k-fold at the same cost per shot ([06 §4.2]). A stockpile round advances five progress per visit up to `reloadtime` and bills in proportion to progress, so nukes and anti-nukes build rounds in 1/k of the time at the same total cost, demanding k× per visit ([06 §11.1]); the stock 3,600–5,400-tick rounds (2–3 minutes) take 30–45 s at ×4. `burstrate` spaces shots within a burst on its own schedule and is untouched; a burst longer than the scaled reload overlaps the next ([06 §4.3]). The veteran reload is an integer percentage of `reloadtime`, so the bonus flattens at the smallest reloads ([06 §4.2]). `SetMaxReloadTime` hands scripts the scaled value ([06 R-WPN-05 §3]). Weapons with no reload — meteor, burn and death weapons — are unaffected. The Classic AI does not read reload; the Modern AI's damage-rate estimates scale. |
| Unit speed | unit `MaxVelocity`, `TurnRate` (from its stored unsigned 16-bit value), `MoveRate1` and `MoveRate2` by k; `Acceleration` by k²; `BrakeRate` by k² for a unit that does not fly and by k for one that does | every unit moves and turns k× as fast along paths of the same shape | The mover chooses the ground or flight integrator on the definition's `canfly` bit alone ([04 R-MOV-01 §1]). The ground mover accelerates only while the lookahead is beyond twice its turning distance `|err|·speed/turnrate` and the point two ahead is beyond its braking distance `speed²/(2·brakerate)` ([04 R-MOV-01 §4]); both are unchanged, as is the distance to reach top speed, and the time to reach it is 1/k. The flight integrator compares horizontal speed with `brakerate` itself and turns the excess toward the heading, so there `brakerate` is a speed ([04 §10.1]); the loss term `acceleration/maxvelocity` of its decay grows k-fold, and so do the approach speed `sqrt(2·a·d)` and the terminal speed. The movement-rate tiers compare speed with the two thresholds, so a unit changes tier at the same fraction of its speed ([04 R-MOV-01 §6]). No COB port sets a speed ([04 R-COB-03 §1]). Fixed distances do not scale — the waypoint capture radius, the lookahead, the air service radii — so a unit whose step outgrows the capture radius can pass a waypoint and loop back to it, and the collision validator tests only the proposed rectangle ([04 R-COLL-01 §1]); the maintainer accepts overshoot at high factors (P18). Tick cadences (repath, air order deadlines) and weapons — projectile speed, turret turn rate — are unchanged, so fast units outrun more fire. `cruisealt` and the bank and pitch gains are untouched. The Modern AI's absolute speed thresholds (fast, scout) see the scaled speeds. |

**Expanded sight data — Nanolathe mutator policy (issue #94, user-authorized
2026-10-05).** Above ×1, enlarge the per-battle compiled inputs in every mode,
including Strict 3.1. Retail itself keeps its authored caps: with stock data,
True sight stops at 8 cells and Circular at 14 ([03 R-COMP-02 §1], SC9).
The former transform only scaled the definition, so most stock units gained
little or no visible range. The new transform supports the scaled authored
radius, not a multiple of the already capped visible footprint.

After scaling definitions, collect their positive signed-word radii divided by
32. Preserve all originally reachable tables and frames byte for byte. For each
needed radius above the original cap, generate an inclusive integer disc
(`x² + y² ≤ r²`). A circular frame has side `2r+1` and anchor `(r,r)`.
A terrain table has `2r` first-quadrant rays: aim at `(r,0)` through `(r,r)`,
then `(r−1,r)` through `(1,r)`. At dominant-axis distance `d=1..r`, each
coordinate is `(endpoint*d + floor(r/2)) div r`; stop when the point leaves
the disc. The consumer supplies its existing four rotations and its existing
strict terrain-horizon comparisons. This is generated Nanolathe content,
not a claim about retail's authored geometry; long compiled lines are not
subject to the retail text loader's 511-byte value limit.

Extend the declared terrain count to `largest needed radius + 1`, because
group `g` reads slot `g−1` and the clamp stops at count minus one. Keep unused
new slots at the old clamped footprint, and retain originally unreachable
terrain sections after the new declared list. Circular slots likewise retain
the old clamped mask unless a definition needs that larger radius. Generate
only distinct needed radii: allocation is quadratic per generated radius,
rather than for every intermediate radius up to the largest mod value.
Missing raster resources remain missing; an empty terrain table range remains
empty. Unit publication and temporary death sight both read the transformed
definition, so entry, movement, death, mode changes and save reconstruction
share the same data. No mutator is consulted during a tick, no RNG is drawn,
and no new gameplay seam or per-tick state is added.

The existing wrapping byte refcounts are retained, including repeated visits
where terrain rays overlap. Expanded sight increases both raster work and
coverage overlap; extreme mod radii or crowded observers can still wrap these
counts. Tests exercise hole-free current coverage at the stock ×4 radii,
terrain obstruction, balanced removal, original-slot preservation and clone
isolation. No-mutator battles retain the original content and fingerprints.

**Known hazards.** The unit-reclaim pulse forms a 32-bit product of
`workertime`, a kill factor, `maxdamage` and 15, which retail lets overflow
([05 R-WORK-01 §4]). Build speed no longer reaches it; Health at ×4 multiplies
it by up to 4, but the 32,767 cap limits that: ARMCOM reclaiming CORKROG
overflows from 70 kills instead of 75. The abandoned-frame decay forms the 32-bit product `11·buildtime`
([05 R-WORK-01 §9]); at Build speed ×0.25 the largest stock `buildtime` gives
164,673,432, well inside the range. An interceptor's catch test squares its
unhalved area as a signed 32-bit product, which wraps from 46,341
([06 R-WPN-05 §10]); the stock interceptors author 96, 384 at ×4. Unit speed ×k multiplies the flight
integrator's decay loss `acceleration/maxvelocity` by k ([04 §10.1]); the loss
would reach the whole speed at a ratio of 1/k, and the stock largest ratio,
0.042, keeps it below 0.17 at ×4. Salvage skips indestructible definitions
because the deposit pass keeps only the low byte of their metal
([05 R-FEAT-01 §7]), but Build cost, older, still scales an indestructible
corpse-chain definition; no stock definition is both, so only mod content can
reach that wrap. Mutators do not guard downstream
products, because the retail arithmetic stays retail.

**Derived values.** `ApplyMutators` must recompute any compile-time value
derived from a field it changes. A search of the unit compiler found these
fields assigned and not otherwise derived from. The implementing unit confirms
the same for AI profiles, build menus and the weapon linker. Sight additionally
extends its compiled raster inputs above ×1 under the policy above; both
rasters continue selecting those inputs from the live definition ([03 §3.2]).

### 6.6 Identity and reports

- The clone's `Catalog.Hash` becomes a hash of `mutators/3`, the base hash and
  `Mutators.String()`. With no mutators, nothing is transformed and nothing is
  rehashed, so every existing identity and fingerprint is unchanged. When the
  battle also has unit restrictions, the base hash is the restricted one
  (§15.4).
- Per-definition `Hash` fields stay identities of authored records. The
  implementing unit checks every consumer that compares them (save restore,
  presentation caches) and routes any consumer that must see mutated values to
  the catalog identity instead.
- Headless and simulation-cost reports, battle-benchmark scene metadata and
  debug captures gain `mod` and `mutators` fields beside `rules` and
  `content_profile`. The headless report of both commands and the battle
  benchmark's metadata spell the mod `<id>@<version>`, or `none`.
- `--mutator <name>=<factor>` (repeatable) on both commands selects them. In
  the desktop window the settings key `mutators` selects them when no flag
  is given. The desktop command's `--shot`, `--film`, `--battle-benchmark`
  and `--headless`, and the whole displayless command, never read the key,
  so fingerprint, capture and benchmark runs reproduce from their command
  line.

## 7. The save sidecar

The sidecar is `save.Sidecar` (read and written by `internal/save`); the
session builds its half (`session.SaveSidecar`) and the desktop command adds
the mod, the content profile and the configured unit limit and applies a
loaded one (`cmd/nanolathe/save_sidecar.go`). Saving a battle that runs with
mutators was refused until the sidecar existed; it no longer is.

### 7.1 Placement and lifecycle

A save `SAVEGAME/<name>.SAV` gets `SAVEGAME/<name>.SAV.nanolathe.json`. The
retail enumerator lists only `*.SAV`, so the sidecar never appears as a save
and the bank bytes are unchanged; a retail executable can still read the bank.

The sidecar is written, through a temporary file and a rename, immediately
after the bank is written successfully. Campaign continuation saves get one
too. Overwriting a save removes its old sidecar before the new bank is
written, so an interrupted overwrite never pairs the new bank with the old
selection; a sidecar that then cannot be written removes the new bank as
well and reports the failure, because the bank alone would load as if no
mutators had been active. Deleting a save through the dialog deletes its
sidecar. A sidecar whose bank is missing is ignored.

### 7.2 Contents

```json
{
  "schema": 1,
  "profile": "nanolathe-1.0",
  "mod": {"id": "prota", "version": "4.8", "sha256": "…"},
  "contentProfile": "prota",
  "contentManifest": "<vfs manifest hash of the mounted set>",
  "catalog": "<catalog hash after mutators>",
  "rules": "community-3.9",
  "gameplay": "community-3.9",
  "community": {"sources": {…}, "entry": {…}},
  "unitLimit": 1500,
  "mutators": {"buildSpeed": "2", "health": "1.5"},
  "ai": {"seed": 3427855529, "generators": [{"player": 1, "position": 1311768467463790320}],
         "overrides": [{"player": 1, "params": "jitter=0,style=eco"}]}
}
```

`mod` is `null` when no mod was selected, and the string `custom` for a manual
root stack. `unitLimit` is the configured unit-limit word, the one that sizes
a battle. `catalog` and `contentManifest` are the battle catalog's identity
after mutators and the mounted set's manifest hash. A sidecar with another
`schema`, or one that does not parse, refuses the load rather than being read
as absent, which would silently drop the selection it records. A battle with
unit restrictions adds `"restrictions": {"armkrog": 0, "armpw": 20}` and is
written as schema 2, so that a build reading only schema 1 refuses it rather
than restoring the bank without them; every other sidecar stays schema 1
(§15.5). `contentProfile`
is the mounted content's report name: the running config's id, or `retail`
for content without one. `community` records the session's Community sources and its
battle-entry table (`Session.CommunitySources`, `Session.EntryCommunity`), so
a restored battle resolves and switches exactly as the saved one did, whatever
the host's current settings are. A mod config's table is recorded as a
content source whose `base` is the complete value, so a sidecar needs no
engine-side table name. A sidecar written before 2026-09-29 names the
removed per-mod tables (`{"table": "escalation"}`, `tazero`, `mayhem`) as its
content source; this build no longer resolves those names, so such a source
is replaced on load by one whose `base` is the sidecar's recorded `entry`
table, the complete value the battle ran under, and the battle restores
exactly as saved (`main.TestSidecarRemovedTableUsesEntry`). `ai`, present when the battle had a computer
player, is the Modern AI controllers' record (the battle seed, each
controller's generator position, each computer player's configured brain
parameters, `overrides`, and the computer players marked Modern, `modern`;
DESIGN_SESSIONS_AI_SAVE "Modern AI computer player"); the save package keeps
it as raw JSON. Like the mutators, the
recorded parameters replace the host's configuration on a load, and a
record naming a parameter this build does not know refuses the load.

### 7.3 Loading

1. **No sidecar**, as with retail saves and older Nanolathe saves: behave
   exactly as today. Use the current selection, the existing unit-limit rules
   and no mutators, and infer nothing from the loaded content
   ([DESIGN_GAMEPLAY_RULES §6](DESIGN_GAMEPLAY_RULES.md#6-save-interaction)).
2. **Mod.** If the recorded mod differs from the running one and that
   version is installed, switch to it (§4.4) carrying the recorded mutators
   and rule set, then load the save on the new content. A load made from
   inside a battle leaves that battle before the switch, as any load
   replaces it. The direct battle view (`--map`) has no menu shell to
   reload, so it refuses and says to start without `--map`. A mod whose
   base requirements (§4.2) are unmet is refused, naming the missing path.
   A version that is not installed is refused with a message naming it;
   when the cached catalogue (§5.2, read offline) offers it, the message
   says to download it from *Get more mods* and load again. The load does
   not start the download itself. The load makes the recorded mod the
   selected mod (P6). A `custom` sidecar loads only in a manual stack, and a
   manual stack loads only a `custom` sidecar.
3. **Rule set.** Bind the recorded name. If this build cannot select it (a
   registered set that is not linked), load under the recorded base and show a
   warning on the battle message line. The load also makes the bound set the
   host's gameplay selection, as it does for the mutators, so the options
   control shows the set the restored battle runs and a restart or the next
   battle agrees with it.
4. **Community.** Stage with the recorded sources and entry table in place of
   the host's. Reconstruct entry transforms, including the stockpile reload
   clamp, from that table rather than the current rule selection; a rule
   switch made before saving did not undo the original entry transform.
5. **Unit limit.** Set the configured limit to the recorded one before
   staging, in every mode. Strict 3.1 still sizes its pool from the pre-load
   configured word ([08 R-SESS-01 §9]); the host has only chosen that word
   first. Retail's post-load carry of `Summary.maxunits` into the configured
   word is kept.
6. **Mutators.** Apply the recorded mutators to the restore clone and make
   them the selected mutators (P6). The settings, the chip, a restart of the
   battle and the next new battle then all match the game that was loaded.
   A skirmish save's recorded unit restrictions are checked and applied to
   the clone before the mutators, before the terrain, the pool and every
   unit, and become the running content's restriction setting (§15.5).
7. **Integrity.** Compare the recorded `catalog` hash with the restored
   catalog's. A mismatch (a different base install, different mod bytes) loads
   with a warning on the battle message line rather than refusing.

**After a package update.** The sidecar keeps the archive SHA-256 from the
install receipt; it is provenance, not a separate installed-version selector.
Loading selects by id/version and uses the package currently installed there.
The catalogue offers its current package for that version and cannot promise
the exact bytes a save recorded. The existing catalog-hash check above warns
when compiled content differs; a changed archive hash alone neither proves
incompatibility nor refuses the load. Packaging-only changes can leave the
compiled catalog equal. Replacement does not promise compatibility with every
older save. Legacy suffixed versions are still selected by their exact saved
version when installed.

A restored battle opens without the loading screen, so its warnings are
posted to the battle message line (and standard error) rather than as
loading-screen lines (§8.3).

**Continuation saves.** A between-missions save starts the next mission
fresh, so what it takes from its sidecar is the selection that mission is
entered under: the mod (step 2), the mutators, the rule set and the unit
limit. Its Community sources are not carried forward; the fresh entry
resolves the host's, as every new battle does.

### 7.4 Contracts this replaces

- DESIGN_GAMEPLAY_RULES §6 says a save records no rule-set name. The bank
  still records none; the sidecar does, and a load restores it. A save without
  a sidecar keeps the §6 behaviour.
- The `TODO(question)` in `internal/session/retail_save.go` asking for a
  Nanolathe-side save metadata area is settled by §7.
- The Modern and Community saved-unit-limit policy
  ([DESIGN_SESSIONS_AI_SAVE](DESIGN_SESSIONS_AI_SAVE.md#modern-save-unit-limits))
  is unchanged. With a sidecar, the configured word already equals the saved
  limit before that policy is asked.

## 8. Presentation

### 8.1 The main-menu chip

A Nanolathe-owned control drawn by the host over the authored `MAINMENU`, in a
fixed position on the logical 640×480 surface: the left half of a centred
pair whose right half is the REPLAYS button
([DESIGN_INTERFACE_HUD_INPUT "Replays"](DESIGN_INTERFACE_HUD_INPUT.md#replays)).
Its button reads *NANOLATHE*
and its status line, for example, *ProTA 4.8 · Community 3.9 · 2 mutators*
(with unit restrictions, *· 3 restrictions* follows, §15.9).
In the window it opens the Nanolathe screen
([DESIGN_INTERFACE_HUD_INPUT §3.17](DESIGN_INTERFACE_HUD_INPUT.md#317-the-nanolathe-screen)),
which chooses the mod, the rules and the mutators together; a shell with no
window opens the Mods & Mutators screen, which the Nanolathe screen's
*Manage mods* button also opens for installing, downloading and removing. Mods repaint the front end, so its position is chosen by `--shot`
review against the retail, ProTA and Escalation menus rather than relative to
any authored art. With a manual root stack it reads *Custom content*.

### 8.2 The Mods & Mutators screen

A Nanolathe-owned window in the style of the existing Nanolathe options pages,
in two columns, so that no list competes with another for space (D14):

- **Mods (left).** Installed mods only, with *Total Annihilation* (no mod)
  first. Each row shows the name and version, and flags an unmet base
  requirement. Selecting a row shows its summary, its minimum gameplay and the
  gameplay selection that applying will produce (§4.3). It also shows the
  *Use recommended settings* toggle when switching to a mod that names a
  preset (§4.3), and *Remove*.
  *Get more mods…* sits beneath the list.
- **Mutators (right).** A summary, not the editor, so the column never grows
  with the catalogue: up to four active mutators (then *and N more*),
  *Change...* and *Reset*.
- *Apply* writes the settings for both columns. When the mod changed it
  reloads (§4.4), and the settings are written only once the new content is
  bound. *Cancel* discards both.

The screen and both dialogs are built on the map-select window (`SELMAP`)
read from the **base install alone**, never through the running mod's
overlay. A mod may ship its own map-select art (ProTA's is full-screen, with a
different panel layout), and the screen that switches mods should look the
same whichever mod is running. The fonts, button art and palette still come
from the running content. A list heading in these windows is drawn in the
window's own colour over its darkened row; retail darkens the heading text
with the row ([07 R-WGT-01 §4]), which is too dim to read at this size.
Retail windows keep the retail heading. Nanolathe-authored art for these
windows is a possible follow-up, not a need.

On the Nanolathe screen, an installed mod's X opens a removal confirmation.
Its click target takes precedence over row selection, including when rule or
controls badges move the X into the row. *Keep* leaves the install unchanged;
*Remove* deletes it from the library and refreshes the list. The running mod
has no X and cannot be removed.

**The Mutators dialog.** *Change...* opens a modal over the screen built
from the mutator catalogue (`content.MutatorCatalog`), so a new mutator needs
no layout work: a scrolling list with a heading row per group (Economy,
Combat, Movement, Vision), each row showing the mutator and its factor; the
selected mutator's description beneath; *Raise*, *Lower*, *Default* and
*Reset all* in the right-hand column; *OK* keeps the changes for the screen's
*Apply*, and *Cancel* restores the set the dialog opened with.

**The Get more mods dialog.** A modal over the screen. Opening it fetches the
manifest (§5.2); when that fails, it shows the cached manifest and its age.
It lists every manifest entry whose id, version and case-insensitive archive
hash do not match an install, with name, version, download size and summary.
The Nanolathe screen's catalogue instead keeps matching rows visible as
*Installed*. A changed hash offers *Download* again under the original version;
both screens disable the mounted version with *Switch mods before updating*.
*Download* on a row starts §5.3, with a progress bar; while the archive transfers
the button reads *Cancel*. One download and install runs at
a time in the process: while one runs, the dialog shows its progress and
offers no *Download*, and closing the dialog leaves it running. Once the
archive is verified its install finishes even if the dialog or the screen
closes; the outcome is shown the next time the dialog opens, and the mod
joins the installed list at once and is not selected automatically.
*Cancel*, or leaving the Mods & Mutators screen, stops the transfer and keeps
the part file in `.downloads/`, so a later download of the same archive
resumes where the server allows it (§5.3). *Download cancelled* is shown only
when the player cancelled; a stalled transfer shows its own error. Closing
the dialog also stops a catalogue fetch still in progress.

### 8.3 The loading screen

Up to three lines in the retail font and title colour, above the existing map
line:

1. `<mod> <version> · <gameplay> · Unit limit <n>`, with *Total
   Annihilation* when there is no mod. `<n>` is the limit the battle enters
   with: when the player chose none (no saved `unitLimit` or `--unit-limit`),
   a Community feature table's limit applies (DESIGN_COMMUNITY_PATCH §3.2),
   and the field then names the source, as in *Unit limit 1500 (set by
   ProTA)*. Strict 3.1 ignores the table and shows the setting;
2. `Mutators: Build speed ×2, Health ×1.5`, omitted when there are none,
   followed on a skirmish or Survival battle with unit restrictions by
   `Restrictions: 3 removed, 2 capped` (§15.9);
3. any warning from §7.3, and the notice for saved restrictions the running
   content cannot apply (§15.3).

This is a Nanolathe divergence from the authored screen
([07 "The loading screen"]). A restored battle does not pass through the
loading screen, so §7.3's warnings go to the battle message line instead.

### 8.4 The load dialog

The selected save's summary panel gains one line with the sidecar's mod and
mutators, and the count of its unit restrictions when it has any (§15.5),
so the player knows before loading that it will switch. It is a
Nanolathe label (`NLSIDECAR`) beneath the authored `TIME` field; a mod the
library lacks is marked *(not installed)*, and a save without a sidecar
leaves the line empty (DESIGN_INTERFACE_HUD_INPUT §2.6).

## 9. Packages and boundaries

| Package | Owns | Imported by |
|---|---|---|
| `internal/modlibrary` | data directory layout, metadata and the mod's config (§4.2), installed listing, selection resolution (mod → root, config, minimum, preset), extraction and validation, receipts. No network. | both commands |
| `internal/maplibrary` | separate installed-map root and map-only payload validation, reusing modlibrary extraction and receipts. No network. | desktop command |
| `internal/modfetch` | mod and map manifest fetch and cache, downloads with resume and progress. The content-download package that imports `net/http`; multiplayer WebSocket HTTP is isolated in `internal/relay` (DESIGN_MULTIPLAYER §16.5.6). | `cmd/nanolathe` only |
| `internal/content` | `Factor`, `Mutators`, `ApplyMutators`, the mutated catalog identity; `Restrictions`, `CheckRestrictions`, `ApplyRestrictions` and the restricted identity (§15.2) | session, commands |
| `internal/save` | the sidecar type and its read/write, separate from the bank's bytes | session, commands |
| `internal/session` | mutators, unit restrictions and recorded Community sources in battle-entry and restore requests; building the sidecar value; reports | commands |
| `internal/settings` | the `mod`, `mutators` and mod-scoped `restrictions` keys | commands |
| `cmd/nanolathe` | chip, screen, in-process reload, loading-screen lines, drop-to-install, `--mod`, `--mutator`, `--restrict`, the restriction card and the unit viewer's restriction editor (§15.9) | — |
| `cmd/nanolathe-headless` | `--mutator`, `--restrict` and `--mod` (flags only, never the settings file). `--mod` mounts an installed mod from the library as the last root with its own config, through `modlibrary`'s command-line selection; it is refused beside several `--root` flags, and the command never fetches. `--mod-config` names a config file for a manual stack or in place of the mod's own | — |

**Guards.** Architecture tests: only `internal/modfetch` and the multiplayer
WebSocket transport in `internal/relay` import `net/http`; only `cmd/nanolathe`
imports `internal/modfetch`; no simulation
package imports either new package. `ApplyMutators` walks weapon damage
through `DamageKeysSorted`, never by ranging a map ([INVARIANTS](INVARIANTS.md)
I1). Everything used — `archive/zip`, `net/http`, `crypto/sha256` and, later,
`crypto/ed25519` — is standard library, so there are no new module
dependencies.

## 10. Verification

| What | Where |
|---|---|
| Map catalogue dependency references and canonical OTA paths validate; cache and archive trust checks also cover maps | `modfetch` |
| Map installs reject gameplay/config payloads, parse OTA/TNT pairs, and list installed roots deterministically | `maplibrary` |
| Map download preserves mod and setup, installs shared dependencies once, and exposes the selected map without restart; map picker and download panel visually reviewed | desktop map-catalog tests and captures |
| Extraction refuses absolute paths, `..`, symlinks and case-folded duplicates; skips executables and `__MACOSX`; enforces the caps; an interrupted install leaves nothing installed | `modlibrary` tests on authored fixture zips |
| Metadata disagreeing with the manifest refuses the install; unmet `requires` block selection | `modlibrary` |
| Catalogue replacement preserves the original version, verifies the archive, keeps the old install on failure and recovers interrupted publication before staging cleanup; manual duplicates still refuse | `modlibrary.TestCatalogueReplacementKeepsOldInstallUntilValidated`, `modlibrary.TestReplacementRequiresVerifiedArchiveIdentity`, `modlibrary.TestOpenRecoversInterruptedReplacement` |
| Both mod screens require id/version/hash agreement for current status and protect mounted versions before download and across asynchronous install/reload, including manual root aliases | `main.TestCatalogueCurrentRequiresVersionAndArchiveHash`, `main.TestBothModScreensRefuseUpdatingMountedVersion`, `main.TestModUpdateGuardAcrossAsynchronousInstall`, `main.TestModUpdateRefusesManuallyMountedDirectory` |
| A mod row's X receives clicks with any badge combination, opens confirmation without selecting the mod, and removes it only after confirmation | `main.TestNLScreenModRemovePointer` |
| A negative build page lock is refused; a config carries it as `content.presentation.build_menu_page_size`, and the player's settings value wins | `modlibrary.TestBuildMenuPageSizeMetadata`, `main.TestContentBuildMenuPageSize`, `main.TestExpandedSidebarBuildPageLock` |
| Precedence: `--mod` over setting, a manual stack disables both, a mod's own config beats a saved `contentProfile`, `--mod-config` beats both, a removed profile name is ignored with the notice, a command line below the minimum is rejected | `modlibrary`, `main.TestContentConfigPrecedenceWithoutAMod`, `main.TestManualStackWithoutAConfigShowsTheNotice` |
| Every config section is closed and validated (content, reserved gameplay words, feature bounds, settings keys and types, reserved settings keys, keyboard actions and chords, lock paths); the settings layer applies only what it names | `modlibrary.TestParseConfigDocument`, `modlibrary.TestParseConfigDocumentRefusals`, `modlibrary.TestConfigSettingsLayer` |
| A schema 1 or metadata-less mod is plain content with the notice and no recommendations | `modlibrary.TestSchemaOneModIsPlainContent`, `modlibrary.TestLocalPackageIsPlainContent` |
| The four repository configs reproduce the removed tables and profiles exactly (value and digest); ProTA's and TA Zero's settings and keys are their preset columns; Escalation and Mayhem carry a minimum only | `modlibrary.TestShippedConfigsReproduceTheRemovedTables`, `modlibrary.TestShippedConfigsCarryTheRemovedProfiles`, `main.TestShippedConfigSettingsAreThePresetRows` |
| Preset contents, and the retail preset keeping skirmish rows | `main.TestCommunityControlsPresetContents`, `main.TestRetailControlsPresetKeepsSkirmishRows`, `main.TestRetailPresetColumnIsTheDefaults` |
| A ProTA package's recommendations are its settings layer after `--mod`; a change under ProTA stays ProTA's and the original game plays the base; the loading line names the effective unit limit | `main.TestProTARecommendedSettingsAreItsLayer` (retail tier) |
| Layers, diffs and atomic subtrees; a change is kept per mod; locked paths play the mod's value until overridden and keep the player's; presets save and apply by part | `settings.TestLayers*`, `main.TestModSettingsKeepChangesPerMod`, `main.TestModConfigSettingsAndLocks`, `main.TestNLScreenPresetsSaveAndApplyPart` (retail tier) |
| SHA-256 and size mismatch, truncated download, resume only for the same expected hash, off-origin redirect refused, offline cache shown | `modfetch` against `httptest` |
| Zero mutators leave the clone deep-equal with an equal `Hash`; each mutator changes exactly its fields; rounding, minimum-one, saturation and `v ≤ 0` boundaries; the hash is independent of field order | `content` |
| Sight above ×1 expands both rasters, preserves terrain obstruction and balances retirement; original inputs and reductions remain unchanged | `content.TestSightMutatorExtendsOnlyNeededRasterInputs`, `content.TestGeneratedSightRaysCoverDisc`, `visibility.TestSightMutatorWidensCoverageAndRetires`, `visibility.TestSightMutatorKeepsTerrainOcclusion`, `visibility.TestStockSightMutatorCoverage` (retail tier) |
| Mutators reach fresh skirmish, mission entry and restore; all six fingerprint locks unchanged | `session`, `headless` |
| Under Strict 3.1 with Build speed ×2, a fixed construction finishes in half the ticks and bills the same total resources (a relationship, not a census) | `session` |
| Under Strict 3.1, Income scales a generator's and a solar collector's settlement production k-fold and leaves a self-powered unit neutral; Unit speed shortens a straight move to about 1/k of the ticks; Fire rate builds a stockpile round in 1/k of the ticks at the authored cost (relationships) | `session.TestStrictIncomeScalesSettlementProduction`, `session.TestStrictUnitSpeedShortensAStraightMove`, `session.TestFireRateStockpilesAtEqualCost` |
| At the extreme steps on the stock catalog: no aircraft's flight decay reaches zero under Unit speed, and no metal deposit is scaled | `session.TestStockCatalogUnderExtremeSteps` (retail tier) |
| Sidecar round trip; a load without one behaves as today; a load restores rule set, Community sources and entry table, unit limit and mutators, and selects the recorded mod and mutators (P6); bank bytes unchanged | `save.TestSidecarRoundTripsEveryModSpelling`, `save.TestSidecarRefusesAnotherSchemaAndMalformedFiles`, `main.TestSaveSidecarRestoresTheRecordedSelection` (retail tier) |
| A save made under another installed mod reloads onto it and then restores; one whose mod is not installed is refused, naming it | `main.TestLoadingAnotherModsSaveSwitchesToItFirst`, `main.TestSaveSidecarRestoresTheRecordedSelection` (retail tier) |
| The reclaim-pulse product at the maximum step for the stock catalog (§6.5) | `content`, retail tier |
| Chip on retail, ProTA and Escalation menus; Mods & Mutators screen and the *Get more mods* dialog; loading-screen lines | `--shot` captures, reviewed |
| A merged hosted zip resolves the same winners and catalog hash as its original root list | the check tool (§13 unit 8) |
| Unit restrictions | the tests of §15.11 |

The applicable gates are those in [ARCHITECTURE §6](ARCHITECTURE.md#6-verification).

## 11. Documents to update on implementation

- `AGENTS.md` and [INVARIANTS](INVARIANTS.md) I11 record the mutator
  exception (done with this design).
- [DESIGN_GAMEPLAY_RULES §6](DESIGN_GAMEPLAY_RULES.md#6-save-interaction): the
  sidecar (§7.4).
- [DESIGN_CONTENT_VFS §5](DESIGN_CONTENT_VFS.md#5-divergences): the mod root
  and the explicit profile selector.
- [DESIGN_SESSIONS_AI_SAVE](DESIGN_SESSIONS_AI_SAVE.md): the sidecar contract
  and restore order.
- [DESIGN_INTERFACE_HUD_INPUT](DESIGN_INTERFACE_HUD_INPUT.md): chip, Mods &
  Mutators screen, *Get more mods* dialog, loading-screen and load-dialog
  lines.
- [PROTA_SUPPORT](PROTA_SUPPORT.md): the Mods & Mutators screen as the
  ordinary route, with manual roots as the alternative.
- For unit restrictions (§15): AGENTS.md and [INVARIANTS](INVARIANTS.md) I11
  (the fifth mode-independent exception; INVARIANTS done with this design,
  AGENTS.md by the maintainer);
  [DESIGN_MULTIPLAYER](DESIGN_MULTIPLAYER.md) §3.3 and §8.6 field 12 (done with
  this design); [DESIGN_SURVIVAL](DESIGN_SURVIVAL.md) §6.5 and
  [DESIGN_SESSIONS_AI_SAVE](DESIGN_SESSIONS_AI_SAVE.md#modern-ai-restriction-caps)
  (done with this design); on implementation,
  [DESIGN_INTERFACE_HUD_INPUT §3.17](DESIGN_INTERFACE_HUD_INPUT.md#317-the-nanolathe-screen)
  (the card) and
  [DESIGN_DEVELOPER_TOOLS §7](DESIGN_DEVELOPER_TOOLS.md#7-3d-unit-viewer-preview)
  (a pointer to the editor of §15.9).

## 12. Confirmed proposals (2026-09-23)

The maintainer confirmed every proposal this document made, amending P6 so
that a save selects its mutators as well as its mod.

| ID | Decision | Section |
|---|---|---|
| P1 | Build speed scales `buildtime` inversely (revised and confirmed 2026-09-23). The first version scaled `workertime`, which the integer worker quantum makes inexact and able to stop builders | §6.5 |
| P2 | Build cost also scales corpse-chain feature `metal`/`energy`; map-authored features are not scaled | §6.5 |
| P3 | Radar also scales the two jamming distances | §6.5 |
| P4 | Initial step list 0.25, 0.5, 0.75, 1, 1.5, 2, 3, 4, spelled as decimals | §6.4 |
| P5 | Mutators apply to campaign missions as well as skirmish | §6.3 |
| P6 | Loading a save selects both the mod and the mutators its sidecar records | §7.3 |
| P7 | An unselectable recorded rule set loads under its base, with a warning | §7.3 |
| P8 | A catalog hash mismatch on load warns and does not refuse | §7.3 |
| P9 | Older versions stay installed until removed | §4.1 |
| P10 | Controls preset offered with a default-on checkbox, applied once | §4.3 |
| P11 | Metadata-less local packages install as `local-*` | §4.5 |
| P12 | Manifest path, extraction caps and data directory location, open to change later | §4.1, §5.1, §5.3 |
| P13 | The load dialog shows the sidecar line | §8.4 |
| P14 | Blast size (areaofeffect) leaves a weapon with an area of 16 or less alone and never scales a larger area below 17, so direct-hit weapons stay direct-hit (added 2026-09-23 at the maintainer's request) | §6.5 |
| P15 | Income scales production only: the surplus of `energymake` over a positive `energyuse`, the other producing keys and the solar arm; upkeep, `makesmetal`, storage and costs stay (added 2026-09-26 at the maintainer's request) | §6.5 |
| P16 | Salvage scales every destructible feature's pools, map features included, and composes with Build cost as one product; metal deposits are excluded (added 2026-09-26) | §6.5 |
| P17 | Fire rate scales `reloadtime` inversely and leaves `burstrate` alone (added 2026-09-26) | §6.5 |
| P18 | Unit speed keeps paths' shape (velocity and turn rate k, acceleration k², braking k² on the ground and k in the air); overshoot at high factors is accepted, since mutators are for fun (added 2026-09-26) | §6.5 |

## 13. Work units

Ordered so each lands green on its own. No unit moves an existing
fingerprint. Units 1–8 are implemented.

1. **Mutator core.** `content.Factor`, `Mutators`, `ApplyMutators`, the
   mutated identity and boundary tests; the derived-value audit and the
   reclaim-pulse check recorded in §6.5. No consumer yet.
2. **Mutator plumbing.** Battle entry, mission entry and restore requests;
   `--mutator` and the settings key; report fields; the Strict build-speed
   relationship test.
3. **Save sidecar.** Write, delete and overwrite with the bank; load restores
   rule set, Community, unit limit and mutators, and selects the recorded
   mutators (P6). A sidecar naming a different mod is refused until unit 5.
4. **Local mod library.** Data directory, metadata, extraction, drop-to-install,
   `--mod`, the explicit profile, the gameplay minimum, the controls preset,
   the in-process reload.
5. **Mod switching on load.** A sidecar of another mod selects it,
   reloads onto it and then loads the save (P6).
6. **Presentation.** Chip, the two-column Mods & Mutators screen (installed
   mods and mutators, D14), loading-screen lines and the load-dialog line,
   with captured review.
7. **Remote catalogue.** `internal/modfetch`, manifest cache, download,
   verification and install through unit 4's path, and the *Get more mods*
   dialog with its progress.
8. **nanolathe.gg content.** The manifest, the cleaned zips and the merge
   check tool for multi-package mods (§5.5).
9. **Second mutator set.** Income, Salvage, Fire rate and Unit speed (P15–P18),
   their boundary tests in `content` and relationship tests in `session`.
   Implemented.
10. **Follow-ups.** Manifest signatures (§5.4); fuzz targets for the exposed
   readers, recommended before the catalogue is advertised widely;
   `minimumEngine` once releases are stamped.
11. **Unit restrictions.** Implemented as the four units of §15.12; the
   follow-ups listed there remain.

## 14. Research map

| Behaviour | Owning research |
|---|---|
| Stored integer widths of the mutated unit keys | [02 R-KEYS-01 §5] |
| Construction step and spend | [05 R-WORK-01 §1] |
| Repair terms | [05 R-WORK-01 §3] |
| Unit reclaim pulse and its overflow | [05 R-WORK-01 §4] |
| Feature reclaim independent of `workertime` | [05 R-WORK-01 §5] |
| Capture timer | [05 R-WORK-01 §6] |
| Resurrection | [05 R-WORK-01 §7] |
| Abandoned-frame decay | [05 R-WORK-01 §9] |
| Malformed build numbers | [05 R-WORK-01 §11] |
| Production keys, their single store and the settlement gather (Income) | [05 R-PROD-01 §1], [05 R-ECO-01 §2] |
| Deposits seed the metal byte (Salvage's exclusion) | [05 R-FEAT-01 §7] |
| Reload countdown, per-shot cost, bursts and the stockpile queue (Fire rate) | [06 §4.2], [06 §4.3], [06 §11.1] |
| Ground and flight integrators (Unit speed) | [04 R-MOV-01 §4], [04 §10.1] |
| Sight-shape and terrain-ray quantization | [03 §3.2] |
| Configured unit-limit carry on load | [08 R-SESS-01 §9] |
| The loading screen | [07 "The loading screen"] |
| Unit restrictions: the allocator gate, its census and failure table; removal as compaction | [05 R-SHARE-01 §8], [05 R-ECO-02 §4], [08 R-ENTRY-01 §2] |
| Unit restrictions: the limit field, the lobby apply, `norestrict` and `wacky` | [05 R-SHARE-01 §9] |
| Unit restrictions: the computer player never reads them | [05 R-SHARE-01 §10], [08 R-AI-01 §12] |
| Unit restrictions: the `RESTRICT2` screen, slider range, Reset and `.LST` lists | [08 R-SKIR-01 §10], [08 R-SAVE-02 §5] |
| Unit restrictions: a removed product's greyed slot | [07 R-HUD-03 §6] |
| Unit restrictions: sort, `CANBUILD` and download appends after compaction | [02 R-CAT-01 §5], [02 R-CAT-01 §8] |

## 15. Unit restrictions

**User-authorized 2026-10-05; design approved the same day; all four units
of §15.12 implemented 2026-10-06.** On 2026-10-05 the
user chose *Retail restrictions, every mode*: implement retail's multiplayer
restriction rules once, offer them in
skirmish and Survival beside the mutators, edit them in the unit viewer, and
let the multiplayer lobby reuse them later (D17). The same day the user
decided that in Survival everyone obeys them, the wave attacker included, as
retail binds computer players exactly as it binds humans (D18), and that
Classic computer players keep retail's behaviour while the Modern AI does not
choose a unit once it has reached that unit's cap (D19). Like the mutators,
Survival, the Modern AI computer player and multiplayer, unit restrictions are
a mode-independent exception (AGENTS.md,
[INVARIANTS I11](INVARIANTS.md#i11--retail-baseline-and-modern-gameplay)).
What this section had to decide beyond those words is listed in §15.10;
the user approved every proposal there as written on 2026-10-05.

### 15.1 Policy and boundary

**What a restriction is.** One count per unit definition in the battle's
match selection (§3):

- **absent** — *No limit*. The definition keeps the parser's `-1` limit and
  the allocator skips its per-definition test, as in every retail
  single-player battle [05 R-SHARE-01 §9].
- **0** — the definition is **removed from the battle**. Battle entry deletes
  it from the per-battle catalog clone exactly as retail's battle-entry
  compile deletes a record whose creatable bit is clear: the survivors are
  renumbered, and the removed definition has no unit index, no build-menu
  button and cannot be spawned by name [05 R-SHARE-01 §8]
  [08 R-ENTRY-01 §2]. It is retail's multiplayer path for a row left at zero,
  whose definition the lobby's close and apply mark not creatable before the
  compile compacts it out [05 R-SHARE-01 §9] [08 R-SKIR-01 §10].
- **1 to 100** — a **per-player cap**. The definition stays and its limit
  field holds the count. The allocator's third test counts the records of
  that definition in the creating player's own slice — nanoframes, completed
  units and dead units whose teardown has not yet cleared the index — and
  refuses when the count has reached the cap [05 R-SHARE-01 §8]. Nothing
  before the allocator reads the field: the button is drawn like any other,
  the product can be queued and placed, and the refusal is the allocator's,
  through its failure table — the factory, mobile-build and resurrect
  caption with its 300-tick wait, the VTOL build that abandons, the
  ownership transfer that leaves the unit with its owner, the spawner that
  continues [05 R-SHARE-01 §8] [05 R-ECO-02 §4].

The range is retail's slider, 0 to 100 with *No Limit* above it
[08 R-SKIR-01 §10].

**Every mode, two sessions.** Retail offers restrictions only in the
multiplayer battleroom: no skirmish path reaches its screen, and a skirmish
or campaign battle never runs its apply [08 R-SKIR-01 §10]
[05 R-SHARE-01 §9]. Nanolathe offers them in skirmish and Survival under
every gameplay mode, Strict 3.1 included, because, like a mutator, a
restriction transforms the battle's content rather than a rule. It is
applied once to the per-battle catalog clone at battle entry, adds no seam to
`session.RuleSet`, draws no random numbers and keeps no per-tick state.
Inside a tick its caps are read where retail reads them, by the allocator's
existing per-definition test on every creation path, and otherwise only by
the Modern AI's observation (§15.7). The retail arithmetic that consumes
them — the compaction and the allocator's census — is unchanged in every
mode.

**Not campaign missions.** A mission is entered under its own authored
restriction, the `UseOnlyUnits` list ([08 R-ENTRY-01 §2] step 4,
`Catalog.RestrictToCreatable`), and its placement, its triggers and its AI
profile name the unit types that list keeps
([DESIGN_SESSIONS_AI_SAVE §2.3](DESIGN_SESSIONS_AI_SAVE.md#23-internaltriggers--the-eighteen-conditions)).
A player's restriction layered on top could remove a unit a victory
condition counts or a placement creates and make the mission unwinnable
without saying so, and a campaign would have to carry the set from mission
to mission. The authorization names skirmish and Survival; mission entry,
mission restore and continuation saves take no restriction set. Mutators,
which change numbers rather than which units exist, still apply to missions
(P5).

**Who obeys.** Everyone. Every creation passes through the allocator, which
binds a computer player exactly as it binds a human [05 R-SHARE-01 §10]:
human players, Classic and Modern computer players and, in Survival, the wave
attacker (§15.6). How each kind of computer player meets a restriction is
§15.7.

**`norestrict` and `wacky`.** A definition authoring `norestrict` is never
restricted: retail's screen does not offer it, and the bit has no other
reader [05 R-SHARE-01 §9] [08 R-SKIR-01 §10]. An entry naming one is refused
(§15.3). The reference install has fourteen such definitions, both
commanders among them, and no stock definition authors `wacky`
[05 R-SHARE-01 §9]. A `wacky` definition is restricted like any other;
retail's lobby treats it differently only in its seed and its *Reset*, and the
editor's *Reset* returns it to *No limit* with every other unit (§15.9).

**Nothing is seeded.** The empty set is the default and changes nothing:
battle entry hands the compiled catalog through unaltered — the same catalog
value, as with no mutators — so `Catalog.Hash`, every definition index and
per-definition `Hash`, the simulation-content manifest and every fingerprint
are what they were, for content with `wacky` definitions as for any other.
Retail's lobby seeds a `wacky` definition's restriction at 0 and its *Reset*
puts every row at 100 [05 R-SHARE-01 §9] [08 R-SKIR-01 §10]; neither is a
default here:

1. the seed belongs to the multiplayer lobby record, which a retail skirmish
   never builds, so a retail skirmish runs every definition, `wacky` ones
   included, with no per-definition limit [05 R-SHARE-01 §9] — the baseline
   this feature starts from;
2. a seed would restrict content the player never chose to restrict and move
   the identity of every battle on that content: a hidden selection of the
   kind §4.3 rules out;
3. a board of 100s caps every unit, which is a choice, not the absence of
   one.

Both are editor actions the player takes (§15.9). Retail's seeded state with
the restriction screen never closed — a `wacky` record kept with limit 0, on
the menus and refused at every creation — has no single-player equivalent and
is left to the online lobby (DESIGN_MULTIPLAYER §15 Q16).

**Baseline.** Strict 3.1 with no restrictions and no mutators is the retail
baseline, and every fingerprint lock runs with neither.

**Order with the other transforms.** Restrictions apply immediately after
the catalog compile, before the Community weapon preparation and the
mutators: the position mission entry gives `RestrictToCreatable`. The
mutators then see the restricted catalog, as they see a mission's: Build cost
reaches the corpse chains of the definitions that remain, and Sight extends
rasters only for radii a remaining definition needs. A restore applies them
in the same order (§15.5), so a restored battle's catalog is the one its
fresh entry built.

### 15.2 Shape

```go
package content

// RestrictionMaxCount is the largest per-player count: retail's slider
// stores 0..100 and shows No Limit above it [08 R-SKIR-01 §10].
const RestrictionMaxCount = 100

// Restriction is one entry. Count 0 removes every retained record carrying
// the name from the battle; 1..RestrictionMaxCount caps each player's
// records of it (§15.1).
type Restriction struct {
	Unit  string // CanonicalKey of the unit name
	Count uint8
}

// Restrictions is one battle's set: at most one entry per key, kept in
// ascending key order. A unit without an entry has No limit. The zero value
// restricts nothing.
type Restrictions struct{ entries []Restriction }

func ParseRestrictions(values map[string]int) (Restrictions, error) // settings and sidecar spelling
func ParseRestriction(text string) (Restriction, error)            // flag spelling, "armpw=20"
func (r Restrictions) IsZero() bool
func (r Restrictions) Entries() []Restriction // a copy, in key order
func (r Restrictions) Count(unit string) (count uint8, restricted bool)
func (r *Restrictions) Set(unit string, count uint8) error // refuses a count above 100
func (r *Restrictions) Clear(unit string)                  // back to No limit
func (r Restrictions) Equal(o Restrictions) bool
func (r Restrictions) Map() map[string]int // settings and sidecar spelling; empty when zero
func (r Restrictions) String() string      // canonical, e.g. "armkrog=0,armpw=20"; "" when zero
func (r Restrictions) Digest() string      // HashDefinition("restrictions/1\n" + String() + "\n")

// RestrictionIssue says why one entry cannot apply to a catalog (§15.3).
type RestrictionIssue struct {
	Unit   string
	Reason RestrictionReason // RestrictionUnknownUnit, RestrictionNoRestrict, RestrictionRemovesCommander
}

// CheckRestrictions splits r into the entries this catalog accepts and an
// issue for every other entry, both in key order. It reads only immutable
// definition data and writes nothing.
func (c *Catalog) CheckRestrictions(r Restrictions) (accepted Restrictions, issues []RestrictionIssue)

// ApplyRestrictions transforms a per-battle clone in place (§15.4). Like
// RestrictToCreatable and ApplyMutators, it is never called on a shared
// compiled catalog. It writes nothing and returns an error when
// CheckRestrictions would report any issue, so an unchecked set can never
// half-apply.
func (c *Catalog) ApplyRestrictions(r Restrictions) error
```

The set is a sorted slice, not a map, so no reader ranges a map (INVARIANTS
I1), and two sets compare with `Equal`. Parsing checks spelling only: a key
that is not already its own `CanonicalKey` (field 12 refuses the same), a
count outside 0..100, a flag count with a sign, a fraction or a leading zero,
a flag without `=`, and a key given twice are refused. Whether a key names a
unit is the catalog's question (§15.3). The type is closed; a later kind of
restriction is a new field and its tests, as a new mutator is.

### 15.3 Names and resolution

**A key is a unit name, and it means every record so named.** An entry
applies to every retained record whose unit name has its key, not only to the
record the catalog's name lookup (`Catalog.Unit`) returns. That lookup
returns the first record of a name in the sorted table and hides the later
ones [02 R-CAT-01 §5], while retail's tree holds one entry per record, keyed
by the record's content checksum, so its screen lists every record
[05 R-SHARE-01 §9]. Restricting only the record the lookup returns would let
a removal expose a hidden record under the same name, on the same menus,
which no player means; restricting the name restricts the unit as the player
sees it (proposal R-P1). A name counts as `norestrict` when any record
carrying it authors `norestrict`, and as `wacky` when any record carrying it
authors `wacky`.

**Checks.** `CheckRestrictions` refuses an entry for one of three reasons:

| Issue | When |
|---|---|
| `RestrictionUnknownUnit` | no retained record carries the name |
| `RestrictionNoRestrict` | a record carrying the name authors `norestrict` (§15.1) |
| `RestrictionRemovesCommander` | the count is 0 and a side record names the unit as its commander. Skirmish entry places a commander for every player and, never fabricating one, refuses a side whose commander the catalog lacks, so such a set would stop every battle on that content. This is a Nanolathe check on the selection, not a claim about retail's start spawn. A positive count is accepted (proposal R-P3). |

Each source of a set answers an issue in its own way, so that a saved
preference never stops a game and an explicit request never silently loses
an entry (proposal R-P4):

| Source | On an issue, or a malformed entry |
|---|---|
| `--restrict` | the command stops with its standard diagnostic naming the entry, as for a bad `--ai-player` row: `nanolathe: unit restriction armfoo=0: logical path --restrict, providers searched [unit catalog], expected a unit the running content defines that is not marked norestrict` |
| the settings key (§15.9) | the entry is left out of that battle and named in a notice on the loading screen (§8.3) and on the Nanolathe screen card; it stays in the file and applies again whenever content that has the unit runs |
| a save's sidecar (§15.5) | the load is refused, naming the entry: the bank's definition indices were written against the restricted table, which can no longer be rebuilt |
| battle-configuration field 12 | the configuration is rejected (DESIGN_MULTIPLAYER §8.6) |

**Field 12.** DESIGN_MULTIPLAYER's battle-configuration field 12 carries
`{DefinitionID u16, Unit key, Limit u8}` records in ascending definition-ID
order. A `Restrictions` value becomes one record for every retained record an
entry names: that record's index in the unrestricted compiled catalog, the
entry's key and its count. An entry for a duplicated name therefore gives one
record per copy, which is how the field keeps duplicate-name identities. The
reverse mapping requires every record carrying a named key to be present
with one limit, refuses the three issues above, and gives back the same
`Restrictions`. Admission compares it with the set the frozen inputs record
(§15.5), as it compares the mutators. The two directions are
`session.MatchUnitRestrictions` and `session.RestrictionsFromMatch`;
`FreezeMatchInputs` runs the reverse mapping against the install's
unrestricted catalog, since admission sees only the frozen catalog's
surviving records (DESIGN_MULTIPLAYER §16.6).

**Retail `.LST` lists** — the `SAVEGAME\<name>.LST` files of
`(content checksum, restriction value)` pairs that retail's restriction
screen saves and loads [08 R-SAVE-02 §5] [08 R-SKIR-01 §10] — are a later
item, not part of
this delivery (proposal R-P12). Matching them to definitions needs the
composite per-definition checksum of [02 "Content checksum"], which Nanolathe
does not compute today, and a per-record key, which the name-level set does
not express.

### 15.4 The transform

`ApplyRestrictions` changes the clone in three steps:

1. **Removal.** Every retained record carrying a name whose count is 0 leaves
   the table. The survivors keep their relative order and are renumbered from
   1 (`UnitDefID`). The first-name index (`Catalog.Units`), the category
   registry with every definition's masks, and each survivor's
   per-definition `Hash`, which covers its index, are rebuilt as
   `RestrictToCreatable` rebuilds them. Records taken out of a table sorted by
   name leave it sorted, so for survivors with distinct names this is the
   order retail's battle-entry sort gives the compacted table
   [02 R-CAT-01 §5]. `TODO(question)`: whether retail's sort, which does not
   keep equal names in their input order, gives a group of same-name
   survivors the order they had in the full table is not established. Settle
   it by keeping each record's discovery position and discovery-time name at
   compile and re-running the retained sort over the survivors, or by a
   trace; until then the group keeps its compiled order. An authored Survival
   roster (DESIGN_SURVIVAL §5.1) drops its entries for removed units, so the
   clone's roster names only definitions the clone holds.
2. **Caps.** Every retained record carrying a name whose count is 1 to 100
   gets `Limit = count` and `LimitEnabled = true`. `Limit` is excluded from
   definition identity, so caps move no per-definition `Hash`; the
   simulation-content manifest digests it with each unit (DESIGN_MULTIPLAYER
   §8.7).
3. **Identity.** `Catalog.Hash` becomes
   `HashDefinition("catalog+restrictions\n" + base + "\n" + r.Digest() + "\n")`,
   `base` being the clone's hash before the call, as the mutators restamp it.
   The tag `restrictions/1` inside `Digest` moves whenever the transform
   changes meaning. The mutators then hash over this value (§6.6), so a
   battle with both has the identity `mutators(restrictions(compiled))`.

**What it leaves alone.** Build menus are not recompiled. Every consumer
resolves a product's name through the catalog lookup, and a removed product
resolves to nothing: its menu slot is greyed (§15.8), and the computer
players' tables and the Survival tier walk skip it. Weapons, features — a
removed unit's corpse feature stays, since a map may place it — movement
classes, sides and AI profiles are untouched. One difference from retail
remains, shared with `RestrictToCreatable`: retail resolves each builder's
`CANBUILD` list and its download appends after the compaction, so a removed
product takes none of the list's 30 resolved positions, and a builder whose
list reaches that bound can admit a later download entry
[02 R-CAT-01 §5] [02 R-CAT-01 §8]. The compiled lists keep their positions.
Rebuilding them over the filtered table, for both filters, is a follow-up
(§15.12).

### 15.5 Battle entry, saves, replays and reports

- **Entry.** `session.SkirmishEntryOptions.Restrictions` (a
  `content.Restrictions`) travels beside `Mutators` and is fixed for the
  battle; a Survival battle is a skirmish entry and takes the same field.
  `prepareSkirmishEntry` calls `applyEntryRestrictions` after the compile and
  before `prepareCommunityWeapons` and `applyEntryMutators`. A zero set
  returns the catalog itself; any other set clones, checks and applies, and an
  issue is an entry error, because the host has already resolved its
  preference (§15.3). The prepared catalog is what the frozen inputs hold, so
  composition and admission see the restricted table; the frozen selection
  records the set beside the mutators (`SimulationInputs.Restrictions()`). The
  manifest needs no entry of its own — the restricted records carry the
  effect — so an unrestricted battle's manifest digest is unchanged.
  `Session.Restrictions` records the set, as `Session.Mutators` does.
  `MissionEntryOptions` has no such field.
- **Restore.** `session.RetailLoadDeps.Restrictions` applies a skirmish
  save's recorded set to the restore clone where mission restore applies
  `UseOnlyUnits` — before the terrain, the pool and every unit, and before the
  mutators — so the restored definition index space is the one the bank was
  written against [08 R-ENTRY-01 §8]. A sidecar that pairs a set with a
  campaign bank is refused.
- **Sidecar.** `save.Sidecar.Restrictions` (`map[string]int`,
  `"restrictions": {"armkrog": 0, "armpw": 20}`), written from
  `Session.Restrictions.Map()` by `session.SaveSidecar` when the battle has a
  set. A sidecar carrying it is written as schema 2 (proposal R-P7): a build
  that reads only schema 1 refuses it (§7.2), where it would otherwise load
  the bank without the restrictions and misread its definition indices. A
  sidecar without restrictions stays schema 1, so every other save remains
  readable by older builds. This build reads both; a schema-1 sidecar with
  `restrictions`, or a schema-2 sidecar without a non-empty one, is malformed.
- **Load.** §7.3 step 6 re-checks the recorded set against the restore's base
  catalog (§15.3: an issue refuses the load), applies it before the
  mutators, and makes it the running content's restriction setting, as P6
  does for mutators, so the settings, a restart and the next battle match the
  game that was loaded (proposal R-P6). A load without a sidecar restores no
  restrictions (§7.3 step 1). The catalog-hash comparison of step 7 then
  covers the restricted identity.
- **Replays.** Nothing in a battle changes the set, so no command kind
  carries it. The single-player recording header (DESIGN_MULTIPLAYER §10,
  milestone M4, not built yet) records it beside the mutators, as field-12
  records (§15.3), and playback composes with it; a recording without it
  would replay another battle.
- **Reports.** The headless report of both commands (read also by the
  simulation-cost benchmark), the battle benchmark's scene metadata and the
  debug-capture metadata gain `restrictions`: the canonical `String()`, empty
  for none, beside `mutators` (§6.6).
- **Flags.** `--restrict <unit>=<count>` (repeatable, count 0 to 100) on
  `nanolathe` and `nanolathe-headless`, and `--restrict none` for an
  explicitly empty set (proposal R-P11). Any `--restrict` replaces the saved
  set for that run. A key given twice, `none` beside an entry, and
  `--restrict` beside `--mission` or `--load-save` (a save brings its own set)
  stop the command; names are checked once the content is compiled (§15.3).
- **Reproduction.** The desktop command's `--shot`, `--film`,
  `--battle-benchmark` and `--headless`, and the whole displayless command,
  never read the settings key, so fingerprint, capture and benchmark runs
  reproduce from their command line, exactly as for the mutators (§6.6).

### 15.6 Survival

Everyone obeys (D18). The wave pool is built from the restricted catalog, so
a removed unit is never planned, and neither is a unit reachable only
through a removed builder, because the tier walk resolves every product
through the catalog (DESIGN_SURVIVAL §5). An authored roster has already
lost its removed entries (§15.4 step 1). Under a restriction set the pool,
walked or authored, must still hold an ordinary tier-1 attacker — the
opening rule DESIGN_SURVIVAL §5.1 already applies to rosters — and when it
does not, or the pool is empty, Survival entry is refused with the existing
diagnostics, which then name the restriction set; an unrestricted battle's
checks are unchanged. The start site and the
extra deposits are measured with the first factory and the first extractor
on the commander's menu that remain (DESIGN_SURVIVAL §4.2, §4.5).

A capped unit is planned as before, from the same draws. At creation the
allocator counts the attacker's own records of it and refuses once the cap
is reached, and the director already drops a refused pick
(DESIGN_SURVIVAL §6.5): the pick uses one of that tick's creations, draws
nothing, is not retried and does not hold up the rest of the wave, so the
director neither stalls nor spins and needs no change in any mode
(proposal R-P9). A wave whose picks were refused is smaller than its budget,
as when the attacker reaches its unit limit, and still scores the budget it
was planned with; best scores, when they are kept, include the restriction
set in their key (DESIGN_SURVIVAL §8). Infection's takeover is an ownership
transfer through the allocator, so a cap refuses it and leaves the victim
with its owner, as at the unit limit (DESIGN_SURVIVAL §6.7).

The survivors share sight, radar and income, but each owns its own slice of
the unit pool [05 R-SHARE-01 §7], so each meets a cap on its own records; a
teammate's units never count against it.

### 15.7 Computer players

- **Classic.** Unchanged in every mode (D19). The retail planner reads
  neither the restriction set nor the limit field [08 R-AI-01 §12]
  [05 R-SHARE-01 §10]. A removed type is absent from the class vectors it
  compiles over the restricted catalog; a capped type it chooses is refused
  at the allocator like any other exhaustion, its factory showing the caption
  and retrying 300 ticks later [05 R-SHARE-01 §8].
- **Modern.** The Modern AI does not choose a unit once its own records have
  reached that unit's cap; the policy, the state it reads and its tests are
  [Modern AI restriction caps](DESIGN_SESSIONS_AI_SAVE.md#modern-ai-restriction-caps).
  A removed type is absent from its table, which is built over the battle
  catalog.
- **Whose records.** A cap counts the records in the creating player's own
  slice [05 R-SHARE-01 §8], so a computer player's "side" is the player
  itself, in skirmish and in Survival alike (proposal R-P10).

### 15.8 In battle

- **Removed products.** A product slot naming a removed definition is greyed,
  and a greyed button swallows its click, so no build request forms: on
  authored and generated pages through retail's product-name resolution pass
  [07 R-HUD-03 §6], and in the Modern expanded sidebar through its existing
  rule that a cell whose name resolves to no definition greys, without
  repaginating or regrouping
  ([DESIGN_INTERFACE_HUD_INPUT](DESIGN_INTERFACE_HUD_INPUT.md) "Modern
  expanded sidebar"). The unit cannot be spawned by name, and the developer
  spawn command's patterns no longer match it.
- **Capped products.** An ordinary button: placing and queueing work as for
  any product, and the refusal is the allocator's, with the retail caption
  `Unable to create any more units` and the failure table's wait or abandon
  [05 R-SHARE-01 §8].
- **Optional.** Drawing the remaining allowance on a capped product's button
  (for example `3/5`) would be a Nanolathe presentation preference read from
  the committed frame, off by default and never a gameplay input. It is not
  part of this delivery.

### 15.9 Editing and storage

**Storage.** The settings key `restrictions` holds the canonical map,
`{"armkrog": 0, "armpw": 20}`, kept verbatim by `internal/settings` and
parsed by `content.ParseRestrictions`, as `mutators` is. Unlike the
mutators, which are the player's alone (§4.6), a restriction names one
content set's units, so `restrictions` is a mod-scoped path: the base block
holds the original game's set and `modSettings.<mod id>.restrictions` the
player's set for that mod, layered as §4.6 describes. It is atomic, like
`keyBindings`: a mod's layer that states a set replaces the base set whole
rather than merging with it, and a mod with no set of its own plays the base
set, whose names it lacks are left out with the notice of §15.3
(proposal R-P5). A mod's config cannot recommend restrictions: `restrictions`
joins the match-selection keys its `settings` section refuses (§4.2), and
its `locks` may not name it. A
manual root stack and the original game play the base set.

**The Nanolathe screen card.** The Mutators tab of the Nanolathe screen
([DESIGN_INTERFACE_HUD_INPUT §3.17](DESIGN_INTERFACE_HUD_INPUT.md#317-the-nanolathe-screen))
leads with a *Unit restrictions* card, before the mutator cards. Its control is a
summary, not the editor, so it never grows with the catalogue: *No
restrictions*, or the numbers removed and capped and up to four names with
their states, then *and N more*; *Edit…* opens the unit viewer's editor over
the screen; *Clear* empties the draft. Its description says that
restrictions apply to skirmish and Survival under every rule set, Strict 3.1
included, that every player obeys them, computer players and Survival waves
included, and that campaign missions keep their own unit lists. Its chips name
saved entries the running content leaves out (§15.3). It has no comparison
scene: a restriction changes which units exist, not how a scene looks. The
set is part of the
screen's draft: *Apply* writes it to the running content's layer and *Back*
discards it. The line under the wordmark names the restriction count beside
the mutators.

**The unit viewer's editor.** The unit viewer
([DESIGN_DEVELOPER_TOOLS §7](DESIGN_DEVELOPER_TOOLS.md#7-3d-unit-viewer-preview))
gains a restriction editor, which this section owns. The viewer stays
presentation only: it edits a `content.Restrictions` draft beside its own
unrestricted preview catalog, and nothing it does creates a battle, compiles
a battle catalog or reaches the simulation.

- *Rows.* Each library row shows its name's state: nothing for *No limit*,
  the count for a cap, *Removed* and a dimmed row for 0, and a padlock with
  its reason for a name that cannot be restricted (authored `norestrict`) or
  cannot be removed (a side's commander, which still takes a cap). Every
  record carrying a name shows that name's state, and a record hidden by a
  duplicate name says the entry covers every record so named.
- *Selected unit.* An *On/Off* switch, *On* being *No limit* and *Off* being 0,
  and a count stepper over retail's slider range, 0…100 with *No limit* above
  100 [08 R-SKIR-01 §10]: stepping down from *No limit* gives 100, and up from
  100 gives *No limit*. Unlike the slider, stepping up from *No limit* starts a
  cap at 1 (user decision 2026-10-06), so a cap of five is five presses rather
  than a count down from 100, and the plus button cycles. A `norestrict`
  unit's controls are disabled, and a commander's *Off* and 0, each saying
  why.
- *Restricted only* narrows the library to names with an entry, together with
  the search.
- *Reset* empties the set: every unit returns to *No limit*, `wacky` names
  and entries the running content leaves out included (user decision
  2026-10-06). Retail's *Reset* instead gives every row 100, and 0 to a
  `wacky` definition [08 R-SKIR-01 §10], a board that reads as a cap on every
  unit; the editor does not copy it.
- A footer counts the names removed and capped.
- *Entry and exit* (proposal R-P8). Opened from the card, the editor edits the
  Nanolathe screen's draft, and *Back* returns to the screen with it. Opened
  from the main menu with Ctrl+U, the viewer shows *Apply*, with the number of
  changed names, beside *Back* once the draft differs: *Apply* writes the
  running content's layer and *Back* discards. The card is a visible route to
  the editor; the viewer itself stays unlisted.

**Other surfaces.** The main-menu chip's status line adds the restriction
count (§8.1); the loading screen adds `Restrictions: 3 removed, 2 capped`
under the mutators, and its warning line names entries left out (§8.3); the
load dialog's sidecar line adds the count (§8.4). These are Nanolathe
divergences, like the lines they join.

### 15.10 Proposals (approved 2026-10-05)

D17–D19 settle the policy. These are the choices this design made beyond
them; each is implementable as written and none moves the retail baseline.
The user approved all twelve as written on 2026-10-05.

| ID | Proposal | Section |
|---|---|---|
| R-P1 | A restriction names a unit name and applies to every retained record of that name, not only to the record the name lookup returns, so a removal never exposes a hidden duplicate under the same name | §15.3 |
| R-P2 | Removal keeps the survivors' compiled order and renumbers them; the order of same-name survivors stays a `TODO(question)` | §15.4 |
| R-P3 | A side's commander can be capped but not removed, because skirmish entry needs one per player | §15.3 |
| R-P4 | An entry the content cannot take stops a `--restrict` command, is left out with a notice from the settings, refuses a save's load, and rejects a field-12 configuration | §15.3 |
| R-P5 | `restrictions` is a mod-scoped, atomic settings path (per mod, inherited from the base when a mod has none); a mod's config cannot recommend restrictions | §15.9 |
| R-P6 | Loading a save makes its recorded restrictions the running content's setting, as P6 does for mutators | §15.5 |
| R-P7 | A sidecar with restrictions is written as schema 2, every other sidecar stays schema 1 | §15.5 |
| R-P8 | The card sits on the Mutators tab and opens the editor; the viewer opened with Ctrl+U gains *Apply* | §15.9 |
| R-P9 | Survival plans a capped unit as before and drops a refused pick, with no re-pick; the wave keeps its planned budget for scoring; a unit reachable only through a removed builder leaves the pool; a roster loses its removed entries | §15.6 |
| R-P10 | The Modern AI's cap is its own slice's count, plus its own requests that have no record yet; the executor drops a command whose product's cap was reached in the reaction window | §15.7, [DESIGN_SESSIONS_AI_SAVE](DESIGN_SESSIONS_AI_SAVE.md#modern-ai-restriction-caps) |
| R-P11 | `--restrict none` selects an explicitly empty set | §15.5 |
| R-P12 | Retail `.LST` import and export are a later item | §15.3 |

### 15.11 Verification

Contract-level and light, as the test policy asks; every relationship is
checked under Strict 3.1 and under Modern unless the row says otherwise.

| What | Where |
|---|---|
| Parsing: canonical keys only, counts 0..100, flag spellings, a key given twice, `String`/`Map`/`ParseRestrictions` round trip, `Digest` independent of input order | `content` |
| An empty set: `applyEntryRestrictions` returns the catalog itself; a clone given the empty set to `ApplyRestrictions` is deep-equal with an equal `Hash`, on a fixture with a `wacky` definition | `content`, `session` |
| Removal: absent from `Units` and `UnitRecords`, survivors renumbered in order with masks and per-definition hashes rebuilt; every record of a duplicated name removed; the roster loses the entry; the source catalog untouched | `content` |
| Caps set `Limit`/`LimitEnabled` and leave every per-definition `Hash` alone; `Catalog.Hash` follows `catalog+restrictions` over the base and the set | `content` |
| `CheckRestrictions` refuses unknown, `norestrict` (any record of the name) and commander removal, accepts a commander cap; `ApplyRestrictions` writes nothing on an issue | `content` |
| Under a cap of N, the allocator refuses the (N+1)th record of a player's slice with nanoframes counted, accepts again after a teardown, and another player's records do not count; the factory shows the retail caption and retries after 300 ticks (relationships, Strict and Modern) | `session` |
| A removed product cannot be built or spawned by name; a builder's page with a removed product shows the slot greyed | `session`; a reviewed `--shot` capture (unit 4) |
| Restrictions then mutators: the restricted-and-mutated hash is `mutators(restrictions(compiled))` at fresh entry (unit 1), and a restore gives the same catalog (unit 2) | `session` |
| No fingerprint lock moves; the reserved sets' headless reports match the pre-change bytes apart from the new empty `restrictions` field | `headless`, retail tier |
| Survival: a removed unit never enters the pool or a plan; a capped attacker pick is refused, dropped, not retried and draws nothing; the wave plan's draws are unchanged by a cap; a roster that loses its only tier-1 opener refuses entry naming the restrictions | `survival`, `session` |
| Classic computer player against a capped product: refused, retried after 300 ticks, no other change | `session` (retail tier) |
| Modern AI: a candidate at its cap is not chosen; its pending requests count; a stale capped command is dropped by the executor; synchronous and asynchronous hosts agree | `aikit` (see [Modern AI restriction caps](DESIGN_SESSIONS_AI_SAVE.md#modern-ai-restriction-caps)) |
| Sidecar: schema 2 with restrictions and schema 1 without; a save and load restores the restricted index space and selects the set; an unknown recorded unit refuses the load | `save`, `main` (retail tier) |
| Settings: the per-mod layer replaces the base set whole; a name the content lacks is left out with the notice; a mod config naming `restrictions` is refused | `settings`, `modlibrary`, `main` |
| Flags: `--restrict` replaces the saved set, `none`, the refusals of §15.5; capture and benchmark modes never read the key | `main`, `nanolathe-headless` |
| Editor and card: row states, stepper ends and the start at 1 from *No limit*, Reset emptying the set, *Restricted only*, draft and *Apply*/*Back* from both entries; captures of the card and the editor reviewed | `main`, `--shot` |

### 15.12 Work units

Ordered so that each lands green on its own; none moves a fingerprint lock.
Units 2 and 3 can run in parallel after unit 1; unit 4 follows unit 2.

1. **Core, entry and reports.** Owns `internal/content/restrictions.go` and
   its test (new), `internal/content/catalog.go` (factoring the rebuild
   `RestrictToCreatable` performs into a helper both filters share, with no
   change to `RestrictToCreatable`'s behaviour), `internal/content/sim_inputs.go`,
   `internal/session/restrictions.go` (new), `internal/session/skirmish.go`,
   `internal/session/session.go`, `internal/headless/runner.go` and
   `report.go`, `cmd/nanolathe-headless/main.go`, and in `cmd/nanolathe`
   `flags.go`, `battle_composition.go`, `headless.go` and
   `battle_benchmark.go`. Delivers §15.2–§15.4, the entry half of §15.5, the
   flags and the report fields. *Done when* the content and session rows of
   §15.11 pass, `tools/check ./internal/content ./internal/session
   ./internal/headless ./cmd/nanolathe ./cmd/nanolathe-headless` is green and
   the fingerprint locks hold.
2. **Settings, sidecar and restore.** Owns `internal/settings/settings.go`
   and `layers.go`, `internal/modlibrary/config.go`, `internal/save/sidecar.go`,
   `internal/session/sidecar.go` and `retail_stage.go`, and in
   `cmd/nanolathe` `settings.go`, `mods.go` (saved-set resolution, notices,
   the chip) and `save_sidecar.go` (load, selection, the load-dialog line).
   Delivers §15.9's storage, the rest of §15.5 and §15.3's per-source
   handling; the replay header is M4's to build (§15.5). *Done when* the
   sidecar, settings and load rows of §15.11 pass.
3. **Survival and the Modern AI.** Owns `internal/session/survival.go`; the
   allocator's per-definition census factored into one read-only function
   that the allocator gate and a new `units.World` accessor share, in
   `internal/pool/pool.go` and `internal/units/units.go`; and in
   `internal/aikit` `info.go`, `obs.go`,
   `host.go`, `cmd.go` and the candidate sites of `brains/utility` and
   `brains/survival`. Delivers §15.6 and the policy of
   [Modern AI restriction caps](DESIGN_SESSIONS_AI_SAVE.md#modern-ai-restriction-caps).
   *Done when* the Survival, Classic and Modern AI rows of §15.11 pass and
   the arena's synchronous and asynchronous runs agree.
4. **Editor, card and lines.** Owns `cmd/nanolathe/unit_viewer*.go`,
   `nlscreen_pages.go`, `nlscreen.go`, `nlscreen_presets.go` (presets leave
   `restrictions` out) and `loading.go`, and the pointer
   paragraphs in DESIGN_INTERFACE_HUD_INPUT §3.17 and DESIGN_DEVELOPER_TOOLS
   §7. Delivers the card, the editor and the loading-screen lines of §15.9.
   *Done when* the editor row of §15.11 passes and captures of the card and
   the editor at 1440×900 and 1024×640 are reviewed.

**Follow-ups**, not in this delivery: retail `.LST` import and export
(R-P12); rebuilding `CANBUILD` lists over a filtered table for both filters
(§15.4); the equal-name `TODO(question)` (§15.4); the optional allowance on
capped buttons (§15.8); and a restriction-editing lobby screen with retail's
seeded-but-unclosed state (DESIGN_MULTIPLAYER §15 Q16). Online, the lobby
carries the host's set as field 12 (DESIGN_MULTIPLAYER §16.6).
