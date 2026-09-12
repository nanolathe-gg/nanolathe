# COB / BOS — Unit Animation Scripts (`.cob`, `.bos`)

## Overview

Every unit has a script that animates its 3DO model and answers engine
queries: which piece to aim with, where the muzzle flash is, how to explode
when killed. Scripts are written in **BOS** ("Basic Object Script"), a
C-like text language, and compiled by Cavedog's *Scriptor* (or the community
*Cobbler*) into **COB** bytecode, which is what the engine actually loads
from `scripts/<UNITNAME>.COB`. Some retail archives also ship the `.bos`
source next to the `.cob` (e.g. `scripts/ARMFLASH.BOS` in `totala1.hpi`).

COB targets a small stack-based virtual machine with statics, locals, piece
operations, and cooperative script starts. The container stores ordinary
named entry points; which names the retail engine actually invokes is a
runtime contract owned by document 04.

This document covers the COB container, bytecode encoding and stack shapes,
compiler value scaling, BOS authored vocabulary, and retail asset census.
**Evidence scope:** container relocation is established in
[02 "Compiled script archive (COB)"]; interpreter-supported encodings in
[04 §4.3]. Compiler vocabulary and bounded asset censuses do not exhaust the
retail interpreter's accepted words. Host policies are labeled separately.

## Format at a glance

```
+--------------------------------+ 0x00
| Header (44 bytes = 11 × u32)   |
+--------------------------------+
| Code (u32 words × code_len)    |  all scripts back to back;
|                                |  entry points via index table
+--------------------------------+
| ScriptCodeIndexArray  u32[n]   |  entry word index per script
| ScriptNameOffsetArray u32[n]   |  -> "Create\0" ...
| PieceNameOffsetArray  u32[m]   |  -> "base\0" "turret\0" ...
| name strings                   |
+--------------------------------+
```

The diagram illustrates one packing order; header offsets locate the tables
and code, so their physical order is not prescribed. The optional trailing
record table is described below.

Every field, table entry, and instruction word is a little-endian u32. All
offsets are absolute file offsets. Code addresses (jump targets, entry
points) are **word indexes relative to the start of the code section**, not
byte offsets.

## Container reference

### Header (44 bytes, 11 × u32)

| Offset | Name | Description |
| ---: | --- | --- |
| 0x00 | VersionSignature | `4` for TA. (Kingdoms uses other versions; not covered here.) **The executable never reads it** (`[02 R-MALF-01 §8]`). |
| 0x04 | NumberOfScripts | Count of script entry points (functions) |
| 0x08 | NumberOfPieces | Count of piece names |
| 0x0C | CodeLength | Length of the code section **in u32 words** (historically labelled "Unknown_0" — it is the code word count) |
| 0x10 | NumberOfStatics | Count of static-variable slots the program declares (historically "Unknown_1"). There is no static-data section in the file. Runtime initialization is owned by [04 R-COB-04 §7]. |
| 0x14 | TrailingRecordCount | Number of records in the trailing 8-byte record table at `0x28`. Zero in the recorded 835-script corpus, which is how the field acquired its community names "Always_0" / "Unknown_2"; see "The trailing record table" below |
| 0x18 | OffsetToScriptCodeIndexArray | → u32[NumberOfScripts]: per-script entry point, as a word index into the code section |
| 0x1C | OffsetToScriptNameOffsetArray | → u32[NumberOfScripts]: file offsets of NUL-terminated script names |
| 0x20 | OffsetToPieceNameOffsetArray | → u32[NumberOfPieces]: file offsets of NUL-terminated piece names |
| 0x24 | OffsetToScriptCode | → code section (u32 words) |
| 0x28 | OffsetToTrailingRecords | → `TrailingRecordCount` records of 8 bytes. In the recorded 835-file census, the zero-count table offset equals the **value stored in** `ScriptNameOffsetArray[0]`, i.e. the first script-name string offset, which is how it acquired its community names "OffsetToFirstScriptName" / "Unknown_3"; see "The trailing record table" below |

Real example — `scripts/CORTRUCK.COB` from `totala1.hpi` (this is the same
unit the original format note documented; the retail file matches it
byte-for-byte):

**Publication omission:** The retail-derived example is omitted from this
edition. The surrounding format description retains its stated evidence and
confidence.

version=4, 3 scripts, 1 piece, 165 code words, 0 statics, code @ 0x2C,
index array @ 0x2C0 = `[0, 83, 86]`, script names @ 0x2CC =
`SmokeUnit, Create, Killed`, piece names @ 0x2D8 = `base`.

