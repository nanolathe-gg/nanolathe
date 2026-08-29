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

## Format at a glance

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.


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
| 0x10 | NumberOfStatics | Count of static-variable slots the program declares (historically "Unknown_1"). There is no static-data section in the file. Runtime initialization is owned by [R-COB-04 §7]. |
| 0x14 | Always_0 | Zero in all observed files (historically "Unknown_2") |
| 0x18 | OffsetToScriptCodeIndexArray | → u32[NumberOfScripts]: per-script entry point, as a word index into the code section |
| 0x1C | OffsetToScriptNameOffsetArray | → u32[NumberOfScripts]: file offsets of NUL-terminated script names |
| 0x20 | OffsetToPieceNameOffsetArray | → u32[NumberOfPieces]: file offsets of NUL-terminated piece names |
| 0x24 | OffsetToScriptCode | → code section (u32 words) |
| 0x28 | OffsetToFirstScriptName | Offset of the first script name string (historically "Unknown_3"; equals `ScriptNameOffsetArray[0]` in all observed files — effectively the start of the string pool) |

Real example — `scripts/CORTRUCK.COB` from `totala1.hpi` (this is the same
unit the original format note documented; the retail file matches it
byte-for-byte):

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.


version=4, 3 scripts, 1 piece, 165 code words, 0 statics, code @ 0x2C,
index array @ 0x2C0 = `[0, 83, 86]`, script names @ 0x2CC =
`SmokeUnit, Create, Killed`, piece names @ 0x2D8 = `base`.

### Names and indexes

- Script *n*'s code starts at word `ScriptCodeIndexArray[n]` of the code
  section (byte offset `OffsetToScriptCode + index*4`).
- Pieces are referenced from code by zero-based index into the piece name
  array. Piece names correspond (case-insensitively) to 3DO piece names.
- The file records names but no callback classification. BOS/COB may reach any
  stored entry by `call-script` or `start-script`; the retail engine's fixed
  name producers and lookup behavior are specified by [R-CB-01 §1–§2].

## The virtual machine

At the bytecode level, instructions push and pop signed 32-bit cells;
`alloc-local` adds a local cell; statics are addressed by indexes from `0` to
`NumberOfStatics-1`; and `start-script`/`call-script`, signal, sleep, and piece
wait instructions encode cooperative control flow. Exact runtime allocation,
initial values, signal masks, failure edges, tick conversion, wake order, and
piece-motion timing are intentionally not duplicated here. They are specified
by [04 §4.1–§4.6], [R-COB-01 §1], and [R-COB-04 §7].

### Value scaling conventions

Verified by comparing `ARMFLASH.BOS` with its compiled `ARMFLASH.COB`:

| BOS source | Meaning | Compiled constant |
| --- | --- | --- |
| `[v]` (square brackets) | linear distance/speed | `round(v * 163840)` — i.e. 16.16 fixed point of `v * 2.5` model units. `[-1.4]` → `-229376`, `[300]` → `49152000`, `[3.0]` → `491520`. One BOS linear unit ("meter") is 2.5 3DO/world units. |
| `<v>` (angle brackets) | angle or angular speed | `round(v * 65536 / 360)` — full circle = 65536. `<90>` → `16384`, `<50>` → `9102`. |
| bare number | raw integer | as written (`sleep 150` → `150`) |

The table states compiler constant encoding only. Runtime callback argument
units and coordinate transforms are owned by [R-CB-01 §2] and [04 §4.4].

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
| `0x10007000` | cache | piece | `( -- )` | Re-enable texture caching (disables texture animation) |
| `0x10008000` | dont-cache | piece | `( -- )` | Enable animated textures on piece |
| `0x1000A000` | dont-shadow | piece | `( -- )` | |
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
| `0x10062000` | call-script | script index, argument count | `( args... -- )` synchronous call, callee return value discarded |
| `0x10061000` | start-script | script index, argument count | `( args... -- )` spawn thread |
| `0x10065000` | return | — | `( value -- )` return from function/thread |
| `0x10013000` | sleep | — | `( milliseconds -- )` |
| `0x10067000` | signal | — | `( mask -- )` kill other threads matching mask |
| `0x10068000` | set-signal-mask | — | `( mask -- )` set current thread's mask |

Jump targets are word indexes **relative to the code section start** and
must land on an instruction boundary.

### Values and variables

