# Retail executable clean-room design specification

## Purpose and scope

This directory is the behavioral contract for the retail Total Annihilation
executable: what the engine does, described as logical state and arithmetic an
independent implementation can write from. It is the source of truth for
**behavior**. `research/formats` is the source of truth for **byte layout in
files**. They do not compete; where a category document needs a record layout
it cites the format document instead of restating it.

The specification is derived only from isolated static analysis of the retail
executable. It does not use another engine, a replacement implementation,
executable-comparison tooling, or third-party behavior as retail evidence, and
original game assets are never used to fill a gap in the executable analysis —
an asset census settles what stock content *authors*, never what the engine
computes.

The documents deliberately omit executable addresses, memory offsets,
decompiler-generated symbols, disassembly, and translated source code. Exact
byte positions and protocol values appear only when they are part of a data or
wire format rather than a position inside the executable.

Scope is the single-player engine Nanolathe implements: skirmish and the
campaign, and everything they need. Networking is described where the local
path still constructs it, and is otherwise outside scope; the transport, lobby
and codec internals are named as excluded rather than left silently missing.

This is a broad but not complete design. Each document ends with a "Missing and
unknown" list, and every item on it is open. A stated Unknown is part of the
specification: it stops later implementation work from silently substituting a
remembered or modern behavior for a retail behavior nobody established.

## Retail binary and evidence boundary

The analyzed input is the retail PE32 executable whose recorded hashes are:

- MD5: `8e74a1dffa1f5988624c52048f5b20cd`
- SHA-1: `764dc919c3bd0365751aefba8e9a667299a3ce2e`

The specification is self-contained: it never cites the analysis workspace,
because that workspace is not distributed with this repository. Where a
contract matters to an implementer it is stated here in full rather than
referenced.

## Evidence language

Every claim carries one of three confidence levels.

- **Established** — direct static control or data flow, imported API use, an
  embedded schema or protocol vocabulary, a reconciled bounded instruction
  analysis, or a bounded negative search supports the statement.
- **Supported inference** — the direct evidence strongly favors the statement,
  but an important caller, identity, branch, or edge case is still open. A
  Supported inference is a standing invitation to trace it; several have been
  found inverted rather than merely imprecise, so work that depends on one
  verifies it first.
- **Unknown** — the current analysis cannot support a safe contract. An
  implementation keeps the gap explicit.

Bounded absence is not universal absence. Failing to find a reader in the
recovered function set establishes only that the reader is not in the searched
set. It does not justify inventing behavior, and it does not prove that an
unrecovered function cannot contain the reader.

## Deciders

Every **Unknown** names the decider that would settle it. In order of
preference:

1. **Static trace** of the retail executable — the default, and the only
   decider that yields **Established** arithmetic.
2. **Asset census** over an original installation — settles what stock content
   authors (which COB scripts read a port bare, which definitions carry a key),
   never what the engine computes.
3. **Manual retail observation** — a human runs retail with an authored
   `probes/` scenario and records what is seen. Results are written as
   *Observed (retail run YYYY-MM-DD, probe name)* and may **confirm or
   falsify** an inference; they never replace a trace as the source of a
   constant. Probes are authored data (maps, missions, FBI/COB fixtures),
   never copied retail bytes. `probes/README.md` holds the kit and the
   procedure.

## Writing rules

The corpus speaks in one voice. It is a reference, not a notebook.

* **One statement per behavior.** A document says one thing about a behavior,
  in its current form. There is no "previously we thought" text beside it.
* **A correction replaces the text it corrects.** The commit message states
  what the previous text said and why it was wrong; git history is the audit
  trail, so the document itself carries no change log, no dates, and no work
  unit identifiers.
* **Edit the owning category document in place.** Behavior goes in the owning
  category document, byte layout in `research/formats`. Never add a new
  directory, addendum, or notes file.
* **Every claim carries a confidence level.** A Supported inference names the
  open branch; an Unknown names its decider.
* **Anchors are contracts.** A finding that code cites is written under a
  heading carrying its `R-<id>` anchor, and keeps that token across a rewrite.
* **Cite by document and section, never by line number** — `[04 §7.2]`,
  `[05 "Two-stage settlement algorithm"]`, `[04 R-PATH-01 §10]`, `[fmt tnt]`.
* **No addresses**, decompiler-generated names, offsets expressed as
  executable layout, or register narration. If a behavior cannot be described
  without an address, it is not yet understood. The address-level trail stays
  in the raw analysis workspace, where each function address is written beside
  the anchor it supports.
* **The tail is only open items.** Each "Missing and unknown" list carries open
  items only, each naming what is unknown, the section that owns it, and its
  decider. A closed item is removed from the tail; its closure lives in the
  body.

## The eight category documents

The engine is divided into eight documents. Each is the exhaustive,
self-contained home for its feature area: closures and corrections that were
once tracked as separate addendum and gap-analysis files are folded inline,
under headings that keep their `R-<id>` anchors so existing citations resolve.

