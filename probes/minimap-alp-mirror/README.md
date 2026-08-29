# minimap-alp-mirror — radar picture orientation and the ALP operand order

## Question

The generated radar picture is a 2× supersample of the tile art blended
down through the 256×256 ALP table, row-first: `blendTop = ALP[p00][p01]`,
`blendBottom = ALP[p10][p11]`, `out = ALP[blendTop][blendBottom]` with
`p00 = (2x, 2y)`, `p01 = (2x+1, 2y)`, `p10 = (2x, 2y+1)`, `p11 = (2x+1, 2y+1)`
(`[03 §3.7]`, **Established**). What is not settled is the screen
orientation of the result.

Owning section: `[03 §3.7]`, stated in doc 03's "Missing and unknown" list
as:

> Whether the radar picture's left/right orientation within a row is
> mirrored on screen · §3.7 · manual retail observation (asymmetric-palette
> probe with row-first, column-first, and diagonal orderings). Marked
> `TODO(question)`.

and in the body of `[03 §3.7]`:

> `TODO(question):` whether the left/right orientation within the row is
> mirrored on screen remains open; an asymmetric-palette probe (row-first,
> column-first, and diagonal orderings yielding distinct results) settles it.

## Data

| File | Content |
| --- | --- |
| `maps/ta_probe_alp.ota` | Skirmish map header (`Network 1`, four start positions). |
| `maps/ta_probe_alp.tnt` | 2048×2048 px flat map, minimap-present bit **clear** (so the picture is generated from the tiles, `[03 §3.7]` "Radar picture build"). Tile art: north-west green (2), north-east navy (4), south-west olive (3), south-east maroon (1), and a white (255) 8×8-tile block inside the north-west quadrant at tiles (4..11, 4..11). |
| `palettes-left/PALETTE.ALP` | `ALP[a][b] = a` for every pair: the **left** operand wins, so the blend becomes a pure subsample of `p00`. |
| `palettes-right/PALETTE.ALP` | `ALP[a][b] = b`: the **right** operand wins, so the picture is the subsample of `p11`. |
| `gen/main.go` | Regenerates all three binaries: `go run ./gen`. |

Both tables are maximally asymmetric (`ALP[a][b] ≠ ALP[b][a]` whenever
`a ≠ b`) while keeping the diagonal exact, which [fmt pal] requires for the
anti-alias downscale.

## Manual steps

1. Copy `maps/` into the install root, then `palettes-left/PALETTE.ALP` to
   `<install>/palettes/PALETTE.ALP` (create the directory; the archived
   `PALETTE.ALP` is shadowed by the loose file, `[02 §2]`).
2. Skirmish on `ta_probe_alp`, Mapped on (so the whole picture is visible).
3. Screenshot the minimap. Note which corner of the radar rect the white
   block occupies and the colour of each radar quadrant.
4. Replace the loose `PALETTE.ALP` with `palettes-right/PALETTE.ALP`,
   restart the game (the table is loaded once), repeat step 3, and look at
   the one-pixel column where green meets navy: with the right-operand table
   every straddling 2×2 block resolves to its **east** sample, so that
   column shifts one radar pixel west relative to the left-operand run.
5. Remove the loose `palettes/PALETTE.ALP` afterwards.

## Expected-observation template

| Item | Candidate outcomes | Observed (left table) | Observed (right table) |
| --- | --- | --- | --- |
| white block position on the radar | **A** top-left (rows and columns as authored, no mirroring); **B** top-right (row mirrored); **C** bottom-left or bottom-right (column mirrored) | | |
| quadrant colours clockwise from top-left | A: green, navy, maroon, olive; any other order names the mirroring | | |
| colour of the block straddling the green/navy seam | left table: green (west sample is the left operand); right table: navy; a swap of these two falsifies the row-first pairing's operand naming | | |
| letterbox bars | none expected: the map is square so `RadarW = RadarH = 126` (`[03 §3.7]` "Aspect and letterbox") | | |

Also record: `TotalA.exe` MD5, patch level, screenshot file names.

## Recording the result

Edit `research/retail-executable-spec/03-world-visibility-rendering-audio-and-video.md`
in `[03 §3.7]`: replace the `TODO(question):` sentence with *Observed
(retail run YYYY-MM-DD, minimap-alp-mirror)* stating outcome A/B/C and the
seam colour under each table. Outcome A closes the item as *Observed*
(not a trace: the pairing arithmetic stays **Established** from the static
trace; the orientation is an observation). Then delete the "Whether the
radar picture's left/right orientation …" bullet from the doc's "Missing
and unknown" list and remove the `TODO(question)` marker in code that cites
`[03 §3.7]` for it.
