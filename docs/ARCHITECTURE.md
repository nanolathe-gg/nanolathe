# Nanolathe — Architecture

Nanolathe is a clean-room reimplementation of the Total Annihilation engine in
Go. Behavior comes from `research/retail-executable-spec/` (what the retail
executable does) and `research/formats/` (how its files are laid out);
content comes from the original assets, mounted at run time. See the
[publication review](PUBLICATION.md) for the excluded generated remaster art
and the curated history boundary. This document is the map of the Go tree: what each package is for,
how packages depend on each other, where the authoritative tick lives, how the
build is verified, and how a citation in a comment is resolved. Each subsystem
has its own design document; this one only says where the boundaries are.

| Design document | Owns |
|---|---|
| [DESIGN_RUNTIME_DETERMINISM](DESIGN_RUNTIME_DETERMINISM.md) | clock, random streams, fixed point, pools, the tick and its publication boundary, boot |
| [DESIGN_CONTENT_VFS](DESIGN_CONTENT_VFS.md) | archive overlay, format parsers, compiled catalogs, settings |
| [DESIGN_WORLD_VISIBILITY](DESIGN_WORLD_VISIBILITY.md) | terrain, plot cells, placement, wind, line of sight and fog |
| [DESIGN_UNITS_ORDERS_COB](DESIGN_UNITS_ORDERS_COB.md) | unit pool and lifecycle, order queues and handlers, the COB virtual machine, the piece hierarchy |
| [DESIGN_MOVEMENT_PATH](DESIGN_MOVEMENT_PATH.md) | path search, movement profiles, ground steering, collision, flight, transports |
| [DESIGN_ECONOMY_CONSTRUCTION](DESIGN_ECONOMY_CONSTRUCTION.md) | resource ledger and settlement, build requests and factories, features |
| [DESIGN_WEAPONS_PROJECTILES](DESIGN_WEAPONS_PROJECTILES.md) | weapon slots, aiming, the projectile pool, impact and damage, death |
| [DESIGN_INTERFACE_HUD_INPUT](DESIGN_INTERFACE_HUD_INPUT.md) | GUI files and screens, the battle HUD, input, camera, selection and command dispatch |
| [DESIGN_SESSIONS_AI_SAVE](DESIGN_SESSIONS_AI_SAVE.md) | session states, campaign and mission loading, triggers, the computer player, saves, the headless runner |
| [DESIGN_PRESENTATION_CLIENT](DESIGN_PRESENTATION_CLIENT.md) | window and frame loop, the frame composer, model rasterizer, effects, palette, audio |
| [DESIGN_GPU_RENDERER](DESIGN_GPU_RENDERER.md) | the recorded frame draw list, the classic (software) and modern (GPU) executors, the renderer switch, visual parity policy and prototype gates |

Rules that cut across every package are in [INVARIANTS.md](INVARIANTS.md);
places where the reference install disproves the written contract are in
[SPEC_CONFLICTS.md](SPEC_CONFLICTS.md).

## 1. Purpose and scope

The engine plays single-player Total Annihilation: skirmish against the
computer player, and the campaign missions with their briefings, triggers and
continuation. In scope are everything those need — content loading, the
economy, construction, movement and pathfinding, visibility, weapons and
damage, the COB script machine, features and fire, the skirmish planner, the
GUI and HUD, camera and minimap, audio, effects, and save/load of a
single-player battle.

Authoritative behavior follows retail, including documented faults. Bounds
rejection and the renderer presentation policies of DESIGN_GPU_RENDERER are the
sanctioned departures under [INVARIANTS.md](INVARIANTS.md) I11. Original preserves
the retail raster reference; GPU Classic permits visually reviewed raster
approximations, and Enhanced has separately designed visual features. These
presentation choices never select alternate simulation behavior. Current
prototypes remain behind `--renderer=modern`. That mode uses GPU drawing only:
software model bodies/shadows are not a fallback, and unimplemented GPU stages
remain explicitly omitted (DESIGN_GPU_RENDERER §9).

### Deliberately out of scope

Every research section is either cited by a design document or listed here.
These are excluded on purpose, so that "no design document covers X" is a bug
in this table or in a design document, never a silent hole. Nothing below is
to be implemented because it looked missing; changing the scope means
changing this table first.

| Research section | Why it is out |
|---|---|
| `[03 §9]` Smacker cinematics and movie capture | No video decoder and no capture path. The front end goes straight to `MAINMENU`; `[07 R-FE-01 §3]` describes the movie stage that is skipped. Also excluded: the `CDCHECK` gate and the ending movie. |
| `[07 §12]` Lobby and session shell | The multiplayer lobby. The single-player skirmish setup screen is a different surface, owned by DESIGN_INTERFACE_HUD_INPUT and DESIGN_SESSIONS_AI_SAVE through `[07 R-FE-01 §5]` and `[08 R-SKIR-01]`. |
| `[08 "DirectPlay transport"]`, `[08 "Packet framing and dispatch"]`, `[08 "Send pacing and batching"]`, `[08 "Receive buffering"]`, `[08 "Ping and adaptive timing"]`, `[08 "Lockstep advancement"]`, `[08 "Synchronization and integrity checks"]`, `[08 "Disconnect, resign, and peer loss"]` | Networking. `[08 R-OOS-01 §1]` names the packets the *local* path still constructs; those are in scope and DESIGN_SESSIONS_AI_SAVE cites them. |
| `[08 "Multiplayer saves"]` | Follows from the above. Single-player save/load is DESIGN_SESSIONS_AI_SAVE (`[08 "Save-file organization"]`, `[08 "Load process"]`). |
| `[08 "Replay"]` | Only an optional debug recorder is in scope, and none is designed. No competitive desync hashes. |

