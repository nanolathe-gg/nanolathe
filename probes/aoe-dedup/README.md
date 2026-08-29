# aoe-dedup — the 20-entry unit memory of area damage

## Question

Area damage walks the plot cells around the impact by increasing Z then X,
visiting "unit slot zero, unit slot one, then the feature/terrain
candidate" in each cell. "Unit deduplication happens **before** the radius
test, against a memory of at most 20 unit pointers; a candidate already
remembered is skipped entirely, and a candidate encountered when the memory
is full is still processed but not remembered, so a later occurrence is
processed again" (`[06 §9.3]`, **Established**). A unit with a 2×2
footprint occupies four cells, so once the memory is full every further
unit is damaged once per cell it occupies. Whether that is reachable on
retail content is the open item:

> Practical reachability of signed 16-bit AOE distance wrap, and of more
> than 20 unique unit or 64 unique feature-cell candidates, in accepted
> retail maps · §9.3 · asset census over the map corpus (the AOE dedup map
> probe).

(doc 06 "Missing and unknown"). The named decider is a **census of stock
maps** for the "in accepted retail maps" half; this probe settles the other
half by construction — it authors a scenario that exceeds twenty candidates
and observes what the 21st unit receives — and so confirms or falsifies the
"processed but not remembered" reading with an observation. The 64-cell
feature memory and the 16-bit distance wrap are not exercised here.

## Data

| File | Content |
| --- | --- |
| `units/TA_PROBE_BLK.FBI` | Static 2×2-cell target, `MaxDamage=14000`. |
| `units/TA_PROBE_BOOM.FBI` | The same unit with `SelfDestructAs=COMMANDER_BLAST` (the stock weapon: area of effect 950, damage 9999, edge effectiveness 0.75, read from the shipped table) and a five-second countdown. |
| `objects3d/ta_probe_blk.3do`, `objects3d/ta_probe_boom.3do` | Navy / maroon boxes on a ground plate. |
| `scripts/TA_PROBE_BLK.COB`, `scripts/TA_PROBE_BOOM.COB` | Minimal `Create`/`Killed` scripts. |
| `maps/ta_probe_aoe.tnt`, `maps/ta_probe_aoe.ota` | Campaign mission on a flat grey 2048×2048 map: a 5×5 grid of 2×2-cell units at a 3-cell (48 px) pitch centred on cell (64,64); the centre slot is `TA_PROBE_BOOM`, the other 24 are `TA_PROBE_BLK`, all `Player=1`; one `Player=2` block at (1800,1700) keeps the mission alive. |
| `camps/ta_probe_aoe.tdf` | Campaign wrapper. |
| `gen/main.go` | Regenerates every binary **and the OTA**: `go run ./gen`. |

Why 14000: blast radius `R = 950 >> 1 = 475` world units covers the whole
grid (the farthest target is under 140 units away), the falloff is
`(1 − 0.75)·f² + 0.75` so one hit deals between 7499 and 9999 (`[06 §9.3]`
falloff, before veterancy) — never lethal — and any second hit is (two hits
deal at least 14998). Survival therefore reads "hit once", death reads
"hit at least twice".

Target labels, in the row-major order in which the cell walk first sights
them (row = grid row from the north, col from the west):

```
 1  2  3  4  5
 6  7  8  9 10
11 12  X 13 14        X = TA_PROBE_BOOM
15 16 17 18 19
20 21 22 23 24
```

## Manual steps

1. Copy `units/`, `objects3d/`, `scripts/`, `maps/`, `camps/` into the
   install root.
2. Single Player → New Campaign → "RWU-00-6 AOE dedup probe", Easy. The
   camera starts on the grid.
3. Screenshot the grid. Select the maroon centre unit and press **Ctrl+D**
   (self-destruct); wait out the five-second countdown.
4. After the explosion, screenshot the grid, then hover every surviving
   block and note its health from the unit panel (percentage or bar).
5. Record which labelled positions are empty.

## Expected-observation template

| Item | Candidate outcomes | Observed |
| --- | --- | --- |
| which targets are destroyed | **M** exactly the last four in walk order (21, 22, 23, 24 — the east four of the south row): the memory holds the first twenty distinct units, later units are hit once per occupied cell; **A** none destroyed (every unit hit once: no 20-entry limit, or a larger memory); **B** a different set — record it (it names the real walk order or memory size); **C** all destroyed (damage far above the reading; check the weapon table) | |
| health of the survivors | between about 29 % and 46 % of 14000 (one hit of 7499..9999), varying with distance; a survivor is by construction a unit hit exactly once | |
| health pattern vs distance | survivors nearer the centre lower than those farther away (falloff), or flat | |
| the `Player=2` block at (1800,1700) | untouched (outside `R`) | |

Also record: `TotalA.exe` MD5, patch level, screenshot names, and the
weapon values read from the shipped `COMMANDER_BLAST` record on that build.

## Recording the result

Edit `research/retail-executable-spec/06-weapons-projectiles-damage-and-effects.md`
in `[06 §9.3]` after the "Unit deduplication happens **before** the radius
test" paragraph: add *Observed (retail run YYYY-MM-DD, aoe-dedup)* with the
destroyed set and survivor healths. Outcome M promotes "processed but not
remembered" from trace-only to *Observed* and answers the reachability
question for authored content (retail-map reachability still needs the
census the bullet names — reword the bullet to the census only rather than
deleting it). Outcome A or B falsifies the memory-size or walk-order
reading — state what the previous text said and re-trace before changing
the constant. Update the `TODO` marker in the area-damage code that cites
`[06 §9.3]` for the 20-entry memory.
