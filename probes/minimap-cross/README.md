# minimap-cross — the five-pixel viewport cross (visual confirmation)

## Question

The composer-time viewport marker is **Established** from the two line
calls: when the minimap mode byte equals 2, two one-pixel Bresenham lines
`(cx+126, cy+32) → (cx+130, cy+32)` and `(cx+128, cy+30) → (cx+128, cy+34)`
are drawn in the ring/viewport palette index, clipped to the inclusive
minimap rect, where `cx = cameraCenterX − camX` and `cy = cameraCenterZ −
(cameraCenterY >> 1) − camZ` (`[03 §3.12]`, `[07 R-CAM-01 §11]`). Doc 03
retains a capture "only for visual confirmation of the figure, which the
calls fully determine". What remains **Unknown** in the same section is the
**writer of the mode byte**:

> **Mode-byte source (Unknown).** The composer-time marker condition is
> established as an equality test against value 2, but the available
> clean-room writer census found no simulation, session, mission, or map
> input that writes this distinct minimap mode byte. … `TODO(question):`
> identify the retail mode-byte writer or the authoritative setup value
> that selects 2; do not infer it from HUD state.

listed in doc 03's "Missing and unknown" as:

> Writer of the viewport-marker mode byte, or the authoritative setup value
> that selects mode 2 · §3.12 · static trace; it must not be inferred from
> HUD state. Marked `TODO(question)`.

The decider for the writer is a static trace, not this probe. What the
capture *can* contribute is the fact of whether the cross is drawn at all
in a stock skirmish and a stock campaign mission — if it is, the mode byte
reads 2 in those sessions, which bounds the writer census (some setup path
does write 2); if it is not, the marker is not part of ordinary play.

## Data

None. Any stock map; no files to install.

## Manual steps

1. Skirmish on any stock map; screenshot the minimap with the camera in
   the map interior, then with the camera hard against the west edge (so
   the cross clips at the minimap rect's left edge).
2. Open a stock campaign mission (or the kit's `heading-zero-nose` mission)
   and screenshot the minimap the same way.
3. Zoom the captures to pixel level and count the marker's pixels.

## Expected-observation template

| Session | Candidate outcomes | Observed |
| --- | --- | --- |
| skirmish, camera in the interior | **X** a cross of two one-pixel lines, each five pixels long, in the ring/viewport colour; **R** a rectangle/bracket outline instead (a different figure: then the figure being observed is not this marker — the selection brackets are 6/4-pixel figures, `[03 §3.12]`); **N** no marker | |
| skirmish, camera at the west edge | X clipped at the minimap rect's inclusive left edge (`[03 §3.7]` hit rect), or drawn outside it | |
| campaign mission | same vocabulary | |
| marker colour | same colour as the weapon-range/interceptor rings (`[03 §3.12]`), or different — record the RGB | |

Also record the window resolution (the marker offsets `+128`/`+32` are
constants, so the figure's position relative to the radar rect is the same
at every size), `TotalA.exe` MD5, patch level, screenshot names.

## Recording the result

Edit `research/retail-executable-spec/03-world-visibility-rendering-audio-and-video.md`
in `[03 §3.12]`: after "A capture probe is retained only for visual
confirmation of the figure" add *Observed (retail run YYYY-MM-DD,
minimap-cross)*: outcome X confirms the figure (the line calls remain the
**Established** source); outcome R or N is a contradiction to be recorded
as such — do not adjust the geometry from the picture, re-trace. Under
"Mode-byte source (Unknown)" add one sentence: "a stock skirmish/campaign
session was observed to draw (or not draw) the cross, so the mode byte
reads (or does not read) 2 in ordinary play" — the `TODO(question)` for
the writer stays until a static trace finds it.
