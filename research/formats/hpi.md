# HPI — Game Data Archives (`.hpi`, `.ufo`, `.ccx`, `.gp3`)

## Overview

HPI ("HAPI") is Total Annihilation's archive container. Every piece of game
data — models, scripts, textures, maps, sounds, text definitions — ships
inside HPI archives. The format is a recursive directory tree with per-file
compression (none, LZ77, or zlib), a whole-archive XOR obfuscation layer, and
an optional second obfuscation layer on compressed chunks.

Four extensions share the exact same format; they differ only in role and
load precedence:

| Extension | Role | Precedence |
| --- | --- | --- |
| `.gp3` | Official patch data (`rev31.gp3`) | highest |
| `.ccx` | Core Contingency expansion data | |
| `.ufo` | Third-party / downloaded units | |
| `.hpi` | Base game data (`totala1.hpi` … `totala4.hpi`) | lowest |

**Established — provider behavior:** loose-file opens precede archive lookup;
archives are searched in mount order, first match wins. Within an extension
tier, retail preserves host enumeration order without sorting. The ten-HPI
budget applies to newly mounted archives in one invocation, not to the total
mounted set. Revision selection, repeated mounting and content-family rules
(including the rejection of loose FBI definitions) belong to `[02 §2]` and
`[02 R-CAT-01 §4]`; they are not properties of the container bytes.

Saved games begin with `HAPIBANK` but use a different header and account/item
container. Do not parse them as ordinary HPI archives with a substituted
version word; see `[08 "Save-file organization"]`.

**Evidence scope:** the layouts and decoder below combine the cited original
format documentation, the sampled archives and the established retail loader
contract `[02 §2]` / `[02 R-MALF-01 §3]`. Corpus observations are bounded to
the named sample; Nanolathe's defensive acceptance rules are stated separately.

## Format at a glance

```
+--------------------------+  offset 0
| Header (20 bytes, plain) |  "HAPI", version, dir end, key, dir start
+--------------------------+  directory_start (always 0x14 in retail data)
| Directory region         |  encrypted with position-XOR cipher:
|   root node              |    u32 count, u32 offset -> entry list
|   entry lists            |    9-byte entries: name ptr, data ptr, flag
|   file-data records      |    9 bytes: data ptr, size, compression
|   name strings           |    NUL-terminated
+--------------------------+  directory_end
| File data                |  per file, encrypted with the same cipher:
|   stored bytes (comp 0)  |    raw file contents
|   or chunk table + SQSH  |    u32 sizes[n], then n compressed chunks
|   chunks (comp 1/2)      |
+--------------------------+
| Copyright string (plain) |  trailing, unencrypted, not referenced
+--------------------------+
```

All pointers inside the directory and all file-data offsets are **absolute
archive offsets**.

## Reference

### Header (20 bytes, unencrypted)

| Offset | Size | Type | Name | Description |
| ---: | ---: | --- | --- | --- |
| 0x00 | 4 | char[4] | marker | `HAPI` (`48 41 50 49`) |
| 0x04 | 4 | u32 | version | `0x00010000` for these archives. The `BANK` bytes in saved games identify a separate container, not this header layout. |
| 0x08 | 4 | u32 | directory_end | Directory-blob byte count measured from archive offset zero, including the 20-byte header; equivalently the absolute end of that blob. The bytes after the header occupy `directory_end - 20`, not `directory_end`. |
| 0x0C | 4 | u32 | header_key | Obfuscation key seed; only its low byte participates in the retail transform. Stored low bytes `0` and `255` both disable it. |
| 0x10 | 4 | u32 | directory_start | Absolute offset of the directory root node. `0x14` in all observed retail archives, but should be honored, not assumed. |

Real example — the first 20 bytes of `totala1.hpi`:

**Publication omission:** The retail-derived example is omitted from this
edition. The surrounding format description retains its stated evidence and
confidence.

marker=`HAPI`, version=`0x00010000`, directory_end=`0xE795`,
header_key=`0xBF`, directory_start=`0x14`.

### Encryption

**Established:** the directory blob is read from archive offset zero. Its
cipher span starts at offset **20**, independently of the root-directory
offset. The same position-dependent transform is used for stored file bytes,
chunk tables and chunk bytes; the header and copyright trailer stay plain.
For the stored key's low byte `k`, derive:

```
key = 0 if k == 0 else (~((k >> 6) | (k << 2)) & 0xff)
// Apply only when key != 0:
plain = ((pos & 0xff) ^ key ^ (~cipher & 0xff)) & 0xff
```

