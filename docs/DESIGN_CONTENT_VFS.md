# Content, VFS and formats

The engine owns no content. Every byte it runs on is authored data from a
retail install, reached through one logical namespace, parsed by lossless
readers, and compiled once into immutable definitions before the first tick.
This document describes that path: the overlay that resolves a logical name to
bytes, the format readers that turn those bytes into structures, the catalogs
that turn structures into definitions with retail's defaults and conversions
already applied, and the preference store that survives a restart.

Related documents: [ARCHITECTURE.md](ARCHITECTURE.md) for the package map and
citation conventions, [INVARIANTS.md](INVARIANTS.md) for the rules every diff
is reviewed against, [SPEC_CONFLICTS.md](SPEC_CONFLICTS.md) for the places
where the reference install disproves the written contract.

## 1. Purpose and boundary

This area answers three questions and nothing else.

* **Where do these bytes come from?** `vfs` resolves a logical path such as
  `gamedata/sidedata.tdf` against an ordered overlay of loose directories and
  HPI-family archives, returns the winning bytes, and records who won and who
  was shadowed `[02 §2]`.
* **What is in them?** `formats` decodes one authored file into a structure
  and loses nothing on the way — offsets, duplicate records and source order
  survive, because the next layer's decisions depend on them `[02 §4]`
  `[02 §6]` `[02 §7]`.
* **What does the simulation get?** `internal/content` compiles the whole
  install into a `Catalog` of immutable definitions with retail's defaults
  filled in and its unit conversions applied exactly once `[02 §5]`. This is
  where the authored second-and-degree world becomes ticks and 16.16, and
  nothing downstream converts again [I8].

The boundary out of this area is `*content.Catalog` plus the `vfs.FSOps` read
surface. A simulation package takes a compiled catalog and never re-opens a
TDF, never re-applies a conversion, and never mutates a definition; art and
audio resolution happens through the same catalog before a battle starts, not
inside a frame. The boundary in is a filesystem root and a mount plan.

What this area does **not** own: the COB opcode interpreter and the piece
hierarchy built from a 3DO (DESIGN_UNITS_ORDERS_COB), the palette and SHD
lookup tables and the drawing of anything decoded here
(DESIGN_PRESENTATION_CLIENT), the GUI screens that consume a parsed `.gui`
(DESIGN_INTERFACE_HUD_INPUT), and the terrain runtime built from a parsed TNT
(DESIGN_WORLD_VISIBILITY). Those packages take structures from `formats` or
definitions from `content` and go on from there.

## 2. Packages and key types

### 2.1 `vfs` — the logical content namespace

`vfs.New` returns an empty `FS`; `FS.MountGameDirectory(root)` mounts a retail
install with `DefaultRetailMountPlan`, and `MountGameDirectoryWithPlan` takes
an explicit plan. Mounting indexes names and metadata only. Archive payloads
stay on disk until a file is opened, so startup cost is independent of the
size of the install.

| Type | What it is |
|---|---|
| `FS` | The overlay. Holds mounts in search order — priority descending, then mount order descending — so a lookup walks the slice directly with no per-open sort |
| `MountPlan`, `MountTier` | The tier assignment for each archive extension plus the loose root, and a `Revision` string that names the ordering policy in effect |
| `Provenance` | Where a winning entry came from: logical path, original (unfolded) path, provider type, source path, mount root, priority, mount order, compression. `ProviderID()` is its portable identity |
| `EntryInfo` | One indexed file or directory: `Name` (base name), `Path` (logical path), size, directory flag, `Source` provenance |
| `File` | The open handle: reader, reader-at, seeker, closer, plus `Info()` |
| `FSOps` | The narrow read surface everything downstream takes: `Open`, `ReadFileLimit`, `ReadDir`, `Stat`, `CacheStamp`. `*FS` and `*PinnedMount` both satisfy it |
| `Archive` | One opened HPI-family container. `OpenArchive` and `NewArchive` build one; `ArchiveOptions` carries the defensive limits |
| `ManifestRecord`, `ManifestOptions` | The deduplicated identity view: one record per logical path with its winner, its shadowed providers, and optionally the hash of the winning bytes |
| `ProviderInfo` | One mounted provider in precedence order — the unit a diagnostic names |
| `PinnedMount` | An inspection view for one mount: `Open`, `ReadFileLimit`, `Stat`, and `CacheStamp` all resolve against that provider alone, so a missing or unreadable pinned copy cannot acquire a winning overlay provider's bytes or identity. `ReadDir` remains the overlay's directory-enumeration view. |

Reads: `Open`, `ReadFile`, `ReadFileLimit`, `ReadFileRange` (which decodes only
the chunks a byte range needs), `Stat`, `ReadDir` (sorted, the canonical
contract for content consumers), `RetailReadDir` (provider enumeration order,
for the front-end wildcard scans that retail does not sort `[02 R-CAT-01 §1]`),
`Sources` (every provider holding a path, winner first), `Entries` (one record
per mount, for diagnostics), `Providers`, `Notes`, `Manifest`, `ManifestHash`,
`OpenMount`, `Pinned`, `Close`.

