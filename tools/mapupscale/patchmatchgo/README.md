# patchmatchgo — 2x terrain upscale from original game data

Prototype. Not wired into the engine yet.

## Goal

Nanolathe wants an optional "remastered" zoomed-in view: 64x64 map tiles and
2x unit rendering. The retail maps only ship 32x32 tiles, and we will not
invent art. This tool synthesizes a 64x64 tile for every unique 32x32 tile of
a map using nothing but that map's own authored pixels and the retail
palette-blend table, so the result is derived entirely from original game
data. With `-relax=false` it reduces back to the retail map exactly; the
default relaxed mode trades that exact cycle for substantially better tone.

Input is the decoded export written by `tools/mapupscale/export`
(indexed pixels, palette, ALP table, metadata). Output is an indexed tileset
(`-tileset.bin`, 64x64 tiles in order of first appearance) plus a
placement map (`-tilemap.json`), which is the shape a TNT-like 64px container
and the engine loader will want. `-preview` and `-full-preview` add PNGs for
inspection with `tools/mapupscale/patchmatch-gallery.html`. Generated output
is a derived retail asset and is never committed.

## The premise: scale recurrence through the ALP blend

Retail reduces a map one octave with its palette blend table: a 2x2 block
`(a b / c d)` becomes `ALP[ALP[a,b], ALP[c,d]]`, nearest palette entry to the
blend [03 §3.7]. That gives an exact pair of "high resolution" and "low
resolution" images for free: the authored map and its own ALP reduction. We
assume the map's texture statistics recur one octave up (grass one octave
down still looks like grass), so an authored 2x2 block whose reduction equals
a source pixel is a legitimate 2x rendering of that pixel *if* its reduced
neighborhood resembles the source pixel's neighborhood. The upscale is then
a nearest-neighbor search, not a model, and the ALP cycle holds by
construction: reducing the 2x output reproduces the retail map pixel for
pixel (100% on every map tried with `-relax=false`; see step 5 for why the
default trades some of that for tone).

**One-octave-down validation.** Reducing Moon Quartet to half size, upscaling
the reduction and comparing with the real map shows what the premise buys and
what it does not. Block texture statistics are reproduced (fraction of
uniform 2x2 blocks, luma range within a block), and the tone is right by
construction. But the authored map is 6.5 luma brighter than its own
reduction, so a faithful copy is brighter than the input by the same amount;
and the synthesized detail is noisier than the real map's, which has smooth
oriented streaks that no independent per-pixel block choice recovers. Black
regions keep hard, stair-stepped boundaries in the real map too: only 9% of
authored grey blocks touching black contain a black pixel, so sub-pixel
anti-aliasing of those edges is not something the data supports.

A small ML model (`tools/mapupscale/ml/v5.py`) trained on the same pairs
produced visually similar results with a closed vocabulary of ~9k blocks and
a 150 s runtime; this search copies from ~250k authored blocks in about a
second, so it replaced the model.

## Algorithm

1. **Unique tiles.** The map is split into 32x32 tiles and deduplicated by
   content. Each unique tile is laid on an atlas with a 4-pixel halo copied
   from its first placement (a 5x5 reduced-pixel neighborhood spans 10 source
   pixels). Both the example database and the queries come from this atlas,
   so work scales with unique tiles, not placements (Moon Quartet: 4,388
   tiles for 93,940 placements). One 64x64 result per unique tile is also
   what the engine wants to store; tile seams are statistically and visually
   invisible because every block is chosen from the halo context anyway.
2. **Example database.** The atlas is reduced at the four 2x2 phase offsets.
   Every reduced position is an example: its authored 2x2 block, its parent
   index (the reduction), and its neighborhood features. Positions whose
   neighborhood would cross into another tile's cell are excluded. Blocks
   that would copy a rare bright palette entry under a different parent are
   excluded (they scatter highlights). Parent indices that only occur where
   two different tiles meet at a non-first placement are recovered by a
   parallel scan of the full map.
3. **Features.** Each 5x5 neighborhood of palette RGB (75 values) is
   projected to 8 PCA dimensions, fitted by power iteration on 100k windows
   drawn per *placement* (fitting per unique tile over-weights rare
   high-contrast tiles and visibly coarsens the output). Features are
   quantized to int16 and packed with the parent key and the block's RGB sum
   into one 24-byte record, so one random memory access decides a candidate.