Meta sections carry no behavior and are cited by nobody by design:
`[01 §10]`, `[03 §10]`, `[04 §11]`, `[06 §14]`, and each document's "Purpose
and evidence boundary", "Dependencies" and "Missing and unknown".

## 2. Source layout

Module path `github.com/nanolathe-gg/nanolathe`. One line per package; the
design document named is the one whose contract list and research map the
package implements.

### Content and formats

| Package | Responsibility | Design document |
|---|---|---|
| `vfs` | The logical content namespace: an overlay of loose directories and HPI-family archives (HAPI, cipher, SQSH) with mount order, shadowing and a provenance manifest | DESIGN_CONTENT_VFS |
| `formats` | Lossless readers and writers for the authored formats — TDF (comments blanked with byte offsets preserved), GAF, TNT, 3DO, OTA, PAL, PCX, FNT, WAV, GUI, SCT, BMP | DESIGN_CONTENT_VFS |
| `internal/content` | Compiles authored data into immutable definitions with defaults and conversions applied once: units, weapons, features, movement classes, sides, sounds, maps, AI profiles, battle tables; the catalog hash; and the authored animation metadata the SIMULATION depends on — a feature's burn/die/reclaim frame geometry and lifetimes in visits, an effect entry's frame count (`CompileSimArt`), compiled before the session's features and strips exist so headless and windowed battles run one simulation | DESIGN_CONTENT_VFS |
| `internal/settings` | Front-end preferences that survive a restart (last skirmish setup, per-slot side/colour/ally, difficulty) | DESIGN_CONTENT_VFS |

### Runtime core

| Package | Responsibility | Design document |
|---|---|---|
| `internal/clock` | The 30 Hz fixed-step budget: scaled host time, the `0..5` sub-tick clamp, speed and pause, the scheduler save box | DESIGN_RUNTIME_DETERMINISM |
| `internal/sim/numeric` | `Fixed` 16.16, `uint16` angles, the 512-entry sine table, truncation toward zero | DESIGN_RUNTIME_DETERMINISM |
| `internal/sim/rng` | The two random streams: Park-Miller simulation stream and the CRT stream | DESIGN_RUNTIME_DETERMINISM |
| `internal/pool` | Fixed-capacity pools with slot 0 null, lowest-free allocation and immediate reuse | DESIGN_RUNTIME_DETERMINISM |
| `internal/frame` | The committed frame: what the simulation publishes at the end of each sub-tick and what presentation samples | DESIGN_RUNTIME_DETERMINISM (publication), DESIGN_PRESENTATION_CLIENT (consumption) |
| `internal/session` | The authoritative session: state machine, battle entry for skirmish and mission, the twelve-phase tick, publication, results, save staging and restore, effect strips, eyeballs, status cues | DESIGN_RUNTIME_DETERMINISM (tick, publication), DESIGN_SESSIONS_AI_SAVE (states, entry, results, saves) |
| `internal/version` | Build identity | DESIGN_RUNTIME_DETERMINISM |

### World

| Package | Responsibility | Design document |
|---|---|---|
| `internal/world` | Terrain from TNT, height queries, coordinate conversions, the plot-cell grid, yard maps and placement, feature stamping, wind | DESIGN_WORLD_VISIBILITY |
| `internal/visibility` | The word and byte visibility grids, sensor stamps, the diamond predicate, fog state | DESIGN_WORLD_VISIBILITY |

### Units and behavior

| Package | Responsibility | Design document |
|---|---|---|
| `internal/units` | The unit pool, per-unit records, creation and death lifecycle, the unit limit | DESIGN_UNITS_ORDERS_COB |
| `internal/orders` | The order descriptor table, order records and queues, the queue pump and result codes, order handlers, command resolution | DESIGN_UNITS_ORDERS_COB |
| `internal/cob` | The compiled-script loader, the opcode interpreter, threads, engine ports and callbacks | DESIGN_UNITS_ORDERS_COB |
| `internal/model` | The 3DO piece hierarchy and transform composition | DESIGN_UNITS_ORDERS_COB |
| `internal/path` | Weighted A\* over the plot grid: heap, node store, goal families, the request scheduler | DESIGN_MOVEMENT_PATH |
| `internal/movement` | Movement profiles, route publication, ground steering, collision and occupancy, flight, takeoff and landing, transports | DESIGN_MOVEMENT_PATH |
| `internal/airdiag` | Diagnostic harness that flies one named aircraft through a real session and records a per-tick trace | DESIGN_MOVEMENT_PATH |
| `internal/economy` | Per-player ledger and buckets, the settlement deadline, two-stage admission, sharing, alliances, statistics | DESIGN_ECONOMY_CONSTRUCTION |
| `internal/construction` | Build requests and queues, factory lifecycle, nanoframes and progress, capture, resurrection, reverse construction | DESIGN_ECONOMY_CONSTRUCTION |
| `internal/features` | Feature runtime: placement, reclaim, burning, reproduction, sinking, geothermal | DESIGN_ECONOMY_CONSTRUCTION |
| `internal/combat` | Weapon slots and targeting, aiming and the ballistic solver, the projectile pool, fire, motion families, impact, damage, death causes, stockpiles, meteors | DESIGN_WEAPONS_PROJECTILES |