`vfs/hpi.go` implements the container: header and footer validation, the
directory-blob cipher, the 9-byte directory entries, and SQSH chunk decoding
`[02 §2]` `[fmt hpi]`. `vfs/hpi_write.go` writes one — used by the remaster
pipeline to pack authored art, never by the engine.

### 2.2 `formats` — lossless readers

One package, one file per format, no rendering or audio dependency. Every
reader takes a byte slice (or an `FSOps` plus a logical path) and returns a
structure; readers that need bounds carry an explicit `…Limits` struct with a
`Default…Limits()` so a malformed file cannot request an unbounded allocation.

| Reader | Owns | Layout authority |
|---|---|---|
| `ParseTDF`, `ParseTDFWithLimits`, `Document`, `Section`, `Item` | The generic authored-text grammar: sections, assignments, nesting, comment blanking, duplicate policy, typed accessors | `[fmt tdf]` `[02 §4]` |
| `LoadGAF`, `GAF`, `GAFEntry`, `GAFFrame` | The animation archive: entries, frame references, sub-frames, the transparent index | `[fmt gaf]` `[02 §6]` |
| `LoadTNT`, `TNT`, `TNTAttribute`, `TNTFeatureRecord` | The map terrain file: version word, tile index, per-cell attributes, feature records | `[fmt tnt]` `[02 §6]` |
| `LoadThreeDO`, `ThreeDO`, `ThreeDOObject`, `ThreeDOPrimitive` | The model archive: the object tree with its pointer relocation, vertices, primitives, texture names | `[fmt 3do]` `[02 §6]` |
| `LoadGUI`, `GUI`, `Gadget`, `CommonGadget` | Interface panel files, text and binary form | `[fmt gui]` `[02 §6]` |
| `LoadOTA`, `OTA`, `OTASchema` | Map metadata over a TDF document: the global header, the schema probe, the language-prefixed strings | `[fmt ota]` `[02 §6]` |
| `LoadPAL`, `LoadPaletteTable`, `Palette`, `PaletteTable` | The 768/1024-byte palette and the rectangular lookup tables built on it | `[fmt pal]` `[02 §7]` |
| `LoadFNT`, `FNT`, `FNTGlyph` | The bitmap font: height, the second header word, per-glyph rasters | `[fmt fnt]` `[02 §7]` |
| `LoadPCX`, `PCX` | The run-length image used by the front end | `[fmt pcx]` `[02 §7]` |
| `LoadWAV`, `LoadAudio`, `WAV` | PCM metadata for canonical RIFF/WAVE and the legacy container, sample bytes left in the VFS | `[fmt wav]` `[02 §7]` |
| `LoadSCT`, `LoadBMP` | The editor section file and the uncompressed BMP variants the install carries | `[02 §6]` |

The compiled script archive is decoded by `internal/cob`, not here; its byte
layout is `[fmt cob]` and its behaviour belongs to DESIGN_UNITS_ORDERS_COB.
The unit record's key table is `[fmt fbi]`.

Two writers exist alongside the readers — `gaf_write.go` and
`three_do_write.go` — for the remaster pipeline. They are authoring tools; the
engine only reads.

`Document`/`Section` deliberately preserve source order and duplicate records.
A section builds its resolved lookup vector once and binary-searches it;
`FirstValue`, `LastValue` and the typed accessors sit on top, so a caller
picks a duplicate policy explicitly instead of inheriting one.

### 2.3 `internal/content` — compiled catalogs

`content.Compile(fs)` walks the install and returns a `*Catalog`;
`CompileWithProgress` is the same compile with an observer for the loading
screen, and a nil observer makes it exactly `Compile` `[07 §4]`.

The compile is two-stage `[02 §5]`. Stage 1 discovers and parses each family
into typed records; stage 2 links cross-references, so enumeration order can
never leak into identity. The order of work inside the compile follows the
executable's own `[02 R-CAT-01 §5]`:

1. weapons (`weapons/*.tdf`), retaining the same-ID duplicate diagnostics;
2. units (`units/*.fbi`), then the category registry compiled from their
   category and target strings `[02 R-P0-03]`;
3. features (`features/<group>/*.tdf`, recursive), with successor links
   resolved inside the family;
4. movement classes (`gamedata/moveinfo.tdf`), then the footprint fields
   applied to unit definitions before any placement consumer sees one;
5. sides (`gamedata/sidedata.tdf`), sounds (`gamedata/sound.tdf` and the
   aliases in `gamedata/allsound.tdf`), map headers (`maps/*.ota` paired with
   `maps/*.tnt`), AI profiles (`ai/*.txt`);
6. battle tables (`gamedata/los.tdf`, `gamedata/meteor.tdf`) and the authored
   sight shapes `[03 §3.2]`;
7. link: unit weapon slots resolve against the finished weapon table; build
   menus compile from the side data and the downloadable enforcement walks
   their button names; the model catalog is sorted and per-unit model tops and
   page-count probes are filled `[02 R-CAT-01 §5]` `[02 R-CAT-01 §7]`;
   generated download pages compile and apply `[02 R-CAT-01 §8]`; unit scripts
   are resolved;
