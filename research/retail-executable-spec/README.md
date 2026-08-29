# Retail executable clean-room design specification

## Scope

This directory is a category-oriented design specification for the retail
Total Annihilation executable. It describes the engine as behavior and logical
data structures that can guide an independent implementation.

The specification is derived only from the isolated decompilation and static
analysis of the retail executable. It does not use another engine, a
replacement implementation, executable-comparison tooling, or third-party
behavior as retail evidence. Original game assets are not used to fill gaps in
the executable analysis.

The documents intentionally omit executable addresses, memory offsets,
decompiler-generated symbols, disassembly, and translated source code. Exact
file-record byte positions and protocol values are included only when they are
part of a data or wire format rather than a position inside the executable.

## Retail binary and evidence boundary

The analyzed input is the retail PE32 executable whose recorded hashes are:

- MD5: `8e74a1dffa1f5988624c52048f5b20cd`
- SHA-1: `764dc919c3bd0365751aefba8e9a667299a3ce2e`

At the time of this synthesis (re-measured 2026-08-28 after a function-boundary reconciliation pass), the decompiler index contains 4,024 function starts covering 960,488 of the 1,026,560 code bytes (93.6 %), and every one of those functions has an exported decompilation. Code that no function claims is down to 40 bytes; a further 25,061 bytes of the code section remain undecoded, alongside 34,144 bytes of alignment padding and 6,827 bytes of in-code data tables. Of the recognized functions, 883 (158,231 bytes) are identified as compiler runtime, C++ standard library, compression library, or the developer's shared debug and performance library; 500 (60,159 bytes) have no reference of any kind anywhere in the image; the remaining 2,641 functions (742,098 bytes) are game code. A second disassembler, run independently, recognizes 2,608 starts and agrees with 2,480 of them; each tool still finds starts the other misses, and end boundaries differ. (The previous text here — 2,657 starts, 941 exported decompilations, a 3,808-start independent index agreeing on 2,568 — described an earlier, partly lost analysis state and is superseded.)

The specification is self-contained: it does not cite the analysis workspace,
because that workspace is not distributed with this repository. Where a
contract matters to an implementer, it is stated here in full rather than
referenced.

This is therefore a broad but incomplete executable design. Every category
ends with an explicit list of missing and unknown work. A placeholder is part
of the specification: it prevents later implementation work from silently
substituting a remembered or modern behavior for a retail behavior that has
not been established.

## Evidence language

The category documents use three levels:

- **Established**: direct static data flow, imported API use, embedded schema
  or protocol vocabulary, a reconciled bounded instruction analysis, or a
  bounded negative search supports the statement.
- **Supported inference**: the direct evidence strongly favors the statement,
  but an important caller, identity, branch, or edge case remains open.
- **Unknown**: the current executable analysis cannot support a safe contract.

Bounded absence is not universal absence. For example, failing to find a
reader in the recovered function set establishes only that the reader is not
in the searched set. It does not justify inventing behavior, and it does not
prove that an unrecovered function cannot contain the reader.

## Category set

The engine is divided into eight categories. This is intentionally near the
upper end of the requested range: fewer documents would combine unrelated
subsystems into unreviewable files, while more would fragment prerequisites
and state transitions across too many boundaries. Each document is the
exhaustive, self-contained home for its feature area: corrections and closures
that were once tracked as separate addenda files (`R-*.md`) and gap-analysis
files have been folded inline, under headings that keep the old `R-<id>`
anchors so existing citations still resolve.

