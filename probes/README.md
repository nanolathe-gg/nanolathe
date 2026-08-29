# Probes

Probes are small, data-driven scenarios for checking Nanolathe against the
retail content contract. They load authored maps, missions, and definitions
through the same VFS and catalog entry points used by the single-player
runtime; they do not contain copied retail bytes or alternate simulation
rules.

Keep scenarios under this directory in the format they exercise (for example,
`*.ota` for map setup). A probe should state the behavior it observes and cite
the owning research section. Prefer a focused scenario that records a stable
relationship, ordering, or hash over a census or implementation milestone.

## Probe kit (RWU-00-6)

Some research questions cannot be settled by static analysis and are marked
in the category docs with the decider *manual retail observation*
([`research/retail-executable-spec/README.md`](../research/retail-executable-spec/README.md),
`docs/PLAN_RESEARCH_COMPLETION.md` §2.4). The kit below is the authored data
for each such probe plus a written expected-observation template. **Nothing
here is automated**: a human copies the files into a retail install, runs the
game by hand, looks, and writes the observation back into the owning
research document. Every byte is authored by us from `research/formats`;
no retail bytes are copied. Binary files are produced by a `go run`-able
generator under `<probe>/gen/` (standard library plus the shared writers in
`kit/author/`) and are committed next to it so nothing needs building.

### Kit index

| Probe | Question | Owning section | Status |
| --- | --- | --- | --- |
| [`fog-corner-gaf/`](fog-corner-gaf/README.md) | Which fog-cache nibble bit paints which 16×16 quarter of the fog cell (`1=NW, 2=NE, 4=SW, 8=SE`)? | `[03 §3.4]`, `[03 R-RR16-A §3]`, doc 03 "Missing and unknown" | authored |
| [`minimap-alp-mirror/`](minimap-alp-mirror/README.md) | Is the radar picture's left/right orientation within a row mirrored on screen; which sample is the ALP blend's left operand? | `[03 §3.7]`, doc 03 "Missing and unknown" | authored |
| [`heading-zero-nose/`](heading-zero-nose/README.md) | Where do a model's source `+X`, `+Y` and `−Z` pieces land on screen at headings 0/90/180/270 (the `ta_probe_xz` fixture)? | `[03 §2.4]` | authored |
| [`dense-pack-fringe/`](dense-pack-fringe/README.md) | Is a later footprint that overlaps a live anchor cell rejected, or does it silently overwrite? | `[03 §2.2]`, `[03 §5.1.2]`, doc 03 "Missing and unknown" | authored |
| packed position-half COB | Is the packed coordinate's high half X or Z? | `[04 R-COB-03 §3]` | **superseded** by `[04 R-COB-03 §3]` (2026-08-28): high half X, low half Z, settled by static trace; "the `TODO(question)` and its probe are withdrawn" |
| lightning-segment calibration map | Physical units of the render-type-7 segmentation constant | `[03 R-FX-01 §2]`, `[06 R-WFX-01 §1]` | **superseded** by `[03 R-FX-01 §2]` (2026-08-29): the constant is `5 << 16`, i.e. `trunc(dist / 5)` whole world units per segment in map space |
| [`map-edge-capture/`](map-edge-capture/README.md) | What is drawn for in-map void cells versus out-of-map clipped regions, and does the backbuffer retain stale bytes beyond the play rect? | `[03 §2.2]`, `[03 §4.1]`, doc 03 "Confidence limits" | authored |
| [`minimap-cross/`](minimap-cross/README.md) | Visual confirmation of the five-pixel viewport cross, and whether a stock session runs with minimap mode byte 2 | `[03 §3.12]`, `[07 R-CAM-01 §11]` | authored (procedure only, no data) |
| [`bar-fill-0/`](bar-fill-0/README.md) | Palette index of the radar letterbox bars | `[03 §3.7]`, doc 03 "Missing and unknown" | authored |
| [`aoe-dedup/`](aoe-dedup/README.md) | Is the 20-entry unit memory of area damage reachable, and what happens to the 21st unit? | `[06 §9.3]`, doc 06 "Missing and unknown" | authored |

Two further candidates named while planning the kit were checked and need
no probe: `[03 R-WATER-01 §1]` and `[03 R-WATER-01 §2]` closed the
wake-rectangle and water/lava questions statically, and `[03 R-FONT-01 §1]`
through `[03 R-FONT-01 §7]` closed the FNT metrics; the residuals those
sections list are static-trace items, not observations.

### How a human runs retail manually and records an observation

1. **Know your install.** The reference install is `~/TotalAnnihilation`
   (run through the Porting Kit wrapper on macOS). Note the build: the
   `TotalA.exe` MD5 and the highest mounted patch archive (`rev31.gp3` is
   3.1). Write both into the observation.
2. **Install the probe as loose files.** Copy the probe's subdirectories
   (`maps/`, `units/`, `anims/`, `palettes/`, `features/`, `objects3d/`,
   `scripts/`, `camps/`) over the install root so the files sit beside the
   `.hpi` archives, e.g. `cp -R probes/fog-corner-gaf/anims ~/TotalAnnihilation/`.
   Loose host files are the **first** provider the VFS tries on every open
   `[02 §2]`, ahead of `rev*.gp3`, `*.ccx`, `*.ufo` and `*.hpi`, so a loose
   `anims/fog.gaf` shadows the archived one. Catalog enumeration (units,
   features, maps, campaigns) is a union in that same order, so a loose
   `maps/ta_probe_alp.ota` appears in the skirmish map list and a loose
   `camps/ta_probe_xz.tdf` in the campaign list. Remove the files afterwards
   — a leftover `fog.gaf` or `PALETTE.ALP` changes every later session.
3. **Start the scenario by hand.** Skirmish probes: Single Player → Skirmish,
   pick the probe map, one AI opponent, then start. Campaign probes: Single
   Player → New Campaign (the `Campaign` list shows the probe campaign for
   either side because it authors `campaignside=ALL`, `[08 "Enumeration of
   campaigns"]`), choose the probe mission, Easy difficulty (the probe
   authors only an `Easy` schema; `[08 "Schema and start-position
   selection"]` tries Easy first on difficulty zero).
4. **Look at exactly what the probe README says**, in the order it says.
   Take screenshots with Ctrl+F9 (the retail screenshot key in the `[07 §2]`
   key table) or the host's capture; when a palette index matters, prefer
   an indexed capture and otherwise compare RGB against `PALETTE.PAL`
   ([fmt pal]: index 0 is `(0,0,0)`, 255 is `(255,255,255)`).
5. **Fill in the observation table** in the probe README (copy it into the
   research edit; do not edit the kit's template in place). Every row has
   candidate outcomes taken from the research text; if what you see matches
   none of them, write what you see — an unexpected observation is the
   valuable kind.
6. **Record it in the owning document.** Edit the category doc in place
   under the owning section (the README names it): add a paragraph
   *Observed (retail run YYYY-MM-DD, `<probe name>`)*, quote the observation,
   and state which candidate it confirms or falsifies. An observation may
   promote a **Supported inference** to *Observed* or falsify it; it never
   replaces a static trace as the source of a constant
   (`docs/PLAN_RESEARCH_COMPLETION.md` §2.4). Then remove the matching
   bullet from the doc's "Missing and unknown" list and the `TODO(question)`
   marker in code that cites it. Keep the screenshot outside the repo (the
   reference install's `Screenshots/` directory); cite its file name.