### Sessions, campaign and computer player

| Package | Responsibility | Design document |
|---|---|---|
| `internal/mission` | Campaign discovery, mission loading and dispatch, schema selection, placement decoding, the `InitialMission` interpreter | DESIGN_SESSIONS_AI_SAVE |
| `internal/triggers` | Mission trigger records, parsing, evaluation, save form | DESIGN_SESSIONS_AI_SAVE |
| `internal/ai` | The skirmish planner: profiles, manager and tasks, strategic refresh, candidate selection, placement, groups | DESIGN_SESSIONS_AI_SAVE |
| `internal/save` | The retail HAPIBANK bank container and its boxes | DESIGN_SESSIONS_AI_SAVE |
| `internal/headless` | Composes and advances an authoritative session with no window or device and emits the report | DESIGN_SESSIONS_AI_SAVE |

### Interface and presentation

| Package | Responsibility | Design document |
|---|---|---|
| `internal/gui` | GUI file loading into the control kinds, hit tests, dispatch | DESIGN_INTERFACE_HUD_INPUT |
| `internal/hud` | The battle HUD: side anchors and bars, selection and build pages, the command latch, panel slide, cursor, footer, queue overlay, score panel | DESIGN_INTERFACE_HUD_INPUT |
| `internal/input` | Platform-neutral key and mouse vocabulary and input rings | DESIGN_INTERFACE_HUD_INPUT |
| `internal/camera` | The orthographic camera, scroll caps, minimap conversions, presentation zoom | DESIGN_INTERFACE_HUD_INPUT |
| `internal/ui` | Screen-level state of the authored front-end panels shared by the desktop binary and its tests | DESIGN_INTERFACE_HUD_INPUT |
| `internal/client` | The window-side frame loop: samples the committed frame, draws terrain, units, effects, HUD and text into a software framebuffer, resolves art | DESIGN_PRESENTATION_CLIENT |
| `internal/render` | Presentation pools and helpers: the strip composer, fixed effects, projectile render types, GAF cursors, fog presentation, shake, the model rasterizer, minimap | DESIGN_PRESENTATION_CLIENT |
| `internal/palette` | Palette, SHD, ALP and LHT tables and logical→physical lookups | DESIGN_PRESENTATION_CLIENT |
| `internal/audio` | The eight-slot cue queue, sample decode and cache, positional attenuation, music, briefing speech | DESIGN_PRESENTATION_CLIENT |
| `internal/audiobackend` | The desktop PCM device boundary behind `internal/audio` | DESIGN_PRESENTATION_CLIENT |
| `internal/platform/ebitenapp` | The Ebitengine adapter: window and loop lifecycle, device input polling, framebuffer upload, the classic/modern executor switch | DESIGN_PRESENTATION_CLIENT |
| `internal/platform/benchlock` | Host file lock serializing benchmark startup and execution across worktrees | BATTLE_BENCHMARK |
| `internal/drawlist` | The recorded committed-frame draw list: command families carrying physical palette indices, the `Sink` executor interface, ordered replay and model packet boundary | DESIGN_GPU_RENDERER |
| `internal/platform/gpurender` | The modern executor: replays a draw list through Ebitengine in palette-index space, table textures, atlases, per-subject GPU model prototypes, expansion to RGB | DESIGN_GPU_RENDERER |
| `internal/upscale` | Load-time 2× synthesis of terrain tiles and feature sprite banks from the map's own pixels, with the on-disk cache; the `tools/mapupscale` synthesizers are wrappers over it | DESIGN_GPU_RENDERER §14 |

### Commands

| Package | Responsibility | Design document |
|---|---|---|
| `cmd/nanolathe` | The game: front-end screens, briefing, battle composition and dispatch, the battle HUD wiring, load/save screens, post-battle, `--shot` captures, `--headless` | DESIGN_INTERFACE_HUD_INPUT (screens, dispatch), DESIGN_SESSIONS_AI_SAVE (composition, headless) |
| `cmd/nanolathe-headless` | The displayless runner: one authoritative session to a tick limit or result, JSON report | DESIGN_SESSIONS_AI_SAVE |

### Hygiene, probes and tools