| Opcode | Name | Operands | Stack |
| --- | --- | --- | --- |
| `0x10021001` | push-constant | value | `( -- value )` |
| `0x10021002` | push-local | local index | `( -- value )` |
| `0x10023002` | pop-local | local index | `( value -- )` |
| `0x10021004` | push-static | static index | `( -- value )` |
| `0x10023004` | pop-static | static index | `( value -- )` |
| `0x10022000` | alloc-local | — | `( -- )` add one local slot (parameters/`var`) |
| `0x10041000` | rand | — | `( low high -- random )` inclusive |
| `0x10042000` | get-unit-value | — | `( sysvar_id -- value )` read engine port, no arguments |
| `0x10043000` | get | — | `( sysvar_id a b c d -- value )` engine query with four argument slots; the compiler always pushes the ID then four values, zero-filling unused slots (verified across all retail bytecode — e.g. `get UNIT_HEIGHT(uid)` compiles to `push 11, push uid, push 0, push 0, push 0, get`) |
| `0x10082000` | set-unit-value | — | `( sysvar_id value -- )` write engine port; retail compiles `set X to V` as `push X, push V, set` |
| `0x10083000` | attach-unit | — | `( unit piece extra -- )`; the compiler supplies `extra = 0`. All 48 retail call sites have this shape, including authored piece `-1`. |
| `0x10084000` | drop-unit | — | `( unit -- )` |
| `0x10044000` | cargo-membership read (conventional name) | — | `( unitid -- value )`; no BOS keyword; see below |
| `0x10045000` | carrier-identity read (conventional name) | — | `( -- value )`; no BOS keyword; see below |

**`0x10044000` and `0x10045000` — encoded interpreter slots absent from
Cavedog's toolchain and retail content.** Neither value appears in Scriptor's
`Compiler.cfg`, `Decompiler.cfg`, or `Defs.h`, so no known BOS syntax emits
it. A census of all 841 COB copies in the retail archives finds zero instances.
Their one-pop/one-push and zero-pop/one-push shapes are established by the
interpreter; cargo-list meaning and side effects are runtime behavior owned by
[R-COB-03 §5]. The names above are conventional labels, not authored names.

### Arithmetic, comparison, logic

All are pure stack operations `( a b -- result )` unless noted.

| Opcode | Name | | Opcode | Name |
| --- | --- | --- | --- | --- |
| `0x10031000` | add | | `0x10051000` | less (`1`/`0`) |
| `0x10032000` | subtract | | `0x10052000` | less-or-equal |
| `0x10033000` | multiply | | `0x10053000` | greater |
| `0x10034000` | divide | | `0x10054000` | greater-or-equal |
| `0x10035000` | bitwise AND *(conventional label, unconfirmed — see below)* | | `0x10055000` | equal |
| `0x10036000` | bitwise OR | | `0x10056000` | not-equal |
| `0x10037000` | bitwise XOR *(conventional label, unconfirmed — see below)* | | `0x10057000` | logical AND |
| `0x10038000` | bitwise NOT `( a -- ~a )` *(conventional label, unconfirmed — see below)* | | `0x10058000` | logical OR |
| | | | `0x1005A000` | logical NOT `( a -- !a )` |

### Reserved / unassigned slots

Cavedog's own Scriptor compiler (`Compiler.cfg`) and decompiler (`Decompiler.cfg`,
`Defs.h`) — recovered from the *Scriptor v1 (RC1)* source release, the actual
Cavedog COB toolchain, not a third-party clean-room guess — enumerate every
operator their compiler can emit. Cross-checked against those files:

| Opcode | Status |
| --- | --- |
| `0x10035000` | In the operator table as bare `"?"`, priority 20 — the same tier as bitwise OR (`0x10036000`) and adjacent to it numerically. Never given a real BOS keyword by Cavedog (no script, including their own retail corpus, can compile to it), but its grouping strongly favors a bitwise-family binary op (AND, per a common cross-engine convention) over modulo. |
| `0x10037000`, `0x10038000` | **Absent from both `Compiler.cfg` and `Decompiler.cfg` entirely** — not even a placeholder token. Cavedog's own compiler cannot emit these opcodes under any BOS syntax; the decompiler falls back to printing `"UK"` if it ever sees them. Community engines' "bitwise XOR" / "bitwise NOT" labels for these are conventions, not attested by Cavedog's own tools. |
| `0x10039000`, `0x1003A000`, `0x1003B000` | Present in the operator table only as placeholder tokens `"??"`, `"???"`, `"????"` — priority 5 (lower than logical AND/OR). Confirmed reserved-but-unused opcode slots; not previously documented here at all. |
| `0x10059000` | **Does not appear anywhere in Cavedog's Scriptor source** (not in `Defs.h`, not in either `.cfg`). The "logical XOR" label in earlier versions of this doc was an unverified guess (likely borrowed from a community engine's opcode table) with zero corroboration — treat as unconfirmed. |