**Host-safety policy.** `NumberOfStatics` is not bounded by any corresponding
on-disk static-data span. Nanolathe therefore applies an explicit per-VM
static-storage budget before parsing publishes a program and again before a VM
or restore accepts an externally supplied program. This is a host-safety
policy, not a retail limit; it prevents a small header from requesting an
unbounded allocation. **Established implementation behavior:** `internal/cob.Load`
also requires version 4, aligned nonempty code/name/index tables, in-range
entry words and terminated nonempty names. The version-4 restriction is an
explicit host compatibility policy; retail ignores that word
[02 R-MALF-01 §8]. Duplicate script names are accepted: name lookup retains
the first exact, case-sensitive match, while indexed calls retain every
declared entry [04 R-COB-01 §1]. The
loader does not predecode opcodes or validate instruction boundaries.

### The trailing record table

**Established.** Header words `0x14` and `0x28` are a *count/pointer pair* for
a fifth table, not a reserved word and a redundant string-pool offset. The loader reads the file
whole, records its content checksum in the script cache, and then relocates exactly five
table pointers — script entry points (pointer only; the entries are word
indexes into the code array and are *not* relocated), script names (pointer plus
every entry), piece names (pointer plus every entry), the code array (pointer
only) and this trailing table (pointer, plus **the second dword of each of its
`0x14` records**, each record being 8 bytes). Document 02's "Compiled script
archive (COB)" and `[02 R-MALF-01 §8]` describe the same five-table relocation
from the loader side; this entry is what fixes the two words to the two slots.

In the recorded 835-file census the count is zero and the table offset equals
the first script-name string offset. A zero count does not require that
coincidence; both fields retain their table roles.

**Established (bounded compiler source):** the inspected Scriptor v1 (RC1)
writer emits a zero trailing-record count for both its TA and Kingdoms
outputs. It places the zero-count pointer at the start of the string pool.
This writer therefore supplies no example of the nonempty record layout.

**Unknown.** What an 8-byte record *means*. Its second dword is a file offset
(it is relocated); the first is not. The surveyed scripts carry none, and the recorded reader search found no
consumer after relocation. That is a bounded negative observation, not proof
that every authored use is inert. Decider: a modded or Kingdoms-era COB that
authors a non-zero count, or a reader of the relocated table elsewhere in the
image. Nanolathe validates the table's span but neither interprets the records
nor validates the target of each second-word offset; `Program` does not retain
this table. That is an implementation limitation for nonempty tables.

### Names and indexes

- Script *n*'s code starts at word `ScriptCodeIndexArray[n]` of the code
  section (byte offset `OffsetToScriptCode + index*4`).
- Pieces are referenced from code by zero-based index into the piece name
  array. Piece names correspond (case-insensitively) to 3DO piece names.
- The file records names but no callback classification. BOS/COB may reach any
  stored entry by `call-script` or `start-script`; the retail engine's fixed
  name producers and lookup behavior are specified by [04 R-CB-01 §1–§2].

## The virtual machine

At the bytecode level, instructions push and pop signed 32-bit cells;
`alloc-local` adds a local cell; statics are addressed by indexes from `0` to
`NumberOfStatics-1`; and `start-script`/`call-script`, signal, sleep, and piece
wait instructions encode cooperative control flow. Exact runtime allocation,
initial values, signal masks, failure edges, tick conversion, wake order, and
piece-motion timing are intentionally not duplicated here. They are specified
by [04 §4.1–§4.6], [04 R-COB-01 §1], and [04 R-COB-04 §7].

### Value scaling conventions

**Established — sampled scale relationships:** the recorded comparison of
`ARMFLASH.BOS` with `ARMFLASH.COB` supports the scales below. Exact compiler
quantization used to produce these shipped constants remains Unknown.

| BOS source | Meaning | Compiled constant |
| --- | --- | --- |
| `[v]` (square brackets) | linear distance/speed | `v * 163840`, quantized to an integer — i.e. 16.16 fixed point of `v * 2.5` model units. `[-1.4]` → `-229376`, `[300]` → `49152000`, `[3.0]` → `491520`. One BOS linear unit ("meter") is 2.5 3DO/world units. |
| `<v>` (angle brackets) | angle or angular speed | `v * 65536 / 360`, quantized to an integer — full circle = 65536. `<90>` → `16384`, `<50>` → `9102`. |
| bare number | raw integer | as written (`sleep 150` → `150`) |

**Established (bounded compiler source):** Scriptor v1 (RC1)'s numeric
converter scales a parsed number using configurable floating-point linear
or angular factors, then converts the result to a signed integer. For
representable results that conversion truncates toward zero; it does not
round to nearest. The settings store both factors as single-precision
values. The application's default linear factor is `163840.0`, but its
default angular factor is `182.0`, not the exact ratio `65536/360`.
Consequently this release's default settings do not reproduce every angle
in the sampled table.

**Unknown:** which compiler release, factor settings and intermediate
precision produced each shipped COB's fractional constants. The RC1 source
settles that converter's final integer conversion, not the provenance of
retail bytecode. A matched compiler build/settings record or a reproduced
boundary-value compilation would settle the remainder. This is a BOS
authoring question, not a runtime COB-decoding ambiguity.

