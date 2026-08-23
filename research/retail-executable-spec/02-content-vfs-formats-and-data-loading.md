# Retail content, VFS, formats, and data loading

This document specifies the content-facing behavior of the retail executable.
It is a clean-room behavioral contract, not a transcription of
implementation. Every statement is derived
from static analysis of the retail executable: its parsers, loaders, relocation
code, and embedded string vocabulary. No other engine, no capture, and no
original game asset is used as evidence.

Byte offsets, record strides, key names, default values, and unit conversions
appear here in full. They are properties of the retail *data*, not positions
inside the executable, and a reimplementation cannot read retail content
without them.

Each section separates three levels of certainty:

* **Established fact** means the behavior is directly visible in the retail
  decompilation or in a bounded executable-string census.
* **Supported inference** means the behavior follows from several direct
  observations but still needs focused executable analysis.
* **Unknown** means the retail contract has not been established and an
  implementation must retain an explicit placeholder rather than silently
  inventing behavior.

The executable is an ANSI-era Windows program. Paths, identifiers, and text
must therefore be treated as byte strings with case-insensitive comparison
where this contract says so; do not silently substitute a modern Unicode or
case-sensitive policy.

## 1. Startup and content discovery

### Established fact

Startup performs these content-facing phases in the order established in
document 01 §2.1:

1. Establish the process/runtime environment and enforce one active instance
   using the named semaphore `Total Annihilation` (existing semaphore causes
   immediate `-1` without a window).
2. Seed the CRT/TLS random stream, then parse the command line (capturing a
   bare language token when present).
3. Resolve startup command/path information and establish the content root by
   calling `GetModuleFileNameA` into a 256-byte stack buffer, retaining the
   directory component through the final backslash, and calling
   `SetCurrentDirectoryA` on it. Neither return value is checked; on success
   the content CWD is exactly the executable directory independent of the
   inherited launch directory. Failure/truncation is not a deliberate fallback.
4. Create the display context and application window (popup style
   `0x90080000`/`0x00040000`, `CS_DBLCLKS`) before content mounting.
5. Install the fixed 30-unit scaled timebase.
6. Mount the virtual file system (see §2).
7. Resolve language with precedence command line, then registry value
   `language`, then English fallback, and load the translation table.
8. Read the remaining persistent registry preferences and select
   display/sound/music/game-speed options.
9. Initialize audio, display surfaces, the generic TDF parser, packet tables,
   fonts, GAF roots, sound aliases, catalogs, side data, and GUI state.

The Park–Miller simulation stream is seeded at battle entry, not here.

The normal display coordinate space is 640 by 480 logical pixels. The
executable reads the actual display dimensions but keeps GUI and HUD layout
in this logical coordinate system.

The current directory is part of the path-resolution contract: loose files
located relative to the resolved game root are considered before archive
providers. A reimplementation must make the resolved root explicit in its
content context and must not let the process working directory vary between
front-end, map, and multiplayer code paths. A safe reimplementation may fail
with a diagnostic on module-name retrieval truncation or `SetCurrentDirectoryA`
failure rather than reproducing unsafe memory reads.

Missing core resources are reported through a fatal content-error path. The
known hard requirements include `MOVEINFO.TDF` and `GAMEDATA.TDF`. The
translation table `gamedata\translate.tdf` is **not** a hard requirement:
missing data leaves an empty table and lookup returns the source string
unchanged (byte-exact compare). Optional animation, sound, and presentation
resources can degrade through separate paths.

### Supported inference

The root should be captured once during startup and passed to every content
consumer. This avoids a subtle retail-compatible failure mode in which a
relative path resolves differently after a lobby or save/load transition.
The content context should retain the winning provider for every opened
logical path, because save metadata and multiplayer identity use the resolved
content set.

### Unknown

The exact command-line path grammar beyond the bare language token, the
complete handling of a current-directory switch named by command-line option
`C`/`c` (which involves `online.dll`/`ONLGetVersion`/`ONLLoadConfigFile` and a
version gate), and behavior when the executable is launched from a non-install
directory remain unknown. Directory fallback on `GetModuleFileNameA`/
`SetCurrentDirectoryA` failure is not a deliberate retail fallback. The exact
fatal-versus-recoverable classification for every remaining resource family
is also incomplete beyond the classifications stated in this document,
though `MOVEINFO.TDF`/`GAMEDATA.TDF` are fatal while the
translation table is explicitly optional.

## 2. Virtual file system and provider precedence

### Established fact

The VFS has a singleton context containing an ordered provider list. The
observed mount/search sequence, established by the mount append order, is:

1. Loose host files (tried first on every open via `fopen`).
2. The revision/patch archive `rev<name>.GP3` with keep-open flag 1.
3. Every `*.CCX` with keep-open flag 1.
4. Every `*.UFO` with flag 0.
5. Local `*.HPI` with flag 0, up to ten successfully mounted archives (the
   eleventh successful local HPI is not mounted).
6. `*.hpi` discovered on each `DRIVE_CDROM` drive with flag 0.

The exact mounted revision and base archive names are installation-dependent
and discovered through wildcard enumeration. The observed open path tries a
loose host file first, then scans mounted archive providers from index 0
upwards and returns the first matching provider; the keep-open flag does not
alter precedence. A provider flagged 0 is closed immediately after validation
and lazily reopened on next file open; a provider flagged 1 remains open. A
separate validation pass temporarily reopens closed providers to test
availability, prunes failures, and closes successful validation opens again.
Within one wildcard group host enumeration order is `FindFirstFileA` order,
which retail does not sort and is therefore not a portable
executable-defined order.

Mounting deduplicates archives. Before a candidate provider is appended, its
full path is canonicalized and compared case-insensitively against the
already-mounted providers; an equal full path suppresses the second mount.
Append order is precedence order.

The CD-ROM tier scans every drive reported as a CD-ROM and offers its
`%c:\*.hpi` discoveries for mounting. Acceptance of a CD in the full
bootstrap is additionally gated by an identity check that reads
`<drive>:\TOTALA.ID` and inspects its `Contents` content (supported
inference: the gate lives in the loader adjacent to the mount loop; its exact
sequencing relative to mounting is not traced).

The VFS supports both single-file reads and union enumeration. Enumeration
is used for catalogs, maps, campaigns, GUI files, save slots, and sound
aliases; its results deduplicate by canonical entry name under
case-insensitive equality — the first physical backing wins — and filter the
host filesystem's internal pseudo-entry names and directory entries. A
provider can be rejected during mounting; invalid providers are
pruned before the final VFS is exposed. Union visibility is rebuilt by
recursively clearing mutable entry bit 2 before rebuilding; the installed
corpus contains only persisted entry flags 0 for files and 1 for directories
across 303 directories and 7,889 files — other persisted bits are synthetic
and their retail handling remains unknown beyond the keep-open flag.

Path and identifier matching is case-insensitive. Wildcards are supported.
The implementation must retain the logical requested path separately from the
provider path so diagnostics can identify both the request and the winning
source.

### HPI-family container format

Every archive extension listed above uses the same container. The reader
validates the container, never the extension.

**Header, 20 bytes at offset 0:**

| Offset | Size | Meaning |
|---:|---:|---|
| 0 | 4 | Tag, the ASCII bytes `HAPI` |
| 4 | 4 | Version. The only accepted byte pattern is `00 00 01 00`. |
| 8 | 4 | Directory-blob size in bytes, counted from file offset 0 |
| 12 | 4 | Obfuscation key; only the low byte participates |
| 16 | 4 | File offset of the root directory record |

**Footer:** the reader seeks to the end of the file, reads 36 trailing bytes,
finds `0000` within the template `Copyright 0000 Cavedog Entertainment`,
overwrites the corresponding four footer bytes with literal `0000`, and then
requires the normalized footer to equal the template. The accepted shape is
therefore `Copyright <any four bytes> Cavedog Entertainment` with no digit
check. A mismatch in the tag, the version bytes, or the normalized footer
rejects the archive; the provider object is released and never enters the
mount list. **Installed-corpus observation** (this corpus only, not a claim
about every edition): 30 root archives use footer years `1997:3`, `1998:25`,
`1999:1`, `2000:1`.

**Mount-time read-length handling:** the initial 20-byte header read, footer
read, and directory-blob read ignore their return counts; later payload reads
do have explicit length checks. A safe reimplementation must treat short
metadata reads as rejection without claiming retail checked them.

**Directory blob.** The reader allocates *directory-blob size* bytes and reads
that many bytes from offset 0, so the blob contains the header. It then derives
the working key from the stored key byte:

* a stored key byte of zero means the archive is not obfuscated and no
  transform is applied anywhere in it;
* otherwise the working key is `~((k >> 6) | (k << 2))` in eight-bit
  arithmetic, and it replaces the stored byte in memory.

Every byte of the blob from offset 20 onward is then transformed in place:

```
plain[0x14 + i] = ((i + 0x14) & 0xFF) XOR key XOR (NOT cipher[0x14 + i])
```

**Directory records.** The root-directory offset field is biased by the blob
base, giving a pointer to a directory record:

| Offset | Size | Meaning |
|---:|---:|---|
| 0 | 4 | Entry count |
| 4 | 4 | File offset of the entry array |

The entry-array offset is biased by the blob base. Each entry is **nine
bytes**:

| Offset | Size | Meaning |
|---:|---:|---|
| 0 | 4 | File offset of the entry's NUL-terminated name |
| 4 | 4 | File offset of the entry's data: a file record, or a nested directory record |
| 8 | 1 | Flags; bit 0 set means this entry is a subdirectory |

Both offsets in every entry are biased by the blob base, and every subdirectory
is processed recursively at load time. Names live in a contiguous pool that the
name offsets point into.

**Lookup.** A requested path is split on **backslash only**; a forward slash is
an ordinary name character and never separates components. Each component is
compared case-insensitively against the current directory's entries, scanned
**from the last entry backwards**, so where one directory declares the same
name twice the later declaration wins. A non-final component must have the
subdirectory flag set or the lookup fails. There is no special treatment of `.`
or `..`: they are matched as literal names and normally fail, so
archive-relative traversal is not a retail behavior.

**Payload obfuscation.** When the archive key is nonzero, file bytes are
transformed on read using the byte's absolute offset within the archive:

```
plain[i] = ((i + fileOffset) & 0xFF) XOR key XOR (NOT cipher[i])
```

**Compression.** A file entry records whether its payload is stored (`0`) or
compressed (nonzero); the chunk header selects the actual decoder and retail
does not require the two method numbers to match. A compressed payload is cut
into chunks of 65,536 decompressed bytes; a per-file table stores one `u32`
stored size per chunk and chunk N's archive offset is the sum of sizes
`0..N-1`. A read resolves the chunk containing the current position,
decompresses it into a per-handle buffer, and copies the requested slice out.
The handle keeps that one decompressed chunk and reuses it while the position
stays inside it, so sequential reads decompress each chunk once. A chunk whose
stored length does not read back completely, or whose decompression fails,
aborts the read with the all-ones failure value after reporting a diagnostic
naming the chunk index, chunk count, length, and file.

**Chunk wire format (`SQSH`, 19-byte header + payload):**

| Offset | Size | Meaning |
|---:|---:|---|
| 0 | 4 | `SQSH` tag |
| 4 | 1 | unknown/version byte, copied but not validated |
| 5 | 1 | method: 1 = LZ77, 2 = zlib (4 or greater is rejected) |
| 6 | 1 | payload encoding flag |
| 7 | 4 | compressed payload length |
| 11 | 4 | expected decompressed length |
| 15 | 4 | unsigned byte-sum checksum over the payload |

Payload bytes are decoded as: verify `SQSH` and method range, verify checksum
over the payload before optional decoding, undo encoded payload bytes with
`(byte - index) XOR index`, dispatch method 1 to a 4,096-byte ring LZSS variant
(write cursor 1, LSB-first tag bits, 2..17-byte matches, match position zero as
terminator) or method 2 to a zlib stream, then verify output length. Result
codes are 0 success, 1 bad marker, 2 checksum mismatch, 3 output-length
mismatch, and 4 method byte out of range.

**Installed-corpus observation:** 9 stored file records, 2,450 marked method 1,
5,430 marked method 2; across 35,805 `SQSH` chunks, 9,630 use method 1 and
26,175 use method 2; every `+4` byte is 2 and every encoding flag is 1, with no
file/chunk method mismatches in this corpus. Both methods are required to load
the installed game.

**Sharing and failure.** The reader is shared by every content decoder. A
failed open or short read propagates to the caller; the caller decides whether
the failure is fatal, optional, or a missing-content warning.

**Safety note for a reimplementation.** Retail validates the tag, the version
bytes, and the normalized footer, but it does not bound-check relocated offsets
against the blob size, does not detect directory cycles, ignores mount-time
read counts, and trusts compressed-size fields enough to checksum the stated
payload length. An implementation must add those checks — name and data offsets
inside the blob, monotonic chunk tables, bounded decompressed sizes, and cycle
detection on subdirectory links — without changing which archives are accepted.
Every one of the 30 installed root archives must pass the safe reader; synthetic
out-of-range offsets, cycles, integer overflow, short metadata, oversized chunks,
and unterminated names must fail without memory-unsafe access.

### Supported inference

The provider identity should be part of a content manifest. A logical path
alone is insufficient for multiplayer or deterministic save identity because
two installations can resolve the same path to different archive bytes.

### Unknown

The container, its cipher, its directory shape, its duplicate rule, its
separator rule, its absence of traversal handling, the mount append order and
keep-open flag semantics (flag 1 keeps open, flag 0 closes/lazily reopens;
precedence is unaffected), and the footer four-byte wildcard are established
above. What remains open is narrower: enumeration order within one wildcard
group is decided by the host directory listing, which retail does not sort, so
it is not a property of the executable; the entry flag bits beyond the
subdirectory bit and the mutable enumeration-visibility bit (bit 2,
recursively cleared before union rebuild) are not enumerated for synthetic
values; the exact recovery behavior for a structurally malformed but
header-valid archive is untraced (see safety note); whether any shipped
archive variant outside the installed corpus departs from the container above
has not been established from the executable alone; and the exact sequencing
of the `TOTALA.ID` CD identity gate relative to the mount loop is not fully
traced (the gate itself is carried as supported inference above).

## 3. Registry configuration, language, and localization

### Established fact

Persistent settings live in the Windows registry under the current user, in
`Software\\Cavedog Entertainment`. Two subkeys are used: `Total Annihilation`
for everything below, and `Total Annihilation\\Skirmish` for the skirmish
setup.

One shared helper performs every access. It creates the three key levels on the
way down, so **reading a value creates the key path if it is missing**. Reading
requests read access; writing requests set-value and create-subkey access. A
read that fails because the caller's buffer was too small is still reported as
success.

Startup reads each setting, and **when a setting is absent it installs the
default below and immediately writes it back**, so the registry becomes fully
populated on first run.

**Scalar settings and their defaults**

| Value name | Default |
|---|---|
| `Interface Type` | 0, clamped to at most 1 |
| `DisplaymodeWidth` | 640 |
| `DisplaymodeHeight` | 480 |
| `DisplaymodeDepth` | read; no scalar default installed |
| `Difficulty` | 1 |
| `Gamma` | 12 |
| `scrollspeed` | 32 |
| `mousespeed` | 10 |
| `gamespeed` | 10 |
| `textlines` | 10 |
| `textscroll` | 10 |
| `unitchat` | 10 |
| `unitchattext` | 5 |
| `Movie Output Rate` | 10 |
| `fxvol` | 27 |
| `musicvol` | 32 |
| `cdmode` | 4 |
| `MixingBuffers`, `CDAudioVolume`, `WaveOutVolume`, `Sound Mode` | read; no scalar default installed |
| `SingleCommanderDeath`, `SingleMapping`, `SingleLineOfSight`, `SingleLOSType` | 1 |
| `MultiCommanderDeath`, `MultiMapping`, `MultiLineOfSight`, `MultiLOSType` | 1 |
| `screenchat` | 1 |
| `PlayMovie` | 1 |
| `NumSkirmishPlayers` | 4 |
| `Nickname`, `Game Name`, `Password` (17-byte buffers), `Image Output Directory` | empty |

**Flag settings.** Several options are bits of packed option words rather than
independent values. Their installed defaults are: anti-aliasing, shadows,
vehicle shadows, feature shadows, and shading **on**; dithered fog, damage
bars, alt-switching, clock display, and volume restoration **off**;
acknowledgement effects, build effects, and speech effects **on**; music mode
**on**.

**Skirmish settings**, under the skirmish subkey: `SkirmishMap`,
`SkirmishLocation`, `SkirmishDifficulty`, `SkirmishLOSType`,
`SkirmishLineOfSight`, `SkirmishMapping`, `SkirmishCommanderDeath`, and the
per-slot values `Player%dController`, `Player%dSide`, `Player%dColor`,
`Player%dAllyGroup`, `Player%dMetal`, `Player%dEnergy`, where the format is
filled with the slot index. Absent per-slot values install these defaults:
controller 0, ally group 5, metal and energy 1000, color the slot index
itself, and side the slot index masked to parity (slot & 1).

`NumSkirmishPlayers` deserves precision. A missing value installs the default
4. The loader then compares the stored value against the documented 2..10
window, but **both comparison branches store the raw value unchanged**: the
range validation is a compiled no-op, out-of-range values persist unclamped,
and consumers iterate whatever was stored. Retail behavior is
accept-and-store; a reimplementation must decide deliberately whether to
reproduce that quirk.

`Nickname`, `Game Name`, and `Password` are read into bounded **17-byte**
buffers.

Other named values include `Games` and `AllMissions` (campaign progress
gating), and `user_images`, which participates in building the image output
path with the pattern `<directory>\\<name>`.

A unit limit of 250 is applied before clamping elsewhere in the startup path.

The language setting is read as a string. An empty value selects English.
The language string is kept in one global buffer and drives two distinct
mechanisms.

