# ZRB — Smacker cinematics

## Evidence and scope

**Established (supplied asset inspection):** the five `Data/1.zrb` through
`Data/5.zrb` movies are ordinary `SMK2` files. The filename extension does not
add an outer container. This page owns their bytes; the shell sequencing
contract remains [08 R-OOS-01 §4], and [03 §9] owns the cinematic boundary.

The decoding description was independently written from the public
[Smacker format description](https://wiki.multimedia.cx/index.php/Smacker),
asset inspection, and authored-file experiments with the installed FFmpeg
command-line decoder as a black-box reference. No decoder implementation
source was used. The public description's palette component labels are
inverted: an authored pure-red literal proves the byte order is red, green,
blue. It also omits the zero bit terminating each present Huffman tree;
parsing all supplied tree sections establishes that delimiter. RAD's
[playback documentation](https://www.radgametools.com/binkhsap.htm) distinguishes
pixel doubling from skipped alternate scanlines; both have twice the stored
height. This format reader reports that display extent without rendering it.

## Header and packet layout

**Established (format description and asset inspection):** scalar fields are
little-endian. The fixed header occupies 104 bytes:

| Byte offset | Type | Meaning |
|---:|---|---|
| 0 | four bytes | `SMK2`; `SMK4` is a different video revision |
| 4, 8 | two u32 | stored width and height |
| 12 | u32 | number of frames to play |
| 16 | i32 | frame interval: positive milliseconds, negative magnitude in 10 microseconds |
| 20 | u32 | bit 0 adds a ring packet; bit 1 requests alternate black rows, bit 2 repeated rows, both doubling display height |
| 24 | seven u32 | largest decoded audio packet sizes |
| 52 | u32 | encoded video-tree section length in bytes |
| 56 | four u32 | decoder allocation hints for map, color, full and type trees |
| 72 | seven u32 | audio track descriptions |
| 100 | u32 | reserved |

**Established (retail library examination, [03 §9]):** a zero interval remains
zero. The public format description claims a 100 ms fallback, but an authored
zero-interval file also produces an invalid-timebase diagnostic from the
installed FFmpeg. Nanolathe rejects zero cadence explicitly as unsupported
rather than inventing a playback rate. None of the supplied movies uses zero.

**Established:** an audio description's low 24 bits contain sample rate;
bit 31 denotes compression, bit 30 presence, bit 29 sixteen-bit samples,
and bit 28 stereo. Bits 26 or 27 select alternative perceptual codecs.
Each sample otherwise occupies one unsigned byte or one signed little-endian
16-bit word. Channels in decoded PCM are interleaved left then right.

**Established:** the physical packet count is the logical frame count plus
one when the ring flag is present. After the header come that many u32 packet
sizes, then that many one-byte packet content masks, the video-tree section,
and the packets themselves. Clear the low two bits of each size to obtain its
byte length. Size bit 0 marks a keyframe; size bit 1's purpose is **Unknown**,
and neither changes sequential decoding. A content mask's bit 0 denotes a
palette chunk; bits 1 through 7 denote audio tracks 0 through 6. Within a
packet, the palette precedes ascending audio tracks, followed by video bits.
The ring packet is indexed but is not a frame in play-once playback.

## Bits and trees

**Established (format description, asset tree parsing, authored fixtures):**
bits are consumed least-significant first within a byte; the first bit of a
multi-bit integer is its least-significant bit. Tree topology is prefix
ordered: a zero denotes a leaf, and a one introduces first the zero branch,
then the one branch. Byte-valued trees start with a presence bit; an absent
tree yields zero. A present tree stores eight bits per leaf and ends with one
zero delimiter after the complete topology. Traversal chooses branches by
successive stream bits and consumes no bits for a constant leaf.

**Established:** the video section holds four word-valued trees, in map,
color-pair, full-color-pair, block-type order. Each has a presence bit. A
present word tree contains two byte trees (low byte, high byte), three literal
16-bit cache-marker values, its topology, and a final zero delimiter. Each
leaf is decoded using the low-byte tree and then the high-byte tree. Leaves
matching the markers become references to three initially zero cache slots.
The tree section can include unused trailing bytes; the header's section
length determines the packet boundary.

**Established (format description, corpus comparison):** each word tree has
its own cache of the last three distinct-in-succession outputs. Resolve a
cache leaf before updating the cache. If the resulting word equals the newest
entry, leave all entries alone; otherwise shift newest to second, second to
third, and install the word as newest. Reset these three values before every
video frame. Unreferenced cache markers need no physical leaf.

## Palette

**Established (authored reference fixture and corpus comparison):** the first
palette byte times four is the entire chunk length, including that byte.
Palette operations fill entries from index zero, retaining the previous
palette as an immutable copy source. For control byte `c`:

- If its high bit is set, retain the next `(c & 127)+1` entries in place.
- Otherwise, if bit 6 is set, read one source index and copy `(c & 63)+1`
  entries from that index in the previous palette to the output cursor.
- Otherwise, `c` is the red component, followed by green then blue. Each is
  six bits and expands to eight by `(v << 2) | (v >> 4)`.

Stop at 256 entries or chunk exhaustion; bytes after the palette span belong
to the next chunk, regardless of how many palette entries were visited.
Palette colors are opaque. Copy operations always read the old palette even
when the source and destination spans overlap.

## Video blocks

**Established (format description and corpus comparison):** blocks are 4 by 4
pixels, visited left to right then top to bottom, with partial edge blocks
clipped to the stored dimensions. Decode a word from the type tree. Its low
two bits select the block operation; bits 2 through 7 select a run length.
Indices 0 through 58 mean index plus one blocks; indices 59 through 63 mean
128, 256, 512, 1024, and 2048 blocks. The high byte supplies the solid color.
Repeat the selected operation for that many consecutive blocks:

- Type 0: decode a color-pair word, then a map word. Visit pixels in row order;
  successive map bits from low to high select low-byte color for zero and
  high-byte color for one.
- Type 1: each of four rows decodes two full-color-pair words. The first
  supplies columns 2 and 3, low byte then high; the second supplies columns
  0 and 1 in the same byte order.
- Type 2: retain the prior pixels.
- Type 3: fill with the type word's high byte.

## Audio

**Established (format description and corpus comparison):** each audio chunk
starts with a u32 length including that length field. Uncompressed chunks
then contain PCM. Compressed chunks have a second u32 giving decoded source
byte count, then an independent bitstream. Its first three bits specify data
presence, stereo, and sixteen-bit samples. The latter two must agree with the
track description. An absent-data chunk yields no samples.

**Established:** build one byte tree per byte in an interleaved sample frame:
one for 8-bit mono, two for 8-bit stereo or 16-bit mono, four for 16-bit stereo.
Read initial channel samples in reverse channel order; for 16-bit samples read
the high byte before the low byte. Output those initial samples in ordinary
channel order. Thereafter decode each channel in ordinary order, using its
low-byte tree then (for 16-bit) high-byte tree. Assemble a complete delta word
before adding it modulo 65536 to the previous 16-bit sample; for 8-bit samples
add modulo 256. Thus low-byte overflow carries into the high byte, and a
negative delta wraps correctly. Stop after the declared decoded source byte
count, including the initial samples. Each packet restarts its predictors.
The API expands unsigned bytes to signed16 using `(sample-128)*256` and keeps
16-bit values unchanged; it does not resample or remix channels.

## Supplied corpus and implementation boundary

**Established (header and first audio packet inspection):**

| Movie | Stored pixels | Display pixels | Logical frames |
|---|---|---|---:|
| 1 | 640×240 | 640×480 | 599 |
| 2 (Intro) | 640×240 | 640×480 | 4058 |
| 3 | 640×240 | 640×480 | 889 |
| 4 | 640×240 | 640×480 | 1841 |
| 5 | 640×304 | 640×304 | 4020 |

All five store frame interval −3333 (33.33 milliseconds), and one compressed
22050 Hz, stereo, unsigned-eight-bit track. Their audio packet flags agree
with the header; treating them as mono 16-bit would misinterpret the data.

**Nanolathe host policy:** the reader supports SMK2 video, PCM and Huffman
DPCM audio, rejecting SMK4 and alternative audio codecs explicitly. Bounds,
Huffman depth/node budgets, cumulative duration overflow checks, one million
physical packets, pixel and decoded audio allocation limits reject
unsafe files, and malformed reads become errors rather than fabricated data.
The borrowed input must remain immutable. Frame pixels and per-packet audio
may be reused on the next call. A whole-track audio pass walks indexed packets
and shares the audio decoder without advancing video or decoding its blocks.
These are API and safety decisions, not retail behavior claims.

**Unknown:** the meaning of packet-size bit 1 and unsupported SMK4/perceptual
codec details are unnecessary for the supplied corpus. New assets exercising
those features and an independent format investigation would settle them.
Retail-library palette transfer, device mixing and synchronization are owned
by [03 §9], not established by agreement with an external decoder. Exact
audio-cursor calibration and focus-loss behavior remain behavioral unknowns
there; this reader has no device or timing loop.

## Reference validation

**Established (FFmpeg 8.1.1 black-box comparison):** every RGB24 frame of all
five supplied movies matches the reference frame MD5, and each complete
signed16 soundtrack matches its SHA256. This covers all 11,407 frames. The
asset-gated test stores only digests, including an aggregate of the per-frame
video hashes; it also verifies the independent audio pass against the same
reference. An independently authored 16-bit stereo stream produces left/right
samples `(255, -32768)`, `(257, 32767)`, `(259, 32766)` in both decoders,
establishing low-byte carry and reverse-order channel bases outside the
eight-bit stock corpus. No FFmpeg library is linked or needed at runtime.
