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

The executable parses these fields into fixed records before unit creation; the parser's key order, defaults and reader census are closed in [R-TRIG-01 §9] (`InitialGroup` is read as an integer and packed into the flag byte's low nibble; a unit-block `Kills` key has no reader).
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
records. The builder probes the eighteen condition keys of `[GlobalHeader]`
in one fixed chain — the eleven victory keys in the order of the victory
table under "Victory and defeat triggers", then the seven defeat keys in the
order of the defeat table — and appends one record per present key to the
owning array, so a mission holds at most one trigger per key and a queue's
order is the vocabulary order, never the authored order [R-TRIG-01 §2].
Record sizes fall in **nine** buckets: 12 bytes (`KillEnemyCommander`,
`DestroyAllUnits`, `CommanderKilled`), 16 (the two timers,
`AllUnitsKilled`), 20 (`KillAllMobileUnits`, `AnyUnitPassesX/Z`), 44
(`CaptureUnitType`), 48 (`KillUnitType`, `UnitTypeKilled`), 50
(`BuildUnitType`), 52 (`UnitTypePassesX/Z`), 54 (`KillAllOfType`,
`AllUnitsKilledOfType`) and 64 (`MoveUnitToRadius`). **Correction
(2026-08-29, RWU-08-5).** The previous text listed "seven established
buckets — `0x0C` flag-only, `0x10` timer, `0x14` boundary, `0x30`
string+count variant A, `0x32` string+count variant B, `0x36`
canonicalizing string shape, and `0x40` radius"; it omitted the 44-byte
capture record and the 52-byte type-gated boundary records, and its
"string+count" labels were wrong — only the 48-byte record carries a count
(see [R-TRIG-01 §2] for the field layout of every bucket).

Every record leads with a pointer to a six-slot table — poll, unit-removed
notification, capture notification, unit-created notification, save, load —
followed by the *Satisfied* (completed) and *Celebrated* (cue played)
flags; the ten conditions that walk a player's unit slice carry a second
one-slot table for their per-unit visitor [R-TRIG-01 §2].

The mission object owns two fixed sixteen-entry arrays for victory and
defeat conditions, each with its count beside it. An empty array receives an
injected default `DestroyAllUnits` victory or `AllUnitsKilled` defeat
record at build time, and again at poll time if it is found empty
[R-TRIG-01 §6].

Timer triggers store `authored seconds × 30` in a 32-bit signed word
[R-TRIG-01 §2].

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

Wind for the briefing screen is drawn from the CRT stream before the simulation consumes either value: speed is a uniform integer in the authored minimum to maximum inclusive range, and the second draw's low six bits seed the display's jitter countdown (not a direction — corrected in [R-CAMP-01 §2]). The authored wind bounds are stored in the mission object; the two drawn values are briefing-screen display state only [01 §7.3]. Missing wind keys default to zero. [P0-05]

Missing optional media can fall back or suppress presentation without aborting battle entry; missing data required to identify the mission or its terrain is a load error. A planet value that matches no table entry selects table entry 0, Green planet ([R-CAMP-01 §2]; the earlier "leaves the previous art unchanged" was wrong).

### Progression — Established with bounded negative and unknown residual [P0-05]

The executable contains campaign selection, mission list, briefing, end-mission, score and report, and between-mission state. The end-of-mission latch is established: the trigger queues are polled once per 30 ticks in the local player's slice only, with victory as an AND across its queue and defeat as an OR, and victory evaluated first so simultaneous completion resolves as victory. When either side completes, the latch arms a countdown at four that decrements roughly once per second before a latch word is written with separate bits for ending, won, and lost; the lose path clears the win bit it would otherwise share. The score helper that writes the campaign result combines kills multiplied by a kill multiplier and the global tick divided by 60 multiplied by a time multiplier, both truncated, summed and clamped at zero, and stores a W or L character per mission slot ([R-CAMP-01 §7]; the earlier "divided by 1800" was wrong — the divisor is 60). [P0-05]

Persistence is split. Difficulty, the skirmish lobby fields, and three registry mirrors — difficulty, a games flag, and an all-missions flag — are kept under the installed software registry path and written back immediately when absent so a first run fully populates the registry (the earlier "two registry mirrors" count is corrected: the enumeration below lists three). One mirror holds the difficulty value masked to 16 bits and cycled by the difficulty controls; a second mirror holds a games flag gated on a display mode, and a third holds an all-missions flag as a single bit. The per-mission W and L characters in memory and the between-missions bank account named Summary that carries a BetweenMissions flag are written through the bank system, not the registry. The bank's timing block that persists scheduler state is a 28-byte binary box; larger boxes have trailing bytes ignored, and the bank's string pool, header, and account enumeration are not range-checked. Campaign continuation on load inspects the saved Summary account for the BetweenMissions flag to decide between fresh mission spawning and battle reconstruction — the battle-entry gate routes the flag-present case to the fresh spawner and the flag-absent case to battle restoration, never the reverse. [P0-05] [lane 08 BetweenMissions polarity]

VFS first-win, first-gap termination, language-prefixed names, wind draws from the CRT stream before simulation, the latch bits and scoring arithmetic, and the registry versus bank split are established. The all-missions registry bit's consumer is located: the single-player panel shows its "Any Msn" control and keeps the bit when set, a toggle callback (inert while the panel's text field holds the "DRDEATH" easter-egg string) flips the bit, toggles the control, and writes the bit back to the registry, and a writeback helper persists it. The mission-list build always counts every mission and neither the new-game panel nor the end-mission screen filters the list by the bit — the exact listbox-selection effect of the toggle remains the bounded residual (the earlier "no reader anywhere" is superseded). The provider-specific enumeration order beyond mount order is host-dependent but deterministic for a given filesystem, and the exact narration and glamour fallback beyond silent suppression is not closed. [P0-05] [lane 08 AllMissions]



### Closed — campaign catalog: file grammar, enumeration and the mission list [R-CAMP-01 §1] (2026-08-29)

**Scope.** RWU-08-3. This section states, at implementable precision, how the
campaign catalog is enumerated and how a campaign file becomes a mission list.
Everything below is **Established** by static trace unless marked otherwise;
the raw trail lives outside the repo.

**The campaign object.** One heap object (the *campaign record*) holds the
session kind word (1 campaign, 2 skirmish, 3 multiplayer — the discriminant
[08 "Mission type dispatch"] reads), the selected campaign name (256 bytes),
nine 256-byte *media slots* numbered 0–8, the parsed campaign TDF, the parsed
mission TDF, the current mission index and a companion word that every index
write clears to zero. The slots are:

| Slot | Content | Directory | Extension | Reader |
|---|---|---|---|---|
| 0 | campaign file path | `camps` | `TDF` | catalog open |
| 1 | terrain file path | `Maps` | `TNT` | terrain loader; the slot's byte size is also stored (a missing slot stores 0) |
| 2 | briefing text | `camps\briefs` | `TXT` | briefing text region (§3) |
| 3 | narration sound | `camps\briefs` | `WAV` | briefing narration (§3) |
| 4 | mission hint | `camps\hints` | `TXT` | **no reader in the image** (dead slot) |
| 5 | outcome glamour | *(empty)* | `PCX` | end-of-mission outcome art (§7) |
| 6 | use-only units list | `camps\useonly` | `TDF` | build-restriction loader [05] |
| 7 | AI profile | `ai` | `txt` | computer-player profile loader [08 "Computer-controlled players"]; falls back to `default` when the key is empty |
| 8 | glamour sound | `camps\briefs` | `WAV` | end-of-mission glamour display (§7) |

**Slot path construction.** A slot is filled by the *media resolver* from
`(directory, name, extension)`: when `name` is empty the slot is cleared
(and for slot 1 the byte size is 0). Otherwise the path is
`<directory>\<name>`, the text from the **last `.`** onward is removed (so an
authored `Ac01hint0.txt` or `tabtcore.pcx` loses its extension), and
`.<extension>` is appended. When the language buffer of [02 §3] is non-empty
the resolver first tries `<directory>-<language>\<name>.<extension>` and keeps
it only if the VFS can open it; otherwise it falls back to the plain form. The
stock install carries `camps\briefs-french`, `-german`, `-italian`,
`-spanish` for exactly this rule (asset census). The general path joiner used
elsewhere in this unit applies the same language-suffix-then-plain rule with
`.<extension>` appended after stripping the name's own extension. For slot 5
the directory is empty, so the slot holds `\<glamour>.PCX`; the outcome-art
loader (§7) skips the leading separator and re-joins under `bitmaps\glamour`.

**Enumeration of campaigns.** The new-game panel builds its campaign list by
enumerating `camps\*.TDF` through the VFS (mount order and first-win as in
"Campaign discovery" above). Two 256-byte-per-entry name arrays are
allocated for the count; every enumerated file is opened as TDF and admitted
to the visible list only when it has a `[HEADER]` block whose `campaignside`
equals, case-insensitively, the local player's side name **or** the literal
`ALL`. Files without `[HEADER]` are skipped silently. The returned count is
the number admitted; the list order is the enumeration order.

**Opening a campaign.** The catalog opener copies the requested name into the
record, resets all nine slots to empty, and — when the name is non-empty —
resolves slot 0 as `camps\<name>.TDF` and parses it. A parse failure raises
the message box `The requested campaign file, %s, does not exist.` (`%s` is
slot 0's path) and re-enters the opener with an empty name, which leaves the
record with no campaign and no mission list. On success the mission index and
its companion word are zeroed and the mission loader runs for index 0.

**Mission list.** The list builder counts blocks named `MISSION%d` from 0
until the first missing index (the earlier text's "Mission zero, Mission one"
wording is corrected: the block names are `MISSION0`, `MISSION1`, … with no
space, and the decimal is unpadded). It allocates `count × 256` bytes tagged
`MissionList` and fills entry *i* with the block's `missionname` read through
the language-prefixed accessor (`<Language>missionname` first, then
`missionname`, 256-byte buffer); a block with neither key yields the literal
`Error -- Unnamed Mission`. A campaign with zero blocks returns count 0 and no
array. Stock campaign files carry `[HEADER] campaignside=ARM|CORE` and
`[MISSIONn] missionfile=…; missionname=…; Germanmissionname=…;
Frenchmissionname=…; Italianmissionname=…; Spanishmissionname=…;` (asset
census).

**Index helpers.** *Has-mission(i)* is `i < count` with the same contiguous
count. *Advance* (`next mission`) succeeds only when `count > index + 1`; it
then increments the index, zeroes the companion word and reloads. *Set(i)*
stores `i`, zeroes the companion word and reloads. Neither helper tests the
W/L marks — progression by outcome is decided by the end-mission screen
(§7), not by the record.

**Look-up by name.** The save loader restores a campaign mission by name: for
session kind 1 it walks the mission list comparing `missionname` values
case-insensitively and sets the first match's index; no match leaves the
record on index 0 and returns failure. For kinds 2 and 3 the name is an OTA
path handled by the skirmish loader [R-SKIR-01 §1].

**Mission loader, campaign branch (kind 1).** With `MISSION%d` for the current
index: a missing block raises `The requested mission file, %s, does not
exist.` (`%s` is the block name). Otherwise `missionname` (language-prefixed)
is stored as the mission's display name; `missionfile` (plain key) missing
raises `Old TED format no longer supported!`; the OTA path is joined as
`Maps\<missionfile>.OTA` (extension replaced); an unparsable OTA raises
`Hey, joker!  There is no mission defintion for this mission: %s` (sic); a
parsed OTA without `[GlobalHeader]` raises `Hey, joker!  Mission file %s is
corrupt`. All four are message boxes at the same 480-pixel width and abort
the load (return 0). The `maxunits` key of `[GlobalHeader]` is read with
default 200 into the session's unit-cap word here, and slot 1 becomes
`Maps\<missionfile>.TNT`.

**Common tail (all kinds).** After `[GlobalHeader]` is selected the loader
fills the media slots in this order: `brief` → slot 2, whose file is then
read whole into an allocated `Briefing` buffer (VFS size + 1, NUL-terminated;
a missing or empty file leaves the buffer null, so the briefing text region
stays empty); `narration` → slot 3; `missionhint` → slot 4;
`glamour` → slot 5; `glamoursound` → slot 8; `UseOnlyUnits` → slot 6. Then
`mapping` (default 0) and `lineofsight` (default 0) are stored in the
mission's session-option words, the two companion option words are forced to
1 and 0, and `memory`, `numplayers`, `Planet` (each up to 128 bytes, default
empty) are copied verbatim into the record. `nomovie` (default 0) is stored
in the session's outro-suppression word. `missiondescription` (default `No
description available`) is stored once, as the translation-table entry for
its upper-cased text when one exists, else as the raw text. Wind, gravity, tidal,
lava, sea-level, water-damage, `killmul` and `timemul` (floats, default 0.0)
follow, then the schema selection of [08 "Schema choice"]; its failure path
is a single message box `No suitable schema type in mission file!` (the
`Map error` text the string census lists is pushed beside it and never read
— the box helper takes one argument). The schema branch also reads
`HumanMetal`, `HumanEnergy`, `ComputerMetal`, `ComputerEnergy` (ints,
default 0, stored as floats), `SurfaceMetal`, `aiprofile` → slot 7
(`default` when empty), and the meteor keys [08 "Meteor showers"].

**Confidence.** Established throughout; the slot table, extension replacement,
language-suffix probe, `ALL` side wildcard and unpadded `MISSION%d` grammar
are read directly from the code.

### Closed — briefing screen: planet table, panorama, rotation, text, narration [R-CAMP-01 §2] (2026-08-29)

**Correction.** "Planet and briefing selection" above said a planet value that
matches no table entry "leaves the previous art unchanged rather than
aborting" and that the wind draws include a "direction" of "the low six bits
of a CRT draw". Both are wrong: an unmatched planet selects **entry 0 (Green
planet)**, and the second draw seeds a **countdown**, not a direction (see
"Wind display" below). Doc 01 §7.3 already records the draws as display-only;
this section fixes their meaning.

**Planet table.** The briefing screen builder holds four parallel 15-entry
tables (plus a terminating null). Index order and contents:

| # | `Planet` value (case-insensitive) | briefing GAF | panorama sequence | rotation sequence |
|---|---|---|---|---|
| 0 | `Green planet` | `Greenbrief` | `GreenPan` | `GreenRotate` |
| 1 | `Archipelago` | `Archibrief` | `ArchiPan` | `ArchiRotate` |
| 2 | `Wet Desert` | `WDesertbrief` | `WDesPan` | `WDesertRotate` |
| 3 | `Desert` | `Desertbrief` | `DDesPan` | `DDesRotate` |
| 4 | `Lava` | `Lavabrief` | `LavaPan` | `LavaRotate` |
| 5 | `Red Planet` | `Marsbrief` | `MarsPan` | `MarsRotate` |
| 6 | `Lunar` | `Lunarbrief` | `LunarPan` | `LunarRotate` |
| 7 | `Metal` | `Metalbrief` | `MetalPan` | `MetalRotate` |
| 8 | `Lunar2` | `Lunar2brief` | `Lunar2Pan` | `Lunar2Rotate` |
| 9 | `Ice` | `Icebrief` | `IcePan` | `IceRotate` |
| 10 | `Lush` | `Lushbrief` | `LushPan` | `LushRotate` |
| 11 | `Slate` | `Slatebrief` | `SlatePan` | `SlateRotate` |
| 12 | `Water World` | `Waterbrief` | `WaterPan` | `WaterRotate` |
| 13 | `Acid` | `Acidbrief` | `AcidPan` | `AcidRotate` |
| 14 | `Crystal` | `Crystalbrief` | `CrystPan` | `CrystalRotate` |

The lookup walks the name column from 0 comparing the mission's `Planet`
text (the 128-byte record copy) case-insensitively; the first match wins.
When the walk reaches the null terminator without a match the index is
**0**. The stock OTA census shows one `planet=Urban` and nine empty values,
all of which therefore brief as Green planet. Spelling asymmetries
(`WDesertbrief`/`WDesPan`/`WDesertRotate`, `Desertbrief`/`DDesPan`/`DDesRotate`,
`Crystalbrief`/`CrystPan`/`CrystalRotate`) are retail's and must be
reproduced. The stock install carries `anims\<x>brief.gaf` for every row
(asset census).

**Lunar rewrite.** Before the walk, if the `Planet` text equals `Lunar` and
the local player's side index (0 Arm, 1 Core) is non-zero, the character `2`
is appended in place, so a Core player on a `Lunar` mission briefs with row 8
(`Lunar2`). Nothing else rewrites the planet text.

**What the tables drive.** The briefing GAF (`<x>brief`, resolved by the
window's animation-directory prefix with extension `GAF`) is loaded as the
window's animation set; on load failure nothing below happens and the
previous GAF stays. Then the `PANORAMA` gadget's animation pointer is set to
the sequence named by the panorama column (frame index 0) and its custom
draw callback to the *panorama scroller*; the `PLANET` gadget's animation
pointer is set to the rotation-column sequence (frame index 0) with the
*planet rotator* callback, and the rotation sequence is registered with the
frame-advance timer. A missing sequence leaves that gadget without animation.

**Panorama scroller** (per draw): a horizontal strip is drawn by tiling the
panorama sequence's frames left-to-right starting at `x = gadget.x − scroll`
and continuing while frame index ≤ frame count (indices wrap modulo the
count), where `scroll` advances by 1 pixel every time the presentation clock
passes a deadline of `now + 2` presentation units and wraps to 0 when it
reaches the sum of all frame widths. The gadget's frame word is set to
`(now / 3) mod frameCount` before drawing. After the strip, frame
`localSide` (0 Arm, 1 Core) of the sequence named **`Panmask`** in the same
GAF is drawn at the window origin (0, 0) — `Panmask` is the per-side
foreground overlay, which answers its role. The presentation clock is
`GetTickCount × rate / 1000` where `rate` is the configured presentation
frame rate; every "unit" in this section is one such tick.

**Planet rotator** (per draw, rate-limited to one step per 25 ms of wall
clock): when the narration has stopped playing and the `SHUTUP` control is
visible, it is hidden; then if the current presentation tick differs from the
last one seen, the rotation sequence advances one frame and the frame is
blitted centred in the `PLANET` gadget.

**Wind display.** At screen entry two CRT draws are taken (this is the
[01 §7.3] pair): `speed = rand() % (maxwindspeed − minwindspeed + 1) +
minwindspeed`, then `countdown = rand() & 0x3F`. Each draw of the
`SOLARSYSTEM` region decrements the countdown; when it drops below 1 the
speed changes by `rand() % 5 − 2`, is clamped to
`[minwindspeed, maxwindspeed]`, and the countdown is re-seeded
`rand() % 63`. The region shows the label `Wind Speed` (translated) as
`"%s : %d"` at (x+80, y+20) and `Gravity` as `"%s : %.1f"` at (x+80, y+40).
These values never reach the mission record or the simulation; they are
display state only, and every draw here is a **CRT** draw taken while the
front end runs (no simulation is ticking).

**Briefing text and narration.** After the gadgets are bound, the text
loader reads slot 2 (`camps\briefs\<brief>.TXT`) into a scrolling text
region (`TextRegion`, `MOREBAR` pages it); the `SOLARSYSTEM` and `TextRegion`
gadgets are given font index `localSide + 1`. Slot 3 (narration) is started
with a 60-scaled-tick (two-second) **delay** at full DirectSound volume
unless the session is in the live-battle state (corrected 2026-08-29 per
[03 R-AUD-02 §1]: this sentence previously read "at volume 60" — the 60 is
the timer period, the volume argument is 0 = full scale); `SHUTUP` is a
toggle — stage 0 stops the stream, any other stage restarts it (visible while
it plays). The `Start` control runs the campaign-CD check
(§5) and, when it passes, re-mounts the archive set, stops the narration
and routes the front end into battle entry; on
failure it shows the campaign-CD message box and stays. `PrevMenu` stops the
narration and returns to the previous panel. The whole screen is `MSNBRIEF.GUI`
with background bitmap `mbrief<side>`; the stock GUI's gadgets are exactly
`PrevMenu`, `Start`, `PLANET`, `PANORAMA`, `MOREBAR`, `TextRegion`, three
`FONT` entries (`smlfont`, `armfont`, `corefont`), `SOLARSYSTEM`, `SHUTUP`
(asset census).

**Confidence.** Established. The slot-2 text buffer, the 25 ms rotator gate,
the scroll and mask arithmetic, and the countdown semantics are read from
the callbacks.

### Closed — new-game panel: `CampaignKnob`, `MissionsKnob`, `playanygame4`, `newcampaign4x` [R-CAMP-01 §3] (2026-08-29)

The single-player new-game panel is `NEWGAME.GUI` (gadgets `PrevMenu`,
`Start`, `Campaign`, `CampaignKnob`, `MissionsKnob`, `Missions`, `Side0`,
`Side1`, `SIDENAME`, `Difficulty`, `Arm`, `Core`, `TEXT`; asset census). The
front-end router opens it in two modes selected by one argument.
*Correction ([07 R-FE-01 §3], 2026-08-29):* this paragraph previously said
mode **0** (new campaign) came "from the single-player menu's `NewCamp`
control". Both `NewCamp` and `AnyMsn` pass mode **1** (any mission — the
play-any layout with both lists); the mode-0 campaign-only layout is reached
only from a shell phase that this executable never enters. The results
screen's `Missions`/`Start` route (§7) also passes mode 1. All entries run
the campaign-CD check first (§5).

*Background and knobs.* Mode 0 counts `camps\*.TDF`: more than two files
loads background `newcampaign4`; two or fewer loads `newcampaign4x` and sets
a **fixed-campaign** flag. Mode 1 loads `playanygame4` and clears that flag.
In mode 1 the `Campaign` list and `CampaignKnob` are moved to y = 308 with
height 48 and the `Missions` list height is set to 62; `CampaignKnob` and
`MissionsKnob` are the scroll knobs of the two lists and carry no data.

*Population.* The `Side0`/`Side1` sequences have their offsets zeroed; the
`Difficulty` control's state byte and label (`Easy`/`Medium`/`Hard`) follow
the difficulty word. When the fixed-campaign flag is clear or the mode is 1:
`Campaign`/`CampaignKnob` are shown, the campaign list is built by the §1
enumeration for the local side (a change callback is installed only in mode
1), and in mode 1 `Missions`/`MissionsKnob` are shown and the mission list is
built by opening the campaign selected in `Campaign` and running the §1 list
builder. The initial focus is `Missions` (mode 1), else `Campaign` (flag
clear), else `Difficulty`.

*Side selection.* `Side0`/`Arm` sets the local side index 0 and the two
player slots' side bytes to (0, 1); `Side1`/`Core` sets 1 and (1, 0); each
rebuilds the campaign list for the new side (and the mission list in mode 1)
and plays the `SideSelect`/`SideSelect2` cue. `Difficulty` cycles
0 → 1 → 2 → 0 in the difficulty word.

*Start.* `Start` (and, in mode 1, selecting a `Missions` row or, in mode 0,
selecting a `Campaign` row) runs the campaign-CD check (§5), re-mounts the
archive set, **resets the progress marks to 25 × `U`** (§7), then chooses the
campaign name: the `Campaign` selection when the fixed-campaign flag is
clear, else the literal `Core Campaign` if the local side byte is non-zero,
else `Arm Campaign`. The mission index is the `Missions` selection in mode 1,
else 0. Set(index) (§1) runs; on success the player colours are set to (0, 1),
the registry mirrors are written [R-SKIR-01 §1], and the front end proceeds to
sub-state 15 (mode 0) or 16 (mode 1) — the briefing. A failed load stays on
the panel.

*`AnyMsn` bit.* The `AllMissions` registry bit of "Progression" is not read
by this panel or by the results screen; the **any-mission** mode is entered
by the `AnyMsn` control regardless of the bit, and its list carries every
mission of the campaign. Whether anything else reads the bit remains the
"Progression" residual.

**Confidence.** Established.

### Closed — the developer warp entry is dead code [R-CAMP-01 §4] (2026-08-29)

Two routines read `<module directory>\Warp.ini` through the private-profile
API: section `WARPLEVELS`, key `warp%dcampaign` (string, default `default`,
256-byte buffer) and key `warp%dmission` (integer, default 0). Each sets the
session kind to 1, opens the campaign by that name (§1) and calls Set(mission)
(§1); on success it sets the front end's *mission-loaded* and *skip-menu*
flags so the router jumps to the briefing. One routine takes the level number
as an argument; the other formats the key names with no argument at all
(whatever is on the stack). **Neither routine has a caller anywhere in the
image**: the warp entry is unreachable in the retail executable. It bypasses
nothing because it runs nothing; a reimplementation must not expose it.
Established (call-graph query, both directions).

### Closed — the CD gate family [R-CAMP-01 §5] (2026-08-29)

**The check.** One *disc check* routine takes a selector — 0 `Campaign`,
1 `Multiplayer`, anything else fails — and returns a drive letter on success
or 0 on failure. Its first statement tests a **build-time constant** in the
initialized data segment; when the constant is non-zero it returns the
sentinel `'.'` immediately. In the retail image that constant is **1**, so
in this build every disc check succeeds without touching a drive, and none of
the message boxes below can appear. The dormant body, for completeness: it
walks CD-ROM drive letters (`GetDriveType == DRIVE_CDROM`, starting after the
previously returned letter, `A`..`Z`), parses `<letter>:\TOTALA.ID` as TDF and
accepts the first drive whose `[Contents]` block has a non-zero integer under
the selector's key; a companion mode flag replaces the drive walk with the
fixed letter `h`. The stock install carries no `TOTALA.ID` (asset census), so
the dormant path would fail on this install.

**Archive re-mount.** Every successful gate is followed by the *archive
re-mount* routine, which is likewise gated on the same constant: with the
constant set it re-mounts `rev31.GP3`, then `*.CCX`, then `*.UFO`, then up
to ten `*.HPI` from the module directory, then `<letter>:\*.hpi` for each
CD-ROM drive (the directory-scan order of [02] applies). The movie path
joiner uses the same constant: with it set, movies resolve to
`.\\<dir>\<file>` (the `'.'` sentinel formatted as the drive letter — a
relative path), never to a disc.

**Sites and what a failed check would do** (all Established; unreachable in
this build):

| Site | Selector | On failure |
|---|---|---|
| single-player menu `NewCamp`, `AnyMsn` | 0 | message box `Please insert the Campaign CD (Disc 2) and try again` (translated), stay |
| single-player menu `Skirmish` | 1 | `Please insert the Multiplayer CD (Disc 1) and try again`, stay |
| new-game panel `Start`/row select | 0 | campaign message, stay |
| briefing `Start` | 0 | campaign message, stay |
| in-battle options `RESTART` | 0 (kind 1) / 1 (kind 2) | matching message, stay; in kind 3 the control does nothing at all |
| results state 4 (campaign only) | 0 | opens `CDCHECK.GUI`; its `OK` re-checks and continues to state 5 on success, else re-shows the campaign message |
| end-mission `Start`/`Missions` | 0 | shows the campaign message **but continues anyway** — the mission is loaded regardless (retail quirk) |
| main menu `INTRO`/credits | 0 then 1 | `Please insert a Total Annihilation CD and try again` when both fail |
| main menu `MULTI` | *(none)* | tests that `maps\multiplay.tdf` (language-suffixed directory first) parses; else the multiplayer message |
| online-service button | *(online.dll)* | `Please insert the Installation CD (Disc 1) and select%s"%s" again.` is produced by the online library's button handler — out of scope (networking) |

The gate reads no registry value and writes nothing on failure besides the
message box. Established.


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

Commander fallback placement for eligible slots draws from the simulation stream: two draws per eligible slot for X and Z interior jitter, each bounded by map dimension in cells minus 160, then offset by 80 cells and scaled to 16.16 fixed point. (**Corrected 2026-08-29:** the bounds and offset are in world units, and the jitter exists only on the multiplayer path; the skirmish stamp has no jitter and a `StartPos` miss is fatal — [R-ENTRY-01 §5], [R-ENTRY-01 §10].) When the bound is zero or negative the helper returns zero without advancing the stream, so tiny maps produce no jitter draws and the position collapses to the 80-cell margin. If a matching StartPos is found by numeric suffix lookup, its stored short coordinates are shifted to fixed point and overwrite the jitter; otherwise the jitter is kept. Missing or extra StartPos entries are handled gracefully: the lookup scans for the requested suffix and, when not found, leaves the jitter untouched and can emit a diagnostic without crashing; surplus positions beyond the player count are simply unused. [P0-04]

### Unit creation and InitialMission timing — Established [P0-04] [P0-06]

Mission-unit creation during battle entry uses a two-pass sparse array. The loader allocates a created array sized by the unit count and zeroes it. Pass one walks the unit records in placement order from zero upward: it validates the unit type name exists, adjusts the authored player number from one-based to zero-based with a zero-to-one fixup, checks the same eligibility predicate used for start positions — a failure formats `Player number %d invalid for unit %s` into the modal fatal channel and terminates the process (**correction 2026-08-29:** the earlier "emits a diagnostic … but still proceeds" is retracted, [R-TRIG-01 §9]) — then runs a position fixup helper and the normal unit allocator. The allocator scans for the lowest free pool slot in the owning player's slice and can fail; on failure the entry stays null and no unit is created. Successful creation copies the immunity high bit into a runtime status bit, scales health by percentage, and copies the authored **facing angle** into the unit's heading word (the earlier "copies build priority" is retracted — build priority is parsed but never copied or read; the placement record's angle word is the copy source). Pass two walks the same order again and invokes the InitialMission interpreter only when the record carries a non-null script string and the corresponding created entry is non-null; the interpreter tokenizes the string and queues orders. A per-record creation countdown field is parsed but has no reader in the image; no delayed queue, cargo loop, or separate attachment pass exists beyond the immediate attach verb. Recursive reconstruction for linked or carried units uses the same allocator path for saves but not for fresh mission spawns. [P0-04] [P0-06] [lane 08 facing angle]

Timing is fixed: the mission loader's common tail runs, then for multiplayer a barrier pumps network state and sleeps fifty milliseconds until peers arrive, then start-position assignment stamps slots, then commanders are created with the jitter described above and resources are granted as floating-point metal and energy, then camera focus is chosen, then the sparse two-pass spawner runs, then visibility and mapping are rebuilt, then the first authoritative tick runs. (**Superseded 2026-08-29:** the visibility rebuild precedes the spawner and the per-player phase is primed at tick 0 before the first pump — [R-ENTRY-01 §6–§9], correction [R-ENTRY-01 §10].) The InitialMission strings are therefore interpreted after every mission unit exists at its fixed-point position but before any creation script, movement, or visibility publication for that tick, and they never take a tick of their own. BetweenMissions handling for saves uses a bank account named Summary that carries a BetweenMissions flag; **the polarity is settled by the battle-entry save-blob gate: when the flag is absent (the in-battle marker) the loader runs the battle restoration dispatcher and skips the fresh spawner; when the flag is present the loader skips battle restoration and runs the fresh spawner — the campaign continuation rebuilds the mission from the authored mission file.** The earlier contradictory phrasing (flag-present → restore player and feature state from the bank) is retracted: a BetweenMissions save contains only the Summary account, so the flag-present route could not restore a battle; the gate's two branches verify this. [P0-04] [P0-05] [lane 08 BetweenMissions polarity]

### Feature and terrain convergence — Established [P0-04] [P0-05]

Terrain-provided and mission-provided feature records converge on the same feature stamping service during the loading worker. Deterministic load order matters because occupancy, successor state, geothermal registration, and save identifiers depend on it. The order within the worker is units first, then specials, then features, and the two-pass unit spawner preserves placement order rather than pool order when allocation gaps occur. [P0-04]

### Placement and start barrier — Established [P0-04]

Multiplayer initialization enters a barrier after content and player state are prepared. The user interface reports that it is waiting for other players. The loop pumps network state and sleeps for fifty milliseconds between checks. When all required peers reach the barrier, the executable reports synchronization complete and allows authoritative ticks. The normal simulation tick is not driven by this sleep; it is confined to lobby, placement, and barrier behavior. The same barrier is not used for single-player campaign skirmish entry. [P0-04]

### Closed — who runs battle entry: the loading screen, the worker thread, and the handoff [R-ENTRY-01 §1] (2026-08-29)

Established by RWU-08-7 from a full read of the battle-entry orchestrator,
its one caller, the loading-screen state, the world-rebuild routine and every
routine they call before the first tick. Sections §1–§9 are the ordered
sequence; §10 lists the corrections to earlier text; §11 what stays open.

**Established — three actors.** Battle entry is not one function on the main
thread. It is:

1. **The loading-screen state** (main thread, one call per frame). On its
   first frame it resets the scheduler block — scaled-clock anchor ← now,
   pending ticks ← 0, global tick ← 0, slew counter ← 0 — sets the
   commander-death countdown to −1 (the "unarmed" value of [R-SKIR-01 §3]),
   shows `loadgame2bg`, fills the ten-slot participation table (a slot
   participates when its record is live and its controller is 1 or 2; a
   second table marks slots whose lobby record carries the *watching* bit),
   then starts **a worker thread** whose body is the orchestrator below. A
   failed thread start is fatal with the verbatim modal
   `Unable to start the loading thread!`. Every later frame of the state
   only pumps the network (multiplayer), redraws the six progress bars
   (`Textures`, `Terrain`, `Units`, `Animation`, `3D Data`, `Explosions`,
   each from a percent byte the worker writes), and sleeps 200 ms.
2. **The loading worker thread** runs the whole orchestrator of §2–§8 —
   seeding, world rebuild, placement, spawning, visibility — and ends by
   setting the *battle ready* bit of the session flag word.
3. **The handoff frame.** The loading state's next frame sees the ready bit
   and, still inside the loading state, runs (in this order) four
   presentation helpers this unit did not trace (doc 07; none is reachable
   from the tick executor), then **the battle pump once** (§9 — the first
   authoritative ticks happen here), then the interface reset (doc 07),
   and only then
   installs the in-battle state (state number 6 with the battle-state
   handler), zeroes the progress bytes and starts CD audio. The first tick
   therefore runs before the battle state exists and before any input is
   read.

**Established — the thread has its own C-runtime stream.** The worker is
started through the C-runtime thread starter, which allocates a fresh
116-byte per-thread block whose `rand` word is initialised to `1`. The
CRT stream is per-thread ([01 §5.1]); consequences are in §2.

**Established — multiplayer barrier plumbing (OOS, ordering only).** For
session kind 3 the worker sets the *waiting for peers* bit after the world
rebuild and spins (50 ms sleeps) until the main thread — which polls the
peer-ready predicate from the loading state — sets the *peers ready* bit.
The barrier of "Placement and start barrier" above is this pair of bits;
nothing else waits.

### Closed — seeding, the session words, the save gate and the setup record [R-ENTRY-01 §2] (2026-08-29)

The worker's orchestrator runs these steps in this order, before anything
else:

1. **Simulation stream seed.** `QueryPerformanceCounter`; seed =
   `((low32 + high32) XOR 0x66e29572) OR 1` ([R-CORE-02], [01 §7.1]).
   The Park–Miller state is a process global, so this is the battle's stream.
2. **CRT stream seed** from the time-of-day helper — **into the worker
   thread's own block** (§1). The main thread's CRT state, seeded once at
   process start and consumed by the front end, is **not** touched by battle
   entry. Every CRT draw the *tick* makes (wind interval, meteor scheduler,
   victory-timer arm, camera shake, strips, sound variants — [01 R-DET-01
   §4/§5]) is made on the main thread and therefore continues the front-end
   stream; every CRT draw the *worker* makes (the explosion-frame builder of
   §3 and the skirmish shuffle of §5) comes from the freshly seeded thread
   stream and is discarded with the thread. Correction in §10.
3. **Global tick ← 0** (again; the loading state already zeroed it).
4. **Session words by kind** (kind getter of "Game-mode selection"):
   - *Kind 1 (campaign)*: local-authority flag ← 0; commander-death rule ←
     the mission's rule word (0, [R-SKIR-01 §4]); visibility mode word bits
     `2 ← losType & 1`, `0 ← mapping & 1`, `1 ← lineofsight & 1` from the
     mission-global words ([R-SKIR-01 §4], [03 R-VIS-01 §1]); then the
     **unit-restriction loader** runs: it opens resource-path slot 6
     (`UseOnlyUnits`, [02 R-MAP-01 §1]); when the file exists it clears the
     *available* bit of every catalog definition from index 2 upward, then
     sets it again for every definition whose name matches a `[name]`
     section of the file (case-insensitive compare). A missing file leaves
     every definition available. The available bit is the same one the unit
     allocator tests ([05 R-SHARE-01 §8]).
   - *Kind 2 (skirmish)*: session unit limit ← lobby unit limit;
     local-authority flag ← 1; commander-death rule and the three mode bits
     from the setup record ([R-SKIR-01 §2]).
   - *Kind 3 (multiplayer)*: session unit limit ← lobby copy; local-authority
     ← 1; pause bit cleared; if this peer is not the host, pump until the
     host slot, its colour and its name are known; resolve the map from the
     host's lobby record ([02 R-MAP-01 §1]); then local-authority ← host bit
     13, commander-death ← host bits 11–12, mode bits 0–2 ← host option
     byte bits 0–2, session unit limit ← host limit word.
   The "local-authority" flag of [R-SKIR-01 §2] has exactly one reader:
   the chat commit callback ([07 §5 Chat]), which ORs a routing bit into
   the message's target mask when the flag is set — it is a chat-routing
   flag, not a simulation authority. Campaign leaves it clear.
   *Correction (2026-08-29, [R-OOS-01 §2]): the reader is right but the
   destination is wrong — the OR goes into the `+` command dispatcher's
   route word (bit 2 = the cheat table), not the message's recipient mask.
   The word is the cheat-enable gate: skirmish 1, campaign 0, multiplayer
   the host's `Cheat Codes` bit.*
5. **The save gate, first visit.** If a save bank is open and its `Summary`
   account lacks `BetweenMissions` (the in-battle marker, "Timing is fixed"
   above): for each of the ten slots the `Player%i` account (`i` = slot
   index, 0-based) is looked up; when present the setup-record row's
   controller ← its `Controller` item (default 0) and the slot's controller
   byte ← its low byte; when absent both ← 0. Then, **kind 2 only**, the
   skirmish row count ← `max(current, 1 + highest row index whose
   controller is 1 or 2)` and the row→player conversion of [R-SKIR-01 §2]
   runs (alliances, names, local player). Kinds 1 and 3 restore their
   slots later from the `Players` account (§8).

### Closed — the world rebuild: every allocation, in order [R-ENTRY-01 §3] (2026-08-29)

**Established.** One routine (the *world rebuild*) runs next, once per
battle, for every kind and for loads alike. Its call order, each step with
what it allocates or writes:

| # | Step | What it does |
|---|---|---|
| 1 | message ring clear | the F12 message ring is emptied ([07 R-CAM-01 §2]) |
| 2 | interface scratch reset | eleven interface words are zeroed or cleared (identities are doc 07's — §11); the pause/interrupt word's bits 0 and 11 are cleared; bits 7, 8 and 9 of an interface flag word are cleared |
| 3 | HUD light-bar reset | the six HUD progress words are zeroed and the `LIGHTBAR` frame handle re-fetched |
| 4 | sound state reset | the active-sound list is drained (each entry's handle released) and the per-category "recently played" table zeroed |
| 5 | texture table | the `textures` directory is enumerated, `logos.GAF` skipped, every other GAF loaded into the `TEXTURE_PTRS` table; the *Textures* percent byte advances per file and ends at 100 |
| 6 | feature TDF catalog | every file under `features` is parsed into the feature catalog container ([05 R-FEAT-01 §1/§2]) |
| 7 | a flag word ← 1 | (a bare store; its reader was not traced — §11) |
| 8 | strip table | ten 16-byte strip vectors allocated ([03 R-STRIP-01]) |
| 9 | projectile pool | `WEAPON ARRAY`: 300 records × 107 bytes allocated and zeroed; live count ← 0 ([06 §5]) |
| 10 | weapon table reset | the 256 weapon records: name byte zeroed, slot byte stamped ([06 R-DMG-01 §5]) |
| 11 | **map load** | the terrain loader ([02 R-MAP-01 §6], [03 R-TERR-01 §1]): TNT, plot memory, surface-metal seed, feature-name table, feature stamping from the TNT and the OTA `[features]`, tile set, LOS tables, metal-byte seeding ([05 R-FEAT-01 §3/§7]) — the *Terrain* percent byte |
| 12 | camera reset | the camera block reset that preserves the scroll byte ([07 R-CAM-01 §10]) |
| 13 | unit catalog | the FBI/3DO catalog compile ([02 R-CONTENT-01]) — the *Units* percent byte |
| 14 | download-menu table | the `download` directory's build-menu files are compiled ([02 §1]) |
| 15 | **unit pool** | `UNIT MEMORY`: `limit × 10 + 1` records, per-slot slices ([05 R-SHARE-01 §7], [04 §2.3a]) |
| 16 | feature successor pass | `featuredead` / `featurereclamate` / `featureburnt` links resolved by name, parsing a missing successor on demand ([05 R-FEAT-01 §2]) — the *Animation* percent byte |
| 17 | feature TDF container freed | the parsed TDF trees of step 6 are released (the compiled catalog stays) |
| 18 | path class layer | ([04 R-DOC04-B]) |
| 19 | wind seed | wind change interval ← 5000, wind deadline ← 0, then the wind routine is called once: its gate is `deadline < globalTick`, i.e. `0 < 0`, false — **no draw**, the wind-active flag ← 0 ([05 R-PROD-01 §3], [R-CORE-02]) |
| 20 | renderer scratch | `TEMP XFORM PTS` (2400 bytes), `TEMP PROJECTED PTS` (1600), `ASSEM PTS` (160) |
| 21 | **scheduler block** | scaled-clock anchor ← now; global tick ← 0; kind 3 only: requested speed ← 10 and active speed ← 10; fractional carry ← 0. (Kinds 1 and 2 keep whatever the speed words already hold — §11.) |
| 22 | meteor scheduler | active ← 0, next strike ← the authored value, weapon resolved by name ([01 R-CORE-01], [06 §6.5]) |
| 23 | minimap surface | ([03], [fmt tnt]) |
| 24 | **per-player reset** | for every slot whose controller byte is non-zero: the economy/statistics block is zeroed (stocks, incomes, expenditures, the sharing thresholds and flags — [05 R-P0-01]), the per-player timers ← global tick (0), the storage-bonus flag cleared, six selection/target words reset (four to 0, two to 0xffff), a per-player byte map of `(cellW/2)·(cellH/2)` entries (rounded up to 8) re-allocated and zeroed, the `SQUADS` table (ten 32-byte squad records) allocated; then **unless** the controller is 3 (remote), the **AI record** is constructed — the ten task records with their initial thresholds ([08 R-AI-01 §1]) and the strategic state, whose constructor makes the **eight simulation draws** of [R-DET-01 §4] ("AI player setup") — and the per-side classifier table entry is built. Humans get an AI record too; only remote peers do not. Then the AI profile is loaded from resource slot 7, falling back to `ai\default.txt` ([R-AI-01 §12]), and for every computer-controlled slot the difficulty tables are applied. |
| 25 | target/threat registry | the per-battle registry object (`AISearch touched mapentries` bitmap sized to the cell grid) allocated ([06 §3.1]) |
| 26 | **explosion-frame builder** | the `CalcedExplosion` tables: table 0 = 12 frames of radius 64 down by 4; table 1 = 15 frames from radius 128 stepping `(16−128)/15 = −7`; table 2 = 15 frames from 200 stepping `(32−200)/15 = −11` (C integer division); one CRT draw per generated pixel on the **worker thread's** stream (391,606 draws, [06 R-WFX-01 §6]); the explosion pool's 300 records and 1,800 debris records (the six debris animation names cycling) are initialised ([04 R-COB-04 §4/§5]); the *Explosions* percent byte goes 20 → 50 → 100 |
| 27 | command tables | the three battle command tables registered ([07 R-CAM-01 §6]) |
| 28 | counters | every slot's *units ever created* ← 0; the game-over latch bits cleared again; the ally-icon byte ← 0xff; the thirty statistic words and the two frame counters zeroed |

Steps 24 and 26 are the only ones that draw: eight simulation draws per
constructed AI record (slot order 0..9, humans included), and the CRT
pixel draws — which, being on the worker's stream, never reach the tick.

### Closed — start positions and commanders, per kind [R-ENTRY-01 §5] (2026-08-29)

(§4 is folded into §1's barrier paragraph.) After the world rebuild:

**Kind 3 (OOS, ordering and draws only).** Barrier (§1). Mode bits and the
commander-death rule are re-copied from the host record. Then for each slot
`0..9` whose record is live and whose controller is 1 or 2, **two
simulation draws** `sim(mapWidthWorld − 160)`, `sim(mapDepthWorld − 160)`
form `x = (draw + 80) << 16`, `z = (draw + 80) << 16`, `y = 0` — the
extents are the TNT width/height × 16, i.e. world units, the same words the
commander-respawn placement of [R-SKIR-01 §3] writes straight into unit
positions (the "cells" wording of "Randomization for skirmish starts" is
corrected in §10). The draws happen **before** the watching test, so a
watching slot still consumes them. For a non-watching slot the `StartPos`
lookup by the slot's assigned position byte overwrites `x,z` on a hit and
leaves the jitter on a miss with **no diagnostic**; the side's commander is
allocated there; the storage-bonus flag is set and the bonus words ←
`float32(max(hostShort × 100, 200))` for energy and metal. The camera
centres on the local human's position, or on the map centre with mode bits
0–1 cleared when the local player is watching.

**Kind 2 (skirmish), no save file.** `StartLocation = 0` → the eligible
list, the 50/50 gate draw when fewer than three are eligible, and the
Fisher–Yates walk exactly as [R-SKIR-01 §2] states, on the **worker's** CRT
stream; `StartLocation ≠ 0` → identity. Either way the stamp helper runs for
every eligible slot `i` (record live, controller 1/2/3, side ≠ 10) with
position `p_i`, in slot order:

1. side and colour copied from the setup row into the lobby record;
2. storage-bonus flag set; energy bonus ← `float32(max(row.energy, 200))`,
   metal bonus ← `float32(max(row.metal, 200))` ([05 R-ECO-01 §4]);
3. the `StartPos` lookup with `p_i`: the specials array is scanned in
   authored order for the first record of type *start position* whose
   stored number equals `p_i`. **The stored number is the authored suffix
   minus one** — `StartPos1` is stored as 0 (a suffix of 0 stays 0; a
   record with no digit after `StartPos` takes a running counter instead).
   So slot `i` under identity placement takes `StartPos<i+1>`. A hit gives
   `x = short(X) << 16`, `y = 0`, `z = short(Z) << 16`.
4. a miss is **fatal**: `Error: Could not find start position number %i on
   the map!` (the `%i` is the zero-based number, one less than the label)
   through the modal-fatal helper — message box, then process exit code 1
   ([R-TRIG-01 §9]). No jitter fallback exists on this path; the earlier
   "leaves the jitter untouched and can emit a diagnostic without crashing"
   described the kind-3 path only (§10).
5. the side's commander (the side record's commander name resolved to a
   definition) is allocated at `(x, 0, z)` through the common allocator
   with the same three trailing arguments as mission spawning
   (`1, 1, 0` — [05 R-SHARE-01 §8])
   — the allocator's two simulation draws ([04 §2.3b]) occur here, one
   commander per eligible slot in slot order;
6. if `i` is the local player, the camera is placed at
   `(x_int − viewW/2, z_int − viewH/2)` ([07 R-CAM-01 §12]).

Then the **resource grant** of [R-SKIR-01 §2] (stock ← `float32(row)`, no
floor) — its first of two runs. No simulation draw is made by the stamp or
the grant themselves.

**Kind 2 with a save, kind 1, kind 3:** no stamps, no grant here.

### Closed — the campaign path: spawner, InitialMission, camera, trigger reset [R-ENTRY-01 §6] (2026-08-29)

After placement (all kinds) the **visibility rebuild of §7 runs first**.
Then:

- no save and kind ≠ 1 → skip to §8;
- save present and in-battle (no `BetweenMissions`) → the **battle
  restoration dispatcher** ([R-SAVE-02 §11]) runs instead of the spawner:
  `Summary.maxunits` → lobby unit-limit copy (note: the pool was sized in
  §3 from the copy as it stood *before* this restore — a loaded save's
  pool uses the current `totala.ini [Preferences]` `UnitLimit` ([01 R-PLAT-01 §3]), and the restored value only
  reaches the next battle; §10); then `Players`, `Camera`, `Features`,
  `Metal`, `PlayerFeatures`, `Mapping`, `Units`, `Meteor`, trigger records,
  in that order; the *restored* flag is set; skip to §8;
- kind 1 without a save, or any kind with a `BetweenMissions` save → the
  **two-pass spawner**, then the **campaign camera**.

**The spawner, exactly (Established, precision added to "Unit creation and
InitialMission timing").**

1. `created[]` ← `max(unitCount, 0)` pointers, each set to 0.
2. Pass one over placement records `0..unitCount−1`:
   - definition ← catalog binary search by `Unitname`; a miss stores 0 and
     continues (no diagnostic);
   - `p ← Player − 1` (the loader already turned 0 into 1); eligibility =
     `p < 10` and slot `p` live and controller ∈ {1,2,3} and side ≠ 10;
     failure → `Player number %d invalid for unit %s` (the `%d` is the
     zero-based `p`) through the modal-fatal helper, exit code 1;
   - **position fixup**: for a non-mobile definition the authored `x,z`
     are snapped to the footprint grid — `cell = (coord − footprint·2^19 +
     2^19) >> 20`, `coord' = (footprint + 2·cell) << 19` per axis (i.e. to
     the centre of a footprint-aligned 16-unit cell), and `y` ← the terrain
     height probe at that cell (`<< 16`); a mobile definition keeps the
     authored `x,y,z` untouched;
   - allocation through the common allocator with the fresh-build arguments
     (two simulation draws on success, [04 §2.3b]; refusal — slice full or
     per-definition limit — stores 0, no diagnostic);
   - on success: immunity bit ← the record's flag bit 7 (into unit status
     bit 15); health ← `maxHealth × HealthPercentage / 100` (16-bit,
     truncating); heading ← the record's angle word; `created[i] ← unit`.
