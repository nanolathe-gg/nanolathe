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
and boots (see `docs/SPEC_CONFLICTS.md` SC2; the fatal resource is the
`gamedata\` directory). The box that reads `Can't load GAMEDATA.TDF` is the
failure branch of the `gamedata\sidedata.tdf` load, so it is raised by a
missing or unreadable **`SIDEDATA.TDF`**; no file named `gamedata.tdf` is ever
opened (`[R-MALF-01 §5]`). The translation table
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

## 2. Virtual file system and provider precedence

### Established fact

The VFS has a singleton context containing an ordered provider list. The
observed mount/search sequence, established by the mount append order, is:

1. Loose host files (tried first on every open via `fopen`).
2. The revision/patch archive — one fixed name, `rev31.GP3` — with keep-open
   flag 1.
3. Every `*.CCX` with keep-open flag 1.
4. Every `*.UFO` with flag 0.
5. Local `*.HPI` with flag 0. The mount loop carries a ten-valued budget
   that decrements only when a candidate is **newly mounted** (validation
   passed and the full path was not already mounted); the loop abandons the
   enumeration when the budget reaches zero, so the eleventh *new* local HPI
   of a given pass is not even attempted in that pass. The budget is
   per-invocation: the mount orchestrator runs at several call sites, each
   restarting the budget, and already-mounted archives never consume it, so
   repeated invocations converge to every valid local HPI mounted. The cap is
   real but per-pass, not a global limit, which is how the 13-HPI reference
   install of `docs/SPEC_CONFLICTS.md` SC1 comes to be fully mounted; mounting
   every local HPI in one pass reproduces the converged retail state.
6. `*.hpi` discovered on each `DRIVE_CDROM` drive with flag 0.

Only tiers 3 to 6 — `*.CCX`, `*.UFO`, local `*.HPI`, and the per-CD-drive
`*.hpi` — are genuine wildcard enumerations, so only those names are
installation-dependent. The revision/patch tier is **not** a wildcard: the
orchestrator formats its pattern from a compiled revision token and hands the
enumerator the resulting literal `rev31.GP3`, which contains no `?` and no `*`,
so the matcher can accept exactly that one name and the enumeration only
discovers whether that file is present. The observed open path tries a
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
loop's CD tier mounts `%c:\*.hpi` from every CD-ROM drive it finds.

The VFS supports both single-file reads and union enumeration. Enumeration
is used for catalogs, maps, campaigns, GUI files, save slots, and sound
aliases; it yields the host sequence first and then each archive in mount
order, and it performs no name filtering of its own. The host filesystem's
current- and parent-directory pseudo-entries are skipped by every **caller**,
not by the enumerator, and directory records are deliberately yielded — an
archive directory as `read-only | subdirectory` with size 0 — because the
shadow-marking pass recurses on exactly those records [R-CAT-01 §1].
Deduplication is likewise not performed inside the enumerator: a path is
enumerable from exactly one provider — the first physical backing wins —
because the shadow pass has already set the enumeration-visibility bit on
every later copy of that path. A `flag & 1` marks a subdirectory.
**Flag census (established):** the union
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
check. **Mount-time validation is the open plus exactly three content
checks.** The reader requires `fopen` success, and then tests, in order: the
four magic bytes `HAPI`; the version bytes `00 00 01 00` at offset 4; and the
normalized footer. Nothing else is validated at mount: the
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

**Established:** the transform is enabled only when the derived working byte
is nonzero. Stored low byte `255` derives zero and disables it, just as stored
zero does; upper key bytes are ignored. For a nonzero working byte, every byte
of the blob from offset 20 onward is transformed in place:

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
is processed recursively at load time. **Established:** each authored pointer
is followed independently; neither a root immediately after the header nor
a contiguous name pool or adjacent records is required.

**Lookup.** A requested path is split on **backslash only**; a forward slash is
an ordinary name character and never separates components. Each component is
compared case-insensitively against the current directory's entries, scanned
**from the last entry backwards**, so where one directory declares the same
name twice the later declaration wins. A non-final component must have the
subdirectory flag set or the lookup fails. There is no special treatment of `.`
or `..`: they are matched as literal names and normally fail, so
archive-relative traversal is not a retail behavior.

**Established consequence — duplicate subtrees.** If two sibling entries name
one directory, only the later directory's children are reachable through that
component. Children unique to the earlier directory do not merge into it. A
later file of the same name also blocks traversal into the earlier directory.
The wildcard enumerator locates its requested directory through this same
component walk before scanning entries forward [R-CAT-01 §1].

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
stored length does not read back completely aborts the read with the
all-ones failure value **silently**; a chunk whose decompression fails is
**fatal** — the diagnostic naming the failure code, chunk index, chunk count,
archive, length and file goes to the fatal channel, and the read never
returns (`[R-MALF-01 §1]`).

**Chunk wire format (`SQSH`, 19-byte header + payload):**

| Offset | Size | Meaning |
|---:|---:|---|
| 0 | 4 | `SQSH` tag |
| 4 | 1 | linked writer stores 2; decoder ignores it; original authoring meaning remains unknown |
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

### Wildcard matching, the union enumerator, and how "first backing wins" is enforced [R-CAT-01 §1]

**Established fact — the wildcard matcher.** Enumeration patterns are
matched by one routine used for loose and archive entries alike. Both the
name and the pattern are compared character by character after folding each
to upper case. `?` matches exactly one character; `*` matches any run,
including an empty one; every other pattern character must equal the name
character. The matcher keeps a set of live pattern positions (a
non-deterministic walk: at a `*` it both stays and advances); the set holds
at most **100** positions and an alternative that would be the 101st is
dropped — inert for any stock pattern. A name matches when, after its last
character, some live position is at the end of the pattern or at a trailing
`*`. A pattern whose basename is exactly `*.*` is replaced by `*` before
matching, so `*.*` matches names without a dot (the host `FindFirstFile`
semantics are reproduced for archives).

**Established fact — the enumerator.** An enumeration handle carries the
directory part of the request (everything up to the last backslash), the
basename pattern, a *current provider* index (−1 = the host file system,
0.. = the archives in mount order), a *current entry* index inside an
archive directory, and a *continue* flag. Each step yields one record in the
C-runtime `_finddata_t` shape: attributes, three times (zero for archive
entries), size, and the name (260 bytes). For an archive entry the
attributes are `read-only` for a file, with the size from its file record,
and `read-only | subdirectory` with size 0 for a directory; host entries
carry what the host reported (attribute bit 4 = subdirectory). Order:

1. with provider −1, the host `FindFirstFile`/`FindNextFile` sequence (host
   order, not sorted — §2);
2. when the host sequence ends and the continue flag is set, archive 0, 1,
   …: in each, the request's directory is located by the same
   backslash-split, last-entry-wins walk as "Lookup" (an archive that lacks
   the directory, or where a component is not a subdirectory, is skipped),
   and its entries are visited **from index 0 upward** (the opposite of the
   lookup's backward scan), yielding every entry whose name matches the
   pattern **and whose flag bit 1 is clear**;
3. an exhausted archive advances to the next; the enumeration ends after
   the last archive, or after the single provider it was started on when
   the continue flag is clear.

Callers skip the names `.` and `..` themselves. Closing a handle releases
the host find handle when one is open and frees the record.

**Established fact — shadow marking (the "first backing wins" mechanism).**
After every mount pass, and before any enumeration, the executable clears
flag bit 1 on every entry of every mounted archive (recursively) and then
walks the whole union from the root with the continue flag set. For every
**file** record the walk yields from provider *P* (host = −1), it looks the
same relative path up in every archive **after** *P* in mount order and sets
bit 1 on the file entry it finds there (a directory entry of that name is
left alone). Every **directory** record yielded — from any provider, since
directories are never marked — is recursed as a new walk of *that
provider's* subtree, so every provider's subtree is visited and the marking
is applied at every depth. The result is that a file path is enumerable from
exactly one provider: the host copy hides every archive copy, and an archive
copy hides the same path in every later archive. This is the deduplication
§2 describes; it is a property of the enumerate path only — the *open* path
never consults bit 1 and simply tries the host, then the archives in order.
The marking walk uses the same enumerator, so it is subject to the same host
ordering; the result does not depend on that order.

**Established fact — mount-list housekeeping** (completing §2's
prose). *Mounting one archive*: the candidate's full path is resolved with
`GetFullPathNameA`, compared case-insensitively against the stored full
path of every mounted provider, and rejected on a match; otherwise the
provider is opened and validated (the three content checks of §2) and
appended to the provider array, which is reallocated by one slot per mount.
*The
validation pass*: for every provider whose handle is closed, the file is
reopened read-only; on failure the provider record and its directory blob
are freed and the array is compacted **preserving order**; on success the
file is closed again and the handle left closed. *Working directory*: the
mount orchestrator begins by resetting the process working directory to the
executable's directory (§1 step 3, repeated on every pass), so a lobby or
save transition cannot leave loose-file resolution pointing elsewhere.

**Established fact — the two list collectors built on the enumerator.** The
flat collector used for `units\*.FBI`, `weapons\*.tdf`, `download\*.TDF`
and the map/campaign lists walks one enumeration with the continue flag set
and appends each yielded **name** to a string vector. The recursive
collector used for the feature
TDF catalog enumerates `<dir>\*`, skips `.` and `..`, recurses into every
directory record with the walk restricted to the provider it came from
(continue flag clear) — the same per-provider recursion as the shadow walk,
so every provider's subtree is visited — and appends `<dir>\<name>` for
every file whose name passes the caller's wildcard filter. Both yield
already-deduplicated paths because the shadow bits have been applied.

### Supported inference

The provider identity should be part of a content manifest. A logical path
alone is insufficient for multiplayer or deterministic save identity because
two installations can resolve the same path to different archive bytes.

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

#### Settings: the persisted values and their defaults

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
| `MixingBuffers` | 8 (`[R-SND-01 §2]`) |
| `Sound Mode` | 1, held in bits 0–2 of the sound flags byte (`[R-SND-01 §2]`) |
| `CDAudioVolume`, `WaveOutVolume` | read only when `RestoreVolume` is set; no default installed |
| `SingleCommanderDeath`, `SingleMapping`, `SingleLineOfSight`, `SingleLOSType` | 1 |
| `MultiCommanderDeath`, `MultiMapping`, `MultiLineOfSight`, `MultiLOSType` | 1 |
| `screenchat` | 1 |
| `PlayMovie` | 1 |
| `NumSkirmishPlayers` | 4 |
| `Nickname`, `Game Name`, `Password` (17-byte buffers), `Image Output Directory` | empty |
| `side` | 0 — the last chosen player side (`[R-KEYS-01 §3]`) |

**Flag settings.** Several options are bits of packed option words rather than
independent values. Their installed defaults are: anti-aliasing, shadows,
vehicle shadows, feature shadows, and shading **on**; dithered fog, damage
bars, alt-switching, clock display, and volume restoration **off**;
acknowledgement effects, build effects, and speech effects **on**; music mode
**on**.
The sound word's bit map, its consumers, and which of these values the
loader actually writes back are in `[R-SND-01 §2]` below.

**Skirmish settings.** The seven scalar values — `SkirmishMap` (a 256-byte
string), `SkirmishLocation`, `SkirmishDifficulty`, `SkirmishLOSType`,
`SkirmishLineOfSight`, `SkirmishMapping` and `SkirmishCommanderDeath` — are
read under the main `Total Annihilation` key. Only the per-slot values
`Player%dController`, `Player%dSide`, `Player%dColor`, `Player%dAllyGroup`,
`Player%dMetal` and `Player%dEnergy` live under
`Total Annihilation\\Skirmish`, where the format is filled with the slot
index. Absent per-slot values install these defaults: controller 0, ally
group 5, metal and energy 1000, color the slot index itself, and side the
slot index masked to parity (slot & 1). `FixedLocations` is emitted by the
settings writer and read by nothing — the loader has no read of it, so it is
write-only legacy, inert (`[R-KEYS-01 §3]`).

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

#### Unit limit [R-CONTENT-03]

**Established.** At startup the executable reads the limit from the Windows
profile file `<executable directory>\totala.ini`,
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

**Established — startup language selection and precedence.** The language
buffer begins empty. Command-line parsing copies every unconsumed positional
token reached by the main token loop into that buffer, so the last such token
wins. This excludes operands consumed inside a recognized switch arm, such as
the configuration reference following `C`/`c`. Only when the buffer is still
empty after parsing does startup query the `language` string under the
`Total Annihilation` registry key, with a 64-byte destination. If the resulting
string is empty, startup stores the literal lowercase name `english`. It then
loads the translation table before the remaining persistent preferences and
content catalogs.

The startup selection buffer and the translation loader's remembered current
name are distinct and both begin empty. Consequently the default `english`
selection is different from the loader's current name and the first startup
call does parse a present `gamedata\\translate.tdf`; it is not skipped as an
already-selected language. A malformed present table therefore still reaches
the parser's failure path under the default English selection even when the
table contributes no `english` entries.

The registry read in this path does not install or write back the English
fallback. Neither `<executable directory>\\totala.ini` nor the `C`/`c` online
configuration block supplies the language string. The resulting selected name
is kept in one global buffer for ordinary startup and drives two distinct
mechanisms.

#### Translation table

The translation loader takes a TDF path and the language
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
base field — a `German` language setting reads `Germanname` before `name`. The
default selected name `english` tries `english<key>` first and then the plain
key; English content therefore normally reaches its unprefixed field through
the ordinary fallback rather than through an empty-prefix special case.

The unit catalog reads its display name and description through this accessor,
so localized unit names and descriptions authored in the unit record **are**
honored.

### The audio preference values: names, defaults, bit map, write-back, and what is not registry [R-SND-01 §2]

The consumers of these values are document 03's and are only cited here.

**Established fact — the packed sound flags byte.** The second packed
option word this section calls the "sound option word" is one byte, loaded
from these DWORD values (`value & 1` for each flag bit, `value & 7` for the
mode field):

| Bits | Value name | Absent → | Consumer |
|---|---|---|---|
| 0–2 | `Sound Mode` | `1` (`Mono`); and the device's 3-D flag is cleared | every play gate requires the field non-zero; value `2` (`3D`) sets the device 3-D flag, any other value clears it `[03 R-AUD-01 §1]`, `[03 R-AUD-01 §2]` |
| 3 | `RestoreVolume` | `0` | gates the `WaveOutVolume` / `CDAudioVolume` round-trip below |
| 4 | `ackfx` | `1` | none — persisted and displayed, gates nothing `[03 R-AUD-01 §2]` |
| 5 | `buildfx` | `1` | none — as above |
| 6 | `speechfx` | `1` | the unit voice line's audible gate `[03 R-AUD-01 §3]` |

**Established fact — the remaining audio values.** `MixingBuffers` (absent
→ **8**) is handed straight to the sound device as its voice limit
`[03 R-AUD-01 §1]`; `musicmode` (absent → bit 0 set) is the CD enable;
`cdmode` (absent → **4**) is the CD play mode `Play All|Random|Repeat|Custom`
= 1..4 `[03 R-AUD-01 §4]`; `fxvol` (absent → 27) and `musicvol` (absent →
32) are the two gauges. `WaveOutVolume` and `CDAudioVolume` are read **only
when bit 3 is set**, and then pushed to the system wave mixer and the CD
auxiliary device respectively; when bit 3 is clear they are neither read
nor defaulted.

**Established fact — `CDLISTS`.** A binary value under the same key,
**2,720 bytes** (20 entries × 136: 32 unused bytes, a 4-byte disc serial,
100 category bytes). It is read once at session initialisation, immediately
after the scalar settings above (absent → the in-memory ring is zeroed), and
written back on the disc-change path and at shutdown. What the ring means
and how a disc is matched is `[03 R-AUD-01 §4]`.

**Established fact — write-back.** The write-back-on-absence rule this
chapter opens with is per value. For the audio values it holds for **`Sound
Mode` only**: `MixingBuffers`, `RestoreVolume`, `musicmode`, `cdmode`,
`ackfx`, `buildfx`, `speechfx`, `fxvol` and `musicvol` install their
defaults in memory without a write; they reach the registry only through
the settings saver, which writes every audio value unconditionally
(`WaveOutVolume`/`CDAudioVolume` only when bit 3 is set, from the *current*
device levels). A bounded census of the loader finds exactly thirty-two
names written back on absence — thirty-one DWORD-valued names plus the
`SkirmishMap` string, whose default is synthesised rather than a constant —
and none of the audio names except `Sound Mode` is among them; the other
thirty-one are display, LOS/mapping, skirmish-scalar and interface values
(including `side`).

**Established fact (bounded negative) — not registry.** `NoDirectSound` and
`UseWindowsSound` are **not** registry values. They are integers read from
the `[Preferences]` section of `<executable directory>\totala.ini` with
default 0, the same profile accessor the unit limit uses (`R-CONTENT-03`
above); the image contains no registry read of either name. Their effect is
`[03 R-AUD-01 §1]`.

### The registry primitive's access masks, and the image-output directory default [R-CAT-01 §2]

**Established fact — the shared helper.** Every registry access opens the
three levels `Software` → `Cavedog Entertainment` → `<subkey>` under the
current-user hive with the *create-key* call, so a missing path is created
on the way down even for a read. The access mask is
`STANDARD_RIGHTS_WRITE | KEY_SET_VALUE | KEY_CREATE_SUB_KEY` for a write and
exactly `KEY_READ` (`STANDARD_RIGHTS_READ | KEY_QUERY_VALUE |
KEY_ENUMERATE_SUB_KEYS | KEY_NOTIFY`) for a read. The read mask is formed by
**adding** `KEY_QUERY_VALUE | KEY_SET_VALUE | KEY_NOTIFY` to the write mask
rather than taking a union with it: the carry out of the shared
`KEY_SET_VALUE` bit is what clears the write-only rights and turns the sum
into `KEY_READ`, so a reimplementation that ORs the two masks together asks
for more access than retail does. A read that fails with `ERROR_MORE_DATA`
(the caller's buffer is too small) reports **success**, with the buffer left as
the API filled it; every other failure reports failure. Handles are closed
in reverse order. The typed wrappers (integer, string, binary) sit on this
one helper.

**Established fact — `Image Output Directory`.** Its default is not empty:
when the value is absent the loader builds `user_images\<name>` where
`<name>` is the Windows account name returned by `GetUserNameA` (256-byte
buffer), or the literal `user_images` again when that call fails or returns
an empty name — so the fallback path is `user_images\user_images`. The
"empty" default in the scalar table above applies to the three 17-byte
strings only. This default is installed in memory; whether the settings
saver writes it back is Unknown (tail).

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

## 4. Generic TDF grammar and semantics

### Established fact

The retail grammar is:

* A section begins with a bracketed name and a brace-delimited body.
* A field is a key, equals sign, value, and semicolon.
* A closing brace returns to the parent immediately after that byte; there
  is no optional trailing-semicolon rule for sections. Nanolathe currently
  accepts an adjacent `;` as a host compatibility extension.
* Comments may use line or block form.
* Spaces, tabs, carriage returns, and line feeds are whitespace.
* There is no quoted-string or escape syntax in the observed grammar.

Before parsing, comments are blanked: `//`-to-end-of-line and `/* ... */`
spans are overwritten with ASCII spaces **preserving every character offset**,
so all downstream offsets see text of unchanged length. Comment delimiters
are recognized even inside would-be values; they are not quoted literal text.
Inline trailing comments after the `;` are blanked too. An unterminated `/*`
blanks everything through end of file. Sibling sections retain source order;
key vectors are maintained by the
insertion rule below. Both key lookup and first-match section lookup use
case-insensitive comparison.

Repeated sections remain separate nodes. A first-match section accessor is
common — it linear-scans and returns the first matching sibling, duplicates
retained — while explicit enumerators can visit every section.

**Key insertion and the duplicate-key rule — Established.** The section's key
vector is never sorted; it is built by insertion, one assignment at a time, in
file order. Each insertion binary-searches the vector for the **lower bound** of
the key under the case-insensitive comparison, then compares byte for byte
against the entry **at that position only** — the head of the run of entries
that fold-compare equal, and no other member of that run. An identical spelling
assigns the value in place, so it is last-write-wins with one entry that keeps
its original position. Any other outcome inserts the new entry **at the lower
bound**, in front of every fold-equal entry already present. The typed
accessors take the same case-insensitive lower bound and read the entry there,
so **the case variant a typed lookup returns is the last one parsed.** Because
the byte comparison sees only the run head, a spelling that a later variant has
pushed behind the head is inserted again rather than assigned, and one spelling
can hold two entries.

This replaces the previous text, which said accessors return "the first variant
in case-insensitive sort order — deterministic but not last-wins", marked that a
supported inference needing black-box confirmation, and added "stock content does
not rely on that edge case". The ordering claim was inverted and the last
sentence was false. Twelve stock unit records author two spellings of one
movement key inside one `[UNITINFO]` — `MaxWaterDepth=0` early and
`maxwaterdepth=255` at the end (`ARMFIG`, `ARMLANCE`, `ARMSEAP`, `ARMSEHAK`,
`ARMSFIG`, `Armcsa`, `CORHUNT`, `CORSEAP`, `CORSFIG`, `CORTITAN`, `CORVENG`,
`Corcsa`), and they are the only two-spelling, two-value key runs in a 438-file
sweep of `units/`, `weapons/`, `features/`, `gamedata/` and `guis/`. Retail
therefore resolves their maximum water depth to 255. The inverted reading gave
0, which makes `[04 R-AIR-01 §6a]`'s aircraft water rule dead code for every
aircraft — a floor derived from a maximum depth of 0 already sits at sea level,
so its "raise the floor to sea level" arm can never fire — and which is why no
seaplane could treat water as landable ground. `[fmt tdf "Duplicate keys"]`
carries the same rule for format readers.

**Malformed input and failure behavior.** The tokenizer reports parse errors
with the exact prefix `Parse error in .TDF File! ` (trailing space; the
box title is the application name), followed by one of five diagnostics,
verbatim:

1. `Data field - '=' not found`
2. `Data field - ';' not found`
3. `Sub-record - closing ']' not found`
4. `Sub-record - opening '{' not found`
5. `End of file - nextblock not zero`

All five go to the **fatal channel** — the system-modal box titled with the
application name, then process exit code 1 — so a syntax error anywhere in a
TDF the executable parses ends the process; no partially-built tree is
returned. The detail suffix is ` - name = '<section>' from file <path>`
(the literal word `name`, then the section the parser was inside — `root`
at the top level — then the logical path). What *is* recoverable is a
**missing or empty file**: the loader returns no tree, typed reads return
their defaults, and the resource family decides whether that is fatal
(`[R-MALF-01 §4]`).

### Typed accessors

Every typed read goes through one of a small fixed family of accessors. All of
them locate the key the same way: a **binary search** over the section's sorted
key/value vector using the case-insensitive comparison, followed by a
confirmation compare. Sections are located by a **linear** case-insensitive
scan. A key whose stored value pointer is absent is treated as missing.

| Accessor | Absent key | Present key |
|---|---|---|
| Integer | returns the caller's default | CRT decimal integer conversion: optional sign, leading whitespace, decimal digits, trailing junk ignored, zero for unparsable text; no hexadecimal syntax |
| Floating | returns the caller's default | CRT decimal conversion constructs binary64, then returns it at working precision; caller scaling starts from that already-converted value |
| **Fixed-point** | stores the caller's default **verbatim** | floating conversion, multiplied by 65,536, truncated toward zero — the caller's default is therefore already in 16.16 units |
| String | copies the caller's default **without applying the length limit**, and reports "defaulted" | bounded copy to the caller's limit with forced termination, and reports "found" |
| Raw | reports absent | returns the stored text pointer, which is how a caller distinguishes an authored key from a missing one |
| Language-prefixed string | falls through to the plain key, then to the string-accessor rules above | as the string accessor, after trying `<language><key>` first |

Consequences a reimplementation must preserve: an absent numeric key returns
the caller default, while an authored zero returns zero, even with a nonzero
default. Numeric accessors do not separately report whether a key was found;
the string/raw accessors do. The fixed-point accessor's default is already in
stored units, so a default of 65,536 means an authored value of 1.0.

**Established — floating scanner and result.** The linked conversion skips
ASCII whitespace including vertical tab and form feed, then accepts a sign,
decimal mantissa and optional `e/E/d/D` exponent. An exponent without digits
is ignored. The initial decimal point is `.`; hexadecimal, infinity and NaN
spellings are not recognized. Failed mantissas produce positive zero. Range
status is ignored: the binary64 result retains signed infinity, subnormals or
signed zero; an all-zero mantissa retains its sign even with a huge exponent.
`[fmt tdf "Floating conversion"]` specifies authored examples and the remaining
long-decimal rounding and locale limits. Caller fixed-point scaling/truncation
follows this conversion and retains its existing contract.

Other typed behavior:

* An explicitly present empty string differs from a missing key in the raw
  tree.
* There is no boolean parser. Callers interpret the numeric result; packed
  unit and weapon flags keep only its low bit [R-KEYS-01 §5]. A universal
  nonzero truth test would incorrectly turn authored 2 into a set flag.
* Unknown fields are retained by the parser but ignored by a caller that does
  not request them.

The implementation should preserve raw text, original spelling, source file,
provider, duplicate history, parsed value, and the caller default used. This
is necessary to distinguish missing, empty, valid, malformed, defaulted, and
derived values even though retail runtime structures often collapse those
states.

### Key, value and section-name trimming [R-CAT-01 §3]

**Established fact.** The parser hands every section name (the text between
`[` and `]`), every key (the text before `=`) and every value (the text
between `=` and `;`) through one trimming step before storing it: leading
characters in the set space, tab, carriage return, line feed are skipped,
and the same set is stripped from the end. A span that is entirely blank
stores the **empty string** (it is interned, not null, so a present-but-empty
key is distinguishable from an absent one as §4 states). Nothing else is
normalised: interior whitespace, quotes and case are stored as authored.
The comparison that later finds a key or section is the case-insensitive
one; the trimming is what makes `name = Foo ;` and `name=Foo;` the same
authored value.

### Supported inference

The parser should expose both a lossless tree and typed convenience accessors.
The lossless tree must retain duplicate sections and case-variant keys even
if a compatibility accessor follows retail first/last behavior.

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

#### Unit record: identity and presentation

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
run the roulette, and index the unit-definition table with the pick result.

#### Unit record: economy

| Key | Accessor | Default |
|---|---|---|
| `buildcostenergy`, `buildcostmetal` | integer, stored as single float | 0 |
| `energymake`, `energyuse`, `metalmake`, `extractsmetal` | floating | 0.0 |
| `windgenerator`, `tidalgenerator` | floating | 0.0 |
| `energystorage`, `metalstorage` | floating | 0.0 |
| `makesmetal` | integer | 0 |
| `buildtime` | integer | 0 |
| `workertime`, `healtime` | integer | 0 |
| `cloakcost` | integer, stored as floating | 0 |
| `cloakcostmoving` | integer, stored as floating | the value just read for `cloakcost` |

#### Unit record: movement and geometry

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

**Runtime units of the locomotion fields — Established.**
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

#### Unit record: combat and sensors

| Key | Accessor | Default |
|---|---|---|
| `maxdamage` | integer | 0 |
| `sightdistance`, `radardistance`, `sonardistance` | integer | 0 |
| `radardistancejam`, `sonardistancejam`, `mincloakdistance` | integer | 0 |

**Flags and postures.** The following use the integer accessor with default
0, except the two standing orders, which default to 2. The packed flags retain
only the low bit; `bmcode` retains its unsigned byte and standing orders retain
their authored field widths. [R-KEYS-01 §5] owns the individual stores:

`standingmoveorder`, `standingfireorder`, `init_cloaked`, `downloadable`,
`builder`, `stealth`, `bmcode`, `zbuffer`, `isairbase`, `istargetingupgrade`,
`teleporter`, `hidedamage`, `shootme`, `armoredstate`, `activatewhenbuilt`,
`canfly`, `canhover`, `upright`, `floater`, `amphibious`, `isfeature`,
`noshadow`, `immunetoparalyzer`, `hoverattack`, `antiweapons`, `digger`,
`onoffable`, `mobilestandorders`, `firestandorders`, `canstop`, `canattack`,
`canguard`, `canpatrol`, `canmove`, `canload`, `canreclamate`, `canresurrect`,
`cancapture`, `candgun`, `kamikaze`, `norestrict`, `showplayername`,
`commander`, `cantbetransported`.

**Definition-flag mapping — Established.** The unit parser reads
the `onoffable` integer and packs its boolean value into bit 2 (`0x04`) of the
definition flags word. This bit is not an authored unit `noradar` field and is
not derived from runtime cloak or hidden state. The unit parser has no
`noradar` accessor; `noradar` is a weapon-record key. The radar-circle use of
this definition bit is specified in [03 §3.9].

**Established — `wacky`.** The catalog parses and retains this flag. The
multiplayer restriction tree uses it for zero-valued initial limits, and the
restriction screen's reset operation uses it for zero-valued rows
[05 R-SHARE-01 §9][08 R-SKIR-01 §10]. Those consumers do not establish a
single-player gameplay effect; a blanket no-reader claim is incorrect.

**Established — downloadable enforcement.** After build-menu compilation,
only the first item's product name in each download record participates in
this pass. A matching definition with `downloadable` clear has it set. The
site formats `Hey!  Somebody forgot to set downloadable=1 for %s` without
displaying it and does not sort or re-finalize the catalog. The complete
algorithm and the separately unresolved diagnostic site are recorded under
[R-CAT-01 §8] and "Missing and unknown".

`selfdestructcountdown` is read with the raw accessor, so the record can tell
an authored value from an absent key.

**The runtime *compatible* / *creatable* bit is not a key — Established.**
Bit 23 of the record's first definition-flags
word is written only by the executable: set for the `None` sentinel and for
every FBI whose `Version`, `Copyright` and loose-file gates pass
([R-CAT-01 §4]); carried with the record by the compaction moves
([R-CAT-01 §4], [R-CAT-01 §5] step 3); cleared and re-set per definition by
a campaign mission's `UseOnlyUnits` file at battle entry ([08 R-ENTRY-01
§2] step 4) and by the multiplayer restriction dialog ([05 R-SHARE-01 §9]).
Its readers are the two compactions and the unit allocator ([05 R-SHARE-01
§8]). No FBI key parses into it and no accessor reads it as authored data.

### Building heading field audit [R-P28-ANG-01R §1]

**Established — authored values.** An asset-backed read of the reference
install resolved the stock unit records through the normal VFS and preserved
these source values:

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
other reader for that field. `ovradjust` remains a retained unknown source
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

Build menus are assembled from catalog data, not side data. The per-builder
lists live in **`gamedata\sidedata.tdf`** as a top-level `[CANBUILD]` section
whose child sections are named by unit name (`CANBUILD %s` is the *allocation
tag*, not a section name), and the numbered `canbuild<n>` keys are read from 1
upward **until the first absent key**, so a gap ends the list
(`[R-CAT-01 §5]`). The executable's key vocabulary also names `MENU`, `UNITMENU`,
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
| Model (`objectname`) | **fatal**: the model loader's null result is passed to the fatal channel with the path `objects3d\<objectname>.3DO` as the whole message (`[R-MALF-01 §5]`; the same holds for a weapon `model` and a feature `object`). The compile path never tolerates a missing model. | yes | direct |
| Side (`side`) | the build-pick filter compares the authored string; a mismatch rejects the pick — an empty side mismatches every acting side | no, but affects AI builds | direct |
| Sound category (`soundcategory`) | **absent key → category index 0** (the first section of `sound.tdf`); **present but matching no category name → the decimal conversion of the authored text** (0 for non-numeric text, so again the first category; an authored number selects that ordinal directly, unbounded). There is no placeholder record: the index is 0 or the parsed number (`[R-CAT-01 §5]`). | no | direct |

The feature-record equivalent is different: a feature name found in no parsed
feature node raises the fatal diagnostic `Record "%s" missing from feature
files` (§5 above).

### Unit catalog discovery: enumeration, the sentinel record, the per-file reads, and the three drop gates [R-CAT-01 §4]

This is the first of the two stages §5 opens with, as the executable runs
it. The second stage is `[R-CAT-01 §5]`. Everything is **Established** by
direct trace unless marked.

**Weapon trees.** The loader first enumerates `weapons\*.tdf` through the
union enumerator (`[R-CAT-01 §1]`) and parses every file into its **own**
tree. A file is kept only when the parse produced a tree **and** the file was
read from an archive (see the gates below); otherwise its slot is skipped.
These trees exist only for the checksum step below and are released at the
end of this stage — they are not the weapon catalog of R-CONTENT-02.

**Enumeration and the record table.** `units\*.FBI` is enumerated the same
way. The catalog count becomes *files + 1*, and a table of that many
585-byte definition records is allocated and zero-filled. **Record 0 is a
sentinel**: its unit name is `None`, its "compatible" flag (bit 23 of the
first definition-flags word) is set, and it is never parsed. The catalog
therefore always has at least one record, and unit index 0 means "no unit"
everywhere else in the executable.

**Per file, in enumeration order** (record *i* for file *i − 1*; the
record's index word is provisionally *i*):

1. Open the file through the VFS; a file that fails to open leaves its record
   zeroed — flag bit 23 clear — and the loop continues.
2. Read the whole file and compute the content checksum of §6 over its bytes
   into the record's **file checksum** word.
3. Open `units\<file name>.OVR` as a bank filtered on the tag
   `TA Unit Override`; if it opens and holds an account `Compatability`, the
   integer item named by the decimal spelling of that checksum replaces the
   word (§6 "Content checksum").
4. Parse the bytes with the generic parser (the diagnostic file label is
   `<NO FILE>`) and select `[UNITINFO]`. **A file without a `UNITINFO`
   section aborts the whole stage**: the loader returns failure at once, and
   because its caller ignores that result, every file after it in
   enumeration order stays an unparsed (bit-23-clear) record that the
   compiler of `[R-CAT-01 §5]` silently compacts out. No box is raised.
5. Read, in this order: `name` (language-prefixed, 32 bytes) into the
   record head; `unitname` (32); `side` (30); `ai_weight` (64); `ai_limit`
   (64); `objectname` (32) — when absent, `unitname` is copied into it;
   `buildcostenergy` and `buildcostmetal` (integer, stored as floating);
   `norestrict` → bit 15 and `wacky` → bit 16 of the second flags word.
6. **Weapon checksum fold.** For each of `weapon1`, `weapon2`, `weapon3`,
   `explodeas`, `selfdestructas` read with the raw accessor: when the key is
   present and non-empty, the weapon trees are scanned in enumeration order
   for the first tree holding a top-level section of that name (first-match
   child lookup from the root, case-insensitive), and that section's stored
   section checksum (the word the parser stamps on every section, `[R-MAP-01
   §3]`) is XORed into the record's **weapon checksum** word. A name found in
   no tree contributes 0. The two checksum words (file, weapon) are separate
   fields; which lobby/network identity consumes which is document 08's.
7. `Version` and `Copyright` gates exactly as `[R-MALF-01 §5]`.
8. **The loose-file gate (Established).** The record is also dropped, and
   the "incompatible units" box suppressed, when the FBI was **not** read
   from an archive and the executable is an installed (hard-disk) build — a
   constant that is 1 in this image — or when the CD-content-drive flag is
   set (unreachable in this build, since the installed constant short-circuits
   the CD path). The installed constant has no writer anywhere in the image —
   it is loaded from four sites and no code or data reference stores to it —
   so it is 1 for the whole run. **Consequence: loose `units\*.FBI` files are
   enumerated, parsed, and then always dropped silently; loose `weapons\*.tdf`
   files are opened and read but never parsed — neither into the checksum
   trees here nor into weapon records by the weapon-record compiler
   (`[R-CONTENT-02]`).** Retail unit content must come from
   a mounted archive. The community rule that units "must be packed" is thus
   the executable's rule, not a packaging convention.
9. One four-byte field of the record is set to −1 (its reader is not traced
   here), and the file, bank and tree are released.

**After the loop.** The weapon trees are released. Then records are
compacted from the end: for *i* from *count − 1* down to 1, a record whose
bit 23 is clear is removed by **moving the current last kept record into its
slot** (its index word rewritten to *i*) and decrementing the count — order
is not preserved here, which is why `[R-CAT-01 §5]` sorts. If any record was
dropped and the suppress flag is clear, the translated `Incompatible units
found.  They will be ignored.  Please download the latest version of the
game.` is shown through the non-fatal `Error` box.

### The catalog compiler: order of work, name sort and unit indices, build-menu pages, scripts, and the side `CANBUILD` lists [R-CAT-01 §5]

The compiler runs once at startup after `[R-CAT-01 §4]` and again at every
battle entry (§8). **Established** throughout unless marked.

**Order of work.**

1. `gamedata\moveinfo.tdf` (fatal `Can't load MOVEINFO.TDF` when absent),
   then `CLASS0`..`CLASS31` as "Movement class record" states; the class
   name is the section's `name` key (100-byte buffer) interned into the
   record head.
2. A composition memory cache is created and sized from the display size
   and the map; its arithmetic is document 03's (`[03 R-REN-03A]`).
3. **Compaction** of the record table: a stable remove of every record from
   index 1 upward whose bit 23 is clear (this is the pass that removes the
   records `[R-CAT-01 §4]` left unparsed); the count is rewritten.
4. **Sort.** Records 1 .. count−1 are sorted by `unitname` with the
   case-insensitive comparison; record 0 (`None`) stays first. The sort is
   the C++ library's unstable sort (insertion sort below 17 elements,
   median-of-three partitioning above it). For a range larger than 16, take
   the median key from its first, middle (`floor(length/2)`) and last records.
   Scan inward using strict-less comparisons, swapping even equal keys
   until the scans cross; recurse on the smaller partition and continue
   with the larger. Finish with insertion that shifts only strictly greater
   predecessors. Thus two records with the same
   `unitname` land in an order that depends on the algorithm's partition
   choices, not on file order — stock content has no such pair. Every record
   then receives its **unit index** = its position in this order (0 for the
   sentinel). The unit index is the value the save file, the network
   messages and every "unit type" field carry, and the by-name lookup used
   by the side `CANBUILD` lists and by the mission/save loaders is a
   **binary search over this sorted table**, so the order is a contract, not
   an implementation detail. A word holding the bit length of the count
   (the number of halvings until zero) is stored beside it.
5. A model-pointer table of *count* entries is allocated. Then for each
   record *i* ≥ 1, in index order:
   * the load-progress byte the front end displays is set to
     `i × 100 / count` (integer division);
   * if `units\<unitname>.FBI` has a non-zero size the unit-record compiler
     of §5 ("Unit record") re-parses it into the record;
   * `objects3d\<objectname>.3DO` is loaded whole, relocated, **mirrored**,
     and texture-bound (`[R-CAT-01 §7]`; a null load is fatal with the path);
     `objectname` is taken through a 32-byte copy, so at most 31 characters
     reach the path;
   * the height word of `[R-CAT-01 §7]` is computed;
   * **build-menu pages**: with `<n>` = `unitname` with any extension
     stripped, `guis\<n>0.GUI` existing (non-zero size) sets bit 31 of the
     first flags word; then `guis\<n>1.GUI`, `guis\<n>2.GUI`, … are probed
     until the first missing one. The record's page-count byte becomes the
     index of that first missing page when at least one numbered page
     existed (so page 0 is counted whether or not it exists), else 1 when
     page 0 exists, else 0;
   * `scripts\<unitname>.COB` is loaded through the script loader
     (`[04 R-COB-01 §1]`; null on absence).
6. `gamedata\sidedata.tdf` is loaded (fatal `Can't load GAMEDATA.TDF` —
   the misnamed box of `[R-MALF-01 §5]`). For every record *i* ≥ 1 the
   build-list count and pointer are zeroed; then, for records with the
   `builder` bit (bit 6 of the first flags word) only: the top-level section
   `CANBUILD` is selected, then its child section named by `unitname`; when
   both exist, `canbuild1`, `canbuild2`, … are read as 32-byte strings until
   the first absent key and each name is resolved through the by-name binary
   search — a name that is no unit yields index 0 and is **skipped**, not
   stored. The resolved 16-bit indices are collected in a **60-byte scratch
   buffer that is never bounded**: a builder authoring more than 30
   resolvable entries writes past it (*accept-with-garbage*). Every builder
   then receives its own 60-byte copy of the scratch buffer (allocation tag
   `CANBUILD <unitname>`) and the count of entries stored; entries beyond
   the count are stale bytes from earlier builders (the scratch is never
   cleared) and are not consumed. A builder with no `CANBUILD` child keeps
   count 0 but still receives the 60-byte copy.
7. The progress byte is set to 100 and the catalog-ready flag to 1.

**`soundcategory` resolution.** The unit-record compiler reads `soundcategory` (100 bytes). Absent
→ index 0. Present → linear scan of the loaded category records (352-byte
stride) with the case-insensitive comparison; the first match's ordinal is
stored; **no match → the C-runtime decimal conversion of the authored text**
is stored as the index, unbounded — `soundcategory=7;` selects the eighth
category, and any non-numeric unknown name selects category 0. There is no
muted placeholder.

**Established — empty and duplicate names.** An absent or empty `unitname`
remains empty through compaction, sorting and secondary-file selection. If
`units/.FBI` exists and has nonzero size, the ordinary second parser can reread
its fields; otherwise that identity remains empty. The compiler builds resource paths from the stored name, never the discovered
filename: empty names probe `units/.FBI`, `scripts/.COB` and GUI pages beginning
with `guis/0.GUI`. Only absent `objectname` copies `unitname`; an explicit
model name still selects that model, while an empty one probes
`objects3d/.3DO` and follows the ordinary fatal missing-model path.

Compatible duplicate names remain separate sorted records, including empty
names. Name lookup returns the first equal record in the sorted non-sentinel
range; equality does not establish a stable order among the duplicates.
Nanolathe retains the full record table and projects the runtime lookup
(described below) into its name index. Index-based construction, resource
linking and cloning preserve later equal records `[fmt fbi]`.

**Established — secondary parse write set and failures.** Discovery fills
only the fields listed in [R-CAT-01 §4], leaving gameplay fields at their
allocation zeros. After compaction and sorting, each retained record receives
its index and selects one FBI resource using its stored `unitname`: assemble
`units/<name>`, strip the suffix beginning with the last period, if any, and
append `.FBI`. The suffix scan crosses directory separators: a stored name
`folder.old/unit` selects `units/folder.FBI`, while `unit.old` selects
`units/unit.FBI`. Empty names select `units/.FBI`. This never falls back to
the discovery filename and never follows a renamed identity with another
FBI read during that pass. The same helper selects model and script files:
use `objects3d` plus the newly stored `objectname` (at most 31 bytes), or
`scripts` plus the newly stored `unitname`, strip the last-period suffix from
the assembled path, then append `.3DO` or `.COB`, respectively.

A successful secondary `UNITINFO` parse writes the ordinary "Unit record"
fields, with this precise division:

| Fields | Secondary action |
|---|---|
| `unitname`, language-selected `name`, `objectname`, `buildcostenergy`, `buildcostmetal`, `norestrict` | Overwrite discovery values with second-source reads and normal absent-key defaults. Missing `unitname` or `name` clears it; absent `objectname` copies the newly read `unitname`, while explicit empty remains empty. The language trial uses empty defaults; a present empty localized name suppresses the base-name fallback. |
| `side`, `ai_weight`, `ai_limit`, `wacky` | Retain the discovery values. The second parser does not read these keys. In particular, retaining `wacky` does not imply retaining `norestrict`. |
| Description; mission and category strings; movement, geometry, economy, construction, combat, sensor and other capability/posture fields; weapon, corpse, sound and movement links | Read from the secondary section with the existing "Unit record" conversions and defaults. Missing keys do not inherit discovery-file text. Chained defaults use values just read from this secondary section. |
| Admission state, file/weapon checksums and assigned unit index | Preserve discovery/catalog state; no version, copyright or archive-provider admission gate is repeated. |

A missing or zero-sized resource skips the second parser. Failure to open or
a failed read also returns without changing the record. A nonempty parsed
file without `UNITINFO` likewise performs no writes. Thus these cases retain
**discovery-only state**, rather than a full unit compiled with absent-key
defaults: for example standing orders, bank scale, damage modifier, health
and footprint remain zero. Category membership (including `ALL`) and the
weapon links are also written only on a successful secondary parse; an
unparsed retained record still owns its assigned index but has no such
links. A malformed TDF syntax still follows the generic
fatal parser path. **Unknown:** bytes seen after a nonnegative short read
that leaves part of the allocated input buffer unwritten; that run-specific
storage cannot be reconstructed from the file bytes. Nanolathe parses only
bytes actually returned by its bounded reader and does not synthesize the
unwritten tail.

**Established — name lookup after secondary renaming.** The secondary
parser can change `unitname`, but the compiler neither sorts again nor
changes the assigned indices. The runtime name lookup still executes its
lower-bound search over this final order: start at the first non-sentinel
record with a count covering the whole non-sentinel range; while count is
positive take `half = floor(count / 2)` and inspect `start + half`. When
that stored name compares strictly less than the query, move start just past
it and subtract `half + 1` from count; otherwise replace count with half.
At termination return the record's index only if start has not reached the
end and its stored name equals the query; otherwise return sentinel zero.
Comparisons are case-insensitive. With unchanged sorted names this selects
the first equal record. With final order `z, b, c`, it cannot find either
`z` or `b`, although those records remain accessible by index. Nanolathe's
name index preserves those misses and never silently repairs the ordering.

**`YardMap` compilation (Established; complements `[05 R-ECO-01]`).** For a
unit whose `bmcode` is 0 (a structure), a `FootprintX × FootprintZ` byte map
is allocated (tag `BUILDING YARD`, **not cleared**) and filled row-major
from the `YardMap` text with this character table: `.` → 0, `C` → 0x35,
`G` → 0x8F, `O` → 0x2B, `Y` → 0x31, `c` → 0x2D, `f` → 0x6F, `o` → 0x2F,
`w` → 0x37, `y` → 0x29. Any other character (space, tab, newline, or an
unlisted letter) is **skipped without consuming a cell**, and the cursor
advances past it unconditionally — even onto and past the terminator. After
a listed character the cursor advances only while the next byte is not the
terminator, so a `YardMap` that ends in a listed character and is shorter
than the footprint **replays its last character** into every remaining
cell; a `YardMap` that ends in an unlisted character (a trailing space, say)
runs the cursor past the terminator and fills the remaining cells from the
bytes that follow it in the 1,024-byte read buffer (*accept-with-garbage*);
an empty `YardMap` is the terminator case at once. For a mobile unit
(`bmcode` ≠ 0) the map pointer is null. The meaning of the cell values is
document 05's.

**Three derived fields the key table does not show.** The record's
`maxvelocity ÷ (MaxSlope + 1)` quotient — computed in 64 bits as
`(maxvelocity << 16) / ((MaxSlope + 1) << 16)`, truncating toward zero, so
the result is the 16.16 velocity divided by the class's `MaxSlope` byte plus
one and is still 16.16 — is stored beside the velocity. **Its only reader is
the unit-information panel's text formatter** (`[07 §6]`), which prints it
next to `maxvelocity`: a displacement census over the image finds the store,
the two halves of the record-copy helper the catalog compaction uses, and that
one reader. No simulation path reads it, so a reimplementation that never
computes it loses nothing but the panel line (Established).

Second, when the cloak-capable bit (`cloakcost > 0`, a **strict**
single-precision compare) is set and
`mincloakdistance` compiled to 0, the compiler stores **80** in its place.
This runs for every definition the compiler parses, at startup and again at
every battle entry, so a cloak-capable definition that omits the key — or
authors it as 0 — carries a compiled breach radius of 80, never 0.

Third, a **word-A flag bit (bit 16)** is set when any of `weapon1`, `weapon2`
or `weapon3` resolved to a weapon record other than the inactive record-0
sentinel, and cleared when all three resolved to it; `explodeas` and
`selfdestructas` do not participate. It is the definition's "carries a real
weapon" bit, and a whole-image census over every mask and every shift applied
to word A closes its reader set at **two** (Established).

The first reader is the initializer that binds a definition to a new unit: it
keeps word A's upper half, shifts it left by fifteen — so bit 16 is the only
bit that survives — and installs the result as the **top bit (bit 31) of the
unit's status word**, clearing that bit first. That is the "armed" / "has an
aimable weapon" status bit of `[04 R-ORD-01 §3]` and `[04 R-SPEC-01 §1]`, so
every consumer of the status bit is an indirect consumer of this definition
bit; the two are the same fact one step apart. The second reader tests it
together with the `kamikaze` bit (word A bit 28) as a single mask — a "this
definition can do damage" gate — in front of the order-time weapon-retarget
body, before a single-precision comparison; docs 04 and 06 own that gate's
behaviour.

`selfdestructcountdown`: absent → 5 in the 3-bit field (bits 20–22 of the
second flags word); present → its decimal value masked to 3 bits.

### The download-menu compile and the per-builder list extension [R-CAT-01 §8]

Runs at battle entry after the catalog compiler (`[08 R-ENTRY-01 §3]`).
**Established** throughout.

1. `download\*.TDF` is enumerated through the union enumerator; one
   189-byte menu record per file is allocated (tag `DOWNLOADMENU`, **not
   cleared**). Each file is parsed and its top-level sections visited by
   index; the record's count word is rewritten to the running section count
   after each section, and each section fills one 37-byte item: `UNITMENU`
   (32-byte string) is resolved to a unit index by a **linear**
   case-insensitive scan of the catalog from index 0 (so `None` can match),
   then `MENU` (integer → byte), `BUTTON` (integer → byte) and `UNITNAME`
   (32-byte string) are stored. A section whose `UNITMENU` is absent or
   names no unit leaves its item's bytes as the allocator returned them
   (*accept-with-garbage*: a stale unit index there can match a real unit
   in the passes below).
2. For every definition, its build-menu **page-count byte** (`[R-CAT-01
   §5]` step 5) is raised to the largest `MENU` byte of every item, in every
   menu record, whose resolved `UNITMENU` index equals the definition; it
   is never lowered.
3. Downloadable enforcement: for every definition, for every menu record,
   **only the first item's** `UNITNAME` is compared (case-insensitively)
   with the definition's `unitname`; on a match with the definition's
   `downloadable` bit clear, the text `Hey!  Somebody forgot to set
   downloadable=1 for %s` is formatted into a stack buffer — **and not
   displayed by this site** — and the bit is set. No sort or re-finalize
   happens here; §5's "re-sorts and re-finalizes" belongs, if anywhere, to
   the other push site (tail item).
4. For every definition that holds a `CANBUILD` list pointer (every
   builder, `[R-CAT-01 §5]` step 6), every item in every menu record whose
   `UNITMENU` index equals the definition has its `UNITNAME` resolved by the
   by-name binary search and, when it resolves and the list count is
   **at most 30**, appended and the count incremented. The list block is
   60 bytes (30 entries), so the 31st append writes two bytes past the block
   (*accept-with-garbage*).

### Weapon record

Weapon files are TDF; each top-level section is one weapon.

**Record identity.** The parser reads `ID` with the integer accessor and a
default of -1 **first**, and uses it to select the weapon record it fills. The
section name is then stored into that record as the weapon's catalog name, and
`name` is read separately as a 64-byte display string. A weapon section without
an authored `ID` therefore selects the slot before the table.

#### Weapon-family discovery and same-ID merge [R-CONTENT-02]

**The family is `Weapons\*.tdf` alone — Established.** The executable never
parses `gamedata\weapons.tdf`. The only weapon-family pattern in the
executable's string vocabulary is `Weapons\*.tdf`, pushed at exactly two
sites (the weapon-record compiler and the unit-catalog loader below), and the
bounded census of every `gamedata\` path-building site accounts for version,
sidedata (twice), moveinfo, sound, allsound, los, help, meteor, category, and
the full-path `gamedata\translate.tdf` literal — none name a weapons file.
The stock "ID 36 collision" (`[earthquake]` in `gamedata\weapons.tdf` against
`[cormine2]` in `weapons/cormine2_weapon.tdf`) is between a file retail never
reads and the parsed family; retail never sees it.

**Discovery order — Established.** The weapon family is exactly the union
enumeration of `Weapons\*.tdf`: provider mount precedence first, then the
host's unsorted directory order within one provider's directory (§2, SC3),
with duplicate logical paths resolved first-provider-wins. Two independent
passes walk it — their relative call order does not affect either result:

* The **weapon-record compiler** opens every file in the family, but feeds
  its **top-level sections, in file order**, to the record parser only when
  that file was opened **from a mounted archive**. It applies the same
  archive/installed-build gate `[R-CAT-01 §4]` step 8 applies to `units\*.FBI`
  (and the unit-catalog loader applies to its own weapon trees), so in an
  installed build a file resolved from a loose host directory is opened, read
  and then discarded with none of its sections reaching the record parser: a
  loose weapon TDF can neither add nor overwrite a weapon record. Because the
  overlay resolves a loose host file **before** any archive (§2), a loose
  `weapons\foo.tdf` that shadows an archived one also suppresses the archived
  copy — the loose file wins the open and is then skipped.
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
parser. One field escapes the replace-whole rule (`[06 R-DMG-01 §1]`): the
catalog initializer clears each record's name byte and stamps its slot
number but does **not** clear the `[DAMAGE]` override-table pointer, so a
later section with the same `ID` **appends** its per-name damage entries
into the earlier record's table (same-spelling keys overwrite in place,
case-variant keys insert) instead of starting a fresh table; `default` and
every scalar field still replace whole. Stock has no same-ID
pair, so this is a third-party-content edge only. Record 0 is special: consumers treat a weapon reference as inactive
when it points at record 0 (recognized by its zero slot-number byte), and the
stock corpus fills it with `[noweapon]` (`ID=0` in `weapons/weapons.tdf`).

**Runtime name resolution — Established.** A weapon name resolves by a linear
scan of the record table from slot 0 upward, comparing case-insensitively
against each record's catalog name; the **first** matching slot wins. A name
that matches no record returns not-found. When the FBI compiler resolves
`weapon1..3`, `explodeas`, or `selfdestructas`, a miss is replaced by a
reference to **record 0** — the inactive sentinel — not by an error. The
sentinel is record 0, identified by its zero slot-number byte.

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
| `minbarrelangle` | floating | -11.25 | multiplied by the image's own degrees-to-radians constant, then stored **single precision** — radians |

The degrees-to-radians constant is a stored double, and it is **not** the
correctly rounded value: it is `0.017453292519943278`, **five** units in the
last place below the correctly rounded `pi/180`
(`0.017453292519943295`) — a relative error of about `1e-15`. The
product is then narrowed by a single-precision store, so a clone that
multiplies by its own `math.Pi/180` in double precision and keeps the double
carries a different bound. The field is a launch-angle admission bound
(`[06 §3.3]`), so the difference only shows on a tie inside that band.

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

**Behavior flags.** Integer accessor, default 0, followed by a single-bit
store (`value & 1`), including `lineofsight` [R-KEYS-01 §5]:
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
all a caller census can establish. Authoring any of the six in a weapon section has no effect of any kind, and they occupy
no record byte, so they cannot even be preserved as inert data — unlike, for
example, `accuracy` and `tolerance`, which are parsed into the record and do
have consumers `[06 §3.3]`.

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
| `metal`, `energy` | integer | 0 | reclaim yield; the conversion result is **masked to its low sixteen bits** before the single-precision store — `float(uint16(value))`, as `[05 R-FEAT-01 §1]` states |
| `damage` | integer | 0 | hit points |
| `spreadchance`, `reproduce`, `reproducearea` | integer | 0 | fire spread and regrowth |
| `sparktime` | floating | 0.0 | multiplied by 30, truncated — ticks |
| `burnweapon` | string | empty | weapon emitted while burning |
| `animating`, `animtrans`, `shadtrans` | integer | 0 | animation mode flags |
| `flamable` | integer | 0 | spelled with one `m` |
| `geothermal`, `blocking`, `reclaimable` | integer | 0 | `geothermal` has no registry: the requirement is enforced by the building footprint validator, which requires at least one covered terrain cell to hold a feature whose catalog entry carries this flag |
| `autoreclaimable` | integer | **1** | the only feature flag that defaults on |
| `indestructible`, `nodisplayinfo`, `nodrawundergray` | integer | 0 | `nodrawundergray` is **also forced on** by section name (below) |

**The sixteen-bit yield mask (Established).** `metal` and `energy` are the
only feature keys whose stored value is not the accessor's result: the parser
narrows the 32-bit conversion to its low sixteen bits, zero-extends it and
converts *that* to the record's single-precision field. An authored value
above 65,535 therefore **wraps**, and an authored negative value becomes a
large positive yield. The stock content reaches it: four sections in the
reference install author a `metal` above 65,535 — the Arm and Core Gate wreck
and heap features — so reclaiming an Arm Gate wreck pays the wrapped
remainder, not the authored figure. No stock `energy` or `damage` value is
outside its field's range. `[05 R-WORK-01 §5]` owns what the payout then does
with it.

**Four section names force `nodrawundergray` (Established).** After the flag
is stored the parser compares the section name case-insensitively against
`DragonsTeeth`, `DragonsTeeth_Core`, `Fortification` and `Fortification_Core`
and ORs the bit **on** for any match, whatever the section authored.
`[05 R-FEAT-01 §1]` owns the rule and its effect.

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
terminating the loop (stock content authors `CLASS0..CLASS14` contiguously,
which is why an "until a gap" reading coincides there). Each parsed class
reads eight keys **in this parse order**, and defaults chain off values read earlier in the same record, so
order is contract:

1. `FootPrintX` — integer, default 0, stored as 16-bit.
2. `FootPrintZ` — integer, default 0, stored as 16-bit.
3. `MaxWaterDepth` — integer, default the record's own prior value (preserved; the startup template value on the first parse).
4. `MinWaterDepth` — integer, default the record's own prior value (preserved).
5. `MaxSlope` — integer, default the record's own prior value (preserved), stored as a byte.
6. `BadSlope` — integer, default **half (`>>1`) of the `MaxSlope` value just read**.
7. `MaxWaterSlope` — integer, default the record's own prior value (preserved), byte.
8. `BadWaterSlope` — integer, default **half (`>>1`) of the `MaxWaterSlope` value just read**.

Three clamps then run **unconditionally on every class**, in order:

* if movement class MaxWaterSlope is below movement class MaxSlope, MaxSlope becomes MaxWaterSlope;
* if the resulting MaxSlope is below BadSlope, BadSlope becomes MaxSlope;
* if MaxWaterSlope is below BadWaterSlope, BadWaterSlope becomes MaxWaterSlope.

**Established fact:** The comparisons are unsigned byte comparisons with no
authored gate; the three checks execute for every class regardless of which
keys were authored. No clamp is conditional on key presence, and none can be:
the parser reads every field through the integer accessor, which cannot
distinguish an absent key from an authored zero (§4), so a key-presence gate
is not expressible with the accessor retail uses here.

#### Movement-profile template initialization [R-CONTENT-01]

**Established.** A startup initializer registered in the C-runtime
function-pointer table pre-fills all 32 movement-class records with
`MaxSlope` = `BadSlope` = `MaxWaterSlope` = `BadWaterSlope` = 255,
`MaxWaterDepth` = 10000 and `MinWaterDepth` = −10000 **before the first class
parses**. A class that omits a key therefore keeps the template value, the
three unconditional clamps above are identity for it, and every stock class
compiles to its authored slope limits.

**Established — no reset between classes.** Each `CLASS%d` index owns one
record slot, and each slot is parsed at most once per compile. The
"prior value" defaults read that record's **own** prior bytes — the template
on the first parse, and the previous parse's values on the battle-entry
rebuild (§8), which for stock content is bit-identical.

**Established — record fields.** Each 32-byte record holds the interned
authored `name` value in its head (a missing `name` key yields the empty
string, still interned) followed by the eight parsed fields; the remaining
bytes are never written by the parser.

The full contract — template pre-fill, no reset, unconditional clamps, the
per-cell layer classifier, and A\* blocking only on layer 0 — is
`[04 §6.1 R-DOC04-A]` and `[04 R-DOC04-B]`.

#### Per-field conversion

**Established.** Every field goes through the integer accessor: optional
sign, decimal digits, trailing junk ignored, 32-bit result
— and then stores with truncation to the field width. There is no scaling and
no range clamp on the authored value itself.

| Key | Authored domain | Stored width | Arithmetic |
|---|---|---|---|
| `FootPrintX`, `FootPrintZ` | any integer | 16-bit, used signed | store low 16 bits |
| `maxwaterdepth`, `minwaterdepth` | any integer | 16-bit signed | store low 16 bits |
| `maxslope`, `badslope`, `maxwaterslope`, `badwaterslope` | any integer | 8-bit | store low 8 bits; all consumers compare unsigned |
| `badslope` default | — | 8-bit | `(maxslope just read & 0xFF) >> 1` — logical shift, 0..127 |
| `badwaterslope` default | — | 8-bit | `(maxwaterslope just read & 0xFF) >> 1` |

**Record identity and FBI resolution — Established.** Each parsed record's
head is the interned value of the section's authored `name` key (string accessor, 100
bytes, default empty) — not the `CLASS%d` section name. The FBI compiler
resolves its `movementclass` string by a linear scan of the 32 records with
the case-insensitive comparison against that interned name value. The scan visits
numeric class slots in ascending order and returns immediately on the first
equal name; a later class with the same authored name cannot replace that match.
Records with a null name slot are skipped; a miss yields the null profile. A
`MovementClass=TANKSH2` reference therefore resolves against the authored
`Name` value, not against the `CLASS%d` section name, exactly as `[fmt tdf]`
states.

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

**Established:** Slope is derived from a 2×2 height neighbourhood. The plot
expansion computes per-cell derived `MinHeight` and `MaxHeight` as the minimum
and maximum of up to four height bytes (cell, east, south, southeast, with edge
guards) — these derived values are the slope inputs, not a single height
sample. The height byte itself reaches the plot verbatim: there is no scaling
or shift between the TNT record and the derived pair. Height queries use
bilinear interpolation of the four corner heights with low-four-bit fractions
and signed-bias correction. The structure placement validator
(`[04 R-P0-08]`) and the spawner height probe (`[08 R-ENTRY-02 §1]`) aggregate
`min of mins` and `max of maxes` across the footprint rectangle; the movement
classifiers — the per-class layer stamp, the rectangle restamp and both commit
validators — instead evaluate **each cell on its own derived pair** (slope =
that cell's `MaxHeight − MinHeight`, 8-bit) and combine a footprint by taking
the **minimum tier** over its cells, so a 2×2 class is never judged on the 3×3
corner window (`[04 R-SLOPE-01]`). Passability comparisons are strict `<` for
the hard blocks (`slope == limit` passes) and `≤` for the clear/steep boundary.
Land-vs-water slope selection happens per cell in the movement classifier
(`hmin` below sea level switches to the water pair) and in the mobile-movement
wrapper; the structure validator's slope gate always uses the land pair.

### Sound aliases

The alias catalog is a TDF in the game-data directory. Each top-level section
is one alias; its `sound` key names the sample. Alias registration
deduplicates path/alias entries and is capped at **255** registrations, each
holding a 32-byte alias name. Decoded samples are cached for subsequent
playback; the cache and mixer are separate from the byte-level sample decoder.
Alias-cache eviction is bounded-negative (no eviction site found in the
census) `TODO(question)`; sample precedence follows the VFS mount order
established in §2.

### The alias catalog file and its per-section read [R-CAT-01 §6]

**Established fact.** The alias catalog is `gamedata\allsound.tdf`, built
through the ordinary path builder and parsed with the generic TDF parser. The
loader first zeroes the alias count, so a re-run starts from an empty table.
A missing or unreadable file yields no tree, registers nothing and raises no
diagnostic. On success every top-level section is visited **by index in file
order**; for each section the alias name is the section name copied with a
32-byte bounded copy (no terminator is forced, so a name of 32 or more
characters is not terminated inside the buffer — stock names are far
shorter), and the sample path is the
section's `sound` key read with the bounded string accessor into a 256-byte
buffer. A section without a `sound` key registers nothing; a section whose
`sound` is present but empty registers an alias with an empty path. Each
surviving pair is handed to the alias registrar (the dedup and the 255-entry
cap above). After the last section the sound-category loader
(`[R-SND-01 §1]`) runs, and only then is the alias tree released.

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

**Variant gathering.** For each of the 23 event keys `K`, the loader reads
`K` first, then `K1`, `K2`, `K3`, and so on, stopping at the first numbered
index whose key is absent. Every successful read appends to two parallel
growable arrays of 64-byte strings held by that event: the sound alias, and
a caption read from the companion key `<that key>text` (empty when absent).
A category's event therefore holds an ordered list of variants and their
captions, plus the count. The bare read and the numbered loop are
**independent**: a missing bare key contributes nothing and the numbered
loop still runs from index 1 [R-SND-01 §1].

**What the reference install actually authors [I14].** Counted over the 120
categories of the reference `sound.tdf`, so that a future silence bug is not
mistaken for a compilation or VFS failure. Every category authors exactly one
variant for `select`, `underattack` and each of the seven countdown slots
(17–23); 76 author `ok` and `cant`, 63 `arrived`, 36 `working`, 30 `build`,
22 `repair`, 21 each `activate` and `deactivate`, 12 `unitcomplete`, and two
each `cloak`, `uncloak` and `capture`. **No category authors `load` or
`unload` at all**, so slots 12 and 13 are silent in a stock install however
they are driven, and an empty row is a common case rather than an edge one.
That is 219 distinct alias names across the whole table; 206 of them resolve
to a sample through the mount order of §2 and 13 do not (among them `build`,
`untdone`, `snipsel1`, `torpsel1`), which is the registration probe's
retained-but-unresolved outcome of `[03 §8.3]` and is silent by design, not a
defect. Separately, 278 unit definitions name a `soundcategory`: 267 resolve
against the table and 11 do not (`none` six times, plus `core_kbot`,
`cor_tank` and `core_mex`). Those eleven are **not** voiceless: none of the
four spellings is numeric, so the failure policy above converts each to
ordinal 0 and the eleven speak the first authored category's lines —
`ARM_KBOT` in the reference install, which authors `select`, `ok`, `arrived`,
`cant`, `underattack` and the countdown slots. An earlier reading of this
corpus note claimed silence and contradicted both the failure-policy row and
the `soundcategory`-resolution paragraph of `[R-CAT-01 §5]`; the failure row
is the direct evidence and is what the loader does. Weapon
sounds are a different family and are healthy: 198 weapon definitions supply
62 distinct `soundstart`/`soundhit`/`soundwater` names and all 62 resolve.

### The sound-category loader: file, record, bare and numbered keys, captions [R-SND-01 §1]

Everything here is the content layer; the reader side (queue, draw, gates)
is `[03 §8.3]` and `[03 R-AUD-01 §3]`.

**Established fact — catalog open.** The loader zeroes the category count
and record pointer, builds the path `gamedata\sound.tdf` through the usual
path builder, and parses it with the generic TDF parser. If the parse fails
(file absent or unreadable) both stay zero: there are **no** categories and
no diagnostic is raised here. On success the count is the number of
top-level sections, one 352-byte record per section is allocated (tagged
`Sound Categories`) and zero-filled, and each section is visited **by
index** in file order — so a category's index is its ordinal position in
`sound.tdf`, and that ordinal is what a unit's `soundcategory` name resolves
to.

**Established fact — the record.** Per section: the section name is copied
into the 64-byte name field, at most **63** characters. The 24 twelve-byte
rows follow (row 0 is never written); row *k* for slot *k* holds
`{count, aliases, captions}` where the two pointers address growable arrays
of 64-byte strings (tag `Say Choice Array`, reallocated to
`(count + 1) × 64` bytes on every append).

**Established fact — the per-key read.** One helper reads one authored key
into a slot row. It looks the key up in the current section with the
bounded string accessor (64-byte buffer, so an alias is at most 63
characters); an **absent** key returns failure and appends nothing. A
present key — including one whose value is empty — succeeds: the alias is
appended, then the caption key is formed as the key name followed by
`text` (`select1` → `select1text`) and read the same way, empty when
absent; both arrays grow by one and the count increments.

**Established fact — the gather order.** For each slot in table order
(`select` … `canceldestruct`):

1. read the bare key `K` — the helper's result is **not tested**;
2. set `n = 1`; read `K<n>` (formatted `%s%i`, decimal, no padding);
3. while that read succeeded, `n = n + 1` and read `K<n>`;
4. stop at the first absent `K<n>`.

Consequences, all direct: a category authoring `select1`, `select2` and no
`select` has two variants; one authoring `select` and `select1` has two
variants with the bare one first; `select1` and `select3` without `select2`
yields one variant; `select1=;` (present, empty) yields one variant whose
alias is the empty string (the resolver's audible path then plays nothing,
`[03 §8.3]` step 3 — "the codec performs no empty-path check").

**Established fact — how the reader indexes the record** (cross-check of
`[03 §8.3]`, no new contract): the voice-cue resolver reaches the record by
the unit definition's stored category index times 352, then row `slot`,
and draws `idx = trunc(rand15 × count ÷ 32768)` over the *whole* variant
list; the record carries no memory of which entries came from the bare key
and which from numbered keys.

**Corpus note.** The stock catalog's `select1`/`ok1`/`cant1`/`arrived1`
with no bare forms (SC7's observation) is therefore exactly what the loader
expects; no compatibility divergence is needed.

### Category token registry and membership-bitset compilation [R-P0-03]

The consumption of these bitsets by weapon and order masks is owned by
document 06 §3.1.

#### Result [R-P0-03 §1]

**Established.** Retail does **not** assign one bit per category token by
hashing the token. A category token is a case-insensitive registry key whose
value is a bitset of unit definition IDs. Compiling a unit's `category` string
sets that unit's ID bit in the bitset for every token named by the string.
Weapon and order masks then refer to those token bitsets and test candidate
unit IDs. Category bits are membership sets indexed by unit ID, never token
ordinals or hash buckets.

#### Registry construction [R-P0-03 §2]

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

#### Category-string compilation [R-P0-03 §3]

The `category` value is scanned as a whitespace-separated sequence (a `%s`-style
token scan that also reports the consumed-character count). Each token is
looked up or created in the registry, then the current unit ID bit is ORed into
that token's bitset. Empty or repeated whitespace has no semantic effect.

After the token loop, the compiler also looks up the registry entry named
by the literal token `ALL` and sets the current unit ID bit in that bitset.
`ALL` membership is mandatory and unconditional: every compiled unit is a
member of the `ALL` category regardless of its authored tokens, and an
authored `ALL` token is a no-op duplicate. The entry is the ordinary token
`ALL`, consumed through the ordinary registry lookup — a mask built from the
name `ALL` (for example an authored `noChaseCategory=ALL`) matches every
unit. The `ALL` literal also appears in
the campaign-side selector (a `campaignside` value of `ALL` matches any
side), which is a separate consumer.

Unit category fields and weapon masks use the same registry. The bad-target
fields (`wpri_badTargetCategory`, `wsec_badTargetCategory`, and
`wspe_badTargetCategory`) and `noChaseCategory` default to the authored token
`none` when absent. `none` is not a reserved compiler keyword: it is looked up
like any other token, so its mask is empty unless an authored unit is actually
compiled into that token [06 §3.1].

#### Unknown and duplicate handling [R-P0-03 §4]

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

#### Related mask lookup [R-P0-03 §5]

**Established — separate lookup consumers.** The general mask helper first
resolves a name against the unit-name index. If the name is a unit name, it
sets that one unit ID bit; otherwise it ORs the whole category bitset into
the output mask. This preserves the distinction between a direct unit target
and a category target [06 §3.1].

The FBI parser does **not** use that general helper for
`wpri_badTargetCategory`, `wsec_badTargetCategory`, `wspe_badTargetCategory`
or `noChaseCategory`. Each field directly looks up or creates one registry
entry and retains its shared membership bitset. A matching unit name has no
special precedence. For example, a `zulu` unit without category `zulu` does
not belong to an FBI target mask named `zulu`; a different unit authoring
category `zulu` does. An explicitly empty target also selects an ordinary
empty-name registry entry, rather than an empty-name unit definition.

The FBI target pointers are installed during each successful secondary parse,
before that record contributes its category membership. Later records can
populate the same shared bitsets, and every earlier pointer sees those added
bits. Copying the completed registry membership after all secondary parses
therefore preserves the result: unit renaming cannot alter these category-only
lookups. The general helper's direct-unit precedence does not apply to these
four FBI fields.

#### Algorithm and ordering contract [R-P0-03 §6]

```text
build unit catalog
  case-insensitively order unit records
  assign stable unit IDs

for each unit in stable ID order
  for token in whitespace_tokens(unit.category)
    set category_registry[casefold(token)][unit.id] = 1
  set category_registry["ALL"][unit.id] = 1

compile authored bad-target/no-chase category names
  resolve one case-insensitive registry entry per name
  retain its unit-ID bitset pointer/value
```

The category vector is sorted for lookup; token order within one string does
not affect the resulting bitsets. Unit ID ordering is the determinism boundary:
the same case-insensitive catalog order must be used before setting bits.
Registry allocation and membership are integer operations; no FNV, modulo, or
floating-point step participates.

#### Evidence and confidence [R-P0-03 §7]

- Unit category fields, defaults, two-stage catalog loading, and retained
  unknown keys: §5 above.
- Category masks as unit-target membership sets: [06 §3.1].
- Exact registry shape, case-insensitive sorted insertion, zeroed 16-word
  allocation, whitespace tokenization, unit-ID word/mask calculation, and the
  mandatory `ALL` membership: static-analysis notes kept outside the
  repository.

Confidence is high for registry identity, bitset layout, unknown/duplicate
behavior, unit-ID indexing, and the `ALL` token.

#### Implementation guidance [R-P0-03 §8]

Compile a registry entry to a mutable/immutable 512-bit unit-membership mask,
not to a token ordinal. Casefold names using the retail-compatible
case-insensitive comparison, sort registry entries for deterministic lookup,
retain unknown names with zero masks, and OR duplicate memberships. Assign unit
IDs only after the stable case-insensitive unit catalog ordering. Keep `none`
as a normal token default unless content analysis proves an authored special
case. Set every unit's ID bit in the `ALL` entry as the mandatory membership.


### Key consumer table [R-KEYS-01]

**Established** for the enumeration (every row is a typed-accessor
read found in one of the retail parsers — the unit-definition compiler and
the unit-catalog loader, the weapon-record parser, the feature parser, the
movement-class parser, the mission loader, the placed-object compiler, the
trigger builder, the side loader, the sound-category loader and the
preferences loader); the consumer column carries its own evidence level per
row.

**How to read it.** One row per key the executable reads. *Accessor* is the
typed accessor of §4 "Typed accessors" (`integer`, `floating`, `fixed`,
`string`, `lang-string` = language-prefixed string, `raw` = bare value
pointer); *stored width* is the field the parser writes (a `flag bit n` row
stores `(value & 1) << n` into that record's packed word — an authored `2`
stores as 0). The **unit** definition carries **two** 32-bit packed flag
words and thirteen bit numbers are claimed by a key in each, so every unit
flag row names its word — `flag word A, bit n` or `flag word B, bit n`. Word A
is the first definition-flags word (the one whose bit 23 is the compatible bit
of `[R-CAT-01 §4]`), word B the second. Each unit row's word was read from the
store the parser makes for that key, so the column is **Established** for all
of them, `wacky` included (its store is in the catalog loader rather than the
unit-record compiler). The weapon, feature and movement-class records carry a
single packed word each, so their rows stay `flag bit n`.
*Default* is the accessor's default argument, already in
stored units. *Consumer* is the section that states the reader's contract,
or `inert (reader census: none)` when a whole-export reader census found no
load of the stored field, or `unknown:` with the decider. Rows whose
consumer is `[02 R-KEYS-01 §n]` are stated in the numbered notes below;
every other citation points at the owning document.

**Census.** 398 keys enumerated; 372 with a
consumer citation, 11 inert by reader census, 15 Unknown (one side key,
fourteen presentation-only registry values whose readers no document has
traced yet — all named with their decider in the table). Keys with no
string in the image at all (`aimrate`, `movingaccuracy`, `noselfdamage`,
`impulsefactor`, `impulseboost`, `startfire`, `MohoMetal`, `SCHEMACOUNT`,
`size`, `solarstrength`, and the FBI editor keys listed in `[fmt fbi]`) are
not rows: they are not read, so they have no reader to census.

**Maintenance.** The table's first form was emitted by a generator kept beside
the raw corpus; that generator is no longer part of the corpus, so the table is
now maintained in place. Correct a row only against a re-read of the parser
store that writes the field, and keep the raw trail for the change in
`$HOME/ta-decompile/notes/`.

#### Unit-record consumers not stated elsewhere [R-KEYS-01 §1]

* **`defaultmissiontype` — Established.** The 100-byte string is converted
  through the mission-type vocabulary (the same name table the `InitialMission`
  script and the order system use, `[04 §3.6]`) into a one-byte mission code
  stored on the definition; an empty or unrecognised name stores 0. Exactly one
  reader: the primary order-queue pump. When a unit's order queue is empty,
  its owner has controller type 1 or 2, and the definition's code is non-zero,
  the pump allocates a fresh order record carrying that mission code and
  pushes it as the unit's standing task; a zero code leaves the unit idle.
  Cross-doc: doc 04 §3.4 should cite this as the idle-refill source.
* **`antiweapons` — Established (reader census).** Word A bit 29 of the
  definition. Its only reader in the exported corpus is the range-ring
  overlay drawer: for a selected unit whose definition carries the flag, each
  of the three weapon slots whose weapon record carries `interceptor` draws a
  coverage circle. Nothing in the simulation reads it — interception itself
  is gated by the weapon's own `interceptor` flag `[06 §11.2]` — so the key is
  presentation-only.
* **`canreclamate` and capability bit 9 — Established.** The parser stores
  `canreclamate` in capability-word bit 10, and while storing `canresurrect`
  (bit 11) it also writes **bit 9 as a copy of bit 10**. There is no key for
  bit 9; it is derived. Readers of bit 9 versus bit 10 are not separated here
  (the doc 04/05 reclaim gates cite the capability, not the bit) —
  *decider:* a bit-9 reader census, which matters only if a reader tests bit
  9 alone.

#### Weapon-record consumers not stated elsewhere [R-KEYS-01 §2]

* **`toairweapon` — Supported inference for the gate, Established for the
  readers.** Flag bit 17 of the weapon record has two readers: the
  attack-order resolver (after the shared target search accepts a target it
  records whether the chosen slot's weapon is *not* to-air, which selects the
  resolver's return code) and the fire-order handler's weapon-slot pick,
  which skips a slot flagged to-air unless the target's airborne state bits
  read 2. `[06 R-WPN-05 §1]` settles the operand: it is the target's
  *committed mover mode* — the low two bits of its state word,
  `[04 R-MOV-01 §8]` — which must read exactly 2. The same gate is the order
  side's shot-admission test, so the row is Established throughout.
* **`shellweapon` — inert (reader census: none).** Flag bit 2 is stored and
  never loaded, in any of the decompiler's renderings (dword mask,
  shift-and-and, byte-narrowed mask). Bounded by the export, like every
  census in this table.
* **`model` — Established.** The 256-byte name resolves to a model handle at
  parse time (empty → null handle); the handle is read by the model-family
  projectile renderer `[06 R-WFX-01 §4]`.
* **`shakemagnitude`, `shakeduration` — Established.** Stored as a 32-bit
  integer and as `trunc(seconds × 30)` ticks; both feed the screen-shake
  request `[03 §5.6]`.
* **`paralyzer` (bit 7) and `smoketrail` (bit 18)** are consumed by
  `[06 §10]` (plus the shared target search's paralysed-target skip,
  `[06 §3.2]`) and `[06 §7.3]` respectively.

#### Registry values not listed above [R-KEYS-01 §3]

* **`side` — Established read, Supported inference for meaning.** The
  preferences loader reads a DWORD value named `side` under the
  `Total Annihilation` key, installs 0 when absent, and stores it in a
  session global. Its readers are the campaign briefing screens and the
  save-account restore, i.e. it is the last chosen player side (0/1) that
  the campaign-side filter of `[08 R-CAMP-01 §2]` compares against.
* **`Games` and the session option word — Established.** The loader reads
  the registry display-depth value `DisplaymodeDepth`; only when it is
  exactly 256 does it also read `Games`, and when that is 1 it sets bit 1 of
  the 16-bit *session option word*; any other outcome clears bit 1. (The
  skirmish player count is read much earlier in the same loader and plays no
  part in this gate.) That pair is the settings-loader route to developer
  access, `[07 R-CAM-01 §9]`. It then unconditionally sets bits 2 and 3, clears
  bit 4, and copies `clock` into bit 6. Three in-game toggles flip bits 7, 8
  and 9 of the same word. This word is distinct from the two packed display
  and sound option words §3 describes. The `clock` copy retains only the
  stored DWORD's low bit, with a failed read clearing bit 6. The `Clock`
  command flips that bit and immediately invokes the common writer, which
  serializes the resulting on/off value with the rest of the settings
  ([07 R-CAM-01 §6]).

#### The `shootme` option bit: writer census [R-KEYS-01 §4]

`[04 R-SPEC-01 §5]` asked which setting produces the session option bit that
admits any target to a human player's autonomous target search. That bit is
**bit 10 of the session option word** of §3 above, and it is **not** a
setting: **Established**, its only writer in the image is the chat command
`ShootAll`, whose handler toggles the bit and touches nothing else. The
command is registered in the chat-command table with route mask 1, so it is
available in every session kind. The preferences loader writes bits 1, 2, 3,
4 and 6 of the word and the in-game toggles write bits 7, 8 and 9; none of
them touches bit 10, and the word is zero-filled before any initializer runs,
so the bit is clear until a player types the command. Its only reader is the
acquisition admission of `[06 §3.2]`, which owns the contract; `[07 R-CAM-01
§6]` owns the command table.

#### `burstrate`, `duration` and `smokedelay` are unsigned 16-bit tick words [R-KEYS-01 §6]

**Established — the store.** The weapon loader converts each of the three
keys as `trunc(authored × 30)` and stores the low sixteen bits of the result,
exactly as it does for `weapontimer`, `randomdecay` and `flighttime`
(the "floating · 16-bit" rows of the table below carry that width).

**Established — the widening.** Every reader of the three words zero-extends
them: the loads are plain sixteen-bit moves into a register that was cleared
first (or masked to sixteen bits immediately after), never a sign-extending
load, and the consumer arithmetic is unsigned. The readers are all in the
projectile update — the burst scheduler for `burstrate`, the beam latch for
`duration`, the trail-smoke deadline for `smokedelay` — and doc 06 states
each one in [06 R-WPN-05 §12]. There is no other reader of any of the three
words anywhere in the image (bounded negative: a whole-image search for
sixteen-bit accesses at the three record offsets finds only the loader's
stores and the projectile update's loads).

**Consequence for the compiled value.** The value a Nanolathe consumer must
see is `uint16(trunc(authored × 30))` widened to a non-negative integer:
`0..65535` ticks. A negative authored value does not produce a negative
interval — `burstrate=-1` is `65506` ticks — and a value at or above
`65536/30 ≈ 2184.53` seconds wraps (`2184.6` → `2` ticks). No stock weapon
authors any of the three outside `0..32767/30` seconds, so nothing shipped
distinguishes the widening; third-party content can.


#### The table [R-KEYS-01 §5]

**Established — packed stores with different write forms.** Unit
`mobilestandorders` and weapon `lineofsight` replace only bit zero in their
respective flag words. Feature `autoreclaimable` replaces only bit eight,
using the parsed low bit and default one. These are single-bit stores, not
full-width integer truth tests; the width of the containing word does not
change the accepted bit.

<!-- Initial accessor enumeration was generated from private analysis. Stored widths are corrected here after direct writer audits. -->

**unit (FBI `[UNITINFO]`)**

| Key | Accessor · stored width | Default | Consumer | Evidence |
|---|---|---|---|---|
| `unitname` | string · 32 bytes | empty | catalog identity `[02 §5]`; placement by name `[08 R-TRIG-01 §9]` | Established |
| `name` | lang-string · 32 bytes | empty | `[07 §6]` (unit-information panel) | Established |
| `description` | lang-string · 64 bytes | empty | `[07 §6]` | Established |
| `defaultmissiontype` | string · 100 bytes | empty | `[02 R-KEYS-01 §1]` (idle order-queue refill) | Established |
| `wpri_badTargetCategory` | string · 100 bytes | `none` | `[06 §3.1]` | Established |
| `wsec_badTargetCategory` | string · 100 bytes | `none` | `[06 §3.1]` | Established |
| `wspe_badTargetCategory` | string · 100 bytes | `none` | `[06 §3.1]` | Established |
| `noChaseCategory` | string · 100 bytes | `none` | `[06 §3.1]`, `[06 §3.2]` | Established |
| `objectname` | string · 32 bytes | empty | `[03 §2.4]` (model cache resolution) | Established |
| `buildcostenergy` | integer · single float | 0 | `[05 "Construction arithmetic"]`, `[05 R-WORK-01]` | Established |
| `buildcostmetal` | integer · single float | 0 | `[05 "Construction arithmetic"]`, `[05 R-WORK-01]` | Established |
| `maxvelocity` | fixed · 32-bit | 0 (0) | `[04 R-MOV-01 §1]`, `[04 §5.2]` | Established |
| `brakerate` | fixed · 32-bit | 0 (0) | `[04 R-MOV-01 §1]`, `[04 R-MOV-01 §4]` | Established (cited) |
| `acceleration` | fixed · 32-bit | 0 (0) | `[04 R-MOV-01 §1]` | Established (cited) |
| `bankscale` | fixed · 32-bit | 65536 (1) | `[04 R-AIR-01 §2]` | Established (cited) |
| `pitchscale` | fixed · 32-bit | 0 (0) | `[04 R-AIR-01 §2]` | Established (cited) |
| `damagemodifier` | fixed · 32-bit | 65536 (1) | `[06 R-DMG-01 §2]` | Established |
| `moverate1` | fixed · 32-bit | twice the `maxvelocity` just read | `[04 R-MOV-01 §6]`, `[04 §5.2]` | Established |
| `moverate2` | fixed · 32-bit | twice the `maxvelocity` just read | `[04 R-MOV-01 §6]`, `[04 §5.2]` | Established |
| `turnrate` | integer · 16-bit | 0 | `[04 R-MOV-01 §2]`, `[04 R-MOV-01 §4]` | Established |
| `waterline` | integer · 8-bit | 0 | `[04 R-MOV-01 §9]`, `[04 §9.2]` | Established |
| `transportsize` | integer · 8-bit | 0 | `[04 §10.2]`, `[04 R-AIR-01 §9]` | Established (cited) |
| `transportcapacity` | integer · 8-bit | 0 | `[04 §10.2]` | Established (cited) |
| `energymake` | floating · single float | 0.0 | `[05 R-ECO-01]`, `[05 R-PROD-01 §1]` | Established |
| `energyuse` | floating · single float | 0.0 | `[05 R-ECO-01]`, `[05 R-PROD-01 §1]` | Established |
| `metalmake` | floating · single float | 0.0 | `[05 R-ECO-01]`, `[05 R-PROD-01 §1]` | Established |
| `extractsmetal` | floating · single float | 0.0 | `[05 R-PROD-01 §1]`, `[03 R-TERR-01 §1]` | Established |
| `makesmetal` | integer · 8-bit | 0 | `[05 R-PROD-01 §1]` | Established |
| `windgenerator` | floating · single float | 0.0 | `[05 R-PROD-01 §1]` | Established |
| `tidalgenerator` | floating · single float | 0.0 | `[05 R-PROD-01 §1]` | Established |
| `energystorage` | floating · single float | 0.0 | `[05 R-ECO-01]` | Established |
| `metalstorage` | floating · single float | 0.0 | `[05 R-ECO-01]` | Established |
| `buildtime` | integer · 32-bit | 0 | `[05 "Construction arithmetic"]`, `[05 R-WORK-01]` | Established |
| `workertime` | integer · 16-bit | 0 | `[05 "Construction arithmetic"]`, `[05 R-WORK-01]` | Established |
| `healtime` | integer · 16-bit | 0 | `[04 R-SPEC-01 §4]` | Established |
| `maxdamage` | integer · 32-bit | 0 | `[06 §9.2]`, `[04 R-ORD-01 §7]`, `[04 R-SPEC-01 §6]` | Established |
| `sightdistance` | integer · 16-bit | 0 | `[03 §3.2]`, `[03 §3.4]` | Established |
| `radardistance` | integer · 16-bit | 0 | `[03 §3.4]`, `[06 R-WPN-03 §5]` | Established (cited) |
| `sonardistance` | integer · 16-bit | 0 | `[03 §3.4]` | Established (cited) |
| `radardistancejam` | integer · 16-bit | 0 | `[03 §3.4]`, `[03 §3.10]`, `[06 §3.1]` | Established (cited) |
| `sonardistancejam` | integer · 16-bit | 0 | `[03 §3.4]`, `[03 §3.10]` | Established (cited) |
| `bmcode` | integer · 8-bit | 0 | `[03 R-RND-02A]`, `[04 R-COLL-01 §2]` | Established |
| `standingmoveorder` | integer · flag word A, bits 0-1 | 2 | `[04 R-STANCE-01 §6]` | Established |
| `standingfireorder` | integer · flag word A, bits 2-3 | 2 | `[04 R-STANCE-01 §6]` | Established |
| `init_cloaked` | integer · flag word A, bit 4 | 0 | `[04 R-ORD-01 §2]`, `[05 R-ECO-01]`, `[03 §3.4]` | Established (cited) |
| `downloadable` | integer · flag word A, bit 5 | 0 | `[02 §5]` (downloadable enforcement), `[08 R-AI-01]` | Established |
| `builder` | integer · flag word A, bit 6 | 0 | `[04 R-ORD-01 §5]`, `[04 R-ORD-01 §2]`, `[04 R-ORD-01 §7]` | Established (cited) |
| `stealth` | integer · flag word A, bit 8 | 0 | `[03 §3.4]`, `[03 §3.9]` | Established (cited) |
| `cloakcost` | integer · single float | 0 | `[04 R-SPEC-01 §10]`, `[03 §3.4]` | Established |
| `cloakcostmoving` | integer · single float | the `cloakcost` value just read | `[04 R-SPEC-01 §10]` | Established |
| `mincloakdistance` | integer · 16-bit | 0 | `[03 §3.4]`, `[03 §3.2]` | Established (cited) |
| `buildangle` | integer · 16-bit | 0 | `[04 §2.3b]`, `[04 R-FAC-01]` | Established |
| `builddistance` | integer · 16-bit | 0 | `[04 R-ORD-01 §7]`, `[04 R-ORD-01 §1]`, `[04 §10.3]` | Established (cited) |
| `sortbias` | integer · 16-bit | 0 | inert (reader census: none) — `[04 R-SPEC-01 §7]` | Established |
| `cruisealt` | integer · 16-bit | 0 | `[04 §10.2]`, `[04 R-ORD-01 §7]`, `[04 §10.1]` | Established (cited) |
| `zbuffer` | integer · flag word A, bit 7 | 0 | `[03 R-REN-03A]` | Established |
| `isairbase` | integer · flag word A, bit 9 | 0 | `[04 R-ORD-01 §7]`, `[04 R-UNIT-06 §3]`, `[04 R-AIR-01 §6]` | Established (cited) |
| `istargetingupgrade` | integer · flag word A, bit 10 | 0 | `[04 R-SPEC-01 §8]` | Established |
| `teleporter` | integer · flag word A, bit 13 | 0 | inert (reader census: none) — `[04 R-SPEC-01 §2]` | Established |
| `hidedamage` | integer · flag word A, bit 14 | 0 | `[04 R-SPEC-01 §6]` | Established |
| `shootme` | integer · flag word A, bit 15 | 0 | `[04 R-SPEC-01 §5]`, `[06 §3.2]` (the option bit that bypasses it is the `ShootAll` chat toggle: `[02 R-KEYS-01 §4]`) | Established |
| `armoredstate` | integer · flag word A, bit 17 | 0 | `[06 R-DMG-01 §2]` | Established |
| `activatewhenbuilt` | integer · flag word A, bit 18 | 0 | `[04 R-SPEC-01 §12]` | Established |
| `canfly` | integer · flag word A, bit 11 | 0 | `[04 R-ORD-01 §7]`, `[04 §10.2]`, `[04 R-AIR-01 §7]` | Established (cited) |
| `canhover` | integer · flag word A, bit 12 | 0 | `[04 R-SPEC-01 §15]`, `[04 R-MOV-01 §8a]` | Established |
| `upright` | integer · flag word A, bit 20 | 0 | `[04 R-MOV-01 §9]`, `[04 R-MOV-01 §5]`, `[04 R-MOV-01 §8a]` | Established (cited) |
| `floater` | integer · flag word A, bit 19 | 0 | `[04 R-SPEC-01 §15]`, `[04 R-MOV-01 §8a]` | Established |
| `amphibious` | integer · flag word A, bit 21 | 0 | `[04 R-SPEC-01 §15]` | Established |
| `isfeature` | integer · flag word A, bit 24 | 0 | `[04 R-SPEC-01 §12]`, `[05 R-FEAT-01]`, `[06 §12.2]` | Established (cited) |
| `noshadow` | integer · flag word A, bit 25 | 0 | `[03 §5.3]`, `[03 §2.4]`, `[03 §10]` | Established (cited) |
| `immunetoparalyzer` | integer · flag word A, bit 26 | 0 | `[04 R-SPEC-01 §9]`, `[06 §10]` | Established |
| `hoverattack` | integer · flag word A, bit 27 | 0 | `[04 R-AIR-01 §8]`, `[04 §9.2]`, `[04 R-MOV-01 §9]` | Established (cited) |
| `antiweapons` | integer · flag word A, bit 29 | 0 | `[02 R-KEYS-01 §1]` (range-ring overlay only) | Established |
| `digger` | integer · flag word A, bit 30 | 0 | `[03 R-REN-03A]`, `[04 R-SPEC-01 §3]` | Established |
| `onoffable` | integer · flag word B, bit 2 | 0 | `[04 R-SPEC-01 §11]`, `[03 §3.9]` | Established |
| `mobilestandorders` | integer · flag word B, bit 0 | 0 | `[04 R-STANCE-01 §5]`, `[04 R-STANCE-01 §6]`, `[04 R-STANCE-01 §8]` | Established (cited) |
| `firestandorders` | integer · flag word B, bit 1 | 0 | `[04 R-STANCE-01 §5]`, `[04 R-STANCE-01 §6]`, `[04 R-STANCE-01 §8]` | Established (cited) |
| `canstop` | integer · flag word B, bit 3 | 0 | `[04 R-STANCE-01 §8]`, `[04 R-STANCE-01 §6]` | Established (cited) |
| `canattack` | integer · flag word B, bit 4 | 0 | `[04 R-ORD-01 §3]`, `[07 §8]` | Established (cited) |
| `canguard` | integer · flag word B, bit 5 | 0 | `[07 §8]` | Established (cited) |
| `canpatrol` | integer · flag word B, bit 6 | 0 | `[07 §8]` | Established (cited) |
| `canmove` | integer · flag word B, bit 7 | 0 | `[03 R-RND-02A]`, `[07 §8]`, `[08 R-TRIG-01 §3]` | Established (cited) |
| `canload` | integer · flag word B, bit 8 | 0 | `[04 R-AIR-01 §9]`, `[04 §10.2]`, `[07 §8]` | Established (cited) |
| `canreclamate` | integer · flag word B, bit 10 | 0 | `[04 R-ORD-01 §5]`, `[05 R-WORK-01]` (capability bit 9 is a copy, `[02 R-KEYS-01 §1]`) | Established |
| `canresurrect` | integer · flag word B, bit 11 | 0 | `[04 R-ORD-01 §5]`, `[05 R-WORK-01]` | Established |
| `cancapture` | integer · flag word B, bit 12 | 0 | `[04 R-ORD-01 §5]`, `[04 R-STANCE-01 §6]`, `[05 R-WORK-01]` | Established (cited) |
| `candgun` | integer · flag word B, bit 14 | 0 | `[07 §8]` | Established (cited) |
| `maneuverleashlength` | integer · 16-bit | 0 | `[04 R-STANCE-01 §4]`, `[04 R-STANCE-01 §1]`, `[04 R-STANCE-01 §6]` | Established (cited) |
| `attackrunlength` | integer · 16-bit | 0 | `[04 R-STANCE-01 §4]`, `[04 R-STANCE-01 §6]`, `[04 R-AIR-01 §8]` | Established (cited) |
| `kamikaze` | integer · flag word A, bit 28 | 0 | `[04 R-SPEC-01 §1]` | Established |
| `kamikazedistance` | integer · 16-bit | 0 | `[04 R-SPEC-01 §1]` | Established |
| `norestrict` | integer · flag word B, bit 15 | 0 | `[05 R-SHARE-01]`, `[08 R-SKIR-01 §10]` | Established |
| `showplayername` | integer · flag word B, bit 17 | 0 | inert (reader census: none) — `[04 R-SPEC-01 §14]` | Established |
| `commander` | integer · flag word B, bit 18 | 0 | `[08 R-TRIG-01 §3]`, `[05 R-SHARE-01]` | Established |
| `cantbetransported` | integer · flag word B, bit 19 | 0 | `[04 §10.2]` | Established (cited) |
| `selfdestructcountdown` | raw · flag word B, bits 20-22 | absent → 5 in the field (the raw accessor itself returns null) | `[04 R-SPEC-01 §13]` | Established |
| `category` | string · 100 bytes | empty | `[02 R-P0-03]` | Established |
| `soundcategory` | string · 100 bytes | empty | `[03 §8.3]` | Established |
| `corpse` | string · 100 bytes | empty | `[06 R-DMG-01 §5]`, `[05 R-FEAT-01]` | Established |
| `movementclass` | string · 100 bytes | empty | `[04 §6.1]`, `[02 §5]` (movement class record) | Established |
| `weapon1` | string · 128 bytes | empty | `[06 R-DMG-01 §5]` (resolution), `[06 §4.2]` (slot use) | Established |
| `weapon2` | string · 128 bytes | empty | `[06 R-DMG-01 §5]` (resolution), `[06 §4.2]` (slot use) | Established |
| `weapon3` | string · 128 bytes | empty | `[06 R-DMG-01 §5]` (resolution), `[06 §4.2]` (slot use) | Established |
| `explodeas` | string · 128 bytes | empty | `[06 R-DMG-01 §3]`, `[06 R-DMG-01 §5]` | Established |
| `selfdestructas` | string · 128 bytes | empty | `[06 R-DMG-01 §3]`, `[06 R-DMG-01 §5]` | Established |
| `YardMap` | string · 1024 bytes | empty | `[05 R-ECO-01]`, `[05 "Factory production lifecycle"]` | Established |
| `side` | string · 30 bytes | empty | `[02 §5]` (AI build-pick roulette filter) | Established |
| `ai_weight` | string · 64 bytes | empty | `[08 R-AI-01]` | Established |
| `ai_limit` | string · 64 bytes | empty | inert (reader census: none) — `[02 §5]` | Established |
| `wacky` | integer · flag word B, bit 16 | 0 | multiplayer restriction defaults/reset `[05 R-SHARE-01 §9]`, `[08 R-SKIR-01 §10]` | Established |

**weapon (`Weapons\*.tdf` section)**

| Key | Accessor · stored width | Default | Consumer | Evidence |
|---|---|---|---|---|
| `ID` | integer · 32-bit | -1 | record slot selection `[02 §5]` (R-CONTENT-02) | Established |
| `name` | string · 64 bytes | empty | `[07 §6]` (display string) | Established |
| `weaponvelocity` | floating · 32-bit | 0.0 | `[06 §3.3]`, `[06 R-WPN-03 §3]`, `[06 §4.3]` | Established (cited) |
| `startvelocity` | floating · 32-bit | 0.0 | `[06 §3.3]`, `[06 §6.6]` | Established (cited) |
| `weaponacceleration` | floating · 32-bit | 0.0 | `[06 §3.3]`, `[06 §6.6]` | Established (cited) |
| `range` | integer · 32-bit | 32767 | `[06 §3.3]` | Established |
| `coverage` | integer · 32-bit | 0 | `[06 §3.3]` (range-circle overlay only) | Established |
| `reloadtime` | floating · 16-bit | 0.0 | `[06 §3.3]`, `[06 §4.2]`, `[06 §11.1]` | Established (cited) |
| `energypershot` | floating · single float | 0.0 | `[06 §4.2]`, `[06 §11.1]`, `[07 §8]` | Established (cited) |
| `metalpershot` | floating · single float | 0.0 | `[06 §4.2]`, `[06 §11.1]`, `[07 §8]` | Established (cited) |
| `areaofeffect` | integer · 16-bit | 0 | `[06 §3.3]`, `[06 §12.2]`, `[06 R-DMG-01 §5]` | Established (cited) |
| `edgeeffectiveness` | floating · single float | 0.0 | `[06 §9.2]` | Established |
| `weapontimer` | floating · 16-bit | 0.0 | `[06 §4.3]`, `[06 §7.3]`, `[06 R-WFX-01 §4]` | Established (cited) |
| `noautorange` | integer · flag bit 27 | 0 | `[06 §7.3]` | Established (cited) |
| `turnrate` | floating · 16-bit | 0.0 | `[06 §3.3]`, `[06 §6.7]` | Established |
| `burst` | integer · 16-bit | 0 | `[06 §3.3]`, `[06 §4.3]` | Established (cited) |
| `burstrate` | floating · 16-bit | 0.0 | `[06 §4.3]`, `[06 §7.3]` | Established (cited) |
| `sprayangle` | integer · 16-bit | 0 | `[06 §4.3]`, `[06 R-WPN-03 §1]`, `[06 §3.3]` | Established (cited) |
| `duration` | floating · 16-bit | 0.0 | `[06 §6.10]`, `[06 §4.3]`, `[06 §7.3]` | Established (cited) |
| `randomdecay` | floating · 16-bit | 0.0 | `[06 §4.3]`, `[06 §7.3]` | Established (cited) |
| `smokedelay` | floating · 16-bit | 0.0 | `[06 §7.3]` | Established (cited) |
| `flighttime` | floating · 16-bit | 0.0 | `[06 §6.6]`, `[06 §7.3]` | Established (cited) |
| `holdtime` | floating · 16-bit | 0.0 | `[06 §7.3]`, `[06 §4.4]`, `[06 §8.1]` | Established (cited) |
| `minbarrelangle` | floating · single float | -11.25 | `[06 §3.3]` | Established (cited) |
| `firestarter` | integer · 8-bit | 0 | `[05 "Feature burning"]`, `[06 §6.10]` | Established |
| `rendertype` | integer · 8-bit | 0 | `[06 R-WFX-01 §4]`, `[06 R-WFX-01 §1]` | Established (cited) |
| `color` | integer · 8-bit | 0 | `[06 R-WFX-01 §1]`, `[06 R-WFX-01 §4]`, `[03 R-FX-01 §2]` | Established (cited) |
| `color2` | integer · 8-bit | 0 | `[06 R-WFX-01 §4]`, `[06 R-WFX-01 §1]`, `[03 §5.4]` | Established (cited) |
| `soundtrigger` | integer · flag bit 11 | 0 | `[06 §13.2]`, `[06 §4.3]`, `[06 R-WFX-01 §1]` | Established (cited) |
| `guidance` | integer · flag bit 12 | 0 | `[06 §6.7]`, `[06 §6.9]` | Established (cited) |
| `tracks` | integer · flag bit 13 | 0 | `[06 §6.6]` | Established (cited) |
| `lineofsight` | integer · flag bit 0 | 0 | `[06 §3.3]`, `[06 §6.2]`, `[06 §6.10]` | Established (cited) |
| `ballistic` | integer · flag bit 1 | 0 | `[06 §3.3]`, `[06 §6.2]`, `[06 R-WFX-01 §4]` | Established (cited) |
| `unitsonly` | integer · flag bit 14 | 0 | `[06 §9.3]` | Established (cited) |
| `groundbounce` | integer · flag bit 15 | 0 | `[06 §8.2]` | Established (cited) |
| `waterweapon` | integer · flag bit 16 | 0 | `[06 §6.9]` | Established (cited) |
| `toairweapon` | integer · flag bit 17 | 0 | `[02 R-KEYS-01 §2]` (attack resolver / fire-order weapon pick), `[06 R-WPN-05 §1]` (shot-admission gate) | Established |
| `smoketrail` | integer · flag bit 18 | 0 | `[06 §7.3]` | Established |
| `turret` | integer · flag bit 19 | 0 | `[06 §4.4]`, `[06 §7.3]`, `[06 §3.3]` | Established (cited) |
| `selfprop` | integer · flag bit 20 | 0 | `[06 §6.2]`, `[06 §3.3]`, `[06 R-WFX-01 §4]` | Established (cited) |
| `propeller` | integer · flag bit 21 | 0 | `[06 §6.1]`, `[06 §7.1]`, `[06 R-WFX-01 §4]` | Established (cited) |
| `noexplode` | integer · flag bit 22 | 0 | `[06 §6.10]`, `[06 §9.1]`, `[06 R-DMG-01 §5]` | Established (cited) |
| `burnblow` | integer · flag bit 23 | 0 | `[06 §6.6]`, `[06 §6.7]` | Established (cited) |
| `twophase` | integer · flag bit 24 | 0 | `[06 §6.6]` | Established (cited) |
| `cruise` | integer · flag bit 25 | 0 | `[06 §3.3]`, `[06 §6.7]`, `[06 §6.8]` | Established (cited) |
| `commandfire` | integer · flag bit 26 | 0 | `[06 §3.2]`, `[06 §4.2]`, `[06 §6.8]` | Established (cited) |
| `stockpile` | integer · flag bit 28 | 0 | `[06 §3.3]`, `[06 R-P0-07]`, `[06 R-WPN-03 §6]` | Established (cited) |
| `targetable` | integer · flag bit 29 | 0 | `[06 §11.2]` | Established (cited) |
| `interceptor` | integer · flag bit 30 | 0 | `[06 §3.2]`, `[06 §3.3]`, `[06 §11.2]` | Established |
| `beamweapon` | integer · flag bit 3 | 0 | `[06 §6.3]`, `[06 R-WFX-01 §4]` | Established (cited) |
| `shellweapon` | integer · flag bit 2 | 0 | inert (reader census: none) — `[02 R-KEYS-01 §2]` | Established |
| `dropped` | integer · flag bit 8 | 0 | `[06 §6.2]`, `[06 §3.2]`, `[06 §3.3]` | Established (cited) |
| `vlaunch` | integer · flag bit 4 | 0 | `[06 §3.3]`, `[06 R-P0-07]`, `[06 R-WPN-03 §6]` | Established (cited) |
| `meteor` | integer · flag bit 5 | 0 | `[06 §6.2]`, `[06 §6.5]` | Established |
| `noradar` | integer · flag bit 6 | 0 | `[06 §11.3]`, `[03 §3.9]` | Established (cited) |
| `paralyzer` | integer · flag bit 7 | 0 | `[06 §10]` | Established |
| `startsmoke` | integer · flag bit 9 | 0 | `[06 §7.3]`, `[06 R-WFX-01 §4]`, `[06 R-WFX-01 §5]` | Established (cited) |
| `endsmoke` | integer · flag bit 10 | 0 | `[06 §7.3]`, `[06 R-WFX-01 §5]` | Established (cited) |
| `accuracy` | integer · 16-bit | 0 | `[06 §4.4]`, `[06 §3.3]`, `[06 R-WPN-03 §1]` | Established (cited) |
| `tolerance` | integer · 16-bit | 0 | `[06 R-WPN-03 §2]`, `[06 R-WPN-03 §1]`, `[06 §3.3]` | Established (cited) |
| `pitchtolerance` | integer · 16-bit | 0 | `[06 R-WPN-03 §2]`, `[06 §3.3]`, `[06 R-WPN-03 §1]` | Established (cited) |
| `shakemagnitude` | integer · 32-bit | 0 | `[03 §5.6]` | Established |
| `shakeduration` | floating · 32-bit | 0.0 | `[03 §5.6]` | Established |
| `model` | string · 256 bytes | empty | `[06 R-WFX-01 §4]` (model render families) | Established |
| `explosiongaf` | string · 256 bytes | empty | `[06 R-WFX-01 §1]` | Established (cited) |
| `explosionart` | string · 256 bytes | empty | `[06 R-WFX-01 §1]` | Established (cited) |
| `waterexplosiongaf` | string · 256 bytes | empty | `[06 R-WFX-01 §1]` | Established (cited) |
| `waterexplosionart` | string · 256 bytes | empty | `[06 R-WFX-01 §1]` | Established (cited) |
| `lavaexplosiongaf` | string · 256 bytes | empty | `[06 R-WFX-01 §1]` | Established (cited) |
| `lavaexplosionart` | string · 256 bytes | empty | `[06 R-WFX-01 §1]` | Established (cited) |
| `soundstart` | string · 256 bytes | empty | `[06 §13.2]`, `[06 R-WFX-01 §1]`, `[06 R-WFX-01 §3]` | Established (cited) |
| `soundhit` | string · 256 bytes | empty | `[06 §13.2]`, `[06 R-WFX-01 §1]`, `[06 R-WFX-01 §3]` | Established (cited) |
| `soundwater` | string · 256 bytes | empty | `[06 §7.3]`, `[06 §13.2]`, `[06 R-WFX-01 §1]` | Established (cited) |

**weapon `[DAMAGE]` child section**

| Key | Accessor · stored width | Default | Consumer | Evidence |
|---|---|---|---|---|
| `default` | integer · 16-bit | 0 | `[06 R-DMG-01 §1]`, `[06 §9.2]` | Established |

**feature (feature TDF section)**

| Key | Accessor · stored width | Default | Consumer | Evidence |
|---|---|---|---|---|
| `Description` | string · 20 bytes | empty | `[07 §6]` (feature information text) | Supported inference |
| `footprintx` | integer · 16-bit | 0 | `[05 R-WORK-01]`, `[05 R-FEAT-01]`, `[03 §5.1]` | Established (cited) |
| `footprintz` | integer · 16-bit | 0 | `[05 R-WORK-01]`, `[05 R-FEAT-01]`, `[03 §5.1]` | Established (cited) |
| `height` | integer · 8-bit | 0 | `[05 R-FEAT-01]`, `[03 R-TERR-01 §4]`, `[03 §3.5]` | Established (cited) |
| `object` | string · 256 bytes | empty | `[05 R-FEAT-01]`, `[03 §5.1]` | Established (cited) |
| `filename` | string · 256 bytes | empty | `[05 R-FEAT-01]`, `[03 §5.1]` | Established (cited) |
| `seqname` | string · 256 bytes | empty | `[05 R-FEAT-01]`, `[03 §5.1]` | Established (cited) |
| `seqnameshad` | string · 256 bytes | empty | `[05 R-FEAT-01]`, `[03 §5.1]`, `[03 §5.3]` | Established (cited) |
| `seqnameburn` | string · 256 bytes | empty | `[05 R-FEAT-01]`, `[05 "Feature burning"]`, `[03 §5.1]` | Established (cited) |
| `seqnameburnshad` | string · 256 bytes | empty | `[05 R-FEAT-01]` | Established (cited) |
| `seqnamedie` | string · 256 bytes | empty | `[05 R-FEAT-01]`, `[03 §5.1]` | Established (cited) |
| `seqnamedieshad` | string · 256 bytes | empty | `[05 R-FEAT-01]` | Established (cited) |
| `seqnamereclamate` | string · 256 bytes | empty | `[05 R-FEAT-01]`, `[03 §5.1]` | Established (cited) |
| `seqnamereclamateshad` | string · 256 bytes | empty | `[05 R-FEAT-01]` | Established (cited) |
| `spreadchance` | integer · 8-bit | 0 | `[05 R-FEAT-01]`, `[05 "Feature burning"]`, `[03 §5.1]` | Established (cited) |
| `reproduce` | integer · 8-bit | 0 | `[05 R-FEAT-01]`, `[05 "Feature reproduction"]`, `[05 "Required implementation invariants"]` | Established (cited) |
| `reproducearea` | integer · 8-bit | 0 | `[05 R-FEAT-01]`, `[03 §5.1]` | Established (cited) |
| `metal` | integer · masked to 16 bits, then single float | 0 | `[05 R-SHARE-01]`, `[05 R-WORK-01]`, `[05 R-FEAT-01 §1]` | Established (cited) |
| `energy` | integer · masked to 16 bits, then single float | 0 | `[05 R-SHARE-01]`, `[05 R-WORK-01]`, `[05 R-FEAT-01 §1]` | Established (cited) |
| `damage` | integer · 16-bit | 0 | `[05 R-FEAT-01]`, `[05 "Feature catalog and placement"]`, `[03 §5.1]` | Established (cited) |
| `animating` | integer · flag bit 1 | 0 | `[05 R-FEAT-01]`, `[03 §5.1]` | Established (cited) |
| `animtrans` | integer · flag bit 2 | 0 | `[05 R-FEAT-01]`, `[03 §5.1]` | Established (cited) |
| `shadtrans` | integer · flag bit 3 | 0 | `[05 R-FEAT-01]`, `[03 §5.1]`, `[03 §5.3]` | Established (cited) |
| `flamable` | integer · flag bit 4 | 0 | `[05 R-FEAT-01]`, `[03 §5.1]` | Established (cited) |
| `geothermal` | integer · flag bit 5 | 0 | `[05 R-FEAT-01]`, `[05 "Feature catalog and placement"]`, `[05 "Prerequisite structures"]` | Established (cited) |
| `blocking` | integer · flag bit 6 | 0 | `[05 R-FEAT-01]`, `[05 "Prerequisite structures"]`, `[05 "Feature catalog and placement"]` | Established (cited) |
| `reclaimable` | integer · flag bit 7 | 0 | `[05 R-FEAT-01]`, `[05 "Prerequisite structures"]`, `[05 "Feature catalog and placement"]` | Established (cited) |
| `autoreclaimable` | integer · flag bit 8 | 1 | `[05 R-FEAT-01]`, `[05 "Feature catalog and placement"]`, `[03 §5.1]` | Established (cited) |
| `indestructible` | integer · flag bit 9 | 0 | `[05 R-FEAT-01]`, `[05 "Prerequisite structures"]`, `[05 "Feature catalog and placement"]` | Established (cited) |
| `nodisplayinfo` | integer · flag bit 10 | 0 | `[05 R-FEAT-01]` | Established (cited) |
| `nodrawundergray` | integer · flag bit 11, also forced set by four section names | 0 | `[05 R-FEAT-01 §1]`, `[03 §5.1]` | Established (cited) |
| `sparktime` | floating · 16-bit | 0.0 | `[05 R-FEAT-01]`, `[05 "Prerequisite structures"]`, `[03 §5.1]` | Established (cited) |
| `burnweapon` | string · 256 bytes | empty | `[05 R-FEAT-01]`, `[05 "Feature burning"]`, `[03 §5.1]` | Established (cited) |

**movement class (`MOVEINFO.TDF` `[CLASS<n>]`, also applied to an FBI section)**

| Key | Accessor · stored width | Default | Consumer | Evidence |
|---|---|---|---|---|
| `FootPrintX` | integer · 16-bit | 0 | `[04 §6.1]`, `[04 R-PATH-01 §7]` | Established |
| `FootPrintZ` | integer · 16-bit | 0 | `[04 §6.1]`, `[04 R-PATH-01 §7]` | Established |
| `maxwaterdepth` | integer · 16-bit | the record's own prior value (template pre-fill, `[04 §6.1]`) | `[04 §6.1]`, `[04 R-COLL-01 §2]` | Established |
| `minwaterdepth` | integer · 16-bit | the record's own prior value (template pre-fill, `[04 §6.1]`) | `[04 §6.1]`, `[04 R-COLL-01 §2]` | Established |
| `maxslope` | integer · 8-bit | the record's own prior value (template pre-fill, `[04 §6.1]`) | `[04 §6.1]`, `[04 R-COLL-01 §2]` | Established |
| `badslope` | integer · 8-bit | half (`>>1`, logical) of the `maxslope` value read immediately before it — **not** the record's prior value (`[04 §6.1]`) | `[04 §6.1]` | Established |
| `maxwaterslope` | integer · 8-bit | the record's own prior value (template pre-fill, `[04 §6.1]`) | `[04 §6.1]` | Established |
| `badwaterslope` | integer · 8-bit | half (`>>1`, logical) of the `maxwaterslope` value read immediately before it — **not** the record's prior value (`[04 §6.1]`) | `[04 §6.1]` | Established |

**campaign file `[MISSION<n>]` (read on the campaign path only; `maxunits` is then read from the OTA `[GlobalHeader]`)**

| Key | Accessor · stored width | Default | Consumer | Evidence |
|---|---|---|---|---|
| `missionname` | lang-string · 256 bytes | empty | `[08 R-CAMP-01 §1]` | Established |
| `missionfile` | string · 256 bytes | empty | `[02 R-MAP-01 §2]`, `[08 R-CAMP-01 §1]` | Established |
| `maxunits` | integer · 16-bit | 200 | `[02 R-MAP-01 §3]`, `[08 R-SKIR-01 §6]`, `[05 "Unit creation and limits"]` | Established |

**OTA `[GlobalHeader]` / `[Schema <n>]`**

| Key | Accessor · stored width | Default | Consumer | Evidence |
|---|---|---|---|---|
| `brief` | lang-string · 256 bytes | empty | `[02 R-MAP-01 §3]`, `[08 R-CAMP-01 §2]` | Established |
| `narration` | lang-string · 256 bytes | empty | `[02 R-MAP-01 §3]`, `[03 §8.4]` | Established |
| `missionhint` | lang-string · 256 bytes | empty | inert (reader census: none) — `[02 R-MAP-01 §3]` | Established |
| `glamour` | string · 256 bytes | empty | `[02 R-MAP-01 §3]`, `[08 R-CAMP-01 §2]` | Established |
| `glamoursound` | string · 256 bytes | empty | `[02 R-MAP-01 §3]`, `[03 §8.4]` | Established |
| `UseOnlyUnits` | string · 256 bytes | empty | `[02 R-MAP-01 §3]`, `[08 "Session structures"]` | Established |
| `mapping` | integer · 32-bit | 0 | `[08 R-SKIR-01 §4]` | Established |
| `lineofsight` | integer · 32-bit | 0 | `[08 R-SKIR-01 §4]` | Established |
| `memory` | string · 128 bytes | empty | inert (reader census: none) — `[02 R-MAP-01 §3]` | Established |
| `numplayers` | string · 128 bytes | empty | inert (reader census: none) — `[02 R-MAP-01 §3]` | Established |
| `Planet` | string · 128 bytes | empty | `[02 R-MAP-01 §3]`, `[08 R-CAMP-01 §2]` | Established |
| `nomovie` | integer · 32-bit | 0 | `[08 R-CAMP-01 §6]` | Established |
| `missiondescription` | string · 128 bytes | `No description available` | `[02 R-MAP-01 §3]` (translated; front-end screen reader open, doc 07) | Established |
| `minwindspeed` | integer · 32-bit | 0 | `[02 R-MAP-01 §6]`, `[03 R-TERR-01 §6]`, `[05 R-PROD-01 §1]` | Established |
| `maxwindspeed` | integer · 32-bit | 0 | `[02 R-MAP-01 §6]`, `[03 R-TERR-01 §6]`, `[05 R-PROD-01 §1]` | Established |
| `gravity` | integer · 32-bit | 0 | `[02 R-MAP-01 §6]`, `[06 §6.2]` | Established |
| `tidalstrength` | floating · single float | 0.0 | `[02 R-MAP-01 §6]`, `[05 R-PROD-01 §4]` | Established |
| `lavaworld` | integer · 32-bit | 0 | `[03 R-TERR-01 §2]`, `[08 R-SKIR-01 §3]` | Established |
| `nosealeveltrigger` | integer · 32-bit | 0 | `[06 §8.2]`, `[04 R-COB-04 §2]` | Established |
| `waterdoesdamage` | integer · 32-bit | 0 | `[04 §9.2]` | Established |
| `waterdamage` | integer · 32-bit | 0 | `[04 §9.2]` | Established |
| `killmul` | floating · single float | 0.0 | read by the end-of-battle score helper `[08 R-CAMP-01 §7]` | Established |
| `timemul` | floating · single float | 0.0 | read by the end-of-battle score helper `[08 R-CAMP-01 §7]` | Established |
| `HumanMetal` | integer · single float | 0 | `[08 R-SKIR-01 §5]` | Established |
| `HumanEnergy` | integer · single float | 0 | `[08 R-SKIR-01 §5]` | Established |
| `ComputerMetal` | integer · single float | 0 | `[08 R-SKIR-01 §5]` | Established |
| `ComputerEnergy` | integer · single float | 0 | `[08 R-SKIR-01 §5]` | Established |
| `SurfaceMetal` | integer · 32-bit | 0 | `[03 R-TERR-01 §1]`, `[02 R-MAP-01 §6]` | Established |
| `aiprofile` | string · 256 bytes | empty | `[08 R-AI-01]`, `[02 R-MAP-01 §5]` | Established |
| `MeteorWeapon` | string · 32 bytes | empty | `[02 §6]` (meteor merge and enable contract), `[06 §6.5]` | Established |
| `MeteorRadius` | integer · single float | 0 | `[02 §6]` (meteor merge and enable contract), `[06 §6.5]` | Established |
| `MeteorDensity` | floating · single float | 0.0 | `[02 §6]` (meteor merge and enable contract), `[06 §6.5]` | Established |
| `MeteorDuration` | floating · single float | 0.0 | `[02 §6]` (meteor merge and enable contract), `[06 §6.5]` | Established |
| `MeteorInterval` | floating · single float | 0.0 | `[02 §6]` (meteor merge and enable contract), `[06 §6.5]` | Established |

**OTA placed objects (`[units]`, `[features]`, `[specials]`)**

| Key | Accessor · stored width | Default | Consumer | Evidence |
|---|---|---|---|---|
| `Unitname` | string · 1024 bytes | empty | `[08 R-TRIG-01 §9]`, `[08 R-TRIG-01 §11]`, `[08 R-AI-01]` | Established (cited) |
| `Ident` | string · 1024 bytes | empty | `[08 R-TRIG-01 §9]`, `[08 R-TRIG-01 §11]` | Established (cited) |
| `InitialMission` | string · 1024 bytes | empty | `[08 R-TRIG-01 §9]`, `[08 R-TRIG-01 §3]`, `[04 §3.6]` | Established (cited) |
| `XPos` | integer · 32-bit | 0 | `[08 R-TRIG-01 §9]` | Established (cited) |
| `YPos` | integer · 32-bit | 0 | `[08 R-TRIG-01 §9]` | Established (cited) |
| `ZPos` | integer · 32-bit | 0 | `[08 R-TRIG-01 §9]` | Established (cited) |
| `Angle` | integer · 16-bit | 0 | `[08 R-TRIG-01 §9]` | Established (cited) |
| `Player` | integer · 8-bit | 0 | `[08 R-SKIR-01 §1]`, `[08 R-TRIG-01 §9]`, `[08 "Skirmish configuration"]` | Established (cited) |
| `HealthPercentage` | integer · 16-bit | 100 | `[08 R-TRIG-01 §9]` | Established (cited) |
| `BuildPriority` | integer · 16-bit | 0 | `[08 R-TRIG-01 §9]`, `[08 "Session structures"]` | Established (cited) |
| `CreationCountdown` | integer · 32-bit | 0 | `[08 R-TRIG-01 §9]` | Established (cited) |
| `MissionCriticalUnit` | integer · 8-bit | 0 | `[08 R-TRIG-01 §9]`, `[08 "Session structures"]` | Established (cited) |
| `AiIgnore` | integer · 8-bit | 0 | `[08 R-TRIG-01 §9]`, `[08 "Session structures"]` | Established (cited) |
| `AiPriorityTarget` | integer · 8-bit | 0 | `[08 R-TRIG-01 §9]`, `[08 "Session structures"]` | Established (cited) |
| `InitialGroup` | integer · 8-bit | 0 | `[08 "Session structures"]`, `[08 R-TRIG-01 §9]`, `[08 R-TRIG-01 §11]` | Established (cited) |
| `Immunity` | integer · 8-bit | 0 | `[08 R-TRIG-01 §9]` | Established (cited) |
| `Featurename` | string · 128 bytes | empty | `[08 R-TRIG-01 §9]` | Established (cited) |

**OTA triggers (`[GlobalHeader]`)**

| Key | Accessor · stored width | Default | Consumer | Evidence |
|---|---|---|---|---|
| `KillEnemyCommander` | integer · 32-bit | 0 | `[08 R-TRIG-01 §2]`, `[08 "Session structures"]`, `[08 "Victory and defeat triggers"]` | Established (cited) |
| `DestroyAllUnits` | integer · 32-bit | 0 | `[08 R-TRIG-01 §4]`, `[08 "Victory and defeat triggers"]`, `[08 R-TRIG-01 §2]` | Established (cited) |
| `KillAllMobileUnits` | integer · 32-bit | 0 | `[08 R-TRIG-01 §2]`, `[08 "Session structures"]`, `[08 "Victory and defeat triggers"]` | Established (cited) |
| `BuildUnitType` | string · 256 bytes | empty | `[08 R-TRIG-01 §4]`, `[08 "Victory and defeat triggers"]`, `[08 R-TRIG-01 §2]` | Established (cited) |
| `CaptureUnitType` | string · 256 bytes | empty | `[08 "Victory and defeat triggers"]`, `[08 R-TRIG-01 §10]`, `[08 "Session structures"]` | Established (cited) |
| `KillAllOfType` | string · 256 bytes | empty | `[08 R-TRIG-01 §2]`, `[08 "Victory and defeat triggers"]`, `[08 "Session structures"]` | Established (cited) |
| `KillUnitType` | string · 256 bytes | empty | `[08 R-TRIG-01 §2]`, `[08 "Victory and defeat triggers"]`, `[08 "Session structures"]` | Established (cited) |
| `MoveUnitToRadius` | string · 256 bytes | empty | `[08 R-TRIG-01 §2]`, `[08 "Victory and defeat triggers"]`, `[08 R-TRIG-01 §3]` | Established (cited) |
| `UnitTypePassesX` | string · 256 bytes | empty | `[08 "Victory and defeat triggers"]`, `[08 R-TRIG-01 §4]` | Established (cited) |
| `UnitTypePassesZ` | string · 256 bytes | empty | `[08 "Victory and defeat triggers"]`, `[08 R-TRIG-01 §4]` | Established (cited) |
| `VictoryTimerRunsOut` | integer · 32-bit | 0 | `[08 "Victory and defeat triggers"]`, `[08 R-TRIG-01 §4]` | Established (cited) |
| `CommanderKilled` | integer · 32-bit | 0 | `[08 R-TRIG-01 §2]`, `[08 "Session structures"]`, `[08 "Victory and defeat triggers"]` | Established (cited) |
| `AllUnitsKilled` | integer · 32-bit | 0 | `[08 R-TRIG-01 §2]`, `[08 "Session structures"]`, `[08 "Victory and defeat triggers"]` | Established (cited) |
| `AllUnitsKilledOfType` | string · 256 bytes | empty | `[08 "Victory and defeat triggers"]`, `[08 R-TRIG-01 §2]`, `[08 "Session structures"]` | Established (cited) |
| `UnitTypeKilled` | string · 256 bytes | empty | `[08 "Victory and defeat triggers"]`, `[08 R-TRIG-01 §2]`, `[08 "Session structures"]` | Established (cited) |
| `DeathTimerRunsOut` | integer · 32-bit | 0 | `[08 "Victory and defeat triggers"]`, `[08 R-TRIG-01 §4]` | Established (cited) |
| `AnyUnitPassesX` | integer · 32-bit | -1 | `[08 "Victory and defeat triggers"]`, `[08 R-TRIG-01 §4]` | Established (cited) |
| `AnyUnitPassesZ` | integer · 32-bit | -1 | `[08 "Victory and defeat triggers"]`, `[08 R-TRIG-01 §4]` | Established (cited) |

**side (`SIDEDATA.TDF` `[SIDE<n>]` and its anchor subsections)**

| Key | Accessor · stored width | Default | Consumer | Evidence |
|---|---|---|---|---|
| `name` | string · 30 bytes | empty | `[07 §6]`, `[08 R-CAMP-01 §2]` (campaign-side match) | Established |
| `nameprefix` | string · 4 bytes | empty | unknown: no reader located by the key-name grep — decider: reader census on the side record's 4-byte prefix field | Unknown |
| `commander` | string · 32 bytes | empty | `[08 R-TRIG-01 §3]`, `[08 R-SKIR-01 §7]` | Established |
| `font` | string · 256 bytes | empty | `[02 §6]` (side font load; missing font is fatal) | Established |
| `energycolor` | integer · 32-bit | 0 | `[02 §6]`, `[07 §6]` (resource bar palette index) | Established |
| `metalcolor` | integer · 32-bit | 0 | `[02 §6]`, `[07 §6]` | Established |
| `x1` | integer · 32-bit | 0 | `[02 §6]`, `[07 §6]` (anchor rectangles) | Established |
| `y1` | integer · 32-bit | 0 | `[02 §6]`, `[07 §6]` | Established |
| `x2` | integer · 32-bit | 0 | `[02 §6]`, `[07 §6]` | Established |
| `y2` | integer · 32-bit | 0 | `[02 §6]`, `[07 §6]` | Established |

**weapon `[DAMAGE]` child section**

| Key | Accessor · stored width | Default | Consumer | Evidence |
|---|---|---|---|---|
| `<any other key>` | integer · 32-bit (sorted override table entry) | 0 | `[06 R-DMG-01 §1]` (per-unit-name override; lookup `[06 §9.2]`) | Established |

**sound category (`SOUND.TDF` section)**

| Key | Accessor · stored width | Default | Consumer | Evidence |
|---|---|---|---|---|
| `select` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-ORD-01 §1]` | Established (cited) |
| `underattack` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-ORD-01 §1]` | Established (cited) |
| `activate` | string (variant list) · 64 bytes per variant | no variant | `[03 R-RND-02A]`, `[03 §8.3]`, `[04 R-SPEC-01 §12]` | Established (cited) |
| `deactivate` | string (variant list) · 64 bytes per variant | no variant | `[03 R-RND-02A]`, `[03 §8.3]`, `[04 R-P0-10]` | Established (cited) |
| `ok` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-ORD-01 §1]`, `[07 §11]` | Established (cited) |
| `arrived` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-ORD-01 §1]`, `[04 R-ORD-01 §3]` | Established (cited) |
| `cant` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-ORD-01 §1]` | Established (cited) |
| `unitcomplete` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-ORD-01 §1]` | Established (cited) |
| `build` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-ORD-01 §1]` | Established (cited) |
| `repair` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-ORD-01 §1]` | Established (cited) |
| `working` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-ORD-01 §5]`, `[04 R-ORD-01 §1]` | Established (cited) |
| `load` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-ORD-01 §1]`, `[07 §5]` | Established (cited) |
| `unload` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-ORD-01 §1]` | Established (cited) |
| `cloak` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-STANCE-01 §2]`, `[04 R-ORD-01 §1]` | Established (cited) |
| `uncloak` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-ORD-01 §1]` | Established (cited) |
| `capture` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-ORD-01 §1]`, `[04 R-ORD-01 §5]` | Established (cited) |
| `count5` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-ORD-01 §1]`, `[04 R-SPEC-01 §13]` | Established (cited) |
| `count4` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-ORD-01 §1]` | Established (cited) |
| `count3` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-ORD-01 §1]` | Established (cited) |
| `count2` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-ORD-01 §1]` | Established (cited) |
| `count1` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-ORD-01 §1]` | Established (cited) |
| `count0` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-ORD-01 §1]`, `[04 R-SPEC-01 §13]` | Established (cited) |
| `canceldestruct` | string (variant list) · 64 bytes per variant | no variant | `[03 §8.3]`, `[04 R-ORD-01 §1]` | Established (cited) |
| `<event><n>, <event>text, <event><n>text` | string (numbered variants and captions) · 64 bytes each (63 characters) | empty caption | `[02 R-SND-01 §1]` (gather order: bare, then `1..n` contiguous, bare never gates), `[03 §8.3]` | Established |

**registry preference (`Total Annihilation` key)**

| Key | Accessor · stored width | Default | Consumer | Evidence |
|---|---|---|---|---|
| `Interface Type` | DWORD · 32-bit | 0 | `[07 §8]` | Established |
| `DisplaymodeWidth` | DWORD · 32-bit | 640 | `[07 R-FE-01 §11]` (the campaign/skirmish load transitions resize the window and offscreen surface) | Established (cited) |
| `DisplaymodeHeight` | DWORD · 32-bit | 480 | `[07 R-FE-01 §11]` (as `DisplaymodeWidth`) | Established (cited) |
| `side` | DWORD · 32-bit | 0 | `[02 R-KEYS-01 §3]` (last chosen side; briefing screens and save restore) | Supported inference |
| `Difficulty` | DWORD · 32-bit | 1 | `[07 §5]`, `[07 §11]`, `[08 R-CAMP-01 §3]` | Established (cited) |
| `scrollspeed` | DWORD · 32-bit | 32 | `[03 R-FX-01 §7]` | Established (cited) |
| `SingleCommanderDeath` | DWORD · 32-bit | 1 | `[08 R-SKIR-01 §4]` (single-player option word, by analogy with the skirmish loader) | Supported inference |
| `SingleMapping` | DWORD · 32-bit | 1 | `[03 §3.1]` | Established (cited) |
| `SingleLineOfSight` | DWORD · 32-bit | 1 | `[03 §3.1]` | Established (cited) |
| `SingleLOSType` | DWORD · 32-bit | 1 | `[03 §3.1]` | Established (cited) |
| `screenchat` | DWORD · 32-bit | 1 | `[07 R-FE-01 §11]` (footer line filter branch; polarity still open there) | Established (cited) |
| `damagebars` | DWORD · 32-bit | bit clear (0) | `[03 R-FX-01 §6]` | Established (cited) |
| `Sound Mode` | DWORD · 32-bit, low 3 bits kept | 1 (`Mono`) | `[02 R-SND-01 §2]`, `[03 R-AUD-01 §1]`, `[03 R-AUD-01 §2]` (play gate; `2` = 3-D) | Established (cited) |
| `MixingBuffers` | DWORD · 32-bit | 8 | `[02 R-SND-01 §2]`, `[03 R-AUD-01 §1]` (mixer voice limit) | Established (cited) |
| `RestoreVolume` | DWORD · 32-bit, bit 0 kept | bit clear (0) | `[02 R-SND-01 §2]`, `[03 R-AUD-01 §2]` (gates the `WaveOutVolume`/`CDAudioVolume` round-trip) | Established (cited) |
| `WaveOutVolume` | DWORD · 32-bit | no default installed | `[02 R-SND-01 §2]`, `[03 R-AUD-01 §2]` (read/written only when `RestoreVolume` is set) | Established (cited) |
| `CDAudioVolume` | DWORD · 32-bit | no default installed | `[02 R-SND-01 §2]`, `[03 R-AUD-01 §2]` (read/written only when `RestoreVolume` is set) | Established (cited) |
| `CDLISTS` | binary · 2,720 bytes | zeroed | `[02 R-SND-01 §2]`, `[03 R-AUD-01 §4]` (per-disc CD category ring) | Established (cited) |
| `Anti-Alias` | DWORD · 32-bit | bit set (1) | `[03 R-REN-03A]` (structure anti-aliasing gate) | Supported inference |
| `Shadows` | DWORD · 32-bit | bit set (1) | `[03 §5.3]` | Supported inference |
| `FeatureShadows` | DWORD · 32-bit | bit set (1) | `[03 §5.3]` | Supported inference |
| `VehicleShadows` | DWORD · 32-bit | bit set (1) | `[03 §5.3]` | Supported inference |
| `Shading` | DWORD · 32-bit | bit set (1) | `[03 R-RND-02A]` | Established |
| `DitheredFog` | DWORD · 32-bit, low bit kept | bit clear (0) | `[03 R-RR16-A §2]`, `[07 R-FE-01 §11]` (fog-presenter pattern selection) | Established (cited) |
| `Gamma` | DWORD · 32-bit; loader maps exactly 10 to 12, otherwise preserves it | 12 | `[07 R-FE-01 §11]` (slider factor and command factor have distinct conversions; writer preserves the current integer) | Established (cited) |
| `SwitchAlt` | DWORD · 32-bit | no default installed | `[07 R-CAM-01 §4]` | Established (cited) |
| `Password` | string · 11 bytes (incl. NUL) | empty | out of scope (multiplayer lobby) | Established |
| `Nickname` | string · 17 bytes (incl. NUL) | empty | `[08 R-SKIR-01 §1]` (local player name); lobby use out of scope | Supported inference |
| `Game Name` | string · 17 bytes (incl. NUL) | empty | out of scope (multiplayer lobby) | Established |
| `Image Output Directory` | string · 256 bytes (incl. NUL) | empty | `[03 §9]` (screenshot/movie output path) | Supported inference |
| `Movie Output Rate` | DWORD · 32-bit | 10 | `[03 §9]` | Established (cited) |
| `textlines` | DWORD · 32-bit | 10 | `[07 R-FE-01 §11]` (chat ring line budget; 0 disables storage) | Established (cited) |
| `textscroll` | DWORD · 32-bit | 10 | `[07 R-FE-01 §11]` (line expiry `(textscroll + 1) × 30` ticks) | Established (cited) |
| `mousespeed` | DWORD · 32-bit | 10 | **no reader** — persisted only (`[07 R-FE-01 §11]`, bounded negative) | Established (cited) |
| `gamespeed` | DWORD · 32-bit | 10 | `[07 R-CAM-01 §3]` (speed setter, clamp 1..20) | Established (cited) |
| `unitchat` | DWORD · 32-bit | 10 | `[03 R-AUD-01 §3]` | Established (cited) |
| `unitchattext` | DWORD · 32-bit | 5 | `[07 R-FE-01 §11]` (caption gate `10 − v < priority`) | Established (cited) |
| `musicmode` | DWORD · 32-bit, bit 0 kept | bit set (1) | `[02 R-SND-01 §2]`, `[03 R-AUD-01 §4]` (CD enable) | Established (cited) |
| `cdmode` | DWORD · low byte stored | 4 (`Custom`) | `[02 R-SND-01 §2]`, `[03 R-AUD-01 §4]` (CD play mode 1..4) | Established (cited) |
| `ackfx` | DWORD · 32-bit, bit 0 kept | bit set (1) | `[02 R-SND-01 §2]`, `[03 R-AUD-01 §2]` — persisted, gates nothing (bounded negative) | Established (cited) |
| `buildfx` | DWORD · 32-bit, bit 0 kept | bit set (1) | `[02 R-SND-01 §2]`, `[03 R-AUD-01 §2]` — persisted, gates nothing (bounded negative) | Established (cited) |
| `speechfx` | DWORD · 32-bit, bit 0 kept | bit set (1) | `[02 R-SND-01 §2]`, `[03 R-AUD-01 §3]` (unit voice audible gate) | Established (cited) |
| `fxvol` | DWORD · 32-bit | 27 | `[03 R-AUD-01 §2]` (play gate ≠ 0; system wave mixer level `v << 10`) | Established (cited) |
| `musicvol` | DWORD · 32-bit | 32 | `[03 R-AUD-01 §2]`, `[03 R-AUD-01 §4]` (CD auxiliary level `v << 10`) | Established (cited) |
| `clock` | DWORD · 32-bit, bit 0 kept | bit clear (0) | `[02 §3]`, `[07 R-CAM-01 §6]` (persistent stand-alone clock display) | Established (cited) |
| `NumSkirmishPlayers` | DWORD · 32-bit | 4 | `[07 §5]`, `[08 R-SKIR-01 §1]`, `[08 "Skirmish configuration"]` | Established (cited) |
| `MultiCommanderDeath` | DWORD · 32-bit | 1 | `[08 R-SKIR-01 §4]` (multiplayer option word, by analogy with the skirmish loader) | Supported inference |
| `MultiMapping` | DWORD · 32-bit | 1 | `[03 §3.1]` | Established (cited) |
| `MultiLineOfSight` | DWORD · 32-bit | 1 | `[03 §3.1]` | Established (cited) |
| `MultiLOSType` | DWORD · 32-bit | 1 | `[03 §3.1]` | Established (cited) |
| `SkirmishCommanderDeath` | DWORD · 32-bit | 1 | `[08 R-SKIR-01 §1]` | Established (cited) |
| `SkirmishMapping` | DWORD · 32-bit | 1 | `[03 §3.1]`, `[08 R-SKIR-01 §1]` | Established (cited) |
| `SkirmishLineOfSight` | DWORD · 32-bit | 1 | `[03 §3.1]`, `[08 R-SKIR-01 §1]` | Established (cited) |
| `SkirmishLOSType` | DWORD · 32-bit | 1 | `[03 §3.1]`, `[08 R-SKIR-01 §1]` | Established (cited) |
| `SkirmishDifficulty` | DWORD · 32-bit | 1 | `[08 R-SKIR-01 §1]`, `[08 R-SKIR-01 §9]` | Established (cited) |
| `SkirmishLocation` | DWORD · 32-bit | 1 | `[08 R-SKIR-01 §1]`, `[08 R-SKIR-01 §7]` | Established (cited) |
| `SkirmishMap` | string · 256 bytes (incl. NUL) | empty | `[08 R-SKIR-01 §1]` | Established (cited) |

**registry preference (`Total Annihilation\\Skirmish` key)**

| Key | Accessor · stored width | Default | Consumer | Evidence |
|---|---|---|---|---|
| `Player%dController` | DWORD · 32-bit | 0 | `[08 R-SKIR-01 §1]` | Established (cited) |
| `Player%dSide` | DWORD · 32-bit | no default installed | `[08 R-SKIR-01 §1]` | Established (cited) |
| `Player%dColor` | DWORD · 32-bit | no default installed | `[08 R-SKIR-01 §1]` | Established (cited) |
| `Player%dAllyGroup` | DWORD · 32-bit | 5 | `[08 R-SKIR-01 §1]` | Established (cited) |
| `Player%dMetal` | DWORD · 32-bit | 1000 | `[08 R-SKIR-01 §1]` | Established (cited) |
| `Player%dEnergy` | DWORD · 32-bit | 1000 | `[08 R-SKIR-01 §1]` | Established (cited) |

**registry preference (`Total Annihilation` key)**

| Key | Accessor · stored width | Default | Consumer | Evidence |
|---|---|---|---|---|
| `PlayMovie` | DWORD · 32-bit | 1 | `[03 §9]` | Established (cited) |
| `DisplaymodeDepth` | DWORD · 32-bit | no default installed | `[R-KEYS-01 §3]`, `[07 R-CAM-01 §9]` (its only established use: equality with 256 gates the `Games` read) | Established |
| `Games` | DWORD · 32-bit | bit set (1) | `[02 R-KEYS-01 §3]` (sets option-word bit 1 when `DisplaymodeDepth` is 256 and `Games` is 1) | Established |
| `AllMissions` | DWORD · 32-bit | bit clear (0) | `[08 R-CAMP-01 §3]` | Established |

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
| `gaffile` | integer | 0 | low bit installed in the gadget flags; selects external gadget art and bypasses the ordinary button/quickkey builder [07 R-WGT-01 §3] |

**Panel header keys, read on the header gadget:** `totalgadgets` (integer,
default 0, stored as 16-bit), and `panel`, `crdefault`, `escdefault`, and
`defaultfocus` (strings, 16 bytes each, default empty). The header may also
contain a `[VERSION]` subsection with `major`, `minor`, and `revision`
(integers, default 0, stored as bytes); **the subsection is optional in the
parser** — a missing `[VERSION]` is skipped silently and the three bytes stay
zero — even though every one of the 368 retail GUIs authors it.

**Button keys:** `status` (integer, 16-bit), `text` (string), `quickkey`
(string, at most 18 bytes before alphabetic/decimal-prefix conversion; result
stored as a byte [07 R-WGT-01 §11]), `grayedout` (integer, 16-bit), `stages` (integer,
byte).

**Scrollbar keys:** `range`, `knobpos`, `knobsize` (integers, 16-bit),
`thick` (integer, narrowed to signed 16-bit then widened to 32-bit),
`text` (string). Semantics ([07 R-WGT-01 §5]):
`range` is the knob travel in pixels (`knobpos` runs `0..range−1`), which the
engine overwrites for assoc-driven bars and horizontal `SLIDERS`-art bars;
`thick` is the read-out range of the attribute-4 value label.

**List, text-entry, and compound control keys:** `itemheight`, `maxchars`,
`range`, `knobpos`, `knobsize` (integers, 16-bit), `thick` (integer, narrowed
to signed 16-bit then widened to 32-bit),
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

**Map-global keys.** The complete key/consumer table — every key the loader
reads, the section it must sit in, the accessor and width, the default, where
the value is stored and who reads it — is `[R-MAP-01 §3]` (global keys) and
`[R-MAP-01 §5]` (schema keys) below.

**Meteor merge and enable contract — Established.** The selected schema keys
([R-MAP-01 §5]) feed a storm record
committed to the mission globals. An empty `MeteorWeapon` is the only disable
predicate: it disables the shower and loads the defaults. With a nonempty
weapon name the authored parameters are taken, and if any of radius, density,
duration, or interval is zero **all five values are substituted** from the
`gamedata/METEOR.TDF` `[Default]` record before the shower is enabled — zero
parameters substitute rather than disable, and the enable step is reached on
both the authored and substituted paths. A map authoring nonzero parameters
with no weapon key is disabled with its parameters discarded.

Stock content authors `[Default]` as weapon `Meteor`, radius 300, density 2,
duration 5, interval 60. **Established (direct static trace):** a missing file
or missing `[Default]` section returns before changing the incoming record,
without a bogus-data diagnostic. An existing section without `MeteorWeapon`
writes an empty weapon string, emits `Hey, hoser!  The default meteor shower data was bogus!`
(note the double space after `hoser!`), and terminates the process with failure.
The numeric fields have not been read on that fatal branch. A present weapon key, including an empty value, admits the numeric reads:
radius is stored as an integer, and density, duration and interval as single
precision. Any zero among those four numbers emits the same diagnostic after
the writes and terminates the process with failure. Those partially written
values never reach storm installation. These are fatal branches, not a
recoverable partial-default policy. The enable bit is chosen from the original
mission weapon's emptiness, independently of the substituted weapon string.
Installation converts the stored numbers using the working-precision and
signed-64/low-word boundaries in [06 §6.5]. **Unknown:** when the original
weapon name is empty and defaults are absent, the numeric schema reads were
skipped and the incoming numeric values are not established. See [06 §6.5]
for the lifetime-trace decider and explicit checked-host initialization policy.

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
negative degrees, wrapping correctly through 360..65535). `BuildPriority`,
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

#### Mission-file diagnostics

The mission open path owns six exact strings. Five report through the status
pane:

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

#### Terrain file

The terrain loader accepts exactly two version words and
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
cells in row-major order (west to east, north to south).

**Established — plot-cell fields.** The expanded terrain grid holds a
loader-zeroed marker whose nonzero meaning remains open (feature reproduction
requires zero), the authored height, derived maximum and minimum neighbourhood
heights, metal content, a feature reference, unsigned anchor-to-fringe deltas,
and flags. The allocation loop writes the empty-feature sentinel `0xFFFF` and
the metal byte and clears the lowest two flag bits; it initializes **neither**
derived neighbourhood height — not the minimum and not the maximum. The
feature deltas record positive
anchor-to-fringe distances and are subtracted to recover the anchor.

The pass that derives neighbourhood heights takes the minimum and maximum over
the cell's own height byte and its east, south and south-east neighbours, and
clips its extent to `W−1` and `H−1` **exclusive** — so the last column and the
last row never receive a derived pair and those two bytes keep whatever the
plot allocation held, which is a plain heap block and not zero-filled
([04 §6.1]). Nothing observes it: the strip pass voids those edges and every
footprint validator rejects a rectangle reaching them.

Feature sentinels on the 16-bit feature reference are quaternary and tested by a
threshold. Consumers treat any value below `0xFFFB` as a live feature-table
index; higher values are void or fringe:

| Value | Meaning | Consumer test |
|---|---|---|
| `< 0xFFFB` | live feature-table index | `feature < 0xFFFB` — dereference after bounds check |
| `0xFFFE` | fringe member of a multi-cell feature — not a feature itself; resolve via the unsigned anchor deltas to the anchor cell | `feature == 0xFFFE` — follow offsets |
| `0xFFFF` | empty — no feature | `feature == 0xFFFF` — empty |
| `0xFFFD` | void hole — engine-derived map edge or lava-world fill; never dereferenced | `feature == 0xFFFD` |
| `0xFFFC` and `0xFFFB` | further void thresholds; treated like void by the `< 0xFFFB` test and by explicit equality checks | `feature == 0xFFFC` / threshold |

The threshold `0xFFFB` is the canonical value; the legacy path uses `0x00FC`.
The resolver is: read the cell's feature word; if it is below `0xFFFB`,
return it directly; if it is `0xFFFE`, read the Z and X anchor deltas as
**unsigned** values, **subtract** them from the current cell
coordinates (`anchorX = x − DX`, `anchorZ = z − DZ`) to locate the anchor
cell, bounds-check the anchor, and return the anchor's feature word only when
that anchor word is itself below `0xFFFB`; otherwise report not-found.
An unresolved fringe (`0xFFFE` whose offsets do not reach a live anchor) stays
not-found and is treated by the footprint validator as blocking for yard bit 5
and non-satisfying for geothermal bit 7.

**Encoding of the two offset bytes.** The stamp writer stores, at footprint
cell `(i, j)` other than the anchor, `DX = i` and `DZ = j` — the
**positive** anchor→fringe deltas, `0..footX−1` and `0..footZ−1` — and all
three resolvers (the world-position resolver, the cell-address hop, and the
transition/replace path) read them **zero-extended** and **subtract**:
`anchor = fringe − (DZ·width + DX)`. The encoding is an unsigned offset, not
a signed one and not an absolute coordinate; an 8-bit absolute field could not
address the corpus's 402×408-cell maps in any case. The Z delta is multiplied
by the map width when locating the anchor;
the X delta is unscaled. Deltas beyond 255 cannot be stored; no
authored footprint approaches that.

The flag byte is stamped for every cell at load as
`flags = (flags & 0xD7) | 0x50`: preserve bits 0, 1, 2, and 7, clear bits 3 and
5, and set bits 4 and 6 (`0xD7 = 11010111`, `0x50 = 01010000`). Bits 3–6 carry
the placer's nibble as `(placer & 0xF) << 3` with bits 0,1,2,7 preserved; map
load passes placer value 10. Bit 0 marks a live feature instance present at the
anchor, bit 1 marks a building footprint occupied, bit 2 is the never-seen fog
state owned by the visibility phase (presentation only), and bit 7 is preserved
by the mask but has no isolated reader in the bounded census — reported as
`TODO(T23)` platform residual.

There are no hidden map sections or flood-fill state beyond the plot-cell array
and the two anchor deltas. A bounded census over the terrain loader's writers
found only two sites that store the anchor deltas: the expansion zero and the
derived anchor-offset stamp — no writer copies hidden attribute bytes beyond
height and feature, no separate stamping geometry, and no use of the occupancy
fields to propagate anchors. This is a negative bounded result
over the loader's writer set.

Fringe-anchor reconstruction is a derived step, not a loaded field: the TNT
attribute record carries no anchor data.
At load (and at every runtime feature stamp) the placement writer stamps the
feature's footprint rectangle in feature-definition order: the anchor cell
receives the feature index and, for multi-cell features, every other cell of
the rectangle receives the fringe sentinel `0xFFFE` with the **positive
anchor→fringe offsets** in its two offset bytes (Z delta scaled by the map
width when locating the anchor; X delta unscaled). A later
stamp overwrites earlier fringe cells wherever rectangles overlap, so the
effective anchor for a fringe cell is the last stamp that covered it — there
is no left/above propagation. Fringe cells whose `0xFFFE` no longer points at
a live anchor (overwritten by another feature's anchor, or the map-authoring
case of raw `0xFFFE` with no covering footprint) resolve to not-found; the
writer contract resolves every footprint-covered fringe cell by construction
(see `research/retail-executable-spec/03` [R-P0-04] for the
resolver's shared use). An unresolved fringe stays not-found per the resolver
above.

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
* **South-edge void:** walking rows upward from `Height-1`, the predicate
  `z*16 − (height(x, z) >> 1) > PlayBottom` — equivalently
  `(Height-1-z)*16 + (height >> 1) < 112` — is evaluated on row `z`, and
  when it holds the cell voided is **`(x, z−1)`, the row above the tested
  row**, provided that cell is empty or fringe; the walk stops at the first
  row where the predicate fails. The bottom row `Height-1` is therefore
  never voided by this rule; row `Height-2` is voided when the bottom row's
  height is below 224, `Height-3` when row `Height-2`'s height is below 192,
  … down to row `Height-8`, voided when row `Height-7`'s height is below 32.
  The traced statement is `[03 R-TERR-01 §2]`.

* **Lava-world flood:** when the mission `lavaworld` flag is set, a bulk sweep
  sets `0xFFFD` for every cell where `hmin ≤ SeaLevel` and the feature word is
  `0xFFFF` or `0xFFFE`, turning the entire low basin into void.

All four edge rules test the **raw** height byte (the
north/south predicates) or the derived `hmin` (lava), and only cells whose
feature word is empty or fringe are converted — placed features and anchors
are never voided.

Outside the map rectangle, height returns the sentinel `-1` with unsigned
candidate bounds before any terrain read; the movement validator returns
blocked for generic modes but returns pass for factory-exit search mode 2; the
LOS writer stores an empty footprint and returns; projectiles report no terrain
collision; and the camera is clamped to the `PlayRight`/`PlayBottom` insets
above.

Per-cell metal is uniform on canonical maps: the loader writes the signed
byte of the mission `SurfaceMetal` scalar to the metal field of **every** plot cell
at load, gated on the authored value being non-negative and the terrain being
the canonical version (negative or legacy → seed 0). No per-cell metal raster
is allocated — a negative bounded census found no `Width × Height`
metal allocation beyond the plot-cell array — and the canonical TNT unknown byte
at attribute `+3` is uniformly zero and not carried. **The legacy (0x1020)
path differs: the per-cell seed comes from the legacy attribute record
itself** (byte 6 of the 8-byte record, copied to every cell's metal field), so
legacy maps carry per-cell metal without any separate file — the "varying
per-cell metal source file" question is answered: there is no such file; the
varying source is the legacy attribute byte. An extractor samples once at
placement by summing `unsigned(metalByte) + 1` over its footprint cells and
multiplying by the definition's `extractsmetal` scalar; the result is stored on
the unit and never resampled. Feature-definition metal is reclaim
reward only and does not enter the extractor sum.

**Legacy attribute record (established).** The legacy terrain attribute array
is `Width × Height × 8` bytes with an 8-byte stride: byte 0 is the height
(copied to the plot-cell height), byte 2 is a one-byte feature reference
(values below `0xFC` are feature-table indices; `0xFC` and above are
void/none — a single sentinel band rather than the canonical quaternary),
byte 6 is the per-cell metal seed (copied to the plot-cell metal field), and bytes
1, 3, 4, 5, 7 are never read.

### The map-load pipeline: entry points, order, and the resource-path slots [R-MAP-01 §1]

**Established** (direct static trace of the mission loader, its four
front-end entry points, the battle-entry orchestrator and the session-start
sequence). This section owns the *load pipeline* and the key/consumer tables;
what the loaded cells mean to the simulation is `[03 R-TERR-01 §1–§8]`,
feature stamping is `[05 R-FEAT-01 §2/§3/§7]`, mission objects and triggers
are `[08 R-TRIG-01]`, the skirmish setup record is `[08 R-SKIR-01]`.

**Two files, two moments.** A map is loaded in two separate steps that can be
minutes apart:

1. **OTA parse** — runs in the front end, when a map or mission is selected
   (skirmish map pick, campaign mission entry, load-game), again on every
   save-summary write, and once more at battle entry immediately before
   session start. It fills the *mission record* (the map-global block, the
   eight resource paths, the placement arrays, the trigger set) and touches no
   terrain memory.
2. **TNT load** — runs inside session start, before the feature-successor
   pass `[05 R-FEAT-01 §2]` and after the per-battle catalogs are up. It reads the terrain file named
   by resource slot 1, builds the plot array, stamps features, and allocates
   every map-sized presentation buffer.

The OTA parse's success value is **ignored** by battle entry: a map whose OTA
parsed without a `GlobalHeader` still starts, with the mission record's
prologue sentinels in force (§6 below).

**Session kinds.** The mission record's kind word (`1` campaign, `2`
skirmish, `3` multiplayer — the same word `[08 R-SKIR-01 §2]` calls the
session kind) selects the parse path. Campaign entries locate the OTA through
the campaign file: `MISSION<n>` section → `missionfile` key → `Maps\<file>.OTA`.
Skirmish and multiplayer entries receive the map name directly and open
`Maps\<name>.OTA`. The "next mission" entry counts the campaign file's
`MISSION<n>` sections from 0 upward until one is missing and loads index
`current + 1`; the campaign-open entry blanks all eight resource slots and
reports a missing campaign file as `The requested campaign file, %s, does
not exist.`

**Prologue — what every parse resets first.** Before any file is opened the
loader destroys and recreates the trigger set (`[08 R-TRIG-01 §2]`), sets the
map-global integers `SurfaceMetal`, `minwindspeed`, `maxwindspeed` and
`gravity` to **−1**, `tidalstrength` to **−1.0**, `lavaworld` and
`nosealeveltrigger` to 0, blanks `Planet` and the description, and frees the
three placement arrays and the briefing text. These sentinels are what the
terrain loader sees when the parse never reaches a `GlobalHeader` (§6).

**Resource-path slots.** The mission record carries eight 256-byte path
strings filled by one helper from `(directory, name, extension)`:

| Slot | Key (level) | Directory | Extension | Reader |
|---|---|---|---|---|
| 1 | campaign `missionfile` / skirmish map name | `Maps` | `TNT` | the terrain loader (§6); the map-hash builder (§3 row 0); two further readers outside this unit (a front-end preflight and the AI profile loader) |
| 2 | `brief` (global, language-prefixed) | `camps\briefs` | `TXT` | read whole into the briefing text buffer at parse time; the briefing screen |
| 3 | `narration` (global, language-prefixed) | `camps\briefs` | `WAV` | briefing screen narration |
| 4 | `missionhint` (global, language-prefixed) | `camps\hints` | `TXT` | **none** — the path is built and never read (reader census over the slot accessor and the record offset: no site) |
| 5 | `glamour` (global) | *empty* | `PCX` | the glamour (mission-complete) picture loader, which skips the slot's leading `\` and rebuilds `bitmaps\glamour\<name>.PCX` through the same path helper |
| 6 | `UseOnlyUnits` (global) | `camps\useonly` | `TDF` | the unit-restriction loader |
| 7 | `aiprofile` (schema) | `ai` | `txt` | the AI profile loader (four sites) |
| 8 | `glamoursound` (global) | `camps\briefs` | `WAV` | briefing screen |

The helper: an empty name stores an empty slot (and, for slot 1, a TNT size
of 0). Otherwise, when a language directory is configured (`[02 §3]`), it
formats `<directory>-<language>\<name>`, cuts the name at its **last** `.`
(so an authored extension is discarded), appends `.<extension>` and probes
the VFS; when the probe fails or no language is configured it stores
`<directory>\<name>` + `.<extension>` **without** probing. Slot 5's empty
directory therefore yields `\<glamour>.PCX`. Slot 1 additionally records the
TNT's file size (0 when the file is absent). The slot accessor returns "no
path" for an empty slot.

### Opening the OTA: paths, the alias retry, and every diagnostic [R-MAP-01 §2]

**Established** (strings verbatim from the image; channels traced).

**Path.** `Maps\<name>.OTA` is built by the same directory/name/extension
helper as the slots (language-directory variant first, plain second), so a
localized `Maps-<language>\` copy of an OTA overrides the stock one.

**Campaign path**, in order: `MISSION<n>` section absent from the campaign
file → `The requested mission file, %s, does not exist.` (`%s` =
`MISSION<n>`); `missionname` is read from that section with the
language-prefixed accessor into the mission's display name; `missionfile`
absent → `Old TED format no longer supported!`; the OTA fails to open or
parse → `Hey, joker!  There is no mission defintion for this mission: %s`
(`%s` = the `missionfile` value; two spaces, "defintion" verbatim); parsed but
no `GlobalHeader` → `Hey, joker!  Mission file %s is corrupt (no header
found).`; otherwise `maxunits` is read from `GlobalHeader` (integer, default
**200**) into the unit-limit global and slot 1 is set to `Maps\<file>.TNT`.
All four messages go to the **status pane** (the same in-game text channel
as the six strings listed under "Mission-file diagnostics" above); none is
fatal, and each returns failure to the caller.

**Skirmish/multiplayer path.** The name is copied into the display name;
`Maps\<name>.OTA` is opened; on failure the name is run through the
translation table **backwards** (find the entry whose translated value is the
name, use the untranslated key — the "alias probe") and opened once more; a
second failure returns silently with no message. On success slot 1 is set to
`Maps\<name>.TNT` **before** the header check, so a header-less OTA still
names a terrain file; on an open failure slot 1 is left untouched and keeps
whatever the previous parse stored (only the campaign-open entry blanks the
slots). No `GlobalHeader` → `No GlobalHeader block in mission file!` (status
pane, failure).

**Common tail** (both paths, `GlobalHeader` current): the global keys of §3
are read in the order given there, the trigger set is built, and the schema
is selected (§4). No acceptable schema → `No suitable schema type in mission
file!` in a modal **Error** box (topmost, not fatal) and failure. With a
schema, the schema keys of §5 are read, the mission objects are compiled
(`[08 R-TRIG-01 §9]`) and the parse succeeds.

**Three channels, for reference.** (a) the status pane; (b) a non-fatal
modal box titled `Error`; (c) the fatal box — the message under the
application title, then process exit with code 1; when the fatal channel is
handed a null string it shows nothing and exits. The terrain loader's
failures (§6) use (c) only.

### `[GlobalHeader]` keys: level, accessor, default, store, consumer [R-MAP-01 §3]

**Established** (loader trace for every read; reader census over the
decompiled corpus for every stored value; asset census over the 275 retail
OTAs for the "authored" column).

**Scoping rule (established, and the reason the level column matters).** The
typed accessors of §4 "Typed accessors" search **only the current section's
own key vector**; there is no walk to the parent section or into children.
The loader reads global keys with `GlobalHeader` current and schema keys with
the selected `[Schema N]` current, so a key authored at the other level is
simply absent and takes its default.

**Read order and table.** The keys are read in exactly this order; nothing
between them depends on an earlier key except as noted.

| # | Key | Accessor, width | Default | Stored / consumer | Authored (275 OTAs) |
|---|---|---|---|---|---|
| 0 | — | section digest | — | the `GlobalHeader` section's body checksum (the four-accumulator primitive of §6 "Content checksum" over the text between its braces, computed at parse time) is copied into the mission record and is the OTA half of the map identity hash; the TNT half is the same primitive over the 64-byte header, the raw attribute array and the raw feature-name table, XORed together and memoised per terrain path; the two halves XOR into the hash `[08 "Map/resource identity"]` consumes | — |
| 1 | `brief` | language-prefixed string, 256 | empty | slot 2; the file is then read **whole** into a heap buffer tagged `Briefing` (size + 1, NUL-terminated); absent file → no buffer | 275 |
| 2 | `narration` | language-prefixed string, 256 | empty | slot 3 | 275 |
| 3 | `missionhint` | language-prefixed string, 256 | empty | slot 4 — **inert** (no reader) | 275 |
| 4 | `glamour` | string, 256 | empty | slot 5 | 275 |
| 5 | `glamoursound` | string, 256 | empty | slot 8 | 106 |
| 6 | `UseOnlyUnits` | string, 256 | empty | slot 6 | 275 |
| 7 | `mapping` | integer | 0 | mapping global `[08 R-SKIR-01 §4]` (skirmish/multiplayer overwrite it from the setup record at battle entry) | 275 |
| 8 | `lineofsight` | integer | 0 | line-of-sight global `[08 R-SKIR-01 §4]` (same overwrite) | 275 |
| — | (two globals) | — | — | the loader then stores constants 1 and 0 into two neighbouring session globals; their readers are outside this unit — recorded, not named | — |
| 9 | `memory` | string, 128 | empty | mission record — **inert** (no reader) | 275 |
| 10 | `numplayers` | string, 128 | empty | mission record — **inert** (no reader; the lobby's player count comes from the setup record and the `[specials]` census of §4) | 275 |
| 11 | `Planet` | string, 128 | empty | mission record; read (through its pointer accessor) by the briefing-screen globe, which matches it case-insensitively against its planet vocabulary (`Green planet`, `Archipelago`, `Wet Desert`, `Desert`, `Red Planet`, `Lunar`, …) to pick the globe art; an unmatched value selects nothing (doc 07 owns the screen) | 275 |
| 12 | `nomovie` | integer | 0 | end-of-campaign movie gate (the mission-end flow skips the cinematic when non-zero) | 12 |
| 13 | `missiondescription` | string, 128 | `No description available` | lower-cased, then passed through the translation table (`[02 §3]`); if the lookup returns the lower-cased text unchanged the **original spelling** is kept, else the translation; read through its pointer accessor by one front-end screen (doc 07) | 275 |
| 14 | `minwindspeed` | integer | **0** | map-global block, §6 | 275 |
| 15 | `maxwindspeed` | integer | **0** | §6 | 275 |
| 16 | `gravity` | integer | **0** | §6 | 275 |
| 17 | `tidalstrength` | floating (single) | **0.0** | §6 | 275 |
| 18 | `lavaworld` | integer | 0 | lava flood `[03 R-TERR-01 §2]`, respawn reject `[08 R-SKIR-01 §3]`, debris/water tests | 274 |
| 19 | `nosealeveltrigger` | integer | 0 | the "opaque liquid mode" flag of `[06 §8.2]` (submerged-impact early return, and the projectile/feature-hit and cruise-waypoint water tests beside it) and the fragment/debris water tests of `[04 R-COB-04 §2]`; unrelated to the sea level itself | 118 |
| 20 | `waterdoesdamage` | integer | 0 | water damage gate `[04 §9.2]` (both flag and amount must be non-zero) | 183 |
| 21 | `waterdamage` | integer | 0 | water damage amount, same site | 183 |
| — | trigger keys | see `[08 R-TRIG-01 §2]` | — | the trigger builder runs here, between `waterdamage` and `killmul` | — |
| 22 | `killmul` | floating (single) | 0.0 | mission record; read by the end-of-battle score helper `[08 R-CAMP-01 §7]` | 275 |
| 23 | `timemul` | floating (single) | 0.0 | mission record; read by the end-of-battle score helper `[08 R-CAMP-01 §7]` | 275 |
| — | `maxunits` | integer | 200 | read **before** this table on the campaign path only (§2); unit-limit global, readers in doc 05 | 184 |
| — | `missionname`, `size`, `SCHEMACOUNT`, `solarstrength` | — | — | **inert**: `missionname` has no OTA reader (both readers read the campaign file's `MISSION<n>` section); the other three have no string in the image | 275 each |

`maxunits` at global level is authored by 184 maps; the 91 that omit it get
200 on the campaign path. Every retail OTA authors `minwindspeed`,
`maxwindspeed`, `gravity` and `tidalstrength`; retail values include
authored zeros for both wind keys and for tidal strength, and `timemul=-1`
on three campaign maps (inert).

### Schema selection, exactly [R-MAP-01 §4]

**Established** (direct static trace of the selector and its two
callers).

**Vocabulary.** Seven type strings, compared case-insensitively against the
schema's `Type` key (string accessor, 32 bytes): `Easy`, `Medium`, `Hard`,
`Network 1`, `Network 2`, `Network 3`, `Network 4`. Schemas are found by
formatting `Schema %i` for `i = 0, 1, 2, …` under `GlobalHeader` with the
first-match section finder and stopping at the first miss — `SCHEMACOUNT` is
never read, and a gap ends the probe (no retail OTA has a gap; retail counts
are 1 (95 maps), 2 (2), 3 (176), 4 (2)).

**Preference order.** Campaign (kind 1): the session difficulty word
(`[08 R-SKIR-01 §1]`) selects the order — `0` → `Easy, Medium, Hard`; `1` →
`Medium, Easy, Hard`; `2` → `Hard, Medium, Easy`; any other value accepts
nothing. Skirmish and multiplayer (kinds 2/3): `Network 1, Network 2,
Network 3, Network 4`. For each type in that order the schema probe restarts
at `Schema 0`.

**Campaign acceptance.** The first schema (in type-preference, then index
order) whose `Type` matches is accepted immediately and becomes the current
section; the search does not continue.

**Skirmish/multiplayer acceptance.** The wanted player count `want` is,
for skirmish, one more than the index of the highest setup-record row whose
controller word is non-zero (`[08 R-SKIR-01 §1]`), and for multiplayer one
more than the highest lobby slot that is neither empty nor the `-1`
sentinel; when no row qualifies it is the lobby player-count global. For
every matching-type schema the loader counts `n` = the number of
`[specials]` children whose `specialwhat` begins with `StartPos`
(case-insensitive, eight characters — the same test as the placement
compiler, `[08 R-TRIG-01 §9]`). The schema becomes the candidate when

```
n != 0 && (n == want || want == 0 || (best < n && best != want))
```

with `best` the candidate's count so far (0 initially). The search runs
through **all four** network types and every schema; so a later exact match
replaces an earlier one (last exact match wins), an exact match is never
displaced by a larger count, and without an exact match the largest count
wins with the earliest on ties. The candidate's section becomes current
when the search ends. A schema with a matching type but **no** `StartPos`
specials is never chosen. When the caller passes no output name (the map
browser, §9) the first matching-type schema is accepted without the
`StartPos` census.

**Failure.** No candidate → `No suitable schema type in mission file!` (§2).
A `GlobalHeader` that disappears between the caller's check and the
selector's own re-find raises the fatal `Very bad news!  No MSG!`; it is
unreachable from the loader, which checks first.

The selected schema's section name (`Schema <i>`) is handed to the mission
object compiler, which re-finds `globalheader` and that schema by name.

### `[Schema N]` keys and the schema-level reads [R-MAP-01 §5]

**Established**. Read with the chosen schema current, in this order:

| # | Key | Accessor, width | Default | Stored / consumer | Authored (635 schemas) |
|---|---|---|---|---|---|
| 1 | `Type` | string, 32 | — | §4 only | 635 |
| 2 | `HumanMetal` | integer → single float | 0 | starting resources `[08 R-SKIR-01 §5]` | 635 |
| 3 | `HumanEnergy` | integer → single float | 0 | same | 635 |
| 4 | `ComputerMetal` | integer → single float | 0 | same | 635 |
| 5 | `ComputerEnergy` | integer → single float | 0 | same | 635 |
| 6 | `SurfaceMetal` | integer | 0 | metal seed, §6 / `[03 R-TERR-01 §1]` (stored as a signed byte per cell: retail authors 244–255, which seed −12…−1 and read back as 244–255 unsigned in the extractor sum) | 635 |
| 7 | `aiprofile` | string, 256 | empty | slot 7 as `ai\<name>.txt`; when the resulting slot is **empty** (key absent or empty) the helper is called again with the literal name `default`, so the profile is `ai\default.txt` | 635 (27 empty) |
| 8 | `MeteorWeapon` | string, 32 | empty | "Meteor merge and enable contract" above (unchanged) | 635 |
| 9–12 | `MeteorRadius` (integer), `MeteorDensity`, `MeteorDuration`, `MeteorInterval` (floating) | | 0 / 0.0 | same | 635 |
| — | `MohoMetal` | — | — | **inert** (no string in the image) | 635 |
| — | `[units]`, `[features]`, `[specials]` | | | `[08 R-TRIG-01 §9]` and "Placed-object keys" above | |

The `Human*`/`Computer*` values are read as integers and widened to single
floats at the store — the fractional part of an authored value is lost
before it is ever a float.

### Terrain load: open, version gate, the map-global block, and the fatal set [R-MAP-01 §6]

**Established** (direct static trace). Cell semantics are cited, not
restated.

**Open and read.** Session start asks the mission record for slot 1. The
file is opened through the VFS; a miss is **fatal** — the fatal box shows the
*path string itself* as its message (there is no formatted text), then the
process exits. An empty slot 1 (no OTA ever named a terrain file in this
session) hands the fatal channel a null path: no box, immediate exit. The
file is read whole into one heap block tagged with its own path, in ten
reads of `size/10` bytes with the loading-progress byte advanced 9, 18, …,
90 after each, then one read of the remainder. Every section pointer in the
header is then biased by the block's address `[fmt tnt]`.

**Version gate.** Header slot 0 must be `0x1020` (legacy) or `0x2000`
(canonical); anything else is fatal with `Unknown TNT version:  0x%08x` (two
spaces; the value in hexadecimal). The slot maps of both versions are
`[03 R-TERR-01 §1]` and `[fmt tnt]`.

**Map-global block — the defaults as they really are.** The block is written
in this order, before any plot memory exists. "Canonical" in the table is a
**signed `≥ 0x2000`** test on the header's version slot, not an equality test;
because the version gate above has already rejected every value but `0x1020`
and `0x2000`, the two are equivalent here.

| Value | Rule | What a retail OTA that *omits* the key gets |
|---|---|---|
| minimum wind | OTA `minwindspeed` when `≥ 0` **and** canonical; else the legacy header slot, which on a canonical map is the constant 100 | **0** — the integer accessor's default is 0, which passes `≥ 0` |
| maximum wind | as above with `maxwindspeed` / 2000 | **0** |
| gravity | OTA `gravity` when `≥ 0` and canonical: `ftol((double)g × 65536.0 × (1/900))` (constants and truncation as `[03 R-TERR-01 §6]`); else legacy slot 13 when non-zero, converted the same way; else the compiled `0x1FDB` | **0** (converted 0), not `0x1FDB` |
| tidal strength | mission `tidalstrength` unless `< 0.0` (strict), else `0.5` | **0.0** |
| sea level | header slot 9, low byte | — |
| surface metal seed | schema `SurfaceMetal` when `≥ 0` and canonical, else 0 | 0 either way |

So the "fallback" branches (100 / 2000 / `0x1FDB` / 0.5) are reached in
exactly three cases: a legacy terrain file, an authored **negative** value,
or a session whose OTA parse never reached a `GlobalHeader` (the prologue
sentinels of §1 are −1 / −1.0). An omitted key is not one of them. Every
retail OTA authors all four keys, so no shipped map exercises the
omitted-key case.

**Per-cell work, in order** (all cited): minimap picture (§7); tile map
copy (§7); plot allocation and per-cell initialization `[03 R-TERR-01 §1]`;
feature-name table compilation `[05 R-FEAT-01 §2]` (§8 for the miss
policy); the attribute passes and the void/feature stamps `[03 R-TERR-01
§1]`; the mission-file feature pass `[05 R-FEAT-01 §3]` (§8); tile-set copy
(§7); LOS tables (§7); camera counts and sort grid (§7); the full min/max
recompute `[03 R-TERR-01 §3]`; the air sector grid and LOS height words
`[03 R-TERR-01 §5]`, `[03 R-P0-18-B]`; the edge/lava void sweep `[03
R-TERR-01 §2]`; the mapping array (zeroed, `Width × Height / 2` bytes) `[03
R-TERR-01 §7]`; the render sort lists (§7); metal-deposit seeding `[05
R-FEAT-01 §7]`; the eyeball buffer; the feature reproduction cursor reset;
progress byte 100.

### Which stages are presentation only [R-MAP-01 §7]

**Established**. These allocations happen inside the terrain loader
but nothing in the simulation reads them; a headless Nanolathe may skip
them without changing a tick:

* **Minimap picture.** When the header's minimap-present bit is set, a
  surface of the embedded width × height (plus a 24-byte surface header) is
  allocated under the tag `TED GENERATED PIC` and the embedded pixels are
  blitted into it at (0, 0); when the bit is clear the picture pointer is
  null and the radar picture is generated from the tile set instead
  `[03 §3.7]`. The surface lifetime is `[03 §3.6]`.
* **Tile map and tile set.** The tile map (`(Width·16/32) × (Height·16/32)`
  16-bit indices, tag `TILE MAP`) and the tile set (`Tiles × 1024` pixel
  bytes behind an 8-byte header of count and pixel pointer, tag `TILE SET`)
  are copied out of the file verbatim and never written again `[03 R-TERR-01
  §3]`; their only readers are the terrain tile blitter and the generated
  minimap `[03 §2.2]`, `[03 §3.7]`.
* **LOS sight tables.** Between the tile-set copy and the camera setup the
  loader (re)loads `gamedata\LOS.TDF`: `[TABLEINFO] numtables`, then
  `[TABLE<n>] numlines` and its `line<n>` rows. This is the "numbered
  text-table resource reader" the string triage could not place; its
  consumer is the sight-shape builder `[03 §3.2 R-VIS-01 §3]`.
* **Camera counts and render sort grid.** The view size in pixels is
  divided by 16 and by 32 (toward zero) into view-cell and view-tile
  counts; a 16-byte sort-grid header records columns = view cells X / 2 + 2
  (+1 when the view width is not a multiple of 32) and rows = view cells
  Z / 2 + 2 (+1 likewise); `SORT UNIT LIST` (`(viewCellsZ+32) ·
  (viewCellsX+12) · 4` bytes), `SORT INDICES` (`(viewCellsZ+32) · 4`) and
  `SORT LINE COUNT` (`(viewCellsZ+32) · 2`) are allocated for the frame
  composer; the fog-cache-valid bit is cleared
  so the fog overlay rebuilds `[03 §3.3]`. `EYEBALL MEMORY` (720 bytes) is
  the LOS work buffer.
* **Loading-progress byte** — written by the TNT reader (9…90) and set to
  100 at the end; the progress bar reads it.

### Feature-name resolution and the miss policy [R-MAP-01 §8]

**Established**.

* **TNT name table.** Every record of the terrain file's feature-name table
  is compiled, in table order, by the feature parser as the catalog is built
  `[05 R-FEAT-01 §2]`; the cell feature words index that table directly. A
  name that no mounted feature TDF defines is **fatal**: the parser raises
  `Record "%s" missing from feature files` through the fatal channel (box,
  then exit). There is no skip.
* **OTA `[features]`.** Each record with a non-blank name (blanking rules:
  `[08 R-TRIG-01 §9]`) is resolved by a linear case-insensitive scan of the
  catalog in ordinal order; a miss calls the parser on demand, which appends
  the definition or dies with the same fatal string. The anchor cell is the
  authored pixel position converted to a cell for sprite features, and for
  3-D features the pixel position minus half the footprint (integer division
  by 2 of `footprintx`/`footprintz`) — then stamped through the shared
  service with placer nibble 10 `[05 R-FEAT-01 §3]`. The mission pass runs
  only on canonical terrain and not when a save is being restored
  `[03 R-TERR-01 §1]`.
* Matching is case-insensitive at both sites; the name is the TDF section
  name.

### The map browser: enumeration and acceptance [R-MAP-01 §9]

**Established** (skirmish/multiplayer map list builder).

The list is built once per front-end session and cached (tag `MULTI MAPS`).
The VFS is enumerated for `Maps\*.ota` (union enumeration order, `[02 §2]`;
`.` and `..` skipped). For each entry `Maps\<name>.OTA` is parsed; the map
is **accepted** when the selector (§4) run as multiplayer with no output name
finds any schema whose `Type` is `Network 1`…`Network 4` — a `GlobalHeader`
is required, a `StartPos` census is not. The display name is the file name
with its extension cut at the last `.`, lower-cased and passed through the
translation table; if the lookup returns the lower-cased name unchanged the
**original file name** (original case) is shown, otherwise the translation.
Names are stored NUL-separated in enumeration order; the skirmish fallback
(no remembered map) picks the first entry. In a multiplayer session the
network drain runs once per enumerated file (out of scope).

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
it is the transparent palette index of the ordinary raw keyed path — that
blitter skips every source pixel equal to it), a compression flag byte (0 = raw pixels,
1 = per-row RLE), a 16-bit subframe-count field of which **only the low
byte is read** (`[R-MALF-01 §6]`; the high byte selects the table-remapped
composition path for a subframe), a 4-byte zero field, a 32-bit data
offset, and a trailing reserved word. The data offset is always biased by the
file base. `[fmt gaf]` carries the byte-level layout.

**Raw frame payload.** When the compression flag is clear, the data offset
points directly at `width × height` bytes, row-major, one palette index per
pixel. Raw frames carry no skip runs. Ordinary keyed rendering uses the
frame's own color key, so palette index 0 is opaque on that path. Special
consumers need their own transparency contract [R-MALF-01 §6]. 6,068 of the
48,519 retail frames are raw.

**Compressed frame payload.** When the compression flag is set, the data
offset points at a row table: one 16-bit stored byte count per row followed by
that row's command stream. Each command is a **byte** read as:

* low bit set — skip `(command >> 1)` pixels, leaving destination untouched
  (this skip is the only transparency mechanism on the RLE path; palette
  index 0 is opaque black);
* otherwise bit 1 set — repeat the following byte `(command >> 2) + 1` times;
* otherwise — copy `(command >> 2) + 1` literal bytes.

Commands are bytes; a word-width decoder mis-decodes every RLE row.

Rows decode left to right until the row width is covered, then the next row's
count and stream follow. A zero width or height still allocates its header but
clips to nothing and draws nothing; out-of-bounds output is discarded rather
than wrapped.

**Subframes.** When the subframe count is nonzero, the data offset points at an
array of that many 32-bit subframe-header offsets. Each is biased by the file
base, and each subframe's own data offset is biased in turn — one level of
nesting only. A subframe is an ordinary frame header, so a subframe may itself
be compressed. **Established:** the general frame blitter draws children in
file order with the unchanged pen position. Each leaf subtracts its own
placement offset and clips against the destination surface; the composite
parent's dimensions do not impose an additional clip. Plain leaves overwrite
opaque pixels; alternate leaves blend with the existing destination through
ALP. `[03 R-COMP-01 §2]` owns this drawing contract. Recursive drawing exists,
but the loader does not recursively relocate nested child tables. **Unknown:**
which nested layouts, if any, are usable through this ordinary loader; a
recursive host representation alone does not establish retail acceptance.

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

**Established — entry-name comparison.** Lookup scans entry order and returns
the first matching name. The inspected executable uses the default comparison
mode: ASCII uppercase bytes fold to lowercase, and all other bytes remain
unchanged. The locale selector is zero-initialized and has no writer in the
bounded reference census; Unicode folding is not part of this contract.

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

**Load-time primitive reordering — Established (direct static trace).** After
relocation, and before the model is ever drawn, each object is compiled in
place:

1. The all-ones selection word is the sole no-selection sentinel. When the
   selection word is any other value and the signed primitive count is
   positive, the selected primitive is exchanged with primitive zero and
   the selection word is rewritten to zero. Thus authored selection index zero
   is a real selection primitive, and a positive authored index moves that
   primitive to zero while moving authored primitive zero to the selected
   index.
2. Primitive zero is excluded from ordering whether or not the object declares
   a selection primitive. For three or more primitives, repeated adjacent
   passes cover indexes one through the last index. A pair is exchanged only
   when the right key is strictly less than the left key. The result is stable
   ascending order: equal keys retain their order after the selection exchange.
   Objects with zero, one, or two primitives perform no ordering comparisons.
3. A primitive's key is the signed integer mean of its indexed vertices'
   second coordinates. Each unsigned 16-bit vertex index selects one signed
   32-bit coordinate. The sum is a signed 32-bit accumulator whose additions
   wrap modulo 2^32; signed 32-bit division by the signed vertex-index count
   then truncates toward zero. The signed 32-bit quotient is compared directly,
   with no wider intermediate and no subsequent narrowing.

Two boundary examples distinguish this arithmetic. Coordinates
`[2147483647, 1]` wrap their sum to `-2147483648` and produce the key
`-1073741824`; a widened sum would instead produce `1073741824` and can reverse
the face's order against a zero-key face. Coordinates `[-3, 0]` produce `-1`,
not the floor quotient `-2`. Equal quotients do not exchange records.

The pass has no malformed-data recovery. A compared primitive with a zero
vertex-index count reaches signed division by zero. An out-of-range vertex
index reads beyond the object's declared vertex array. Any selection value
other than all-ones is used as a primitive index when the primitive count is
positive, without an index bound. These cases can fault or consume unrelated
loaded bytes and define no portable fallback; a checked host decoder rejects
them `[02 R-MALF-01 §8]`.

Draw order within a piece is therefore fixed once at load, not recomputed per
frame. The renderer skips primitive zero only when the object declares a
selection primitive; with the all-ones sentinel, primitive zero remains the
unsorted first drawn face. Draw consumption is `[03 §2.4]` and `[03 §2.4.1]`.

#### Mirroring

A separate recursive pass negates the first and third
coordinates of every vertex and the first and third parent translations of
every object in a hierarchy, which is a half-turn about the vertical axis
applied to a whole model.

Model references are resolved from unit and feature model names. The
primitive's colour, texture-name, and flag interpretation at draw time belongs
to document 03.

### The unit model's height word and the catalog-time texture bind [R-CAT-01 §7]

**Established fact — height word.** After loading, relocation and mirroring,
the compiler measures the upper Y bound with a recursive sibling-chain walk.
Each invocation starts a signed 32-bit maximum at zero. For each piece,
compare every vertex Y plus that piece's own Y translation with the maximum.
For a child chain, recurse with a fresh zero maximum, add the current piece's
Y translation to the returned child maximum, and compare that result too.
Continue with the next sibling. Additions wrap to 32 bits; comparisons are
signed. This differs from a global maximum of accumulated vertex positions:
a zero child result still contributes a positive parent translation.
The value is stored as the definition's **upper Y bound**; the lower Y bound is zeroed just before,
so the definition's Y extent (upper − lower) is the same number. The X and Z
bounds of the same bounding record come from the footprint, not the model:
`±(FootprintX << 20) / 2` and `±(FootprintZ << 20) / 2` in 16.16, with the
extents `maxX − minX`, `maxZ − minZ` and a "radius" word
`(extentX + extentZ) / 3` (integer division) written by the unit-record
compiler. Consumers (selection box, picking, the composition image key) are
documents 03 and 07 and are not enumerated here.

**Established fact — there is no min-Y walk, and who reads the height word.**
The height walk above is the **only** bound retail derives from model
geometry. The minimum-Y word is written exactly once — the zero store
immediately before the walk — and no second walk with an inverted comparison
exists: the height helper has a single caller (this compiler), and the rest of
the bounding record is footprint-derived. Every unit definition in the corpus
therefore has minimum-Y zero, and any contract that appears to want a model
bottom is either reading that zero or reading the maximum-Y word instead. [07 R-REV-01 §7] traces the same pass independently from
the hover-reduction side and agrees.

The maximum-Y dword's **high half** is the height in whole world units, and it
is the word the behavioral consumers share — at two different read widths:

| Consumer | Read width | Citation |
|---|---|---|
| LOS observer emitter height addend | **byte** — a model above 255 world units wraps | `[03 R-P0-18-A §1]` |
| Weapon above/below-water gates | signed 16-bit | `[06 R-WPN-05 §1]` |
| Transport lowering and hang altitude offset | signed 16-bit | `[04 R-AIR-01 §9]` |
| `setSFXoccupy` band-3 (fully submerged) test | signed 16-bit | `[04 R-MOV-01 §8a]`, `[04 R-MOV-01 §8b]` |

A reimplementation may keep the byte-masked and full forms as separate fields,
but must not feed the byte-masked form to the 16-bit readers.

**Established fact — texture bind.** Immediately after the height, every
primitive of the model that carries a texture name is bound as
`[03 §2.4]` item 4 states; one precision worth recording here: the
**ten-frame team-texture test is applied only to entries found in the
fallback (common) texture set**, never to entries found in the side texture
sets, and the per-instance animation player list receives every multi-frame
binding except those ten-frame entries.

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

**Established:** loading computes the original file's content checksum and
stores it alongside the loaded file in the script cache. This does not
overwrite an authored COB header word. Save/load validation compares that
cached content signature.

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

An override path accompanies that hash: for each unit
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

**Established.** The retail bitmap font format is not Windows FNT. Its
header is four single bytes: unsigned height, an ignored byte, signed baseline
vertical offset, and the first character code. The absolute glyph-offset table
has one entry for every code from that first code through 255 and is indexed
by `code - firstCode` for both measurement and drawing. This table selection
is applied once; it is not a second adjustment to an already selected glyph
[fmt fnt][03 R-FONT-01 §1].

Offset 0 marks an absent code, skipped in both measurement and drawing.
Stock space (code 32) has a nonzero offset, advance width 7, and an all-zero
bitmap, so it advances without drawing. Each present glyph stores an advance
width followed by a continuous, most-significant-bit-first bitmap. Glyph rows
start at `penY - signedBaseline`. The first-code byte is zero in the stock
fonts; a nonzero value shortens the offset table without changing that
baseline arithmetic.

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
`ymax-ymin+1`) over a 128-byte header. A byte whose two high bits are set introduces a run whose count is its
low six bits; other bytes are literal pixels. Rows consume the visible
width, ignoring `bytes_per_line`; every run is clamped to the remaining row width
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

RIFF parsing walks `fmt ` and `data` chunks with a size-plus-eight stride
and **no odd-size padding**, and reads only the channel count, sample rate
and bits per sample from `fmt ` (the format tag and block-align words are
never read; block align is recomputed as `bits/8 × channels`); the `data`
chunk's declared size, not the file size, becomes the payload length. Legacy
DIGI metadata reads its sample-rate word at byte 22 of `HSHD`: a rate of
11,000 is remapped to 11,025, and the payload is **everything from byte 40
to the end of the file** (`size − 40`; the `SDAT` size field is never read);
the sample is treated as 8-bit mono thereafter. Raw samples default to
11,025 Hz mono 8-bit.

**Established:** callers choose static, transient load-and-play, or streaming
DirectSound loading. These are runtime modes, not authored format fields
[03 R-AUD-01 §1], [03 R-AUD-02 §1].

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
  The rebuild works this way: the battle-entry path calls the same catalog
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

### The loader malformed-input matrix [R-MALF-01 §1]

**Established** (direct static trace of every reader and validator
named below; asset census over the 958 retail GAFs for §6) unless a cell
says otherwise. For every file type retail loads, this section states what a
malformed input turns into. Byte-level *what the check reads*
lives in the format docs; this block owns *what the outcome is*. Doc 04 owns
what a running COB does with bad operands, doc 08 owns the save-file layout;
both are cited, not restated.

**The four outcomes, and the channels behind them.**

* **fault** — the process dies through the operating-system handler. Two
  paths: an unchecked memory access (out-of-blob pointer, wild write during
  relocation, unbounded recursion) raises the exception the
  unhandled-exception filter of `[01 R-PLAT-01 §8]` reports to `ErrorLog.txt`
  before returning continue-search; or the process calls the **fatal
  channel** — one shared routine that shows a system-modal, stop-icon message
  box titled with the application name, then calls the C-runtime `exit(1)`
  (a null message shows no box and exits at once — `[R-MAP-01 §2]` channel
  (c)). Allocation failure is the third route to the same end: the tagged
  allocator's null result reaches the out-of-memory hook of
  `[01 R-PLAT-01 §5]` (log line, modal, abort), so every "huge declared size"
  cell below that says *fault (out of memory)* means that hook.
* **skip** — the record or file is dropped. The loader returns null or zero
  and the caller decides; the non-fatal **`Error` box** (topmost,
  ok-only, no title text beyond `Error`) is the only visible variant.
* **default** — a typed read returns the caller's default.
* **accept-with-garbage** — the reader continues on bytes it did not check:
  the tail of an allocation the heap left behind (the tagged allocator never
  clears, `[01 R-PLAT-01 §5]`), the bytes after a short record, or memory
  reached through an unbounded pointer. Where the garbage is read through a
  pointer that can leave the address space the cell says *garbage or fault*.

**Shared VFS primitives.** Every loader below sits on the same four
routines, and their edge behaviour recurs in every row:

* *Open* — loose `fopen` first, then the providers in mount order; a miss
  returns null with no diagnostic. A compressed record's chunk-size table is
  read at open with its **read count ignored**.
* *Whole-file load* (used by GAF, 3DO, COB, FNT, PAL, and the OVR/GUI
  checksum passes) — a size of **0 or less returns null, indistinguishable
  from a missing file**; otherwise it allocates exactly the size and reads
  once; a read count **below 1** frees and returns null, any positive count
  is accepted, so a short read leaves the block's tail as heap contents.
* *Read* — a stored record clamps the request to `size − position` and
  returns whatever the C-runtime read returned (a short read passes through
  as a smaller count); a compressed record decodes the 64 KiB chunk that
  contains the position: a chunk whose stored length does not read back
  completely makes the read return the **all-ones value with no diagnostic**;
  a chunk whose `SQSH` decode fails is **fatal** — the message is
  `[HAPI_readfromfile] Decompression Error: <SQUASHERR name>` on one line and
  `block <n> of <count>`, `base name '<archive>'`, `length = <n>`,
  `name = '<file>'` on the following lines. The two failures are distinct: a
  short chunk read is silent all-ones, a decode failure is the fatal box.
* *TDF load* — its own loader: a missing file or a size of 0 or less yields
  **no tree** (typed reads then return defaults and the resource family
  decides whether that is fatal); a parse error is **fatal** (§4).

#### The matrix [R-MALF-01 §2]

Columns are seven malformed-input classes. Each cell names the outcome and the
check that decides it; "no check" means the reader walks on. Details and
the exact comparisons are in the numbered sections that follow.

| File | Truncated | Oversize (declared size beyond the file) | Wrong magic / version | Duplicate keys / sections | Bad reference | Zero-length | Integer overflow of a declared size |
|---|---|---|---|---|---|---|---|
| **HPI / UFO / CCX / GP3** (§3) | *garbage or fault*: the 20-byte header, 36-byte footer and directory-blob reads ignore their counts — a cut inside the blob leaves heap bytes that the relocation pass biases and writes back; a cut inside a stored record is a short read passed to the caller; a cut inside a chunk is silent all-ones | *garbage or fault*: blob size beyond the file → as truncated; record size beyond the file → short read; chunk stored length beyond the file → silent all-ones; chunk **decompressed** length above 65,536 → the LZ77 and zlib decoders write past the 64 KiB chunk buffer (heap overrun) and then report `SQUASHERR_BADUNPACKSIZE` fatally if the produced length differs | *skip* at mount: tag ≠ `HAPI`, version bytes ≠ `00 00 01 00`, or normalized footer ≠ template → not mounted, no message. *fault (fatal box)* at read: chunk marker ≠ `SQSH` → `SQUASHERR_BADHEADER`; method byte `> 3` → `SQUASHERR_BADUNPACKTYPE` (methods 0 and 3 pass the test, decode nothing and fail as `BADUNPACKSIZE`); byte-sum mismatch → `SQUASHERR_BADCHECKSUM` | *accept*: within one directory the **later** entry wins (backward scan, §2 "Lookup"); across archives the first provider wins (§2) | *fault*: an entry offset outside the blob is biased and written back at mount (write to a wild address); a subdirectory chain that loops recurses without a visited set (stack overflow); no check on any of them | *skip*: an empty archive file fails the tag test; an empty stored record is null to every whole-file consumer (size < 1) | *fault (out of memory)*: blob size or chunk stored length ≥ the address space fails allocation; a blob size below 20 skips the decipher loop (signed test) and relocates through bytes beyond the block; an entry count with the top bit set is treated as **no entries** (signed loop bound); chunk index is `position >> 16`, never bounded against the table |
| **TDF text** — FBI, OTA, weapon, feature, movement, side, sound, GUI, campaign, `translate`, `version`, `los` (§4) | *fault (fatal box)*: `Parse error in .TDF File! End of file - nextblock not zero` when the cut is inside a section; `Data field - '=' not found` / `Data field - ';' not found` inside a field; a cut between complete top-level items is accepted | n/a (text) | none: any bytes are text; a binary file reaches `Data field - '=' not found` (fatal) at its first non-blank byte, unless that byte is `[` or `}` | *accept*: repeated sections are all retained, the first-match accessor returns the earliest; an identical key spelling replaces the value (last wins); a case-variant spelling coexists as a second sorted entry (§4) | per family, §5 and the §5 cross-reference table; the generic parser has no references | *default*: a file of size 0 or less is treated as absent — no tree, every typed read returns its default; the family decides whether absent is fatal (§5) | *accept*: the integer accessor's conversion has **no overflow test** and wraps modulo 2³²; the fixed-point accessor multiplies by 65,536, truncates to signed 64 bits, and retains the low 32 bits; finite overflow of the signed 32-bit range wraps, while non-finite or signed-64 overflow yields a zero low word [01 R-DET-01 §1]; the floating accessor returns whatever the C-runtime decimal conversion produced |
| **FBI unit record** (§5) | as TDF | n/a | *skip*: the catalog loader reads `Version` and `Copyright` from every unit section; a version newer than the executable's (3.1) or a copyright line that does not match the template drops the unit from the catalog — with the `Error` box `Incompatible units found.  They will be ignored.  Please download the latest version of the game.` for the version case, silently for the copyright case | as TDF | weapon miss → record 0 (inactive); corpse miss → no wreck; movement class miss → scratch record; **model miss → fatal, the box shows the path `objects3d\<objectname>.3DO`** (corrects the cross-reference table's "slot stays empty"); script miss → null script, crash at the first creation `[04 R-COB-04 §8]`; sound category miss → index 0, or the decimal value of the authored text (`[R-CAT-01 §5]`) | as TDF (an empty FBI compiles a unit with every default and no name) | as TDF |
| **OTA map / mission** (§5, `[R-MAP-01]`) | as TDF | n/a | no magic; a parsed file without `GlobalHeader` → status-pane message and failure (`[R-MAP-01 §2]`), battle entry proceeds on the prologue sentinels | as TDF; `Schema <n>` probed by index, a gap ends the probe (`[R-MAP-01 §4]`) | `[units]` name that is no unit → *skip* (slot 0, nothing spawned); `Player` outside 1..10 or a slot without a controller → **fatal** `Player number %d invalid for unit %s`; `[features]` name → **fatal** `Record "%s" missing from feature files`; the TNT named by the OTA missing → **fatal** (path as message); `aiprofile` miss → `ai\default.txt` (`[R-MAP-01 §5]`) | as TDF | as TDF |
| **Catalog TDFs** — weapons, features, `moveinfo`, `sidedata`, `sound`, `meteor` (§5) | as TDF | n/a | `moveinfo.tdf` absent → **fatal** `Can't load MOVEINFO.TDF`; `sidedata.tdf` absent → **fatal**, and the box reads `Can't load GAMEDATA.TDF` (the text names the wrong file; nothing named `gamedata.tdf` is ever opened — this settles `docs/SPEC_CONFLICTS.md` SC2); `sound.tdf` absent → no categories, silent; `meteor.tdf` absent or without `[Default]` → record untouched (§6) | weapon: a second section with the same `ID` replaces the record (R-CONTENT-02); `CLASS<n>` gaps skipped; feature duplicates: first parsed document wins the name scan | weapon `ID` is **not range-checked**: `table + ID × 277` for any ID, so an ID above 255 or below −1 writes outside the 256-record table (*accept-with-garbage*, into neighbouring session state); weapon `model` miss → **fatal** (path); feature `object` miss → **fatal** (path); feature `filename` GAF miss → null root, every sequence null, silent; `seqname*` entry absent from the GAF → null sequence, silent; side `font` miss → **fatal**; side anchor subsection miss → **fatal** (§6) | as TDF | as TDF |
| **GUI panel** (§5) | as TDF | n/a | none; a file that is not a panel yields gadgets with default fields | sections are visited **by index in file order**, whatever their names; `totalgadgets` is read and then **overwritten** by the section census (inert) | `[COMMON]` absent → the gadget's common fields are **not written** (whatever the window record held); a kind byte other than 0–8 and 10 reads only `[COMMON]`; art names that resolve to no GAF entry follow the fallback chain of §6 | as TDF (loader returns 0 to its caller; doc 07 owns what a screen does without its panel) | *accept-with-garbage*: gadget records (347 bytes) are written into a fixed 69,463-byte window record with **no count check**; the array holds exactly 200 records, of which the first is the panel's own, so a panel with more than 199 gadget sections writes past it |
| **GAF** (§6) | *garbage or fault*: no size is checked; the loader biases every entry, frame, data and subframe offset it finds and **writes the biased values back**, so offsets that leave the block fault at load | same as truncated (offset table, frame count, data offset all trusted) | none: the version word is **never read** | entry names: linear scan, **first** match wins | entry name absent → null (the anims cache makes a missing **file** fatal with the path; a feature `filename` root or a `seqname` that is absent is a silent null) | *skip*: null, treated as missing (fatal or silent per caller as above) | entry count is a **signed 16-bit** field (≥ 0x8000 → no entries); frame count is 16-bit; the subframe count field is 16 bits wide but **only its low byte is read** (a count of 300 composes 44 subframes); a composite canvas of `width × height + 24` bytes and the 16 MiB-class raw frames are allocations, so absurd dimensions fault as out-of-memory |
| **TNT terrain** (§7) | *garbage or fault*: the file is read whole (counts summed, not checked) and every header pointer is biased without a bound; the tile map, attribute array, feature-name table and tile set are copied by their declared counts | same | **fatal** `Unknown TNT version:  0x%08x` for any header word other than `0x1020` / `0x2000`; the TNT file missing → **fatal** with the path as the whole message | n/a | feature-name table entry that no feature TDF defines → **fatal** `Record "%s" missing from feature files`; a cell feature word below the void threshold but **at or beyond the table count** indexes past the catalog (*garbage*, no bound) | *fault (fatal box)*: a 0-byte allocation's first word is read as the version — the `Unknown TNT version` box for any value but the two accepted ones | `Width × Height × 13`, tile count × 1,024, minimap `w × h` are allocations (out-of-memory fault); negative dimensions skip the per-cell loops (signed tests) |
| **3DO model** (§8) | *garbage or fault*: whole-load, then unconditional relocation of the name, vertex, primitive, sibling and child offsets and each primitive's three offsets; nothing is bounded | same | none: the version signature is **never read** | piece names: the script's piece-name lookup takes the first match (doc 04) | texture name that no texture GAF holds → the primitive becomes flat colour index 209 (`[03 §2.4]`); the model file missing → **fatal** (path) at unit compile, weapon compile and feature compile alike | *skip*: null → fatal as above | counts drive signed loops; a huge vertex count only walks memory |
| **COB script** (§8) | *garbage or fault*: whole-load, checksum over the file, then relocation of the five tables by their declared counts, unbounded | same | none: the version word is **never read** | script/piece names: first match (`[04 R-CB-01]`) | piece name absent from the model → doc 04 (`[04 R-COB-01]`); the script file missing → null, and the first unit created from that definition **crashes** (`[04 R-COB-04 §8]`) | *skip*: null → as missing | signed loop bounds; the code array is never bounded (doc 04 owns out-of-range jumps) |
| **PCX** (§9) | *skip* when the 128-byte header does not read completely; otherwise *accept-with-garbage*: a short read leaves the previous byte in place, so the decoder re-runs the last byte as every further command and value (a stale `0xC0` never advances the row — **hang**) | row runs are clamped to the remaining row width; dimensions come from the header only | *skip*: manufacturer byte ≠ `0x0A` or version ≠ 5 → 0 to the caller, no message; the caller decides (unit pictures blank) | n/a | n/a | *skip* (header read fails) | `width × height` from two 16-bit extents is an allocation (out-of-memory fault); a file shorter than 768 bytes seeks to a negative palette offset, the seek fails and the 768 palette bytes are read from wherever the position was (*garbage*) |
| **PAL** (§9) | *accept-with-garbage*: the whole block is what the file held, the consumer reads 1,024 bytes | n/a | none | n/a | a `.PAL` that is missing **or zero-length** is rebuilt from `palettes\<name>.PCX`, written back to `palettes\<name>.PAL` on the host, and `palettes\PALETTE.ALP`, `.LHT`, `.SHD` are deleted; the PCX missing too → **fatal** (path of the PCX) | see previous cell | n/a |
| **FNT** (§9) | *garbage or fault*: no validation of any kind; glyph offsets are used as read (`[03 R-FONT-01 §1]`) | same | none | n/a | a font file missing → **fatal** (path) for the two startup fonts and every side font (§6) | *skip* → fatal as missing | none (offsets are 16-bit) |
| **WAV** (§10) | *skip*, silent: a RIFF whose `data` size exceeds what can be read → the buffer is released and the sample is null; a RIFF cut before `fmt ` or `data` → null | same | *accept-with-garbage*: anything that is neither `DIGI`+`HSHD`+`SDAT` nor `RIFF`+`WAVE` is played as **raw 8-bit mono 11,025 Hz** from byte 0, header included | first `fmt `/`data` chunk wins | a sample file missing → null; the alias plays nothing, silently | *Unknown (external backend)* — empty raw reaches a zero-byte static buffer request unchanged; streaming instead requests its regular two-second capacity and fills EOF with silence · inspect or manually observe the target DirectSound backend | chunk sizes are compared signed: `fmt ` below 16 bytes or a `data` size ≤ 0 → null; the RIFF walk stops when the next chunk offset ≥ the RIFF size + 8 |
| **Save (HAPIBANK)** (§11, layout `[08 R-SAVE-02]`) | *garbage or fault*: item tables are trusted | same | tag ≠ `HAPIBANK` or bank version ≠ 1 → the load-game screen's modal `Invalid savegame file`, back to the screen; a compressed directory or account that fails to decode → **fatal** `[HapiBank::OpenBank] Decompression Error: %s` / `[HapiBank::LoadAccount] Decompression Error: %s` + `File: %s` | doc 08 | `Mission` missing or naming no mission, `Gametype` outside 1..2 → `Invalid savegame file`; save `Version` ≠ 0x11 → units skipped (`[08 R-SAVE-02]`) | *skip* (bank open fails) | doc 08 |
| **TAD demo** | not a retail input — the executable neither reads nor writes `.tad`; `[fmt tad]` documents a third-party recorder | — | — | — | — | — | — |

**Headline.** 16 rows × 7 classes = 112 cells. Counting each cell by its
dominant outcome: **fault** 21 (fatal box, unchecked access, or
out-of-memory), **skip** 17, **default** 5, **accept-with-garbage** 24,
**Unknown** 1; the remaining 44 are not applicable to the format or are
well-defined acceptances (duplicate rules, clamps) owned by the cited
section.

#### HPI: what the mount validates, and what the read path does after [R-MALF-01 §3]

Mount-time validation is the three content checks §2 states. The details
that decide the malformed cells: the header read (20 bytes), the footer
read (36 bytes)
and the directory-blob read all **ignore their return counts**, so a short
file is validated on whatever the stack or heap held; the working key is
derived and the blob deciphered from byte 20 up to the blob size (a signed
test — a blob size below 20 deciphers nothing); the root-directory pointer,
every entry's name and data pointer, and every subdirectory's entry array are
**biased by the block address and written back in place**, recursively, with
no bound and no visited set. An offset that leaves the blob is therefore a
wild *write* at mount time (access-violation fault through the crash
filter); a subdirectory that points back at an ancestor recurses until the
stack is exhausted (stack-overflow fault). The entry-count loop runs
`count − 1 ≥ 0` times as a **signed** test, so a count with the top bit set
means no entries.

**Unknown — acyclic shared directories.** The outcome when separate entries
reference the same directory node after an earlier visit relocated it is not
established by the ancestor-cycle case above. A trace of that repeated-node
relocation and a stock directory-reference census would settle support; a
checked host accepting the graph is not evidence of retail acceptance.

At read time a stored record clamps the request to `size − position` and
returns the C-runtime count. A compressed record reads its chunk table at
open (count ignored), then per chunk: allocates the stored length, reads it
— **a count that differs from the stored length returns all-ones silently**
— deciphers it with the archive key, and decodes it (§2 "Chunk wire
format"). The decoder's result codes map to the names `SQUASHERR_OK`,
`SQUASHERR_BADHEADER` (marker), `SQUASHERR_BADCHECKSUM`,
`SQUASHERR_BADUNPACKSIZE` (produced ≠ declared), `SQUASHERR_BADUNPACKTYPE`
(method byte above 3) and every nonzero code is **fatal** with the five-line
message of §1. Two things the decoder does not check: the method-byte test
is `> 3`, so methods 0 and 3 pass, decode nothing and fail as
`BADUNPACKSIZE`; and **neither decoder bounds its output to the 64 KiB chunk
buffer** — the LZ77 variant stops only at a match whose position is zero
(and reads past the compressed buffer if the stream has none), the zlib
variant is given the header's declared length as its output room. A chunk
declaring more than 65,536 output bytes corrupts the heap before the size
check can reject it. (`[fmt hpi]` carries the same facts in byte terms.)

#### TDF: a parse error is fatal, and the exact message [R-MALF-01 §4]

**Established.** A missing or empty file yields no tree, and every typed read
then returns its default. A **syntax error** is fatal: each of the five
diagnostics is formatted into one buffer and handed to the fatal channel —
system-modal box, application title, then `exit(1)`. There is no error return
from the parser and no partially-filled tree survives.

The message is one line: `Parse error in .TDF File! ` (trailing space),
then the diagnostic, then ` - name = '<section>' from file <path>` — the
literal word `name`, the **section name** the parser was inside (the
top-level call passes `root`), and the logical path the loader was given.

The parser's decisions, in order, on each non-blank byte: `[` → the closing
`]` must exist somewhere after it (`Sub-record - closing ']' not found`),
then, after whitespace, the next byte must be `{` (`Sub-record - opening '{'
not found`), then the section body is parsed recursively; `}` closes the
current section — at the **top level a `}` ends the parse and everything
after it is ignored**; end of text inside a section is `End of file -
nextblock not zero` (at the top level it is the normal end); any other byte
starts a field, whose `=` and then `;` are located by a forward scan to the
end of the text (`Data field - '=' not found`, `Data field - ';' not
found`), so a missing `;` swallows every following line into the value
before failing at the file's last field. Keys and values are unbounded
copies of the source text; only the string accessor bounds them (to the
caller's buffer, forced terminator).

Numeric edges: the integer accessor's conversion is the C-runtime decimal
`atol` — whitespace, sign, digits, `total × 10 + digit` with **no overflow
detection**, so an authored `4294967297` reads as 1 and `2147483648` as
−2147483648.

**Established — fixed-point and weapon conversion identity [RT-01].** The
fixed-point accessor multiplies the present decimal value by 65,536, calls
the shared signed-64 truncation helper, and stores the low 32 bits. The weapon
parser uses that same helper after its own field-specific multiply, retaining
either 32 or 16 bits. Neither path converts directly to a signed 32-bit
integer. Finite values outside signed 32-bit range therefore wrap; NaN,
infinity, and products outside signed 64-bit range yield zero in every retained
32-, 16-, or 8-bit destination [01 R-DET-01 §1]. A missing fixed-point key
returns the supplied raw fixed-point default without converting it. The
arithmetic before this conversion remains owned by the individual field's
contract; this closure does not change intermediate floating-point stores.


#### Catalog-level outcomes not stated elsewhere [R-MALF-01 §5]

* **Unit `Version` and `Copyright` gate (Established).** The unit catalog
  loader reads two keys from every unit section that the compiler of §5 does
  not: `Version` with the floating accessor (default 0.0) and `Copyright`
  with the string accessor (128 bytes; the default is a joke sentence that
  cannot match). The floating result is first saved as binary64. Before each
  signed-64 low-word conversion, a runtime wrapper rounds its binary64 operand
  **toward negative infinity**: `major = trunc64(floor(v)).low32`. That signed
  low word is promoted back to the floating value while the original binary64
  stays available for the second expression,
  `minor = trunc64(floor((v − float64(major)) × 10)).low32`. Both conversions
  then use the shared signed-64 truncation helper of `[01 R-DET-01 §1]`; there
  is no signed-32 conversion or intervening decimal parse. The version is
  accepted when `major < 3`, or
  `major = 3` and `minor ≤ 1` (the executable's own version bytes are 3, 1,
  1); the copyright is accepted when, after the four characters at the
  year position are overwritten with `0000`, it equals
  `Copyright 0000 Humongous Entertainment. All rights reserved.` byte for
  byte (the same wildcard trick as the archive footer). A unit failing the
  version test is dropped from the catalog and, once the pass ends, the
  `Error` box `Incompatible units found.  They will be ignored.  Please
  download the latest version of the game.` is shown — **unless** any unit
  also failed the copyright test or a loose-file/session-mode test, which
  sets a suppress flag and drops **silently**. `[fmt fbi]`'s "community
  lore" that units without the exact copyright line fail to load is thereby
  established, with the mechanism: the unit is not rejected by the parser,
  it is compacted out of the catalog after parsing.
* **Weapon `ID` (Established).** The record parser indexes
  `table + ID × 277` with **no range check**; R-CONTENT-02's "scratch slot
  before record 0" for the default −1 is the only sanctioned out-of-table
  case. Any other ID outside 0..255 writes a 277-byte record into whatever
  session state neighbours the table.
* **Unit and weapon models (Established).** `objectname` and weapon `model`
  are loaded at compile time through the shared model loader; a null result is
  passed to the fatal channel with the **path** (`objects3d\<name>.3DO`) as
  the entire message.
* **Feature GAF references (Established).** A feature that names a `filename` GAF which
  does not exist gets a null root and no diagnostic (this loader, unlike
  the anims cache used for effects, does not treat a missing file as
  fatal); every `seqname*` lookup against a null root, or against a root
  that lacks the entry, stores a null sequence. Burn, death and reclaim
  sequences that do resolve have their loop byte cleared as §5 states. A
  feature with a null idle sequence draws nothing; there is no substitute
  art and no message.
* **`sidedata.tdf` and the misnamed box (Established; settles SC2).** The
  catalog compiler loads `gamedata\moveinfo.tdf` and stops with
  `Can't load MOVEINFO.TDF` when that fails, and later loads
  `gamedata\sidedata.tdf` and stops with `Can't load GAMEDATA.TDF` when
  **that** fails. No file named `gamedata.tdf` is opened anywhere; the
  branch is reachable and it guards the side-data file.