**Translation table.** The translation loader takes a TDF path and the language
name. If the requested name equals the current one, case-insensitively, it does
nothing. Otherwise it releases the existing table, allocates a new one, stores
the new language name, and parses `gamedata\\translate.tdf` with the generic
parser. It then enumerates every top-level section by index; for each section
the **section name** is the source string and the value of the key **named by
the language string** is the translation. A section whose language key is
absent or empty contributes nothing. Entries go into a sorted map keyed by the
source string, inserted if absent and overwritten if already present, so a
repeated section name keeps the last translation.

Lookup takes a source string and returns the mapped translation, or the input
unchanged when no table is loaded or no entry matches. This lookup compares
**byte-exactly**, unlike every other name comparison in the content layer,
which is case-insensitive.

**Language-prefixed authored keys.** The same language string is also used as a
*key prefix* by a dedicated string accessor. That accessor builds
`<language><key>`, looks that up first, and falls back to the plain `<key>`.
Authored records therefore carry localized variants in the same section as the
base field — a `German` language setting reads `Germanname` before `name`. With
the default empty language string the prefixed key is identical to the plain
key, so English content needs no special case.

The unit catalog reads its display name and description through this accessor,
so localized unit names and descriptions authored in the unit record **are**
honored. This corrects an earlier reading that localization was handled only by
the translation table.

### Supported inference

All user-visible runtime messages should pass through the translation map
before rendering. The exact callers are not fully enumerated, but the
translation loader is clearly separate from unit-catalog localization.

### Unknown

The registry hive, key path, value names, defaults, and write-back behavior are
established above, as are missing-translation fallback (return the source
string unchanged; missing `translate.tdf` leaves an empty map and is not fatal)
and duplicate policy (last section wins). Language precedence is command-line
bare token, then registry `language` value when the token is absent, then
English fallback (`english`). What remains incomplete: the language-selection
interface, the installed-corpus language vocabulary (observed: `french`,
`german`, `italian`, `piglatin`, `spanish` in the installed translation table;
plus four `japanesename` prefixes in FBI records with no corresponding
translation-table key in this corpus — not a claim about every edition),
code-page behavior for high bytes, language-specific font fallback, the
precedence among registry/INI/command-line for non-language configuration, and
which runtime messages pass through the translation lookup.

**Installed-corpus observation:** 21 font filename spellings (20
case-folded identities); both startup-required `COMIX.FNT` and `SMLFONT.FNT`
are present in this corpus.

## 4. Generic TDF grammar and semantics

### Established fact

The retail grammar is:

* A section begins with a bracketed name and a brace-delimited body.
* A field is a key, equals sign, value, and semicolon.
* Comments may use line or block form.
* Spaces, tabs, carriage returns, and line feeds are whitespace.
* There is no quoted-string or escape syntax in the observed grammar.

Before parsing, comments are blanked: `//`-to-end-of-line and `/* ... */`
spans are overwritten with ASCII spaces **preserving every character offset**,
so all downstream offsets see text of unchanged length. Comments cannot
appear inside a value, and inline trailing comments after the `;` are blanked
too. An unterminated `/*` blanks everything through end of file. Sections and
keys are stored in sorted vectors using case-insensitive comparison.
Original source order is not a semantic property of the retail tree.

Repeated sections remain separate nodes. A first-match section accessor is
common — it linear-scans and returns the first matching sibling, duplicates
retained — while explicit enumerators can visit every section.

Key insertion binary-searches the section's sorted key vector with the
case-insensitive comparison; on a fold-equal hit, a second byte-for-byte
case-sensitive compare decides. Identical spelling replaces the value in
place: last write wins, one entry. A case-variant spelling is inserted as a
second distinct sorted entry, so variants coexist. Typed lookups return the
lower-bound entry — the first variant in case-insensitive sort order — which
is deterministic but not last-wins (supported inference: the mechanism is
directly visible; accessor return among variants still needs black-box
confirmation). Stock content does not rely on that edge case.

**Malformed input and failure behavior.** The tokenizer reports parse errors
under the exact title `Parse error in .TDF File!`, with detail lines shaped
` - <name> = '<value>' from file <file>`. Five parse diagnostics exist,
verbatim:

1. `Data field - '=' not found`
2. `Data field - ';' not found`
3. `Sub-record - closing ']' not found`
4. `Sub-record - opening '{' not found`
5. `End of file - nextblock not zero`

All five emit through one message-box diagnostic family. A failed load never
terminates at the parser: the caller receives a valid-but-empty tree, so
subsequent typed reads return their defaults. The caller decides whether that
state is fatal for its resource family.

### Typed accessors

Every typed read goes through one of a small fixed family of accessors. All of
them locate the key the same way: a **binary search** over the section's sorted
key/value vector using the case-insensitive comparison, followed by a
confirmation compare. Sections are located by a **linear** case-insensitive
scan. A key whose stored value pointer is absent is treated as missing.

| Accessor | Absent key | Present key |
|---|---|---|
| Integer | returns the caller's default | CRT decimal integer conversion: optional sign, leading whitespace, decimal digits, trailing junk ignored, zero for unparsable text; no hexadecimal syntax |
| Floating | returns the caller's default | CRT decimal floating conversion, returned in x87 extended precision, so any caller scaling happens before the narrowing store |
| **Fixed-point** | stores the caller's default **verbatim** | floating conversion, multiplied by 65,536, truncated toward zero — the caller's default is therefore already in 16.16 units |
| String | copies the caller's default **without applying the length limit**, and reports "defaulted" | bounded copy to the caller's limit with forced termination, and reports "found" |
| Raw | reports absent | returns the stored text pointer, which is how a caller distinguishes an authored key from a missing one |
| Language-prefixed string | falls through to the plain key, then to the string-accessor rules above | as the string accessor, after trying `<language><key>` first |

Consequences a reimplementation must preserve: an integer or floating field
cannot distinguish an authored zero from an absent key, because both yield the
caller default; a string field can, because the accessor reports which path it
took; and the fixed-point accessor's default is not in the authored unit
system, so a default of 65,536 means an authored value of 1.0.

Other typed behavior:

* An explicitly present empty string differs from a missing key in the raw
  tree.
* There is no boolean parser. Boolean fields are numeric and nonzero means
  true.
* Unknown fields are retained by the parser but ignored by a caller that does
  not request them.

The implementation should preserve raw text, original spelling, source file,
provider, duplicate history, parsed value, and the caller default used. This
is necessary to distinguish missing, empty, valid, malformed, defaulted, and
derived values even though retail runtime structures often collapse those
states.

### Supported inference

The parser should expose both a lossless tree and typed convenience accessors.
The lossless tree must retain duplicate sections and case-variant keys even
if a compatibility accessor follows retail first/last behavior.

### Unknown

Floating `INF`/`NAN` and malformed exponent behavior, line-length limits, and
caller-specific duplicate-section merging policies are not established. The
duplicate-key winner mechanism, the parse-diagnostics set with its title and
empty-tree failure policy, and comment-blanking offset preservation are now
established above.

## 5. Catalog construction and linking

### Established fact

Retail content loading is two-stage:

1. Discover files/sections and create stable catalog records.
2. Parse typed fields, apply defaults, and resolve cross-references.

This avoids directory enumeration order deciding whether a reference can be
resolved. Known links include unit weapons, corpses, movement classes,
feature successors, 3DO objects, GAF animation sequences, and sound aliases.

Catalog construction must be case-insensitive for names. Unknown keys should
remain in the source tree even when the runtime catalog ignores them.

### Unit record (`.fbi`)

Each unit file contributes one `UNITINFO` section. The record below lists every
key the executable reads, its accessor, and its default. "Fixed" means the
16.16 accessor, so the authored value is multiplied by 65,536 and truncated,
and the stated default is already in 16.16 units.

**Identity and presentation**

| Key | Accessor | Default | Notes |
|---|---|---|---|
| `unitname` | string, 32 bytes | empty | canonical catalog name |
| `name` | language-prefixed string, 32 bytes | empty | display name |
| `description` | language-prefixed string, 64 bytes | empty | |
| `side` | string, 30 bytes | empty | faction tag; consumed by the build-pick filter below |
| `objectname` | string, 32 bytes | empty | 3DO model name |
| `category` | string, 100 bytes | empty | category token list |
| `soundcategory` | string, 100 bytes | empty | resolves to a sound-category index |
| `corpse` | string, 100 bytes | empty | resolves to a feature identity |
| `movementclass` | string, 100 bytes | empty | resolves to a movement class |
| `weapon1`, `weapon2`, `weapon3` | string, 128 bytes each | empty | resolve to weapon identities |
| `explodeas`, `selfdestructas` | string, 128 bytes each | empty | resolve to weapon identities |
| `YardMap` | string, 1,024 bytes | empty | occupancy map text |
| `defaultmissiontype` | string, 100 bytes | empty | |
| `wpri_badTargetCategory`, `wsec_badTargetCategory`, `wspe_badTargetCategory`, `noChaseCategory` | string, 100 bytes each | `none` | per-slot exclusion categories |

