# Retail engine sessions, campaign, AI, networking, save, and replay

## Purpose and evidence boundary

This document specifies how the retail executable creates game sessions,
loads campaigns and missions, configures skirmishes, evaluates victory and
defeat, represents computer-controlled players, exchanges deterministic
commands over DirectPlay, and saves or restores a battle.

It is a clean-room design derived only from the decompilation corpus for the
retail executable. It contains no executable addresses, memory offsets,
decompiler variable names, or source translation. It does not use behavior
from another engine, a replacement implementation, executable-comparison
tooling, or original data files as evidence.

Some parts of this category are much less complete than the simulation and
renderer. An older analysis incorrectly labeled the scenario unit reconstructor
as the strategic AI planner. That analysis is retracted. The later whole-image
static census instead positively roots a distinct strategic planner and
establishes its recovered scope: the class-vector refresh and classifier, ten
manager task slots (nine active and one null), direct task-group writer, wave
bootstrap/merge, task deadlines and dispatch gates, economy-mixed scoring and
profile handling, and placement selection and helpers [P0-01] [P0-02] [P0-03]
[R-P0-04] [R-P0-05]. Those contracts are specified in the AI sections below.

**Correction to the prior summary.** The earlier sentence that this document
“treats the strategic planner as unknown and retains only the AI fields and
control paths that the executable directly establishes” was wrong. It carried
the scenario-reconstructor identity error forward as if it were evidence that
no strategic planner had been found; the positive manager, classifier, group,
wave, scoring, and placement paths now establish the planner itself. The
planner is established only to that recovered scope. Its remaining bounded
residuals — the semantic name of the classification helper, the two opaque
high runtime status-bit semantics, the strategic half-capacity state writer,
any additional indirect task-vector writer, and transport/naval/air policy or
geometry — remain explicitly Unknown below and must not be filled by
inference. [P0-01] [P0-02] [P0-03] [R-P0-04] [R-P0-05]

Evidence terms:

- **Established**: direct static data flow, import use, embedded protocol or
  schema strings, or a bounded negative search supports the statement.
- **Supported inference**: several direct facts support the design, but an
  important caller, field identity, or state transition is not closed.
- **Unknown**: the executable analysis does not yet provide a safe contract.

## Dependencies

This category relies on the following other specifications:

- The core runtime defines tick generation, phase order, object pools, the
  shared simulation random stream, floating-point behavior, and diagnostics.
- The content system defines VFS lookup, TDF parsing, mission and campaign
  ingestion, unit catalogs, map loading, GUI files, and localization.
- The world system defines terrain, feature, visibility, radar, and map identity
  state.
- The unit and combat systems define canonical orders, target identities,
  death, construction, and script state.
- The economy system defines player resources, sharing, ownership transfer,
  unit limits, and construction queues.
- The interface system defines the campaign, skirmish, lobby, briefing,
  load/save, score, chat, and endgame screens.

## Session structures

### Game session

A running game session contains at least:

- game type and front-end mode;
- selected campaign and mission, or selected multiplayer/skirmish map;
- difficulty and mission schema;
- a map/mission object;
- ten fixed player slots;
- the local player slot;
- session and game identifiers;
- authoritative global tick and scheduler state;
- network-enabled and placement/start flags;
- map, terrain, feature, and resource identity/checksum state where recovered;
- unit, projectile, feature, script, mission-trigger, economy, path, radar, and
  presentation subsystem state;
- save metadata and game time;
- end-state flags for victory, defeat, disconnect, and abort.

The session object is reconstructed for load rather than simply restoring a
native memory image.

### Player slot

A player slot is shared by lobby, simulation, networking, and save systems. It
contains:

- whether the slot is empty, open, blocked, or participating;
- stable slot number;
- local or remote network identity;
- connection/alive state;
- human/computer/control state;
- side, color, team, and ally group;
- ready and map-control flags;
- starting energy and metal;
- current resource and statistic state;
- unit ownership and unit-limit state;
- ping/round-trip timing;
- resource and sensor sharing options;
- per-peer send/receive and synchronization state;
- lobby display state.

Several numeric slot-state values are directly observed, but the semantic name
of every value is not completely reconciled. Implementations should keep the
wire values distinct even when UI labels collapse them.

### Peer transport state

Each network peer has state for:

- DirectPlay player identity;
- mapping between DirectPlay identity and player slot;
- send pacing and next-send time;
- outgoing packet/frame queue;
- receive buffer;
- future/out-of-order frame queue;
- acknowledgement and retransmission state;
- last ping time and measured round-trip time;
- disconnect and integrity status;
- remote-progress/lag state feeding soft tick pacing.

The network layer is bounded and uses fixed-size or capped queues. It does not
expose a rollback snapshot or an unbounded event log.

### Mission object

A mission object owns:

- mission metadata, description, planet, media, and briefing fields;
- selected difficulty or multiplayer schema;
- map name and companion terrain file;
- global wind, tidal, gravity, water, lava, metal, meteor, and economy setup;
- player counts and starting resources;
- unit placement records;
- start-position/special records;
- feature placement records;
- victory trigger objects;
- defeat trigger objects;
- mission timers and mission-specific flags;
- restrictions such as allowed units and maximum units;
- score multipliers and end-of-mission state.

### Mission placement record

A unit placement record can contain:

- unit type name;
- stable mission identity;
- world X, Y, and Z;
- facing angle;
- owning player;
- health percentage;
- build priority (parsed but never read by this executable);
- delayed creation countdown;
- mission-critical flag (parsed but never read);
- AI ignore and AI priority-target flags (parsed but never read);
- initial group (parsed but never read);
- immunity state — the only placement flag byte the creation reader consumes,
  extracted as the byte's high bit and applied to the created unit;
- initial mission string.

The creation reader also consumes the **facing angle**: it copies the
authored angle word from the placement record into the created unit's
heading word, and that heading word is read by the steering code. The
placement record's fixed fields in order are: the three interned name
pointers, the fixed X/Z/Y coordinates, the angle word, the health
percentage word, the creation-countdown dword, the build-priority word,
the player byte, and the flag byte — the build-priority word sits between
the countdown and the player byte, not after the health word as earlier
noted.

The executable parses these fields into fixed records before unit creation.
A complete reader census over the runtime unit record closes the parsed-only
list above: `MissionCriticalUnit`, `AiIgnore`, `AiPriorityTarget`,
`InitialGroup`, `BuildPriority` and the delayed-creation countdown have no
reader anywhere in the image — the earlier phrasing "the creation reader
copies build priority" is retracted; the copy it observed was the angle.
The immunity high bit *is* transferred to a
runtime status bit on the created unit — and that bit likewise has no reader,
so the single consumed flag is itself inert in this executable. Retain all of
them verbatim for save fidelity and diagnostics; act on none of them.
`UseOnlyUnits` is not inert: it is read at OTA load and routed through the
resource resolver into the campaign `camps\useonly` area — path-building
evidence that it feeds the campaign restricted-units mechanism.

Facing angle for each placement record is converted from authored degrees to a 16-bit circle by a fixed-point magic multiply. The retail sequence multiplies the scaled degrees by a fixed magic constant and corrects with a division by 360 scaled to 65536, all with truncation toward zero. The equivalence domain is now settled: for non-negative degrees below the 32-bit wrap it is bitwise identical to truncate toward zero of degrees times 65536 divided by 360; for negative degrees the result is that truncation **plus one** unit (about 0.0055 degrees of bias); degrees in the 360..65535 range wrap correctly and stay identical to the floating formula modulo the circle; only |degrees| at or beyond 65536 (where the degrees-times-65536 intermediate wraps 32-bit) diverge from the floating formula. The earlier "differs for greater-than-360 values" phrasing is retracted (it wraps correctly), and "equivalent for the whole integer range" is retracted (negative degrees carry the one-unit bias). [P0-06] [lane 08 angle equivalence]

The initial-mission string is interpreted once at battle start, on the loading worker after all units exist, for fresh mission-type-1 starts and for BetweenMissions-continuation loads (the save-blob gate runs the same fresh spawner when the BetweenMissions flag is present), and before any creation script, movement, or visibility publication for that tick. No other consumer of the stored script strings is located in the bounded search. [P0-06] [lane 08 BetweenMissions polarity]

Tokenization scans the string for the comma character, copies each span into a 256-byte frame, and splits on every comma; the scan restarts past the comma with whitespace skipped. Tokens whose first character lies outside the A through w range are ignored and scanning resumes at the next comma. The remaining tokens dispatch through a 23-entry table that is case-insensitive for most verbs but carries a quirk: an uppercase-led W enters the build block rather than the wait block, so a plain wait can only be written lowercase as w and a wait-for-attack as lowercase wa, while an uppercase W with a following w character selects the stockpile build form. The families that appear in stock assets are move, attack with a numeric coordinate form and a by-type name form, build and stockpile build, self-destruct, guard, immediate attach, flag-bit write, patrol with timeout, make-selectable, unload, wait with seconds and an optional trailing integer, and wait-for-attack by name. Unknown letters, digits, and punctuation are ignored silently with no diagnostic and scanning resumes. [P0-06]

Argument parsing uses the retail scan-format family with formats for two floats, three floats, integer pairs, and name scansets that accept alphanumerics, underscore, and dot — with one exception: the wait-for-attack scanset is `" %[a-zA-Z0-9.]"` and excludes the underscore (the earlier "all name scansets accept underscore" phrasing is retracted; attack-by-type and guard do include it). Only the numeric attack form tests that two floats were converted and only the wait-for-attack form tests that a name was converted; every other verb ignores the conversion count and still queues an order with whatever values the scan left in the frame, which for malformed numbers means zero-valued or stale stack values but still a queued order. Names for guard, immediate attach, and the name form of attack are resolved by scanning the sparse created array in placement order from zero upward, testing Ident case-insensitively first and then Unitname, returning the first occurrence and skipping null gaps left by failed allocations; duplicate names therefore resolve to the lowest placement index. A failed type lookup for attack or build produces no queue, while a failed unit lookup for guard produces no queue and for wait-for-attack falls back to self. The order queues and position scaling use truncate toward zero of floats multiplied by 65536 for coordinates and by 30 for timeouts, matching the retail helper's truncation; the flag verb writes bits without queuing and the immediate attach verb posts an internal attach without queuing. [P0-06]

A postlude runs after the string is exhausted: when at least one order was queued it clears bit 5 of the unit's class word, and unless the string contained a numeric attack, patrol, self-destruct, or make-selectable, it queues a final make-selectable with zero auxiliaries. The census over the shipped mission corpus shows every verb shape is exercised in stock assets, with illustrative counts such as selectable near two thousand, guard above two thousand, flag-bit writes near one thousand, wait and move each above one thousand, and build, stockpile, wait-for-attack, and unload each in the hundreds; malformed-argument paths beyond the two tested verbs are not present in stock assets but still queue silently. [P0-06]

The complete verb grammar, argument formats, and malformed-input handling are specified in document 04, section 3.6, which this summary now mirrors without reproducing raw table bytes.

### Trigger object

Mission victory and defeat conditions are allocated as polymorphic trigger
records. A builder routine probes the eighteen condition keys in a straight chain and
allocates a per-condition record on a hit. Record size varies with the
condition and is one of seven established buckets — `0x0C` flag-only (12 bytes), `0x10` timer (16), `0x14` boundary (20), `0x30` string+count variant A (48), `0x32` string+count variant B (50), `0x36` canonicalizing string shape (54), and `0x40` radius (64) carrying X/Z plus radius payload [08 "Trigger object"] [GAP T10]. Every record leads with a vtable pointer whose table covers all
eighteen condition types — eight slots per entry (shared destructor/helper slots plus the six named behavioural slots described under Evaluation: poll, unit-died notification, capture/transfer notification, created notification, and save/load) — with the trigger's completed flag adjacent [08 "Trigger object"].

The mission object owns separate growable arrays for victory and defeat
conditions, each count kept beside its array. An empty queue receives an
injected default destroy-all-units-class victory or all-units-killed-class
defeat trigger, guaranteeing one win and one lose condition.

Timer triggers store time in authoritative ticks after multiplying authored
seconds by thirty [08 "Trigger object"].

### Save tree

The retail save is a binary `HAPIBANK` bank, not a TDF tree or dump of
compiler-native memory. Its payload is a sequence of named accounts, each
containing typed integer, double, string items and binary boxes, addressed by
the string pool. Logical references are written as stable identifiers, names,
numeric fields, and fixed records, then resolved during loading.

## Game-mode selection

The executable distinguishes at least:

- single-player campaign;
- direct mission selection;
- skirmish;
- multiplayer lobby/session;
- saved-game loading;
- front-end-only and media states.

Mode selection controls which mission schema is selected, how player slots are
populated, which options are read, whether DirectPlay is initialized, and which
start synchronization barrier is required.

## Session lifecycle

### Session states

A single session callback table drives the game-mode state machine across
eight states:

| State | Callback | Behavior | Label |
|---:|---|---|---|
| 0 | teardown A | cleanup variant A, then state 2 | Supported inference |
| 1 | teardown B | alternate cleanup, then state 2 | Supported inference |
| 2 | front-end/session router | selects state 3, 4, or 5 | Supported inference |
| 3 | network pre-load | network startup polling, then state 5 | Supported inference |
| 4 | local pre-load | initializes local two-player records, then state 5 | Supported inference |
| 5 | battle loading/setup | loading UI/thread and readiness barrier | Established |
| 6 | battle | live battle loop | Established |
| 7 | results/postgame | post-battle handling | Established |

Transitions and side effects are established; the semantic names for states
0–4 are supported inference.

Observed transition graph:

```text
0 -> 2
1 -> 2
2 -> 3 | 4 | 5
3 -> 5
4 -> 5
5 -> 6
6 -> 7   (normal battle completion)
6 -> 2   (abort/return path)
7 -> 2   (front-end return paths)
```

Save/load integration rides this graph unchanged: a Gametype-2 save selects
state 5 directly; a Gametype-1 save first selects state 4, which configures
two campaign players and then selects the same state-5 path. Completion of the
state-5 loading thread installs state 6; its first run happens on the next
orchestration dispatch, not inline.

Residual: UI-level names for states 0–4, and whether any provider-specific
DirectPlay behavior adds transitions outside the reviewed callbacks.

### Admission masks

Packet admission masks treat exactly three session-state classes: bit 0 (`1`)
admits states other than 5 and 6; bit 1 (`2`) admits state 5 (battle
loading/setup); bit 2 (`4`) admits state 6 (live battle).

## Campaign catalog and progression [P0-05]

### Campaign discovery — Established [P0-05]

Campaign definition files live under the campaigns directory and are enumerated through the VFS in mount order: loose directory first, then patched archives, then expansion archives, then base archives, then CD-ROM archives. Within one provider the enumeration follows the host directory scan order without sorting. The first provider that contains a logical path wins; a duplicate logical path later in the scan order is hidden by the earlier winner's case-insensitive deduplication. A requested campaign file that does not resolve produces a diagnostic naming it; a missing companion terrain file for a mission is a load error, while missing optional media can fall back.

A campaign file is an ordinary text file whose top level contains contiguous mission sections named Mission zero, Mission one, and so on, enumerated until the first missing index. The name MissionList that appears in the executable is only the allocation tag for the resulting 256-byte-per-entry name array, not an authored wrapper section. Each mission section supplies a mission name read through the language-prefixed string accessor that tries a language-specific key first and then the plain key, defaulting to a built-in unnamed mission error string. Discovery constructs a campaign list from the enumerated files and then a mission name list for the selected campaign by counting the contiguous sections and filling the array with those names. Duplicate mission basenames that appear in different campaigns are isolated per campaign, but when the same mission filename is requested the resolved bytes follow the same first-provider-wins rule; patch archives therefore shadow base archives. The counting stops at the first gap, so a mission section after a gap is invisible. [P0-05]

The campaign front end consumes titles, descriptions, difficulty choices, planet identifiers, briefing and narration names, panorama, rotation, glamour, and sound media, mission ordering and availability, and completion state. The front end presents both a campaign list and a mission list.

### Planet and briefing selection — Established with supported inference [P0-05]

Planet values select parallel tables for briefing keys, panorama art, and rotation animation, each table holding the same number of entries and indexed by the same planet comparison. A special lunar branch rewrites the briefing selection when a display flag is set. The briefing controller opens a briefing panel, hides and shows specific interface groups, populates text, and fetches panorama and planet imagery through the same graphic lookup used elsewhere.

Wind for the briefing screen is drawn from the CRT stream before the simulation consumes either value: speed is a uniform integer in the authored minimum to maximum inclusive range, and direction is the low six bits of a CRT draw. The authored wind bounds and the resulting values are stored in the mission object and later initialized for the simulation. Missing wind keys default to zero. [P0-05]

Missing optional media can fall back or suppress presentation without aborting battle entry; missing data required to identify the mission or its terrain is a load error. A missing planet value that matches no table entry leaves the previous art unchanged rather than aborting.

### Progression — Established with bounded negative and unknown residual [P0-05]

The executable contains campaign selection, mission list, briefing, end-mission, score and report, and between-mission state. The end-of-mission latch is established: the trigger queues are polled once per 30 ticks in the local player's slice only, with victory as an AND across its queue and defeat as an OR, and victory evaluated first so simultaneous completion resolves as victory. When either side completes, the latch arms a countdown at four that decrements roughly once per second before a latch word is written with separate bits for ending, won, and lost; the lose path clears the win bit it would otherwise share. The score helper that writes the campaign result combines kills multiplied by a kill multiplier and elapsed ticks divided by 1800 multiplied by a time multiplier, both truncated and clamped at zero, and stores a W or L character per mission slot. [P0-05]

Persistence is split. Difficulty, the skirmish lobby fields, and three registry mirrors — difficulty, a games flag, and an all-missions flag — are kept under the installed software registry path and written back immediately when absent so a first run fully populates the registry (the earlier "two registry mirrors" count is corrected: the enumeration below lists three). One mirror holds the difficulty value masked to 16 bits and cycled by the difficulty controls; a second mirror holds a games flag gated on a display mode, and a third holds an all-missions flag as a single bit. The per-mission W and L characters in memory and the between-missions bank account named Summary that carries a BetweenMissions flag are written through the bank system, not the registry. The bank's timing block that persists scheduler state is a 28-byte binary box; larger boxes have trailing bytes ignored, and the bank's string pool, header, and account enumeration are not range-checked. Campaign continuation on load inspects the saved Summary account for the BetweenMissions flag to decide between fresh mission spawning and battle reconstruction — the battle-entry gate routes the flag-present case to the fresh spawner and the flag-absent case to battle restoration, never the reverse. [P0-05] [lane 08 BetweenMissions polarity]

VFS first-win, first-gap termination, language-prefixed names, wind draws from the CRT stream before simulation, the latch bits and scoring arithmetic, and the registry versus bank split are established. The all-missions registry bit's consumer is located: the single-player panel shows its "Any Msn" control and keeps the bit when set, a toggle callback (inert while the panel's text field holds the "DRDEATH" easter-egg string) flips the bit, toggles the control, and writes the bit back to the registry, and a writeback helper persists it. The mission-list build always counts every mission and neither the new-game panel nor the end-mission screen filters the list by the bit — the exact listbox-selection effect of the toggle remains the bounded residual (the earlier "no reader anywhere" is superseded). The provider-specific enumeration order beyond mount order is host-dependent but deterministic for a given filesystem, and the exact narration and glamour fallback beyond silent suppression is not closed. [P0-05] [lane 08 AllMissions]



## Mission and map schema selection

### Mission/terrain pair

A playable map is represented by a text mission file plus a companion terrain
file. Map enumeration starts from mission files. Loading the selected mission
resolves and validates its terrain pair, mission version/schema, map-global
values, placements, and triggers.

### Mission type dispatch

The mission loader dispatches on the mission type discriminant:

- **Type 1 (campaign)** builds the `MISSION%d` section name and requires the
  mission file's `GlobalHeader` block plus `missionfile`/`missionname`.
  Distinct diagnostics cover each failure shape: missing mission file ("The
  requested mission file … does not exist"), corrupt mission file ("Hey,
  joker! Mission file %s is corrupt"), legacy format ("Old TED format no
  longer supported!"), and missing block ("No GlobalHeader block in mission
  file!").
- **Types 2 and 3** (skirmish/multiplayer and loaded-save OTA) join the raw
  OTA path directly against the maps directory; when parsing misses, a fuzzy
  search falls back to the closest match.

The common tail loads briefing/environment values, builds trigger objects,
meteor configuration, and the remaining mission subsystems.

### Schema choice

Schema selection seeks `[GlobalHeader]` (missing: "Very bad news! No MSG!"),
then `Schema %i`, then reads `Type` and matches it case-insensitively against
literal candidate values tried in fixed trial order:

- Campaign mode tries the three difficulty literals in a difficulty-dependent
  permutation — difficulty 0 tries `Easy`,`Medium`,`Hard`; difficulty 1 tries
  `Medium`,`Easy`,`Hard`; difficulty 2 tries `Hard`,`Medium`,`Easy`. Other
  difficulty values fail.
- Skirmish/multiplayer modes try `Network 1` through `Network 4`.

For skirmish/multiplayer, each candidate additionally counts its `StartPos`
special records and is accepted when that count equals the player count
(counted as the last occupied lobby slot index plus one — equivalent to the
player count for the dense skirmish lobby; the "counting nonzero lobby
entries" phrasing is corrected), **or** the counted player count is zero
(with the candidate still required to have at least one StartPos),
**or** — as a fallback while no exact match has been found — this candidate's
StartPos count is the largest seen so far. A schema with zero StartPos
records is never accepted when the player count is non-zero. The last
accepted schema name is copied out and fed to the placement builder: after an
exact match only later equal-count candidates overwrite it, and in the
no-exact-match fallback a strictly larger count overwrites the previous
choice — "first accepted" is retracted.

Selection happens before placement records are instantiated, so all peers must
agree on the same schema.

### Wind initialization

Initial wind is drawn from the CRT stream at briefing-screen entry, before the
simulation consumes either value: speed = `rand() % (maxWind − minWind + 1) +
minWind` from the mission's parsed minimum/maximum, and direction =
`rand() & 0x3F`. Document 01 carries the full simulation-side draw arithmetic.

### Mission-global values

Directly read mission fields include:

- mission description;
- planet;
- no-movie flag;
- memory requirement;
- player count (stored as an 80-byte string in the mission object; it is a
  presentation field — schema selection uses the lobby occupancy count, not
  this value, and no tick consumer reads it);
- wind minimum/maximum and related wind state;
- gravity;
- tidal strength;
- water and lava behavior;
- surface metal;
- human and computer starting energy/metal;
- kill and time score multipliers (defaults 0.0 when absent);
- briefing, narration, hint, glamour, and glamour-sound names;
- allowed-unit restrictions;
- AI profile name;
- mission unit limit (campaign branch only, default 200, stored as the
  16-bit unit-limit word);
- meteor configuration;
- other game-mode flags (`mapping`/`lineofsight` set the campaign-mode LOS
  defaults merged at battle entry; there is no mission-level
  `commanderDeath` key — that rule is a lobby value).

The content and world specifications define numeric parsing and how these
values initialize subsystems. The `commanderDeath`, `numplayers`, and
`maxunits` dispositions above are closed by the mission loader and per-player
phase; the earlier "commanderDeath injection" question is resolved — no
mission key exists and no defeat trigger is injected for it.

### Meteor showers

Meteor processing is closed. Authored parameters merge with the
`gamedata/METEOR.TDF` `[Default]` record exactly as specified in document 02;
an empty `MeteorWeapon` is the only disable predicate. Nine scheduler fields
(enabled, active, next-strike, strike-end, next-hit, origin X/Z, target X/Z)
persist in the save's `Meteor` account — the "and the resolved weapon"
phrasing is retracted: the account carries nine integer items and no weapon
identity; the weapon is resolved at load from the authored mission
configuration. [lane 08 meteor account]

Scheduling runs in the tick phase **after wind jitter** and after the
projectile phase, so a spawned meteor first moves on the next tick. When the
next-strike time arrives the scheduler draws four values from the CRT stream —
target Z and X uniformly over the map, then origin offsets north of the target
— arms the storm for its authored duration, and schedules the next storm at
interval plus duration; each active hit tick then spawns one meteor using two
more CRT draws (lateral magnitude inside the authored radius, angle), for six
draws per spawned meteor. A meteor enters from 1350 world units of altitude on
a displaced lateral offset and flies a straight 90-tick line to reach its
randomly chosen map target, moving at fixed downward speed with horizontally
interpolated velocity copied verbatim into an ordinary pool record with null
attacker. Credit is neutral: nobody is charged; damage is the resolved
weapon's ordinary blast. The only capacity limit is the shared projectile
pool; a full pool drops that meteor silently and does not retry the slot.

## Placement and battle entry [P0-04] [P0-05] [P0-06]

### Mission and terrain loading order — Established [P0-04]

The common-tail mission loader validates a global header block and required keys, then builds placement records in strict order: first unit records, then special records that carry start positions, then feature records. Each phase allocates a distinct heap block with its own stride and count and fills fields by scanning the text file's enumeration order without sorting. The unit phase interns name strings into a bump area after the unit array, scales world positions by shifting left 16 to 16.16 fixed point, converts facing angle with a fixed-point magic multiply that truncates toward zero, packs the owning player byte with a zero-to-one fixup, and packs flag bits for immunity, mission-critical, AI ignore, and group membership. The special phase stores a type that marks start positions together with a numeric suffix parsed from the name, and the feature phase stores a name buffer with coordinates that are cleared when negative. No other heap writer for those three counts and bases is located within the bounded search. [P0-04]

### Schema and start-position selection — Established [P0-04] [P0-05]

Schema choice precedes placement record instantiation so all peers agree on the same schema. Campaign mode tries difficulty literals in a difficulty-dependent permutation: difficulty zero tries Easy, Medium, Hard; one tries Medium, Easy, Hard; two tries Hard, Medium, Easy; any other difficulty value fails. Skirmish and multiplayer modes try Network 1 through Network 4 in order, counting per-schema start-position specials by scanning for the StartPos prefix. A candidate is accepted when its start-position count equals the counted player count, or the counted player count is zero (the candidate still needs at least one StartPos), or — while no exact match has been found — this candidate's count is the largest seen so far; the last accepted schema name is copied out and fed to the placement builder (after an exact match only later equal-count candidates overwrite it; in the fallback only a strictly larger count does). The counting of lobby players uses non-zero slot occupancy for skirmish and a distinct closed-value sentinel for the multiplayer path, both dense when the lobby is densely packed but counted as last occupied index plus one. [P0-04]

Start-position eligibility is established as three conjuncts: the ten fixed player slots are scanned in order, a slot participates only when its base value is non-zero, its control value is one, two, or three, and its terminator byte is not the newline sentinel. Special records that fail the StartPos prefix test are ignored for this purpose. [P0-04]

### Randomization for skirmish starts — Established [P0-04]

Two random streams are split. When a per-lobby Location flag is zero, skirmish start positions are shuffled with the CRT stream using a Fisher–Yates walk over a dense list of eligible slot numbers: a gate draw is taken when fewer than three players are eligible, then for each index from one to count minus one a draw is taken with bound index plus one. The bound expansion follows the CRT helper's rule of building a mask from 15-bit chunks until the mask covers the bound, then taking the remainder; for the small bounds that occur with ten slots this is a single draw per iteration. The resulting permutation is then assigned in slot order through a helper that stamps each logical slot with a start-position index, overwriting earlier random interior coordinates when a matching StartPos exists and otherwise retaining the random fallback. When the Location flag is non-zero the assignment is identity with no draws. [P0-04]

Commander fallback placement for eligible slots draws from the simulation stream: two draws per eligible slot for X and Z interior jitter, each bounded by map dimension in cells minus 160, then offset by 80 cells and scaled to 16.16 fixed point. When the bound is zero or negative the helper returns zero without advancing the stream, so tiny maps produce no jitter draws and the position collapses to the 80-cell margin. If a matching StartPos is found by numeric suffix lookup, its stored short coordinates are shifted to fixed point and overwrite the jitter; otherwise the jitter is kept. Missing or extra StartPos entries are handled gracefully: the lookup scans for the requested suffix and, when not found, leaves the jitter untouched and can emit a diagnostic without crashing; surplus positions beyond the player count are simply unused. [P0-04]

### Unit creation and InitialMission timing — Established [P0-04] [P0-06]

Mission-unit creation during battle entry uses a two-pass sparse array. The loader allocates a created array sized by the unit count and zeroes it. Pass one walks the unit records in placement order from zero upward: it validates the unit type name exists, adjusts the authored player number from one-based to zero-based with a zero-to-one fixup, checks the same eligibility predicate used for start positions and emits a diagnostic for invalid player numbers but still proceeds to a position fixup helper and the normal unit allocator. The allocator scans for the lowest free pool slot in the owning player's slice and can fail; on failure the entry stays null and no unit is created. Successful creation copies the immunity high bit into a runtime status bit, scales health by percentage, and copies the authored **facing angle** into the unit's heading word (the earlier "copies build priority" is retracted — build priority is parsed but never copied or read; the placement record's angle word is the copy source). Pass two walks the same order again and invokes the InitialMission interpreter only when the record carries a non-null script string and the corresponding created entry is non-null; the interpreter tokenizes the string and queues orders. A per-record creation countdown field is parsed but has no reader in the image; no delayed queue, cargo loop, or separate attachment pass exists beyond the immediate attach verb. Recursive reconstruction for linked or carried units uses the same allocator path for saves but not for fresh mission spawns. [P0-04] [P0-06] [lane 08 facing angle]

Timing is fixed: the mission loader's common tail runs, then for multiplayer a barrier pumps network state and sleeps fifty milliseconds until peers arrive, then start-position assignment stamps slots, then commanders are created with the jitter described above and resources are granted as floating-point metal and energy, then camera focus is chosen, then the sparse two-pass spawner runs, then visibility and mapping are rebuilt, then the first authoritative tick runs. The InitialMission strings are therefore interpreted after every mission unit exists at its fixed-point position but before any creation script, movement, or visibility publication for that tick, and they never take a tick of their own. BetweenMissions handling for saves uses a bank account named Summary that carries a BetweenMissions flag; **the polarity is settled by the battle-entry save-blob gate: when the flag is absent (the in-battle marker) the loader runs the battle restoration dispatcher and skips the fresh spawner; when the flag is present the loader skips battle restoration and runs the fresh spawner — the campaign continuation rebuilds the mission from the authored mission file.** The earlier contradictory phrasing (flag-present → restore player and feature state from the bank) is retracted: a BetweenMissions save contains only the Summary account, so the flag-present route could not restore a battle; the gate's two branches verify this. [P0-04] [P0-05] [lane 08 BetweenMissions polarity]

### Feature and terrain convergence — Established [P0-04] [P0-05]

Terrain-provided and mission-provided feature records converge on the same feature stamping service during the loading worker. Deterministic load order matters because occupancy, successor state, geothermal registration, and save identifiers depend on it. The order within the worker is units first, then specials, then features, and the two-pass unit spawner preserves placement order rather than pool order when allocation gaps occur. [P0-04]

### Placement and start barrier — Established [P0-04]

Multiplayer initialization enters a barrier after content and player state are prepared. The user interface reports that it is waiting for other players. The loop pumps network state and sleeps for fifty milliseconds between checks. When all required peers reach the barrier, the executable reports synchronization complete and allows authoritative ticks. The normal simulation tick is not driven by this sleep; it is confined to lobby, placement, and barrier behavior. The same barrier is not used for single-player campaign skirmish entry. [P0-04]


## Victory and defeat triggers

Conditions are authored as text and matched against a fixed vocabulary of
condition names. A condition that takes arguments parses them from the rest of
the line with a scan format, so the authored syntax is the condition name
followed by comma-separated arguments.

Two argument formats exist. `<name>,<integer>` takes an alphabetic unit-type
token and one integer. `<name>,<integer>,<integer>,<integer>` takes a
unit-type token and three integers. The literal token **`ANYTYPE`** is
accepted wherever a unit type is expected and means any type; it is recognized
by the boundary conditions.

### Victory trigger types

| Authored name | Arguments |
|---|---|
| `KillEnemyCommander` | none |
| `DestroyAllUnits` | none |
| `KillAllMobileUnits` | none |
| `BuildUnitType` | type, count |
| `CaptureUnitType` | type, count |
| `KillAllOfType` | type, count |
| `KillUnitType` | type, count |
| `MoveUnitToRadius` | type, and three integers |
| `UnitTypePassesX` | type or `ANYTYPE`, boundary |
| `UnitTypePassesZ` | type or `ANYTYPE`, boundary |
| `VictoryTimerRunsOut` | none |

### Defeat trigger types

| Authored name | Arguments |
|---|---|
| `CommanderKilled` | none |
| `AllUnitsKilled` | none |
| `AllUnitsKilledOfType` | type |
| `UnitTypeKilled` | type, count |
| `DeathTimerRunsOut` | none |
| `AnyUnitPassesX` | boundary |
| `AnyUnitPassesZ` | boundary |

### Default triggers

If the mission creates no victory condition, the engine inserts a default
destroy-all-units condition. If it creates no defeat condition, it inserts a
default all-units-killed condition.

**Configured lobby skirmish ownership — Supported inference; exact retail
dispatch distinction Unknown.** The previous text treated every type-2 mission
object as owning and polling these injected queues. That conflated a direct OTA
type-2 session with a skirmish created from populated lobby configuration.
Retail's save graph persists trigger queues only for campaign, while a skirmish
save carries the lobby rule words instead; stock network OTAs may omit end keys,
and one also authors `DestroyAllUnits`, whose established immediate-satisfaction
quirk would end an ordinary lobby match on its first countdown if the injected
or authored queue were authoritative. Nanolathe therefore treats a populated
lobby configuration as owning its end condition and does not poll its OTA
trigger queues; direct OTA type-2 sessions retain the established type-2 poll
behavior below. The exact executable branch that distinguishes those two
construction paths has not yet been isolated.

### Evaluation

**Architecture — poll versus notification — Established.** Triggers are vtable objects, not type-byte structs [08 "Trigger object"]. Each of the eighteen conditions carries an eight-slot table — shared destructor/helper slots plus six named behavioural slots: a tick poll, a unit-died notification, a capture/transfer notification, a created notification (present but unused by any shipped condition), and save/load [08 "Evaluation"]. The tick site calls only the poll slot; the notification slots are driven by the corresponding gameplay events. A poll therefore never consumes a kill or capture — the countdown in `KillUnitType` advances only from the unit-died slot [08 "Evaluation"].

**Established — poll-time scans (mutate only Completed; already-completed stays set).**

- `DestroyAllUnits` — **the quirk is the contract.** Its poll reads a `u16` counter whose only reference in the image is that read; nothing ever writes it, so it holds its initial zero and the condition is satisfied from the first poll [08 "Evaluation"]. Shipped missions rely on this: at least one campaign mission uses `DestroyAllUnits` as an AND-term beside a real `BuildUnitType` condition, where a live scan would never let the mission complete. The injected default victory inherits the same quirk and therefore resolves.

- `KillAllMobileUnits` — succeeds when no live mobile unit (`CanMove`) belonging to the enemy owner remains [08 "Evaluation"].

- `BuildUnitType` — succeeds when the local player owns at least the authored count of **completed** units of the named type (`Remaining == 0`) [08 "Evaluation"]. A want below one is treated as one; the scan walks live units in pool order and counts only `Owner == LocalOwner` with matching type (`ANYTYPE` bypasses the name compare). Enemy-owned units of the same type do not count.

- `KillAllOfType` vs `AllUnitsKilledOfType` vs `AllUnitsKilled` — annihilation checks. `KillAllOfType` is a victory term and gates on the enemy owner; `AllUnitsKilledOfType` is its defeat counterpart and accepts any owner; `AllUnitsKilled` is any type but gates on the local owner and succeeds when the local player has no live units left [08 "Evaluation"]. `ANYTYPE` bypasses the name compare where a type slot exists.

- `KillEnemyCommander` / `CommanderKilled` — absence scans. Victory gates on the enemy owner, defeat on the local owner [08 "Evaluation"]. **Established:** retail resolves commander identity through the `SIDEDATA` commander-name table, not through the definition's `Commander` flag [08 "Evaluation"]. The two sources agree for stock content; the table is the authority. An implementation that tests the flag is observably correct on stock data but diverges on synthetic sides that rename the commander.

- Boundary conditions (`UnitTypePassesX`/`Z` and `AnyUnitPassesX`/`Z`) — each carries a single integer threshold stored after an arithmetic `>>4` of the authored value [08 "Evaluation"]. The poll compares the signed world coordinate (unit `X` for `…PassesX`, `Z` for `…PassesZ`, after the same `>>4`) against the stored threshold and is satisfied when `abs(coord - threshold) < 3`, i.e. a ±2-world-unit tolerance [08 "Evaluation"]. The type-gated variants compare `UnitName` case-insensitively against the authored type and `ANYTYPE` (empty stored name) bypasses the compare.

- `MoveUnitToRadius` — the largest record (64 bytes) carries the authored X, a sentinel value `0x12345678`, the authored Z, and radius as `authored <<16` (16.16 fixed) [08 "Trigger object"] [08 "Evaluation"]. Its poll optionally clamps the authored centre when the sentinel is present (map-edge and terrain-height snap, writing `X<<16` / `Z<<16` back), then scans the world partition [08 "Evaluation"]. Distance is planar **X/Z Euclidean squared** (`dx*dx + dz*dz <= r*r` with the 64-bit product reduced to its high 32 bits), Y is ignored, tile bounds are `(centre ± r) >>17` clamped to map tile counts, and iteration follows the world partition's unit-list links. Type gating is `ANYTYPE`-aware as above. **Supported inference made Established:** earlier speculation about an axis-swapped or 3-D metric is closed — subtraction uses the unit's world X and world Z fields [08 "Evaluation"].

- Timer triggers (`VictoryTimerRunsOut` / `DeathTimerRunsOut`) — the builder stores `seconds × 30` ticks with no saturation (wrap preserved) [08 "Trigger object"] [08 "Evaluation"]. The poll compares the authoritative global tick against the stored deadline as **unsigned** `globalTick >= deadline` (a carry-based unsigned comparison, not a signed one) [08 "Evaluation"]. Comparison is `>=`, not `>`, and a zero-second deadline is therefore satisfied on the first poll. The `>=` and unsigned shape are Established; the wrap without clamp is Established.

Completion sets the trigger's `Completed` flag and, if `Celebrated` is still clear, raises the localized "Victory Condition" notification and sets `Celebrated`; the exact presentation channel (message/sound) is not decomposed beyond that invocation [08 "Evaluation"].

**Established — notification-driven countdowns (do nothing on poll).**

- `KillUnitType` (victory) and `UnitTypeKilled` (defeat) share a countdown stored in `Args[0]`. Each qualifying **unit-died** notification whose subject type matches the authored type decrements the count and completes when `<=0` [08 "Evaluation"]. `KillUnitType` counts only enemy-owner losses; `UnitTypeKilled` accepts any owner [08 "Evaluation"]. Poll returns false until notified; polling never advances the count.

- `CaptureUnitType` — same countdown but driven by the **capture/transfer** slot, type-gated, completing at `<=0` [08 "Evaluation"].

The created notification slot is present in every vtable but unused by any shipped condition [08 "Evaluation"].

**The tick site — Established for mission-owned queues; lobby distinction is a Supported inference.** The victory and defeat queues are polled from the per-player tick phase, **in the LOCAL player's slice only**, on that slot's own **once-per-30-tick cadence** (`globalTick >= dueTick` then `dueTick += 30` — the same next-due-plus-30 shape the economy settlement deadline uses) [08 "Evaluation"]. The earlier "campaign type 1 only / types 2 and 3 never poll" reading remains retracted: the poll block handles direct mission types 1, 2, and 3, with per-type precedence (below). The later wording that this necessarily included a populated lobby skirmish was too broad: lobby construction owns separate rule words and omits trigger queues from saves, as described under Default triggers. Poll order is the victory array then the defeat array, in builder order.

**Combination and precedence — Established, per type.** Victory is an **AND** across its queue — every victory trigger must report `Completed`. Defeat is an **OR** — any single defeat trigger ends the mission. For campaign (type 1), **victory is evaluated first**, so a tick on which both would fire resolves as a victory [08 "Evaluation"]. For skirmish and multiplayer (types 2/3), the **defeat branch is evaluated before the victory poll** and the defeat queue is polled only when the local side's commander marker (a runtime bit on the local player's side definition) is clear — the commander-dead test — so a simultaneous commander death and victory resolves as defeat; the victory queue is polled unconditionally. Completion arms a shared end-of-mission countdown at **four**, which then decrements roughly once per second (once per local 30-tick due: `4 → -1` over five invocations, ~150 ticks) before the end-latch word is written; the latch distinguishes "ending" (bit 2, value `0x04`), "won" (`0x10` or `0x20`) and "lost" (`0x40`, clearing `0x10`) and the lose path clears the win bit it would otherwise share [08 "Evaluation"]. The `~1/sec` rate is the poll cadence, not a separate timer. **A second, per-tick countdown site exists for multiplayer only (mission type 3, commander-death rule not value two, no active player remaining): it decrements the same shared countdown every tick (latch in about five ticks).** The earlier "no-human campaign path decrements per-tick" reading is retracted — the fast site is gated on the multiplayer type; a humanless campaign keeps the 30-tick cadence. The skirmish/multiplayer commander-death rule is a lobby value, not a mission key: value two respawns a new commander (valid-placement search with up to 9999 trials, two simulation draws per trial, plus terrain and lava gates; then resources and visibility rebuild), any other value ends the game through the watch-mode path ("You're out! Continue watching?").

**Owner gating is a player-index compare, not an alliance test — Established.** Each poll-time scan that walks live units compares the unit's player index against `LocalOwner` or `EnemyOwner` as above; no ally-group merge is performed [08 "Evaluation"]. `UnitTypeKilled` and `AllUnitsKilledOfType` explicitly accept any owner [08 "Evaluation"].

**Record shapes, the eighteen-entry vtable map, these evaluator bodies, the tick site, and the combination rules are established.** The presentation sequence between latch write and session teardown is closed in the Session end section (post-battle handler: network drain, multiplayer frame copy, music stop, timed display, CD check, campaign progress and end-of-mission screen, outro movie, score/statistics screen, front-end return). Whether any non-stock `SIDEDATA` divergence would expose the commander-flag vs table distinction (stock content agrees, but a synthetic side definition could diverge — retained as bounded residual, not a poll-vs-notification gap). The timer comparison beyond `>=` unsigned and the `MoveUnitToRadius` axis pair beyond X/Z planar are now closed; do not retain them as Unknown.

## Skirmish configuration

Skirmish preferences include:

- number of players;
- map;
- per-slot controller type;
- side;
- color;
- ally group;
- starting metal and energy;
- difficulty;
- starting location;
- commander-death rule;
- mapping rule;
- line-of-sight enable and type.

The installed scalar lobby defaults are difficulty 1 (Medium), location 1
(pre-determined commander positions), commander-death 1 (commander death ends
the game), mapping 1 (terrain is blacked out until explored), line-of-sight 1,
and line-of-sight type 1 (terrain elevations affect LOS). Difficulty, location,
commander death, and mapping each toggle/cycle through their ordinary menu
alternatives. The commander-death rule is a lobby value carried into the
game-mode word and consumed by the per-player phase, not a mission key: value
one ends the game through the watch-mode path when the local commander dies,
and value two respawns a new commander (a valid-placement search with up to
9999 trials of two simulation draws each, plus terrain and lava gates, then
metal/energy grants and a visibility rebuild). The lobby UI identifies value
zero as continuing after commander destruction. **Supported inference:** for
Nanolathe's two offered menu choices, value zero keeps a team active while any
live, non-dying unit remains (including a building), while value one keeps it
active only while a commander remains. This matches the observed
commander-versus-all-units setup semantics, but the exact retail value-zero
all-live-unit sweep and its alliance aggregation have not been isolated and
remain **Unknown**. The rule word also selects the
multiplayer no-active-player fast countdown site. The retail `LineOfSight`
callback is a three-state control: it
cycles from elevation-aware LOS (`LineOfSight=1`, `LineOfSightType=1`), to
elevation-agnostic LOS (`1,0`), to all mapped terrain visible
(`LineOfSight=0`, `LineOfSightType=1`), then back to the default. These defaults
are separate from the per-slot defaults below.

For a missing per-slot value, the retail lobby supplies controller `0`, ally
group `5`, metal `1000`, energy `1000`, color equal to the slot index, and side
equal to the slot index modulo two. The frontend row state then distinguishes
`Open` (`0`), `Player` (`1`), and `Computer` (`2`) before converting those
values into the session's player-controller representation at battle entry.

The retail Energy and Metal lobby buttons adjust the selected slot by 500.
Decrementing floors at 200; incrementing caps at 10000, with the preserved
callback quirk that an increment from 200 would produce 700 and is immediately
rewritten to 500. These are setup-time values, before battle-entry resources
are granted.

The front end builds ten-slot player state from these values and validates it
against the selected map/schema. Computer-controlled slots use distinct player
state values that later tick code recognizes.

The stock skirmish screen does not contain a custom opponent-count selector.
The persisted `NumSkirmishPlayers` setting determines how many `Player%d`
rows the runtime appends to `SKIRMISH.GUI`; the missing-value default is four
players and the executable's registry validation stores an explicitly supplied
value without clamping. Each visible row is laid out from the active row count,
with a vertical step of `200 / count` and a first-row Y coordinate of
`(180 - (count - 1) * step) / 2 + 79`. The authored screen supplies the
round/setup controls (`StartLocation`, `CommanderDeath`, `Mapping`,
`LineOfSight`, and staged `Difficulty`); the dynamic row builder supplies
controller, side, color, alliance, metal, and energy controls.

The setup callbacks use the following retail validation order. A selected map
must resolve to both its OTA metadata and terrain; at least one visible row
must be `Player` and at least one must be `Computer`; the number of players
requested by the lobby must fit the selected map's network schema; and the
live rows must not all share one non-sentinel alliance group. The alliance
group value `5` is the unassigned sentinel: if every live row is `5`, the
same-group error is not raised. Open rows are ignored after the first live
non-`5` group is found. The retail diagnostic strings are, respectively,
`The terrain for the selected map does not exist.`,
`There must be at least one player and one computer opponent`,
`There are too many players enabled for this map`, and
`All players may not be in the same allied group.`

The start callback's allied-group preflight is intentionally narrower than a
simple "all live rows have the same value" test. It first finds the first live
row whose ally group is not 5. If no such row exists (no live rows, or every
live row is group 5), the check passes. Otherwise open rows are ignored and the
check fails only when another live row has a different group. The color callback
has a similarly observable quirk: it accepts the next logo when that candidate
does not conflict with another live row, but after a conflict its fallback
search scans every configured row, including open rows, from logo 0 upward and
stores -1 when all ten stock logos are present. [07 "Retail closure for the
single-player menu slice"]

## Lobby behavior

The battleroom heartbeat is a front-end state synchronizer, not the strategic
AI. It repeatedly:

- drains and displays chat;
- synchronizes the selected map name;
- updates the ten player rows;
- shows or hides unused and blocked slots;
- displays side, ally, team icon, starting resources, ping, and memory;
- propagates ready state;
- decides which controls the local player may edit;
- enables start only when lobby conditions are satisfied;
- publishes local changes to peers.

Human, computer, open, and blocked slot states have different editing and
readiness rules. The exact semantic name of every numeric state and every host
privilege bit remains incomplete.

## Computer-controlled players

### Established AI-facing data and rooted planner [P0-01] [P0-02] [P0-03]

The executable reads these AI-related definition and mission values:

- per-unit `ai_weight` text in a dedicated definition field (64-byte capacity), parsed by the weight loader and applied through the profile system;
- per-unit `ai_limit` text in a separate definition field — no semantic reader is found after the parse, so it must not be wired to limits; the functioning `limit` token comes from the profile file, not this field;
- mission `aiprofile` string via a mission resource slot that loads `ai\<profile>.txt` with fallback to `ai\default.txt`;
- mission placement fields for AI ignore, AI priority-target, build priority, and initial group — parsed at mission load but no transfer or reader is found in the creation path, so they are inert for planning;
- computer difficulty from the registry and setup state that gates profile plans and scales controller-2 economy;
- player control byte that gates manager execution.

The strategic planner is positively rooted and distinct from the scenario unit loader. The earlier analysis that mistook the unit reconstructor for AI is retracted.

#### Strategic state construction and refresh — Established [P0-01]

Each computer-capable player owns a fixed-size strategic state that holds counts, a map center, and two per-type coefficient families. One family is a single-byte per-type weight used only at initialization; the other is a three-byte per-type triple recomputed during play. The two families live in different vectors within the same state object and are not aliases.

Construction builds the state, zeroes the vectors, establishes the center at half-map, ensures per-type capacity, and writes the single-byte vector once: the value starts at zero, adds 40 when a per-definition category flag is clear, and adds 20 when that definition's build-option list is non-empty. The triple vector is zeroed at construction and not written by that initializer.

Refresh runs every 30 ticks. It clears and rebuilds per-type completed counts and the weighted center from live units. The single-byte vector written at construction is never touched again by the recomputation routine. The triple is recomputed only when an outer random gate succeeds and once unconditionally at state creation; otherwise the refresh leaves the triple unchanged. [P0-01]

#### Class-vector recomputation loop and inputs — Established [P0-01]

When the gate opens, the recomputation routine walks the unit definition catalog in strict ascending type order, skipping the zero sentinel, one definition per iteration. No build-option list is read inside this routine; weapon data is the only per-type list it iterates (exactly three weapon table slots, each checked for an active flag before contributing).

Per-definition inputs consumed in plain terms are:

- per-definition economy cost fields for metal and energy;
- the extracts-metal flag as a floating-point zero versus non-zero test;
- category and movement-class flag bits that contribute fixed integer addends and select weapon-budget bases;
- footprint and yard-related size flags that contribute small constants and gate multipliers;
- a slope-related field that triples one accumulator when non-negative;
- a weapon-related floating field that can zero one accumulator when combined with a global half-compare;
- the weapon table entries themselves, where active weapons contribute damage divided by 40 plus **range** divided by 100 plus small constants — the TDF identities are closed: the weapon parser stores the `DAMAGE` section's `default` key into the damage word (the /40 read) and the `range` key into the range word (the /100 read); `reloadtime` is stored elsewhere (scaled by thirty) and is not read by this routine. The earlier "reload divided by 100" phrasing is retracted; it is range divided by 100, consistent with the weapons ballistics field assignment;
- a global helper that returns a signed classification value compared against zero;
- per-type completed counts from the strategic state that double or quadruple one accumulator and gate halving from the previously computed single-byte coefficient;
- a player-wide flag that gates halving of one accumulator.

All weapon-slot contributions are bounded by clamps before they are summed with cost-derived terms. [P0-01]

#### Arithmetic and clamping — Established [P0-01]

The routine performs its floating work with the retail x87 pattern: integer addends are loaded, economy costs are multiplied by constants, differences are taken in floating point, and each result is narrowed to 32-bit float at invocation boundaries before the next operation. The constants that appear are zero, minus one hundredth, minus two thousandths, thirty, minus two and a half thousandths, five, one hundred, minus two hundredths, and small integer addends such as one, ten, eleven, twenty, twenty-one, twenty-five, thirty, forty, fifty and one hundred. Every floating-to-integer conversion truncates toward zero, matching the retail helper's behavior, and the final per-type results are clamped to minus one hundred to plus one hundred before they are stored as signed bytes. No 80-bit retention crosses a helper invocation; the store to float32 is the truncation boundary. [P0-01]

#### Random gate — Established [P0-01]

The recomputation routine itself draws no random numbers. Its outer dispatcher draws once per 30-tick window with bound 30; only when that draw is zero does it invoke the recomputation. The single unconditional invocation at construction draws nothing. [P0-01]

#### Strategy manager and its task graph — Established [P0-02]

A per-player strategy manager of fixed size is allocated for every participation-eligible slot except the live remote path. It holds a countdown that triggers a classification sweep every 30 eligible entries, a throttle deadline written by the unit-loss path, and ten task slots. Nine slots are active and one slot remains intentionally empty with a null task that never runs.

The nine active tasks are:

- eco and queue management that handles activatable building toggles and builder queue insertion — rescheduled at current tick plus 30;
- construction and positioning that selects a build candidate, finds placement, issues a build order, and then repositions builders when at least five builders are present — rescheduled at current tick plus 90;
- two attack-wave tasks that share the same code but hold distinct distance thresholds and count bounds — each rescheduled at current tick plus 300; the underlying wave merge is three-phase and strict: an empty own group first takes the peer's first member, then the own group sheds its farthest member to the peer while distance squared is at least threshold times the task's group count, then the peer's members within distance squared strictly below threshold times the task's count are collected and transferred into the own group — and the peer is the paired regroup task, not the other wave — thresholds twenty thousand and fifty thousand, minimum three members and maximum six per wave; the full merge order is under R-P0-04 §5 below. Audit: the earlier phrasing described the merge loosely as moving members between wave groups when distance squared exceeds threshold times count; the caller census established the two-phase order with an inclusive first comparison and a strict second, which is now the contract;
- two regroup tasks paired with the waves — each rescheduled at current tick plus 150 and moving the task's group toward the peer wave's centroid;
- an explore and gather task — rescheduled at current tick plus 30 plus a random value below 900;
- a random-walk rally task that integrates a drifting target and validates exploration — rescheduled at current tick plus 30 plus a random value below 150.

Deadline arithmetic is unsigned tick plus offset; due is defined as deadline at or before the global tick. [P0-02]

#### Dispatch gates and order sinks — Established [P0-02]

Per-tick dispatch iterates the ten player slots in order and calls a manager tick and a strategic refresh for each eligible slot (non-empty slot whose control value is one, two, or three and whose sentinel is not the closed value). Inside the manager tick, work proceeds only when the manager's own control value equals the computer policy value. When that outer gate fails, the manager still runs weapon maintenance but no virtual tasks.

When the gate passes, the manager decrements its 30-countdown; when it reaches zero it resets to 30 and runs the classification sweep over the eligible player range. It then scans the ten task slots in order and invokes any task whose deadline has arrived through its virtual table. After the virtual sweep it runs weapon maintenance. A separate alternate dispatcher with the same countdown but without weapon maintenance exists and is not used by the live tick. [P0-02]

All order submission from manager tasks uses the ordinary order service. Build orders enter as a build command with a world position, queue modifier one, and type identity; positioning uses move and patrol-like commands; waves use a formation helper that can issue attack or move orders against a unit target or a centroid; the eco path can enqueue a build-option choice into a builder queue and can toggle an activatable building's active state; the regroup and explore tasks issue move-like orders to centroids or random map targets; the rally task issues a mix of stockpile and formation orders after a guard check. There is no privileged mutation path that writes economy or unit state outside those ordinary submissions. [P0-02]

#### Eco toggle and group-vector population — Established with direct writer census [P0-02] [R-P0-04]

The eco task scans its own group vector and examines only completed units, gated on the building-class and live status bits with the under-construction bit clear. It has **two** branches, selected by whether the definition's authored makes-metal byte is non-zero.

**Makes-metal branch.** For activatable buildings it compares twice the stored metal income against current energy: when metal is at most half of energy it disables; otherwise when net energy is at or below zero it leaves the unit as is; otherwise it draws once with bound five and enables the unit only on a non-zero result. The two argument forms disable and enable correspond to those two invocation sites; the semantic name metal-maker on/off is supported inference, but the argument values, the eighty percent gate, and the metal-energy compare are established.

**Factory-queue branch — Established (2026-08-26).** A building whose makes-metal byte is zero and whose definition carries a non-zero build-option count is a factory, and this task is what queues its products. It skips the unit when its primary order queue is non-empty, so exactly one product is queued at a time and the next is queued only after the queue drains. Otherwise it runs the ordinary cumulative-reservoir selection over the builder's build options — the same selection the construction task uses — and submits one build of the selected product.

This branch is why the construction task never needs to see a factory: the classifier sends every building to this record and only mobile builders to the construction record. An implementation that queues factory products from the construction task is relying on the classifier misfiling buildings, and will stop working as soon as the building-class bit is correct.

The nine manager task group vectors are initialized empty, but the extended
whole-image static census now locates the specialized direct
manager-group writer. The manager's classifier invokes it every 30-countdown
pass for ungrouped units that carry runtime bit `0x20`, assigning categories 1
(resource), 3 (regroup A), 4 (construction), 5 (null), 7 (regroup B), or 8
(explore) in ascending unit-pool order. It does not assign wave A/B or rally
(categories 2, 6, and 9). The same writer is also called by wave merge, unit
load, control-group assignment, unit initialization, and death removal; it
appends to the destination vector, removes from the source by replacement with
the last element, and has no gameplay member cap. See R-P0-04 §3 and §4 below for the complete
writer and direct-store census. Transport, naval, air, or special scouting
tasks with distinct tables are not found among the six unique virtual tables
that cover the nine slots. The empty slot's null task is intentionally inert.
[P0-02] [R-P0-04]

The earlier helper-only callback census (`6a3f9c5`) remains valid for its
scope: it found no additional lifecycle admission callback that seeds these
vectors through the generic insertion helper or the previously searched
callback routes. It did not enumerate direct xrefs to the specialized
direct manager-group writer. The new positive-static result therefore supersedes only
the old conclusion about the direct manager writer; it does not invent a
creation/completion/capture callback beyond the separately located init,
load, control-group, wave-transfer, and death invocation sites.

The classifier admission bit is the `uint32` runtime status word on the
unit's runtime record, not an authored UnitDef field. The common
allocator initializer sets bit `0x20`; the save-load path can restore
it from the packed saved status word; and the death path clears it with the
low-byte `0xcf` mask. InitialMission clears it after queuing at least one
order. The exact `MakeSelectable` handler clears `0x8000`, sets
`0x20`, stores the status word, and returns code 5; InitialMission's `s` verb
and postlude queue this order. The separate `Selectable` option callback
scans active units and ORs the same bit. Selection bulk-clear paths
also mask it. No direct capture, activation, or
factory-completion writer was found; completion sets the distinct status bit
`0x2000`. These transitions are positive-static, while the absence of an
additional authored/capture/completion writer is bounded-negative. [R-P0-04
"Runtime eligibility bit lifecycle"]

#### R-P0-04 §1 — Result: no per-tick group producer; two vector families — Established [R-P0-04]

The retail AI has two distinct group-vector families, and neither is produced by a per-tick combat-capability scan:

1. **Strategic state vectors** — produced during the 30-tick strategic refresh;
2. **Manager task vectors** — the inputs consumed by the construction, eco/queue, wave, regroup, explore, and rally tasks.

The manager's nine task vectors are allocated empty, but that is only their initial state: the extended static census locates the direct group-record writer and the manager's classifier, which together give the task vectors reachable producers. A production-time scan that assigns every apparently combat-capable unit to wave, explore, rally, and regroup slices is not a retail producer and must not be retained as authoritative AI behavior (see R-P0-04 §5).

#### R-P0-04 §2 — Manager task slots and group-record identity — Established [R-P0-04]

The manager allocates nine task objects in ten fixed slots; the tenth slot is an intentional null task that never runs. The slot order is load-bearing: resource/activity and builder queue, attack wave A, regroup A, construction/positioning, null, attack wave B, regroup B, explore/gather, random-walk rally. Each task points at its own player group record — a begin/end/capacity vector of unit pointers — and the dispatcher runs the task slots in ascending slot order when the computer-controller gate is active and the task's deadline is at or before the global tick. Deadline execution does not imply the task's vector is non-empty. Group record zero is the ungrouped sentinel and is not one of the nine task records. Task deadlines are listed under Strategy manager and its task graph above. [08 "Strategy manager and its task graph"; 08 "Dispatch gates and order sinks"]

#### R-P0-04 §3 — Located producers and transfer order — Established [R-P0-04]

The producers below are the reachable set; their order of application matters for save and simulation determinism.

##### Strategic refresh vectors

The strategic state owns three separate vectors, each rebuilt on the 30-tick refresh by clearing it and scanning the live unit pool in ascending pool order. The eligibility predicates differ per vector; they are not tactical-group population. Creation, completion, death, and capture affect these vectors only through the next refresh's live-pool scan — no creation hook inserts directly. [08 "Strategic state construction and refresh"]

##### Wave merge

**Correction (2026-08-26).** The previous text said the merge "operates only on the two wave groups passed by the task instance" and that it "can operate only after a wave vector has members". Both statements were wrong, and together they made the attack waves look unreachable. A re-derivation of the manager constructor, the wave task and the merge establishes that (a) the merge's peer is the **paired regroup task**, not the other wave, and (b) the merge **seeds an empty own group from that peer**. The classifier populates the regroup records, so the waves do have a reachable producer. The corrected contract follows.

The attack-wave task calls the wave merge helper before target selection, passing the **peer slot index stored on the task instance**. Slot index and group-record index are the same number throughout the manager, so the peer slot index is also the peer group number. The authored pairings are wave A → regroup A and wave B → regroup B; the regroup tasks point back at their wave in the same way. The merge is therefore a transfer path between one wave record and its own regroup record, never between the two waves.

The recovered order:

1. resolve the peer task from the passed slot index; if it is the calling task itself, return;
2. **bootstrap**: if the own group is empty, return when the peer group is also empty; otherwise transfer the peer group's **first** member into the own group through the ordinary group-transfer helper, and continue with the steps below;
3. compute the own group's centroid as the integer mean of its members' signed world coordinate words, truncating toward zero;
4. while the own group holds more than one member, find its farthest member from that centroid and, while `distanceSquared >= threshold * ownGroupCount`, transfer that member to the peer group through the same helper;
5. after each such transfer, subtract the departed member's coordinates from the running sums and recompute the centroid against the new count;
6. scan the peer group and collect members satisfying `distanceSquared < threshold * ownGroupCount` into a temporary vector, in peer-vector order; and
7. transfer the collected members into the own group through the same transfer path, in that same order.

The farthest-member comparison is inclusive (`>=`) and the peer-collection comparison is strict (`<`). The farthest search keeps the **first** maximum on ties, which makes the vector order the tie-break. Wave A uses threshold 20,000 and wave B uses 50,000; both carry minimum three and maximum six members. No random draw is taken by the merge.

The bootstrap is the single step that lets a wave start from nothing, and it moves exactly one member per merge call — so a wave fills from its regroup peer over successive 300-tick task runs rather than all at once. [08 "Strategy manager and its task graph"]

##### The direct manager-group writer

The direct group-record writer is a mutation of a unit's group membership, not a generic strategic-vector helper. It reads the unit's stored group record, removes the unit pointer from that record (replace-with-last and decrement the end), and — when the new group differs from the remove sentinel — appends the unit pointer to the new group's record and stores the new group number on the unit. The append path reserves and reallocates the four-byte pointer vector when capacity is exhausted; no gameplay member cap is tested. The remove sentinel removes without appending; group record zero is the ungrouped sentinel and is not one of the nine task records.

The whole-image caller census finds exactly six caller classes: the wave merge (peer and current task records, transferring existing members); the manager's classifier (six fixed destinations); unit allocation and initialization (places a newly initialized unit in the ungrouped record); death teardown (remove sentinel, so a dying unit leaves its record); scenario and save unit load (restores the saved or mission group in load order); and the control-group assignment sweep (inserts selected units and clears matching old members). No capture-specific caller exists in the set; a captured unit may keep its stored group until one of the established writers or cleanup paths acts.

##### Classifier eligibility, destinations, and order

The classifier runs on the manager's 30-countdown cadence and scans the current player's unit slice in ascending pool order. For each unit it first requires the allocator-initialized runtime status bit (see the Eco toggle and group-vector population section above) and the ungrouped record, then emits at most one assignment per unit on that pass through the direct writer, in this exact branch order:

| Predicate, in order | Destination task |
|---|---|
| first high status bit set, second high status bit clear | resource/activity |
| first high status bit set, second high status bit set | null task |
| otherwise, definition builder flag set | construction |
| otherwise, definition can-fly flag set | explore/gather |
| otherwise, definition max-slope field signed greater than zero | regroup B |
| otherwise, second high status bit set | regroup A |
| otherwise | stays ungrouped |

The classifier never assigns wave A, wave B, or rally; it draws no random numbers; its insertion order is the ascending unit-pool traversal, and the ungrouped gate prevents duplicate append on later passes.

**Correction (2026-08-26): the two high status bits are no longer opaque.** They are set once by the common allocator initializer, from the definition, and by nothing else — a whole-image scan of the runtime status word finds exactly one write site for each, so both are stable for the unit's lifetime.

- The **first** high bit is the **building-class** bit. It is set when the definition's authored `bmcode` byte is zero. That same authored byte is what decides whether a YardMap is parsed for the definition at all, so bmcode zero is the authored meaning of "building" and bmcode one is "mobile". This is also the factory production handler's building-class gate — see 05 "Factory production lifecycle". It is not a yard-map, footprint, or immobility heuristic.
- The **second** high bit is the **armed** bit. The definition's boolean flag word carries a derived bit that is set unless all three of the definition's resolved weapon slots are empty, and the initializer copies that derived bit into the runtime status word. So the second high bit means "this unit resolved at least one weapon".

Reading the branch table with those names: an unarmed building goes to resource/activity, an armed building goes to the inert null record, and among mobile units an ordinary armed ground unit — not a builder, not a flyer, max-slope not positive — goes to regroup A, which is the wave-A merge's peer. That is the path by which produced combat units reach an attack wave. [08 "Eco toggle and group-vector population"; 08 "Wave merge"]

##### Runtime eligibility bit lifecycle

The classifier's eligibility bit is a runtime instance-status bit, not an authored definition flag. Its lifecycle — allocator initialization, InitialMission clear, save restore, death and selection clear, MakeSelectable and Selectable writes, and the bounded absence of capture, activation, and factory-completion writers — is fully specified in the Eco toggle and group-vector population section above; that text is the home for this finding. No authored UnitDef field should be added to represent the bit, and it must not be aliased with the script-owned in-build-stance byte.

#### R-P0-04 §4 — Bounded writer census — Established [R-P0-04]

The generic vector insertion helper has exactly two caller classes: the strategic refresh (its three lists, inserting eligible live units in pool order) and the wave merge (temporary peer collection and transfer). The earlier helper-only census was incomplete because the direct group writer is a specialized writer rather than a path through the generic insertion helper. The constructor and the record allocator still initialize all task vectors empty, but the caller set above proves reachable population and removal after initialization. No other direct store, copy, or assignment path with a manager record alias was found in the whole-image search around the manager root, the ten task slots, the task vector fields, the record allocator and free pair, and the generic and direct writer references. The census is therefore positive-static for the direct writer and the classifier's six destinations, and bounded-negative for any additional distinct writer. The two high status bits, previously the only opaque fields of this census, are now named under "Classifier eligibility, destinations, and order" above. [08 "Eco toggle and group-vector population"]

#### R-P0-04 §5 — Current heuristic versus retail contract — Established [R-P0-04]

The production-time group scan performs actions that are not established retail behavior: it scans the whole world each tick, classifies units through movement, weapon, and economy proxies, filters owner, completion, and death within the scan, assigns ungrouped units to wave A, wave B, explore, rally, regroup A, then regroup B in a fixed fallback order, and imposes local caps of six or ten while doing so. The retail evidence establishes instead: the 30-entry manager cadence, the status-bit plus ungrouped gate, the branch order above, and the six classifier destinations. The heuristic's combat-capability predicates, per-tick timing, fallback order, and local caps are unsupported and must not be retained. The narrow producer runs only on the established cadence, appends in unit-pool order, and leaves wave A, wave B, and rally untouched. Wave A and wave B are then supplied by the wave merge's bootstrap and peer-collection steps from their paired regroup records, which the classifier does populate — see the Wave merge subsection above. Rally has no runtime producer at all: its task returns immediately when its own record is empty and calls no transfer helper, so record 9 stays empty unless a save or control-group path fills it.

#### R-P0-04 §6 — Implementation guidance — Established [R-P0-04]

Keep the task-vector records and the task dispatch machinery, since their identity and deadlines are established; initialize the vectors empty, then run the narrow classifier on the manager's 30-entry cadence. Implement the wave merge as a transfer operation between a wave record and its paired regroup record, including the empty-own-group bootstrap, with the recovered comparison strictness and thresholds. Do not pair a wave with the other wave, and do not omit the bootstrap: without it the wave records are unreachable and the computer player never issues an attack order. Keep the direct writer's swap-delete source removal and append destination order for lifecycle and control-group integration. Do not use the strategic refresh vectors as substitutes for tactical groups: they have different records, consumers, and eligibility predicates.

Locked by the group-vector fixtures in internal/ai (groups_test.go, strategic_test.go) and the manager-record-order fixtures in internal/session and internal/save.

```text
TODO(question): Does a distinct writer, separate from the recovered direct-writer caller set, mutate manager task vectors through an indirect alias? The direct writer and the classifier are established; no additional writer is claimed without new static or dynamic evidence.
```

#### Placement root and search helpers — Established [P0-03]

The placement root is called by the construction task after a candidate has been chosen. It first grows a per-player search radius by 160 cells, capped at the larger of map width and height in cells; on successful placement the radius is reset to zero, otherwise the grown value is retained. It then steps an origin toward the strategic center: the vector from the builder to the center is measured with a floating-point square root after loading the 16.16 fixed-point deltas, converted back with truncation toward zero, scaled to 16.16 by shifting the radius, and compared as fixed-point distance. When the distance is zero or at least the scaled radius, the origin is the strategic center itself; otherwise the origin is the builder position plus the center delta scaled by radius over distance using 64-bit fixed-point multiply and divide. This is a fixed-point interpolation, not a normalized floating vector.

Helper selection for extractor candidates is strict and opposite to the earlier inference. When the candidate's extracts-metal flag compares equal to floating zero, the root calls the statistical scatter helper directly without drawing. Otherwise it draws once with bound 255 and calls the exhaustive patch helper when the mission's uniform surface metal value is strictly less than the draw; otherwise it calls the scatter helper. The test is strictly less-than, so equality chooses the scatter path.

The exhaustive patch helper scans a precomputed metal-patch record vector within the search circle whose radius is scaled by four, filters by distance squared, sorts the qualifying patches by distance, and then validates each candidate in sorted order with the footprint blocker and the metal score. The score is the **sum of the per-cell metal bytes across the footprint** (the blocker accumulates each footprint cell's metal byte; the score getter is a trivial read of that accumulator). The exhaustive helper keeps the candidate with the highest accumulated metal sum (strict greater-than; ties keep the earlier sorted entry). It stops early when the distance of the next patch exceeds the best distance found by a slack of 160. The statistical scatter helper attempts up to 30 trials around the origin, quantizing each trial to the map grid and to per-region bounds selected by the candidate's slope sign; each trial draws up to four values (a radius-scaled offset, a direction, and region-cell offsets) and validates with the placement validator and a comparison of the same metal-byte-sum score against a limit computed as surface metal times footprint X times footprint Z times two. The scatter path writes the chosen placement as fixed-point world coordinates derived from the quantized grid, scaling the grid index by a fixed factor that corresponds to half-tile increments.

Water legality is now located: neither placement helper consults the waterline value directly. The placement validator's yard path (which the scatter helper uses with the waterline-check flag) rejects footprint cells whose terrain height byte falls outside the band defined by the candidate's slope fields relative to the waterline; its non-yard path delegates to the footprint blocker with no waterline test. The exhaustive helper therefore has no waterline test at all — a "water-only extractor filter" remains a heuristic, as stated above, but the mechanism behind water legality is the yard path's waterline band.

Failed exhaustive helper does not fall through to the scatter helper; it returns failure for the entire placement attempt. Success requires the yard and occupancy validator to report placeable; missing yard data is not treated as permissive. The scatter helper's limit check is established as the product above, and the exhaustive helper contributes no random draws while the scatter helper contributes only the draws counted per trial. The selector's single draw is the only random draw on the extractor path when the candidate is non-extractor. [P0-03] [lane 08 placement score and water]

RNG sites for AI planning are the outer 30 gate, the cumulative weighted reservoir, the extractor selector with bound 255, the positioning scatter with bounds up to the current radius and 65536, the unit-loss throttle (deadline is current tick plus 30 plus a draw bounded 300), the strategic-state constructor (eight draws at setup, in order: 10, 3, then the two slope-negative region widths, then 20, 3, then the two slope-positive region widths — these seed the region and offset words the scatter helper later reads), the eco toggle with bound five, the explore task (deadline draw bounded 900, plus body draws bounded 2 and the map-dimension fractions), and the rally task (deadline draw bounded 150, drift-seed gate bounded 10, two drift draws bounded 65536, and score-comparison draws bounded by the score values). The wave, regroup, and merge bodies draw nothing. "Any other bound in this package is a bug" is retracted; the throttle, constructor, and task-body draws above complete the inventory. Separately, the session package's skirmish commander-respawn path (commander-death rule value two) draws twice per placement trial (map-width and map-height bounds) with up to 9999 trials. [P0-01] [P0-02] [P0-03] [lane 08 RNG inventory]

#### R-P0-05 §1 — Score inputs and update order — Established [R-P0-05]

The AI candidate score must consume the retail player economy aggregates and the strategic class vectors, not a proxy of current stock plus per-pass produced values. The runtime production and net-production accessors read player-record aggregates that are populated from the ledger's per-unit production and request buckets plus leftover stock; current stock and capacity are separate fields on the same record. The exact bucket arithmetic and settlement order are the economy contract. [05 "Player slot"; 05 "Settlement cadence"] This wording is deliberately aligned with document 05's settlement contract: the aggregates are the settled ledger values, and this doc's "per-pass snapshots" phrasing is not used — a candidate selection never sees the current pass's un-settled production.

The score has two layers: dynamic economy pressure read from the player record at candidate-selection time, and per-definition class coefficients stored in the strategic state and recomputed only on the established cadence. Update order matters: in the per-player tick loop the manager task dispatch runs before the strategic refresh, and the economy ledger runs later in that loop. A manager selection therefore observes the previous settled economy values and the previous class vectors for that tick; refresh and ledger writes become inputs to later ticks (see R-P0-05 §6).

#### R-P0-05 §2 — Player economy record and strategic score fields — Established [R-P0-05]

The player economy fields consumed by the score are: current energy stock, current metal stock, energy capacity, metal capacity, and four aggregates — energy production, energy usage, metal production, and metal usage. The production accessors are not per-pass produced values; the ledger folds per-unit production and request buckets and leftover stock into the aggregates.

The strategic state contributes, per definition type: a three-byte class triple holding signed coefficients for the other, metal, and energy mixes; a completed-owner count; a single-byte coefficient used by the class path and the conditional half-addition; and an initialization-only single-byte vector written once at construction and never recomputed. The state also holds the last 30-tick refresh tick and the placement search radius. The three-byte class vector and the single-byte vectors are distinct arrays; the initialization-only vector is not an alias of the refresh-written families. [08 "Strategic state construction and refresh"]

#### R-P0-05 §3 — Hard gates and economy pressure — Established [R-P0-05]

For each candidate the hard gates run first:

- reject when current energy is strictly below `50.0`;
- reject when current metal is strictly below `25.0`;
- reject when the special mission mode equals `1` and the candidate's definition carries a particular established status bit (the bit's authored semantic name is not closed);
- reject when the candidate's completed count reaches its profile limit — `count < limit` is required, and `-1` means unlimited.

The pressure values then use the player fields:

```text
energyRaw = trunc(max(0, (min(energyCapacity, 1000) - currentEnergy) * 0.125))
metalRaw  = trunc(max(0, (min(metalCapacity,  500) - currentMetal)  * 0.25))
```

Adjustments are applied in this order:

```text
if netEnergy < 1.0: energyRaw += 20
if netMetal  < 1.0: metalRaw  += 20

if energyProduction < 50.0: energyRaw += 100
else if energyProduction < 200.0: energyRaw += 10

if metalProduction < 3.0: metalRaw += 100
else if metalProduction < 5.0: metalRaw += 20
```

All integer conversions truncate toward zero.

#### R-P0-05 §4 — Candidate score and cumulative weighted selection — Established [R-P0-05]

The three-way mix is:

```text
metalMix  = clamp(metalRaw, 0, 100)
energyMix = clamp(energyRaw - metalMix, 0, 100)
otherMix  = max(0, 100 - metalMix - energyMix)
```

For the class triple `(other, metal, energy)` and profile weight `weight`:

```text
score = trunc((other * otherMix
             + metal * metalMix
             + energy * energyMix) * weight / 10000)
```

Scores at or below zero are excluded. The remaining positive scores are selected in authored build-option order by cumulative weighted reservoir selection using one simulation-random draw bounded by the running positive total. The builder's own definition name is rejected as a candidate. [08 "Placement root and search helpers"]

#### R-P0-05 §5 — Class-vector compilation and refresh — Established [R-P0-05]

The class routine walks definition IDs in strict ascending type order, skips the zero sentinel, and draws no random numbers itself. All float-to-integer conversions truncate toward zero; float32 narrowing occurs at the recovered helper boundaries. Final signed-byte coefficients clamp to `[-100, 100]`.

The initialization-only single-byte vector is written once at construction — zero, plus 40 when the definition's category flag is clear, plus 20 when the build-option list is non-empty — and is never rewritten by the refresh routine. The category flag's authored semantic name is not closed and must not be replaced with a guessed meaning. [08 "Strategic state construction and refresh"]

The single coefficient (first pass):

```text
acc = 1
if ExtractsMetal != 0.0: acc = 11
if MakesMetal != 0:      acc += 10
if Classify(def) < 0:    acc += 10

t0 = trunc(float32(acc) - BuildCostMetal  * 0.01)
t1 = trunc(float32(t0) - BuildCostEnergy * 0.002)

weaponBase = 11 if CanAttack else 1
weaponSum = weaponBase
for each of the three weapon slots:
    if weapon.active != 0:
        weaponSum += weapon.damage / 40 + 5 + weapon.range / 100
weaponSum = clamp(weaponSum, -100, 100)
coefficient = clamp(weaponSum + t1, -100, 100)
```

The damage and range field identities, widths, and divisions are established
(the `DAMAGE/default` word and the `range` word of the weapon parser); the
earlier "reload /100" reading is retracted, and the two weapon-field TDF-key
residual in R-P0-05 §8 is closed. The strategic half-capacity state field
remains: its writer is bounded-negative and stock state leaves its branch
false (see R-P0-05 §8).

The triple class coefficients — the other-mix accumulator starts at zero and receives these addends:

```text
if CanAttack:                 acc = 21
if Builder && count < 3:     acc += 30
if Classify(def) < 0:         acc += 50
if ExtractsMetal != 0.0:     acc += 50
if MakesMetal != 0:          acc += 25
if CanFly:                    acc += 40
if SonarDistance != 0:        acc += 15
if RadarDistance != 0:       acc += 5
```

Then `count == 0` multiplies the accumulator by four, `count == 1` by two, and `MaxSlope >= 0` by three. A global half-capacity comparison may contribute half of the single coefficient; its strategic-state writer is not located and stock state leaves that branch false. The coefficient is then zeroed when `CanLoad` is set, when `IsFeature` is set, or when the wind-generator/global-wind comparison is true, and is clamped to the signed-byte range.

The energy coefficient: `clamp(trunc(BuildCostEnergy * -0.0025 - Classify(def) * 5.0), -100, 100)`.

The metal coefficient:

```text
metalBase = 100 if ExtractsMetal != 0.0 else 0
metal = clamp(trunc(metalBase
                    - BuildCostMetal * 0.02
                    - (25 if MakesMetal != 0 else 0)), -100, 100)
```

The definition inputs consumed by the routine — extracts-metal, makes-metal, metal and energy build costs, can-attack, builder, can-fly, can-load, is-feature, max-slope, radar and sonar distance, and wind-generator — are recovered runtime field mappings, not guesses based on similarly named proxies. Confidence is high for the comparisons, constants, cadence, and field mappings; medium for the classification helper's semantic name; the two weapon-field key identities are closed as the `DAMAGE/default` word and the `range` word.

#### R-P0-05 §6 — Cadence and same-tick ordering — Established [R-P0-05]

The established per-player sequence is:

1. the player slot dispatches the manager, including any due candidate-selection task, before the strategic refresh;
2. the 30-tick refresh runs when due: it clears and rebuilds the completed counts and the weighted center, stores the refresh tick, draws a single random value with bound 30, and recomputes the class vectors only when that draw is zero;
3. the rest of the per-player unit and session work runs; and
4. when its ledger gate is due, the economy ledger updates production, consumption, stock, and capacities from the unit buckets.

A class refresh therefore sees the live-unit pool and the previous strategic counts, then writes vectors for subsequent selections. A manager task in the same iteration ran before that refresh, and a resource ledger write later in the iteration is not an input to that same manager invocation; the next tick's manager uses those newly settled aggregates. The class routine consumes zero random numbers; exactly one bound-30 draw occurs per due refresh, and the initial class computation at strategic-state creation draws none. The candidate selection's cumulative draw and the extractor placement draw are separate later consumers of the simulation stream. [08 "Strategic state construction and refresh"; 05 "Settlement cadence"]

#### R-P0-05 §7 — Extractor, profile, and request gates — Established [R-P0-05]

Extractor candidates take a separate placement branch: the root draws once with bound 255 and compares the draw with the mission's uniform surface-metal value; the strict `surfaceMetal < draw` result selects one placement helper, while non-extractors go directly to the other. The exact geometry of those helpers and any additional water legality are not established here; a water-only extractor filter is a heuristic, not a retail score gate. [08 "Placement root and search helpers"]

Profile loading resolves the mission `aiprofile` through the resource system and falls back to `ai\default.txt`. `plan` enables subsequent directives only for `any` or the current difficulty (`0=easy`, `1=medium`, `2=hard`); `weight` multiplies and clamps the per-type profile weight (default 100); `limit` updates the per-type limit (default -1). The unit-definition `ai_limit` text is parsed but has no bounded runtime reader and must not replace the profile `limit`. [08 "Established AI-facing data and rooted planner"]

Candidate request identity is determined by the ordinary service path: the construction task selects from the assigned builder's build list, resolves placement, and submits a build command with type identity and queue modifier one through the ordinary order service; the resource/queue task selects a build option for an idle builder and queues it through the same service. Neither path writes a unit or economy record directly. [08 "Dispatch gates and order sinks"]

#### R-P0-05 §8 — Implementation guidance and blockers — Established [R-P0-05]

Replace the economy adapter's stock-plus-produced proxy with the player runtime aggregate mapping. Preserve the strict gate comparisons, the pressure mix, the profile weight and limit state, the authored candidate order, the cumulative random draw, and the manager-before-refresh-before-ledger ordering. Replace strategic field proxies with the recovered definition fields, and leave unresolved semantic fields behind explicit TODOs.

Locked by the score fixtures in internal/ai (o6_score_test.go, strategic_test.go) and the economy aggregate fixtures in internal/economy.

```text
TODO(question): What is the exact runtime meaning and writer of the strategic half-capacity state field? (The weapon-field TDF identities are closed: `DAMAGE/default` feeds the damage read and `range` feeds the range read of the class routine; `reloadtime` is not read by it.)

TODO(question): What are the exact geometry and water-legality contracts of the two placement helpers beyond the established extractor selector draw? (Narrowed: the metal score is the footprint per-cell metal-byte sum and the waterline band is enforced only by the yard path of the placement validator — see the Placement root section.)
```

### What remains not established — Supported inference and unknown [P0-01] [P0-02] [P0-03]

The class routine's per-definition inputs now have recovered field identities — extracts-metal, makes-metal, can-attack, builder, can-fly, can-load, is-feature, max-slope, radar and sonar distance, and wind-generator — as runtime field mappings (R-P0-05 §5). Audit: this doc previously claimed the addend-gating flag names were not closed; the field-level census recovered those identities, so the residual narrows to the semantic name of the classification helper and the strategic half-capacity state writer. The classifier's two high runtime status bits retain opaque semantic names while their tests and destinations are fixed (R-P0-04 §3). The weapon table field identities are closed: the class routine reads the `DAMAGE/default` word (damage divided by 40) and the `range` word (range divided by 100); `reloadtime` is not read by it (the earlier "reload divided by 100" was inference and is retracted). The metal score returned by the placement helpers is established as the footprint per-cell metal-byte sum (the exhaustive helper maximizes it; the scatter helper compares it against surface metal times footprint X times footprint Z times two), and water legality is the yard path's waterline band.

The population of the nine manager task group vectors is now positive-static
for the direct writer and the six classifier destinations; no additional
distinct writer is located in the whole-image direct-store/xref census. The
classifier's two high runtime bits retain opaque semantic names, and no
capture-specific caller of the direct manager-group writer was found. AI transport geometry and any
distinct naval or air expansion policy beyond the generic move orders are not
in this lane and remain unknown. The x87 control-word edge beyond the
established narrowing to float32 at every helper invocation boundary remains an
unknown of platform residual class; the default rounding mode is assumed.
[P0-02] [P0-03] [R-P0-04]

The four inert mission placement fields and the definition `ai_limit` field remain closed as bounded negative and must not be treated as strategic inputs. Earlier weighted-random loader claims are retracted.


## DirectPlay transport

### Transport choice

Retail networking is built on DirectPlay. The executable imports the DirectPlay
runtime and uses its interface for send, receive, session/lobby initialization,
and player identities. No raw socket transport is present in the binary.

There are separate paths for:

- connections supplied by a DirectPlay lobby;
- connections initialized directly by the game;
- pre-game lobby state;
- in-game deterministic command exchange.

### Topology

The observed design is peer-oriented. Each participant maintains the same ten
player slots and can send to one peer or broadcast by iterating eligible remote
slots. There is no directly observed dedicated simulation server.

DirectPlay can provide guaranteed delivery. The executable selects guaranteed
or ordinary send behavior per path. It also implements its own frame batching,
future queues, pacing, and acknowledgement/retransmission state above the
transport.

### Custom and direct wire paths

The retail network has two selectable wire paths that both feed the same central
packet dispatcher:

1. **Direct path** — one game packet as one DirectPlay message (type byte at `+0`).
2. **Custom path** — multiple game records batched behind a descending `i32`
   transport sequence (independent of simulation tick), optionally compressed,
   checksummed, XOR-transformed, and sent as one DirectPlay message.

Custom envelope, when used, is:
`+0 flag u8` (`3` raw, `4` compressed), `+1 checksum u16le` (sum of XORed bytes
`3..W-4`), `+3 payload`; bytes `W-3..W-1` are neither XORed nor checksummed;
payload itself begins with `i32` sequence `(-2,-3,...INT_MIN,-2)` then
concatenated game records. Valid record types satisfy `2 <= type < 0x2D`;
`0x2C` is variable length (`type u8 + length u16le` then bitstream, base size
`3`). DirectPlay message length supplies wire length; compression is attempted
only for payload >=13 bytes and only if `compressed+3 < original` and not
disabled.

The global DirectPlay guaranteed flag selects `DPSEND_GUARANTEED` per send:
enabled at session setup, disabled for the `0x02` probe, and cleared at the
pre-battle barrier completion. The flag applies to both direct and custom sends.

### Loopback/diagnostic path

A diagnostic mode bypasses the DirectPlay invocation and routes the same packet bytes
through internal loopback helpers. This does not define a second gameplay
protocol; it is a transport/debug alternative.

## Packet framing and dispatch

### Framing

**The first byte of a packet is its type.** Dispatch is a direct indexed
invocation through a handler table using that byte.

Three parallel tables are indexed by the type byte. Their capacity is 46, so
valid types run from `0x00` through `0x2D`:

1. **Handler.** Session initialization installs a reject-with-failure stub for
   every declared type; the concrete handlers are installed later by whichever
   subsystem owns the type.
2. **Length.** A 16-bit **fixed byte length for that packet type**. The receive
   path uses it to walk a datagram — it subtracts the length from the bytes
   remaining and advances the cursor by the same amount. **A datagram is
   therefore a concatenation of fixed-length, self-delimiting packets, and no
   packet carries a length field on the wire.** A type with no declared length
   cannot be walked past and ends parsing.
3. **Admission mask,** three bits wide, keyed to the session state: bit 1
   gates state 5 (battle loading/setup), bit 2 gates state 6 (live battle),
   and bit 0 admits a packet in every other state. A packet failing its gate
   is discarded before its handler runs.

Session initialization also allocates an 8,192-byte packet/reassembly buffer.

### Declared packet types

The in-game receiver switches on the type byte and either decodes the payload
inline or forwards the whole packet to a subsystem. The roles below come from
that dispatch: a role is stated where the handler is independently identified,
and left descriptive where it is not.

**Every decoded payload's fields fit its declared length exactly**, with no
slack and no padding, which independently validates the length table.

| Type | Len | Mask | Role and payload |
|---:|---:|---:|---|
| `0x02` | 13 | 7 | Round-trip probe. The handler samples the wall clock and replies. |
| `0x03` | 3 | 7 | Inline; not decoded here. |
| `0x05` | 65 | 7 | **Chat message.** One byte of type then a 64-byte text field, passed to the message-arrival path. |
| `0x06` | 1 | 7 | Request; the handler finds the first active player and replies with type `0x07`. No payload. |
| `0x07` | 1 | 7 | Reply to the above; sets a per-peer bit. No payload. |
| `0x08` | 1 | 7 | Sets a session flag. No payload. |
| `0x09` | 23 | 4 | **Unit creation.** Forwarded whole to the spawn path. |
| `0x0a` | 7 | 4 | Unit occupancy/placement change. Forwarded whole. |
| `0x0b` | 9 | 4 | **Damage packet.** Forwarded whole to the central damage intake. |
| `0x0c` | 11 | 4 | **Unit death.** Forwarded whole to the central death handler in replay mode. |
| `0x0d` | 36 | 4 | **Projectile creation**, the packet-velocity path. Forwarded whole. |
| `0x0e` | 14 | 4 | **Projectile impact.** Forwarded to the impact and removal root. |
| `0x0f` | 6 | 4 | **Feature ignition or damage.** `+1` a one-byte sub-case, `+2` and `+4` two 16-bit tile coordinates. |
| `0x10` | 22 | 4 | **Run a script on a unit.** `+1` u16 unit, `+3` u16 script, `+5` u8, then four 32-bit arguments at `+6`, `+10`, `+14`, `+18`. |
| `0x11` | 4 | 4 | **Unit state transition.** `+1` u16 unit, `+3` u8 state. The handler applies the value once and its complement once, driving the Activate, Deactivate, StartBuilding, and StopBuilding edges. |
| `0x12` | 5 | 4 | Build-related command. `+1` u16, `+3` u16. |
| `0x13` | 18 | 7 | **Play a sound.** `+1` u8 selector, `+2` i32 sound identity, and when the selector is zero a three-component world position at `+6`. A nonzero selector plays without a position. |
| `0x14` | 24 | 4 | **Ownership transfer**, the capture path. `+1` u16, `+3` i32, remainder read by the handler. |
| `0x15` | 1 | 6 | Sets a per-player flag, gated on a global bit. No payload; the sender comes from the transport. |
| `0x16` | 17 | 4 | **Resource and sensor sharing.** `+1` u8 subtype (1/2/3), `+2` 3-byte reserved, `+5` u32 source DPID, `+9` u32 dest DPID, `+13` f32 value. Subtype 3 copies visibility bits; float is zero in traced subtype-3 producer. |
| `0x17` | 2 | 7 | `+1` u8. Session control. |
| `0x18` | 2 | 7 | `+1` u8. Session control. |
| `0x19` | 3 | 7 | **Pause and game speed.** `+1` u8 selector, `+2` u8 value. Selector zero sets the pause bit from the value's low bit; otherwise the value sets the game speed. |
| `0x1a` | 14 | 1 | Lobby-side; forwarded whole. |
| `0x1b` | 6 | 7 | `+1` i32, `+5` u8. |
| `0x1c` | 5 | 7 | **Peer loss notice.** `+1` i32 peer identity. Formats a translated message into the chat region, which is why the disconnect text is localized. |
| `0x1d` | 9 | 0 | **Dead type.** Its admission mask is zero so it is rejected in every state, and its handler is a three-byte stub. |
| `0x1e` | 2 | 6 | `+1` u8. |
| `0x1f` | 5 | 6 | `+1` i32 peer identity, mapped to a slot index whose per-player marker is then set. |
| `0x20` | 186 | 7 | Bulk state block; the largest packet. Only two fields are touched inline: the map-content compatibility value inside the metadata block and a destination-selecting DPID near the end. |
| `0x21` | 10 | 1 | Lobby-side. `+1` u8, `+2` i32, `+6` i32. |
| `0x22` | 6 | 1 | Lobby-side. `+1` i32, `+5` u8. |
| `0x23` | 14 | 7 | `+1` i32, `+5` i32, `+9` u8, `+10` i32. |
| `0x24` | 6 | 7 | `+1` i32, `+5` u8. |
| `0x25` | 5 | 1 | **No case in the in-game switch.** Its mask admits it only outside the battle-loading/live-battle states, so it is handled by the lobby receiver instead. |
| `0x26` | 41 | 7 | Forwarded whole; roster semantics observed on the receive side: an empty roster decodes to zero participants and a special class value expands to all slots. |
| `0x27` | 17 | 7 | **Integrity breach.** `+1` i32 peer identity; formats the translated "has modified his executable" text into the chat region. The twelve trailing bytes are opaque; their producer algorithm is unresolved. |
| `0x28` | 58 | 7 | **Participant state message** (economy/player record). `+1` u8 flag, `+2` i32 sign-extended i16, `+6` i32 sign-extended i16, `+10` i32 sign-extended i16, `+14` i32 sign-extended i16, `+18` u32, `+22` u32, `+26` u32, `+30` u32, `+34` f32, `+38` f32, `+42` f32, `+46` f32, `+50` f32, `+54` f32. Consumer widens floats to doubles and copies every field into participant state unconditionally — no comparison, threshold, or abort; it can answer with a three-byte control plus optional re-send on nonzero flag. |
| `0x29` | 3 | 7 | `+1` u8, `+2` u8. |
| `0x2a` | 2 | 7 | `+1` u8 stored into a per-peer field. The local producer emits it as the mean of six per-peer bytes (loading progress). |
| `0x2c` | 3 | 4 | **Build completion**; routes into the unit-creation path. |

Two corrections to the earlier family list: the integrity-breach type is
`0x27`, not `0x25`; and `0x28` carries unit state synchronization rather than
an economy hash.

The types whose admission mask is 1 are absent from this switch, which is
consistent: mask 1 admits a type only outside the battle-loading/live-battle
states, so those are lobby packets belonging to a different receiver.

**Unknown.** The payload layouts of the types forwarded whole live in their
receiving subsystems and are not decoded here, nor are the semantic names of
the several small control types.

### Command canonicalization

Interface order names are first converted to small internal order identifiers
and modifier flags. Directly observed order families include attack, blast,
defend, repair, patrol, reclaim, capture, unload/load, ordinary build,
stockpiled weapon build, and related special orders.

The canonical order plus its target, position, selected units, queue modifier,
and other payload state is then serialized into deterministic command frames.
Network receipt reconstructs the same canonical order and enters it through the
normal authoritative order service.

The exact wire layout for every order is unknown. The clean-room boundary is
the canonical command, not a serialized copy of an interface widget.

## Send pacing and batching

The network layer derives a send interval from a rate clamped to the range two
through thirty. At the normal maximum this corresponds to approximately one
send opportunity every 33 milliseconds; the queue cadence converts that period
to the engine's 30-unit clock via `(period*30+999)/1000`.

Custom queue capacity is 11 queues (one general, ten participant-directed);
default descriptor storage is 100×32 bytes with two `0x43E`-byte buffers. A
game packet is appended while payload < `0x42B`; force-flush occurs when pending
bytes > `0x429`, descriptor count `0x400`, or explicitly requested. Flush groups
by destination DPID and emits one custom payload per group plus the descending
transport sequence. Direct-path sends still use the `1,066`-byte datagram
threshold and `1,024` entry bound from the large dispatcher; descriptors are
owned by the queue and bytes are copied at enqueue.

Each sent frame records timing used for pacing and round-trip estimation; no
application ACK or retained sent-history keyed by transport sequence is proved
in the bounded custom trace. Exact checksums beyond the envelope XOR-sum, wraps,
and guaranteed selection per family remain incomplete beyond the global flag
above.

## Receive buffering

The receiver drains DirectPlay, validates/decodes the custom envelope, orders
by the descending transport sequence (suppressing duplicates, retaining one
pending frame; the receive-window maintenance helper is a no-op in this binary), scans the whole payload
for complete records (abort on incomplete), and places entries into a per-peer
ring (512 entries, `0x180C`-byte record ring, grown to requested+`0x100`).

Directly observed custom state includes:

- up to 512 entries per peer and one pending frame;
- local tick tags derived from the simulation tick at parse time: one path tags
  every record with current tick, another distributes a backlog over tick tags
  with delta clamped 1..30 and a 4-bit fixed-point cadence;
- withholding of entries tagged 1..30 ticks in the future;
- sender identity (source/dest DPIDs) retained beside peer state and restored
  on return;
- dynamic DirectPlay receive-buffer growth on insufficient space;
- copy into engine-owned packet buffer before dispatch.

A malformed custom payload has a hang path in the retail decoder (retail
defect preserved).

The 31-tick future window is not proof of a universal 30-tick input delay. A
separate value of thirty appears in delayed gameplay queues and in the tick-tag
cadence. Until header and scheduler fields are fully closed, command delay and
retention window must remain distinct concepts.

## Ping and adaptive timing

The ping path records a wall-clock send time, sends a control packet, receives
an echo/response, and stores the elapsed milliseconds in peer/player timing
state. Probe emission recurs on a sixty-unit clock cadence; probe timeouts
scale as thirty ticks per configured unit. Lobby UI consumes this timing.

Pacing and retransmission code uses tick and wall-clock values, but the exact
formula converting round-trip time into command delay or resend cadence is not
fully typed.

## Lockstep advancement

### Established model

At a scheduled step the local tick counter is incremented, the
central network dispatcher runs, eligible packets mutate state,
then unit, projectile, script, feature, economy, and other simulation
subsystems run in fixed order; custom send queues are serviced after
simulation for that step.

The recovered synchronization state requires peer agreement on:

- selected map and mission schema;
- player-slot configuration;
- initial placements and options;
- simulation timing.

**Random state is not agreed.** The battle-entry orchestrator reseeds the
Park–Miller stream from its own `QueryPerformanceCounter` sample (the
seed-setter has exactly one call site in the whole image — battle entry)
and the CRT stream from its own clock; the packet registry (all 46 declared
types with byte-exact layouts) contains no seed-exchange packet, no handler
writes the seed state, and the pre-battle readiness barrier exchanges only
readiness and assignment data. Peers therefore run divergent random
streams, which is consistent with the engine's known desync behavior; the
earlier "requires peer agreement on random state" phrasing is retracted.
The doc 01 §7.1 seed formula (sum of low and high parts of
`QueryPerformanceCounter`, XORed with a fixed constant, forced odd) is
authoritative and is what battle entry applies. [lane 08 seed agreement]

Matching executable, mounted-provider, and full catalog identity is a sensible
compatibility requirement, but it is not a closed field in the recovered
network contract. Map, terrain, feature, and resource checksum state is
described in the integrity section below.

### Soft pacing — no per-tick input barrier

No packet carries a per-tick complete input bundle, no generic application
acknowledgement packet exists, no scheduler branch waits for one command
contribution from every active peer before incrementing each tick, and no
fixed input-delay constant exists (bounded over the packet registry, the
custom send/receive chain, and the schedulers).

Advancement is instead throttled by remote progress. The wall-clock scheduler
scans the active remote participant slots, takes the oldest progress value
among them, computes lead = globalTick − oldestRemoteProgress, and:

- below 900 ticks of lead applies no throttle and clears the lag-throttle
  flag;
- at 900 or more sets the lag-throttle flag, clamps lead at 3600, and scales
  the normal speed term `activeSpeed · 0.1` by
  `max(0.01, (3600 − lead) / 2700)`.

The integer budget per pump iteration is then `trunc(delta · speedFactor +
carry)` clamped to 0..5; excess beyond five is dropped while the fractional
carry survives. This is soft pacing, not rollback: drift changes only tick
production. No state resynchronization, repair packet, or retransmission
protocol exists in the reviewed paths.

Pause does not stall networking: with the pause/control bit set and networking
active, the engine still flushes the custom send queues, dispatches received
packets, and emits a periodic one-byte control message (keepalive gated on the
scaled clock at a period of sixty scaled units). Only the simulation subsystem
sequence stops.

Residual: the pacing-scan progress dword has two competing readings of the
same code — the remote peer's reported progress versus an earliest pending
order time. Naming unresolved.

### No rollback or world snapshot resync

The decompilation shows retransmission, buffered future frames, hashes,
disconnects, and integrity errors. It does not show a general rollback engine
or a protocol that downloads a complete live world snapshot to repair a
desynchronized peer.

### Authority

The peer that may change lobby/map/start state is constrained by slot and host
flags. In-game commands are broadcast and deterministically applied.
Host-authority migration, where performed, deterministically selects the
numerically greatest DPID among eligible roles. The exact authority rules for
pause, speed, resign, resource sharing, player removal, and cheat/debug
commands are not fully closed.

## Synchronization and integrity checks

### Map/resource identity

The executable computes checksums over map data, including the terrain header,
plot/tile data, and raw feature records (the map-checksum builder applies the
four-accumulator primitive to the 64-byte header, raw plot, and raw
feature records, XORs components, and XORs one further map descriptor field;
the result is stored in participant metadata and carried on wire inside packet
`0x20`'s 185-byte metadata block, shown to lobby as map-content compatibility
with a version-gated compare). Resource/economy integrity values are also
observed in participant state but no single on-wire hash covering the whole
world is established.

The checksum primitive itself is established. It runs four independent 8-bit
accumulators over a buffer of length *n*; for each index *i* with byte *b*, an
additive accumulator takes `+ b`, an exclusive-or accumulator takes `^ b`, a
second additive accumulator takes `+ ((i & 0xFF) ^ b)`, and a second
exclusive-or accumulator takes `^ ((i + b) & 0xFF)`. The 32-bit result packs
them least-significant byte first in that order. The same primitive produces
the per-unit-definition content identity described in document 02, which
exclusive-ors the checksums of the unit's compiled script, its interface
files, and its downloadable data file.

Which of these values actually crosses the wire as peer content identity, and
whether the executable image or the archive provider set participates, is not
closed.

### Economy and integrity checks — overwrite-sync, not compare

No packet family establishes a fixed resource/economy hash comparison. The
earlier assignment of that role to `0x28` is retracted. Packet `0x28` is a
58-byte participant state message produced by a dedicated scanner thread polling
every 250 ms across locally-owned × remote participant pairs, plus an
immediate echo path that re-emits the packet toward its originator when the
inbound echo flag is set. It is not per-tick and is tied to no tick modulo.
The receiver copies every field into local participant state (shorts and ints
sign-extended back, floats widened back to doubles) with **no comparison, no
threshold, and no abort** — overwrite, not compare-and-react. The
sole gate is an anti-spam check that skips the copy while an echo-flood marker
is set.

Peer divergence therefore surfaces only indirectly through the kick/chat
flows: the executable-integrity notice posts its translated breach text into
chat repeatedly before disconnect cleanup removes the peer, disconnect notices
report peer loss, and a manual `+syncerr` chat command exists (handler
inferred).

The executable also contains a code-checksum routine that returns zero
unconditionally in retail 3.1, leaving its guarded "Code segment checksum
error found when switching FE states." diagnostic branch dead. Self-checks run
only when switching front-end states, never per tick. Residual: a separate
orchestrator referencing numbered `.zrb` files remains untraced, including its
guard relationship to the checksum path; the producer algorithm of packet
`0x27`'s twelve opaque trailing bytes is unresolved; the exact three-byte
throttle-acknowledgement format in the `0x28` receiver is not fully recovered.

The four-accumulator checksum primitive and the map/terrain checksum state are
established, but their on-wire exchange, cadence, and mismatch handling remain
unknown until a distinct packet builder, length, handler, and consumer are
traced. Do not label any packet an economy hash until every payload input,
cadence, and mismatch path is closed.

### Full-world hash

No direct evidence establishes a comprehensive per-tick hash of every world
object. Existing hashes cover the envelope XOR-sum (weak corruption check,
ignores last three bytes), the executable-integrity `0x27` notification
(algorithm unresolved), and the `0x28` participant message (overwrite, not compare).
A clean-room implementation must not describe them as a complete world-state
hash until all payload inputs are traced.

### Integrity failure

The executable can report an integrity breach, disconnect a player, or abort a
network game. Diagnostics and exception paths may also terminate the process.
The exact user-visible messages, broadcast order, and whether one peer can
force global termination remain partly unresolved.

## Disconnect, resign, and peer loss

Disconnect handling updates player connection state, broadcasts a control
packet where appropriate, reports the player name, and changes the lockstep
membership/barrier state. Resign and commander-death rules are distinct
gameplay events even if both remove effective player control.

No complete live reconnection or late-join state-transfer protocol has been
found. The behavior for a transient DirectPlay loss versus a permanent peer
departure remains incomplete.

## Save-file organization

### Location and representation

Retail saves are written beneath the savegame directory, addressed by the
pattern `savegame\\<name>.sav`. No save path examined uses a memory-mapped
native object image.

**Container.** A save is a *bank*: a self-describing keyed container that the
engine also uses elsewhere. Its header is exactly 34 bytes (`0x22`), written
zero-initialized and rewritten after the pool is complete:

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

The bank tag compared after pool load is `Total Annihilation 3.0`
(case-insensitive). The reader seeks to `stringPoolFileOffset`, reads to EOF,
optionally decompresses, and validates `pool + bankTagOffset`. Header offsets
and the tag offset are not range-checked before use. Accounts are enumerated
from `firstAccountFileOffset` until the file cursor reaches
`stringPoolFileOffset`, advancing by each account's `totalStoredSize`.

A wrong magic, wrong version, or wrong expected tag closes the file, frees the
pool if allocated, and returns zero. The payload after the header is either
read as is or decompressed with the same decompressor the archive reader uses;
a decompression failure emits a `HapiBank::OpenBank::Decompression...` /
`LoadAccount::Decompression...` diagnostic but parsing continues.

**Accounts.** The payload is a sequence of *accounts*. Each nonempty account
begins with a 32-byte header; empty accounts are not emitted and the body may
be compressed as one unit:

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

The account body follows and, when the flag says so, is separately
decompressed. Reading an account seeks past it using the declared span, so an
unwanted account can be skipped without decompressing or parsing, and a name
filter can select one account. The body groups, strictly in this order, are:

1. integer items — 8 bytes each: `u32 namePoolOffset`, `i32 value`;
2. floating items — 12 bytes each: `u32 namePoolOffset`, `f64 value`;
3. string items — 8 bytes each: `u32 namePoolOffset`, `u32 valuePoolOffset`;
4. binary-box descriptors — 16 bytes each: `i32 nameOrMarker` (nonnegative
   name offset or `-1` for numbered box), `i32 number` (or 0 for named),
   `u32 absolutePayloadOffset` (pre-compression image; converted to body-relative
   after decompression), `u32 length`.

Every name and text offset is relative to the shared uncompressed logical string
pool; the pool itself may be stored compressed. Duplicate accounts merge, later
scalar items overwrite earlier same-name items, and repeated descriptors for the
same box **append** payload bytes. Binary payload bytes are the concatenation
following the descriptors. Boxes grow as needed; empty file boxes have no
descriptor. Offsets, counts, and pool references are not range-checked; short
headers leave stack bytes in offsets; negative scalar/box counts skip loops
rather than reject; a declared span `<= 32` leaves the cursor after the header
instead of at `start+span` on the unfiltered path; decompression errors emit a
diagnostic but parsing continues; payload offsets outside the logical body are
not validated before copy.

The executable can also dump a bank as a readable audit listing — account
count, per-account name, each box's name or number and byte count, and each
item's name with its integer, floating, or string value — which independently
confirms the item model above.

Subsystem state therefore lands in the file as named items and named binary
boxes inside named accounts, not as a fixed positional record layout.

### File naming and write policy

`.SAV` normalization strips the last dot and everything after it from the
assembled path, then appends `.SAV`. The strip is not path-component aware,
so dots inside directory names are hit too.

The destination file is opened directly for truncate-write: an existing file
truncates at successful open. There is no temporary file, backup, fsync, or
rename transaction; there is no overwrite prompt and no delete confirmation
(the save list refreshes regardless of delete result). After open, every
write, seek, header-rewrite, flush, and close result is ignored and the
writer reports success after attempting close, leaving a possibly truncated
or partial file; the save callbacks ignore the writer result entirely.
Delete failure surfaces only through the backend error reporter's error code,
which wrappers and UI ignore.

### Account inventory

The account and entry names below come from the save writers themselves.

| Account | Entries |
|---|---|
| `Summary` | dynamic `BUILD DATE:`/`BUILD TIME:` keys (integer 0), `maxunits`, `Campaign`, `Mission`, `Map`, `Difficulty`, `Side`, `Players`, `Gametype`, `Thumbs`, multiplayer-only `CommanderDeath`, `Location`, `Mapping`, `LineOfSight`, `LineOfSightType`, `BetweenMissions` (=1 on non-battle saves), `Description`, `Game ID`, `Game Time`, and the live-battle-only `Radar Image` box |
| `Camera` | `X Position`, `Z Position` |
| `Players` | `Human Player`, `GameTime`, and per slot `Player%i` with `Controller`, `Energy`, `Metal`, `TotalEnergyProduced`, `TotalMetalProduced`, `TotalEnergyConsumed`, `TotalMetalConsumed` |
| `Units` | `Version`, `Number of Units`, per-unit records, per-unit `Script%i` script state, and a per-unit key formatted as `u%04xm%04x` |
| `Features` | `Feature Type Names`, then three parallel groups: `Number of Normal Features` with `Normal Features`, `Number of Animating Features` with `Animating Features`, and `Number of 3D Features` with `3D Features` |
| `Metal` | `Plotmap` |
| `PlayerFeatures` | `Plotmap` |
| `Mapping` | the mapping grid blob |
| `Meteor` | `Enabled`, `Active`, `Next Strike Time`, `Time Strike Ends`, `Next Hit Time`, `Origin X`, `Origin Z`, `Target X`, `Target Z` |
| `VictoryCondition_<name>` | one account per condition type, each with `Satisfied` and `Celebrated`, plus condition-specific entries such as `NumUnits` and `NumLeftToKill` |
| `DefeatCondition_<name>` | one account per defeat condition type, same shape |

Trigger accounts iterate the live victory array and then the defeat array,
calling each object's own save/load virtual. No count, instance identifier,
authored name, or array index is stored: same-type conditions overwrite one
another's account (last writer wins) and share it on load.

The unit-type name table is written separately, keyed as `UTYPENAME%4d`, so
unit records can refer to definitions by index and still survive a catalog
whose ordering differs on reload.

The save-list interface opens the bank with an account filter selecting only
the `Summary` account and never loads the rest of the file, which is what the
per-account length field and the name filter in the account reader are for.
It surfaces the game name, the description, the player count, the game type —
rendered as `Single` or as `Skirmish (%d players)` — and a `Radar Image`
preview.

The bulk records are byte-exact where established: the bank header is 34
bytes and each account header is 32 bytes with 16-byte box descriptors and a
trailing string pool whose offsets are logical and relative to its
uncompressed image. Typed items (integer, double, string) and binary boxes are
distinct groups inside each account. Logical references use stable identifiers
rather than native pointers: unit slot zero is null and each live unit's slot
index is its stable identifier. The per-unit record is 184 bytes (hex B8) with
the field table described under subsystem writers; the per-order record is 58
bytes (hex 3A) with subtype boxes that carry leaked prefix bytes that are
discarded on load. Script state is a 0x528-byte snapshot plus a stack words
area plus a per-piece 0x6C-byte table where two slots leak old stack values.
Terrain metal is a byte per cell covering world width times height; player
features are packed nibbles covering half that; mapping is raw bytes for half
the cells; the radar image is 8 bytes of width and height plus width times
height preview bytes and is never used for authoritative load.

**Established fact — fix-up and partial-load order.** Loading mutates live
initialized state in fixed account order rather than building a shadow copy:
Players are restored first, then Camera, then Features, then Metal, then
PlayerFeatures, then Mapping, then Units, then Meteor, then trigger state.
Units are reconstructed recursively by stable identifier, allocating each
through the canonical allocator's forced-slot path with lowest-free scan and
per-definition limit checks, then restoring script, accessory and mobile state,
and publishing visibility. If any box is short or has the wrong size, that
family is skipped and earlier families remain mutated — there is no
transactional rollback. If the Units version is not hex 11, the entire Units
family is skipped. File I/O is opened as write-plus-binary and truncates at
open with no temporary or backup; every post-open write, seek, or close result
is ignored.

**Established fact — battle versus campaign continuations and timing.** Load
preflight accepts only game type 1 (campaign) and 2 (multiplayer); any other
value fails as invalid. **A save with BetweenMissions equal to 1 routes
through campaign-continuation handling: battle restoration is skipped and
the fresh mission spawner rebuilds the battle from the authored mission file
(the campaign's Summary metadata — Campaign, Mission, Difficulty, Thumbs
W/L marks — drives the front end). A save without the BetweenMissions item
(the in-battle marker) routes through the six-state path and directly enters
battle restoration.** This settles the earlier contradictory corpus readings
(flag-present → restore versus flag-present → continuation): the battle-entry
gate checks the Summary BetweenMissions item twice, and only the absent/zero
case calls the battle restore dispatcher; the present case falls through to
the fresh spawner, and a BetweenMissions save contains only Summary so no
battle state could be restored by any other reading. The 28-byte scheduler
block is persisted verbatim and then
recomputed on the first budget pass from a stale anchor, which can produce a
capped five-tick catch-up, zero, or pause; the per-player UpdateTime deadline
is an absolute tick advanced by 30 when due and gates settlement. Both random
streams are omitted from the persisted state and are reseeded from wall-clock
sources before any bank restoration, so no bit-identical random continuation
exists across a load.

### Summary

The summary writer always emits, in order: the dynamic `BUILD DATE:` and
`BUILD TIME:` keys (integer zero values), `maxunits`, `Campaign`, `Mission`,
and `Map` names, `Difficulty` and `Side`, `Players`, `Gametype`, `Thumbs`;
then, on multiplayer saves only, `CommanderDeath`, `Location`, `Mapping`,
`LineOfSight`, and `LineOfSightType`; then `BetweenMissions` = 1 on saves
written outside a live battle, `Description` when the caller supplies a
non-null one, `Game ID`, and `Game Time` (the global simulation tick,
presentation metadata — authoritative continuation uses
`Players/GameTime`). The `Radar Image` binary box is written on live-battle
saves only. The Summary integer named `Mapping` is distinct from the separate
`Mapping` account.

`BetweenMissions=1` routes a load into campaign continuation handling rather
than battle reconstruction; the battle loader runs only for battle saves.

Load preflight accepts only `Gametype` 1 (campaign) or 2 (multiplayer);
anything else fails as an invalid savegame. Campaign saves require the
campaign CD and multiplayer saves require the multiplayer CD — distinct
insert-CD dialogs stop the load before any battle transition. Per-item load
defaults on missing or mistyped items: `maxunits` low 16 bits else 0;
`Difficulty`, `Side`, and `Players` default 0; the five multiplayer rule
fields default 1. `Thumbs` is copied during preflight, so a malformed absence
can drive a copy from a null source (retail risk preserved).

Residual: the upstream GUI filename edit-character policy and code-page
interpretation remain unknown.

### Subsystem writers

Directly identified save families cover:

- camera state and per-unit destination/waypoint fields, but not active
  path-search queues;
- player and resource state;
- units, unit scripts, attachments, and construction queues;
- mapping/exploration state;
- normal, animated, and 3D features;
- player-feature state;
- terrain metal state;
- meteor state;
- the campaign-gated mission-object pool;
- a radar-image presentation dump that the reviewed load dispatcher does not
  restore;
- campaign/mission metadata.

The complete bounded standard battle save/load graph contains no ordinary
projectile-pool reader or writer. Battle setup creates the pool with a zero
active count before load reconstruction begins. Standard battle save/load thus
resumes without in-flight projectiles or burst-scheduler records. Replay and
any undiscovered nonstandard snapshot path remain open.

Each subsystem is written field by field or as explicit fixed records within
a bank account's typed items and binary boxes. Native pointers are not portable
save identities.

### Unit and script records

Units are enumerated in stable slot order. Each record stores enough identity
and state to allocate the same logical unit, then fix up ownership, cargo,
attachments, targets, scripts, and build queues. Script sections use sequential
names. Build queue records use encoded unit/request names and payload fields.

The exact persistence of every script thread local, operand stack, wait state,
signal mask, and callback is not yet closed.

#### R-SAVE-UNIT-01 — Unit base record and fixed-slot reconstruction

This addendum closes the part of the unit save contract needed to stage a
deterministic fixed-slot restore. It supersedes only the preceding statement
that a unit record stores “enough” state; it does not close the order, script,
accessory, mobile, or feature boxes. The positions below are offsets within a
save-file unit box, not executable layout. The record uses little-endian
integers and has two accepted lengths: 184 bytes (`0xB8`) and the distinct
182-byte compatibility form (`0xB6`). [Established; [01 §6.1], [04 §2.3]]

**Correction to the preceding revision.** The earlier table called the
position order X/Z/Y and left both short words and the orientation payload
unassigned. The writer/read census, reconciled with the movement and damage
callers, establishes the runtime order as X/Y/Z, identifies the first short as
current health, identifies the second as the wrapping kill counter, and
separates bank, heading, and pitch. The authored mission-placement record
still uses its own X/Z/Y order; that unrelated format order caused the earlier
save-record reversal. [Established; direct writer/read symmetry and bounded
caller census]

**Writer/reader symmetry.** The writer emits the fields in the fixed order
below. The reader selects the same numbered box, accepts only one of the two
lengths, and decodes the `0xB8` fields before running the later fix-up passes.
The field's runtime name is included only where the current evidence closes
it; an opaque word must remain opaque in a staged image rather than being
assigned a plausible `Unit` field.

| Save bytes | Wire form | Meaning and restore disposition |
|---|---|---|
| `0x00..0x1F` | 32-byte NUL-padded text | Unit definition name. It is the definition lookup key. [Established] |
| `0x20` | `u8` | Source owner/player byte. It is part of the base identity and must be validated against the player slice used for the forced allocation. [Established for the wire field; exact malformed-value policy is Unknown] |
| `0x21..0x22` | `u16` | Stable unit ID and forced pool slot. Zero is the null sentinel; a live unit may not use slot zero. [Established] |
| `0x23..0x26` | `u32` | Count of order/build records associated with this unit. The records themselves are restored by the later order pass. [Established] |
| `0x27..0x2A` | `u32` | Runtime boolean word. Its authoritative semantic name is Unknown; preserve it without converting it to `Alive`, `Dying`, or a command flag. |
| `0x2B..0x2E`, `0x2F..0x32`, `0x33..0x36` | `u32` each | Fixed-point world X, Y, and Z, respectively. They are copied as raw 16.16 values; no terrain resampling is part of base restore. [Established; the authored placement record's separate X/Z/Y order is not this runtime record] |
| `0x37..0x38` | `u16` | Signed bank/roll orientation component. [Established by the orientation consumer and writer source word] |
| `0x39..0x3A` | `u16` | Unsigned 16-bit heading/yaw on the circular angle domain. [Established] |
| `0x3B..0x3C` | `u16` | Signed pitch orientation component. [Established] |
| `0x3D..0x3E` | `s16` | Current health. Damage and death consumers treat it as signed 16-bit state. [Established; [06 §9.2]] |
| `0x3F..0x40` | `u16` | Unit kill counter used by veterancy; it wraps as a word. It is not health. [Established; [06 §9.2]] |
| `0x41..0x88` | 3 × 24-byte embedded records | Three weapon-slot payloads. The exact per-slot map and the fields intentionally omitted from it are closed below in **R-SAVE-WEAPON-01**. [Established; [R-SAVE-WEAPON-01]] |
| `0x89..0x8A`, `0x8B..0x8C` | `u16` each | Stable IDs for two optional cross-unit references; zero means absent. These are resolved recursively before attachment is committed. [Established] |
| `0x8D` | `u8` | Reference-associated byte, or `0xFF` when the first reference is absent. Its complete semantic name is Unknown; preserve the sentinel. |
| `0x8E` | `u8` | Low byte of another runtime state word. Semantic name is Unknown. |
| `0x8F..0x92` | `u32` | Stored extractor/site-yield word used by the unit's placement/economy state. Preserve verbatim; recomputing it from the map would change save behavior. [Established for persisted word; exact public field mapping is implementation-dependent] |
| `0x93..0x9E` | 3 × `u32` | Cached cell/footprint words used by placement and occupancy. They are derived-looking but are written and read as part of the base record; preserve them until occupancy fix-up. Their exact packing is Unknown. |
| `0x9F..0xA6` | 2 × `u32` | Two persisted runtime counters/deadline words. Their complete semantic names are Unknown; do not map them to kills, experience, or a timer without a field-isolation probe. |
| `0xA7..0xAA` | raw `f32` | Construction remaining fraction. It is the saved construction state and follows the retail fraction convention (completed is zero, an unfinished unit is positive up to the authored range). [Established] |
| `0xAB..0xAD`, `0xB0..0xB1` | five `u8` values | Persisted runtime bytes whose individual semantics are Unknown. Preserve exactly. |
| `0xAE..0xAF` | `u16` | Capability/pending-mask word. Only the established low-width behavior may be interpreted; preserve the full short for later command/construction fix-up. [Established wire field; complete bit map remains partial] |
| `0xB2..0xB3` | `u16` | Low state byte, zero-extended: bit 0 activated, bit 1 armored, bit 2 engine-driven cloak, and bit 3 building. The edge machine consumes these values after base restore. [Established; [04 §2.4]] |
| `0xB4..0xB7` | `u32` packed word | Packed unit status plus the low nibble of the second state byte. The status component carries the authoritative alive bit, death latch, and completion bit: alive is bit `0x10000000`, dying/death-mark is `0x4000`, and construction-complete is `0x2000`. The second state byte's low nibble carries in-build-stance, busy, yard-open, and bugger-off state, but its exact packed bit positions inside this save word are not closed. Bits 17..19 can contain writer-side uninitialized values and must not drive state. [Established logical status bits; Unknown packed placement of the second byte and remaining bits; [04 §2.4]] |

The table is deliberately asymmetric in one important respect: definition,
owner, fixed-slot identity, position, orientation, current health,
construction remaining, and the established lifecycle/state bits are safe
inputs to a minimally live staged unit. The definition's compiled maximum
health is the authoritative cap; no maximum-health scalar is serialized in
the base record. The opaque weapon-slot subfields, cached cells, counters,
individual bytes, and unassigned high status bits do not block allocation, but
must be retained and deferred to their owning passes. [Established]

A loader must not manufacture a live unit from a name and stable ID alone: it
must apply the saved owner, fixed-point transform, signed health, remaining
fraction, activation/building state, and packed alive/death status, then run
the later registration and occupancy steps. [Established]

**Identity and minimal base restore.** The loader first validates the Units
account version (`0x11`) and count. For each enumerated box, a `0xB8` stable ID
of zero is a null record; a nonzero ID is looked up by scanning the numbered
boxes for that ID, not by treating the enumeration index as the slot. The
definition name must resolve in the loaded catalog. The canonical allocator
then receives the saved owner/player identity and the saved stable ID as a
forced slot. It rejects slot zero, an occupied slot, a slot outside the
owner's battle-entry slice, and a definition-limit violation. Slot slices
come from the one battle-entry player permutation and are not recomputed from
save enumeration order. [Established; [01 §6.1], [01 §6.1 player-slice order],
[04 §2.3]]

The first base-restore write set is therefore: definition identity, owner,
stable slot, X/Y/Z fixed-point position, bank/heading/pitch, signed current
health, construction remaining, low activation/building state, the alive and
death status bits, the complete opaque state/weapon payload, and reference
placeholders. The allocator's live bit and the saved live bit must agree for
a reconstructable record; a saved death latch is retained so the normal
slot-end death path sees a dying unit rather than silently reviving it. The
restored object is not published as a fully usable unit until reference,
accessory/mobile, order, script, registration, derived occupancy, and
visibility steps have completed. A missing definition, failed forced
allocation, duplicate stable ID, or absent referenced box is a failed staged
image rather than an invitation to allocate a replacement slot.
The retail reader can skip a failed reconstruction; the transactional loader
planned by CRD-009 must report the failure before commit so that a failed image
cannot partially replace the live session. The latter is an implementation
boundary, not a claim that retail itself was transactional. [Retail behavior
Established; transactional consequence Supported inference]

**References and later-owned state.** The two stable-ID fields at `0x89` and
`0x8B` are logical references, never native pointers. Resolution is recursive
and depth-first: resolve a referenced ID, then attach it to the already staged
parent. Consequently, numbered-box order does not determine allocation order.
A missing ID, duplicate ID, or cycle is malformed for a transactional image;
retail's bounded early-active guard can leave a partial result on a cycle and
must not be copied as successful commit behavior. [Established retail
resolution; Supported inference for rejecting cycles during pre-commit]

The following state is intentionally restored by later passes rather than by
the base record alone:

- the count at `0x23` drives numbered order boxes (`u%04xm%04x`) and their
  subtype records; these rebuild order nodes, targets, links, and build queues;
- the three weapon-slot payloads are completed by the weapon/subtype path;
- per-unit accessory and mobile boxes restore attachment and mobile-builder
  state;
- sequential script boxes restore the COB snapshot, stack, pieces, waits,
  signals, and callbacks; script persistence remains only partially closed;
- registration and derived occupancy run after raw state and references;
  visibility is published last, before the first authoritative tick.

This order prevents an order or script reference from observing an unallocated
unit and prevents the first tick from observing footprints that were derived
from stale map state. [Established fix-up order; [08 “Unit and script
records”], [04 §2.3]]

#### R-SAVE-WEAPON-01 — Fixed weapon-slot records and transient aim state

The three records at `0x41 + 0x18*n`, for `n = 0..2`, are fixed-width,
little-endian 24-byte records. The save writer and unit reconstructor use the
same byte positions; the reconstruction writes the decoded values into the
slot's initialized runtime words before order and script fix-up. This is a
wire map, not a prescription for Nanolathe's in-memory layout. [Established;
[01 §6.1], [06 §1.2]]

| Record bytes | Wire form | Runtime meaning and restore rule |
|---|---|---|
| `0x00..0x03` | `u32` pair of `s16` words | Low word is the target-unit pool index in unit mode, or ground X in point mode. High word is the target-mode/ground-Z word: `0x8000` is the unit-target sentinel; any other signed value selects a ground point and is its ground-Z component. The writer and reader copy the full pair, so the target kind is not inferred from zero/nonzero. A nonzero unit index is resolved only after all fixed slots have been allocated. [Established; [06 §3.1]] |
| `0x04..0x07` | `u32` | Copy of a slot target/aim payload word. It is restored byte-for-byte into the corresponding slot word. Its exact axis and packing are not established by the save census; do not name it X, Y, or Z. [Established wire copy; Unknown semantic name] |
| `0x08..0x0B` | zero-extended `u8` | The resolved weapon definition's active/inactive byte, not a weapon catalog ID. Nonzero is the definition-side active gate; zero is the inactive case. The weapon identity comes from the unit definition's ordered `weapon1..3` links and the catalog, with record 0 (`noweapon`, ID 0) as the inactive sentinel when that family is present. The reader writes this byte back to the resolved definition's active field, but it cannot select a different weapon definition. [Established; [02 §5], [06 §1.2]] |
| `0x0C..0x0F` | `u32` | Copy of a second slot payload word. It is restored into the matching runtime word. The bounded target census does not establish a public target axis or identity for it; retain it as an opaque staged value. [Established wire copy; Unknown semantic name] |
| `0x10..0x11` | `s16` | Signed reload countdown in ticks. Restore the low 16 bits exactly, including zero and negative bit patterns; the firing gate consumes the signed slot word. [Established; [06 §1.2], [06 §4.4]] |
| `0x12..0x13` | `s16` | Desired yaw/heading for the slot's aim request. Restore exactly on the circular 16-bit angle representation. [Established; [06 §3.3]] |
| `0x14..0x15` | `s16` | Desired pitch for the slot's aim request. Restore exactly; it is not a unit orientation field. [Established; [06 §3.3]] |
| `0x16` | `u8` | Stockpile remainder (completed rounds). Restore the byte exactly; launch decrements this value and does not change reload. For non-stockpile weapons the serialized byte is still part of the fixed record and must not be repurposed. [Established; [06 §11]] |
| `0x17` | `u8` | Only bits 0..4 are meaningful persisted slot flags. Bit 0 is the Aim-request/result latch, bit 1 is armed/has-target, and bit 4 is tracking; bits 2 and 3 retain their slot-flag positions but their semantic names are not closed. The writer and reader discard/overwrite the upper three bits; their observed high-bit values are stack residue, not state. [Established bit behavior; Unknown names for bits 2/3; [06 §3.3]] |

The active-definition byte at `0x08..0x0B` is the important identity
boundary. Two different weapon definitions with the same active gate produce
the same byte, so a loader must never interpret this field as a catalog index.
Conversely, an empty or unresolved unit weapon link resolves to the inactive
record-0 definition when available; its identity is supplied by the restored
unit definition, while this field remains zero. A catalog without a record-0
definition leaves the link nil/inactive, as specified by the content linker.
[Established; [02 §5]]

The target encoding is complete in the first four bytes of each record. The
runtime target pair uses `-0x8000` as the unit-latch sentinel and any other
high word as a ground point's Z component; the low word is respectively a
unit pool index or ground X. The pair is copied as one `u32`, preserving both
words. The two later payload words remain unnamed; at least one is cleared as
part of Aim setup, so neither is a second target-kind discriminator.
Consequently:

- a unit-mode pair must resolve its low-word logical stable ID against the
  staged pool and preserve the high-word sentinel;
- a point-mode pair must restore both signed coordinate words and derive the
  terrain height through the normal target resolver;
- the order/subtype pass may replace this target pair, but it must not infer
  target kind from zero/nonzero values.

This preserves the established logical-reference rule (pool indices are
16-bit, slot zero is null, and there are no generations) with the exact
sentinel discriminator. [Established; [01 §6.1], [04 §3.5], [06 §3.1]]

The record also does not serialize the COB Aim completion, callback/thread
identity, or the synchronous muzzle-piece query result. The low flag bit is a
saved latch bit, but it is not proof that a pending asynchronous callback can
be resumed. Aim/COB restoration must therefore restore script state and
re-establish callbacks in the script pass; it must not synthesize a completion
or a ready result from this byte alone. The muzzle/piece used for projectile
initialization is queried from the live unit/COB state when needed and is
recomputed after restore. [Established omission; [04 §5.3], [06 §3.3], [06
§4.1]]

**Validation and fix-up.** A transactional reader accepts exactly three
records within the enclosing `0xB8` unit form and rejects an overrun or any
record boundary other than `0x18*n`. It resolves weapon identity from the
unit definition before applying the active byte and validates that a nonzero
target-unit index names a staged, live slot when the later target pass elects
unit mode. It preserves reload, yaw, pitch, ammo, and flags without clamping;
the normal tick gates perform their retail checks. It masks the final flag
byte to bits 0..4 and never uses its high three bits. The deterministic order
is: resolve definition links; copy the persistent scalar fields; retain the
target payload and latch; allocate every referenced unit; let order/subtype
records install target kind and logical links; restore COB/Aim callbacks; then
publish the staged unit. [Established for the copies and ordering; Supported
inference for the transactional rejection boundary]

**Boundary probes.** Implementation tests should:

- save units with three distinct slot definitions, including `noweapon` and
  two different active weapons sharing the same active byte, proving that the
  byte at `0x08` is not identity;
- use reload values `-1`, `0`, and `32767`, desired yaw/pitch at both signed
  boundaries, ammo `0`, `1`, and `255`, and all eight flag-byte patterns to
  verify signed/byte preservation and low-five-bit masking;
- compare a unit target and a ground target while holding the low word equal,
  proving that `0x8000` in the high word selects unit mode and every other
  value selects point mode;
- vary the Aim callback from pending to completed and vary the queried muzzle
  piece while holding the slot record constant, proving these transient values
  are not restored from the 24-byte record;
- feed exact `0xB6`, `0xB8`, short, and long enclosing unit records, asserting
  that only the enclosing compatibility rule accepts `0xB6` and that a partial
  weapon record never crosses into the next slot.

The former R-SAVE-UNIT-01 statement that the complete weapon-slot schema was
unknown is superseded by this map. The remaining Unknowns are limited to the
semantic names of the two copied payload words, bits 2/3 of the saved flags,
and the exact reader-side policy when a saved active byte disagrees with the
unit definition. None of these permits treating the active byte as identity
or restoring a muzzle piece from the record. [Unknown]

**Validation and compatibility-length rules.** A valid unit image requires
Units version `0x11`, a nonpositive unit count to produce no units, exact
`0xB8` records for reconstructable units, nonzero unique stable IDs, a
definition name present in the catalog, an owner in the configured player
domain, and a forced slot inside the owner's canonical slice. The loader must
also reject occupied slots, per-definition limits, unresolved references, and
any exact-length mismatch before commit. No additional coordinate or health
range is invented here; map geometry, signed health, and status-bit validation
belong to their owning passes once their wire mapping is closed.

`0xB6` is not a truncated `0xB8` and is not a legacy unit record. Retail
accepts it only as a compatibility/null-dispatch form: the stable ID is
cleared and no unit is restored. A transactional reader must retain that
distinction: accept the form as “no unit,” never pad it to `0xB8`, and reject
all other lengths. [Established]

**Deterministic staging and commit order.** A future loader should use this
order, with no live-session mutation before validation completes:

1. Validate bank/account version, unit count, numbered-box lengths, definition
   names, stable-ID uniqueness, owner/slice membership, and all reference IDs;
2. stage every `0xB8` base record by stable ID and reserve its forced pool slot;
3. recursively resolve the two reference fields depth-first and stage cargo or
   attachment relationships;
4. restore accessory/mobile state and the embedded weapon-slot payloads;
5. restore numbered order/subtype records and link their logical targets;
6. restore script snapshots, piece state, stacks, waits, signals, and
   callbacks in the script reader's established internal order;
7. run registration and derived occupancy/placement fix-up;
8. publish visibility and atomically commit the staged image.

The exact point at which a particular script subcomponent is installed is
still bounded by the script reader; its known ordering constraint is that
piece state is installed before the stack is made live. [Established]

**Implementation probes and residual Unknowns.** The following small probes
are sufficient to close the fields an implementation must otherwise keep
opaque:

- write one unit while varying only X, Y, Z, bank, heading, pitch,
  construction remaining, and signed health; assert the established positions
  and orientation words survive a save/load round trip and the definition's
  maximum health is used as the cap;
- compare a live unit and a dying-but-not-yet-cleaned unit to map the lifecycle
  bits in the packed status word, then compare a completed and unfinished unit
  for the build/edge words;
- use IDs whose numbered records are reversed, then use absent, duplicate, and
  cyclic references, to verify ID-based depth-first resolution and the
  transactional rejection boundary;
- vary one weapon slot, cached footprint cell, counter, and callback edge at a
  time; retain unknown words byte-exactly until each consumer is identified;
- feed exact `0xB6`, `0xB8`, short, and long boxes under version `0x11`, plus
  version `0x10`, to assert the compatibility/null and whole-account gates.

The remaining Unknowns are the boolean at `0x27`, the semantic names of the
two weapon-slot payload words and flag bits 2/3 (see R-SAVE-WEAPON-01), the
exact cached-cell packing, counter/deadline meanings, the individual byte
meanings, the full capability/pending mask, the packed placement of the
second state byte inside `0xB4..0xB7`, and the high status bits that can
contain writer-side stale values. None blocks fixed-slot allocation or the
minimally live base fields above. They are not safe to infer from width or
adjacency; an implementation depending on one must carry the raw bytes
through the staged image and settle its mapping with the corresponding
field-isolation probe. [Unknown]

### Feature records

Features are partitioned into normal, animated, and 3D records. Loading maps
save-local feature type names to the current feature catalog and recreates
footprints through the normal placement service.

### Player records

Each `Player%i` account serializes one player slot. Wire type is the bank item
type; runtime width is the destination field width:

| Item | Wire type | Runtime width | Missing/wrong default |
|---|---|---|---|
| `Energy`, `Metal` | double | f32 (narrowed) | 0.0 |
| `TotalEnergyProduced`, `TotalMetalProduced`, `TotalEnergyConsumed`, `TotalMetalConsumed`, `EnergyWasted`, `MetalWasted` | double | f64 | 0.0 |
| `PlayerEnergyStorage`, `PlayerMetalStorage` | double | f32 (narrowed) | 0.0 |
| `AddPlayerStorage` | integer | bit 0 of halfword | 0 |
| `Kills`, `Losses` | integer | low signed 16 bits | 0 |
| `UpdateTime` | integer | i32 | 0 |
| `WinLoseTime` | integer | i32 | 0 |
| `DisplayTimer` | integer | i32 | 0 |
| `Controller` | integer | u8 | 0 |
| `Logo` | integer | low byte | 0 |
| `Side` | integer | low byte | 0 |

`UpdateTime` is the player's economy settlement deadline — an absolute tick
value compared against the global tick and advanced by thirty when due; see
document 05 for the settlement cadence. Deadlines are persisted as absolute
values and are not re-seeded on load; battle init seeds `UpdateTime`,
`WinLoseTime`, and `DisplayTimer` to the current tick for all active slots
before load overwrites them. The consumer census for the sibling fields is
closed: `WinLoseTime` has **no reader anywhere in the image** beyond the
save writer (the earlier display-candidate attributions were targeting and
feature helpers that do not reference it) — it is persisted verbatim for
compatibility and otherwise inert; `DisplayTimer` is the **HUD resource-rate
refresh deadline**: a presentation function advances it by thirty whenever
it trails the global tick and refreshes the four displayed resource-rate
floats from the player record.

The `Alliances` box is exactly 11 bytes and loads only when the selected box
size is exactly 11; afterwards the player's own self-alliance byte is forced
to 1 while other bytes keep their initialized values.

All `Player%i` scalar restoration is gated on a successful 28-byte
`Players/GameTime` read: a short or absent read processes zero `Player%i`
accounts (after the human-player byte has already been applied). Before main
battle init, an independent pre-pass visits all ten player accounts and
restores `Controller` (default 0) so controller types exist for setup.

## Load process

Loading proceeds as reconstruction, not pointer restoration:

1. validate the `HAPIBANK` header, bank tag, version, and optional pool decompression; select and parse the requested account(s) via the pool and per-account headers;
2. verify version, game type, and required media/content conditions;
3. restore campaign, mission, map, difficulty, player, and game metadata;
4. reset or allocate a fresh battle world;
5. load terrain and catalogs required by the saved mission;
6. restore player state;
7. recreate units in stable slots and restore scripts/queues;
8. fix cross-unit and cross-subsystem references;
9. restore features, mapping, terrain metal, mission, and meteor state;
10. rebuild derived occupancy, registrations, lists, and presentation caches,
    including the radar image;
11. enter the normal battle start path.

Invalid or incompatible input produces an invalid-save error. Campaign and
multiplayer media requirements have distinct failure messages.

Partial-load policy: there is no shadow world, no validation pass, and no
transactional commit/rollback anywhere in the load graph. Preflight catches
only coarse container/mode/mission failures — magic/version/tag, game type
outside {1,2}, CD absence, mission-selection failure, and full-bank reopen
failure. Once battle restoration begins, each account mutates live initialized
state in fixed order (Players, Camera, Features, Metal, PlayerFeatures,
Mapping, Units, Meteor, campaign trigger state); later corruption skips or
leaks according to each family's documented behaviors (exact-size box guards,
unchecked reads, partial writes, allocation leaks), and earlier subsystems'
mutations always remain. The battle-load dispatcher marks restoration
attempted and returns success unconditionally: missing accounts are created
empty and each subsystem's own defaults govern the result.

## Scheduler and random state in saves

The `Players` `GameTime` binary box is exactly the first 28 bytes of the
scheduler timing block. The writer copies those 28
bytes verbatim; the loader requires at least 28 bytes and continues only if that
amount is available (larger boxes have trailing bytes ignored).

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

The battle-loading state initializes the timing block before the worker starts, but
the battle-setup initializer overwrites all 28 bytes. No direct code between the read and the
later scheduler reset modifies the block, so a stale saved clock anchor is
effective on the first budget pass and can produce a capped five-tick catch-up,
zero for a negative delta, or zero when pause bit 0 was saved set. The top-level
summary also writes `globalTick` as integer `Game Time` presentation metadata.

Bounded census of the complete random-state writer graph and every bank
account/item/box API invocation site shows no serialization of the Park-Miller
simulation RNG or the per-thread CRT RNG. The load path reseeds both families (the
simulation seed from the sum of the low and high parts of
`QueryPerformanceCounter`, XORed with a fixed constant and forced odd per
document 01 §7.1, and the CRT seed from local/system time) before any bank
restoration, so no bit-identical RNG continuation exists even in the same
process. Consequently, loading a save resumes logical world state and the 28-byte
timing block, but not bit-identical future random consumption.

## Multiplayer saves

The multiplayer boundary is established: the local save callback performs no
game-type/host/authority guard before writing; load preflight explicitly
accepts Gametype 2 (multiplayer CD prompt, Summary values restored, battle
loading entered); and no transport/session state — peer membership,
retransmission history, packet queues, host identity — is serialized anywhere
in the save graph. A loaded multiplayer save is therefore file-permitted but
is not a serialized live network session: no path exists by which loading
re-establishes a DirectPlay session or resumes live multiplayer play.

Residual: upstream multiplayer GUI authority/menu enablement — whether all
peers can reach the save callback (local absence of a guard does not prove
reach) remains open.

Unit order state is saved with units rather than as a raw network-history
snapshot.

## Replay

### Bounded absence

Static searches found no dedicated replay file extension, replay browser GUI,
or literal replay feature vocabulary in the retail executable. This is a
bounded absence over known strings and function indexes, not proof that no
hidden or externally driven capture path can exist.

### What can be stated safely

The retail engine already has deterministic command frames, numbered network
packets, mission/save reconstruction, and movie/image capture. Those mechanisms
could support re-execution or external recording, but the decompilation has not
established a named retail command-log replay format or an in-game replay
player.

A clean-room implementation of this executable should therefore:

- implement save/load independently of replay;
- preserve deterministic command framing for networking;
- leave a replay file format and UI as unknown unless direct retail evidence is
  recovered;
- not label movie capture as simulation replay.

## Session end and reporting

Victory, defeat, resign, disconnect, or fatal integrity state transitions the
battle to end-game screens. Campaign uses mission-end and progression screens;
multiplayer uses score/report/end-multiplayer screens. Statistics are derived
from authoritative player state and saved/reported using the interface and
localization systems.

The closed prefix of the post-battle order: the latch bits drive the session
router into the results/postgame state, whose handler drains the network at
entry, then (multiplayer) copies the last game frame, stops the music, runs a
ten-unit timed display, performs the campaign CD check, writes campaign
progress and enters the authored end-mission screen (outcome art, the full
mission list ordered by W/L marks with the next unplayed mission selected,
difficulty refresh), plays the outro movie, and finally shows the
score/statistics screen with per-player stat bars (kills, losses, energy and
metal produced and wasted, score) before returning to the front-end router.
The score values come from the score helper's display array, which multiplies
kills by the kill multiplier and elapsed ticks over 1800 by the time multiplier
with truncation and a zero clamp. Resign and host-loss latch ended-without-win
directly (the end-game dialog callbacks and the peer-loss path, respectively)
and can override an armed victory, while ordinary victory/defeat run through
the shared four-count countdown. The exact tick at which simulation stops is
the session's switch out of the live battle state; the residual is only the
precise presentation sequencing of the overlay transitions inside the
front-end router.

**Correction: the post-battle message-box wording is superseded.** The
results surface is the authored `ENDMSN.GUI`/`endmsn.gaf` family. Its outcome
copy is selected from the authored `victory` or `defeat` frame and its
available route is the authored `Start` control when campaign progression has
a next mission, otherwise `MainMenu` [07 §11] [08 "Progression"].

## Required implementation invariants

A conforming clean-room implementation must preserve these established
properties:

- Sessions use ten stable player slots.
- Campaign, skirmish, multiplayer, mission, and load modes select different
  schemas and start paths.
- Networked advancement is soft-paced by oldest remote progress (900-tick lead
  threshold, 3600 clamp); no per-tick input bundle or wait-for-all-peers
  barrier exists.
- Packet admission masks key to three session states: battle loading (bit 1),
  live battle (bit 2), every other state (bit 0).
- Mission time values used by triggers are converted at thirty ticks per
  second.
- Missing victory and defeat lists receive executable-defined defaults.
- Victory/defeat trigger queues are polled in the local player's once-per-30-tick block for every mission type: campaign polls victory first (AND) then defeat (OR); skirmish and multiplayer poll the defeat queue first, gated on the local commander marker, then the victory queue. No alliance merge is performed by any poll.
- The simulation random stream is never synchronized across peers: each process reseeds Park–Miller from its own `QueryPerformanceCounter` sample (per doc 01 §7.1) and the CRT stream from its own clock at battle entry; no packet carries or writes a seed.
- Scenario unit reconstruction is a load path, not the strategic AI.
- Computer-player strategic construction selection, profile `plan`/`weight`/`limit` handling, economy-mixed weighted reservoir choice, per-type class-vector recomputation with outer gate bound 30 and x87 truncation, full manager deadline graph and dispatch gates, direct manager-group writer/classifier for six destinations, eco toggle with eighty percent gate, and placement origin step, radius, selector, and two helper contracts are established (the metal score is the footprint per-cell metal-byte sum; water legality is the yard path's waterline band); the AI score inputs (player economy aggregates and strategic class vectors), hard gates, pressure arithmetic, three-way mix, cumulative weighted reservoir selection, profile plan/weight/limit defaults, and manager-before-refresh-before-ledger update order are established (R-P0-05); the manager task-vector slot order, direct group-writer semantics, classifier branch order, and wave merge strictness are established (R-P0-04); the class routine's weapon reads are the `DAMAGE/default` word divided by 40 and the `range` word divided by 100; any additional distinct group writer or transport geometry remain bounded negative or inference. [P0-01] [P0-02] [P0-03] [R-P0-04] [R-P0-05]
- Multiplayer transport is DirectPlay in the retail process.
- Packet dispatch starts with a one-byte type and uses a fixed handler table.
- Network input is consumed before the rest of an authoritative tick.
- Future/out-of-order frames are buffered within bounded per-peer queues.
- Send batching, pacing, acknowledgements, and retransmission exist above
  DirectPlay.
- A 31-tick receive window must not be conflated with a proven universal input
  delay.
- The placement barrier sleeps in the lobby/start path, not the normal tick.
- Map, terrain, feature, and resource checksum checks precede or accompany
  network play where recovered.
- No rollback or full-world snapshot resynchronizer is established.
- Participant state pushes overwrite without comparison; divergence handling
  is the kick/chat flows only.
- Save writes truncate in place and ignore post-open write errors; partial
  loads mutate live initialized state without rollback.
- Saves are `HAPIBANK` bank/account typed-item and binary-box logical serialization, not TDF text or memory images.
- Loading reconstructs identities and fixes pointers/links from logical data.
- Network retransmission history is not an established saved subsystem.
- Dedicated retail replay support remains a bounded unknown.

## Missing and unknown

The following work remains before this category is a complete retail design:

- Recover UI-level names for session states 0–4 (their behavior, transitions,
  callbacks, and admission-mask classes are established above), and close any
  provider-specific DirectPlay transitions outside the reviewed callbacks.
- Campaign progression latch (countdown four then roughly one per second, win and lose bits, scoring), MissionList as allocation tag with first-gap termination, language-prefixed mission names, VFS first-provider-wins, wind CRT draws before simulation, and registry versus bank split are established; the all-missions bit's consumer is the single-player panel's AnyMsn toggle and its exact listbox-selection effect remains the bounded residual (the earlier "no reader at all" is superseded). [P0-05] [lane 08 AllMissions]
- Campaign and mission catalog ordering across multiple VFS providers (mount order loose then patched then expansion then base then CD-ROM, first-provider-wins, MissionList allocation tag, contiguous Mission sections until first gap, language-prefixed names) are established. [P0-05]
- Derive exact planet, panorama, rotation, briefing, narration, glamour, and
  optional-media fallback rules.
- Inventory every mission-global key, type, default, clamp, and consumer. The
  census is completed: `maxunits` (campaign branch only, default 200, stored
  as the 16-bit unit-limit word that also seeds the AI half-capacity
  global), `numplayers` (stored as an 80-byte string, presentation only, no
  numeric consumer), `mapping`/`lineofsight` (campaign-mode LOS defaults
  merged at battle entry), and `commanderDeath` (a lobby value, not a
  mission key; consumed by the skirmish defeat gate and respawn path) are
  closed; `killmul`/`timemul` defaults are 0.0. The allocator enforces
  per-definition limits from definition fields (see document 05), so the
  mission value and the physics stores are distinct.
- Placement order for mission units, start-position specials, and features (strict units then specials then features) and the sparse two-pass unit spawner with null gaps, plus attacker and feature stamping convergence, are established; cargo and feature successor handling beyond the shared stamping service, delayed creation countdown (parsed but no reader), and script attachment beyond the immediate attach verb remain bounded negative; the meteor scheduler's phase (after wind jitter and projectiles) is established. [P0-04] [P0-06]
- Player-to-start-position selection for skirmish is established for the CRT Fisher–Yates shuffle versus simulation jitter split, eligibility predicate (non-zero slot, control one through three, terminator not newline), schema Network 1 through 4 trial with largest-so-far fallback and difficulty permutation, and commander interior jitter with degenerate no-advance; the transport-selection policy beyond generic move orders and any distinct naval or air geometry remain unknown. [P0-04]
- Trigger evaluator bodies and direct-mission polling are closed: owner gating is a player-index compare (no alliance merge), the tick site is the local player's once-per-30-tick block, campaign polls victory before defeat (AND then OR), direct type-2/3 missions poll defeat before victory with the defeat queue gated on the local commander marker, and disconnect/resign precedence is closed (host-loss and resign latch ended-without-win directly and can override an armed countdown; the countdown itself keeps per-type precedence). The remaining ownership Unknown is the exact executable branch by which a populated lobby skirmish uses its rule words without making injected/authored OTA triggers authoritative. The value-zero all-units survival sweep used by Nanolathe is likewise a Supported inference pending that executable trace. Record shapes, the vtable map, and the per-condition saved fields remain established.
- Meteor spawning, motion, damage, scoring, and persistence are closed (see
  Meteor showers above); only presentation of meteors outside the world
  renderer remains a rendering-lane question.
- Restriction-flag disposition is closed: `UseOnlyUnits` resolves into the
  campaign useonly area, `Immunity` is consumed at unit creation, and
  mission-critical/AI-ignore/AI-priority-target/build-priority/initial-group
  are parsed but unread (bounded negative).
- Computer-player strategic construction selection, profile plan/weight/limit handling, economy-mixed weighted reservoir choice, per-type class-vector recomputation (single-byte init 40 plus 20 versus three-byte triple, outer gate bound 30, x87 constants and truncation), full manager deadline graph (construction plus 90, eco plus 30, waves plus 300 and plus 150, explore plus 30 plus 900, rally plus 30 plus 150, one empty slot), direct manager-group writer/classifier for six destinations, eco toggle with eighty percent gate, and placement radius and helper selection (fixed-point 16.16 origin step, radius plus 160 capped, strict less-than selector opposite inferred, exhaustive patch sorted versus scatter 30 trials with four draws per trial, product limit, no fall-through, yard validator required; the metal score is the footprint per-cell metal-byte sum, and water legality is the yard path's waterline band) are established [P0-01] [P0-02] [P0-03] [R-P0-04]; AI score fields and update order — player economy aggregates, hard gates, pressure and mix arithmetic, cumulative weighted reservoir selection, class-vector compilation, manager-before-refresh-before-ledger ordering, and profile gates — are established [R-P0-05], with the weapon-field TDF identities closed (`DAMAGE/default` and `range` feed the damage and range reads; `reloadtime` is not read by the class routine) and the strategic half-capacity state writer as the explicit residual (R-P0-05 §8); the manager task-vector slot order, direct group-writer semantics, classifier branch order, and wave merge strictness are established [R-P0-04]; the AI RNG inventory is complete (throttle draw bounded 300, eight strategic-constructor draws, explore and rally body draws; the "any other bound is a bug" sentence is retracted); any additional distinct group writer and transport geometry remain bounded negative, and inert mission fields plus definition `ai_limit` remain closed as bounded negative.
- Remaining computer-player expansion, scouting, targeting, retreat, repair, reclaim, transport, naval, and air policies: the complete task-dispatch surface is bounded-negative over the seven vtable addresses (one contiguous family; six task classes plus the intentional null class; each class has exactly two virtual slots and only the first is ever invoked; the only vtable writers are the task and manager constructors). No distinct transport/naval/air/repair/reclaim/retreat policy table exists; such behavior can only act through the ordinary order service. The semantic names of several definition flag bits and any behavior only a dynamic trace could reveal remain the largest AI unknowns. [lane 08 task tables]
- Reconcile every lobby slot-state value, host privilege, ready flag, blocked
  state, edit permission, and start condition.
- Complete DirectPlay provider/session enumeration, lobby handoff, connection
  setup, addressing, password, and teardown.
- Decode the payloads of the packet types the in-game receiver forwards whole
  to a subsystem, by following each into its handler. The type byte, the
  per-type fixed length, the admission mask, the dispatch roles, and the field
  layouts of every inline-decoded type are established.
- Recover the lobby receiver's switch, which owns the types whose admission
  mask excludes the battle-loading/live-battle states.
- Type frame numbers, sequence numbers, acknowledgement fields, checksums,
  sender identity, and wrap behavior within those payloads.
- Specify guaranteed versus ordinary delivery for every packet family.
- Derive the exact local-input scheduling delay separately from future-frame
  retention and delayed gameplay queues.
- Close send pacing, batch flush, retransmission timeout, retry count, queue
  overflow, and round-trip adaptation.
- Specify duplicate, stale, future, oversized, unknown-type, and wrong-sender
  packet handling beyond the established malformed-custom-payload hang path.
- Resolve the semantic identity of the pacing-scan progress dword: remote-peer
  reported progress versus earliest pending order time are competing readings
  of the same code (soft pacing itself is established).
- Determine command authority for host, local player, remote player, computer
  player, observer, pause, speed, sharing, and game termination.
- Complete map, resource, economy, and other hash contents, cadence, payloads,
  and mismatch handling; trace the zrb orchestrator, recover packet `0x27`'s
  trailing-integrity-data producer algorithm, and recover the exact
  throttle-acknowledgement format in the participant-state receiver.
- Initial simulation-random seed agreement between peers: **closed as a bounded absence.** The simulation seed setter has exactly one call site in the image (battle entry, from the low-plus-high `QueryPerformanceCounter` sum per doc 01 §7.1); the CRT seed has two (process startup and battle entry); the packet registry contains no seed-exchange packet and no handler writes either seed state. Each peer reseeds from its own clock, so peer streams are not identical — consistent with the engine's known desync behavior.
- Recover reconnect, late join, spectator join, and temporary transport-loss
  behavior, or establish their bounded absence. Host-authority migration is
  closed as deterministic selection of the numerically greatest DPID among
  eligible roles.
- Complete the byte layout inside each bulk binary box beyond the now-established lengths, descriptor groups, and sampled field maps (unit 184-byte `0xB8` records with three 24-byte embeddings, 58-byte order records and subtype codes, `Feature Type Names` 128-byte names, normal 8/animating 10/3D 26-byte feature records, radar preview header). Many unit/order words still lack retail source names and are typed by width and exact runtime offset. The container, account inventory, and scalar entry names are established.
- Close script-thread and operand-stack persistence beyond the established record: the COB persistence family is byte-exact (identity word, eight-snapshot blocks with one zeroed word per block, stack words, per-piece records whose two gap words leak stack bytes into per-piece virtual setters); the residual is the semantic naming of the individual snapshot words (local versus static versus control), which belongs to the script/COB lane.
- Path, effect, projectile, and AI-history persistence: closed as a bounded absence for standard battle saves (no writers in the reachable save graph; the projectile pool is reset before reconstruction; path working state is recomputed; effects and AI history are omitted). Trigger, radar-preview, mapping, terrain-metal, feature, attachment, and order persistence are established.
- Indirect serialization of the simulation/CRT random states: **closed** — the writer census over the complete reachable save graph finds zero writers for either state; the scheduler's fractional carry is the only indirect persistence (the 28-byte block). [p1-13 §5]
- Consumers of `WinLoseTime`/`DisplayTimer` beyond their save keys: **closed** — `WinLoseTime` has no reader anywhere in the image (persisted verbatim, inert); `DisplayTimer` is the HUD resource-rate refresh deadline (advanced by thirty when trailing the global tick, refreshing the four displayed rate floats).
- Close upstream multiplayer GUI authority/menu enablement for saving (whether
  all peers can reach the save callback) and the upstream save-name
  edit-character policy and code page.
- Campaign progress saved outside battle `.sav` files is inventoried: the in-memory W/L array (one byte per mission slot, `'U'` unplayed), the Summary `Thumbs` item carrying it across BetweenMissions saves, the registry difficulty/games/all-missions mirrors, and the BetweenMissions flag itself; the residual is only the AnyMsn toggle's exact listbox-selection effect.
- Bounded replay searches are extended to dynamically built file names, debug modes, and media/capture paths: the whole-image sweep finds only movie capture (`MOVIE%03i` pattern) and screenshot vocabulary; no replay file name, extension, or vocabulary exists. The replay absence bound now covers these paths.
- If a replay path is found, derive its framing, initial snapshot, command
  timing, random state, seek behavior, version checks, and UI.
- Session-end ordering: the post-battle prefix is closed (network drain at entry, multiplayer frame copy, music stop, ten-unit timed display, campaign CD check, campaign-progress write and authored end-mission screen, outro movie, score/statistics screen with per-player stat bars, front-end return); the residual is only the precise presentation sequencing of the overlay transitions inside the front-end router.