| Document | Owns |
|---|---|
| [01-core-runtime-platform-and-determinism.md](01-core-runtime-platform-and-determinism.md) | The process: singleton, startup, window creation and the message pump, orderly shutdown; the wall-clock budget that decides how many fixed 30 Hz steps run; the authoritative twelve-phase tick order; threads, locks and thread-local runtime state; fixed pools and linked queues whose reuse and iteration order are behavior; the two random streams; the x87 environment and integer conversion; diagnostics, configuration and error paths |
| [02-content-vfs-formats-and-data-loading.md](02-content-vfs-formats-and-data-loading.md) | Startup content discovery, the mount tiers and provider precedence, the HPI family, registry configuration, language and localization; the TDF grammar and typed access with defaults and duplicate policy; catalog construction and linking for units, weapons, features, movement classes, sides, sounds, GUI and maps; the interface, map, animation, model, script, font, image and sample decoders; failure, caching and lifetime rules |
| [03-world-visibility-rendering-audio-and-video.md](03-world-visibility-rendering-audio-and-video.md) | World coordinates, terrain grids, height, water and projection; visibility, LOS, radar and fog presentation; the 8-bit indexed software renderer, its ten fixed-order effect strips, palettes and asset layers; world render passes and object presentation including models, shadows, selection and the nanolathe effects; media-dependent impacts; fonts, text and interface-owned drawing; audio backends, mixing and music; Smacker playback and movie capture |
| [04-units-orders-scripts-and-movement.md](04-units-orders-scripts-and-movement.md) | Simulation prerequisites and the per-unit sweep; player, unit, definition and lifetime identities; orders, queues and dispatch, including stances, guard, factory completion and the special-behavior keys; the COB loader, VM, threads and script timing; the engine-to-COB callbacks and ports; terrain and movement prerequisites; ground path search and its scheduler; ground steering, collision and occupancy; hover, floaters and amphibious behavior; VTOL, flight and transports |
| [05-economy-construction-players-and-features.md](05-economy-construction-players-and-features.md) | Player resource state, the authoritative settlement order and its two-stage algorithm, admission and carry, stocks, counters and waste, activation and stall transitions, allied resource and sensor sharing; unit creation and limits; build requests and factory queues; construction arithmetic and the nano cadence; repair, unit and feature reclaim, capture, resurrection; the feature catalog, placement, wreckage, burning, reproduction and sinking; saving all of it |
| [06-weapons-projectiles-damage-and-effects.md](06-weapons-projectiles-damage-and-effects.md) | Combat prerequisites and phase order; the weapon catalog and logical flags; target acquisition, retention and fire eligibility including the weapon-query path and the accuracy family; firing callbacks, costs, reload and bursts; the projectile pool, identity and lifetime; family dispatch, motion and timers; collision and impact selection; impact, armor, damage and area effects; paralyzer, stockpile and interception; death, kill credit, corpses and feature conversion; weapon-driven feature, fire, audio and effect events |
| [07-interface-input-camera-and-front-end.md](07-interface-input-camera-and-front-end.md) | The two interface families — the `.gui` front-end shell and the in-battle HUD; Win32 input translation and focus; modal windows and event ownership; the GUI file and widget model; front-end screen and state families; the battle HUD and side data; text, palette and localization use; the software cursor and world picking; selection, control groups, orders and build pages; camera, scrolling, projection and the radar/minimap; running display, pause, chat, options and outcomes |
| [08-sessions-campaign-ai-network-save-and-replay.md](08-sessions-campaign-ai-network-save-and-replay.md) | Session structures, game-mode selection and the session lifecycle; the campaign catalog and progression, mission and map schema selection, placement and battle entry; victory and defeat triggers; skirmish configuration; computer-controlled players — the class-vector refresh, manager task slots, task groups, deadlines and dispatch gates, scoring, placement and the per-domain policies; the DirectPlay transport and lockstep material that remains out of scope; save-file organization, the load process, and the bounded replay evidence |

## Reading order

The documents are numbered by dependency, not by importance.

1. Read the core runtime first for time, iteration, identity, lifetime, random,
   and floating-point rules.
2. Read content loading before any subsystem whose definitions come from retail
   data.
3. Read world and presentation before movement, construction or combat when the
   behavior depends on coordinates, terrain cells, water, visibility, or
   presentation events.
4. Read units/orders/scripts/movement before construction and combat, because
   both consume unit state, orders, pieces, and target identities.
5. Economy/construction/features and combat can then be read in either order;
   their cross-links are stated explicitly.
6. Interface consumes the preceding simulation and presentation state.
7. Sessions, campaign, AI and save orchestrate and serialize all categories.

## Cross-category coverage map