`side` is read by the catalog loader, not the per-unit compiler. Its one
located consumer is a weighted build-choice roulette over an acting unit's
build list: cumulative weights select a candidate through the simulation RNG,
and the winning candidate's `side` string is then compared byte-for-byte —
case-sensitively — against the acting unit's own; any mismatch rejects the
pick. The enclosing routine feeds the result into the unit-definition table,
consistent with strategic-AI build selection; its exact identity is a
residual (supported inference that it is AI-side).

**Economy**

| Key | Accessor | Default |
|---|---|---|
| `buildcostenergy`, `buildcostmetal` | integer | 0 |
| `energymake`, `energyuse`, `metalmake`, `extractsmetal` | floating | 0.0 |
| `windgenerator`, `tidalgenerator` | floating | 0.0 |
| `energystorage`, `metalstorage` | floating | 0.0 |
| `makesmetal` | integer | 0 |
| `buildtime` | integer | 0 |
| `workertime`, `healtime` | integer | 0 |
| `cloakcost` | integer, stored as floating | 0 |
| `cloakcostmoving` | integer, stored as floating | the value just read for `cloakcost` |

**Movement and geometry**

| Key | Accessor | Default |
|---|---|---|
| `maxvelocity`, `brakerate`, `acceleration` | fixed | 0 |
| `bankscale` | fixed | 65,536, i.e. 1.0 |
| `pitchscale` | fixed | 0 |
| `damagemodifier` | fixed | 65,536, i.e. 1.0 |
| `moverate1`, `moverate2` | fixed | twice the `maxvelocity` value just read |
| `turnrate` | integer | 0 |
| `waterline`, `cruisealt` | integer | 0 |
| `transportsize`, `transportcapacity` | integer | 0 |
| `buildangle`, `builddistance`, `sortbias` | integer | 0 |
| `maneuverleashlength`, `attackrunlength`, `kamikazedistance` | integer | 0 |

**Combat and sensors**

| Key | Accessor | Default |
|---|---|---|
| `maxdamage` | integer | 0 |
| `sightdistance`, `radardistance`, `sonardistance` | integer | 0 |
| `radardistancejam`, `sonardistancejam`, `mincloakdistance` | integer | 0 |

**Flags and postures.** All of the following use the integer accessor with
default 0 and are consumed as booleans, except the two standing orders, which
default to 2:

`standingmoveorder`, `standingfireorder`, `init_cloaked`, `downloadable`,
`builder`, `stealth`, `bmcode`, `zbuffer`, `isairbase`, `istargetingupgrade`,
`teleporter`, `hidedamage`, `shootme`, `armoredstate`, `activatewhenbuilt`,
`canfly`, `canhover`, `upright`, `floater`, `amphibious`, `isfeature`,
`noshadow`, `immunetoparalyzer`, `hoverattack`, `antiweapons`, `digger`,
`onoffable`, `mobilestandorders`, `firestandorders`, `canstop`, `canattack`,
`canguard`, `canpatrol`, `canmove`, `canload`, `canreclamate`, `canresurrect`,
`cancapture`, `candgun`, `kamikaze`, `norestrict`, `showplayername`,
`commander`, `cantbetransported`.

`wacky` is parsed by the catalog loader into bit 16 of the same packed
definition flag word that carries `norestrict` (bit 15). No reader of that
bit was found anywhere in the reviewed executable corpus: the key is parsed,
stored, and preserved across record moves but semantically inert as far as
static analysis reached.

**Downloadable enforcement.** After build-menu pages compile, the loader
walks every unit definition and compares its name case-insensitively against
every build-menu button name. A match whose `downloadable` bit is clear
raises the exact warning `Hey! Somebody forgot to set downloadable=1 for %s`
once, silently forces the bit on, and re-sorts and re-finalizes the catalog.
A unit reachable from any build menu therefore behaves as downloadable for
the rest of the session.

`selfdestructcountdown` is read with the raw accessor, so the record can tell
an authored value from an absent key.

**Executable-bounded absent fields.** Editor-only fields, unit number and
designation metadata, `noautofire`, `ovradjust`, `steeringmode`, and several
alternate transport names have no reader. They must be retained as unknown
source fields and must not be given behavior. `ai_weight` and `ai_limit` are
retained as raw strings; their strategic consumer is not located.

### Build-menu catalog keys

Build menus are assembled from catalog data, not side data. Per-unit numbered
pages derive from `CANBUILD %s` sections and numbered `canbuild%d` keys,
enumerated from 1 upward; a gap yields a missing page rather than terminating
the loop. The executable's key vocabulary also names `MENU`, `UNITMENU`,
`DOWNLOADMENU`, and `BUTTON` sections consumed while assembling build and
order menus; the download tier additionally keys off the unit `downloadable`
flag (enforcement rule above). Wiring of those sections beyond this presence
is supported inference.

### Weapon record

Weapon files are TDF; each top-level section is one weapon.

**Record identity.** The parser reads `ID` with the integer accessor and a
default of -1 **first**, and uses it to select the weapon record it fills. The
section name is then stored into that record as the weapon's catalog name, and
`name` is read separately as a 64-byte display string. A weapon section without
an authored `ID` therefore selects the slot before the table.

**Unit conversions.** The weapon record is where the authored second-based unit
system is converted to ticks and 16.16 world units. The conversions are exact:

| Key | Accessor | Default | Conversion |
|---|---|---|---|
| `weaponvelocity`, `startvelocity` | floating | 0.0 | multiplied by 65,536/30, truncated — 16.16 world units per tick |
| `weaponacceleration` | floating | 0.0 | multiplied by 65,536/900, truncated — 16.16 world units per tick squared |
| `reloadtime`, `weapontimer`, `burstrate`, `duration`, `randomdecay`, `smokedelay`, `flighttime`, `holdtime`, `shakeduration` | floating | 0.0 | multiplied by 30, truncated — whole ticks; an authored value below 1/30 second becomes zero |
| `turnrate` | floating | 0.0 | multiplied by 1/30, truncated — per tick |
| `minbarrelangle` | floating | -11.25 | multiplied by pi/180 — radians |

**Remaining scalar fields**

| Key | Accessor | Default |
|---|---|---|
| `range` | integer | 32,767 |
| `coverage` | integer | 0 |
| `areaofeffect` | integer | 0 |
| `edgeeffectiveness` | floating | 0.0 |
| `energypershot`, `metalpershot` | floating | 0.0 |
| `burst`, `sprayangle` | integer | 0 |
| `accuracy`, `tolerance`, `pitchtolerance` | integer | 0 |
| `shakemagnitude` | integer | 0 |
| `firestarter`, `rendertype`, `color`, `color2` | integer | 0 |

**Behavior flags.** Integer accessor, default 0, consumed as booleans:
`noautorange`, `soundtrigger`, `guidance`, `tracks`, `lineofsight`,
`ballistic`, `unitsonly`, `groundbounce`, `waterweapon`, `toairweapon`,
`smoketrail`, `turret`, `selfprop`, `propeller`, `noexplode`, `burnblow`,
`twophase`, `cruise`, `commandfire`, `stockpile`, `targetable`, `interceptor`,
`beamweapon`, `shellweapon`, `dropped`, `vlaunch`, `meteor`, `noradar`,
`paralyzer`, `startsmoke`, `endsmoke`.

**Consumed-linking semantics for the combat flags.** The interceptor flag has a
gameplay consumer: an interceptor-flagged blast force-detonates every alive
non-self projectile inside its unhalved `areaofeffect` after the ordinary area
enumeration, and publishes victim signatures for exact-match removal. The aim
scan that reserves victims measures an axis-aligned square of side twice
`coverage << 16` around the incoming projectile's stored aim point; the
authored `coverage` value itself drives only the range-circle overlay display,
never engagement. `firestarter` is read by exactly one site as a nonzero test:
any authored percentage ignites deterministically, so 70 and 100 behave
identically. Document 06 owns the full chains.

**Asset names.** String accessor, 256 bytes, default empty: `model`,
`explosiongaf`, `explosionart`, `waterexplosiongaf`, `waterexplosionart`,
`lavaexplosiongaf`, `lavaexplosionart`, `soundstart`, `soundhit`, `soundwater`.
Each sound name resolves to a sound index, or to the all-ones sentinel when
absent.

**Damage table.** The weapon may carry a nested `DAMAGE` section. Its `default`
key is read with the integer accessor and default 0 and becomes the fallback
damage. Every **other** key in that section is then enumerated: the key is an
armor-class name and its integer value is that class's damage. The class names
are interned into a per-weapon sorted map, so damage lookup is by armor-class
name, not by a fixed index. A weapon with no `DAMAGE` section has a fallback
damage of zero.

`aimrate` and `startfire` have no reader in this executable.

### Feature record

Feature files are TDF; each top-level section is one feature type.