| Package | Responsibility | Design document |
|---|---|---|
| `internal/architecture` | Repository guards that inspect source rather than importing it: the platform boundary, random-stream ownership, retail-only content, shrink-only parity ratchets | this document, §6 |
| `internal/cleanroom` | The clean-room lint and its per-file debt baseline | this document, §6 |
| `internal/docs` | The citation resolver: every research citation in `docs/` and in Go comments resolves | this document, §6 |
| `internal/parity` | Opt-in, passive evidence capture for parity investigations (authoritative hashes, traces) | DESIGN_RUNTIME_DETERMINISM |
| `internal/compat/spec03` | Black-box checks of the published presentation boundary | DESIGN_PRESENTATION_CLIENT |
| `internal/testsupport`, `internal/testsupport/retailcat` | `RetailRoot()`, the one place an asset-gated test skips; the shared compiled retail catalog | this document, §6 |
| `probes/` | Authored, data-driven scenarios for questions only a manual retail observation settles; generators under `<probe>/gen/`, shared writers in `probes/kit/author` | `probes/README.md` |
| `tools/` | `check` and `check-retail` (the two test tiers), `ci/baseline.sh`, the clean-room baseline and report commands, `loc-audit`, the map upscale prototypes | this document, §6 |

## 3. Dependency graph

Derived from the packages' imports (`go list -f '{{.Imports}}'`), not from any
build order. Arrows point from importer to imported; a package may import
anything in a lower layer and nothing in a higher one.

```
platform      cmd/nanolathe ─► platform/ebitenapp ─► client, audiobackend
              cmd/nanolathe ─► upscale ─► formats, palette   (load-time 2× art, DESIGN_GPU_RENDERER §14)
              cmd/nanolathe-headless ─► headless

presentation  client ─► render, hud, audio, camera, palette, input, model, frame,
                        units, visibility, world, content, formats, vfs, numeric, rng
              ui ─► gui, input           render ─► camera, combat, visibility, world, model, palette, frame, content
              hud ─► render, camera, input, frame, units, world, content

composition   session ─► every simulation package below, plus frame, hud, render, audio, save
              headless ─► session, ai, orders, units, content, pool, vfs
              airdiag ─► session, movement, orders, units, content, pool, vfs

planner       ai ─► economy, orders, units, world, content, pool, numeric, rng, vfs

simulation    mission ─► triggers, movement, orders, units, content, formats, pool, numeric, vfs
              construction ─► movement, orders, combat, economy, frame, model, units, world, content, pool, rng
              movement ─► path, orders, combat, cob, model, units, world, content, pool, rng
              orders ─► combat, economy, features, save, cob, units, world, content, pool, rng
              combat ─► economy, features, visibility, cob, units, world, content, pool, rng
              triggers ─► save, units          save ─► clock, economy
              features ─► units, world, content, rng
              economy ─► units, world, pool
              units ─► cob, model, world, content, pool, numeric, rng, vfs
              visibility ─► world, content, numeric
              world ─► content, formats, vfs, numeric, rng
              path ─► pool                     audio ─► frame, content, pool, numeric, rng, vfs

content       content ─► cob, model, palette, formats, vfs
              cob ─► model, numeric, rng, vfs        model ─► formats, numeric, vfs
              gui ─► formats, vfs               palette ─► vfs           formats ─► vfs

leaves        vfs, clock, pool, frame(pool, numeric), camera(pool, numeric), input,
              settings, version, sim/numeric, sim/rng
```

Three boundaries in this graph are enforced by tests in `internal/architecture`
rather than by convention:

* **Only the platform adapter reaches Ebitengine.** `internal/platform/ebitenapp`,
  `internal/platform/gpurender`, `internal/audiobackend` and
  `cmd/nanolathe` are the only packages whose
  import closure (including their test binaries) may contain the Ebitengine
  modules. Every other package, and every other test, stands up with no
  window and no audio device.
* **Simulation never imports the window.** `internal/client` is imported only
  by the platform adapter, the two desktop commands and the presentation
  conformance tests; no simulation package can reach it, and the headless
  command's dependency closure contains neither the client nor the device
  packages. `internal/session` does import `frame`, `render`, `hud` and
  `audio`: those are the publication targets it fills at the end of each
  sub-tick, and none of them owns a window, a device or a clock. The
  committed frame is the only channel from simulation to presentation
  `[03 §2.4]` [I6].
* **Presentation does not own a random stream.** The presentation packages
  (`client`, `render`, `audio`, `hud`, `gui`, `ui`, `camera`) do not import
  `internal/sim/rng` except through a shrink-only allowlist of files that copy
  stream state into a private presentation copy; only `internal/sim/rng` and
  `internal/session` construct a stream, and the process-wide streams are
  referenced only from the stream package, session bootstrap, the commands
  and tests `[01 §7.1]` `[01 §7.2]` [I4].

## 4. The authoritative tick

`internal/session` owns the tick. Detail is in DESIGN_RUNTIME_DETERMINISM;
the shape is this.

**Clock.** The simulation runs at 30 Hz. Each pump of the loop turns the scaled
host time into a budget of `0..5` sub-ticks — the product `delta × speed +
carry` is formed in `float64`, its integer part clamped to five, the
fractional carry kept in `float32` `[01 §4.2]` `[01 §4.3]`. The simulation
never reads the wall clock; the caller supplies the scaled time. A paused
session stalls the anchor and yields one capped burst on unpause.

