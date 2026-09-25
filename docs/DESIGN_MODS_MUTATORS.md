# Design — Mods, mutators and match identity

How a player installs, selects and switches content mods such as ProTA,
TA Zero and TA: Escalation without command-line flags; how global
**mutators** (build speed, cost, health, damage, blast size, sight and radar
multipliers)
are selected and locked for a battle; and how a Nanolathe save records all of
it so that loading the save restores the same match.

**Status: implemented (2026-09-24)** apart from the follow-ups in §13 unit 9.
The mutators, the mod library with drop-to-install, the remote catalogue
client and the hosted catalogue, the in-process reload, the screens, the save
sidecar and mod switching on load are in place (§13 units 1–8), and the
displayless command mounts an installed mod with `--mod`. A load that needs a
mod which is not installed is refused with a message naming it and saying
whether *Get more mods* offers it; the load does not start the download
itself (§7.3 step 2). The maintainer's decisions of 2026-09-23 are in §2. The
proposals this document made were confirmed the same day and are listed in
§12.

This document owns the mod library, the remote catalogue, the mutator
transform and the save sidecar. It builds on mechanisms owned elsewhere and
restates none of them: the overlay and content profiles
([DESIGN_CONTENT_VFS §5](DESIGN_CONTENT_VFS.md#5-divergences) "Content
profiles"), the gameplay rule sets and their registry
([DESIGN_GAMEPLAY_RULES](DESIGN_GAMEPLAY_RULES.md)), the Community feature
table ([DESIGN_COMMUNITY_PATCH §3](DESIGN_COMMUNITY_PATCH.md#3-the-feature-table))
and the retail save bank
([DESIGN_SESSIONS_AI_SAVE §2.5](DESIGN_SESSIONS_AI_SAVE.md#25-internalsave--the-retail-bank)).

## 1. Purpose and boundary

Three features share one idea — the **match selection** (§3), the complete
set of choices that decides what a battle is:

1. **The mod library.** Mods live in a Nanolathe data directory, one extracted
   content root per mod version. Selecting one mounts it as the last content
   root, exactly as a second `--root` does today, and selects its content
   profile explicitly. A catalogue hosted on nanolathe.gg lists downloadable
   mods; manual installs remain possible.
2. **Mutators.** A closed set of global multipliers applied to the per-battle
   catalog clone at battle entry and fixed for that battle.
3. **Match identity.** A Nanolathe sidecar file beside every save records the
   match selection. Loading the save restores it, switching mod if needed,
   and the loading screen shows it.

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
| D5 | The client may use the network to fetch the mod manifest and mod downloads, and for nothing else. |
| D6 | One zip per mod version, containing the complete content root. No layers. |
| D7 | Mutators are global: every player, human and computer, plays the same catalog. |
| D8 | Gameplay mode and renderer stay orthogonal to the mod. A mod may declare a minimum gameplay mode, which the selector enforces visibly; nothing switches silently. |
| D9 | Downloads are extracted to disk and mounted as ordinary directory roots. There is no zip VFS provider. |
| D10 | Applying a different mod reloads the content in process, from the menu, with no restart (§4.4). |
| D11 | The main-menu entry is a Nanolathe-owned chip in a fixed position, not a gadget positioned against authored menu art (§8.1). |
| D12 | A selected mod passes its content profile explicitly, so a saved `contentProfile` preference cannot apply one mod's directory table to another. |
| D13 | A mod may recommend a controls preset (the Community host options). It is offered when switching, never forced over the player's own choices (§4.3). |
| D14 | The Mods & Mutators screen shows the installed mods and the mutators side by side. Mods that can be downloaded appear only in a separate *Get more mods* dialog (§8.2). |

D9–D13 were proposed in review and accepted with the rest of the design
direction. D14 is the maintainer's layout for the screen. D13's offer was
extended on 2026-09-24: a mod that starts by any other route is offered its
preset once on the main menu, and the preset became the mod's full recommended
settings (§4.3).

## 3. The match selection

| Part | Chosen by | Fixed at | Recorded in |
|---|---|---|---|
| Mod (id, version, archive SHA-256) | Mods & Mutators screen, `--mod`, settings `mod` | process start (mount) | sidecar |
| Content profile | the mod's metadata; with no mod, today's precedence | mount | sidecar, existing reports |
| Gameplay rule set (name and base) | options control, `--gameplay`, settings `gameplay` | battle entry, and the phase-1 command boundary thereafter ([DESIGN_GAMEPLAY_RULES §5](DESIGN_GAMEPLAY_RULES.md#5-switch-timing)) | sidecar records the set bound when saving |
| Community sources and entry table | content profile, settings, `--gameplay-feature` ([DESIGN_COMMUNITY_PATCH §3.2](DESIGN_COMMUNITY_PATCH.md#32-sources-and-precedence)) | battle entry | sidecar |
| Unit limit | settings `unitLimit`, `--unit-limit` | battle entry | sidecar |
| Mutators | Mods & Mutators screen, `--mutator`, settings `mutators` | battle entry, for the whole battle | sidecar, catalog hash, reports |

Renderer, audio, display and other host preferences are not part of the
selection and are never recorded.

## 4. The mod library

### 4.1 On disk

Mods live under the XDG data directory, following the settings file's rule of
one documented layout on every platform:

```
$XDG_DATA_HOME/nanolathe/mods/      default ~/.local/share/nanolathe/mods
  <id>/<version>/                   one extracted content root, mounted as a root
    nanolathe-mod.json              the mod's metadata (§4.2)
    install.json                    receipt: archive SHA-256, size, source URL, time
  manifest.json                     last fetched catalogue, for offline display (§5.2)
  .downloads/                       <id>-<version>.zip.part: partial downloads, kept so a later attempt resumes
  .staging/                         extractions and removals in progress
```

`.staging/` holds only work that ends with its process. The first opening of
the library in each process clears it, never while an install is extracting
into it; later openings leave it alone.

The directory is the index. An installed mod is a `<id>/<version>/` directory
whose metadata parses and whose receipt exists; there is no separate index
file to drift out of step. Installing a new version leaves older versions in
place until the player removes them, because saves name a version (§7).

### 4.2 Mod metadata

`nanolathe-mod.json` sits at the root of every hosted zip and every installed
mod:

```json
{
  "schema": 1,
  "id": "prota",
  "name": "ProTA",
  "version": "4.8",
  "summary": "One line for the mod lists.",
  "homepage": "https://…",
  "contentProfile": "prota",
  "minimumGameplay": "community-3.9",
  "controls": "community",
  "requires": ["<logical path the base install must supply>"]
}
```

- `id` is lowercase `[a-z0-9-]`, stable across versions. `version` is opaque
  and compared for equality only; the manifest's order is the display order.
- `contentProfile` names a shipped profile or a profile JSON path relative to
  the mod root. Omitted means detection, exactly as today.
- `minimumGameplay` is a reserved word; omitted means none.
- `controls` names a controls preset (§4.3); omitted means none.
- A mod that omits `minimumGameplay` or `controls` takes the value its
  resolved content profile names, if any: shipped and user profiles carry the
  same two optional keys (`internal/content/profiles`; the shipped `prota`
  profile names `community` and `community-3.9`). The metadata always wins.
  This is what gives a metadata-less local package (§4.5) its content set's
  recommendations.
- `requires` lists logical paths that the **base** install must resolve, for
  example a map from an expansion the mod overlays. A mod whose requirements
  fail is listed but cannot be selected, and the screen names the missing
  paths.

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
  valid.
- **Content profile.** A selected mod's `contentProfile` is passed as the
  explicit selector, so it wins over a saved `contentProfile` preference
  (D12). A mod that names none, such as a local package (§4.5), is detected;
  the saved preference never applies to a mod. With no mod, today's
  precedence (flag, saved preference, detection) is unchanged.
- **Gameplay minimum.** While a mod with `minimumGameplay` (its own or its
  content profile's, §4.2) is selected, the
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
  `retail` writes each row's retail default. The rows are defined once, in
  `controlsPresetRows` (`cmd/nanolathe/controls_preset.go`):

  | Setting | Where the player changes it | `community` | `retail` |
  |---|---|---|---|
  | `presentation.communitySelection` (idle unit keys) | Options → Orders | 1 | 0 |
  | `presentation.doubleClickSelection` | Options → Orders | 1 | 0 |
  | `presentation.queuedOrderDrag` | Options → Placement | 1 | 0 |
  | `switchAlt` (digits recall groups) | Options → Orders, `+switchalt` | 1 | 0 |
  | `presentation.communityCounters` | Options → HUD | 1 | 0 |
  | `presentation.reloadBars` | Options → HUD | 1 | 0 |
  | `presentation.veteranLabels` | Options → HUD | 1 | 0 |
  | `presentation.groupNumbers` | Options → HUD | 1 | 0 |
  | `presentation.weatherReport` (wind and tide readout) | Options → HUD | 1 | 0 |
  | `presentation.overview` | settings file | 1 (Megamap) | 0 (Zoom) |
  | `presentation.megamapWheel`, `megamapWheelMove`, `megamapFlash` | settings file | 1 each | unchanged |
  | `presentation.megamapDoubleClickMove` | settings file | 0 | unchanged |
  | `presentation.megamapRadarMinimum`, `megamapSonarMinimum`, `megamapSonarJamMinimum`, `megamapAntiNukeMinimum` (one row) | settings file | 0 each | unchanged |
  | `presentation.playerDotColors` (one row, *Dot colours*) | settings file | ProTA: 227, 249, 18, 250, 67, 149, 208, 117, 210, 34 | Default: 227, 212, 80, 235, 108, 219, 208, 93, 130, 67 |
  | `presentation.victoryCue` | Options → HUD | 1 | 0 |
  | `clock` (stand-alone battle clock) | `+clock` | 1 | 0 |
  | `audio.soundMode` | Options → Sound | 2 (3D) | 1 (Mono) |
  | `audio.mixingBuffers` | settings file | 128 | 8 |
  | `audio.cdMode` | Options → Music | 2 (Random) | 4 (Custom) |
  | `skirmish.numPlayers` (skirmish rows shown, `NumSkirmishPlayers`) | the `*III`…`*X` selector | 10 | unchanged |

  3D sound is the positional placement the audio device already implements
  (DESIGN_PRESENTATION_CLIENT §2.6). 128 voices exceeds the mixer's 32 tracked
  slots, so no sound is cut off for the voice limit, which is ProTA's
  "unlimited" [03 R-AUD-01 §1]. Ten skirmish rows only shows more rows: each
  row keeps its controller, so a skirmish starts with the same players, and
  the retail preset never removes rows. The megamap rows are the optional
  overview of [DESIGN_INTERFACE_HUD_INPUT §3.15](DESIGN_INTERFACE_HUD_INPUT.md#315-optional-megamap);
  the retail preset returns the overview to Zoom and leaves the megamap's own
  preferences as the player set them. The four ring minimums are one row
  because ProTA sets them alike, and the ten dot colours are one row naming
  the draw engine's defaults or ProTA's table; a table matching neither, or
  unequal minimums, read *Custom* in the offer. The dot colours are read by
  the megamap's icons only: the draw engine's table is not read by the
  retail minimap's contacts, so ProTA's minimap dots come from its content.
  The victory cue is
  [DESIGN_INTERFACE_HUD_INPUT §3.16](DESIGN_INTERFACE_HUD_INPUT.md#316-optional-victory-cue).
  The unit limit is not in the preset, because the Community feature table
  already sets it (§8.3).

  A preset is **offered, never forced**, and each mod's offer is made once.
  When the player switches to a mod that names a preset (or whose content
  profile does), the Mods & Mutators screen shows a *Yes / No* toggle
  captioned *Use recommended settings*, *Yes* by default (P10); *Apply*
  writes the rows once when it is *Yes*. A mod that starts by any other route
  — `--mod`, the saved choice, a save that switches mod (§7.3), a local
  install selected later — is offered its preset once on the main menu, in a
  *Recommended settings* window built like the Mods & Mutators screen: it
  lists every row with its new value and, where different, the player's
  current one, and offers *Apply* and *Keep mine*. The offer is never shown
  during a battle, by a capture or benchmark, or where the settings file
  cannot be written. A content profile mounted without a mod
  (`--root … --content-profile prota`, or detection) is offered its profile's
  preset the same way. Either answer, and either way of offering, records the
  mod id — `profile:<name>` for a profile without a mod — in the settings
  key `controlsOffered`, so it is not asked again. Later changes by the
  player stick, and switching back does not restore anything automatically.
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

1. mounts the base install plus the chosen mod, with the mod's own content
   profile (D12);
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
as that mod.
Without metadata, it installs as `local-<sanitized name>` with version
`local` and a detected content profile, and the Mods & Mutators screen marks
it *Local*. Its gameplay minimum and controls preset are its detected
profile's, if the profile names them (§4.2): a ProTA package dropped without
metadata gets ProTA's. Copying a prepared directory into the
data directory by hand also works; it needs a metadata file and a receipt,
which the screen can write with *Adopt*.

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
        "size": 12440085,
        "sha256": "…"
      },
      "contentProfile": "prota",
      "minimumGameplay": "community-3.9",
      "controls": "community",
      "requires": []
    }
  ]
}
```

An archive URL is either on the manifest's own origin (a relative URL
resolves against the manifest) or a release asset of a `nanolathe-gg`
repository, `https://github.com/nanolathe-gg/<repository>/releases/download/<tag>/<file>`.
The website repository keeps one release, `mods`, whose assets are the hosted
zips, one per mod version, named `<id>-<version>.zip`; its README describes
packaging and upload.

The zip's own `nanolathe-mod.json` must agree with its manifest entry on `id`,
`version`, `contentProfile`, `minimumGameplay` and `controls`; a disagreement
refuses the install. A minimum engine version is deliberately absent: the
build carries no release version today (`internal/version` names a save
profile, not a release). Add `minimumEngine` once releases are stamped.

### 5.2 When the client talks to the network

- Only when the player opens the *Get more mods* dialog (§8.2) or starts a
  download. Never at start-up, never in battle, never in the displayless
  command (D5).
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

1. Download to `.downloads/<id>-<version>.zip.part`, resuming with an HTTP
   range request when the server supports it. The screen shows progress and
   can cancel. A transfer that stops part-way, cancelled or stalled, keeps
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
4. Validate: parse the metadata and check it against the manifest; mount the
   base install plus the staged root in a scratch `vfs.FS`, resolve the
   content profile, and require the products `openContent` requires. A mod
   that would not start is never installed.
5. Write `install.json`, then rename the staged directory to
   `<id>/<version>/`. The rename is on one filesystem, so an interrupted
   install leaves nothing half-installed. Delete the zip.

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
- `nanolathe-mod.json` is at the root.
- No executables, DLLs, installers, `ddraw` wrappers, launcher INIs or
  base-game archives.
- A new version is a new file. Old versions stay hosted, because saves name
  them.
- **Merging packages is not a file copy.** A mod published as several packages
  (TA Zero is Base plus Alpha 5) is merged into one root, but precedence
  differs between the two shapes. As separate roots, the later root wins. In
  one root, archives of the same tier are ordered lexically
  ([DESIGN_CONTENT_VFS §5](DESIGN_CONTENT_VFS.md#5-divergences) SC3), which
  can invert the winner. A merged zip is accepted only when a check tool
  mounts the original root list and the merged root and finds identical
  winners (`vfs.Manifest`) and an identical compiled catalog hash (§13 unit 8).

## 6. Mutators

### 6.1 Policy

A mutator is a deterministic transform of compiled definitions. It is applied
to the per-battle catalog clone before a session exists. The result is a
catalog that a content package could have authored with those values, and
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
  ([04 §4.4], [06 §9.2]); 65,535 for weapon `areaofeffect`; 2³¹−1 for
  `buildtime`. The maximum is the narrowest
  store the value reaches, not only its FBI or TDF store.
  Unit costs are multiplied as integers and then stored as `float32`, which is
  exact below 2²⁴.

Saturating rather than wrapping is deliberate. Retail wraps an out-of-range
*authored* integer when it stores it, and content that authors one keeps the
wrapped value, because the mutator starts from the stored value. A mutator
never introduces a wrap.

**Identity tag.** The mutated catalog's identity hashes the tag
`mutators/2`, the base hash and the canonical set. The tag changes whenever
any mutator's transform changes meaning (it moved to 2 when Build speed
switched to `buildtime`).

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
| Sight | unit `SightDistance` | k× sight | Both sight lookups clamp at their table's last entry, so large factors saturate for long-sighted units ([03 §3.2]). The ranges that read `sightdistance` widen with it: the fire-at-will opportunity scan and hold-position leash ([04 R-STANCE-01 §3], [04 R-STANCE-01 §4]) and the patrol and VTOL work scans ([04 R-ORD-01 §4], [04 R-ORD-01 §7]). |
| Radar | unit `RadarDistance`, `SonarDistance`, `RadarDistanceJam`, `SonarDistanceJam` by the same factor | k× radar and sonar | All four are plain search radii, so detection and jamming stay in proportion ([03 R-VIS-01 §5]). The emitter's height bonus is not scaled ([03 R-VIS-01 §4]); `mincloakdistance` is left alone. |

**Known hazards.** The unit-reclaim pulse forms a 32-bit product of
`workertime`, a kill factor, `maxdamage` and 15, which retail lets overflow
([05 R-WORK-01 §4]). Build speed no longer reaches it; Health at ×4 multiplies
it by up to 4, but the 32,767 cap limits that: ARMCOM reclaiming CORKROG
overflows from 70 kills instead of 75. The abandoned-frame decay forms the 32-bit product `11·buildtime`
([05 R-WORK-01 §9]); at Build speed ×0.25 the largest stock `buildtime` gives
164,673,432, well inside the range. An interceptor's catch test squares its
unhalved area as a signed 32-bit product, which wraps from 46,341
([06 R-WPN-05 §10]); the stock interceptors author 96, 384 at ×4. Mutators do not guard downstream
products, because the retail arithmetic stays retail.

**Derived values.** `ApplyMutators` must recompute any compile-time value
derived from a field it changes. A search of the unit compiler found these
fields assigned and not otherwise derived from. The implementing unit confirms
the same for AI profiles, build menus, the weapon linker and the LOS tables,
and lists anything it finds here.

### 6.6 Identity and reports

- The clone's `Catalog.Hash` becomes a hash of `mutators/2`, the base hash and
  `Mutators.String()`. With no mutators, nothing is transformed and nothing is
  rehashed, so every existing identity and fingerprint is unchanged.
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
  "mutators": {"buildSpeed": "2", "health": "1.5"}
}
```

`mod` is `null` when no mod was selected, and the string `custom` for a manual
root stack. `unitLimit` is the configured unit-limit word, the one that sizes
a battle. `catalog` and `contentManifest` are the battle catalog's identity
after mutators and the mounted set's manifest hash. A sidecar with another
`schema`, or one that does not parse, refuses the load rather than being read
as absent, which would silently drop the selection it records. `community` records the session's Community sources and its
battle-entry table (`Session.CommunitySources`, `Session.EntryCommunity`), so
a restored battle resolves and switches exactly as the saved one did, whatever
the host's current settings are.

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
   the host's.
5. **Unit limit.** Set the configured limit to the recorded one before
   staging, in every mode. Strict 3.1 still sizes its pool from the pre-load
   configured word ([08 R-SESS-01 §9]); the host has only chosen that word
   first. Retail's post-load carry of `Summary.maxunits` into the configured
   word is kept.
6. **Mutators.** Apply the recorded mutators to the restore clone and make
   them the selected mutators (P6). The settings, the chip, a restart of the
   battle and the next new battle then all match the game that was loaded.
7. **Integrity.** Compare the recorded `catalog` hash with the restored
   catalog's. A mismatch (a different base install, different mod bytes) loads
   with a warning on the battle message line rather than refusing.

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
fixed position on the logical 640×480 surface. It reads, for example,
*ProTA 4.8 · Community 3.9 · 2 mutators*, and opens the Mods & Mutators
screen. Mods repaint the front end, so its position is chosen by `--shot`
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

**The Mutators dialog.** *Change...* opens a modal over the screen built
from the mutator catalogue (`content.MutatorCatalog`), so a new mutator needs
no layout work: a scrolling list with a heading row per group (Economy,
Combat, Vision), each row showing the mutator and its factor; the
selected mutator's description beneath; *Raise*, *Lower*, *Default* and
*Reset all* in the right-hand column; *OK* keeps the changes for the screen's
*Apply*, and *Cancel* restores the set the dialog opened with.

**The Get more mods dialog.** A modal over the screen. Opening it fetches the
manifest (§5.2); when that fails, it shows the cached manifest and its age.
It lists every manifest entry whose id and version are not installed, with
name, version, download size, summary, minimum gameplay and any unmet base
requirement. *Download* on a row starts §5.3, with a progress bar; while the
archive transfers the button reads *Cancel*. One download and install runs at
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
   with: when a Community feature table sets one it overrides the player's
   `unitLimit` (DESIGN_COMMUNITY_PATCH §3.2), and the field then names the
   source, as in *Unit limit 1500 (set by ProTA)*. Strict 3.1 ignores the
   table and shows the setting;
2. `Mutators: Build speed ×2, Health ×1.5`, omitted when there are none;
3. any warning from §7.3.

This is a Nanolathe divergence from the authored screen
([07 "The loading screen"]). A restored battle does not pass through the
loading screen, so §7.3's warnings go to the battle message line instead.

### 8.4 The load dialog

The selected save's summary panel gains one line with the sidecar's mod and
mutators, so the player knows before loading that it will switch. It is a
Nanolathe label (`NLSIDECAR`) beneath the authored `TIME` field; a mod the
library lacks is marked *(not installed)*, and a save without a sidecar
leaves the line empty (DESIGN_INTERFACE_HUD_INPUT §2.6).

## 9. Packages and boundaries

| Package | Owns | Imported by |
|---|---|---|
| `internal/modlibrary` | data directory layout, metadata, installed listing, selection resolution (mod → root, profile, minimum, preset), extraction and validation, receipts. No network. | both commands |
| `internal/modfetch` | manifest fetch and cache, downloads with resume and progress. The only package that imports `net/http`. | `cmd/nanolathe` only |
| `internal/content` | `Factor`, `Mutators`, `ApplyMutators`, the mutated catalog identity | session, commands |
| `internal/save` | the sidecar type and its read/write, separate from the bank's bytes | session, commands |
| `internal/session` | mutators and recorded Community sources in battle-entry and restore requests; building the sidecar value; reports | commands |
| `internal/settings` | the `mod` and `mutators` keys | commands |
| `cmd/nanolathe` | chip, screen, in-process reload, loading-screen lines, drop-to-install, `--mod`, `--mutator` | — |
| `cmd/nanolathe-headless` | `--mutator` and `--mod` (flags only, never the settings file). `--mod` mounts an installed mod from the library as the last root with its content profile, through `modlibrary`'s command-line selection; it is refused beside several `--root` flags, and the command never fetches | — |

**Guards.** New architecture tests: only `internal/modfetch` imports
`net/http`; only `cmd/nanolathe` imports `internal/modfetch`; no simulation
package imports either new package. `ApplyMutators` walks weapon damage
through `DamageKeysSorted`, never by ranging a map ([INVARIANTS](INVARIANTS.md)
I1). Everything used — `archive/zip`, `net/http`, `crypto/sha256` and, later,
`crypto/ed25519` — is standard library, so there are no new module
dependencies.

## 10. Verification

| What | Where |
|---|---|
| Extraction refuses absolute paths, `..`, symlinks and case-folded duplicates; skips executables and `__MACOSX`; enforces the caps; an interrupted install leaves nothing installed | `modlibrary` tests on authored fixture zips |
| Metadata disagreeing with the manifest refuses the install; unmet `requires` block selection | `modlibrary` |
| Precedence: `--mod` over setting, a manual stack disables both, a mod's profile beats a saved `contentProfile`, a command line below the minimum is rejected | `modlibrary`, `cmd/nanolathe` |
| A mod without `controls` or `minimumGameplay` takes its content profile's; metadata wins; only `prota` ships them | `modlibrary.TestLocalPackageTakesItsProfileRecommendations`, `profiles.TestProfileRecommendations` |
| Preset contents, and the retail preset keeping skirmish rows; the offer key and its once-only record | `main.TestCommunityControlsPresetContents`, `main.TestRetailControlsPresetKeepsSkirmishRows`, `main.TestControlsOfferIsRememberedPerMod` |
| A local ProTA package is offered its preset on the Mods & Mutators screen and once on the main menu after `--mod`; the loading line names the effective unit limit | `main.TestProTARecommendedSettingsAreOfferedOnce` (retail tier) |
| SHA-256 and size mismatch, truncated download, resume, off-origin redirect refused, offline cache shown | `modfetch` against `httptest` |
| Zero mutators leave the clone deep-equal with an equal `Hash`; each mutator changes exactly its fields; rounding, minimum-one, saturation and `v ≤ 0` boundaries; the hash is independent of field order | `content` |
| Mutators reach fresh skirmish, mission entry and restore; all six fingerprint locks unchanged | `session`, `headless` |
| Under Strict 3.1 with Build speed ×2, a fixed construction finishes in half the ticks and bills the same total resources (a relationship, not a census) | `session` |
| Sidecar round trip; a load without one behaves as today; a load restores rule set, Community sources and entry table, unit limit and mutators, and selects the recorded mod and mutators (P6); bank bytes unchanged | `save.TestSidecarRoundTripsEveryModSpelling`, `save.TestSidecarRefusesAnotherSchemaAndMalformedFiles`, `main.TestSaveSidecarRestoresTheRecordedSelection` (retail tier) |
| A save made under another installed mod reloads onto it and then restores; one whose mod is not installed is refused, naming it | `main.TestLoadingAnotherModsSaveSwitchesToItFirst`, `main.TestSaveSidecarRestoresTheRecordedSelection` (retail tier) |
| The reclaim-pulse product at the maximum step for the stock catalog (§6.5) | `content`, retail tier |
| Chip on retail, ProTA and Escalation menus; Mods & Mutators screen and the *Get more mods* dialog; loading-screen lines | `--shot` captures, reviewed |
| A merged hosted zip resolves the same winners and catalog hash as its original root list | the check tool (§13 unit 8) |

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
9. **Follow-ups.** Manifest signatures (§5.4); fuzz targets for the exposed
   readers, recommended before the catalogue is advertised widely;
   `minimumEngine` once releases are stamped.

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
| Sight-shape and terrain-ray quantization | [03 §3.2] |
| Configured unit-limit carry on load | [08 R-SESS-01 §9] |
| The loading screen | [07 "The loading screen"] |