| Key | Accessor | Default | Notes |
|---|---|---|---|
| `Description` | string | empty | |
| `footprintx`, `footprintz` | integer | 0 | occupancy extent in cells |
| `height` | integer | 0 | |
| `object` | string | empty | 3DO model name |
| `filename` | string | empty | sprite source, used when no model |
| `seqname`, `seqnameshad` | string | empty | idle animation and its shadow |
| `seqnameburn`, `seqnameburnshad` | string | empty | burning animation and shadow |
| `seqnamedie`, `seqnamedieshad` | string | empty | death animation and shadow |
| `seqnamereclamate`, `seqnamereclamateshad` | string | empty | reclaim animation and shadow |
| `metal`, `energy` | integer | 0 | reclaim yield |
| `damage` | integer | 0 | hit points |
| `spreadchance`, `reproduce`, `reproducearea` | integer | 0 | fire spread and regrowth |
| `sparktime` | floating | 0.0 | multiplied by 30, truncated — ticks |
| `burnweapon` | string | empty | weapon emitted while burning |
| `animating`, `animtrans`, `shadtrans` | integer | 0 | animation mode flags |
| `flamable` | integer | 0 | spelled with one `m` |
| `geothermal`, `blocking`, `reclaimable` | integer | 0 | `geothermal` has no registry: the requirement is enforced by the building footprint validator, which requires at least one covered terrain cell to hold a feature whose catalog entry carries this flag |
| `autoreclaimable` | integer | **1** | the only feature flag that defaults on |
| `indestructible`, `nodisplayinfo`, `nodrawundergray` | integer | 0 | |

A second pass resolves `featuredead`, `featurereclamate`, and `featureburnt`
— all string, default empty — to catalog identities, creating a late record
when a referenced definition can still be discovered. Missing links use an
explicit no-successor sentinel. When a requested feature record name appears
in no parsed feature node, the loader formats the exact diagnostic
`Record "%s" missing from feature files` and emits it through the fatal
diagnostic path; recovery beyond that dialog is not modeled, so a
reimplementation treats it as fatal.

The fire/reclaim/regrowth keys are all consumed. Reproduction runs inside the
feature tick pass, once per simulation tick, after the catalog animation
advance and before the instance-list walk:

* A single global cell cursor steps once per pass, decrementing by one; the
  wrap stores `Width*Height-1` and evaluates nothing on that tick, so map
  cell `Width*Height-1` is never considered. A full sweep takes
  `Width*Height` ticks.
* A visited cell is eligible when it holds a registered feature reference
  (below the void sentinels) and carries no live instance — filename/GAF
  features at rest only.
* Every eligible visit consumes one simulation-RNG draw out of 100 **even at
  probability zero**; a copy spawns only when the roll is below the
  definition's `reproduce`.
* A passing roll spends two further draws on offsets within `reproducearea`
  around the parent cell (an area of one or less always fails). The
  destination must lie in bounds and be exactly empty — void and filler are
  rejected — and the source cell's marker short must be clear; there are no
  terrain, blocking, water, or capacity checks and no retry.
* The copy is stamped through the standard placement path at terrain-height
  position with a zero health word and placer nibble 10, notifying pathing
  exactly like map-loaded features.

Reproduction is stock-inert only because every shipped feature authors
`reproduce=0` (`reproducearea=6` where present). The one remaining
authored-data question for burning is whether shipped burn-animation
sequences carry a looping flag byte (completion timing depends on it).

### Movement class record

Movement classes come from `CLASS` sections in the movement catalog. Each class
reads eight keys, and **their defaults chain off values read earlier in the same
record**, so read order is part of the contract:

1. `FootPrintX` — integer, default 0, stored as 16-bit.
2. `FootPrintZ` — integer, default 0, stored as 16-bit.
3. `maxwaterdepth` — integer, default: the profile's current value.
4. `minwaterdepth` — integer, default: the profile's current value.
5. `maxslope` — integer, default: the profile's current value, stored as a byte.
6. `badslope` — integer, default: **half the `maxslope` value just read**.
7. `maxwaterslope` — integer, default: the profile's current value, byte.
8. `badwaterslope` — integer, default: **half the `maxwaterslope` value just read**.

Three clamps then run, in order:

* if `maxwaterslope` is below `maxslope`, `maxslope` becomes `maxwaterslope`;
* if the resulting `maxslope` is below `badslope`, `badslope` becomes `maxslope`;
* if `maxwaterslope` is below `badwaterslope`, `badwaterslope` becomes
  `maxwaterslope`.

The executable contains no key evidence for pivot-turn, reverse, arc-turn,
minimum turn radius, or minimum turn speed.

### Sound aliases

The alias catalog is a TDF in the game-data directory. Each top-level section
is one alias; its `sound` key names the sample. Alias registration
deduplicates path/alias entries and is capped at **255** registrations, each
holding a 32-byte alias name. Decoded samples are cached for subsequent
playback; the cache and mixer are separate from the byte-level sample decoder.

### Sound category record

The sound-category catalog is `gamedata\\sound.tdf`. Each top-level section is
one category, named by the section, and a unit's `soundcategory` key selects
one by name.

The engine defines **24 event slots**, of which slot 0 is an unused sentinel
and slots 1 through 23 are real events. Each is identified by an authored key
name and, for some, a default speech caption built into the executable. Each
slot also carries a priority and a cooldown that the audio layer uses;
document 03 covers those.

| Slot | Key | Default speech |
|---:|---|---|
| 1 | `select` | — |
| 2 | `underattack` | Under Attack |
| 3 | `activate` | — |
| 4 | `deactivate` | — |
| 5 | `ok` | — |
| 6 | `arrived` | Arrived |
| 7 | `cant` | Cannot Comply |
| 8 | `unitcomplete` | Nanolathe Complete |
| 9 | `build` | — |
| 10 | `repair` | — |
| 11 | `working` | — |
| 12 | `load` | — |
| 13 | `unload` | — |
| 14 | `cloak` | Cloaked |
| 15 | `uncloak` | Visible |
| 16 | `capture` | — |
| 17 | `count5` | five |
| 18 | `count4` | four |
| 19 | `count3` | three |
| 20 | `count2` | two |
| 21 | `count1` | one |
| 22 | `count0` | zero |
| 23 | `canceldestruct` | Self destruct terminated |

A category record is 352 bytes: a name of up to 64 bytes, then 24 event rows of
twelve bytes indexed by slot, each holding a variant count and two parallel
arrays of 64-byte strings.

**Variant gathering.** For each event key `K`, the loader reads `K` first, then
`K1`, `K2`, `K3`, and so on, stopping at the first index whose key is absent.
Every successful read appends to two parallel growable arrays of 64-byte
strings held by that event: the sound alias, and a caption read from the
companion key `<that key>text` (empty when absent). A category's event
therefore holds an ordered list of variants and their captions, plus the count.

An event key that is absent for the bare form contributes no variants at all,
because the bare read is what gates the numbered loop.


## 6. Interface, side, map, animation, model, and script files

### SIDE and battle interface data

`gamedata\\sidedata.tdf` is parsed as sequential `SIDE0`, `SIDE1`, and so on
until the first missing side. An optional `[GENERAL]` section carries
`baseheight` (integer, default **480**), the logical interface height the
layout assumes.

Per-side scalar keys: `name` and `nameprefix` (strings), `commander` (string,
the unit name of that side's commander), `font` and `fontgui` (strings;
retail sides author `console` and `armbutt`), `intgaf` (string, default
empty), and `energycolor` and `metalcolor` (integers, default 0).

`energycolor` and `metalcolor` are palette indices: they select the inner-bar
palette index when the ENERGYBAR and METALBAR controls draw. No bar mask or
style keys exist in the executable's vocabulary or in retail `sidedata.tdf`.

`intgaf` names the side's interface GAF. When present, the loader opens it and
binds three named entries from it — `PANELTOP`, `PANELSIDE`, and `PANELBOT` —
which are the resource-panel backgrounds drawn at insets 0,0 / 0,128 / 0,448.
Retail sides author `ARMINT`/`CORINT`, whose GAFs carry those entries.

Each side also declares a set of named interface anchors. **Every anchor is a
subsection holding four integer keys named `x1`, `y1`, `x2`, `y2`** — corner
rectangles, not origin-plus-size. All four default to 0.

The anchors read directly are `LOGO`, `ENERGYBAR`, `ENERGYNUM`, `METALBAR`,
`METALNUM`, `TOTALUNITS`, `TOTALTIME`, `ENERGY0`, `METAL0`, `ENERGYMAX`, and
`METALMAX`. A shared helper reads the remainder: `LOGO2`, `DAMAGEBAR`,
`DAMAGEBAR2`, `DESCRIPTION`, `MISSIONTEXT`, `ENERGYCONSUMED`,
`ENERGYPRODUCED`, `METALCONSUMED`, `METALPRODUCED`, `UNITNAME`, `UNITNAME2`,
`UNITENERGYMAKE`, `UNITENERGYUSE`, `UNITMETALMAKE`, `UNITMETALUSE`, and three
numbered reload anchors.

The helper raises the same fatal missing-section diagnostic as the direct
reads, naming the missing section and the side, so **every anchor above is
mandatory**. A missing anchor is a data error, not silently invented geometry.