4. **Search.** Classic PatchMatch, run independently per tile on one worker:
   four random initial candidates, then alternating raster scans that
   propagate the two visited neighbors' matches (shifted by one), a
   shrinking random window (radius 8, 4, 2, 1) around the current match, and
   two global draws per pixel. A candidate is admissible if its parent
   equals the query pixel, or is an admitted stand-in for it (step 5).
   Global draws pick a parent from the admitted set in proportion to its
   example count, then use alias tables weighted by how many
   placements share the example's tile, so the prior matches the map as laid
   out; half of them are restricted to a bucket keyed by parent and the
   coarse first two PCA coordinates, which lands near the query in feature
   space and roughly halves the mean match cost. Eight passes by default.
5. **Tone.** The ALP reduction is not tone-neutral: nearest-entry blending
   in a sparse palette brightens dark regions and drifts hue (Great Divide's
   authored map is R+10.5, G-3.0, B+8.0 above its own reduction; Painted
   Desert +5.5 luma). A pure copy inherits the inverse bias, so the 2x view
   sat about +4 luma above the 1x view, worst in dark areas. The candidate
   cost therefore adds `tone * |blockRGBsum - 4*parentRGB + spread/8 *
   meanNeighborError|^2`: each block is pulled toward its parent's colour and
   also asked to offset the mean error of its already-chosen 3x3 neighbors,
   so pixels compensate for one another where no darker block exists. With
   the defaults the shift drops to +0.3 luma on Great Divide and +1.1 on
   Painted Desert and the 8x8 local tone error from 4.1 to 0.4.

   On a sparse palette the tone term has a failure mode: for Moon Quartet's
   common greys almost no authored block is as dark as its parent (the
   blocks under the lum-59 grey average +10 luma above it), so the only
   candidates that satisfy the tone term are *uniform* blocks, and the
   output turns into a visible 2x2 grid of flat blocks along dark crevices
   (36% uniform blocks in the mid greys against 3% in the authored map).
   Two things address this. A dead zone (`-deadzone`, per-channel RGB-sum
   error tolerated at no cost) removes the tone term's preference for the
   exact parent colour. More importantly, **relaxation** (`-relax`, on by
   default) admits blocks that reduce to a palette entry one ALP step from
   the parent, meaning the blend of the two snaps to one of them, so no
   entry lies between; the neighbourhood comes from the map's own ALP
   table, tight on dense palettes and wide on sparse ones. A stand-in is
   admitted only when its blocks are on average closer to the parent's
   colour than the parent's own blocks, so it can only reduce the bias.
   Without that direction test the extra choice lets the feature term drift
   the tone (Great Divide's bright band went 6 luma dark, Painted Desert's
   dark band 3 luma brighter). The cost is that the ALP cycle is no longer
   exact: the output reduces to the retail map in 72-84% of pixels and to an
   adjacent palette entry in the rest (`cycle=` in the summary line).
6. **Assemble.** Each query pixel's matched block becomes its 2x2 output;
   pixels whose parent has no example anywhere (0.03% on Great Divide)
   become a uniform block of the parent.

Everything is deterministic: seeds derive from tile and sample indices, and
tiles are independent, so worker count does not change the result.

## Performance (M2 Pro, 12 workers, 8 passes)

| Map | Unique tiles | Total | Search |
|---|---|---|---|
| Great Divide | 2,773 | 1.8 s | 1.4 s |
| Moon Quartet | 4,388 | 3.6 s | 2.6 s |
| Painted Desert | 6,875 | 4.8 s | 3.5 s |

Relaxation costs 0.2-0.4 s of search per map (one more alias draw per global
draw and a larger candidate pool); `-relax=false` gives the earlier times.

The search is memory-latency bound on random reads into the record array;
`-cpuprofile` writes a Go CPU profile. The full-map database with jump-flood
propagation this replaced took 5.4 / 15.4 / 19.2 s at four passes.

## Open questions and next steps

- The one-octave-down validation above compares statistics; a perceptual
  comparison, and the question of whether the missing oriented detail can
  be recovered with larger or multi-scale patches, are open.
- The darkest band keeps uniform blocks above the authored rate (Moon
  Quartet dark band 20% against 12%; Great Divide 10% against 1%) because
  black has no darker stand-in; the +2 luma residual there is the same
  limit.
- Map features (metal, trees, rocks) are separate GAF art with their own
  palettes and need their own treatment; this tool is terrain only.
