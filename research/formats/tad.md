# TAD — TA Demo Recorder recordings

## Overview

`.tad` files are match recordings produced by the community **TA Demo
Recorder** (TADR, by Fnordia/SJ/Yeha, distributed as a replacement
`dplayx.dll`). The recorder intercepts Total Annihilation's multiplayer
session traffic and writes a timed log of intercepted TA traffic plus lobby
metadata. Recorder options can filter traffic; a file is not necessarily a
complete packet log. Because TA multiplayer is an *asynchronous*
peer-to-peer model — each client simulates its own units and broadcasts
resulting unit state — a recording is primarily **state telemetry** (unit
sync, resource status, combat events) rather than a player-input log.
Replay tools work by re-feeding this traffic into a synthetic multiplayer
session, not by deterministic re-simulation.

`.tad` recordings are useful observational evidence for classic-engine
behavior. They are not an authoritative engine state format.

**Not a retail input.** The retail executable neither reads nor writes
`.tad` files; the malformed-input matrix of `[02 R-MALF-01 §2]` lists the
format only to say so. The file wrapper and Smartpak transforms are contracts
with third-party recorders. Underlying retail wire behavior is identified
separately and cited to doc 08.

**Scope:** single-player saves use the retail save-bank contract in doc 08,
not this format. Nanolathe contains no TAD parser, recorder or replay engine.
References below to a corpus analyzer (including its Rust implementation)
describe external tooling, not repository packages or checks in this Go engine.
The nine-file private corpus was located; framing, encrypted status records
and match-body subpacket streams were independently checked. Retail writers
and readers establish the field corrections below. Historical statistical
interpretations remain separate from those traces and are retained only where
they do not depend on a disproved field assignment.
Corpus observations can support an inference about retail behavior; they do
not establish a simulation contract. Undecoded fields remain **Unknown**.

This inherited third-party format description covers the file envelope, the TA wire-packet encodings
(XOR/checksum, LZ77 compression), the recorder's "smartpak" re-encoding of
unit-sync packets, and the subpacket taxonomy. It is an inherited description
for third-party tooling; catalog- and recorder-dependent gaps below must be resolved before claiming
a universal compatible reader.

Two related identifiers appear inside recordings and must not be confused:

- the **file format version** (u16 in the header; 5 for every corpus file
  we hold, including 2020 recordings), and
- the **recorder version string** (an extra-sector string such as `0.99`,
  `0.99.3.513`, or `3.9.2.0`) identifying the TADR build.

## Format at a glance

```
file := record*                     every record: u16le totalLength
                                    (totalLength INCLUDES the 2 length bytes)

record sequence:
  1        header        magic "TA Demo\0", version, numPlayers, maxUnits, map
  1        extraHeader   { i32 numSectors }                        (version 5)
  n        extraSector   { i32 type, bytes data }                  (version 5)
  players  player        { u8 color, u8 side, u8 number, name }
  players  statusMsg     { u8 number, TA-encrypted 0x20 PLAYER_INFO packet }
  1        unitData      concatenated 14-byte 0x1a unit-data subpackets
  many     packet        { u16 dtMs, u8 sender, u8 flag, payload }

packet payload:
  flag 0x03: payload = subpacket stream
  flag 0x04: payload = LZ77 stream; decompressed = subpacket stream

subpacket stream := TA subpackets (fixed sizes per id, table below) with
  0x2c unit-sync packets re-encoded by the recorder ("smartpak"):
    0xfe <u32 tick>   sets the running tick counter
    0xfd <u16 len> <body>   a 0x2c with its u32 tick elided (tick = counter++)
    0xff              an idle 0x2c (body ffff 0100)   (tick = counter++)
```

## Reference

### 1. Record framing

The entire file, from byte zero to EOF, is a sequence of records introduced
by a `u16le` **total length that includes the two length bytes themselves**.
The historical analyzer reported that all five corpus files tile exactly
from byte 0 to EOF with zero gaps
(7,247 / 33,145 / 55,987 / 32,631 / 108,307 records).

### 2. Header record

```
u16   length
char  magic[8]      "TA Demo\0"
u16   version       3 = TADR 0.80b, 4 = 0.81a, 5 = 0.90b and ALL later
                    recorders through at least 2020 (corpus range)
u8    numPlayers
u16   maxUnits      per-player unit-slot count (500, 785 or 1500 observed);
                    ABSENT in version < 5 header (no maxUnits field)
char  mapName[]     NUL-terminated
```

Worked example (corpus, Gods of War 2005):

```
1a00 5441 2044 656d 6f00 0500 02 f401 "Gods of War"
len   T A   D e  m o \0  v=5  2  500
```

**Unknown:** versions beyond 5 are outside this description; a reader
implementing only this contract should report them as unsupported. Treat
versions 3 and 4 separately because their headers omit `maxUnits`; the four
extra packet bytes described in §7 are attributed specifically to version 3,
not to every version below 5. The listed corpus contains only version 5, so
older layouts require source- or file-based validation before use.