A side whose font cannot be loaded fails the same way: the font search
combines the `fonts` directory, the side's font key, and a fixed extension
table, and a null result raises the side's formatted missing-font message
through the same modal/fatal diagnostic channel used for a missing anchor.
The loader does not continue past it — **a missing side font is a data
error**, not a silent fallback font.

### Interface panel files (`.gui`)

An interface file is an ordinary TDF. Its top-level sections are named
`GADGET0`, `GADGET1`, and so on. Every gadget section contains a nested
`[COMMON]` subsection plus type-specific keys read at the gadget level.
`GADGET0` is the panel header and carries the whole-panel keys as well.

**`[COMMON]` — read for every gadget.** Every key uses the integer accessor
with default 0 unless noted.

| Key | Accessor | Default | Notes |
|---|---|---|---|
| `id` | integer | 0 | control kind; selects the type-specific parser |
| `assoc` | integer | 0 | association/group index |
| `name` | string, 16 bytes | empty | also the event-binding and animation-entry name |
| `xpos`, `ypos` | integer | 0 | stored as 16-bit |
| `width`, `height` | integer | 0 | stored as 16-bit |
| `attribs` | integer | 0 | 32-bit attribute word |
| `colorf`, `colorb` | integer | 0 | masked to 16 bits |
| `texturenumber`, `fontnumber` | integer | 0 | stored as bytes |
| `active` | integer | 0 | stored as a byte |
| `commonattribs` | integer | 0 | stored as a byte |
| `help` | string | empty | |
| `gaffile` | integer | 0 | stored as 16-bit |

**Panel header keys, read on the header gadget:** `totalgadgets` (integer,
default 0, stored as 16-bit), and `panel`, `crdefault`, `escdefault`, and
`defaultfocus` (strings, 16 bytes each, default empty). The header may also
contain a `[VERSION]` subsection with `major`, `minor`, and `revision`
(integers, default 0, stored as bytes); the subsection is optional.

**Button keys:** `status` (integer, 16-bit), `text` (string), `quickkey`
(string, stored as a byte), `grayedout` (integer, 16-bit), `stages` (integer,
byte).

**Scrollbar keys:** `range`, `knobpos`, `knobsize` (integers, 16-bit),
`thick` (integer, 32-bit), `text` (string).

**List, text-entry, and compound control keys:** `itemheight`, `maxchars`,
`range`, `knobpos`, `knobsize` (integers, 16-bit), `thick` (integer, 32-bit),
`text`, `link`, `filename` (strings), `hotornot` and `nuttin` (integers).

Position sentinels are negative: an authored -1 centres the gadget on the
screen axis and -2 anchors it to the far edge; document 07 owns the exact
arithmetic. Animation entries are resolved by name and rendered onto an
indexed-colour surface.

**Control-kind mapping.** The parser's control-kind byte selects among twelve
handled cases: background/panel (which also centers on the -1 sentinel and
resolves its `BackTile` fallback chain), button (including staged buttons
chosen by best-fit frame size and `|`-separated multi-line labels), listbox,
text input (name capped at 127 bytes), slider (which synthesizes two scrollbar
child gadgets), a text case, an unnamed zeroing case, two embedded-file cases,
and three further single-purpose cases. Gadgets live in fixed 347-byte records;
the parser resolves each kind's art from its own named GAF entry first, then
the side-specific interface GAF, then the built-in fallback name. What remains
open is the callback map — which runtime events each widget receives — owned by
document 07.

The executable also contains a writer that serializes the same grammar back
out, section by section, which is how the section naming and nesting above are
established beyond doubt.

### Map files

Maps are discovered from `Maps\\*.ota` and use a companion `.tnt` terrain
file. OTA is parsed as TDF with a `GlobalHeader` and schema sections. The
loader supports campaign difficulty schemas, network schemas, and map-browser
enumeration. Network schema selection uses the number of `StartPos` records
to match lobby player count.

**Map-global keys.** All of the following come from the map's global section.
Localized keys use the language-prefixed accessor.

| Key | Accessor | Default |
|---|---|---|
| `missionname` | language-prefixed string | empty |
| `missionfile` | string | empty |
| `missiondescription` | string | `No description available` |
| `brief`, `narration`, `missionhint` | language-prefixed string | empty |
| `glamour`, `glamoursound` | string | empty |
| `Planet`, `memory`, `numplayers` | string | empty |
| `aiprofile` | string | empty |
| `UseOnlyUnits` | string | empty |
| `maxunits` | integer | **200** |
| `mapping`, `lineofsight`, `nomovie` | integer | 0 |
| `minwindspeed`, `maxwindspeed`, `gravity` | integer | 0 |
| `tidalstrength` | floating | 0.0 |
| `lavaworld`, `nosealeveltrigger`, `waterdoesdamage`, `waterdamage` | integer | 0 |
| `killmul`, `timemul` | floating | 0.0 |
| `HumanMetal`, `HumanEnergy`, `ComputerMetal`, `ComputerEnergy` | integer | 0 |
| `SurfaceMetal` | integer | 0 |
| `MeteorWeapon` | string | empty |
| `MeteorRadius` | integer | 0 |
| `MeteorDensity`, `MeteorDuration`, `MeteorInterval` | floating | 0.0 |

**Meteor merge and enable contract.** The map-global keys feed a storm record
committed to the mission globals. An empty `MeteorWeapon` is the only disable
predicate: it disables the shower and loads the defaults. With a nonempty
weapon name the authored parameters are taken, and if any of radius, density,
duration, or interval is zero **all five values are substituted** from the
`gamedata/METEOR.TDF` `[Default]` record before the shower is enabled — zero
parameters substitute rather than disable, and the enable step is reached on
both the authored and substituted paths. A map authoring nonzero parameters
with no weapon key is disabled with its parameters discarded.

Stock content authors `[Default]` as weapon `Meteor`, radius 300, density 2,
duration 5, interval 60. If that record itself carries an empty weapon name
or a zero density/duration/interval, the loader raises the exact diagnostic
`Hey, hoser!  The default meteor shower data was bogus!` (note the double
space after `hoser!`) and commits the partly loaded record as-is. A missing
file or missing `[Default]` section returns early leaving the record
untouched.

**Meteor scheduler and geometry.** The storm scheduler runs unconditionally
after wind jitter in the simulation tick body. Timing is integral:
per-hit delay `trunc(30/density)` ticks, duration `trunc(duration*30)` ticks,
interval `trunc(interval*30)` ticks. Weapon resolution at world init seeds
the first storm to the per-hit delay. When the next-strike time arrives the
storm activates, sets its end time to tick + durationTicks, and schedules its
next strike at intervalTicks + endTime — storms recur every
interval + duration ticks, and the first hit fires on the activation tick.
While active, one spawn attempt fires per hit-delayed tick (approximately
`floor(durationTicks/perHitDelay)+1` meteors per storm); density of 31 or
more collapses the delay to zero, one attempt per tick. A full shared
projectile pool drops a spawn silently.

Each meteor spends six random draws on the **CRT `rand()` stream**, not the
Park–Miller simulation stream: four scheduling draws pick the uniform-random
target cell over the map (height axis, then width axis) and the origin
offsets, which place the origin 6–15 cells north of the target; two further
draws pick a lateral angle and an offset within `radius`, applied through a
512-word sine table. Meteors consume **zero** simulation-stream draws.
Velocity aims at the target horizontally in exactly 90 ticks with a constant
15-world-units-per-tick vertical fall, and the spawn altitude is exactly
90 × 15 = 1,350 world units, so entry, flight, and impact all resolve on the
90th tick. Meteors spawn side-neutral with no attacker, so they earn no
veterancy or statistics credit, and their area damage hits every side.

All storm fields, including the enabled flag, persist in saves in the
`Meteor` block.

**Placed-object keys.** Map objects are read by a separate pass with three
shapes, compiled into typed arrays that are freed and rebuilt together on
every mission load (heap tags `MISSIONUNIT DATA`, `MISSIONRULE DATA`, and
`MISSIONFEATURE DATA`). Loading requires locating the `[globalheader]` block
and then the named `[Schema %i]`; a miss yields zero entries rather than an
error.

A unit placement compiles to a **36-byte** record. The strings `Unitname`,
`Ident`, and `InitialMission` (an opaque comma-separated script string) are
stored as pointers into a tail heap appended after the record array.
Coordinates store fixed-point dwords (authored value shifted left 16) in
X/Z/Y slot order (supported inference on the exact slot assignment). `Angle`
converts authored degrees onto the engine's 65,536-per-turn angle scale (the
exact rounding chain is supported inference). `BuildPriority`,
`HealthPercentage` (default **100**), and `CreationCountdown` fill dedicated
fields; `Player` defaults 0 with negatives clamped to 0; the four flags
`MissionCriticalUnit`, `AiIgnore`, `AiPriorityTarget`, and `Immunity` pack as
bits of one byte; `InitialGroup` composes into a nibble field.

A special compiles to a **12-byte** record: a kind field (1 = StartPos), an
id parsed from the numeric suffix of `specialwhat` (alphabetic suffixes are
distinguished from integer ones), and XPos/ZPos shorts. Indexed start
positions arrive this way.