| Document | Category | Main ownership |
|---|---|---|
| [01-core-runtime-platform-and-determinism.md](01-core-runtime-platform-and-determinism.md) | Core runtime, platform, and determinism | PE/Win32 lifecycle, window/message pump, scheduler, authoritative phase order, threads, locks, allocators, pools, queues, RNG, x87, diagnostics, runtime failures |
| [02-content-vfs-formats-and-data-loading.md](02-content-vfs-formats-and-data-loading.md) | Content, VFS, formats, and loading | Current directory, mount tiers, loose/archive resolution, HPI family, TDF grammar, configuration/localization, catalogs, file decoders, linking, caching and load failures (incl. R-P0-03 category registry) |
| [03-world-visibility-rendering-audio-and-video.md](03-world-visibility-rendering-audio-and-video.md) | World and presentation | Coordinates, terrain, height/water, visibility/radar/fog, software rasterizer, GDI/DirectDraw, palettes, 3DO/GAF presentation, minimap, features rendering, effects, shadows, audio, CD music, Smacker and capture (incl. R-P0-18-A/B LOS height, R-RR16-A fog, R-P0-19 nanolathe pipeline) |
| [04-units-orders-scripts-and-movement.md](04-units-orders-scripts-and-movement.md) | Units, orders, scripts, and movement | Unit identity/lifetime, order state, COB VM and pieces, A*, path scheduling, steering, collision/occupancy, hover, VTOL, transport and air work positioning (incl. R-P0-01/02/08/09/10, R-P0-19 mobile-build walk target) |
| [05-economy-construction-players-and-features.md](05-economy-construction-players-and-features.md) | Economy, construction, players, and features | Resource buckets/settlement, storage, extraction, sharing, nanoframes, factory queues, build/repair/reclaim/capture/resurrection, limits, feature placement/successors/fire/sinking (incl. R-P0-06 nano cadence) |
| [06-weapons-projectiles-damage-and-effects.md](06-weapons-projectiles-damage-and-effects.md) | Weapons and combat | Weapon definitions/slots/targets, costs and cadence, projectiles/trajectories/guidance/beams, collision, armor, AOE, status, veterancy, stockpile/interception, death and combat events (incl. R-P0-07 weapon query path) |
| [07-interface-input-camera-and-front-end.md](07-interface-input-camera-and-front-end.md) | Interface, input, camera, and frontend | Win32 input, software cursor, picking/selection/groups, command UI, build pages, queue overlays, camera control, minimap interaction, GUI widgets/screens, HUD, chat and text (incl. R-P0-11 UI order producers) |
| [08-sessions-campaign-ai-network-save-and-replay.md](08-sessions-campaign-ai-network-save-and-replay.md) | Sessions, campaign, AI, networking, save, and replay | Game modes, campaigns/missions/triggers, skirmish/lobby, known AI inputs, DirectPlay packet/lockstep state, checksums, disconnects, save/load and bounded replay absence (incl. R-P0-04/05 AI group vectors and score fields) |

## Reading order

The documents are numbered by dependency, not by importance:

1. Read the core runtime first for time, iteration, identity, lifetime, random,
   and floating-point rules.
2. Read content loading before any subsystem whose definitions come from retail
   data.
3. Read world/presentation before movement, construction, or combat when the
   behavior depends on coordinates, terrain cells, water, visibility, or
   presentation events.
4. Read units/orders/scripts/movement before construction and combat because
   both consume unit state, orders, pieces, and target identities.
5. Economy/construction/features and combat can then be read in either order;
   their cross-links are stated explicitly.
6. Interface consumes the preceding simulation and presentation state.
7. Sessions/network/save orchestrate and serialize all categories.

## Cross-category coverage map

The following map is intended to prevent a subsystem from disappearing between
document boundaries:

| Engine concern | Owning document | Important consumers |
|---|---|---|
| Process startup, singleton, Win32 window, message loop, shutdown | 01 | 02, 03, 07, 08 |
| Authoritative tick, pause/speed, catch-up, phase order | 01 | 04, 05, 06, 08 |
| Threads, synchronization, memory, pools, queues, identity | 01 | all simulation documents |
| Simulation and CRT random streams, x87 conversions | 01 | 03, 04, 05, 06, 08 |
| Registry preferences, language, VFS, archives, paths | 02 | 03, 07, 08 |
| TDF grammar, typed access, defaults, duplicates | 02 | 04, 05, 06, 07, 08 |
| Unit, weapon, feature, movement, sound, side, GUI and mission catalogs | 02 | 03–08 |
| HPI family, TDF, FBI, MOVEINFO, GAF, 3DO, TNT, OTA, GUI, SIDE, FNT, PCX, WAV | 02 | 03–08 |
| Coordinates, terrain cells, height, metal, water, lava, occupancy substrate | 03 | 04, 05, 06, 07, 08 |
| LOS, mapping memory, radar/sonar, fog and minimap presentation state | 03 | 04, 06, 07, 08 |
| Indexed framebuffer, blitting, palettes, lighting/shading tables, models/sprites | 03 | 05, 06, 07 |
| Sounds, mixing, CD music, cinematics and movie capture | 03 | 06, 07, 08 |
| Unit definitions/instances, slots, creation/deletion, flags | 04 | 05, 06, 07, 08 |
| Canonical orders, state dispatch and queued unit intent | 04 | 05, 06, 07, 08 |
| COB loader, VM, threads, callbacks and piece animation | 04 | 05, 06, 08 |
| A*, path scheduler, locomotion, collision, hover, VTOL, transport | 04 | 05, 06, 07 |
| Player resources, settlement, storage, sharing and statistics | 05 | 06, 07, 08 |
| Building, repair, reclaim, capture, resurrection, factories and unit limits | 05 | 04, 07, 08 |
| Features, wrecks, successors, fire, sinking and geothermal registration | 05 | 03, 04, 06, 08 |
| Weapon slots, firing, projectiles, collision, damage, status and death | 06 | 03, 04, 05, 07, 08 |
| Keyboard/mouse/focus, cursor, selection, groups and command UI | 07 | 04, 05, 06 |
| Camera/minimap interaction, frontend GUI and battle HUD | 07 | 03, 08 |
| Campaigns, missions, triggers, skirmish, lobby and endgame | 08 | 02, 07 |
| Computer-player data and the explicitly unknown strategic planner | 08 | 04, 05, 06 |
| DirectPlay, command frames, lockstep, checksums and disconnects | 08 | 01, 04, 05, 06 |
| Save/load and bounded replay evidence | 08 | all categories |

## Source hygiene and corrections

The decompilation corpus contains raw exports, current clean-room notes,
superseded notes, incomplete notes, quarantined placeholder reports, machine
indexes, and contradiction ledgers. This synthesis follows these rules:

- Explicitly retracted, superseded, or quarantined behavior is not promoted to
  an affirmative contract.
- A corrected function identity outranks an earlier semantic label.
- A direct writer/reader chain outranks a field name inferred only from an
  offset or nearby string.
- Data loaded from a key is not assumed to affect gameplay until a consumer is
  found.
- A parsed but unconsumed field is documented as retained/inert or unknown,
  according to the bounded search.
- UI text and function adjacency can identify a subsystem, but they do not by
  themselves establish its arithmetic.
- Imported APIs establish available platform behavior; a specific engine use
  requires a call path.
- File decoders are specified only to the extent their retail read/write paths
  have been traced.
- Strategic AI, several sensor interactions, some script opcodes, and many
  malformed-input paths remain placeholders rather than reconstructed folklore.

Important corpus corrections reflected across the category documents include:

- the scenario unit reconstructor is not the strategic AI planner;
- the ordinary visibility writer is distinct from fog/minimap presentation;
- sequence advancement is distinct from visibility scanning;
- the corrected economy uses floating-point stock and two-stage carry
  settlement;
- automatic resource sharing runs after settlement from the local source,
  uses separate thresholds and destination capacity gaps, and selects the
  last qualifying player slot;
- the corrected construction helper uses a decreasing remaining fraction;
- current pathfinding uses a 16-world-unit grid and the corrected A* roots;
- the per-unit script drain uses the COB interpreter path, not a small GAF
  allocation helper;
