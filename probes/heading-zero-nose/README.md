# heading-zero-nose — the `ta_probe_xz` fixture

## Question

The 3DO import applies one persistent load-time half-turn (negating X and
Z of every vertex and every parent translation — `H_A`, established), and
engine heading 0 travels toward `+Z` and increases toward `+X`
(`[04 R-MOV-01 §2]`). What has not been observed is the resulting
**heading-zero nose mapping**: at heading 0, on which side of the unit
origin does a piece authored at source `−Z` (the model front, [fmt 3do])
appear on screen, and where does a source `+X` piece go — and how do both
rotate through 90°, 180° and 270°.

Owning section: `[03 §2.4]`, whose text reads:

> heading-zero nose mapping remains supported inference, probe-pending — the
> `ta_probe_xz` fixture (child translation signs at headings 0/90/180/270)
> is designed to settle it

The same observation settles the `Angle` → facing caveat of [fmt ota]
("The exact `Angle` → facing mapping (degrees, 0 = which direction,
rotation sense) is unconfirmed"), since the placed units take their heading
from the authored `Angle` word (`[08 R-TRIG-01 §9]`).

## Data

| File | Content |
| --- | --- |
| `objects3d/ta_probe_xz.3do` | Root `base` (grey 16×12×16 body over a 32×32 ground plate at primitive 0, selection value 0) with three coloured children whose translations are nonzero on exactly one source axis: **`nose` white at (0, 6, −24)** (source −Z), **`px` maroon at (+24, 6, 0)** (source +X), **`py` green at (0, +22, 0)** (source +Y, marks the origin). Flat-coloured quads only (`IsColored` bit 0 set). |
| `scripts/TA_PROBE_XZ.COB` | Minimal script: `Create` and `Killed` return 0; piece table `base, nose, px, py`. |
| `units/TA_PROBE_XZ.FBI` | Static structure-class unit (`BMcode=0`, no `MovementClass`, no weapons) with the catalog admission line ([fmt fbi] `Copyright`, `Downloadable=1`). |
| `maps/ta_probe_xz.ota` / `.tnt` | Campaign mission on a flat green 2048×2048 map: four `Player=1` units at `Angle=0` (900,900), `90` (1140,900), `180` (900,1140), `270` (1140,1140) around the camera start (1020,1020), and one `Player=2` unit at (1800,1700) so the mission does not end at the first victory poll ([fmt ota]). |
| `camps/ta_probe_xz.tdf` | Campaign wrapper (`campaignside=ALL`, `MISSION0`). |
| `gen/main.go` | Regenerates the 3DO, COB and TNT: `go run ./gen`. |

Screen conventions: map X grows to the right, map Z grows downward
([fmt ota] "Coordinates and size"); the world is drawn with the `Z − Y/2`
shear (`[03 §2.4]`), so the tall green `py` piece leans up-screen from the
origin — use its *base* as the origin marker.

## Manual steps

1. Copy `units/`, `objects3d/`, `scripts/`, `maps/`, `camps/` into the
   install root.
2. Single Player → New Campaign → campaign "RWU-00-6 heading-zero nose
   probe", Easy. The camera starts between the four units.
3. Screenshot the four units at the default zoom. For each, record the
   screen offset (in pixels, right = +, down = +) of the **white** piece
   and of the **maroon** piece from the base of the **green** piece.
4. Hover each unit to confirm which is which (all four are named "Probe
   XZ"; identify by position: NW = Angle 0, NE = 90, SW = 180, SE = 270).

## Expected-observation template

Candidate mappings at `Angle=0` (the load-time transform is `H_A`; the
observation decides the *nose* sign and the rotation sense):

* **A** — nose (source −Z) appears **below** the origin (screen south, world
  +Z, the direction heading 0 travels); px (source +X) appears **left**
  (world −X). This is `H_A` applied and heading 0 = +Z.
* **B** — nose **above** (world −Z), px **left**: the front points away from
  travel (the `H_C`-like reading `[03 §2.4]` rejected statically).
* **C** — nose **above**, px **right**: no persistent conversion at all.
* **D** — nose **below**, px **right**: source-Z-only reflection ([fmt 3do]
  "convert source Z with `z = −z`"), which is a mirror of A.

| Unit | white `nose` offset (dx, dy) | maroon `px` offset (dx, dy) | Candidates | Observed |
| --- | --- | --- | --- | --- |
| Angle 0 (NW) | | | A / B / C / D above | |
| Angle 90 (NE) | | | if heading increases from +Z toward +X, the Angle-90 nose points screen **right** under A; screen **left** means the opposite rotation sense | |
| Angle 180 (SW) | | | nose opposite to the Angle-0 nose; px opposite to the Angle-0 px | |
| Angle 270 (SE) | | | nose opposite to the Angle-90 nose | |
| `py` lean | | | the green piece leans up-screen by about half its height (the `Z − Y/2` shear) | |

Record also whether `Angle=90` turns the model a quarter turn at all (an
`Angle` conversion of `trunc(deg × 65536 / 360)` predicts exactly a quarter
turn, `[08 "Mission placement record"]`).

## Recording the result

Edit `research/retail-executable-spec/03-world-visibility-rendering-audio-and-video.md`
in `[03 §2.4]` at the sentence "heading-zero nose mapping remains supported
inference, probe-pending": add *Observed (retail run YYYY-MM-DD,
heading-zero-nose)* naming the outcome letter and the four offset pairs.
Outcome A promotes the mapping to *Observed*; B, C or D falsifies the
current reading — write the observed mapping and state what the previous
text said. Also close the `Angle` caveat in `research/formats/ota.md`
"Unknowns and caveats" with the observed 0-direction and rotation sense, and
remove the corresponding `TODO(question)` marker in code that cites
`[03 §2.4]` for the nose sign.