For `k = 0xBF`, the working byte is `0x01`. Stored `0x00` and `0xFF`
both produce a zero working byte, so neither enables the transform. Upper
header-key bytes never participate. `pos` is the absolute archive position, not an
index relative to the directory or payload. Everything below assumes
decrypted bytes. `[02 §2]` owns the loader contract.

### Directory tree

A **directory node** is 8 bytes:

| Offset | Size | Type | Description |
| ---: | ---: | --- | --- |
| +0 | 4 | u32 | number of entries in this directory |
| +4 | 4 | u32 | absolute offset of the entry list |

The **entry list** is a packed array of 9-byte entries (note the odd size —
there is no alignment padding anywhere in the directory):

| Offset | Size | Type | Description |
| ---: | ---: | --- | --- |
| +0 | 4 | u32 | offset of the NUL-terminated entry name |
| +4 | 4 | u32 | offset of the entry's data record |
| +8 | 1 | u8 | Persisted stock values: `1` = subdirectory, `0` = file. Retail classifies by bit 0; see below. |

For a subdirectory, the data record at `+4` is another 8-byte directory
node — the structure recurses. For a file, it is a 9-byte **file-data
record**:

| Offset | Size | Type | Description |
| ---: | ---: | --- | --- |
| +0 | 4 | u32 | absolute offset of the file's data |
| +4 | 4 | u32 | decompressed file size in bytes |
| +8 | 1 | u8 | compression: `0` = stored, `1` = LZ77, `2` = zlib |

Real example — the decrypted start of the `totala1.hpi` directory:

**Publication omission:** The retail-derived example is omitted from this
edition. The surrounding format description retains its stated evidence and
confidence.

The root node at 0x14 says: 15 entries, entry list at 0x1C. The first entry
(at 0x1C) is `name @ 0xA3, data @ 0xAA, flag 01` — a subdirectory whose name
at 0xA3 is `sounds`. Its node at 0xAA lists the WAV files inside. Following
the first file entry of `sounds/` leads to `BEEP2.WAV` with this file-data
record at 0xD88:

```
data_offset = 0xE795   size = 2100   compression = 1 (LZ77)
```

Note `0xE795` equals `directory_end` — file data begins immediately after
the directory.

