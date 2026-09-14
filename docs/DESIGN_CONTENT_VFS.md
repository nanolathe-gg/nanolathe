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
inside a frame. The boundary in is an ordered list of filesystem roots and a mount plan.

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
size of the install. `FS.MountGameDirectories(roots)` adds an outer root
priority: every later root wins over every earlier root. A one-root list
retains the existing priorities and manifest identity.

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
`[02 §2]` `[fmt hpi]`. `vfs/hpi_write.go` writes archives for authored test
fixtures; the runtime engine only reads them.

### 2.2 `formats` — lossless readers

Ordinary formats share one package, one file per format. The stateful movie
decoder lives in `formats/zrb`; neither imports rendering or audio devices. Every
reader takes a byte slice (or an `FSOps` plus a logical path) and returns a
structure; readers that need bounds carry an explicit `…Limits` struct with a
`Default…Limits()` so a malformed file cannot request an unbounded allocation.

`LoadThreeDO` is source-faithful: it preserves the raw input, authored object
selection values, source offsets and primitive order. `internal/model` derives
the retail selection swap and stable mean-Y primitive order once for its
immutable presentation model, retaining each compiled primitive's authored
index for source-facing consumers `[02 "Model archive (3DO)"]` `[03 §2.4]`.
Catalog admission calls `model.ValidateSource` before publishing required
models or derived heights. This checks compilation requirements without
allocating presentation geometry or sorting a second time.

| Reader | Owns | Layout authority |
|---|---|---|
| `ParseTDF`, `ParseTDFWithLimits`, `Document`, `Section`, `Item` | The generic authored-text grammar: sections, assignments, nesting, comment blanking, duplicate policy, typed accessors | `[fmt tdf]` `[02 §4]` |
| `LoadGAF`, `GAF`, `GAFEntry`, `GAFFrame` | The animation archive: entries, frame references, sub-frames, the transparent index | `[fmt gaf]` `[02 §6]` |
| `LoadTNT`, `TNT`, `TNTAttribute`, `TNTFeatureRecord` | The map terrain file: version word, tile index, per-cell attributes, feature records | `[fmt tnt]` `[02 §6]` |
| `LoadThreeDO`, `ThreeDO`, `ThreeDOObject`, `ThreeDOPrimitive` | The lossless model archive: the object tree with pointer relocation, authored selection word, vertices, primitives in authored order, texture names, source offsets and raw bytes | `[fmt 3do]` `[02 "Model archive (3DO)"]` |
| `LoadGUI`, `GUI`, `Gadget`, `CommonGadget` | Interface panel files, text and binary form | `[fmt gui]` `[02 §6]` |
| `LoadOTA`, `OTA`, `OTASchema` | Map metadata over a TDF document: the global header, the schema probe, the language-prefixed strings | `[fmt ota]` `[02 §6]` |
| `LoadPAL`, `LoadPaletteTable`, `Palette`, `PaletteTable` | The 768/1024-byte palette and the rectangular lookup tables built on it | `[fmt pal]` `[02 §7]` |
| `LoadFNT`, `FNT`, `FNTGlyph` | The bitmap font: byte height, ignored byte, signed baseline, first code, per-glyph rasters | `[fmt fnt]` `[02 §7]` |
| `LoadPCX`, `PCX` | The run-length image used by the front end | `[fmt pcx]` `[02 §7]` |
| `LoadWAV`, `LoadAudio`, `WAV` | PCM metadata for canonical RIFF/WAVE and the legacy container, sample bytes left in the VFS | `[fmt wav]` `[02 §7]` |
| `zrb.New`, `Decoder.Next`, `Decoder.DecodeAudio` | Smacker 2 movie frames, palette history, static and dynamic Huffman trees, PCM/DPCM soundtrack; bounded state and explicit unsupported-codec rejection | `[fmt zrb]` |
| `LoadSCT` | The editor section file: validated `2W × 2H` `Heights`, exact v2/v3 `AttributeData`, and preserved `Raw` bytes | `[02 §6]` |
| `LoadBMP` | The uncompressed BMP variants the install carries | `[02 §6]` |

`formats/zrb` retains encoded movie bytes and one indexed image. `Next` returns
reusable palette/image/audio buffers; callers copy any data they retain beyond
the next decode. `DecodeAudio` decodes a selected complete soundtrack without
advancing video state. The shell owns playback cadence, scanline display and
menu restoration; the decoder owns only authored data `[fmt zrb]` `[03 §9]`.

