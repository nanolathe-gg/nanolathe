# dense-pack-fringe — a later footprint over a live anchor cell

## Question

Every feature placement goes through one stamping service that validates
the anchor rectangle against the map bounds, "checks footprint collision by
attempting a conditional teardown of any overlapping occupant (guarded by
`indestructible`)", stamps the anchor and fringe cells, and notifies derived
occupancy (`[03 §5.1.2]`). TNT attribute features are stamped first, OTA
`[features]` later, through the same path. Whether the later stamp is
**rejected** or **silently overwrites** when it covers a live anchor — and
whether `indestructible` on the earlier feature changes that — is the open
"dense-pack rule".

Owning section: `[03 §2.2]`, stated in doc 03's "Missing and unknown" list
as:

> Dense-pack rule: whether a footprint overlapping a live anchor cell is
> rejected or silently overwrites · §2.2 · manual retail observation
> (dense-pack fringe map probe). Marked `TODO(question)`.

and in the body of `[03 §2.2]`:

> `TODO(question)` remains only for the dense-pack rule — whether a
> footprint that overlaps a live anchor cell is rejected or silently
> overwrites — which the stamper's occupancy guard decides per consumer.

## Data

| File | Content |
| --- | --- |
| `features/ta_probe_dense.tdf` | Three 2×2-cell sprite features: `ta_probe_dense_a` (maroon), `ta_probe_dense_b` (green), `ta_probe_dense_c` (white, `indestructible=1`). |
| `anims/ta_probe_dense.gaf` | Their sprites: `blocka`, `blockb`, `blockc`, one 32×32 solid frame each. |
| `maps/ta_probe_dense.tnt` | 2048×2048 flat grey map with the **earlier** stamps in the attribute grid (anchor cell + three `0xFFFE` fringe cells, [fmt tnt]): `a` at cell (40,40), `c` at (60,40), `a` at (40,60). |
| `maps/ta_probe_dense.ota` | Skirmish header whose `[features]` block stamps `b` **later** at cell (39,39) over pair 1's anchor, (59,39) over pair 2's anchor, (80,39) over nothing (control), and (41,59) over pair 4's *fringe* cell only. Cell = pixel / 16; the authored pixel is the cell centre. |
| `gen/main.go` | Regenerates the GAF and TNT: `go run ./gen`. |

The `b` anchors sit one cell north-west of the `a`/`c` anchors, so under
"overwrite" the green sprite lands one cell up-left of where the maroon or
white sprite was, and under "reject" no green sprite appears there at all.

## Manual steps

1. Copy `features/`, `anims/`, `maps/` into the install root.
2. Skirmish on `ta_probe_dense`, Mapped on. The camera starts near the
   four sites (StartPos1 is at (1000,1300)); the sites are at map pixels
   (640,640), (960,640), (1280,640) and (640,960), i.e. cells (40,40),
   (60,40), (80,40) and (40,60).
3. Screenshot each site; hover it to read the feature description shown
   ("Probe block A/B/C").
4. Order the commander to walk *through* each site: a blocking feature
   stops it (`blocking=1`), which tells whether an invisible feature still
   occupies the cells.

## Expected-observation template

| Site | Setup | Candidate outcomes | Observed |
| --- | --- | --- | --- |
| pair 1, cell (40,40) | later `b` covers `a`'s anchor | **R** rejected: only maroon `a` visible, `b` dropped silently; **O** overwrite: green `b` visible one cell up-left, `a` gone (torn down); **B** both visible (overlapping sprites) — then record which one blocks movement and which the hover names | |
| pair 2, cell (60,40) | later `b` covers indestructible `c`'s anchor | R: only white `c`; O: green `b`, white gone (the guard does not protect `c`); **G** guard: white `c` stays *and* `b` is dropped even if pair 1 overwrote — the `indestructible` guard vetoes the teardown | |
| pair 3, cell (80,39) | control, empty ground | green `b` visible at (1288,632) — if absent, OTA feature placement itself is not working and the other rows are void | |
| pair 4, cell (40,60) | later `b` covers only a fringe cell of `a` | same R/O/B/G vocabulary; a difference from pair 1 means the guard distinguishes anchor overlap from fringe overlap | |
| blocking walk | — | for each site, which sprite (if any) stops the commander | |

Also record: `TotalA.exe` MD5, patch level, screenshot file names.

## Recording the result

Edit `research/retail-executable-spec/03-world-visibility-rendering-audio-and-video.md`
in `[03 §2.2]` at the sentence beginning "`TODO(question)` remains only for
the dense-pack rule": replace it with *Observed (retail run YYYY-MM-DD,
dense-pack-fringe)* giving the four site outcomes, and add one sentence to
`[03 §5.1.2]`'s collision description saying which of reject/overwrite the
"conditional teardown" produces and what `indestructible` does. Evidence
level: *Observed* (the stamping service's existence and order stay
**Established**; the observation fixes the collision outcome). Delete the
"Dense-pack rule" bullet from the "Missing and unknown" list and the
`TODO(question)` marker in the feature stamper that cites it.