The table states compiler constant encoding only. Runtime callback argument
units and coordinate transforms are owned by [04 R-CB-01 §2] and [04 §4.4].

## Instruction set

Each instruction is one opcode word, optionally followed by inline operand
words. Notation: `piece` and `axis` (0=X, 1=Y, 2=Z) are inline operands;
stack effects are shown as `( before -- after )` with the top of stack on
the right.

### Piece animation

| Opcode | Name | Operands | Stack | Notes |
| --- | --- | --- | --- | --- |
| `0x10001000` | move | piece, axis | `( speed target -- )` | Animate piece to absolute offset `target` at `speed`/sec. Stock compiler pushes speed first, destination second. |
| `0x1000B000` | move-now | piece, axis | `( target -- )` | Jump immediately to offset |
| `0x10002000` | turn | piece, axis | `( speed target -- )` | Rotate to absolute angle |
| `0x1000C000` | turn-now | piece, axis | `( target -- )` | Jump immediately to angle |
| `0x10003000` | spin | piece, axis | `( accel speed -- )` | Continuous rotation; compiled from `spin ... speed S accelerate A` (plain `spin ... speed S` pushes 0 accel) |
| `0x10004000` | stop-spin | piece, axis | `( decel -- )` | 0 = stop immediately |
| `0x10011000` | wait-for-turn | piece, axis | `( -- )` | Block until turn completes |
| `0x10012000` | wait-for-move | piece, axis | `( -- )` | Block until move completes |
| `0x10005000` | show | piece | `( -- )` | |
| `0x10006000` | hide | piece | `( -- )` | |
| `0x10007000` | cache | piece | `( -- )` | Set the render-piece cache flag [04 §4.3] |
| `0x10008000` | dont-cache | piece | `( -- )` | Clear the render-piece cache flag [04 §4.3] |
| `0x1000A000` | dont-shadow | piece | `( -- )` | |
| `0x1000D000` | shade | piece | `( -- )` | Set the render-piece shading flag [04 §4.3] |
| `0x10009000` | legacy effect (conventional name) | piece | `( a b -- )` | Empty unit adapter: consumes both values without a unit effect [04 §4.3] |
| `0x1000E000` | dont-shade | piece | `( -- )` | |
| `0x1000F000` | emit-sfx | piece | `( sfxtype -- )` | Emit effect (smoke, wake, flame...) from piece; see SFX types below |
| `0x10071000` | explode | piece | `( flags -- )` | Blow the piece off using explosion flags below |

The opcode numbers, inline operands, and stack shapes above are file/bytecode
facts. Per-tick arithmetic, busy-state transitions, wait wake-up, and immediate
commit behavior are the runtime contract in [04 §4.6].

### Flow control, threads, signals

| Opcode | Name | Operands | Stack |
| --- | --- | --- | --- |
| `0x10064000` | jump | target word index | `( -- )` |
| `0x10066000` | jump-if-false | target word index | `( cond -- )` — jumps when cond == 0 |
| `0x10062000` | call-script | script index, argument count | `( args... -- )` on successful start; caller waits for child thread, return value discarded |
| `0x10061000` | start-script | script index, argument count | `( args... -- )` on successful start; spawn thread |
| `0x10065000` | return | — | `( value -- )` return from function/thread |
| `0x10013000` | sleep | — | `( milliseconds -- )` |
| `0x10067000` | signal | — | `( mask -- )` release every thread whose mask intersects the popped mask, **the signalling thread included** |
| `0x10068000` | set-signal-mask | — | `( mask -- )` replace the current thread's mask |

Every engine-started root thread begins with signal mask **`1`**, and
`start-script` and `call-script` give the child the parent's current mask.
The community report of a zero root starting mask is wrong `[04 R-P0-10]`. Signal termination delivers nothing to
a completion receiver `[04 §4.2]`.

Jump targets are word indexes **relative to the code section start**.
Compiler-generated control flow targets instruction boundaries; the retail
interpreter has no separate boundary table. Failed starts leave arguments on
the caller's stack, and a failed `call-script` still blocks [04 §4.3].

### Values and variables