8. the manifest hash is taken from the VFS and `Catalog.Hash` is computed over
   canonical bytes.

| Type | What it is |
|---|---|
| `Catalog` | The compiled result: `Units`, `Weapons`, `Features`, `Movement`, `Sides`, `Sounds`, `Maps`, `Categories`, `LOS`, `Meteor`, `Sight`, `AIProfiles`, `Aliases`/`AliasOrder`, `BuildMenus`, `DownloadPlacements`, `Warnings`, `Manifest`, `Hash` |
| `UnitDef`, `WeaponDef`, `FeatureDef`, `MovementClass`, `SideDef`, `SoundCategory`, `SoundAlias`, `MapHeader`, `AIProfile`, `LOSTables`, `MeteorDefaults`, `SightShapes` | The immutable definitions. Each carries `DefinitionHeader` as its first field |
| `DefinitionHeader` | `CanonicalKey`, `Provenance`, `Hash` — identity, origin, content digest |
| `Provenance` | The content-side view of where a definition's bytes came from: logical path, provider ID, mount order |
| `CategoryRegistry`, `CategoryMask` | The sorted case-insensitive category token registry and the membership bitsets built from it `[02 R-P0-03]` |
| `BuildMenuPage`, `DownloadMenuPlacement` | The authored and generated build pages |
| `PresentationCatalog`, `AssetID`, `AssetSequence`, `FeatureAsset` | The content/presentation boundary: every image asset discovered and decoded before a battle starts, handed to draw code as stable IDs |
| `SkirmishManifest`, `SkirmishAsset`, `SkirmishDiagnostic` | The preflight result (§3.5) |

Accessors: `Unit`, `Weapon`, `WeaponByName`, `WeaponByID`, `WeaponLink`,
`WeaponRecordsByID`, `Category`, `ResolveCategoryMask`, `SortedUnitKeys`,
`UnitDefIndex`, `UnitDefByIndex`, `UnitIndexOf`, `SortedModels`, `ModelIndex`,
`ModelForUnit`, `DownloadPlacementsForPage`. Lifecycle: `Validate`, `Clone`,
`RestrictToCreatable`, `Finalized`.

`CanonicalKey` is the one key rule: trim, fold to lower case. Every catalog
map, every cross-reference and every hash input goes through it, so a lookup
can never disagree with a sort order `[02 §5]`.

### 2.4 `internal/settings` — preferences that survive a restart

Retail keeps the front-end preferences in the registry, plus the per-player
unit limit in the profile file's `[Preferences]` section `[02 §3]` `[07 §10]`.
Nanolathe keeps the same value set, the same defaults and the same
read-once/write-whole shape, and swaps both stores for one JSON file at
`$XDG_CONFIG_HOME/nanolathe/settings.json`.

`Settings` carries the last skirmish setup (`Skirmish` and its ten `Player`
slots: controller, side, colour, ally group, metal, energy), the campaign
difficulty, the `Messages` and `Display` blocks, and the interface flag word.
`Defaults()`, `Load`/`LoadFrom`, `Save`/`SaveTo`, `Path`, and the `Normalize`
methods are the whole surface.

Two shapes matter. First, a decode starts from `Defaults()` rather than from a
zero value, because several stored values are legitimately zero (LOS off,
mapping off, commander death off, ally group 0) and `Normalize` cannot tell an
absent field from a stored zero; starting from the defaults makes the
distinction for it. Second, only the preferences retail actually persists are
stored — the audio mixing and networking identity values retail keeps have no
owner here and are deliberately absent rather than written as invented
defaults [I9]. The display block and the visual option values are present
because the options screen's visuals page is their only writer
`[07 R-FE-01 §6]` `[07 R-FE-01 §11]`, with the registry loader's missing-value
defaults `[02 R-KEYS-01 §5]`.

### 2.5 Immutable definitions, mutable instances

A definition is compiled once and never changes: exported fields, no setters,
no runtime state. The simulation holds instances in its pools and points them
at definitions. Three consequences are load-bearing:

* `Catalog.Clone()` deep-copies for per-battle isolation, rewiring weapon and
  feature successor pointers into the cloned maps so a clone shares no mutable
  state with the original. A per-battle restriction is applied to a clone,
  never to a shared catalog.
* Identity is a hash over canonical bytes, taken in a fixed order and never by
  ranging a map [I1].
* Provenance travels with the definition, so a diagnostic about a bad value
  can name the file and the provider that supplied it without the simulation
  keeping the filesystem open.

## 3. Contracts

Two contract lists meet in this area, and they were numbered independently.
The overlay-and-formats list (§3.1–§3.3) is the one a bare `Cn` in a comment
under `vfs/` or `formats/` refers to; the catalog list (§3.4) is the one a
bare `Cn` under `internal/content` refers to. Numbers are stable: they are
what the code cites.

