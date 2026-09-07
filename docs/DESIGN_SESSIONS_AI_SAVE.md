# Design — Sessions, the computer player and saves

Everything that decides *which battle is running, who is playing it, who wins,
and what survives the process*. This document owns the eight-state session
machine, skirmish setup and campaign mission loading, placement and start
positions, the `InitialMission` interpreter, the eighteen victory and defeat
triggers, the commander-death chain and the end latch, the post-battle result,
the retail `HAPIBANK` save container and the battle it restores, the skirmish
computer player, and the displayless runner that drives all of it without a
window.

Packages: `internal/mission`, `internal/triggers`, `internal/ai`,
`internal/save`, `internal/headless`, `cmd/nanolathe-headless`, and the parts
of `internal/session` that are not the tick — the state machine, the two
battle-entry constructors, the results, and the save projection and restore.

Related documents: [ARCHITECTURE.md](ARCHITECTURE.md) for the package map and
the citation routing that makes a bare `[Cn]` in `internal/mission`,
`internal/triggers` or `internal/ai` resolve to §3 below and a `[PLAN 11 Cn]`
resolve to §3.3; [DESIGN_RUNTIME_DETERMINISM](DESIGN_RUNTIME_DETERMINISM.md)
for the clock, the two random streams, the twelve phases and the publication
boundary, which this document takes as given;
[DESIGN_ECONOMY_CONSTRUCTION](DESIGN_ECONOMY_CONSTRUCTION.md) for the
per-player settlement loop that carries both the end-condition block and the
computer player; [INVARIANTS.md](INVARIANTS.md) and
[SPEC_CONFLICTS.md](SPEC_CONFLICTS.md).

## 1. Purpose and boundary

These packages answer four questions:

* **What is the session doing right now?** One state word with eight values and
  a fixed transition graph. Authoritative ticks run in exactly one of them, and
  save and load ride the same graph rather than a second one
  `[08 "Session states"]`.
* **How does a battle start?** Two entry paths — a skirmish setup record and a
  campaign mission file — that converge on one composition: seed both streams,
  build the world, stamp players, place features, create units, cross the
  placement barrier, grant starting resources `[08 "Placement and battle
  entry"]` `[08 R-ENTRY-01 §2]` `[08 R-ENTRY-01 §5]` `[08 R-ENTRY-01 §6]`.
* **When is it over, and who won?** One end-condition block inside the local
  player's settlement due, running either the authored trigger queues (campaign)
  or the two elimination predicates (skirmish), and one shared countdown into
  one latch word `[08 R-TRIG-01 §1]` `[08 R-TRIG-01 §6]` `[08 R-SKIR-01 §3]`.
* **What persists?** The retail bank: a `HAPIBANK` container of named accounts
  carrying either a campaign continuation or a whole battle
  `[08 "Save-file organization"]` `[08 R-SAVE-02 §11]`.

Plus one agent: the computer player, the only thing besides a human that issues
orders. It is a rooted per-player manager with nine task records, not a
behaviour tree and not a second simulation `[08 "Strategy manager and its task
graph"]` `[08 R-AI-01 §1]`.

The boundary is drawn at six places:

* **The tick belongs to `internal/session`'s step order**, which
  DESIGN_RUNTIME_DETERMINISM owns. Nothing here registers a phase. The
  computer player and the end-condition block both hang off the *economy's*
  per-player walk inside phase 5, supplied as callbacks so neither
  `internal/economy` nor `internal/ai` imports the other
  `[05 "Authoritative settlement order"]` `[01 R-CORE-01 §4.4.1]`.
* **Orders belong to `internal/orders`.** The computer player and the
  `InitialMission` interpreter both submit through the ordinary command
  resolver and the ordinary queue API; neither has a privileged mutation path
  into unit, economy or movement state `[04 §3.3]` `[04 R-ORD-02 §1]`.
* **Placement legality belongs to `internal/world`.** The mission spawner, the
  commander stamp and the computer player's placement helpers are all callers
  of the one placement predicate and the one height probe
  `[08 R-ENTRY-02 §1]` `[08 R-AI-03 §5]`.
* **Screens belong to `internal/gui`, `internal/hud` and `cmd/nanolathe`.**
  The skirmish setup screen, the briefing, the save and load lists and the
  result panel's chrome are DESIGN_INTERFACE_HUD_INPUT
  `[07 R-FE-01 §5]` `[07 R-FE-01 §10]`. What lives here is the *record* those
  screens edit, the metadata a save exposes to them, and the post-battle state
  order they play.
* **Byte layout of a save is layout, not behaviour.** `internal/save` owns
  exact offsets because the container is a file format; what those bytes *mean*
  when they re-enter a battle is `internal/session`'s restore path
  `[08 "Location and representation"]` `[08 "Load process"]`.
* **Networking is out of scope** and is not partially implemented here. The
  local path still constructs the death, damage, creation and impact packets
  and forwards them whole to the central handlers, and that is all
  `[08 R-OOS-01 §1]` `[08 R-OOS-01 §3]`.

The most dangerous mistake in this area is reading a slot's identity off the
*setup record*. A skirmish setup record is a pre-battle mirror; battle entry
copies side and colour out of it into the **player record**, and a load
restores only the five rule words and the map name back into the setup record.
Every post-entry reader — commander identity, alliance, the score rows, the
save's `Player%i` account — reads the player record `[08 R-SKIR-01 §2]`
`[08 "Player records"]`.

## 2. Packages and key types

### 2.1 `internal/session` — states, entry, results, saves

Only the non-tick half is described here.

**The state machine.** `State` is `0..7` with one dispatch method,
`Session.Advance`, that runs exactly one state operation per call. The
transition graph is a fixed matrix: `0→2`, `1→2`, `2→{3,4,5}`, `3→5`, `4→5`,
`5→6`, `6→7` on normal completion, `6→2` on abort and `7→2` on the front-end
return `[08 "Session states"]`. Single-player takes `2→5`; state 3, the network
pre-load, is present and unreachable. Completion of the loading state installs
state 6 whose *first run happens on the next dispatch*, never inline — the
session exposes that pending flag so the deferral is testable rather than
implied. `AdmissionMaskForState` is the three-bit packet admission rule: one
bit admits the loading state, one the battle state, one everything else
`[08 "Admission masks"]`.

State names for `0..4` are this package's own descriptive labels. The trace is
a bounded negative: the state word is one dword written by a setter with an
eight-way switch, and neither the setter nor any of the five callbacks
references a string, so retail has no name to clone `[08 R-SESS-01 §8]`.
Nothing in the simulation reads the label.

**The skirmish setup record.** `SkirmishConfig` is the lobby's record: a map
name, a validated player count, ten `SkirmishPlayer` rows (nickname,
controller, side, colour, ally group, starting metal and energy), the six
scalar **rule words** — difficulty, start location, commander death, mapping,
line of sight and line-of-sight type — the per-player unit limit, and the two
explicit stream seeds. `ApplyDefaults` installs the missing-value defaults once
and then locks them, so a player who deliberately selects a zero is not
overwritten by the next call `[08 "Skirmish configuration"]` `[02 §3]`.