3. Pass two over the same records: when the record's `InitialMission`
   string is non-null and `created[i]` is non-null, the interpreter of
   [04 §3.6] runs on that unit.
4. When `unitCount < 1` the trigger set's *victory-condition countdown*
   word is cleared — the only side effect of an empty `[units]` block
   ([R-TRIG-01 §6]).
5. `created[]` freed.

**Campaign camera.** The first *start position* special with stored number
0 (`StartPos1`) places the camera at `(X − viewW/2, Z − viewH/2)`, marks the
camera *jumped*, copies the target into the glide words and clears mode bit
3 ([07 R-CAM-01 §12]). No such special → the camera keeps the world-rebuild
reset position; no diagnostic.

### Closed — the visibility and mapping rebuild, exactly [R-ENTRY-01 §7] (2026-08-29)

**Established.** Called with the *full* argument once, immediately after
placement and **before** the spawner/restoration of §6 (and again at every
commander respawn and watch-mode entry, [R-SKIR-01 §3]):

1. **Mapped mask** (`cellW × cellH` bytes, the whole grid): filled with
   `0xff` when mode bit 0 is **clear** (Mapped) and with `0x00` when it is
   set (Unmapped) — [03 R-VIS-01 §1]'s polarity, restated at the byte.
2. **Per-player visible mask**: for every slot that is live, controller
   ∈ {1,2,3}, side ≠ 10 — filled with `1` when mode bit 1 (Line of Sight)
   is **clear** and `0` when set. The fill covers the player's mask byte
   count; slots that fail the test keep stale bytes.
3. When mode bit 1 is set, every *active* unit in the pool is walked in
   record order: its observer record is formed from position, the
   definition's sight distance, height (floored at `(seaLevel + 1) << 16`)
   and the definition's sight-type byte; bit 2 set (true LOS) → the
   height-ray stamper; bit 2 clear → the circle stamper with radius index
   `clamp((sightDistance >> 5) − 5, 0, tableCount − 1)` ([03 R-VIS-01
   §2/§3]). Bit 1 clear → no unit is visited.
4. The *minimap dirty* bit is set, mode bit 3 (the "rebuild pending" bit)
   is cleared, and the two blip refreshers run.

Because this precedes the spawner, mission units are **not** in step 3;
they register their own sight at allocation ([03 R-VIS-01 §2]) and the
first sensor phase completes the picture. (The earlier order "spawner, then
visibility" is corrected in §10.)

### Closed — the tail: main GUI, phase priming, second grant, teardown, ready [R-ENTRY-01 §8] (2026-08-29)

**Established**, in order, all kinds:

1. the palette routine of [03 §4.3] called with a null picture name;
2. the main battle GUI (`…MAIN2.GUI` by side prefix; doc 07) is loaded and
   its handler installed; the local player's lobby record gets the
   *in-battle* bit (bit 4);
3. kind 3: the lobby records are broadcast and a session-name string of the
   form `<player name padded to 16><map name padded to 15>` is published
   (OOS);
4. **the per-player phase is run once, at global tick 0** — the same
   routine the tick executor calls as phase 5 ([01 §4.4], [R-SKIR-01 §3]).
   What it does at tick 0, per slot in order `0..9` (live, controller
   ∈ {1,2,3}, side ≠ 10):
   - the path-search scheduler's per-tick pass ([04 R-MOV-02A]);
   - the **AI manager** for a controller-2 slot: the strategic-refresh
     countdown (initial 30) decrements to 29 — no refresh; then **all ten
     task records run**, because every task deadline was constructed as 0
     and `0 <= 0` ([R-AI-01 §1]) — whatever simulation draws those bodies
     make on an empty or one-commander world ([R-AI-01 §2–§8]) are made
     here, before the first tick; a non-computer slot takes the
     manager's "inactive" branch;
   - the target-registry cadence gate: `lastRebuild (0) + 30 <= 0` is
     false — **no** `sim(30)` at tick 0 ([06 §3.1]);
   - every active unit of the slot: the LOS refresh ([03 R-VIS-01 §2]);
   - the 30-tick block (`due (0) <= 0` holds): `due ← 30`; the
     victory/defeat evaluation of [R-TRIG-01 §6] / [R-SKIR-01 §3] with the
     countdown at −1 (so at most the *arm* step, never the *fire* step);
     then the **economy settlement** of [05 R-ECO-01 §2] for a controller-1
     or -2 slot with live units or none ever created — on stocks that are
     the §5 grant for kind 2 and **zero** for kind 1;
   - the local player's HUD/minimap refresh.
5. **the resource grant again** ([R-SKIR-01 §2]) — this is the one that
   survives: kind 1 stocks and bonus from the mission's authored words,
   kind 2 from the rows, kind 3 from the host's shorts × 100; it overwrites
   whatever the tick-0 settlement produced. (Skipped when a save was
   restored.)
6. the save bank, if open, is closed and freed;
7. for every slot that owns an AI record (§3 step 24 — humans included), the
   **metal-spot list** is rebuilt: every cell of the grid is visited in row
   order and each cell whose feature definition has a non-zero metal value and bit
   1 of its flag byte set (the census of [05 R-FEAT-01 §6]) appends `(cellX, cellY, metal)` to the record's
   vector ([05 R-PROD-01 §6] reads it);
8. the *battle ready* bit is set; the worker returns.

**Load path summary (kinds 1/2/3 with an in-battle save).** §2 seeding and
words (with the `Player%i` controller restore) → §3 world rebuild in full,
including the AI constructors' draws and pool sizing from the *current*
lobby limit → no placement, no grant → §7 visibility rebuild on an empty
pool → the restoration dispatcher ([R-SAVE-02 §11]) → GUI → phase priming
on the restored world at the **restored** global tick (the per-slot timers
restored by `Players` decide whether the 30-tick block fires) → no grant →
bank closed → metal-spot lists → ready. The scheduler block restored by
`Players` is what §9 sees.

*Addendum (2026-08-29, from [07 R-FE-02 §2]):* battle-entry definition
finalisation raises the front end's catalog-reload flag unconditionally, so
the shell pump rebuilds the entire unit catalog after every battle (and
whenever the catalog count is 0); the front end also forces 640×480 on the
post-battle path.

### Closed — the pre-tick state and the first pump [R-ENTRY-01 §9] (2026-08-29)

**Established — state at the handoff (fresh battle).** Global tick 0;
pending ticks 0; fractional carry 0; scaled-clock anchor = the instant of
§3 step 21 (world rebuild), **not** the handoff instant; slew counter 0;
pause bit clear; commander-death countdown −1; wind deadline 0 (so the
tick-1 wind chain of [R-CORE-02] fires); every per-slot 30-tick timer =
30 (advanced by the priming); the sim stream has consumed: eight draws per
AI record (slot order), then two per commander/mission unit allocated (in
the §5/§6 order), then whatever the priming's AI tasks drew.

**Established — the first pump.** The loading state's handoff frame calls
the battle pump once. Single-player: pause bit clear → the budget routine
of [01 §4.2] runs with `delta = now − anchor`, i.e. the wall-clock time
between the world rebuild and the handoff (the rest of loading: spawner,
GUI, priming, metal scan), and `ticks = trunc(delta × activeSpeed × 0.1 +
carry)`, clamped: `< 6` keeps it (and steps the slew counter down), else
**5** (and steps the slew up). A non-zero budget runs the tick executor
for that many consecutive sub-ticks *in the loading state's frame*; a zero
budget (only when the remaining load took under one scaled-clock unit at
speed 10, [01 §4.1]) defers the first tick to the battle state's first frame. Then the
presentation refresh, and the state switch. Multiplayer runs the budget
and the network-gated executor instead (OOS).

**Supported inference.** Because the rest of loading is far longer than
six tenths of a second on retail hardware, the first pump runs five ticks
back-to-back in practice. Settled by a manual retail observation of the
game-time counter at the first rendered battle frame.

### Corrections and cross-document needs [R-ENTRY-01 §10] (2026-08-29)

1. **"Timing is fixed" (above) — order.** It said "then the sparse two-pass
   spawner runs, then visibility and mapping are rebuilt, then the first
   authoritative tick runs". Wrong on two counts: the visibility rebuild
   runs **before** the spawner (§6/§7), and between the spawner and the
   first tick come the GUI load, the **per-player phase priming at tick
   0**, the second grant and the metal-spot scan (§8). Its "camera focus is
   chosen, then the spawner" is also reversed for the campaign camera,
   which runs after the spawner (§6).
2. **"Randomization for skirmish starts" (above).** "bounded by map
   dimension in cells minus 160, then offset by 80 cells" — the words are
   the TNT extents × 16, i.e. **world units** ([R-CORE-02] had it right).
   "the lookup … leaves the jitter untouched and can emit a diagnostic
   without crashing" conflates two paths: kind 3 keeps the jitter with no
   diagnostic; kind 2 has no jitter and a miss is fatal (§5).
3. **[R-SKIR-01 §2] order line.** "session words → placement stamps →
   grant → world rebuild → main GUI …" — the world rebuild precedes the
   stamps (§3 before §5). The rest of that line holds.
4. **Doc 01 [R-CORE-02] and [R-DET-01 §5], doc 06 [R-WFX-01 §6] —
   cross-doc need.** "Both writes land in the calling (main) thread's
   state" and "every draw made before battle entry is wiped" are inverted
   for the CRT stream: the reseed lands in the *worker's* thread block and
   dies with it; the main thread's CRT state carries the front-end draws
   into the tick's CRT consumers unbroken (§2). The 391,606 explosion-frame
   draws are made **per battle, on the worker, after the worker's reseed**
   — not "at process startup" and not "wiped by the battle-entry reseed"
   (§3 step 26). Doc 01 §5.1's "No gameplay worker pool" should name the
   loading worker (it runs no tick, but it runs all of battle entry).
   Doc 01's "Before the first tick" table should add the priming's AI-task
   draws (§8 step 4) and move the explosion draws to a per-battle row.
5. **[R-SAVE-02 §11] — cross-doc need.** "Summary `maxunits` → Players →
   …" is the restore order, but the unit pool was already sized before the
   restore (§6). A loaded skirmish uses the current registry limit.
6. **Doc 04 §3.6** says the interpreter runs "before any creation script,
   movement, or visibility publication for that tick" — true, and now
   more precisely: after the §7 rebuild and before the tick-0 priming.

### Open — what this unit did not close [R-ENTRY-01 §11] (2026-08-29)

Listed in the tail with deciders: the identities of the eleven interface
words the world rebuild resets; the writers of the requested/active speed
words for kinds 1 and 2 (they are not written at entry); which chat
targets the routing bit of the "local-authority" flag selects; the external
post-placement hook the multiplayer path calls (OOS).


### Closed — the spawner's height probe, exactly [R-ENTRY-02 §1] (2026-08-29)

**Established.** §6 said only that a non-mobile placement record's `y` is
"the terrain height probe at that cell (`<< 16`)". The probe is one small
routine shared with the build-placement anchor of [07 §9]; its contract:

- **Inputs.** The definition's footprint width `fw` and height `fh` (cells)
  and its yard-map bytes (row-major, `fh` rows of `fw`); the snapped cell pair
  `(cx, cz)` packed as two 16-bit halves; the map's cell width and height; the
  plot cells' low and high height bytes (the `hmin`/`hmax` of the 2×2 plot
  expansion, [04 §6.1], [04 R-DOC04-B]); the sea-level byte; the definition's
  `waterline` byte [fmt fbi].
- **Guards.** The probe returns `0` — and the unit is spawned at `y = 0`
  with no diagnostic — unless `cx > 0`, `cz ≥ 1`, `cx + fw < cellWidth` and
  `cz + fh < cellHeight` (signed 16-bit arithmetic on the halves; the `cz`
  test is the unsigned test "packed pair > 0xFFFF").
- **Aggregates.** Walking the footprint row-major, a yard byte with **bit 3**
  set folds that cell into `minLow = min(minLow, hmin)` (start 255) and
  `maxHigh = max(maxHigh, hmax)` (start 0); a yard byte with **bit 4** set
  folds `hmax` into a third maximum that this routine never reads.
- **Result.** `maxHigh < minLow` — which can only be true when no covered
  cell carried bit 3 — yields `SeaLevel − waterline` as an 8-bit subtraction
  (it wraps); otherwise the result is `minLow`. The caller shifts the byte
  left by 16, so the spawned `y` is the height byte in whole world units.

These are exactly the bit-3/bit-4 aggregates of the structure validator
[05 "Geothermal requirement"], with **none** of its gates: the spawner never
rejects a position for slope, depth or a missing geothermal cell, and it never
consults the bit-4 maximum. A footprint whose yard map has no bit-3 character
therefore sits at the water surface less `waterline` regardless of the
terrain under it — the same rule the validator's `maxHigh < minLow` branch
applies to a build site.

### Closed — ledger closure notes for the placement and spawner clusters [R-ENTRY-02 §2] (2026-08-29)

**Established — what the remaining "placement builder" and "battle entry
orchestrator" functions are.** The bottom-up ledger pass over these clusters
found no engine behavior left unstated. Every routine not already cited by
[R-ENTRY-01], [R-AI-01], [R-SKIR-01] or [R-TRIG-01] is one of: a compiler
template instantiation (the reference-counted string class, growable-vector
insert/copy/size helpers for one-, two-, three-, four- and eight-byte
elements, nested vector copy/destroy helpers for the LOS and mapping tables,
the sort and lower-bound helpers), the `Sleep` thunk, an unreachable debug
tokenizer, or a routine owned by another document (the bitmap cache, the
download-menu compile, the path class layer, the minimap surfaces, the
narration stream, the front-end gadget helpers, the weapon fire-method
selector). Those are re-laned in the ledger; the cross-document needs are
listed in the merge commit. Two details this pass did settle:

- The per-slot **profile passes** of [R-AI-01 §12] open the `plan` gate by
  calling the same setter the `plan` directive itself uses, before walking
  the catalog; there is no separate "fragment mode" flag.
- The strategic state constructor's `+20` term ("build-option list is
  non-empty", "Strategic state construction and refresh") tests the
  definition's **download-menu build list pointer** — the list the world
  rebuild's step 14 compiles from the `download` directory, capped at 31
  entries per definition — not the `canbuild` page table. Whether the
  pointer is allocated only for definitions with at least one entry (so that
  "non-null" and "non-empty" coincide) is left in the tail.


## Victory and defeat triggers

Conditions are authored as `[GlobalHeader]` keys and matched against a fixed
vocabulary of eighteen condition names. The builder that turns them into
trigger records, the exact grammar of every key, every evaluator body, the
tick site and the notification sites are closed under [R-TRIG-01] below; the
two tables here are the vocabulary with the **corrected** argument grammar.
**Correction (2026-08-29, RWU-08-5).** The earlier tables listed
`BuildUnitType`, `CaptureUnitType` and `KillAllOfType` as "type, count" and
`AllUnitsKilledOfType` as "type". The builder never runs a scan format on
those four: it copies the whole key value as the type name, so a value
`ARMSY, 1` is stored verbatim and matches no unit. Only `KillUnitType`,
`UnitTypeKilled`, `UnitTypePassesX/Z` (name + one integer) and
`MoveUnitToRadius` (name + three integers) parse arguments [R-TRIG-01 §4].

### Victory trigger types

| Authored name | Value grammar | Kind |
|---|---|---|
| `KillEnemyCommander` | non-zero integer | notification (unit removed) |
| `DestroyAllUnits` | non-zero integer | poll |
| `KillAllMobileUnits` | non-zero integer | notification (unit removed) |
| `BuildUnitType` | type name only | poll |
| `CaptureUnitType` | type name only | notification (capture) |
| `KillAllOfType` | type name only | notification (unit removed) |
| `KillUnitType` | `<letters>,<int>` | notification (unit removed) |
| `MoveUnitToRadius` | `<letters or ANYTYPE>,<X>,<Z>,<radius>` | poll |
| `UnitTypePassesX` | `<letters or ANYTYPE>,<X>` | poll |
| `UnitTypePassesZ` | `<letters or ANYTYPE>,<Z>` | poll |
| `VictoryTimerRunsOut` | integer seconds, strictly positive | poll |

### Defeat trigger types

| Authored name | Value grammar | Kind |
|---|---|---|
| `CommanderKilled` | non-zero integer | notification (unit removed) |
| `AllUnitsKilled` | non-zero integer | poll |
| `AllUnitsKilledOfType` | type name only | notification (unit removed) |
| `UnitTypeKilled` | `<letters>,<int>` | notification (unit removed) |
| `DeathTimerRunsOut` | integer seconds, strictly positive | poll |
| `AnyUnitPassesX` | integer `X`, zero or positive | poll |
| `AnyUnitPassesZ` | integer `Z`, zero or positive | poll |

### Default triggers

If the builder creates no victory condition it appends a `DestroyAllUnits`
record; if it creates no defeat condition it appends an `AllUnitsKilled`
record. The campaign victory and defeat predicates repeat the same injection
at poll time when they find an empty queue, so a queue is never empty when it
is polled [R-TRIG-01 §6].

**Correction (2026-08-29, RWU-08-5).** The paragraph that stood here
("Configured lobby skirmish ownership — Supported inference; exact retail
dispatch distinction Unknown … Nanolathe therefore treats a populated lobby
configuration as owning its end condition and does not poll its OTA trigger
queues; direct OTA type-2 sessions retain the established type-2 poll
behavior below") is retracted. There is no "populated lobby" versus "direct
OTA" distinction anywhere in the executable: the authority branch is the
session kind word alone, and **no type-2 or type-3 session ever polls a
trigger queue** — see [R-TRIG-01 §1]. Its supporting argument that
`DestroyAllUnits` "would end an ordinary lobby match on its first countdown"
rested on the never-written-counter reading retracted in [R-TRIG-01 §4].

### Evaluation

This heading is retained so that existing `[08 "Evaluation"]` citations keep
resolving; its content was rewritten from a full static trace on 2026-08-29
and now lives in the closed sections [R-TRIG-01 §1]–[R-TRIG-01 §10] that
follow. The corrections to the previous text are itemized in
[R-TRIG-01 §10].

### Closed — authority: which sessions poll the authored triggers [R-TRIG-01 §1] (2026-08-29)

**Established.** The mission loader's common tail runs the trigger builder
for **every** session kind — campaign, skirmish and multiplayer alike — so a
skirmish map whose `[GlobalHeader]` authors condition keys gets the same
records a campaign mission would. What differs is who reads them:

- The **victory predicate** polled from the tick site branches on the session
  kind word: kind 1 (campaign) polls the victory queue (AND); kind 2
  (skirmish) runs the elimination sweep of §6; kind 3 (multiplayer) runs the
  alliance-aware sweep recorded in [R-SKIR-01 §3]. Kinds 2 and 3 never touch
  the victory queue.
- The **defeat predicate** likewise: kind 1 polls the defeat queue (OR);
  kinds 2 and 3 return `local live-unit count == 0` — the predicate
  [R-SKIR-01 §3] describes — and never read the defeat queue.
- The **notification sites** (§7) are not kind-gated: a unit removal or a
  capture notifies every built trigger in every session kind. In a skirmish
  the only observable effect is presentational — a victory-class condition
  that completes from a notification plays the `Victory Condition` cue (§8)
  — because nothing ever polls the record's Satisfied flag.
- The trigger **save/load** slots run only for kind 1 (§8).

So the "lobby-skirmish rule word versus authored trigger" question has a
one-word answer: the session kind. The commander-death rule word and the
live-unit counters own every kind-2/3 end condition; authored and injected
triggers own kind 1 only. Both a skirmish started from the setup screen and a
kind-2 session started on an OTA directly are the same code path.

### Closed — record shape, vtable slots and construction [R-TRIG-01 §2] (2026-08-29)

**Established — the primary table has six slots, and the poll is slot 0.**
Every record's first word points at a six-slot table: (0) **poll**, called
with no argument, returns non-zero when the condition holds; (1)
**unit-removed notification**, called with the unit being torn down; (2)
**capture notification**, called with the unit whose owner is about to
change; (3) **unit-created notification**, called with every newly allocated
unit — present in all eighteen tables and a no-op in every one; (4) **save**
and (5) **load**, called with the save bank. Conditions that do not use a
slot point it at a shared no-op (poll no-op returns the Satisfied flag).
**Correction.** The previous text said "the tick site calls only the poll
slot … the countdown in `KillUnitType` advances only from the unit-died
slot" — true — but it also described `DestroyAllUnits`, `KillAllMobileUnits`,
`KillAllOfType`, `KillEnemyCommander` and `CommanderKilled` as "poll-time
scans"; four of those five are notification-driven and only
`DestroyAllUnits` polls (§4).

**Established — the visitor table.** The ten conditions that walk a player's
unit slice (`KillAllMobileUnits`, `BuildUnitType`, `KillAllOfType`,
`MoveUnitToRadius`, `UnitTypePassesX/Z`, `AllUnitsKilled`,
`AllUnitsKilledOfType`, `AnyUnitPassesX/Z`) carry a second one-slot table
whose single entry is the per-unit visitor; the slice walk calls it for every
occupied slot and stops when it returns zero. The radius scan of §5 ignores
the return value.

**Established — construction.** The builder probes the eighteen keys in one
fixed chain, victory keys in table order then defeat keys in table order,
and appends one record per present key to the owning array (sixteen slots
each; never more than eleven and seven are used). The queue order is
therefore the vocabulary order above, never the authored order, and a key
authored twice yields one record. Presence tests:

- flag keys (`KillEnemyCommander`, `DestroyAllUnits`, `KillAllMobileUnits`,
  `CommanderKilled`, `AllUnitsKilled`): integer read with default 0,
  present when non-zero — `KillEnemyCommander=0;` builds nothing;
- timer keys: integer read with default 0, present when **strictly
  positive**; the record stores `seconds × 30` in a 32-bit signed word with
  no saturation (wrap preserved);
- `AnyUnitPassesX/Z`: integer read with default −1, present when `>= 0`, so
  a zero boundary is a valid trigger and a negative one is ignored; the
  record stores `authored >> 4` (arithmetic shift), i.e. the map cell;
- name keys: string read into a 256-byte frame, present when the key exists
  at all (an empty value is present and stores an empty name);
- argument keys: after the string read, the frame is parsed with the C scan
  family — `%[a-zA-Z],%i` for `KillUnitType`, `UnitTypeKilled`,
  `UnitTypePassesX/Z` and `%[a-zA-Z],%i,%i,%i` for `MoveUnitToRadius`. The
  name scanset is **letters only**: it stops at the first digit, underscore
  or space, so a type name containing a digit is truncated and never
  matches; whitespace after each comma is skipped by `%i`. The conversion
  count is never tested: a missing integer leaves whatever the stack frame
  held (zero-valued or stale) and the record is still built. The three
  integers of `MoveUnitToRadius` are, in order, X, Z, radius.
- `ANYTYPE` (case-insensitive) is recognised only by `MoveUnitToRadius` and
  `UnitTypePassesX/Z`, which store an empty name for it. The other name
  keys store the literal text, and `ANYTYPE` there resolves to no
  definition and can never match.

Record sizes and layouts (by field, in construction order): 12-byte records
are table + Satisfied + Celebrated only; the 16-byte timer record adds the
tick deadline; `AllUnitsKilled` (16) and `KillAllMobileUnits`/`AnyUnitPasses`
(20) add the visitor table and, for the boundary pair, the cell threshold;
the 44/48/50/52/54-byte records add a 32-byte name (the visitor table sits
before the name where present), then for `KillUnitType`/`UnitTypeKilled` a
32-bit remaining count, for `BuildUnitType`/`KillAllOfType`/
`AllUnitsKilledOfType` a 16-bit resolved definition index (zero until
resolved) plus, for the last two, a 32-bit scratch count, for
`UnitTypePassesX/Z` the cell threshold; the 64-byte `MoveUnitToRadius`
record holds the name, X (authored pixels, unconverted), a sentinel word,
Z (authored pixels), and `radius << 16`.

### Closed — the owner and unit predicates every condition shares [R-TRIG-01 §3] (2026-08-29)

**Established — "local" and "enemy" are player slots 0 and 1, literally.**
Every owner test in the trigger code is one of two things: a compare of the
unit's owner-slot byte (the player index copied onto the unit at
allocation) against the constant 1, or a walk of a player record's unit
slice — always slot 0's slice or slot 1's slice, never the slot named by the
local-player index. Authored `Player=1` is slot 0, `Player=2` is slot 1
(the one-based-to-zero-based fixup with the zero-to-one fixup, "Mission
placement record"). Units owned by `Player=3` and above are invisible to
every slice walk and fail every `== 1` test; they are seen only by the
notification-driven conditions that carry no owner test (`UnitTypeKilled`,
`AllUnitsKilledOfType`'s own subject). No alliance row is consulted by any
trigger. **Correction.** The earlier text's `LocalOwner`/`EnemyOwner`
suggested the local-player index; the code compares against constants.

**Established — "live unit" for annihilation checks.** A player's unit slice
is the contiguous run of pool records from the player's first to last
record; the walks skip records whose definition index is zero (a free
slot). A record whose unit is dying but not yet torn down is still occupied
and still counted.

**Established — the "eligible unit" predicate** (used by `AllUnitsKilled` and
`MoveUnitToRadius`) is the selection-eligibility test: status bit 5
(`0x20`, *selectable*) set **and** construction remaining `== 0.0`
(finished) **and** the post-capture grace counter `== 0` **and** either no
carrier or the carrier's *cargo-selectable* status bit (bit 30) set. Bit 5
is the bit the `InitialMission` postlude clears for a scripted unit and the
`s`/`MakeSelectable` order sets again [04 §3.6]; a player unit still under
script control is therefore **not** a live unit for `AllUnitsKilled` and
cannot satisfy `MoveUnitToRadius`. The grace counter is armed to 150 ticks
only by a capture whose new owner is a remote (multiplayer) controller and
decrements once per tick in the per-unit sweep; in single-player it is
always zero.

**Established — "mobile"** (`KillAllMobileUnits`) means the unit carries a
mover object, which the allocator attaches when the definition's `BMcode`
is 1. **Correction.** The earlier "`CanMove`" reading is retracted.

**Established — "commander"** (`KillEnemyCommander`, `CommanderKilled`)
means the unit's definition name equals, case-insensitively, the commander
name of the **unit's own owner's** side in the side-data table — not the
local side's, not the definition's `Commander` flag.

**Established — boundary coordinate.** The `…PassesX/Z` compares use the
unit's **stamped footprint cell** (the 16-pixel cell of the footprint's
anchor corner, refreshed by the occupancy stamp when the unit moves), as a
signed 16-bit value, against the record's `authored >> 4` cell; the test is
`|cell − threshold| < 3`, i.e. a tolerance of two cells (32 pixels) either
side of the line. **Correction.** The earlier "±2-world-unit tolerance"
mistook the cell unit for a world unit; the earlier "after the same `>>4`
of the world coordinate" is also wrong — the compare reads the stamped
cell, not a shifted position.

### Closed — every condition, exactly [R-TRIG-01 §4] (2026-08-29)

All claims Established from the evaluator bodies. "Complete" means set
Satisfied and, if Celebrated is clear, play the cue and set Celebrated (§8);
"S" means the record's Satisfied flag. Notification-driven conditions do
nothing on poll except return S.

**Poll conditions (kind 1, local 30-tick due, §6).**

- `DestroyAllUnits` — returns `slot-1 live-unit count == 0` (the 16-bit
  counter both allocators increment and the teardown decrements,
  [R-SKIR-01 §3]). Plays the cue once when true; **never sets S**, so the
  result is recomputed every poll and a save records `Satisfied=0`.
  **Correction.** The previous text called this counter "a `u16` counter
  whose only reference in the image is that read; nothing ever writes it, so
  it holds its initial zero and the condition is satisfied from the first
  poll" and made "the quirk … the contract". That was an address-search
  artefact: the writers use the player-record base plus the slot stride, so
  a search for the absolute location of slot 1's field finds only this read.
  The condition is the ordinary "the enemy has no live units". The
  inference drawn from it — that a shipped mission "relies on" the quirk as
  an AND-term beside `BuildUnitType` — is withdrawn with it: such a mission
  completes when the enemy is annihilated *and* the unit is built.
- `BuildUnitType` — if S return true. If the resolved definition index is
  still zero, resolve the stored name through the definition-name binary
  search (unknown name stays zero and is retried every poll). Walk slot 0's
  slice; the first occupied record whose definition index equals the
  resolved index and whose construction remaining is `0.0` completes and
  stops the walk. No count, no `ANYTYPE`, no owner other than slot 0.
  **Correction.** "at least the authored count of completed units …
  a want below one is treated as one … `ANYTYPE` bypasses the name compare"
  is retracted; none of that exists.
- `UnitTypePassesX` / `UnitTypePassesZ` — if S return true. Walk **slot 0**'s
  slice; for each unit, if the stored name is non-empty and differs
  case-insensitively from the unit's definition name, skip; else apply the
  boundary test of §3 on the stamped X cell (Z cell); the first hit
  completes and stops the walk.
- `AnyUnitPassesX` / `AnyUnitPassesZ` — if S return true. Walk **slot 1**'s
  slice (the enemy's units, no type gate) with the same boundary test; the
  first hit sets S (no cue: defeat conditions never celebrate).
  **Correction.** The earlier text did not say which units; it is the
  enemy's.
- `AllUnitsKilled` — set S, then walk slot 0's slice and clear S on the
  first *eligible* unit (§3); return S. S is therefore recomputed on every
  poll and is not sticky.
- `MoveUnitToRadius` — §5.
- `VictoryTimerRunsOut` / `DeathTimerRunsOut` — return
  `deadline <= globalTick` as an **unsigned** 32-bit compare, where deadline
  is `seconds × 30` from construction. `>=`, not `>`; never sets S or
  Celebrated; no cue.

**Notification conditions (any session kind, §7).**

- `KillEnemyCommander` — on unit removal: if the unit's owner slot is 1 and
  its definition name equals its owner's side commander name, complete.
- `KillAllMobileUnits` — on unit removal: if the unit's owner slot is 1 and
  it has a mover, count the units with a mover in slot 1's slice (the
  removed unit is still occupied and counts; the walk stops at the second
  hit); complete when the count is below 2, i.e. when no *other* mobile
  enemy unit remains.
- `KillAllOfType` — on unit removal: if S, return; if the unit's owner slot
  is 1 and its definition name matches the stored name, resolve the name to
  a definition index, count units of that index in slot 1's slice (stop at
  two), complete when below 2.