- weapon accuracy is read by the turret executor's fire-time spread and
  tolerance/pitch-tolerance by the angular-drift gate — one reader each, none
  in admission, motion or damage `[R-WPN-03 §1]`; `aimrate` and
  `movingaccuracy` have no key in the image at all (corrected 2026-08-29; the
  earlier text said they had no gameplay reader);
- automatic targeting uses randomized preferred/fallback selection, while
  target retention and shot-time physical admission intentionally recheck
  different predicates;
- projectiles use a fixed packed pool with stable compaction, and ordinary
  save/load does not serialize that pool;
- area damage, paralyzer scheduling, damage-packet arithmetic, local versus
  received-network Killed dispatch, corpse-chain placement, and no-explode
  behavior use the corrected retail control flow in document 06;
- receive retention windows and delayed gameplay queues do not prove one
  universal network input delay;
- the weapon record's catalog slot is selected by its authored `ID` key, and
  the section name is stored as the record's name — an earlier reading that
  `ID` is unread is retracted;
- authored unit names and descriptions are localized through a
  language-prefixed key lookup, not only through the translation table;
- the terrain file's legacy version carries wind and gravity in its own
  header while the canonical version hard-codes those fallbacks — an earlier
  reading had the two versions reversed;
- interface anchors are corner rectangles named `x1`, `y1`, `x2`, `y2`, not
  origin-plus-size;
- economy settlement is gated by an absolute per-player deadline normally
  advanced by 30 ticks; eligible per-tick helpers run before the deadline
  compare, and a late player catches up one settlement per tick;
- a script sleep costs at least one tick, never zero;
- a script pop with an unrecognized addressing mode advances without popping
  rather than faulting;
- the weapon start sound precedes the Fire callback rather than following it.

Further corrections folded during the 2026-08-22 reconciliation pass:

- a paralyzer hit prepends a scheduled command task carrying the stun credit
  as a one-shot timed wait; there is no per-tick decay pool, and repeat hits
  extend the wait;
- `burst = N` is an inclusive total of flying pellets plus one immobile anchor
  record that silently self-removes when pellet N launches; spray re-aims the
  root record's velocity between copies, so every draw is relative to the
  original aim;
- interceptor claim happens at spawn after a fire-time rescan; the aim scan
  measures the separate interceptor-coverage square on the incoming
  projectile's stored aim point, while the slot stores its current position;
- unit status bits 0–1 mirror the runtime mover movement mode (stopped versus
  active locomotion), not a static unit family;
- water damage applies on exactly every 30th tick to player-class 1/2 units at
  or below sea level without the hover flag, as a no-callback damage type;
- sinking wrecks descend at a fixed constant rate (-11468 fixed) with a
  corpse-placement medium predicate deciding whether the descent starts;
- feature reproduction has a live consumer that rolls per visited cell and is
  stock-inert only because every shipped feature authors zero;
- zero-valued meteor parameters merge the `gamedata/METEOR.TDF [Default]`
  values while leaving the shower enabled; only an empty `MeteorWeapon`
  disables it;
- factory products queue in the primary order list; coalescing of identical
  products is tail-only;
- initial wind strength and six-bit direction use CRT draws; the next-change
  interval is `((CRTdraw * 10) / 0x8000 + 5) * 30` ticks, while later strength
  and full 16-bit heading use the simulation stream, jumping instantly and
  notifying wind generators only on change ticks;
- GAF frame-reference durations are whole simulation ticks;
- the LOS mask carries per-source-player-slot bits and is never OR'd across
  allied players.

## Legacy packet citation routing

Early implementation comments cite research packets that were later folded
into the category documents. The packet files and orchestration plan are not
part of the curated reference; this table preserves their searchable IDs and
routes readers to the current authoritative home. A packet-local `§` suffix in
an old citation describes the retired packet outline, not a section number in
the destination document.