The compiled script archive is decoded by `internal/cob`, not here; its byte
layout is `[fmt cob]` and its behaviour belongs to DESIGN_UNITS_ORDERS_COB.
The unit record's key table is `[fmt fbi]`.

Two writers exist alongside the readers — `gaf_write.go` and
`three_do_write.go` — for authored test fixtures. The runtime engine only
reads these formats.

`Document`/`Section` deliberately preserve source order and duplicate records.
A section builds its resolved lookup vector once and binary-searches it;
`FirstValue`, `LastValue`, `ResolvedAssignments` and the typed accessors sit
on top, so a caller
picks a duplicate policy explicitly instead of inheriting one.

### 2.3 `internal/content` — compiled catalogs

`content.Compile(fs)` walks the install and returns a `*Catalog`;
`CompileWithProgress` is the same compile with an observer for the loading
screen, and a nil observer makes it exactly `Compile` `[07 §4]`.

The compile is two-stage `[02 §5]`. Stage 1 discovers and parses each family
into typed records; stage 2 links cross-references. FBI records retain the
retail sort permutation, including its deterministic equal-name ordering. The order of work inside the compile follows the
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
| `AssetID`, `AssetSequence` | Typed presentation asset identities and frame-sequence metadata used by texture playback |
| `SkirmishManifest`, `SkirmishAsset`, `SkirmishDiagnostic` | The preflight result (§3.5) |

Accessors: `Unit`, `Weapon`, `WeaponByName`, `WeaponByID`, `WeaponLink`,
`WeaponRecordsByID`, `Category`, `ResolveCategoryMask`, `SortedUnitKeys`, `UnitRecords`,
`UnitDefIndex`, `UnitDefByIndex`, `UnitIndexOf`, `SortedModels`, `ModelIndex`,
`ModelForUnit`, `DownloadPlacementsForPage`. Lifecycle: `Validate`, `Clone`,
`RestrictToCreatable`, `Finalized`.

Weapon name lookup scans an immutable slot-ordered view built during compilation
and index rebuilding. Public `WeaponRecordsByID` still returns its own slice;
cloning rebuilds the retained view against cloned definitions. Runtime lookups
therefore preserve first-slot precedence without allocating a sorted slice.

`Catalog.UnitRecords()` returns a copied slice of all retained FBI definitions
in 1-based ID order, including duplicate and empty names; sentinel zero is
implicit. `Units` and `SortedUnitKeys` expose the runtime name lookup and
its reachable unique keys. `UnitDefByIndex` and `UnitIndexOf` preserve each
record's identity; `UnitDefIndex` follows the retained order's lower-bound
lookup, selecting the first equal name when the final names remain sorted.
Compiled IDs stay fixed
until a battle-local restriction compacts and renumbers the clone. Map-only
fixture catalogs retain their existing sorted-key fallback.

Discovery preserves record slots through the retail compatibility gates and
compaction, then uses the retail partition/insertion sort [02 R-CAT-01 §§4–5].
`compileUnitDiscovery` fills only the first pass's fields. After sorting,
`compileUnitSecondary` reads the resource selected by stored `UnitName` and
uses the established selective overwrite contract. Missing secondary data
leaves `DiscoveryOnly` state, which prevents category and weapon linking from
initializing gameplay links while retaining its ID. Runtime names can change
without a second sort; `firstUnitNames` projects the retail lower-bound lookup rather than repairing
the final order. `DiscoveryProvenance` retains the admission source;
`Provenance` and untyped source text follow a successful secondary read,
except for discovery-only `ai_limit`. Empty resource identities remain valid
path inputs, selecting `objects3d/.3do` and `scripts/.cob`.
The host collects a warning for each unavailable secondary definition in
retained record order, naming the attempted resource and the original
discovery path/provider, with read failures retaining their cause.
Required preflight use of a `DiscoveryOnly` record is refused; optional use
is diagnosed without inventing gameplay fields. These are host diagnostics
around the unchanged discovery contract `[02 R-CAT-01 §5]`.

All category, weapon, movement, model, script, page and downloadable passes
consume retained records. Download builder indices and restriction names
still resolve only to the first matching record. Clone and hash include every
record; stock unique-name catalogs preserve their prior IDs and hash stream.

Feature reads use the TDF parser's byte budget. A read failure returns the
winning logical path, provider and underlying cause before successor linking;
it cannot silently remove a definition and later masquerade as a missing
successor. Authored syntax and successor-miss policies remain unchanged.
Filesystem causes display their operation and reason without a host filename;
returned errors retain the original cause for programmatic inspection.