This map exists so a subsystem cannot disappear between two document
boundaries.

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
| Indexed framebuffer, effect strips, palettes, shading tables, models/sprites | 03 | 05, 06, 07 |
| Sounds, mixing, CD music, cinematics and movie capture | 03 | 06, 07, 08 |
| Unit definitions/instances, slots, creation/deletion, flags | 04 | 05, 06, 07, 08 |
| Canonical orders, state dispatch and queued unit intent | 04 | 05, 06, 07, 08 |
| COB loader, VM, threads, callbacks and piece animation | 04 | 03, 05, 06, 08 |
| Path search, scheduler, locomotion, collision, hover, VTOL, transport | 04 | 05, 06, 07 |
| Player resources, settlement, storage, sharing and statistics | 05 | 06, 07, 08 |
| Building, repair, reclaim, capture, resurrection, factories and unit limits | 05 | 04, 07, 08 |
| Features, wrecks, successors, fire, sinking and geothermal registration | 05 | 03, 04, 06, 08 |
| Weapon slots, firing, projectiles, collision, damage, status and death | 06 | 03, 04, 05, 07, 08 |
| Keyboard/mouse/focus, cursor, selection, groups and command UI | 07 | 04, 05, 06 |
| Camera/minimap interaction, front-end GUI and battle HUD | 07 | 03, 08 |
| Campaigns, missions, triggers, skirmish and endgame | 08 | 02, 07 |
| Computer-player data and the rooted strategic planner | 08 | 04, 05, 06 |
| DirectPlay framing and lockstep, kept only as the single-player boundary | 08 | 01, 04, 05, 06 |
| Save/load and the bounded replay evidence | 08 | all categories |

## Citation forms

A citation names a document and a place inside it, never a line number.

| Form | Resolves to |
|---|---|
| `[04 §7.2]` | section 7.2 of document 04. Documents 01–04, 06 and 07 carry numbered sections |
| `[05 "Two-stage settlement algorithm"]` | a heading of document 05. Documents 05 and 08 have unnumbered headings, so they are cited by heading text |
| `[04 R-PATH-01 §10]` | the inline finding anchored `R-PATH-01` in document 04, sub-section §10 |
| `[fmt tnt]` | `research/formats/tnt.md`. Format documents are cited whole; nothing inside one is anchored |

`internal/docs` resolves all four forms mechanically. `go test ./internal/docs`
fails when a citation in `docs/*.md` or in a Go comment under the source trees
does not resolve, and when an anchored finding no design document cites — a
traced contract with no implementation home. A dangling citation sends an
implementer to a section that is not there, and the usual outcome is an
invented constant.

## Inline finding anchors

A traced finding that code cites is written inline under a heading carrying an
`R-<id>` anchor, next to the numbered section it refines. This table is
generated from those headings: it names the document that introduces each
anchor, what the anchor establishes, and the range of `§k` sub-sections its
headings carry (`—` where the finding is a single heading with no sub-sections).
An anchor introduced in more than one document has one row per document, and
the row's document is authoritative, not the citing one.

