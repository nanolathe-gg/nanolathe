# map-edge-capture — in-map void, out-of-map clip, stale backbuffer

## Question

Visibility culling is binary and hard-edged, and "unexplored map
borders/voids can remain black where no valid tile blit reaches the
backbuffer" (`[03 §3.3]` fog family text). The evidence for the exact
difference between an **in-map void tile** (attribute feature word
`0xFFFC`, [fmt tnt]) and an **out-of-map clipped region** beyond the play
rect is medium, and whether the backbuffer keeps **stale pixels** beyond
the play rect is untested. `[03 R-TERR-01 §2]` establishes that no consumer
treats void cells specially by name and that "the tile art under a void
cell is drawn normally".

Owning sections: `[03 §2.2]` and `[03 §4.1]`, stated in doc 03's "Missing
and unknown" list as:

> Edge behavior for unexplored in-map void cells, map border clipping, and
> whether the backbuffer retains stale bytes beyond the play rect · §2.2,
> §4.1 · manual retail observation (map-edge capture probe).

and in "Confidence limits":

> In-map void rendering versus out-of-map clipped regions is less certain
> than the hard cull; the exact distinction and the persistent backbuffer
> behavior are probe-pending (a capture at the map edge settles which pixels
> the backbuffer retains beyond the play rect).

## Data

| File | Content |
| --- | --- |
| `maps/ta_probe_edge.tnt` | 2048×2048 flat map (height 20) whose tile art colour-codes each band: interior **green**; east cells 120–121 **purple** and authored `0xFFFC` void; east cells 122–125 **teal** (ordinary cells outside `PlayRight = 2016`); east cells 126–127 **olive** (the engine's own right-column void strip); north cell row 0 **teal** (voided by the north rule at height 20); south cell rows 120–127 **maroon** (rows 120–126 are voided by the south rule; the bottom row never is). |
| `maps/ta_probe_edge.ota` | Skirmish header with Mapped and Line of Sight **on** (the `[GlobalHeader]` defaults; the lobby can override), StartPos1 at (1800,1700) next to the south-east corner. |
| `gen/main.go` | Regenerates the TNT: `go run ./gen`. |

Play insets ([03 R-TERR-01 §2] rule 1): `PlayRight = 2048 − 32 = 2016`,
`PlayBottom = 2048 − 128 = 1920`, so the camera clamp stops at cell column
126 and cell row 120.

## Manual steps

Run twice: once with the lobby's Mapped/LOS at their defaults (fog on) and
once with Mapped on and Line of Sight off (everything visible).

1. Copy `maps/` into the install root; skirmish on `ta_probe_edge`.
2. Scroll the camera hard against the **east** edge; screenshot. Then hard
   against the **south** edge; screenshot. Then the south-east corner.
3. Scroll away from the edge and back three times, changing the zoom or
   opening/closing the side panel between passes, and screenshot the edge
   again — stale-backbuffer bytes show as content that does not match the
   current frame.
4. With fog on: walk the commander east until the purple band enters its
   sight circle; screenshot the purple/teal/olive bands both inside and
   outside the circle.

## Expected-observation template

| Region | Candidate outcomes | Observed (fog on) | Observed (fog off) |
| --- | --- | --- | --- |
| purple in-map void band (cells 120–121), explored | **T** drawn as its tile art (purple), exactly like ordinary cells — `[03 R-TERR-01 §2]` "no void raster for rendering"; **K** drawn black; **F** drawn as permanent fog | | |
| purple band, never explored | black/fog like any unexplored cell (T), or something distinct (record) | | |
| teal cells 122–125 (past `PlayRight`) | can they be scrolled into view at all? if yes: tile art, black, or stale | | |
| olive engine void strip (126–127) | never reachable by the camera clamp, or partly visible at the clamp; if visible, tile art vs black | | |
| maroon south rows (120–127) | same vocabulary; note where the clamp stops (`PlayBottom` = cell row 120) | | |
| pixels beyond the play rect after repeated scrolling | **C** consistent (cleared or re-drawn every frame); **S** stale (previous frames' bytes persist) | | |
| commander movement into the purple band | blocked (void blocks movement, `[03 R-TERR-01 §2]`) or not | | |

Also record the window mode (windowed GDI vs fullscreen DirectDraw, `[03
§4.1]`/`[03 §4.2]`), `TotalA.exe` MD5, patch level, screenshot names.

## Recording the result

Edit `research/retail-executable-spec/03-world-visibility-rendering-audio-and-video.md`:
in `[03 §3.3]` at the paragraph "Unexplored map borders/voids can remain
black …" add *Observed (retail run YYYY-MM-DD, map-edge-capture)* with the
table; if outcome T holds, the "medium" caveat becomes *Observed* and
`[03 R-TERR-01 §2]`'s rendering sentence gains an observation citation;
outcome S or K adds a new **Established-by-observation** sentence to
`[03 §4.1]` about backbuffer retention. Then delete the "Edge behavior for
unexplored in-map void cells …" bullet from "Missing and unknown" and the
matching "Confidence limits" bullet, and update the `TODO(question)` marker
in the terrain renderer that cites `[03 §2.2]` for void drawing.