- Dark regions on maps like Painted Desert keep a +5 luma residual. Allowing
  a synthetic uniform parent block as a candidate would close it at the cost
  of flat spots; not done because it is not authored data.
- Engine integration: a 64px tileset container with loader-side ALP
  verification, and a `--remaster`-style flag to pick it.

## Usage

```sh
go run ./tools/mapupscale/export -list-maps
go run ./tools/mapupscale/export -map "Great Divide" -out /tmp/great-divide-mapupscale
go run ./tools/mapupscale/patchmatchgo -data /tmp/great-divide-mapupscale \
  -out tools/mapupscale/output/great-divide-patchmatch-go -full-preview
```

Flags: `-iterations` (8), `-tone` (12), `-spread` (16), `-deadzone` (8),
`-relax` (true), `-coherence` (64), `-seam` (0), `-seamzone` (-1 = authored
mean), `-settle` (2), `-samples` (100000), `-workers` (CPU count),
`-preview`, `-full-preview`, `-cpuprofile`.

## Coherence, seam and settle (2026-09-04)

Measured against the authored maps at native scale ("isolated" is the share
of pixels whose RGB distance to the mean of their eight neighbours exceeds
40; both rows compare directly), the original output had 30% more lone
specks than the authored art on the two smooth maps and, in a one-octave-down
test (reduce the map through the blend, upscale that, compare with the
authored map at the same scale), mushier fleck texture and smeared streaks.
Neither the tone term nor relaxation is the cause: `-tone 0` raises isolated
to 0.337 and `-relax=false` only lowers it to 0.264 on Great Divide.

Three terms were added; the defaults below are the shipped ones.

- **`-coherence W` (default 64)** — the Image Analogies synthesis term: the
  authored full-resolution pixels around a candidate block (two rows above
  and two columns left in the atlas, mirrored on backward passes) are
  compared with the output already synthesized around the query. It rewards
  copying contiguous authored structure, which restores the fleck texture
  and sharpens ridges and crater rims. It overrides the tone term a little,
  so `-tone` moved from 4 to 12. Supplement records have no authored
  surround and are exempt.
- **`-seam W` (default 0)** — charges edge pixel pairs between a candidate
  block and its chosen neighbours beyond `-seamzone` (default: the map's
  own mean adjacent-pixel distance). It removes boundary speckle but one
  weight does not fit all maps (16 matches Great Divide and over-smooths
  Moon Quartet), so it stays off unless calibrated per map.
- **`-settle N` (default 2)** — a pixel whose match survived N passes
  unchanged skips its random-window and global draws; propagation still
  runs. Costs 0.2–2.7% of match quality for ~30% of the CPU.

Records shrank from 28 to 16 bytes (int8 features: the first PCA coordinate
in units of 16, the rest in units of 4, weighted back in the distance) and
the feature-hash buckets gained a third coordinate, which is what lowered the
match cost; the coherence neighbourhood is table-driven and each pixel visit
evaluates its neighbour state once.

| Map | authored isolated / luma | before: cost, isolated, luma, CPU | now: cost, isolated, luma, CPU |
|---|---|---|---|
| Great Divide | 0.200 / 75.3 | 18978, 0.267, 76.1, 24.8 s | 16657, 0.271, 76.0, 17.9 s |
| Moon Quartet | 0.156 / 123.4 | 19488, 0.185, 124.3, 45.5 s | 16052, 0.177, 124.3, 38.6 s |
| Painted Desert | 0.637 / 148.0 | 26318, 0.622, 149.5, 66 s | 22526, 0.605, 149.5, 54.2 s |

"Before" is the previous code with `-coherence 64 -tone 12`; cost is the
mean feature distance of the final matches (lower is a better search), CPU
is user+sys over the whole run (the summary prints it as `cpu=`; wall times
on a loaded machine vary by 2x between identical runs). The remaining luma
drift is +0.7 / +0.9 / +1.5.

The one-octave-down test stays pessimistic for structure: a one-pixel line
at 1x is lost by the nearest-entry blend at half size, so nothing recovers
it there; at the real 1x to 2x step the line exists and only its edges are
synthesized. Iteration convergence on Great Divide before these changes
(mean cost 17.2k, 15.8k, 14.3k, 12.7k, 11.8k at 1, 2, 4, 8, 12 passes)
showed the search was not converged at 8; better candidates turned out to be
worth more than more passes.