| Anchor | Doc | Sub-sections | Establishes |
|---|---|---|---|
| `R-AI-01` | 08 | §1–§20 | computer-player manager entry, slot indexing, and the verified constants |
| `R-AI-02` | 08 | §1, §2 | the rally task's constructor state |
| `R-AI-03` | 08 | §1–§7.4 | the metal-spot vector: builder, record, scan, consumer |
| `R-AI-04` | 08 | §1–§6 | the task-class run is complete: seven classes and nothing else |
| `R-AIR-01` | 04 | §1–§17 | the flight command block, its per-tick producer, and the transport executor pairs with their hang and drop offsets |
| `R-AIR-02` | 04 | — | how a factory-built aircraft leaves the pad |
| `R-AUD-01` | 03 | §1–§8 | sound device bring-up, sample buffers, the 32-voice mixer, and the 3-D model |
| `R-AUD-02` | 03 | §1, §2 | the streamed narration path: delay timer, half-buffer refill, stop |
| `R-CAM-01` | 07 | §1–§14 | the host frame where input becomes simulation state, the battle hotkey census, chat commands, interface options, movie capture, developer mode |
| `R-CAMP-01` | 08 | §1–§11 | the campaign catalog and its grammar, the briefing screen, the new-game panel, the CD gate family, the dead warp entry |
| `R-CAT-01` | 02 | §1–§8 | wildcard matching, the union enumerator and how "first backing wins" is enforced; key, value and section trimming; the download-menu compile; the alias catalog read |
| `R-CB-01` | 04 | §1–§9 | the engine-to-COB callback name census and its bounded negative |
| `R-COB-01` | 04 | §1–§3 | COB VM initialization and engine-call frames |
| `R-COB-02` | 04 | §1, §2 | vertical-slice callback and port mappings |
| `R-COB-03` | 04 | §1–§6 | the engine port set, exactly, including the transport attach and drop reads |
| `R-COB-04` | 04 | §1–§10 | the explode opcode's flag bits and debris record |
| `R-COB-05` | 04 | — | `BUGGER_OFF` has no engine reader |
| `R-COB-06` | 04 | — | the script-touched marker is gate bit `0x4` |
| `R-COLL-01` | 04 | §1–§11 | the collision commit step, in order |
| `R-COMP-01` | 03 | §1–§5 | the frame composer's passes, starting with the tile pass |
| `R-COMP-02` | 03 | §1–§7 | the LOS table accessors are one-based, and how the fog and mask layers compose |
| `R-CONTENT-01` | 02 | — | movement-profile template initialization |
| `R-CONTENT-02` | 02 | — | weapon-family discovery and same-ID merge |
| `R-CONTENT-03` | 02 | — | the unit limit |
| `R-CORE-01` | 01 | — | phase 9, 10 and 11 identities, the shake arithmetic, and the visibility publication seam |
| `R-CORE-02` | 01 | — | battle RNG seeding and a chronological draw census |
| `R-CORE-03` | 01 | — | the phase-12 radar blink cadence (also cited as `CRD-008`) |
| `R-CRD-005` | 03 | §1 | phase-7 model-texture sequence traversal |
| `R-CRD-006` | 03 | §2 | the follow-camera producer census |
| `R-CRD-006` | 07 | §1 | the camera cadence seam and its two retail writers |
| `R-DET-01` | 01 | §1–§6 | the float→int conversion census, the two round-to-nearest sites, control-word mutations, the per-phase draw table, the CRT stream's other consumers, and the lane claims checked against the census |
| `R-DMG-01` | 06 | §1–§14 | the armor table is the weapon's `[DAMAGE]` block; damage-packet arithmetic and area effects |
| `R-DOC04-A` | 04 | — | the movement startup pool template and the class-record defaults |
| `R-DOC04-B` | 04 | — | the per-cell passability classifier, its consumption by the search, and the request revision pass |
| `R-DOC04-C` | 04 | — | the order descriptor table verified from the static templates |
| `R-ECO-01` | 05 | §1–§12 | settlement deadline strictness and the floating-point environment, the admission helpers, the two stages and the apply-back, commit order, the waste clamp and the four HUD rates |
| `R-ECO-02` | 05 | §1–§4 | the building validator's entry bounds and its two published outputs |
| `R-EGRESS-01` | 04 | — | why no-rally products queue at a factory exit |
| `R-EGRESS-02` | 05 | — | a no-rally product column is a route-publication defect, not retail |
| `R-ENTRY-01` | 08 | §1–§10 | battle entry: who runs it, the seeding and save gate, the world rebuild in allocation order, start positions, the visibility rebuild, the tail, and the first pump |
| `R-ENTRY-02` | 08 | §1–§3 | the mission spawner's height probe and the profile passes' plan gate |
| `R-FAC-01` | 04 | — | the factory movement boundary |
| `R-FAC-01` | 05 | — | the bounded factory-release audit |
| `R-FAC-01B` | 04 | — | the factory release boundary |
| `R-FAC-01B` | 05 | — | the targeted release-boundary pass over the stock exit pieces |
| `R-FAC-01C` | 05 | — | the factory-release call-chain continuation |
| `R-FAC-01R` | 05 | — | the final increment through factory idle and close |
| `R-FAC-02` | 04 | §1–§9 | the product is cargo: attach at allocation |
| `R-FE-01` | 07 | §1–§12 | the front-end controller: phases, substates, and the pump |
| `R-FE-02` | 07 | §1–§12 | the multiplayer screen edges, named as out of scope |
| `R-FEAT-01` | 05 | §1–§17 | the feature parser's fields, widths, defaults and key census, and feature placement, successors, burning, reproduction and sinking |
| `R-FONT-01` | 03 | §1–§7 | the FNT record as the executable reads it, and text drawing |
| `R-FX-01` | 03 | §1–§7 | named GAF banks and slots, the fog fade families, and the cursor bank |
| `R-FX-02` | 03 | §1–§6 | the strip object pool and the base object |
| `R-HUD-02R` | 07 | — | the HUD footer's state and formatting boundary |
| `R-HUD-03` | 07 | §1–§14.5 | the ordinary footer: sources, priority, redraw and clearing |
| `R-HUD-04` | 07 | §1–§5 | the Space-held Kills/Losses score panel |
| `R-HUD-05` | 07 | — | the battle chrome at display modes larger than 640×480 |
| `R-KEYS-01` | 02 | §1–§6 | the key consumer table: unit- and weapon-record consumers not stated elsewhere |
| `R-LAYER` | 03 | §1–§4 | the mapping word grid is the path search's owner/building mask; the wreck-smoke trigger is the corpse finalizer's land path; strip 5 has no combat producer |
| `R-MALF-01` | 02 | §1–§11 | the loader malformed-input matrix |
| `R-MAP-01` | 02 | §1–§9 | the map-load pipeline: entry points, order, the resource-path slots, and schema selection |
| `R-MM-01` | 03 | §1–§3 | the minimap does carry a viewport rectangle |
| `R-MOV-01` | 04 | §1–§9 | the ground mover, exactly, and the movement-mode status bits |
| `R-MOV-02A` | 04 | — | the dynamic-blocker boundary |
| `R-MOV-03` | 04 | §1–§11 | the per-player unit sweep step by step, its three gates, the queue helpers and the purge |
| `R-OOS-01` | 08 | §1–§5 | the single-player boundary: the packets the local path still constructs |
| `R-ORD-01` | 04 | §0–§18 | the pending word's bits and who arms them |
| `R-ORD-02` | 04 | §1–§7 | command resolution, exactly |
| `R-ORDER-02` | 04 | §1–§3 | handler retry, pre-reject mapping, and the cleanup callbacks |
| `R-P0-01` | 04 | — | final-order arrival and the satisfied-bit handshake |
| `R-P0-02` | 04 | — | the build footprint anchor and the model center |
| `R-P0-03` | 02 | §1–§8 | the category token registry and membership-bitset compilation |
| `R-P0-04` | 08 | §1–§6 | AI group vectors: no per-tick group producer, and two vector families |
| `R-P0-05` | 08 | §1–§10 | score inputs and update order |
| `R-P0-06` | 05 | §1–§6 | the construction nano cadence: work-admission gating and the emission producers |
| `R-P0-07` | 06 | — | the weapon-query path: callbacks and seeds, the per-slot and fire-time pipelines, aim dispatch, and the target-point resolver |
| `R-P0-08` | 04 | — | placement footprint legality |
| `R-P0-08-B` | 04 | §1 | yard bit 0: the structure-yard mark and the known-site gate |
| `R-P0-09` | 04 | — | factory completion, activation, and rally inheritance |
| `R-P0-10` | 04 | — | engine port write semantics, the factory stance handshake, and thread-start masks |
| `R-P0-11` | 07 | §1–§6 | the UI order producers, starting with the factory product click |
| `R-P0-16-A` | 04 | — | player-slice order at battle entry |
| `R-P0-18-A` | 03 | §1, §2 | observer emitter height and model-top provenance |
| `R-P0-18-B` | 03 | §1–§4 | terrain height-word polarity and the LOS height word |
| `R-P0-19-N` | 03 | — | the nanoframe reveal |
| `R-P0-19-P` | 03 | — | the nanolathe spray: its source point's coordinate space and the inclusive rectangle filler |
| `R-P28-ANG-01R` | 02 | §1 | the building heading field audit |
| `R-P28-ANG-01R` | 04 | §2 | the unit-initialization heading |
| `R-P28-ANG-01R` | 05 | §3 | the factory product heading |
| `R-P28-COB-01R` | 04 | — | the construction-KBot initial-pose boundary |
| `R-PATH-01` | 04 | §1–§15 | the path search working set, entry array and touched bitmap, its costs, and the scheduler |
| `R-PLAT-01` | 01 | §1–§9 | the application and battle host pumps, the command-line and profile reads, pause framing and the speed clamp, the thread census, the tagged allocator, input queue capacity and overflow, which thread's CRT block each consumer reads, the exception filter, and the developer console |
| `R-PLAT-02` | 01 | §1–§8 | process static initialisation and the engine block, the quit and shutdown sequence, window-creation defaults, and the scaled-clock timer table |
| `R-PROD-01` | 05 | §1–§8 | the economy fields with widths, defaults and reader census; the activated bit; the wind phase and its draws; tidal strength; upkeep timing; the metal byte and footprint sampling |
| `R-RAST-01` | 03 | §1–§8 | the polygon raster: edge walk, span inclusion, the winding cull, the fixed-point steps, and SHD-only lighting in the model path |
| `R-REN-02R` | 03 | — | red/purple fringe provenance |
| `R-REN-03A` | 03 | — | the per-unit composition image, the height key, and structure anti-aliasing |
| `R-REN-03D` | 03 | — | model shadows: projection, fill, tinting, and cache |
| `R-REV-01` | 07 | §7, §10 | hover hull extrema, corner mapping, projection sign, the polygon predicate, and the HOT UNITS producer |
| `R-REV-02` | 04 | — | the exit-piece locator transform |
| `R-RND-02A` | 03 | — | model-path shading and stock reachability |
| `R-RND-02A` | 04 | — | the script-side shading census |
| `R-RR16-A` | 03 | §1 | gray table construction |
| `R-SAVE-02` | 08 | §1–§15 | the Save Game screen: file naming, the slot list, overwrite and delete |
| `R-SAVE-FEATURE-01` | 08 | — | feature record maps and staged reconstruction |
| `R-SAVE-ORDER-01` | 08 | — | per-unit order records and subtype payloads |
| `R-SAVE-UNIT-01` | 08 | — | the unit base record and fixed-slot reconstruction |
| `R-SAVE-WEAPON-01` | 08 | — | fixed weapon-slot records and transient aim state |
| `R-SEL-02A` | 03 | — | selection geometry, palette, and the composition boundary |
| `R-SEL-02A` | 07 | — | the selection overlay and the picking evidence |
| `R-SEL-02B2` | 07 | — | hover hull arithmetic and its publication boundary |
| `R-SENSOR-01` | 03 | — | sensor phase placement in the tick |
| `R-SESS-01` | 08 | §1–§9 | session kinds and the accessor, the player record's peer-identity sort key, and the two live-player counters |
| `R-SHARE-01` | 05 | §1–§10 | the sharing control bytes, the two alliance rows and the alliance predicate, the two transfer helpers, and the automatic dispatcher |
| `R-SKIR-01` | 08 | §1–§11 | the skirmish setup record and every option's consumer chain |
| `R-SLOPE-01` | 04 | §5 | the height byte's path into the movement slope test, and the footprint rule |
| `R-SND-01` | 02 | §1, §2 | the sound-category loader: file, record, bare and numbered keys, captions |
| `R-SPEC-01` | 04 | §0–§15 | the special-behavior FBI keys: storage, reader census, contracts |
| `R-STANCE-01` | 04 | §1–§9 | stance values and labels, the writer chain from button to unit field, the standing-fire and standing-move gates, the chase leash, defaults and factory inheritance |
| `R-STRIP-01` | 03 | §1–§3 | the effect strips: producer census and per-strip events, object families and terminal state, and the sweep's CRT draws |
| `R-TERR-01` | 03 | §1–§8 | the two terrain attribute encodings and the header slot map, the void strips, the height queries and their sentinels, the air sector grid, the map-global block, and the absence of deformation |
| `R-TRIG-01` | 08 | §1–§12 | trigger authority, record shape and construction, the shared predicates, every condition, `MoveUnitToRadius` geometry, and the tick site's cadence and latch |
| `R-UNIT-06` | 04 | §1–§6 | guard assistance retargeted and sized, the guard's wake and re-target producers, and the attachment and transport callback encoding |
| `R-VIS-01` | 03 | §1–§9 | the visibility mode word's provenance and polarity, and the LOS mask's per-source-slot bits |
| `R-WATER-01` | 03 | §1, §2 | wakes are the script-emitted strip-2 sprinkles; there is no wake rectangle |
| `R-WFX-01` | 06 | §1–§6 | the weapon presentation keys: parse, storage, art binding, and the loop byte |
| `R-WGT-01` | 07 | §1–§13 | the gadget service pass and who closes the window, the key matrix, per-kind gadget behavior, the parser's key table, and the grey flag |
| `R-WGT-02` | 07 | §1–§5 | the front-end bitmap cache, window-record words and their setters, and the gadget appenders |
| `R-WIND-01` | 03 | — | the wind direction vector: which table feeds which axis |
| `R-WORK-01` | 05 | §1–§15 | the shared construction step instruction-exact, and reclaim, capture and resurrection, exactly |
| `R-WPN-01` | 06 | §4 | the fixed trigonometry table is 512 entries, quantizing to 128 angle units |
| `R-WPN-02` | 06 | §2, §5 | the death latch and the paralyzer gate read the victim's player controller type |
| `R-WPN-03` | 06 | §1–§6 | the accuracy family's full reader census, the drift gate, and the spread — one reader each, and no moving-accuracy or aim-rate mechanism |
| `R-WPN-04` | 06 | §1–§4 | the target-point resolver: point-target height, the dead-target clear, and SweetSpot's vertex-box centre |
| `R-WPN-05` | 06 | §1–§12 | the weapon-slot engagement distance and the order-side shot-admission gate, the slot control byte, the wind words, and the stockpile queue's malformed arms |