Directory entry names are single path components; the full path is built by
joining parents with a separator (the game is DOS-heritage, so archives were
authored with `\`; any modern reimplementation can use `/`). Name matching is
case-insensitive. **Established:** lookup searches each directory's entries
backwards, selecting the last matching component. Earlier duplicate directories
do not contribute children to the selected directory. Retail also uses bit 1
as mutable enumeration visibility; other flag bits are not semantic types
`[02 §2]`. Nanolathe likewise classifies by bit 0 and does not reject
other persisted bits.

### Stored files (compression 0)

The file-data offset points at `size` raw bytes (encrypted with the archive
cipher like everything else). Absent from the retail sample described below; third-party tools
(e.g. unit viewers) commonly wrote stored archives with `header_key = 0`.
Joe D's own reference HPI writer (`HPIUtil.c`, the primary source for this
doc) defaults to `header_key = 0x7D` when writing an LZ77-compressed archive
and `header_key = 0` when writing a zlib/SQSH one — a writer convention, not
a format requirement.

### Compressed files (compression 1 or 2)

A compressed file is split into 65536-byte logical chunks:
`chunk_count = ceil(size / 65536)`. The last chunk holds the remainder
(`size mod 65536`, or a full 65536 if it divides evenly). A compressed chunk
can be *larger* than 64 KiB if the data was incompressible.

The file-data offset points at a **chunk size table**: `chunk_count` × u32,
each the total stored byte length of one chunk (including its 19-byte SQSH
header). The chunks themselves follow the table back-to-back, in order. To
seek to chunk *n*, sum the sizes of chunks 0..n-1.

Each chunk starts with a 19-byte **SQSH header**:

| Offset | Size | Type | Name | Description |
| ---: | ---: | --- | --- | --- |
| +0 | 4 | char[4] | marker | `SQSH` (`53 51 53 48`) |
| +4 | 1 | u8 | writer constant | **Established:** the linked chunk writer stores `2`; the retail decoder ignores it. Its authoring meaning is unknown. |
| +5 | 1 | u8 | comp_method | `1` = LZ77, `2` = zlib. The chunk byte selects the decoder; equality with the file-level byte is not required by retail [02 §2]. |
| +6 | 1 | u8 | encoded | Nonzero = payload has the extra chunk obfuscation applied (see below), `0` = not |
| +7 | 4 | u32 | compressed_size | Payload length in bytes. `stored_chunk_size = compressed_size + 19`. |
| +11 | 4 | u32 | decompressed_size | Output length (65536 except for the final chunk) |
| +15 | 4 | u32 | checksum | Sum of all payload bytes as unsigned values, 32-bit wrapping. Computed over the payload **before** undoing the chunk obfuscation. |

Real example — the single chunk of `sounds/BEEP2.WAV` in `totala1.hpi`
(after archive-level decryption). The chunk size table holds one entry,
`1927`; the chunk follows at 0xE799:

**Publication omission:** The retail-derived example is omitted from this
edition. The surrounding format description retains its stated evidence and
confidence.

marker=`SQSH`, unknown=2, method=1 (LZ77), encoded=1, compressed=1908
(1908 + 19 = 1927, the table entry), decompressed=2100 (the file size),
checksum=0x39676.

#### Chunk obfuscation (`encoded != 0`)

Applied to the payload after compression. To undo, for each payload byte at
payload-relative index `x`:

```
plain[x] = (data[x] - x) XOR x        // all 8-bit arithmetic
```

#### LZ77 variant (method 1)

A byte-oriented LZSS with a 4096-byte ring-buffer window and 2–17 byte
matches:

- Read one **tag byte**; its bits are consumed least-significant first, one
  bit per item.
- Tag bit `0`: copy one literal byte from input to output, and append it to
  the window.
- Tag bit `1`: read a u16 (little-endian). The upper 12 bits are a window
  *position*, the lower 4 bits are `length - 2` (so matches span 2–17
  bytes). **A window position of 0 terminates the stream.** Otherwise copy
  `length` bytes one at a time from the window position, appending each to
  the window as you go (positions wrap mod 4096, and a match may overlap the
  write cursor, which is how runs are encoded).
- After 8 items, read the next tag byte.

The window starts zero-filled and its write cursor starts at position **1**, not 0 (position 0 is
reserved as the terminator). **Established:** reaching the declared output
length does not terminate decoding; a zero-position match is still required.
The produced length is checked after that terminator. A safe implementation
also rejects an item that would exceed the declared length. Retail chunks include one padding byte after
the two-byte terminator, so up to one trailing byte after the terminator is
normal.

#### zlib variant (method 2)

The payload is a standard zlib stream (RFC 1950, `78 ...` header). Inflate
it; the output must be exactly `decompressed_size` bytes.

### Trailing copyright

Retail archives end with an unencrypted plaintext string, e.g.
`Copyright 1997 Cavedog Entertainment`. Nothing in the directory points to
it, but **the executable requires it**: the mount validator reads the last
36 bytes and compares them with `Copyright 0000 Cavedog Entertainment` after
overwriting the four bytes at the year position with `0000` — any four bytes
pass, anything else in the string fails the mount
(`[02 R-MALF-01 §3]`). A writer may ignore the trailer, but a reader that
wants to accept exactly what retail accepts must reject an archive without
it.

## Retail corpus notes

The original ten-archive sample uses `directory_start = 0x14`. It splits
cleanly into two generations:

| Archives | header_key | Compression |
| --- | --- | --- |
| `totala1/2/4.hpi` (1997 base game) | `0xBF` | LZ77 (method 1) throughout |
| `totala3.hpi`, `rev31.gp3`, `CCDATA/CCMAPS/CCMISS.CCX`, `btdata/btmaps.ccx` | `0` (unencrypted) | zlib (method 2) throughout |

No archive in that sample contains stored (method 0) entries — that mode appears
only in third-party tools' output. `totala3.hpi` is not game data at all:
it is the CD-2 installer carrier, containing `install/SETUP.EXE`,
`install/Totala.exe`, the network provider DLLs, installer art — and a
complete **nested archive** `install/totala1.hpi` (the real 32 MB game
data). This is an archive stored as an ordinary payload; it does not establish
automatic recursive mounting by the game.

## Writing archives

Layout used by the retail archives and `WriteHPI` (the reference writer the
format was reverse-engineered from): header at 0, directory node at 0x14,
followed by entry lists, subdirectory nodes, file-data records and name
strings (all inside `directory_start..directory_end`), then file data in
directory order. Nothing in the format requires this layout — pointers are
free — but tools that hand-walk archives may assume the directory
immediately follows the header.

## How the engine validates it

Behaviour is owned by `[02 §2]` and `[02 R-MALF-01 §3]`; this list is the
byte-level checklist a reader needs to accept exactly what retail accepts.

- **Mount-time checks, exactly three:** bytes 0–3 equal `HAPI`; bytes 4–7
  equal `00 00 01 00`; the 36 trailing bytes equal the copyright template
  with the year wildcarded. Any failure: the archive is not mounted, no
  message. The three reads ignore their return counts, so a file shorter
  than 36 bytes is compared against uninitialised bytes (in practice
  rejected).
- **Nothing else is checked at mount.** `directory_size` is allocated and
  read as-is (short read accepted; a value below 20 skips the decipher
  loop, a value with the sign bit set fails allocation); the root offset
  and every directory/entry offset are biased by the block address and
  written back, recursively, with no bound and no cycle detection — an
  offset outside the block is a write fault, a directory loop is a stack
  overflow. The entry count is used as a signed loop bound (≤ 0 → no
  entries).
- **Read-time checks:** a stored record clamps the request to what remains
  and returns the underlying read count (short reads pass through). For a
  compressed record the chunk table is read at open (count ignored); per
  chunk the stored length must read back exactly, else the read returns
  all-ones with no message; then the `SQSH` header is checked in this
  order: marker (`SQUASHERR_BADHEADER`), method byte `> 3`
  (`SQUASHERR_BADUNPACKTYPE` — 0 and 3 pass and later fail as
  `BADUNPACKSIZE`), byte-sum checksum over `compressed` payload bytes
  (`SQUASHERR_BADCHECKSUM`), decode, produced length ≠ `decompressed`
  (`SQUASHERR_BADUNPACKSIZE`). Any nonzero code is fatal (process exit).
- **No output bound:** the LZ77 decoder stops only at a match with position
  0 and the zlib decoder is given `decompressed` as its output room; both
  write into a 65,536-byte chunk buffer, so `decompressed > 65536` corrupts
  the heap before the size check runs. A safe reader must bound output at
  65,536 per chunk.

## Implementation coverage

**Established — implementation inspection:** `vfs/hpi.go` implements stored,
LZ77 and zlib reads, archive and chunk transforms, checksums, and the header /
footer checks. It adds bounded metadata reads, directory-cycle detection and
allocation limits. These are host safety policies, not retail rejection rules.

The reader requires the LZ77 terminator as well as the declared output
length, classifies directory entries by flag bit 0, and enables the archive
transform only for a nonzero derived working byte. Authored contract fixtures
cover the zero-derived-key and additional-flag-bit cases.
`AllowBank` does not implement the retail save-bank layout; that is
`internal/save`'s separate reader.

## Unknowns and caveats

- **Unknown:** the intended meaning of SQSH byte +4 (`0x02` in the sample).
  The linked writer establishes the constant and the decoder ignores it; an
  original authored definition or a consumer assigning meaning would settle
  whether it was intended as a version. This does not gate decoding.
- **Established:** the retail loader follows the authored root, entry-array,
  name and record offsets independently. It requires neither a root at 0x14
  nor adjacent records or a contiguous name pool. The sample establishes a
  writer convention, not a placement restriction; malformed pointers remain
  unchecked retail input as described above.
- **Established:** same-tier archive precedence depends on host enumeration;
  Nanolathe's deterministic mount policy is documented in
  `docs/SPEC_CONFLICTS.md` SC3. It is not an HPI byte-layout rule.
- **Established:** the checksum covers still-obfuscated payload bytes and is a
  wrapping byte sum, not an authenticity check.

## Sources

- Joe D, *HPI File Format*, document version 1.4 — the primary
  reverse-engineering note, including the cipher, SQSH layout, and LZ77
  algorithm:
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/ta-hpi-fmt.txt>
- *TA Files: The Nuts and Bolts*, TA Design Guide — archive roles and
  directory layout:
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/ta-files.htm>
- UnitUniverse help page (Gnome, 2006) — extension load order and retail
  loader limits: <https://www.units.tauniverse.com/?p=help>
- Original validation sample: ten archives spanning base, patch, Core
  Contingency and Battle Tactics. This is not a census of every installed
  provider or edition.
- Retail loader and malformed-input analysis: `[02 §2]`, `[02 R-MALF-01 §3]`.
- Nanolathe conformance consumer: `vfs/hpi.go`; its source is implementation
  evidence only.