`CanonicalKey` is the one key rule: trim, fold to lower case. Every catalog
map, every cross-reference and every hash input goes through it, so a lookup
can never disagree with a sort order `[02 §5]`.

### 2.4 `internal/settings` — preferences that survive a restart

Retail keeps the front-end preferences in the registry, plus the per-player
unit limit in the profile file's `[Preferences]` section `[02 §3]` `[07 §10]`.
Nanolathe keeps the same value set, defaults except for the unit limit (§5),
and the same read-once/write-whole shape, and swaps both stores for one JSON file at
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
rather than silent; `FS.Notes()` returns it. Count only newly successful mounts,
excluding rejected candidates and duplicate paths.

**C3 — mount dedup.** A provider is keyed by its canonicalized absolute path,
compared case-insensitively; an equal path suppresses the second mount and
records a note `[02 §2]`. The suppression applies at both append points —
directories and archives — which also makes a second pass over the same plan a
no-op. Failed mount attempts do not enter the dedup set, so a corrected
candidate can be explicitly retried.

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

Automatic discovery continues after `ErrRejectedArchive` header/footer gate
failures and candidate open failures, recording portable provider diagnostics
in `FS.Notes()`. Direct `MountArchive` retains its error. Directory and payload
failures remain fatal; they are not reclassified as harmless container rejection.

Map catalog discovery similarly skips only `ErrMissingOTAHeader`, retaining a
`Catalog.Warnings` diagnostic before reading the rejected candidate's terrain
`[02 R-MAP-01 §2]` `[02 R-MAP-01 §9]`. The parser's existing 16 MiB host limit
governs OTA reads. Read failures, syntax errors and required TNT failures still
abort compilation. Accepted campaign maps remain in the catalog; the browser
owns network-schema filtering. No rejected map receives fabricated metadata.

**C6 — directory cipher.** The key byte is the low byte of the header key
word; the derived byte is `(k>>6) | (k<<2)`; from offset `0x14` onward each
byte is recovered as position XOR key XOR complement of the stored byte
`[02 §2]` `[fmt hpi]`. A stored key word of zero means the blob is plain.

**C7 — directory entries.** Entries are 9 bytes and are scanned so that the
last duplicate wins; names split on backslash only, with no `.` or `..`
handling `[02 §2]` `[fmt hpi]`. The overlay above accepts both slash styles
for host ergonomics (§5), but archive-internal indexing keeps retail's rule. Component selection applies to
entire duplicate directories: a replaced subtree contributes no lookup or
wildcard result. The raw audit index still retains its authored entries.

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
link. A unit's weapon slots resolve after weapon compilation. Discovery order
still determines same-ID replacement and the point where a missing `UNITINFO`
ends unit parsing `[02 R-CONTENT-02]` `[02 R-CAT-01 §4]`.

The VFS winner is retained throughout discovery (SC24): loose FBI winners are
parsed before the archive gate drops them; loose weapon winners are skipped
before parsing. A shadowed archive is never substituted. Unit files require
`UNITINFO`; there is no first-section fallback. An absent `UNITINFO` ends the
stage, leaving later enumerated files unparsed `[02 R-CAT-01 §4]`.

**C2 — weapon identity.** The `ID` key is read **first**, as an integer with
default −1, and selects the record; the section name becomes the catalog name
and `name` is a separate display string. A later section with the same ID
replaces the ordinary parser-owned fields — including fields whose keys the
later section omits — and its section name becomes the surviving catalog name.
The existing damage-override table is the documented exception: a same-ID
parse contributes to that table. The compiler retains both the per-spelling
values and their lookup order, so a later case variant precedes earlier
fold-equal entries; cloning copies that order alongside the map
`[06 R-DMG-01 §1]`. The superseded name then matches no record `[02 R-CONTENT-02]`. Sections without
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

**C5 — compiled weapon fields and their consumers.** `range` defaults to 32767.
`accuracy` feeds the turret spread; ballistic creation consumes those angles,
while ordinary creation solves again from the aim point. `tolerance` and
`pitchtolerance` feed the angular-drift gate. Combat owns these consumers
`[06 R-WPN-03 §1]` `[06 R-WPN-05 §5]`.