Three anchors that code and the design documents still cite are introduced
under a bold paragraph lead-in rather than a heading, so they are not in the
table above and are listed here instead: `R-DOC04-D` (mobile occupancy and path
search, document 04), `R-P0-08-A` (the occupancy layer finished buildings do
not write, document 04, cited from 05), and `R-P0-19` (the nanolathe
presentation pipeline, document 03, with the mobile-build walk target in 04).
They resolve, because the citation checker indexes an anchor wherever it
appears in a document, but a finding that code cites belongs under a heading;
promote them when the owning section is next edited.

## Legacy packet citation routing

Early implementation comments cite research packets that were later folded into
the category documents. The packet files are not part of the curated reference;
this table keeps their searchable identifiers and routes each to its current
home. A packet-local `§` suffix in an old citation describes the retired packet
outline, not a section of the destination.

| Legacy ID | Current home |
|---|---|
| `[P0-01]`, `[P0-02]`, `[P0-03]` | `[08 "Established AI-facing data and rooted planner"]` |
| `[P0-04]` | `[08 "Placement and battle entry"]` |
| `[P0-05]` | `[08 "Campaign catalog and progression"]`, `[08 "Session end and reporting"]` |
| `[P0-06]` | `[04 §3.6]`, `[08 "Mission and map schema selection"]` |
| `[P0-07]`, `[P0-08]`, `[P0-09]` | `[04 §3]` — orders, queue pumping, and same-tick dispatch |
| `[P0-10]` | `[06 §3]`, `[06 §4]` — targeting, Aim, and firing |
| `[P0-11]` | `[03 §3.2]`, `[06 §3]` — sensors and target admission |
| `[P0-12]` | `[04 §8]` — ground collision and occupancy |
| `[P0-13]` | `[04 §7]` — path search, goals, and scheduling |
| `[P0-14]` | `[04 §3.8]`, `[05 "Construction arithmetic"]` |
| `[P0-15]` | `[05 "Unit reclaim"]`, `[05 "Capture"]`, `[05 "Resurrection"]` |
| `[P0-16]` | `[04 §2]`, `[05 "Unit creation and limits"]` |
| `[P0-17]` | `[03 §2]` terrain grids, plus `[fmt tnt]` plot-cell encoding |
| `[P0-18]` | `[03 §3.2]` — visibility mask and LOS raster behavior |
| `[P1-01]` | `[08 "Session end and reporting"]` |
| `[P1-02]` | `[02 §6]` mission data, `[08 "Mission and map schema selection"]` |
| `[P1-03]` | `[02 §5]` movement-class data, `[04 §6]`, `[04 §9]` |
| `[P1-04]` | `[04 §9]`, `[04 §10]` — hover, amphibious, and flight |
| `[P1-05]` | `[04 §10.2]`, `[04 R-AIR-01 §9]`, `[04 R-UNIT-06 §3]` — transport and attachment |
| `[P1-06]` | `[05 "Authoritative settlement order"]`, `[05 "Resource admission and carry"]`, `[05 "Allied resource and sensor sharing"]` |
| `[P1-07]` | `[04 §8]`, `[06 §8]`, `[06 §9]` — collision and damage |
| `[P1-08]` | `[06 §5]`, `[06 §6]`, `[06 §7]` — projectile families and edge states |
| `[P1-09]` | `[06 §11]` — stockpile and interception |
| `[P1-10]` | `[05 "Feature catalog and placement"]`, `[05 "Feature burning"]`, `[05 "Feature sinking and water interaction"]` |
| `[P1-11]` | `[04 §4]`, `[04 §5]` — COB VM, ports, callbacks, and persistence |
| `[P1-12]` | `[02 §2]`, `[02 §8]` — content resolution, overrides, and error policy |
| `[P1-13]` | `[08 "Save-file organization"]`, `[08 "Load process"]` |
| `[P1-14]` | `[04 §3.7]`, `[07 §8]`, `[07 §9]` — picking, selection, latches, and build UI |
| `[P1-15]` | `[03 §2]` terrain, `[05 "Resource contributions"]` terrain-metal extraction |

