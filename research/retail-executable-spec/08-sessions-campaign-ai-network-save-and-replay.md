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
renderer. In particular, an older analysis incorrectly labeled the scenario
unit reconstructor as the strategic AI planner. That analysis is retracted.
This document treats the strategic planner as unknown and retains only the AI
fields and control paths that the executable directly establishes.

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

The executable parses these fields into fixed records before unit creation.
A complete reader census over the runtime unit record closes the parsed-only
list above: `MissionCriticalUnit`, `AiIgnore`, `AiPriorityTarget`,
`InitialGroup`, `BuildPriority` and the delayed-creation countdown have no
reader anywhere in the image. The immunity high bit *is* transferred to a
runtime status bit on the created unit — and that bit likewise has no reader,
so the single consumed flag is itself inert in this executable. Retain all of
them verbatim for save fidelity and diagnostics; act on none of them.
`UseOnlyUnits` is not inert: it is read at OTA load and routed through the
resource resolver into the campaign `camps\useonly` area — path-building
evidence that it feeds the campaign restricted-units mechanism.

The initial-mission string is interpreted once at battle start, on the load
worker after all units exist (mission-type-1 games and `BetweenMissions`
restores). It is processed as comma-separated verb tokens (`m`, `a`, `b`,
`bw`, `d`, `g`, `i`, `o`, `p`, `s`, `u`, `w`, `wa` families) that queue orders
through the ordinary order-descriptor registry. The complete verb grammar,
argument formats, and malformed-input behavior are specified in document 04,
section 3.6.

### Trigger object

Mission victory and defeat conditions are allocated as polymorphic trigger
records. A builder probes the eighteen condition keys in a straight chain and
allocates a per-condition record on a hit. Record size varies with the
condition: the smallest are flag-only twelve-byte records; boundary conditions
carry a threshold; string-plus-count shapes and a canonicalizing string shape
are larger; the radius condition is the largest, carrying X/Y/Z plus radius
payload. Every record leads with a vtable pointer whose table covers all
eighteen condition types — shared destructor and helper slots plus one
condition-specific checker slot — with the trigger's completed flag adjacent.

The mission object owns separate growable arrays for victory and defeat
conditions, each count kept beside its array. An empty queue receives an
injected default destroy-all-units-class victory or all-units-killed-class
defeat trigger, guaranteeing one win and one lose condition.

Timer triggers store time in authoritative ticks after multiplying authored
seconds by thirty.

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

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

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

## Campaign catalog and progression

### Campaign discovery

Campaign definition files live in the campaign directory and are enumerated
through the VFS; a requested campaign file that does not resolve produces a
diagnostic naming it. A campaign file is an ordinary TDF whose top level
contains contiguous `MISSION0`, `MISSION1`, and so on sections, enumerated
until the first missing index; `MissionList` is only the allocation tag for the
resulting array, not an authored wrapper. Each mission section supplies a
`missionname` read through the language-prefixed string accessor, defaulting to
a built-in "unnamed mission" error string. Discovery constructs a campaign list
and then a mission list for the selected campaign.

The campaign front end consumes:

- campaign and mission titles;
- descriptions;
- difficulty choices;
- planet identifiers;
- briefing and narration names;
- panorama, rotation, glamour, and sound media;
- mission ordering and availability;
- completion/progression state.

The front end has campaign and direct mission-selection lists. Progression and
unlock behavior, including whether an all-missions override exists, remains
unknown.

### Planet and briefing selection

Planet values select parallel executable tables for briefing keys, panorama
art, and rotation animation. A special lunar branch is controlled by display
state. The briefing path combines GUI panels, text, planet imagery, narration,
and mission media.

Missing optional media can fall back or suppress presentation. Missing data
required to identify the mission or its terrain is a load error.

### Progression

The executable contains campaign selection, mission list, briefing, end-mission,
score/report, and between-mission state. Static analysis has not yet closed the
complete progression state machine, unlock rules, or every persisted campaign
field. Those remain unknown rather than being inferred from screen names.

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
(counting nonzero lobby entries), **or** the counted player count is zero,
**or** — as a fallback while no exact match has been found — this candidate's
StartPos count is the largest seen so far. The first accepted schema name is
copied out and fed to the placement builder.

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
- player count;
- wind minimum/maximum and related wind state;
- gravity;
- tidal strength;
- water and lava behavior;
- surface metal;
- human and computer starting energy/metal;
- kill and time score multipliers;
- briefing, narration, hint, glamour, and glamour-sound names;
- allowed-unit restrictions;
- AI profile name;
- mission unit limit;
- meteor configuration;
- other game-mode flags.