| Legacy ID | Current authoritative home |
|---|---|
| `[P0-01]`, `[P0-02]`, `[P0-03]` | doc 08 §"Established AI-facing data and rooted planner" |
| `[P0-04]` | doc 08 mission/skirmish placement and initialization |
| `[P0-05]` | doc 08 campaign progression and session end |
| `[P0-06]` | docs 04 §3.6 and 08 mission loading |
| `[P0-07]`, `[P0-08]`, `[P0-09]` | doc 04 §3 orders, queue pumping, and same-tick dispatch |
| `[P0-10]` | doc 06 §§3–4 targeting, Aim, and firing |
| `[P0-11]` | docs 03 §3.2 and 06 §3 sensors and target admission |
| `[P0-12]` | doc 04 §8 ground collision and occupancy |
| `[P0-13]` | doc 04 §7 path search, goals, and scheduling |
| `[P0-14]` | docs 04 §3.8 and 05 construction completion |
| `[P0-15]` | doc 05 unit reclaim, capture, resurrection, and reverse construction |
| `[P0-16]` | docs 04 §2 and 05 §"Unit creation and limits" |
| `[P0-17]` | doc 03 terrain plus `[fmt tnt]` plot-cell encoding |
| `[P0-18]` | doc 03 §3.2 visibility-mask and LOS raster behavior |
| `[P1-01]` | doc 08 §"Session end and reporting" |
| `[P1-02]` | docs 02 mission data and 08 mission loading/media behavior |
| `[P1-03]` | docs 02 movement-class data and 04 §6/§9 terrain-medium behavior |
| `[P1-04]` | doc 04 §§9–10 hover, amphibious, and flight behavior |
| `[P1-05]` | doc 04 transport and attachment behavior |
| `[P1-06]` | doc 05 settlement, admission, and sharing |
| `[P1-07]` | docs 04 §8 and 06 §§8–9 collision and damage |
| `[P1-08]` | doc 06 §§5–10 projectile families and edge states |
| `[P1-09]` | doc 06 §11 stockpile and interceptor behavior |
| `[P1-10]` | doc 05 feature placement, extraction, lifecycle, and fire |
| `[P1-11]` | doc 04 §§4–5 COB VM, ports, callbacks, and persistence |
| `[P1-12]` | doc 02 content resolution, overrides, sounds, and error policy |
| `[P1-13]` | doc 08 save organization, contents, and load process |
| `[P1-14]` | docs 04 §3.7 and 07 picking, selection, latches, and build UI |
| `[P1-15]` | docs 03 terrain and 05 terrain-metal extraction |

## Gap disposition

The gap-analysis files (`GAP-ANALYSIS.md`, `GAP_ANALYSIS.md`) were removed on
2026-08-26: they were a working audit that had gone stale, and open questions
should be ephemeral — tracked as `TODO(T23)` / `TODO(T25)` / `TODO(question)`
markers at the exact site in code and as **Unknown** items in each category
doc's "Missing and unknown" list. Their closed tasks were promoted into the
category documents before removal; the mapping recorded in the last revision:

| Task | Promoted to |
|---|---|
| T1 economy ledger cadence | doc 05 settlement |
| T2 flight integrator arithmetic | doc 04 §10 |
| T3 factory production lifecycle | docs 04 §3.5 / 05 |
| T4 death-cause producer table | doc 06 §12.1 |
| T5 ballistic malformed/overflow table | doc 06 |
| T6 pool-full fire retains draws | doc 06 |
| T7 visibility predicate + sight shapes | doc 03 §3.2 |
| T8 InitialMission mini-language | docs 02 / 08 |
| T9 session states, lockstep pacing, save | doc 08 |
| T10 trigger objects and mission mechanics | doc 08 |
| T11 peer hash overwrite-sync | docs 01 / 08 |
| T12 SP vs MP pause/carry | doc 01 §4.3 |
| T13 wind draw arithmetic | doc 01 §7.3 |
| T14 content-layer promotions | doc 02 |
| T15 COB callback catalog + same-tick windows | doc 04 §5.4 |
| T16 route publication + water damage | doc 04 §7.3/§9.2/§10.2 |
| T17 sensor phase + fog + compositor | doc 03 |
| T18 script rotation order | docs 03 / 04 |
| T19 path heuristic family | doc 04 §7.2 |
| T20 economy/feature residuals | doc 05 |
| T21 combat residuals | doc 06 |
| T22 interface promotions | doc 07 |
| T23 platform residuals | `TODO(T23)` markers in code — none gate gameplay |
| T24 network internals | out of scope (no multiplayer) |
| T25 accepted blocked items | `TODO(T25)` markers in code (extractor placement helpers, resource-activity ledger arguments, definition flag semantics, save bulk-box byte layouts) |

