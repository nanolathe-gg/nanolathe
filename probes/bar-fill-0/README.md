# bar-fill-0 — the radar letterbox bar colour

## Question

On a map whose play area is not square the radar picture is `RadarW ×
RadarH` with one of them 126 and the other scaled, centred in the 126×126
lens; the picture allocation is exactly `RadarW × RadarH` bytes, so "the
bars are therefore HUD canvas outside the radar rect, not picture heap"
(`[03 §3.7]` "Aspect and letterbox", **Established**). Their colour is
inferred:

> **Supported inference.** The bar pixels read 0 (black), consistent with
> the panel clear and the dark fog-fill index; a canvas-capture probe
> settles it. `TODO(question):` bar fill color.

listed in doc 03's "Missing and unknown" as:

> Bar fill colour · §3.7 · manual retail observation (canvas-capture probe;
> the bar fill 0 probe). Marked `TODO(question)`.

## Data

| File | Content |
| --- | --- |
| `maps/ta_probe_bars.tnt` | 1024×4096 px map (64×256 cells, height 20), no embedded minimap, white/grey 4-tile checkerboard art so the picture is unmistakable. Play area 992×3968: `RadarW = 992·126/3968 = 31`, `RadarH = 126`, `originX = (126 − 31)/2 = 47` — 47 bar columns on each side. |
| `maps/ta_probe_bars.ota` | Skirmish header (`Network 1`, four start positions along the map). |
| `gen/main.go` | Regenerates the TNT: `go run ./gen`. |

## Manual steps

1. Copy `maps/` into the install root; skirmish on `ta_probe_bars`, Mapped
   on.
2. Screenshot the minimap panel. Prefer an indexed (8-bit) capture; with an
   RGB capture compare the bar pixels against `PALETTE.PAL` ([fmt pal]:
   index 0 is `(0,0,0)`).
3. Read the pixels in the bar columns left and right of the 31-pixel-wide
   picture, at the top, middle and bottom of the lens.
4. Toggle the minimap's own blink cycle (wait a few seconds) and confirm
   the bar colour does not change; then move the camera so the viewport
   cross sits at the picture edge and confirm the cross clips at the
   picture, not at the lens.

## Expected-observation template

| Item | Candidate outcomes | Observed |
| --- | --- | --- |
| bar pixel RGB / index | **0** black, palette index 0 (the inference); **P** the panel art's own colour (the bars are untouched HUD canvas showing whatever the panel painted); **D** the dark fog-fill index; other — record RGB | |
| picture width | 31 pixels, centred with 47-pixel bars (confirms the truncating aspect arithmetic); a different width names a different formula | |
| bar stability over time | unchanged across the blink cycle and camera moves; or repainted | |
| radar contacts / cross drawn into the bars? | never (clipped to the inclusive picture rect), or yes | |

Also record the window resolution, `TotalA.exe` MD5, patch level,
screenshot names.

## Recording the result

Edit `research/retail-executable-spec/03-world-visibility-rendering-audio-and-video.md`
in `[03 §3.7]`: replace the "**Supported inference.** The bar pixels read 0
… `TODO(question):` bar fill color." sentences with *Observed (retail run
YYYY-MM-DD, bar-fill-0)* and the value seen. Outcome 0 promotes the
inference to *Observed*; P or D falsifies it — state what the previous text
said. Delete the "Bar fill colour" bullet from "Missing and unknown" and
remove the `TODO(question)` marker in the HUD radar panel that cites it.
