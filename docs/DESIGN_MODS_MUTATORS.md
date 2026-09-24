# Design — Mods, mutators and match identity

How a player installs, selects and switches content mods such as ProTA,
TA Zero and TA: Escalation without command-line flags; how global
**mutators** (build speed, cost, health, damage, sight and radar multipliers)
are selected and locked for a battle; and how a Nanolathe save records all of
it so that loading the save restores the same match.

**Status: design, not implemented.** The maintainer's decisions of 2026-09-23
are in §2. The proposals this document made were confirmed the same day and
are listed in §12.

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
| D3 | Redistribution permission is assumed. nanolathe.gg hosts the manifest **and** every download, so availability does not depend on the original sites and the hosted zips can be cleaned (§5.5). |
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
direction. D14 is the maintainer's layout for the screen.

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
  .staging/                         partial downloads and extractions; cleared at start
```

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
- `requires` lists logical paths that the **base** install must resolve, for
  example a map from an expansion the mod overlays. A mod whose requirements
  fail is listed but cannot be selected, and the screen names the missing
  paths.

### 4.3 Selection and precedence

- **Flags and settings.** `--mod <id>[@<version>]` and `--mod none` on both
  commands; the settings key `mod` (`{"id": …, "version": …}`) is the saved
  choice, written by the Mods & Mutators screen. The flag wins over the
  setting. A missing version selects the newest installed version.
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
  (D12). With no mod, today's precedence (flag, saved preference, detection)
  is unchanged.
- **Gameplay minimum.** While a mod with `minimumGameplay` is selected, the
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
  options. `community` enables the Community host options in the settings
  `presentation` block (`communitySelection`, `doubleClickSelection` and the
  Community HUD options;
  [DESIGN_COMMUNITY_PATCH §7](DESIGN_COMMUNITY_PATCH.md#7-host-and-presentation-features-out-of-the-profile));
  `retail` disables them. When the player switches to a mod that names a
  preset, the Mods & Mutators screen shows *Use ProTA controls*, checked by
  default. Applying it writes those options once. Later changes by the player stick,
  and switching back does not restore anything automatically.
- **A missing mod at start.** If the saved mod's directory has gone, the game
  starts with no mod and the main menu shows one message; start-up never fails
  for this.

### 4.4 Applying a switch

A different mod needs a different mount, so applying it **reloads the
content in process** (revised 2026-09-23; the first version restarted the
process). The window loop holds the running shell through one indirection.
A switch is requested from the menu and performed after the current step
returns, never inside one. The reload:

1. mounts the base install plus the chosen mod, with the mod's own content
   profile (D12);
2. builds a fresh shell on that content, reads the settings into it and
   enforces the mod's gameplay minimum (§4.3);
3. rebinds the client to it: model file system and caches, palette, font,
   cursors, visual options and UI stage;
4. only then releases the old shell: its dialogs, its voices and music, and
   its archive handles.

A failure at any step before the last leaves the running shell as it was and
says why on its main menu. Process-global state tied to the mounted content
must reset on a remount. One case is known: `client.LoadMaterialTable` must
reinstall the embedded material table when the new content has none, rather
than keep the previous mod's. A save whose
sidecar names another mod switches the same way and then loads the save
(§7.3).

### 4.5 Manual installs

Dropping a `.zip` or a folder onto the window (Ebitengine's dropped-files
input) installs it through the same extraction and validation path as a
download (§5.3). A package with `nanolathe-mod.json` installs as that mod.
Without metadata, it installs as `local-<sanitized name>` with version
`local`, no minimum, no preset and a detected content profile, and the Mods &
Mutators screen marks it *Local*. Copying a prepared directory into the
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
        "url": "https://nanolathe.gg/mods/prota/prota-4.8.zip",
        "size": 16302551,
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

The zip's own `nanolathe-mod.json` must agree with its manifest entry on `id`,
`version`, `contentProfile`, `minimumGameplay` and `controls`; a disagreement
refuses the install. A minimum engine version is deliberately absent: the
build carries no release version today (`internal/version` names a save
profile, not a release). Add `minimumEngine` once releases are stamped.

### 5.2 When the client talks to the network

- Only when the player opens the *Get more mods* dialog (§8.2) or starts a
  download. Never at start-up, never in battle, never in the displayless
  command (D5).
- Only `https://` URLs on `nanolathe.gg`. A redirect to another origin is
  refused.