**Phases.** Each sub-tick increments the global tick and then runs twelve
phases in the retail order `[01 §4.4]`, one named method per phase, one call
site per phase, in `internal/session/step.go`:

| Phase | Work |
|---|---|
| 1 | command drain (the single-player form of the network phase) |
| 2 | unit update — the per-unit sweep, death finalization |
| 3 | projectiles |
| 4 | effects |
| 5 | the path scheduler, then per player `0..9` the orders and work pump, then that player's visibility stamps `[01 R-CORE-01 §4.4.1]` |
| 6 | feature lifecycle |
| 7 | model-texture sequence cursors `[03 R-CRD-005 §1]` |
| 8 | the scheduled wind redraw `[01 §7.3]` |
| 9 | meteor shower `[06 §6.5]` |
| 10 | camera shake driver `[03 §5.6]` |
| 11 | the ten effect-strip sweeps `[03 R-STRIP-01]` |
| 12 | the radar blink cadence `[01 R-CORE-03]` |

After phase 12 the sharing pass and the per-sub-tick result evaluation run;
then the session **publishes** the committed frame. Publication is outside
the phase registry and follows every completed sub-tick; nothing presents
between phases. Original presentation samples the committed tick without
interpolation `[03 §2.4]`; Enhanced may blend the last two committed ticks
under DESIGN_GPU_RENDERER §13.5 [I6]. After the last runnable sub-tick of a pump the
executor tail runs once — the temporary-sight expiry sweep, the deadline-ring
slide and the barrier routines `[03 R-COMP-02 §2]` `[01 R-PLAT-02 §7]`.
Authoritative ticks run only in the battle state of the session state machine
`[08 "Session states"]`.

Publication assigns each live unit a presentation-only `InstanceID`. It stays
stable for the same unit object and changes when a pool slot is reused, even
without an intervening empty frame. Only the integer enters the snapshot;
retired object references are cleared. This is cache ownership bookkeeping,
not an authoritative handle, saved generation, or RNG consumer [I6].

**Random streams.** There are two, and call order is behavior [I4]. The
simulation stream is Park-Miller (`16807`, modulus `0x7fffffff`, Schrage) and
is seeded at battle entry `[01 R-CORE-02]`; the CRT stream (`×214013 +
2531011`) is seeded once at process start `[01 R-PLAT-01 §7]`. Gameplay draws
from the simulation stream; meteor geometry, screen shake, audio variant
selection and the wind interval draw from the CRT stream `[01 §7.3]`. A
consumer draws the documented number of times even when it discards the
result. Presentation draws only from private copies.

**Fixed point.** World state is 16.16 `numeric.Fixed`; angles are `uint16`
per circle; narrowing truncates toward zero except where retail floors
[I2] [I3].

## 5. What runs today

The desktop binary `nanolathe --root <install>` opens the retail front end at
`MAINMENU`. From `SKIRMISH` it composes a battle on a chosen map against
computer players, runs it to the skirmish end rules `[08 R-SKIR-01 §3]`, shows
the post-battle report and returns to the menu. From `SINGLE → NEWGAME` it
opens a campaign, plays the briefing, runs the mission with its authored
`InitialMission` orders and triggers, evaluates victory before defeat at the
local player's due `[08 R-TRIG-01 §1]`, and continues to the next mission on
victory. The battle draws the ten-strip frame from real TNT, GAF, 3DO and
palette data with fog, the HUD from the side's GUI files, and plays cues
through the eight-slot audio queue. Saves are the retail HAPIBANK account
format; campaign continuation persists and an in-battle save restores through
the retail account boundary. `--load-save` opens an existing retail `.SAV` in
the windowed shell; `--remaster` mounts a remastered-art override above every
retail archive.

The same authoritative session runs with no window: `nanolathe --headless` and
the separate `nanolathe-headless` command compose a skirmish (`-map`) or a
mission (`-mission "camps/Arm Campaign.tdf:MISSION0"`) at a difficulty, seed
both streams from `-seed`, step to `-ticks` or to a result, and print a JSON
report of the outcome, per-player statistics, draw counts and the
authoritative state hash. Two entries exist; a third is not to be added.

`nanolathe --shot <file.png>` composes one battle frame after a number of
authoritative ticks and exits without opening a window; `--shot-select`,
`--shot-modal`, `--zoom`, `--shot-focus` and `--shot-size` stage the
capture. A capture is the evidence for any visual change.

Retail gaps that remain are explicit at their site as `TODO(T23)`,
`TODO(T25)` or `TODO(question)` and as Unknown bullets in the owning research
document; none is replaced by a plausible default [I9].

## 6. Verification