### 3. Extra sectors (version 5)

After the header: one record `{ i32 numSectors }`, then `numSectors`
records `{ i32 sectorType, bytes data }`:

| type | content |
| --- | --- |
| 1 | comments (free text, may repeat) |
| 2 | chat log (plaintext; **privacy-sensitive**) |
| 3 | recorder version string (`0.99.3.513`, `3.9.2.0`, ...) |
| 4 | recording date string (`02.03.2005`) |
| 5 | recorded-from string (`TLobby.Connect`, ...) |
| 6 | one per player: lobby address, XOR-0x2a masked (**contains IP addresses — privacy-sensitive; never emit raw**) |

### 4. Player and status records

The file sequence above groups all player records before all status records;
the two record shapes are:

```
player     { u8 color, u8 side (0=ARM 1=CORE 2=watcher), u8 number, char name[] }
statusMsg  { u8 number?, TA packet }   -- a full ENCRYPTED custom envelope (§5)
                                          containing a transport sequence then
                                          a 0x20 PLAYER_INFO subpacket
```

The 0x20 PLAYER_INFO subpacket (192 bytes raw) carries map name, map hash,
map size (u16 width/height), maxUnits, TA version major/minor, clicked-in
state, watcher/cheats/permanent-LOS flags, side, color/slot, and an **is-AI
flag** — a recorded per-player setup message; it does not turn the recording into
authoritative engine state. Offsets (0-based
within the raw subpacket, from ta-forever's parser): width @140, height
@142, player1Id @145, clicked @156, maxUnits @166, versionMajor @168,
versionMinor @169, player2Id @187.

### 5. TA packet encryption and compression

**Established — retail transport has both a direct game-record path and a
custom envelope path** [08 "Custom and direct wire paths"]. The encrypted
status records observed here use the custom envelope; do not apply this header
to every possible DirectPlay message.

```
u8   flag        0x03 plain, 0x04 compressed
u16  checksum    sum of the transformed bytes in the range below, modulo 65536
u8   payload[]   optionally compressed, then partly XOR-masked
```

**Established — custom wire transformation.** Let `W` be the complete wire
message length and `i` a zero-based byte index from its flag. For each
`3 <= i < W - 3`, XOR byte `i` with `i mod 256`. The checksum is the sum of
those transformed bytes, reduced to 16 bits. The final three wire bytes are
neither XORed nor included in that sum; they remain part of the payload, not
an extra trailer to discard. Decryption uses the same index and range. For
flag `0x04`, decompress the entire decrypted payload after the three-byte
header. The resulting payload starts with a four-byte transport sequence,
followed by game records. This sequence is distinct from the unit-sync tick.
[08 "Custom and direct wire paths"]

**Established as a corpus observation.** All 22 status messages in the nine-file
follow-up pass the checksum rule above. Ten are plain and twelve compressed;
each decodes to sequence `-1` followed by exactly one 192-byte `0x20` record.
The decoded major/minor bytes are `3, 1` and each status `maxUnits` matches its
file header, including the 785-slot file. These bytes do not identify a precise
patch build. The alternative XOR mask `i - 1` fails the expected decoded
shape in every status. This closes the earlier index-phase ambiguity.

**Stored match packets differ.** The inherited recorder description says the
body form is `flag + subpacket stream` (compressed for flag `0x04`), with the
wire checksum, masking and transport sequence removed; it additionally
replaces unit-sync records with Smartpak forms (§7). Only the status-message
records above retain the encrypted custom envelope. The later match-body rerun is described under Corpus validation; it checks
framing and selected field relationships, not every historical analysis.

Compression (`flag 0x04`) is LZ77 with control bytes, applied to the
subpacket stream:

- read a control byte; process its 8 bits LSB-first;
- bit = 0: copy one literal byte to the output;
- bit = 1: read `u16le w`; `offset = w >> 4` is a **1-based index into the
  output produced so far**; `offset == 0` terminates the stream;
  copy `(w & 0x0F) + 2` bytes starting there (copies may overlap forward —
  RLE-style; the source region may extend into bytes produced by the copy
  itself).

Encoded match lengths are 2..17 bytes; the compressor chooses matches of at
least 3 bytes. The 12-bit field is an absolute 1-based index into the first
4095 output bytes, not a backward distance. The inherited recorder-source
description says its compressor gives up
beyond input offset 2000. This is an encoder policy, not a decoder offset: the
compressed status payload begins immediately after the wire header, with no
two-byte uncompressed prefix.

### 6. Subpacket taxonomy

After decompression the payload is a concatenation of subpackets. Sizes
include the one-byte ID. The table retains the inherited recorder-reader
lengths; audited retail meanings are identified in §6.1–§6.5. **Unknown —
length strata:** the inspected retail executable declares lengths 3, 18 and
186 for types `0x03`, `0x13` and `0x20`, respectively, whereas the inherited
recorder taxonomy gives 7, 19 and 192. The nine-file corpus contains 22
192-byte initial status messages and 63 further 192-byte `0x20` records,
but no `0x03` or `0x13` match records. Do not silently replace observed lengths
with the retail table or assume the larger forms are universal retail layouts.
A version-matched recorder transformation or executable writer must explain
these differences. Major/minor bytes `3,1` do not identify that writer.
[08 "Declared packet types"]

| id | size | name / meaning |
| --- | --- | --- |
| 0x00 | run of zero bytes | padding (absorb consecutive zeros) |
| 0x02 | 13 | ping (from, id, value u32s) |
| 0x03 | 7 | unknown |
| 0x05 | 65 | chat: NUL-padded text `<sender> text` (older recorders can overflow; if last byte != 0 the chat is the whole rest of the stream, minus a trailing 5-byte 0xfc if present). **Privacy-sensitive.** |
| 0x06 | 1 (42 in lobby traffic) | pad/encrypt marker |
| 0x07 | 1 | unknown |
| 0x08 | 1 | loading started |
| 0x09 | 23 | **unit build started**: u16 unitTypeId @1, u16 netId @3, u32 x @7, u32 y @11, u32 z @15 — TA convention (x east, y up/height, z south); values are consistent with map-pixel units. Corpus shows the packet can be sent twice for the same netId (dedupe on netId + tick window). |
| 0x0a | 7 | unknown (netId + ff 01 pattern) |
| 0x0b | 9 | **Damage:** victim ID, attacker ID, amount, hit direction and damage kind; established retail fields in §6.1. |
| 0x0c | 11 | **Death:** victim ID, attacker-player transport identity, attacker-unit ID, severity and packed cause/variant; §6.2. |
| 0x0d | 36 | **Projectile creation:** position, target/velocity triple, weapon identity and class-dependent tail; §6.3. |
| 0x0e | 14 | **Projectile impact/removal:** stored target triple and weapon ID; §6.3. |
| 0x0f | 6 | **Feature transition/damage:** code plus two tile-coordinate words; §6.4. |
| 0x10 | 22 | **Run authored script:** unit ID, signed script index, argument count and four complete argument words; §6.5. |
| 0x11 | 4 | unit state: u16 netId @1, u8 state @3. State values: 0=off, 1=on, 2+=other. Field analysis against the corpus. |
| 0x12 | 5 | **unit build finished**: u16 built netId @1, u16 builder netId @3 |
| 0x13 | 19 (recorder taxonomy; retail 18) | Play sound. Retail has selector u8 at 1, sound identity i32 at 2, XYZ i32 at 6/10/14. Position is consumed only for selector zero; both audited emitters send selector one. Recorder extension remains unverified. |
| 0x14 | 24 | give unit (also appears in unit-data contexts) |
| 0x15 | 1 | start |
| 0x16 | 17 | share resources: u8 kind?, u32 fromDpId, u32 toDpId, f32 amount |
| 0x17 | 2 | unknown |
| 0x18 | 2 | host migration |
| 0x19 | 3 | **game speed**: u8 mode (1 = user set) @1, u8 speed+10 @2 |
| 0x1a | 14 | unit data: u8 sub @1, u32 fill @2, u32 unitTypeCrcId @6, then {u16 status, u16 limit} (sub=3) or {u32 crc} (sub=2); id 0xffffffff carries the overall unit-list CRC in `fill` |
| 0x1b | 6 | reject (u32 dpId) |
| 0x1e | 2 | start |
| 0x1f | 5 | unknown |
| 0x20 | 192 (observed recordings; retail 186) | Player info; §4. Do not transfer the recording offsets to the shorter retail layout without a version/recorder mapping. |
| 0x21 | 10 | unknown |
| 0x22 | 6 | ident3 (u32 dpId, u8 number) |
| 0x23 | 14 | ally: u32 fromDpId @1, u32 toDpId @5, u8 alliedFromWithTo @9, u32 alliedToWithFrom @10 |
| 0x24 | 6 | team (u32 dpId, u8 team) |
| 0x26 | 41 | ident2 (10 × u32 dpIds) |
| 0x28 | 58 | **player resource info** (see §8) |
| 0x29 | 3 | Synchronization control/reply: flag at 1, responding participant's per-peer state flag at 2. The `0x28` reply sets the first flag; complete handshake lifecycle remains Unknown. [08 "Economy and integrity checks — overwrite-sync, not compare"] |
| 0x2a | 2 | loading progress percent |
| 0x2c | u16le @1 | **unit stat and move** (see §7) |
| 0x2e | 9 | unknown |
| 0x42 | u16le @1 + 3 | "Thaldren extended" (later-patch extension) |
| 0xf6 | 1 | unknown |
| 0xf9 | 73 | recorder: enemy-chat relay (u32 from, u32 to, text) |
| 0xfa | 1 | recorder/replayer marker |
| 0xfb | u8 @1 + 3 | recorder: data connect |
| 0xfc | 5 | recorder: sender's camera/minimap position (presentation-only) |
| 0xfd | u16le @1 − 4 | smartpak: 0x2c with elided tick (§7) |
| 0xfe | 5 | smartpak: set tick counter (u32le @1) |
| 0xff | 1 | smartpak: idle 0x2c (§7) |

**Supported inference from the cited recorder taxonomy:** ids ≥ `0xf6`
are recorder constructs; do not infer retail wire behavior from those ids.
`0xfc` map-position packets and all chat ids are presentation/session data
and must never feed gameplay-state analysis; chat additionally must be
excluded from committed artifacts.

### 6.1 Damage record

**Established — retail fields, zero-based packet offsets:**

| Offset | Type | Meaning |
| ---: | --- | --- |
| 1 | u16 | Victim unit ID; zero is rejected by the receiver. |
| 3 | u16 | Attacker unit ID; zero means no attacker. |
| 5 | 16 bits | Low word of scaled amount: signed for ordinary health subtraction, unsigned for paralyze and heal. |
| 7 | u8 | High byte of impact-relative direction; ordinary weapon damage expands it by shifting left eight for `HitByWeapon`. |
| 8 | u8 | Damage kind: 1 ordinary weapon damage, 2 paralyze, 6 default cargo-cascade damage, 10 healing. Other kinds follow the damage contract. |

The direction byte is not a damage multiplier, and kind 6 is not paralyze.
Scaling, signedness, reaction order and kind-specific branches belong to
[06 §9.1] and [06 §12.1]. **Established corpus observation:** the rerun sees
kinds 1, 3, 5 and 6 across nine recordings and no kind 2. The earlier claim
that this corpus contained only kinds 1 and 6 was incomplete.

### 6.2 Death record

**Established — retail fields:** offset 1 is victim ID u16; offset 3 is the
attacker player's transport identity u32; offset 7 is attacker unit ID u16;
offset 9 is signed severity i8; offset 10 packs death cause in the high nibble
and corpse-depth/variant output in the low nibble. The transport identity comes
from the damage-time attacker-side snapshot; absent or invalid attribution is
all-one bits. It is separate from the attacker unit ID, which may be zero.
The receiver resolves these two identities independently. Neither offset 9 nor
the packed byte is a weapon ID or wreck probability. Death credit, script
severity and corpse interpretation follow [06 §12.1].

### 6.3 Projectile creation and impact

**Established — ordinary unit-shot `0x0d` layout:**

| Offset | Type | Meaning |
| ---: | --- | --- |
| 1, 5, 9 | i32 each | Signed 16.16 origin X, Y, Z. |
| 13, 17, 21 | i32 each | Signed 16.16 target point X, Y, Z in unit-shot paths. |
| 25 | u8 | Authored weapon ID. |
| 26 | u8 | Bit 0 is the interceptor flag; the upper seven bits are ignored by the receiver. |
| 27, 29 | 16 bits each | Stored weapon-slot yaw and pitch. |
| 31 | u16 | Target unit ID. |
| 33 | u16 | Shooter unit ID. |
| 35 | u8 | Zero-based weapon-slot index. |

The unit-shot writers replace only bit 0 of the flags byte and do not initialize
its other bits. Thus observed bytes `0x00` and `0xfe` both mean interceptor
clear; they do not form an air-launch enumeration. Origin is producer-specific:
one free-shot path sends the unit's position even though its local projectile
creator received a muzzle point. The old universal muzzle claim and reversed
shooter/target labels were incorrect. Historical cadence/angle fits based on
those labels are withdrawn pending a corrected analysis.

**Established — meteor exception:** the weapon definition selects the meteor
path. Its second triple carries velocity, not a target point; the receiver
bypasses the unit/angle tail. The meteor producer initializes the two triples
and weapon ID without defining the unused tail. This packet cannot be decoded
semantically from length alone. [06 §6.5]

**Established — `0x0e`:** three signed 16.16 stored target coordinates begin at
1, 5 and 9, followed by weapon ID at 13. The receiver finds the first projectile
in pool order matching that stored triple and weapon ID, then invokes its
impact/removal path. It does not encode an area radius. [06 §11.2]

### 6.4 Feature record

**Established:** offset 1 is a code u8, offset 2 anchor tile X u16, and offset
4 anchor tile Z u16. Codes `0xfd`, `0xfe` and `0xff` request death transition,
ignition and reclaim transition, respectively; other code values select a
weapon definition for feature damage. Byte 4 is part of Z, not an independent
action byte, and byte 5 is not padding. [05 R-FEAT-01 §8] [05 R-FEAT-01 §9]

### 6.5 Script record

**Established:** offset 1 is unit ID u16; offset 3 is authored script index
i16; offset 5 is argument count u8; offsets 6, 10, 14 and 18 are four complete
32-bit argument words. The receiver starts the script deferred, writes all four
physical argument cells and sets the logical top from the argument count.
Offset 8 is part of the first argument, not an independent unknown field;
zero-valued argument bytes are not protocol padding. The count is not an
execution mode. [04 R-COB-01 §1] [04 §5.3]

### 7. Unit sync — 0x2c and the smartpak re-encoding

TA broadcasts each player's unit state via 0x2c packets:

```
u8   0x2c
u16  length        total, including these 3 bytes and the tick
u32  tick          sender's game tick; tick mod maxUnits is the scheduled
                   status-scan slot
bits body[]        zero or more movement entries, an all-ones 16-bit
                   movement-list terminator, then scheduled status
```

**Established — retail bit convention and context.** Fields are packed
least-significant bit first with no alignment between entries. Signed fields
use two's-complement bits. Let `C` be the retained unit-definition row count,
including reserved row zero; the definition-index width `W` is its bit length
(the number of right shifts until zero). At an exact power of two, this is one
more than `log2(C)`. `maxUnits` does not determine `W`. Matching catalog identity
is needed both for index meanings and movement-family selection.
[08 "Unit-sync ownership and body structure"]

**Established — identity and repetition.** Unit ID zero is reserved; block `b`
owns IDs `b*maxUnits+1` through `(b+1)*maxUnits`, inclusive. For nonzero IDs,
`blockBase=((ID-1)/maxUnits)*maxUnits` and `localSlot=(ID-1) mod maxUnits`.
Read a local-slot u16 after the tick and after every movement payload. All-one
bits end the list; otherwise a `W`-bit definition index follows. The referenced
definition's `canfly` selects the ground or air grammar below. A definition
mismatch is repaired before the receiver invokes the corresponding movement
reader. Block assignment is separate from recording sender order.

#### Ground movement entry

**Established:** after local slot and definition index, read blocked flag 1 bit,
point count 2 bits, then that many pairs of signed X16/Z16. Count is 0..3. The
writer takes up to the first three retained path points when its path-active
flag is set and otherwise sends zero points. The receiver expands the integer
coordinates to 16.16 by shifting left 16. These points are an ordered path
prefix; the format does not require the first pair to be the current position.
Total entry length is `16+W+3+32*pointCount` bits. The next local-slot marker
follows immediately, including after a zero-point blocked-state update.

#### Air movement entry

**Established:** after local slot and definition index, read a 2-bit selector.
Each form ends with a 2-bit movement-state value after its selected payload:

| Selector | Payload, in wire order |
| ---: | --- |
| 0 | No subordinate-controller payload. |
| 1 | Flags u8, followed by conditional fields in the order below. |
| 2 | Turn-enabled 1 bit; position XYZ signed fixed32; velocity XYZ signed fixed32; target heading u16 only when turn-enabled is set. |

Selector 1 conditional fields, in this exact order:

| Flag bit | Additional fields |
| ---: | --- |
| 0 | Signed attachment piece i16, then target unit ID u16 (zero is null). |
| 4 | Signed scalar i16; precise meaning Unknown. |
| 3 | Signed vertical offset i16. |
| 6 | Signed relative heading i16. |
| 5 | Position XYZ signed fixed32. |

The other flag bits add no fields in the audited pair of routines. Their full
meaning remains **Unknown**. The receiver treats selector 3 as no subordinate
payload, but the audited sender does not deliberately emit it. Unsupported
controller kinds can omit the selector entirely; proving those states
unreachable requires a separate lifecycle trace. Do not invent a selector-3
encoding for that sender branch.

#### Scheduled status

**Established:** after the movement-list terminator, read presence 1 bit. The
audited writer always sets it; the receiver reads no status when it is clear.
The scheduled local slot is `tick mod maxUnits`. When present, read definition
index in `W` bits. Zero ends the status and identifies an empty slot. Otherwise:

| Field | Width |
| --- | ---: |
| Health (signed word) | 16 bits |
| Construction remaining byte | 8 bits |
| Activation/status flags | 8 bits |
| Unit-state value | 2 bits |
| Attached flag | 1 bit |

For construction remaining fraction `r`, the sender writes zero when `r==0`;
otherwise it writes `low8(1-trunc(r * -254.0))`, multiplying the stored binary32
fraction by binary32 `-254.0` at working precision before truncation. For valid
`0<r<=1`, the result is 1..255; 255 means entirely unbuilt. The receiver
multiplies the unsigned byte by the binary32 reciprocal-of-255 constant and
stores the result as binary32 when changed. This is a remaining fraction, not
a completed percentage. Status flags use the ordinary transition helper;
bit 0 drives Activate/Deactivate and bit 3 StartBuilding/StopBuilding.

If attached is set, read attached-to unit ID in 15 bits and signed attachment
piece in 8 bits; that ends the status. Otherwise read position XYZ as three
signed fixed32 values, then three angle16 values (heading followed by two
orientation components). Finally, read one fixed32 movement scalar **only if
that unit has movement state**. Ordinary creation allocates it for `BMcode==1`,
but actual lifecycle state is the precise condition; there is no wire presence
bit for this final word. A reader lacking that context must report ambiguity.
The other orientation-axis names and movement scalar's full semantics remain
**Unknown** in this bounded packet trace.

Without movement entries, health begins at absolute bit `73+W` from the full
reconstructed packet. An empty status occupies `ceil((73+W)/8)` bytes. The
historical eleven-byte idle shape and fixed byte/bit health offset therefore
depend on catalog width. The sender rounds the full record to bytes. Its
movement scan can stop after a completed entry takes the rounded byte count
above 511, but still appends terminator and scheduled status; 512 is not a
universal final-size cap. [08 "Unit-sync ownership and body structure"]

The recorder shrinks the dominant 0x2c traffic ("smartpak"):

- the first 0x2c in a bundle emits `0xfe + its u32 tick`;
- every 0x2c is rewritten as `0xfd + u16 length + body` with the 4 tick
  bytes removed; the length field keeps the ORIGINAL 0x2c length, so the
  chunk occupies `length − 4` bytes and the body is `length − 7` bytes;
- the recorder's eleven-byte idle `0x2c` form becomes the single byte `0xff`;
  compatibility with a different catalog-width idle length remains unverified;
- each reconstructed 0x2c consumes one tick: `tick = counter++`.

Reconstruction: `2c <origLen u16> <tick u32> <body>`.

A recorder option (`onlyunits`) drops all non-0x2c subpackets from the
recording; per-file subpacket coverage must therefore be measured, not
assumed.

**Version 3 files** (TADR 0.80b): each stored packet has four extra bytes
between the flag and the subpacket stream (skipped by all later readers).

### 8. Participant resource and score snapshot — 0x28

**Established — retail producer and receiver:** this is a 58-byte participant
snapshot, with fields below at zero-based packet offsets. The four leading
counters are sign-extended from signed 16-bit state to signed 32-bit wire
values; the receiver keeps their low 16 bits. The next four fields preserve
binary32 bits. The final six narrow binary64 running totals to binary32 and
are widened back by the receiver. [08 "Economy and integrity checks — overwrite-sync, not compare"]

| Offset | Type | Field |
| ---: | --- | --- |
| 1 | u8 | Echo/request flag. |
| 2 | i32 | Kills. |
| 6 | i32 | Losses. |
| 10 | i32 | Commander kills. |
| 14 | i32 | Commander losses. |
| 18 | f32 | Current metal stock. |
| 22 | f32 | Current energy stock. |
| 26 | f32 | Metal storage capacity. |
| 30 | f32 | Energy storage capacity. |
| 34 | f32 | Cumulative energy produced. |
| 38 | f32 | Cumulative energy requested. |
| 42 | f32 | Cumulative energy wasted. |
| 46 | f32 | Cumulative metal produced. |
| 50 | f32 | Cumulative metal requested. |
| 54 | f32 | Cumulative metal wasted. |

The requested totals use save keys `TotalEnergyConsumed` and
`TotalMetalConsumed`, but represent requested work rather than successfully
paid work [05 R-ECO-01 §6]. The final field is not an income rate. The old
metal/energy labels on offsets 34 and 38 and the unknown-family labels on the
remaining totals were wrong; numerical trend alone did not establish them.

**Established — retail ownership:** the writer sends the local participant's
snapshot with that participant's transport identity as source. The receiver
resolves that source participant and overwrites its fields, subject to the
existing synchronization gate; it does not search the prefix for an owner ID.
A nonzero flag can cause a control reply and a snapshot from the answering
participant. The first 17 bytes therefore encode a flag and four score
counters, not unit references or a ledger identifier. Joining a recording's
sender number to that retail participant or a unit-ID block still needs the
recorder/session mapping; packet bytes do not supply that join by themselves.

**Established corpus observation:** the independent rerun finds 14,506
snapshots in the original five files and 19,117 across all nine. All ten float
fields are finite; all four signed counter words equal sign-extension of their
low 16 bits. Each of the six corrected cumulative fields is nondecreasing in
same-file, same-sender capture order. The earlier prefix-cardinality/net-ID
interpretations are superseded by the retail counter writers. Recorded wall
intervals remain capture timing, not exact simulation intervals, and no
per-second income or conservation rule follows from these checks.

### 9. Time model

Three clocks appear:

1. **Record deltas** (`dtMs`): wall-clock milliseconds since the previous
   record at the capturing peer, quantized by that machine's timer
   (observed ~33 ms multiples in one file, ~15.6 ms in another).
2. **0x2c ticks** per sender: sim-frame counter (~30 Hz nominal, subject to
   the 0x19 speed packets).
3. **0x19 speed changes**: u8 `speed + 10` (e.g. 0x0b = +1).

Analyses should prefer ticks for sim-time and use record deltas only for
wall-clock alignment.

Non-sync packets have no point tick in this format. The analyzer brackets a
lifecycle packet only when preceding and following Smartpak syncs from the
same sender exist in the same uninterrupted clock and speed segments. The
inclusive `[previous, next]` range is capture-order evidence, not an assertion
that the event happened at either endpoint or at a particular engine tick.

The historical external analyzer's bounded Theil–Sen fits, separated by sender, clock
continuity segment, and 0x19 segment, put the dominant natural-play segments
near 30 reconstructed ticks per recorder wall second in all five corpus files.
Painted Desert contains late mode-1 steps through signed speeds +1 to +10; its
three sufficiently sampled +10 sender segments fit at roughly 58.5–59.0
ticks/s. Within the owner's rebuttable `presumed_3_1c` admission this is target
evidence for a speed-dependent clock interpretation, but it does not by itself
identify the exact Windows 3.1c speed law because `dtMs` measures the capture
peer rather than authoritative simulation time.

## Corpus validation

**Established — follow-up envelope validation.** The private
`tada-ota-3.1` corpus contains nine files; all nine byte lengths and SHA-256
hashes match its manifest. Independent length-prefix scanning tiles each file
exactly to EOF. The five original record counts and their header fields in the
table below reproduce exactly. The later independent match-body scan
reproduces the five known checksum
markers and finds no other splitting failures. Historical field interpretations
are superseded where the retail trace above contradicts them; unrelated timing
fits remain inherited, not rerun.

| map | header | recorder | players | maxUnits | records | subpacket errors |
| --- | --- | --- | --- | --- | ---: | --- |
| Gods of War | v5 | 0.99.3.513 | 2 | 500 | 7,247 | 0 |
| Painted Desert | v5 | 0.99ß2 | 2 + watcher | 1500 | 33,145 | 5 (see quirk below) |
| fox holes | v5 | 3.9.2.0 | 4 | 1500 | 55,987 | 0 |
| lava mania | v5 | 3.9.2.0 | 2 | 1500 | 32,631 | 0 |
| red triangle | v5 | 3.9.2.0 | 3 | 1500 | 108,307 | 0 |

The four additional files extend the directly checked envelope sample:

| map / recording date | header | recorder string | players | maxUnits | records |
| --- | --- | --- | ---: | ---: | ---: |
| Gods of War / 2001-02-09 | v5 | 0.97ß | 2 | 785 | 30,276 |
| Sail Away / 2003-06-24 | v5 | 0.99ß2 | 2 | 500 | 26,809 |
| Hundred Isles / 2003-07-13 | v5 | 0.99ß2 | 2 | 500 | 8,553 |
| Painted Desert / 2010-03-22 | v5 | 1.0.0.545 | 2 | 500 | 31,757 |

Dates identify manifest entries; recorder strings above come from their type-3
sectors. The manifest normalizes some strings (for example `0.99ß2` to `0.99`),
so use the stored sector when distinguishing recorder builds. The 785-slot
observation rules out treating the two original slot sizes as an enumeration.
The sample still contains no format-version 3 or 4 file.

The independent status-message check covers all 22 participants; §5 gives its
checksums, compression split and decoded header relationships. It emits no
player names, lobby addresses, chat, or raw packet dumps.

**Established — independent match-body rerun.** A bounded decoder using the
recording length strata above splits 3,185,002 subpackets across nine files,
including Smartpak records. It excludes exactly the five known checksum-error
records below. The original five files yield 73,180 `0x0d` records; all nine
contain 148,141. There are no `0x03`, `0x0e`, `0x13` or `0x14` records in this
sample. The resource-field checks are in §8. Successful framing establishes
these sample observations, not all malformed-input behavior or historical
combat-fit conclusions.

Historical packet-level cross-checks (the following timing/lifecycle fits were
not rerun by this follow-up):

- **Tick reconstruction largely reconciles in the historical sample.**
  Predicting the tick counter by counting
  0xfd/0xff chunks re-synchronizes with the next explicit 0xfe value in
  >99.9% of ~219,000 checks across the corpus; the residual deltas are +6
  (one missing bundle) with a handful of larger jumps. Since Painted Desert
  is 62% compressed records, this supports that decoder on the observed
  compressed streams; it does not prove every LZ77 edge case or account for
  the remaining discontinuities.
- **Sides and slots decode.** Player records give side 0/1/2 (the Painted
  Desert third participant is side 2 = watcher, matching archive metadata).
- **Commander types match side detection.** The first 0x09 per player is
  the commander: unitTypeId 36 for ARM, 169 for CORE — inside TADR's own
  ARM $21..$24 / CORE $a4..$a8 detection windows.
- **NetId blocks confirmed.** First units appear at netId 1, 501 (maxUnits
  500) or 1, 1501, 3001 (maxUnits 1500); block base order follows lobby
  order, not sender index.
- **Start coordinates are inside map extents** with the middle field a
  plausible terrain height (e.g. commanders at (3840, 85, 1872) and
  (336, 85, 3696) on Gods of War).
- **Lifecycle counts reconcile with archive aggregates**: builds/deaths
  132/131 (GoW), 1113/1113 (PD), 688/686 (FH), 603/398 (LM),
  4704/4705 (RT) vs archive-derived unit totals ~116/1014/622/567/4424
  (differences: duplicate 0x09 packets and rebuilt netIds; LM's low death
  count reflects the match ending with armies intact).
- **Stratum variance is real**: fox holes contains zero compressed records;
  Painted Desert is compression-heavy; 0xfc map-position volume varies by
  orders of magnitude between files.

Quirk: five Painted Desert records contain the recorder's checksum-failure
marker. The source string is `<error in checksum?!?!>`; the stored body bytes
in this corpus begin with its tail `in checksum?!?!>` and end with a recorder
camera marker. TADR 0.99ß2 wrote this marker when an intercepted wire packet
failed its checksum. Parsers recognize both spellings and exclude them rather
than treating either as a subpacket stream.

## Unknowns and caveats

- **Unknown:** recording-specific definition-table identity and width, complete
  dynamic movement-state lifetime, unsupported air-controller reachability,
  selector-1 scalar/flag semantics, the status unit-state value, two orientation
  component names and the final movement scalar's complete meaning. The audited
  grammar in §7 establishes widths and branches; these remaining questions need
  the corresponding catalog/lifecycle or semantic consumer trace.
- **Unknown:** why recorder player-info records contain 192 bytes while this
  retail writer emits 186; and whether the inherited 7-byte `0x03` and 19-byte
  sound forms belong to other writers. Source or executable-version evidence
  must reconcile these strata. Empty-sync Smartpak handling outside the
  observed catalog width is also unverified.
- **Unknown:** remaining untraced control/lobby records (`0x03`, `0x07`,
  `0x0a`, `0x17`, `0x1f`, `0x21`), the full ownership-transfer
  `0x14` layout, and later recorder/patch records (`0x2e`, `0xf6`, `0x42`).
  The complete `0x28`/`0x29` synchronization handshake also remains open.
  Existing category-08 roles are the starting point for each consumer trace;
  an inherited unknown label is not evidence that retail ignores a record.
- **Unknown:** an exact recording-sender-to-retail-participant/block mapping
  for each session; the resource prefix is now established score data, not a
  candidate owner ID. Cadence, conservation and weapon-fit analyses must use
  corrected field identities and must not cross capture or clock gaps.
- Recorder strata differ in compression, filtering and extensions. The header
  `maxUnits` is per-player capacity, not definition count. The `0x1a` exchange
  concerns unit-data compatibility but does not by itself provide all catalog
  context required by a stand-alone semantic decoder.
- Chat (`0x05`, `0xf9`, sector 2) and lobby-address sectors are private metadata;
  do not copy them into committed artifacts or analysis reports.

## Sources

- Inherited private-recording analyses (five-file envelope survey and later
  expanded packet-field surveys). Corpus relationships are **Supported
  inference** for semantics, even where the old wording says "confirmed" or
  "proven"; counts establish only observations in those samples. The recordings
  and the external Rust analyzer were subsequently located in
  the neighboring analysis project. The follow-up independently rechecked
  framing, headers, checksums, status decoding and match-body subpacket streams.
  Corrected resource relationships were checked; historical weapon-cadence,
  angle-fit, movement-fit and timing analyses were not rerun.
- **Established provenance:** the private `tada-ota-3.1` manifest (schema
  `openta.private-ta-recorder-corpus`, version 1) identifies nine files by
  SHA-256. Independent follow-up checks use those bytes as observations;
  neither recordings nor decoded private metadata are distributed here.
- Retail custom-envelope producer, variable unit-sync writer and ownership
  allocator, translated
  into [08 "Custom and direct wire paths"], [08 "Framing"] and
  [08 "Unit-sync ownership and body structure"]. These settle the underlying
  wire transformation, variable-length framing and block arithmetic separately
  from third-party recorder transformations. Further retail traces of movement
  serializers, scheduled status, damage/death/projectile/script records and
  participant snapshots establish the specific field corrections in §§6–8.
- TA Demo Recorder 0.99b2 source (Fnordia/SJ/Yeha, released 2003-11-05,
  clan-sy.com; local copy inspected with project-owner authorization):
  `packet.pas` (encrypt/compress/split tables),
  `Recorder/idplay.pas` (smartpak, 0x09/0x2c/0x19/0x23 handling),
  `Server/savefile.pas` (file layout, unsmartpak), `Server/Unitsync.pas`
  (0x1a), `Docs/saveformat.txt`, `Docs/PACKETS.TXT`, `Docs/TANET.TXT`,
  `Docs/paketjakt.txt` (subpacket examples).
- ta-forever `gpgnet4ta` (github.com/ta-forever/gpgnet4ta, `develop`
  branch): `libs/tapacket/TPacket.{h,cpp}`
  (subpacket names/sizes, bin2int, PLAYER_INFO offsets),
  `libs/tapacket/notes/`, `apps/gpgnet4ta/GameMonitor2.cpp` (tick usage).
- Facts taken from these sources are format documentation only. This project
  does not ship a TAD reader, recorder, or replay engine.