## Gap disposition

The gap-analysis files were a working audit that went stale and were removed;
open questions are ephemeral and belong at the site of the work, as
`TODO(T23)` / `TODO(T25)` / `TODO(question)` markers in code and as **Unknown**
items in each document's "Missing and unknown" list. Their closed tasks were
promoted into the category documents first. A `[GAP Tn]` citation in code means
the task's content now lives here:

| Task | Promoted to |
|---|---|
| T1 economy ledger cadence | `[05 "Authoritative settlement order"]` |
| T2 flight integrator arithmetic | `[04 §10]` |
| T3 factory production lifecycle | `[04 §3.5]`, `[05 "Build request and factory queue behavior"]` |
| T4 death-cause producer table | `[06 §12.1]` |
| T5 ballistic malformed and overflow table | `[06 §7]` |
| T6 pool-full fire retains draws | `[06 §5]` |
| T7 visibility predicate and sight shapes | `[03 §3.2]` |
| T8 InitialMission mini-language | `[04 §3.6]`, `[08 "Mission and map schema selection"]` |
| T9 session states, lockstep pacing, save | `[08 "Session lifecycle"]`, `[08 "Save-file organization"]` |
| T10 trigger objects and mission mechanics | `[08 "Victory and defeat triggers"]` |
| T11 peer hash overwrite-sync | `[01 §9]`, `[08 "Synchronization and integrity checks"]` |
| T12 single- versus multiplayer pause and carry | `[01 §4.3]` |
| T13 wind draw arithmetic | `[01 §7.3]` |
| T14 content-layer promotions | `[02 §5]` |
| T15 COB callback catalog and same-tick windows | `[04 §5.4]` |
| T16 route publication and water damage | `[04 §7.3]`, `[04 §9.2]`, `[04 §10.2]` |
| T17 sensor phase, fog, and compositor | `[03 §3]`, `[03 §5]` |
| T18 script rotation order | `[03 §5]`, `[04 §4]` |
| T19 path heuristic family | `[04 §7.2]` |
| T20 economy and feature residuals | `[05 "Feature catalog and placement"]` and the settlement sections |
| T21 combat residuals | `[06 §9]`, `[06 §12]` |
| T22 interface promotions | `[07 §6]`, `[07 §9]` |
| T23 platform residuals | `TODO(T23)` markers in code — none gate gameplay |
| T24 network internals | out of scope; no multiplayer |
| T25 accepted blocked items | `TODO(T25)` markers in code: extractor placement helpers, resource-activity ledger arguments, definition flag semantics, save bulk-box byte layouts |