**Two test tiers.** `tools/check` is the fast tier and the one CI runs:
`gofmt` on tracked files, `go build`, `go vet`, and `go test ./...` with the
retail-asset variables cleared, so asset-gated tests skip and the run means the
same thing on every machine. `tools/check-retail` is the pre-merge tier: it
exports `NANOLATHE_RETAIL_ASSETS` (defaulting to `~/TotalAnnihilation`,
honouring the older `NANOLATHE_TA_ROOT`), fails loudly if the directory is
missing, and runs `go vet` and `go test` with `-tags retail`, which adds the
tagged whole-corpus files — catalog compiles, map and mission censuses, long
headless sessions — to the untagged set. It runs `tools/lint` first: pinned
`staticcheck` and `deadcode` over the retail-tagged build, because the corpus
tests are the only callers of some production code and the untagged build
would report their targets as unused. A retail diagnostic string that trips
`ST1005` carries `//lint:ignore ST1005 retail text` with its citation. `internal/testsupport.RetailRoot` is
the single place a test consults those variables and skips.

**Partial state fingerprint.** The headless report retains the JSON key
`state_hash`; its value now starts with `partial-v1:`. This diagnostic covers
the explicit subset in DESIGN_RUNTIME_DETERMINISM §4. Equal values do not
establish whole-simulation equivalence: projectile, meteor, AI, visibility and
other future-affecting state is omitted. The reference run is

```
nanolathe-headless -map "ashap plateau" -seed 7 -ticks 6000
```

Its value is not written down here. Identical setups must agree on this
fingerprint and both RNG draw counts [I4], but changes outside its coverage
need their own contract tests. A change to an included value must explain the
difference. Compare only the same fingerprint version; changing its field set
or encoding requires a new version.

**Visual evidence.** `nanolathe --shot` renders a frame headlessly; a
screenshot is reviewed, an assertion that it should look right is not. Retail
captures outrank a clean-room note when the two disagree about appearance.

**Citation checks.** `go test ./internal/docs` resolves every `[0N §x]`,
`[0N "Heading"]`, `[0N R-… §k]` and `[fmt …]` citation in `docs/*.md` against
the research tree and fails on a dangling one; it fails on any anchored
research finding (`R-…`) that no document cites; and it scans every Go
comment under `internal`, `cmd`, `vfs`, `formats`, `probes` and `tools`
with the same resolver, failing on a citation that does not
resolve unless it is listed in `internal/docs/testdata/go_citation_baseline.txt`.
That file holds the misses present when the check was introduced, keyed by
citation text; it only shrinks.

**Hygiene guards.** `internal/architecture` inspects source without importing
it: the platform boundary and headless dependency closure (§3), the three
random-stream ownership guards (§3), retail-only content, and the parity
ratchets. The map guard type-checks the authoritative package graph and
requires each map range to have a reviewed enclosing-function record; the `float64`
guard keeps its shrink-only per-file baseline, with declaration-scoped records
for the existing I2 operations formerly hidden by file-wide exceptions.
`internal/cleanroom` is the clean-room
lint: a census of raw-forensics text per file in `baseline.go`, failing when a
file exceeds its count and when a count drops without the baseline being
regenerated (`tools/cleanroom-baseline`; `tools/cleanroom-report` lists the
sites). `internal/compat/spec03` checks the published presentation boundary
as a black box. `tools/ci/baseline.sh` records the toolchain, module graph and
asset presence of a full run outside the tree.

## 7. Citation conventions

Code and documents cite research by document and section, never by line
number. `research/retail-executable-spec` wins for behavior;
`research/formats` wins for byte layout.

| Form | Means |
|---|---|
| `[04 §7.2]` | `research/retail-executable-spec/04-units-orders-scripts-and-movement.md`, section 7.2 |
| `[05 "Two-stage settlement algorithm"]` | documents 05 and 08 have unnumbered headings — cite the heading text |
| `[04 R-PATH-01 §10]` | an inline finding anchor: the heading in document 04 carrying `R-PATH-01`, sub-heading §10 |
| `[fmt tnt]` | `research/formats/tnt.md` |
| `[I4]` | rule I4 of [INVARIANTS.md](INVARIANTS.md) |
| `SC22` | entry SC22 of [SPEC_CONFLICTS.md](SPEC_CONFLICTS.md) |

Document numbers: `01` core/determinism, `02` content/VFS/formats, `03`
world/visibility/rendering/audio, `04` units/orders/scripts/movement, `05`
economy/construction/features, `06` weapons/projectiles/damage, `07`
interface/input/camera/front end, `08` sessions/campaign/AI/save.

### Citation routing

Go comments also carry token families whose definition lives outside the
research tree. This table is the definition of record for each. It carries no
orphan rows any more: `[I16]`, `[R-P1-11]` and `[RR-04]` were the last three,
and CL-6 rewrote their sites to `[P0-I16]`, `[04 R-PATH-01 §3]` and
`[08 R-TRIG-01 §6]` respectively. A token that cannot be routed is rewritten
at its sites, never defined here.