Three vocabularies are closed rather than open. `CommanderDeathMode` narrows
the rule word to exactly three behaviours — the owner's units continue, the
owner is eliminated, or the local commander respawns — and an out-of-range
value fails closed to the elimination behaviour instead of inventing a fourth
`[08 R-SKIR-01 §3]`. The controller predicates keep *observer* distinct from
*computer*: an observer is a nonzero controller, and treating "nonzero" as
"computer" hands an observer slot a manager, units and a share of the result.
Retail's start-up clamp — a missing value becomes 250, below 20 becomes 20,
above 500 becomes 500 — is applied to a profile value, not a lobby gadget, and
applied there only: it lives in `internal/settings`' loader. Skirmish battle
entry copies the configured word over the session's unit-limit word verbatim
(`unitLimitOrDefault`, which rewrites only this build's zero sentinel), so only
a campaign keeps a map's authored count, and a value a save restore carried
into the configured word reaches the next battle unclamped `[08 R-SKIR-01 §6]`
`[08 R-SESS-01 §9]` `[05 "Fixed unit slots"]`.

**Battle entry.** Two production constructors, one composition. Both seed the
simulation and CRT streams before any setup draw `[01 R-CORE-02]`
`[08 R-ENTRY-01 §2]`, compile or accept one immutable catalog, load terrain,
apply the schema, size the sliced unit pool from the session's unit limit, and
then run the per-kind stamp:

* *Skirmish.* Per eligible slot in slot order: copy side and colour into the
  player record, apply the storage bonus, look up the slot's start position,
  and allocate the commander at that point through the common allocator. The
  stored start-position number is the authored label minus one, and a miss is
  **fatal** — retail raises `Error: Could not find start position number %i on
  the map!` through its modal-fatal channel; this build carries the same text
  as a battle-entry error and returns to the screen the start was launched
  from `[08 R-ENTRY-01 §5]`.
* *Campaign.* The mission spawner creates the authored units at their placement
  records' coordinates with the height probe of `[08 R-ENTRY-02 §1]`, runs
  `InitialMission` once after every unit exists, positions the camera and
  resets the trigger queues `[08 R-ENTRY-01 §6]`.

The computer players are constructed *before* any battle unit is allocated, and
each AI-owning slot consumes its established block of draws from the shared
simulation stream in ascending player order — that ordering is the reason
manager construction is a battle-entry step and not a lazy one
`[08 R-ENTRY-01 §3]` `[08 R-AI-01 §9]` [I4]. The entry tail then primes the
per-player phase once, grants no second helping of resources, and snapshots the
metal-spot vector `[08 R-ENTRY-01 §8]`.

**The commander-death chain.** The kill-record path compares the dead unit's
definition name against the commander name on the *owner's side record*, read
from the player record. On a match it clears the owner's storage bonus for
every rule value, and for rules 1 and 2 sweeps the owner's remaining units: a
controlled owner's units take 30000 self-damage through the ordinary damage
path, an inactive or non-controlled owner's units are destroyed silently, both
with the same death cause. Rule 0 skips the sweep entirely. The sweep arms
nothing by itself — it drives the live count to zero, and the ordinary defeat
predicate notices at the next settlement due `[08 R-SKIR-01 §3]`
`[08 R-SKIR-01 §5]`.

**Elimination, the end latch and the result.** A slot is eliminated when its
live-unit count is zero *and* its units-ever-created count is not — the exact
negation of the settlement gate's status pair, so no separate elimination flag
exists `[08 R-SKIR-01 §3]` `[05 R-ECO-01 §12]`. `EndLatch` is the one signed
16-bit countdown plus the latch bits: ending, the two win bits, and the lose
bit that clears the first win bit. `Result` is the frozen outcome — kind,
reason, winner and loser teams, per-player score rows and their column maxima —
and `PostBattleController` plays the retail post-battle state order over it.
`BankProgress` holds the 25-entry campaign mark array that the score helper
writes once per battle, at the transition into the ending state, before the
result handler is installed `[08 R-CAMP-01 §7]` `[08 R-CAMP-01 §8]`.

**Save projection and restore.** `RetailSaveInputs` and `RetailProjection` are
the detached, writer-facing view: values and copied byte slices only, no
session pointer, catalog object or presentation cache crosses the boundary
`[08 R-ENTRY-02 §3]`. `RetailBattleStage` is the mirror-image read side — a
fully detached staging result validated before anything live is touched — and
`RestoreRetailBattleCore` commits it. `RetailLoadResult` names the route the
`Summary` account selected and carries exactly one of a campaign continuation
or a staged battle `[08 R-SAVE-02 §11]`.

**The trigger adapter.** The session supplies the trigger evaluator its
world-facing callbacks: the stamped footprint anchor a boundary condition
reads, the projection that turns authored map pixels into world coordinates on
the first radius poll, the commander-identity predicate, and the unpositioned
`Victory Condition` cue, which is admitted as a committed frame event so it
crosses the publication boundary like every other cue `[08 R-TRIG-01 §3]`
`[08 R-TRIG-01 §5]` `[08 R-TRIG-01 §8]` [I6].

### 2.2 `internal/mission` — campaigns, missions, placement, `InitialMission`

`Campaign` and `Stub` are the discovery products: a `camps/*.tdf` file and the
contiguous run of `MISSION0..N` sections that stops at the first gap.
`MissionList` is an allocation tag, not an authored section, and requiring it
loses every campaign `[08 "Campaign discovery"]` `[08 "Progression"]`.

`Type` is the load discriminant — campaign through a campaign wrapper, or a
direct `maps/<name>.ota` — and is deliberately *not* the session-kind word.
The two happen to share numerals; the session kind comes from a save's own
`Summary` gametype or from the constructor, never from this discriminant
`[08 "Mission type dispatch"]` `[08 R-SESS-01 §7]`.

`Mission` is the loaded object: the parsed OTA, the terrain key, the selected
`Schema`, the three placement arrays, the retained wind bounds, the
`UseOnlyUnits` route, the two trigger queues, and the campaign provenance a
continuation needs `[08 "Mission object"]`. The three placement records keep
retail's field identity — 36 bytes for a unit, 12 for a special, 136 for a
feature — as named Go fields rather than packed structs [I13]
`[08 "Mission placement record"]`.

`Schema` selection reads `[GlobalHeader]`, then tries a candidate list chosen
by the *mode*, not by the file's contents: campaign tries the three difficulty
literals in a difficulty-dependent permutation, skirmish and direct-OTA loads
try `Network 1` through `Network 4` against the schema's `StartPos` count.
Inferring the mode from which family the file happens to carry lets a skirmish
pick up `Easy`, with the wrong placements and no error `[08 "Schema choice"]`
`[02 R-MAP-01 §3]`.

`MissionGlobals` is the census of every `[GlobalHeader]` key with its accessor
type, default, clamp, consumer, and a class: authoritative, presentation, or
inert. It is the record of which map-global keys reach the simulation at all,
and it is where a key with no reader is written down as having none.

The `InitialMission` interpreter runs once, on the loading path, after every
mission unit exists — for a campaign mission and for a between-missions
restore. It registers with no tick dispatcher; the ordinary queue pump consumes
what it queued from the next tick `[04 §3.6]`. Name resolution scans the
mission's sparse created array in placement order, testing the placement
identifier case-insensitively and then the unit name, and skips the null gaps a
failed allocation leaves.

`DecodeTriggers` probes `[GlobalHeader]` for the eighteen condition keys in
fixed vocabulary order and splits them into the victory and defeat queues; a
duplicate spelling collapses to its last value and produces at most one record
`[08 R-TRIG-01 §2]`.

### 2.3 `internal/triggers` — the eighteen conditions

`Kind` enumerates eleven victory and seven defeat conditions in the builder's
probe order `[08 "Victory trigger types"]` `[08 "Defeat trigger types"]`.
`Trigger` carries the kind, the authored type token, up to three integer
arguments and its own completed flag; `RecordSize` states the allocation
identity per kind without reproducing the packed layout [I13]
`[08 "Trigger object"]`.

`ParseCondition` handles the two authored argument shapes, `<name>,<int>` and
`<name>,<int>,<int>,<int>`, and accepts the literal `ANYTYPE` wherever a unit
type is expected `[08 "Victory and defeat triggers"]`.

`PollContext` is the session-supplied surface: the tick, the unit world, the
mission-armed flag, and the four callbacks named in §2.1. `Poll` is a pure
predicate that mutates only its own completed flag; `Notify` drives the three
notification slots — removal, capture, creation — and reads the pre-transfer
owner only at the capture slot `[08 R-TRIG-01 §4]` `[08 R-TRIG-01 §7]`.
`Evaluate` owns the queue combination and nothing else: it re-injects the
defaults at poll time, evaluates victory as an AND stopping at the first false
and, only when victory is false, defeat as an OR stopping at the first true.
The caller owns the kind gate, the cadence and the latch `[08 R-TRIG-01 §6]`.

`RetailTriggerAccount` is the save form — the per-trigger integer box the bank
carries — with a matching restore that writes the completed flags back into an
already-built queue rather than rebuilding the queue from the save
`[08 R-TRIG-01 §9]`.

### 2.4 `internal/ai` — the skirmish computer player

`Profile` is the compiled `ai/<name>.txt`: a plan gate, a per-type weight table
clamped to `0..100` with default 100, and a per-type limit table defaulting to
unlimited. The file's directive stream is retained in authored order, because
the full grammar — the multi-argument `plan`, the exact-versus-category name
matcher over the catalog's sort key, and the two independent lock vectors that
decide profile-versus-per-definition precedence — needs the definition catalog
that a bare parse does not have. `ApplyUnitDefinitions` replays the stream
against a catalog and then folds each definition's authored weight fragment
into the types the file did not lock `[08 R-AI-01 §12]` `[08 R-AI-01 §18]`
`[08 R-AI-01 §20]`.

`Manager` is the per-player planner: the player index, the embedded `Strategic`
state, the ten task deadlines, the profile, the nine group vectors, the
classification countdown, the two attack-wave engagement latches, the rally
task's best/probe/drift triple, and the session bindings it must not do without
— the build submitter, the order binding, the alliance predicate, the
visibility predicate and the shot-time gate. Every binding fails *closed*: an
unbound manager keeps an empty rally vector rather than granting omniscient
target knowledge, and infers no hostility from ownership or side identity
`[08 R-AI-01 §9]` `[08 R-AI-01 §19]`.

`TaskKind` is the ten-slot task vector and its order is load-bearing. Slot 0
holds no task object at all — the dispatcher's null test skips it — while slot
5 holds a real object of the null task class whose body returns immediately but
which *owns group record 5*, the armed-buildings group the attack wave reads
for its first-choice gather point. Collapsing the two misnumbers every group
record `[08 R-P0-04 §2]`. The remaining eight are the resource and builder
queue, the two attack waves, their two regroups, construction and positioning,
explore and gather, and the random-walk rally
`[08 "Strategy manager and its task graph"]` `[08 R-AI-01 §2]`
`[08 R-AI-01 §3]` `[08 R-AI-01 §4]` `[08 R-AI-01 §5]` `[08 R-AI-01 §6]`
`[08 R-AI-01 §7]`.

`Strategic` is the per-player strategic state: the three-axis strategic centre
in 16.16, the placement radius in world units, the battle-entry metal-spot
snapshot, the two constructor-drawn lattice regions, the last refresh tick, the
per-type completed counts, the build-capable count, and three per-type
coefficient families — the initialization-only single byte, the recomputed
single byte, and the three-byte class triple. The initialization-only vector is
never overwritten by the refresh, which is the distinction that makes the two
families two `[08 "Strategic state construction and refresh"]`
`[08 R-P0-05 §5]` `[08 R-P0-05 §10]`. It also carries the session words the
class routine reads — the per-player unit limit and the map's maximum wind —
each with an explicit *bound* flag, so an unwired fixture cannot read a zero as
a real cap and fire an addend everywhere `[08 R-AI-01 §13]` `[08 R-P0-05 §9]`.

`ClassVector`, `ScoreInputs`, `ComputeMix` and `ComputeScore` are the candidate
score as a pure function, testable before it is wired to anything.
`MetalSpot`, `PlacementRegion` and `PlacementResult` are the placement root's
inputs and its typed answer, including the reason code a rejection carries
`[08 R-AI-03 §1]` `[08 R-AI-03 §2]` `[08 R-AI-03 §5]`.

The classifier sweep is in `groups.go`. Its input status bits are the
building-class and armed bits the allocator writes once from the definition;
its output bits are named only by their masks, because no design-level name is
established `[08 "Classifier eligibility, destinations, and order"]`
`[08 "Eco toggle and group-vector population"]` `[08 R-P0-04 §3]`.

### 2.5 `internal/save` — the retail bank

Byte layout is the contract here, and it is the one place in this document
where exact offsets are the subject [I13].

`Bank` is the `HAPIBANK` container: a 34-byte header — the case-sensitive
magic, the bank tag's string-pool offset, the absolute pool file offset, the
first account offset, the version, a compression flag and nine reserved zero
bytes — followed by accounts enumerated from the first account offset until the
cursor reaches the string pool, each advancing by its own stored span. The tag
compared after the pool loads is `Total Annihilation 3.0`, case-insensitively
`[08 "Location and representation"]`.

Accounts begin with a 32-byte header, empty accounts are not emitted, and a
body may be compressed as a unit. A wrong magic, version or tag closes the file
and returns nothing; a decompression failure emits its diagnostic verbatim and
**parsing continues** — retail's bank/account decode is permissive by
contract, not by oversight: a malformed or missing optional record is
defaulted and the scan moves on to the next account `[08 "Account inventory"]`
`[08 "Load process"]`. That is a parsing-boundary contract, and it is
distinct from the application boundary this build adds on top of it. Parsing
a bank into a `BattleImage` (`DecodeBattleImage`) follows the same
optional-record leniency; applying a decoded image to a live session is a
separate, later step, and that step is transactional in this build —
`RestoreRetailBattleCore` validates and stages every pass before touching any
live state, and the caller swaps the session in only on success (§2.1, "Save
projection and restore"). The parsing leniency does not carry over into that
commit: a staging failure discards the whole attempt rather than leaving a
partially applied session.

`Summary` is the account the load screen reads without constructing anything:
the description and game identity, the gametype, the between-missions flag, the
campaign and mission identity, difficulty, side, player count and the five rule
words. `PlayerSlot` is one `Player%i` account — the slot's scalars, its side
and logo bytes, and, as that account's last item, its eleven-byte alliance row
with the forced self-alliance `[08 "Summary"]` `[08 "Player records"]`.

**Which limit a save persists, and which battle a load can affect.** The
`maxunits` item is the **configured** unit-limit word — this build's copy of
`[Preferences] UnitLimit`, held by `cmd/nanolathe`'s setup record — and never
the battle's own session limit, which in a campaign comes from the mission OTA
instead `[08 R-SESS-01 §9]` `[08 R-SKIR-01 §6]`. The session package holds only
the per-battle copy, so `RetailBattleSummary` takes the configured word as a
parameter rather than reading a setting; the continuation writer takes it on
`ContinuationSaveMetadata`. On the way back in, `applyRestoredUnitLimit` stores
the saved word, unclamped, into that same configured record — and only when the
account carried the item, which is why `save.Summary` reports presence
(`HasMaxUnits`) separately from value. The battle being restored is unaffected:
its pool was already sized from the configured word as it stood before the
restore (`RetailLoadDeps.UnitLimit`), so a restored limit reaches the *next*
battle entry, not this one `[08 R-ENTRY-01 §6]`.

`bulk.go` holds the established raw box sizes — the two accepted unit-box
lengths, the order box and its five subtype lengths, the script snapshot's base
and per-piece sizes, the feature type-name buffer, and the units-account
version word — and validates rather than guesses `[08 "Unit and script
records"]` `[08 "Feature records"]` `[08 R-SAVE-02 §6]` `[08 R-SAVE-02 §7]`
`[08 R-SAVE-02 §8]` `[08 R-SAVE-02 §9]` `[08 R-SAVE-02 §10]`. The per-family
record maps live in `[08 R-SAVE-UNIT-01]`, `[08 R-SAVE-WEAPON-01]`,
`[08 R-SAVE-ORDER-01]` and `[08 R-SAVE-FEATURE-01]`.

**The 3D feature record's five values are named.** `internal/features` writes
and reads them as named instance fields rather than as an opaque blob: the
position triple at `0x08..0x13` as three 16.16 world coordinates, then bank at
`0x14..0x15`, heading at `0x16..0x17` and pitch at `0x18..0x19`, with the damage
accumulator at `0x06..0x07` shared with the other two families
`[08 R-SAVE-FEATURE-01]`. The reader hands the position and orientation to the
ordinary stamp as its two optional pointers, so both are stored **verbatim** —
no footprint centre, no terrain re-sample — and writes the accumulator
afterwards, because the stamp zeroes it. Velocity is not serialized and is not
re-derived: a wreck saved mid-descent reloads at its saved Y with zero velocity
and stays **suspended** there. That is retail's behavior, not a gap to patch —
do not latch the sinking velocity back on from the height/sea-level relation.
`RestoreAt` remains the single owner of the family switch, so the session's
restore loop is unchanged.

This build's 3D damage path is a countdown from the definition's `damage`
rather than retail's wrap-around 16-bit accumulator, so the saved word
round-trips losslessly on its own instance field and the live damage path does
not read it; the mismatch is a `TODO(question)` at the field rather than an
invented conversion `[05 R-FEAT-01 §8]`.

`compression.go` decodes the single-chunk `SQSH` framing the pools and account
bodies use; the archive LZ77 variant is what the retail writer selects, and the
zlib method is accepted as a format-level variant `[fmt hpi]`
`[08 R-ENTRY-02 §3]`.

`BattleImage` is the detached, wire-validated read of a whole battle account
set — no pointer into a session, catalog, terrain or bank-owned slice — and
`RetailProjection` is its write-side twin. The writer emits a
between-missions projection as `Summary` alone, and a live-battle projection as
the fixed account order `[08 "File naming and write policy"]`
`[08 R-SAVE-02 §12]`.

### 2.6 `internal/headless` and `cmd/nanolathe-headless`

`FreshBattleRequest` is the immutable battle-entry value both the graphical and
the displayless adapters build, so a battle composed with a window and one
composed without take the same constructor path
`[08 R-ENTRY-01 §2]`. `Request` adds the host-side knobs: the install root, the
scenario selector, difficulty, the two seeds, the tick limit and the report
path.

`Run` and `RunSession` advance a composed session through the ordinary bounded
`Step` loop and then build the report. The observer is a read of committed
state: it draws no random numbers and writes no authoritative field.

`Report` is the stable diagnostic surface — scenario kind and identity, both
seeds, both final stream states, both draw counts, the catalog and manifest
hashes, the final tick, the status, the session state and result, the
authoritative state hash, and ten `PlayerReport` rows carrying live units,
units created, whether a manager was bound, orders submitted, kills, losses,
the order-intent census, the first attack-family order a player's units
received, and periodic task-group samples. The group sample cadence is 300
ticks so exactly one sample exists per attack-wave invocation
`[08 R-AI-01 §4]`; its index is the retail group-record number, 0 being the
ungrouped sentinel `[08 R-P0-04 §2]`.

The command exposes `-root`, `-map`, `-mission`, `-difficulty`, `-seed`,
`-ticks`, `-report` and two pprof paths that never reach the session. Exit
codes are the contract: 0 for a session that reached the post-battle state, 2
for the tick limit, 1 for anything else. `-difficulty` is validated against the
`0..2` battle vocabulary at parse time with the standard diagnostic shape, and
is the same word the campaign constructor threads through and the skirmish
config takes after `ApplyDefaults` has run `[08 R-SKIR-01 §9]`
`[08 R-AI-01 §12]`.

## 3. Contracts

Three numbered sets meet here, and each keeps the numbering its comments use.
Which set a bare `Cn` selects is decided by the package the comment lives in:

| Comment site | Set |
|---|---|
| `internal/session`, `internal/save` — states, setup, entry, results, saves | §3.1 |
| `internal/mission`, `internal/triggers` | §3.2 |
| `internal/ai`, cited as `[PLAN 11 Cn]` | §3.3 |

A bare `Cn` in `internal/session` that is about the clock, the twelve phases,
publication or the pools belongs to DESIGN_RUNTIME_DETERMINISM's set instead;
those two areas share the file, not the numbering.

### 3.1 Sessions, setup, entry, results and saves — C1…C23

**C1 — eight states, one graph.** All eight states exist with the transition
matrix `0→2`, `1→2`, `2→{3,4,5}`, `3→5`, `4→5`, `5→6`, `6→7`, `6→2`, `7→2`.
Single-player takes `2→5` directly; state 3 is present and unreachable
`[08 "Session states"]`.

**C2 — loading completion is deferred.** Completion of the loading state
installs the battle state, and its first run happens on the **next** dispatch,
never inline. `Advance` runs exactly one state operation per call, so a state
that changes the state word does not also run the new state's operation
`[08 "Session states"]`.

**C3 — the save preflight.** Only gametype 1 (campaign) and 2
(skirmish/multiplayer) are accepted; every other value is rejected. The route
is selected strictly on the between-missions flag being one — every other value
is a battle restoration. Preflight inspects no bulk account, seeds no stream
and constructs no session `[08 "Session states"]`
`[08 "Save-file organization"]` `[08 R-SAVE-02 §11]`.

**C4 — the single-player router.** The router enters the local loading path
directly; the network pre-load state stays in the table with its handler
present. Packet admission is the three-bit mask, one bit per admitted state
class `[08 "Session states"]` `[08 "Admission masks"]`.

**C5 — the end-condition seam sits inside phase 5.** The twelve-phase order and
the executor tail belong to DESIGN_RUNTIME_DETERMINISM. What this document owns
inside them is one seam: the economy's per-player settlement block calls the
end-condition hook on every due slot, and the hook returns immediately unless
the slot is the local player's. It therefore runs exactly once per settlement
due, and a load resumes it on the saved deadline phase
`[08 R-TRIG-01 §6]` `[05 "Authoritative settlement order"]`.

**C6 — one publication per completed sub-tick.** DESIGN_RUNTIME_DETERMINISM's
contract; restated here only because the result, the end countdown and the
score rows are published state and are written on a due, so nothing in this
area needs a per-sub-tick presentation refresh `[03 §2.4]` [I6].

**C7 — pause is the single-player branch.** DESIGN_RUNTIME_DETERMINISM's
contract. The in-battle options path is its writer while the options window is
modal `[01 §4.3]` `[07 §11]`.

**C8 — skirmish configuration defaults.** The player count is validated `2..10`
(retail's validation is a compiled no-op that stores the raw value either way);
per-slot metal and energy default to 1000, the ally group to the unassigned
sentinel 5, the controller to human, the colour to the slot index, the side to
the slot's low bit, and nicknames to a 17-byte buffer. The six scalar rule
words default to difficulty 1, location 1, commander death 1, mapping 1, line
of sight 1 and line-of-sight type 1, and are installed **once**, so a
deliberately chosen zero survives `[08 "Skirmish configuration"]`
`[08 "Lobby behavior"]` `[02 §3]`.

**C9 — battle entry order.** Seed both streams; build the world; stamp players;
place features; reconstruct units; cross the placement barrier; then grant
starting resources **directly to live stock, outside the ledger**. On the
skirmish path the stamp helper runs per eligible slot in slot order, copying
side and colour into the player record, applying the storage bonus, and
allocating the commander at the slot's start position through the common
allocator with its two simulation draws. A missing start position is fatal with
the verbatim diagnostic; there is no jitter fallback on this path
`[08 "Placement and battle entry"]` `[08 R-ENTRY-01 §5]` `[08 R-ENTRY-02 §1]`.

**C10 — the two load routes.** A between-missions save yields campaign
continuation metadata — campaign path, campaign and mission name, mission
index, difficulty, side and the 25-byte mark array, reset to all-unplayed when
the source is not exactly 25 bytes. Every other save yields a staged battle:
detached staging first, then core restoration, then the battle-entry tail —
the graphical user interface, the per-player phase primed once on the restored
world at the restored tick, no second resource grant, then the metal-spot
lists. The tail follows the restoration dispatcher, not the other way round
`[08 R-SAVE-02 §11]` `[08 R-SAVE-02 §11-A]` `[08 R-ENTRY-01 §8]`.

**C11 — the container header.** 34 bytes: magic `HAPIBANK` compared
case-sensitively, the tag's pool offset, the absolute pool offset, the first
account offset, version exactly 1, a compression flag byte, nine reserved
zeroes. The tag compared after the pool loads is `Total Annihilation 3.0`,
case-insensitively. Accounts are enumerated from the first account offset until
the cursor reaches the pool offset, advancing by each account's stored span
`[08 "Location and representation"]`.

**C12 — accounts.** A 32-byte account header; empty accounts are not emitted; a
body may be compressed as a unit. A wrong magic, version or tag closes the file
and returns nothing. A decompression failure emits its diagnostic verbatim and
parsing **continues** `[08 "Location and representation"]`
`[08 "Account inventory"]`.

**C13 — header offsets are bounded.** Retail does not range-check them. This
build does, and rejects an out-of-range bank as a format error. It is the
sanctioned exception to I11 and is listed in §5.

**C14 — write policy.** The bank writer normalizes the extension after the last
dot to `.SAV` and truncate-opens directly; post-open errors are not unwound.
The reader's partial-load behaviour is preserved: a missing account is created
empty and each subsystem's defaults govern `[08 "File naming and write
policy"]` `[08 "Load process"]`.

**C15 — the 28-byte scheduler box and the deadlines.** The scheduler image and
the per-player settlement deadlines round-trip as absolute ticks and are never
re-seeded on load. Neither random stream is saved, and no Nanolathe-authored
continuation format exists `[08 "Scheduler and random state in saves"]`
`[05 "Saving economy, construction, and features"]`.

**C16 — the alliance row.** Eleven bytes, emitted inside the slot's own
`Player%i` account rather than as a bank-level box, with the self-alliance
forced to one `[08 "Player records"]`.

**C17 — one battle-entry wind initializer.** Mission and skirmish load retain
the map's wind bounds and perform no draw; the single initializer at battle
entry consumes the established chain. Its arithmetic and its phase are
DESIGN_RUNTIME_DETERMINISM's `[08 "Wind initialization"]` `[01 §7.3]`.

**C18 — the elimination predicate is derived, not stored.** There is no
elimination flag. A slot is eliminated when its 16-bit live count is zero and
its 32-bit ever-created count is not; that is the exact negation of the
settlement gate's status pair. Both counters are incremented by the allocators
and the live count is decremented where the alive bit clears
`[08 R-SKIR-01 §3]` `[05 R-ECO-01 §12]`.

**C19 — the commander-death chain.** Identity is the dead definition's name
against the owner's side commander name, read from the **player record**. On a
match the storage-bonus flag clears for every rule value; rule 0 stops there.
Rules 1 and 2 sweep the owner's other units — self-damage of 30000 through the
ordinary damage path for a controlled owner, a silent destroy for an inactive
or non-controlled one, both with the same death cause. The sweep runs at the
death, from the kill-record path, not at the next due `[08 R-SKIR-01 §3]`
`[08 R-SKIR-01 §5]`.

**C20 — the two skirmish predicates, in order, on one due.** Defeat first: the
local player's live-unit count is zero. Only when defeat is false does the
victory sweep run. Defeat therefore wins a tie, and a wipe that leaves nobody
standing is a local defeat, not a draw. The victory sweep walks slots 0–9,
skips the local slot, skips any slot allied in the local player's first
alliance row, skips any slot with zero live units, and declares victory only if
nothing survives those skips; under the deathmatch rule it returns false
immediately, because the local player's own elimination is what arms the
respawn `[08 R-TRIG-01 §6]` `[08 R-SKIR-01 §3]`.

**C21 — one shared countdown, one latch.** A true due finds the signed
countdown negative and sets it to 4; each later true due decrements it; the due
whose decrement takes it below zero is terminal — the sixth consecutive true
due, 150 ticks after the first. A false due neither resets nor advances it. At
most one predicate steps it per due. On the terminal due the rule word alone
selects the arm: the deathmatch rule respawns the local commander, any other
value writes the latch — the ending bit always, the two win bits on the won
path, the lose bit with the first win bit cleared on the lost path. A rule-2
session can never reach the latch write: an exhausted respawn search simply
leaves the countdown below zero and the next true due re-arms it
`[08 R-TRIG-01 §6]` `[08 R-SKIR-01 §3]`.

**C22 — the score and the campaign mark.** The end-of-battle score is
`trunc(float(ticks / 60) × timemul) + trunc(kills × killmul)`, each product
truncated separately before the sum, then clamped at zero. The tick divisor is
60, taken as an unsigned integer divide, not a floating one; the two
multipliers are the mission's authored globals, defaulting to zero when absent
`[08 R-CAMP-01 §7]` `[08 R-CAMP-01 §11]`. The single win/loss mark is written
once per battle at the transition into the ending state, before the result
handler is installed, into the 25-slot campaign array; the registry holds only
the difficulty and the two flag mirrors, and progress persists solely through a
save bank's `Summary` `[08 R-CAMP-01 §8]`.

**C23 — the post-battle sequence is a screen, not an overlay.** The controller
plays retail's state order over a frozen result, emitting the darkening fade,
the glamour art, the click-to-continue prompt, the statistic reveal and the
gamma restore, then routes: a skirmish exposes only the return to the main
menu, a campaign additionally exposes the continuation when a successor mission
exists. Its rendering is DESIGN_INTERFACE_HUD_INPUT's `[08 R-CAMP-01 §6]`
`[08 R-CAMP-01 §8]` `[07 R-FE-01 §10]`.

Three things about that sequence are easy to get backwards, and are pinned by
tests in `cmd/nanolathe/postbattle_integration_test.go`. The glamour fade runs
**from black up into the picture's own palette**, not from the picture's
palette down to the game's — the image is blitted once and only the palette
moves. Nothing restores the display palette when the glamour screen ends; the
`ENDMSN` population's own `outcome1`/`outcome0` background install is what does
it. And from the end of the darkening fade onwards the sequence runs at
640×480, like every other front-end screen, whatever display mode the battle
was at.

### 3.2 Campaign, missions and triggers — C1…C18

**C1 — campaign discovery.** Walk `MISSION0..N` and stop at the first gap.
`MissionList` is an allocation tag, not an authored section, and is not
required. Each section's mission name is read through the language-prefixed
string accessor with the built-in fallback `[08 "Campaign discovery"]`.

**C2 — mission type dispatch.** A campaign mission requires both the global
header and the mission-file key, with four diagnostics reproduced verbatim.
Direct-OTA loads read `maps/<name>.ota` and, on a read or parse miss, take
exactly one translated-name retry through the reverse translation table before
failing `[08 "Mission type dispatch"]` `[08 R-CAMP-01 §11]`.

**C3 — schema selection.** Seek the global header — its absence emits `Very bad
news! No MSG!` verbatim — then read each `Schema %i`'s type and match it
case-insensitively against a literal candidate list chosen by the mode.
Campaign tries the three difficulty literals in a difficulty-dependent
permutation and fails on any other difficulty value. Skirmish and direct-OTA
try `Network 1` through `Network 4`, each candidate counting its `StartPos`
records and accepted when that count equals the player count, or the counted
player count is zero, or — as a fallback while no exact match has been found —
this candidate's count is the largest seen. Failure emits `No suitable schema
type...` `[08 "Schema choice"]`.

**C4 — selection precedes instantiation.** The schema name is resolved before
any placement record is built, and is what the placement builder is fed
`[08 "Schema choice"]`.

**C5 — mission load retains the wind bounds and draws nothing.** The single
battle-entry initializer consumes them (§3.1 C17) `[08 "Wind initialization"]`.

**C6 — placement record identity.** Units carry name pointers into a tail heap,
coordinates shifted left sixteen, and an angle converted from authored degrees
by a truncate-toward-zero of degrees × 65536 / 360 — bitwise identical to the
floating form for stock angles, differing for negative or above-360 values.
Specials are the twelve-byte record. Features carry a 128-byte name buffer with
coordinates defaulting to −1 and cleared when negative. Go keeps named fields
[I13] `[08 "Mission placement record"]`.

**C7 — one flag byte is read.** The creation reader consumes only the immunity
high bit. The AI-ignore, AI-priority, build-priority, initial-group and
mission-critical flags are parsed and unread: retain them, act on none
`[08 "Mission placement record"]` `[08 "What remains not established"]`.

**C8 — the use-only route is live.** The `UseOnlyUnits` key is read at OTA load
and routed through the resource resolver into the campaign use-only area, where
it restricts the catalog the battle runs on `[08 "Mission placement record"]`
`[08 R-ENTRY-01 §2]`.

**C9 — `InitialMission` runs once.** After every mission unit exists, on the
loading path, for a campaign mission and for a between-missions restore. It
registers with no tick dispatcher `[04 §3.6]`
`[08 "Unit creation and InitialMission timing"]`.

**C10 — the verb table.** A 23-entry dispatch over the first character,
tokenized by scanning for commas and copying each span into a fixed frame,
splitting on every comma. Coordinates parse as floats scaled by 65,536 and
truncated toward zero; times scale by 30 with the same truncation `[04 §3.6]`.

**C11 — the uppercase-`W` quirk is a contract.** An uppercase-led `W…` token
enters the build block, whose second character selects the weapon form, so an
uppercase-led token can never be a plain wait, and wait-for-attack must be
written lowercase `[04 §3.6]`.

**C12 — the postlude.** When at least one order was queued, the unit's
class/state bit clears; and unless the script contained a numeric-form attack,
patrol, defend or stop, a final make-selectable order queues with zero
auxiliary arguments `[04 §3.6]`.

**C13 — malformed input is silent.** Unknown letters, digits and punctuation
are ignored with scanning resuming past the comma; a failed type lookup for
attack or build produces no queue; a failed unit lookup for guard produces no
queue and for wait-for-attack falls back to self; move, patrol, unload, wait
and flag-bit tokens do not test their conversion counts and queue zero-valued
orders when parsing fails. Only the numeric attack form tests for two floats
and only wait-for-attack tests for one name. A comma-free run past the frame
length is clamped rather than overflowed — the one divergence this area
introduces, and the one the research itself recommends `[04 §3.6]`.

**C14 — trigger argument shapes.** Two formats, `<name>,<int>` and
`<name>,<int>,<int>,<int>`, with `ANYTYPE` accepted wherever a type is expected
and recognized by the boundary conditions `[08 "Victory and defeat triggers"]`.

**C15 — all eighteen kinds exist.** Eleven victory and seven defeat conditions,
in the builder's probe order `[08 "Victory trigger types"]`
`[08 "Defeat trigger types"]`.

**C16 — defaults are injected at poll time, not at load.** With no authored
victory condition the evaluator injects destroy-all-units; with no defeat
condition, all-units-killed. Injection happens inside the kind-1 predicate on
each poll, so the mission object's queues are not rewritten
`[08 "Default triggers"]` `[08 R-TRIG-01 §6]`.

**C17 — evaluators are pure polls.** A poll mutates only its own completed
flag. The counted kill condition decrements a countdown and completes at zero
or below; the boundary conditions compare the signed world coordinate read from
the unit's stamped footprint anchor against the threshold and are satisfied
when the absolute difference is **below three** world units; timer conditions
compare the tick count against seconds × 30 `[08 "Evaluation"]`
`[08 R-TRIG-01 §3]` `[08 R-TRIG-01 §4]`.

**C18 — the `StartPos` counter advances only when it is taken.** The placement
builder keeps one counter, local to the schema read and reset to zero each
time. For each special whose type begins with the eight characters `StartPos`,
compared case-insensitively, the single byte immediately after the prefix
decides: if it is a decimal digit the number is the digit run from that byte
onward and **the counter is untouched**; otherwise the counter is incremented
first and the record takes the incremented value. The stored number is that
value minus one when positive and the value itself otherwise, so `StartPos0`
and `StartPos1` both name stored position zero, and slot *i* under identity
placement takes `StartPos<i+1>`. The digit test is a character-class test on
that one byte, not an integer parse whose zero result falls back to the counter
`[08 R-TRIG-01 §12]` `[08 R-TRIG-01 §9]` `[08 R-ENTRY-01 §5]` `[fmt ota]`.

Only a campaign session polls the authored queues. Skirmish and direct-OTA
sessions build the trigger records — the builder runs for every session kind —
and never read them; their end conditions are the elimination predicates of
§3.1 C20 `[08 R-TRIG-01 §1]`.

### 3.3 The computer player — C1…C12

Cited from `internal/ai` as `[PLAN 11 Cn]`.

**C1 — the two entry gates.** Per-tick entry iterates the ten players; for the
outer controller values and a player index below the sentinel it reaches the
manager through the player's manager pointer, and the **inner** gate is the
computer-policy controller value — due virtual tasks execute only there. Both
gates are required `[08 "Established AI-facing data and rooted planner"]`
`[08 "Dispatch gates and order sinks"]`.

**C2 — the strategic state and its 30-tick refresh.** A fixed-size per-player
object, named fields here [I13], refreshed every 30 ticks: the per-type
completed counts, the build-capable count and the strategic centre are rebuilt
each window. Two per-type coefficient families exist. The single byte written
once at construction starts at zero, gains 40 when a per-definition category
flag is clear and 20 when that definition's build-option list is non-empty, and
is **never** written by the recomputation routine. The three-byte triple is
zeroed at construction, computed once unconditionally there, and thereafter
recomputed only when the outer gate's draw with bound 30 yields zero at a
refresh `[08 "Strategic state construction and refresh"]` `[08 R-P0-05 §5]`
`[08 R-P0-05 §6]` [I4].

**C3 — task deadlines and the classification sweep.** Construction and
positioning reschedule at tick + 90; resource and queue management at + 30;
attack waves at + 300 with the two distance thresholds and the member bounds
that drive the merge; regroups pair with the waves at + 150; explore and gather
at + 30 plus a draw below 900; the random-walk rally at + 30 plus a draw below
150. Slot order is: resource, wave A, regroup A, construction, null, wave B,
regroup B, explore, rally — with slot 0 empty and slot 5 the real null-task
object that owns group record 5. The classification sweep runs every 30
countdown expiries and, on every eligible unit whether grouped or not, also
rewrites the standing orders: fire-at-will unconditionally, and the manoeuvre
move order when the definition can capture, otherwise roam
`[08 R-P0-04 §2]` `[08 R-AI-01 §10]` `[08 R-AI-02 §2]` `[08 "Wave merge"]`.

**C4 — the profile grammar is gated.** `plan` is a gate: weight and limit lines
before it do not apply, and an unrecognized plan name leaves the gate clear so
every directive after it is inert. `weight <type> <factor>` multiplies the
stored weight, clamps it to `0..100` and marks the entry; `limit <type> <n>`
stores the limit, unlimited by default, and marks the entry. Defaults are
weight 100 and zero completed counts. The weight factor is read by the C
runtime's float parser, with that tokenizer's comment rule
`[08 "Established AI-facing data and rooted planner"]` `[08 R-AI-01 §12]`
`[08 R-AI-01 §20]`. The two per-definition passes then run the whole authored
fragment with kinds unfiltered, folding into the types the file did not lock
`[08 R-AI-01 §18]`.

**C5 — the candidate gates.** Applied before scoring: an energy stock below 50,
a metal stock below 25, the per-definition gate bit, and the profile limit —
which rejects type index zero and any index at or above the catalog count
before it reads the limit vector. A third hard gate rejects a downloadable
candidate under the campaign session mode. The **post-selection** filter
compares the *selected* definition's authored side string against the builder's
own, byte for byte and case-sensitively, and discards the whole selection on a
mismatch: no re-draw, no runner-up, and the draw is still spent
`[08 R-AI-01 §8]` `[08 R-AI-02 §2]` `[08 R-P0-05 §3]`
`[05 R-SHARE-01 §10]`.

**C6 — the score.** Computed with truncation exactly as written, with the
resource inputs in their established single-precision representation:

```
energyRaw = trunc(max(0, (min(cap, 1000) - curEnergy) * 0.125))
          + (netEnergy < 1 ? 20 : 0)
          + (prodEnergy < 50 ? 100 : prodEnergy < 200 ? 10 : 0)
metalRaw  = trunc(max(0, (min(cap, 500) - curMetal) * 0.25))
          + (netMetal < 1 ? 20 : 0)
          + (prodMetal < 3 ? 100 : prodMetal < 5 ? 20 : 0)
metalMix  = clamp(metalRaw, 0, 100)
energyMix = clamp(energyRaw - metalMix, 0, 100)
otherMix  = max(0, 100 - metalMix - energyMix)
score     = trunc((class0*otherMix + class1*metalMix + class2*energyMix) * weight / 10000)
```

The signed net-energy query reads the live wind scalar and the immutable map
tidal strength, so a gated recompute observes the current wind rather than a
copy `[08 R-P0-05 §1]` `[08 R-P0-05 §4]` `[05 R-PROD-01 §1]`
`[08 "Established AI-facing data and rooted planner"]` [I2].

**C7 — one draw per selection.** Positive scores enter a cumulative weighted
reservoir and the choice is a single draw on the global simulation stream
against the running total. One draw per selection, not one per candidate and
not one per builder `[08 R-P0-05 §4]` [I4].

**C8 — placement.** The search origin steps toward the strategic centre in
16.16: the builder-to-centre distance is a square root over the loaded
fixed-point deltas, truncated toward zero; the radius is a count of world
units grown by 160 per failure, capped at the larger map dimension and reset to
zero on success; the origin is the centre when the distance is zero or at least
the scaled radius, and otherwise the builder plus the delta scaled by radius
over distance through a 64-bit multiply and divide. A candidate whose
extracts-metal word compares equal to floating zero goes to the scatter helper
with **no draw**; otherwise one draw with bound 255 selects the exhaustive
metal-spot helper when the mission's uniform surface metal is strictly less
than the draw, and the scatter helper otherwise — strictly less, so equality
chooses scatter. The exhaustive helper scans the battle-entry metal-spot vector
within a circle of four times the radius, filters and sorts by distance,
validates in sorted order and stops when the next distance exceeds the best by
a slack of 160; a failure does **not** fall through to scatter. The scatter
helper attempts up to 30 trials around the origin, drawing up to four values
per trial, selecting its lattice region by the definition's water-depth sign,
and comparing the footprint's per-cell metal sum against surface metal ×
footprint X × footprint Z × 2. Success requires the yard and occupancy
validator to report placeable `[08 R-AI-03 §2]` `[08 R-AI-03 §3]`
`[08 R-AI-03 §4]` `[08 R-AI-03 §4-A]` `[08 R-AI-03 §5]` `[08 R-AI-03 §7.4]`
`[08 "Placement root and search helpers"]`.

**C9 — the draw-bound census.** The bounds this package uses are 30 for the
refresh gate, the cumulative total for the choice, 255 for the extractor
branch, 65536 for positioning, and 5, 150 and 900 for the task rescheduling.
Any other bound here is a defect `[08 R-P0-05 §4]` `[08 R-AI-01 §6]`
`[08 R-AI-01 §7]` [I4].

**C10 — the inert inputs stay inert.** The per-definition AI weight fragment is
read; the per-definition AI limit has no semantic reader and is wired to
nothing. The mission placement flags of §3.2 C7 are parsed and inert
`[08 "What remains not established"]`.

**C11 — the planner runs inside the settlement walk.** The session passes the
manager's tick as the economy's before-deadline callback, so it runs after the
per-tick helpers and before the deadline compare, and a slot the gate skips
invokes neither the planner nor a deadline advance. That the dispatch is
specifically one of that step's auxiliary helpers is a supported inference and
is marked as such at the call site
`[05 "Authoritative settlement order"]` `[08 "Established AI-facing data and
rooted planner"]`.

**C12 — ordinary paths only.** Builds go through the construction queue, orders
through the command resolver and the order descriptor registry. There is no
privileged mutation path into unit or economy state, and no stockpile or
transfer shortcut `[04 R-ORD-02 §1]` `[08 R-AI-01 §7]`.

### 3.4 Not implemented

* **Multiplayer everything.** The lobby, the transport, packet framing, pacing,
  lockstep, integrity checks, peer loss, host migration and multiplayer saves
  are out of scope and named in ARCHITECTURE's exclusion table. What remains
  here is the local construction of the death, damage, creation and impact
  packets, forwarded whole to the central handlers `[08 R-OOS-01 §1]`
  `[08 R-OOS-01 §2]` `[08 R-OOS-01 §3]`.
* **The two live-player counters have no call site.** Both are called
  exclusively from the multiplayer branch of the end-condition block, so a
  single-player engine needs neither. The site is absent by contract, not
  deferred `[08 R-SESS-01 §1]`.
* **Session kind 3 is never built.** The pool's player-slice comparator
  switches on the session kind, and only kind 3 consults the peer-identity sort
  key; campaign and skirmish both order by slot `[08 R-SESS-01 §7]`.
* **The `Radar Image` account is not produced.** It is a presentation preview
  the load dispatcher never restores; no box is written and the load screen
  shows no preview `[08 "Account inventory"]`.
* **The computer player builds no transports and orders no repair, reclaim,
  guard or capture directly.** No transport producer exists: the manager passes
  only four command codes and never a target unit with the load code, and the
  class routine's zeroing makes every non-extracting carrier score at or below
  zero and be skipped without a draw. Repair and reclaim happen only inside the
  patrol handlers that the construction repositioning and explore orders
  resolve to when the builder authors reclaim capability `[08 R-AI-04 §1]`
  `[08 R-AI-04 §2]` `[08 R-AI-04 §5]`.
* **There is no air wave, air rally or landing-pad logic.** Air policy is the
  classifier's flyer row into the explore record and one class-routine addend;
  naval policy is three water-depth tests. Nothing established by
  `[08 R-AI-04 §3]` or `[08 R-AI-04 §4]` is missing, and nothing beyond them is
  invented.
* **The extraction rate is sampled at the placement call sites, not by the unit
  creator.** A unit created by a path that bypasses a placement yields none.
  The contract and its home are DESIGN_ECONOMY_CONSTRUCTION's
  `[05 R-PROD-01 §6]`.

## 4. Retail behaviour that is not a bug

* **The computer player re-attacks every 300 ticks while its wave latch is set,
  even as it loses members.** A trickle of single units after a failed wave is
  retail; the latch is cleared only by the gather arm at three or fewer members
  `[08 R-AI-01 §4]`.
* **A computer player that picks a candidate from the wrong side simply builds
  nothing that tick.** The side filter runs *after* selection and discards the
  whole selection on a mismatch — no re-draw and no runner-up — and the
  reservoir draw is still spent, so the draw census is unchanged
  `[08 R-AI-01 §8]` [I4].
* **The class triple usually does not change at a refresh.** Recomputation is
  gated behind a one-in-thirty draw, so the coefficients a manager plans with
  are typically several windows old. That is the cadence, not a stale cache
  `[08 R-P0-05 §6]`.
* **Defeat is evaluated before victory, and there is no draw.** A kind-2 end
  that wipes everyone is a local defeat. No mutual-destruction rule exists in
  any research section `[08 R-TRIG-01 §6]`.
* **The end takes 150 ticks after the condition first holds.** The shared
  countdown arms to 4 and needs six consecutive true dues; a single false due
  in between neither resets nor advances it, so a condition that flickers can
  take far longer `[08 R-TRIG-01 §6]`.
* **An eliminated slot keeps advancing its settlement deadline and simply never
  settles.** The deadline advance deliberately precedes the elimination test
  `[05 R-ECO-01 §12]` `[05 "Authoritative settlement order"]`.
* **A deathmatch session never ends by elimination.** The victory sweep returns
  false immediately under that rule, and an exhausted respawn search leaves the
  countdown below zero for the next due to re-arm. There is no
  post-exhaustion transition to find `[08 R-SKIR-01 §3]`.
* **A campaign battle's commander death does nothing on its own.** The OTA load
  writes the continue rule, and that rule skips the owner sweep entirely
  `[08 R-SKIR-01 §3]` `[08 R-SKIR-01 §4]`.
* **`StartPos0` and `StartPos1` name the same stored position.** The stored
  number is the authored value minus one when positive, so the two collide, and
  so does the first non-numeric label `[08 R-TRIG-01 §12]`.
* **An unrecognized `plan` name silently disables the rest of the profile
  file.** The gate is cleared first and set only on a match
  `[08 R-AI-01 §12]`.
* **The player-count validation stores whatever it is given.** Retail's
  validation compiles to a no-op with both branches storing the raw value; the
  `2..10` range is the lobby's, not the parser's `[08 "Skirmish
  configuration"]`.
* **A decompression failure inside a save does not abort retail's load.** The
  diagnostic is emitted verbatim and parsing continues; missing accounts are
  created empty and each subsystem's defaults govern. Retail's bank parsing
  and account application share one non-transactional pass by contract
  `[08 "Location and representation"]` `[08 "Load process"]`. This build keeps
  only the parsing half of that leniency (§2.5, "the retail bank") — applying
  a parsed image to a live session is a separate, transactional commit
  boundary (§2.1, "Save projection and restore"), not the same pass.
* **Random state is not saved.** A restored battle continues from a stream that
  was seeded at the original battle entry and has advanced since; nothing
  re-seeds it on load `[08 "Scheduler and random state in saves"]`
  `[01 R-PLAT-01 §7]`.

## 5. Divergences

* **SC25 — the stock save/load screen authors fewer gadgets than the section
  lists.** The reference install's load-game layout does not author four of the
  gadgets the screen code sets by name, so those writes are inert rather than
  wrong. The four are treated as optional and the writes are kept: the
  section's list is a superset of stock content, not a contract stock content
  satisfies. Open — a census of the other language and patch archives would
  settle whether any stock variant authors them
  `[08 R-SAVE-02 §1]`.
* **Save header offsets are bounds-checked** (§3.1 C13). Retail does not
  range-check them. This is the sanctioned exception to I11 for this area, and
  mirrors the archive hardening DESIGN_CONTENT_VFS carries. The observable
  effect differs only for a malformed file retail would follow into memory it
  does not own.
* **A fatal `StartPos` miss is an error, not a process exit.** Retail raises
  the verbatim diagnostic through a modal-fatal helper and exits. This build
  carries the same text as a battle-entry error, shows it in the retail message
  window and returns to the screen the start was launched from — the shape it
  uses for every fatal that retail answers with a modal
  `[08 R-ENTRY-01 §5]`.
* **The tokenizer frame is clamped rather than overflowed** (§3.2 C13). A
  comma-free run past the frame length overruns retail's fixed buffer; the
  research recommends the clamp, and no stock mission reaches the length.
* **State names for 0–4 are ours.** No name exists in the image to clone. The
  labels are descriptive and unread by the simulation `[08 R-SESS-01 §8]`.
* **SC18's save half is superseded.** That entry describes an earlier
  Nanolathe-specific save codec and an unsupported in-battle restoration. The
  codec is gone and the restoration is implemented against the retail accounts
  (§3.1 C10); what remains open in SC18 is the allocator's zero-fill byte
  count, which belongs to DESIGN_RUNTIME_DETERMINISM.

No other entry in [SPEC_CONFLICTS.md](SPEC_CONFLICTS.md) originates in these
packages. SC21's building-versus-factory identity reaches the computer player's
placement path through the definition selector it shares with construction, and
is owned by DESIGN_ECONOMY_CONSTRUCTION `[08 R-AI-03 §7.4]`.

## 6. Research map

| Behaviour | Owning research |
|---|---|
| The eight states, their callbacks and the transition graph | `[08 "Session states"]`, `[08 R-SESS-01 §8]` |
| Packet admission per state | `[08 "Admission masks"]` |
| Which mode word each entry path sets, and what reads it | `[08 "Game-mode selection"]`, `[08 R-SESS-01 §7]` |
| The skirmish setup record and every option's consumer chain | `[08 R-SKIR-01 §1]`, `[08 "Skirmish configuration"]`, `[08 "Lobby behavior"]`, `[02 §3]` |
| What the setup record becomes at battle entry; row-to-player conversion; save persistence | `[08 R-SKIR-01 §2]`, `[08 "Player records"]` |
| Commander death: trigger site, owner sweep, counters, defeat and victory detection | `[08 R-SKIR-01 §3]` |
| Line of sight and mapping rules; the storage bonus | `[08 R-SKIR-01 §4]`, `[08 R-SKIR-01 §5]` |
| The unit limit: where it is read, clamped and copied | `[08 R-SKIR-01 §6]`, `[05 "Fixed unit slots"]` |
| AI difficulty as a lobby word | `[08 R-SKIR-01 §9]` |
| Who runs battle entry; seeding and the session words | `[08 R-ENTRY-01 §1]`, `[08 R-ENTRY-01 §2]` |
| The world rebuild, allocation by allocation, including the manager block | `[08 R-ENTRY-01 §3]` |
| Start positions and commanders per kind; the fatal miss | `[08 R-ENTRY-01 §5]` |
| The campaign path: spawner, `InitialMission`, camera, trigger reset | `[08 R-ENTRY-01 §6]` |
| The entry tail: interface, phase priming, second grant, teardown, ready | `[08 R-ENTRY-01 §8]` |
| The pre-tick state and the first pump | `[08 R-ENTRY-01 §9]`, `[08 R-ENTRY-01 §10]` |
| The spawner's height probe, exactly | `[08 R-ENTRY-02 §1]`, `[08 R-ENTRY-02 §2]` |
| The bank writer's compression policy and typed-item primitives | `[08 R-ENTRY-02 §3]` |
| Campaign catalog grammar, enumeration and the mission list | `[08 R-CAMP-01 §1]`, `[08 "Campaign discovery"]`, `[08 "Progression"]` |
| Briefing screen; new-game panel | `[08 R-CAMP-01 §2]`, `[08 R-CAMP-01 §3]` |
| The results sequence and the score helper | `[08 R-CAMP-01 §6]`, `[08 R-CAMP-01 §7]`, `[08 R-CAMP-01 §11]` |
| The end-of-mission screen and the progress write | `[08 R-CAMP-01 §8]` |
| Elimination announcements and the kill-lead line | `[08 R-CAMP-01 §9]`, `[08 R-CAMP-01 §10]` |
| Mission type dispatch and the translated-name recovery | `[08 "Mission type dispatch"]`, `[08 R-CAMP-01 §11]` |
| Schema selection, exactly | `[08 "Schema choice"]`, `[02 R-MAP-01 §3]` |
| Placement records, the immunity bit, the use-only route | `[08 "Mission placement record"]`, `[08 "Mission object"]` |
| The `InitialMission` verb table, the uppercase quirk, the postlude, the silent-malformed rules | `[04 §3.6]`, `[08 "Unit creation and InitialMission timing"]` |
| Wind bounds retained at load; the single initializer | `[08 "Wind initialization"]`, `[01 §7.3]` |
| Which session kinds poll the authored triggers | `[08 R-TRIG-01 §1]` |
| Trigger record shape, vtable slots and construction | `[08 R-TRIG-01 §2]`, `[08 "Trigger object"]` |
| The owner and unit predicates every condition shares | `[08 R-TRIG-01 §3]` |
| Every condition, exactly; the boundary tolerance | `[08 R-TRIG-01 §4]`, `[08 "Evaluation"]` |
| Move-to-radius geometry and its first-poll projection | `[08 R-TRIG-01 §5]` |
| The tick site: cadence, order, countdown and latch | `[08 R-TRIG-01 §6]` |
| Notification sites: removal, capture, creation | `[08 R-TRIG-01 §7]` |
| The `Victory Condition` cue and the missing-alias silence | `[08 R-TRIG-01 §8]` |
| The `[units]`, `[features]` and `[specials]` readers; save and load | `[08 R-TRIG-01 §9]`, `[08 R-TRIG-01 §10]`, `[08 R-TRIG-01 §11]` |
| The `StartPos` running counter, exactly | `[08 R-TRIG-01 §12]`, `[fmt ota]` |
| Default trigger injection | `[08 "Default triggers"]`, `[08 "Victory and defeat triggers"]`, `[08 "Victory trigger types"]`, `[08 "Defeat trigger types"]` |
| Manager entry, the two verified constants | `[08 R-AI-01 §1]`, `[08 "Established AI-facing data and rooted planner"]` |
| The six task bodies | `[08 R-AI-01 §2]`, `[08 R-AI-01 §3]`, `[08 R-AI-01 §4]`, `[08 R-AI-01 §5]`, `[08 R-AI-01 §6]`, `[08 R-AI-01 §7]` |
| Candidate selection and the side filter | `[08 R-AI-01 §8]` |
| Shared helpers: centroid, nearest hostile, group broadcast | `[08 R-AI-01 §9]` |
| The classifier also writes standing orders | `[08 R-AI-01 §10]`, `[08 "Classifier eligibility, destinations, and order"]` |
| Reaction to being attacked | `[08 R-AI-01 §11]` |
| Difficulty vocabulary, profile grammar, the economy effect | `[08 R-AI-01 §12]`, `[05 R-ECO-01 §3]` |
| The half-capacity comparison | `[08 R-AI-01 §13]` |
| The manager diagnostic string | `[08 R-AI-01 §14]` |
| The weapon-maintenance sweep | `[08 R-AI-01 §15]` |
| Strategic refresh corrections, and the target-registry twin | `[08 R-AI-01 §16]`, `[06 §3.1]`, `[04 R-SPEC-01 §8]` |
| Open items the task-body pass did not close | `[08 R-AI-01 §17]` |
| The two per-definition passes run the whole fragment | `[08 R-AI-01 §18]` |
| Rally admission for buildings, and the broadcast's forwarded word | `[08 R-AI-01 §19]` |
| The `weight` factor's parser and the tokenizer's comment rule | `[08 R-AI-01 §20]` |
| The rally task's constructor state | `[08 R-AI-02 §1]` |
| Profile-limit edges, strategic accessors, standing-order bits, the target pick's swap-remove | `[08 R-AI-02 §2]` |
| The metal-spot vector: builder, record, scan, consumer | `[08 R-AI-03 §1]` |
| The placement root's origin step | `[08 R-AI-03 §2]` |
| The exhaustive metal-spot helper | `[08 R-AI-03 §3]` |
| The statistical scatter helper and its surface-metal key | `[08 R-AI-03 §4]`, `[08 R-AI-03 §4-A]` |
| What the placement root returns, and the radius | `[08 R-AI-03 §5]`, `[08 "Placement root and search helpers"]` |
| Placement unknowns, with deciders | `[08 R-AI-03 §6]` |
| The blocker's row test, the validator's mode, the dead submitted height, the building/mobile selector | `[08 R-AI-03 §7]`, `[08 R-AI-03 §7.1]`, `[08 R-AI-03 §7.2]`, `[08 R-AI-03 §7.3]`, `[08 R-AI-03 §7.4]` |
| The task-class run is complete: seven classes and nothing else | `[08 R-AI-04 §1]` |
| Transport, naval, air, repair and reclaim policy | `[08 R-AI-04 §2]`, `[08 R-AI-04 §3]`, `[08 R-AI-04 §4]`, `[08 R-AI-04 §5]`, `[08 R-AI-04 §6]` |
| No per-tick group producer; the two vector families | `[08 R-P0-04 §1]` |
| Manager task slots and group-record identity | `[08 R-P0-04 §2]` |
| Located producers, transfer order, the writer census | `[08 R-P0-04 §3]`, `[08 R-P0-04 §4]`, `[08 "Eco toggle and group-vector population"]`, `[08 "Wave merge"]` |
| Current heuristic versus retail; implementation guidance | `[08 R-P0-04 §5]`, `[08 R-P0-04 §6]` |
| Score inputs, update order, the strategic score fields | `[08 R-P0-05 §1]`, `[08 R-P0-05 §2]` |
| Hard gates and economy pressure | `[08 R-P0-05 §3]`, `[08 "Dispatch gates and order sinks"]` |
| Candidate score and cumulative weighted selection | `[08 R-P0-05 §4]` |
| Class-vector compilation and refresh; cadence and same-tick ordering | `[08 R-P0-05 §5]`, `[08 R-P0-05 §6]`, `[08 "Strategic state construction and refresh"]` |
| Extractor, profile and request gates | `[08 R-P0-05 §7]` |
| Placement-geometry residual; the three class-routine inputs; the initialization-only vector's single reader | `[08 R-P0-05 §8]`, `[08 R-P0-05 §9]`, `[08 R-P0-05 §10]` |
| The two live-player counters and the box write primitives | `[08 R-SESS-01 §1]`, `[08 R-SESS-01 §2]` |
| The single-player temporary-sight producer | `[08 R-SESS-01 §3]`, `[03 R-COMP-02 §2]`, `[01 R-PLAT-02 §5]` |
| The feature writer's animating-cell rule; the session-kind accessor; the code-3 order constructor | `[08 R-SESS-01 §4]`, `[08 R-SESS-01 §5]`, `[08 R-SESS-01 §6]` |
| The player record's peer-identity word as the kind-3 sort key | `[08 R-SESS-01 §7]` |
| The restored unit-limit word: where it lands, unclamped, and what reads it | `[08 R-SESS-01 §9]` |
| Save and load screens; every load diagnostic verbatim | `[08 R-SAVE-02 §1]`, `[08 R-SAVE-02 §2]` |
| The summary panel and in-game options greying | `[08 R-SAVE-02 §3]`, `[08 R-SAVE-02 §4]`, `[08 R-SAVE-02 §5]` |
| The `Units` account word by word; the resource account; the mover record; the script box | `[08 R-SAVE-02 §6]`, `[08 R-SAVE-02 §7]`, `[08 R-SAVE-02 §8]`, `[08 R-SAVE-02 §9]` |
| The order subtype families | `[08 R-SAVE-02 §10]`, `[08 R-SAVE-ORDER-01]` |
| What is not saved, the fix-up order, and the load-path tail | `[08 R-SAVE-02 §11]`, `[08 R-SAVE-02 §11-A]` |
| Camera, metal, per-player features and mapping | `[08 R-SAVE-02 §12]` |
| Corrections, the account inventory and the two later closures | `[08 R-SAVE-02 §13]`, `[08 R-SAVE-02 §14]`, `[08 R-SAVE-02 §15]` |
| Per-family record maps for units, weapons and features | `[08 R-SAVE-UNIT-01]`, `[08 R-SAVE-WEAPON-01]`, `[08 R-SAVE-FEATURE-01]` |
| Container layout, account enumeration, naming and write policy | `[08 "Location and representation"]`, `[08 "Account inventory"]`, `[08 "File naming and write policy"]`, `[08 "Save-file organization"]`, `[08 "Summary"]` |
| Load ordering and the non-transactional failure policy | `[08 "Load process"]` |
| Unit, script and feature record bodies | `[08 "Unit and script records"]`, `[08 "Feature records"]` |
| The scheduler box; random state is not saved | `[08 "Scheduler and random state in saves"]` |
| Economy, construction and feature state in saves | `[05 "Saving economy, construction, and features"]` |
| The single-player boundary: packets the local path still constructs | `[08 R-OOS-01 §1]`, `[08 R-OOS-01 §2]`, `[08 R-OOS-01 §3]`, `[08 R-OOS-01 §4]`, `[08 R-OOS-01 §5]` |
| The per-player settlement loop the end block and the planner ride | `[05 "Authoritative settlement order"]`, `[01 R-CORE-01 §4.4.1]` |
| The settlement status pair as the elimination test | `[05 R-ECO-01 §12]` |
| The computer player's gate is the profile limit, never the definition limit | `[05 R-SHARE-01 §10]` |
| Battle-entry stream seeding | `[01 R-CORE-02]`, `[01 §7.1]`, `[01 §7.2]` |
| Which thread's CRT block each consumer reads | `[01 R-PLAT-01 §7]` |
| The front-end controller and the single-player transition table | `[07 R-FE-01 §1]`, `[07 R-FE-01 §5]`, `[07 R-FE-01 §10]` |
| The order resolver the planner and the interpreter both submit through | `[04 §3.3]`, `[04 R-ORD-02 §1]` |

## 7. Not implemented and open

Markers in these packages, one line each.

* `TODO(T23)` in `internal/ai`'s class-vector recomputation — the narrowing to
  single precision at every helper invocation boundary is established and
  reproduced; the control word in force *between* those points is not, and the
  research classifies it as a platform residual with the default rounding mode
  assumed. It can change a result only if retail's word differs from the
  default `[08 "What remains not established"]`.
* `TODO(T23)` in `internal/ai`'s candidate score — the same residual, at the
  named energy and metal expressions, which are evaluated in single precision
  and narrowed at the truncations §3.3 C6 shows
  `[08 "Established AI-facing data and rooted planner"]` [I2].
* `TODO(T23)` in `internal/session`'s effect-strip flame spawner — a span under
  five world units makes the segment life zero and retail's per-axis divide
  faults on it, so there is no behaviour to clone; the placeholder is the
  family's one-tick minimum. This is presentation, not session: the strip
  families belong to DESIGN_PRESENTATION_CLIENT, and the file is only where
  they are published from.
* Two `TODO(question)` markers in `internal/session`'s world-click order
  producer — whether the producer receives a goal point alongside a target
  handle, and whether the side panel's non-world-click issues run the same
  duplicate test. Both need a trace of the interface's call into the producer
  and belong to DESIGN_INTERFACE_HUD_INPUT `[07 §9]`.

No `TODO(question)`, `TODO(T23)` or `TODO(T25)` marker remains in
`internal/mission`, `internal/triggers`, `internal/save`, `internal/headless`
or `cmd/nanolathe-headless`.

Open questions carried by the contracts above rather than by a marker, each
with the observation that would settle it:

* **The classification helper's semantic name.** The bit positions, the
  zero-versus-nonzero tests and the recovered field identities are established;
  only the design-level name is not. Naming, no behaviour
  `[08 R-P0-05 §5]` `[08 "What remains not established"]`.
* **Which cells the two placement helpers enumerate.** The metric and the
  limits are closed; the loop geometry is the residual. A static trace of the
  two helpers' loops settles it `[08 R-AI-03 §6]` `[08 R-P0-05 §8]`.
* **Two order gate-mask bit names the construction task tests**, and **which
  global option bit selects the rally task's two probe-validation forms**. The
  package consumes the selected predicate rather than inventing a mapping
  `[08 R-AI-01 §17]`.
* **Whether the attack wave's engagement latch is serialized.** It is kept as
  manager state and not written to a save box `[08 R-AI-01 §17]`.
* **The initial-group low-nibble reader**, and **what a unit-created
  notification means** — every shipped condition ignores that slot, so the slot
  is kept and read by nobody `[08 R-TRIG-01 §11]`.
* **Whether any stock language or patch archive authors the four save-screen
  gadgets SC25 names.** A census of those archives' layouts settles it
  `[08 R-SAVE-02 §1]`.
* **Which retail spans inside a save body remain opaque.** Only those the
  corrections section does not name; every account it names is implemented
  `[08 R-SAVE-02 §13]`.