**C6 — movement classes.** Discovery probes exact `CLASS0` through `CLASS31`
in numeric order, skips gaps, and uses the TDF accessor's first matching
section. The catalog preserves the first numeric class for each authored name,
matching FBI resolution's first case-insensitive match
`[02 "Movement class record"]` `[02 §4]`. Every class record starts from the
startup template — 255 on `MaxSlope`, `BadSlope`, `MaxWaterSlope` and
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
program that crashes retail at first creation `[04 R-COB-04 §8]`. Catalog
linking retains that definition with a warning and the attempted logical path;
an existing unreadable or malformed file also retains its winning provenance.
A required missing, empty or malformed program refuses preflight (§3.5), and
unit creation/restore independently refuses it before allocation. Valid programs
remain immutable catalog assets. This user-authorized host refusal boundary
keeps an unrelated broken unit from preventing use of the whole install; it
never substitutes an empty VM. A model miss is fatal, reported
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
consumes (`noautofire`, `ovradjust`, `steeringmode`, `ai_limit`, …)
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

* **Configured skirmish unit limit (user-requested policy).** Default 1000
  per player instead of retail's 250, configurable without a UI gadget via
  top-level JSON `unitLimit` or `--unit-limit`. Both desktop entry (including
  direct `--map`, captures and `--headless`) and the displayless command accept
  the override. Precedence is explicit CLI, saved setting, default. Existing
  stored choices are preserved; normal menu settings writes retain the active
  configured value. Settings clamp to 20..3276; CLI values outside that range
  are rejected. Ten player slices at 3276 fit positive signed 16-bit occupancy
  identities (movement's `occupancyWord` rejects larger IDs). This replaces only
  the configured default/range in `[08 R-SKIR-01 §6]`; campaign OTA `maxunits`
  and save-restoration semantics retain their own sources. The simulation
  benchmark keeps its explicit default of 400 for workload comparability.
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

**Ordered roots and host discovery (Nanolathe policy).** The user-requested
startup extension accepts repeated `--root` in both commands. Command-line
order is load order: root priority is compared before archive/loose tier and
within-tier order. A later root's HPI can therefore shadow an earlier root's
GP3, CCX, or loose file. This precedence divergence applies only when more
than one root is selected; the single-root policy and retail catalog admission
rules are unchanged. An explicit mod root need not contain a base install.
Equal full provider paths still keep the first mount (C3). `--remaster` wins
above all roots, even with many roots. The content context retains the entire
list for battle restarts; the first root remains the save directory.

`internal/install.Resolve` owns host discovery, outside simulation and retail
evidence. Explicit flags win, then a nonempty `NANOLATHE_TA_ROOT` selects one
root. Otherwise bounded searches find likely installed copies, require a
case-insensitive `totala1.hpi` file, deduplicate physical locations, and return
all matches in deterministic order. Missing candidates are skipped; an empty
result reports searched paths and requests `--root`. Detection is a candidate
check: normal VFS mounting and required-product checks still validate the data.
No whole-disk recursive scan is performed, and arbitrary custom directories
remain selectable through `--root`. Windows-style custom Steam library paths
inside Wine metadata also require explicit roots; discovery reads native host
paths from Steam metadata.

Discovery checks Windows registry hints and standard/platform installation
locations first, including Steam libraries, GOG game collections, and known
Wine prefixes. It then checks the launch and executable directories (including
nearby named game folders), and finally `~/TotalAnnihilation`, which remains a
convenient place for macOS and Linux users to put their data. Directory children
are visited lexically; Steam libraries follow their metadata order. All detected
roots participate, and later matches override earlier ones. Use explicit roots
to select one installation or control the order yourself.

| Host | Additional locations searched |
|---|---|
| Windows | Conventional game, Cavedog, GOG and Steam folders on fixed drives; Program Files variants; registered Steam and game installation paths |
| Linux | Steam defaults, XDG and Flatpak Steam directories, configured Steam libraries, Proton prefixes, Lutris and Bottles locations, CrossOver bottles |
| macOS | Steam and configured libraries, system/user Applications folders, CrossOver and Whisky bottles |
| Wine hosts | `WINEPREFIX`, `~/.wine`, prefixes under home Games directories and `CX_BOTTLE_PATH`; conventional Windows game folders within each prefix |


Host limits and compatibility boundaries are recorded under [I11]:

* **Path ergonomics.** The overlay accepts both slash styles, folds `.` and
  rejects `..`; retail splits on backslash only and gives `.`/`..` no meaning.
  No shipped archive contains a `/`, `.` or `..` entry, so the window is
  unexercised, and archive-internal indexing keeps retail's rule (C7).
* **Bounds.** Directory offsets must be in range, decompressed size is capped,
  and HPI directory cycles are detected. Format readers have host limits;
  3DO, GAF and TDF bound nesting, 3DO iterates sibling lists, and 3DO/GAF
  budget aggregate geometry or references. These are Nanolathe host-safety
  policy, not recovered retail limits, and must not reject stock assets.
  The whole-install format walk checks that compatibility boundary [I11].
* **Expansion limits — user-authorized host divergence [I11].** Acyclic shared
  references can cause exponential work even when cycles are rejected. HPI
  indexing therefore budgets every traversed entry, including hidden duplicate
  branches, directory depth including the root, and aggregate name scanning and
  path construction. Defaults are 65,536 entries, depth 64 and 16 MiB of string
  work per archive. Both name scans include the terminator; path work charges
  the logical and original joined lengths before normalization/allocation.
  Exceeding a limit fails the archive index instead of publishing a partial
  provider. The existing raw-directory and decoded-file limits remain separate.
* **GAF row compatibility.** Metadata and pixel loading share the bounded
  retail row walk `[fmt gaf]`: clipped run lengths, empty rows, zero-progress
  skips and stored next-row anchors agree in both readers. Zero-size leaves
  retain geometry without pixel reads; composites retain their children.
  File bounds remain mandatory. The host `MaxRLECommands` policy defaults to
  128 Mi commands per bank across unique frames, including zero-progress
  commands and overlapping row scans; zero selects the default.
* **GAF expansion limits.** Metadata validation computes each subtree's full
  traversal cost from cached child costs, counting shared children once per
  reference without actually expanding them. Checked additions reject a graph
  before pixel materialization if it exceeds 65,536 expanded frame visits or
  128 Mi expanded source pixels per bank, summed over distinct top-level frames.
  Repeated top-level pointers share one cached variant and are charged once;
  shared descendants of distinct roots are charged to each root. Existing
  unique-frame storage and depth-64 budgets still apply. Metadata and full
  decoding accept the same inputs. These bounds also constrain downstream
  recursive resampling and drawing of loaded assets; presentation scaling still
  multiplies the source-pixel cost. `GAFLimits` permits explicit overrides;
  zero for either new expansion field selects its default, never unlimited work.
  Accepted frame order and composition remain unchanged.
* **Expansion-limit evidence and remaining unknowns.** The installed-asset
  census found HPI maxima of 1,955 traversed entries, depth 4 and 130,578 string
  bytes across 30 archives. The 426 visible GAF banks used at most 5,183 expanded
  visits (`anims/trees.gaf`) and 78,223,933 expanded source pixels
  (`anims/ur-buildings1.gaf`). These observations justify default headroom, not
  retail limits or a fixed inventory requirement. Authored regressions exercise
  exponential sharing, budget boundaries, cached subtrees and counter overflow.
  Retail acceptance of shared HPI nodes and nested GAF layouts remains
  **Unknown** in `[02 R-MALF-01 §3]` and `[02 R-MALF-01 §6]`; host rejection
  limits do not require those layouts to be classified first.
* **Text terminators.** The parser scans forward to the next semicolon across
  newlines; reaching EOF without one is a fatal syntax error
  `[02 R-MALF-01 §4]`. Empty unit identity is preserved through discovery and
  secondary resource selection as established in [02 R-CAT-01 §5]; there is
  no filename-stem substitution.

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

Open questions are maintained at their code sites and in the owning research
category's "Missing and unknown" list. A package-wide absence of markers is
not a completion claim. Current examples include high-byte locale comparison
in the VFS, TDF/GAF readers and content lookup, GAF nested/alternate raster
consumers. These are distinct from the established contracts above.

* **Host-specific enumeration order** remains an explicit deterministic
  substitute (SC3). The manifest identifies the winning provider.
* **Excluded platform/session work** includes the CD-ROM discovery tier and
  multiplayer OVR checksum exchange (§3.6); neither is required for supported
  single-player startup.
* **Behavior owned elsewhere** stays at its owner: model/GAF rendering and
  sound in doc 03; GUI callbacks and localization presentation in doc 07;
  limits and build admission in doc 05. Consult those closures before treating
  an older loader-side question as unresolved.
* **Format follow-ups** remain separate review units: U18 owns the COB trailing
  header wording, and REND-11 owns the WAV wrapper/chunk discrepancy. `[fmt cob]` and `[fmt wav]` own byte layout;
  this design does not restate it.