| Token | Resolves to |
|---|---|
| `[0N §x]`, `[0N "Heading"]`, `[0N R-XXX-nn §k]`, `[fmt <name>]` | research, as above; checked by `internal/docs` |
| `[In]`, `INVARIANTS In` | [INVARIANTS.md](INVARIANTS.md) I1–I14 |
| `SCn`, `docs/SPEC_CONFLICTS.md SCn` | [SPEC_CONFLICTS.md](SPEC_CONFLICTS.md) entry `n` |
| `[P0-nn]`, `[P1-nn]` | retired research packets; routed by `research/retail-executable-spec/README.md` §"Legacy packet citation routing". A packet-local `§` suffix describes the packet outline, not a section of the destination |
| `[GAP Tn]` | closed gap task; `research/retail-executable-spec/README.md` §"Gap disposition" names the section each task's content was promoted to |
| `[P0-Inn]`, `[P1-Inn]` | early integration anchors; see the table below |
| `[PLAN 11 Cn]` | contract `n` of the computer-player contract list, carried under the same numbers (C4–C9) by DESIGN_SESSIONS_AI_SAVE |
| `[PLAN 01 C13]` | the content/VFS contract that a diagnostic names a provider without exposing a host path; DESIGN_CONTENT_VFS |
| `[Cn]`, `Cn` (plan-relative) | contract `n` of the contract list the package's design document carries, numbered as its source plan numbered it: `internal/mission`, `internal/triggers`, `internal/ai` → DESIGN_SESSIONS_AI_SAVE; `internal/visibility` → DESIGN_WORLD_VISIBILITY (C1–C16); `internal/construction`, `internal/economy` → DESIGN_ECONOMY_CONSTRUCTION; `internal/session`, `internal/clock` → DESIGN_RUNTIME_DETERMINISM; `[C-1]`, `[C-3]` in `internal/client` → DESIGN_PRESENTATION_CLIENT |
| `WU-nn-n`, `RWU-nn-n` | historical work-unit ids. They name the commit series that did the work, nothing in the current documents; resolve with `git -C /path/to/private-development-repository log --grep 'WU-nn-n'` |
| `RS-nn`, `RS-P0-nnn`, `RX-nn`, `ON-nn`, `F-P0-nnn`, `M-n`, `CNT-nn`, `P2-nn`, `SP-REV-nn`, `P28-OBS-nn` | historical review and dispatch-round item ids, same disposition as work-unit ids: `git -C /path/to/private-development-repository log --grep`. Two are still defined by a document: `P2-03` in SPEC_CONFLICTS, `P28-OBS-00C` in INVARIANTS I2 |
| `[F-P1-008]` | presentation-only zoom. `camera.Scale` (integer, 1 = native, 2 = the detail view) scales the world view, the chrome insets and pointer conversions; it is not a retail concept, changes no authoritative state, and is driven by F9, middle-drag and `--zoom`. DESIGN_INTERFACE_HUD_INPUT §3.8 and DESIGN_GPU_RENDERER §14 |
| `DET-01` | random-stream ownership: no package-global fallback; the session injects the streams it constructs; presentation uses private copies. Enforced by the three guards in `internal/architecture` (§3) |
| `DET-02` | the single phase registry: `Session.Step` delegates to one complete sub-tick boundary, which calls each of the twelve phases exactly once, in order, from one site |
| `DET-03` | the complete scheduled wind redraw runs in phase 8 (one CRT interval draw, then simulation strength and heading, then vectors); battle entry zeroes the deadline and draws nothing |
| `DET-04` | the camera shake driver is authoritative and runs in phase 10 with exactly two CRT draws per active tick, publishing its offset on the committed frame; the scroll toward the camera target stays presentation-owned |
| `DET-05` | unused |
| `DET-06` | the visibility publication seam lives inside phase 5: the path scheduler first, then per player ascending the orders and work pump, then that player's dirty-checked stamp sweep; it is not a phase of its own |
| `PROC-03` | the shrink-only parity-drift ratchets in `internal/architecture` (authoritative `map` iteration and `float64` counts per file may only decrease). The plan heading its comment names no longer exists; the definition is this row |
| `[OW-3-P]` | goal-family wiring between orders and path search: which order produces which goal family (`[04 §7.2]` `[04 §7.4]` `[04 §3.5]`) — point goals by default, the annulus where retail establishes a stand-off, the rectangle perimeter for the work orders that approach a footprint, and the air-goal chaining for patrol deliberately unwired because no established producer exists. DESIGN_MOVEMENT_PATH. (`[OW-3-O]` is the SC22 heading) |
| `CRD-005`, `CRD-006`, `CRD-008` | research anchors: `[03 R-CRD-005 §1]`, `[07 R-CRD-006 §1]`, and `CRD-008` in the heading of `[01 R-CORE-03]` |

The early integration anchors:

| Anchor | Home |
|---|---|
| `[P0-I01]` | DESIGN_RUNTIME_DETERMINISM — session composition and validation |
| `[P0-I03]` | DESIGN_MOVEMENT_PATH — the path scheduler, and its registration in the tick |
| `[P0-I04]` | DESIGN_WEAPONS_PROJECTILES — the weapon/projectile/damage pipeline |
| `[P0-I05]` | DESIGN_ECONOMY_CONSTRUCTION — construction sites and progress |
| `[P0-I06]` | DESIGN_ECONOMY_CONSTRUCTION — feature lifecycle |
| `[P0-I08]` | DESIGN_SESSIONS_AI_SAVE — mission execution and mission setup |
| `[P0-I10]` | DESIGN_SESSIONS_AI_SAVE — the session state machine and terminal result |
| `[P0-I11]` | DESIGN_SESSIONS_AI_SAVE — retail save and continuation |
| `[P0-I12]` | DESIGN_SESSIONS_AI_SAVE — the strategic planner and its registration |
| `[P0-I13]` | DESIGN_SESSIONS_AI_SAVE — triggers, results and player-role integration |
| `[P0-I14]` | DESIGN_INTERFACE_HUD_INPUT — picking, selection and HUD commands |
| `[P0-I16]` | DESIGN_RUNTIME_DETERMINISM — determinism and multi-session isolation |
| `[P1-I01]` | DESIGN_UNITS_ORDERS_COB — COB/model integration and composition |
| `[P1-I02]` | DESIGN_MOVEMENT_PATH — hover, naval, amphibious, transport and flight |
| `[P1-I04]` | DESIGN_ECONOMY_CONSTRUCTION — sharing, the cloak debit, ledger integration |
| `[P1-I05]` | DESIGN_ECONOMY_CONSTRUCTION — feature robustness and save integration |
| `[P1-I09]` | SPEC_CONFLICTS SC17–SC18 and the owning design documents |

## 8. Retail coverage

The external executable ledger maps recovered functions to research contracts
and scope classifications. Its current check and limits are documented in
`research/retail-executable-spec/README.md` under "How coverage of the executable
was established". A covered row is a citation/index classification, not proof
that all behavior is settled or implemented. The owning research sections
state confidence and unresolved questions; REVIEW.md tracks implementation
findings. The ledger remains outside the repository because its identifiers
belong to raw executable analysis.

The table below maps cluster roles to design ownership. It does not certify
implementation completion or a complete recovered-function census.

| Design document | Retail clusters (role names) |
|---|---|
| DESIGN_RUNTIME_DETERMINISM | startup and shell pump; battle host pump; tick budget clamp; tick phase executor; tick cadence flip; session timer; simulation RNG sampler; pool compaction; modal fatal status channel |
| DESIGN_CONTENT_VFS | archive account open; archive account lookup; TDF parser; OTA map parser; content catalog loader; movement class and model catalog; sound category loader; sound alias loader; WAV decode; GUI panel loader |
| DESIGN_WORLD_VISIBILITY | world object prep; world geometry pre-pass; LOS raster; LOS stamp; LOS stamp wrapper; LOS bulk refresh; fog cache and draw; wind field; wind jitter; per-player economy and visibility phase (visibility half) |
| DESIGN_UNITS_ORDERS_COB | order handlers; primary order queue pump; secondary order queue pump; per-unit tick sweep; per-unit state update; unit death and slot release; COB opcode interpreter; COB piece animation interpolator; COB thread drain |
| DESIGN_MOVEMENT_PATH | movement driver; path request setup; path search scheduler |
| DESIGN_ECONOMY_CONSTRUCTION | resource settlement; resource sharing; per-player economy and visibility phase (economy half); construction progress helper; construction completion transition; build progress quantum; unit self repair; feature tick |
| DESIGN_WEAPONS_PROJECTILES | per-unit weapon update; projectile simulation tick; weapon impact dispatch; damage intake; unit death cause dispatch; projectile draw pass |
| DESIGN_INTERFACE_HUD_INPUT | front-end state machine; main menu shell; interface panel driver; interface tick; interface dispatch; gadget record machinery; sorted list machinery; order acknowledgement presentation; camera scroll and shake; minimap and radar prep; alliance icon refresh; one cluster per authored screen — MAINMENU, SINGLE, NEWGAME, SELGAME, SELMAP, SKIRMISH, VIEWMAP, RESTRICT2, ALLIES, SHARE, BRIEFING, MSNBRIEF, ENDMSN, LOADGAME, SAVELIST, LOADLIST, YESORNO, MSGBOX, TALK, TABMENU, EXITMENU, RESTART, GAMEOPTIONS, SPEEDS, SOUNDSRT, MUSICRT, SELVMODE, ARMOPT, CONTROL, PREFS, REPORT, SGEN, TIMEOUT, UNITINFOX |
| DESIGN_SESSIONS_AI_SAVE | session mode dispatch; battle session bootstrap; battle entry orchestrator; mission unit spawner; end of battle report screen; computer player manager; computer player per-player work; computer player order dispatch; placement builder; save writer; save reader; save account restore; feature account writer |
| DESIGN_PRESENTATION_CLIENT | battle frame composer; terrain tile blitter; surface and clip descriptor; fixed effect pool tick; fixed effect draw pass; effect GAF handle table init; effect strip append; effect strip update sweep; effect strip notify pass; animation sequence advance; GAF playback cursor; GAF font loader; audio subsystem init; sound device buffer; sound cue emitter; CD audio tick |
| out of scope (§1) | network packet drain; the LOUNGE2, MODEM, NEWMULTI, TCP, SERIAL and SELPROV screens |

## Curated history boundary

Historical work-unit identifiers in this document refer to the original private
development record. The public history combines adjacent integration steps and
does not promise to retain every work-unit message or intermediate correction.
Use the current category documents and retained section anchors for behavioral
citations; consult the original private record when a historical audit trail is
needed. Publication omissions are marked explicitly and are not new behavioral
claims.