The content and world specifications define numeric parsing and how these
values initialize subsystems.

### Meteor showers

Meteor processing is closed. Authored parameters merge with the
`gamedata/METEOR.TDF` `[Default]` record exactly as specified in document 02;
an empty `MeteorWeapon` is the only disable predicate. Nine scheduler fields
(enabled, active, next-strike, strike-end, next-hit, origin X/Z, target X/Z,
and the resolved weapon) all persist in the save's `Meteor` account.

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

## Placement and battle entry

### Start positions

Special records whose names identify start positions are collected separately
from units and features. Lobby player order and mission schema determine which
start position belongs to each player.

Randomized placement, where enabled, consumes the shared simulation stream in
a fixed axis order. Exact player-to-position shuffling and network agreement
belong to the core determinism contract and remain only partly mapped.

### Unit reconstruction

Scenario and save unit records are resolved by unit name or definition identity,
then allocated through the normal unit pool. Reconstruction restores logical
fields, script state, cargo/attachment links, order queues, and cross-unit
references. Recursive reconstruction is used for linked or carried units.

This path is a loader. It is not the strategic AI planner.

### Feature placement

Terrain-provided and mission-provided feature records converge on the same
feature stamping service. Deterministic load order matters because occupancy,
successor state, geothermal registration, and save identifiers depend on it.

### Placement/start barrier

Multiplayer initialization enters a barrier after content and player state are
prepared. The UI reports that it is waiting for other players. The loop pumps
network state and sleeps for fifty milliseconds between checks. When all
required peers reach the barrier, the executable reports synchronization
complete and allows authoritative ticks.

The normal simulation tick is not driven by this sleep; it is confined to
lobby/placement/barrier behavior.

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

### Evaluation

Every evaluator is a pure poll taking the trigger and a context and mutating
only its own completed flag. Decoded bodies:

- `KillUnitType` runs a countdown: while the subject unit's type matches the
  authored type it decrements the authored count, completing and firing the
  victory notification once the count reaches zero or below.
- Type-gated annihilation/capture checks compare the trigger's type string
  against the subject unit type together with death/capture event flags.
- Boundary conditions (`…PassesX`/`…PassesZ`) compare the signed world
  coordinate carried in the context against the stored threshold, satisfied
  when the absolute difference is below three world units (a ±2 tolerance).
- Timer triggers compare the authoritative tick count against the stored
  seconds×30 deadline.

Completion sets the trigger's completed flag and raises the localized
"Victory Condition" notification (message/sound presentation; the exact
medium is not decomposed).

**The tick site.** The victory and defeat queues are polled from the per-player
phase, in the LOCAL player's slice only, on that slot's own once-per-30-tick
cadence (the same next-due-plus-30 shape the economy settlement deadline uses),
and only when the mission type is 1. Skirmish and multiplayer sessions do not
poll these queues at all.

**Combination and precedence are fixed.** The victory queue is an **AND** across
its members — every victory trigger must report complete. The defeat queue is an
**OR** — any single defeat trigger ends the mission. **Victory is evaluated
first**, so a tick on which both would fire resolves as a victory. Completion
arms a shared end-of-mission countdown at four, which then decrements roughly
once per second before the end-latch word is written; the latch distinguishes
"ending", "won" and "lost" as separate bits, and the lose path clears the win
bit it would otherwise share.

**Owner gating differs per kind and is not an alliance test.** Each trigger
carries an inner match object used by the poll-time scans (the ones that walk
live units: build-type, boundary crossings, all-units-killed, move-to-radius).
The owner byte compared is the unit's player index. Victory conditions gate on
the enemy owner index; `CommanderKilled` gates on the local one;
`UnitTypeKilled` and `AllUnitsKilledOfType` accept any owner. The commander
identity used by `KillEnemyCommander` and `CommanderKilled` comes from the
SIDEDATA commander-name table, not from a unit flag.