A feature placement compiles to a **136-byte** record: a 128-byte
`Featurename` buffer plus XPos/ZPos integers defaulting to **-1**; negative
coordinates are cleared before stamping.

OTA features are added through the same feature stamping path used by the
terrain file.

**Mission-file diagnostics.** The mission open path owns five exact strings.
Four report through the status pane: `The requested mission file, %s, does not exist.`,
`Hey, joker!  Mission file %s is corrupt (no header found).`
(two spaces after `joker!`), `Old TED format no longer supported!`, and
`No GlobalHeader block in mission file!`. The fifth,
`No suitable schema type in mission file!`, uses a different message channel,
emitted when the schema selector accepts no schema. Campaign loads own the
sibling `The requested campaign file, %s, does not exist.`.

**Terrain file.** The terrain loader accepts exactly two version words and
rejects anything else with a diagnostic naming the value. Both versions share
the leading header fields — version, cell width, cell height, a tile-map
offset, an attribute-array offset, a tile-graphics offset, a tile count, a
map-object count, a map-object name-table offset, and a sea-level value — and
all offsets are file-relative and biased by the file base at load.

The two versions differ in the tail of the header and in the attribute record:

* The **legacy** version carries minimum wind, maximum wind, and gravity in
  its own header, plus a minimap offset and a minimap-present flag, and uses a
  narrower attribute record.
* The **canonical** version reuses those header slots for the minimap offset
  and its flag, and **hard-codes** minimum wind 100, maximum wind 2,000, and
  gravity 0 as the fallbacks. Its attribute record is four bytes and carries a
  height byte plus a feature reference with a distinguished void value.

Map-global wind and gravity from the map's `.ota` override the terrain values
only when the authored value is non-negative **and** the terrain file is the
canonical version; the legacy version always uses its own header values.
Gravity falls back to a fixed engine constant when neither source supplies one,
and tidal strength falls back to one half.

Sea level is a byte in the terrain header. Tile-map dimensions are half the
cell dimensions in each axis, with one 16-bit tile index per entry; each index
selects a 1,024-byte 32-by-32 indexed block in the tile set. The runtime
expands the attribute array into 13-byte plot cells whose layout is typed:

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

The exact byte layout of the legacy attribute record, beyond what feeds these
runtime cells, is the only remaining terrain-format unknown.

### Animation archive (GAF)

An animation archive is read whole into one allocation and relocated in place,
so every offset in the file is absolute from file start.

**File header.** A version word, then a 32-bit **entry count** — of which only
the low 16 bits are used — then one reserved word, then an array of one 32-bit
entry offset per entry. Every entry offset is biased by the file base.

**Entry record.**

| Offset | Size | Meaning |
|---:|---:|---|
| 0 | 2 | Frame count |
| 2 | 2 | Reserved |
| 4 | 4 | Reserved |
| 8 | 32 | NUL-terminated entry name |
| 40 | 8 per frame | Frame-reference array |

Entries are found by a linear, case-insensitive comparison against the name at
offset 8.

**Frame reference, 8 bytes.** A 32-bit frame-header offset, biased by the file
base at load, and a 32-bit **duration in whole simulation ticks**, which the
loader leaves untouched.

**Frame header.** Width and height as 16-bit values, signed 16-bit x and y
offsets, a reserved byte, a compression flag byte, a subframe count byte, a
reserved word, a 32-bit data offset, and a trailing reserved word. The data
offset is always biased by the file base.

**Compressed frame payload.** When the compression flag is set, the data
offset points at a row table: one 16-bit stored byte count per row followed by
that row's command stream. Each command is a 16-bit word read as:

* low bit set — skip `(word >> 1)` pixels, leaving destination untouched
  (this skip is the only transparency mechanism; palette index 0 is opaque
  black);
* otherwise bit 1 set — repeat the following byte `(word >> 2) + 1` times;
* otherwise — copy `(word >> 2) + 1` literal bytes.

Rows decode left to right until the row width is covered, then the next row's
count and stream follow. A zero width or height still allocates its header but
clips to nothing and draws nothing; out-of-bounds output is discarded rather
than wrapped.

**Subframes.** When the subframe count is nonzero, the data offset points at an
array of that many 32-bit subframe-header offsets. Each is biased by the file
base, and each subframe's own data offset is biased in turn — one level of
nesting only. A subframe is an ordinary frame header, so a subframe may itself
be compressed. Subframes are composed in file order onto the parent canvas at
their own offsets relative to the parent's, and later subframes overwrite
earlier ones wherever they are opaque.

**Animation playback.** A playback cursor holds the current frame index, a
countdown, a loop-or-hold flag, and the entry pointer. Binding a cursor clamps
an out-of-range start index to zero and loads that frame's duration into the
countdown. Each step decrements the countdown; when the countdown reaches its
last tick the frame index advances and the next frame's duration is loaded. On
running past the last frame, a looping cursor wraps to zero and a holding
cursor detaches from the entry and reports termination. A separate step form
takes a tick delta and repeatedly subtracts durations, so it can skip whole
frames when more than one tick has elapsed. Entries with a single frame never
advance.

Archive roots are loaded for cursors, interface chrome, effects,
fog/visibility masks, title overlays, unit and build sprites, and animation
sequences. Named entries are cached per root and reused by interface, HUD,
feature, and weapon paths.

### Model archive (3DO)

A model file is read whole into one allocation and relocated in place; every
offset in the file is absolute from file start.

**Object record, 52 bytes.**

| Offset | Size | Meaning | Relocated |
|---:|---:|---|---|
| 0 | 4 | Version signature | no |
| 4 | 4 | Vertex count | no |
| 8 | 4 | Primitive count | no |
| 12 | 4 | Selection primitive index, or all-ones for none | rewritten, see below |
| 16 | 4 | Signed 16.16 X translation from parent | no |
| 20 | 4 | Signed 16.16 Y translation from parent | no |
| 24 | 4 | Signed 16.16 Z translation from parent | no |
| 28 | 4 | Offset of the object's name | when nonzero |
| 32 | 4 | A further optional offset | when nonzero |
| 36 | 4 | Offset of the vertex array | always |
| 40 | 4 | Offset of the primitive array | always |
| 44 | 4 | Offset of the next sibling object | when nonzero, then recursed |
| 48 | 4 | Offset of the first child object | when nonzero, then recursed |

**Vertex, 12 bytes.** Three signed 32-bit coordinates.

**Primitive record, 32 bytes.** Three of its fields are offsets and are
relocated: one optional offset, the always-present offset of the vertex-index
array, and the optional offset of the texture name. The remaining fields carry
the colour index, the vertex-index count, and three further values.

**Load-time primitive reordering.** After relocation, and before the model is
ever drawn, each object is reordered:

1. If the object declares a selection primitive, that primitive record is
   swapped with primitive zero and the selection index field is rewritten
   to zero.
2. The remaining primitives, from index one upward, are bubble-sorted into
   ascending order of the **mean of their vertices' second coordinate**,
   using integer division by the primitive's vertex-index count.

Draw order within a piece is therefore fixed at load time, not recomputed per
frame, and an implementation that sorts at draw time will not reproduce the
retail order for ties.

**Mirroring.** A separate recursive pass negates the first and third
coordinates of every vertex and the first and third parent translations of
every object in a hierarchy, which is a half-turn about the vertical axis
applied to a whole model.

Model references are resolved from unit and feature model names. The
primitive's colour, texture-name, and flag interpretation at draw time belongs
to document 03.

### Compiled script archive (COB)

A compiled script file is read whole into one allocation and relocated in
place; every offset in the file is absolute from file start.

**Header.** A version word; a **script count**; a **piece count**; two further
words; a **record count** for the trailing table; then five offsets:

| Header slot | Points at | Relocation |
|---|---|---|
| script entry-point table | one 32-bit entry per script | the table pointer is biased; **entries are not** — each is a word index into the code array |
| script name table | one 32-bit name offset per script | the table pointer and every entry are biased |
| piece name table | one 32-bit name offset per piece | the table pointer and every entry are biased |
| code array | the 32-bit opcode words | the pointer is biased |
| trailing record table | *record count* records of 8 bytes | the pointer is biased, and the second word of each record is biased |

One header word is overwritten at load with the file's content checksum, so
its authored value is not used. The loaded object is cached by file, and the
stamped checksum is the signature that save/load validation compares.

The opcode encoding carried in the code array is specified in document 04.

### Content checksum

The same byte checksum is used wherever the engine needs a content identity. It
runs four independent 8-bit accumulators over a buffer of length *n*; for each
index *i* with byte *b*:

* an additive accumulator takes `+ b`;
* an exclusive-or accumulator takes `^ b`;
* a second additive accumulator takes `+ ((i & 0xFF) ^ b)`;
* a second exclusive-or accumulator takes `^ ((i + b) & 0xFF)`.

The 32-bit result packs them, least-significant byte first, in that order.

Each unit definition also carries a composite content checksum: the
exclusive-or of the checksum of its compiled script, the checksum of every
interface file whose name begins with the unit name in the interface
directory, the checksum of the unit's downloadable data file when one exists,
and one further definition word.