| Opcode | Name | Operands | Stack |
| --- | --- | --- | --- |
| `0x10021001` | push-constant | value | `( -- value )` |
| `0x10021002` | push-local | local index | `( -- value )` |
| `0x10023002` | pop-local | local index | `( value -- )` |
| `0x10021004` | push-static | static index | `( -- value )` |
| `0x10023004` | pop-static | static index | `( value -- )` |
| `0x10024000` | discard | — | `( value -- )`; implemented by the TA interpreter [04 §4.3] |
| `0x10022000` | alloc-local | — | `( -- )` add one local slot (parameters/`var`) |
| `0x10041000` | rand | — | `( low high -- random )` inclusive |
| `0x10042000` | get-unit-value | — | `( sysvar_id -- value )` read engine port, no arguments |
| `0x10043000` | get | — | `( sysvar_id a b c d -- value )` engine query with four argument slots; the compiler always pushes the ID then four values, zero-filling unused slots (verified across all retail bytecode — e.g. `get UNIT_HEIGHT(uid)` compiles to `push 11, push uid, push 0, push 0, push 0, get`) |
| `0x10082000` | set-unit-value | — | `( sysvar_id value -- )` write engine port; retail compiles `set X to V` as `push X, push V, set` |
| `0x10083000` | attach-unit | — | `( unit piece extra -- )`; the compiler supplies `extra = 0`. All 48 retail call sites have this shape, including authored piece `-1`. |
| `0x10084000` | drop-unit | — | `( unit -- )` |
| `0x10044000` | cargo-membership read (conventional name) | — | `( unitid -- value )`; walks **this** unit's cargo list and pushes 1 on the first entry whose 16-bit identifier matches, 0 when the list is empty or exhausted `[04 R-COB-03 §5]`; no BOS keyword; see below |
| `0x10045000` | carrier-identity read (conventional name) | — | `( -- value )`; follows this unit's **carrier back-pointer** (not the cargo-list head) and pushes that unit's identifier, or 0 when the unit is not carried `[04 R-COB-03 §5]`; no BOS keyword; see below |

**`0x10044000` and `0x10045000` — encoded interpreter slots absent from
Cavedog's toolchain and retail content.** Neither value appears in Scriptor's
`Compiler.cfg`, `Decompiler.cfg`, or `Defs.h`, so no known BOS syntax emits
it. A census of all 841 COB copies in the retail archives finds zero instances.
Their one-pop/one-push and zero-pop/one-push shapes are established by the
interpreter, and their cargo-list semantics by `[04 R-COB-03 §5]`, which also
owns the linkage they read (a carrier back-pointer plus a singly linked cargo
list, new cargo pushed at the head). The names above are conventional labels, not authored names.

### Arithmetic, comparison, logic

All are pure stack operations `( a b -- result )` unless noted.

| Opcode | Name | | Opcode | Name |
| --- | --- | --- | --- | --- |
| `0x10031000` | add | | `0x10051000` | less (`1`/`0`) |
| `0x10032000` | subtract | | `0x10052000` | less-or-equal |
| `0x10033000` | multiply | | `0x10053000` | greater |
| `0x10034000` | divide | | `0x10054000` | greater-or-equal |
| `0x10035000` | bitwise AND [04 §4.3] | | `0x10055000` | equal |
| `0x10036000` | bitwise OR | | `0x10056000` | not-equal |
| `0x10037000` | bitwise XOR [04 §4.3] | | `0x10057000` | logical AND |
| `0x10038000` | bitwise NOT `( a -- ~a )` [04 §4.3] | | `0x10058000` | logical OR |
| | | | `0x10059000` | word exclusive-or [04 §4.3] |
| | | | `0x1005A000` | logical NOT `( a -- !a )` |

### Compiler emission and reserved slots

Cavedog's own Scriptor compiler (`Compiler.cfg`) and decompiler (`Decompiler.cfg`,
`Defs.h`) — recovered from the *Scriptor v1 (RC1)* source release, the actual
Cavedog COB toolchain, not a third-party clean-room guess — enumerate every
operator their compiler can emit. Cross-checked against those files:

| Opcode | Status |
| --- | --- |
| `0x10035000` | **Established compiler limitation:** the operator table has only a bare `"?"` at priority 20, with no ordinary BOS keyword. Retail's interpreter nevertheless implements bitwise AND [04 §4.3]. |
| `0x10037000`, `0x10038000` | **Established compiler limitation:** absent from `Compiler.cfg` and `Decompiler.cfg`; the decompiler prints `"UK"`. Retail's interpreter implements word XOR and bitwise NOT respectively [04 §4.3]. |
| `0x10039000`, `0x1003A000`, `0x1003B000` | Present in the compiler operator table only as placeholder tokens `"??"`, `"???"`, `"????"`, priority 5. This establishes compiler placeholders, not runtime semantics. |
| `0x10059000` | **Established compiler limitation:** absent from Scriptor's opcode definitions and both configuration tables. Retail implements word exclusive-or, not a normalized boolean XOR [04 §4.3]. |

**Established runtime:** `0x10063000` is a dispatched TA opcode. It has
two inline words: an ignored first operand and a signed count. It discards
that many stack values (none for a nonpositive count), then advances by three
words. Its bounds and malformed-input outcomes are [04 R-COB-04 §6]. No
instance occurs in the recorded shipped-script census.

Scriptor also reuses this number as an internal decompiler jump marker. That
tool convention does not remove the retail interpreter's reserved pop-N arm.
The historical note's `call-script` assignment is wrong: real `call-script`
is `0x10062000`, and `start-script` is `0x10061000`.

### TAK-only opcodes (not used by TA)

