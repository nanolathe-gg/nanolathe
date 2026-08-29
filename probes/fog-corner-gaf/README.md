# fog-corner-gaf — which nibble bit paints which fog quarter

## Question

The fog overlay composer accumulates a 4-bit nibble per 32×32 fog cell from
the four visibility tiles that meet at the cell's centre corner, then draws
frame `value − 1` of the `Black<n>`/`Gray<n>` entries of `anims/fog.gaf`
(`[03 §3.4]`, `[03 R-RR16-A §3]`, `[03 R-RR16-A §4]`, `[03 R-FX-01 §1]`).
The bit→cache-cell geometry is direct-static (a fogged tile ORs bit 1 into
its own cell, bit 2 into the west cell, bit 4 into the north cell, bit 8
into the north-west cell). Which **quarter of the cell each bit paints** is
inferred from the stock frame offsets only.

Owning section: `[03 §3.4]` (fog cache), stated in doc 03's "Missing and
unknown" list as:

> Whether the fog cache's `1 = NW` corner-to-bit assignment holds · §3.4
> [R-RR16-A] · manual retail observation (asymmetric fog GAF probe). Supported
> inference today.

and in the body of `[03 §3.4]`:

> Corner→bit `1=NW,2=NE,4=SW,8=SE` (which GAF quarter each bit paints)
> remains supported inference pending asymmetric fog.gaf probe; retail's
> hard fog edge must not be softened to improve image metrics.

## Data

| File | Content |
| --- | --- |
| `anims/fog.gaf` | Eight entries `Black1..Black4`, `Gray1..Gray4`, fourteen raw frames each, hold word 10, colour key 9, cloud pixels index 0 — the stock geometry (frame `n` for nibble `n+1`; single-bit frames are 16×16 with offsets `(0,0)`, `(−16,0)`, `(0,−16)`, `(−16,−16)`; unions are the bounding box). Each quarter carries a distinct glyph: **bit 1 solid square, bit 2 hollow ring, bit 4 diagonal stripes, bit 8 4×4 checkerboard.** |
| `gen/main.go` | Regenerates the file: `go run ./gen` from this directory. |

## Manual steps

1. Copy `anims/` into the retail install root (loose files win, `[02 §2]`).
2. Start a skirmish on any stock map with **Mapped** off and **Line of
   Sight** on (so both the history/black and current/gray channels exist).
3. Let the commander stand still; its sight circle is surrounded by fog.
   Take a screenshot showing the fog boundary north, south, east and west
   of the circle, then move the commander a few tiles and take a second
   screenshot so previously seen tiles show the black (history) family
   behind the gray (current) family.
4. Zoom the screenshot. At a boundary fog cell, note which glyph is drawn
   in which quarter relative to the explored tiles.

## Expected-observation template

| Boundary | Where the explored tile lies | Glyph seen in the cell's quarter nearest the explored tile | Candidate outcomes | Observed |
| --- | --- | --- | --- | --- |
| west edge of the sight circle | east of the cell | | **A** (inference `1=NW,2=NE,4=SW,8=SE`): the north-east quarter shows the *ring* (bit 2) and the south-east the *checkerboard* (bit 8) when the tiles east of the cell are fogged; **B**: the assignment is a rotation/mirror of A — record which glyph sits where | |
| east edge | west of the cell | | A: solid square NW, stripes SW; B: otherwise | |
| north edge | south of the cell | | A: stripes SW, checkerboard SE; B: otherwise | |
| south edge | north of the cell | | A: solid NW, ring NE; B: otherwise | |
| any cell with all four tiles fogged | — | | frame 13 (value 14) is a 32×32 union of all four glyphs; value 15 short-circuits to solid dark with **no** GAF frame — record whether a solid-black cell without glyphs appears (it must, for unexplored cells) | |
| gray family | — | | the same glyph geometry, but drawn as a gray remap of the terrain rather than black ([03 R-RR16-A §8]) | |

Also record: `TotalA.exe` MD5, patch level, map name, screenshot file names.

## Recording the result

Edit `research/retail-executable-spec/03-world-visibility-rendering-audio-and-video.md`
under `[03 §3.4]` (the paragraph ending "pending asymmetric fog.gaf probe"):
add *Observed (retail run YYYY-MM-DD, fog-corner-gaf)* with the four
boundary rows. Outcome A promotes the corner→bit assignment from
**Supported inference** to *Observed*; outcome B falsifies it — write the
observed assignment and correct the sentence, stating what the previous
text said. Then delete the "Whether the fog cache's `1 = NW` …" bullet from
the doc's "Missing and unknown" list and update the matching sentence at the
end of `[03 §4.3]` ("the `1=NW` corner→bit mapping remains supported
inference pending probe").