An override path accompanies that hash: for each unit definition, an archive
component spelled `OVR` is probed for an account named `Compatability` under
the title `TA Unit Override`; when present, its value replaces the computed
definition hash at its definition offset. Which gate consumes the replaced
hash, and whether further accounts are read, are not established (residual).

## 7. Font, image, and sample files

### FNT

The retail bitmap font format is not Windows FNT. Its file begins with a
16-bit height, a 16-bit control word whose two halves behave differently, and
a 256-entry table of absolute glyph offsets. Offset 0 marks a truly absent
code and is skipped in both measurement and drawing. Space (code 32) has a
non-zero offset entry, an advance width of 7, and an all-zero bitmap, so it
advances without drawing. Each present glyph stores an advance width followed
by a continuous, most-significant-bit-first bitmap.

The control word splits by byte. The **low byte** is a baseline descender:
glyph rows are drawn at `y` minus that byte (established arithmetic; the
field naming is inferred). The **high byte** is a base-character bias applied
during width measurement — the measured index becomes the code minus the
bias, gated to apply only when the bias does not exceed the code. Retail
fonts always store zero there (their values 1–3 occupy only the low byte), so
the subtraction never fires; a non-zero bias would activate ragged
base-character support.

Width measurement sums glyph advances and stops at newline (NUL also
terminates), in both the measurer and the drawer. Drawing clips text,
truncates to a maximum width when requested, and blits the bitmap into an
indexed-color surface using a transparent palette sentinel.

The startup path preloads COMIX and SMLFONT. Side and GUI fonts use the same
VFS/font decoder and are retained as shared immutable font images. The active
font is selected by the GUI/text context.

### PCX

Retail PCX is 8-bit, single-plane RLE written by a version-5 encoder. The
decoder validates **only** the manufacturer byte `0x0A` and the version byte
5; encoding, pixel-depth, and plane fields are unchecked (they are implied by
the RLE logic). Image dimensions are inclusive (`xmax-xmin+1` by
`ymax-ymin+1`) over a 128-byte header. Run-length commands take their count
from the two high bits; every run is clamped to the remaining scanline width
and the excess discarded, so malformed files cannot overflow a scanline. The
256-color palette is read by seeking to file size minus 768 — **without any
check of the palette marker byte** the format defines. The palette expands to
RGBA with alpha forced to zero. It can decode directly to an indexed GUI
surface or return palette data. The encoder writes standard scanline RLE with
`bytes_per_line` equal to the width and a palette trailer.

### WAV

The audio loader recognizes three families with a fixed detection order:
first the legacy DIGI container (`DIGI` at offset 0, then `HSHD` at 8 and
`SDAT` at 0x20), then RIFF (`RIFF` at 0 with `WAVE` at 8), otherwise the
buffer is raw PCM carrying no header at all.

RIFF parsing walks `fmt ` and `data` chunks with a size-plus-eight stride,
honors odd-size padding, and extracts PCM format, channel count, sample rate,
bit depth, block alignment, and data length; the `data` chunk's declared size,
not the file size, becomes the payload length. Legacy DIGI metadata reads its
sample-rate word from `HSHD`: a rate of 11,000 is remapped to 11,025, and the
SDAT payload is trimmed by ten bytes; the sample is treated as 8-bit mono
thereafter. Raw samples default to 11,025 Hz mono 8-bit.

Decoding serves three allocation modes: a plain memory blob (raw fallback
fixed at 11,025 Hz mono 8-bit), a preloaded DirectSound buffer, and a
DirectSound streaming path that returns a streaming-handle sentinel rather
than sample data.

## 8. Failure, caching, and lifetime rules

### Established fact

* VFS handles support open, seek, read, size, and close.
* HPI decompression uses bounded windows and per-handle caching.
* Fonts, GAF roots, palettes, sound aliases, and catalog records are retained
  across many consumers after startup.
* Sound samples have a separate alias/cache layer and a mixer/channel layer.
* Required catalog roots and core parser resources fail through a fatal error
  path when missing.
* GUI, animation, and sound paths have optional/degraded behavior in some
  callers, but not every optionality boundary is enumerated.
* Feature successor links may lazily create referenced feature records, then
  are fixed up in a second pass.
* OTA/TNT map data and catalog records are retained for the lifetime of the
  active mission/session and are rebuilt when a new map or save is loaded.

### Supported inference

The content subsystem should use immutable decoded asset objects with shared
references and a session-owned cache for catalogs/map data. Cache keys should
include logical path, winning provider identity, and relevant decode mode.
This matches retail's reuse while preventing stale data after an overlay or
save/load transition.

## Missing and unknown

Closed since the previous revision, and now specified above: the archive
container, its cipher, its directory shape, its duplicate and separator rules,
and its absence of traversal handling; the footer four-byte wildcard and the
mount-time read-length policy; the mount append order, per-extension keep-open
semantics, and lazy reopen/validation lifecycle; the chunk-size table and full
`SQSH` header/checksum/payload-transform/LZSS/zlib decoder contract; the
typed-accessor family and its default and scaling behavior; the language
precedence (command line > registry > English)
and the translation fallback (empty map, source returned byte-exactly) plus
the installed-corpus language/font census; startup executable-directory
behavior via `GetModuleFileNameA`/`SetCurrentDirectoryA`; the complete
authored key set, accessor, default, and unit conversion for the unit, weapon,
feature, movement-class, and map records; the interface anchor rectangle
convention and its mandatory anchor set; the animation-archive and model-archive
record layouts and their load-time transforms; the compiled script header; and
the content checksum. Closed in this revision: the full meteor merge,
scheduler-timing, geometry, CRT-rand-stream, and save-persistence contract;
the feature reproduction cadence including its unconditional per-visit RNG
draw; the TDF parse-diagnostics set, empty-tree failure policy, offset-
preserving comment blanking, and duplicate-key winner mechanism; the fatal
feature cross-reference diagnostic; the mission-object record shapes and the
mission-file diagnostic vocabulary; the SIDEDATA `[GENERAL]` height, per-side
interface-GAF panel binding, bar palette indices, and fatal side-font path;
the build-menu catalog keys and the downloadable warning with its silent
flag repair; the CD identity gate, mount deduplication, and
union-enumeration dedup; the WAV detector order, DIGI normalization, and
three allocation modes plus the 255-entry alias cap; the PCX validation,
run clamping, and marker-less palette read; the FNT descender/bias split;
the skirmish per-slot defaults with the `NumSkirmishPlayers` no-op
validation; and the TNT feature-reference sentinel refinement.

Still open:

* Exact handling of `C`/`c` command-line path/configuration, full command-line
  grammar for non-language switches, and behavior when the executable is
  launched from a non-install directory (beyond the `GetModuleFileNameA` CWD
  established above).
* Archive entry flag bits beyond the subdirectory bit and the mutable
  enumeration-visibility bit (bit 2) for synthetic values.
* Recovery behavior for a structurally malformed but header-valid archive
  beyond the safety hardening noted above, and whether any shipped archive
  variant outside the installed corpus departs from the container specified
  here.
* Exact generic TDF behavior for malformed floating values, line limits,
  and caller-specific section merging (the parse-diagnostics set,
  empty-tree failure policy, comment blanking, and duplicate-key winner
  are now specified in §4).
* Cross-reference failure policy when a unit names a missing weapon, corpse,
  movement class, model, or sound category (the feature-record equivalent is
  the fatal diagnostic specified in §5).
* Exact strategic-AI use of `ai_weight` and `ai_limit`, and the identity of
  the enclosing routine behind the `side`-filtering build picker (mechanics
  confirmed in §5; its classification as AI code is supported inference).
* Whether shipped burn-animation sequences carry a looping flag byte
  (completion timing depends on it); feature reclaim and reproduction timing
  are specified above and in document 05, and no geothermal registry exists —
  enforcement is the footprint validator's yardmap-bit-7 check.
* Complete sound alias precedence, eviction, and DirectSound streaming rules.
* The draw-time interpretation of model primitive colour, texture, and flag
  fields is narrowed in document 03 (flat colours bypass the shade table,
  indexed texture pixels use it, team textures select per-player frames,
  no backface culling); the compressed-animation pixel decoder itself is now
  specified above.
* GUI widget callback map (the parser-side control-kind mapping is closed
  above) and texture lifetime behavior.
* Map schema fallback, map hash inputs, and initial-mission script
  interpretation. Meteor processing is fully specified above (merge
  contract, scheduler position and timing, geometry, CRT-rand stream, save
  persistence).
* Full override semantics of the OVR archive's `Compatability` account hash
  replacement (which gate consumes the replaced hash; whether further
  accounts are read).
* Exact sequencing of the CD `TOTALA.ID`/`Contents` identity gate relative
  to the mount loop (gate behavior carried as supported inference in §2).
* The legacy terrain attribute record's exact byte layout beyond what feeds
  the runtime plot cells above.
* Remaining code-page behavior for high bytes and localized font selection
  beyond the installed-corpus census; and which runtime messages pass through
  the translation lookup.
* Exact cache invalidation boundaries across map changes, saves, and lobby
  sessions.