Scriptor's grammar gates a few opcodes with `Flag=NOTA` ("not available in
TA") — they compile only for *Total Annihilation: Kingdoms* (COB version
signature `6`, detected as `Header.VersionSignature == 6` in the decompiler).
Documented here only so the numbers aren't mistaken for unknown TA opcodes:

| Opcode | Name | Notes |
| --- | --- | --- |
| `0x10072000` | play-sound | `play-sound("name", priority)` — plays a sound directly from a script. Its BOS keyword returns a value that callers usually discard. |
| `0x10073000` | Mission-Command | `Mission-Command("name", args...)` — single-player mission-scripting hook. |

The discard word `0x10024000` can follow a TAK `play-sound` expression, but
is also implemented by TA and is listed under "Values and variables" above.

TAK also extends the header: past the 11 TA words it appends
`OffsetToSoundNameArray: u32` and `NumberOfSounds: u32` (a 52-byte, 13-word
header) to name the sounds `play-sound` can reference. TA's header is
unaffected — confirmed 44 bytes / 11 words, matching the CORTRUCK.COB dump
above; the decompiler's in-memory struct is simply the 13-word TAK superset,
with the trailing two fields left unread/ignored (`if(TAK)` gated) for
version-4 files.

### Worked example

`CORTRUCK.COB`'s `Create` (entry word 83) and the start of `Killed`
(entry word 86), disassembled from the retail file:

**Publication omission:** The retail-derived example is omitted from this
edition. The surrounding format description retains its stated evidence and
confidence.

## The BOS language

BOS is compiler input rather than the runtime-loaded format; some retail
archives also ship sources. Summary of the language, composited from
stock scripts (`scripts/ARMFLASH.BOS` and the BOS guide's excerpts of
`armbats`, `armsilo`, `armcarry`):

### Declarations and preprocessor

**Publication omission:** The retail-derived example is omitted from this
edition. The surrounding format description retains its stated evidence and
confidence.

**Established:** the `piece` list order defines zero-based bytecode indexes.
Index `0` denotes the first declared piece; index `1` denotes the second.
A piece-name expression compiles to the same index domain as an explicitly
authored numeric piece index.

Standard headers shipped with Scriptor — **and, importantly, shipped inside
`totala1.hpi` itself** (`scripts/EXPTYPE.H`, `SFXTYPE.H`, `SMOKEUNIT.H`,
`STATECHG.H`, `HITWEAP.H`, `ROCKUNIT.H`, `YARD.H`, `STDSCRPT.H`,
`STDTANK.H`, `HELP.H`, alongside all 157 stock `.bos` sources): `exptype.h`
(explosion flags and sysvar IDs), `sfxtype.h` (SFX types), `smokeunit.h`
(damage smoke helper `SmokeUnit()`), `statechg.h` (activation state
machine; expects `ACTIVATECMD`/`DEACTIVATECMD` defines and is included
twice around them), `hitweap.h`, `rockunit.h`, `yard.h`
(`OpenYard`/`CloseYard`). The retail archive is therefore the authoritative
source for both the constants and idiomatic BOS.

### Statements

**Publication omission:** The retail-derived example is omitted from this
edition. The surrounding format description retains its stated evidence and
confidence.

Axis keywords: `x-axis`, `y-axis`, `z-axis`. The common −Z-forward asset
convention is discussed in [3do.md](3do.md); it is not a bytecode restriction.
Turns around Y are headings, around X are pitches. `0` position/angle means
the piece's authored rest pose.

**Runtime coordinate boundary.** Angle words are composed with the unit's
heading, pitch and bank in [03 §2.4]'s model space after the persistent
half-turn of authored X and Z. The subsequent world-offset mapping is
[03 R-RAST-01 §8]. Axis signs must follow those contracts; an observation of a
single animation does not establish a different file import transform.

Two quirks visible throughout the stock sources: Scriptor has no unary
minus in expressions — negation is written `0 - x` (`turn sleeves to x-axis
(0 - pitch)`, `attach-unit unitid to 0-1;`) — and the sea-transport pickup
idiom relies on `attach-unit` to piece `-1`: attach the cargo to the crane
piece (`link`), swing the crane aboard, then re-attach to piece `-1` so the
cargo rides the transport without following any piece
(`scripts/ARMTSHIP.BOS`, `TransportPickup`).

### Engine ports (system variables)

