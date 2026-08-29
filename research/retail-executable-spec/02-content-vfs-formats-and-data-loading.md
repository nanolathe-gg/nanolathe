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
known hard requirements are `MOVEINFO.TDF` and `SIDEDATA.TDF`; a missing
`gamedata\` directory is fatal as well. **`GAMEDATA.TDF` as a file is not a
hard requirement**: the reference install contains no `gamedata.tdf` anywhere
and boots (see `docs/SPEC_CONFLICTS.md` SC2 — an earlier revision of this
document listed `GAMEDATA.TDF` as fatal; the fatal resource is the
`gamedata\` directory, and the "Can't load GAMEDATA.TDF" diagnostic is pushed
from the side-data loader region but its branch is unreachable when the
directory exists without the file). The translation table
`gamedata\translate.tdf` is **not** a hard requirement: missing data leaves an
empty table and lookup returns the source string unchanged (byte-exact
compare). Optional animation, sound, and presentation resources can degrade
through separate paths.

### Supported inference

The root should be captured once during startup and passed to every content
consumer. This avoids a subtle retail-compatible failure mode in which a
relative path resolves differently after a lobby or save/load transition.
The content context should retain the winning provider for every opened
logical path, because save metadata and multiplayer identity use the resolved
content set.

### Command line, established

The command line is whitespace-tokenized. A token not starting with `-` or `/`
is copied into the language buffer (the last such token wins). `-`/`/`
switches comprise nineteen recognized developer switches (`-memfussy`,
`-memnofussy`, `-memfrontalign`, `-gonzo`, `-memset`, `-memnoset`,
`-fpufussy`, `-fpunofussy`, `-dprinton`, `-dprintoff`, `-dprintfile`,
`-memorystatus`, `-performancestatus`, `-disableimagehlp`,
`-enableimagehlp`, `-disableimagehlplines`, `-enableimagehlplines`,
`-debughelper`, `-saveresources`), the multiplayer lobby flag words `lock`,
`deathends`, `deathplays`, `deathmatch`, `fixedloc`, `mapping`, `circlos`,
`truelos`, `permlos`, `cheating`, `watching`, a numeric-argument switch
clamped to the 30..300 window, and the `C`/`c` config switch. Unknown
switches are ignored.

The `C`/`c` switch takes a config reference (attached to the switch or as the
following token) and loads it through `online.dll`: the executable resolves
its own directory, loads `online.dll` from it, requires the exported
`ONLGetVersion` to return exactly **3**, and then calls
`ONLLoadConfigFile(config, buffer, 336)` into a 336-byte config block; a
missing DLL, missing export, or wrong version leaves the block zeroed with no
fallback dialog.

Behavior when the executable is launched from a non-install directory is fully
covered by the established `GetModuleFileNameA`-based content-root pinning:
nothing in the command-line path alters the content root. Directory fallback
on `GetModuleFileNameA`/`SetCurrentDirectoryA` failure is not a deliberate
retail fallback.

### Unknown

Open items only; the decider follows each.

- Data contract of `ONLLoadConfigFile` · not decidable from the retail
  executable: the function is defined by `online.dll`.
- Exact bit consumers of the lobby flag words · static trace (document 08
  owns the consumers).
- Fatal-versus-recoverable classification for the resource families not
  classified in this document; `MOVEINFO.TDF` and `SIDEDATA.TDF` are fatal,
  the translation table is optional, and `GAMEDATA.TDF` as a file is not fatal
  (SC2) · static trace.


## 2. Virtual file system and provider precedence

### Established fact

The VFS has a singleton context containing an ordered provider list. The
observed mount/search sequence, established by the mount append order, is:

1. Loose host files (tried first on every open via `fopen`).
2. The revision/patch archive `rev<name>.GP3` with keep-open flag 1.
3. Every `*.CCX` with keep-open flag 1.
4. Every `*.UFO` with flag 0.
5. Local `*.HPI` with flag 0. The mount loop carries a ten-valued budget
   that decrements only when a candidate is **newly mounted** (validation
   passed and the full path was not already mounted); the loop abandons the
   enumeration when the budget reaches zero, so the eleventh *new* local HPI
   of a given pass is not even attempted in that pass. The budget is
   per-invocation: the mount orchestrator runs at several call sites, each
   restarting the budget, and already-mounted archives never consume it, so
   repeated invocations converge to every valid local HPI mounted. This
   reconciles the earlier "eleventh successful local HPI is not mounted"
   reading with the 13-HPI reference install (`docs/SPEC_CONFLICTS.md` SC1):
   the cap is real but per-pass, not a global limit; mounting every local HPI
   in one pass reproduces the converged retail state.
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
`%c:\*.hpi` discoveries for mounting (this tier has no identity check and no
budget). CD *content-path* selection is a separate path: the CD-content
loader adjacent to the mount loop walks each CD-ROM drive, reads
`<drive>:\TOTALA.ID`, parses it as TDF, locates the `Contents` section, and
reads the integer key named by the bootstrap mode (`Campaign` for campaign
entry, `Multiplayer` for multiplayer); a nonzero value accepts that drive and
its letter feeds the `%c:\%s` content paths (retried once), a missing
file/section/zero value advances to the next CD-ROM drive. The sequencing
question is therefore settled structurally: the identity gate is not part of
the mount loop — it gates which drive serves CD content, while the mount
loop's CD tier mounts `%c:\*.hpi` from every CD-ROM drive it finds. This
upgrades the earlier supported inference to established fact.

The VFS supports both single-file reads and union enumeration. Enumeration
is used for catalogs, maps, campaigns, GUI files, save slots, and sound
aliases; its results deduplicate by canonical entry name under
case-insensitive equality — the first physical backing wins — and filter the
host filesystem's internal pseudo-entry names and directory entries. A
`flag & 1` marks a subdirectory. **Flag census (established):** the union
enumerator skips every entry whose flag byte has bit 1 (`0x02`) set — the
mutable enumeration-visibility bit, recursively cleared before union rebuild —
and classifies the surviving entries by bit 0 (set → directory, clear →
file with a size read from the file record). No other flag bit is tested by
the mount, validate, or enumerate paths; synthetic flag values beyond these
two bits have no executable-defined meaning. Union visibility is rebuilt by
recursively clearing mutable entry bit 1 before rebuilding; the installed
corpus contains only persisted entry flags 0 for files and 1 for directories
across 303 directories and 7,889 files.

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
check. **Mount-time validation is exactly three checks.** The reader requires: fopen
success; the four magic bytes `HAPI`; the version bytes `00 00 01 00` at
offset 4; and the normalized footer. Nothing else is validated at mount: the
directory-blob size is not bounded against the file, relocated offsets are not
checked against the blob, and entry counts are trusted. A structurally
malformed but header-valid archive therefore **mounts** and its failures
surface at read time (short reads, decompression errors, and the all-ones
failure value with the chunk diagnostics), not at mount time. A mismatch in
the tag, the version bytes, or the normalized footer rejects the archive; the
provider object is released and never enters the mount list.
**Installed-corpus observation** (this corpus only, not a claim
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

Open items only; the decider follows each. The container, its cipher, its
directory shape, its duplicate and separator rules, its absence of traversal
handling, the mount append order, the keep-open flag semantics, the footer
four-byte wildcard, and the recovery behavior for a structurally malformed but
header-valid archive are established above. Enumeration order within one
wildcard group is not an executable property at all — it is the host directory
listing, which retail does not sort.

- Entry flag bits other than the subdirectory bit and the mutable
  enumeration-visibility bit (bit 1, mask `0x02`, recursively cleared before
  union rebuild), for synthetic values · static trace. Bounded-negative today:
  no other bit is tested by any mount, validate, or enumerate path, so
  synthetic values are inert.
- Whether any shipped archive variant outside the installed corpus departs
  from the container contract above · asset census.


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

The scalar skirmish preferences are also defaulted by the same loader when
absent: `SkirmishDifficulty=1` (Medium), `SkirmishLocation=1` (pre-determined
start positions), `SkirmishCommanderDeath=1` (commander death ends the game),
`SkirmishMapping=1` (terrain is blacked out until explored),
`SkirmishLineOfSight=1` (LOS enabled), and `SkirmishLOSType=1` (terrain
elevations affect LOS). These values are the installed lobby defaults. The
retail LineOfSight control cycles the coupled state through elevation-aware
LOS (`1,1`), elevation-agnostic LOS (`1,0`), and all mapped terrain visible
(`0,1`); the other scalar controls expose their ordinary zero-valued
alternatives.

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

**Unit limit (R-CONTENT-03, opener).** The previous text said only "a unit
limit of 250 is applied before clamping elsewhere in the startup path"; the
source is now traced. **Established:** at startup the executable reads the
limit from the Windows profile file `<executable directory>\totala.ini`,
section `[Preferences]`, key `UnitLimit`, through the integer-profile accessor
with default **250**, clamps the result into **20..500** (below 20 becomes 20,
above 500 becomes 500), and stores it as a 16-bit configured limit. It also
sets a "unit limit" mode flag to 1 alongside the read; a bounded census finds
**no reader** of that flag anywhere in the recovered executable, so it is
retained-and-inert as far as static analysis reached (write sites: the startup
store and one battle-setup store that takes the flag's value as a parameter).

**Established:** the configured limit is overridden by an integer item named
`maxunits` when a save or mission file carries one, and the map loader reads
`maxunits` from the OTA global section (default **200**, see the map-global
key table below) into the **active** limit word used during play; the active
word is also seeded at session entry from the lobby setting or the configured
limit. The active limit's consumers (construction admission, AI production,
lobby display) are owned by document 05 ("Unit creation and limits") and are
not enumerated here.

**Established (bounded-negative):** there is **no per-unit-definition limit
key** in the executable. The string vocabulary contains no `maxthisunit` or
`unitlimit` FBI key in any spelling; `limit` belongs to the AI-profile grammar
(see the `ai_weight` note above). Any implementation field for a per-definition
unit limit keyed from FBI data has no retail source and must not be invented.

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

**Installed-corpus observation:** 21 font filename spellings (20
case-folded identities); both startup-required `COMIX.FNT` and `SMLFONT.FNT`
are present in this corpus. The installed translation table spells `french`,
`german`, `italian`, `piglatin`, and `spanish`; four `japanesename` prefixes
appear in FBI records with no corresponding translation-table key in this
corpus. Neither is a claim about every edition.

### Unknown

Open items only; the decider follows each. The registry hive, key path, value
names, defaults, write-back, missing-translation fallback, duplicate policy,
and the command-line/registry/English language precedence are established
above.

- The language-selection interface — how a language is chosen at runtime ·
  static trace.
- Code-page behavior for high bytes · static trace. Marked `TODO(T23)`.
- Language-specific font fallback beyond the installed-corpus census · asset
  census.
- Which runtime messages pass through the translation lookup · static trace.
- Precedence among registry, INI, and command line for non-language
  configuration · static trace (doc 01 §3.1 owns the scalar half).


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

Open items only; the decider follows each.

- Caller-specific duplicate-section merging policies · static trace. The
  first-match section accessor, the enumerator behavior, the duplicate-key
  winner mechanism, the parse-diagnostics set with its title and empty-tree
  failure policy, and comment-blanking offset preservation are all established
  above, as is the floating accessor's CRT `atof` behavior and the absence of
  any tokenizer line limit.

## 5. Catalog construction and linking

### Established fact

Retail content loading is two-stage:

1. Discover files/sections and create stable catalog records.
2. Parse typed fields, apply defaults, and resolve cross-references.

This avoids directory enumeration order deciding whether a reference can be
resolved. Known links include unit weapons, corpses, movement classes,
feature successors, 3DO objects, GAF animation sequences, sound aliases, and
category tokens (the category token registry is specified under R-P0-03
below).

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
| `category` | string, 100 bytes | empty | category token list (see R-P0-03) |
| `soundcategory` | string, 100 bytes | empty | resolves to a sound-category index |
| `corpse` | string, 100 bytes | empty | resolves to a feature identity |
| `movementclass` | string, 100 bytes | empty | resolves to a movement class |
| `weapon1`, `weapon2`, `weapon3` | string, 128 bytes each | empty | resolve to weapon identities |
| `explodeas`, `selfdestructas` | string, 128 bytes each | empty | resolve to weapon identities |
| `YardMap` | string, 1,024 bytes | empty | occupancy map text |
| `defaultmissiontype` | string, 100 bytes | empty | |
| `wpri_badTargetCategory`, `wsec_badTargetCategory`, `wspe_badTargetCategory`, `noChaseCategory` | string, 100 bytes each | `none` | per-slot exclusion categories (see R-P0-03) |

`side` is read by the catalog loader, not the per-unit compiler. Its one
located consumer is a weighted build-choice roulette over an acting unit's
build list: cumulative weights select a candidate through the simulation RNG,
and the winning candidate's `side` string is then compared byte-for-byte —
case-sensitively — against the acting unit's own; any mismatch rejects the
pick. The enclosing routines are the strategic-AI build passes: the periodic
AI planner and a companion builder-refresh pass both iterate builder units,
run the roulette, and index the unit-definition table with the pick result —
the classification as AI-side is now established.

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

**Runtime units of the locomotion fields (Established, 2026-08-28, RWU-04-1).**
Because `maxvelocity`, `brakerate`, `acceleration`, `moverate1` and `moverate2`
take the **fixed-point** accessor, the compiled field is the authored decimal
multiplied by 65,536 and truncated toward zero, and the ground mover consumes
it verbatim: there is no further scaling, no division by the tick rate, and no
conversion at use. `maxvelocity` is therefore 16.16 world units **per tick**
and `acceleration`/`brakerate` 16.16 world units **per tick squared**
[04 §8.1 R-MOV-01 §1]. `moverate1`/`moverate2` are the two movement-tier
thresholds of [04 §5.2], in the same units, each defaulting to the
`maxvelocity` value just read shifted left one. `turnrate` takes the
**integer** accessor into a 16-bit field that every reader zero-extends, so
its domain is 0..65535 on the 65,536-per-circle angle scale and an authored
value is taken modulo 65,536; it is angle units per tick. `brakerate` and
`turnrate` are both used as unguarded divisors in the ground steering step, so
the compiled default of 0 is a fault for any unit that actually moves
[04 §8.1 R-MOV-01 §4].

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

**Definition-flag mapping correction (Established).** The unit parser reads
the `onoffable` integer and packs its boolean value into bit 2 (`0x04`) of the
definition flags word. This bit is not an authored unit `noradar` field and is
not derived from runtime cloak or hidden state. The unit parser has no
`noradar` accessor; `noradar` is a weapon-record key. The radar-circle use of
this definition bit is specified in [03 §3.9].

`wacky` is parsed by the catalog loader into bit 16 of the same packed
definition flag word that carries `norestrict` (bit 15). No reader of that
bit was found anywhere in the reviewed executable corpus: the key is parsed,
stored, and preserved across record moves but semantically inert as far as
static analysis reached.

**Downloadable enforcement.** After build-menu pages compile, the loader
walks every unit definition and compares its name case-insensitively against
every build-menu button name. A match whose `downloadable` bit is clear
raises the exact warning `Hey!  Somebody forgot to set downloadable=1 for %s`
(two spaces after `Hey!`, verbatim) — the string exists at two push sites in
the executable, one in the catalog region and one in the downloader region,
so the "raised once" reading is per-site, not a counted global — silently
forces the bit on, and re-sorts and re-finalizes the catalog.
A unit reachable from any build menu therefore behaves as downloadable for
the rest of the session.

`selfdestructcountdown` is read with the raw accessor, so the record can tell
an authored value from an absent key.

### Building heading field audit [R-P28-ANG-01R §1]

**Established — authored values.** An asset-backed read with
`NANOLATHE_TA_ROOT=/path/to/home/TotalAnnihilation` resolved the stock unit
records through the normal VFS and preserved these source values:

| Unit record | `FootprintX` × `FootprintZ` | `YardMap` | `buildangle` | `Ovradjust` |
|---|---:|---|---:|---:|
| `ARMSOLAR` | 5 × 5 | `ooooooooooooooooooooooooooo` | 4096 | 1 |
| `ARMLAB` | 6 × 6 | `yoccoy ooccoo ooccoo ooccoo ooccoo yoccoy` | 4096 | 1 |

The values above are evidence of authored data only; field names and the
visual orientation of a screenshot are not semantic evidence.

**Established — `buildangle` reader.** The unit compiler reads `buildangle`
with the integer accessor (default zero) and stores the resulting low 16 bits
as an unsigned bound. On each successful unit allocation, the unit
initialization path invokes the global simulation random sampler once with
that bound and uses the result to initialize the unit heading. The allocator
does not read `Ovradjust`/`ovradjust`; the bounded executable census found no
other reader for that field. Thus `buildangle` is no longer an untyped or
readerless candidate, while `ovradjust` remains a retained unknown source
field with no behavior assigned.

The sampler's exact heading arithmetic and lifecycle are owned by [04
§2.3b] and the factory placement boundary by [05 "Factory production
lifecycle"].

**Executable-bounded absent fields.** Editor-only fields, unit number and
designation metadata, `noautofire`, `ovradjust`, `steeringmode`, and several
alternate transport names have no reader. They must be retained as unknown
source fields and must not be given behavior. `ai_weight` and `ai_limit` are
retained as raw strings, but their fates differ: **`ai_weight` is consumed**
— the strategic-AI pass parses its text with the profile grammar, its
`weight` directives reach the live per-unit-type weight array (default 100,
clamped to 0..100) that scales build-candidate scores, and embedded `limit`
directives are registered too; **`ai_limit` has no runtime reader** — the
live per-type limit array is populated only by the ai/ profile parser's
`limit` token, never by this FBI key (bounded-negative; do not treat
`ai_limit` as the source of the retail candidate limit).

### Build-menu catalog keys

Build menus are assembled from catalog data, not side data. Per-unit numbered
pages derive from `CANBUILD %s` sections and numbered `canbuild%d` keys,
enumerated from 1 upward; a gap yields a missing page rather than terminating
the loop. The executable's key vocabulary also names `MENU`, `UNITMENU`,
`DOWNLOADMENU`, and `BUTTON` sections consumed while assembling build and
order menus; the download tier additionally keys off the unit `downloadable`
flag (enforcement rule above). Wiring of those sections beyond this presence
is supported inference.

### Cross-reference failure policy

When a unit definition names a resource that cannot be resolved, the catalog
does not abort; each reference family has its own failure outcome:

| Reference | Missing → behavior | Fatal? | Evidence |
|---|---|---|---|
| Weapon (`weapon1..3`, `explodeas`, `selfdestructas`) | the name lookup against the weapon record table misses and the slot is filled with a reference to record 0 — the inactive sentinel (R-CONTENT-02); no abort | no | direct |
| Corpse (`corpse`) | the `0xFFFF` no-corpse sentinel; the unit leaves no wreck | no | direct |
| Movement class (`movementclass`) | the unit compiler falls back to a scratch record — 255 slopes, depth limits ±10000 — parsed from the unit's own FBI keys (see "Movement class record"); a null profile otherwise | no (degraded) | direct |
| Model (`objectname`) | the model cache slot stays empty; rendering degrades (no model) | no (degraded) | supported inference |
| Side (`side`) | the build-pick filter compares the authored string; a mismatch rejects the pick — an empty side mismatches every acting side | no, but affects AI builds | direct |
| Sound category (`soundcategory`) | the category index falls back to a muted placeholder; playback is skipped | no (muted) | supported inference |

The feature-record equivalent is different: a feature name found in no parsed
feature node raises the fatal diagnostic `Record "%s" missing from feature
files` (§5 above).

### Weapon record

Weapon files are TDF; each top-level section is one weapon.

**Record identity.** The parser reads `ID` with the integer accessor and a
default of -1 **first**, and uses it to select the weapon record it fills. The
section name is then stored into that record as the weapon's catalog name, and
`name` is read separately as a 64-byte display string. A weapon section without
an authored `ID` therefore selects the slot before the table.

#### R-CONTENT-02 — Weapon-family discovery and same-ID merge (closed)

**Correction.** PLAN_02's post-review amendment recorded — and earlier
revisions of this document implied — that `gamedata\weapons.tdf` parses before
the `weapons\` directory, so referenced names survive a stock ID 36 collision
between `[earthquake]` there and `[cormine2]` in `weapons/cormine2_weapon.tdf`.
That premise is **wrong**: the executable never parses `gamedata\weapons.tdf`.
The correction is behavioral, not speculative: the only weapon-family pattern
in the executable's string vocabulary is `Weapons\*.tdf`, pushed at exactly two
sites (the weapon-record compiler and the unit-catalog loader below), and the
bounded census of every `gamedata\` path-building site accounts for version,
sidedata (twice), moveinfo, sound, allsound, los, help, meteor, category, and
the full-path `gamedata\translate.tdf` literal — none name a weapons file.
The stock "ID 36 collision" is between a file retail never reads and the
parsed family; retail never sees it. **Established.**

**Discovery order — Established.** The weapon family is exactly the union
enumeration of `Weapons\*.tdf`: provider mount precedence first, then the
host's unsorted directory order within one provider's directory (§2, SC3),
with duplicate logical paths resolved first-provider-wins. Two independent
passes walk it — their relative call order does not affect either result:

* The **weapon-record compiler** parses every file and feeds **every
  top-level section, in file order**, to the record parser.
* The **unit-catalog loader** re-parses the same family into its own document
  set and resolves the unit record's `weapon1..3`, `explodeas`, and
  `selfdestructas` names against those documents (see below).

**Record table — Established.** The catalog is a fixed table of **256 weapon
records** (277 bytes each) that the compiler initializes in place before any
parsing: each record's catalog-name bytes are emptied and each record's
**slot-number byte is stamped once with its own index** (0..255). The record a
section fills is `table + ID × 277`; with the default -1 an ID-less section
fills the 277-byte scratch slot immediately **before** record 0 — all ID-less
sections clobber the same unreachable slot, and the last one wins it.

**Same-ID merge — Established.** The record parser is called once per section
and behaves as follows: it reads `ID` first; copies the **section name** over
the record's catalog-name bytes; reads `name` as the 64-byte display string;
and then **unconditionally stores every field it parses** — the authored value
when the key is present, the accessor default when it is not. A later section
with the same ID therefore does not sparse-merge with the earlier one: it
**replaces** every parser-owned field of the record (authored-or-default) and
rewrites the catalog name. The surviving catalog name is the **later**
section's name; the record's slot-number byte is never rewritten by the
parser. Record 0 is special: consumers treat a weapon reference as inactive
when it points at record 0 (recognized by its zero slot-number byte), and the
stock corpus fills it with `[noweapon]` (`ID=0` in `weapons/weapons.tdf`).

**Runtime name resolution — Established.** A weapon name resolves by a linear
scan of the record table from slot 0 upward, comparing case-insensitively
against each record's catalog name; the **first** matching slot wins. A name
that matches no record returns not-found. When the FBI compiler resolves
`weapon1..3`, `explodeas`, or `selfdestructas`, a miss is replaced by a
reference to **record 0** — the inactive sentinel — not by an error. This
corrects the cross-reference table below, whose previous text said the
unresolved weapon id was an all-ones sentinel; the sentinel is record 0
identified by its zero slot-number byte.

**Unit identity contribution — Established accumulator, Supported inference
for its role.** While resolving the five weapon-name keys, the unit-catalog
loader finds each named section in the parsed weapon documents and
exclusive-ors a per-section value into one definition word. That per-section
value is the section's **own raw-text content checksum** — the same
four-accumulator checksum as §6, computed by the TDF parser when a section
closes and stored on the section. The accumulated word is a definition-identity
input (§6's "one further definition word" in the unit composite hash), not a
weapon slot; the composite-hash consumer link is the Supported inference.

**Stock corpus census — Established for this corpus, not a claim about every
edition.** The union `Weapons\*.tdf` view holds 77 files with 198 top-level
sections, and **every authored ID is unique** — there is no same-ID collision
anywhere in the parsed family. ID 36 is `[cormine2]`
(`weapons/cormine2_weapon.tdf`) alone. `[earthquake]` in the parsed family is
`weapons/earthquake.tdf` at ID **227**. `gamedata\weapons.tdf` (present in
three providers, 23 KB, 33 sections with authored IDs scattered across
0..36, including `[earthquake] ID=36`) is inert for the retail executable. A name lookup for a superseded
spelling has no stock instance; with a hypothetical same-ID collision, the
superseded (earlier) section's name would match no record and resolve to the
record-0 inactive sentinel.

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

**Keys with no field at all — Established, and stronger than a reader census.**
`aimrate`, `movingaccuracy`, `noselfdamage`, `impulsefactor`, `impulseboost`
and `startfire` do not occur **anywhere in the image's string data** — not as a
parser key, not in a diagnostic, not in any other literal. A whole-image
case-insensitive scan of every string in the executable returns nothing for any
of the six, while every key listed above in this section is found (`holdtime`,
`toairweapon`, `minbarrelangle`, `propeller` and the rest each appear exactly
once, as the parser literal). Since the TDF accessors take the key as a string
literal, no key means no field: these six are never read, never stored on the
weapon record, and cannot have a reader anywhere in the image. That is stronger
than the bounded "no reader was found in the recovered function set", which is
all a caller census can establish and all the previous text claimed. Authoring
any of the six in a weapon section has no effect of any kind, and they occupy
no record byte, so they cannot even be preserved as inert data — unlike, for
example, `accuracy` and `tolerance`, which are parsed into the record and do
have consumers `[06 §3.3]`.

**Correction.** This paragraph previously read, in full, "`aimrate` and
`startfire` have no reader in this executable." It was right about those two
but understated the evidence — a missing reader, where the truth is a missing
key — and omitted four further keys in exactly the same position.
`movingaccuracy`, `noselfdamage`, `impulsefactor` and `impulseboost` circulate
in third-party weapon-key documentation; they are not retail keys, and neither
this document nor `[fmt tdf]` should be read as implying that the engine merely
ignores them.

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
when a referenced definition can still be discovered. Missing links use the
explicit no-successor sentinel `0xFFFF`; an unresolved chain ends with no
feature rather than a substitution. When a requested feature record name
appears in no parsed feature node, the loader formats the exact diagnostic
`Record "%s" missing from feature files` and emits it through the fatal
diagnostic path; recovery beyond that dialog is not modeled, so a
reimplementation treats it as fatal.

Catalog entries are `0x100` bytes each, live animation slots are `0x800`
entries of `0x30` bytes each, and the terrain plot grid is `0xD` bytes per
cell. Exhausting the catalog, the animation pool, or map bounds causes a silent
failure with no placement. Burning sequences have their loop byte forced to
non-looping at load, so every shipped burn animation has a finite lifetime
between 46 and 282 visits and ends only when its animation pointer clears; the
countdown derived from `sparktime` fires a one-shot spread and optional
burn-weapon event and then stays inert.

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
`reproduce=0` (`reproducearea=6` where present). Shipped burn animations are
finite — the loader forces every burn, burn-shadow, death, and reclaim sequence
to non-looping, so completion is gated on the animation pointer clearing, not
on a loop byte carried from the GAF.

### Movement class record

Movement classes come from `CLASS` sections in the movement catalog. The
loader scans `CLASS0` through `CLASS31` — the loop is bounded by the 32-slot
pool, **not** by section presence: a missing section skips that index without
terminating the loop (this corrects the earlier "until a gap" reading; stock
content happens to author `CLASS0..CLASS14` contiguously, which is why the two
readings coincide there). Each parsed class reads eight keys **in this parse
order**, and defaults chain off values read earlier in the same record, so
order is contract:

1. `FootPrintX` — integer, default 0, stored as 16-bit.
2. `FootPrintZ` — integer, default 0, stored as 16-bit.
3. `MaxWaterDepth` — integer, default the record's own prior value (preserved; zero on the first parse).
4. `MinWaterDepth` — integer, default the record's own prior value (preserved).
5. `MaxSlope` — integer, default the record's own prior value (preserved), stored as a byte.
6. `BadSlope` — integer, default **half (`>>1`) of the `MaxSlope` value just read**.
7. `MaxWaterSlope` — integer, default the record's own prior value (preserved), byte.
8. `BadWaterSlope` — integer, default **half (`>>1`) of the `MaxWaterSlope` value just read**.

Three clamps then run **unconditionally on every class**, in order:

* if movement class MaxWaterSlope is below movement class MaxSlope, MaxSlope becomes MaxWaterSlope;
* if the resulting MaxSlope is below BadSlope, BadSlope becomes MaxSlope;
* if MaxWaterSlope is below BadWaterSlope, BadWaterSlope becomes MaxWaterSlope.

**Established fact:** The comparisons are unsigned byte comparisons with no authored gate; the three checks execute for every class regardless of which keys were authored.

#### R-CONTENT-01 — Movement-profile template initialization (closed)

**SUPERSEDED 2026-08-27 by `[04 §6.1 R-DOC04-A]`.** Everything between this
note and the "Consequence" note at the section tail — the falsification of
the template hypothesis, the "all eight fields zero" initial values, and the
zero-prior reading of the preserved defaults — was derived from a writer
census that missed a startup initializer registered in the CRT
function-pointer table, which pre-fills all 32 records (through the pool base
plus a small offset) with `MaxSlope` = `BadSlope` = `MaxWaterSlope` =
`BadWaterSlope` = 255, `MaxWaterDepth` = 10000, `MinWaterDepth` = −10000
before the first parse. The corrected, authoritative contract — startup
template pre-fill, no reset between classes, unconditional clamps, and the
consumer-side layer classifier — is `[04 §6.1 R-DOC04-A]` and `[R-DOC04-B]`
in document 04. The text below is retained unmodified for the audit trail;
read it as history, not contract.

**Correction.** The previous text carried a standing `TODO(question)` whose
working escape hatch was a profile template pre-filling each record (with
`MaxWaterSlope ≈ 255`) before the first class parses, so that an absent
`maxwaterslope` would preserve a large value and the clamps would be identity.
That hypothesis is **falsified**: it was written when the pool's initial bytes
were unproven; the executable-layout verification (against the PE
section table) shows the pool is zero-filled at load, and the bounded writer
census shows the `CLASS` loop is the pool's only writer. There is no template
write before the first parse. **Established.**

The initialization contract, settled:

* **Initial values — Established.** The class pool (32 records × 32 bytes)
  lives in the executable's data section beyond the raw-data extent, so the PE
  loader zero-fills it; the loader's prologue performs no fill and no template
  copy, and a bounded census over the whole text segment finds the `CLASS`
  loop as the pool's only writer. Every record therefore starts with a null
  name slot and all eight fields **zero** before the first parse.
* **Reset between classes — Established: none.** Each `CLASS%d` index owns one
  record slot, and each slot is parsed at most once per compile. The "prior
  value" defaults read the record's **own** prior bytes — not the previous
  class's values and not a shared template. Because the pool starts zeroed
  and nothing else writes it, a later class that omits `maxwaterslope` holds
  **zero** (its own zero prior). The battle-entry catalog rebuild (§8)
  re-parses the same pool in place, so on a re-parse an omitted key defaults
  to that record's own previous-parse value; for stock content the re-parse
  result is bit-identical.
* **Clamp conditionality — Established: none.** All three clamps are unsigned
  byte comparisons and run on every class. No clamp is conditional on key
  presence, and none can be: the parser reads every field through the integer
  accessor, which cannot distinguish an absent key from an authored zero (§4),
  so a key-presence gate is not expressible with the accessor retail uses
  here. The bounded search found no authored-flag storage beside the record.
* **Record fields — Established.** Each 32-byte record holds the interned
  authored `name` value in its head (a missing `name` key yields the empty
  string, still interned) followed by the eight parsed fields; the remaining
  bytes are never written and stay zero.

**Per-field conversion — Established.** Every field goes through the integer
accessor: optional sign, decimal digits, trailing junk ignored, 32-bit result
— and then stores with truncation to the field width. There is no scaling and
no range clamp on the authored value itself.

| Key | Authored domain | Stored width | Arithmetic |
|---|---|---|---|
| `FootPrintX`, `FootPrintZ` | any integer | 16-bit, used signed | store low 16 bits |
| `maxwaterdepth`, `minwaterdepth` | any integer | 16-bit signed | store low 16 bits |
| `maxslope`, `badslope`, `maxwaterslope`, `badwaterslope` | any integer | 8-bit | store low 8 bits; all consumers compare unsigned |
| `badslope` default | — | 8-bit | `(maxslope just read & 0xFF) >> 1` — logical shift, 0..127 |
| `badwaterslope` default | — | 8-bit | `(maxwaterslope just read & 0xFF) >> 1` |

**Consequence and remaining paradox — RESOLVED (superseded by
`[04 §6.1 R-DOC04-A]`, 2026-08-27).** The paragraph below this note was
wrong: its writer census missed a startup initializer registered in the CRT
function-pointer table that pre-fills all 32 class records (through the pool
base plus a small offset) with `MaxSlope` = `BadSlope` = `MaxWaterSlope` =
`BadWaterSlope` = 255, `MaxWaterDepth` = 10000, `MinWaterDepth` = −10000
before any parse. The "all eight fields zero" initialization claim and the
"zero-filled pool" correction earlier in this section are therefore
superseded: omitted keys carry the TEMPLATE values, the unconditional clamps
are identity for them, every stock class compiles to its authored slope
limits, and there is no paradox. The prior text is retained below for the
audit trail; the authoritative statement — template pre-fill, unconditional
clamps, the per-cell layer classifier, and A* blocking only on layer 0 — is
`[04 §6.1 R-DOC04-A]`/`[R-DOC04-B]`. Nanolathe's SC5 gated clamps are
closed out: delete them and initialize from the template.

**Superseded reading (2026-08-27, earlier the same day).** The arithmetic is forced:
every class that omits `MaxWaterSlope` computes `MaxSlope = 0` after the
unconditional first clamp, and the stock catalog's thirteen omitting classes
(only `TANKDH3` and the two hover classes author `MaxWaterSlope`) compile to
`MaxSlope = 0` — while the movement classifier below hard-blocks every land
cell whose slope exceeds `MaxSlope`. All writers and comparisons are
enumerated and byte-verified; the contradiction with assumed stock playability
is a genuine bounded unknown whose decider is a runtime trace of the compiled
pool or of a unit definition's slope copy, not further static analysis.
Nanolathe's gated clamps (clamps 1 and 3 gated on whether `MaxWaterSlope` was
authored, per `docs/SPEC_CONFLICTS.md` SC5) remain the install-compatible
divergence.

**Record identity and FBI resolution.** Each parsed record's head is the
interned value of the section's authored `name` key (string accessor, 100
bytes, default empty) — not the `CLASS%d` section name. The FBI compiler
resolves its `movementclass` string by a linear scan of the 32 records with
the case-insensitive comparison against that interned name value; records with
a null name slot are skipped; a miss yields the null profile. This closes the
question of whether `MovementClass=TANKSH2` resolves against `CLASS%d` names
or the authored `Name`: it resolves against the authored `Name` value, exactly
as `research/formats/tdf.md` states.

**Fallback template for an unresolvable movement class.** When the FBI
movement-class lookup fails, the unit's compiler initializes a scratch record
with `MaxSlope = BadSlope = MaxWaterSlope = BadWaterSlope = 255`,
`MaxWaterDepth = 10000`, `MinWaterDepth = -10000`, and then parses that
scratch record with the same eight-key parser against the unit's own section —
so an FBI that authors footprint/slope/depth keys supplies them, and everything
unwritten keeps the 255/±10000 defaults. The unit definition then copies from
the resolved record (pool or scratch): footprint extents, the two depth
limits, `MaxSlope`, and `MaxWaterSlope`.

**Slope consumers — established.** Three consumers read the class record:

* The **movement classifier** that builds the 2-bit passability layer used by
  path search reads the record's four slope bytes directly. Per cell it
  computes `slope = hmax − hmin` (the derived 2×2 heights). On land (`hmin ≥
  SeaLevel`): `slope ≤ BadSlope` is clear; `BadSlope < slope ≤ MaxSlope` is the
  passable-but-penalized steep tier; `slope > MaxSlope` is hard-blocked. Under
  water (`hmin < SeaLevel`) the same three-way split uses `BadWaterSlope` and
  `MaxWaterSlope`. **`BadSlope`/`BadWaterSlope` are therefore read, and the
  "soft tier retained for cost" reading is the steep band of this
  classifier** — upgraded from supported inference to established fact for
  the three-way split; the path cost the steep band carries is owned by
  document 04.
* The **structure validator** (building placement, yard-gated) aggregates the
  footprint's derived minima/maxima over the yard-selected cells and fails
  when `bMax − bMin > MaxSlope`; it never reads `MaxWaterSlope`, and its
  non-land branch compares the peak contributor against the sea level minus
  the unit's waterline. Water-depth gates fail when the footprint minimum is
  below `SeaLevel − MaxWaterDepth` or the maximum is above
  `SeaLevel − MinWaterDepth`.
* The **mobile-movement wrapper** (non-structure units) applies a per-cell
  depth-excess gate: `(SeaLevel − MaxWaterDepth) − hmin ≤ MaxSlope`, or, when
  the cell is underwater, `≤ MaxWaterSlope`. This is a depth gate, not a
  terrain-slope test; with the stock clamped values it degenerates to the pure
  `MaxWaterDepth` limit.

**Established:** Slope is derived from a 2×2 height neighbourhood. The plot expansion computes per-cell derived `MinHeight` and `MaxHeight` as the minimum and maximum of up to four height bytes (cell, east, south, southeast, with edge guards) — these derived values are the slope inputs, not a single height sample. Height queries use bilinear interpolation of the four corner heights with low-four-bit fractions and signed-bias correction. Validation aggregates `min of mins` and `max of maxes` across the footprint rectangle. Passability comparisons are strict `<` for the hard blocks (`slope == limit` passes) and `≤` for the clear/steep boundary. Land-vs-water slope selection happens per cell in the movement classifier (`hmin` below sea level switches to the water pair) and in the mobile-movement wrapper; the structure validator's slope gate always uses the land pair.

### Sound aliases

The alias catalog is a TDF in the game-data directory. Each top-level section
is one alias; its `sound` key names the sample. Alias registration
deduplicates path/alias entries and is capped at **255** registrations, each
holding a 32-byte alias name. Decoded samples are cached for subsequent
playback; the cache and mixer are separate from the byte-level sample decoder.
Alias-cache eviction is bounded-negative (no eviction site found in the
census) `TODO(question)`; sample precedence follows the VFS mount order
established in §2.

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
because the bare read is what gates the numbered loop. **Correction (SC7):**
this gating is wrong — the numbered loop runs regardless of whether the bare
key is present. The stock corpus ships no bare forms at all for the core cues
(120 `select1`, 76 `ok1`, 76 `cant1`, 63 `arrived1`), and retail still counts
one variant for each via the numbered path; a strict bare-gate would mute them.
See `[03 §8.3]` for the full sound-category contract and
`docs/SPEC_CONFLICTS.md` SC7.

### R-P0-03 — Category token registry and membership-bitset compilation

Addendum R-P0-03 is folded into this subsection; the consumption of these
bitsets by weapon and order masks is owned by document 06 §3.1.

#### §1 — Result

**Established.** Retail does **not** assign one bit per category token by
hashing the token. A category token is a case-insensitive registry key whose
value is a bitset of unit definition IDs. Compiling a unit's `category` string
sets that unit's ID bit in the bitset for every token named by the string.
Weapon and order masks then refer to those token bitsets and test candidate
unit IDs.

An earlier reading — one bit per token derived by hashing the token name
(`FNV(token) % 32`) — is the wrong contract: it would make category bits token
ordinals or hash buckets, while retail makes them membership sets indexed by
unit ID. The hash reading was tried and is rejected by the recovered static
path; this paragraph records the reversal so it stays auditable.

#### §2 — Registry construction

The category registry is a sorted vector of entries, each holding a name
pointer and a bitset pointer. Lookup uses a case-insensitive comparison. If a
name is already present, lookup returns the existing bitset; otherwise it
allocates a zeroed bitset of 16 32-bit words (512 unit-ID positions), stores
the normalized name/entry, and inserts it into the case-insensitively sorted
vector.

There is no evidence of an ordinal assigned to a category token and no evidence
that token insertion order controls unit membership bits. The stable unit
catalog is built in a separate stage, with unit IDs assigned after the
catalog's case-insensitive record ordering (§5 above).

The registry's bitset layout:

```text
word = unitID >> 5
mask = 1 << (unitID & 31)
```

The compiler sets `registry[token][word] |= mask`. The same unit ID can be set
in many token bitsets. Downstream, a bad-target/no-chase mask for token T is
effectively tested as `T.bitset[unitID >> 5] & (1 << (unitID & 31))` [06 §3.1].

#### §3 — Category-string compilation

The `category` value is scanned as a whitespace-separated sequence (a `%s`-style
token scan that also reports the consumed-character count). Each token is
looked up or created in the registry, then the current unit ID bit is ORed into
that token's bitset. Empty or repeated whitespace has no semantic effect.

After the token loop, the compiler also looks up the registry entry named
by the literal token `ALL` and sets the current unit ID bit in that bitset.
`ALL` membership is mandatory and unconditional: every compiled unit is a
member of the `ALL` category regardless of its authored tokens, and an
authored `ALL` token is a no-op duplicate. This corrects the earlier reading
of an "empty/sentinel entry with no established spelling": the entry is the
ordinary token `ALL`, and it is consumed through the ordinary registry
lookup — a mask built from the name `ALL` (for example an authored
`noChaseCategory=ALL`) matches every unit. The `ALL` literal also appears in
the campaign-side selector (a `campaignside` value of `ALL` matches any
side), which is a separate consumer.

Unit category fields and weapon masks use the same registry. The bad-target
fields (`wpri_badTargetCategory`, `wsec_badTargetCategory`, and
`wspe_badTargetCategory`) and `noChaseCategory` default to the authored token
`none` when absent. `none` is not a reserved compiler keyword: it is looked up
like any other token, so its mask is empty unless an authored unit is actually
compiled into that token [06 §3.1].

#### §4 — Unknown and duplicate handling

| Input case | Retail behavior | Confidence |
| --- | --- | --- |
| Unknown token | Create a zeroed registry bitset; do not reject the unit or emit a required diagnostic. It remains empty until another unit contributes the same case-insensitive token. | Established |
| Duplicate token in one `category` string | OR the same unit bit again; no duplicate membership and no second registry entry. | Established |
| Same token with different case | Case-insensitive lookup returns the same registry entry/bitset. | Established |
| Duplicate category names across units | One registry entry; each unit ID is ORed into that entry's bitset. | Established |
| Empty/missing category value | Token loop contributes none, but the mandatory `ALL` registry membership is still set. | Established |
| `none` default mask | Ordinary registry lookup; zero unless a unit is a member of `none`. | Established, with authored-data caveat |

Unknown category text is thus tolerated data, not a hash collision and not an
error path. A later definition can populate an already-created empty bitset,
which is why registry construction must not discard unknown names during the
first pass.

#### §5 — Related mask lookup

The mask helper first resolves a name against the unit-name index. If the name
is a unit name, it sets that one unit ID bit; otherwise it ORs the whole
category bitset into the output mask. This preserves the distinction between a
direct unit target and a category target and further rules out a
token-to-single-bit model [06 §3.1].

#### §6 — Algorithm and ordering contract

```text
build unit catalog
  case-insensitively order unit records
  assign stable unit IDs