- `KillUnitType` — on unit removal: only while the remaining count is
  `> 0`; if the unit's owner slot is 1 and its definition name matches,
  decrement; complete when the result is `< 1`. `KillUnitType=X, 0` and
  negative counts can therefore never complete.
- `CaptureUnitType` — on capture: if the unit's owner slot **before** the
  transfer is 1 and its definition name matches the stored name, complete.
  No count. **Correction.** "same countdown but driven by the
  capture/transfer slot, completing at `<= 0`" is retracted.
- `CommanderKilled` — on unit removal: if the unit's owner slot is 0 and its
  definition name equals its owner's side commander name, set S (no cue).
- `AllUnitsKilledOfType` — on unit removal of **any** owner whose definition
  name matches the stored name: resolve the index, count units of that index
  in slot 0's slice and then slot 1's slice (each walk stopping at two, the
  count carried across both), set S when the total is below 2. A `Player=3`
  subject is not in either slice, so its own removal counts only the
  survivors in slots 0 and 1.
- `UnitTypeKilled` — on unit removal of **any** owner whose definition name
  matches: decrement the remaining count unconditionally (it goes negative
  and keeps going) and set S when the result is `< 1`.

**One-shot versus repeating.** Every notification condition and the three
`S`-guarded polls (`BuildUnitType`, `UnitTypePasses`, `AnyUnitPasses`,
`MoveUnitToRadius`) latch: once S is set it is never cleared except by a
save/load. `DestroyAllUnits`, `AllUnitsKilled` and the two timers are pure
predicates re-evaluated at every poll; if the enemy gains a unit after the
count hit zero, `DestroyAllUnits` is false again until the next
annihilation. Because victory is an AND across the queue and the shared
countdown (§6) only ever counts down while the predicate stays true, a
victory combining a latching term with `DestroyAllUnits` can stall if the
enemy is reinforced during the five-due countdown.

### Closed — `MoveUnitToRadius` geometry [R-TRIG-01 §5] (2026-08-29)

**Established — de-projection on first poll.** The authored X and Z are map
**pixels in the editor's projected view**, where the displayed Z of a point
is its world Z minus half its terrain height. The record is built with the
raw pixels and a sentinel in the Y word; the first poll that sees the
sentinel converts them once:

```
x  = clamp(X, 0, mapWidthPx − 1);  z = clamp(Z, 0, mapHeightPx − 1)   (signed)
row = (z & ~15) + 128                       start eight 16-px rows below z
repeat up to nine times (row, row−16, …, row−128):
    h    = max(seaLevel, height(x, row))    terrain height sampler at (x<<16, row<<16)
    proj = row − (h >> 1)
    if proj <= z: break
    row −= 16
if all nine rows had proj > z:  result = (x<<16, h<<16, row<<16) from the last row tried; done
above = row + 16;  h2 = max(seaLevel, height(x, above));  proj2 = above − (h2 >> 1)
if proj < proj2 or z <= proj2:
    zFinal = (row<<16) + ((z − proj) << 20) / (proj2 − proj)   signed 32-bit, truncating
    y      = max(seaLevel, height(x, zFinal)) << 16
else:
    zFinal = row<<16;  y = h<<16
write x<<16 over X, y over the sentinel, zFinal over Z
```

The height sampler is the terrain height at a 16.16 position (the same
helper the corpse creator uses, [R-FEAT-01 §13]); it is called twice per
row when the result exceeds sea level, with no effect. `mapWidthPx`/
`mapHeightPx` are the map extents in pixels. Once converted the sentinel is
gone and later polls skip this block; a save does not persist the converted
centre (only Satisfied/Celebrated, §8), so a loaded mission re-derives it.
**Correction.** The previous "optionally clamps the authored centre when the
sentinel is present (map-edge and terrain-height snap, writing `X<<16` /
`Z<<16` back)" named the effect but not the algorithm; the row search and
interpolation above are the contract.

**Established — the radius scan.** With centre `(cx, cz)` (16.16) and
`r = radius << 16`: partition tiles are 128 pixels (`coordinate >> 23` in
16.16); the tile range is `(cx − r) >> 23 .. (cx + r) >> 23` and likewise
for Z, each bound clamped to `0 .. tileCount − 1` (a negative bound clamps
to 0). Tiles are walked Z-outer, X-inner, each tile's unit list followed
through the partition's per-unit link. For each unit, `dx = unitX − cx`,
`dz = unitZ − cz` (signed 16.16), and the test is
`(dx·dx >> 32) + (dz·dz >> 32) <= (r·r >> 32)` with 64-bit products — i.e.
`trunc(dx_px²) + trunc(dz_px²) <= radius²` in pixel units, **inclusive**,
planar X/Z, Y ignored. The visitor runs for every unit that passes,
regardless of owner; the visitor's own gates are: owner slot 0; stored
name empty or equal (case-insensitive) to the unit's definition name;
the eligible-unit predicate of §3. A hit completes the condition; the scan
continues through the remaining tiles. **Correction.** "tile bounds are
`(centre ± r) >>17`" is retracted — the shift is 23 (128-pixel tiles); the
rest of the earlier description stands.

### Closed — the tick site: cadence, order, countdown and latch [R-TRIG-01 §6] (2026-08-29)

**Established.** In the per-player phase, when the walk reaches the **local**
player's slot and that slot's due tick has arrived (`due <= globalTick`,
then `due += 30`), the end-condition block runs once. For **kind 1**: the
victory predicate is evaluated; if true the shared countdown steps on the
*won* path; otherwise the defeat predicate is evaluated and, if true, the
countdown steps on the *lost* path. Victory therefore wins a tie, and at
most one predicate advances the countdown per due. For kinds 2/3: if the
local record is inactive or its side's watch-mode bit is clear, the defeat
predicate (live count zero, [R-SKIR-01 §3]) is evaluated first and, if true,
steps the countdown on the lost path; otherwise the victory sweep is
evaluated and, if true, steps the won path. **Correction.** "the defeat
queue is polled only when the local side's commander marker (a runtime bit
on the local player's side definition) is clear — the commander-dead test"
is retracted on both counts: the bit is the watch-mode bit that the
elimination handler sets, and no defeat queue is polled in kinds 2/3.

**The kind-1 predicates.** Both first require the mission object's *armed*
flag, which the mission-object constructor sets and the mission spawner
clears when the mission has **no `[units]` records** (kind 1 only) — a
unit-less campaign mission can never end by trigger, which also prevents
the injected `AllUnitsKilled` from ending it on the first due. The defeat
predicate then runs the copy-protection deadline path recorded in
[R-SKIR-01 §3] (never armed by Nanolathe). Then: victory injects a
`DestroyAllUnits` if the victory queue is empty and returns the **AND** of
the polls in queue order, stopping at the first false; defeat injects an
`AllUnitsKilled` if the defeat queue is empty and returns the **OR** of the
polls in queue order, stopping at the first true.

**The kind-2 victory sweep — Established, and a correction to
[R-SKIR-01 §3].** For skirmish the sweep is simpler than the multiplayer
one: walk slots 0–9; skip the local slot, skip any slot whose byte in the
local player's first alliance row is non-zero (an ally), skip any slot with a
zero live-unit count; if any slot survives the skips, no victory; after all
ten, victory. No shared-victory bit, no controller or elimination test, and
no rule-word test. [R-SKIR-01 §3]'s "Victory detection" describes the
**kind-3** sweep (rule 2 never ends, shared-victory bits, both rows, all-k
check) and then states "a skirmish is won only when every other player's
live count is zero, allies included"; that last sentence is wrong for kind
2 — allied players are excluded by the first alliance row, which battle
entry fills from the setup screen's team groups [R-SKIR-01 §2].

**Countdown and latch.** One signed 16-bit countdown is shared by every
path: a true predicate finds it negative and sets it to 4; each later true
due decrements it; when the decrement takes it below zero the end latch is
written — the sixth consecutive true due, 150 ticks after the first — as
*ending* (bit 2) plus, on the won path, bits 4 and 5, or, on the lost path,
bit 6 with bit 4 cleared. A false due neither resets nor advances the
countdown. The presentation after the latch is the Session end section's.

### Closed — notification sites: removal, capture, creation [R-TRIG-01 §7] (2026-08-29)

**Established — unit removed.** The unit teardown (the final release of a
pool record, which runs for every death cause including the capture
re-creation below) notifies all victory records then all defeat records in
queue order, after it has stored the killer link and owner byte and before
it detaches carrier links, damages cargo, or clears the record; the unit's
owner, definition and stamped cell are therefore still valid, and the record
is still occupied for the slice walks of §4. Cargo aboard a dying transport
is damaged after the parent's notification and is notified on its own later
teardown. Teardown returns early, without notifying, for a record that
never became live.

**Established — capture.** The ownership-transfer routine notifies all
records (victory then defeat) with the unit **before** anything changes,
when the new owner differs and the unit is live and not factory-pending.
Its subsequent behaviour matters for the other conditions: when the old
owner is a human or computer controller and the new owner is a remote
controller, the old record gets the 150-tick grace counter, loses its
selected bit, is described to the peers in a packet and is then damaged
30000 with damage kind 4; otherwise, when the new owner is a human or
computer controller, a **new** record is allocated for the new owner
(copying health, construction remaining, heading and stance bits) and the
old record is damaged 30000 with damage kind 4. Either way the old record
dies through the ordinary damage path and its teardown fires a
unit-removed notification — so in single-player a capture is also a slot-1
loss for `KillUnitType`, `KillAllOfType`, `KillEnemyCommander` and the other
removal-driven conditions — and the new record fires a unit-created
notification.

**Established — created.** Both unit allocators notify slot 3 of every
record after allocation; every shipped condition ignores it.

### Closed — the `Victory Condition` cue, and save/load [R-TRIG-01 §8] (2026-08-29)

**Established — the cue.** "Complete" in a victory condition (all ten
non-timer victory conditions) plays a sound, not text: the literal alias
`Victory Condition` is looked up case-insensitively in the sound alias
table built from the section names of `gamedata/ALLSOUND.TDF` (stock:
`[Victory Condition] sound=victory2;`), and the resolved sample is started
on the digital channel when sound is enabled; a missing alias plays nothing.
No network packet is sent (the call passes the no-broadcast flag) and no
status-cue or message text is raised. The record's Celebrated flag gates it
to once per record. Defeat conditions and the timers never play it.
**Correction.** The earlier "raises the localized 'Victory Condition'
notification … the exact presentation channel (message/sound) is not
decomposed" is closed as above.

**Established — save and load (kind 1 only).** The mission's save writer
and loader call slots 4 and 5 of every record; each condition uses an
account named `VictoryCondition_<Name>` / `DefeatCondition_<Name>` with
integer items `Satisfied` and `Celebrated`, and `KillUnitType`/
`UnitTypeKilled` add `NumLeftToKill`. Nothing else persists: the resolved
definition indices, scratch counts, timer deadlines (rebuilt from the OTA at
load) and the de-projected `MoveUnitToRadius` centre are all re-derived.
Because the account name is the condition name, two records of one
condition would share an account; the builder's one-per-key rule makes
that unreachable.

### Closed — mission objects: `[units]`, `[features]`, `[specials]` readers [R-TRIG-01 §9] (2026-08-29)

**Established — the unit record parser** reads, per `[unitN]` block, in this
order: `Unitname`, `Ident`, `InitialMission` (strings, interned), `XPos`,
`YPos`, `ZPos` (integers, each `<< 16`), `Angle` (degrees, the magic
multiply of "Mission placement record"), `Player` (integer; 0 becomes 1),
`HealthPercentage` (default 100), `BuildPriority` (integer), `CreationCountdown`
(integer), `MissionCriticalUnit`, `AiIgnore`, `AiPriorityTarget` (each
`& 1`, packed into flag bits 4–6), `InitialGroup` (**integer** read, low
four bits packed into flag bits 0–3 — the stock values `patrol` and `Rockos`
parse as 0), `Immunity` (bit 7). Every key is read with the ordinary
section accessor, so a key absent from a block takes its default. Reader
census over the parsed records: `Unitname`, `XPos`/`YPos`/`ZPos`, `Angle`,
`Player`, `HealthPercentage`, `Immunity` (copied to the unit's status bit
15, itself unread) and `Ident`/`InitialMission` (the interpreter and the
name resolver, [04 §3.6]) have readers; `BuildPriority`,
`CreationCountdown`, `MissionCriticalUnit`, `AiIgnore`, `AiPriorityTarget`
and `InitialGroup` have **no reader** anywhere — the census in "Mission
placement record" stands. `Kills` on a unit block is **inert**: no
placement reader exists (the executable's `Kills` strings belong to the
score screen and unit-info panels); veterancy cannot be authored.
`OffMapUnit` has no string in the image. The unit is created immediately by
the two-pass spawner; there is no delayed-creation queue, no cargo loop and
no group attachment — `CreationCountdown` and transport contents cannot be
authored. Created health is `MaxDamage × HealthPercentage / 100` with
integer truncation.

**Established — the invalid-player path is fatal.** The spawner's first pass
checks `Player − 1 < 10`, the slot active, its controller human/computer/
remote and its team byte not the eliminated sentinel; a failure formats
`Player number %d invalid for unit %s`, shows it in a modal box and then
**terminates the process** through the CRT exit — it does not proceed.
**Correction.** "emits a diagnostic for invalid player numbers but still
proceeds to a position fixup helper and the normal unit allocator" is
retracted; the message sink is the fatal channel.

**Established — `[features]`.** Each `[featureN]` reads `Featurename` (up to
128 bytes), `XPos` and `ZPos` (integers, default −1); a missing name or a
**negative** coordinate blanks the name, and the feature placer skips blank
names — the record is dropped, not clamped. **Correction.** "coordinates
that are cleared when negative" is retracted; the name is cleared. Names
are matched case-insensitively against the feature catalog at placement.

**Established — `[specials]`.** Each `[specialN]` reads `specialwhat`; only
values beginning with `StartPos` (case-insensitive, eight characters) are
kept, with `XPos` and `ZPos` as 16-bit values. The suffix after `StartPos`
is parsed as an integer when its first character is a digit, else it is a
running counter starting at 1 in file order; the stored index is
`value − 1` when `value > 0`, else `value` — so `StartPos0` and `StartPos1`
both store 0. **Established — a missing start position is fatal.** When
the commander creator cannot find the assigned `StartPos` it formats
`Error: Could not find start position number %i on the map!`, shows it and
terminates the process through the same fatal channel; no commander is
placed and no fallback is taken. (The random-interior jitter of "Randomization
for skirmish starts" applies to slot assignment before this lookup, not to a
lookup failure.)

**Established — the start barrier text.** The multiplayer barrier screen
draws one bar per active, non-eliminated slot and a caption formatted
`%s.  %i %s` from the localized `Waiting for other players`, the count of
slots that have reached the barrier, and the localized `player ready`
(count exactly one) or `players ready` (any other count); once the barrier
releases the caption is `Synchronization complete`. Which slot flag the
count reads is a Supported inference (a per-slot ready byte beside the
controller byte); the text is multiplayer-only presentation and never
drives a tick.

### Closed — corrections to earlier text [R-TRIG-01 §10] (2026-08-29)

Each entry quotes the retracted sentence and names the section that now
owns the contract.

1. "seven established buckets — `0x0C` … `0x40`" — nine; §2 and "Trigger
   object".
2. "`BuildUnitType` type, count", "`CaptureUnitType` type, count",
   "`KillAllOfType` type, count" — name only; tables and §2.
3. "`DestroyAllUnits` — the quirk is the contract … nothing ever writes it"
   — it is slot 1's live-unit count; §4.
4. "`KillAllMobileUnits` — succeeds when no live mobile unit (`CanMove`)
   belonging to the enemy owner remains" — notification-driven, `BMcode`,
   "no other"; §3, §4.
5. "`BuildUnitType` — at least the authored count … `ANYTYPE` bypasses" —
   first finished unit of the type; §4.
6. "`KillEnemyCommander` / `CommanderKilled` — absence scans" —
   notification-driven equality on the owner's side commander name; §4.
7. "satisfied when `abs(coord − threshold) < 3`, i.e. a ±2-world-unit
   tolerance" — stamped cell, ±2 cells; §3.
8. "tile bounds are `(centre ± r) >>17`" — `>> 23`; §5.
9. "`CaptureUnitType` — same countdown … completing at `<= 0`" — no
   count; §4.
10. "the defeat queue is polled only when the local side's commander marker
    … is clear" — watch-mode bit, and no queue in kinds 2/3; §6.
11. "Configured lobby skirmish ownership — Supported inference" — session
    kind is the branch; §1.
12. "Owner gating … compares the unit's player index against `LocalOwner` or
    `EnemyOwner`" — constants 0 and 1; §3.
13. "raises the localized 'Victory Condition' notification" — a sound alias;
    §8.
14. "emits a diagnostic for invalid player numbers but still proceeds" —
    fatal; §9.
15. "coordinates that are cleared when negative" (features) — the name is
    cleared and the record dropped; §9.
16. [R-SKIR-01 §3] "a skirmish is won only when every other player's live
    count is zero, allies included" — kind 2 skips the first alliance row;
    §6.

### Open — what this unit did not close [R-TRIG-01 §11] (2026-08-29)

- **Unknown — `InitialGroup` low-nibble reader.** The parser packs the value
  into the flag byte and no reader was found in the bounded search; the
  `g <n>` order operand that stock scripts use resolves through `Ident`/
  `Unitname` only [04 §3.6]. Decider: static trace from the flag byte's
  bit 0–3 mask; until then treat the key as inert.
- **Unknown — the meaning of a unit-created notification.** Every shipped
  condition ignores slot 3; whether any non-shipped condition type existed
  is unrecoverable. Decider: none needed for implementation; keep the slot.
- **Unknown — side-table divergence.** Whether a synthetic side whose
  `SIDEDATA` commander differs from the `Commander`-flagged unit is
  reachable in stock content is a bounded residual (stock agrees); the
  table is the authority. Decider: asset census over non-stock content.

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

### Closed — the skirmish setup record and every option's consumer chain [R-SKIR-01 §1] (2026-08-29)

Status: **Established** unless a claim says otherwise (direct static trace of
the `SKIRMISH.GUI` handler and its row builder, the registry preference
loader/writer, the battle-entry orchestrator, the start-slot stamp, the
resource grant, the kill-record handler, the per-player phase, the two
elimination predicates, the `GAMEOPTIONS.GUI` overlay and the `RESTRICT2.GUI`
screen). This unit is RWU-08-2; the raw trail is kept out of the repo.

**The setup record.** One heap object, allocated at start-up and freed at
shutdown, holds the whole skirmish configuration. It carries ten row
records of six 32-bit words — *controller* (`0` open, `1` player, `2`
computer), *side* (index into the side table), *ally group* (`0..4`, or the
unassigned sentinel `5`), *metal*, *energy*, *colour* — followed by five
32-bit rule words — *CommanderDeath*, *Mapping*, *LineOfSight*,
*LineOfSightType*, *StartLocation* — a 256-byte map name, the authored
gadget count of the screen (saved so the dynamic rows can be rebuilt), the
row index the last click landed on, and the *difficulty* word. The number of
rows actually shown is the separate `NumSkirmishPlayers` global (missing
value 4; an explicit value is stored unclamped — the loader's "in 2..10" and
"outside 2..10" branches store the same thing).

**Registry mirror — every key, its default, and the store-on-miss rule.**
The loader reads each value under the `Total Annihilation` key; when a value
is absent it assigns the default **and writes that default back** (the helper
called on the miss path is the DWORD *store*, three stack arguments, the
third being the value; it is not a delete). The rule words and their
defaults are `SkirmishCommanderDeath` 1, `SkirmishMapping` 1,
`SkirmishLineOfSight` 1, `SkirmishLOSType` 1, `SkirmishDifficulty` 1 (stored
`& 0xffff`), `SkirmishLocation` 1, and the string `SkirmishMap` (on a miss
the loader selects the skirmish mission catalog, takes its first entry's
name, copies up to 256 bytes and stores it). The per-row values live under
the `Total Annihilation\Skirmish` subkey as `Player%dController` (miss 0),
`Player%dSide` (miss `slot & 1`, i.e. slot index modulo two),
`Player%dColor` (miss = slot index), `Player%dAllyGroup` (miss 5),
`Player%dMetal` (miss 1000), `Player%dEnergy` (miss 1000), for
`0 <= slot < NumSkirmishPlayers`. The writer stores every one of these plus
`NumSkirmishPlayers` when the Start button passes validation and whenever
the row count changes. The parallel `Single*` and `Multi*` triples
(`…CommanderDeath`, `…Mapping`, `…LineOfSight`, `…LOSType`, all default 1)
are read into separate globals; see §4 for why the `Single*` mirror is dead
in practice.

**Screen build.** The `SKIRMISH.GUI` loader copies the record's difficulty
into the session difficulty global, sets the `Difficulty` gadget's state to
it and lights the matching label (`Easy`, `Medium`, `Hard`), resolves the
record's map name against the map catalog (falling back to the first entry
when it no longer exists), then the row builder runs: if every row's
controller is `0` it forces row 0 to `Player` and row 1 to `Computer`; it
zeroes every `TEAMICONSx` frame; for each row it writes the `Player%d`
caption (`Open`/`Player`/`Computer`), the `Side%d` state, the decimal
`Metal%d`/`Energy%d` text, the `Color%d` gadget's image set (`logos.gaf`)
and frame (the colour word), and the `Allies%d` gadget's image set
(`TEAMICONSx`) with frame 10; open rows have their side/allies/metal/energy/
colour gadgets disabled. Then it stamps the four rule gadgets' state byte and
description text: StartLocation `0` → state 1, `Commanders are randomly
placed on the battle field.`, else state 0, `Commanders are placed at
pre-determined locations.`; CommanderDeath `0` → state 1, `Game continues
after Commander is destroyed.`, else state 0, `Game ends when commander is
destroyed.`; Mapping `0` → state 1, `Terrain is visible.`, else state 0,
`Terrain is blacked out until explored.`; LineOfSight `0` → state 0, `All
mapped terrain is visible.`; else LineOfSightType `1` → state 1, `Terrain
elevations affect a unit's view.`; else state 2, `Terrain elevations do not
affect a unit's view.` Finally the `MapName` text is set. Row geometry is in
[07 "Retail closure for the single-player menu slice"].

**Callbacks — control to field, exactly.** The clicked gadget's trailing
digits select the row. `Player%d` cycles controller `0 → 2`, `1 → 0`, and
`2 → 1` only when no other row is `Player`, else `2 → 0`; on becoming live
the row's colour is checked against every live row and, on a conflict, the
callback rescans logo indices `0..9` for one no configured row holds
(**this scan ignores the controller word, so an open row's colour also
blocks**) and stores `-1` when all ten are taken. `Side%d` sets
`side = (side + 1) mod sideCount`. `Allies%d` sets `group = (group + 1) mod
6` and refreshes every `Allies%d` frame: with `n` live rows sharing the
group, frame `10` when `n = 0`, `2*group + 1` when `n = 1`, `2*group` when
`n >= 2`. `Color%d` steps `colour ± 1` (left click `+1`, right click `-1`)
modulo the `logos.gaf` frame count, mapping `-1` to `count - 1`, and
**re-steps while the candidate equals a live row's colour** (a live row is
one whose controller is non-zero; the row itself is excluded) — it never
stores `-1`. **Correction.** The previous text said the colour callback
"after a conflict … scans every configured row, including open rows, from
logo 0 upward and stores -1 when all ten stock logos are present"; that
scan belongs to the controller-cycle callback described above, not to
`Color%d`. `Metal%d`/`Energy%d`: left click `v = min(v + 500, 10000)` then
`if v == 700 then v = 500`; right click `v = v - 500; if v < 201 then v =
200` (the compare is `< 0xc9`, so 200 is the floor and the quirk rewrites
the one increment from 200). `CommanderDeath`, `StartLocation`, `Mapping`
toggle their word with `xor 1` and rewrite the description as above.
`LineOfSight` cycles `(LOS=0) → (1,1)`, `(1,1) → (1,0)`, `(1,0) → (0,1)`.
`Difficulty` cycles the session global and the record `0 → 1 → 2 → 0`,
writing both. `SelectMap` opens `SELMAP.GUI` (empty skirmish catalog →
`There are no skirmish maps to choose from`). Every callback plays the
`Skirmish` UI cue except `Difficulty`, which plays `SKirmish` (sic).

**The hidden player-count selector (`SkirmishCheat`).** The screen keeps a
15-byte typed-key buffer. A key handler compares its tail against `*III`,
`*IV`, `*V`, `*VI`, `*VII`, `*VIII`, `*IX`, `*X` and, on a match, sets
`NumSkirmishPlayers` to 3..10, writes all preferences, stores the count,
reloads the preferences (so rows beyond the old count get the miss
defaults), rebuilds the rows from the saved authored gadget count, plays the
`SkirmishCheat` cue and marks the screen dirty. The buffer is cleared after
`*III`, `*IV`, `*VIII`, `*IX`, `*X` (not after `*V`, `*VI`, `*VII`, whose
prefix keeps matching the longer sequences). "Cheat Codes" as an option
is multiplayer-only (§9); this is the only skirmish meaning of the token.

**Start preflight.** Unchanged from the paragraphs above: terrain lookup,
at least one `Computer` and one `Player`, `players <= schema players`, the
ally-group test (first live row whose group is not 5; then fail if any live
row has a different group — open rows and group-5 rows are skipped). The
diagnostics are quoted above. On success the player count global becomes
`players + computers`, the row-to-player conversion runs (§2), the
preferences are written, and the front end switches to the battle state.

### Closed — battle entry: what the record becomes [R-SKIR-01 §2] (2026-08-29)

**Row-to-player conversion (skirmish only, before the loading screen).**
For each row `i < NumSkirmishPlayers`: controller `1` copies colour and side
into the player's lobby record, registers the slot as human, and makes it the
local player (both local-player indices = `i`; the last `Player` row wins);
controller `2` copies colour and side and registers the slot as computer;
controller `0` registers it as inactive. Registration resets the slot's
two alliance rows to zero, sets `allied[i][i] = 1` in both, stores the
controller byte, and (skirmish only) names the slot `Player` for a human or
`Arm`/`Core` for a computer by side (`side == 0` → `Arm`). Then, for a live
row `i`, every row `j` (`j < NumSkirmishPlayers`) with the **same ally
group, a non-zero controller, and group ≠ 5** — or `j == i` — sets
`allied[i][j] = 1`. **This is the alliance predicate:** `allied(i, j)` is
the byte at column `j` of player `i`'s first alliance row; it is symmetric in
skirmish because it is derived from equal group numbers, and group 5 rows
are allied with nobody but themselves. The second alliance row (used by the
multiplayer alliance screen) keeps only the diagonal in skirmish. Doc 05
should cite this predicate for sharing and the AI's side filter.

**Session words.** The battle-entry orchestrator, for session kind 2
(skirmish): copies the configured unit limit into the session unit-limit
word (§6); sets the local-authority flag; copies *CommanderDeath* into the
commander-death rule word; and writes the visibility mode word's bits
`0 = Mapping & 1`, `1 = LineOfSight & 1`, `2 = LineOfSightType & 1`
([03 §3.1 R-VIS-01 §1] has the polarity and consumers). It then (no save
file) stamps start positions — identity when *StartLocation* ≠ 0; when it is
0 the CRT-stream shuffle of "Randomization for skirmish starts" — with one
precision added: the gate draw taken when fewer than three slots are
eligible is `((draw * 2) / 32768)` in 64-bit arithmetic, i.e. shuffle only
when `draw >= 16384`; a gate result of zero leaves the identity order and
takes no further draws. Eligible slots are those whose record is active,
whose controller is human, computer or remote, and whose side is not the
sentinel 10. The stamp helper per slot copies side and colour again, calls
the storage-bonus setter with the row's energy and metal (each floored at
200 and converted to single precision, bonus flag set — [R-ECO-01 §4]),
resolves `StartPos<n>` for the assigned position (diagnostic `Error: Could
not find start position number %i on the map!` on a miss), creates the
side's commander there, and centres the camera on the local player's.

**Resource grant.** After every slot is stamped, the grant pass sets, for
every player index whose row is skirmish-derived, `energy stock =
float32(row.energy)` and `metal stock = float32(row.metal)` — the plain
integer-to-single conversion, **no 200 floor** on the stock (the floor is
only on the bonus operands). The same pass runs again at the end of battle
entry (still gated on the absence of a save file), so the grant is written
twice with identical values;
the campaign branch of the pass uses the mission's authored values and the
multiplayer branch `hostShort * 100`. Order within battle entry: session
words → placement stamps → grant → world rebuild → main GUI → per-player
phase primed → grant again → session start flag. (**Corrected 2026-08-29:** the world rebuild precedes the stamps — [R-ENTRY-01 §3], [R-ENTRY-01 §10].) Nothing here draws from
the simulation stream.