### 3.1 Overlay and archives (C1–C8)

**C1 — provider precedence.** Highest first: loose host files, `rev<name>.GP3`,
`*.CCX`, `*.UFO`, local `*.HPI` `[02 §2]`. `DefaultRetailMountPlan` encodes
exactly this as tier numbers; the CD-ROM tier is not scanned (§5). The single
best smoke test is that `gamedata/sidedata.tdf`, `units/armcom.fbi` and
`ai/default.txt` resolve to the patch archive on the reference install, the
patch correctly beating the base archives.

**C2 — every local HPI mounts.** The spec's ten-archive cap is a per-invocation
budget, not a global limit, and the converged retail state is every valid
local archive mounted (SC1). When more than ten local HPI archives are present
the mount records one note naming the count, so the discrepancy stays visible
rather than silent; `FS.Notes()` returns it.

**C3 — mount dedup.** A provider is keyed by its canonicalized absolute path,
compared case-insensitively; an equal path suppresses the second mount and
records a note `[02 §2]`. The suppression applies at both append points —
directories and archives — which also makes a second pass over the same plan a
no-op.

**C4 — the patch tier's extensions.** The tier is `rev<name>.GP3` in retail;
the mount also accepts `.gp4`, `.gpf` and `.swx` at that tier because installs
in the wild carry them. The extension stays visible in
`ManifestRecord.ProviderID`, so an unexpected winner is nameable.

**C5 — container gate.** The header is `"HAPI"` plus version `0x00010000`, and
the trailing window must name `Cavedog Entertainment` with `Copyright`
present. The four edition bytes between them are not compared, and the year
census is an observation, not a gate `[02 §2]`. A save bank (`ArchiveOptions.
AllowBank`) is the one other accepted version word; that container belongs to
DESIGN_SESSIONS_AI_SAVE.

**C6 — directory cipher.** The key byte is the low byte of the header key
word; the derived byte is `(k>>6) | (k<<2)`; from offset `0x14` onward each
byte is recovered as position XOR key XOR complement of the stored byte
`[02 §2]` `[fmt hpi]`. A stored key word of zero means the blob is plain.

**C7 — directory entries.** Entries are 9 bytes and are scanned so that the
last duplicate wins; names split on backslash only, with no `.` or `..`
handling `[02 §2]` `[fmt hpi]`. The overlay above accepts both slash styles
for host ergonomics (§5), but archive-internal indexing keeps retail's rule.

**C8 — compressed records.** A compressed record is a `u32` chunk-size table
followed by that many chunks, each with the 19-byte `SQSH` header, and every
chunk but the last decodes to exactly 65,536 bytes `[02 §2]` `[fmt hpi]`. That
last property is what makes `ReadFileRange` exact: a chunk's decompressed
offset is its index times the chunk size, so the chunks before a requested
range are stepped over with arithmetic instead of being read and decoded. A
header probe on a multi-megabyte record costs one chunk.

### 3.2 The authored-text grammar (C9–C12)

**C9 — comment blanking preserves offsets.** `//` to end of line, `/* */`, and
an unterminated `/*` to EOF are overwritten with ASCII spaces, character for
character, so every reported offset is a true offset into the source
`[02 §4]`. Newlines inside block comments are preserved as well, which keeps
reported line numbers meaningful; retail guarantees only the offsets.

**C10 — duplicate keys.** Keys are compared case-insensitively and the
resolved vector is sorted. An identical duplicate replaces (last wins); case
variants each survive as their own entry and the accessor returns the lower
bound of the run `[02 §4]` `[02 R-CAT-01 §3]`. Section and key names are
trimmed the way the loader trims them `[02 R-CAT-01 §3]`.

**C11 — the five diagnostics.** The parse diagnostics are reproduced verbatim
under the verbatim title, with retail's detail line (§4). A failed parse
yields a valid but empty tree, never a process exit; the caller decides
whether the failure is fatal for its family `[02 §4]` `[02 R-MALF-01 §4]`.

**C12 — typed accessors.** Each accessor locates its key the same way and
applies exactly one conversion. The integer accessor returns the caller's
default verbatim when the key is absent, so an authored zero is
indistinguishable from a missing key. The fixed-point accessor parses an
authored decimal as value × 65536 truncated, and stores its **default**
verbatim — a default of `65536` means 1.0, already in 16.16 `[02 §4]`.

### 3.3 Provenance and identity (C13, C14)

#### C13 — a diagnostic names a provider, never a host path

Every read carries `Provenance`, and every diagnostic that names where content
came from names the *provider* — an archive's file name, or a loose file's
path relative to its mount root — through `Provenance.ProviderID()`. An
absolute host path never reaches a manifest, a manifest hash, a definition
header or an error message. Two consequences: `Manifest()` and
`ManifestHash()` are byte-identical across runs, machines and filesystems
given the same set of archives, so content identity is a property of the
content; and a diagnostic is reproducible and quotable without leaking where a
particular user installed the game. Code cites this contract as
`[PLAN 01 C13]`.