- The last good manifest is cached in the data directory and shown, with its
  age, when the fetch fails. Installed mods never need the network.
- Requests carry a `nanolathe/<profile>` user agent and nothing identifying:
  no cookies, no identifiers, no telemetry.

### 5.3 Download, verify, extract, commit

1. Download to `.staging/<id>-<version>.zip.part`, resuming with an HTTP
   range request when the server supports it. The screen shows progress and
   can cancel.
2. Check size and SHA-256 against the manifest. Any mismatch deletes the file
   and reports it with the standard diagnostic.
3. Extract to `.staging/<id>-<version>/`:
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

v1 trusts HTTPS to nanolathe.gg (D4). The manifest's SHA-256 still matters:
it catches truncated and corrupted downloads, and it is the archive identity a
save records (§7).

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

For whoever builds the zips on nanolathe.gg:

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
	Health     Factor
	Damage     Factor
	Sight      Factor
	Radar      Factor
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
  ([04 §4.4], [06 §9.2]); 2³¹−1 for `buildtime`. The maximum is the narrowest
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
| Sight | unit `SightDistance` | k× sight | Both sight lookups clamp at their table's last entry, so large factors saturate for long-sighted units ([03 §3.2]). The ranges that read `sightdistance` widen with it: the fire-at-will opportunity scan and hold-position leash ([04 R-STANCE-01 §3], [04 R-STANCE-01 §4]) and the patrol and VTOL work scans ([04 R-ORD-01 §4], [04 R-ORD-01 §7]). |
| Radar | unit `RadarDistance`, `SonarDistance`, `RadarDistanceJam`, `SonarDistanceJam` by the same factor | k× radar and sonar | All four are plain search radii, so detection and jamming stay in proportion ([03 R-VIS-01 §5]). The emitter's height bonus is not scaled ([03 R-VIS-01 §4]); `mincloakdistance` is left alone. |

**Known hazards.** The unit-reclaim pulse forms a 32-bit product of
`workertime`, a kill factor, `maxdamage` and 15, which retail lets overflow
([05 R-WORK-01 §4]). Build speed no longer reaches it; Health at ×4 multiplies
it by up to 4, but the 32,767 cap limits that: ARMCOM reclaiming CORKROG
overflows from 70 kills instead of 75. The abandoned-frame decay forms the 32-bit product `11·buildtime`
([05 R-WORK-01 §9]); at Build speed ×0.25 the largest stock `buildtime` gives
164,673,432, well inside the range. Mutators do not guard downstream
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
  `content_profile`.
- `--mutator <name>=<factor>` (repeatable) on both commands and the settings
  key `mutators` select them; the flag wins over the setting.

## 7. The save sidecar

### 7.1 Placement and lifecycle

A save `SAVEGAME/<name>.SAV` gets `SAVEGAME/<name>.SAV.nanolathe.json`. The
retail enumerator lists only `*.SAV`, so the sidecar never appears as a save
and the bank bytes are unchanged; a retail executable can still read the bank.

The sidecar is written, through a temporary file and a rename, immediately
after the bank is written successfully. Campaign continuation saves get one
too. Deleting or overwriting a save through the dialog does the same to its
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
root stack. `community` records the session's Community sources and its
battle-entry table (`Session.CommunitySources`, `Session.EntryCommunity`), so
a restored battle resolves and switches exactly as the saved one did, whatever
the host's current settings are.

### 7.3 Loading