The compressed GAF decoder is **not** a gap: it is fully specified in
`[fmt gaf]` and implemented.

## How coverage of the executable was established

The corpus was completed from the executable inward, not from the documents'
own lists of open questions. Every reachable function in the retail
executable's game code carries a row in a coverage ledger with one of six
classifications: **covered** — a research section states its behavior at
implementable precision and the row cites that section; **library** — compiler
runtime, decompressor, video codec or platform shim, with the evidence;
**dead** — no caller, no table reference, no callback registration; **out of
scope** — the networking transport, lobby and codec internals excluded above,
still named so the boundary is explicit; and **partial** and **uncovered**, of
which none remain. Functions were grouped into clusters by walking the call
graph from known roots — the tick phase dispatcher, the order descriptor
table, the GUI window handler table, the COB port switch, the front-end state
table — and by string vocabulary (GUI screen names, TDF keys, diagnostics);
each cluster maps to one document. A section was accepted as covering a row
only when a fresh implementer, reading that section alone, could write the
function without choosing anything: inputs named with units and defaults,
arithmetic and widths spelled out, comparisons exact, edges and timing stated,
confidence marked per claim.

The ledger itself lives outside this repository, in the raw analysis corpus,
because its rows are keyed by executable address. It is re-checked against the
research headings whenever a document changes, so a rewrite that drops a cited
section is caught there as well as by `internal/docs`. What can be committed —
the map from cluster role to design document, in role names only — is in
`docs/ARCHITECTURE.md`.