**C14 — the deduplicated view.** `Manifest()` is the identity view: one record
per logical path, sorted, with the winner and the shadowed providers named.
`Entries()` returns one record **per mount** and `EntryInfo.Name` is a base
name, not a logical path — code that groups by `Name`, or that treats
`Entries()` as the unique file set, silently produces nonsense (SC4). Hashing
the winning bytes is opt-in through `ManifestOptions`, because hashing a full
install reads about a gigabyte.

### 3.4 Catalog contracts (C1–C15)

**C1 — two stages.** Discover and parse every family into typed records, then
link. A unit's weapon slots resolve only after every weapon compiles, so
enumeration order cannot leak into identity `[02 §5]`.

**C2 — weapon identity.** The `ID` key is read **first**, as an integer with
default −1, and selects the record; the section name becomes the catalog name
and `name` is a separate display string. A later section with the same ID
replaces every parser-owned field — including fields whose keys the later
section omits — and its section name becomes the surviving catalog name. The
superseded name then matches no record `[02 R-CONTENT-02]`. Sections without
an ID share one scratch slot the runtime name scan never reaches, so they are
inert. Duplicates are retained as `WeaponDuplicate` diagnostics in discovery
order, winner last.

**C3 — weapon conversions.** Velocity × 65536/30, acceleration × 65536/900,
durations × 30, turn rate × 1/30, and the minimum barrel angle's −11.25°
default in radians. Each conversion truncates after its multiply and is never
composed in a different order; an authored duration below 1/30 second becomes
0 ticks `[02 §5]` `[02 R-KEYS-01 §6]`.

**C4 — the damage table.** A weapon may carry a nested `DAMAGE` section. Its
`default` key is read with the integer accessor, default 0, and becomes the
fallback damage; **every other key in the section is enumerated**, its name
interned and its integer value stored in a per-weapon sorted map. A weapon
with no such section has fallback damage zero `[02 §5]`. The names are stored
as strings and nothing more: the runtime lookup is a case-insensitive search
against the target definition's exact unit name, not a category lookup
`[06 §9.2]`, and DESIGN_WEAPONS_PROJECTILES owns it.

**C5 — parsed but inert weapon fields.** `range` defaults to 32767.
`accuracy`, `tolerance` and `pitchtolerance` are parsed, stored and left
unwired; they are not spread inputs `[06 §4.1]`.

**C6 — movement classes.** Every class record starts from the startup
template — 255 on `MaxSlope`, `BadSlope`, `MaxWaterSlope` and
`BadWaterSlope`, 10000 and −10000 on the depth limits — so an omitted key
parses to the template value. The eight keys are read in order with their
chained defaults (`badslope` is half the `maxslope` just read; `badwaterslope`
half the `maxwaterslope` just read), and the three min-clamps then run
**unconditionally**, in order `[04 §6.1 R-DOC04-A]` `[02 R-CONTENT-01]`. With
the template in place the first clamp is the identity for a class that omits
the water fields, which is why the earlier gated-clamp divergence was wrong
(SC5).

**C7 — names and translation.** A unit's display name is language-prefixed
with fallback: the configured language's key, then the bare key `[02 §3]`. A
missing `gamedata/translate.tdf` yields a byte-exact identity mapping and is
not fatal.

**C8 — sides.** `SIDE0..N` are read up to the first gap. All thirty interface
anchors are mandatory and are stored **verbatim** as `x1,y1,x2,y2` corners,
not normalized, so a rectangle with `x2 < x1` survives to the consumer that
has to deal with it. A missing side font is fatal `[02 §6]`.

**C9 — cross-reference failure policy.** A feature successor that resolves to
nothing is fatal with the verbatim message (§4). The other misses have their
own documented recoveries `[02 R-MALF-01 §2]` `[02 R-MALF-01 §5]`: a weapon
miss links weapon record 0, the inactive sentinel, and consumers test for
inactivity rather than for a nil pointer; a corpse miss is the no-corpse
sentinel and leaves no wreck; a movement-class miss takes the per-unit scratch
record, which is compiled through the same ordered reads and unconditional
clamps as a pooled class so the two cannot drift; a script miss is a null
program that crashes retail at first creation, which the preflight turns into
a refusal to start (§3.5) `[04 R-COB-04 §8]`; a model miss is fatal, reported
against the `objects3d\<objectname>.3DO` path.

**C10 — the downloadable enforcement.** A unit reachable from a build menu
without `downloadable=1` produces the verbatim warning once (§4) and is
forced; the warning is collected in `Catalog.Warnings` and the caller owns
display `[02 §5]`.

**C11 — sound categories.** A category is 24 rows indexed by slot, with slot 0
an unused sentinel and slots 1–23 the named events. Variants gather as `K`,
`K1`, `K2`, … and a `<key>text` key supplies each caption; numbering is
contiguous from 1 and a present-but-empty value still counts
`[02 R-SND-01 §1]`. The per-slot priority and cooldown are static and global
across categories; they are compiled in here from `[03 §8.3]` and consumed by
the audio queue. Aliases from `gamedata/allsound.tdf` register in file order,
capped at 255, with 32-byte names `[02 R-CAT-01 §6]`.

