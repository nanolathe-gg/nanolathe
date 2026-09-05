# TAD — TA Demo Recorder recordings

## Overview

`.tad` files are match recordings produced by the community **TA Demo
Recorder** (TADR, by Fnordia/SJ/Yeha, distributed as a replacement
`dplayx.dll`). The recorder intercepts Total Annihilation's multiplayer
session traffic and writes a timed log of every TA packet exchanged between
peers, plus lobby metadata. Because TA multiplayer is an *asynchronous*
peer-to-peer model — each client simulates its own units and broadcasts
resulting unit state — a recording is primarily **state telemetry** (unit
sync, resource status, combat events) rather than a player-input log.
Replay tools work by re-feeding this traffic into a synthetic multiplayer
session, not by deterministic re-simulation.

OpenTA uses `.tad` recordings as *observational evidence* for
classic-engine behavior (see `RECORDER.md`). They are never an
authoritative state format; that remains `openta.replay`.

**Not a retail input.** The retail executable neither reads nor writes
`.tad` files; the malformed-input matrix of `[02 R-MALF-01 §2]` lists the
format only to say so. Everything below is a contract with third-party
recorders, not with the engine.

This document covers the file envelope, the TA wire-packet encodings
(XOR/checksum, LZ77 compression), the recorder's "smartpak" re-encoding of
unit-sync packets, and the subpacket taxonomy. It is the byte-level contract
for the `openta-recorder` crate.

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
All five corpus files tile exactly from byte 0 to EOF with zero gaps
(7,247 / 33,145 / 55,987 / 32,631 / 108,307 records).

### 2. Header record

```
u16   length
char  magic[8]      "TA Demo\0"
u16   version       3 = TADR 0.80b, 4 = 0.81a, 5 = 0.90b and ALL later
                    recorders through at least 2020 (corpus range)
u8    numPlayers
u16   maxUnits      per-player unit-slot count (500 in all corpus files);
                    ABSENT in version < 5 header (no maxUnits field)
char  mapName[]     NUL-terminated
```

Worked example (corpus, Gods of War 2005):

```
1a00 5441 2044 656d 6f00 0500 02 f401 "Gods of War"
len   T A   D e  m o \0  v=5  2  500
```

Readers must reject `version > 5` (unknown) and treat `version < 5` as a
separate stratum (different header size, and the body packets carry four
extra bytes — see §7).

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

Per player, in order:

```
player     { u8 color, u8 side (0=ARM 1=CORE 2=watcher), u8 number, char name[] }
statusMsg  { u8 number?, TA packet }   -- a full ENCRYPTED TA packet (see §5)
                                          containing a 0x20 PLAYER_INFO subpacket
```