* **GUI panels (Established).** The panel loader parses `guis\<name>.gui`
  with the shared TDF loader (a missing file returns 0 to the screen; a
  syntax error is fatal as §4) and then walks the top-level sections **by
  index, in file order** — the `GADGET<n>` names are not consulted. The
  header gadget's `totalgadgets` is read and then overwritten with the
  number of sections minus one. Each gadget's `[COMMON]` is read only when
  present; the kind byte selects extra keys for kinds 0–8 and 10 (a text
  box's `maxchars` is capped at 128); any other kind reads nothing more.
  Gadget records are 347 bytes inside a 69,463-byte window record, and the
  record array begins 63 bytes into that allocation, so it holds exactly
  200 records and fills the allocation to its last byte
  (63 + 200 × 347 = 69,463). The loader writes one record per section, and
  section 0 is the panel's own record — the one whose `totalgadgets` field
  the section census overwrites — so **199 gadget sections** fit. No count is
  tested anywhere, so the 201st section (the 200th gadget) is written past
  the end of the allocation.

#### GAF: what the loader and the blitter check [R-MALF-01 §6]

The loader reads the entry count as a **signed 16-bit** value (the 32-bit
field's low half; a value with bit 15 set means no entries), then for each
entry the 16-bit frame count, and for each frame biases the header offset,
the data offset, and — when the byte at frame offset 10 is nonzero — that
many subframe pointers and each subframe's data offset. Every bias is
written back; nothing is bounded, so a truncated or corrupt GAF faults during
load, not during draw. The version word is never read.

**The subframe count is one byte.** The field at frame offset 10 is two bytes
wide in the file, but **the executable reads only the low byte** — in the
loader's relocation loop and in the blitter's composition loop alike — so a
count of 256 composes nothing and 300 composes 44. The high byte (frame
offset 11) has a separate meaning on a **subframe**: when it is nonzero the
compositor draws that subframe through the tinted ALP blitter instead of the
plain one. **Established:** its nontransparent pixels write
`ALP[source * 256 + destination]`. The call requires the window's alpha-blend
table capability, enabled at application startup independently of the Shading
preference; the earlier Shading-gate claim is withdrawn by the writer trace in
[03 R-REN-03D §4]. A tinted composite propagates tint to every descendant.
`[03 R-REN-03D §4]` and `[03 R-COMP-01 §2]` own the drawing behavior. Retail data never exercises either edge: a census
over the 958 GAFs of the reference install (123,294 frames including
subframes) finds a maximum subframe count of 12 and no nonzero high byte.
`[fmt gaf]` carries the byte-level statement.

**Established — consumer-specific composition.** Gray and dithered fog
compositors recurse through every child with their own operation and raw-frame
gate, ignoring the alternate selector. Nonzero-mode glyph composition sends
every child through ALP tint; flash composition preserves its LHT operation.
Precomputed visibility masks and projectile-model texture spans instead read
the selected frame data directly as raster storage, without decoding children.
The frame-to-surface adapter also wraps the data directly. A dormant scaled
keyed compositor dispatches alternate children to scaled ALP, but no live
caller was found; it does not establish a general scaled-image rule.

**Unknown — remaining consumers and nested relocation.** Ordinary loading
relocates direct children only, while drawing can recurse. Which authored
nested layouts survive, including multiple references to the same nested
frame, requires a caller/layout trace through relocation and the selected
pixel reader. Host decoding or resampling of an authored shared graph does
not establish retail support for that layout. Feature-mask and
structure-texture paths outside the bounded census remain open. Nanolathe's
ordinary-only compatibility raster is a host fallback, not a universal retail
composition; callers must follow their particular contracts `[fmt gaf]`.

**Established — RLE row consumption.** The blitter decodes each row until it
has produced `width` pixels: a skip, repeat or literal run that would overshoot is **clamped to the
remaining width**. A repeat consumes its one palette byte; a literal reads
and advances by only the copied, clamped count. The earlier full-authored-count
source-advance wording is corrected for the ordinary row output path: the
discarded literal suffix is not read. A zero-length skip consumes the command
byte without advancing the pixel position. A row whose stored payload count
is zero is left untouched (fully transparent); a row whose commands run out **before**
`width` pixels is not detected — the decoder continues into the next row's
count bytes and payload as commands, and only the next row start (computed
from the stored count) is correct again. Frames wholly outside the clip
rectangle draw nothing; there is no per-pixel bound beyond the row width.

**Established — empty leaf geometry.** A zero width or height makes the leaf's
inclusive rectangle empty, so the ordinary blitter returns without reading
raw pixels or RLE rows. The loader does not reject that geometry. Composite
parents bypass the leaf rectangle test and traverse their children even when
the parent dimensions are zero. This does not waive the loader's pointer
relocation or establish safe out-of-file data. Nanolathe's file bounds and
aggregate work limits remain host policy, documented in `[fmt gaf]`.

#### TNT [R-MALF-01 §7]

`[R-MAP-01 §6]` states the open, the ten-read load and the version gate. For
the matrix: the summed read count is never compared with the size; every
header pointer is biased without a bound; the tile map is copied as
`(Width·16/32) × (Height·16/32)` 16-bit entries from wherever the tile-map
pointer lands, the attribute pass walks `Width × Height` records of 4 (or 8)
bytes, the feature-name table `count` records of 132 bytes, the tile set
`count × 1,024` bytes. A cell's feature word is stamped when it is below the
void threshold (`0xFFFB` canonical, `0xFC` legacy) with **no comparison
against the compiled table's count**, so an index at or beyond the count
reads footprint and flags from memory past the catalog. A zero-length TNT
allocates nothing, reads the version word from the empty block and fails
the version gate (fatal) unless the heap happens to hold one of the two
accepted values.

#### 3DO and COB [R-MALF-01 §8]

Both are loaded whole and relocated in place (§6 "Model archive", "Compiled
script archive"). Neither loader reads the version word. The model relocator
biases the name and auxiliary offsets when nonzero, the vertex and primitive
array offsets always, and recurses into the sibling and child offsets when
nonzero (no visited set); then each primitive's three offsets. The script
relocator biases the five table pointers and the entries of the script-name,
piece-name and trailing-record tables by their declared counts. A missing
model is fatal wherever a definition names one (unit, weapon, feature) with
the path as the message; a missing script is a null script pointer and the
crash at creation that `[04 R-COB-04 §8]` establishes. An unresolved
primitive texture is `[03 §2.4]`'s flat colour 209.

#### PCX, PAL and FNT [R-MALF-01 §9]

**PCX.** The decoder reads 128 bytes and requires the read to be complete,
the first byte `0x0A` and the second `5`; on failure it returns 0 and the
caller decides (no message). It takes `width = xmax − xmin + 1`,
`height = ymax − ymin + 1` from the 16-bit extents, allocates `width ×
height`, seeks to `size − 768` and reads the palette **without looking for
the 0x0C marker**, seeks back to 128 and decodes rows of exactly `width`
pixels: a run is `byte & 0x3F` pixels of the next byte, clamped so the row
never overflows; a literal is one pixel; **`bytes_per_line` is never read**,
so a file whose scan lines are padded decodes each row from the previous
row's padding (visible shear, no failure). Each command byte is read through the shared VFS read into
the same one-byte buffer; at end of file the read returns 0 and leaves the
previous byte, so a truncated body replays its last byte as every remaining
command and value: a stale literal fills the rest with that index, a stale
run fills it with the value byte, and a stale `0xC0` (run of zero) **never
advances the row and hangs the loader**. On a file shorter than 768 bytes
the palette seek is negative, the C-runtime seek fails, and the 768 bytes
are read from the current position (garbage palette).

**PAL.** The palette loader probes `palettes\<name>.PAL`. When the file is
missing **or zero-length** it allocates a 1,024-byte palette, decodes
`palettes\<name>.PCX` (fatal, path as message, when that fails) and packs
its 768-byte trailer into 256 four-byte entries with the fourth byte zero,
then **writes the 1,024 bytes to `palettes\<name>.PAL` on the host file
system** (open-for-write relative to the working directory; the result is
ignored) and deletes the host files `palettes\PALETTE.ALP`,
`palettes\PALETTE.LHT` and `palettes\PALETTE.SHD` (errors ignored) so the
derived tables are rebuilt. When the `.PAL` exists it is loaded whole with
no size check, and the consumer reads 1,024 bytes: a 768-byte three-byte
palette is read as 256 four-byte entries — the first 192 entries wrong by
one byte per entry and the last 64 taken from beyond the block. The engine
never consults a 768-byte palette as such; it misreads it.

**FNT.** Loaded whole (`[03 R-FONT-01 §1]`), no header or table validation;
the two startup fonts and every side font are fatal when missing (path as
message). A truncated font's glyph offsets point past the block and the
rasterizer reads whatever follows.

#### WAV [R-MALF-01 §10]

The classifier seeks to 0 and reads four bytes, then — **each time into the
same buffer, so a failed read keeps the previous bytes** — tests `DIGI` at
0, `HSHD` at 8 and `SDAT` at 32 (legacy), else `RIFF` at 0 and `WAVE` at 8,
else raw. The `RIFF` test in particular performs **no fresh read**: it compares
whatever four bytes the last read left in that buffer. For every reachable
input this is indistinguishable from testing offset 0, because a file that is
not `DIGI` fails the first test and leaves the offset-0 bytes in the buffer; it
differs only for a malformed file that begins with `DIGI` but lacks `HSHD` at
8, where the `RIFF` comparison sees the bytes at offset 8. Three details
behind §7 "WAV": (1) the legacy payload is the
loader's seek to byte 40 and **file size − 40** bytes as the sample, i.e.
everything after the 8-byte `SDAT` chunk header, whose size field is never
read; the rate is the 32-bit word at 22, remapped 11,000 → 11,025; (2) the
RIFF chunk walk does **not** honour odd-size padding — the next chunk is at
`offset + 8 + size` exactly; (3) the format tag and block-align words of
`fmt ` are **never read** — only channels (+2), sample rate (+4) and bits per
sample (+14) are taken, and block align is recomputed as
`(bits >> 3) × channels`, so a compressed-format file is decoded as PCM of
the declared width. The walk
stops when the next chunk offset reaches the RIFF size + 8; a `fmt ` chunk
shorter than 16 bytes or a `data` size of zero or less returns null. The
buffer creator then reads `data size` bytes and requires the count to reach
the size; a short read releases the buffer and returns null. A null sample
is silent everywhere: the alias plays nothing and no message is raised. The
raw path (no recognized header) plays the whole file, header bytes included,
as 8-bit mono 11,025 Hz.

**Established — device boundary.** Static and transient loading pass the
selected PCM parameters and payload length directly to DirectSound buffer
creation, with no special zero-length rejection. Creation, lock, short-read
or unlock failure releases the buffer and returns null. Streaming derives
capacity from two seconds of PCM rather than file length; empty raw input
therefore receives normal capacity and EOF silence fill if creation succeeds.
**Unknown:** the external backend's acceptance of zero-byte static requests
or unusual PCM parameters; the wrapper trace cannot determine that result
`[fmt wav]`, [03 §8.2].

#### Save file [R-MALF-01 §11]

`[08 R-SAVE-02]` owns the bank layout and the version-0x11 gate. The failure
edges: the bank opener requires the eight-byte tag `HAPIBANK` and a bank
version word of 1, else returns failure silently; the load-game screen turns
that, an unreadable `summary` account, a `Gametype` outside 1..2, or a
`Mission` name that resolves to no mission into the modal `Invalid savegame
file` and stays on the screen. A compressed directory or account whose
`SQSH` decode fails is **fatal** with `[HapiBank::OpenBank] Decompression
Error: %s` or `[HapiBank::LoadAccount] Decompression Error: %s` plus
`File: %s` on a second line. Item tables inside an account are walked by
their declared counts with no bound (garbage or fault when corrupt); a
missing or type-mismatched item returns the caller's default.

## Missing and unknown

Open items only. Each bullet states what is unknown, the section that owns it,
and the decider that would close it.

Fixed-point overflow is closed by [01 R-DET-01 §1] and [R-MALF-01 §4].
Floating text conversion still needs a bounded comparison for long-mantissa
rounding, intermediate scaling and extreme exponent cancellation, plus a
trace of alternate numeric-locale selection. Scanner spelling, malformed
exponents and range-result selection are established in "Typed accessors";
ordinary stock-value tests do not settle the remaining arithmetic. Fixed-point
conversion after parsing remains settled.
Unit-limit admission is established by [05 R-SHARE-01 §§7–10]; loader-side
questions do not supersede those consumer contracts.


* Bytes consumed after a successful short secondary-FBI read leaves temporary
  input storage unwritten · [R-CAT-01 §5] · run-specific storage, not a
  portable authored default; Nanolathe parses the returned bounded bytes.
* Optional 3DO auxiliary-reference targets and COB trailing-record meanings ·
  §7 "Model archive (3DO)" / "Compiled script archive (COB)" · find authored
  nonzero references with identifiable target data or a traced consuming
  reader. Relocation alone establishes offsets, not the target schema.
* HPI acyclic shared-directory support after the first relocation visit ·
  [R-MALF-01 §3] · trace repeated-node relocation and census stock directory
  references; ancestor-cycle behavior does not settle shared-node acceptance.
* GAF nested child layouts, including shared nested frames, supported by
  ordinary relocation, and remaining alternate-child behavior in feature-mask
  and structure-texture consumers ·
  [R-MALF-01 §6] · trace each selected frame through its actual pixel reader.
  Fog, glyph, flash, precomputed visibility-mask and projectile-model paths
  have bounded contracts there; implementation reconciliation is separate
  from recovering the remaining consumers.
* Data contract of `ONLLoadConfigFile` · §1 · not decidable from the retail
  executable — the function is defined by `online.dll`. The executable side
  (directory resolution, `ONLGetVersion()==3` gate, 336-byte block, zeroed
  block on any failure) is established.
* Exact bit consumers of the multiplayer lobby flag words · doc 08 · static
  trace.
* Archive entry-flag bits other than the subdirectory bit and the mutable
  enumeration-visibility bit (bit 1, mask `0x02`) · §2 "HPI-family container
  format" · static trace. Bounded-negative today: no other bit is tested by any
  mount, validate, or enumerate path, so synthetic values are inert.
* Whether any shipped archive variant outside the installed corpus departs
  from the container contract in §2 · §2 · asset census.
* Caller-specific duplicate-section merging policies · §4 · static trace. The
  first-match section accessor, the enumerator, and the duplicate-key winner
  are established.
* Precedence among registry, INI, and command line for non-language
  configuration · §3 · static trace (doc 01 §3.1 owns the scalar half).
* Sound alias-cache eviction outside the bounded negative census · §5
  "Sound aliases" · a reader/lifetime trace beyond that census. DirectSound
  sample and streaming descriptors are established in [03 R-AUD-01 §1] and
  [03 R-AUD-02 §1]; those flags are not an open loader question.
* External DirectSound acceptance of zero-byte static WAV buffers or unusual
  PCM parameter combinations · `[R-MALF-01 §10]`, [03 §8.2] · inspect or
  manually observe the target backend. The wrapper passes these requests
  without a local repair; streaming capacity is independent of payload size.
* Reader for plot-mask bit 7 · §6 "Map files" · static trace over the
  unrecovered regions. Marked `TODO(T23)` at the site; the mask preserves the
  bit and no isolated reader exists in the bounded census.
* Code-page behavior for high bytes · §3 · static trace. Marked `TODO(T23)` at
  the site.
* Language-specific font fallback and the census of runtime messages that pass
  through the translation lookup · §3 · static trace for the message census,
  asset census for the font fallback. Ordinary startup language selection and
  precedence are Established in §3.
* Which front-end screen reads the translated `missiondescription`, and what
  the briefing globe shows for a `Planet` value outside its vocabulary · §6
  `[R-MAP-01 §3]` · static trace (doc 07 owns both screens; the loader side is
  closed).
* Readers of the two session globals the mission loader sets to the constants
  1 and 0 between `lineofsight` and `memory` · §6 `[R-MAP-01 §3]` · static
  trace (naming-only; nothing in the load pipeline depends on them).
* Draw-time interpretation of model primitive colour, texture, and flag fields
  beyond what doc 03 narrows, and animation/model texture lifetime · doc 03,
  §6 "Model archive (3DO)" · static trace.
* GUI widget callback map · doc 07 · static trace. The parser-side
  control-kind mapping and the optional `[VERSION]` subsection are established
  in §6 "Interface panel files (`.gui`)".
* Whether the unit-limit mode flag is read by unrecovered code · §5 · static
  trace over the unrecovered regions. Bounded-negative in the recovered
  corpus, so it is retained-and-inert.
* Consumer list for document 06's category mask helper · doc 06 · static
  trace.
* Reader of the side record's `nameprefix` field · §5 `[R-KEYS-01 §5]` ·
  reader census on the 4-byte prefix field of the side record.
* Remaining presentation-setting reader gaps marked **Unknown** in
  [R-KEYS-01 §5] · docs 01/03/07 · trace the particular stored value's reader.
  The list is not a claim that all registry consumers remain open: for
  example, [03 R-AUD-01 §2] establishes `MixingBuffers`, and doc 07 owns the
  preferences and display/input consumers.
* Whether any reader tests unit capability bit 9 (the derived copy of
  `canreclamate`) separately from bit 10 · §5 `[R-KEYS-01 §1]` · bit-9
  reader census.
* Reader of the four-byte definition field the catalog loader sets to −1
  after the Version/Copyright gate (`[R-CAT-01 §4]` step 9) · §5 · reader
  census on that field (naming only; no load-path behaviour depends on it).
* Whether the settings saver writes the `Image Output Directory` default
  (`user_images\<account name>`) back to the registry · §3 `[R-CAT-01 §2]`
  · static trace of the saver's value list.
* Which of the two push sites of `Hey!  Somebody forgot to set
  downloadable=1 for %s` ever displays the text: the download-menu compiler
  of `[R-CAT-01 §8]` only formats it into a stack buffer · §5 · static trace
  of the downloader-region site.
