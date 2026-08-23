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

Most retail files are canonical RIFF: `RIFF` chunk, `WAVE` form type, `fmt `
chunk (PCM, format tag 1), `data` chunk. A survey of the retail WAV corpus
gives the canonical profile an implementation must accept:

| Format | Count | Used for |
| --- | ---: | --- |
| PCM mono 11025 Hz 8-bit | 482 | all in-game sound effects and unit voices |
| PCM mono 22050 Hz 16-bit | 74 | mission briefing narration (`camps/briefs/`) |
| PCM stereo 44100 Hz 16-bit | 4 | Core Contingency victory music (`Exp1armvict.wav` etc.) |
| PCM mono 22254 Hz 8-bit | 1 | `sounds/CDOGGY.WAV` (22254 Hz is the classic Macintosh sample rate — an authoring leftover) |

The currently inspected installation also contains these legacy containers:

| Container | Count | Layout |
| --- | ---: | --- |
| Raw PCM | 1 | unsigned 8-bit mono at 11025 Hz; `sounds/HONK.WAV` has no header |
| `DIGI` | 1 | big-endian `HSHD` metadata followed by an `SDAT` PCM chunk; `sounds/SING.WAV` is unsigned 8-bit mono at 11025 Hz |

Real example — `sounds/BUTTON12.WAV` from `totala1.hpi`:

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.


PCM, 1 channel, 11025 Hz, 8-bit → a 471-sample button click.

## Unknowns and caveats

- Whether the retail engine accepts formats beyond the table above
  (compressed codecs, other rates) is untested.
- Volume/attenuation and 3D positioning are engine behavior, not stored in
  the files.

## Sources

- Microsoft RIFF/WAVE specification (public standard).
- *WAV*, TA Design Guide — usage locations:
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/wavdesc.htm>
- Verified against `sounds/BUTTON12.WAV` from `totala1.hpi`;
  OpenTA parser: `formats/wav.go`.