The 0x20 PLAYER_INFO subpacket (192 bytes raw) carries map name, map hash,
map size (u16 width/height), maxUnits, TA version major/minor, clicked-in
state, watcher/cheats/permanent-LOS flags, side, color/slot, and an **is-AI
flag** — the authoritative per-player game-setup source. Offsets (0-based
within the raw subpacket, from ta-forever's parser): width @140, height
@142, player1Id @145, clicked @156, maxUnits @166, versionMajor @168,
versionMinor @169, player2Id @187.

### 5. TA packet encryption and compression

On the wire, every TA packet is:

```
u8   flag        0x03 plain, 0x04 compressed (0x06 pad/lobby variants exist)
u16  checksum    sum of the encrypted payload bytes
u8   payload[]   XOR-masked: byte at 0-based offset i (i >= 3) is
                 XORed with (i - 1)  [1-based Pascal: data[i] xor (i-1), i from 4]
```

**In `.tad` bodies the recorder stores packets DECRYPTED and with the
checksum removed**: the stored form is `flag + payload` where payload is the
(possibly compressed) subpacket stream. Only the per-player status records
(§4) store a still-encrypted packet.

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
4095 output bytes, not a backward distance. The compressor never compresses
the first two payload bytes and (in TADR's implementation) gives up beyond
input offset 2000.

### 6. Subpacket taxonomy

After decompression the payload is a concatenation of subpackets. Sizes
include the 1-byte id. Names follow ta-forever's `tapacket` library; ids
match TADR's tables and were re-validated against the corpus.

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
| 0x0b | 9 | **unit take damage**: u16 victim netId @1, u16 attacker netId @3, u16 damage @5, u8 damage_modifier @7 (97 distinct values in corpus), u8 damage_type @8 (2 distinct: 1 = normal damage, 6 = paralyzer damage). Field analysis against the corpus. |
| 0x0c | 11 | **unit killed**: u16 victim netId @1, u32 undecoded @3 (4 bytes, low entropy), u16 killer netId @7, u8 weapon/cause @9, u8 param @10. Killer-netId=0 means unattributed death. Field analysis on five recordings. |
| 0x0d | 36 | **weapon fired**: u32 origin x @1, u32 origin y @5, u32 origin z @9, u32 aim x @13, u32 aim y @17, u32 aim z @21 — all 16.16 fixed-point world units, TA convention (x east, y up/height, z south); origin is the muzzle position at launch, aim is the target point including lead. u8 weapon TDF `ID` @25 (joins `weapons/*.tdf` authored `ID=` — confirmed by per-(ID, shooter) fire-gap cadence matching `reloadtime × 30` sender ticks across nine recordings and four recorder eras, e.g. EMG 16 → 12 ticks, ARMRL_MISSILE 106 → 60, ARMTRUCK_ROCKET 124 → 360; 100.0% of corpus shots satisfy horizontal origin→aim distance ≤ weapon `range`). u8 flag @26 (2-state: 0x00 or 0xfe; semantics unknown — air-launched weapons skew 0xfe). i16 launch heading @27 (TA angle units, 65536 = full turn; equals `atan2(dx,dz) + 32768`, circular fit R=0.956, median residual 2.9°). i16 launch pitch @29 (same units; equals `atan2(dy, horiz)` for line-of-sight weapons, median residual 1.4°; ballistic weapons deviate). u16 shooter netId @31, u16 target netId @33 (same id space as 0x0b victim/attacker: unit id = playerIndex × maxUnits + slot + 1; the playerIndex is the lobby/block index, which does NOT equal sender order — in 5 of 9 corpus recordings the capture peer owns a non-zero block, so block bases must be reconciled per session). u8 firing weapon slot @35 (0-based Weapon1/2/3 index; proven by ARMAAS_WEAPON1/2/3 → 0/1/2 etc.). A "firing-unit netId @1" reading of the first field is spurious — those are the fractional bytes of origin x. Decoded against all five recordings, 73,180 events. |
| 0x0e | 14 | area of effect |
| 0x0f | 6 | feature action: u8 reference/category @1 (5 distinct), u16 param @2 (48 distinct), u8 action @4 (35 distinct), u8 zero @5 (constant). Field analysis against the corpus. |
| 0x10 | 22 | **unit start COB script**: u16 unit netId @1, u8 script-index @3 (13 distinct), u8 zero @4 (constant), u8 mode/flags @5 (3 distinct), u16 arg0 @6, u8 undecoded @8 (3 distinct), u8 zero @9 (constant), u16 arg1 @10, 10 bytes zero-padding @12..21 (constant). Field analysis against the corpus. |
| 0x11 | 4 | unit state: u16 netId @1, u8 state @3. State values: 0=off, 1=on, 2+=other. Field analysis against the corpus. |
| 0x12 | 5 | **unit build finished**: u16 built netId @1, u16 builder netId @3 |
| 0x13 | 19 | play sound |
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
| 0x20 | 192 | player info (see §4) |
| 0x21 | 10 | unknown |
| 0x22 | 6 | ident3 (u32 dpId, u8 number) |
| 0x23 | 14 | ally: u32 fromDpId @1, u32 toDpId @5, u8 alliedFromWithTo @9, u32 alliedToWithFrom @10 |
| 0x24 | 6 | team (u32 dpId, u8 team) |
| 0x26 | 41 | ident2 (10 × u32 dpIds) |
| 0x28 | 58 | **player resource info** (see §8) |
| 0x29 | 3 | unknown |
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

Ids ≥ 0xf6 never appear on the real TA wire; they are recorder constructs.
`0xfc` map-position packets and all chat ids are presentation/session data
and must never feed gameplay-state analysis; chat additionally must be
excluded from committed artifacts.

### 7. Unit sync — 0x2c and the smartpak re-encoding

TA broadcasts each player's unit state via 0x2c packets:

```
u8   0x2c
u16  length        total, including these 3 bytes and the tick
u32  tick          sender's game tick; tick mod maxUnits is the scheduled
                   status-scan slot
u16  marker        0xffff = status update; otherwise the zero-based local
                   slot of the moved unit
u8   body[]        BIT-PACKED state (see below)
```

Key facts:

- Each player owns a contiguous netId block: `startId = netId − (netId mod
  maxUnits)` (blocks assigned in lobby-determined order, `maxUnits` apart;
  netIds observed are 1-based within block: the commander of the player
  with block base 500 is netId 501).
- The tick increments once per sender sim frame (~30 Hz nominal). Status
  updates cycle round-robin by `tick mod maxUnits`: with maxUnits = 500, a
  given status slot is synced every ~16.7 s. Movement updates are event-driven
  and identify their unit with the non-`0xffff` marker instead.
- An **idle/empty slot** produces the 11-byte form: length = 0x000b, body =
  `ff ff 01 00` after the tick. Replay tools treat a slot's idle sync as
  "no unit alive in this slot".
- In the 0xffff status form, the known bit-packed fields (1-based byte
  indexing into the full reconstructed 0x2c, bits within the 4 bytes at
  offsets 11..14): **health = 16 bits starting at bit 2 of byte 11**,
  **buildDone = next 8 bits (0 = complete)**. TADR uses exactly these for
  its kill/health tracking.
- In the common non-`0xffff` movement form, the marker is the zero-based local
  slot (`netId = sender block base + marker + 1`). The confirmed common form
  has leading discriminator 0x24, 0x29, or 0xa9; its exact meaning remains open.
  A shape nibble at bits 8..11 selects two (8/A) or three (C/E) unaligned `u16`
  X/Z world-coordinate pairs beginning at bit 12. The first pair is current
  position; later pairs are a coherent forward path whose precise prediction/
  interpolation role remains open. The simple form ends with 17 one bits and
  11 zero bits, appearing as roughly `f0 ff 1f 00` due to nibble alignment.
  Optional trailing fields remain undecoded, so total body length is not a
  field-layout key.

The recorder shrinks the dominant 0x2c traffic ("smartpak"):

- the first 0x2c in a bundle emits `0xfe + its u32 tick`;
- every 0x2c is rewritten as `0xfd + u16 length + body` with the 4 tick
  bytes removed; the length field keeps the ORIGINAL 0x2c length, so the
  chunk occupies `length − 4` bytes and the body is `length − 7` bytes;
- an idle 0x2c (length 0x000b) becomes the single byte `0xff`;
- each reconstructed 0x2c consumes one tick: `tick = counter++`.

Reconstruction: `2c <origLen u16> <tick u32> <body>`.

A recorder option (`onlyunits`) drops all non-0x2c subpackets from the
recording; per-file subpacket coverage must therefore be measured, not
assumed.

**Version 3 files** (TADR 0.80b): each stored packet has four extra bytes
between the flag and the subpacket stream (skipped by all later readers).

### 8. Player resource info — 0x28

58 bytes. Bytes 1..17 (after the id) are near-zero in corpus observations
(identity/flags TBD); then ten `f32le` fields at 0-based offsets:

| offset | field (behavior-identified, names TBD) |
| --- | --- |
| 18 | current metal stock |
| 22 | current energy stock |
| 26 | metal storage capacity |
| 30 | energy storage capacity |
| 34 | cumulative metal produced |
| 38 | cumulative energy produced |
| 42 | cumulative counter (excess/wasted/shared family) |
| 46 | cumulative counter (same family) |
| 50 | cumulative counter (same family) |
| 54 | slow stepwise rate (income-like; steps as economy grows) |

ta-forever's notes name the groups "lastsharedm/e, sharedm/e, incomem/e,
lasttotalm/e"; the exact assignment of offsets 42/46/50/54 is not yet
pinned. Cadence in the corpus: roughly one 0x28 per ~4 s per sender.
Because offsets 34/38 are cumulative, their differences can constrain counter
increments without stock-level sampling aliasing. However, the packet sender
is not yet proven to identify the resource ledger. The analyzer retains
successive packets only as a sender-scoped transport stream, never exposes the
still-opaque prefix bytes, and does not call the resulting wall-time
differences engine income. Comparisons also must not cross recorder coverage,
Smartpak clock, or speed boundaries.

The bounded analyzer observes 14,506 valid 0x28 packets across the five-file
OTA corpus, with zero malformed or non-finite values. A corpus-wide aggregate
field lab strongly supports the 17-byte prefix's syntactic shape as one byte
followed by four little-endian `u32` words. The first two candidate words are
high-cardinality (1,023 and 1,082 distinct); the last two are low-cardinality
(3 and 2 distinct) and zero in 13,536 and 13,304 packets. Little-endian word
comparisons produce 2,086/2,121/552/274 matches against observed lifecycle raw
net IDs, while equivalent big-endian comparisons produce none. These are
candidate net-ID-like references, not promoted semantic fields.

The whole prefix is not a stable owner or ledger ID: there are 3,393 distinct
prefixes and 3,560 prefix changes among 14,492 consecutive same-sender packet
transitions. Splitting by full prefix discards continuity without improving a
counter invariant: sender-scoped cumulative metal and energy have zero
regressions in every individual recording before prefix splitting, while
full-prefix grouping reduces positive-wall candidate pairs from 11,234 to
7,736. Therefore the packet sender may be retained as a **transport-scoped
cumulative-counter stream**, but is not yet a proven economic player/owner.
Any sender-scoped delta per recorder wall second is direct transport-sequence
telemetry only; it is not engine income per simulation second or evidence for
player conservation, wind/tidal attribution, sharing, or starvation.

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

The Rust analyzer's bounded Theil–Sen fits, separated by sender, clock
continuity segment, and 0x19 segment, put the dominant natural-play segments
near 30 reconstructed ticks per recorder wall second in all five corpus files.
Painted Desert contains late mode-1 steps through signed speeds +1 to +10; its
three sufficiently sampled +10 sender segments fit at roughly 58.5–59.0
ticks/s. Within the owner's rebuttable `presumed_3_1c` admission this is target
evidence for a speed-dependent clock interpretation, but it does not by itself
identify the exact Windows 3.1c speed law because `dtMs` measures the capture
peer rather than authoritative simulation time.

## Corpus validation

A throwaway Python reference implementation of this contract was run against
all five private corpus recordings (see `RECORDER.md` for the corpus):

| map | header | recorder | players | maxUnits | records | subpacket errors |
| --- | --- | --- | --- | --- | ---: | --- |
| Gods of War | v5 | 0.99.3.513 | 2 | 500 | 7,247 | 0 |
| Painted Desert | v5 | 0.99ß2 | 2 + watcher | 1500 | 33,145 | 5 (see quirk below) |
| fox holes | v5 | 3.9.2.0 | 4 | 1500 | 55,987 | 0 |
| lava mania | v5 | 3.9.2.0 | 2 | 1500 | 32,631 | 0 |
| red triangle | v5 | 3.9.2.0 | 3 | 1500 | 108,307 | 0 |

Cross-checks that passed:

- **Tick reconstruction is exact.** Predicting the tick counter by counting
  0xfd/0xff chunks re-synchronizes with the next explicit 0xfe value in
  >99.9% of ~219,000 checks across the corpus; the residual deltas are +6
  (one missing bundle) with a handful of larger jumps. Since Painted Desert
  is 62% compressed records, this also proves the LZ77 decoder byte-exact
  in practice.
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

- **The remaining 0x2c body bit-packing is a crown-jewel unknown.** Health,
  buildDone, movement identity, and the common two/three-point X/Z prefix are
  now decoded by OpenTA corpus observation. Heading, velocity/vertical state,
  the later point role, alternate movement shapes, and optional trailing
  fields remain undecoded. The external implementations inspected replay the
  body opaquely or read only the tick.
- The unknown-purpose subpackets: 0x03, 0x07, 0x0a, 0x17, 0x1f, 0x21, 0x29,
  0x2e, 0xf6; and the exact layouts of 0x0e, 0x0a, 0x13, 0x14, 0x42; plus
  the undecoded 4-byte field at 0x0c offsets 3-6, the undecoded byte at
  0x10 offset 8, and the 2-state flag byte at 0x0d offset 26.
  - 0x0b damage_modifier and damage_type are decoded (damage_type: 1 =
    normal, 6 = paralyzer). The damage_modifier semantics (97 distinct values)
    are not yet mapped to damage-modifier formulas.
  - 0x0e (area-of-effect), 0x13 (play-sound), and 0x14 (give-unit) have 0
    corpus occurrences across all five recordings; layouts remain speculative.
- The 0x28 leading 17 bytes and the exact naming of its last four floats.
- 0x42 "Thaldren extended" packets appear only in later-patch strata; their
  content is unknown and they must be surfaced per-stratum.
- Recorder-version strata differ in behavior (e.g. compression usage:
  one corpus file contains zero compressed records; the `onlyunits` option
  changes coverage). Never pool strata without checking coverage first.
- The header `maxUnits` is per-player slot-block size; nothing here
  guarantees all clients agree on unit *type* rows beyond the 0x1a
  unit-data CRC exchange.
- Chat (0x05, 0xf9, sector 2) and lobby address sectors are
  privacy-sensitive: parsers must never copy them into committed artifacts
  or reports.

## Sources

- Direct byte analysis of five private corpus recordings, which remains the
  ground truth for every claim above; structural results were derived
  independently before source inspection and then reconciled.
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
- Facts taken from these sources are format documentation only; OpenTA
  implementations are written fresh from this document (no code
  translation). See `docs/provenance.md`.