`0x10063000`, historically claimed as `call-script`'s opcode by the 1998
community note, is in fact `CMD_FAKE_JUMP` — an internal marker the
*decompiler* substitutes in memory for an already-consumed `jump` instruction
while reconstructing `if`/`while`/`break`/`continue`. It never appears in
on-disk bytecode; the note's author most likely misread decompiler output.
Real `call-script` is `0x10062000` (`start-script` is `0x10061000`), matching
retail bytecode.

### TAK-only opcodes (not used by TA)

Scriptor's grammar gates a few opcodes with `Flag=NOTA` ("not available in
TA") — they compile only for *Total Annihilation: Kingdoms* (COB version
signature `6`, detected as `Header.VersionSignature == 6` in the decompiler).
Documented here only so the numbers aren't mistaken for unknown TA opcodes:

| Opcode | Name | Notes |
| --- | --- | --- |
| `0x10072000` | play-sound | `play-sound("name", priority)` — plays a sound directly from a script. Its BOS keyword returns a value that callers usually discard. |
| `0x10024000` | (discard) | Pops and discards a value, used after a `play-sound` call whose result isn't consumed as an expression. Even Scriptor's own decompiler wasn't fully sure of its purpose (comment: `"stop-sound?0"`). |
| `0x10073000` | Mission-Command | `Mission-Command("name", args...)` — single-player mission-scripting hook. |

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

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.


## The BOS language

BOS is compiled, not shipped — but retail data includes sources, and all
community units are written in it. Summary of the language, composited from
stock scripts (`scripts/ARMFLASH.BOS` and the BOS guide's excerpts of
`armbats`, `armsilo`, `armcarry`):

### Declarations and preprocessor

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.


The `piece` list order defines the piece indexes used when a script assigns
a piece *by number* (e.g. `piecenum = 1;` in `QueryLandingPad` refers to the
first declared piece — note this collides with piece-name assignment;
stock scripts use both styles).

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

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.


Axis keywords: `x-axis`, `y-axis`, `z-axis`. For a piece, −Z is its facing
(see [3do.md](3do.md) — the modeling notes say +Z, retail data says −Z);
turns around Y are headings, around X are pitches. `0` position/angle means
the piece's authored rest pose.

**Rotational sense.** A `turn ... to y-axis` word turns a piece the same way
a heading word turns a hull: increasing from +Z toward +X. A renderer that
negates it draws turret traverse, radar dishes and every other yaw-driven
animation backwards, and shows a barrel pointing somewhere other than where
the shot actually leaves from. The synchronized retail ARMSOLAR completion
pose establishes the remaining axes after canonical source-Z reflection: X
uses the opposite of the old source-space sign and Z retains it. Applying the
old negation to both axes opens only two of its four +/-90-degree panels. This
is separate from `move`, whose X axis runs opposite the 3DO model X.

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
[04 §4.4], [04 §4.7], and [R-COB-03 §1–§6].

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

**Correction (2026-08-29).** Earlier text presented a masked-OR implementation
ABI and said retail did not independently expose the pack/unpack arithmetic.
That caveat is superseded: retail uses the signed-add packing and negative-Z
borrow correction established in [R-COB-03 §3]. This format document does not
restate that runtime arithmetic.

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
[R-COB-04 §1].

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
[R-COB-03 §6] and [03 R-FX-01 §3].

### Engine callbacks

COB stores ordinary named functions; it has no callback table or per-function
callback bit. Three distinct facts must therefore remain separate:

1. **Authored convention:** stock BOS scripts use familiar names and helper
   bodies.
2. **Compiler vocabulary:** Scriptor's `COMMON_FUNC` table recognizes some
   names and argument counts for diagnostics/decompilation.