1. **No sidecar**, as with retail saves and older Nanolathe saves: behave
   exactly as today. Use the current selection, the existing unit-limit rules
   and no mutators, and infer nothing from the loaded content
   ([DESIGN_GAMEPLAY_RULES §6](DESIGN_GAMEPLAY_RULES.md#6-save-interaction)).
2. **Mod.** If the recorded mod differs from the running one: if that version
   is installed, switch to it (§4.4); if the manifest offers it, offer the
   download, then switch; otherwise refuse with the standard diagnostic
   naming the mod and version. The load makes the recorded mod the selected
   mod (P6). A `custom` sidecar loads only in a matching manual stack.
3. **Rule set.** Bind the recorded name. If this build cannot select it (a
   registered set that is not linked), load under the recorded base and show a
   warning on the loading screen.
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
   with a warning on the loading screen rather than refusing.

### 7.4 Contracts this replaces

- DESIGN_GAMEPLAY_RULES §6 says a save records no rule-set name. The bank
  still records none; the sidecar does, and a load restores it. A save without
  a sidecar keeps the §6 behaviour.
- The `TODO(question)` in `internal/session/retail_save.go` asking for a
  Nanolathe-side save metadata area is settled by §7 once it is implemented.
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
  gameplay selection that applying will produce (§4.3). It also shows
  *Use <mod> controls* when the mod names a preset (§4.3), and *Remove*.
  *Get more mods…* sits beneath the list.
- **Mutators (right).** A summary, not the editor, so the column never grows
  with the catalogue: up to four active mutators (then *and N more*),
  *Change...* and *Reset*.
- *Apply* writes the settings for both columns, and reloads when the mod
  changed (§4.4). *Cancel* discards both.

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
Durability, Vision), each row showing the mutator and its factor; the
selected mutator's description beneath; *Raise*, *Lower*, *Default* and
*Reset all* in the right-hand column; *OK* keeps the changes for the screen's
*Apply*, and *Cancel* restores the set the dialog opened with.

**The Get more mods dialog.** A modal over the screen. Opening it fetches the
manifest (§5.2); when that fails, it shows the cached manifest and its age.
It lists every manifest entry whose id and version are not installed, with
name, version, download size, summary, minimum gameplay and any unmet base
requirement. *Download* on a row starts §5.3, with a progress bar and
*Cancel*. Downloads run one at a time. A finished download joins the
installed list and is not selected automatically. Leaving the Mods & Mutators
screen cancels an unfinished download and clears its staging files; a later
download of the same archive resumes where the server allows it (§5.3).

### 8.3 The loading screen

Up to three lines in the retail font and title colour, above the existing map
line:

1. `<mod> <version> · <gameplay> · Unit limit <n>`, with *Total
   Annihilation* when there is no mod;
2. `Mutators: Build speed ×2, Health ×1.5`, omitted when there are none;
3. any warning from §7.3.

This is a Nanolathe divergence from the authored screen
([07 "The loading screen"]), recorded in DESIGN_INTERFACE_HUD_INPUT when
implemented.

### 8.4 The load dialog

The selected save's summary panel gains one line with the sidecar's mod and
mutators, so the player knows before loading that it will switch.

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
| `cmd/nanolathe-headless` | `--mod` (installed mods only, never fetches), `--mutator` | — |

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
| SHA-256 and size mismatch, truncated download, resume, off-origin redirect refused, offline cache shown | `modfetch` against `httptest` |
| Zero mutators leave the clone deep-equal with an equal `Hash`; each mutator changes exactly its fields; rounding, minimum-one, saturation and `v ≤ 0` boundaries; the hash is independent of field order | `content` |
| Mutators reach fresh skirmish, mission entry and restore; all six fingerprint locks unchanged | `session`, `headless` |
| Under Strict 3.1 with Build speed ×2, a fixed construction finishes in half the ticks and bills the same total resources (a relationship, not a census) | `session` |
| Sidecar round trip; a load without one behaves as today; a load restores rule set, Community sources and entry table, unit limit and mutators, and selects the recorded mod and mutators (P6); bank bytes unchanged | `session`, `save`, `cmd/nanolathe` |
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

## 13. Work units

Ordered so each lands green on its own. No unit moves an existing
fingerprint.

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