A `[GAP Txx]` citation in code means the task's content now lives at the
promoted location above. The compressed GAF decoder is **not** a gap: it is
fully specified in `[fmt gaf]` and implemented in `formats/gaf.go`.

### Tails and markers (RWU-00-5, 2026-08-28)

The eight "Missing and unknown" tails, and the "### Unknown" blocks inside
documents 02 and 07, were regenerated on 2026-08-28 so that they contain
**only open items**. Each item is one bullet naming what is unknown, the
section that owns it, and the decider that would settle it — *static trace*,
*asset census*, or *manual retail observation*. Closure narratives were
deleted from the tails; every finding they recited is in the body section that
owns it, and each regenerated tail carries a correction note saying what the
previous text said and why it was wrong. Where a tail held established text
that existed nowhere else, that text was promoted into its section rather than
deleted.

Markers and tails are reconciled in both directions: every live
`TODO(question)` / `TODO(T23)` / `TODO(T25)` / `TODO(CRD-006)` marker in
`research/` has a bullet in the owning document's tail that names it, and no
tail bullet describes something already closed. The category documents carry
86 markers in their bodies and 64 tail mentions of them; markers that appear
only inside a quotation of retracted text no longer spell the marker syntax,
so a grep counts live questions. `research/formats/pal.md` holds the single
marker outside the category documents (the blue tint table's provenance),
listed in doc 03's tail.

### Inline finding anchors

Closures are written inline under a heading — or, in documents 05 and 08,
under a bold paragraph lead-in — carrying an `R-<id>` anchor. This table maps
every anchor in the corpus to the document that introduces it, so a
`[R-…]` citation resolves without a search. The citation resolver kept in the
raw corpus (`scripts/check_citations.py`) regenerates it and reports anchors
that are cited but never introduced.

| Anchor | Introduced in |
|---|---|
| `R-CORE-01`, `R-CORE-02`, `R-CORE-03`, `R-DET-01` | doc 01 |
| `R-CONTENT-01`, `R-CONTENT-02`, `R-CONTENT-03`, `R-KEYS-01`, `R-MAP-01`, `R-P0-03` | doc 02 |
| `R-CRD-005`, `R-FX-01`, `R-P0-18-A`, `R-P0-18-B`, `R-P0-19`, `R-P0-19-N`, `R-P0-19-P`, `R-RAST-01`, `R-REN-02R`, `R-REN-03A`, `R-RR16-A`, `R-SENSOR-01`, `R-STRIP-01`, `R-TERR-01`, `R-VIS-01`, `R-WIND-01` | doc 03 |
| `R-AIR-01`, `R-CB-01`, `R-COB-01`, `R-COB-02`, `R-COB-03`, `R-COB-04`, `R-COLL-01`, `R-DOC04-A`, `R-DOC04-B`, `R-DOC04-C`, `R-DOC04-D`, `R-MOV-01`, `R-MOV-02A`, `R-ORD-01`, `R-ORDER-02`, `R-P0-01`, `R-P0-02`, `R-P0-08`, `R-P0-08-A`, `R-P0-09`, `R-P0-10`, `R-P0-16-A`, `R-P28-COB-01R`, `R-PATH-01`, `R-REV-02`, `R-SPEC-01`, `R-STANCE-01`, `R-UNIT-06` | doc 04 |
| `R-ECO-01`, `R-FAC-01C`, `R-FAC-01R`, `R-FEAT-01`, `R-P0-06`, `R-PROD-01`, `R-SHARE-01`, `R-WORK-01` | doc 05 |
| `R-DMG-01`, `R-P0-07`, `R-WFX-01`, `R-WPN-01`, `R-WPN-02`, `R-WPN-03` | doc 06 |
| `R-CAM-01`, `R-HUD-02R`, `R-P0-11`, `R-REV-01`, `R-SEL-02B2` | doc 07 |
| `R-AI-01`, `R-CAMP-01`, `R-P0-04`, `R-P0-05`, `R-SAVE-02`, `R-SAVE-FEATURE-01`, `R-SAVE-ORDER-01`, `R-SAVE-UNIT-01`, `R-SAVE-WEAPON-01`, `R-SKIR-01`, `R-TRIG-01` | doc 08 |
| `R-CRD-006` | docs 03 (§2 producer census) and 07 (§1 cadence seam) |
| `R-FAC-01`, `R-FAC-01B` | docs 04 (movement boundary) and 05 (release audit) |
| `R-LAYER` | docs 03 (§§1–4) and 06 |
| `R-P28-ANG-01R` | docs 02 (§1), 04 (§2) and 05 (§3) |
| `R-RND-02A`, `R-SEL-02A` | docs 03 and 04 / 03 and 07 respectively |

`R-REV-01` (hover hull extrema, corner mapping, projection sign, polygon
predicate, and the HOT UNITS producer) and `R-REV-02` (the factory exit-piece
locator's runtime transform) landed on 2026-08-28 and are the newest entries.
Some anchors are introduced in one document and cited from several — `R-LAYER`
and `R-DOC04-A` are the widest — which is why the home column, not the citing
document, is authoritative.

## How to use the specifications

For implementation work:

1. Implement only established behavior as a strict retail contract.
2. Isolate supported inference behind named compatibility decisions so later
   decompilation can replace it without rewriting unrelated systems.
3. Represent unknown behavior explicitly in code and documentation; do not
   choose a modern default and label it retail.
4. Preserve data that the retail executable reads even when its consumer is
   unknown, provided preservation does not invent an effect.
5. Preserve authoritative order, single-precision narrowing, integer
   truncation, stable pool order, allocation failure, sentinel values, and
   queue capacity wherever established.
6. Keep presentation events separate from authoritative outcomes even when the
   same retail function initiates both.
7. Update the relevant document's final unknown list when new static evidence
   closes or contradicts a behavior. New findings are edited into the owning
   category doc in place — a closure of a tracked unknown or a long finding is
   written inline under a heading carrying an `R-<id>` anchor (the old addenda
   files no longer exist); a correction must state what the previous text said
   and why it was wrong.

## Global missing and unknown

- Complete function-boundary reconciliation across the whole executable.
- Recover executable regions missed by both current disassemblers.
- Re-audit all library/runtime identifications so compiler support code is not
  mistaken for engine behavior and game code is not discarded as a library.
- Finish reader/writer/caller censuses for every dynamically addressed global
  structure.
- Close all explicitly quarantined COB opcode, port, callback, construction,
  visibility, content-format, renderer, audio, AI, and save placeholders.
- Establish malformed-input, overflow, allocation-failure, and shutdown
  behavior for every file, queue, pool, and platform service.
- Complete cross-category same-tick ordering for creation, callbacks, economy,
  movement, projectiles, features, visibility, network commands, and cleanup.
- Derive bit-exact floating-point and random-consumption behavior for every
  authoritative algorithm that still has ambiguous evaluation order.
- Locate the strategic AI and prove its inputs, state, cadence, and actions.
- Decode the complete DirectPlay packet protocol and lockstep barrier.
- Inventory every save section and determine whether random/scheduler state is
  serialized indirectly.
- Extend the bounded replay search to unrecovered functions and dynamically
  constructed names.
- Reconcile all current contradictions without using visible behavior or a
  non-retail implementation as the deciding source.