**C12 — catalog identity.** `Catalog.Hash` is a digest over canonical
definition bytes including applied defaults and the battle tables. Every map
it walks is walked in sorted canonical-key order with a total tie-break, so
neither provider order nor Go's map randomization can move it, and two
compiles of the same install agree [I1].

**C13 — model identity.** Models are referenced by name only. The distinct
model names are sorted case-insensitively and indexed before any per-unit
pointer is cached, which is what makes piece identity independent of provider
order `[03 §2.4]`. Geometry loading belongs to `internal/model`.

**C14 — inert keys are retained.** Keys the compilers parse but nothing
consumes (`noautofire`, `ovradjust`, `steeringmode`, `wacky`, `ai_limit`, …)
are kept in the definition's `Unknown` map so a later consumer can be wired
without re-parsing, and they affect no behaviour and no identity `[02 §5]`
`[02 R-KEYS-01 §1]` `[02 R-KEYS-01 §2]`.

**C15 — battle tables.** `gamedata/los.tdf` and `gamedata/meteor.tdf` compile
into immutable `LOSTables` and `MeteorDefaults` that retain source precision.
Visibility and combat consume the typed values and never reopen or reparse
those files [I8].

### 3.5 Unit limits, restriction and preflight

**Unit limits.** There is no per-definition unit-limit key in an FBI, and one
must not be invented. The definition parser writes −1 (unlimited) into every
definition's limit field and sets its creatable bit; the only other writer is
the multiplayer restriction tree, which a skirmish or campaign battle never
runs `[05 R-SHARE-01 §9]`; the opener's per-player unit limit is the
preferences value `[02 R-CONTENT-03]`. The allocator's limit step is therefore always
passed in single player, and the only bound is the per-player slice size
`[05 R-SHARE-01 §7]`. The computer player's own gate is its profile's per-type
limit table, never the definition field `[05 R-SHARE-01 §10]`. `LimitEnabled`
distinguishes the parser's written −1 from a hand-built fixture's Go zero,
because a written 0 means the definition may not be created at all.

**Restriction.** `RestrictToCreatable` is the campaign restriction: retail
expresses it as a per-definition creatable bit, clears the bit on every record
from index 1 up, sets it again for the first record matching each listed name
case-insensitively, and then compacts every bit-clear record out of the table
and renumbers what is left `[08 R-ENTRY-01 §2]` `[05 R-SHARE-01 §8]`. Because
a compiled table then carries the bit on every record it holds, the bit is not
the mechanism — the removal is. The method performs that compaction, restamps
the category registry's unit indices, and re-digests the catalog. It runs on a
clone, once per battle.

**Preflight.** `PreflightSkirmish` resolves the whole content bundle a battle
needs — map, side, commander, the opening build chain, their models, scripts,
sounds and art — before the session starts, and returns a `SkirmishManifest`
whose `Diagnostics` name every failure at once rather than failing on the
first. It is where a missing or malformed required asset becomes a refusal to
start: a missing model or COB, a COB that names a piece the 3DO hierarchy does
not have `[04 §4.1]` `[fmt cob]`, a missing required entry point (`Create`,
the primary weapon's query/aim/fire family for an armed unit, the nanolathe
queries for a builder). Optional authored references are reported only when
they are internally inconsistent. The manifest hashes to a stable identity and
records the winning provider per asset, never a host path (C13).

### 3.6 Not implemented

* **The ten-archive mount cap** `[02 §2]`. Recorded, not implemented; the
  budget is per-invocation and mounting every local archive reproduces the
  converged retail state (SC1).
* **The CD-ROM archive tier** `[02 §2]`. Not scanned. A CD install path is
  supplied as an explicit root overlay instead.
* **Intra-tier enumeration order** `[02 §2]`. Retail inherits the host's
  directory order, which the research itself calls non-portable; the mount
  sorts lexically inside a tier (§5).
* **The OVR content-checksum replacement** `[02 §6]`. The account names and
  the four-accumulator checksum shape are recorded; nothing consumes the
  replacement, because its consumer is the lobby content-identity exchange
  `[08 R-OOS-01 §2]`, which is out of scope. `.OVR` is not a mounted provider
  extension in the installed corpus, so the path is stock-inert.

## 4. Diagnostics

Retail text is reproduced verbatim where the spec quotes it, character for
character, because a user comparing against retail is comparing strings.