**Save persistence.** The save `Summary` account records, for session kind 2
only, `CommanderDeath`, `Location`, `Mapping`, `LineOfSight`,
`LineOfSightType` (the record's five rule words), plus `Difficulty`,
`Players`, `maxunits` and `Side`. Load restores the five into the setup
record and the map name, then a small helper rewrites the commander-death
word and the three mode-word bits from them — the same bit assignments as
battle entry.

### Closed — commander death: the whole chain [R-SKIR-01 §3] (2026-08-29)

**Vocabulary.** The rule word is `0`, `1`, or `2`. The in-battle
`GAMEOPTIONS.GUI` overlay names them `Game Continues`, `Game Ends`,
`Deathmatch` in that order; the skirmish screen offers only `0`/`1`.

**Trigger site.** The kill-record handler (the function that files a unit's
death for statistics) compares the dead unit's type name with its owner's
side commander name. When they match it first **clears the owner's
storage-bonus flag** (so the starting-resource capacity of §5 is lost with
the commander under every rule value); then, when the rule word is non-zero
and the owner's controller is human or computer, it pumps the front end until
no panel is open, then runs the **owner sweep**: for every unit in the
owner's pool slice that is alive and not already dying, if the unit's owner
record is inactive or not human/computer it is destroyed silently (death
kind 3, dying bit set, kill record filed), otherwise it receives
`30000` damage from itself with damage kind 3 — the ordinary damage path,
so armour and death animations apply and the units die over the following
ticks, not in the same tick. The sweep is gated on the owner's live-unit
count being non-zero at the time. Rule `0` skips the sweep entirely: the
player keeps every unit and nothing else happens on commander death. Rule
`2` runs the sweep too (the commander's other units are lost), then respawns
(below).

**Counters.** Each player carries a 16-bit *live unit count* and a 32-bit
*units ever created*. Both unit allocators increment both; the
kill-record handler decrements the live count when the unit is finally
removed (the same function that clears the unit's "alive" bit), and in
multiplayer notifies peers when it reaches zero.

**Defeat detection.** In the per-player phase, on the **local** player's
30-tick due (`globalTick >= due` then `due += 30`), for session kinds 2 and
3, when the local record is inactive or its watch-mode bit is clear, the
defeat predicate is evaluated: for kinds 2/3 it is simply **`local live unit
count == 0`** (the campaign kind polls its defeat queue instead). A second,
preceding branch of the same predicate arms a random deadline of `9000 +
(draw * 9000) / 32768` ticks (CRT stream, one draw) and returns true when it
passes; it is gated on a flag set only by the CD-presence helper when its
drive-letter probe disagrees with itself — a copy-protection path Nanolathe
never arms. A true predicate runs the shared countdown: `-1 → 4` on first
detection, then `-1` per due; when it goes negative (five dues, ~150 ticks
after the first true poll) the rule word selects: `2` → **respawn** (search
up to 9999 candidate points, each two simulation draws for X/Z inside the
map minus a tenth on each side, accepted when all nine cells of a 3×3
footprint probe pass the side commander's placement test, no unit is in the
way, and — when the map has a lava/water sentinel — the terrain height
exceeds the sea level; then create the commander at the local player, apply
the storage-bonus setter with the local lobby record's metal and energy
shorts `× 100`, and add the same energy and metal products to the new
unit's stored energy/metal — scaled `× 0.5` for difficulty 0 and `× 0.7`
for difficulty 1 when the owner is a computer — then rebuild visibility and
the build menu; those shorts are written only by the multiplayer lobby, so
in a skirmish reached with rule `2` through the registry the additions are
zero and the bonus floors at 200); any other value → in multiplayer with watching allowed, set the
local watch-mode bit, clear mode-word bits 0–1, rebuild visibility, and
either post `You're out!  Continue Watching?` (`YESORNO.GUI`) or, when the
local host still hosts live AI players, `You are placed in watch mode
because you are hosting AI players which are still alive.  If you exit, they
will be terminated.`; in skirmish (kind 2) it writes the end latch directly:
`ending` bit set, `won` cleared, and `lost` set when the local record's
end-flag byte is clear. **Correction.** The previous text said value one
"ends the game through the watch-mode path when the local commander dies";
the watch-mode path is multiplayer-only and the end is not keyed on the
commander at all — it is keyed on the live-unit count that the owner sweep
drives to zero. Consequently the "value-zero all-live-unit sweep" that the
tail listed as Unknown is the same predicate: under rule 0 the player is
defeated when the last of their units dies, exactly as under rule 1 after
the sweep. Cross-section: the "Evaluation" paragraph stating that for kinds
2/3 "the defeat queue is polled only when the local side's commander marker
… is clear — the commander-dead test" describes this predicate wrongly (it
is the live-count test above, and the campaign kind is the only one that
polls a defeat queue); restated in [R-TRIG-01 §6], which also corrects
the "allies included" sentence below for the skirmish kind.

**Victory detection.** The elimination sweep run from the same due returns
false immediately when the rule word is `2` (deathmatch never ends by
elimination). Otherwise, for every other active player `j` with a
human/computer/remote controller, side ≠ 10 and watch-mode bit clear: if
`j` has created no unit yet, no victory; if `j` still has live units, then
victory continues only when both `j` and the local player have the lobby
*shared-victory* bit set and `allied(local, j)` and `allied'(local, j)`
(both rows) hold, and every other active, non-eliminated player `k` is in
`j`'s alliance row; any failure is no victory. The shared-victory bit is
written only by the `ALLIES.GUI` screen's `VICTORY` control (opened from the
in-battle `TABMENU.GUI`'s `ALLIES` button); the skirmish setup never sets it, so a skirmish is won
only when every other player's live count is zero, allies included.

### Closed — line of sight and mapping [R-SKIR-01 §4] (2026-08-29)

The three bits' consumers, polarity and the `Permanent`/`Circular`/`True`
and `Mapped`/`Unmapped` names are established in [03 §3.1 R-VIS-01 §1];
this document owns only the provenance. One addition: the OTA loader, when
it parses a map's `GlobalHeader`, also writes the **single-player** option
globals — `mapping` (key default 0), `lineofsight` (key default 0),
LOSType `= 1`, commander death `= 0` — every time an OTA is loaded, which
happens for every campaign mission and also when the skirmish screen
resolves its map. So for a campaign battle the mode word comes from the
mission's OTA keys, not from the registry `Single*` triple that is read at
start-up and immediately shadowed; the `Single*` values reach nothing.
Skirmish is unaffected because the kind-2 branch reads the setup record.
Cross-doc: [03 §3.1 R-VIS-01 §1] says the loader "then deletes the value"
on a registry miss; the helper stores the default (§1 above).

### Closed — starting metal and energy [R-SKIR-01 §5] (2026-08-29)

Ladder: the row value starts at the registry value (miss 1000) and moves by
500 per click within `[200, 10000]` with the 200→500 rewrite (§1). At battle
entry the integer becomes two single-precision values: the stock (`float32(v)`,
unfloored) and the storage bonus (`float32(max(v, 200))`, bonus flag set),
so a row set to 200 starts with 200 stock and +200 capacity, and the default
1000 starts with 1000 stock and +1000 capacity — the bonus is what lets the
commander alone hold the starting stock. The `GAMEOPTIONS.GUI` overlay
prints the row's integer values under `Starting Metal:` / `Starting
Energy:` (presentation only; multiplayer prints `hostShort * 100`).

### Closed — unit limit: setup field and lobby side [R-SKIR-01 §6] (2026-08-29)

The configured limit is read once at start-up from `totala.ini`,
`[Preferences]` `UnitLimit`, default 250, then clamped: `> 500 → 500`, `< 20
→ 20`. The OTA loader writes the **session** limit word from the map's
`maxunits` key (default 200) whenever an OTA is parsed; skirmish and
multiplayer battle entry then overwrite the session word with the configured
limit (multiplayer: the host's lobby word), so the OTA value survives only
for campaign missions. No skirmish gadget edits the limit; the multiplayer
battleroom's `MAXUNITS` control does. The `GAMEOPTIONS.GUI` overlay prints
the session word under `Max Units:`; the in-battle options snapshot copies
it as the first word of its block. Simulation consumers (construction gate,
AI gate) are doc 05's [RWU-05-4].

### Closed — start placement, Fixed and Random [R-SKIR-01 §7] (2026-08-29)

`StartLocation` is stored in the record and mirrored as `SkirmishLocation`;
`1` (`Fixed`) assigns slot `i` start position `i`; `0` (`Random`) shuffles as
in §2 and "Randomization for skirmish starts". The overlay names them
`Random`/`Fixed` from the record for kind 2 and from bit 14 of the host's
lobby word for kind 3. The command-line switches `fixedloc`, `deathends`,
`deathplays`, `deathmatch`, `mapping`, `circlos`, `truelos`, `permlos`,
`cheating`, `watching` set the multiplayer host's lobby word only (they
never touch the skirmish record).

### Closed — player colours [R-SKIR-01 §8] (2026-08-29)

The colour word is an index `0 .. frameCount(logos.gaf) - 1` (stock: ten
frames; the row-default is the slot index). It is copied to the player's
lobby colour byte at battle entry and consumed by (a) the 3DO renderer,
which selects frame `colour` of any multi-frame team texture, (b) the unit
and radar blip presenters, which select frame `colour` of the blip image
sets, and (c) the score screen's `PlayerColor%d` gadgets, which show
`logos.gaf` frame `colour`. It never touches the palette or the side; side
is the separate `Side%d` word. Two rows may share a colour only through the
`-1` quirk of §1 or by editing the registry; the renderer then reads frame
`-1` as an unsigned byte — out of range for a ten-frame set — **Unknown**
what the texture lookup returns for it (decider: static trace of the
frame-array accessor's bound handling).

### Closed — AI difficulty [R-SKIR-01 §9] (2026-08-29)

`Difficulty` on the skirmish screen writes both the record word and the
session global (`0 → 1 → 2 → 0`, labels `Easy`/`Medium`/`Hard`); the
`SKIRMISH.GUI` loader copies record → global on entry and the Start path
persists it as `SkirmishDifficulty`. The plain `Difficulty` registry value
(miss 1, `& 0xffff`) feeds the campaign. Consumers: the computer player's
economy discount and transfer scaling [R-AI-01 §12] and the settlement
discount [R-ECO-01 §3]; the commander-respawn grant scaling in §3; the
`GAMEOPTIONS.GUI` overlay's `Difficulty:` label (kinds 1/2 only — kind 3
shows `Cheat Codes:` and `Watching:` from bits 13 and 15 of the host word,
`Allowed`/`Disallowed`). The AI profile grammar is [R-AI-01 §12].

### Closed — map restrictions [R-SKIR-01 §10] (2026-08-29)

`RESTRICT2.GUI` is opened only by the multiplayer battleroom handler; no
skirmish path reaches it. The screen builds, for every definition except
index 0 whose `norestrict` capability bit is clear — **this is the reader
of `norestrict` that doc 05 recorded as absent**: a `norestrict` definition
is simply not offered for restriction — a 98-byte row (caption
`"%s\r%s %dM  %dE"`, definition index, current limit, and the restriction
lookup's status) sorted by a comparator over the row, plus `OLDCOUNTS`, an integer
per row snapshotting each limit before editing. Each `SLIDER%d` runs
`0..101`; a value `< 101` is stored as the limit and printed, `101` prints
`No Limit` and stores `-1`; every change is written straight into the
restriction container (a tree keyed by definition, one record per
definition holding a *restricted* short and a *limit* word). `Reset`
sets every row to `100`, or to `0` when the definition's `wacky` capability
bit is set, writing only rows whose value changed; `Cancel` (`Previous` cue)
writes every `OLDCOUNTS` value back. The close path, when the host slot (the
lobby record whose local flag is set) is human- or computer-controlled,
walks the rows and marks each definition restricted (limit
`0`) or unrestricted through the container's two setters. The container is
process-lifetime memory: no registry, save or file writer touches it, and the
per-definition limit field doc 05 could not find a writer for is this
container's *limit* word. Simulation consumers (the build-menu and order
gates) are [RWU-05-4]; the AI's construction task consults the same
container.

### Closed — `GAMEOPTIONS.GUI` and the remaining tokens [R-SKIR-01 §11] (2026-08-29)

`GAMEOPTIONS.GUI` is the read-only in-battle "GameSettings" overlay. It
prints `Commander Death:` (rule word → `Game Continues`/`Game Ends`/
`Deathmatch`), `Starting Locations:`, `Mapping Mode:` (`Mapped`/`Unmapped`
from mode bit 0), `Line of Sight:` (`Permanent` when bit 1 clear, else
`True` when bit 2 set, else `Circular`), then for kind 3 `Cheat Codes:` and
`Watching:`, otherwise `Difficulty:`, then `Map:`, `Starting Metal:`,
`Starting Energy:`, `Max Units:`. Presentation only: it reads the live words
and writes none (reader census: this overlay and the in-battle options
snapshot are the only readers of the rule words outside the simulation
sites named above). `TECHLEVL` and `COMMNDER` remain gadget-name tokens in a
static table with no code reader found — **Unknown** whether any stock
`.GUI` names them (decider: asset census of the stock GUI files, then a
trace of the table's reader).


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

**Out of scope (2026-08-29, RWU-08-6).** The whole battleroom — 67 functions
in the ledger's `LOUNGE2.GUI` cluster plus the provider/connection screens —
is outside Nanolathe's single-player scope; nothing in it is reached from the
campaign or skirmish paths ([R-OOS-01 §3]). The only battleroom-authored
values the single-player session reads are the lobby word's `Cheat Codes` bit
(via its entry-time copy, [R-OOS-01 §2]) and the watching bit (loading-state
table, [R-OOS-01 §2]); the skirmish screen writes the setup record instead
([R-SKIR-01 §1]). The bullets above stay as a description, not a contract.

## Computer-controlled players

### Established AI-facing data and rooted planner [P0-01] [P0-02] [P0-03]

The executable reads these AI-related definition and mission values:

- per-unit `ai_weight` text in a dedicated definition field (64-byte capacity), parsed by the weight loader and applied through the profile system;
- per-unit `ai_limit` text in a separate definition field — no reader exists, and [R-AI-01 §12] now names the mechanism: both per-definition profile passes, the weight pass and the limit pass, read the `ai_weight` field, so `ai_limit` is parsed and abandoned. It must not be wired to limits; the functioning `limit` token comes from the profile file, not this field;
- mission `aiprofile` string via a mission resource slot that loads `ai\<profile>.txt` with fallback to `ai\default.txt`;
- mission placement fields for AI ignore, AI priority-target, build priority, and initial group — parsed at mission load but no transfer or reader is found in the creation path, so they are inert for planning;
- computer difficulty (`0` easy, `1` medium, `2` hard) from the registry and setup state; it gates profile `plan` directives and scales every positive production contribution whose **destination** player is computer-controlled by 0.5, 0.7 or 1.0 — the exact evaluation points and float widths are doc 05's ([R-ECO-01 §3]; the earlier [R-AI-01 §12] wording is superseded by that closure);
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

All order submission from manager tasks uses the ordinary order service. Build orders enter as a build command with a world position, queue modifier one, and type identity; positioning uses move and patrol-like commands; waves use a formation helper that can issue attack or move orders against a unit target or a centroid; the eco path can enqueue a build-option choice into a builder queue and can toggle an activatable building's active state; the regroup and explore tasks issue move-like orders to centroids or random map targets; the rally task submits attack orders per member. **Correction (2026-08-28, RWU-08-1):** the earlier reading "the rally task issues a mix of stockpile and formation orders after a guard check" was wrong — the body issues attack-intent orders one unit at a time and never uses the group broadcast helper or any stockpile command. See [R-AI-01 §7]. There is no privileged mutation path that writes economy or unit state outside those ordinary submissions. [P0-02]

#### Eco toggle and group-vector population — Established with direct writer census [P0-02] [R-P0-04]

The eco task scans its own group vector and examines only completed units, gated on the building-class and live status bits with the under-construction bit clear. It has **two** branches, selected by whether the definition's authored makes-metal byte is non-zero.

**Makes-metal branch.** It compares the player's current **energy stock** against twice the player's current **metal stock**: when `energyStock <= 2 x metalStock` it disables; otherwise when net energy production is at or below zero it leaves the unit as is; otherwise it draws once with bound five and enables the unit only on a non-zero result. The two argument forms disable and enable correspond to those two invocation sites; the semantic name metal-maker on/off is supported inference, but the argument values and the stock compare are established.

**Correction (2026-08-28, RWU-08-1).** The previous text read "it compares twice the stored metal income against current energy: when metal is at most half of energy it disables" and named "the eighty percent gate". Both were wrong. The compared quantities are the two **stock** fields, not an income field; the polarity is the reverse of what was written — the converter is switched **off** when metal is at least half of energy, which is the sensible rule for an energy-to-metal converter; and no `0.8` term exists anywhere in this body. The exact expression is in [R-AI-01 §2].

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

The manager allocates nine task objects in ten fixed slots. The slot order is load-bearing: resource/activity and builder queue, attack wave A, regroup A, construction/positioning, null, attack wave B, regroup B, explore/gather, random-walk rally.

**Clarification (2026-08-28, RWU-08-1).** The earlier phrase "the tenth slot is an intentional null task that never runs" conflated two distinct things, and a reimplementation that keeps only one of them will misnumber every group record. There are two: **slot 0** is an empty *pointer* — the constructor zero-fills all ten slots and then assigns only slots 1 through 9, so slot 0 holds no task object at all and the dispatcher's null test skips it; and the task in **slot 5** is a real object of the intentional **null task class**, whose first virtual slot returns immediately. Slot 5 therefore owns group record 5, which the classifier populates with armed buildings, and the attack wave reads that record's centroid as its first-choice gather point ([R-AI-01 §4]). The constructor's allocation order — eco, construction, null, wave A, regroup A, wave B, regroup B, explore, rally — is not the slot order; only the slot order matters at dispatch. A task record holds, in order, its virtual table, the manager back-pointer, its group record pointer, its deadline and its owning player slot index; the group record base is on the player record and records are a fixed stride apart, so record index equals slot index. Each task points at its own player group record — a begin/end/capacity vector of unit pointers — and the dispatcher runs the task slots in ascending slot order when the computer-controller gate is active and the task's deadline is at or before the global tick. Deadline execution does not imply the task's vector is non-empty. Group record zero is the ungrouped sentinel and is not one of the nine task records. Task deadlines are listed under Strategy manager and its task graph above. [08 "Strategy manager and its task graph"; 08 "Dispatch gates and order sinks"]

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

**Correction (2026-08-29, RWU-AI-03).** The "definition max-slope field" row
above names the wrong key. The word the classifier tests is the definition's
**`MinWaterDepth`** (copied from its movement class at FBI compile), and the
test is `MinWaterDepth >= 1` — a definition that may stand in water goes to
regroup B. The comparison and destination are unchanged; only the field label
was wrong ([R-AI-03 §6]; the same word selects the scatter helper's region set
in [R-AI-03 §4]).

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

**Amendment (2026-08-28, RWU-08-1).** With the task bodies specified, the
per-branch draw counts are exact and are stated at each site in [R-AI-01 §2]
through [R-AI-01 §7]; the two entries above that were summarised loosely are
sharpened here. The **positioning** draws are two `RNG(65536)` angle draws in
the construction task's repositioning pass — one in each of its two branches,
taken only when the branch's distance test selects the random-hop case
([R-AI-01 §3]); the placement root's scatter draws are separate and unchanged.
The **explore** task draws `RNG(900)` for its deadline every run, then either
`RNG(2)` plus two draws per patrol leg for two or three legs, or nothing at all
on the nearest-hostile fallback, or two or three draws on the map-edge branch —
`RNG(2)`, then either `RNG(mapWidth)` and `RNG(2)`, or `RNG(2)` and
`RNG(mapHeight)` ([R-AI-01 §6]). The **rally** task's score comparison is two
draws, one bounded by the incumbent score and one by the challenger, taken only
when the probe validates ([R-AI-01 §7]). The wave, regroup, merge, classifier
and weapon-maintenance bodies draw nothing.

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

Scores at or below zero are excluded **without drawing**. The remaining positive scores are selected in authored build-option order by cumulative weighted reservoir selection using one simulation-random draw bounded by the running positive total.

**Correction (2026-08-28, RWU-08-1).** This paragraph previously closed with "The builder's own definition name is rejected as a candidate." That is not what the post-selection filter does. The filter compares the **selected** definition's authored `side` string against the **builder's own `side`** string and discards the whole selection when they differ, with no re-draw and no runner-up. The full contract, including the wasted draw a cross-side build list causes, is in [R-AI-01 §8]. [08 "Placement root and search helpers"]

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
residual in R-P0-05 §8 is closed. The "strategic half-capacity state field" is
also closed and was misnamed: the compared value is the owning **player
record's live unit count**, reached through the strategic state's back-pointer,
and the global is the session's per-player unit limit. See [R-AI-01 §13].

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

Then `count == 0` multiplies the accumulator by four, `count == 1` by two, and `MaxSlope >= 0` by three. When `(unitLimit >> 1) < player.liveUnitCount` — an unsigned compare of the session's per-player unit limit against the owning player's live unit count — half of the single coefficient is added; this is the branch that used to be described as an unlocated "strategic half-capacity state field", and it is reachable in ordinary late-game state ([R-AI-01 §13]). The coefficient is then zeroed when `CanLoad` is set, when `IsFeature` is set, or when the wind-generator/global-wind comparison is true, and is clamped to the signed-byte range.

**Correction (2026-08-29, RWU-AI-03).** The `MaxSlope >= 0` term above, and
"max-slope" in the input lists of this section and of [R-AI-01 §16], name the
wrong key: the definition word the class routine reads is **`MinWaterDepth`**
(the movement-class value the FBI compile copies into the definition,
[04 R-DOC04-A]); the multiply-by-three applies when `MinWaterDepth >= 0`, i.e.
to definitions that may stand in water. The comparison and factor are
unchanged ([R-AI-03 §6]).

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

The half-capacity residual is **closed**: the field is the owning player
record's live unit count, not a strategic-state field, and the comparison is
against the session's per-player unit limit ([R-AI-01 §13]). The remaining
residual is the placement geometry:

```text
TODO(question): What are the exact geometry and water-legality contracts of the two placement helpers beyond the established extractor selector draw? (Narrowed: the metal score is the footprint per-cell metal-byte sum and the waterline band is enforced only by the yard path of the placement validator — see the Placement root section.)
```

### R-AI-01 — Task-class bodies, difficulty, and reaction to damage — Established [R-AI-01]

RWU-08-1 (2026-08-28) opened the first virtual slot of every task class. The
sections below give the per-invocation body of each class at implementable
precision, plus the manager entry constants they depend on, the difficulty
vocabulary, the damage reaction, and the closure of the half-capacity
residual. Where a claim replaces earlier text the replaced sentence is quoted.

Conventions used throughout: *tick* is the global simulation tick; positions
are 16.16 fixed-point world coordinates unless the text says "world units";
`trunc` is truncation toward zero (the retail float-to-integer helper);
`RNG(n)` is one draw from the single simulation stream returning `0..n-1`
[01 §7]; *intent* numbers are the arguments of the shared order-request
resolver that turns an intent plus the acting unit's capabilities into a
canonical command name, which is then submitted through the ordinary order
service [04 "Order descriptor table"], [07 "UI order producers (R-P0-11)"].
The intents the computer player uses are `2` (move family — `Move_Ground`,
`VTOL_Move`, `QMove` when queued), `3` (attack family — `Attack_Chase`,
`Attack_NoMove`, `Attack_Kamikaze`, the air variants, `Suppress`), `9`
(patrol family — `Patrol`, `QPatrol`, `VTOL_Patrol`, `RepairPatrol`,
`VTOL_RepairPatrol`) and `14` (`MobileBuild` / `VTOL_MobileBuild`).

#### R-AI-01 §1 — Manager entry, slot indexing, and the two verified constants — Established [R-AI-01]

The per-player manager tick runs before the strategic refresh [R-P0-05 §6] and
does exactly this, in order:

1. **Outer gate.** Proceed only when the manager's player record exists (its
   first word is non-zero) *and* the player's control byte equals `2`, the
   computer value. On failure the manager still runs the weapon-maintenance
   sweep (§15) with its argument `0` and returns.
2. **Classification countdown.** Decrement the manager's countdown. When the
   decremented value is **less than 1**, reset it to **30** and run the
   classifier (§10, [R-P0-04 §3]). The reset is to 30, not to the pre-decrement
   value; the compare is `< 1`, so a countdown that starts at 0 or below fires
   on the same pass. **Verified constant (1 of 2).**
3. **Task sweep.** Walk exactly **ten** task slots in ascending slot order.
   Slot index and group-record index are the same number; slot `0` holds the
   intentional null pointer and records `1..9` belong to the nine tasks
   [R-P0-04 §2]. For each non-null task, invoke its first virtual slot when
   `deadline <= tick` compared as **unsigned 32-bit**. **Verified constant
   (2 of 2):** ten slots, unsigned inclusive compare, ascending order.
4. **Weapon maintenance** (§15) with argument `1`.

The slot-to-class assignment is: 1 resource/queue, 2 attack wave A, 3 regroup
A, 4 construction/positioning, 5 null, 6 attack wave B, 7 regroup B, 8
explore/gather, 9 random-walk rally. Group record `0` is the ungrouped
sentinel and belongs to no task.

Each group record carries, in this order, a back-pointer to the owning player,
its own group number, and a growable vector of unit pointers. Two members of
that record are load-bearing for the task bodies: the **vector** is what the
tasks count and iterate, while the **group number** is what the order-broadcast
helper (§9) matches against each unit's stored group.

A task record carries a back-pointer to the manager, a pointer to its group
record, its deadline, and its owning player index. The wave and regroup
records additionally carry the per-instance tunables named in §4 and §5.

The seven distinct virtual tables are one contiguous run of function pointers
in read-only data with two entries per class; the second entry of every class
is never invoked by either dispatcher and has no direct caller
[08 "Strategy manager and its task graph"].

#### R-AI-01 §2 — Resource and builder-queue task body — Established [R-AI-01]

Reschedule first: `deadline = tick + 30`. Then walk the task's group vector in
vector order. A member is examined only when its runtime status word has the
**building-class** bit set, the **live** bit set, and the **under-construction**
bit clear.

The body then branches on the definition's authored `makes-metal` byte
[fmt fbi].

**Makes-metal branch (byte non-zero).** Inputs are the owning player's
**current energy stock**, **current metal stock**, **energy production** and
**energy usage** aggregates [05 "Player slot"].

```text
if energyStock <= 2.0 * metalStock:
    setActive(unit, false)                       # disable
else if (energyProduction - energyUsage) <= 0.0:
    leave the unit unchanged
else if RNG(5) != 0:
    setActive(unit, true)                        # enable
```

All three comparisons are on 32-bit floats. The doubling is `metal + metal`,
evaluated before the compare. `setActive` is the ordinary activation toggle:
it rewrites the unit's active bit and, on a change, raises the COB `Activate`
or `Deactivate` callback and the matching order-descriptor event [04 §5].
There are no other writes.

**Correction.** This section previously said "when metal is at most half of
energy it disables". That is the inverted reading: the executable disables when
`energyStock <= 2 x metalStock`, that is when metal is at least half of energy,
which is the behavior that makes sense for an energy-to-metal converter. The
"eighty percent gate" the earlier text also mentioned does not exist in this
body; there is no `0.8` term anywhere in it. The enable draw and its bound of
five, the strict `> 0` net-energy test, and the enable-on-non-zero rule are
unchanged and remain established.

**Factory-queue branch (makes-metal byte zero).** The unit is skipped unless
its definition's build-option count is non-zero and its primary order queue is
empty. Then the ordinary cumulative-reservoir selection of §8 runs over the
builder's build options and the winner is submitted through the same
build-queue producer the interface uses for a factory product click
[07 "Factory product click producer (R-P0-11 §1)"], with the queue count `1`.
Because the queue-empty test precedes the selection, exactly one product is
queued at a time and the next is queued only after the queue drains.

#### R-AI-01 §3 — Construction and positioning task body — Established [R-AI-01]

Reschedule first: `deadline = tick + 90`. Then read the **strategic centre**
(the weighted own-unit centroid rebuilt every 30 ticks, [R-P0-05 §5]) once into
a local, and run **two** independent passes over the task's group vector, each
in vector order. The first pass places buildings; the second repositions
builders. A member can be acted on by both passes in the same invocation.

Two per-player inputs gate both passes:

* the **build-capable count** — the number of the player's own live, completed
  units whose definition has a non-empty build-option list. It is recomputed by
  the 30-tick strategic refresh, not by this task ([R-P0-05 §5]; the refresh
  clears it and increments it once per qualifying unit during its live-pool
  scan).
* the **damage throttle deadline** on the manager, written only by the
  damage-reaction path of §11.

The branching field is the definition's authored `cancapture` flag [fmt fbi].

**Pass 1 — choose and place a building.**

```text
for unit in group vector order:
    if unit.def.buildOptionCount == 0: continue
    if unit.def.cancapture:
        if buildCapableCount >= 5: continue                 # signed compare
        if tick < manager.throttleDeadline: continue        # unsigned compare
    if unit.currentOrder != none and (order.gateMask & 0x8) != 0: continue
    chosen = selectCandidate(player, unit)                  # R-AI-01 §8
    if chosen == none: continue
    placed = placeCandidate(player, unit.position, chosen.def, out)
                                                            # 08 "Placement root"
    if unit.def.cancapture:
        dx = out.x - centre.x
        dz = out.z - centre.z
        d  = trunc(sqrt(float(dx)*float(dx) + 0.0 + float(dz)*float(dz)))
        if d > ((playfieldWidth + playfieldHeight) / 3) << 16: placed = false
    if placed:
        submit intent 14 (MobileBuild) for `chosen` at `out`, queue modifier 1
```

`playfieldWidth` and `playfieldHeight` are the map's world-unit extents **less
the border strips**: width is the terrain width in world units minus 32, height
is the terrain height in world units minus 128; both are derived once at map
load from the terrain cell counts multiplied by 16 [fmt tnt], [03 §2.2]. The
integer division by three truncates toward zero and the shift converts the
world-unit result to 16.16, so the cap is a plain world-unit radius. The
vertical term of the distance is a literal zero loaded onto the x87 stack, not
an omitted term: the sum is `dx*dx + 0 + dz*dz` in that order, square-rooted in
80-bit and truncated once. Non-`cancapture` builders are not distance-capped.

The order-queue test uses the current order's static gate mask, not its command
identity [04 "Order descriptor table"]; the semantic name of mask bit 3 is not
closed by this unit (§17).

**Pass 2 — reposition.**

```text
for unit in group vector order:
    if unit.currentOrder != none and (order.gateMask & 0x4000) == 0: continue
    if unit.def.cancapture and buildCapableCount < 5: continue
    target = centre                                   # working copy
    if unit.def.cancapture:
        centre.y := unit.y                            # in the working copy
        d = trunc(sqrt(dx*dx + 0 + dz*dz))            # centre - unit, dy forced 0
        if d > 640 << 16:
            a = RNG(65536)
            target = (centre.x - sin(a)*640, unit.y, centre.z - cos(a)*640)
        else:
            target = (2*centre.x - unit.x, unit.y, 2*centre.z - unit.z)
        submit intent 2 (move)   to target, queue modifier 0
        submit intent 9 (patrol) to centre, queue modifier 1
    else:
        d = trunc(sqrt(dx*dx + dy*dy + dz*dz))        # full 3-D, centre - unit
        if d >= 320 << 16:
            target = centre
        else if d < 16 << 16:
            a = RNG(65536)
            target = (unit.x - sin(a)*320, unit.y, unit.z - cos(a)*320)
        else:
            scale  = (320 << 32) / d                  # 64-bit signed divide
            target = unit + ((centre - unit) * scale) >> 16   # per axis, 64-bit
        submit intent 9 (patrol) to target, queue modifier 0
```

`sin(a)*r` and `cos(a)*r` denote the shared fixed-point trig helpers: a 512-entry
signed 16-bit quarter-symmetric table indexed by `((angle + 32) >> 6) & 0x3fe`,
multiplied by the magnitude in 64-bit, rounded by adding `0x1000` and shifted
right 13 [04 §8]. The cosine helper is the sine helper with a quarter turn
added to the angle first. Both results are **negated** at every use site in the
computer player, so the offset points opposite the tabulated direction; that
sign is part of the contract because it selects which side of the centre the
unit is sent to for a given draw.

The `>= 320` case sends the unit to the centre itself; the `< 16` case is a
random 320-world-unit hop from the unit's own position with the height
unchanged; the middle case is a fixed-point interpolation that lands 320 world
units from the unit **along the direction to the centre**, which overshoots the
centre whenever the unit is nearer than 320. Each axis is scaled independently
by the same 64-bit quotient, so the result is exact only up to the per-axis
`>> 16` truncation.

The two passes never draw when `cancapture` is set and the distance test keeps
the mirrored target; the only draws are the two `RNG(65536)` angle draws above,
plus whatever §8 and the placement root consume.

#### R-AI-01 §4 — Attack-wave task body: engagement hysteresis and target selection — Established [R-AI-01]

Reschedule first: `deadline = tick + 300`. Then run the wave merge
([08 "Wave merge"]) with the instance's peer slot index and distance threshold,
and only then read the group count. The instance tunables are: minimum `3`,
maximum `6`, threshold 20,000 for wave A and 50,000 for wave B, peer slot 3 for
wave A and 7 for wave B (the paired regroup records).

```text
n = groupCount
if n == 0: return
engage = false
if task.min < n:                      # strictly greater than 3
    if task.engaged: engage = true
    else if task.max <= n: engage = true   # 6 or more
if not engage:
    # GATHER
    for record in (slot 5 null, slot 1 resource, slot 4 construction):
        if centroid(record, out c): 
            task.engaged = false
            broadcast(intent 2, queueModifier 0, position c, spacing 0xa0)
            return
    # no base centroid anywhere: fall through and attack
task.engaged = true
c = centroid(own group)
target = nearestHostileUnit(player, c)
if target != none:
    broadcast(intent 3, queueModifier 0, unit target, spacing 0)
```

`task.engaged` is a per-instance latch, initialized clear. Reading the two
branches together gives the wave's whole life cycle and is the **retreat rule**:

* a wave gathers at the base until it holds **six** members, then latches
  engaged and attacks;
* while latched it keeps attacking every 300 ticks even as it loses members;
* when attrition brings it to **three or fewer** the `task.min < n` test fails,
  the latch is cleared, and the wave is ordered back to the base centroid —
  this is the only retreat behavior the computer player has, and it is a
  group-level return-to-base, never a per-unit disengage;
* it then re-gathers to six and re-engages.

There is no other retreat: no health test, no losing-fight test, no per-unit
withdrawal, and no fallback position other than the three base records. A
whole-image call census over the task bodies finds no other order submission.

The **gather destination** is the centroid of the first non-empty record among
slot 5 (the null task's record, which the classifier fills with **armed
buildings**), slot 1 (the resource task's record, **unarmed buildings**) and
slot 4 (the construction record, **builders**), tried in that order
[R-P0-04 §3]. So the wave rallies on the defended part of the base first, on
the economy second, and on the builders last. The `0xa0` argument is the
broadcast helper's spacing parameter and is passed only on this gather; the
regroup task passes `0` for the same parameter (§5).

The **attack destination** is the single nearest hostile unit to the wave's own
centroid, chosen by the helper of §9. Nothing about the target's type, value,
threat, or the wave's composition enters the choice.

The wave body draws no random numbers, and neither does the merge.

#### R-AI-01 §5 — Regroup task body — Established [R-AI-01]

Reschedule first: `deadline = tick + 150`. The peer is the task slot named by
the instance's peer index — wave A for regroup A, wave B for regroup B. The
body returns unless **both** its own group and the peer's group are non-empty,
then computes the peer's centroid and broadcasts intent `2` (move) to it with
queue modifier `0` and the spacing parameter `0`.

That is the whole body: no target selection, no draws, no state writes other
than the deadline. The regroup record is the pool the wave merge draws its
members from ([08 "Wave merge"]), so a regroup group that is never emptied by
the merge simply follows its wave around at a 150-tick cadence.

#### R-AI-01 §6 — Explore and gather task body — Established [R-AI-01]

```text
r = RNG(900)
deadline = tick + r + 30
n = groupCount
if n < 5:
    centre = strategicCentre(player)
    if (int16)(centre.x >> 16) != 0 or (int16)(centre.z >> 16) != 0:
        k  = RNG(2)                                  # 0 or 1
        gw = mapWorldWidth  >> 3                     # trunc toward zero
        gh = mapWorldHeight >> 3
        for i in 0 .. k + 1:                         # two or three iterations
            tx = centre.x + (RNG(gw) - gw/2) * 65536
            tz = centre.z + (RNG(gh) - gh/2) * 65536
            broadcast(intent = 2 if i == 0 else 9,
                      queueModifier = 0 if i == 0 else 1,
                      position (tx, centre.y, tz), spacing 0)
        return
    c = centroid(own group)
    target = nearestHostileUnit(player, c)
    broadcast(intent 9, queueModifier 1, position target.position, spacing 0)
    return
# n >= 5: strike for a random map edge
y = 0
if RNG(2) != 0:
    x = RNG(mapWorldWidth) << 16
    z = 0 if RNG(2) != 0 else (mapWorldHeight - 1) << 16
else:
    x = 0 if RNG(2) != 0 else (mapWorldWidth - 1) << 16
    z = RNG(mapWorldHeight) << 16
broadcast(intent 9, queueModifier 0, position (x, y, z), spacing 0)
```

Notes that a reimplementation must reproduce:

* `mapWorldWidth` / `mapWorldHeight` here are the **full** terrain extents in
  world units (cell count times 16), not the border-reduced playfield extents
  the construction task uses in §3.
* The "centre is unset" test looks only at the **high 16 bits** of the centre's
  x and z as signed shorts. A centre whose magnitude is under one world unit on
  both axes therefore reads as absent, and the task falls into the
  nearest-hostile branch.
* The far edge is written as `(dimension + 0xffff) << 16` in 32-bit arithmetic.
  Because the shift discards the carried bit, the stored value is exactly
  `(dimension - 1) << 16` for every dimension from 1 to 65536 — one world unit
  inside the map, not one 16.16 unit and not an overflow. Reproduce the value,
  not the expression.
* The near branch issues a small patrol route: the first order is a fresh move
  and the remaining one or two are appended patrol legs, so the group ends up
  patrolling between two or three points scattered within an eighth of the map
  around the strategic centre. The per-leg offset is `RNG(g) - g/2`, both
  divisions truncating, so the offset range is asymmetric by one when `g` is
  odd.
* **Edge, retail fault.** In the nearest-hostile branch the position handed to
  the broadcast helper is formed from the helper's return value **before** it is
  tested for `none`. With an empty group the helper never dereferences it and
  nothing happens; with a non-empty group and no hostile unit anywhere the read
  is a wild pointer. The branch is reachable only when the strategic centre
  reads as absent *and* the player still owns units *and* no hostile unit
  exists, which stock play does not produce. Nanolathe may bound this as a
  sanctioned divergence by treating "no hostile unit" as "issue nothing".
* Draw order per invocation is fixed: the 900-bound deadline draw always
  happens first, then the branch draws in the order written above.

#### R-AI-01 §7 — Random-walk rally task body — Established [R-AI-01]

The rally task keeps three vectors on its own record: a **best** point, a
**probe** point, and a **drift** vector, plus a **best score**.

```text
r = RNG(150)
deadline = tick + r + 30
if groupCount == 0: return                 # no producer fills record 9 [R-P0-04 §5]
if RNG(10) == 0:
    probe = best                           # reseed from the incumbent
    a = RNG(65536)
    drift = (-sin(a) * 320, 0, -cos(a) * 320)
probe = probe + drift                       # every invocation
if probeIsOnKnownGround(probe):
    s = probeScore(player, probe, 160)
    if RNG(bestScore) < RNG(s):
        bestScore = s
        best = probe
for unit in group vector order:
    if not unit.def.canattack: continue
    if unit.hasNoLocomotion and not orderWouldBeAccepted(unit, unit.position, best):
        continue
    resolve intent 3 (attack) for unit at `best`
    if the resolved command is not the reject sentinel:
        submit it with queue modifier 0
```

* `probeIsOnKnownGround` has two forms selected by a global option word. When
  the option's second bit is set, the probe's map cell — `cellX = (probe.x >> 16) >> 5`
  and `cellZ = ((probe.z >> 16) - ((probe.y >> 16) >> 1)) >> 5`, each taken from
  the coordinate's signed high half, with the usual height-shear projection
  [03 §2.1] — is bounds-checked against the **owning player's** explored-terrain
  grid and accepted when that cell's byte is non-zero. Otherwise the same cell
  index is looked up in the **global per-cell visibility word** and accepted when
  the bit for the **local viewing slot** is set. The second form makes the
  computer player's rally search depend on the local viewer, which is a
  determinism hazard in a networked session; single-player is unaffected because
  the local slot is the only human. The identity of the global option bit is
  recorded as **Supported inference** (it is the same word doc 03 reads for the
  mapping/fog option) and named as an open item in §17.
* `probeScore` sums the **single per-type strategic coefficient** ([R-P0-05 §5])
  over the members of the strategic state's **first vector** — the non-allied
  live units the 30-tick refresh collected [R-P0-04 §3] — whose planar distance
  from the probe satisfies `((dx*dx) >> 32) + ((dz*dz) >> 32) <= 160 * 160`, with
  the products taken in 64-bit and shifted back. So the rally point is scored by
  how much **enemy** value sits within 160 world units of it.
* The adoption test is a **randomized comparison**: one draw bounded by the
  incumbent score and one bounded by the challenger score, adopted when the
  first is strictly less than the second. A zero incumbent score therefore
  always yields to any positive challenger, and two equal scores swap with
  probability just under one half. Both draws are taken every time the probe
  validates, in that order.
* `hasNoLocomotion` reads the unit record's locomotion-object pointer — the
  same field the shared build-queue producer uses to choose between the
  building-build and mobile-build commands [07 "Factory product click producer
  (R-P0-11 §1)"], so a null value means an immobile unit. Only for such a unit
  is the shared order-admission predicate consulted first, and only such a unit
  can be skipped by it; a mobile member is always ordered. The reading of that
  field as "locomotion object" is **Supported inference** from the build-queue
  producer's use of it; its width and null test are established.
* Orders are resolved and submitted **per unit**, not through the group
  broadcast helper of §9, so the rally task is the only task whose order
  submission follows group-vector order rather than unit-pool order.
* The body ends by releasing a null temporary, which is a no-op.

#### R-AI-01 §8 — Candidate selection: the side filter — Established [R-AI-01]

The cumulative weighted reservoir is unchanged: walk the builder's build
options in authored order, score each with the candidate score of
[R-P0-05 §3][R-P0-05 §4], skip scores at or below zero **without drawing**, add
each positive score to a running total, draw once bounded by the running total,
and take the candidate when the draw is strictly less than that candidate's own
score.

**Correction.** [R-P0-05 §4] closed with "The builder's own definition name is
rejected as a candidate." That is wrong. After the reservoir finishes, the
selected definition's authored **`side`** string [fmt fbi] is compared, byte for
byte and case-sensitively, against the **builder's own `side`** string. When
they differ the whole selection is discarded and the caller is told "no
candidate"; when they match the selection stands. There is no re-draw, no
fallback to the runner-up, and no filtering of mismatched sides before the
draw — a build list containing another side's definitions therefore wastes
whole selections, and the wasted draw is still taken. The comparison is on the
30-byte `side` field, not on the unit name.

The score's third hard gate is also closed: it rejects the candidate when the
session mode word equals `1` **and** the candidate definition carries the
authored `downloadable` flag [fmt fbi]. That is the same flag that gates which
definitions the per-definition profile text of §12 is read from. The earlier
"the bit's authored semantic name is not closed" is retracted.

#### R-AI-01 §9 — Shared helpers: centroid, nearest hostile, group broadcast — Established [R-AI-01]

**Group centroid.** Returns false when the group vector is empty. Otherwise it
sums each member's three signed 16-bit **world-unit** position words (not the
16.16 words), divides each sum by the member count with truncation toward zero,
and shifts each quotient left 16 to produce a 16.16 point. The intermediate
sums are 32-bit, so a group large enough to overflow them is a fault the
executable does not guard.

**Nearest hostile unit.** Walks the ten player slots in ascending order. A slot
qualifies when its record is present, its control byte is `1`, `2` or `3`, its
alliance index is not the unassigned value `10`, and the querying player's
alliance table entry for that index is zero (not allied). Within a qualifying
slot it walks that player's unit slice in ascending pool order and considers a
unit when its **live** bit is set, its low two status bits are not the value
`2`, its **dying** bit is clear, and its runtime byte bit `0x4` is clear. The
metric is `((dx*dx) >> 32) + ((dz*dz) >> 32)` with each product taken as a
signed 64-bit multiply of the 16.16 deltas and shifted back — that is the
squared distance in world units, truncated. The best is kept on a **strict**
less-than, so the first minimum wins on ties and the iteration order (player
slot ascending, then pool ascending) is the tie-break. The initial best is the
maximum signed 32-bit value, and the helper returns "none" when nothing
qualifies. The vertical coordinate is passed in but never read.

**Group order broadcast.** Given a player, a group number, an intent, a queue
modifier, an optional target unit, an optional target position and a spacing
parameter, it walks the **player's whole unit slice in ascending pool order**
and, for every unit whose type index is non-zero and whose stored group number
equals the given one, resolves the intent and submits the order. Two
consequences are load-bearing: the broadcast order is unit-pool order, not
group-vector order, so it is stable across group-vector churn; and a unit whose
stored group number was changed since the vector was last rebuilt is included
or excluded by the **stored number**, not by vector membership.

#### R-AI-01 §10 — The classifier also writes standing orders — Established [R-AI-01]

The classification sweep of [R-P0-04 §3] does more than assign groups. For
**every** unit that passes its eligibility bit — including units that are
already grouped and therefore get no group assignment — it rewrites two fields
of the runtime status word before the ungrouped test:

* the **standing move order** field is set to `2` when the definition's
  authored `cancapture` flag is clear, and to `1` when it is set;
* the **standing fire order** field is set to `2` unconditionally.

Those are the same two runtime fields the interface's standing-order buttons
write and the save path restores [07 §8], [04 §3.4]; their authored defaults
come from `standingmoveorder` and `standingfireorder` [fmt fbi]. Reading the
values through doc 04's stance vocabulary, the computer player forces every one
of its units to **fire at will**, puts its capture-capable units (in stock
content the commander and the construction units) on **maneuver**, and puts
everything else on **roam**, refreshing all of it every 30 manager entries. Any
stance a script or a captured unit's history left behind is overwritten on the
next sweep.

This is the input the weapon-maintenance sweep of §15 gates on: it acts only on
units whose standing fire order reads exactly `2`, which is precisely what this
writer guarantees for the computer player's own units.

#### R-AI-01 §11 — Reaction to being attacked — Established [R-AI-01]

There is one reaction site, reached from the damage-application path for every
damaged unit, and it does two independent things.

**Construction throttle (computer players only).** When the damaged unit's
definition has the authored `cancapture` flag, its owning player record exists,
and that player's control byte is `2`, the executable draws `RNG(300)` and
writes the owning player's manager throttle deadline to
`tick + 30 + draw`, then clears the damaged unit's current order through the
ordinary stop path. Pass 1 of the construction task (§3) refuses to start a new
building from any `cancapture` builder while `tick < throttleDeadline`. So the
computer player's response to its commander or construction units taking fire is
to stop that unit where it stands and suspend commander-led construction for
between 30 and 329 ticks — one to eleven seconds — re-armed by every further
hit. There is no relocation, no escort, no counter-attack task, and no effect on
factory production or on the non-`cancapture` builders' repositioning pass.

**Retaliation (all controlled players).** Independently, when the attacker is
known, the victim's owner has control byte `1` or `2`, the victim's definition
is **armed** (its derived flag, set unless all three resolved weapon slots are
empty) **or** carries `kamikaze`, the victim is fully built, and the attacker is
not allied, then:

* if the victim has no current order — or its current order's gate mask carries
  the interruptible bit — and the attacker's type is absent from **both** the
  victim definition's no-chase and bad-target category bitsets [06 §3.2], the
  victim is given an attack order against the attacker through the ordinary
  order service;
* otherwise, when the victim's standing-fire field is non-zero (which §10
  guarantees for computer-player units), each of the victim's three weapon
  slots that is present and enabled is offered the attacker as a target: the
  attacker is assigned when it passes the slot's admission predicate and the
  slot's existing target does not already pass it.

Retaliation is therefore **not** computer-player-specific; it is the engine's
return-fire behavior and applies to human players' units too. What the computer
player adds is only the throttle above.

#### R-AI-01 §12 — Difficulty: vocabulary, profile grammar, and the economy effect — Established [R-AI-01]

**The difficulty word** takes the values `0` easy, `1` medium, `2` hard. It is
written from the registry/lobby setting and from the campaign difficulty
control, and read by the profile grammar, the mission trigger parameter tables
(doc 08 "Triggers"), and the resource transfer path below.

**Profile load order.** After the world is built, the mission's `aiprofile`
resource is resolved; on failure the literal `ai\default.txt` is loaded. The
file's text is parsed once, globally, through the ordinary tokenizer. Then, for
every player slot whose record exists and whose control byte is `2`, two
per-definition passes run in slot order.

**The three directives.** The keyword table is exactly `plan`, `weight`,
`limit`, with the difficulty vocabulary `any`, `easy`, `medium`, `hard`.

* **`plan`** sets a global gate. It clears the gate, then walks its arguments
  from index 1 to the last. For each argument it compares the **first**
  argument against `any` and the **current** argument against the keyword for
  the active difficulty, setting the gate on either match. The `any` comparison
  using a literal index rather than the loop index is a retail quirk: `any` is
  honoured only in the first argument position, and repeating it later has no
  effect. Reproduce the quirk. A `plan` with no arguments runs no iterations
  and therefore leaves the gate **clear**, disabling every directive after it
  until the next `plan`.
* **`weight`** runs only while the gate is set. Its first argument is a name,
  expanded into a per-type bitset by the shared matcher described below. Its
  second argument is a float, defaulting to `0.0`. For every player slot that
  has a manager (which includes human slots, not only computer slots) and every
  type in the bitset whose weight lock is clear:
  `weight = clamp(trunc(float(currentWeight) * value), 0, 100)`, stored as an
  unsigned byte, where `currentWeight` starts at the per-type default of 100.
  The clamp is "at or below zero becomes zero, at or above 100 becomes 100".
  When the matcher reported an exact naming, the type's weight lock is then set.
* **`limit`** also runs only while the gate is set, takes the same kind of name
  and an integer defaulting to `0`, and applies **only to slots whose control
  byte is `2`**. For every unmatched-lock type in the bitset it assigns the
  per-type limit and, on an exact naming, sets the limit lock. `-1` means
  unlimited; the construction score's fourth hard gate requires
  `completedCount < limit` [R-P0-05 §3].

**The name matcher and the two lock vectors.** The name argument of `weight`
and `limit` is first binary-searched against the definition catalog by authored
`unitname` [fmt fbi], which is the catalog's sort key. On a hit the matcher sets
**exactly that type's** bit in the bitset and reports an exact naming; on a miss
it instead ORs in the whole **category bitset** registered for that name
[02 "R-P0-03 — Category token registry and membership-bitset compilation"] and reports a non-exact naming. The lock is set
only for an exact naming. So `weight ARMCK 2.0` locks `ARMCK`'s weight against
any later per-definition text, while `weight LEVEL1 2.0` scales every member of
the `LEVEL1` category and locks none of them. Weight and limit keep **separate**
lock vectors.

**The per-definition profile text.** The two per-player passes walk the
definition catalog in ascending type order, skipping the zero sentinel, and for
every definition that carries the authored `downloadable` flag and whose
corresponding lock (weight lock in the first pass, limit lock in the second) is
not set, they parse that definition's authored **`ai_weight`** text [fmt fbi] as
a fragment of the same directive grammar, with the `plan` gate pre-set **open**
so a fragment that omits `plan` still applies. So a per-unit `ai_weight` string can
carry `plan`/`weight`/`limit` directives, and it is honoured only for types the
global profile did not lock.

**`ai_limit` has no reader — and now the mechanism is named.** Both passes read
the `ai_weight` field. The `ai_limit` field is parsed into its own 64-byte slot
by the definition loader and is never read by anything. The earlier statement
that "no semantic reader is found after the parse" stands, and the reason is
that the limit pass re-reads `ai_weight` instead of `ai_limit` — a retail defect,
not a missing trace.

**The difficulty economy effect.** Every player-to-player resource transfer —
metal and energy have separate but identical routines — first returns
immediately when either slot index is the unassigned value `10`. When the
routine's accounting flag is set it caps the amount at the **source's** current
stock and debits the source's ledger by that capped amount; a zero amount then
returns. The **destination** is credited next, and when the destination
player's control byte is `2` the credit is scaled by difficulty: `x 0.5` on
easy, `x 0.7` on medium, and unscaled on hard. The source's debit and the
transfer statistics — also updated only under the accounting flag — are **not**
scaled, so resource given to an easy computer player is half-destroyed in
transit.

**Correction — the closing sentence of this section is retracted.** It read:
"This is the whole of the documented 'difficulty scales controller-2 economy':
there is no production, build-rate, cost, or damage multiplier anywhere in the
computer player's path." The second clause is **false**. The same difficulty
ladder — easy `x 0.5`, medium `x 0.7`, hard unscaled, selected from the same
difficulty word — is applied inside the per-player economy settlement to
**every positive production contribution of every unit owned by a
control-byte-2 player**: at seven contribution sites in the settlement
accumulator, and once more on the per-player credit that follows it — eight in
all `[R-ECO-01 §3]`. Doc 05 owns that arithmetic, including the exact
`production := float32(production - (contribution * K))` form, which must be
reproduced literally because the factored form rounds differently.

The transfer finding above stands unchanged and is still Established. What was
wrong was the trailing negative — the claim that the transfer path is the
*only* place difficulty touches the economy. This section was not in a position
to make that claim: it had not traced the settlement accumulator, so the
"whole of" was a bounded absence dressed as a universal one. The remainder of
the retracted sentence (no build-rate, cost or damage multiplier) is likewise a
bounded negative and is **not** re-asserted here: it holds only as far as the
census that produced it reached, and this pass did not re-run that census over
the build, cost and damage paths.

#### R-AI-01 §13 — The half-capacity comparison, closed — Established [R-AI-01]

[R-P0-05 §5] and [R-P0-05 §8] carried an open item: "A global half-capacity
comparison may contribute half of the single coefficient; its strategic-state
writer is not located and stock state leaves that branch false." Both halves of
that sentence were wrong about **where** the compared field lives.

The compared value is **not** a field of the strategic state. The class routine
loads the strategic state's first word — the back-pointer to the owning **player
record** — and reads a 16-bit field of that record. That field is the player's
**live unit count**: it is incremented at unit creation, decremented in unit
teardown, and reaching zero is the player-elimination trigger; it is also the
"player still alive" predicate the sharing and endgame paths use
[05 "Player slot"], [08 "Session end and reporting"].

The global it is compared against is the **per-player unit limit** — the
`Max Units` lobby option, or the mission's unit-count key — copied once into a
16-bit session word when the world is built.

So the branch is:

```text
if (unitLimit >> 1) < player.liveUnitCount:        # unsigned
    otherMixCoefficient += singleCoefficient / 2   # signed byte / 2, truncating
```

It fires for any player that owns **more than half its unit cap**, which is
ordinary late-game state, not an unreachable branch. The earlier "stock state
leaves that branch false" is retracted: it was false only because the wrong
base pointer was assumed. Implementations must read the owning player's live
unit count, not a strategic-state field, and must not initialize an opaque
field to zero to model it.

#### R-AI-01 §14 — The `AI:%s` diagnostic — Established [R-AI-01]

The player-slot initializer builds the slot's display name. When its mode
argument is `1` it copies the locally configured player name; otherwise it
formats `AI:%s` with the **name of the player record at the local slot index**.
So a computer slot is named `AI:` followed by the local human's name, not by a
personality or profile name, and two computer slots in the same session receive
the same string. The format string is reproduced verbatim, with no space after
the colon. It is a name, not a log line: it is written into the slot record and
appears wherever slot names are shown.

#### R-AI-01 §15 — The weapon-maintenance sweep — Established [R-AI-01]

The sweep runs at the end of every manager entry, including entries where the
outer computer gate failed (with its argument `0` instead of `1`). It advances a
**rotating cursor** stored on the manager: the cursor starts at the player's
first unit slot, advances one unit slot per iteration, and wraps to the first
slot when it reaches the last. The number of iterations per call is
`(unitLimit / 30) + 1`, using the same session unit-limit word as §13 — so the
sweep covers a full unit-limit's worth of slots about once every 30 ticks,
matching the classifier's cadence.

A unit is processed when its type index is non-zero, it is fully built, it is
**armed**, and its standing fire order field reads exactly `2` (§10). Each of
its three weapon slots is then processed when the slot is present and enabled,
the weapon's own disable bit is clear, and — only when the manager's argument
was `0`, that is on the path taken when the outer computer gate failed — one
further weapon flag is also required to be clear. That last test excludes a
single weapon class from the non-computer path; it does not exclude the unit.

For each such slot the current target is fetched and rejected when the target's
owner is allied, when the target's type is in that slot's bad-target category
bitset, or when the weapon is flagged as unable to hit the target's current
state. A rejected or absent target is replaced by the ordinary autonomous
acquisition path [06 §3.1]; one weapon class instead consults a dedicated
interception query, and a failed acquisition clears the slot's target. No
random numbers are drawn.

The detailed acquisition arithmetic is doc 06's [06 §3.1]; what this section
establishes is that the computer player has **no** targeting logic of its own —
it reuses the engine's acquisition, and its only contribution is the cadence,
the fire-at-will precondition it writes itself, and the rotation order.

#### R-AI-01 §16 — Strategic refresh: two corrections and one new unknown — Established [R-AI-01]

The 30-tick strategic refresh ([08 "Strategic state construction and refresh"],
[R-P0-04 §3]) also maintains two scalars this document did not name:

* the **build-capable count** used by the construction task's two gates (§3):
  cleared at the top of the refresh and incremented once for every own live,
  completed unit whose definition has a non-empty build-option list;
* a **targeting-upgrade present** flag, set when any own unit whose definition
  carries `istargetingupgrade` is active. Its reader is closed by
  [04 R-SPEC-01 §8] (2026-08-29): the target-registry rebuild sets a per-player
  flag from it, and the registry's area enumeration falls back to radar-only
  contacts only when the visible scan is empty. (Earlier text: "Its reader is
  **Unknown** (§17)" — superseded by the doc 04 trace.)

The three refresh vectors are also now named by predicate: the first collects
**non-allied** live units that pass the ordinary visibility predicate and are
not dying; the second collects non-allied units carrying one further runtime
status bit; the third collects the player's **own** active units whose
definition is both a builder and an air base. Only the first has a located
consumer in this lane — the rally task's probe score (§7).

The centre arithmetic is: for each own completed unit, `w` is the
**initialization-only** per-type coefficient byte ([R-P0-05 §5]); accumulate
`coordinate / 65536 * w` per axis and `w` into a weight sum, all in 32-bit
float; divide each axis by the weight sum **only when that sum is non-zero**;
multiply each axis by the double constant `65536.0`; and truncate toward zero
into the stored 16.16 centre. When the weight sum is zero the centre is the
truncation of the unscaled accumulators, which are then zero — that is the state
the explore task's "centre is unset" test detects (§6).

#### R-AI-01 §17 — Open items this unit did not close — Unknown [R-AI-01]

* The semantic name of the order gate-mask bits the construction task tests —
  bit 3 in pass 1 and bit 14 in pass 2 [04 "Order descriptor table"] · doc 04
  owns the mask · static trace over the descriptor table's consumers.
* Which global option bit selects the rally task's two probe-validation forms,
  and therefore whether the local-viewer-dependent form is reachable in a
  networked session · §7 · static trace from the skirmish option word into the
  world flags word (RWU-08-2 owns the option side).
* (Closed 2026-08-29 by [04 R-SPEC-01 §8]; see §16.) ~~The reader of the
  refresh's targeting-upgrade flag.~~
* Whether the wave's `engaged` latch is serialized; the save path's task-record
  coverage was not re-read by this unit · doc 08 "Save-file organization" ·
  static trace (RWU-08-4).

### What remains not established — Supported inference and unknown [P0-01] [P0-02] [P0-03]

The class routine's per-definition inputs now have recovered field identities — extracts-metal, makes-metal, can-attack, builder, can-fly, can-load, is-feature, max-slope, radar and sonar distance, and wind-generator — as runtime field mappings (R-P0-05 §5). Audit: this doc previously claimed the addend-gating flag names were not closed; the field-level census recovered those identities, so the residual narrowed to the semantic name of the classification helper and the "strategic half-capacity state writer". The second of those is now closed and was a misreading of the base pointer — the value is the owning player record's live unit count ([R-AI-01 §13]) — so the only residual of this paragraph is the classification helper's semantic name. The classifier's two high runtime status bits retain opaque semantic names while their tests and destinations are fixed (R-P0-04 §3). The weapon table field identities are closed: the class routine reads the `DAMAGE/default` word (damage divided by 40) and the `range` word (range divided by 100); `reloadtime` is not read by it (the earlier "reload divided by 100" was inference and is retracted). The metal score returned by the placement helpers is established as the footprint per-cell metal-byte sum (the exhaustive helper maximizes it; the scatter helper compares it against surface metal times footprint X times footprint Z times two), and water legality is the yard path's waterline band.

The population of the nine manager task group vectors is now positive-static
for the direct writer and the six classifier destinations; no additional
distinct writer is located in the whole-image direct-store/xref census. No
capture-specific caller of the direct manager-group writer was found. AI
transport geometry and any distinct naval or air expansion policy beyond the
generic move orders are not in this lane and remain unknown. The x87
control-word edge beyond the established narrowing to float32 at every helper
invocation boundary remains an unknown of platform residual class; the default
rounding mode is assumed.

**Audit (2026-08-28, RWU-08-1).** Two statements above have been superseded and
are removed rather than restated: the classifier's "two high runtime status
bits retain opaque semantic names" was already retracted by [R-P0-04 §3] (they
are the building-class and armed bits), and the claim that what a populated
group would make the task bodies do "is derivable from the task body
decompilations" but not written down is no longer true — the six first virtual
slots are specified in [R-AI-01 §1]–[R-AI-01 §9]. What is still not established
for the computer player is the short list in [R-AI-01 §17] plus the placement
geometry residual in [R-P0-05 §8].
[P0-02] [P0-03] [R-P0-04] [R-AI-01]

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
only when switching front-end states, never per tick. Residual: the producer
algorithm of packet `0x27`'s twelve opaque trailing bytes is unresolved; the
exact three-byte throttle-acknowledgement format in the `0x28` receiver is
not fully recovered. (The earlier residual "a separate orchestrator
referencing numbered `.zrb` files remains untraced, including its guard
relationship to the checksum path" is withdrawn: the `.zrb` files are the
five Smacker cinematics and their sequencer is the front-end movie player,
[R-OOS-01 §4]; it has no relationship to the checksum path.)

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

### Closed — the single-player boundary [R-OOS-01 §1] — packets the local path still constructs (2026-08-29)

Established by RWU-08-6 from a full read of the two send helpers, the
receiver's entry, every constructor that hands a packet to a send helper (42
call sites) and the gate at each site. Nanolathe implements neither
DirectPlay nor the lobby; this block states exactly where the single-player
(kind 1 campaign, kind 2 skirmish) contract stops. §1 packets, §2 lobby
record, §3 the out-of-scope subsystems, §4 video, §5 corrections. The raw
trail is kept out of the repo.

**The two send helpers (Established).** Every packet the engine emits goes
through one of two helpers. The *broadcast helper* first validates the
sender (its slot must be live, its controller 1 or 2, its dropped byte
clear; otherwise it returns 0 without sending). Then, **when the session is
not networked, it returns 1 without sending anything** ([01 R-PLAT-01 §3]).
The *direct helper* (one peer) applies the same sender test plus a target
test (the target must be a remote, controller-3, undropped slot) and the
networked test together, and **returns 0 when the session is not
networked**. The "networked" bit is set at exactly one site — after the
DirectPlay lobby-launch check succeeds in the front-end router — and is
never set by the campaign, skirmish or load paths. No single-player caller
inspects either helper's return value except the four script-run senders,
which pass it up to interface callers that ignore it.

**The receiver (Established).** The packet receiver returns 0 before
touching any state when the session is not networked. Its single-player call
sites — every later frame of the loading state, every frame of the
end-of-battle report screen, the map-select refresh — are therefore no-ops;
the tick executor calls it only on the multiplayer branch of the battle pump
([01 §4.3]). Consequently no packet built in single player is ever
*received*: the local effect of every packet below is the caller's own
mutation, made before or after the (non-)send, never the handler's.

**Packets constructed in kinds 1 and 2.** Type, declared length ("Declared
packet types"), the single-player site, the gate at the site, and the local
effect:

| Type | Len | Constructed by | Gate at the site | Local effect (already applied by the caller) |
|---:|---:|---|---|---|
| `0x05` | 65 | chat commit: the `<%s%s%s> %s` line copied into a 64-byte text field | recipient mode 0, or a line beginning `+`, uses the broadcast helper; mode 3 the direct helper per marked slot; else the direct helper to one slot | the same line is then posted to the local message ring ([07 R-CAM-01 §2]) in every kind; the `+` dispatch has already run (§2) |
| `0x06` | 1 | the keepalive request | loading state, every later frame, once per live human/computer slot (every kind); the battle pump's 60-unit keepalive is multiplayer-only | none |
| `0x09` | 23 | unit allocator | none | the unit is already allocated ([05 R-SHARE-01 §8]) |
| `0x0a` | 7 | occupancy/placement change (28 callers: order handlers, the death handler, the two placement helpers) | none | the caller applies the identical record locally right after the send |
| `0x0c` | 11 | unit death | owner controller 1 or 2 | the death handler runs on the same record immediately after ([06 §12.1]) |
| `0x0d` | 36 | the seven projectile spawn sites ([06 R-WPN-03 §1/§2]) | none | projectile already created |
| `0x0e` | 14 | impact dispatch, two records per pair ([06 §9.3]) | none | impact already applied |
| `0x0f` | 6 | tree burn (sub-case `0xfe`, when its third argument is 0) and feature damage (sub-case from the feature's byte, when above `0xfc`) | none | feature already ignited/damaged ([05 R-FEAT-01 §8/§9]) |
| `0x10` | 22 | the four script-run senders (`StartBuilding`, `BeginTransport`, `AimPrimary…`, and the generic form) | none | at the traced site (`StartBuilding`, interface dispatch) the script has already been invoked locally through the COB entry before the sender is called ([04 R-COB-01]); the other sites were not re-read for ordering |
| `0x11` | 4 | unit state transition | owner controller 1 or 2 | transition already applied |
| `0x12` | 5 | build link | none | link already made |
| `0x13` | 18 | the two sound-cue emitters ([03 R-AUD-01 §1]) | audio device open, channel mask non-zero, not muted | the sound is then started locally |
| `0x19` | 3 | pause `{0x19, 0, bit}` and speed `{0x19, 1, speed}` | none | pause bit / speed words already written ([01 R-PLAT-01 §3]) |

**Never constructed in kinds 1 and 2 (Established, gate named).** `0x0b`
damage — built only when the victim's owner is a controller-3 (remote) slot;
`0x14` ownership transfer — only when the new owner is controller 3; the
`0x0f` sub-case `0xff` — kind 3 only; `0x1e`/`0x2a` loading progress — kind 3
only; `0x20`/`0x24` lobby-record bulk and its reply — the sender tests the
networked bit; the `0x2c` bit-stream — its one caller tests the networked bit;
`0x02`/`0x26` probe and roster, `0x08`, `0x17` — reachable only from the
network pre-load state (state 3, installed from the lobby screens) and the
lobby screens; `0x1b` slot drop — built by the drop routine, whose callers are
the `Reject <player>` confirmation, the lobby map viewer and the
`TIMEOUT.GUI` screen; `0x28` — the scanner thread, started only on the
networked path; the one-byte peer-ready poll — the kind-3 barrier. Kinds 1
and 2 never hold a controller-3 slot: entry writes controllers 1 and 2 only
([R-SKIR-01 §2], [R-ENTRY-01 §2]); a Gametype-2 save restored through the
`Player%i` accounts is the one path that could — see "Multiplayer saves".

**Implementation consequence.** A clean-room single-player engine needs no
packet framing at all: every local mutation is made directly by the caller.
The table exists so that the *ordering* retail imposes (chat posted after
the `+` dispatch; the death handler after the death record is built; the
sound started after its record) can be preserved where a doc cites it, and
so that the pause/speed contract of [01 R-PLAT-01 §3] is read as a pure
local bit flip.

### Closed — the single-player boundary [R-OOS-01 §2] — the lobby record and `Cheat Codes` (2026-08-29)

**Fields of the per-slot lobby record the single-player session reads
(Established).**

1. **Loading state, first frame, every kind:** the *watching* table is
   filled from lobby-word bit 6 of every live slot's record beside the
   participation table ([R-ENTRY-01 §1]). Writers of that bit in the
   recovered set are the wire copy of packet `0x20` and the battleroom only,
   so in kinds 1 and 2 the table is all-zero (Supported inference — the
   table's readers were not traced; decider: static trace of the table's
   readers).
2. **Battle-entry tail, every kind** ([R-ENTRY-01 §8]): the local slot's
   record has its low option nibble and two version bytes rewritten and its
   four option words and unit-limit word read back into the setup summary.
3. **Kind 2 unit limit** comes from the configured-limit global, not from a
   lobby record ([R-SKIR-01 §6]).
4. The record's *started* bit is only ever cleared by local code; its setter
   is the wire copy. Every reader of it (the state-3 router, the host-slot
   finder used by `TABMENU` in kind 3 and by `RESTRICT2`, the feature-damage
   sender's host lookup, peer removal) therefore sees 0 in kinds 1 and 2
   (Supported inference: bounded writer census).

**`Cheat Codes` — closed.** The lobby word's bit 13 has exactly one writer:
the battleroom's cheat toggle (the gadget state `2` sets it, any other
non-zero state clears it — [R-SKIR-01 §11] names the overlay that displays
it). It has one consumer outside presentation: **battle entry** copies it
into a process word — kind 1 writes 0, kind 2 writes 1, kind 3 writes the
host's bit — and that word has exactly one reader, the chat commit callback,
which ORs **route bit 2 into the `+` dispatcher's route word** when the word
is non-zero. Route bit 2 is what the mask-2 (cheat) command table matches
([07 R-CAM-01 §6]). So: **in skirmish every mask-2 command dispatches; in
campaign none does** (unless the developer bit supplies route `7`); in
multiplayer they dispatch iff the host allowed `Cheat Codes`. The `+`
dispatcher itself and the tokenizer never read the lobby word; the local
path consults the lobby *bit* only through this entry-time copy. The
doc 07 tail's "how the multiplayer receive path applies the lobby bit before
re-dispatching a received `+` line" stays open and out of scope.

**Correction to [R-ENTRY-01 §2].** That section said the word was "a
chat-routing flag, not a simulation authority" whose reader "ORs a routing
bit into the message's target mask". The reader was right, the destination
wrong: the OR goes into the command dispatcher's *route* word, not the
message's recipient mask (the recipient mode is forced to *everyone* only
afterwards, and only when the dispatched entry's mask had bit 2 —
[07 R-CAM-01 §6]). The word is the **cheat-enable gate**. The tail item
"Which chat targets the routing bit … selects" is closed by this.

### Closed — the single-player boundary [R-OOS-01 §3] — subsystems entirely out of scope (2026-08-29)

The coverage ledger (raw corpus) tags a function OOS only when every caller,
transitively, is lobby, DirectPlay, network or video code; a fixpoint over
the whole call graph was run this unit and each pre-existing tag sampled.
After this unit the OOS set is 247 functions:

| Subsystem (ledger cluster) | Functions | What it is |
|---|---:|---|
| network packet drain | 113 | the receiver switch, custom-envelope encode/decode/compression, per-peer queues, the scanner thread, the `0x20`/`0x24` lobby-record broadcasters, the DirectPlay wrapper and its `DPERR_*` text table |
| `LOUNGE2.GUI` (battleroom) | 67 | the heartbeat of "Lobby behavior", its row/ready/host/edit logic, the cheat/watching/fixed-location toggles |
| front-end state machine (lobby part) | 16 | DirectPlay lobby connect, session enumeration, the `DPLAY CONNECTION` registry block, the network pre-load state |
| `MODEM.GUI`, `SELPROV.GUI`, `NEWMULTI.GUI`, `SERIAL.GUI`, `TCP.GUI` | 13 + 12 + 6 + 4 + 3 | provider selection and connection screens |
| `VIEWMAP.GUI`, `SELMAP` lobby handler, `TIMEOUT.GUI`, `RESTRICT2.GUI` | 5 + 1 + 1 + 1 | the lobby map viewer, the lobby's map-select handler, the peer time-out dialog, the restriction editor's row sorter ([R-SKIR-01 §10]) |
| startup and shell pump | 3 | the `-N`/`-H` lobby-launch command-line handlers ([01 R-PLAT-01 §2]) |
| battle host pump, unclustered | 1 + 1 | the DirectPlay guaranteed-flag setter; one accessor |

Nine functions previously tagged OOS were on single-player paths and are
retagged (the broadcast helper; the slot↔id lookups; the host lookup before
the feature-damage packet; one script-run sender; the mover record
constructor and destructor; the per-unit account constructor; the
battle-entry lobby refresh) — their contracts live in the sections the
ledger now cites. The *sections* of this document that are out of scope in
their entirety are "Peer transport state", "DirectPlay transport", "Packet
framing and dispatch" beyond the length/mask table §1 relies on, "Send pacing
and batching", "Receive buffering", "Ping and adaptive timing", "Lockstep
advancement" beyond the single-player scheduler facts it restates from doc
01, "Synchronization and integrity checks", "Disconnect, resign, and peer
loss", "Multiplayer saves" and "Lobby behavior"; their tail items are kept
for exhaustiveness only.

### Closed — the single-player boundary [R-OOS-01 §4] — the video boundary (2026-08-29)

**What the single-player path plays (Established).** Five cinematics,
`1.zrb` … `5.zrb`, resolved through the movie path joiner of
[R-CAMP-01 §5], are played by the front-end router: at start-up, when the
display is full-screen, `1.zrb` then `2.zrb` when the `PlayMovie` registry
word is non-zero (miss → 1; the word is then written 0, so this runs once per
install), else `1.zrb` alone unless the restricted-config flag of
[01 R-PLAT-01 §2] (the `-N` switch) is set; the ending states of
[R-CAMP-01 §6] play `3.zrb` or `4.zrb` followed by `5.zrb`; a further shell
state plays `5.zrb` alone. The "`.zrb` orchestrator" that "Economy and
integrity checks" listed as untraced is this sequencer; it has no relation
to the checksum path (correction below).

**What the sequencer does around the library (Established).** For one
file: stop all sounds; join the path; **a missing file is skipped
silently**; lock, clear and unlock the display; hide the cursor; then
exactly one open → play → close pass (the repeat word that would loop it is
written only by a dead debug routine); then the input queue is drained and
the display cleared again. The open step reports, fatally through the modal
of [R-ENTRY-01 §1], `Could not open movie file, please check filename in INI.` when
the library cannot open the file, `Could not setup Direct Draw to play
movie.` when no surface can be created, and the message box
`Smacker Error` / `Unsupported pixel format.` when the display is neither
8-bit indexed nor one of four 16-bit layouts (565, 555, a 565 variant with a
6-bit green mask, and 655 — [03 §9]). Sound is enabled for the movie iff the
display's full-screen flag is set.

**What the frame loop observes back (Established).** It is a
`PeekMessage` pump: with no message pending it asks the library whether the
next frame is due and, when it is, steps one frame — only while the window
has focus; frame cadence is therefore the library's (the file's frame rate),
never the engine's 30 Hz clock, and the engine reads nothing but "due" and
"palette changed". Playback ends when the frame index reaches the frame
count − 1, or on **any character key** (`WM_CHAR`), or on Alt+F4
(`WM_SYSKEYDOWN` with F4, which also posts the quit message). Nothing about
the codec, frame buffers, audio mixing or ordinals is part of the contract;
Nanolathe may substitute any decoder that honours "play once, skip on any
character key, resume the shell".

### Closed — the single-player boundary [R-OOS-01 §5] — corrections (2026-08-29)

1. **[R-ENTRY-01 §2]** — the word written per kind at battle entry is the
   cheat-enable gate of the `+` dispatcher, not a message-target routing
   flag (§2 above).
2. **"Economy and integrity checks"** said "a separate orchestrator
   referencing numbered `.zrb` files remains untraced, including its guard
   relationship to the checksum path". It is the movie sequencer (§4) and
   has no guard relationship to any checksum; the residual is deleted there.
3. **Doc 07 [R-CAM-01 §6]** — "the single-player path consults no cheat gate
   — every mask-1 and mask-2 command below is live in skirmish and campaign"
   is wrong for campaign: mask-2 commands are gated off in kind 1 (§2).
   Doc 07 is corrected by its owner; this document records the finding.


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
`LoadAccount::Decompression...` diagnostic through the **fatal** channel
(system-modal box, then exit code 1 — [02 R-MALF-01 §11]; corrected
2026-08-29, this sentence previously said "but parsing continues").

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
| `Meteor` | nine typed **integer** items, written and read in this order: `Enabled`, `Active`, `Next Strike Time`, `Time Strike Ends`, `Next Hit Time`, `Origin X`, `Origin Z`, `Target X`, `Target Z`. The first five are 32-bit globals stored and restored whole; the four coordinates are 16-bit globals **sign-extended** to the item's integer width on write and truncated back to sixteen bits on read. Every load uses a default of `0` for a missing or wrong-typed item, so an absent `Meteor` account silently disables and de-activates the shower rather than failing the load. See the writers below. |
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

**Established — the meteor writer and reader, with types.** The meteor shower
is the smallest and most literal example of the field-by-field shape above, and
worth writing out because it is the whole of the shower's persistence. The
writer selects the `Meteor` account by name and then emits nine **integer**
items in fixed order — `Enabled`, `Active`, `Next Strike Time`,
`Time Strike Ends`, `Next Hit Time`, `Origin X`, `Origin Z`, `Target X`,
`Target Z` — one per global, through the bank's named-integer item writer,
which stamps each item with the integer type tag. The reader selects the same
account and reads the same nine names in the same order through the
named-integer accessor, each with an explicit default of `0`; that accessor
returns the default both when the name is absent and when the stored item
carries a different type tag, so no meteor read can fail the load. The first
five globals are 32-bit and round-trip exactly. The four coordinates are 16-bit
globals: the writer sign-extends each to the item's integer width and the
reader stores only the low sixteen bits back, so a value edited outside that
range in a save file wraps rather than being rejected. Nothing else about the
shower is persisted — the authored radius, spacing, duration and interval are
not in the account, so a load reuses whatever the map's own meteor parameters
installed `[06 §6.5]`. The shower's own arithmetic is doc 06's.

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
- per-unit accessory and mobile boxes restore attachment and mover state —
  the "mobile" box is keyed `u%04xmob` and is the unit's **mover** record, not
  mobile-*builder* state (correction, 2026-08-28, RWU-04-1: the earlier
  wording read "mobile-builder state", which named the wrong subsystem; the
  box is written and read by the mover's own serializer and its fields are
  listed in the tail item below and in [04 §8.1 R-MOV-01 §1]);
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

#### R-SAVE-ORDER-01 — Per-unit order records and subtype payloads

This addendum closes the save boundary for the dynamic order list. It
supersedes the earlier statement that the 58-byte order record was only a
length observation. The offsets below are positions in a save-file box, not
native object layout. All integers are little-endian. The main record is
exactly `0x3A` bytes and is named `u%04xm%04x`, where the first number is the
parent unit's stable slot and the second is the order sequence emitted by the
writer. [Established; [01 §6.1], [04 §3.1–§3.3]]

**Main record map.** The first two identifiers are logical pool references;
they are never native pointers. The twelve words after the two one-byte
header fields are copied as words, including fields whose high bits are not
currently named by a consumer.

| Save bytes | Wire form | Restored order state |
|---|---|---|
| `0x00..0x01` | `u16` | Parent unit stable slot. It must equal the unit selected by the box name. A mismatch does not prevent retail from allocating/linking a default node; a transactional reader must reject the image before commit. [Established] |
| `0x02..0x03` | `u16` | Linked unit stable slot, or zero for null. Resolve against the staged fixed-slot unit table after all base units are allocated. The exact order relation represented by this link is subtype/handler-owned and remains Unknown. [Established reference; Unknown relation] |
| `0x04..0x07` | `u32` | Subtype code. Zero means no `g` payload. Codes 2–6 select the exact subtype sizes below; other nonzero codes are not established and must not be guessed. [Established wire dispatch; Unknown other codes] |
| `0x08` | `u8` | Descriptor ordinal in the final case-sensitive sorted descriptor table. Zero is the empty/reject descriptor. [Established; [04 §3.1]] |
| `0x09` | `u8` | Handler-private phase/state byte. Restore without normalization; handlers own its interpretation. [Established] |
| `0x0A..0x0D` | `u32` | Dynamic gate/wake word copied from the order record. Its named low-bit consumers are the pump's pending/capability gates; retain all bits. [Established; [04 §3.2–§3.3]] |
| `0x0E..0x11` | `i32` | Deadline tick, with `-1` meaning no deadline. Do not clamp or convert this absolute value. [Established; [04 §3.3]] |
| `0x12..0x15` | `u32` | Goal X, 16.16 fixed-point. [Established; [04 §3.2]] |
| `0x16..0x19` | `u32` | Goal Y, 16.16 fixed-point. [Established; [04 §3.2]] |
| `0x1A..0x1D` | `u32` | Goal Z, 16.16 fixed-point. [Established; [04 §3.2]] |
| `0x1E..0x21` | `u16,u16` | Guard/fight anchor pair. Preserve both signed 16-bit words; the exact axis labels are handler-owned. [Established wire role; [04 §3.2]] |
| `0x22..0x25` | `u16,u16` | Cached target-position pair. Preserve both signed 16-bit words; validity is controlled by the gate mask, not by zero/nonzero inference. [Established wire role; [04 §3.2]] |
| `0x26..0x29` | `u32` | General parameter 1. Family meanings include weapon slot/stance, build definition/template index, and guard standoff; the descriptor handler is authoritative. [Established; [04 §3.2]] |
| `0x2A..0x2D` | `u32` | General parameter 2. Commonly remaining build count or an operation-specific progress/stance value. Preserve exactly. [Established wire copy; [04 §3.2]] |
| `0x2E..0x31` | `u32` | General parameter 3. For mobile build this is the blocked-area retry counter; other family meanings are handler-owned. [Established; [04 §3.2]] |
| `0x32..0x35` | `u32` | Static descriptor/queue flags. In particular, the `0x40000` bit selects the rear segment; active, purge, tombstone, and other bits are retained for queue behavior. [Established; [04 §3.1–§3.3]] |
| `0x36..0x39` | `u32` | Accumulated satisfied-gate bits. Restore before the first pump so a saved wake/satisfaction state is not silently discarded. [Established; [04 §3.2–§3.3]] |

The descriptor's canonical name is written as the string item
`${box}_name`. On load, a present name is preferred; an absent name permits
ordinal lookup. A present but unknown name leaves the newly allocated order at
its defaults rather than silently selecting a different descriptor. The
ordinal is therefore a compatibility fallback, not a second interpretation of
an unresolvable name. The empty descriptor (ordinal zero or empty name) is a
reject/null identity and must not be pumped as a real command. [Established;
[04 §3.1], [01 §6.1]]

For build-family orders, the definition name is carried by the existing
`UTYPENAME%4d` string table. It remaps the definition index in parameter 1
after the current catalog is loaded; an unknown definition remains unresolved
and is a validation failure for a transactional restore. This side channel is
not an order descriptor name and must not be used to infer a command ID.
[Established; [02 §5], [05 "Factory production lifecycle"]]

**Queue segment and order.** The descriptor's static `0x40000` bit chooses the
secondary/rear list; all other records use the primary/front list. The loader
enumerates sequence numbers in ascending order and inserts records in that
order within each segment. It does not sort by descriptor, subtype, target, or
creation tick. Consequently the stable order is the writer's traversal order,
with primary and secondary relative order preserved independently. The active
marker is a serialized flag and must be validated against the queue invariant
(at most one active primary record); queue reconstruction must never derive it
from a native next pointer. An image with no primary record has no active
primary marker. [Established queue split and flag copy; Supported inference for
writer traversal/order from sequential box naming and list insertion; [04
§3.3]]

Stage records detached from the live queue, resolve `0x02` and every subtype
unit identifier against the complete staged unit table, then bind the queue to
the owning session before making either list reachable. The binding must be in
place before the first order pump; no post-load world sweep is required or
permitted. This is the transactional equivalent of retail's per-unit
reconstruction, which allocates a node, links it into the selected list, and
then performs handler/presentation registration. [Established retail order;
Supported inference for the pre-commit binding boundary; [01 §4.4], [04
§3.3–§3.5]]

**Subtype boxes.** A nonzero subtype code names one additional box `${box}g`.
The reader requires the exact size shown. Every leading “leak” range is copied
from the writer's scratch/prefix area but has no established consumer and is
discarded by the retail reader; it is not a semantic field. The remaining
words are exact payload positions. Where direct evidence identifies a unit
identifier, it is resolved as a stable slot only after all units exist. All
other payload words are opaque subtype state until their handler defines them.

| Code | Exact size | Save payload map |
|---:|---:|---|
| `2` | `0x36` | `0x00..0x07`: leaked prefix, discard. `0x08..0x09`: unit stable slot. `0x0A..0x19`: 16-byte temporary/reference area; the reader consumes it as scratch and does not install it as a pointer. `0x1A..0x1B`: second unit stable slot. `0x1C..0x25`: five little-endian `u16` payload words. `0x26..0x35`: four little-endian `u32` payload words. The semantic subtype name and the relation represented by either unit are Unknown. [Established widths and reference positions; Unknown meanings] |
| `3` | `0x2A` | `0x00..0x07`: leaked prefix, discard. `0x08..0x09`: unit stable slot. `0x0A..0x0B`: one `u16` payload word. `0x0C..0x23`: six `u32` payload words. `0x24..0x29`: three `u16` payload words. The unit reference's subtype relation is Unknown. [Established widths and reference position; Unknown meanings] |
| `4` | `0x10` | `0x00..0x03`: leaked prefix word, discard. `0x04..0x0F`: three `u32` payload words. No logical reference is established. [Established] |
| `5` | `0x18` | `0x00..0x03`: leaked prefix word, discard. `0x04..0x17`: five `u32` payload words. No logical reference is established. [Established] |
| `6` | `0x14` | `0x00..0x03`: leaked prefix word, discard. `0x04..0x13`: four `u32` payload words. No logical reference is established. [Established] |

The subtype table deliberately does not assign names such as target, weapon,
path, or construction state to the opaque words. The bounded writer/reader
census proves widths, copy positions, and the two code-2 plus one code-3 unit
references, but it does not prove the subtype dispatcher's public vocabulary.
Those words must be retained in a staged raw payload (or a typed structure
whose unknown fields are losslessly preserved). Treating the 16-byte code-2
temporary area as a native pointer would create a dangling reference and is
specifically incorrect. [Established; [04 §3.2], [06 §11.1]]

**Fix-up and failure rules.** The minimum deterministic reconstruction order is:

1. Validate the `0x3A` main-box length, parent ID, descriptor side channel,
   subtype code/length pair, and all referenced stable IDs against the staged
   unit image. Validate sequence numbers as nonnegative box suffixes and keep
   their ascending order; do not compact missing records silently.
2. Allocate a zeroed order image with the owning unit, descriptor identity,
   phase, twelve words, and unresolved logical IDs. Resolve the build name and
   remap parameter 1 before the node becomes reachable.
3. Decode each exact-size subtype into its staged payload, retaining opaque
   words and recording the code-2/code-3 unit references.
4. Link each node into its selected primary or secondary segment in sequence
   order, establish exactly one primary active marker when primary is nonempty,
   then install session QueueBinding and subtype target references.
5. Run the established order registration/derived fix-up, followed by script
   and visibility publication at the enclosing unit transaction boundary. The
   first authoritative pump runs only after publication.

Retail is permissive and non-transactional at these boundaries: a short main
box still leaves an allocated node with later words at defaults; a parent-ID
mismatch can leave a default node linked; and a wrong-size subtype leaves its
subtype object at defaults after the exact read fails. A missing or unknown
descriptor name likewise does not abort the retail load. A transactional
reader must instead reject these cases before commit so a malformed image
cannot partially replace a live queue. [Retail behavior Established;
transactional rejection Supported inference; [01 §4.4], [08 "Unit and script
records"]]

The save does not contain native next links, queue anchors, active-marker
ownership, handler callback state, movement path objects, or presentation
payload pointers. It also does not persist any random stream state. These are
rebuilt or reset by the owning session/order/script passes; preserving leaked
prefix bytes cannot restore them. [Established omission; [01 §4.4], [04 §3.3,
§3.5]]

**Closure and residual probes.** The wire contract is sufficient for a
transactional *wire-level* queue image: a loader can validate exact lengths,
preserve every serialized word, maintain primary/secondary sequence, and fix
up all explicitly represented unit IDs before publication. It is not yet
sufficient to expose semantic typed constructors for subtype codes 2–6. The
remaining Unknowns are the subtype family names, the relation of each unit
reference, the meaning of code-2's temporary 16 bytes, and the meanings of
the opaque numeric payload words. They must remain raw/opaque until a bounded
handler census or retail probe closes them. [Established closure boundary;
Unknown semantics]

Implementation-ready probes are small and format-local: round-trip one record
for each code at exactly its accepted size; change one payload word at a time;
test code-2/code-3 references as zero, valid, missing, duplicate, and cyclic;
reverse sequence box order and assert per-segment insertion order; omit the
name side channel, use an unknown name with a valid ordinal, and use an
unknown ordinal with a valid name; truncate the main record and each subtype
by one byte; and verify that no leaked prefix byte or short-read residue
changes the staged semantic fields. Assert that all tests fail before commit
for a transactional reader, while a retail-compatibility fixture may record
the documented partial/default result separately. [Supported inference for
transactional assertions]

### Feature records

#### R-SAVE-FEATURE-01 — Feature record maps and staged reconstruction

This addendum closes the feature portion of the battle image. It supersedes
the preceding one-sentence description of the three partitions. The offsets
below are positions inside save-file boxes, not executable layout. All integer
fields are little-endian. The save contains a type-name side channel and a
plot census; it does not contain the feature allocator's free list. [Established;
[05 "Feature instance and terrain cell"]]

**Writer census and stable order.** The `Features` writer first emits
`Feature Type Names`, one 128-byte NUL-padded copy of each currently compiled
feature definition name, in catalog order. It then scans the map's feature
cells in row-major order (`z` outer, `x` inner). A cell is eligible only when
its feature reference is below the reserved void/fringe range; empty cells and
fringe/void sentinels do not produce records. The live definition class and
the cell's animation-present bit select the destination box:

| Box | Record size | Map |
|---|---:|---|
| `Normal Features` | 8 bytes | `0x00..0x01` cell X (`u16`), `0x02..0x03` cell Z (`u16`), `0x04..0x05` saved catalog ordinal (`u16`), `0x06..0x07` anchor/instance word copied from the cell (`u16`). |
| `Animating Features` | 10 bytes | The first eight bytes are X, Z, and saved catalog ordinal as above; `0x06..0x07` is the live animation-state word, `0x08` is the live animation frame byte, and `0x09` contains the saved animation selector in its low nibble plus the live countdown's high nibble in its high nibble. |
| `3D Features` | 26 bytes | `0x00..0x05` are X, Z, and saved catalog ordinal; `0x06..0x07` is the live animation-state word; `0x08..0x0B`, `0x0C..0x0F`, and `0x10..0x13` copy three live state words; `0x14..0x17` copies a fourth live state word; `0x18..0x19` copies a final live state halfword. |

The three count items are `Number of Normal Features`, `Number of Animating
Features`, and `Number of 3D Features`. Their values are the number of
records appended to their respective boxes, not the number of catalog types
or allocator slots. The writer classifies an object-backed definition as 3D;
otherwise the cell animation bit distinguishes normal from animating. A
feature footprint can cover multiple cells, but only the live anchor cell is
eligible, so one live feature produces one record. [Established]

The 3D words at `0x08..0x19` are copied losslessly, but the bounded census
does not establish public names for them. They are live feature state consumed
by the 3D/animation path, not coordinates that may be recomputed from X/Z or
terrain. Keep them as opaque words in a staged image until a field-isolation
trace identifies their semantics. The same rule applies to the animation-state
word: its wire position and restoration are established, while its complete
internal interpretation is not. [Established wire copy; Unknown semantic
names]

**Type-name remapping.** On load, the name box is interpreted as consecutive
128-byte names; a trailing remainder is ignored. Each saved ordinal is mapped
case-insensitively in this order:

1. If the ordinal is within the current catalog and the name at that ordinal
   matches, retain that ordinal.
2. Otherwise scan the complete current catalog in catalog order and use the
   first matching name.
3. If no current definition matches, ask the feature-definition loader to
   parse that name from the feature data. A newly compiled definition is then
   the mapping result; if parsing fails, the mapping is the empty sentinel.

When the name box is absent, the reader uses the current catalog's ordinal
identity for the mapping table. A present short or long name box changes only
the entries represented by complete 128-byte names; it does not establish a
new feature identity scheme. The saved ordinal is therefore an optimization
when the same catalog entry still has the same name, not permission to bind a
renamed definition by number. [Established]

The feature-definition loader also performs its normal successor fix-up after
catalog entries are available. Missing definitions are not replaced by a
different catalog entry: the remap remains empty and the corresponding
placement has no effect on the retail path. The catalog can consequently gain
a definition while a save is being read; a transactional implementation must
stage that catalog change and discard it on a failed load. [Retail behavior
Established; transactional consequence Supported inference; [05 "Feature
catalog"]]

**Reader and feature relationships.** The reader processes the three count
and box pairs in the order normal, animating, then 3D. Every complete record
is converted from its saved cell coordinates through the ordinary feature
placement/stamping service. That service is the single owner of clipping,
collision/removal policy, allocator acquisition, anchor/filler stamping,
occupancy notification, and any feature-class presentation side effect; the
save reader does not write the plot grid directly. This preserves terrain
footprints and derived occupancy through the same path used by map load,
replacement, fire, reclaim, and wreck creation. [Established; [05 "Feature
instance and terrain cell"]]

After a normal record is stamped, its saved anchor/instance word is copied
back to the anchor cell. After an animating record is stamped, the low nibble
of its state byte selects the already-established transition family: zero
restarts the burn sequence, one selects the death transition, and two selects
the reclaim transition. The reader then restores the saved animation-state
word, frame byte, and countdown high nibble into the allocated live record.
Other selector values do not establish a fourth transition and must remain
uninterpreted. A 3D record passes its three state words, fourth state word,
and final halfword through the placement path and then restores its animation
state word. [Established; [05 "Feature burning"], [05 "Feature sinking"]]

Because stamping replays the normal footprint operation, terrain and derived
occupancy are rebuilt from the saved anchor coordinates and current feature
definition footprint. The save does not preserve a separate free list,
allocator head, or per-cell filler list; those are reconstructed by placement.
Feature animation state is simulation state: the feature tick advances it in
the authoritative tick domain, including when presentation does not draw the
feature. Fire, successor, reclaim, and sinking behavior therefore continue
from the restored live record and are not inferred from a renderer frame.
[Established; [01 §4.4], [05 "Feature instance and terrain cell"]]

**Retail malformed-input boundary.** The retail reader compares the bytes
available for each record with its family size. A short normal, animating, or
3D record is skipped without a placement attempt. A negative family count
performs no iterations. A name-box size that is not a multiple of 128 ignores
the incomplete tail. Allocation failure for the remap table or feature
placement follows the ordinary silent no-placement path; the reader does not
retry with another slot or validate the placement result. A coordinate outside
the map reaches the normal cell lookup/stamping path rather than a dedicated
save diagnostic; the bounded census does not establish a safe retail result
for that case. Duplicate coordinates likewise have no save-specific duplicate
check and are handled by normal sequential stamping and collision policy.
[Established for the observed reader branches; Unknown for the resulting
state of malformed out-of-range or duplicate records]

These permissive paths are not a transactional contract. The CRD-009 loader
must validate every complete record's length, coordinate, remapped type,
family classification, and duplicate anchor before touching the live terrain,
feature pool, catalog, or occupancy cache. It must stage the records in the
writer's family order and row-major order, run placement against the staged
world, and publish only after every family and every derived footprint has
validated. Any failure discards the staged catalog additions, feature slots,
animation records, terrain changes, and occupancy notifications together.
This is an implementation boundary derived from the required whole-session
transaction; retail's own loader is incremental and has no rollback. [Retail
mutation order Established; transactional boundary Supported inference; CRD-009]

**Closure and remaining unknowns.** The wire image is implementation-ready
for lossless feature staging: names remap by case-insensitive identity, family
sizes and fields are fixed, stable order is row-major within each family, and
placement rebuilds terrain relationships and allocator state. The remaining
unknowns are limited to the semantic names of the five 3D state values, the
resulting retail state for out-of-range coordinates and duplicate anchors, and
whether any unrecovered nonstandard snapshot path serializes additional
feature state. Standard battle save/load has no such writer in the bounded
account census. [Established closure boundary; Unknown residuals]

Implementation probes should round-trip one record of each family at its exact
size; alter each 3D word independently; reorder records within and across
families; omit, truncate, and extend the name table; use renamed and unknown
definitions; exercise selector nibbles 0, 1, 2, and an unknown value; and
provide duplicate or out-of-range anchors. Transactional tests should assert
that every malformed case leaves the live session and catalog unchanged,
while a separate compatibility fixture may record the retail skip/no-op
behavior. [Supported inference for transactional assertions]

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

### Closed — the Save Game screen: file naming, the slot list, overwrite and delete [R-SAVE-02 §1] (2026-08-29)

**Established — one GUI file, two screens.** Both the save and the load
dialog are built from `LOADGAME.GUI` with a different backdrop (`DSAVEGAME2`
for saving, `DLOADGAME2` for loading) and a different set of hidden gadgets.
The gadget vocabulary is: `GAMES` (the slot list), `GAMENAME` (the name edit),
`LOAD` (the action button), `DELETE`, `CANCEL`, `TITLE`, and the summary
panel `RADAR`, `GAMETYPE`, `CAMPAIGN`, `CAMPTEXT`, `MISSION`, `TIME`, `SIDE`,
`DIFF`, plus two route buttons `SaveGame` and `LoadGame`. The save screen
sets `TITLE` to the literal `Save Game`, hides `LoadGame`, hides `DELETE`
when the list is empty, and gives `GAMENAME` the keyboard focus. The load
screen hides `DELETE`, `GAMENAME` and `SaveGame`. Both are reached from the
in-game options menu (§4) and from the results screen's `SaveGame` /
`LoadGame` buttons ([R-CAMP-01 §8]).

**Established — the file name.** The typed `GAMENAME` text is the file base
name, verbatim: the path builder is called with directory `SAVEGAME`, the
text, and extension `SAV`, and produces `SAVEGAME\<text>.SAV` by the
strip-last-dot-then-append rule already stated under "File naming and write
policy" (a name `v1.2 final` therefore saves as `SAVEGAME\v1.SAV`, because
the strip acts on the whole assembled path). Before that fallback the builder
first tries a **language-prefixed** location, `<language>-SAVEGAME\<text>.SAV`,
where `<language>` is the language directory name (the `language` registry
value or the bare command-line token, default `english`, [R-CAMP-01 §1]),
and uses it only when a file already exists there; a fresh save never lands
there. The string `savegame\%s.sav` present in the image is referenced by
nothing and is dead. An **empty** name does nothing (no file, no message).
There is no character-set filter of its own on the save path: whatever the
edit gadget admits reaches the file system call, and a name the file system
rejects produces no file while the writer still reports success ("File naming
and write policy"). The edit gadget's own admitted character set and length
belong to the GUI edit control (doc 07); **Unknown** here beyond that
delegation — decider: the edit-gadget key filter in doc 07.

**Established — `Description` and `Game ID`.** The Summary `Description`
string is the typed name itself, and `Game ID` is the C-library wall-clock
time at the moment of saving (seconds since the epoch), so the earlier
inventory wording "when the caller supplies a non-null one" resolves to:
always present for interface saves.

**Established — the slot list.** The list is built by enumerating
`SAVEGAME\*.SAV` (the language-prefixed directory first, by the same
existence rule). Directory entries `.` and `..` are excluded; nothing else
is filtered by attribute. The enumerator records one 32-bit time word per
entry and the list is then **bubble-sorted ascending on that word**, so the
oldest file is first and the newest last (when no time keys are supplied the
same sorter orders by string compare). Which of the file's timestamps the
32-bit word derives from is **Unknown** — decider: static trace of the
enumerator's find-data conversion. Each file's `Summary` account is then
opened (filtered open, nothing else read) and its `Description` string is
taken; a file with no readable bank, no `Summary`, or no `Description` is
**dropped from both the name list and the display list** (the name list is
compacted in place), so such a file can be neither loaded nor deleted through
the interface. The `GAMES` gadget displays the descriptions, not the file
names; selection index `n` maps to the `n`-th surviving file name.

**Established — selection, overwrite and delete.** Selecting a list entry
refreshes the summary panel (§3) and copies the entry's description into the
`GAMENAME` edit. Pressing the action button — or the `GAMES` / `GAMENAME`
activation events, which the handler treats identically — saves under the
current edit text; because a selected entry has just placed its own name in
the edit, saving over an existing slot is a silent truncate-overwrite with no
prompt. `DELETE` removes `SAVEGAME\<selected file name>` through the
file-delete call, ignores the result, rebuilds the list and refreshes the
panel; there is no confirmation. `CANCEL` returns to the previous screen and
frees the name, description, side-name and radar buffers.

### Closed — the Load Game screen and every load diagnostic, verbatim [R-SAVE-02 §2] (2026-08-29)

**Established — the empty list.** When no file survives the list build, the
load screen is closed again and the message box `There are no saved games to
choose from` (width 320) is shown; nothing else happens. The save screen
shows no message for an empty list.

**Established — `Invalid savegame file`, exactly.** The load handler runs on
the action button or the `GAMES` activation event. It shows the message box
`Invalid savegame file` (width 320) and returns, having entered no battle
state, in exactly these cases, tested in this order:

1. the `Summary`-filtered bank open fails (bad magic, version, tag, or an
   unreadable file);
2. the `Summary` integer `Gametype` is neither `1` nor `2`;
3. the unfiltered full reopen of the same file fails;
4. the `Summary` string `Mission` is absent or empty, or the campaign
   catalog cannot resolve it ([R-CAMP-01 §1]).

Before case 3 the CD gates run: `Gametype` 1 without the campaign CD shows
`Please insert the Campaign CD` and returns; `Gametype` 2 without the
multiplayer CD shows `Please insert the Multiplayer CD` and returns
([R-CAMP-01 §5]); both stop the load with no diagnostic other than the
insert-CD box. Every case is a return to the load screen — nothing is
aborted mid-restore, because no battle state has been touched yet; the
first mutation is the `Thumbs` copy and the session-record writes that
follow case 4, after which the load cannot fail through a message.

**Established — no `expected %d units, got %d` on the save path.** The
string `expected %d units, got %d` (and its sibling `No units_expected sent
from player`) is produced by the multiplayer lounge's per-peer status text
— it compares a peer's announced unit count with the units received during
game start — and is not referenced by any save or load function. The
question in this unit's brief rested on a misattribution; there is no
unit-count diagnostic on load. A `Number of Units` larger than the boxes
present simply ends the enumeration when a numbered box is missing (the
loader selects box `i`, and a failed select skips that index), and a
smaller count leaves the extra boxes unread.

**Established — the routes after preflight.** After case 4 passes: the
mission name is copied into the session record; `Thumbs` is copied with a
25-byte bounded copy and, when its length is not exactly 25, the thumbs
array is reset ([R-CAMP-01 §8]); for `Gametype` 2 the `Players` integer and
the five multiplayer rule integers (`CommanderDeath`, `Location`, `Mapping`,
`LineOfSight`, `LineOfSightType`, each default `1`) are installed; the
load-pending flag is raised, the session state becomes the loading state with
the battle-loading worker, and the list buffers are freed. A campaign save
(`Gametype` 1) that carries the `BetweenMissions` item instead clears the
load-pending flag, frees the bank, and enters the new-mission sub-state — the
continuation route already described under "Summary".

### Closed — the summary panel, exactly [R-SAVE-02 §3] (2026-08-29)

The panel reads only the `Summary` account of the selected file. `RADAR`
shows the `Radar Image` box when present (8-byte header: `u32` width, `u32`
height; then `height` rows of `width` palette bytes; a short read yields no
image). `GAMETYPE` is `???` when `Players` is `0`, `Single` when `Gametype`
is `1`, otherwise `Skirmish (%d players)` with `Players`. For `Gametype` 1
the `CAMPAIGN` text is the `Campaign` string (shown, with `CAMPTEXT`), and
`MISSION` is the `Mission` string; otherwise the campaign gadgets are hidden
and `MISSION` is the `Map` string. `TIME` formats the `Game Time` integer
`t` (ticks) as `%02d:%02d:%02d` with hours `t / 108000`, minutes
`(t / 1800) mod 60`, seconds `(t / 30) mod 60` — signed divisions truncating
toward zero. `SIDE` is the side-name table entry indexed by `Side`, or `???`
when the table is absent. `DIFF` is `Easy`, `Medium`, `Hard` indexed by
`Difficulty`; an out-of-range value indexes past the three-entry table
(**Unknown** result — decider: trace of the adjacent data). Every panel
field defaults to the empty string when no entry is selected. [Established]

### Closed — in-game options: when the buttons are greyed [R-SAVE-02 §4] (2026-08-29)

The in-game options window (`ARMOPT.GUI`) routes `LOADGAME` to the load
screen and `SAVEGAME` to the save screen. When the window is built, both
gadgets' first control bit is set exactly when the session kind is `3`
(network multiplayer) and cleared otherwise; that bit is the gadget word the
interface uses for unavailable buttons (doc 07 owns its rendering —
Supported inference that it is the greyed/disabled state; the setting
condition itself is Established). Skirmish (kind 2) and campaign (kind 1)
sessions may therefore save and load from the options menu; the earlier
tail item "upstream multiplayer GUI authority and menu enablement" is closed
by this gate. [Established gate; Supported inference for the visual effect]

### Closed — `SAVELIST` / `LOADLIST` are unit-restriction lists, not save games [R-SAVE-02 §5] (2026-08-29)

`SAVELIST.GUI` and `LOADLIST.GUI` (backdrops `DSaveList`, `DLoadList`; the
title is again `Save Game`) belong to the unit-restriction editor of the
game-setup screens ([R-SKIR-01 §10]). They share the `SAVEGAME` directory
and the same path builder, list enumerator and sorter as §1, but with
extension `LST`: `SAVEGAME\<name>.LST`. The list shows the file base names
with their extension stripped (no bank is opened). The writer emits a binary
file: a `u32` count (one less than the number of loaded definitions), then,
for each definition index from `1` upward that has a restriction record,
one `u32` definition id word and one `u32` restriction value. The empty-list message is `There are no saved lists
to choose from`. Nothing in this family touches a save bank. [Established]

### Closed — the `Units` account, word by word [R-SAVE-02 §6] (2026-08-29)

**Established — writer traversal and account items.** The writer walks the
unit pool from the **last** slot down to the first and emits every unit
whose live bit is set, numbering its boxes with a running index `i`
(0-based, in emission order). Per unit, in this order: the `Script%i` box
(§9), one `u%04xm%04x` box per order — the front list first, then the rear
list, sequence numbers continuing across both — then `u%04xmob` (§8) when
the unit owns a mover, `u%04xacc` (§7), and finally the 184-byte base
record as numbered box `i`. `Number of Units` and `Version` (= `0x11`) are
written **after** the loop and **only when at least one unit was written**;
a battle with no live unit therefore produces a `Units` account with no
`Version`, and the loader's version gate skips it. `Script%i` is numbered by
`i` (the numbered-box index), while the three `u%04x…` keys are numbered by
the unit's stable slot; the two numberings coincide only by accident.

**Established — the base-record words, named.** Every word of the
R-SAVE-UNIT-01 table that it left opaque is closed here by the writer's
source field and the reader's destination field (the same runtime field in
every case), cross-referenced to the consumer that names it. Offsets are
save-record positions.

| Save bytes | Named meaning | Evidence |
|---|---|---|
| `0x27..0x2A` | **Has-mover flag**: `1` when the unit owned a mover at save time, else `0`. The reader restores the `u%04xmob` box only when it is nonzero. It is not `Alive`/`Dying`. | writer tests the mover pointer; reader gates the mover-box read on it |
| `0x37..0x3A` | Signed roll (low word) and unsigned heading (high word), copied as one 32-bit word; `0x3B..0x3C` pitch. | one 32-bit copy of the adjacent roll/heading pair; [04 §2] |
| `0x89..0x8A` | Stable slot of the **carrier** the unit is attached to (transport or air base), or `0` when it has none or the carrier is dead. On load a nonzero value is restored recursively and then re-attached through the local attach command (type 10) with the byte at `0x8D` and the mover-mode bits of `0xB4`. | [R-AIR-01], [04 §6] carrier link |
| `0x8B..0x8C` | Stable slot of the unit's **engagement-target link**, or `0` when absent or dead. Restored recursively as a plain reference; nothing is attached. Its ordinary-play producer is the open item in [04 "Missing and unknown"]. | doc 04 guard handlers (consumer); writer/reader symmetry |
| `0x8D` | **Carrier attach slot**: the index the carrier's attach-position routine uses for this unit; `0xFF` when the unit has no live carrier. | the carried-position resolver reads it beside the carrier pointer [R-MOV-01 §1] |
| `0x8E` | Low byte of the unit's relation-domain byte (initialised to `10` at creation and by the per-tick refresh). Its semantic name is not closed — **Unknown**; preserve. Decider: trace of the target-registry comparison that reads it. | writer/reader copy; creation value |
| `0x8F..0x92` | **Spot-metal yield, `f32`** (not `u32`): the placement-time metal sum for extractors, never resampled. Correction: the earlier row's wire form was `u32`; the bit pattern is a single-precision float. | [R-PROD-01 §6] / doc 04 COB port census |
| `0x93..0x96` | Committed occupancy cell pair (`i16` x, `i16` z). | [R-COLL-01 §1] |
| `0x97..0x9A` | Sight-registration cell pair (`i16` x, `i16` z) — the cell the visibility registration record points at. | [R-VIS-01] registration record |
| `0x9B..0x9E` | Packed footprint size pair (`i16` x size, `i16` z size). | [R-COLL-01 §1] |
| `0x9F..0xA2` | **AI group index**, `−1` for none: the index of the owner's group record the unit is enrolled in. On load the reader moves the unit out of whatever group it holds and into this one (group-vector append, allocating when full), so this is the one base-record word with a side effect beyond a field copy. | [R-P0-04 §2] group records |
| `0xA3..0xA6` | **Reveal deadline tick**: the absolute tick until which the unit is exposed to sensors (the sensor phase writes `tick + 90`, a script port `tick + 300`). | [03 sensor phase], doc 04 port census |
| `0xAB` | **Death-cause byte**: the cause code recorded by the last damage packet and passed to the `Killed` script query. | [06 §9.2] damage packet, [R-COB-04] |
| `0xAC`, `0xAD` | A current/previous byte pair rotated once per unit tick. Semantic name **Unknown** — decider: trace of the unit tick's byte rotation and its reader. Preserve exactly. | writer/reader copy; unit tick |
| `0xAE..0xAF` | Order pending-gate mask (the word the order pump masks with `0x83FF`). | [R-ORD-01 §1] |
| `0xB0` | Stored line-of-sight byte (emitter height in ray mode, shape index in sprite mode). | [R-VIS-01] |
| `0xB1` | A countdown byte decremented once per unit tick while nonzero. Semantic name **Unknown** — decider: trace of the two readers in the unit tick. Preserve exactly. | writer/reader copy |
| `0xB2..0xB3` | The state byte zero-extended to a `u16`: bit 0 activated, bit 1 armored, bit 2 cloaked, bit 3 building; `0xB3` is always `0` on write and ignored on read. | [04 §2.4] |

**Established — the packed status word at `0xB4..0xB7`, exactly.** With
`f` the unit's 32-bit flags word and `s` the second state byte (in-build
stance bit 0, busy bit 1, yard-open bit 2, bugger-off bit 3):

```
word = (s & 0xF)
     | (f & 0x0FFF) << 4          // flags bits 0..11  → word bits 4..15
     | (f & 0x2000) << 3          // flags bit 13      → word bit 16
     | (f & 0xFFFFC000) << 6      // flags bits 14..25 → word bits 20..31
     | (stack residue & 0xE0000)  // word bits 17..19: never written
```

The reader inverts it bit-for-bit: word bits 0..3 into `s`, 4..15 into flags
0..11, 16 into flags 13, 20..31 into flags 14..25; flags bit 12 and bits
26..31 are **not persisted** and keep whatever the allocator set. Named
flag bits, in word positions: mover-mode mirror at word bits 4–5 (also fed to
the allocator and to the re-attach command), move-rate tier at 6–7,
completion marker (flags bit 13) at word bit 16, the auto/initial-posture
flag (flags bit 14) at 20, standing-move (flags 18–19) at 24–25 and
standing-fire (flags 20–21) at 26–27 ([R-STANCE-01 §6]). **Correction.** The
R-SAVE-UNIT-01 row says "the status component carries the authoritative
alive bit … alive is bit `0x10000000`, dying/death-mark is `0x4000`, and
construction-complete is `0x2000`". Those are runtime flag values, and the
live bit (flags bit 28) is **not in the save word at all** — the shift drops
flags bits 26..31; a loaded unit is alive because the forced-slot allocator
made it so, and the reader's "allocator live bit and saved live bit must
agree" sentence has no saved bit to compare. Flags bit 14 is the auto flag,
not a death mark ([04 R-P0-09]); the completion marker is flags bit 13. The
"bits 17..19 can contain writer-side uninitialized values" sentence stands.

**Established — what the reader does with the record, in order.** Definition
lookup by name; forced-slot allocation with the saved owner, position and
mover-mode bits, with the fresh-unit hooks enabled (so a definition whose
fresh-unit hook activates it is activated, and a definition carrying the
flag the hook answers with death-cause `7` plus the auto flag receives them,
before the saved bytes overwrite them); roll/heading/pitch, health, kill counter,
position; the carrier reference (recursive load, then local attach command);
the engagement-target reference (recursive load); the attach slot and
relation byte; spot metal; the three cell pairs; the AI group move; reveal
deadline; construction remaining; the five bytes, the gate mask, the state
byte; the packed word; the `u%04xacc` box; the `u%04xmob` box when
`0x27` is nonzero; the order boxes in sequence order into the front or rear
list by descriptor flag `0x40000`, then the front head is pumped once for
activation; the `Script%i` box; the three weapon-slot records; and, when the
second state byte's yard-open bit is set, the yard re-stamp
([R-COLL-01 §4]). A record whose stable slot is already live is skipped
without reading.

### Closed — `u%04xacc` is the unit's resource account [R-SAVE-02 §7] (2026-08-29)

The component whose two 24-byte halves the box carries is the per-unit
resource account — the structure the direct two-resource payment and the
per-tick request/consumption paths debit ([05 "Direct two-resource
payment"] [R-ECO-01 §7]). The box is the raw image of the 48-byte account:
the first 24 bytes then the next 24, written unconditionally and read back
in place only when the box exists. The account's field layout is doc 05's
(energy half then metal half in the payment helper's argument order); its
individual words are copied verbatim and none is a pointer. [Established
identity and copy; the per-word layout is owned by doc 05]

### Closed — `u%04xmob`, the 35-byte mover record, exactly [R-SAVE-02 §8] (2026-08-29)

| Box bytes | Wire form | Field |
|---|---|---|
| `0x00..0x0B` | 3 × `i32` 16.16 | velocity X, Y, Z |
| `0x0C..0x17` | 3 × `i32` 16.16 | lean-residual X, Y, Z (flight only) |
| `0x18..0x1B` | `i32` 16.16 | scalar speed |
| `0x1C..0x1D` | `i16` | turn residual |
| `0x1E..0x21` | `u32` | **last-stamp tick** (the occupant-age clock, [R-COLL-01 §5]) |
| `0x22` | `u8` | bits 0–1 movement mode, bit 2 blocked flag; bits 3–7 are writer stack residue |

The reader copies the first 34 bytes into the live mover and then merges
**both** bit groups of the final byte into the live state byte — first the
mode bits (mask `3`), then the blocked bit (mask `4`) — leaving the state
byte's other bits untouched. Nothing else of the mover is persisted: the
last-proposal tick, the follower pointer and the route object are rebuilt
([R-MOV-01 §1]). The unit's own mover-mode mirror is restored separately from
the packed status word (§6), so a hand-edited save can disagree between the
two; the mover's byte wins for the mover, the mirror for the unit.

**Correction to the tail (and to [04 R-COLL-01 §5]'s "second unnamed
word").** The tail listed the order as "the 16.16 velocity triple, the
three-component lean residual vector, one unnamed 32-bit word, the scalar
speed word, the signed 16-bit turn residual, a second unnamed 32-bit word,
and finally a byte", which sums to 39 bytes; the box is 35. There is no
unnamed word between the lean vector and the speed, and the single unnamed
word after the turn residual is the last-stamp tick named above. The
earlier RWU-04-1 trail counted the lean vector as two components plus one
unknown; it is three components.

**Verdict on the "route serializer leading bit".** An earlier report claimed
"the route serializer emits one leading bit (mover blocked flag) before the
2-bit count". No save serializer does this. The only save record carrying
the blocked flag is the byte above, where the mode occupies bits 0–1 and the
blocked flag bit 2 — the blocked bit *follows* the mode bits, and no count
field exists in the box. The description matches the **network** unit
stream instead (doc 04 [R-COLL-01 §5] item (4): the follower's stream writer
copies the blocked bit into the stream, and [R-PATH-01 §1]'s
`min(count, 3)` two-bit waypoint count) — the claim is retracted for the
save path and referred to the stream writer, which is out of scope here.

### Closed — the `Script%i` box, byte-exact [R-SAVE-02 §9] (2026-08-29)

The box holds three concatenated images; the reader accepts the box only
when its length equals exactly `0x528 + 4·S + 0x6C·P` (`S` statics, `P`
pieces, both from the bound program's header) — any other length skips the
whole script restore and leaves the freshly bound VM in its post-`Create`
state.

1. **VM image, `0x528` bytes.** `0x000..0x003`: the program signature word the
   bind computes for the compiled program; the reader compares it with the
   live VM's and rejects the box on mismatch (so a save cannot install a
   different script's threads into a unit). `0x004 + 0xA4·t` for `t = 0..7`:
   thread record `t` — raw status, program-counter word index, signed logical
   top, sleep timer, wait piece, wait axis, waited-on callee slot, signal mask,
   the native completion receiver, and 32 physical window words. Raw statuses
   map to idle `0`, running `0x01000000`, wait-turn `0x02100000`, wait-move
   `0x02200000`, sleep `0x02400000`, and wait-call `0x02800000`; other values
   reject the image. The wire logical top is `-1..31` and becomes Go's count
   `top + 1`, so an empty window has `SP=0`. The receiver at record offset
   `0x20` is deliberately zeroed by the writer and is cleared rather than
   reconstructed by the reader; it is not a window word. The 32 window words
   occupy `0x24..0xA0`, and all are restored, while authored opcode pushes and
   locals retain their separate depth-10 semantic limit [04 §4.2] [R-COB-01
   §1]. `0x524..0x527`: the active-thread count. The reader copies all eight
   records back in place and restores the count.
2. **Statics, `4·S` bytes**: the script statics array verbatim.
3. **Piece states, `0x6C·P` bytes**, one record per piece: 24 words of
   per-axis animation state — for axis `a` in `0..2` the words at dword
   positions `a, 3+a, 6+a, 9+a, 12+a, 15+a` are the six per-axis animation
   words of the piece array (the doc 04 move/turn state: targets, speeds and
   flags per axis), dword `18+a` is the adapter's *get-position* result and
   `21+a` the *get-angle* result for that axis — followed by three
   piece-level dwords 24..26 that the reader feeds to the adapter's
   show/hide, cache and shade setters ([R-COB-01 §1] adapter slots). On
   load each piece's first animation word is set to `1` before the setters
   run, then the six words per axis are copied back and the position/angle
   are re-committed through the adapter's set-position/set-angle
   (move-now/turn-now) slots; finally the global animation-dirty gate is set
   and every per-piece busy gate is forced for the next interpolation pass.

**Correction (2026-08-31).** The earlier wording called the record word at
`0x20` unnamed, described only ten physical window words, and called the final
restore state a separate "script restored" word. The settled record census
identifies `0x20` as the native completion receiver, expands the physical
window to 32 words (`0x24..0xA0`), and identifies the final restore state as
the existing global animation-dirty gate. The ten-word limit remains an
authored opcode semantic limit, not a wire-image truncation. This correction
is why the loader validates raw status/top values and restores the complete
window while deliberately leaving the receiver unbound. [Established; [04
§4.1], [04 §4.2], [04 §4.6], [R-COB-01 §1]]

The writer's decompilation stores only one of its three piece-level getter
results inside dwords 24..26 (the other two land in a stack slot the axis
loop overwrites), which is the origin of the standing "two slots leak old
stack values" sentence: dwords 24 and 25 carry stack residue and the reader
installs that residue through the show/hide and cache setters. [Established
layout and sizes; the exact frame position of the two lost getter results is
Supported inference from the decompilation — decider: byte-level trace of
the writer's three indirect calls.] With this, the "script persistence
remains only partially closed" item under R-SAVE-UNIT-01 is closed: the box
persists every thread word, every static and every per-axis animation word,
and nothing else (callbacks are the receiver word inside each thread record;
there is no separate callback table).

### Closed — the order subtype families, named [R-SAVE-02 §10] (2026-08-29)

The code at `0x04` of the `u%04xm%04x` record is the value returned by the
order's sub-object through its class slot, and the reader constructs the
matching class from the `${box}g` box. The five codes are, by the runtime
constructors that make each class:

| Code | Payload | Class | Constructed by |
|---:|---:|---|---|
| `2` | `0x36` | the **path marker** (air work point) — flag word, arrival radius, height offset, side word, owner and target unit links, goal X/Y/Z 16.16 ([R-PATH-01 §9] "Class-D"; [R-AIR-01]) | the VTOL move/patrol/follow order handlers |
| `3` | `0x2A` | a two-vector work record: owner unit link, two 16.16 triples, three `u16` words | the `AirToAir` handler — its only runtime constructor ([R-SESS-01 §6]; this cell said **Unknown** until 2026-08-29) |
| `4` | `0x10` | the **point goal** handle (relative cell pair, radius parameter, squared threshold) | `Move_Ground`/`Patrol` goal install ([R-ORD-01 §1]) |
| `5` | `0x18` | the **annulus goal** (outer/inner) | annulus goal install ([R-ORD-01 §1]) |
| `6` | `0x14` | the **rectangle goal** (packed origin, packed size) | rectangle goal install ([R-ORD-01 §1]) |

Code-2 payload: `0x08` owner-unit stable slot; `0x0A..0x19` the image of
the marker's embedded 16-byte unit-link helper (discarded; the reader
re-links the helper to the unit named at `0x1A`); `0x1A` target-unit stable
slot; `0x1C` flag word (bit 3 height set, bit 4 radius set / altitude
arrival, bit 5 compute cruise Y); `0x1E` horizontal arrival radius; `0x20`
height offset; `0x22` one further `u16` (**Unknown** name); `0x24` side word
(`0xFFFF` = none); `0x26`, `0x2A`, `0x2E` goal X/Y/Z 16.16; `0x32` one
further `u32` (**Unknown** name — decider: field-isolation trace of the
marker's arrival test). Code-3 payload: `0x08` owner-unit stable slot;
`0x0A` `u16`; `0x0C..0x17` first triple; `0x18..0x23` second triple;
`0x24`, `0x26`, `0x28` three `u16`. Codes 4–6 are the goal handles whose
words [R-PATH-01 §9] defines. The name side channel is the string item
`${box}_name`; the `UTYPENAME%4d` string is written only for
`MobileBuild`, `VTOL_MobileBuild` and `BuildingBuild` records whose
parameter 1 names a definition index below the loaded count and only when
that key is not already present. [Established]

### Closed — what is not saved, and the fix-up order that rebuilds it [R-SAVE-02 §11] (2026-08-29)

**Established — the load order, restated from the dispatchers.** Summary
`maxunits` → Players (human-player byte, 28-byte timing block, per-slot
scalars, alliances) → Camera → Features → Metal → PlayerFeatures → Mapping
→ Units (per unit: the §6 sequence) → Meteor → trigger records; then the
battle-loading worker proceeds as for a fresh battle. Nanolathe's own
staged/transactional order is the one under R-SAVE-UNIT-01.

**Established — derived state Nanolathe must rebuild, because retail does.**
The following is absent from every account and is regenerated by the
ordinary constructors and first ticks:

- both random streams (reseeded from the clock before any restore);
- the projectile pool, burst scheduler and in-flight effects (zero active);
- every mover's route object, follower and last-proposal tick; path-search
  queues and the class layer (rebuilt from the restored occupancy stamps);
- occupancy stamps themselves (each unit re-stamps from its restored cell
  pair and footprint at allocation; features re-stamp through placement);
- visibility/LOS masks and sensor registrations (re-registered per unit; the
  sight cell pair is restored only so the registration can be re-issued);
- the AI's strategic state, class vectors and manager tasks (only the unit's
  group index survives, §6);
- selection, order-marker presentation, HUD caches, the minimap surface (the
  `Radar Image` box is presentation for the list only);
- the COB adapter render tables and piece geometry (rebuilt at bind; only
  animation words, thread records and statics are restored);
- weapon aim callbacks and muzzle queries (R-SAVE-WEAPON-01);
- the per-definition unit counts (recomputed as units are allocated);
- the unit-type name table's identity mapping (`UTYPENAME%4d` remaps by
  name);
- the meteor shower's authored parameters (reinstalled from the map);
- victory/defeat scratch counts and timer deadlines ([R-TRIG-01 §8]);
- the scheduler's clock anchor after the first budget pass ("Scheduler and
  random state in saves").

### Closed — Camera, Metal, PlayerFeatures and Mapping, exactly [R-SAVE-02 §12] (2026-08-29)

`Camera`: two integer items, `X Position` and `Z Position`, the camera's
world position words; on load both are copied into the camera's current
*and* target position, one camera state bit is raised and another cleared
(the camera state word is doc 07's; their names are not closed here), and
the presentation re-derives everything else.
`Metal`/`Plotmap`: one byte per plot cell in row-major order, the cell's
metal byte ([R-TERR-01 §1]); the reader requires the box length to equal
`width × height` exactly. `PlayerFeatures`/`Plotmap`: `(width × height)/2`
bytes; each byte packs the **placer nibble** (cell flag byte bits 3..6,
[R-TERR-01 §1][03 §3.3]) of two consecutive cells — the even cell in the high
nibble, the odd cell in the low nibble; the reader restores bits 3..6 of each
cell's flag byte and preserves the others; exact-size gate as for metal.
`Mapping`: one unnamed box of `(width × height) >> 1` bytes, the mapping
grid verbatim ([R-SHARE-01 §6]); exact-size gate. `Players`: the writer
emits a `Player%i` account only for slots whose active byte is set, and the
per-slot item list is the full "Player records" table plus `Logo` and
`Side`; the `Human Player` integer's load default is `10` (no human). All
[Established].

### Closed — corrections to earlier text [R-SAVE-02 §13] (2026-08-29)

1. R-SAVE-UNIT-01's "alive is bit `0x10000000` … dying/death-mark is
   `0x4000`" for the packed word: wrong — see §6; the live bit is not
   persisted and flags bit 14 is the auto flag.
2. R-SAVE-UNIT-01's `0x27` "runtime boolean word, name Unknown": it is the
   has-mover flag (§6).
3. R-SAVE-UNIT-01's `0x8F` "`u32`": it is an `f32` (§6).
4. The "Save and replay" tail's seven-field `u%04xmob` order: wrong (39
   bytes for a 35-byte box); see §8.
5. The tail's "the `u%04xacc` … field list is a separate open item": closed
   by §7 (the resource account's layout is doc 05's).
6. The "Summary" section's "`Description` when the caller supplies a
   non-null one": the interface always supplies the typed name (§1).
7. The account inventory's description of `Script%i` as "per-unit" is right
   but under-specified: it is numbered by box index, not stable slot (§6).
8. The brief's premise that `expected %d units, got %d` is a load
   diagnostic: it is lounge text (§2).

### Closed — the bank writer's compression policy and the typed-item primitives [R-ENTRY-02 §3] (2026-08-29)

"Location and representation" gives the byte layout and the reader's
tolerance; this section adds what an implementer of the **writer** and of
the per-subsystem readers still had to choose.

**Established — the writer.** The bank is written in one pass to the
truncated file: a zeroed 34-byte header first, then every account in
creation order, then the string pool, then the header again with the real
offsets. An account is emitted only when it holds at least one item or one
box descriptor. Per account the writer emits a zeroed 32-byte header, appends
the account name to the pool, then the integer items, the double items and
the string items in that order (each name — and each string value — appended
to the pool as it is met), then one 16-byte descriptor per box whose length
is **greater than zero** (empty boxes leave nothing), then the box payloads in
descriptor order; finally it rewrites the account header with the counts and
the stored span. The string pool begins with the bank tag
(`Total Annihilation 3.0`), so the header's tag offset is always 0.

**Established — compression.** After an account body is written, and again
for the string pool, the writer runs the archive compressor on it as one
**SQSH chunk with the LZ77 method and no obfuscation** [fmt hpi] — the same
chunk the reader's decompressor undoes — and keeps the compressed image only
when the compressor succeeds **and** the chunk (its 19-byte chunk header
included) is strictly shorter than the raw bytes; the body flag (account
header `0x18`) or the pool flag (bank header `0x18`) is then `1` and the file
is truncated to the shorter end. Otherwise the raw bytes stand with flag `0`.
The compressor's output buffer is sized from the bound helper — `120% + 119`
bytes for an account body, `110% + 119` for the pool. Every write, seek and
close result is ignored ("File naming and write policy").

**Established — the audit listing is unreachable.** "Location and
representation" notes that the executable can dump a bank as a readable
audit listing (`HapiBank Audit File`). The dump is gated by the writer's
fourth argument, and the only caller — the battle save dispatcher — passes
it as `0`; no retail path produces the listing.

**Established — the item and box primitives every subsystem writer and
reader uses.**

- *Named items* live in the current account (selected or created by name).
  A writer looks the name up and creates the item when absent; the integer
  writer stores the value with type tag `1`, the string writer duplicates
  the text and stores tag `3`; either writer first frees a string value the
  item held before. So a subsystem rewriting an item changes its type
  silently, and "later scalar items overwrite earlier same-name items" holds
  for writers as well as for the reader's merge.
- *Typed reads* are lookup-without-create: the integer accessor returns its
  caller's default unless the item exists **and** carries tag `1`; the double
  accessor likewise unless tag `2`; the existence test is a bare lookup. No
  read reports a type mismatch — the default is the whole signal (this is
  what makes "no meteor read can fail the load" true, "Subsystem writers").
- *Boxes* are selected by name or by number, creating an empty box when
  absent; the selector returns whether the box currently has any bytes. The
  bounded read copies `min(requested, remaining)` bytes from the box's read
  cursor, advances the cursor by that count and returns it — `0` once the
  box is exhausted. Every record reader compares that count with its record
  size and skips the record on a short read, which is the exact-size box
  guard the load sections describe.
- A bank object created for writing starts with no accounts and no current
  account; the first account-select creates the account.


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
kills by the kill multiplier and the global tick over 60 (not 1800 — corrected
in [R-CAMP-01 §6]) by the time multiplier with truncation and a zero clamp. Resign and host-loss latch ended-without-win
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

### Closed — the results sequence: states, glamour, fade, ending movie [R-CAMP-01 §6] (2026-08-29)

**Correction.** The paragraph above and "Progression" both said the score
helper uses "elapsed ticks divided by 1800". The divisor is **60** (an
unsigned magic-multiply by `0x88888889` with a 37-bit shift — exactly `n / 60`
for the 32-bit tick count). With the 30 Hz global tick this is a two-second
unit. The rest of that sentence (kills × kill multiplier, truncation, zero
clamp) stands; the exact expression is in §7.

**Entry.** The battle pump's end transition fires when the latch word of
[R-TRIG-01 §6] has either the *won* (`0x10`) or *lost* (`0x04`) bit set —
for a multiplayer session only once a peer-side predicate also holds (out of
scope). It runs the
*battle teardown* (which calls the score helper of §7 — the only site that
writes the W/L mark), stops the battle sound, sets the session state to 7
(results) and installs the *results handler* as the session pump. Every
presentation "unit" below is one tick of the presentation clock of §2.

**Results handler states** (a small word, initial 0):

| State | Action |
|---|---|
| 0 | Non-multiplayer: clear the frame-copy pointer, go to 2. Multiplayer: copy the last game frame, run the statistics collector with code 7 (§9), go to 1; if the local slot's rejection-reason byte is set and not 2, show its message (`The game is closed`, `You did not have the correct password`, `The game is full`, `You have lost connection with the host`, `You need a unit you don't have for this game`, `You need a newer version of the game`, `No watching is allowed for this game`, `The creator has left the game`, default `You were rejected from the game`) and clear the byte. |
| 1 | Wait until no `MSGBOX.GUI` is open, then 2 (the frame copy is restored behind the box while it is up). |
| 2 | Set a countdown of 10, deadline `now + 1`, fade-done flag 0; go to 3. |
| 3 | Each time `now > deadline`: draw the full-screen rectangle through the rectangle shader with level `countdown − 29`, set deadline `now + 1`, decrement; at 0 set fade-done. When fade-done: stop the music, go to 4. |
| 4 | Campaign session (kind 1) and the campaign-CD check (§5) fails: open `CDCHECK.GUI` (its `OK` re-checks and, on success, sets state 5), go to 8. Otherwise 5. |
| 5 | Run the *outcome-art preparer* (below). Let `hasNext` = Has-mission(index + 1). If kind 1 and won and **not** `hasNext` and the mission's `nomovie` is 0: the campaign is complete — windowed display goes straight to the main-menu shell state; full-screen goes to the Core ending-movie state when the local player's side byte is non-zero, else the Arm ending-movie state; session state 2 (front end). Otherwise: if kind 1 and won and a glamour image was loaded: build the fade table with 5 steps, deadline `now + 1`, blit the glamour image at (0, 0), go to 6; else populate `ENDMSN.GUI` (§8), fill the score fields (§7), apply the control set (§8), go to 7. |
| 6 | While the fade is not done: apply one fade step per unit (below); when the current palette equals the target the fade is done, then the glamour deadline is `now + rate` (one second). Once done: play slot 8 (glamour sound) once; after the deadline, any key press or mouse click populates `ENDMSN.GUI` (§8, §7) and goes to 7; after five further seconds `Click to continue.` (translated) is drawn at the bottom of the screen (y = height − 20). |
| 7 | `ENDMSN.GUI` is up; the statistic bars are revealed in seven groups (§7). |
| 8 | Idle with the panel up (used by the CD-check dialog). |

**Outcome-art preparer.** Allocates three 1024-byte palette buffers
(`currentPalette`, `desiredPalette`, `FadeTable`) and a 1024-byte `Palette`,
saves the display's gamma word and forces it to 1.0. Then, only for kind 1
when won and slot 5 is non-empty: the path is `bitmaps\glamour\<slot 5 text
after its leading separator>` with the extension replaced by `PCX`; when the
VFS reports a zero size the path is replaced by the literal
`glamour\Arm01.PCX` (retail's fallback — note it lacks the `bitmaps\` prefix,
so it also misses on the stock install, which carries
`bitmaps\glamour\arm01..25`, `core01..25`, `armvict`, `corevict`, `krogvict`,
`tabtarm`, `tabtcore`). The PCX is decoded into an image plus its palette;
a decode failure leaves the image null, which sends state 5 down the
no-glamour branch. In every other case it loads the front-end bitmap
`Outcome1` (kind 1) or `Outcome0` (kinds 2, 3) as the background instead.

**Fade table.** For each of the 1024 palette bytes `b` (index `i`): with
`cur = currentPalette[i]` (the decoded image's palette) and
`dst = desiredPalette[i]` (the display palette at entry), the signed step is
`0` when equal; `max(1, (dst − cur) / 5)` when `dst > cur`; and
`min(−1, (dst − cur) / 5)` when `dst < cur` (integer division truncating
toward zero, steps = 5). **Fade step** (once per unit): each byte becomes
`cur + step` clamped so it never passes `dst` in the step's direction; when
all 1024 bytes equal the target the done flag is set; the current palette is
then pushed to the display. (Presentation of the palette itself is doc 03's.)

**Cleanup.** When `ENDMSN.GUI` closes, the frame copy, glamour image, the four
palette buffers and the gadget data are freed and the display gamma restored.

**Confidence.** Established, including the `glamour\Arm01.PCX` fallback path
and its missing prefix (read from the two string constants).

### Closed — the score helper and every statistic's source [R-CAMP-01 §7] (2026-08-29)

**When.** The score helper runs once, from the battle teardown at the end
transition (§6), before the results handler is installed. It writes:

1. the *won* word = latch bit `0x10` (0 or 1);
2. for kind 1: the *end-mission index* = the record's current mission index,
   and `Thumbs[index] = won ? 'W' : 'L'` (bytes `0x57` / `0x4C`) in the
   25-byte progress-mark array;
3. seven *column maxima*, initialised to 10, 10, 100, 100, 100, 100, 100
   (kills, losses, energy produced, metal produced, energy wasted, metal
   wasted, score);
4. the *display array*: ten rows of 58 bytes, zeroed, one per player slot in
   slot order: a 30-byte name copy and seven 32-bit integers.

A slot gets a row when its record exists, its controller is 1, 2 or 3
(human, local, remote — the [R-SKIR-01 §1] vocabulary), its side is not the
neutral 10 and its lobby record's watcher bit (`0x40`) is clear — **or** when
the slot's auxiliary word is non-zero (Unknown meaning; decider: static trace
of that word's writers) — and, in either case, its rejection-reason byte is
0. Rows are filled as:

| Column | Source | Conversion |
|---|---|---|
| Kills | slot's 16-bit kill counter | sign-extend |
| Losses | slot's 16-bit loss counter | sign-extend |
| Energy produced | slot's double `TotalEnergyProduced` | `__ftol` (truncate) |
| Metal produced | slot's double `TotalMetalProduced` | `__ftol` |
| Energy wasted | slot's double `EnergyWasted` | `__ftol` |
| Metal wasted | slot's double `MetalWasted` | `__ftol` |
| Score | see below | |

`Score = __ftol(float(int64(globalTick / 60)) × timemul) + __ftol(float(Kills)
× killmul)`, evaluated in that order with `timemul` and `killmul` the
mission's `[GlobalHeader]` floats (default 0.0; stock missions author
`killmul=50; timemul=0;`), `globalTick` the 32-bit global tick counter of
[01 §2] and the division unsigned. A negative sum is stored as 0. Each column
maximum is raised to the row's value when the value is larger (strict).

**Where the counters are written.** Kills, losses, commander kills and
losses are the per-slot counters incremented by the death-credit switch of
[06 §12.1] inside the tick, at the death site. `TotalEnergyProduced`,
`TotalMetalProduced`, `TotalEnergyConsumed`, `TotalMetalConsumed` (doubles)
are accumulated by the per-player economy pass [05 "Authoritative settlement
order"]: after the pass sums this tick's production and consumption over the
player's units and its base income, `produced += tickProduction` and
`consumed += tickConsumption` for each resource (float sums widened to
double). `EnergyWasted` / `MetalWasted` accumulate the **storage overflow**:
with `pool = stored + tickProduction` (after the split that pays
consumption), when `pool > capacity` the stored value becomes `capacity` and
`wasted += pool − capacity` (float, widened). Fixed per-tick order:
production/consumption sums → the capacity adjustment of [05] → produced/
consumed totals → overflow clamp and wasted totals. The established Player%i
economy values are saved and restored verbatim under the `Players` account keys
of that name [08 "Account inventory"], and the network statistics copy carries
the same fields as floats. The `Player%i` save table above establishes keys for
Kills and Losses;
it does not establish save keys for the commander-specific counters. Those
counters therefore remain runtime/result data until a separate save-writer
trace settles their persistence.

**Correction (2026-08-31, Wave B2):** The previous wording said that all
twelve displayed values were restored under `Players` keys. That over-read the
statistics/network row census: the Player%i writer/reader table establishes
the economy values plus `Kills` and `Losses`, while commander-specific names
are established only for the network statistics copy. The persistence status
of commander counters is therefore Unknown pending the save-writer trace.

**Screen fields** (the `ENDMSN.GUI` score population, run when the panel is
populated): for every display-array row in slot order, with `r` the running
row number (starting 0) and `y = 93 + 20·r`: the gadget `PlayerColor%d` (`%d`
= r) is created at (16, y, 91×21), its animation set to the logos GAF with
frame = the slot's colour byte; the slot name is drawn beside it in the small
font; then seven bar gadgets are created at `x` = 112 `Kills%d`, 186
`Losses%d`, 260 `EProduced%d`, 334 `MProduced%d`, 408 `EWasted%d`, 482
`MWasted%d`, 556 `Score%d`, each carrying the row's value, the column maximum,
and a per-gadget float `max(1.0, value × 0.06666667)` (`value / 15`; it is
the kind-13 gadget's per-tick **animation step** — `current += ftol(step)` on
each scaled-timer tick until the target is reached — not a fill fraction;
Established, [07 R-HUD-03 §11]). The bar reveal group word is reset to 0.

**Bar reveal** (results state 7). Once the panel is idle, all `Kills%d`,
`Losses%d`, `EProduced%d`, `MProduced%d`, `EWasted%d`, `MWasted%d` and
`Score%d` gadgets are hidden and the cue `ActivateAllStatBars` played; then
every 10 units (or immediately on a key press, except in multiplayer) the
next group is shown in the order Kills, Losses, EProduced, MProduced,
EWasted, MWasted, Score, playing the cue `EndGameStatBar` for the first six
and `EndGameScore` for the seventh. `EndGameStatBar`/`EndGameScore` are
**sound cue names** looked up in the sound table, not scales.

**The un-numbered `MProduced`, `MWasted`, `EWasted` strings** and the
`Kills`/`Losses` labels are the `Players` account keys and the in-battle
score panel's column labels [07 §11]; `Energy Produced`, `Metal Produced`,
`Excess Energy`, `Excess Metal`, `Commanders Killed`, `Commanders Lost`,
`I am Winner` are the field names of the network statistics rows (§9).

**Confidence.** Established except the bar-increment inference marked above.

**Correction (2026-08-31, Wave B3).** The ENDMSN dynamic presentation uses
the exact kind-13 geometry and strict presentation-clock service recorded in
[07 R-HUD-03 §11]: stored dimensions 67×18 are painted inclusively as 68×19,
with the two-pixel raised bevel, inner span `(x+2,y+2)..(x+65,y+16)`, and
foreground endpoint `x+2 + trunc(63*current/max)`. A visible bar enters the
service body only while `current < target`, then advances once only when
`nextDue < presentationUnit`, and schedules the next due unit
one unit after the sample; exact target equality leaves animation set, while a
strictly overshooting candidate clears it. PlayerColor
uses the source slot's colour frame from `textures/logos.gaf:32xlogos` in a
stretched 91×21 surface, with the slot name centred in its 90×15 text area;
the compact result-row ordinal is not substituted for that source slot. The
reveal deadline is strict, the inherited deadline is expired for the first
Kills pass, and each subsequent group is ten presentation units later. In the
single-player surface, a keyboard edge activates all seven groups and plays
`ActivateAllStatBars`, while the same pass still performs only one ordinary
reveal/cue; mouse input is not a shortcut. **Established.**

### Closed — `ENDMSN.GUI`: outcome art, mission list, next mission, `AdjustDiff`, progress write [R-CAMP-01 §8] (2026-08-29)

**Population.** Let `route = (kind == 1) && (hasNext || !won)` where
`hasNext` = Has-mission(endIndex + 1) on the campaign record and `endIndex` is
the index captured by the score helper. If `route`: Set(endIndex) reloads the
just-played mission into the record, the background is `outcome1`, and the
`Start` control's caption is set to the translated `Start`. Otherwise the
background is `outcome0` and focus goes to `MainMenu`. (So a **won final
mission** and every non-campaign session get `outcome0`; a lost mission or a
won mission with a successor gets `outcome1`.)

If `route`: the mission list is built (§1) and rewritten by the *mark
prefixer*: each entry becomes two bytes plus the name, the first byte being
`0xFF` for an `L` mark, `0xFE` for `W`, `0xFD` for `U`, the second a space
(these are glyph codes in the panel font); a 25-slot scan for the first `U` is
computed and discarded. The list is bound to `Missions`, the list gadget's
page size is derived from its height, and the selection is set to `endIndex` and
then to `endIndex + (won ? 1 : 0)`: **the "next mission" is the current index
plus one on a win, the same index on a loss**, offered as the pre-selected row
— nothing in the record advances by itself. The `Difficulty` label is
refreshed from the difficulty word.

The victory/defeat glyph: frame 0 of the front-end *victory* sequence when
won and the local slot is not a watcher, else of the *defeat* sequence,
blitted at (width/2, 28). When the session was launched from an external
lobby (multiplayer, out of scope) the `MainMenu` control is relabelled `OK`.

**Control set.** With `route`: `Start`, `LoadGame`, `SaveGame`, `KNOB`,
`Missions`, `Difficulty`, `AdjustDiff`, `MainMenu` are shown and `Missions`
gets focus. Without it only `MainMenu` is shown, moved to y = 416.
**`AdjustDiff` has no handler**: it is displayed by this set and does nothing
when clicked (the difficulty is cycled by the `Difficulty` control, which
writes 0 → 1 → 2 → 0 to both the skirmish record's difficulty field and the
difficulty word). What `AdjustDiff` "writes" is therefore: nothing.

**Callbacks.** `LoadGame`/`SaveGame` open the load/save dialogs
[08 "Save-file organization"]. `MainMenu` goes to the main-menu shell state,
session state 1. `Start` or a `Missions` selection: the campaign-CD check
(§5) shows its message on failure **and continues**; the archive set is
re-mounted; Set(`Missions` selection) runs; on success the front-end state is
reset, the *mission-loaded* flag set, the kind forced to 1, the latch word's
*won* and *lost* bits cleared, and the shell routed to the briefing-movie
state with session state 2. A failed load leaves the panel up.

**Progress write.** Campaign progress is three in-memory items: the record's
mission index, the 25-byte `Thumbs` mark array (`U` unplayed, `W`, `L`) and
the difficulty word. The marks are initialised to 25 × `U` (plus a trailing
byte) by the new-game `Start` (§3); one byte is written per battle end (§7).
They are **not** written to the registry: the registry holds only the
difficulty, games and all-missions mirrors of "Progression". They persist only
through the save bank's `Summary` account: `Thumbs` (string, 25 characters),
`Campaign` (string), `Mission` (string, the mission's display name), `Map`
(string, same value), `Difficulty` (int), `Side` (int, local slot's side
byte), `Players`, `Gametype` [08 "Account inventory"]. On load `Thumbs` is
copied with a 25-byte bound and, when its length is not exactly 25, reset to
all `U`; `Mission` is resolved by the name look-up of §1.

**Between-missions save quirk.** When the save writer runs outside the live
battle state (the results screen's `SaveGame`), it calls Advance (§1)
**before** writing `Mission`/`Map` and `BetweenMissions=1`, then restores the
end-mission index. A save taken from the results screen therefore names the
**next** mission whenever one exists — regardless of whether the mission was
won — and the current mission only when it was the last. A save taken from
inside a battle names the current mission and omits `BetweenMissions`. The
restart control [07 §11] re-opens the campaign by name and Set(endIndex)
without touching the marks.

**Confidence.** Established.

### Closed — elimination announcements and the kill-lead line [R-CAMP-01 §9] (2026-08-29)

**Elimination.** In the central death handler [06 §12.1], after the victim's
owner's live-unit count is decremented, when it reaches **0**: a multiplayer
session (kind 3) sends the owner's elimination to the peers; a skirmish
session (kind 2) draws `CRT rand() % 3` to pick one of the three possessive
tails — `forces have been obliterated`, `forces have gone to a better place`,
`vermin have been exterminated` (translated) — formats `"%s %s"` with the
owner's name and posts it as a status line of class 4 attributed to the
owner's slot. The eight-entry table (`has been obliterated`, `has been
liquidated`, `has been eradicated`, `has terminated`, `has bowed out`, `has
gone to a better place`, `has been shown the door`, `has left the scene`) has
no reference on the single-player path — but it is **not** dead data: the
multiplayer (kind 3) elimination branch of the same death handler reads it
with a CRT draw masked to eight entries and posts the line locally (corrected
2026-08-29 against [01 R-DET-01 §6]; the earlier text said "no reference in
the image"). **Determinism:** the draw is on
the **CRT** stream [01 §7.2] and happens inside the tick, so a skirmish
elimination advances the CRT stream by one draw; the simulation stream is
untouched. Campaign sessions post nothing.

**Kill lead.** After a kill is credited in a kind 2 or 3 session (the
credited slot exists, controller 1/2/3, side ≠ 10, and its rank byte is
non-zero): with `k` the crediting slot's kill counter — or its commander-kill
counter when the commander-death option word is 2 — the lowest rank among
non-watcher slots whose counter is strictly below `k` and whose rank is
below the crediting slot's becomes the new rank; every slot whose rank lies
in `[new, old)` is shifted down by one; when the new rank is 0 the line
`%s has taken the lead with %d kills` (translated, then formatted) is posted
as a status line of class 2 attributed to slot 10. Established.

### Closed — the multiplayer statistics rows (`I am Winner`, `Excess …`) [R-CAMP-01 §10] (2026-08-29)

The *statistics collector* fills, for every slot with a record (controller
1/2/3, side ≠ 10, or the auxiliary word non-zero), a player row (name pointer,
record pointer, flags: bit 1 always; bit 2 when the controller is 2, or 3
with lobby kind 2; bit 3 when the lobby watcher bit `0x40` is set; bit 4 when
lobby byte flag `0x01` is set; the side name pointer; the ally list of up to
ten slots whose alliance byte is set) and a score board of nine named
integers: `Kills`, `Losses` (16-bit counters), `Energy Produced`, `Metal
Produced`, `Excess Energy`, `Excess Metal` (the four doubles of §7, `__ftol`),
`Commanders Killed`, `Commanders Lost` (16-bit counters), and `I am Winner` =
the latch's won bit for the local slot and for every controller-2 slot, 0
otherwise; the board's total = `__ftol(Energy Produced) + __ftol(Metal
Produced)`. `ScoreBoard%d`, `PlayerInfo%d`, `Allies%d`, `ppScores%d`,
`Scores%d`, `ScoreBoardsArray`, `PlayersArray`, `ScoresArray` are the
allocation tags of these arrays (ten of each). The rows are handed to the
online-service callback and the DirectPlay lobby report; both are
**out of scope** (networking) and are named here only so the vocabulary is
placed. "Excess" is therefore the `EnergyWasted`/`MetalWasted` overflow
accumulator of §7. Established as to fields; OOS as to consumers.


## R-AI-02 — Computer player: ledger-closure findings (2026-08-29)

Bottom-up closure of the last open lane-08 computer-player rows. Everything
here is small; each paragraph exists because an implementer reading
[R-AI-01] alone would still have had to choose a value.

### R-AI-02 §1 — The rally task's constructor state — Established [R-AI-02]

[R-AI-01 §7] names the rally task's three vectors (**best**, **probe**,
**drift**) and its **best score** but not their initial values. The
constructor sets them, in this order, from the map's world-unit extents
(`terrainWidthCells × 16` and `terrainHeightCells × 16` [03 §2.2]):

```text
halfX = trunc(mapWidthWorld  / 2)        # signed integer divide, toward zero
halfZ = trunc(mapHeightWorld / 2)
best  = (halfX << 16, 0, halfZ << 16)    # computed as trunc(float(half) * 65536.0)
probe = best
drift = best
bestScore = 0
```

The conversion goes through the x87 stack (integer loaded, multiplied by the
double `65536.0`, truncated once); because `half` is an integer the result is
exactly `half << 16`, and the form is recorded only so the truncation site is
not mistaken for a rounding one. The base task fields — manager back-pointer,
group record, deadline `0`, owning slot — are written first by the shared task
constructor ([R-P0-04 §2]); the rally-specific fields follow. Every other task
class starts with only the base fields plus the per-class tunables listed in
[08 "Strategy manager and its task graph"] (wave A: threshold 20000, min 3,
max 6, peer slot 3; wave B: 50000, 3, 6, peer 7; regroup A peer 2; regroup B
peer 6).

**Consequence (Established from §7's body).** The body adds `drift` to
`probe` on *every* invocation and reseeds `drift` only on a `RNG(10) == 0`
draw. With the constructor's values the first invocation already moves the
probe to `(mapWidthWorld, 0, mapHeightWorld)` and each later one adds another
half map, so until the first reseed the probe lies outside the map, the
on-known-ground test fails at its bounds check, and no score is adopted. The
rally point therefore stays at the map centre for a geometrically distributed
number of runs (mean ten). This is retail's behavior, not a defect to fix.

### R-AI-02 §2 — Small contracts the ledger pass settled — Established [R-AI-02]

* **Profile-limit gate edges.** The per-candidate limit test of [R-P0-05 §3]
  (`count < limit`, `-1` unlimited) is reached through a one-argument
  narrowing thunk (the player index is masked to a byte) and **rejects** —
  returns "over limit" — for candidate type `0` and for any type index at or
  above the catalog count, before the limit vector is read. The count it
  compares is the strategic state's per-type completed count (a signed 16-bit
  word), the limit the per-type 32-bit word the profile pass writes
  ([R-AI-01 §12]).
* **Strategic-centre and build-capable-count accessors.** The construction
  task reads the centre once into a local through a three-word copy accessor
  and the build-capable count through a plain word accessor; both read the
  strategic state fields the 30-tick refresh writes ([R-AI-01 §16]). Neither
  accessor computes anything, so the "read once" wording of [R-AI-01 §3] is
  the whole contract: a refresh landing between the two passes is not seen by
  pass 2.
* **Classifier standing-order encoding.** The two writes of [R-AI-01 §10]
  are field writes into the runtime status word: the standing move order
  occupies two bits, value `2` (roam) when `cancapture` is clear and `1`
  (maneuver) when set, the other value's bit being cleared in the same store;
  the standing fire order's two-bit field is then set to `2` (fire at will),
  its other bit cleared. The writes happen before the ungrouped test, so an
  already-grouped unit still gets both fields rewritten every 30 manager
  entries.
* **The AI status dump is dead.** The image contains a writer that prints a
  computer slot's game time, name, controller (`HUMAN`/`AI`/`INVALID`),
  terrain and profile names, difficulty, and one `<limit> <base:baseML:baseEL>`
  row per definition to a text file. Its only caller is a debug entry that no
  code, table or callback references; no retail path produces the file.
* **The target pick's vector removal.** The candidate picker of [06 §3.2]
  that the computer player's order dispatch also uses draws a random index
  into a temporary candidate vector, reads the entry, and then removes it by
  **overwriting the drawn slot with the vector's last entry and shortening
  the vector by one** (swap-remove; order is not preserved) before scoring
  it, so a later draw in the same 50-iteration loop can never return the
  same candidate, while the index-to-candidate mapping of later draws
  depends on this exact removal shape. The removal draws nothing.
* **The eco task is vtable-reached.** The resource/builder-queue body of
  [R-AI-01 §2] has no direct caller; like the other task bodies it is entered
  only through the task-class virtual table run by the manager sweep
  ([R-AI-01 §1]). It is not dead.


## R-SESS-01 — Session and account material: ledger-closure findings (2026-08-29)

Closure of the remaining lane-08 rows that belong to no computer-player
section: two per-player counters the per-player phase reads, the bank's box
write primitives, a feature-writer edge, the session-kind accessor, and the
single-player producer of the temporary-sight ("eyeball") record that doc 01
recorded as unreachable.

### R-SESS-01 §1 — The two live-player counters — Established [R-SESS-01]

The per-player phase's end-of-battle block ([R-TRIG-01 §6], [R-SKIR-01 §3])
calls two counters over the ten player slots, in slot order, each returning a
plain count. Both first require the slot's record to exist (its first word is
non-zero) and the slot's side index to differ from `10`, and both treat a
slot as *live* when its 16-bit live-unit count is non-zero **or** its 32-bit
created-unit count is zero (a player that has not yet created anything counts
as live — the same "created nothing yet" rule the kind-3 victory sweep uses).

* **Live computer players hosted here**: additionally the controller byte
  equals `2`. Nothing else is tested; watch mode does not apply to computer
  slots.
* **Live human players still playing**: the controller byte is `1`, `2` or
  `3`; then the slot must be either a local human (`1`) or a remote slot
  (`3`) whose lobby record's registration byte equals `1` — the byte slot
  registration writes ([R-SKIR-01 §2]); that `1` means "registered as human"
  is **Supported inference** from that writer, the test itself is
  Established — so a hosted computer slot (`2`) never counts; and finally the
  lobby record's watch-mode bit (the bit the elimination handler sets,
  [R-SKIR-01 §3]) must be clear.

Every consumer of both counters is on the **kind-3** (multiplayer) branch of
the block: the first decides between `You're out!  Continue Watching?` and
the "hosting AI players" message and gates the whole watch-mode path together
with the lobby's *watching allowed* bit; the second posts the watch-mode
placement line and, at the top of the kind-3 block, steps the shared
countdown toward the end latch when no human is left playing. Kinds 1 and 2
never call either counter, so a single-player engine needs neither; they are
recorded so the boundary is explicit ([R-OOS-01]).

### R-SESS-01 §2 — Box write primitives — Established [R-SESS-01]

[R-ENTRY-02 §3] gives the bounded box **read**. The subsystem writers use
three more primitives on the current account's current box:

* **size** — returns the box's byte count;
* **seek** — sets the box's cursor to `clamp(requested, 0, size)`; a negative
  request seeks to 0, a request past the end seeks to the end;
* **append** — writes `n` bytes at the cursor, first growing the box's buffer
  to exactly `cursor + n` bytes when that exceeds the current capacity (the
  buffer is reallocated in place, existing bytes preserved), then advances the
  cursor by `n` and returns `n`. The copy is a plain forward byte copy; no
  bound other than the grow applies.

The feature writer's idiom — `seek(size)` then `append(record)` — is
therefore "append at end", and a writer that seeks to 0 and appends
overwrites from the start while never shrinking the box. Reads and writes
share the one cursor.

### R-SESS-01 §3 — The single-player eyeball producer — Established; corrects [01 R-PLAT-02 §5] [R-SESS-01]

[01 R-PLAT-02 §5] states that the temporary-sight observer list ("eyeball"
records) has as its only producer the handler of a received unit-death
packet, and concludes that in single player the list is always empty. The
first half is right and the conclusion is wrong: the **same handler is the
central death handler** the local death path calls directly, after building
the death record that networking would send ([R-OOS-01 §1], type `0x0c`;
[06 §12.1]). Retail therefore appends an eyeball in every session kind.

The handler appends when all of these hold, in order:

1. the victim's runtime status carries the *live* bit;
2. the victim's owner slot index equals the local slot index;
3. the visibility mode word has bit 1 set — the `Circular` or `True` modes
   of [03 R-VIS-01 §1], never `Permanent`;
4. the list holds fewer than 20 records (at 20 the append is silently
   dropped).

The record is then filled exactly as [01 R-PLAT-02 §5] lays it out: owner =
the local player record; sight distance = the victim definition's
`sightdistance` word [fmt fbi]; height byte = the low byte of the
definition's height field (the field the target-top and repair-admission
tests read, [04 R-SPEC-01 §15]); position = the victim's world X, Y, Z with
Y raised to `(SeaLevel + 1) << 16` when lower; expiry = `globalTick + 60`.
Before the count is incremented the record's coverage is computed and
published through doc 03's observer path — the true-LOS raster when the mode
word's bit 2 is also set (`True`), the circular coverage tile otherwise
([03 R-VIS-01 §2]); that arithmetic is doc 03's contract and is cited here
only to fix the order: **coverage publish, then count increment**. The 60-tick expiry is then consumed by the
post-loop expiry pass of [01 R-PLAT-02 §5], which is therefore **not** a
no-op in single player.

Implementation consequence: a unit the local player loses keeps revealing
its sight radius for two seconds after death under `Circular`/`True` line
of sight. Doc 01's "always empty" paragraph and its expiry-pass remark, and
doc 03's visibility-producer census, need the corresponding correction
(cross-document follow-up; not edited by this unit).

### R-SESS-01 §4 — Feature writer: an animating cell whose sequence matches no family writes no record — Established [R-SESS-01]

[R-SAVE-FEATURE-01] gives the three record maps and says the animating
record's selector nibble is `0`, `1` or `2` for the burn, death and reclaim
families. The writer side has one more edge: the nibble is chosen by
comparing the cell's live animation-sequence pointer against the
definition's three family sequences in that order, and when it matches
**none** of them the cell is skipped entirely — no `Animating Features`
record, and the `Number of Animating Features` count is not incremented.
Such a feature (one whose live sequence pointer was set by some path other
than the three families) is simply absent from the save and does not exist
after load. The "other selector values" the reader tolerates therefore never
originate from the retail writer. The 3D and normal branches have no such
skip.

### R-SESS-01 §5 — The session-kind accessor — Established [R-SESS-01]

The many sites that "test the session kind" (46 callers across every lane)
read it through one accessor that returns the **first word of the session
object** — the game type of "Game session": `1` campaign, `2` skirmish, `3`
multiplayer ("Mission type dispatch"). There is no other reader shape.
[08 "Mission type dispatch"]


### R-SESS-01 §6 — The code-3 order sub-object has one runtime constructor: the `AirToAir` handler — Established [R-SESS-01]

[R-SAVE-02 §10] left "which VTOL-family order handler" constructs the
code-3 payload (the two-vector work record) as **Unknown**. The class is
[04 R-PATH-01 §9]'s air moving point — the *velocity marker* whose per-tick
turn clamp is [04 R-MOV-03 §2]'s — and its runtime constructor has exactly
one call site: the `AirToAir` handler's phase 0/1 leg ([04 R-AIR-01 §8],
[04 R-ORD-02 §5]). Its only other constructor is the save reader's, which
rebuilds it from the code-3 box. A code-3 record in a save therefore always
belongs to an `AirToAir` order that was in flight at save time; its first
triple is the marker's position and the second its per-tick velocity, and
the class code the reader matches is the marker's own class-code slot
(`3`, [04 R-MOV-03 §9]). The three trailing `u16` words remain unnamed
(the "Save and replay" tail keeps that item).

The AI planner's group-centroid helper the ledger had left uncited is
[R-AI-01 §9]'s ("Group centroid"); the empty virtual slot on the task-class
method table is the base task's ([R-AI-01 §1]) and has no behaviour. The
eleven-slot connection table's static initialiser (send interval 200 ms,
time-out clamp, and the `(ms × 30 + 999) / 1000` tick conversion of
[01 R-PLAT-01 §2]) and the packet-buffer class's empty virtual stubs are
transport code and stay outside the single-player boundary of
[R-OOS-01 §3].

## R-AI-03 — Placement root: the patch vector and the scatter helper, exactly

This unit closes the last implementation-blocking gap of the computer player:
the exact arithmetic of the placement root ([R-AI-01 §3]'s `placeCandidate`),
its two search helpers, and the metal-spot vector the exhaustive helper reads.
It sharpens the prose of "Placement root and search helpers" in six places,
each stated where it applies: the radius growth and its cap are **world
units**, not cells (§2); the origin is the strategic centre when the builder is
*within* the radius, and is interpolated only when it is *outside* it — the
earlier "zero or at least the scaled radius" wording was inverted (§2); the
"sort by distance" is a binary heap, and the early-stop slack of 160 is in
**squared cell distance** (§3); the region set is selected by the sign of the
definition's **MinWaterDepth**, not of a slope field (§4); the footprint
blocker *does* apply the waterline band, so the exhaustive path is not
water-blind (§3); and the scatter path's score comparison reads a **stale**
accumulator for every `bmcode` definition, because the validator mode it uses
never writes the accumulator (§4). Numbers below come from the executable, not
from Nanolathe's stub or prior inference. Vocabulary: "cell" is a 16-world-unit
plot cell [05 R-PROD-01 §6]; `footX`/`footZ` are the definition's footprint
extents copied from its movement class [04 R-DOC04-A]; `RNG(b)` is the bounded
simulation draw, which returns 0 **without advancing** when `b < 2` (signed)
[01 §7.1].

### R-AI-03 §1 — The metal-spot vector: builder, record, scan, consumer — Established [R-AI-03]

**Owner and lifetime.** Each strategic state owns one vector of metal-spot
records. It is built by the battle-entry tail, step 7 of [R-ENTRY-01 §8] — once
per slot that owns an AI record, after the second resource grant, on every
session kind including a restored save. Nothing rebuilds it afterwards: the
30-tick strategic refresh does not touch it, and reclaiming or destroying a
feature does not remove its record. The vector therefore describes the map as
it stood at battle start.

**Record.** `{ cellX int16, cellZ int16, metal float32 }`, eight bytes, packed
as x in the low half-word and z in the high half-word followed by the float.
The `metal` field is the feature definition's authored `metal` value narrowed
through the 16-bit mask the feature parser applies (`float32(value & 0xffff)`)
[05 R-FEAT-01 §6]. **It is never read as metal**: the only consumer overwrites
it with a sort key (§3).

**Scan.** The builder first empties the vector (end := begin, capacity kept),
then visits every plot cell in row-major order — rows `z = 0 … mapCellHeight−1`
outer, cells `x = 0 … mapCellWidth−1` inner — and appends `(x, z, metal)` when
all three hold, tested in this order:

1. the cell's feature reference is a real feature index — strictly less than
   the `0xfffb` reserved band. The `0xfffe` "part of a larger feature" marker
   and the `0xffff` "no feature" value both fail this test, so a multi-cell
   feature yields exactly one record, at its anchor cell;
2. the referenced feature definition's `metal` compares **not equal** to
   floating zero;
3. the definition's `indestructible` flag is set.

There is no threshold on the metal value beyond non-zero, no check of the
reference against the feature count (the loader guarantees it), and no
ordering step: the vector is in row-major cell order. On a stock map every
metal deposit is an indestructible feature with a non-zero `metal`, so the
vector is the deposit list; a map that authors a destructible metal feature
leaves it out, and one that authors an indestructible feature with `metal`
but no per-cell metal seeding yields records the exhaustive helper will visit
and score at the uniform surface value.

**Consumer.** The exhaustive helper (§3) is the only reader. The scatter helper
(§4) never consults it.

### R-AI-03 §2 — The root's origin step, exactly — Established [R-AI-03]

Inputs: the builder position `b` (16.16 world, three axes), the strategic
centre `c` ([R-P0-05 §5]), the per-player **placement search radius** (a plain
integer of world units, zero at construction), and the map's world-unit
extents `W`, `H` (cell counts × 16, [R-AI-01 §3]).

```text
if radius < max(W, H): radius += 160            # signed; world units; the grown
                                                #   value persists across attempts
dx = c.x − b.x ; dy = c.y − b.y ; dz = c.z − b.z # 32-bit 16.16 differences
dist = trunc( sqrt( dx·dx + dy·dy + dz·dz ) )   # x87: three integer loads, the
                                                #   squares summed in that order,
                                                #   one square root, one __ftol
R = radius << 16
if R < dist:                                    # signed: builder OUTSIDE the radius
    scale    = (int64(R) << 16) / int64(dist)   # 64-bit signed divide
    origin.a = b.a + int32( (int64(d_a) · scale) >> 16 )   # a = x, then y, then z
else:                                           # within (or exactly at) the radius
    origin = c
```

Because the deltas are 16.16 integers, `sqrt` of their squared sum is itself a
16.16 distance, so `dist` compares directly with `R`. The interpolation moves
the origin from the builder **toward** the centre by exactly `radius` world
units; when the centre is already within reach the origin *is* the centre. All
three axes are interpolated, but the helpers read only `x` and `z` of the
origin: `origin.y` is dead. The earlier prose had the branch inverted. The
first attempt of a fresh player therefore searches from a point 160 world
units toward the centre (or the centre itself), and each failed attempt widens
the ring by 160 until the radius reaches the larger map extent.

**Helper selection** is unchanged from "Placement root and search helpers":
`extractsmetal == 0.0` → scatter with no draw; otherwise `d = RNG(255)` and
`surfaceMetal < d` (signed) → exhaustive, else scatter. The selector draw is
taken **after** the origin step and before any helper draw. The exhaustive
helper receives `radius × 4`; the scatter helper receives `radius` unscaled.

### R-AI-03 §3 — The exhaustive metal-spot helper — Established [R-AI-03]

Inputs: the definition, the origin (`x`, `z` used), the metal-spot vector of
§1, `D = radius × 4`, and the output cell. It draws **no** random numbers.

**Empty vector → failure** (return false before anything else).

**Origin cell.** Both coordinates are the plot cell whose *top-left* would put
the footprint's centre nearest the origin:

```text
cx = int16( (origin.x − (footX << 19) + (1 << 19)) >> 20 )    # arithmetic shift
cz = int16( (origin.z − (footZ << 19) + (1 << 19)) >> 20 )
```

(`1 << 19` is 8 world units in 16.16; `>> 20` divides by 16 world units and
floors — the same rounding the build cursor uses [07 §9].)

**Filter.** For each record in vector order, `d2 = (px − cx)² + (pz − cz)²`
in 32-bit cell units; keep the record when `d2 <= D · D` (inclusive). `D` is
a count of world units used as a count of cells, so the search disc is
`4 × radius` **cells** — 64× the radius in world units; this is what retail
does. A kept record is copied to a working vector and its `metal` float is
**overwritten** with `float32(−d2)`.

**Ordering.** When the working vector holds at least two records it is made
into a binary max-heap on the float key with the standard library's
make-heap (sift each index from `n/2 − 1` down to 0: move the hole down to a
leaf choosing the right child unless `right.key < left.key`, then push the
saved record back up while `parent.key < key`), and the loop below takes the
front record and re-heaps with the pop-heap that moves the front to the last
slot and re-sifts the former last record. The greatest key is the least
`d2`, so candidates come **nearest first**. Ties in `d2` are ordered by the
heap mechanics — deterministic from the vector order, but **not** the vector
order itself; an implementation must reproduce the heap to reproduce retail's
choice among equidistant deposits.

**Candidate loop.** With `best := 0`, `bestCell := none`, `firstD2 := −1`:

```text
while working vector not empty:
    (px, pz) = front record
    candX = int16( px − trunc((footX − 3) / 2) )      # signed division toward zero
    candZ = int16( pz − trunc((footZ − 3) / 2) )      #   (footX = 1 → +1; 2 → 0; 5 → −1)
    c2 = (candX − cx)² + (candZ − cz)²                # recomputed from the CANDIDATE cell
    if firstD2 >= 0 and c2 > firstD2 + 160: break     # early stop, squared-cell units
    if blocker(def, candX, candZ, self = 0, ghost = 0):
        score = accumulator
        if score > best:                              # strict; a zero-metal footprint never wins
            best = score ; bestCell = (candX, candZ)
            if firstD2 == −1: firstD2 = c2            # set once, at the first accepted candidate
    pop front
return best != 0 ? (bestCell, true) : failure
```

The candidate offset centres the footprint on the deposit for a 3-cell
footprint and biases larger ones toward the top-left. The early stop is
measured against the squared distance of the **first** accepted candidate,
never updated by later better-scoring ones, and the slack `160` is compared as
squared cells (a candidate more than ~12.6 cells beyond the first hit stops
the scan). Failure of this helper is failure of the whole placement attempt —
no fall-through to §4, and the radius keeps its grown value.

**The blocker call.** The validator is the yard-map footprint blocker of
[07 §9] (the "footprint validator"; doc 05 "Geothermal requirement" for the
yard bytes), called with self identity `0` — so any occupant rejects — and the
ghost flag `0`, which **skips** the known-map/visibility gate: an unrevealed
cell is as placeable as a revealed one. Its bounds test is `candX > 0`
(strict — a candidate at cell column 0 is rejected), `candX + footX <
mapCellWidth`, `candZ + footZ < mapCellHeight`; it does **not** test `candZ`
for sign (see §6). On entry it clears the process-wide score accumulator and
then, for every footprint cell in row-major order, adds the cell's **metal
byte** ([05 R-PROD-01 §6]: the uniform `SurfaceMetal` seed on canonical
maps, or the legacy per-cell byte) before applying that cell's yard-byte
rules. The accumulator is therefore the footprint's metal-byte sum, and a
rejected footprint leaves a partial sum behind (unread here, but see §4). The
blocker's tail applies the definition's `MaxSlope` and the waterline band
`waterline − MaxWaterDepth <= lowest yard-bit-3 height` and
`highest <= waterline − MinWaterDepth` — so the exhaustive path **is**
waterline-checked through the blocker; the earlier statement that it "has no
waterline test at all" is retracted.

### R-AI-03 §4 — The statistical scatter helper — Established [R-AI-03]

Inputs: the strategic state, the definition, the origin (`x`, `z`), the
unscaled `radius`, and the output cell.

**Limit.** `limit = ((surfaceMetal × footZ) × footX) × 2`, 32-bit integer
arithmetic in that order, where `surfaceMetal` is the mission's `SurfaceMetal`
word on the session record.

**Region words.** The strategic-state constructor ([R-ENTRY-01 §3]) draws the
eight values of [R-P0-05 §5]'s inventory into two region sets, in this order:

```text
land set  (margin = 3):  cellW = RNG(10) + 11 ; cellH = RNG(3) + 11
                         offX  = RNG(cellW) − cellW/2 ; offZ = RNG(cellH) − cellH/2
water set (margin = 6):  cellW = RNG(20) + 14 ; cellH = RNG(3) + 14
                         offX  = RNG(cellW) − cellW/2 ; offZ = RNG(cellH) − cellH/2
```

(the `+ 11` / `+ 14` is `margin + 8`; the halving truncates). They are int16
words, never rewritten. The helper picks the **land** set when the
definition's `MinWaterDepth` is negative and the **water** set when it is
`>= 0` — that word, copied from the movement class [04 R-DOC04-A], is the
selector, not a slope field.

**Trial loop**, `t = 0 … 29` (thirty trials, counter compared `< 30`):

```text
r  = RNG(radius)                        # draw 1; radius >= 160 here, so always taken
a  = RNG(65536)                         # draw 2
wx = origin.x − sin(a, r << 16)         # the shared trig helpers of [R-AI-01 §3],
wz = origin.z − cos(a, r << 16)         #   negated at use as everywhere in this planner
qx = int16( (wx − (footX << 19) + (1 << 19)) >> 20 )     # same cell rounding as §3
qz = int16( (wz − (footZ << 19) + (1 << 19)) >> 20 )
ox = RNG(cellW − margin − footX)        # draw 3 — skipped (0, no advance) when bound < 2
gx = int16( (int32(qx) / cellW) × cellW + offX + ox )    # idiv: toward zero
oz = RNG(cellH − margin − footZ)        # draw 4 — likewise
gz = int16( (int32(qz) / cellH) × cellH + offZ + oz )
if validator(def, self = 0, (gx, gz), mode = 1) and accumulator <= limit:   # inclusive
    out = (gx, gz) ; return true
return false after the thirtieth trial
```

The quantisation snaps the trial cell to a lattice of `cellW × cellH` blocks
(toward zero, so blocks straddle the origin asymmetrically for negative
cells), shifts the lattice by the per-player `(offX, offZ)`, and scatters
within the block by `ox`, `oz` — leaving `margin + footprint` cells of the
block untouched. When the footprint is at least `cellW − margin − 1` wide the
`ox` bound drops below 2 and **no draw is taken**; the per-trial draw count is
therefore two, three or four depending on the footprint against the drawn
region widths, and the order is always radius, angle, x-offset, z-offset.
The z lattice is 11–13 cells on land and 14–16 on water, so buildings line up
in rows; that is retail's base layout.

**The validator call** is the placement validator with a mode argument
(the routine [R-AI-01 §7]'s rally probe also uses), mode `1`, self identity
`0`. Its contract in this mode:

1. bounds: `gx >= 0`, `gz >= 0`, `gx + footX < mapCellWidth`,
   `gz + footZ < mapCellHeight`; off-map returns **false** (only mode `2`
   treats off-map as placeable);
2. `bmcode == 0` (a mobile definition): delegate to the footprint blocker of
   §3 with self `0` and ghost `0` — which **writes** the accumulator;
3. `bmcode != 0` (every building): walk the footprint cells row-major with the
   plain rule set, no yard bytes and no accumulator write. A cell rejects when
   its feature reference resolves to a feature whose `blocking` flag is set
   (a reference at or beyond the feature count, or in the `0xfffb`–`0xfffd`
   band, blocks; a `0xfffe` part-cell is resolved through its anchor offsets to
   the anchor's feature; `0xffff` is empty); when its occupant id is non-zero
   (self is `0`); when its low height byte is below `waterline −
   MaxWaterDepth`; when its high height byte is above `waterline −
   MinWaterDepth`; or when `high − low` exceeds `MaxSlope` and either the cell
   is above water (`low >= waterline`) or `high − low` also exceeds
   `MaxWaterSlope`. Otherwise placeable.

This is the water legality "Placement root and search helpers" located: the
band is enforced per cell, on every cell, with the definition's own depth
fields.

**The stale score.** Because branch 3 never writes the accumulator, the
`accumulator <= limit` test for a building reads whatever the **last footprint
blocker call in the process** left there: this player's most recent exhaustive
attempt, a mobile-definition validation by any player (branch 2), or the
human player's build cursor and order placement [07 §9] — including a partial
sum from a rejected footprint. The process global starts at zero, so a fresh
session accepts the first valid trial. This is retail's contract, not a
Nanolathe choice; an implementation that wants retail's placement sequence
keeps one process-wide accumulator written only by the blocker, and one that
prefers a sane test (the trial footprint's own metal-byte sum) takes a
sanctioned divergence, since the two differ only in *which* valid trial is
accepted, never in whether a placement is legal.

#### R-AI-03 §4-A — Which authored key the `surfaceMetal` word is — Established [R-AI-03]

§4 above says the limit's `surfaceMetal` is "the mission's `SurfaceMetal` word
on the session record" without saying which authored key fills that word. It
is the **selected schema's** `SurfaceMetal`, not a `[GlobalHeader]` key — the
same word that seeds every plot cell's metal byte [05 R-PROD-01 §6], which is
why §1 can say the metal-spot scan scores "at the uniform surface value".

Evidence is the authored corpus of the reference install: across its 275 map
`.ota` files the key `SurfaceMetal` occurs 635 times and **every** occurrence
is inside a schema section (`[Schema N]`); none is in `[GlobalHeader]`. The
count exceeds the file count because a map authors one per schema. A reader
that takes the word from the OTA's global section therefore yields zero on
every map in the corpus.

**Why this matters, and the failure it produces.** On a canonical map every
cell's metal byte is that same schema word `M`, so a valid trial footprint of
`footX × footZ` cells sums to `M × footX × footZ` — exactly **half** the
`M × footZ × footX × 2` limit. The inclusive `<= limit` test therefore passes
for any ordinary site and bites only where indestructible metal-bearing
features have raised the bytes above the uniform seed across the footprint
[05 R-FEAT-01 §7]; that is what the limit is for. It follows that the
sanctioned divergence of §4 — comparing the trial footprint's own metal-byte
sum instead of retail's stale process accumulator — is only equivalent while
the limit is built from the *same* word that seeded the cells. Supply zero
there and the limit is zero while the sum is positive, and the helper rejects
**every** geometrically valid trial for **every** building: thirty trials
exhausted, no non-extractor site ever accepted, and a computer player that
places nothing but metal extractors (those take the exhaustive helper of §3,
which has no limit test) for the whole battle. This was Nanolathe defect
PT3-14; the reading corrected here is the one that produced it.

**The selector draw reads the same word.** §2's helper selection compares
`surfaceMetal < RNG(255)` (signed) for an extractor definition. A zeroed word
makes that comparison true on 254 of 255 draws, so extractor placement is
effectively always exhaustive; with the schema value of a stock map (`3` on
`Ashap Plateau`, for instance) the scatter branch is reachable as authored.

### R-AI-03 §5 — What the root returns, and the radius — Established [R-AI-03]

On helper success the root converts the cell to a world position and writes
**two** of the caller's three words:

```text
out.x = int32( footX + 2 · gx ) << 19        # = (16·gx + 8·footX) in 16.16: the footprint centre
out.z = int32( footZ + 2 · gz ) << 19
radius = 0
return true
```

`out.y` is not written. The construction task's out buffer is a stack local
that nothing initialises before the call, so the `y` of the submitted
MobileBuild position is stack residue; what the order service does with it is
doc 04's ([R-ORD-01]) — the position's `x`/`z` alone determine the site. On
helper failure the root writes nothing, leaves the grown radius in place, and
returns false; the task then skips the submit and the next invocation (90
ticks later, [R-AI-01 §3]) grows the radius again. The `cancapture` distance
cap of [R-AI-01 §3] is applied by the task to `out.x`/`out.z` after a
successful return and can veto the placement without resetting anything — the
radius is already zero by then.

### R-AI-03 §6 — Unknowns left, with deciders — Unknown [R-AI-03]

- **Negative candidate row in the exhaustive path.** The blocker rejects a
  candidate whose column is `<= 0` but does not test the row's sign; a deposit
  in row 0 or 1 with a footprint of five or more rows yields `candZ < 0` and a
  row pointer before the plot grid. Whether that reads harmlessly (returns
  false on a garbage cell) or faults is not established · static trace of the
  blocker's cell addressing for a negative row, or a retail probe on a map
  with a metal deposit in its top row.
- **The mode-2 off-map acceptance** of the placement validator is outside
  this unit (no computer-player caller passes 2) · static trace of the
  mode-2 callers (doc 04 order handlers).
- **The `y` of the submitted position** is stack residue (§5); whether the
  MobileBuild handler reads it at all · static trace of the order handler
  [R-ORD-01].
- **Field label in the class routine.** [R-P0-05 §5] and [R-P0-04 §3]
  describe a "max-slope" term (`× 3` when non-negative; classifier row
  "definition max-slope field signed greater than zero"). The definition word
  both routines read is the one the scatter helper reads, and that word is
  established here as **MinWaterDepth** (the FBI compile copies the movement
  class's `MinWaterDepth` into it). Corrections are recorded at those
  sections; the residual is only whether any *other* reader of the word
  exists that a label change would affect · static trace of the word's
  readers.
- Nothing else in the placement path is open: every constant, comparison,
  truncation, draw bound and draw order above is read from the executable.

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
- Victory/defeat trigger queues are built for every session kind but polled only in the campaign kind, in the local player's once-per-30-tick block: victory first (AND) then defeat (OR). Skirmish and multiplayer never poll a trigger queue; their end conditions are the live-unit-count predicates of [R-SKIR-01 §3] and [R-TRIG-01 §6]. Removal and capture notifications reach every built record in every kind. No trigger consults an alliance row; the skirmish victory sweep does [R-TRIG-01 §1] [R-TRIG-01 §6].
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

Open items only. Each bullet states what is unknown, the section that owns it,
and the decider that would close it. Findings that closed an item live in the
body and are not restated here.

**Correction (2026-08-28, RWU-00-5).** This tail had become an inventory of
finished work: entire multi-line bullets on campaign progression, mission
placement order, trigger evaluators, meteor spawning, restriction flags, the
computer player's construction selection and score fields, initial seed
agreement, host-authority migration, path/effect/projectile persistence,
indirect random-state serialization, `WinLoseTime`/`DisplayTimer` consumers,
campaign progress outside `.sav`, the replay-absence sweep, and session-end
ordering opened with "are established" or "closed" and then recited the
finding. The recitals are deleted here only; the body sections and the
[R-P0-04] / [R-P0-05] / R-SAVE-* findings continue to own them.

### Sessions and campaign

- The runtime player row that owns the single 11-byte `Players/Alliances`
  save box during battle restoration; the box's detached bytes and forced
  self byte are established, but applying them to a particular player row is
  **Unknown** · "Player records" / [R-SAVE-02 §6] · static trace of the
  alliance reader's destination.
- UI-level names for session states 0–4; their behavior, transitions,
  callbacks, and admission-mask classes are established · "Session states" ·
  static trace.
- The AnyMsn toggle's exact listbox-selection effect — the last residual of
  the all-missions bit and of campaign progress held outside battle `.sav`
  files · "Progression" · manual retail observation.
- The meaning of the per-slot auxiliary word that admits a slot to the score
  display and statistics rows even when its controller test fails ·
  [R-CAMP-01 §7] · static trace of the word's writers.
- Whether commander-kill and commander-loss counters have save-bank keys in
  addition to the established `Kills` and `Losses` entries · "Player records"
  / [R-CAMP-01 §10] · static trace of the Player%i writer and reader.
- Transport-selection policy beyond generic move orders, and any distinct
  naval or air placement geometry · "Placement root and search helpers"
  [P0-04] · static trace.
- The reader, if any, of the placement record's `InitialGroup` nibble ·
  [R-TRIG-01 §11] · static trace from the flag byte's low-nibble mask.
- Which per-slot flag the start barrier's `player(s) ready` count reads ·
  [R-TRIG-01 §9] · static trace of the barrier screen's slot walk.
- What the 3DO texture frame lookup returns for the `-1` colour the
  controller-cycle quirk can store · [R-SKIR-01 §8] · static trace of the
  frame-array accessor's bound handling.
- Whether any stock `.GUI` names the `TECHLEVL` / `COMMNDER` tokens and what
  reads their static table · [R-SKIR-01 §11] · asset census, then static
  trace.
- Presentation of meteors outside the world renderer · doc 03 · static trace.
- The identities of the eleven interface words and the one flag word the
  world rebuild resets before loading content · [R-ENTRY-01 §3] · static
  trace of their readers (doc 07).
- The writers of the requested/active speed words in effect at a fresh
  campaign or skirmish battle (entry writes them only for multiplayer) ·
  [R-ENTRY-01 §3], [R-ENTRY-01 §9] · static trace of the speed words'
  writers (options screen / registry).
- The readers of the loading state's *watching* table, and therefore
  whether an all-zero table has any single-player effect · [R-OOS-01 §2]
  · static trace of the table's readers.
- Whether the first pump's budget is five ticks in practice ·
  [R-ENTRY-01 §9] · manual retail observation of the game-time counter at
  the first battle frame.
- The four presentation helpers of the handoff frame, and the external
  post-placement hook of the multiplayer path · [R-ENTRY-01 §1],
  [R-ENTRY-01 §5] · static trace (doc 07; the hook is OOS).
- Whether the strategic-state constructor's `+20` term sees a null pointer
  or an empty list for a definition with no download-menu entries — i.e.
  whether the download-menu list is allocated for every definition ·
  [R-ENTRY-02 §2], "Strategic state construction and refresh" · static trace
  of the download-menu compile's per-definition allocation.
- The authored FBI key behind the definition height field whose low byte
  the death-eyeball record copies · [R-SESS-01 §3], [04 R-SPEC-01 §15] ·
  static trace of the FBI reader's key table (doc 04 / doc 02 own the key).
- Whether the lobby record's registration byte value `1` means "registered
  as human" for the remote-slot branch of the live-human counter ·
  [R-SESS-01 §1] · static trace of the registration writer's value table.

### Computer player

- Whether a writer separate from the recovered direct-writer caller set
  mutates manager task vectors through an indirect alias · "Strategy manager
  and its task graph" [R-P0-04] · static trace. Marked `TODO(question)`; no
  additional writer may be claimed without new evidence.
- Whether the footprint blocker reads harmlessly or faults on the negative
  candidate row the exhaustive metal-spot helper can produce (a deposit in the
  top two rows with a footprint of five or more rows) · [R-AI-03 §6] · static
  trace of the blocker's cell addressing for a negative row, or a retail probe.
- Whether the MobileBuild order handler reads the `y` of the position the
  construction task submits — the placement root writes only `x` and `z`, and
  `y` is stack residue · [R-AI-03 §5], [R-AI-03 §6] · static trace of the
  handler (doc 04 [R-ORD-01]).
- Whether any reader other than the class routine, the classifier and the
  scatter helper consumes the definition word now established as
  `MinWaterDepth` (previously labelled "max-slope" in this doc) · [R-AI-03 §6]
  · static trace of the word's readers.
- Semantic name of the class routine's signed classification helper · "Class-vector
  recomputation loop and inputs" · static trace.
- Semantic names of the order gate-mask bits the construction task tests
  (bit 3 in its build pass, bit 14 in its repositioning pass) · [R-AI-01 §3],
  doc 04 "Order descriptor table" · static trace.
- Which global option bit selects between the rally probe's two validation
  forms, and therefore whether the form that reads the local viewing slot's
  visibility bit is reachable in a networked session · [R-AI-01 §7] · static
  trace from the skirmish option word into the world flags word.
- Whether the attack wave's engaged latch and the rally task's best point,
  drift and best score are serialized · [R-AI-01 §4], [R-AI-01 §7],
  "Save-file organization" · static trace.
- Transport, naval, air, repair, reclaim and scouting policies as distinct
  task classes · "Strategy manager and its task graph" · bounded negative over
  the complete vtable run; only a manual retail observation could contradict
  it.

### Networking

Nanolathe does not implement multiplayer, so every bullet in this group is
recorded to keep the specification exhaustive rather than to gate work. The
boundary — which packets the single-player path still constructs, which
lobby fields it reads, which subsystems are out of scope in full, and the
video contract — is [R-OOS-01 §1]–[R-OOS-01 §4]; nothing below is needed by
a single-player implementation.

- Lobby slot-state values, host privilege, ready flag, blocked state, edit
  permission, and start condition · "Lobby behavior" · static trace.
- DirectPlay provider and session enumeration, lobby handoff, connection
  setup, addressing, password, and teardown, and any provider-specific
  transitions outside the reviewed callbacks · "Peer transport state" ·
  static trace.
- Payloads of the packet types the in-game receiver forwards whole to a
  subsystem; the type byte, per-type fixed length, admission mask, dispatch
  roles, and inline-decoded field layouts are established · "Packet
  registry" · static trace.
- The lobby receiver's switch, which owns the types whose admission mask
  excludes the battle-loading and live-battle states · "Declared packet types" ·
  static trace.
- Frame numbers, sequence numbers, acknowledgement fields, checksums, sender
  identity, and wrap behavior inside those payloads · "Declared packet types" ·
  static trace.
- Guaranteed versus ordinary delivery per packet family · "Declared packet types" ·
  static trace.
- The local-input scheduling delay, separately from future-frame retention and
  the delayed gameplay queues · "Lockstep advancement" · static trace.
- Send pacing, batch flush, retransmission timeout, retry count, queue
  overflow, and round-trip adaptation · "Lockstep advancement" · static trace.
- Duplicate, stale, future, oversized, unknown-type, and wrong-sender packet
  handling beyond the established malformed-custom-payload hang path
  · "Declared packet types" · static trace.
- Semantic identity of the pacing-scan progress dword — remote-peer reported
  progress versus earliest pending order time, two competing readings of the
  same code; soft pacing itself is established · "Lockstep advancement" · static
  trace.
- Command authority for host, local player, remote player, computer player,
  observer, pause, speed, sharing, and game termination · "Lockstep advancement" ·
  static trace.
- Map, resource, economy, and other hash contents, cadence, payloads, and
  mismatch handling; packet `0x27`'s
  trailing-integrity-data producer algorithm; and the throttle-acknowledgement
  format in the participant-state receiver · "Synchronization and integrity checks" · static trace.
- Reconnect, late join, spectator join, and temporary transport-loss behavior,
  or a bounded absence for each · "Peer transport state" · static trace.

### Save and replay

- Remaining semantic mappings inside the bulk binary boxes: the two
  path-marker words (code-2 payload `0x22` and `0x32`), the code-3 record's
  handler identity and word names, the unit record's relation byte (`0x8E`),
  current/previous byte pair (`0xAC`/`0xAD`) and countdown byte (`0xB1`), and
  the per-word layout of the `u%04xacc` resource account (doc 05) ·
  "Save-file organization" [R-SAVE-02 §6] [R-SAVE-02 §7] [R-SAVE-02 §10] ·
  static trace (field-isolation of each reader). Everything else in the unit,
  mover, script, order, feature and player records is named.
- The exact frame slot of the script writer's two lost piece-level getter
  results (which of dwords 24/25 is residue is Established; which getter was
  lost is not) · [R-SAVE-02 §9] · byte-level trace of the writer's three
  indirect calls.
- Which file timestamp the save-list enumerator's 32-bit sort key derives
  from · [R-SAVE-02 §1] · static trace of the enumerator's find-data
  conversion.
- The `GAMENAME` edit gadget's admitted character set and length, and the
  code page of the file name · [R-SAVE-02 §1], doc 07 · static trace of the
  GUI edit-control key filter.
- The result of an out-of-range `Difficulty` in the summary panel ·
  [R-SAVE-02 §3] · static trace of the adjacent data.
- Which descriptor-class virtual the unit loader invokes on the restored
  head order's sub-object after both queue segments are rebuilt (it is
  called only when the head record carries a sub-object) · [R-SAVE-02 §11],
  [R-SAVE-02 §10] · static trace of the descriptor class vtable slot.
- Framing, initial snapshot, command timing, random state, seek behavior,
  version checks, and UI of a replay path, should one ever be found; the
  whole-image sweep covering dynamically built names, debug modes, and
  media/capture paths found none · "Bounded absence" · static trace.
- Precise presentation sequencing of the overlay transitions inside the
  front-end router at session end; the post-battle prefix is closed
  · "Session end and reporting", doc 07 · static trace.
