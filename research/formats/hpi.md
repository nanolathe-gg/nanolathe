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

The engine mounts every archive in the install directory and overlays them
into one virtual file system. Loose files in real directories next to the
executable override all archives. Within the same extension tier the retail
engine's winner among duplicate paths was not well defined; community notes
also report that the retail engine only loaded the specifically named
`rev31.gp3` and could fail with more than roughly ten `.hpi` archives.
Saved games use the same container with a `BANK` marker (see below) and extra
encryption; they are not covered by this document.

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
| 0x04 | 4 | u32 | version | `0x00010000` for normal archives. Saved games store `BANK` (`0x4B4E4142`) here instead. |
| 0x08 | 4 | u32 | directory_end | Absolute offset of the first byte **after** the directory region (historically documented as "directory size"; because the directory starts right after the 20-byte header the two readings agree, but pointer validation shows it is the end offset). |
| 0x0C | 4 | u32 | header_key | Obfuscation key seed. `0` means the archive is not encrypted. |
| 0x10 | 4 | u32 | directory_start | Absolute offset of the directory root node. `0x14` in all observed retail archives, but should be honored, not assumed. |

Real example — the first 20 bytes of `totala1.hpi`:

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.


marker=`HAPI`, version=`0x00010000`, directory_end=`0xE795`,
header_key=`0xBF`, directory_start=`0x14`.

### Encryption

Everything from `directory_start` to the end of the file data (directory,
stored files, chunk tables, and chunk bytes — but not the plaintext header)
is obfuscated with a position-dependent XOR cipher. Derive the working key
from the header key:

```
key = ~((header_key * 4) | (header_key >> 6))        // 32-bit arithmetic
```

For `header_key = 0xBF` (totala1.hpi): `key = 0xFFFFFD01`.

To decrypt a byte read from absolute archive offset `pos`:

```
plain = (pos ^ key) ^ ~cipher        // per byte, all masked to 8 bits
```

Only the low byte of `pos` and of `key` matter, since the operation is
byte-wise. If `header_key` is `0`, no decryption is applied anywhere.
Everything below assumes decrypted bytes.

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
| +8 | 1 | u8 | flag: `1` = subdirectory, `0` = file |

For a subdirectory, the data record at `+4` is another 8-byte directory
node — the structure recurses. For a file, it is a 9-byte **file-data
record**:

| Offset | Size | Type | Description |
| ---: | ---: | --- | --- |
| +0 | 4 | u32 | absolute offset of the file's data |
| +4 | 4 | u32 | decompressed file size in bytes |
| +8 | 1 | u8 | compression: `0` = stored, `1` = LZ77, `2` = zlib |

Real example — the decrypted start of the `totala1.hpi` directory:

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.


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
case-insensitive.

### Stored files (compression 0)

The file-data offset points at `size` raw bytes (encrypted with the archive
cipher like everything else). Used rarely in retail data; third-party tools
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
| +4 | 1 | u8 | unknown | Always `0x02` in observed data. Possibly a version. Not validated by known tools. |
| +5 | 1 | u8 | comp_method | `1` = LZ77, `2` = zlib. Matches the file-level compression byte in all retail data. |
| +6 | 1 | u8 | encoded | `1` = payload has the extra chunk obfuscation applied (see below), `0` = not |
| +7 | 4 | u32 | compressed_size | Payload length in bytes. `stored_chunk_size = compressed_size + 19`. |
| +11 | 4 | u32 | decompressed_size | Output length (65536 except for the final chunk) |
| +15 | 4 | u32 | checksum | Sum of all payload bytes as unsigned values, 32-bit wrapping. Computed over the payload **before** undoing the chunk obfuscation. |

Real example — the single chunk of `sounds/BEEP2.WAV` in `totala1.hpi`
(after archive-level decryption). The chunk size table holds one entry,
`1927`; the chunk follows at 0xE799:

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.


marker=`SQSH`, unknown=2, method=1 (LZ77), encoded=1, compressed=1908
(1908 + 19 = 1927, the table entry), decompressed=2100 (the file size),
checksum=0x39676.

#### Chunk obfuscation (`encoded = 1`)

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

The window write cursor starts at position **1**, not 0 (position 0 is
reserved as the terminator). Retail chunks include one padding byte after
the two-byte terminator, so up to one trailing byte after the terminator is
normal.

#### zlib variant (method 2)

The payload is a standard zlib stream (RFC 1950, `78 ...` header). Inflate
it; the output must be exactly `decompressed_size` bytes.

### Trailing copyright

Retail archives end with an unencrypted, unreferenced plaintext string, e.g.
`Copyright 1997 Cavedog Entertainment`. Nothing points to it; readers can
ignore any bytes past the last referenced extent.

## Retail corpus notes

Every retail archive uses `directory_start = 0x14`. Beyond that they split
cleanly into two generations:

| Archives | header_key | Compression |
| --- | --- | --- |
| `totala1/2/4.hpi` (1997 base game) | `0xBF` | LZ77 (method 1) throughout |
| `totala3.hpi`, `rev31.gp3`, `CCDATA/CCMAPS/CCMISS.CCX`, `btdata/btmaps.ccx` | `0` (unencrypted) | zlib (method 2) throughout |

No retail archive contains stored (method 0) entries — that mode appears
only in third-party tools' output. `totala3.hpi` is not game data at all:
it is the CD-2 installer carrier, containing `install/SETUP.EXE`,
`install/Totala.exe`, the network provider DLLs, installer art — and a
complete **nested archive** `install/totala1.hpi` (the real 32 MB game
data), demonstrating that archive nesting is a first-class scenario for
readers.

## Writing archives

Layout used by the retail archives and `WriteHPI` (the reference writer the
format was reverse-engineered from): header at 0, directory node at 0x14,
followed by entry lists, subdirectory nodes, file-data records and name
strings (all inside `directory_start..directory_end`), then file data in
directory order. Nothing in the format requires this layout — pointers are
free — but tools that hand-walk archives may assume the directory
immediately follows the header.

## Unknowns and caveats

- The SQSH header byte at +4 (`0x02`) has no confirmed meaning.
- Saved-game (`BANK`) containers use additional/different encryption that has
  not been decoded here.
- Duplicate-path precedence between two archives of the same extension tier
  is not defined by the retail engine (community observation). OpenTA defines
  its own deterministic rule (case-insensitive lexical archive filename
  order, later wins) — that is an OpenTA policy, not a format fact.
- The checksum is a plain byte sum; it detects corruption only. It covers the
  still-obfuscated payload bytes.
- `directory_start` values other than 0x14 and non-contiguous directory
  layouts are legal per the pointer structure but unobserved in retail data.

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
- Verified against all ten retail archives (base, patch, Core Contingency,
  Battle Tactics) and OpenTA's reader
  (`vfs/hpi.go`).