3. **Retail invocation:** only an executable producer proves that the engine
   starts a name. The complete producer, argument, mode, timing, and side-effect
   contract is [R-CB-01 §1–§8].

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

**Corrections (2026-08-29).** `MotionControl(moving, aiming, justmoved)` is a
compiler-recognized signature and an authored convention, but the whole-image
census finds no `MotionControl` occurrence and no retail engine producer.
`MoveRate1`/`MoveRate2`/`MoveRate3` are engine-invoked, but document 04 traces
them to the general movement-tier classifier, not an aircraft-only family.
Names such as `SmokeUnit`, `RestoreAfterDelay`, `OpenYard`, `CloseYard`, and
`Demo` may be authored helpers or script-to-script entries; appearing in BOS
or Scriptor vocabulary does not make them fixed retail callbacks.

## Retail corpus notes

A full decode of every COB in the retail archives (835 scripts across
`totala1.hpi`, `rev31.gp3`, `CCDATA.CCX`, `btdata.ccx`) confirms:

- Version signature is always 4; the reserved header word is always 0;
  the instruction table above decodes **every** retail script with no
  unknown opcodes and no misaligned instruction stream.
- Static-variable counts are small (0–7 covers nearly everything).
- Nine opcodes never occur in retail bytecode: `dont-shadow`
  (`0x1000A000`), bitwise AND/XOR/NOT (`0x10035000/7000/8000`), logical
  XOR (`0x10059000`), `greater`/`greater-or-equal` (`0x10053000`,
  `0x10054000`), and the two transport reads `0x10044000` / `0x10045000`
  documented under "Values and variables". Notably these include every
  historically contested slot — retail data cannot arbitrate them.
  (`greater`=`0x10053000`, `greater-or-equal`=`0x10054000` per Scriptor's own
  `Compiler.cfg` operator table — an earlier version of this doc had the two
  swapped.) The count was seven before the two transport reads were traced to
  the interpreter and censused.
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

- **Historical opcode disagreements — now resolved against Cavedog's own
  tools.** The 1998 command note assigns `call-script` to `0x10063000`; that
  value is actually `CMD_FAKE_JUMP`, a decompiler-internal marker, never a
  real bytecode opcode (see "Reserved / unassigned slots" above). Real
  `call-script` is `0x10062000` (`0x10061000` = start-script), confirmed by
  both retail bytecode and Scriptor's own `Defs.h`. The note also labels
  `0x1005A000` "bitwise NOT"; Scriptor's compiler config confirms it is the
  *logical* NOT (`!`/`NOT`, unary prefix, highest priority) — stock control
  flow (busy-wait on `!ready`) only works under that reading. `0x10038000`
  has no confirmed role at all (see below).
- **Modulo vs. bitwise AND at `0x10035000`:** no longer purely a community
  guess. Scriptor's own operator table places it, unnamed except for a bare
  `"?"` placeholder, at the same priority tier as bitwise OR — grouping that
  favors a bitwise-family op (AND, matching a common cross-engine
  convention) over modulo, though Cavedog never wired a real keyword to it
  either way.
- **`0x10037000`, `0x10038000`, `0x10059000`:** unlike the slots above,
  these have *zero* footprint in Cavedog's own Scriptor source — no opcode
  name, no operator-table entry, nothing. Retail data never exercises them
  either. Their conventional "XOR" / "bitwise NOT" / "logical XOR" labels
  (this doc included, historically) are unverified conventions borrowed from
  other engines' opcode tables, not attested by any Cavedog source seen so
  far.
- **`attach-unit` / `drop-unit` stack shapes** are established from retail
  bytecode (see the instruction table): all 48 retail call sites are uniform,
  including the `piece = -1` idiom, and the compiler-supplied third value is
  always zero. Its engine-side consumer is established in [R-COB-03 §5].
- **Sleep and wait runtime behavior** is established in [04 §4.2] and
  [04 §4.6]. This file retains only the opcode and authored-duration encoding.
- The header word at 0x28 (first-script-name pointer) has no known runtime
  purpose.
- **Loader edges (2026-08-29, RWU-02-3).** The file is read whole; a
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
- Scaling constants verified against the retail
  `ARMFLASH.BOS`/`ARMFLASH.COB` pair from `totala1.hpi`; container examples
  from `CORTRUCK.COB`; corpus statistics from decoding all 835 retail COBs.
