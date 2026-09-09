# featupscale — 2x sprite feature upscale

Applies the terrain upscaler's premise to sprite (GAF) map features: every
authored 2x2 block of every frame of a GAF family, reduced through the retail
palette blend, is an example of how a pixel of that colour looks one octave
up. Transparency is a fifth feature channel and a reduced block is opaque when
at least `-tie` of its four pixels are (default 2), so silhouettes get
authored edge detail. Research tool for remastering; output is derived retail
art and is never committed.

The synthesizer itself lives in `internal/upscale`, which the engine calls at
battle load through `Bank2x` (docs/DESIGN_GPU_RENDERER.md §14.4); this command
is the wrapper that picks the query entries and writes the per-frame PNGs.

```sh
go run ./tools/mapupscale/featupscale -gaf trees -seq leaf1 -epx -out /tmp/featupscale
go run ./tools/mapupscale/featupscale -gaf trees -seq all -frame -1 -out /tmp/featupscale/trees
go run ./tools/mapupscale/featupscale -gaf rocks,trees -seq rockmetal1 -out /tmp/featupscale
go run ./tools/mapupscale/features -map "Great Divide" -out /tmp/features/gd   # extract + stats
go run ./tools/mapupscale/features -census                                     # size histogram
```

`-gaf` lists GAF names under `anims/`; the first holds the query entries
(`-seq`: a name, a comma list, or `all`), every listed GAF supplies examples.
`-frame -1` upscales every frame of an entry, seeding each frame's search
with the previous frame's matches so animated entries do not flicker. Output
is `<entry>-<frame>-2x.png` beside `-1x.png` (and `-epx.png`, the Scale2x
rule, with `-epx`), as palette-indexed PNGs with the retail palette and the
GAF colour-key index 9 transparent, the form the remaster pipeline packs.

## Cost terms

Cost = 5x5 PCA feature distance + tone + coverage + seam + coherence, all on
top of the parent-key match (a block is admissible for a query pixel when it
reduces to the pixel's colour, or to a relaxed stand-in one blend step away
whose blocks average closer to the colour, as in patchmatchgo).

- **Tone** (`-tone 64 -deadzone 0`): squared distance between the block's
  opaque RGB sum and the parent colour times the opaque count. Sprites need
  it stronger than terrain because the sparse sprite palettes have no darker
  stand-ins; with it the 2x luma sits within 1 level of the 1x sprite for
  trees and rocks.
- **Coverage** (`-coverage 200000`): an opaque query pixel expects a fully
  opaque block and a transparent one a clear block, softly. Anything weaker
  erodes thin sprites: a lone speck given a half block loses half its area.
- **Seam** (`-seam 8`) and **coherence** (`-coherence 32`, `-mismatch` for an
  opacity mismatch in the surround): as patchmatchgo.

## Example hygiene

- `-exclude burn,boom,fire,smoke,rec` drops fire, explosion and reclaim
  entries from the example set; their colours (flames, nanolathe sparkle)
  leaked into idle trees and rocks as specks.
- `-closeness 1728` (3 × 24²) admits an example block only when each of its
  colours is within that squared RGB distance of a colour the query sprite
  uses. It stops rare highlights from sibling entries (conifer glints from
  a broadleaf) and is what lets thin sprites keep their mass; a strict
  closure (query colours only) starved shrubs of full blocks.

## Results (2026-09-04)

| Sprite | GAF, examples | luma 1x → 2x | opaque share 1x → 2x |
|---|---|---|---|
| leaf1 | trees, 972k | 62.9 → 62.6 | 0.548 → 0.531 |
| tree3 | trees | 37.0 → 37.2 | 0.672 → 0.665 |
| shrubs3 | trees | 82.9 → 87.6 | 0.174 → 0.147 |
| rockmetal1 | rocks, 35k | 87.6 → 89.0 | 0.663 → 0.651 |
| rock06 | dryrocks, 56k | 75.6 → 76.4 | 0.740 → 0.739 |

Each sprite takes 10–20 ms after a 0.2–1 s example build per GAF family.
Visually, leaves and crystal facets come out finer and read as a higher
resolution render, unlike Scale2x's rounded blobs; silhouettes are clean.

Known limits: very sparse sprites (shrubs3, 1–2 px specks) still lose ~15%
of their area and sit +5 luma; ~1% of pixels have no admissible example and
fall back to a flat 2x2 block; shadow entries are flat two-colour art where
Scale2x or nearest is the better choice.