Read with `get NAME` / `get NAME(args…)`, written with `set NAME to V`.
The numeric IDs are **authoritative**: Cavedog's own `scripts/EXPTYPE.H`
ships inside `totala1.hpi` and defines them (the comments below are
Cavedog's):

The **Access** column is Cavedog's authored intent from that header, not a
claim about which retail engine switch arms exist. Read arithmetic, write
effects, defaults, packing, timing, and failure edges are runtime behavior in
[04 §4.4], [04 §4.7], and [04 R-COB-03 §1–§6].

| ID | Name | Authored access | Cavedog's comment |
| ---: | --- | --- | --- |
| 1 | `ACTIVATION` | set/get | on/off state |
| 2 | `STANDINGMOVEORDERS` | set/get | |
| 3 | `STANDINGFIREORDERS` | set/get | |
| 4 | `HEALTH` | get | 0–100 % |
| 5 | `INBUILDSTANCE` | set/get | builder ready to nanolathe |
| 6 | `BUSY` | set/get | "used by misc. special case missions like transport ships" |
| 7 | `PIECE_XZ` | get | packed x,z of a piece |
| 8 | `PIECE_Y` | get | |
| 9 | `UNIT_XZ` | get | packed x,z of a unit |
| 10 | `UNIT_Y` | get | |
| 11 | `UNIT_HEIGHT` | get | |
| 12 | `XZ_ATAN` | get | atan of packed x,z coordinates |
| 13 | `XZ_HYPOT` | get | hypot of packed x,z coordinates |
| 14 | `ATAN` | get | ordinary two-parameter atan |
| 15 | `HYPOT` | get | ordinary two-parameter hypot |
| 16 | `GROUND_HEIGHT` | get | argument is packed x,z |
| 17 | `BUILD_PERCENT_LEFT` | get | "0 = unit is built and ready, 1-100 = how much is left to build" |
| 18 | `YARD_OPEN` | set/get | "change which plots we occupy when building opens and closes" |
| 19 | `BUGGER_OFF` | set/get | "ask other units to clear the area" |
| 20 | `ARMORED` | set/get | |

Retail packs and unpacks the packed x,z argument with the signed-add packing
and negative-Z borrow correction established in [04 R-COB-03 §3]; this format
document does not restate that runtime arithmetic.

Retail bytecode usage confirms the table: zero-argument reads
(`get-unit-value`) use 17, 4, and 18; multi-argument reads (`get`) use
7–16 with their argument counts; `set-unit-value` writes 1, 5, 6, 18, 19,
20 (yard scripts always toggle `YARD_OPEN` and `BUGGER_OFF` together, per
`YARD.H`).

### Explosion type flags (`explode ... type ...`)

Authoritative values from the retail `scripts/EXPTYPE.H`:

| Value | Name | Meaning (Cavedog's comment) |
| ---: | --- | --- |
| 1 | `SHATTER` | "The piece will shatter instead of remaining whole" |
| 2 | `EXPLODE_ON_HIT` | "The piece will explode when it hits the ground" |
| 4 | `FALL` | "The piece will fall due to gravity instead of just flying off" |
| 8 | `SMOKE` | "A smoke trail will follow the piece through the air" |
| 16 | `FIRE` | "A fire trail will follow the piece through the air" |
| 32 | `BITMAPONLY` | "The piece will not fly off or shatter or anything. Only a bitmap explosion will be rendered." |
| 256–4096 | `BITMAP1` … `BITMAP5` | bitmap explosion art selection |
| 8192 | `BITMAPNUKE` | |
| 16128 | `BITMAPMASK` | "Mask of the possible bitmap bits" |

The retail meaning and tested-bit set are runtime behavior in [04 §4.5] and
[04 R-COB-04 §1].

Flags are OR-ed. Retail `Killed()` bodies overwhelmingly use
`BITMAPONLY | BITMAPn` for light damage and
`EXPLODE_ON_HIT | FALL | SMOKE | FIRE | BITMAPn` (mask `0x?1E`) for heavy
damage, matching the BOS guide's examples.

### SFX types (`emit-sfx N from piece`)

Authoritative authored values from the retail `scripts/SFXTYPE.H`. Its
vector-class constants are `SFXTYPE_VTOL` = 0, `SFXTYPE_THRUST` = 1,
`SFXTYPE_WAKE1` = 2, `SFXTYPE_WAKE2` = 3, `SFXTYPE_REVERSEWAKE1` = 4,
`SFXTYPE_REVERSEWAKE2` = 5. Its point-class constants are
`SFXTYPE_POINTBASED` = 256, `SFXTYPE_WHITESMOKE` = 256|1,
`SFXTYPE_BLACKSMOKE` = 256|2, `SFXTYPE_SUBBUBBLES` = 256|3. Runtime geometry,
visibility gates, dispatch, and presentation effects are owned by
[04 R-COB-03 §6] and [03 R-FX-01 §3].

### Engine callbacks

COB stores ordinary named functions; it has no callback table or per-function
callback bit. Three distinct facts must therefore remain separate:

1. **Authored convention:** stock BOS scripts use familiar names and helper
   bodies.
2. **Compiler vocabulary:** Scriptor's `COMMON_FUNC` table recognizes some
   names and argument counts for diagnostics/decompilation.
3. **Retail invocation:** only an executable producer proves that the engine
   starts a name. The complete producer, argument, mode, timing, and side-effect
   contract is [04 R-CB-01 §1–§8].

The retail executable's fixed-name producer census contains exactly these
forty case-sensitive names:

`Activate` · `AimFromPrimary` · `AimFromSecondary` · `AimFromTertiary` ·
`AimPrimary` · `AimSecondary` · `AimTertiary` · `BeginTransport` · `Create` ·
`Deactivate` · `EndTransport` · `FirePrimary` · `FireSecondary` ·
`FireTertiary` · `HitByWeapon` · `Killed` · `MoveRate1` · `MoveRate2` ·
`MoveRate3` · `QueryBuildInfo` · `QueryLandingPad` · `QueryNanoPiece` ·
`QueryPrimary` · `QuerySecondary` · `QueryTertiary` · `QueryTransport` ·
`RockUnit` · `SetDirection` · `SetMaxReloadTime` · `SetSpeed` · `SweetSpot` ·
`StartBuilding` · `StartMoving` · `StopBuilding` · `StopMoving` ·
`TakeDamage` · `TargetCleared` · `TransportDrop` · `TransportPickup` ·
`setSFXoccupy`.

**Caveats on the list above.** `MotionControl(moving, aiming, justmoved)` is a
compiler-recognized signature and an authored convention, but the whole-image
census finds no `MotionControl` occurrence and no retail engine producer.
`MoveRate1`/`MoveRate2`/`MoveRate3` are engine-invoked, but document 04 traces
them to the general movement-tier classifier, not an aircraft-only family.
Names such as `SmokeUnit`, `RestoreAfterDelay`, `OpenYard`, `CloseYard`, and
`Demo` may be authored helpers or script-to-script entries; appearing in BOS
or Scriptor vocabulary does not make them fixed retail callbacks.

## Retail corpus notes

**Established (bounded asset observation).** The recorded decode of 835 COB
files from `totala1.hpi`, `rev31.gp3`, `CCDATA.CCX` and `btdata.ccx` reports
the following. The separate transport-read census above counts 841 copies;
**Unknown:** the exact enumeration difference between these two recorded
totals. Repeating the 841-copy enumeration with archive/copy provenance would
settle it. Neither total is a universal format limit.

**Established (repeatable header census):** a fresh independent enumeration
of each installed archive, without overlay deduplication, reproduces 835
copies: `totala1.hpi` 157, `rev31.gp3` 200, `ccdata.ccx` 265 and
`btdata.ccx` 213. These represent 272 distinct logical paths and 258 distinct
file-content hashes. Every copy has version 4, zero trailing records, and no
exact duplicate script names. This recount checks headers and names; it does
not repeat the historical instruction decode below. Duplicate-name handling
and nonempty trailing tables therefore concern custom inputs, not a missing
path through these stock scripts.

Including the installed `AFark.ufo`, `AFlea.ufo`, `AScarab.ufo`,
`CorNecro.ufo`, `Cormabm.ufo` and `corplas.ufo` adds one COB copy apiece,
reproducing a total of 841 under that explicit scope. Each added script also
has version 4, zero trailing records and no duplicate names. This explains
how the two totals can arise from different archive sets without asserting
that the unrecorded historical enumeration used exactly that set.

- Version signature is always 4; the trailing-record count at `0x14` is always
  0 (see "The trailing record table" — the word is a count, not a reserved
  word); the recorded instruction decode covers this corpus with no
  unknown opcodes and no misaligned instruction stream.
- Static-variable counts are small (0–7 covers nearly everything).
- The recorded census reports no occurrences of these nine opcodes: `dont-shadow`
  (`0x1000A000`), bitwise AND/XOR/NOT (`0x10035000/7000/8000`), word
  XOR (`0x10059000`), `greater`/`greater-or-equal` (`0x10053000`,
  `0x10054000`), and the two transport reads `0x10044000` / `0x10045000`
  documented under "Values and variables". Their absence from this corpus
  cannot establish whether the interpreter implements them; [04 §4.3] provides that evidence.
  (`greater`=`0x10053000`, `greater-or-equal`=`0x10054000` per Scriptor's own
  `Compiler.cfg` operator table; community tables often swap the two.)
- Common helper-script conventions (script names that are not engine
  callbacks but appear across many units): `Go`/`Stop` (activation
  bodies), `activatescr`/`deactivatescr` (authored animation includes),
  `InitState`/`RequestState(requestedstate[, currentstate])` (from
  `STATECHG.H`; both a 1-arg and 2-arg overload are recognized), `walk`/
  `walklegs`/`walkscr`/`stand`/`swim` (Kbot locomotion),
  `RestorePosition`, `Open`/`Close`/`StartDoorOpen` (door animations),
  `ProcessFlames`, `BoomCalc(posxz, posy)`/`BoomExtend(posxz, posy)`/
  `BoomReset`/`BoomToPad` (crane transports; `BoomCalc`/`BoomExtend`
  signatures confirmed from Scriptor's `COMMON_FUNC` table).
- Typical `sleep` arguments are 40–1500 ms, supporting the
  milliseconds reading.

## Unknowns and caveats

- **Established opcode distinctions:** `0x10063000` has both a Scriptor
  internal-marker use and a real retail reserved pop-N arm. `call-script` is
  `0x10062000`. `0x1005A000` is logical NOT and `0x10038000` is bitwise NOT
  [04 §4.3] [04 R-COB-04 §6]. Compiler emission and retail dispatch are
  separate evidence sources.
- **Established runtime, limited compiler emission.** AND, both word-XOR
  slots and bitwise NOT are traced interpreter operations [04 §4.3]. Their
  absence or placeholder spelling in Scriptor does not make their runtime
  behavior unknown; the compiler limitations are listed above.
- **`attach-unit` / `drop-unit` stack shapes** are established from retail
  bytecode (see the instruction table): all 48 retail call sites are uniform,
  including the `piece = -1` idiom, and the compiler-supplied third value is
  always zero. It is **not** inert: the shared attach/drop commit writes its
  low two bits into the cargo's committed mover-mode pair (`0` attached,
  `1` grounded, `2` airborne) — `[04 R-COB-03 §5]`.
- **Sleep and wait runtime behavior** is established in [04 §4.2] and
  [04 §4.6]. This file retains only the opcode and authored-duration encoding.
- **Established header layout:** file word `0x28` points to the trailing
  record table counted by word `0x14`; it is not a first-script-name pointer.
  The record meaning and any runtime reader remain **Unknown**, as described
  in "The trailing record table" above; a nonempty authored table or a traced
  reader would settle that residual.
- **Loader edges.** The file is read whole; a
  missing or zero-length script is a null script pointer and the first unit
  created from the definition crashes (`[04 R-COB-04 §8]`). The five table
  pointers and the entries of the script-name, piece-name and trailing
  tables are biased by their declared counts with no bound, so a truncated
  file faults at load or at first execution. Full outcome table:
  `[02 R-MALF-01 §2]`.
- **Embedded comments (community `Cobbler` compiler only).** Scriptor's
  decompiler special-cases a code word `0x6C697542` ("cobbler crap" in its
  own source, `#define COBBLER_CRAP`) as a marker for 45 inline words of
  ASCII text — the community *Cobbler* compiler apparently embedded original
  BOS comments in the bytecode stream itself so round-tripping through
  decompile/recompile could restore them. Not a real instruction; any
  disassembler that walks retail bytecode won't hit it (Cavedog's own
  Scriptor never emits it), but a from-scratch COB reader could trip over it
  if ever fed a `Cobbler`-compiled community `.cob`.

## Sources

- *TA COB format note* — container layout with the worked `CorTruck.cob`
  listing:
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/ta-cob-fmt.txt>
- *COB commands note* — opcode meanings and stack effects:
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/cob-commands.txt>
- *BOS File Content Description*, TA Design Guide — the BOS language,
  callbacks, sysvars, explosion flags:
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/bosdesc.htm>
- Historical community opcode cross-check only, not retail evidence: the
  tables in Spring (`rts/Sim/Units/Scripts/CobThread.cpp`,
  <https://github.com/spring/spring>) and kbot-io
  (`formats/scripting/opcodes.go`, <https://github.com/coreprime/kbot-io>).
- Cavedog's own `scripts/EXPTYPE.H` and `scripts/SFXTYPE.H` (plus
  `SMOKEUNIT.H`, `YARD.H`, `STATECHG.H`, and all stock `.bos` sources),
  shipped inside `totala1.hpi` — the authoritative constants above.
- *Scriptor v1 (RC1)* source release — Cavedog's own official BOS
  compiler/decompiler (not a third-party clean-room tool): `Defs.h`,
  `CobCodec.cpp`/`.h` (decompiler), `CobScriptCode.cpp`, `BosCmdParse.cpp`/
  `.h` (compiler), and the shipped `Compiler.cfg`/`Decompiler.cfg` operator
  and callback-signature tables. Used above to resolve several opcode
  ambiguities (the reserved/unassigned slots, `call-script`'s real opcode,
  logical-vs-bitwise NOT) and to confirm `MotionControl`/`SmokeUnit`/
  `BoomCalc`/`BoomExtend`/`RequestState` parameter signatures. Cross-checked
  against a second, independent community opcode table (`commands.txt` from
  a VB6 "COBBuilder" tool in Kinboat's 1998 TA-formats source archive),
  which agrees on every opcode it lists.
- The same RC1 release's `BosCmdParse.cpp` numeric converter and output
  writer, plus `Scriptor.cpp` settings defaults, establish the bounded
  conversion and zero-trailing-table observations above. Configurable
  defaults are not evidence of the settings used for shipped assets.
- Scaling constants verified against the retail
  `ARMFLASH.BOS`/`ARMFLASH.COB` pair from `totala1.hpi`; container examples
  from `CORTRUCK.COB`; corpus statistics from decoding all 835 retail COBs.