| Text | Raised when | Source |
|---|---|---|
| `Parse error in .TDF File!` | the title of every authored-text parse failure, with the detail line ` - <name> = '<value>' from file <path>` | `[02 §4]` `[02 R-MALF-01 §4]` |
| `Data field - '=' not found` | an assignment with no `=` | `[02 §4]` |
| `Data field - ';' not found` | an assignment with no terminator | `[02 §4]` |
| `Sub-record - closing ']' not found` | an unterminated section header | `[02 §4]` |
| `Sub-record - opening '{' not found` | a section header with no body | `[02 §4]` |
| `End of file - nextblock not zero` | an unclosed section at end of file | `[02 §4]` |
| `Record "%s" missing from feature files` | a feature's dead, reclaimed or burnt successor names nothing | `[02 §5]` |
| `Hey! Somebody forgot to set downloadable=1 for %s` | a build-menu-reachable unit without the flag | `[02 §5]` |
| `Can't load GAMEDATA.TDF` | raised by a missing `SIDEDATA.TDF`; the message text is simply misnamed and no file of that name is ever opened | `[02 R-MALF-01 §5]`, SC2 |

Everything Nanolathe raises on its own account follows one shape:

```
nanolathe: <what failed>: logical path <path>, providers searched [<a>, <b>], expected <product>
```

`requiredContentError` and `unitScriptMissingError` in `internal/content` build
it; the provider list comes from `FS.Sources` when the overlay offers it and
from a single `Stat` otherwise, and each entry is a `ProviderID` — never a
host path (C13). Mount-time observations that are not failures — a suppressed
duplicate mount, the archive-count remark — are collected on `FS.Notes()` for
the caller to surface. Nothing in this area logs from inside a tick; the
compile happens before the session exists.

## 5. Divergences

Each is a place where the code deliberately departs from the written contract,
with the reason and the resolution. None is a compatibility flag: there is one
behaviour.

* **SC1 — the ten-archive cap.** The spec states a cap of ten local archives;
  the reference install has thirteen and plays. The cap is real but
  per-invocation, and repeated invocations converge to every valid local
  archive mounted, so every local archive mounts and a note names the count.
* **SC2 — `GAMEDATA.TDF` does not exist.** No file of that name exists in any
  mounted provider; the hard requirement is the `gamedata/` **directory**.
  `Validate` treats a missing `gamedata/`, `MOVEINFO.TDF` or `SIDEDATA.TDF` as
  fatal, `translate.tdf` as optional, and never requires `GAMEDATA.TDF`.
* **SC3 — intra-tier ordering.** Retail resolves same-tier archives in host
  enumeration order, which is not reproducible; the mount sorts lexically
  inside a tier and records every shadowed provider, so a differing winner can
  be named. Measured blast radius on the reference install: 323 logical paths
  are multiply provided within one tier and exactly two differ in size, one of
  which is not game content.
* **SC4 — `EntryInfo.Name` is a base name.** Not a spec conflict but the same
  class of trap: `Entries()` is per-mount and undeduplicated, and grouping by
  `Name` produces nonsense. `Manifest()` is the deduplicated view (C14).
* **SC7 — sound variants without the bare key.** The spec says an absent bare
  event key contributes no variants; stock data authors `select1`, `ok1`,
  `cant1` and `arrived1` with no bare form anywhere, and following the letter
  would mute those voices on a working install. The loader reads the bare key,
  discards the result and gathers the numbered keys unconditionally — which
  the executable itself does, so this is retail rather than a divergence.

Three smaller departures live only here, all recorded under [I11]:

* **Path ergonomics.** The overlay accepts both slash styles, folds `.` and
  rejects `..`; retail splits on backslash only and gives `.`/`..` no meaning.
  No shipped archive contains a `/`, `.` or `..` entry, so the window is
  unexercised, and archive-internal indexing keeps retail's rule (C7).
* **Bounds.** Directory offsets must be in range, decompressed size is capped,
  directory cycles are detected, and each format reader carries explicit
  limits. Format limits include aggregate decoded storage and reference/index
  budgets, so repeated file pointers cannot multiply host allocations; traversal
  depth is bounded and long sibling lists use iteration. These are Nanolathe
  host-safety policy, not recovered retail limits. They reject malformed input
  retail would crash on — the sanctioned exception to [I11] — and must not
  reject anything in a stock install, which the whole-install format walk is
  there to prove.
* **Leniency that keeps odd-but-loadable mod data working.** An assignment
  followed by a newline without its terminator parses instead of raising the
  second diagnostic, and an empty `unitname` falls back to the filename stem.
  Stock content never exercises either case.

## 6. Research map