for each unit in stable ID order
  for token in whitespace_tokens(unit.category)
    set category_registry[casefold(token)][unit.id] = 1
  set category_registry[empty_sentinel][unit.id] = 1

compile authored bad-target/no-chase category names
  resolve one case-insensitive registry entry per name
  retain its unit-ID bitset pointer/value
```

The category vector is sorted for lookup; token order within one string does
not affect the resulting bitsets. Unit ID ordering is the determinism boundary:
the same case-insensitive catalog order must be used before setting bits.
Registry allocation and membership are integer operations; no FNV, modulo, or
floating-point step participates.

#### §7 — Evidence and confidence

- Unit category fields, defaults, two-stage catalog loading, and retained
  unknown keys: §5 above.
- Category masks as unit-target membership sets: [06 §3.1].
- Exact registry shape, case-insensitive sorted insertion, zeroed 16-word
  allocation, whitespace tokenization, unit-ID word/mask calculation, and the
  mandatory `ALL` membership: static-analysis notes kept outside the
  repository.

Confidence is high for registry identity, bitset layout, unknown/duplicate
behavior, unit-ID indexing, and the `ALL` sentinel token; the earlier
"empty/sentinel entry with unknown spelling" reading is retracted.

#### §8 — Implementation guidance and unresolved question

Compile a registry entry to a mutable/immutable 512-bit unit-membership mask,
not to a token ordinal. Casefold names using the retail-compatible
case-insensitive comparison, sort registry entries for deterministic lookup,
retain unknown names with zero masks, and OR duplicate memberships. Assign unit
IDs only after the stable case-insensitive unit catalog ordering. Keep `none`
as a normal token default unless content analysis proves an authored special
case. Set every unit's ID bit in the `ALL` entry as the mandatory membership.


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
| `colorf`, `colorb` | integer | 0 | masked to 16 bits; GUI semantic palette fields, resolved through the per-window GUIPAL→PALETTE map before primitive/FNT writes |
| `texturenumber`, `fontnumber` | integer | 0 | stored as bytes |
| `active` | integer | 0 | stored as a byte |
| `commonattribs` | integer | 0 | stored as a byte |
| `help` | string | empty | |
| `gaffile` | integer | 0 | stored as 16-bit |

**Panel header keys, read on the header gadget:** `totalgadgets` (integer,
default 0, stored as 16-bit), and `panel`, `crdefault`, `escdefault`, and
`defaultfocus` (strings, 16 bytes each, default empty). The header may also
contain a `[VERSION]` subsection with `major`, `minor`, and `revision`
(integers, default 0, stored as bytes); **the subsection is optional in the
parser** — a missing `[VERSION]` is skipped silently and the three bytes stay
zero — even though every one of the 368 retail GUIs authors it.

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

**Contradiction (recorded 2026-08-27, open):** document 06 §6.5 states the
per-field reading — each zero parameter substitutes ITS corresponding
default — while this section states the all-or-nothing reading above. The
two disagree whenever a map authors only SOME parameters as zero. Nanolathe
currently implements the per-field ([06 §6.5]) reading
(`combat.EffectiveMeteor*`). Decider: one probe — a mission authoring, say,
radius nonzero and density zero, then trace which values reach the spawned
storm. Until probed, treat the substitution granularity as
`TODO(question)`; nothing else in either contract depends on it.

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
converts authored degrees onto the engine's 65,536-per-turn angle scale by a
fixed-point magic multiply with truncation toward zero (see document 08, which
owns the exact equivalence domain: identical to `trunc(degrees × 65536 /
360)` for non-negative degrees below the 32-bit wrap, plus one unit for
negative degrees, wrapping correctly through 360..65535; an earlier reading
held the exact rounding chain as supported inference — superseded). `BuildPriority`,
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

**Mission-file diagnostics.** The mission open path owns six exact strings
(five previously recorded; the sixth was missing from the census). Five
report through the status pane:

* `The requested mission file, %s, does not exist.` — a campaign-mission
  request whose `MISSION%d` section is absent from the campaign file;
* `Hey, joker!  Mission file %s is corrupt (no header found).`
  (two spaces after `joker!`) — a campaign-mission OTA that parses but has no
  `GlobalHeader` section;
* `Hey, joker!  There is no mission defintion for this mission: %s`
  (two spaces after `joker!`; the misspelling "defintion" is verbatim) — a
  campaign-mission request whose `Maps\<missionfile>.OTA` file fails to open
  or parse, the `%s` being the authored `missionfile` name;
* `Old TED format no longer supported!` — a campaign-mission request whose
  `missionfile` key is absent;
* `No GlobalHeader block in mission file!` — a directly supplied (skirmish/
  multiplayer) OTA that parses but has no `GlobalHeader` section.

The sixth string, `No suitable schema type in mission file!`, uses a
different message channel, emitted when the schema selector accepts no
schema. A directly supplied OTA whose file fails to open/parse emits **no**
diagnostic: the loader retries through the alias probe and returns silently.
Campaign loads own the sibling `The requested campaign file, %s, does not
exist.`.

**Terrain file.** The terrain loader accepts exactly two version words and
rejects anything else with a diagnostic naming the value. Both versions share
the leading header fields — version, cell width, cell height, a tile-map
offset, an attribute-array offset, a tile-graphics offset, a tile count, a
map-object count, a map-object name-table offset, and a sea-level value — and
all offsets are file-relative and biased by the file base at load.

The two versions differ in the tail of the header and in the attribute record:

* The **legacy** version carries minimum wind, maximum wind, and gravity in
  its own header, plus a minimap offset and a minimap-present flag, and uses
  an 8-byte attribute record whose layout is established below.
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
selects a 1,024-byte 32-by-32 indexed block in the tile set.

The canonical attribute array is `Width × Height × 4` bytes, one record per
attribute cell: height byte at the first byte, feature reference as little-endian
`uint16` at the next two bytes, and an unknown byte that is zero across the
entire retail corpus (171 canonical maps) and is not carried into runtime state.
The loader accepts both versions, allocates a tile map of
`(Width/2 × Height/2)` `uint16` entries, allocates tile graphics, reads the
feature name table (`TileAnims × 132` bytes: `uint32` index plus 128-byte name),
and then expands the attribute array into a dense plot array of `Width × Height`
13-byte cells in row-major order (west to east, north to south).

The runtime plot cell is 13 bytes with a typed layout. All multi-byte fields
are little-endian.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

Void and edge generation runs after the derived minimum/maximum heights are
recomputed for the full map and after feature placement. It performs:

* **Right-edge void:** columns `Width-2` and `Width-1` are set to `0xFFFD` for
  every row where the feature word is `0xFFFF` or `0xFFFE` — `2 × Height`
  cells at most; live features and anchors in those columns survive. The
  playable inset is also set to `PlayRight = WidthPixels - 0x20` (32 pixels,
  two cells) and `PlayBottom = HeightPixels - 0x80` (128 pixels, eight
  cells), which the camera clamp enforces.
* **North-edge void:** an empty-or-fringe cell at row z is set to `0xFFFD`
  when `z*16 − (height >> 1) < 0` — i.e. when the cell's raw height byte
  exceeds `z*32`, so its half-height pokes above the north map edge. Row 0
  voids any height ≥ 1, row 1 heights > 32, row 2 > 64, row 3 > 96, row 4 >
  128, row 5 > 160, row 6 > 192, row 7 > 224, rows 8 and beyond never.
* **South-edge void:** walking rows upward from `Height-1`, an empty-or-fringe
  cell at row z is set to `0xFFFD` when `z*16 − (height >> 1) > PlayBottom`,
  equivalently `(Height-1-z)*16 + (height >> 1) < 112` — low cells near the
  south edge whose terrain surface would fall below the play area. The last
  row voids heights < 224, `Height-2` < 192, `Height-3` < 160, `Height-4` <
  128, `Height-5` < 96, `Height-6` < 64, `Height-7` < 32, `Height-8` never.
  (This closes the earlier `TODO(question)` on the north/south predicates.)
* **Lava-world flood:** when the mission `lavaworld` flag is set, a bulk sweep
  sets `0xFFFD` for every cell where `hmin ≤ SeaLevel` and the feature word is
  `0xFFFF` or `0xFFFE`, turning the entire low basin into void.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

Outside the map rectangle, height returns the sentinel `-1` with unsigned
candidate bounds before any terrain read; the movement validator returns
blocked for generic modes but returns pass for factory-exit search mode 2; the
LOS writer stores an empty footprint and returns; projectiles report no terrain
collision; and the camera is clamped to the `PlayRight`/`PlayBottom` insets
above.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

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

**Frame header, 24 bytes.** Width and height as 16-bit values, signed 16-bit
x and y offsets, a color-key byte (constant 9 across the whole retail corpus;
it is the transparent palette index of the raw-pixel path — the blitter skips
every source pixel equal to it), a compression flag byte (0 = raw pixels,
1 = per-row RLE), a 16-bit subframe count, a 4-byte zero field, a 32-bit data
offset, and a trailing reserved word. The data offset is always biased by the
file base. (This corrects an earlier description that summed to a 21-byte
header with a "reserved" byte at offset 8 and a one-byte subframe count: the
byte at +8 is the color key, the subframe count is a 16-bit value at +10, and
the header is 24 bytes; see `research/formats/gaf.md` [fmt gaf].)

**Raw frame payload.** When the compression flag is clear, the data offset
points directly at `width × height` bytes, row-major, one palette index per
pixel. Raw frames carry no skip runs: their only transparency is the frame's
own color key, so palette index 0 is opaque on this path. (This path was
missing from earlier revisions; 6,068 of the 48,519 retail frames are raw.)

**Compressed frame payload.** When the compression flag is set, the data
offset points at a row table: one 16-bit stored byte count per row followed by
that row's command stream. Each command is a **byte** read as:

* low bit set — skip `(command >> 1)` pixels, leaving destination untouched
  (this skip is the only transparency mechanism on the RLE path; palette
  index 0 is opaque black);
* otherwise bit 1 set — repeat the following byte `(command >> 2) + 1` times;
* otherwise — copy `(command >> 2) + 1` literal bytes.

(The earlier "16-bit word" command reading is wrong — commands are bytes;
a word-width decoder mis-decodes every RLE row.)

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

An override path accompanies that hash and is now fully traced: for each unit
definition, the loader builds the logical path `units\<unitname>.OVR` and
opens it as a HapiBank with the account filter `TA Unit Override`; if an
account named `Compatability` exists, the bank's integer item named by the
decimal spelling of the unit's computed checksum is read and **replaces the
computed definition hash** at its definition offset. No further accounts or
items are probed. Which gate consumes the replaced hash is owned by document
08 (the content-identity comparison in lobby/network metadata); the residual
is reduced to that cross-reference. `.OVR` is not a mounted extension in the
installed corpus (bounded-negative), so the path is stock-inert.

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
  The rebuild is now traced: the battle-entry path calls the same catalog
  compiler that serves startup (unit definitions, movement classes, models,
  scripts, build-menu pages, and side data) immediately after the terrain
  loader and meteor/wind initialization, so each battle entry recompiles the
  catalog in place — the unit-definition array is preserved and re-sorted,
  movement-class records and per-unit fields are re-parsed, and per-map plot
  and tile allocations are remade inside the same path. There is no
  finer-grained invalidation: compilation is all-or-nothing per battle entry,
  and the between-missions flag gates feature placement in the terrain loader.

### Supported inference

The content subsystem should use immutable decoded asset objects with shared
references and a session-owned cache for catalogs/map data. Cache keys should
include logical path, winning provider identity, and relevant decode mode.
This matches retail's reuse while preventing stale data after an overlay or
save/load transition.

## Missing and unknown

Open items only. Each bullet states what is unknown, the section that owns it,
and the decider that would close it. Findings that closed an item live in the
body — several under `R-<id>` headings — and are not restated here.

**Correction (2026-08-28, RWU-00-5).** This tail previously opened with a
~70-line recital of everything the document had closed, followed by a "Still
open" list whose bullets also mixed closures into their prose (the category
`ALL` sentinel, the movement-class slope paradox, the meteor contract). One
bullet was worse than redundant: it still described the movement-class pool as
zero-filled with no template, a reading superseded on 2026-08-27 by
`[04 §6.1 R-DOC04-A]`. The closure narratives are deleted here only; every
finding they recited remains in the body sections that own it.

* Data contract of `ONLLoadConfigFile` · §1 · not decidable from the retail
  executable — the function is defined by `online.dll`. The executable side
  (directory resolution, `ONLGetVersion()==3` gate, 336-byte block, zeroed
  block on any failure) is established.
* Exact bit consumers of the multiplayer lobby flag words · doc 08 · static
  trace.
* Fatal-versus-recoverable classification for the resource families not
  classified in §8 · §8 · static trace.
* Archive entry-flag bits other than the subdirectory bit and the mutable
  enumeration-visibility bit (bit 1, mask `0x02`) · §2 "HPI-family container
  format" · static trace. Bounded-negative today: no other bit is tested by any
  mount, validate, or enumerate path, so synthetic values are inert.
* Whether any shipped archive variant outside the installed corpus departs
  from the container contract in §2 · §2 · asset census.
* Caller-specific duplicate-section merging policies · §4 · static trace. The
  first-match section accessor, the enumerator, and the duplicate-key winner
  are established.
* Behavior for malformed or missing burn sequences on non-filename features
  · §5 "Feature record" · static trace. Marked `TODO(question)` at the site.
* Sound alias-cache eviction policy and the DirectSound streaming flags · §5
  "Sound aliases" · static trace. Eviction is bounded-negative (no eviction
  site in the census); both are marked `TODO(question)` at the site.
* Meteor zero-parameter substitution granularity — whole-record versus
  per-field merge of `gamedata/METEOR.TDF [Default]` · §6 "Map files",
  [06 §6.5] · manual retail observation (a mission authoring one nonzero and
  one zero parameter, tracing which values reach the storm). Marked
  `TODO(question)` at the site.
* Reader for plot-mask bit 7 · §6 "Map files" · static trace over the
  unrecovered regions. Marked `TODO(T23)` at the site; the mask preserves the
  bit and no isolated reader exists in the bounded census.
* Code-page behavior for high bytes · §3 · static trace. Marked `TODO(T23)` at
  the site.
* Language-selection interface, language-specific font fallback, and the
  census of runtime messages that pass through the translation lookup · §3 ·
  static trace for the interface and message census, asset census for the font
  fallback.
* Map schema fallback selection and the complete map content-hash input set
  (header, plot, features, and map descriptor are traced) · §6 "Map files" ·
  static trace; doc 08 owns the lobby consumer.
* Draw-time interpretation of model primitive colour, texture, and flag fields
  beyond what doc 03 narrows, and animation/model texture lifetime · doc 03,
  §6 "Model archive (3DO)" · static trace.
* GUI widget callback map · doc 07 · static trace. The parser-side
  control-kind mapping and the optional `[VERSION]` subsection are established
  in §6 "Interface panel files (`.gui`)".
* Runtime consumers of the unit limit — which active-limit read sites gate
  construction, AI production, and the lobby display, and the per-player
  versus global counter split · doc 05 · static trace. §5 establishes only the
  writer side and the key vocabulary.
* Whether the unit-limit mode flag is read by unrecovered code · §5 · static
  trace over the unrecovered regions. Bounded-negative in the recovered
  corpus, so it is retained-and-inert.
* Consumer list for document 06's category mask helper · doc 06 · static
  trace.
