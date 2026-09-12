# WAV — Sound Effects (`.wav`)

## Overview

Most Total Annihilation audio is standard Microsoft RIFF WAVE. A small number
of stock files use older raw or `DIGI` containers, so readers should accept
those legacy forms as well. Files live in
`sounds/` (referenced without extension from weapon TDFs, sound-category
TDFs, and GUI events) and `camps/briefs/` (mission narration, referenced
from OTA `narration=`/`glamoursound=`).

The canonical RIFF files have nothing TA-specific in their container. This
page records the profile retail data uses, including the legacy exceptions,
so implementations know what they must support.

## Reference

**Established (reference-corpus observation):** most inspected files are
canonical RIFF: `RIFF` chunk, `WAVE` form type, `fmt `
chunk (PCM, format tag 1), `data` chunk. A survey of the retail WAV corpus
records these profiles. The counts describe that survey, not format limits:

| Format | Count | Used for |
| --- | ---: | --- |
| PCM mono 11025 Hz 8-bit | 482 | ordinary in-game sound effects and unit voices |
| PCM mono 22050 Hz 16-bit | 74 | mission briefing narration (`camps/briefs/`) |
| PCM stereo 44100 Hz 16-bit | 4 | Core Contingency victory music (`Exp1armvict.wav` etc.) |
| PCM mono 22254 Hz 8-bit | 1 | `sounds/CDOGGY.WAV` (22254 Hz is the classic Macintosh sample rate — an authoring leftover) |

The currently inspected installation also contains these legacy containers:

| Container | Count | Layout |
| --- | ---: | --- |
| Raw PCM | 1 | unsigned 8-bit mono at 11025 Hz; `sounds/HONK.WAV` has no header |
| `DIGI` | 1 | fixed `HSHD` and `SDAT` signatures; the sample-rate word read by retail is little-endian, independently of unused container-size fields; `sounds/SING.WAV` is unsigned 8-bit mono at 11025 Hz |

Real example — `sounds/BUTTON12.WAV` from `totala1.hpi`:

**Publication omission:** The retail-derived example is omitted from this
edition. The surrounding format description retains its stated evidence and
confidence.

PCM, 1 channel, 11025 Hz, 8-bit → a 471-sample button click.

## How the engine reads it

**Established**, owned by `[02 §7]` and `[02 R-MALF-01 §10]`. Detection
reads four bytes at 0: `DIGI` with `HSHD` at 8 and `SDAT` at 32 → legacy; else `RIFF` with
`WAVE` at 8 → RIFF; else raw. Legacy: the little-endian 32-bit rate at
byte 22 (11,000 → 11,025), the sample is bytes 40 … end of file (`size − 40`; the `SDAT`
size field is not read), 8-bit mono. RIFF: chunks are walked from 12 with
stride `size + 8` and **no odd padding**. The first `fmt ` and first `data`
are selected independently from the beginning of the chunk list, so `data`
may precede `fmt `. The walk stops when the next offset reaches `RIFF size + 8`; the first `fmt ` must be ≥ 16 bytes and
only channels (+2), rate (+4) and bits (+14) are read; the first `data`
must have a size above 0; the declared `data` size must then read back in
full or the sample is null. Raw: the whole file, header included, as 8-bit
mono 11,025 Hz. A null sample is silent (the alias plays nothing, no
message).

## Nanolathe parser and playback policy

`formats.LoadAudio` is the single container parser used by the viewer and
`internal/audio`. For RIFF it retains the authored format tag, byte rate and
block alignment alongside the channel/rate/width fields and the complete
payload span. It checks file bounds before reading and rejects truncated
recognized containers; it does not require PCM metadata to normalize those
authored fields. Chunk-start enumeration stops at the declared RIFF span.

The audio adapter accepts PCM with 8- or 16-bit samples, positive channel count
and sample rate, and representable frame size/byte rate. It derives playback
alignment from the channel count and bit width, copies complete frames and
preserves VFS provenance. Rejecting unsupported codecs and unsafe metadata,
rejecting empty input, and the 16 MiB direct-decode allocation limit are
**Nanolathe host-safety policies**. They do not establish retail validation of
fields it never reads. The parser retains any partial final frame bytes even
though playback excludes that incomplete frame. Zero-length device-buffer
creation remains a **backend-dependent Unknown** `[02 R-MALF-01 §10]`;
the retail wrapper boundary is now established below, but its zero-size
device result was not observed.

## Device-buffer boundary

**Established (static/transient sample paths):** the wrapper constructs PCM
parameters from the selected channel count, rate and width, derives block
alignment and byte rate, and passes the sample length directly to
DirectSound buffer creation. It has no separate zero-length rejection,
codec decoder, or parameter-normalization fallback. Creation failure returns
a null sample. After successful creation it locks the requested extent,
reads the locked byte count and unlocks; lock, short-read or unlock failure
releases the buffer and returns null. The transient path uses this same
creator before starting playback. Thus an empty raw file reaches a zero-byte
device request; it is not rejected by RIFF's positive-data-size check.

**Established (streaming path):** buffer capacity is derived from two seconds
of the selected PCM parameters, independently of payload length. An empty
raw stream therefore requests the ordinary nonzero capacity at 11,025 Hz,
8-bit mono. When the initial refill reaches EOF, it fills the remainder with
unsigned-8-bit silence and records the endpoint. This differs from the
static/transient zero-size request [03 R-AUD-02 §1]. It does not prove that a
particular device successfully creates or plays the buffer.

## Unknowns and caveats

- **Unknown:** the external DirectSound implementation's result for a
  zero-byte static buffer or unusual channel/rate/width combinations. The
  executable passes these requests across the device boundary without a
  local repair; that boundary has been traced, so another wrapper trace
  cannot settle acceptance. Inspection of the actual target backend or a
  manual observation of that backend's creation result would settle it. No
  such backend execution was performed in this audit; [03 §8.2] owns the
  playback boundary.
- The engine never reads the format tag, so formats beyond the table above
  are neither rejected nor decoded: any `fmt ` chunk is taken as PCM of its
  declared channel count, rate and bit depth. Compressed payload bytes are
  handed to the PCM backend without codec decoding. Channels, rate and width can still fail at device-buffer creation;
  successful playback as noise is not guaranteed. See [03 §8.2] and "How the
  engine reads it".
- Volume/attenuation and 3D positioning are engine behavior, not stored in
  the files.

## Sources

- Microsoft RIFF/WAVE specification (public standard).
- *WAV*, TA Design Guide — usage locations:
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/wavdesc.htm>
- Verified against `sounds/BUTTON12.WAV` from `totala1.hpi`;
  Nanolathe parser: `formats/wav.go`.