## Source hygiene

The raw analysis corpus contains exports, current notes, superseded notes,
incomplete notes, quarantined placeholder reports, machine indexes, and
contradiction ledgers. Promotion into these documents follows fixed rules:

- Explicitly retracted, superseded, or quarantined behavior is never promoted
  to an affirmative contract.
- A corrected function identity outranks an earlier semantic label.
- A direct writer/reader chain outranks a field name inferred only from an
  offset or a nearby string.
- Data loaded from a key is not assumed to affect gameplay until a consumer is
  found; a parsed but unconsumed field is documented as retained, inert, or
  unknown, according to the bounded search.
- UI text and function adjacency can identify a subsystem; they do not by
  themselves establish its arithmetic.
- Imported APIs establish available platform behavior; a specific engine use
  requires a call path.
- File decoders are specified only to the extent their retail read and write
  paths have been traced.
- Strategic-AI policy branches, sensor interactions, script opcodes, and
  malformed-input paths that remain open are written as explicit gaps rather
  than reconstructed folklore.

## How to use the specification

1. Implement only established behavior as a strict retail contract.
2. Isolate supported inference behind a named seam, so a later trace can
   replace it without rewriting unrelated systems.
3. Represent unknown behavior explicitly in code and documentation; never
   choose a modern default and label it retail.
4. Preserve data the retail executable reads even when its consumer is
   unknown, provided preservation does not invent an effect.
5. Preserve authoritative order, single-precision narrowing, integer
   truncation, stable pool order, allocation failure, sentinel values, and
   queue capacity wherever they are established.
6. Keep presentation events separate from authoritative outcomes, even where
   one retail routine initiates both.
7. When new static evidence closes or contradicts a behavior, edit the owning
   document in place: the closure goes under the heading that owns it, with an
   `R-<id>` anchor if code will cite it, and the correction replaces the text
   it corrects. Update that document's "Missing and unknown" list in the same
   change.

## Global missing and unknown

- Complete function-boundary reconciliation across the whole executable.
- Recover executable regions missed by every disassembler used so far.
- Re-audit library and runtime identifications, so compiler support code is not
  mistaken for engine behavior and game code is not discarded as a library.
- Finish reader, writer, and caller censuses for every dynamically addressed
  global structure.
- Close the quarantined COB opcode, port, callback, construction, visibility,
  content-format, renderer, audio, AI, and save placeholders.
- Establish malformed-input, overflow, allocation-failure, and shutdown
  behavior for every file, queue, pool, and platform service.
- Complete cross-category same-tick ordering for creation, callbacks, economy,
  movement, projectiles, features, visibility, and cleanup.
- Derive bit-exact floating-point and random-consumption behavior for every
  authoritative algorithm whose evaluation order is still ambiguous.
- Close the strategic planner's remaining policy branches, named as Unknown in
  document 08.
- Inventory every save section and determine whether random and scheduler state
  is serialized indirectly.
- Extend the bounded replay search to unrecovered functions and dynamically
  constructed names.
- Reconcile the remaining contradictions without using visible behavior or a
  non-retail implementation as the deciding source.