| Behaviour | Owning research |
|---|---|
| Startup, content discovery, the hard requirements | `[02 §1]` |
| Provider precedence, mount order, dedup, the HPI container, cipher, SQSH chunking | `[02 §2]`, `[fmt hpi]` |
| Wildcard matching, the union enumerator's record and order, first-backing-wins | `[02 R-CAT-01 §1]` |
| Registry primitive access masks, the image-output directory default | `[02 R-CAT-01 §2]` |
| Registry configuration, language and localization; the translation table | `[02 §3]` |
| Authored-text grammar, comment blanking, duplicate policy, the five diagnostics, typed accessors | `[02 §4]`, `[fmt tdf]` |
| Key, value and section-name trimming | `[02 R-CAT-01 §3]` |
| Catalog construction and linking; unit, weapon, feature, movement, sound records | `[02 §5]`, `[fmt fbi]` |
| Unit catalog discovery, the sentinel record, the three drop gates | `[02 R-CAT-01 §4]` |
| The catalog compiler's order of work, name sort, unit indices, build-menu pages, scripts, side build lists | `[02 R-CAT-01 §5]` |
| The alias catalog file and its per-section read | `[02 R-CAT-01 §6]` |
| The unit model's height word and the catalog-time texture bind | `[02 R-CAT-01 §7]` |
| The download-menu compile and the per-builder list extension | `[02 R-CAT-01 §8]` |
| Weapon-family discovery and the same-ID merge | `[02 R-CONTENT-02]` |
| Movement-profile template initialization | `[02 R-CONTENT-01]`, superseded by `[04 §6.1 R-DOC04-A]` |
| Category token registry and membership bitsets | `[02 R-P0-03]` |
| The building heading field audit | `[02 R-P28-ANG-01R §1]` |
| Unit-record and weapon-record consumers not stated elsewhere; the generated key consumer table; the unsigned 16-bit tick words | `[02 R-KEYS-01 §1]`, `[02 R-KEYS-01 §2]`, `[02 R-KEYS-01 §5]`, `[02 R-KEYS-01 §6]` |
| The malformed-input matrix; HPI mount and read checks; the fatal TDF message; catalog-level outcomes; GAF, TNT, 3DO/COB, PCX/PAL/FNT and WAV checks | `[02 R-MALF-01 §2]`, `[02 R-MALF-01 §3]`, `[02 R-MALF-01 §4]`, `[02 R-MALF-01 §5]`, `[02 R-MALF-01 §6]`, `[02 R-MALF-01 §7]`, `[02 R-MALF-01 §8]`, `[02 R-MALF-01 §9]`, `[02 R-MALF-01 §10]` |
| The map-load pipeline, OTA diagnostics, the global header keys, schema selection, terrain load, the feature-name miss policy, the map browser | `[02 R-MAP-01 §1]`–`[02 R-MAP-01 §9]`, `[fmt ota]`, `[fmt tnt]` |
| The sound-category loader: bare and numbered keys, captions | `[02 R-SND-01 §1]` |
| Audio preference values: names, defaults, bit map, write-back | `[02 R-SND-01 §2]` |
| Side and battle interface data; interface panel files; map files; the animation, model and script archives; the content checksum | `[02 §6]`, `[fmt gui]`, `[fmt gaf]`, `[fmt 3do]`, `[fmt cob]` |
| Font, image and sample files | `[02 §7]`, `[fmt fnt]`, `[fmt pcx]`, `[fmt pal]`, `[fmt wav]` |
| Failure, caching and lifetime rules | `[02 §8]` |
| Model catalog sort and piece identity | `[03 §2.4]` |
| Authored sight shapes | `[03 §3.2]` |
| Static per-slot sound priority and cooldown | `[03 §8.3]` |
| Movement class template, ordered reads, unconditional clamps | `[04 §6.1 R-DOC04-A]` |
| A missing script is a null program that crashes at first creation | `[04 R-COB-04 §8]` |
| Weapon fields parsed and left inert; the damage lookup is by unit name | `[06 §4.1]`, `[06 §9.2]` |
| The loading-screen progress surface | `[07 §4]` |
| Preferences, their defaults and their only writer | `[07 §10]`, `[07 R-FE-01 §6]`, `[07 R-FE-01 §11]` |
| Definition limit field, its writers, the allocator gate, the planner's own limits | `[05 R-SHARE-01 §7]`, `[05 R-SHARE-01 §8]`, `[05 R-SHARE-01 §9]`, `[05 R-SHARE-01 §10]` |
| Restriction applied at battle entry | `[08 R-ENTRY-01 §2]` |
| The content-identity exchange that consumes an override checksum | `[08 R-OOS-01 §2]` (out of scope) |

## 7. Not implemented, and open questions

No `TODO(question)`, `TODO(T23)` or `TODO(T25)` marker remains in `vfs`,
`formats`, `internal/content` or `internal/settings`: every question these
packages once carried was settled by a traced finding, and each is now a
contract above. What remains open is recorded here.

* **The archive-only gate on unit and weapon discovery** is deliberately not
  applied (SC24, §3.6). It closes if mod loading ever needs retail's gate, at
  which point the gate belongs in the catalog loader keyed on the entry's
  provider kind and the fixtures get packed.
* **The override content checksum has no consumer** (§3.6). The comparison
  that would read a replaced definition hash lives in the lobby's
  content-identity exchange `[08 R-OOS-01 §2]`, which is out of scope; the
  path is stock-inert because `.OVR` is not a mounted provider extension.
  Settling it needs a trace of the lobby metadata comparison, and single
  player does not need it.
* **Intra-tier ordering cannot be reproduced**, only measured (SC3). The
  manifest names the winner so a disagreement is nameable rather than silent.
* **Retail's own leniencies are unexercised windows.** The parse leniency and
  the two unit-section fallbacks (§5) have no stock input that reaches them,
  so no observation can confirm or refute what retail does there. They stay
  documented rather than tightened, because tightening them would reject data
  that loads today.
