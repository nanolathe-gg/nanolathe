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

At the time of this synthesis, the primary decompiler index contains 2,657
function starts and 941 exported decompilations. An independent disassembler
recognizes 3,808 starts. The tools agree on 2,568 starts, disagree on some end
boundaries, and each recognizes starts that the other misses. Some executable
regions remain unrecovered by both.

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
and state transitions across too many boundaries.

| Document | Category | Main ownership |
|---|---|---|
| [01-core-runtime-platform-and-determinism.md](01-core-runtime-platform-and-determinism.md) | Core runtime, platform, and determinism | PE/Win32 lifecycle, window/message pump, scheduler, authoritative phase order, threads, locks, allocators, pools, queues, RNG, x87, diagnostics, runtime failures |
| [02-content-vfs-formats-and-data-loading.md](02-content-vfs-formats-and-data-loading.md) | Content, VFS, formats, and loading | Current directory, mount tiers, loose/archive resolution, HPI family, TDF grammar, configuration/localization, catalogs, file decoders, linking, caching and load failures |
| [03-world-visibility-rendering-audio-and-video.md](03-world-visibility-rendering-audio-and-video.md) | World and presentation | Coordinates, terrain, height/water, visibility/radar/fog, software rasterizer, GDI/DirectDraw, palettes, 3DO/GAF presentation, effects, shadows, audio, CD music, Smacker and capture |
| [04-units-orders-scripts-and-movement.md](04-units-orders-scripts-and-movement.md) | Units, orders, scripts, and movement | Unit identity/lifetime, order state, COB VM and pieces, A*, path scheduling, steering, collision/occupancy, hover, VTOL, transport and air work positioning |
| [05-economy-construction-players-and-features.md](05-economy-construction-players-and-features.md) | Economy, construction, players, and features | Resource buckets/settlement, storage, extraction, sharing, nanoframes, factory queues, build/repair/reclaim/capture/resurrection, limits, feature placement/successors/fire/sinking |
| [06-weapons-projectiles-damage-and-effects.md](06-weapons-projectiles-damage-and-effects.md) | Weapons and combat | Weapon definitions/slots/targets, costs and cadence, projectiles/trajectories/guidance/beams, collision, armor, AOE, status, veterancy, stockpile/interception, death and combat events |
| [07-interface-input-camera-and-front-end.md](07-interface-input-camera-and-front-end.md) | Interface, input, camera, and frontend | Win32 input, software cursor, picking/selection/groups, command UI, build pages, queue overlays, camera control, minimap interaction, GUI widgets/screens, HUD, chat and text |
| [08-sessions-campaign-ai-network-save-and-replay.md](08-sessions-campaign-ai-network-save-and-replay.md) | Sessions, campaign, AI, networking, save, and replay | Game modes, campaigns/missions/triggers, skirmish/lobby, known AI inputs, DirectPlay packet/lockstep state, checksums, disconnects, save/load and bounded replay absence |

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
- weapon accuracy, tolerance, and pitch-tolerance are parsed but have no
  gameplay reader in the bounded retail image;
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

## Gap register

[GAP-ANALYSIS.md](GAP-ANALYSIS.md) records the audit of this specification
against the underlying executable analysis: what the specification stated only
as prose while a concrete contract was available, what it declared unknown
that was in fact established, what a bounded re-derivation closed, and what
remains genuinely blocked. It is a working document and is expected to shrink
as its entries are folded into the category documents.

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
   closes or contradicts a behavior.

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