**The `DestroyAllUnits` quirk is a contract.** Its body reads a counter whose
only reference in the whole image is that read — nothing ever writes it, so it
holds its initial zero and the condition is **satisfied from the first poll**.
Shipped campaign missions rely on this: at least one uses `DestroyAllUnits` as
an AND-term beside a real `BuildUnitType` condition, where a genuinely
evaluated destroy-all would never let the mission complete. Reproduce the quirk;
do not "fix" it into a live unit scan.

**Timer storage.** Timer triggers store `seconds × 30` as an absolute tick
deadline and compare the authoritative tick count against it.

**Architecture.** Triggers are vtable objects, not type-byte structs. Each
carries six slots: a poll, a unit-died notification, a capture/transfer
notification, a created notification (present but unused by any shipped
condition), and save/load. The poll is the only one the tick site calls; the
notification slots are driven by the corresponding gameplay events. A poll must
therefore not consume tick events as if they were kills — the countdown in
`KillUnitType` advances from the unit-died notification, never from the passage
of time.

Record shapes, the eighteen-entry vtable map, these evaluator bodies, the tick
site, and the combination rules are established. Residual: the timer evaluators'
comparison bodies beyond the seconds×30 conversion remain undecoded, and the
exact presentation sequence between latch write and session teardown is not
decomposed.

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

The front end builds ten-slot player state from these values and validates it
against the selected map/schema. Computer-controlled slots use distinct player
state values that later tick code recognizes.

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

### Established AI-facing data and rooted planner

The executable reads these AI-related definition and mission values:

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Historical executable-analysis detail omitted from this public edition.

### What remains not established

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

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

A diagnostic mode bypasses the DirectPlay call and routes the same packet bytes
through internal loopback helpers. This does not define a second gameplay
protocol; it is a transport/debug alternative.

## Packet framing and dispatch

### Framing

**The first byte of a packet is its type.** Dispatch is a direct indexed call
through a handler table using that byte.

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
| `0x28` | 58 | 7 | **Participant state push** (economy/player record). `+1` u8 flag, `+2` i32 sign-extended i16, `+6` i32 sign-extended i16, `+10` i32 sign-extended i16, `+14` i32 sign-extended i16, `+18` u32, `+22` u32, `+26` u32, `+30` u32, `+34` f32, `+38` f32, `+42` f32, `+46` f32, `+50` f32, `+54` f32. Consumer widens floats to doubles and copies every field into participant state unconditionally — no comparison, threshold, or abort; it can answer with a three-byte control plus optional re-push on nonzero flag. |
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

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

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

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

The recovered synchronization state requires peer agreement on:

- selected map and mission schema;
- player-slot configuration;
- initial placements and options;
- simulation timing and random state.

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

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

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
58-byte participant state push produced by a dedicated scanner thread polling
every 250 ms across locally-owned × remote participant pairs, plus an
immediate echo path that re-emits the packet toward its originator when the
inbound echo flag is set. It is not per-tick and is tied to no tick modulo.
The receiver copies every field into local participant state (shorts and ints
sign-extended back, floats widened back to doubles) with **no comparison, no
threshold, and no abort** — push-and-overwrite, not compare-and-react. The
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
(algorithm unresolved), and the `0x28` participant push (overwrite, not compare).
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

The exact byte layout inside each binary box is not established. Those are the
bulk records: unit instances, script state, the three feature groups, the two
plot maps, and the mapping grid.

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
before load overwrites them. Consumers of `WinLoseTime`/`DisplayTimer` beyond
their save keys remain open.

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
scheduler timing block at global offset `+0x38A37`. The writer copies those 28
bytes verbatim; the loader requires at least 28 bytes and continues only if that
amount is available (larger boxes have trailing bytes ignored).

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

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

The exact tick at which simulation stops, whether final network commands are
drained, and the order of music, overlays, score calculation, save updates,
and lobby teardown remain incomplete.

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
- Scenario unit reconstruction is a load path, not the strategic AI.
- Computer-player strategic construction selection, profile `plan`/`weight`/`limit` handling, and economy-mixed weighted reservoir choice are established; remaining manager tasks and placement helpers remain partially open.
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

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.