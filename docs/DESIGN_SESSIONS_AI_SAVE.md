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
* **Networking is not implemented here.** Multiplayer belongs to
  DESIGN_MULTIPLAYER, which does not reproduce retail's transport. The local path still constructs the death, damage,
  creation and impact packets and forwards them whole to the central
  handlers, and that is all this document owns of it
  `[08 R-OOS-01 §1]` `[08 R-OOS-01 §3]`.

The most dangerous mistake in this area is reading a slot's identity off the
*setup record*. A skirmish setup record is a pre-battle mirror; battle entry
copies side and colour out of it into the **player record**, and a load
restores only the five rule words and the map name back into the setup record.
Every post-entry reader — commander identity, alliance, the score rows, the
save's `Player%i` account — reads the player record `[08 R-SKIR-01 §2]`
`[08 "Player records"]`.

Computer registration uses each compiled `SideDef.Name`, including a mod's
replacement of base side ordinals 0/1. Stock ARM/CORE definitions keep retail's
`Arm`/`Core` spelling. These labels follow content in every gameplay mode,
including faction composition (DESIGN_MODS_MUTATORS §5.8); explicit player
names remain independent of that default.

## 2. Packages and key types

The save/load directory is `savegame` beneath the resolved installation root,
including when host discovery supplies that root. An explicit `Options.Root`
continues to select the save location for programmatic callers and tests.
Both dialogs use the same directory; the process launch directory does not
replace a discovered installation [08 R-SAVE-02 §1]. A nonempty
`Options.SaveDir` (`--save-dir`) takes precedence over both and names the exact
directory, with no `savegame` suffix appended. Empty preserves the existing
lookup. The shell retains the option across battle entry and restart. Explicit
`--load-save` paths continue to be opened as supplied.

The frontend save/load dialog compiles side definitions from its mounted VFS
without building a battle catalog. It owns a prepared side display-name slice
until close; the summary painter indexes that copy by the saved side ordinal
[08 R-SAVE-02 §3]. Load preparation retains the dialog, selection and buffers
through refusal; the invalid-save message covers the same load screen. Only a
successful route commit releases that dialog [08 R-SAVE-02 §2].
The dialog mirrors the widget's initial row-zero selection after list fill,
so the highlighted first save is immediately loadable and its summary and
name match the selected file [07 R-FE-02 §5] [08 R-SAVE-02 §1].
A battle routes message-box input ahead of its options controls, including
the empty-list refusal that opens without a save/load panel.
Successful saves close the save dialog and release its buffers. Empty names
and write errors retain it for correction; the battle options window underneath
stays paused until dismissed. This is the requested host UI policy in §5.

### 2.1 `internal/session` — states, entry, results, saves

Only the non-tick half is described here.

`MissionEntryOptions` carries both the selected side and an explicit presence
bit through fresh campaign composition. Side zero is a selection. Player side
mirrors and economy side words are installed before entry priming, and entering
local preload preserves that primed economy state. Constructors without an
explicit selection retain authored-side resolution. Full battle restores also
restore the shell campaign identity, side and difficulty before continuation.
Fresh mission entry sets the two live player colours before publication,
independently of side selection or authored-side resolution
[08 R-CAMP-01 §3] [08 R-SKIR-01 §8].

The ENDMSN list uses authored mission indices and includes the whole campaign
with completion marks. Its selection survives repaint; Start resolves the chosen
mission and preflights its briefing GUI before installing the next controller.
Difficulty writes the shared setup selector. Child GUI failures retain the
parent input owner and report the unavailable resource. Fresh skirmish restart
retains the original setup and controller rows; imported saves use their
separate reconstruction path.

`Session.CommitCampaignTeardown` is the shared campaign mark writer for the
ordinary ending transition and manual teardown. It reads the live win bit;
a pending outcome does not replace that bit. The frontend return retains the
retired session's bank after teardown, while starting a new campaign remains
the reset owner [08 R-CAMP-01 §7] [08 R-CAMP-01 §8].

The postbattle adapter admits ending media for the portable shell, including
windowed playback. After the existing final-victory/`nomovie` decision and
results fade, it collects the controller's ordered ending and credits requests,
tears down the battle, then starts that sequence in the frontend movie player.
Defeats, campaigns with a successor, and `nomovie` missions retain their
existing ENDMSN/glamour route [08 R-CAMP-01 §6].

**The state machine.** `State` is `0..7` with one dispatch method,
`Session.Advance`, that runs exactly one state operation per call. The
transition graph is a fixed matrix: `0→2`, `1→2`, `2→{3,4,5}`, `3→5`, `4→5`,
`5→6`, `6→7` on normal completion, `6→2` on abort and `7→2` on the front-end
return `[08 "Session states"]`. Single-player takes `2→5`; state 3, the network
pre-load, is present and unreachable. Completion of the loading state installs
state 6 whose *first run happens on the next dispatch*, never inline — the
session exposes that pending flag so the deferral is testable rather than
implied. The three-bit packet admission rule — one bit admits the
loading state, one the battle state, one everything else — is recorded by
`MaskLoading`, `MaskBattle` and `MaskOther` and its state mapping is pinned by
the state-machine test; no shipped path consumes it, because there is no
packet transport `[08 "Admission masks"]`.

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
Retail's profile read defaults to 250 and clamps to 20..500, without a lobby
gadget. Nanolathe's user-requested default is 1000 and its settings loader
clamps to 20..3276; the command-line override takes precedence (DESIGN_CONTENT_VFS
§5). Skirmish battle entry copies the configured word over the session's unit-limit word verbatim
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

Extraction sampling is implemented at unit creation, including creation paths
that bypass a placement helper. Settlement consumes the stored rate; later
terrain or feature changes do not resample it. The established contract is
`[05 R-PROD-01 §6]`, owned by DESIGN_ECONOMY_CONSTRUCTION.

**Meteor startup.** Before services start scheduling, battle entry and retail
restore install the storm from the selected schema, not `[GlobalHeader]`. The
original weapon name fixes enabled before `METEOR.TDF` may replace the five-field
record. Missing defaults leave that record intact; a present invalid default is
a fatal startup/load result only when the selected record asks for it. The phase
fallback preserves the same fatal outcome for a directly composed host rather
than logging or installing a partial scheduler. The numeric lanes after an
original empty weapon and a missing default are Unknown; the checked host
uses an explicit non-retail all-zero disabled record for that case
`[02 §6]` `[06 §6.5]`.

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
exists `[08 R-SKIR-01 §3]` `[05 R-ECO-01 §12]`. The local skirmish defeat
predicate separately tests only zero live units, so a defeated side restored
with both counters zero still satisfies defeat at its next due
`[08 R-TRIG-01 §6]`. `EndLatch` is the one signed 16-bit countdown plus the latch bits: ending, the two win bits, and the lose
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
or a staged battle `[08 R-SAVE-02 §11]`. The unit writer requires a live
subject and writes carrier and engagement
links as zero when absent or dead. Weapon targets instead preserve valid pool
slot identity, including a slot freed later in the same tick; restoration
checks bounds and the ordinary weapon visit resolves liveness
`[08 R-SAVE-02 §6]` `[08 R-SAVE-WEAPON-01]`. The writer projects `Dying`
into the packed pending-death bit without changing live state, and the reader
restores that logical latch independently of health and damage cause. Reference
reconstruction publishes each unit's saved pose, health and kills before loading
its carrier recursively and attaching locally, then loading its engagement
reference recursively. The scalar/status body follows those references,
so a live pending-death passenger remains loadable. Attachment takes its saved
unit-side mode explicitly; ordinary live admission remains unchanged. The writer
also projects current `Move.ModeMirror` and cached `MoveTier` into the packed movement
nibble, and the reader restores both without recomputing the tier
`[08 R-SAVE-02 §6]` `[04 R-MOV-01 §6]`. Restoring the tier is only half of it:
the movement package keeps the classifier's edge cache in a row of its own, and
`movement.RestoreMover` seeds that row from the restored tier, so a load that
lands in the same category emits no `StartMoving`/`MoveRateN` at all
`[04 §5.2]` — see [DESIGN_MOVEMENT_PATH.md](DESIGN_MOVEMENT_PATH.md) "Save
boxes" for the cache and the open `setSFXoccupy` band question.

The script image preserves draw, cache and shade flags per COB piece through
its binding to model pieces [08 R-SAVE-02 §9]. Earlier Nanolathe saves wrote
all draw/cache flags as set and lost that state; a loader cannot recover the
original flags from those files. It restores the values present without
replaying initialization or inferring visibility from piece names.

After the restoration and battle-entry tail succeed, the load result publishes
one initial frame at the restored global tick. This is the host presentation
boundary for an already committed world: it neither advances the simulation nor
clears the saved pause gate. A paused save must expose its restored units, HUD
and visibility before its first subsequent sub-tick `[08 "Load process"]`
`[08 "Scheduler and random state in saves"]` [I6].

The unit save projection reads `Move.ModeMirror`, the mode last published by a
position commit, while the mover box retains the live mover mode. Attach and
detach requests can change the latter after the cargo's visit. Restore retains
that disagreement in both the unit mirror and collision cache until the next
commit, rather than turning a late release into a completed occupancy update
`[04 R-AIR-01 §10]` `[08 R-SAVE-02 §6]` `[08 R-SAVE-02 §8]`.


Both load routes restore the 25 campaign marks, including the all-`U` reset
for an invalid length, so an in-battle load retains earlier mission results
through teardown and the next save `[08 R-SAVE-02 §2]` `[08 R-CAMP-01 §8]`.
After base restore, constructor building placements are released together;
the derived yard owners are rebuilt from each saved committed cell pair and
yard state. This includes unfinished structures without movers and keeps
port-18 transactions and teardown on the same footprint as movement
`[08 R-SAVE-02 §6, §11]` `[04 R-COLL-01 §4]`.
The save projection copies that cell pair and footprint from the movement
collision record when present, otherwise from construction's retained
placement. An unfinished structure therefore preserves its committed yard
anchor even before it owns a movement collision record.

Feature save projection emits names for the complete live terrain definition
list, including corpse and successor definitions admitted after map loading.
The original TNT name table remains map metadata; appended definition names
come from their compiled canonical keys and retain their live ordinal. This
keeps the saved feature records and name remapping table aligned
`[08 R-SAVE-FEATURE-01]`.

The windowed `--map` entry retains the same `gameShell` ownership as battles
started through the front end. Its save/load dialogs and battle replacement
callbacks therefore use the shared shell lifecycle.

The definition active byte is read through `WeaponDef.ActiveByte`, whose
fresh value is its catalog slot byte. A restored value overrides that initial
projection without changing catalog identity. Staging clones the catalog
before allocation; each unit's post-script weapon pass applies its saved bytes
to those battle-local definitions in slot order. The slot enabled bit stays
independent. This also keeps hand-authored fixture definitions initialized from
ID without requiring a separate constructor, and removes the unused slot
scratch field that previously supplied the writer's active byte.

**The trigger adapter.** The session supplies the trigger evaluator its
world-facing callbacks: the stamped footprint anchor a boundary condition
reads, the projection that turns authored map pixels into world coordinates on
the first radius poll, the shared movement radius query, the commander-identity
predicate, and the unpositioned `Victory Condition` cue, which is admitted as a
committed frame event so it crosses the publication boundary like every other cue `[08 R-TRIG-01 §3]`
`[08 R-TRIG-01 §5]` `[08 R-TRIG-01 §8]` [I6].

### 2.2 `internal/mission` — campaigns, missions, placement, `InitialMission`

**Pending campaign discovery boundary.** The current catalog loader returns
an error for a read failure and continues after a successfully parsed empty
file. The retail enumerator instead retains the admitted prefix and spends
its remaining attempts retrying an empty or unreadable current file, without
advancing to later names [08 R-CAMP-01 §1]. The frontend currently suppresses
campaign availability on a discovery error. Reconcile catalog enumeration
and frontend admission together; do not substitute a claim that retail simply
skips every unreadable entry. Transient reads and short-read contents remain
outside the established bounded contract.

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

The local argument scanner consumes one byte string sequentially for every
numeric and name handler. It preserves seeded destinations after the first
failed conversion, accepts adjacent prefixes, and applies the verb-specific
name scanset `[04 §3.6]` `[08 "Argument parsing"]`. It uses the existing
binary32 argument boundary before scaling. Untouched uninitialized coordinate
temporaries use an explicit zero host policy; they do not model retail's
run-specific temporary contents. Exact rounding for adversarial decimal
inputs remains open in `[fmt ota]`.

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
mission-armed flag, and the callbacks named in §2.1. `Poll` updates the
condition's completion, celebration and one-time centre conversion as its kind
requires; `Notify` drives the three notification slots — removal, capture,
creation — and reads the pre-transfer owner only at the capture slot `[08 R-TRIG-01 §4]` `[08 R-TRIG-01 §7]`.
`Evaluate` owns the queue combination and nothing else: it re-injects the
defaults at poll time, evaluates victory as an AND stopping at the first false
and, only when victory is false, defeat as an OR stopping at the first true.
The caller owns the kind gate, the cadence and the latch `[08 R-TRIG-01 §6]`.

For radius conditions, `PollContext.VisitRadiusUnits` routes through movement's
existing `VisitUnitsInRadius`; it owns the stored-width geometry and retained
sector membership `[08 R-TRIG-01 §5]`. The session adapter excludes attached
units because the shared host index retains cargo for overlap consumers. It
continues the query after a hit and leaves `AllUnitsKilled`'s owner-slice cargo
eligibility unchanged. A missing query callback is an incomplete host context:
no candidates are supplied, the existing completion latch is retained, and no
whole-pool fallback runs. This is host handling, not a retail missing-query path.

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
`[08 R-AI-01 §20]`. Derived weights, limits and locks use retained catalog
record IDs. Exact names select the first equal record; category and fragment
passes visit every record. Name accessors are first-match projections. Catalog
or difficulty rebinding rebuilds the derived tables from authored input.
The strategic planner's counts, class vectors and candidate names still need
record-indexed storage before it can select later equal-name definitions;
this is an implementation limitation, recorded at the selection boundary.

`Manager` is the per-player planner: the player index, the embedded `Strategic`
state, the ten task deadlines, the profile, the nine group vectors, the
classification countdown, the two attack-wave engagement latches, the rally
task's best/probe/drift triple, and the session bindings it must not do without
— the build submitter, the order binding, the alliance predicate, the
visibility predicate and the shot-time gate. Every binding fails *closed*: an
unbound manager keeps an empty rally vector rather than granting omniscient
target knowledge, and infers no hostility from ownership or side identity
`[08 R-AI-01 §9]` `[08 R-AI-01 §19]`.

What the manager *does* with that state on a dispatched tick is selected by the
session's bound rule set: `ai.Planner` is the think step `Manager.Tick`
dispatches. Strict 3.1 and Community 3.9 bind the retail one, the reserved
Modern set binds it with [Modern wave air targets](#modern-wave-air-targets),
and the manager keeps owning its state, its save and its restore either way
([DESIGN_GAMEPLAY_RULES](DESIGN_GAMEPLAY_RULES.md#the-computer-players-think-step)).
A computer player the lobby marks Modern, in any rule set, has its decisions
made instead by the [Modern AI computer player](#modern-ai-computer-player)
("Per-player selection"), whose controller lives in the manager's `Ext`
beside the bindings only it reads — the map's start positions, the battle
seed and the player's own sight predicate.

`TaskKind` is the ten-slot task vector and its order is load-bearing. Slot 0
holds no task object at all — the dispatcher's null test skips it — while slot
5 holds a real object of the null task class whose body returns immediately
(unless the ProTA package switch of §2.8 is on) but which *owns group record 5*, the armed-buildings group the attack wave reads
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

The recomputation's first pass accumulates in `float64` because retail's
working precision is Established at 53 bits and the width decides the stored
byte: for a metal cost that is a non-zero multiple of 100 the exact product
lands just under an integer, which a `float32` sum rounds away, and five
shipped definitions carry such a cost. The constants stay single precision,
the two coefficients retail narrows keep their `float32` stores, and each sum
truncates immediately, which is how the allowlist row reads
`[08 "Arithmetic and clamping"]` `[08 R-P0-05 §5]` [I2]. Every
multiply-then-add in the routine wraps its product in an explicit conversion:
the Go specification permits fusing them into one rounding, and some backends
do, which would make the same source round differently per host [I1].

The remaining class coefficients follow the store boundaries in
`[08 R-P0-05 §5]`: the other-mix energy addition, the energy coefficient's
net-energy product and difference, and the metal coefficient's products and
sums retain working precision except at the named intermediate single stores.
`TestClassCoefficientWorkingPrecision` locks valid authored fractional energy
counterexamples and separately labels fractional metal-cost cases as arithmetic
fixtures; the retail integer cost loader does not establish their reachability.
These corrections preserve refresh RNG order and the Modern AI selection.
All three extractor predicates narrow the catalog value at the consumer and
use ordered nonzero comparison; the two net-energy bonuses admit negative or
unordered query results. Authored archive fixtures lock tiny signed extraction
values that store as zero, while explicitly labeled direct-input fixtures
exercise unordered arithmetic without claiming a retail NaN producer.
The net-energy query also retains its selected product at working precision
through the return, with ordered input gates `[05 R-PROD-01 §1]`.
`TestClassTidalQueryRetainsWorkingProduct` compiles an authored fractional
tidal generator and map scalar to distinguish the coefficient boundary and
checks that refresh still consumes only its outer RNG gate.

The Classic class-vector weapon score narrows `DamageDefault` at the consumer
according to `[08 R-P0-05 §5]`; the catalog retains the authored integer.
`TestWeaponScoreConsumesUnsignedDamageWord` locks wrapping, ordinary damage,
the inactive weapon sentinel, and refresh RNG effects. These authored boundary
cases establish the correction without claiming a stock-content impact. They
do not change which players use the Modern AI controller.

`ClassVector`, `ScoreInputs`, `ComputeMix` and `ComputeScore` are the candidate
score as a pure function, testable before it is wired to anything.
`ScoreInputs` preserves the working-precision net-query results until the
below-one comparison; the source economy aggregates remain single precision.
Stock rejection and pressure bonuses retain their unordered comparison arms
`[08 R-P0-05 §3]`. Direct aggregate fixtures expose those boundaries, including
the unchanged RNG stream when an unordered stock rejects selection; no
nonfinite producer or stock-match incidence is inferred.
`MetalSpot`, `PlacementRegion` and `PlacementResult` are the placement root's
inputs and its typed answer, including the reason code a rejection carries
`[08 R-AI-03 §1]` `[08 R-AI-03 §2]` `[08 R-AI-03 §5]`.

Both Classic placement helpers share `retailOriginCell`, which keeps the
footprint bias in the signed coordinate word before converting to a cell
`[08 R-AI-03 §3]`. Its direct arithmetic boundary tests distinguish wrapping
from a widened intermediate; they do not claim a stock-content producer for
the extreme inputs. The helper consumes no RNG and does not select an AI mode.

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
words. Its one binary box is the live-battle-only
[`Radar Image` preview](#the-radar-image-preview-box).

`PlayerSlot` is one `Player%i` account — the slot's scalars, its side
and logo bytes, and, as that account's last item, its eleven-byte alliance row
with the forced self-alliance `[08 "Summary"]` `[08 "Player records"]`. Its two
storage items are the storage-bonus **operands** and its storage flag the
bonus enable bit; the derived capacities are not persisted — a restored
player's capacity is rebuilt from its units plus the restored bonus at its
first settlement pass `[08 "Player records"]` `[05 R-ECO-01 §4]`.

Names and score-panel ranks have no item in that closed account. Staging and
core restoration initialize each saved slot's registration rank to its slot
index and fill an empty skirmish name from its restored controller and side.
This keeps every qualifying row available, including an eliminated player and
non-contiguous saved slots, without extending the bank. Whether retail later
reorders the initial ranks from restored kill counts remains the existing
research unknown; the current placeholder retains registration order until a
credited kill shifts it `[08 R-SKIR-01 §2]` `[07 R-HUD-04 §1]`.

`PlayersMeta` is the rest of the `Players` account: the `Human Player` integer
and the `GameTime` box. **The load default for `Human Player` is `10`, no
human** `[08 R-SAVE-02 §12]` — our writer always emits the item, so the default
is the malformed-input path, and it matters because the restore adopts the
value as the local and viewing identity only while it is in `0..9`. Defaulting
to the zero value instead would hand a save that lost the item to slot 0.

**Which limit a save persists, and which battle a load can affect.** In Strict
3.1 (and for campaign saves in both modes), the
`maxunits` item is the **configured** unit-limit word — this build's copy of
`[Preferences] UnitLimit`, held by `cmd/nanolathe`'s setup record — and never
the battle's own session limit, which in a campaign comes from the mission OTA
instead `[08 R-SESS-01 §9]` `[08 R-SKIR-01 §6]`. The session package holds only
the per-battle copy, so `RetailBattleSummary` takes the configured word as a
parameter rather than reading a setting; the continuation writer takes it on
`ContinuationSaveMetadata`. On the way back in, `applyRestoredUnitLimit` stores
the saved word, unclamped, into that same configured record — and only when the
account carried the item, which is why `save.Summary` reports presence
(`HasMaxUnits`) separately from value. Strict skirmish pools use the configured
word as it stood before restore (`RetailLoadDeps.UnitLimit`); campaign pools
use the mission OTA. In those cases, a restored configured limit reaches the
*next* skirmish battle entry `[08 R-ENTRY-01 §6]`. Modern skirmish saves and
loads use [Modern save unit limits](#modern-save-unit-limits).

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

The saved accumulator word is the live 3D damage state: weapon hits add their
`[DAMAGE] default` word into it with 16-bit wrap and the death transition fires
when the definition's `damage` is at or below it, unsigned, so a reloaded wreck
keeps its damage and the next hit continues the same sum `[05 R-FEAT-01 §8]`.

**The animating record is a live cursor, not a byte copy.** The record's
frame byte at `0x08` is the live event cursor's frame index and its state byte
at `0x09` packs the family selector in the low nibble with the burn
countdown's **high** nibble in the high nibble — the low nibble is the save's
loss. On reload the selector re-runs its family through the ordinary
ignition or death/reclaim transition, which binds the definition's own
sequence from the battle's content metadata at frame 0 (a burn re-seeds its
countdown with ignition's simulation draw, consumed and then discarded), and
the reader then overwrites the accumulator, the cursor frame and the countdown
(nibble shifted back into place). The cursor's delay is not saved and stays at
frame 0's word, so the restored frame holds for that delay before the sequence
continues at its own cadence; the record then completes on the visit its
remaining frames run out and stamps the family's successor. A family whose
sequence does not resolve takes the contract's own outcome — immediate
replacement for death/reclaim, a resting feature for a burn — and the reader
has no record to overwrite `[08 R-SAVE-FEATURE-01]` `[05 R-FEAT-01 §5]`
`[05 R-FEAT-01 §9]` `[05 R-FEAT-01 §10]`. The writer skips a cell whose
attached bit has no live record behind it, as the retail writer skips a
sequence pointer matching no family `[08 R-SESS-01 §4]`.

#### The `Radar Image` preview box

The Summary's one binary box is written on **live-battle** saves only: an
8-byte header of a `u32` width and a `u32` height, then `height` rows of
`width` palette bytes. It is never restored — the load dispatcher treats it as
presentation for the list — and the load screen's `RADAR` gadget is its only
consumer `[08 "Summary"]` `[08 "Account inventory"]` `[08 R-SAVE-02 §3]`.

Its raster is the radar surface the battle rail composes, which presentation
owns and a session may not reach `[I6]`. The box is therefore the **caller's**
half of the summary: `cmd/nanolathe` encodes the rail's composed FINAL surface
(`save.EncodeRadarImage`) into `save.Summary.RadarImage` before handing the
summary to `RetailBattleSaveInputs`, and `RetailBattleSummary` itself leaves
the field alone. A caller with no radar — a headless save, a continuation —
writes no box, which the panel treats exactly as it treats a short box: it
shows nothing.

The save list does **not** carry preview rasters: `save.ReadSummaryFile` keeps
dropping every box payload, and the panel reads the box of the one selected
file through `save.ReadSummaryFileWithBoxes` `[08 R-SAVE-02 §1]`
`[08 R-SAVE-02 §3]`.

Two extents are unestablished and carry `TODO(question)` at their sites: what
retail's writer puts in the box (this build writes the aspect-fitted radar
picture, contacts included, as the rail last composed it, rather than the
126x126 canvas with its letterbox padding), and how the panel places the box
inside the authored 121x113 `RADAR` rectangle (this build resamples it with its
aspect preserved and centres it). Only the header and row layout are
established.

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
path. A request that carries a filesystem and no catalog also carries the
content limits the mounted content set's profile resolved to, which the
constructor's own compile runs under — the same input `MissionEntryOptions`,
`SkirmishEntryOptions` and `RetailLoadDeps` take, with the zero value keeping
the retail baseline (DESIGN_CONTENT_VFS §5 "Content profiles").

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

### 2.7 ProTA 4.8 package acceptance boundary

`internal/session/prota_retail_test.go` is the asset-gated session acceptance
check for the identified ProTA 4.8 archive. It mounts original assets first and
the package second, resolves the `prota` content profile, and compiles and runs
through the profile's directory table, limits and `prota` gameplay-feature
declaration. The session's resolved feature value must equal the independently
selected `prota` table, while its explicit rule-set selection remains Modern;
content detection does not select a gameplay mode. The check requires the
profile-mapped `SIDEDATA`, Spark, Blaze, Apex, both commanders, and the separate
Arm and Core shipyard direction definitions to retain `ProTA.gp3` provenance.
It then runs a bounded skirmish in which an explicit human order makes Spark
damage Blaze, an Arm commander reaches a live factory nanoframe, and the battle
crosses the retail bank write/read boundary. A correctly authored east-facing
Arm shipyard admits an authored product into its counted factory queue, and the
queue survives that boundary. At the restore boundary the live unit and economy
projections, selected rule set, feature-table digest and ProTA catalog
provenance match; after it, the restored factory remains alive and continues
construction. The bank does not carry the gameplay selection or content
profile: the load dependency record explicitly reselects Modern and supplies
the same profile declaration, then the restore check verifies the result. This
establishes authored definition, weapon, construction, factory-queue and
save/load integration. The explicit attack is not evidence about autonomous
targeting or the package DLL's AI policy. See
[ProTA 4.8 engine package, “Package acceptance cases”](../research/extensions/prota-engine.md#package-acceptance-cases).

The directional-yard result preserves two distinct authored sources. Every Arm
regular/advanced direction and every Core advanced direction has CANBUILD
membership, as do the base and north Core regular yards. `ProTA.gp3` defines
the remaining Core regular units and their physical pages as `CORSYE` and
`CORSYW`, while its two populated CANBUILD sections are named `CORSYNE` and
`CORSYNW`. Those yards retain empty CANBUILD membership; the unmatched section
names are not treated as aliases. The installed `CORSYE1.GUI` and
`CORSYW1.GUI` pages each name `CORCS` as a product gadget, and physical factory
activation resolves that exact gadget independently of CANBUILD [07 §9]. The
acceptance checks therefore keep both directional yards' published CANBUILD
lists empty while proving that clicking each installed `CORCS` gadget reaches
a counted factory queue. No synthesized membership or name-similarity fallback
participates.

The same check enters original Core mission 1 and Core Contingency mission 6,
ticks both as campaign sessions with a campaign AI manager, and checks the
concrete overlay boundaries. Original Core mission 1 uses its authored
use-only list, excludes Apex from the battle-local catalog while leaving the
shared catalog unchanged, advances to the discovered `MISSION1`, and resolves
`CC01.TNT` from `ProTA.gp3`. Core Contingency mission 6 resolves its OTA overlay
from `ProTA.gp3`, its base terrain from `ccmiss.ccx`, and retains authored wind
bounds `90..900`.

This automated check does not cover the twelve-slot GUI, build hotkeys,
portraits, palette colors, campaign GUI build submission, trigger-driven
mission completion, a full autonomous AI match, or music backends. The
package's AI and income behaviour is §2.8. Those remain separate visual,
long-running or manual package acceptance work. The successor assertion proves
campaign discovery and routing only; it does not claim that the bounded session
won mission 1.

### 2.8 ProTA 4.8 package computer-player behaviour

The shipped ProTA 4.8 loader changes the computer player in four places, all
**Established** for that package
([ProTA 4.8 engine package, "AI and economy evidence audit"](../research/extensions/prota-engine.md#ai-and-economy-evidence-audit)).
Each is a Community feature-table switch the mainline table leaves off;
ProTA's mod config turns them on in its `rules.communityFeatures`, and Strict
3.1 ignores them. Selection, the table and the combat half are
[DESIGN_COMMUNITY_PATCH §4.7](DESIGN_COMMUNITY_PATCH.md#47-prota-48-package-behaviours).
The session projects the three AI switches onto every manager's `Community`
field at binding and at construction, beside `Planner`; the think step reads
that copy, never the table.

- **Income** (`AIDifficultyIncome`, owned by `internal/economy`). A computer
  player's per-unit contributions and feature-reclaim credits take Easy
  `0.5`, Medium `1`, Hard/other `4` at the retail store boundary. The
  difficulty selector the plan gate reads is unchanged.
- **Stockpile purchasing** (`AIStockpileProducts`). The null task slot runs the
  resource/builder-queue body over group record 5 (armed buildings), writing
  `tick + 30` first and walking the vector in order. In both records a live,
  completed building that holds *any* secondary order is skipped for the whole
  visit — the order's count and the completed rounds are not read — before the
  activation and product branches. The product branch is the retail one; a
  selected `MAKENUKE`/`MAKEANTI` product reaches the ordinary submission helper
  (`bindAIQueue`), which inserts one counted slot-zero `BuildWeapon` round
  instead of a unit order, sharing the build-page toy's insertion. Production,
  its 200-round cap and launch are the retail stockpile path `[06 §11.1]`.
- **Low-energy appliances** (`AIApplianceEnergy`). The activation arm is
  selected by the signed top byte of the binary32 `energyuse` being at least
  66 instead of by `makesmetal`; the C3 toggle, its draw and its
  no-fallthrough rule are unchanged.
- **Builder stop threshold** (`AIBuilderStopThreshold`). The construction
  task's placement cutoff for a capture-capable member is ten; the reposition
  pass keeps five, so counts five through nine run both passes.

No manager state is added: the null deadline is ordinary task state, not
saved, and rebuilt at battle entry like every other deadline. Two questions
remain **Unknown** and are `TODO(question)` markers at the resource task:
whether any shipped route services the mobile anti-nukes (`ARMSCAB`,
`CORMABM`, which reach neither task), and whether the Core east/west
shipyards consume the differently named CANBUILD lists.

### 2.9 TA Zero Alpha 5 package acceptance boundary

`internal/session/zero_package_retail_test.go` opts into Base, Alpha 5 and
Map Pack 1f through `NANOLATHE_MOD_ROOTS_ZERO`, after the ordinary retail
root. It shares one catalog, preserves GOK/ARM/CORE order and commander
identity, and checks script-bound human/Classic AI entries, explicitly ordered
commander combat, factory nanoframes and their construction progress after
Nanolathe save restoration. An Arm factory's Direct command selects two
repeatable buildpad poses and retains the saved idle pose. The three factions'
actual AI factory scripts consume the bound unit-range and alliance ports;
the established current-source port contract remains independently tested.
Four map sessions check FireRain, Hailstorm and both Tempest parameter sets,
including the authored ten-start schemas despite two/four-player filename
prefixes. Duration and hit spacing use the existing meteor conversions
[06 §6.5]; no weather arithmetic or script engine rule is added.

These are package integration checks in Modern with the `tazero` Community
feature table. They do not establish historical DLL equivalence, shield/VSOC
mechanics, campaign conversion, legacy `.zsv` interoperability, or long-match
AI quality. Tests skip without explicit mod roots. A separately run bounded
probe covered all fifteen map-pack entries and the busy-factory Direct case;
that observation is not a full gameplay parity claim. The evidence and further
acceptance cases remain in
[TA Zero engine](../research/extensions/ta-zero-engine.md#package-acceptance-cases)
and user setup in [TA_ZERO_SUPPORT](TA_ZERO_SUPPORT.md).

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
uses scalar presence, independently of type or value: kind 1 with the marker
selects campaign briefing; kind 2 with the marker selects fresh entry; absence
selects battle restoration. A binary box is not a scalar marker. The kind-2
fresh-entry setup dependency is still unresolved, so loading that form reports
an explicit error instead of restoring saved battle state or entering briefing.
Preflight inspects no bulk account, seeds no stream
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

**C10 — load routes.** A campaign save with the between-missions scalar yields campaign
continuation metadata — campaign path, campaign and mission name, mission
index, difficulty, side and the 25-byte mark array, reset to all-unplayed when
the source is not exactly 25 bytes. A save without that scalar yields a staged battle:
detached staging first, then core restoration, then the battle-entry tail —
the graphical user interface, the per-player phase primed once on the restored
world at the restored tick, no second resource grant, then the metal-spot
lists. The tail follows the restoration dispatcher, not the other way round
`[08 R-SAVE-02 §11]` `[08 R-SAVE-02 §11-A]` `[08 R-ENTRY-01 §8]`. Staging
applies each `Player%i` account onto its player record and then mirrors the
freshly built end latch — countdown −1, no bits — onto every record, as
battle entry's per-player reset does: neither the countdown nor the latch is a
persisted key, and the world rebuild's reset precedes the dispatcher, so a
load resumes unarmed and the settlement gate stays open `[05 R-ECO-01 §12]`
`[08 R-TRIG-01 §6]` `[08 R-ENTRY-01 §8]`.

Detached staging restores Features, Metal and PlayerFeatures before validating
and reserving the stable unit identities. It constructs no units; core
restoration invokes the ordinary forced-slot allocator on each record's first
recursive visit. The numbered scan starts each unseen record, but a carrier or
engagement reference can construct a later numbered record first. Burning
features consume ignition RNG before unit initialization; the allocator's new
hover phase is not a saved field and survives core restoration. Stage state retains the
completed feature pass so core restoration cannot ignite it twice. Burn sounds
remain buffered until core restoration has rebuilt visibility and succeeded;
a discarded stage publishes none. This changes no stable unit identities or
save bytes `[08 R-SAVE-UNIT-01]` `[08 R-SAVE-FEATURE-01]`
`[08 R-SAVE-02 §6, §11]` `[04 R-MOV-01 §5c]`.

Each recursive visit publishes saved pose, health and kills immediately after
construction, restores the carrier recursively and attaches locally, then
restores the engagement reference recursively. A back-reference to an ongoing
visit skips its already live slot. Carrier cycles are rejected independently
of engagement cycles. The visit then restores the scalar/status body, account,
mover, queues and front-head goal, script, and weapon state before returning.
Thus a completed referenced unit's shared weapon-definition writes can affect
later constructors, while a pending-death passenger attaches before its saved
latch is applied. Detached retries retain completed phases, preserving the
constructor draws and attachment order. The final derived occupancy rebuild
still releases all constructor placements together before restoring saved yards
`[08 R-SAVE-02 §6, §11]` `[08 R-SAVE-WEAPON-01]`.

During core restoration, `HasMover` alone selects the 35-byte mover reader;
an unfinished product therefore restores its saved mover fields and committed
occupancy before the staged session is published. Route, follower and proposal
state remain derived and are not reconstructed from the mover box. A completed
no-mover structure separately rebuilds its yard/footprint collision support at
the saved anchor; an unfinished no-mover frame stays under construction
placement ownership. `[08 R-SAVE-02 §6]` `[08 R-SAVE-02 §8]`
`[08 R-SAVE-02 §11]`.

Order save projection obtains subtype data from movement's live record objects,
not the order's restore staging bytes. This includes displaced records in both
queue segments. Restore reconstructs all objects before binding the primary
head, preserving saved satisfied bits; no handler is run to repair a missing
movement wake. Released objects remain absent on subsequent saves
`[08 R-SAVE-02 §10, §11]`.

Once all saved queues exist, construction rebuilds its local progress index
from live producer order targets. The index is derived host bookkeeping;
carrier and `GetBuilt` references have separate lifetimes. This preserves
construction progress in the first published frame, with no order pump or new
save field. Target removal remains owned by the ordinary order observer path
`[08 R-SAVE-02 §11]` `[04 R-ORD-01 §6]`.

The base-record traversal also retains its recursive completion order for AI
group reconstruction. A unit's carrier and engagement references complete
before its group append, so this order can differ from pool order. The manager
consumes the retained unit sequence before the entry prime; it never substitutes
a pool scan, which would change wave bootstrap and distance tie-breaking
`[08 R-SAVE-02 §6]` `[08 R-P0-04 §3]`. The saved AI index is separate from
the UI control-group value; reconstruction never copies one into the other
`[07 §9]`.

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

**C14a — order queue-word boundary.** The order main box's queue word is the
retail static-mask copy, not Nanolathe's local `Node.Flags` domain. Save maps
the local active, caption-pending, auto/default, tombstone, completion, and
StopBuilding-pending states to their established wire bits; restore performs
the inverse mapping. Purge survivorship is reconstructed from retained static
bit 2. All other static bits stay verbatim, including constructor mutations
such as a cleared target-observer bit, so restoring an order never regenerates
its descriptor mask. This is one canonical wire form: it neither reuses local
flag numbers nor attempts to recover an older lossy image `[04 R-MOV-03 §6]`
`[04 R-ORD-01 §13]` `[08 R-SAVE-ORDER-01]`.

**C15 — the 28-byte scheduler box and the deadlines.** The scheduler image and
the per-player settlement deadlines round-trip as absolute ticks. Neither
random stream is saved; the load entry creates fresh stream seeds before the
restore dispatcher, while the saved deadlines retain their absolute phase
`[08 "Scheduler and random state in saves"]` `[08 R-SAVE-02 §11]`
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
`[08 R-SKIR-01 §3]` `[05 R-ECO-01 §12]`. This economy status does not gate
the local defeat predicate, which reads only the live count. A save with no
local units reconstructs both counters as zero and must still resume the local
defeat countdown on its saved settlement phase `[08 R-TRIG-01 §6]`.

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
nothing survives those skips. The kind-2 sweep has no rule-word test, so
deathmatch still permits victory `[08 R-TRIG-01 §6]` `[08 R-SKIR-01 §3]`.

**C21 — one shared countdown, one latch.** A true due finds the signed
countdown negative and sets it to 4; each later true due decrements it; the due
whose decrement takes it below zero is terminal — the sixth consecutive true
due, 150 ticks after the first. A false due neither resets nor advances it. At
most one predicate steps it per due. On the terminal lost due, deathmatch
respawns the local commander; the terminal won path still writes the victory
latch under that rule. Other outcomes write the latch — the ending bit always,
the two win bits on the won path, the lose bit with the first win bit cleared
on the lost path. An exhausted deathmatch respawn search leaves the countdown
below zero and the next true due re-arms it
`[08 R-TRIG-01 §6]` `[08 R-SKIR-01 §3]`.

**C21a — respawn and watch-mode entry both call the full visibility rebuild.**
The rebuild retail calls with the full argument at battle entry is called again
at every commander respawn and at watch-mode entry, so neither is a matter of
republishing live observers: both stores are refilled from the mode word first
`[08 R-ENTRY-01 §7]` `[08 R-SKIR-01 §3]`. Under the default Unmapped mode a
deathmatch respawn therefore loses the dead player's map memory along with its
byte refcounts, rather than inheriting the pre-death grids, and the watcher
clear's Mapped + Permanent bits reach the grids as an all-visible fill instead
of leaving a watcher in unexplored fog `[03 R-VIS-01 §4]` pass 1. Both sites go
through one session helper (`rebuildVisibilityForEntry`), which supplies the
per-slot eligibility of step 2 and the current-raster observer records of step
3; the save-restore seam keeps its own narrower order, because it must install
the saved `Mapping` box between the fills and the observer publication.

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

Result team identifiers and per-row win/loss labels are host presentation
metadata; retail provides the local latch and the per-player score rows, with
no team column `[08 R-CAMP-01 §7]`. Campaign and skirmish results use the same
player-record team mapping for their winner/loser lists and score labels.
Campaign results retain the local latch as their overall outcome. This avoids
marking every score row as a loss by comparing a raw owner slot with a team
identifier. Validation covers both outcomes, either local slot, and the empty
setup rows left after restore; a stock Arm mission save also continues through
its authored location victory with the local row marked as a win.

**C23 — the post-battle sequence is a screen, not an overlay.** The controller
plays retail's state order over a frozen result, emitting the darkening fade,
the glamour art, the click-to-continue prompt, the statistic reveal and the
gamma restore, then routes: a skirmish exposes only the return to the main
menu, a campaign additionally exposes the continuation when a successor mission
exists. Its rendering is DESIGN_INTERFACE_HUD_INPUT's `[08 R-CAMP-01 §6]`
`[08 R-CAMP-01 §8]` `[07 R-FE-01 §10]`.

The frozen battle picture remains through the darkening fade. Once ENDMSN is
entered, the result UI owns the entire surface: the client clears it and
records the authored result art without blending, traversing, or submitting
the retired battle world. This also keeps a large final army out of the
result screen's input and present path.

The shell installs ENDMSN's selected authored mission before reopening the
return panel and MSNBRIEF. Campaign-only presentation must retain that identity
through panel population, just as a between-missions load retains its saved
mission; only an explicit new-campaign action selects mission zero
`[08 R-CAMP-01 §3]` `[08 R-CAMP-01 §8]` `[08 R-SAVE-02 §2]`.
`TestCampaignWinAdvancesToTheNextBriefing` crosses two results transitions in
each layout and checks the successor map and battle request.
`TestCampaignFallbackContinuationKeepsSelectedMission` checks saved continuation
identity and the separate new-campaign action using authored fixtures.

The asynchronous host presents a terminal publication immediately after joining
its batch. Its usual delay behind the latest released tick cannot apply once
the latch stops further ticks: that would keep the renderer on a nonterminal
frame while the host advances the results controller. The join copies the
committed terminal bit with the observed tick, so the end title, frozen result
and ENDMSN all read the same publication without consulting the live session.
`TestAsynchronousPresentationReachesTerminalPublication` locks both outcomes and
the join boundary; `TestCommanderKillReachesThePostBattleScreen` reaches that
boundary through the real commander-death chain.

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

**C3 — schema selection.** The format layer supplies one contiguous,
first-match schema projection to metadata compilation, browser admission and
mission selection. Network types share the exact vocabulary in
`formats.NetworkSchemaRank`. Runtime selection follows all candidate visits
and the StartPos acceptance/overwrite rule in `[02 R-MAP-01 §4]`; browser
admission uses the same types without the runtime StartPos requirement.
Campaign selection follows that section's difficulty preference order.

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
`[08 R-ENTRY-01 §2]`. A zero-byte use-only file yields no TDF tree and leaves
the catalog intact; the installed Example mission relies on this boundary.

**C9 — `InitialMission` runs once.** After every mission unit exists, on the
loading path, for a campaign mission and for a between-missions restore. It
registers with no tick dispatcher `[04 §3.6]`
`[08 "Unit creation and InitialMission timing"]`.

**C10 — the verb table.** A 23-entry dispatch over the first character,
tokenized by scanning for commas and copying each span into a fixed frame,
splitting on every comma. Coordinates and times first parse into single
precision, then promote before scaling by 65,536 or 30 and truncating toward
zero; positional triples carry literal zero Y. The build verb selects mobile
build only for `bmcode == 1` `[04 §3.6]` `[08 R-ENTRY-01 §6]`.

**C11 — the uppercase-`W` quirk is a contract.** An uppercase-led `W…` token
enters the build block, whose second character selects the weapon form, so an
uppercase-led token can never be a plain wait, and wait-for-attack must be
written lowercase `[04 §3.6]`.

**C12 — the postlude.** When at least one order was queued, the unit's
class/state bit clears; and unless the script contained a numeric-form attack,
patrol, self-destruct or make-selectable, a final make-selectable order queues
with zero auxiliary arguments `[04 §3.6]`.

**C13 — malformed input is silent.** Unknown letters, digits and punctuation
are ignored with scanning resuming past the comma; a failed type lookup for
attack or build produces no queue; a failed unit lookup for guard produces no
queue and for wait-for-attack falls back to self; move, patrol, unload, wait
and flag-bit tokens do not test their conversion counts. Each failed field
stops the scan and retains its seed and every later seed: build/stockpile
counts start at one, wait operands and patrol timeout at zero, and standing
operands at current state. Uninitialized coordinates retain Nanolathe's
explicit zero host policy. Only the numeric attack form tests for two floats
and only wait-for-attack tests for one name. Tokens past the frame length are
clamped rather than overflowed, as the research recommends `[04 §3.6]`.

**C14 — trigger argument shapes.** Two formats, `<name>,<int>` and
`<name>,<int>,<int>,<int>`, with `ANYTYPE` accepted wherever a type is expected
and recognized by the boundary conditions `[08 "Victory and defeat triggers"]`.

**C15 — all eighteen kinds exist.** Eleven victory and seven defeat conditions,
in the builder's probe order `[08 "Victory trigger types"]`
`[08 "Defeat trigger types"]`.

**C16 — defaults are owned trigger records.** With no authored victory
condition the builder appends destroy-all-units to the mission's victory queue;
with no defeat condition it appends all-units-killed to its defeat queue. The
poll-time empty-queue guard installs the same owned record, preserving
Satisfied/Celebrated state across later polls and save/load like authored
records; a true destroy-all-units predicate emits `Victory Condition` only
once for that record `[08 "Default triggers"]` `[08 R-TRIG-01 §6]`
`[08 R-TRIG-01 §8]`.

**C17 — polls and notifications have distinct effects.** Polls can update their
condition's completion, emit its one-shot cue and convert its radius centre.
The counted kill condition decrements on removal notification and completes at
zero or below. Boundary conditions compare the signed stamped footprint cell
against the stored cell threshold and accept an absolute difference **below
three cells**; timer conditions compare the tick count against seconds × 30
`[08 "Evaluation"]`
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
each window. Three per-type coefficient vectors exist: the initialization-only
byte, the first-pass single byte and the three-byte triple, the latter two
written only by the recomputation routine. The initialization-only byte written
once at construction starts at zero, gains 40 when a per-definition category
flag is clear and 20 when that definition's compiled build-option list exists —
which is exactly when it carries the authored `builder` flag, the entry count
never being consulted — and is **never** written by the recomputation routine.
The three-byte triple is
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

The resource task's metal-maker branch uses the established single-precision
stock and net-energy comparisons of `[08 R-AI-01 §2]`: energy at or below
metal plus metal disables through the ordinary activation service. Above that
stock threshold, nonpositive net energy leaves activation unchanged; positive
net energy admits one simulation draw bounded by five, enabling only when the
draw is nonzero. A zero draw leaves activation unchanged, including an already
active maker. Callbacks occur only on an activation edge `[04 §5]`.

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

**C5 — the candidate gates.** Three run before any of the score arithmetic: an
energy stock below 50, a metal stock below 25, and the per-definition gate bit.
The profile limit is the fourth and runs **after** the two pressure terms and
their ladders, before the mix — it rejects type index zero and any index at or
above the catalog count before it reads the limit vector. The pressure block
between them draws nothing and writes nothing, so the placement is an ordering
to keep rather than an observable `[08 R-P0-05 §3]`. The base-menu compiler preserves authored
names, so selection skips unresolved products individually without a draw;
resolved definitions still require initialized class vectors. A third hard gate
rejects a downloadable candidate when the manager received authoritative session kind one at battle
construction; fresh and restored construction pass that kind explicitly. The
**post-selection** filter
compares the *selected* definition's authored side string against the builder's
own, byte for byte and case-sensitively, and discards the whole selection on a
mismatch: no re-draw, no runner-up, and the draw is still spent
`[08 R-AI-01 §8]` `[08 R-AI-02 §2]` `[08 R-P0-05 §3]`
`[05 R-SHARE-01 §10]`.

**C6 — the score.** Computed with truncation exactly as written, with the
resource inputs in their established single-precision representation:

```
energyRaw = trunc(max(0, (min(trunc(cap), 1000) - curEnergy) * 0.125))
          + (netEnergy < 1 ? 20 : 0)
          + (prodEnergy < 50 ? 100 : prodEnergy < 200 ? 10 : 0)
metalRaw  = trunc(max(0, (min(trunc(cap), 500) - curMetal) * 0.25))
          + (netMetal < 1 ? 20 : 0)
          + (prodMetal < 3 ? 100 : prodMetal < 5 ? 20 : 0)
metalMix  = clamp(metalRaw, 0, 100)
energyMix = clamp(energyRaw - metalMix, 0, 100)
otherMix  = max(0, 100 - metalMix - energyMix)
score     = trunc((class0*otherMix + class1*metalMix + class2*energyMix) * weight / 10000)
```

Each capacity is truncated toward zero to an integer **before** the 1000/500
clamp, and the clamped integer is converted back to a float for the
subtraction — clamping the float and truncating only the product differs by one
whenever a capacity carries a fraction, and the difference reaches the reservoir
draw bound through the score `[08 R-P0-05 §3]` [I3].

That capacity truncation and the single truncation of the scaled difference are
the **only** narrowing steps: `energyRaw` and `metalRaw` carry their difference
and their product at the same 53-bit working precision as the class routine's
first pass, with `float32` inputs and single-precision multipliers, which is how
the allowlist row reads `[08 R-P0-05 §3]` `[08 "Arithmetic and clamping"]` [I2].
A single-precision difference rounds onto an integer boundary the exact one
stays below — capacity 1000 against a stock of 480.0000305 gives 64 wide and 65
narrowed — and that one count reaches the mix, the score and the reservoir
bound. The width is Established, not a platform residual: the runtime installs
53-bit precision control at startup and nothing reachable from the simulation
writes that field again `[08 "What remains not established"]` `[01 §8]`
`[01 R-DET-01 §3]`. Both terms wrap the difference and the product in explicit
conversions so no backend can fuse them into one rounding [I1].

The signed net-energy query reads the live wind scalar and the immutable map
tidal strength, so a gated recompute observes the current wind rather than a
copy `[08 R-P0-05 §1]` `[08 R-P0-05 §4]` `[05 R-PROD-01 §1]`
`[08 "Established AI-facing data and rooted planner"]` [I2].

**C7 — one draw per positive candidate.** In authored build-option order, each
positive score is added to the signed running total and immediately takes one
global-simulation draw bounded by that total. The candidate replaces the
selection when the returned signed value is strictly below its own score.
Nonpositive scores draw nothing; bounds below two do not advance the stream.
This is the repeated running-total reservoir, not one final draw
`[08 R-AI-01 §8]` `[08 R-P0-05 §4]` [I4].

**C8 — placement.** The established origin and growth algorithm is
`[08 R-AI-03 §2]`: each attempt grows the radius while it is below the larger
map dimension, with no final clamp; success resets it to zero. The origin
steps from the builder toward the strategic centre only when the truncated
three-dimensional distance exceeds the scaled radius. Otherwise, including
equality and zero distance, the origin is the centre. The fixed-point scale is
truncated before multiplying each axis delta, preserving the two arithmetic
stages of that contract. A candidate whose
extracts-metal word compares equal to floating zero goes to the scatter helper
with **no draw**; otherwise one draw with bound 255 selects the exhaustive
metal-spot helper when the mission's uniform surface metal is strictly less
than the draw, and the scatter helper otherwise — strictly less, so equality
chooses scatter. The exhaustive helper scans the battle-entry metal-spot vector
within a circle of four times the radius in cell units, then visits the
nearest deposits through the specified binary heap. Equidistant deposits
follow the heap's tie mechanics, not stable vector order. Validation stops
when a candidate's squared cell distance exceeds the first accepted candidate's
by the contract's slack; a failure does **not** fall through to scatter. The scatter
helper attempts up to 30 trials around the origin, drawing up to four values
per trial, selecting its lattice region by the definition's water-depth sign,
and comparing the footprint's per-cell metal sum against surface metal ×
footprint X × footprint Z × 2 — a trial is **accepted at or below** that limit
and retried above it, which is what keeps ordinary buildings off metal patches.
Success requires the yard and occupancy
validator to report placeable `[08 R-AI-03 §2]` `[08 R-AI-03 §3]`
`[08 R-AI-03 §4]` `[08 R-AI-03 §4-A]` `[08 R-AI-03 §5]` `[08 R-AI-03 §7.4]`
`[08 "Placement root and search helpers"]`.

**C9 — the draw-bound census.** Preserve the complete ordered ledgers of
selection, placement, task bodies and rescheduling in the owning research
sections. Explore reschedules before its body and an empty vector still takes
its nonzero-centre branch: one leg-count draw followed by two coordinate draws
per leg. Construction pass one has no completion gate; its current-order static
mask remains the relevant admission test. Bounds also include the map-derived
placement/scatter dimensions and task-body branches, so a short list of fixed
rescheduling constants is not an exhaustive census `[08 R-AI-01 §3]`
`[08 R-AI-01 §6]` `[08 R-AI-01 §7]` `[08 R-AI-03 §4]` [I4].

**C10 — the inert inputs stay inert.** The per-definition AI weight fragment is
read; the per-definition AI limit has no semantic reader and is wired to
nothing. The mission placement flags of §3.2 C7 are parsed and inert
`[08 "What remains not established"]`.

**C11 — the planner runs inside the settlement walk.** The session's
before-deadline hook runs the manager's computer tasks, its session-bound
weapon maintenance, the strategic refresh and LOS publication. Eligibility
belongs to `TickPlayer`; a skipped slot advances none of these owners. The
manager mapping is established by direct caller tracing
`[05 "Authoritative settlement order"]` `[08 "Dispatch gates and order sinks"]`.

**C12 — ordinary paths only.** Builds go through the construction queue, orders
through the command resolver and the order descriptor registry. There is no
privileged mutation path into unit or economy state, and no stockpile or
transfer shortcut `[04 R-ORD-02 §1]` `[08 R-AI-01 §7]`.

The construction task's mobile build is one of those resolved orders, not a
queue call that bypasses resolution: it is command code 14 issued **without**
the queue modifier, so code 14's gate applies — a non-empty compiled build list
and a live mover — and the issue replaces rather than appends. Only the rally
task branches on the resolver's answer. The other three task submissions and
the group broadcast do not, so a member whose definition fails the issued code's
capability gate has its unprotected front-segment records purged and is left
idle; a queued issue appends the sentinel and leaves the records alone.

A rejected resolution is therefore not a special case in the submission helper:
the sentinel is inserted through the same producer insertion, with the same
unit, target, position and trailing pair, because retail's insertion call does
not change when the resolver writes zero. The identity is simply descriptor row
zero, whose handler returns complete while reading nothing, writing nothing and
drawing nothing, so the record is freed on the pump pass that reaches the head
and the purge is what lasts `[04 §3.4]` `[04 R-ORD-01 §12]` `[08 R-AI-01 §3]`
`[08 R-AI-01 §9]`.

**Pass two's centre is mutable within the pass.** A capture-capable member
writes its own height into the shared strategic-centre local, not into the
per-member target copy, and the write survives to every later member of the same
pass — where it can flip the three-way distance branch and with it the pass's
random-draw count `[08 R-AI-01 §3]`.

**The pass-one height carry remains an implementation gap.** Retail also
copies the placement output's unwritten height into this shared centre before
testing placement success. The numerical value is unresolved, as are the X/Z
reads in its failed-placement cap. Nanolathe currently retains the original
strategic height between passes and tests the cap only for valid results;
these are deterministic placeholders, not established retail outcomes.
Caller-stack provenance or an explicit host policy must settle them before
changing the distance branches and their RNG effects `[08 R-AI-01 §3]`
`[08 R-AI-03 §5]`.

### 3.4 Not implemented

* **Multiplayer everything.** Retail's transport, packet framing and pacing
  stay out of scope and are named in ARCHITECTURE's exclusion table. The
  lobby, lockstep, integrity checks, departures and replays belong to
  DESIGN_MULTIPLAYER. What remains
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

## 4. Retail behaviour that is not a bug

* **A mixed land/hover wave can recall advancing hovercraft.** Wave merge
  sheds members by distance from the group calculation; the paired regroup
  then orders them toward the remaining wave. A usable route across water does
  not exempt a hovercraft from those group operations. The Two Continents
  seed-seven probe demonstrates this sequence in Nanolathe; it is not a reason
  to add a separate hover tactic `[08 R-P0-04 §3]` `[08 R-AI-01 §5]`.

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
* **A deathmatch loss respawns; a victory still ends the battle.** The kind-2
  victory sweep ignores the rule word. An exhausted respawn search on the lost
  path leaves the countdown below zero for the next due to re-arm; there is no
  post-exhaustion transition `[08 R-TRIG-01 §6]` `[08 R-SKIR-01 §3]`.
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
* **Random state is not saved.** Load entry creates fresh seeds before the
  restore dispatcher; save accounts do not overwrite either stream
  `[08 "Scheduler and random state in saves"]` `[08 R-SAVE-02 §11]`.

## 5. Divergences

* **Demo save/load presentation (user-authorized 2026-10-03).** If
  `guis/loadgame.gui` is absent, saving uses `guis/savelist.gui` and loading
  uses `guis/loadlist.gui`. These are authored restriction-list layouts in
  retail [08 R-SAVE-02 §5]; reusing them for games is Nanolathe host
  compatibility policy in every gameplay mode. Keep their rectangles and
  existing game-save controller: name editing, SAV enumeration, overwrite,
  deletion, bank serialization and restore all retain their contracts. Change
  the deletion caption to `Delete Game`. The demo lacks both game-save PCX
  backdrops, so these smaller windows use the existing panel/common-art fill
  and gadget renderer. Summary gadgets absent from a layout remain absent.
  A present retail GUI still requires its direction's retail backdrop; decode
  errors never select the fallback, and the selected fallback GUI is required.
  Direction changes select the corresponding fallback layout. Authored
  fixtures in `demo_save_test.go` lock selection, failure boundaries, focus,
  geometry and SAV-only listing/deletion. Visual acceptance saves a demo
  mission and restores it through the load dialog with the demo as sole root.

* **Installer-selected save directory.** The user-authorized `--save-dir`
  override lets a source installer place both save/load dialogs in a writable
  per-user directory even when retail content is elsewhere. With this override,
  the save screen applies the existing strip-last-dot filename normalization
  to the name alone, then joins it beneath the selected directory. Dots in
  ancestor directories (such as `.local`) therefore cannot truncate the path.
  Names containing either slash style, absolute paths, or host volume names
  are rejected as empty commit paths and write nothing; empty names retain
  their existing no-op. Listing and serialization retain their existing
  contracts. This is host convenience policy, not recovered retail behavior.
  The name-alone normalization applies without the override too: the default
  directory is likewise a host absolute prefix (the resolved installation,
  which discovery finds beneath dotted ancestors such as `~/.steam` or
  `~/.local/share`), while retail's strip ran on its assembled relative
  `SAVEGAME\<name>`, whose only dot-bearing part is the name [08 R-SAVE-02
  §1]. Stripping the joined host path instead wrote the bank at a truncated
  path the load list cannot see, while the save reported success. The typed
  name's own strip is unchanged (`v1.2 final` still saves as `v1.SAV`), in
  every gameplay mode; only the host prefix is excluded from the strip.

* **Autosave, quick save and quick load (user-authorized 2026-10-21, issue
  #146).** Retail has none of them.
  - **Autosave on leaving.** Leaving a live skirmish, campaign or Survival
    battle — the window's close button, or Exit Game or Main Menu in the
    battle menu — first writes it to the `Autosave` slot. It goes through
    the ordinary bank and sidecar projection. Automatic slots stage and
    validate the complete pair before replacing an earlier slot; a failed
    publication restores its previous bank and sidecar. An unrecoverable
    rollback retains and names the backup directory. Explicit retail Save
    keeps its truncate-write contract.
  - **Periodic autosave.** A live battle is also written to `Autosave`
    every ten minutes of game time. A paused game's clock does not run.
  - **Quick save and quick load.** Ctrl+F11 writes the `Quicksave` slot and
    Ctrl+F12 loads it.
  - **What writes nothing.** A battle whose result has latched, an online
    battle and a replay's playback write nothing.
  - **Where.** These writes go only into a host-selected save directory
    (`--save-dir`, which the installer's launcher always passes). Without
    one, the save directory is `SAVEGAME` inside the game folder, which
    nothing writes unasked.
  - **The close button.** The window backend hands the close button to the
    game. The game autosaves after the simulation join, then requests the
    exit. The client notification joins the parallel recorder before changing
    client state. A pending close cannot enter paused presentation sleep or
    be bypassed by audio maintenance. A game that does not answer within five
    seconds closes anyway.
  - **Continuing.** The load dialog opens with the newest save selected;
    the list stays sorted oldest first `[08 R-SAVE-02 §1]`. So continuing
    an interrupted battle is Load Game, then OK.
  - **Tests.** In `cmd/nanolathe`: `TestLeavingALiveBattleWritesTheAutosave`,
    `TestSurvivalBattleAutosavesAndLoadsAsSurvival`,
    `TestLoadDialogOpensOnTheNewestSave`, `TestQuickSaveKeys`,
    `TestQuickSaveThenQuickLoad`, `TestPeriodicAutosave`,
    `TestSaveSlotsNeedAHostSaveDirectory` and
    `TestSaveSlotsStayInADottedSaveDirectory`. In `ebitenapp`:
    `TestWindowCloseIsHandedToTheGameThenForced`,
    `TestPendingWindowCloseCannotRestBeforeHostStep` and
    `TestWindowCloseWaitsForRecorderBeforeNotifying`. Slot publication tests
    inject staging and rename failures and verify the previous pair's bytes.

* **Malformed building yard text uses the shared bounded parser.** AI placement
  applies the same occupied remainder as human placement and occupancy when a
  compiled building's yard text is exhausted, including an absent key. The
  definition remains eligible for the ordinary radius, selector and helper
  steps; it is not rejected as a missing definition. This is Nanolathe host
  policy, not an established retail empty-yard default; retail may read beyond
  that text `[fmt fbi]` `[08 R-AI-03 §2]`.

* **Every save has a Nanolathe sidecar.** Beside `SAVEGAME/<name>.SAV` the
  shell writes `<name>.SAV.nanolathe.json`, recording the mod, content
  profile, bound rule set, Community sources and entry table, configured unit
  limit and mutators the battle ran under; the bank's bytes are unchanged and
  the enumerator never lists the sidecar. A load that finds one restores
  under that selection instead of the host's: the recorded rule set is bound
  at staging, the recorded entry table replaces a resolved one
  (`RetailLoadDeps.EntryCommunity`), the configured unit-limit word is set to
  the recorded one before the Strict pool is sized, and the mutators are
  applied to the restore clone. The Modern saved-limit policy below is asked
  exactly as before; the configured word already equals the saved limit. A
  save without a sidecar loads as retail does. The contract, including mod
  switching and warnings, is owned by
  [DESIGN_MODS_MUTATORS §7](DESIGN_MODS_MUTATORS.md#7-the-save-sidecar).
* **Close the save dialog after a successful write.** This user-requested host
  UI policy dismisses the save dialog after the writer returns success, for
  both battle and campaign-continuation saves. Empty names and failed writes
  leave it open. The underlying options or results surface remains in place.
  Retail's save callback leaves the dialog open [08 R-SAVE-02 §1].
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
* **SC18's allocator policy is closed.** Ordinary retail allocations retain
  prior heap contents; Go zero-initialization is a host policy. The remaining
  unknown is the constructor and slot-reuse write set for records whose owning
  contracts are incomplete, as documented in DESIGN_RUNTIME_DETERMINISM.
  Retail bank restoration is implemented against its own account contracts
  (§3.1 C10).

No other entry in [SPEC_CONFLICTS.md](SPEC_CONFLICTS.md) originates in these
packages. SC21's building-versus-factory identity reaches the computer player's
placement path through the definition selector it shares with construction, and
is owned by DESIGN_ECONOMY_CONSTRUCTION `[08 R-AI-03 §7.4]`.

### Modern save unit limits

**Nanolathe Modern policy (user-authorized).** Modern and Community 3.9
share this save compatibility policy. For a skirmish battle load,
read a present, nonzero `Summary.maxunits` before constructing the pool and
use it for both the player-slice width and the session limit. This preserves
saved slot identities when the current preference differs, including saves
made at 250 before the default became 1000. The Modern and Community 3.9
skirmish writers record the actual session limit in that same existing item so subsequent
saves remain consistent even if the caller's configured word differs.

**Strict baseline.** Strict 3.1 retains the configured-word writer and sizes
skirmish pools from the pre-load setting; the saved word only affects the
next battle `[08 R-SESS-01 §9]`. All modes retain the existing successful-load
update of the host's configured word. Campaign pools still use the mission
OTA and campaign writers still record the configured word; continuation saves
have no battle pool.

Community 3.9 binds the same `ModernUnitLimit` implementation through the
existing `RuleSet`. A saved layout takes precedence over its entry table's
unit limit (including the shipped 1500), without changing the table or the
selected gameplay rules. Resaving records the restored live width. This is
Nanolathe save compatibility policy, not a claim about the Community patch.
Selection occurs before allocation and consumes no RNG or resources.

**Boundaries.** Missing or zero saved limits retain the pre-load setting
(zero cannot describe a usable player slice; early Nanolathe writers emitted
it). A nonzero saved limit must be 1..3276, so ten slices fit positive signed
16-bit occupancy identities. Reject out-of-range values rather than clamp
and change slot boundaries. Existing unit reservation still rejects any
owner/slot disagreement with the selected layout. Retail can write a configured
word different from its live pool after an earlier load; such files cannot
always establish their original width from this item. Do not infer a width
from the largest surviving unit slot or retry guessed limits.

**Verification.** Both decisions — whether the writer records the live limit
and whether a load may size its pool from a saved one — are asked through the
session's `UnitLimitRules` seam, so the policy is selected once with the rest
of the rule set ([DESIGN_GAMEPLAY_RULES](DESIGN_GAMEPLAY_RULES.md)) rather
than by testing the mode word at each site. `TestModernSaveUnitLimitSelection`
locks presence, bounds, campaign exclusion and the Strict bypass at the load
site, and `TestReservedRuleSetsMatchTheModeVocabulary` locks the two answers
each reserved set gives. `TestModernSaveLoadsAcrossUnitLimits`
writes both 250- and 1500-unit layouts in Modern and Community 3.9 with a
deliberately different configured word, then loads each under both modes and
the other preference, including resave metadata. It checks saved identities
and the initial committed frame, compares resources, both RNG streams and the
scheduler against a Strict load at the matching limit, and rejects the
mismatched Strict load. These
run with the session package in `tools/check` and `tools/check-retail`.

### Modern wave air targets

**Nanolathe Modern policy.** A computer player's units that have no weapon
able to engage aircraft do not go after aircraft. The user chose the rule:
units without anti-air should not chase aircraft, while units whose weapons
can shoot aircraft — lasers, for example — still may.

**Strict 3.1 behavior.** The wave task orders every member of an attacking
group to attack the hostile nearest the group's centre, and the explore task
sends a small scout group to that hostile's position; the helper considers
every non-allied, non-immune, uncloaked unit, aircraft included
`[08 R-AI-01 §9]`. When the nearest hostile is an aircraft, members that cannot
shoot it chase it anyway. In the simulation benchmark's three-army battle most
chasing time was ground units without anti-air pursuing aircraft.
`ai.RetailPlanner` keeps this, and Strict 3.1 and Community bind it.

**Modern behavior.** The Modern rule set binds `ai.ModernPlanner`, which runs
the retail step unchanged except at those two broadcasts. When the chosen
hostile's committed mover mode is airborne (2, the operand of the retail
`toairweapon` gate `[06 §3.1]`), each member is asked
`Manager.CanPursueAir(member, target)`; a member that can engage it is ordered
at it as retail orders every member, and one that cannot is ordered at the
hostile nearest the group centre that is not airborne (found once per
broadcast, only when needed, with the same walk and tie order), or given no
order when there is none. The session binds the predicate to
`combat.ModernAirPursuitAdmits`: some enabled weapon slot passes Modern
combat's out-of-range response gate (`combat.ModernResponseAdmits`: the weapon
can damage the target, is not command-fire or an interceptor, the target is not
in the slot's authored bad-target categories, and the retail water/air gate
admits it) and its weapon is not ballistic. A ballistic lob reaches an
aircraft only low, close and slow, so it is no anti-air capability to chase
with; a laser or missile that the retail acquisition gate does not refuse
against a flying target is. The target choice, the task cadence and every
simulation-stream draw are the retail step's.

**Measured effect.** The simulation benchmark's three 250-unit computer armies
(`TestStuckCensus` on research branch `research/path-round4`, 4,200 ticks,
seeds 7, 11 and 23; retail planner against Modern planner, both under the
Modern rule set otherwise): unit-ticks spent stuck 114,845 / 94,758 / 154,782
→ 74,345 / 78,038 / 72,378 (−38% on average), unit-ticks spent chasing
1.19 M / 0.99 M / 1.50 M → 0.94 M / 0.90 M / 0.86 M, and the share of chasing
ticks with the target in weapon range 10% → 14%. Battles are bloodier: fewer
units survive the window, because members fight grounded hostiles instead of
trailing aircraft.

**Boundaries.** Only the wave attack broadcast and the explore move are
affected; a unit's own weapons still acquire aircraft by the retail gate, and
a member with anti-air still attacks the aircraft the retail step chose. A
member given no order keeps whatever it was doing. The predicate is bound per
tick when missing, so a restored manager asks it as a fresh one does; the
policy flag lives only for the duration of one Modern step and is never
saved.

**Determinism and fingerprints.** The extra walk is in the retail helper's
player-then-slot order and draws nothing. Strict, Community and Modern
fingerprints do not move: no wave or explore broadcast in the locked battles
chooses an airborne hostile (a counted Modern run of the long Ashap battle
made 15 such decisions, none at an aircraft).

**Verification.** `ai.TestModernWaveAirTargetsSplitByCapability` (an anti-air
member is ordered at the aircraft, one without at the nearest grounded
hostile or not at all; the retail step and grounded targets never split),
`combat.TestModernAirPursuitAdmitsByWeaponKind` (direct and anti-air weapons
qualify; ballistic, water and command-fire weapons do not),
`session.TestBindRulesProjectsThePlannerOntoEveryComputerPlayer`, and
`ai.TestOrderSubmissionSeamCarriesOnlyMoveAttackPatrolCodes`, which now audits
the split broadcast's call sites too.

### Modern AI computer player

**Nanolathe policy (user-authorized 2026-09-24 and 2026-09-25), research
prototype.** The Modern AI computer player replaces a computer player's
decisions with the util+tac brain — utility-scored economy and production, a
tactical army — hosted by `internal/aikit` and installed by `mods/aikit` as
the Modern AI's think step (`session.RegisterModernAI`). On 2026-09-24 the user approved,
for it, a private random generator, controller state of its own and
background thinking: "Modern doesn't have the same limitations as 3.1 or
community - we should use whatever we can to make the gameplay experience
better." On 2026-09-25 the user made it a choice per computer player in every
gameplay mode: "merging an initial version of this AI to main and having it
be selectable per player in skirmish and survival. Ideally we can select it
per player, e.g. one computer player could use classic, another our modern
AI". Later that day the user retired the rule set that had selected it for
every computer player ("Should we get rid of that explicit rule set? I just
want to be able to select the class vs modern AI per player and that defines
how the AI will play"), chose to pay a Modern AI player in full in every
mode ("Full in every mode"), and switched old settings rows to Modern. Like
mutators and Survival it is therefore a mode-independent exception
(AGENTS.md): a lobby choice about who plays, not a rule.

**Strict baseline.** A computer player marked Classic — the zero value, and
every computer player a setup, a fixture or a save does not mark — runs its
rule set's own think step: the retail planner (§3.3) under Strict 3.1 and
Community 3.9, the retail planner with
[wave air targets](#modern-wave-air-targets) under Modern; and it is paid by
its rule set's income answer, the retail difficulty discount in every
reserved set. A battle whose computer players are all Classic binds no
controller and pays nobody in full, so Strict 3.1 with only Classic players
is the retail baseline and every fingerprint lock runs Classic.

**Per-player selection.** Each computer player is Classic or Modern
(`ai.Controller`, zero value Classic).

- *Where the choice comes from.* The lobby row carries it
  (`session.SkirmishPlayer.AI`): the skirmish and Survival setup screens
  ([DESIGN_INTERFACE_HUD_INPUT §2.6](DESIGN_INTERFACE_HUD_INPUT.md#26-cmdnanolathe--the-front-end-screens)
  "Computer AI"), where a newly added computer row starts Modern (user
  decision 2026-09-25), and the settings file's skirmish rows: a Classic
  computer row stores `"ai": "classic"`, and a row without the key plays
  the Modern AI, so every computer row of a file written before this
  encoding — whose Classic rows stored nothing — loads Modern (user decision
  2026-09-25: old settings rows switch to Modern). `"modern"`, the word the
  first per-row encoding wrote, and any other word also read as Modern; the
  writer omits the key for a Modern row and for a row that is not a
  computer player. A battle the command line composes takes
  `--ai-player <row>=<classic|modern>` or `--ai-player all=<classic|modern>`
  (repeatable, on `nanolathe` and `nanolathe-headless`): rows are numbered 1
  to 10 as the lobby numbers them — row 2 is a `--map` skirmish's computer
  player, rows 2 and 3 a Survival battle's buddies — and `all` marks every
  computer row of the battle, which a named row overrides in either order. A
  row that is not a computer player of the battle, a row or `all` named
  twice, an `all` that finds no computer row, or a word other than `classic`
  or `modern` stops the start with the command's diagnostic. Campaign
  computer players have no lobby row and are Classic. The AI arena keeps its
  own contestant syntax.
- *Battle entry.* After the managers exist and before the entry prime can
  step one, the session marks every live computer row's manager
  (`ai.Manager.Controller`). The human's row, an observer's and the Survival
  attacker's passive manager are never marked.
- *Which think step.* A player marked Modern runs the Modern AI's think
  step, which `mods/aikit` installs in the session's one slot for it
  (`session.RegisterModernAI`), whatever set the battle binds; a Classic
  player runs the bound set's own. The session projects this on every bind
  (`Session.plannerFor`), so the mark survives a rule-set switch at the
  command boundary: the Modern player keeps its controller, the Classic
  players change step with the set. A build that does not link `mods/aikit`
  refuses to compose a player marked Modern rather than playing it as
  Classic.
- *Rules.* A Modern player plays under the bound set's rules. In a Strict 3.1
  battle it plays under Strict 3.1 — every seam answers as retail, and only
  who decides differs; under Modern it plays under Modern, including
  [Modern AI move retention](DESIGN_UNITS_ORDERS_COB.md#modern-ai-move-retention),
  the one order policy that covers Modern AI players alone.
- *Difficulty and income.* The battle's difficulty picks the persona (below)
  in every set. A player marked Modern is paid in full in every rule set,
  Strict 3.1 included (user decision 2026-09-25, "Full in every mode";
  [Modern AI full income](DESIGN_ECONOMY_CONSTRUCTION.md#modern-ai-full-income)):
  its personas were measured on full income, so its difficulty comes from
  the persona alone. A Classic computer player is paid by the bound set's
  answer, the retail discount in every reserved set, so Classic players are
  credited exactly as before, beside a Modern one or not.
- *No rule set chooses it.* The gameplay selection — `--gameplay`, the
  settings file's `gameplay`, the options page — chooses rules only; each
  computer player's lobby mark alone chooses its AI. The `modern-ai` rule set
  that once put every computer player on the Modern AI was retired on
  2026-09-25. `--gameplay modern-ai` is refused, naming its replacement,
  `--gameplay modern --ai-player all=modern`, which plays the same battle:
  Great Divide seed 7 at 27,000 ticks gives the state hash the retired set
  gave (`e658c98a9a8ffb3c`), and the report differs only in `rules`. A
  settings file whose `gameplay` is `modern-ai` loads as `modern` with every
  skirmish row Modern, Classic rows included, the game it selected, and the
  next save writes it that way; refusing it would have stopped the game at
  start-up over a preference. A save's sidecar naming the set is treated as
  "Saves" below says.
- *Who counts as a Modern AI player.* `Session.ModernAIPlayer`: a live
  computer slot (control byte 2), not the Survival attacker, whose bound
  think step says it runs the Modern AI for that manager (`ai.ModernAIStep`:
  the Modern AI's step always does, the arena's host step for a player the
  arena gave a brain). The headless report names such a player
  (`"ai_controller": "modern"`) and says nothing for any other, so a battle
  of Classic players reports exactly what it did before.

**Modern behaviour.**

- *Persona.* The lobby difficulty picks the easy, medium or hard persona:
  how often the brain looks, its reaction latency, its action budget, how
  many operations it runs at once, its skill refinements, and its ambition —
  how much of a top human's plan it attempts. Calibration and results are in
  [MODERN_AI_RESEARCH](MODERN_AI_RESEARCH.md).
- *Income.* [Full income](DESIGN_ECONOMY_CONSTRUCTION.md#modern-ai-full-income)
  in every rule set, so difficulty comes from the persona alone.
- *Observation.* Its own units; hostile units in its own sight and untyped
  contacts inside its own radar; remembered sightings; allied units in its
  sight, by the same predicate (a Survival team's shared sight,
  [DESIGN_SURVIVAL §4.3](DESIGN_SURVIVAL.md)). Its sight is its own
  coverage in both visibility modes — under Permanent LOS its own mapping
  bit, where the retail predicate reads the local viewer's `[03 §3.2]`.
  Start positions and metal spots are public map knowledge.
- *Start assignment.* With pre-determined starts (the lobby's `Location`
  setting, where slot *i* takes start *i*), every player can read from the
  lobby which start each opponent took, so the session tells the controller
  too (`ai.Manager.StartOwners`, published at skirmish placement). Its army
  and scouts then look for an enemy base only at an opponent's start instead
  of walking every start on the map (user decision 2026-09-24). Under random
  starts, on a restored battle and in a campaign it is not told and searches
  the starts. The arena's `-starts slot` is pre-determined; `random` and
  `swap` are random placements.
- *Action.* Player-level orders through the ordinary order and construction
  paths, applied after the persona's reaction window and revalidated against
  the live world, then the retail step's engine upkeep.
- *Randomness and threads.* One private generator per computer player
  ([INVARIANTS.md](INVARIANTS.md) I4 "Modern AI exception"); an optional
  background think whose result equals the synchronous one
  ([DESIGN_GAMEPLAY_RULES](DESIGN_GAMEPLAY_RULES.md#the-modern-ai-controller)).

**Long games, low action budgets and teams.** Nanolathe Modern AI policy.
These are decisions of the brain, not gameplay rules: they use the
ordinary order and construction paths and add no rule seam, simulation
RNG draw or per-tick state.

- *Idle builders.* Ordinary constructor production stops while two or more
  idle economy constructors of the product's domain are already waiting:
  builders that must stand in water are counted apart from all others, and
  the commander is not counted. Advanced constructors stay on the tech
  plan. While at least two builders are idle the economy also spends one
  action on a group order, the human box-select and right-click: a Guard of
  the producing factory with the most idle builders within 1200 wu, and from
  minute 20, when no factory takes them, a Repair of the damaged own or
  allied unit or unfinished frame with the most. The commander and builders
  that cannot guard are left out, and the group runs after the previous
  think's failed builds are processed, so it hides no failed site from
  backoff, placement-failure counts or stuck-builder detection. Reclaiming
  stays a per-builder decision, which rests after a clear that found
  nothing.
- *Action reserve.* The economy reserves one action for the army only while
  the base is threatened, and at most one for a factory with an empty,
  unstalled queue, so a one-action persona's builders are not left
  waiting behind the army and production.
- *Commander safety.* The commander's retreat test runs before both
  reserves and takes the think's action when it fires, so a one-action
  persona can pull it home while the base is under attack. It fires
  more than 350 wu from home, or below 60% health, where enemy threat is
  at least half `threat_half` and outweighs own armed power. No commander
  work site (a build, an extractor, a tower, a gate or a reclaim) is
  offered beyond 350 wu of home where that test already holds. The leash
  is `com_radius` or, when farther, the distance from home to the
  second-nearest land metal spot, fixed when home is: at a start chosen
  away from the map's metal the commander, the opening's only builder,
  otherwise had no extractor it could build.
- *Production batches.* A persona below 100 APM (Easy and Medium) queues a
  chosen combat unit two at a time with one action, within any
  restriction cap; builders, scouts and every product of a faster persona
  are queued one at a time.
- *Long-game growth.* `growth` acts for every ambition from minute 20, not
  only at full ambition, while the metal store is 80% full: the
  `long_growth` switch (default 1) then supplies its expansion, tech-2,
  constructor-ratio and factory parts to the lower personas, so metal
  their smaller plan leaves banked is invested and metal the army needs
  is not. `long_growth=0` keeps the full-ambition gate. The tier curves
  still scale the counts by ambition, so a lower persona's plan before
  minute 20 is unchanged.
- *Factory pads.* A pad clear moves every unit parked on a stalled pad,
  and idle units in the short lane in front of it, in one group move to a
  rally behind home, a quarter of the way from home away from the enemy.
  A factory written off as walled in is cleared too, so its queue resumes
  if its exit opens. A stalled pad gets one builder Unblock before two
  clear attempts; the executor does nothing when the exit is open. While
  at least two pads are stalled and at least half the factories are,
  growth adds no factory.
- *Standing home guard.* From minute 20 the tactics army keeps 10% of its
  value at home at every attention, within the 15% defend share; the guard
  otherwise forms only at Attention 3 after an incident. An under-strength
  guard is filled from main and raid squad members nearest home.
- *Approach warning.* Incidents begin only at an asset, the base or the
  commander, so an army closing on home was answered only once it stood
  among the buildings. Armed enemy ground units within 2400 wu of home
  that are not yet an incident — in sight, on radar, or seen in the last
  five seconds — form one approaching force. When it is worth at least
  600 and what stands a third of the way toward it (towers, commander and
  home guard) holds less than 1.5 times its strength, the home guard and
  the main army defend that point. A squad answering an incident keeps
  it, the main army keeps a fight within 1200 wu of the force, and a main
  army no more than 600 wu farther from home than the force is left to
  meet it. `warn=0` turns it off.
- *Radar over home.* From minute 10, while no stationary radar stands, is
  under way or is assigned within reach of home, the economy wants one
  there at need 4000 instead of the ordinary at most 1000, a quarter of
  the way from home to the front rather than halfway between the front
  and the rally point. It comes before routine energy and extractors but
  not before a tower the base urgently needs, and a lost one is wanted
  again. It is what lets the approach warning see an army coming.
  `radar_home=0` keeps the forward radar alone.
- *Allied assets.* Allied buildings and builders in sight are assets for
  incidents and home defence, so a team's army answers an attack on an
  ally's base although it cannot command the ally. Own army value and
  strength stay own-only.
- *Guard capability.* A definition authored `canguard=0` refuses Guard
  (code 7), so no layer sends it one: a support unit that cannot guard
  follows its ward by moving when it stands idle more than 900 wu away,
  and the economy's guard candidate, the group guard and the assist
  builders skip such builders.
- *Resolver diagnostics.* `ApplyStats` breaks `FailResolve` down by command
  kind and by the first actor's role (commander, builder, combat).
  Diagnostic state only.

Strict 3.1 with only Classic players is unchanged, and every fingerprint
lock but the Modern AI's own runs Classic.

Tests: `utility.TestGrowthLongGameFallback`,
`utility.TestIdleBuildersStopConstructorProduction`,
`utility.TestIdleBuildersGroupGuardAFactory`,
`utility.TestIdleBuildersRepairDamagedBuilding`,
`utility.TestIdleRepairFallbackStartsAfterOpening`,
`utility.TestFailedBuildBeforeIdleRescue`,
`utility.TestPadLaneCoversShortExit`,
`utility.TestLowAPMCombatBatch`,
`utility.TestCommanderRetreatsBeforeReserves`,
`utility.TestCommanderAvoidsThreatenedSites`,
`utility.TestCommanderLeashReachesMetal`,
`utility.TestRadarOverHome`,
`tactics.TestStandingGuardAtLowAttention`,
`tactics.TestStandingGuardRecruitsMainSquad`,
`tactics.TestApproachCallsArmyHome`,
`tactics.TestSupportWithoutGuardFollowsByMoving`,
`aikit.TestFailResolveBreakdown`.

**Radar coverage.** Nanolathe Modern policy (user-requested correction,
2026-09-26): the utility economy offers a radar only when its request point
is at least the larger of its authored radar range and each nearby own
stationary sensor's authored radar range away from that sensor. The boundary
is inclusive: exactly one larger radius apart is allowed. Completed sensors
and nanoframes reserve coverage across unit definitions, including radar on
other buildings. Live construction commitments reserve their request points
immediately, so builders in the same think cannot duplicate one another;
on later thinks only a matching build target keeps that reservation alive.
A dead builder, recycled slot or cancelled order cannot hold an empty site.
Mobile sensors reserve nothing because they may leave. Destroyed towers free
their coverage, and uncovered expansion sites remain eligible. A candidate
with no authored radar range is not offered.

This is a construction-planning spacing rule, not a visibility calculation:
it does not model elevation, activation or jamming, change actual sensor
reach, reclaim existing towers, or search for a different radar site when the
chosen point is covered. The existing time, count, affordability, travel and
threat scores still rank an eligible tower. Evaluation draws no RNG and
spends no resources; an accepted order uses ordinary construction. It belongs
to the Modern computer player in every gameplay mode, including Strict 3.1;
Classic planners retain their bound rules and the retail baseline is
unchanged. `utility.TestRadarCoverage`, `utility.TestRadarReservations` and
`utility.TestRadarRebuildAfterLoss` lock spacing, cross-definition coverage,
unfinished and pending towers, reservation lifetime and replacement after loss.

**Configuration** (user request 2026-09-24: "keep some of the AI behaviors
& personalities configurable (not in the UI). By default we could randomize
it, so each round the AI might behave slightly differently but overall still
smart"; and 2026-09-25: "Maybe for the variety we don't need completely
different styles, just weights between them changing a bit. So some raids
still happen, some towers still get built, etc, just at different rates to
randomize things a bit."). The brain's parameters may be set per battle
without any screen. With nothing configured every game plays the balanced
style (the tuned opening), jitters its opening and waves, and draws a
personality: eight traits (aggression, raids, towers, expansion, tech,
heavy units, air, scouting), each drawn on its own around neutral, which
move the rates at which the same brain raids, builds towers, expands and
so on — nothing is switched off — all from the player's private
generator. Each trait's range was measured to keep the brain as strong as
without it ([utility README §13.15](../internal/aikit/brains/utility/README.md)).

- *Keys.* One vocabulary, the AI arena's player-spec keys for `util+tac`.
  The game and the arena build the brain with the same builder
  (`mods/aikit` `NewUtilTac`), so a key means the same in both. Each layer
  lists its own keys: the utility parameters are `utility.Specs`
  (`internal/aikit/brains/utility/params.go`: name, default, range and
  meaning); the variety switches are those `utility.VarietyFrom` reads
  (`style=<name|random>`, `jitter=0|1`, the attack-value switches, the
  front rules, the opening switches, and the personality:
  `personality=<name|random|off>` and one key per trait,
  `trait_aggression`, `trait_raids`, `trait_towers`, `trait_expansion`,
  `trait_tech`, `trait_heavy`, `trait_air` and `trait_scouting`, each
  −100..100 with 0 neutral, `utility.TraitKeys`; the styles are in
  [MODERN_AI_RESEARCH §8](MODERN_AI_RESEARCH.md#8-variety-and-ambition-utility-brain),
  the personalities and what each trait moves in the
  [utility README §13.15](../internal/aikit/brains/utility/README.md));
  the tactics switches and knobs are listed at the top of
  `internal/aikit/brains/tactics/README.md`; the survival brain's keys,
  which only a Survival battle's computer buddies read, are
  `survival.Specs` (`internal/aikit/brains/survival/params.go`). The arena's persona keys
  (`think`, `apm`, `skill`, `ambition` and the rest) and its `label` are not
  keys here: the lobby difficulty picks the persona.
- *Strict check.* `mods/aikit` `ValidateParams` refuses an unknown key and a
  value its layer would not use as written: a value other than a style
  name, a personality word or `pv`'s word that is not an integer spelled
  plainly (`+1` and `01` are refused, because the layers read them
  differently), a utility parameter outside its documented range (the
  arena clamps it; a configuration is refused), a switch other than `0` or
  `1`, a personality other than an archetype's name, `random` or `off`, a
  trait outside −100..100, a tactics knob that is not a
  32-bit integer of the least value the army reads (`hn`, `sm` and `raidv`
  at least 1, `hv` at least 0), or `pv` other than `main`. `mods/aikit`
  `TestTheVocabularyIsWhatTheLayersRead` fails when a layer reads a key the
  check does not name.
- *The settings block.* The settings file's `modernAI` key has three
  layers of key/value pairs; a value may be a JSON string or number:

  ```json
  "modernAI": {
    "all": {"jitter": 0},
    "difficulty": {"hard": {"style": "units", "w_army": 120}},
    "players": {"3": {"style": "eco"}}
  }
  ```

  `all` applies to every computer player; `difficulty` by the battle's
  difficulty (`easy`, `medium`, `hard`: the skirmish lobby's word, or the
  campaign difficulty), read as the persona reads it
  (`session.ControllerDifficulty`); `players` by slot, numbered 1 to 10 as
  the lobby numbers its rows (Nanolathe decision; the session indexes slots
  from 0). For each key the most specific layer that names it wins: the
  slot's, then the difficulty's, then `all`. In the example the computer
  player in row 3 of a hard battle plays `jitter=0,style=eco,w_army=120`. No
  screen edits the block; the front end writes it back unchanged.
- *The flag.* `--ai key=value[,key=value...]` (repeatable; a later value
  for a key wins) sets every computer player's parameters, which a Modern
  AI player's brain reads, e.g.
  `nanolathe --map "Great Divide" --ai-player all=modern --ai style=tower,jitter=0`. As with
  `--mutator`, a flag replaces the saved block for that run, and `--shot`,
  `--film`, `--battle-benchmark` and `--headless` never read the block, so
  they reproduce from their command line.
- *Validation.* The flag is checked while the command line is read, the
  block at start-up, and a save's record when it is loaded (a record this
  build cannot read refuses the load, as an unknown mutator does). An
  unknown key, a bad value, a difficulty other than the three words or a
  slot outside 1–10 stops the start with the command's diagnostic, naming
  the entry (`logical path modernAI.players.3 in <settings file>`); nothing
  is ignored. None of this runs in a tick.
- *Where it lives.* The command resolves the flag or the block into
  `session.AIOverrides` (canonical text per layer) on every battle request
  (`headless.FreshBattleRequest`, then `SkirmishEntryOptions` or
  `MissionEntryOptions`). Battle entry merges the layers once per computer
  player, after the managers exist and before the entry prime can build a
  controller, into `ai.Manager.ControllerParams`: canonical text, keys in
  order (`session.CanonicalAIParams`), never a map ranged in the tick. The
  Modern AI's controller parses that text when it builds its host; it reads
  no file and no setting.
- *Dormant elsewhere.* Only the Modern AI controller reads the text. A
  Classic player carries it dormant, as it carries the private generator's
  seed, so no fingerprint of a Classic battle moves; the arena's brains take
  their parameters from their player specs.
- *Randomness.* The default style is `balanced` (since 2026-09-25; the
  drawn opening archetypes lost points to it); `style=<name>` plays an
  opening archetype in every game and `style=random` draws one per game at
  the share humans play it. `jitter=0` stops the opening and wave jitter
  and the personality's draws: the default personality is then neutral
  and a named one plays its archetype's centres, so
  `style=balanced,jitter=0` is the deterministic brain.
  `personality=<name>` plays a personality archetype, `personality=off`
  none (every trait neutral unless pinned), and `trait_<name>=<value>`
  pins that trait whatever is drawn, leaving the other traits as drawn.
  With nothing configured the game builds the default util+tac brain
  (`mods/aikit` `TestTheUnconfiguredPlayBrainIsUnchanged`);
  `style=random,personality=off` plays the default from before the
  personality game for game (checked: identical arena result JSON).

**Switching.** A player marked Modern keeps its controller and its full
income across every rule-set switch; a Classic player takes each set's think
step and income word. No switch starts or stops a Modern controller: only
the lobby mark chooses it, and nothing changes the mark in battle.
The AI's difficulty interpretation is also an entry parameter, alongside its
merged parameters: Escalation's reversed index is read through the retained
entry Community table, including on restore. Current-rule income projection
remains separate (DESIGN_COMMUNITY_PATCH §4.8).

**Saves.** The retail bank carries nothing of the controller. What a load
needs to rebuild each controller with the same personality is a small record
for the save's Nanolathe sidecar
([DESIGN_MODS_MUTATORS §7](DESIGN_MODS_MUTATORS.md#7-the-save-sidecar)),
`session.AIControllers`, taken by `session.RecordAIControllers` between
ticks:

```json
"ai": {"seed": 3427855529, "generators": [{"player": 1, "position": 1311768467463790320}],
       "overrides": [{"player": 1, "params": "jitter=0,style=eco"}], "modern": [1]}
```

- *The controller choice.* `modern` lists, ascending, the computer players
  marked Modern (`ai.Manager.Controller`); every other computer player is
  Classic. A load given the record marks exactly those players' managers
  again, before any controller exists, and so gives them the Modern AI's
  step, and full income, under whatever set the load binds. A record without
  the list — every record written before the per-player choice — and a load
  with no record (a retail save, or a sidecar without `ai`) restore every
  computer player Classic, since that is the game that was played; the
  settings file's switch of old rows to Modern does not reach saves. A
  sidecar naming the retired `modern-ai` set, which only saves made on the
  development branch before 2026-09-25 can carry, loads under its recorded
  base like any set this build does not link (DESIGN_GAMEPLAY_RULES §6),
  with its computer players as its record lists them. A record that marks a
  player Modern in a build that cannot play the Modern AI refuses the load.
  Survival battles are never saved, so a buddy's choice is never recorded.

- *Restored.* `seed` is the battle seed every computer player's private
  generator was seeded from (`ai.Manager.BattleSeed`): the fresh battle's
  entry seed, or the seed a restored battle carried forward. It is recorded
  whenever the battle has a computer player, even before any controller has
  begun or under a set that binds none, so a later controller still draws
  from the original battle's seed. `generators` holds, for every controller
  that had begun, its generator's position (the PCG32 state; the stream is
  fixed by the slot). Recording waits for the controller's work in flight,
  which changes no game. A load given the record
  (`session.RetailLoadDeps.AIControllers`) puts the seed on every restored
  manager in place of the load's entry seed and each position on its own
  player's manager (`ai.Manager.ResumeGenerator`). The first controller the
  manager builds — usually in the load's battle-entry prime — runs its
  brain's `Init` from that seed, so it draws the same style, opening
  variation (opening lead, opening jitter, first wave factor) and
  personality (the traits, and the temper the tactics army takes from them
  at its own `Init`) as the saved game did, and then continues its
  generator from the recorded position, so
  later draws such as the per-wave attack factor continue the saved game's
  sequence instead of repeating its opening ones. A controller built after a
  later switch starts from the seed like any fresh one. `overrides` holds
  every computer player's configured parameters (`ai.Manager.ControllerParams`,
  canonical text; a player without an entry played the defaults), and the
  load puts them back on the restored managers, so its controllers are
  built as the saved game's were even if the settings file has changed
  since: a load never reads the current `modernAI` block or `--ai`.
- *Not restored.* The controller's memory: its observation history and
  remembered sightings, its plans, squads and builder tasks, and the wave
  factor in effect at the save (the brain starts again from its opening
  factor and redraws from the continued sequence). The home point and every
  other map reading are taken again from the restored world, so the home
  point is where the commander stands at the load, and a draw that depends on
  them — the opt-in drawn first-factory family (`fac_first=5`) — may differ.
  The battle's shared map analysis is recomputed.
- *No record.* A retail save, or a sidecar without the record, loads as
  before: each controller starts from the load's own entry seed, so its style
  and opening variation are drawn again, and plays the brain's defaults (as
  a save without a sidecar takes no recorded mutators). A record written before
  `overrides` existed restores no parameters, which is how its game played.
- *Where it lives.* `session.SaveSidecar` writes the record as the
  sidecar's `ai` value (`save.Sidecar.AI`, raw JSON), and the desktop load
  path decodes it (`session.SidecarAIControllers`) into
  `RetailLoadDeps.AIControllers`; a record that does not parse refuses the
  load like any malformed sidecar value.

**Survival.** A [Survival](DESIGN_SURVIVAL.md) battle's Modern computer
buddies — a buddy the Survival screen or `--ai-player` marks Modern, in any
rule set — play a survival brain instead of the skirmish one (user request
2026-09-24), while a Classic
buddy plays its set's own step. The survival brain is
the same util+tac layers with survival defaults, wrapped by
`internal/aikit/brains/survival` — a compact base facing away from the
human's start site, a tower ring split between the buddies by bearing and
weighted toward the warned directions, wall segments with corridors,
repairs, the commander kept behind the towers and an army that never
attacks out. The session tells every survivor's manager the scenario at
battle entry and hands it each wave warning as the HUD announces it
(`ai.Manager.Survival`); only the Modern AI controller reads it, and
`survival=0` among the configured parameters keeps the skirmish brain. The
design, its defaults and keys (`survival`, `sv_tower`, `sv_walls`,
`sv_claim`) and its measurements are
[DESIGN_SURVIVAL §16](DESIGN_SURVIVAL.md#16-computer-survivors-under-the-modern-ai).

**Boundaries.** Only a Modern computer player's decisions and its income
discount change; human players, Classic computer players and the rules every
player plays under are untouched. The one order policy that covers Modern AI players alone,
[Modern AI move retention](DESIGN_UNITS_ORDERS_COB.md#modern-ai-move-retention),
is a Modern rule and is off under Strict 3.1 and Community 3.9. The AI arena's
`aikit` and `aikit-retail-income` sets — the host planner with whatever brain
the arena installs, with full or retail income for every computer player —
are registered by `cmd/ai-arena` only and cannot be selected in the game. The
arena gives its brains to managers directly without marking the players
Modern, so its set alone decides every contestant's income.

**Verification.** `session.TestComputerPlayerSightReadsItsOwnCoverage`,
`session.TestReplacingThePlannerReleasesItsController`,
`session.TestTeardownStopsEveryAIController`, the income tests of
[DESIGN_ECONOMY_CONSTRUCTION](DESIGN_ECONOMY_CONSTRUCTION.md#modern-ai-full-income),
`cmd/ai-arena.TestTheArenaRegistersItsRuleSet`,
`architecture.TestAuthoritativePackagesStartNoGoroutines`, the private
generator's `aikit.TestRandPCG32KnownAnswer` and `aikit.TestPlayerRandStreams`,
`aikit.TestSharedMapAnalysisEqualsTheHostsOwn` (the battle's one map analysis
gives every player the analysis it would have computed alone),
`aikit.TestRestoredHostRedrawsInitAndResumesItsGenerator`,
`session.TestAIControllerRecordRoundTrips`,
`aikit.TestModernAIPersonalitySurvivesALoadRetail` (a four-player Modern AI
skirmish saved and loaded under another entry seed keeps every computer
player's style and generator position and plays on), for configuration
`session.TestAIOverridesMostSpecificLayerWins`,
`session.TestCanonicalAIParamsIgnoresMapOrder`,
`session.TestAIControllerRecordCarriesOverrides`,
`settings.TestModernAIBlockRoundTrip`,
`cmd/nanolathe.TestModernAISettingsBecomeTheSessionLayers`,
`cmd/nanolathe.TestTheAIFlagWinsOverTheSavedBlock`,
`cmd/nanolathe.TestASaveWithUnknownAIParametersIsRefused`,
`mods/aikit.TestValidateParamsRejectsWhatTheBrainWouldNotRead`, the two
`mods/aikit` tests named above and
`mods/aikit.TestConfiguredStylesReachTheControllersAndSurviveALoadRetail` (a
configured four-player Modern skirmish of Modern AI players plays the
configured styles, and keeps them through a save and a load), for the
personality
`utility.TestPersonalityDrawsAfterTheOthers`,
`utility.TestPinnedTraitsAndNamedArchetypes`,
`tactics.TestTemperShiftsMarginsAndRaids` and
`mods/aikit.TestTheArmyIsTemperedByItsStrategy` (a load keeping every
computer player's personality and army temper was checked on a
four-player skirmish of Modern AI players; a committed lock is owed beside
the configured-styles test), for the per-player selection
`ai.TestControllerWords`, `ai.TestModernAIDecidesAsksTheBoundStep`,
`session.TestTheModernAIStepIsInstalledOnceAndIsNotARuleSet` (one step, not
a rule set, and the retired word is not selectable),
`session.TestAMarkedPlayerKeepsTheModernAIInEveryRuleSet` (a Modern player
keeps the Modern AI's step and its controller in every reserved set, and a
Classic player takes each set's step),
`session.TestApplyAIControllersMarksOnlyComputerRows`,
`session.TestTheSaveRecordCarriesTheModernPlayers` (including the full-income
mark after a load),
`session.TestComputerAIChoicesNameComputerRows` (named rows and `all`),
`session.TestClassicRowsKeepTheSetupBytes`, for income
`session.TestAModernPlayerIsPaidInFullInEveryRuleSet` and
`economy.TestAFullIncomePlayerIsCreditedAsAHuman`, for the settings file
`settings.TestSkirmishRowAIRoundTrip`, `settings.TestOldSettingsRowsLoadModern`
and `settings.TestTheRetiredModernAISelectionLoadsAsModernWithModernRows`,
`cmd/nanolathe.TestGameplayOptionShowsTheSelectedSet` (the retired word is
refused with its replacement), the front end's tests
(DESIGN_INTERFACE_HUD_INPUT §2.6 "Computer AI"),
`mods/aikit.TestMixedControllersPlayAndSurviveALoadRetail` (one Classic and
one Modern computer player under Strict 3.1 and under Modern play, save,
load with the record as they were and without it both Classic, and play
on), `mods/aikit.TestSurvivalBuddiesPlayTheirOwnAIRetail` (a Strict 3.1
Survival battle's Modern buddy plays the survival brain and its Classic buddy
the retail step), and the three reserved fingerprint locks, which do not
move. The reserved sets' Great Divide seed 7 headless reports (27,000 ticks)
are byte-identical with and without the per-player choice and the per-player
income, and the same battle under Modern with `--ai-player all=modern` gives
the retired `modern-ai` set's state hash. Synchronous and
asynchronous hosts are compared by arena runs
([MODERN_AI_RESEARCH §6](MODERN_AI_RESEARCH.md#6-results-2026-09-23)); an
automated lock is owed.

#### Modern AI stockpile weapons

**Nanolathe Modern policy, user-authorized 2026-10-05 (issue #93).**
The Modern computer player builds and operates stockpile launchers and
interceptors in every gameplay mode, including Strict 3.1. This extends the
existing per-player controller exception, not the rule registry. Classic
players retain their planner, including the separately configured ProTA
pseudo-product path (§2.8). Retail stockpile costs, production, targeting and
interception remain `[06 §11.1]` and `[06 §11.2]`.

**Recognition and commands.** Active authored weapon flags identify offensive
stockpiles and interceptors, without unit-name lists. Neither supplies sustained
DPS or ordinary defense/combat/scout value by itself. Own observations copy
three completed-ammunition counts and three positive signed secondary
`BuildWeapon` queue sums; malformed sums saturate rather than wrap. The
controller emits a slot-specific stockpile command through the normal delayed,
APM-limited batch. At application the executor rechecks ownership, allocation
identity, completion and the active stockpile slot, bounds completed plus
pending rounds to 200, and coalesces an ordinary `BuildWeapon` order without
purging primary work. Enqueue grants no ammunition, debits no resources and
draws no RNG. The ordinary production service pays for every round.

**Investment.** Utility economy candidates include one offensive launcher and
one home interceptor. Existing units, nanoframes and build commitments reserve
that budget. Proactive investment begins at fifteen minutes; an identified
enemy targetable stockpile weapon admits earlier interceptor investment. A
launcher also needs a worthwhile known target within its authored range from
the proposed site. A purchase plus its first round must be fundable from stock
while retaining one fifth of resource capacity, or from 180 seconds of gross
income while retaining that reserve and not consuming faster than income.
Each resource is checked separately. The candidate's base score is 1200, or
3000 for defense against an observed stockpile threat, multiplied by the
existing affordability, travel and site-threat considerations. These are AI
planning choices, not historical constants. A mobile interceptor is the
factory fallback when no completed builder offers a land stationary system;
pending factory requests also reserve the one-system budget. It moves toward
home when idle outside half its weapon's coverage. This first policy protects
the home economy; it does not attempt a coverage network for every expansion.

**Ammunition and targeting.** Before new construction assignments, the economy
layer uses its action budget to maintain one offensive round and three
interceptor rounds per active stockpile slot. It requests only one round at a
time and never adds to an outstanding slot queue. Funding uses the same
reserve rule with a 120-second income horizon. The ordinary autonomous scan
operates loaded interceptors. For an offensive primary slot the controller
also issues ordinary ground attacks at the highest-value remembered hostile
building in range, with stable observation-order ties. An untyped radar blip
or guessed start is not a target. Value must cover half the round's
metal-equivalent cost (energy divided by the existing planning rate of 60),
with a minimum of 100. Own units and observed allies exclude an aim point
within half the largest authored area of slots zero and one (ordinary ground
attack binds both, `[04 R-ORD-01 §3]`) plus a footprint pad of eight times the sum
of footprint dimensions and a 64-world-unit reaction margin. This check is
planning from an observation, not a promise about unseen allies or subsequent
movement. After ammunition decreases, or the safe chosen point changes or
disappears, the brain clears its ground order; it waits one minute between
deliberate attack submissions. All stockpile slots receive ammunition; deliberate
aim selection is driven by a primary offensive stockpile, and the third slot
retains ordinary autonomous acquisition. Engine autonomous fire and physical launch
checks are unchanged.

**Boundaries and verification.** All mutable planning state belongs to the
controller and allocation generation. A load rebuilds it from the copied
ammunition and queue observations. Survival buddies inherit the same utility
economy service; the passive attacker does not think. Unit tests cover authored
roles, queue detachment/coalescing, slot and actor checks, budgets, unchanged
enqueue resources/RNG, funding boundaries, target knowledge and clearance,
replacement reservations and generation reuse. Retail integration tests run
the configured brain with the shipped silo and interceptor in all three rule
sets, verify paid production and bounded reserves, and retain Classic controls.
The synchronous/asynchronous host checks and unchanged Classic fingerprint
locks remain part of verification.

#### Modern AI infector focus

**Nanolathe Modern policy, user-authorized 2026-10-05.**
Local squad focus multiplies a visible active infector's existing damage/value
per health score by four, before the current-target 3/2 preference. The
observation copies live, completed, unstunned takeover capability from the
target's bound `orders.Rules.Infection` on the simulation thread; no live state
reaches background thinking. Ordinary captured hosts, Strict/Community/unbound
infection policies and unseen contacts supply no preference. Existing squad
range plus 150, air suitability, static-defense refusal, commander gate,
canonical first-equal tie and 45-tick switching cooldown remain. No strategic
objective or map-wide pursuit bonus, new RNG draw, resource charge or saved
state is added. Tests preserve immunity, visibility and the disabled-policy
bypass alongside the local preference. The observation asks the
definition-level policy, which reads the catalog's compiled infector flag
([Modern infection](DESIGN_UNITS_ORDERS_COB.md#modern-infection)), so it
tokenizes nothing per contact.

#### Modern AI restriction caps

**Nanolathe Modern AI policy, user-authorized 2026-10-05.** Under
[unit restrictions](DESIGN_MODS_MUTATORS.md#15-unit-restrictions) a positive
count caps each player's records of a definition at the allocator
`[05 R-SHARE-01 §8]`. The user decided that Classic computer players keep
retail's behaviour while the Modern AI does not choose a unit once it has
reached that unit's cap. Like the other Modern AI policies it applies in
every gameplay mode, including Strict 3.1, and extends the per-player
controller exception, not the rule registry.

**Classic baseline.** Unchanged. The retail planner reads neither the
restriction set nor the definition's limit field `[08 R-AI-01 §12]`
`[05 R-SHARE-01 §10]`; a capped type it chooses is refused at creation like
any other exhaustion, and its factory shows the caption and retries 300 ticks
later `[05 R-SHARE-01 §8]`. A removed type is absent from the class vectors
compiled over the restricted catalog.

**What the controller reads.** Only its observation, built on the
simulation thread by the host (`aikit` `buildObs`), as for everything else it
knows:

- `UnitInfo.Cap`, immutable per battle: −1 for *No limit*, otherwise the
  definition's `Limit` in the battle catalog. The table is built over that
  catalog, so a removed definition has no entry and no product list names it.
- `Obs.Capped`, one record per capped definition in table order
  (`Table.Capped`), holding the definition's info, `Records` and `Queued`.
  `Records` is the allocator's own census of the observer's slice — records
  whose definition index is that definition, nanoframes and records awaiting
  teardown included `[05 R-SHARE-01 §8]` — read through a `units.World`
  accessor (`DefinitionCount`) that shares the allocator gate's counting
  function, so the number is exactly what the allocator will compare.
  `Queued` is the observer's own requests for the definition that have no
  record yet, read from its units' queues: a factory product's queued count
  less the one whose frame the factory has bound, and a mobile build until
  its frame is placed. A battle without caps leaves the list empty and adds
  no work.

"Its own" is the observer's slice: the allocator counts the creating
player's records only, so in Survival a teammate's units do not count,
although the team shares sight and income
([DESIGN_SURVIVAL §4.3](DESIGN_SURVIVAL.md#43-one-side-against-the-world)).

**Decision.** A product's allowance is `Cap − Records − Pending`, where
`Pending` is the brain's own outstanding requests for that definition that
have no record yet: those already in its queues (`Queued`) and those the
current think has emitted. The host keeps the second count, so every brain
reads the allowance from the kit (`Kit.Allowance`). Its requests are all in
its queues by the next observation, because a think waits for the previous
batch to apply. Where the allowance is zero or less the economy's extractor
and building candidates, the production candidates and the survival layer's
towers and wall pieces do not offer the product, and the survival layer ends
a planned job whose allowance the economy's orders, issued first, used up.
The kit backs this up at emission: a request past the allowance is not
emitted, and a factory request queues at most the allowance. The executor
backs it up again when it applies a batch after the persona's reaction
window: a placement, factory-queue or replacement command whose product's
live census has reached its cap is dropped as stale, like the executor's
other revalidations, before it searches a site or spends anything
(`ApplyStats.Capped`). Commands issued before the cap was reached follow the
ordinary paths, including the allocator's refusal.

**Boundaries and verification.** No random draw, resource charge, rule seam
or saved state is added: the census is copied afresh each observation and a
load rebuilds it, and the brain's reasoning stays a pure function of its
observation, so the synchronous and asynchronous hosts still agree. Classic
players never read the new fields, so no fingerprint lock moves. Tests:
`aikit.TestModernAIRestrictionCaps` (the census and the queued requests reach
the cap, the think's own requests count against the allowance, a request
past it is not emitted, and a stale command is dropped at application);
`units.TestDefinitionCountIsTheAllocatorsCensus` (the accessor equals the
allocator's count with a nanoframe and a dying record present);
`aikit.TestModernAIMeetsRestrictionCapsRetail` (Modern AI skirmishes in
Strict 3.1 and Modern whose players stay within every cap at every think,
reach their extractor and factory-product caps, and play the same battle with
synchronous and asynchronous hosts);
`aikit.TestSurvivalBuddyMeetsItsOwnCapRetail` (a Survival battle in Strict 3.1
and Modern whose Modern buddy reaches a cap on its own slice while its
teammate holds as many of that type); the Classic control,
`session.TestClassicComputerPlayerMeetsACapRetail`
([DESIGN_MODS_MUTATORS §15.11](DESIGN_MODS_MUTATORS.md#1511-verification));
and the existing synchronous/asynchronous arena comparison.

#### Modern AI page products

**Nanolathe Modern AI policy, user-authorized 2026-10-20** (Switeck's TA
Bugfix left the CORE Modern AI without a radar tower). Like the other Modern
AI policies it applies in every gameplay mode, including Strict 3.1, and adds
no rule seam.

**Baseline.** A build page's product gadget builds the product it names, and
CANBUILD is no whitelist for it [07 §9]: a human builds whatever the
selected unit's authored pages install. Each rule's membership is unchanged
([Modern authored build membership](DESIGN_ECONOMY_CONSTRUCTION.md#modern-authored-build-membership)),
and it remains what the Classic AI, the HUD's resource shortcuts and every
other reader use. Switeck's patch strips from `sidedata.tdf` the entries it
expects download records to supply, while its pages still install them
([Extended build menus](../research/extensions/build-menus.md#canbuild-entries-left-to-the-build-pages)),
so a membership-only AI misses products a human can build.

**Policy.** The catalog records each builder menu's `InstalledProducts`: in
the builder's numbered pages `guis/<n>1.GUI`, `guis/<n>2.GUI`, … up to the
first missing one, every button gadget carrying the product-slot attribute
(`commonattribs` bit 4) whose name resolves to a definition, in page and
gadget order. A slot that a download record patches is skipped, since the
record's product replaces its name; page 0 is the orders state and installs
none, and a page that does not parse installs nothing. The Modern AI's unit
table appends these products to the rule's membership, each once, so they are
candidates for every layer that reads a builder's products. Mission
restrictions prune the list with the others; a definition a restriction
removes has no table entry.

**Identity.** Only the Modern AI reads the list, so the frozen content
identity carries it as the AI-family entry `modern/installed-products`,
present when a computer row chose the Modern AI
([DESIGN_MULTIPLAYER §8.7](DESIGN_MULTIPLAYER.md#87-frozen-content-and-build-contracts-for-u4u5)).
A battle without one keeps the identity, and the replays, it had.

**Boundaries and verification.** No random draw, resource charge, rule seam
or saved state is added; the table is rebuilt from the battle catalog.
Classic players and the HUD never read the list, so no fingerprint lock
moves. Tests: `content.TestInstalledProductsReadAuthoredPages` (product
gadgets on authored pages, with patched slots, page 0, pages after a gap,
order buttons, unmarked and unresolved names excluded),
`content.TestFrozenInputsCarryInstalledProductsForModernAI` (the entry joins
the identity only with a Modern computer) and
`aikit.TestTableAddsPageInstalledProducts` (the table appends them after
the membership, once, under every rule).

#### Modern AI walls

**Nanolathe Modern AI policy, user-authorized 2026-10-20** (a play-test:
the Modern AI never built walls, and its army stood at a human's dragon's
teeth being shot instead of breaking through; the user asked for both, a
constructor sent to reclaim the wall included). Like the other Modern AI
policies it applies in every gameplay mode, including Strict 3.1, uses only
orders a human has, and adds no rule seam.

**Baseline.** The retail planner zeroes the score of every `isfeature`
product, so a Classic computer never builds a wall [08 R-P0-05 §5]. A
finished wall piece is a feature. A projectile that strikes it bursts at
its face, and the piece takes the weapon's default damage only when the
impact lies closer to its centre than half the authored area of effect
[05 R-FEAT-01 §8][06 §13.1]. A two-cell piece's centre lies 16 to 23 wu behind the
face, so small arms (an area of 8) never harm a dragon's tooth, light
cannons (32) harm it at about half their rate, and rockets, plasma and
missiles (48) in full. Reclaiming a feature costs a fixed amount of work
taken from its metal and energy, whatever the builder's power [05 R-WORK-01
§5]: under a second for a dragon's tooth.

**Seeing walls.** A brain names up to eight points a think where its own
units stand (`Kit.WatchWalls`). When that think's batch is applied, the
executor lists in `Obs.Walls` every finished feature of a wall product
within `WallRadius` (384 wu) of one of them, once each, at its footprint
centre. A wall product is an unarmed, immobile land product that finishes
as a solid feature and is no economy, factory or sensor building
(`aikit.WallPiece`, read from authored keys). A map feature has no owner,
so a piece within 320 wu of an own or allied building counts as ours and
is never broken.

**Breaking through (`walls`, tactics).** An engaged ground squad watches
around its centre and its member nearest the target. It is held when the
members within 160 wu of a piece that is not ours are worth a quarter of
its present value and that front member has come no 96 wu nearer the
target for 300 − 2 × skill ticks (260 at Easy, 130 at Hard). A held squad:
- fires at one piece by attacking the ground at its centre, with the members
  whose weapons damage walls (above) within their range of it plus 120 wu.
  The piece is the one nearest the members at the wall that does not lie
  behind them, its distance raised by the enemy's static fire reaching it
  in proportion to the squad's own, and halved beside the piece that fell
  last, so the breach opens where towers cover least and an opening widens;
- sends the members that cannot damage the wall and stand in enemy fire
  within 460 wu of it 300 wu back behind the piece until it falls;
- asks the economy for a constructor (`Board.BreachX`);
- pulls back and cools the target as a lost engagement once its square-law
  strength has fallen to half what it was when it was first held, or after
  90 s without a piece falling.

When a watched piece is gone from the list the breakers patrol on to the
target. A squad still held after that picks the next piece; one that is
no longer held for 10 s stops counting as walled. Focus fire is off while a
squad is walled.

**The constructor (economy).** While the army's request is no older than
90 ticks, one constructor goes to the piece (`Kit.Breach`). It
reclaims the wall pieces within 48 wu, nearest first, with ordinary Reclaim
orders. It is the cheapest reclaim-capable constructor (the nearest among
equals), never the commander, and only while more than two constructors
remain. It may be lost, so it does not turn home for the threat that sends
other builders back, and it keeps its orders until they are done. Only one
is out at a time and at most one every 60 s. It is called off once the
army has asked nothing for 15 s.

**Building walls (`wall_build`, `wall_from`, economy).** With the defense
plan, from minute `wall_from` (12), a tower that serves the ground plan in a
zone enemy ground forces have approached gets one segment. The tower is a
ground tower, or a missile tower under the front rules. The segment is
three touching pieces across the zone's observed approach, 96 wu in front
of the tower; neighbours stand a piece's width apart along the line's
larger axis, so diagonal pieces touch without overlapping on the grid.
The tower nearest the enemy goes first, and a tower is never tried twice.
Pieces keep off the corridors the army's own units walk and out of passes.
`Kit.BuildExact` refuses a blocked site, a factory lane, an allied tower's
space or a metal spot rather than move the piece, and a segment needs two
sites. Towers stand at least 160 wu apart, so neighbouring segments leave
a corridor. Only an idle constructor, or one helping a build, builds a
segment: the nearest to the tower. The cheapest wall piece it makes is
used, while the stores or 90 s of income pay for three. There is one
segment every 90 s. A think whose action budget cannot order every piece
queues the rest on the same constructor on later thinks, while it keeps
the job.

**Boundaries.** No random draw, simulation state or rule seam is added; the
breach and wall orders are a human's attack-ground, reclaim and build.
`Kit.Breach` is command kind 21 of the Modern checkpoint vocabulary
([DESIGN_MULTIPLAYER §16.3.23](DESIGN_MULTIPLAYER.md#16323-u5-typed-intent-and-order-receipt-codecs)).
Survival's computer survivors keep both switches off: the survival layer
plans its own wall segments ([DESIGN_SURVIVAL](DESIGN_SURVIVAL.md)), and no
wave stands behind a wall. Classic players are unchanged, so no Classic
fingerprint lock moves.

**Verification.** `aikit.TestWallsListedNearWatchedPoints`,
`aikit.TestBreachReclaimsTheWallPiecesThere`,
`aikit.TestWallPieceReadsAuthoredKeys`,
`tactics.TestWallDamageFollowsTheBurst`,
`tactics.TestBreachPieceAvoidsTowerFire`,
`tactics.TestHeldSquadBreaksTheWall`,
`utility.TestBreachSendsOneConstructor` and
`utility.TestWallSegmentCrossesTheApproach`.

#### Modern AI profile bans

**Nanolathe Modern AI policy, user-authorized 2026-10-21 (issue #148).**
The Modern AI does not build an economy producer that the content's
default AI profile (`ai/default.txt`) limits to none under every
difficulty. Economy producers are extractors, energy producers, metal
makers and storage. Like the other Modern AI policies, it applies in every
gameplay mode, including Strict 3.1. It extends the per-player controller
exception, not the rule registry.

**Why.** Content can ship a unit only so that old recordings play back, and
forbid it to every computer player through its AI profiles. TA Unofficial
Patch 3.9.02 adds the four McnTerra-NS units (QUARK, FLUXOR, ACKFUS and
ARMAIVP) to the build menus this way. Its `ai/default.txt` limits them to
none in the easy, medium and hard plans. QUARK and FLUXOR build in one tick
and yield 25–30 metal and 500–600 energy per second. A Modern AI that took
the menus as offered chose them over every stock producer. In five-minute
skirmishes of two Modern AI players on three maps, each player held 5–13 of
them, and its economy ran away.

**Classic baseline.** Unchanged. The retail planner honours every limit of
its map's profile under its difficulty's plan `[08 R-AI-01 §12]`. It never
reads the Modern AI's table.

**Which limits.** Only the content-wide ones.

- A map's own profile tunes the planner for that map, and its limits of
  none cover stock producers: `waterwrld` limits the land extractors and
  power plants, `metal` the metal makers, and `acid` the tidal and
  underwater economy.
- One plan's limits tune one difficulty: the default profile's easy plan
  limits the cloakable and underwater fusion plants.

The Modern AI reads the map itself and takes its difficulty from its
persona, so it honours neither kind of limit. A producer is banned when, for
each of easy, medium and hard, that difficulty's plan or the `any` plan of
the default profile names it, and each plan that names it limits it to 0.
The limit directive applies only to computer slots `[08 R-AI-01 §12]`, and a
Modern player always is one.

- On a 3.1 install this bans only the geothermal plants (ARMGEO, CORGEO),
  which no Modern brain builds.
- With TA Unofficial Patch 3.9.02 it also bans the four McnTerra-NS units,
  on every map.

**Decision.** When it builds the table, `banForbidden` marks those
producers and takes them out of every build list. The Modern AI then plays
as if the content's menus did not offer them, as with a definition that a
restriction of 0 removes from the battle catalog. Should a layer reach a
marked product another way, `Kit.Allowance` answers 0 for it and the kit
never emits a placement, factory or replacement request for it.

**Boundaries and verification.** Only economy producers are banned. The
profiles' other limits of none (walls, which Survival builds, nukes,
anti-nukes, transports) are left to the Modern AI, which is meant to use
every unit. No random draw, resource charge, rule seam or saved state is
added. The marks are derived from the battle catalog, which every client of
an online room shares, and a load rebuilds them with the host's table.
Classic players never read the table.

Tests:

- `aikit.TestModernAIProfileBans`: the table bans what every difficulty
  limits to none, through each difficulty's plan or the `any` plan, and
  nothing one plan limits alone, nothing limited above none and no other
  role. No build list holds a banned product, it has no allowance, and it is
  never requested.
- `aikit.TestModernAIHonoursProfileBansRetail`: over the retail default
  profile the table bans the geothermal plants and no stock producer. With
  Arm's solar and wind plants limited to none in a cloned catalog's default
  profile, no build list offers them, and Arm's Modern player never holds
  one in a skirmish where it otherwise builds them.

On a TA Unofficial Patch install, `aikit.TestModernAIMeetsRestrictionCapsRetail`
and `aikit.TestSurvivalBuddyMeetsItsOwnCapRetail` pass with the ban. They
failed there on the runaway economy.

#### Modern AI damage hints

**Nanolathe Modern AI policy** (made for the faction add-ons,
[DESIGN_MODS_MUTATORS §5.8](DESIGN_MODS_MUTATORS.md#58-faction-add-ons)).

**The problem.** The Modern AI's unit table rates a unit's firepower from
its weapon records: damage × burst ÷ reload. A unit script can hold fire
between shots. Talon's Salamander sleeps two and a half seconds after every
pair of shells against a 0.2-second reload, so in match-ups it delivers
about a twenty-fifth of its record rate, and the brain built it ahead of
better units.

**The hint.** Content may author the unit key `nl_ai_dpspermille`: an
integer from 0 to 1000, the share of the record rate the unit delivers.
- The table scales the unit's credited damage rates (all, anti-air, water
  and stun) by it.
- A value outside that range is ignored.
- The key is a Nanolathe extension that only the Modern AI reads. The
  simulation never does, and Classic players and the engine ignore it.
- Stock content authors none, so stock tables and every fingerprint lock
  are unchanged.
- The faction add-ons as released author none either; a measured value
  would be content the faction's package carries.

**Every credited rate is scaled.** The hint applies after every rate is
known, including the anti-air credited from damage tables. A flak weapon
that authors no default damage and names the aircraft it hits is anti-air
only: its unit takes the combat or defense role, keeps no ground DPS, and is
worth its anti-air where that is wanted. A tower with only such fire is an
air defense, rated by its anti-air; every stock defense has ground DPS.

#### Modern AI production variety

**Nanolathe Modern AI policy** (made for the faction add-ons,
[DESIGN_MODS_MUTATORS §5.8](DESIGN_MODS_MUTATORS.md#58-faction-add-ons)). The
Modern computer player spreads its factory production over more of each
roster, in every gameplay mode, including Strict 3.1. Like the other Modern
AI policies it extends the per-player controller exception, not the rule
registry. Classic players are unchanged: their planner, its production and
its random stream are untouched.

**The problem.** Production rated each combat product by its
cost-efficiency, normalized to the factory's best, with a variety term on
the product's share of the whole army. A second factory diluted every
share, so each factory settled on one or two units. Cheap fast armed units
were made only as scouts, three a game. Mobile radar, jammers and assist
builders were never made.

**The policy.** The utility brain's `breadth` parameter is a sum of four
parts, all on by default (15). At 0 the brain plays as before, game for
game.

- *Variety (1).*
  - A combat product's share is counted among its own factory's combat
    units, and its variety term halves at a share of 200‰.
  - An efficiency floor uses the product's efficiency with its counters
    (anti-air, range), relative to the factory's best. At a fifth or less
    the product is cut; its weight ramps up to full at two fifths. Units far
    below the best stay out, and an anti-air or long-range unit the counters
    call for stays in.
  - With jitter, each combat product carries a per-game preference of
    1000 ± up to 250 permille: two uniform terms, so most products sit near
    neutral. It comes from one draw from the player's private generator, in
    production's `Init`, which core runs after the strategy's. That draw is
    hashed with the definition's index through the SplitMix64 output
    function. Without jitter there is no preference and nothing is drawn.
- *Cheap fast combat (2).*
  - An armed unit with the scout role (at most 70 metal, at least 60 world
    units a second) that is not wanted as a scout is a combat candidate.
    Its weight ramps out as cheap fast units go from 15% to 30% of their
    factory's combat units.
  - The factory's normalizer is then its best other combat product, so a
    raider does not set the floor the others are measured by.
  - The army makes the first three cheap fast units of a game scouts, the
    old scout allowance, and assigns later ones like any combat unit (a
    fast one joins the raiders).
- *Support (4).*
  - A support unit is a mobile radar whose radar reaches past its sight,
    or a radar jammer. It is a ground, hover or amphibious unit, unarmed,
    without build power, with no other role a layer uses (builder,
    transport, kamikaze, stockpile, interceptor). A definition that costs
    no metal or has a single hit point is a scripted helper and is left
    out. Sonar and sonar jammers serve at sea and are left out.
  - Each factory offers its cheapest support unit of each kind while the
    army wants more of it. One of each is wanted from an army value of
    1500, one more per further 3000, two at most.
  - It is scored at the army share's weight, times the combat energy gate
    and the factory's reach to the enemy. It is not offered while the base
    is threatened, from a stranded factory, or in the opening's army-first
    window.
  - The tactics army has each support unit guard (follow) the main squad's
    ground member nearest its centre; an unarmed guard never joins its
    ward's fight. It keeps that ward while the ward stays a main member
    within 900 world units of the centre, with at least five seconds
    between orders. With no main squad the unit waits at the gather point.
- *Assist builders (8).*
  - An assist builder is a mobile unit with build power that cannot build
    an extractor or energy producer. It is unarmed and not a ship, and it
    gives at least a third of the best constructor's build power per
    metal-equivalent.
  - A unit whose purpose is another mechanic is left out: one that authors
    resurrection or capture; a builder whose products are all buildings the
    economy never places (a minelayer's mines); and one that makes a
    factory, which it deploys into.
  - One is wanted per two live factories, at most four, while metal
    outruns what build power spends: the economy's guard gate on metal
    coverage (0.9 to 2.5) times the army share, at twice the guard weight,
    times the constructors' energy gate. None is wanted while the ambition
    cap holds constructors, while the base is threatened, or in the
    army-first window.
  - Production sends one without products to guard a producing factory —
    the one the fewest of them guard, the nearest of those — or else to help
    the nearest frame of a building within 1500 world units, and
    reconsiders every ten seconds. A builder with products is the
    economy's, as every builder is.

**Boundaries and verification.**
- The policy changes no rule and adds no observation field, resource
  charge or saved state.
- It draws nothing from either simulation stream. Its one draw is from the
  player's private generator, at `Init`, which a load repeats from the seed
  before restoring the generator's position (Saves above).
- A content set without support units or assist builders skips those
  parts, and every cost is integer arithmetic over the observation and the
  table.
- Classic players and every Classic fingerprint lock are unchanged; the
  Modern AI's online lock (`lockModernAIRun`) moved with this policy.
- Tests:
  - `utility.TestBreadthClasses`: the classes from authored keys, with
    resurrection units and minelayers left out.
  - `utility.TestSupportKindsAgree`: production's support test is the
    army's.
  - `utility.TestPreferenceIsOneDraw`: one draw with jitter, none without.
  - `utility.TestVarietyFloorAndPreference`, `TestFastUnitsJoinTheMix`,
    `TestSupportWithTheArmy` and `TestAssistBuilders`.
  - `tactics.TestSupportFollowsMainSquad` and `TestScoutsAreCapped`.

#### Modern AI upgrade modules

**Nanolathe Modern AI policy** (made for the faction add-ons,
[DESIGN_MODS_MUTATORS §5.8](DESIGN_MODS_MUTATORS.md#58-faction-add-ons)). The
Modern computer player builds upgrade modules and keeps one of each upgrade
they make, in every gameplay mode, including Strict 3.1. Like the other
Modern AI policies it extends the per-player controller exception, not the
rule registry. Classic players are unchanged. Production, construction,
attachment and every upgrade's effect stay the engine's and the content
scripts': Talon's module keeps each product it completes and then makes no
other, and the commander's script reads every completed upgrade of its
owner, at any distance, as its shield, drone, missile pack or uplink
(research/extensions/talon-engine.md "Commander upgrades").

**Recognition.** From authored keys alone, never a unit name (`aikit`
`classifyModules`):

- A *fixture* is a definition whose selected footprint is empty, either
  extent zero: it occupies no cell `[04 R-P0-08-C]`.
- An *upgrade module* (`RoleModule`) is an immobile builder with worker time
  whose every product is a fixture and which has no purpose of its own: no
  role from its definition (weapon, economy, storage, sensor, transport,
  commander), no active weapon (a superweapon's is left out of DPS), and no
  air base or teleporter. It makes nothing that leaves it, so it is not a
  factory.
- Its products take `RoleUpgrade` in place of a mobile unit's roles
  (mobile, scout, combat, fighter, bomber): the module keeps them.

With every faction package mounted this finds `TALCOMUPSH` and its five
products, nothing else; stock content has no fixture. Structures that make
fixtures to upgrade themselves are not modules, and their helpers stay
unplanned: the Talon unit pack's Armageddon (armed) and its shells, the
zero-worker sensor plants, and the B.F.G.'s storage, whose production
needs assistance.

**Decision.** Each think rebuilds the picture from the observation:

- A built module of ours is *spent* when a completed upgrade of ours stands
  on its pad (the factory starts its product at its build piece, and a kept
  unit follows the piece it is attached to `[04 §10.2]`), *busy* when an
  upgrade frame stands there or its queue is not empty, and *free*
  otherwise. A spent module's queue counts for nothing: Talon's closes its
  build gate once it holds an upgrade.
- An upgrade the player's build tree reaches is *had* while any own unit of
  it exists, built or framed, or it is queued at a module that is not
  spent. Restriction caps apply (`Kit.Allowance`).
- *Production.* After ordinary factory production, each free module is
  given the first missing upgrade among its products, one action each.
- *Construction.* An idle builder that can make a module scores one among
  its other buildings while the module's missing upgrades outnumber the
  modules that are free, framed or ordered: a nominal weight of 100
  (1000 as the economy scales its weights) times the affordability,
  travel and site-threat considerations every building takes, and nothing
  during an energy stall (the store under a twentieth of its capacity and
  falling, the stall that also pauses combat production). The module goes
  where the economy's default field puts a building, behind the base.
- *Assistance.* A module works on its upgrade at its own worker time
  (Talon's 30: the uplink's 45,001 work units take 25 minutes alone), so a
  builder weighs helping an upgrade's frame as it weighs helping a
  factory's (priority 1500 of the nominal 1000) in the economy's ordinary
  assist choice.
- *Order.* No fixture's definition authors an effect — no weapon, armour,
  sensor, storage or income — and Talon's are script-only, so the brain
  cannot rate them. It takes the least metal-equivalent value first, then
  table order: the least spent on an effect it cannot judge. This is a
  planning choice, not a measure of worth. For Talon: drone and uplink
  (184 each, the drone first by table order), shield (301), missile pack
  (534), emergency teleport (651). The teleport device only acts when its
  module is activated, which the Modern AI does not do yet (stream A, A7);
  it is built last.

**Boundaries and verification.** No random draw, observation field,
resource charge or saved state is added; the picture is rebuilt from each
observation, so a load rebuilds it, and the think stays a pure function of
its observation and the brain's state. A side with no module in its build
tree — every stock side — skips the policy with one test, so ARM and CORE
games and every fingerprint lock are unchanged. Tests:
`aikit.TestUpgradeModulesFromAuthoredKeys` (the rule finds the module and
leaves a mixed factory, an armed structure and a zero-worker builder as
they were), `utility.TestOneOfEachUpgrade` (free, busy and spent modules;
one of each; a spent module's queue ignored) and
`utility.TestModuleWantedForAMissingUpgrade` (modules framed or ordered
this think count; none during an energy stall).

#### Modern AI commands

**Nanolathe Modern AI policy** (made for the faction add-ons,
[DESIGN_MODS_MUTATORS §5.8](DESIGN_MODS_MUTATORS.md#58-faction-add-ons)). The
Modern computer player can switch, cloak and carry units, and cloaks its
units when that helps, in every gameplay mode, including Strict 3.1. Like
the other Modern AI policies it extends the per-player controller
exception, not the rule registry. Classic players are unchanged.

**Vocabulary.** The kit sends what a human's side panel and clicks send,
applied after the persona's reaction window through the ordinary queue
binding, each actor revalidated as for every command; a group command is
one action.

| Kit command | What each unit receives |
|---|---|
| `Load(transport, cargo)` | order code 6 on the cargo: a pickup, or its air twin, when the session's transport admission accepts the pair `[04 R-ORD-02 §1]` |
| `Unload(transport, x, z)` | order code 5 at the point: a ground unload or its air twin |
| `Activate(units, on)` | the `Activate` or `Deactivate` state order, as the ON/OFF gadget sends it; only an on/off-able definition receives it |
| `Cloak(units, on)` | the `Cloak_On` or `Cloak_Off` state order, as the CLOAK gadget sends it; only a definition whose cloak cost gives it the capability the handlers test receives it `[04 R-ORD-01 §2]` |

The state orders keep the unit's queue and head-insert `[04 R-ORD-01 §13]`.
Each own unit in the observation carries what these change: `Active` (the
on/off state), `Cloaked` (the request) and `Hidden` (whether the last
settlement pass paid for it and hid the unit `[05 "Cloak debit"]`),
`Carrier` (the unit this one is attached to as cargo: a transport, or a gate
holding its passenger) and `Cargo` (how many are attached to it); and
`GoalX`, `GoalZ`, the goal of the order `Order` classifies, which a player
sees on a selected unit. No brain loads transports yet.

**Cloak.** A unit whose definition can cloak, other than a kamikaze, has
its cloak requested while it is threatened — it stands in the board's enemy
reach, or moves toward a point in it — and for fifteen seconds after, or at
all times when its definition is authored to start cloaked; otherwise the
request is cleared. The commander is served first, then the other units in
observation order. Each request costs the unit's authored cloak cost, the
moving cost while its order moves it, at every settlement pass, once a
second. What may be spent is the net energy income the paid cloaks did not
already take plus a thirtieth of the store above a fifth of its capacity
(thirty seconds of runway); a request that does not fit is cleared or not
made. A unit not yet cloaked needs half again its cost free, and a unit is
not switched twice within three seconds. A stock commander's moving cost is
1000 a second, so early in a game the budget rarely admits it. With the
gates below the policy spends at most half the economy layer's actions in a
think, rounded up; most thinks it spends none. These are planning choices,
not game rules.

**Boundaries and verification.** No random draw, resource charge or saved
state is added: a request costs what the engine debits, and the memory kept
between thinks (when each unit was last threatened and switched) only damps
switching, so a load rebuilds it. Switch `cloak=0` turns the policy off.
Every stock side's commander can cloak, so ARM and CORE games differ from
before once a cloak fits the budget. The application history's intent
vocabulary (`ai.ModernApplicationIntent`) accepts command kinds 1 to 14, and
these commands are kinds 15 to 20: with checkpoint capture enabled, a Modern
player's first switch or transport command fails the application history
until that vocabulary admits them. Tests:
`aikit.TestKitSwitchAndTransportVocabulary` (kinds, codes, state orders,
groups and the definition filter),
`aikit.TestKitSwitchAndTransportCommandsRetail` (a Talon commander's paid
cloak and its clearing, an on/off radar switched off and on, and an ARM
transport's pickup and unload, each with its observation field, in Strict
3.1 and Modern), `utility.TestCloakWhileThreatenedAndPaid` and
`utility.TestCloakSwitchDwell`.

#### Modern AI gates

**Nanolathe Modern AI policy** (faction balance stream A, unit A7). The
Modern computer player builds a pair of Talon gates, dials one to the other
and sends its ground army through them, in every gameplay mode, including
Strict 3.1, where the pair is found not to carry (below). Classic players
are unchanged. The gate's behaviour is its script's
(research/extensions/talon-engine.md "Gate network"): an ordinary ground
attack dials the gate nearest the aimed point, within 300 world units, and
the aim never fires; a unit within 30 world units of a linked gate's pad is
attached to it and handed to the peer, which drops it on its own pad and
needs no link of its own; off unlinks; and a repeated identical aim heading
is discarded. A move queued behind the pad does not survive the hand-off:
the unit is released idle on the receiving pad, where it blocks the next
release until it moves.

**Recognition.** No definition authors the `teleporter` key for the gate,
and that key has no consumer `[fmt fbi]`. A gate (`RoleGate`) is read from
the three authored keys its mechanism uses (`aikit` `classifyGates`): an
immobile structure that is no builder, authors a transport capacity (the
script's attach and drop need one), carries an active weapon that fires at
units (the dialing device) and can be switched on and off. With every
faction package mounted this finds `TALGATE` alone; stock content has none.
The gate keeps no damage rate, range or other role, so it is neither placed
as a long-range defense nor counted as an enemy threat.

**Decision.** The picture is rebuilt each think: the base gate is our gate
nearest home within 1200 world units, the front gate our farthest one
beyond that.

- *Trips.* A ground combat unit of ours walking to a goal counts once per
  goal as a trip the pair would shorten when the pair can be planned for
  that goal and would shorten that unit's walk (*Sending* below). The front
  gate's site is 300 world units short of the goal on the way from home, so
  the units that gather at the goal leave its pad clear, and it must be no
  more than halfway to the enemy base, no more in the enemy's reach than the
  site-threat half point, and at least 1600 world units from the base gate.
  The base gate's site is 250 world units from home toward the goal and 450
  to the side away from the makers' and storage field. The army layer sends
  new units to its gathering point, so the trips find the front where the
  army goes; the latest recent trip gives the sites.
- *Building.* The economy scores a gate among a builder's buildings once
  three trips were counted in the last two minutes, it is past minute five,
  ninety seconds of income pays for both gates in metal and in energy, the
  energy store is not stalling, and a builder other than the commander can
  make a gate, since the front gate lies beyond the commander's leash: first
  the base gate, then, once it stands or is framed, the front gate at two
  and a half times the base gate's weight, which completes the pair. A lost
  front gate is not replaced for three minutes. Both take the
  affordability, travel and site-threat considerations every building
  takes, and the commander is never sent beyond its leash for one.
- *Dialing.* While both stand, the base gate is given a ground attack at
  the front gate, once per pair; each new dial aims at the next of four
  points within 96 world units of the front gate, so a dial to a replaced
  gate never repeats a heading the script discards.
- *Sending.* Twenty seconds after the dial, every ground combat unit
  walking to a goal is sent to the base gate's pad when the walk to the pad
  plus the walk from the front gate to its goal, with 200 world units for
  the hand-off, is at most 70% of its walk; one group command, at most
  three sent units on their way or waiting at once (the pad carries one at
  a time, and the army takes back a unit left waiting), and a unit is not
  sent again within a minute. A sent unit standing idle within 200
  world units of the base pad three seconds after it was sent (the pad was
  taken when it arrived) is sent onto it again. Units of ours idle on the
  front gate's pad are moved on: a sent unit to the goal it had, any other
  200 world units toward the latest trip's goal, at most two commands a
  think; a builder there is left to the economy. The army sends an idle
  unit on to its own order's point, as for any unit.
- *Failure.* A sent unit that stands free on the base pad for fifteen
  seconds before any unit was carried marks the gates failed for the
  battle: none is sent, dialed or built again. Under Strict 3.1 the gate's
  script never links (Talon has bounded acceptance under Community 3.9 and
  Modern only, [TALON_SUPPORT](TALON_SUPPORT.md)), so a Strict 3.1 Talon
  player spends one pair and stops.

These distances, times and weights are planning choices, not game rules.
The army (`tactics`) is not told about the gates: a unit is sent only while
it walks under an order the army gave, which the army does not repeat while
the unit moves.

**Boundaries and verification.** No random draw or resource charge is
added; the memory kept between thinks (the dial, the trips, the units sent
and when) is keyed by handle and instance, and after a load a standing pair
is dialed again at the next aim point. A side with no gate in its build
tree — every stock side — skips the policy with one test, and its tables are
unchanged. Switch `gates=0` turns the policy off; the gate's role applies
either way. Standing gates are expensive to simulate: the gate's authored
script scans the whole unit-ID range several times a second, once more for
every other gate, so a Modern Talon player's pair adds several milliseconds
a tick and two players' pairs tens (measurements in the
[utility README §14.3](../internal/aikit/brains/utility/README.md)). Tests: `aikit.TestGateRoleFromAuthoredKeys`,
`aikit.TestKitDialsAGateAndSendsAUnitRetail` (a ground attack through the
kit links a placed pair in Community 3.9 and Modern and a unit moved onto
the pad is carried, shown held in the observation, released idle and
moved on; under Strict 3.1 the pair never links),
`utility.TestGatesWantedOnceTheArmyTravels`,
`utility.TestGatesDialSendAndMoveOn`, `utility.TestGatesFailWhenNothingIsCarried`
and `aikit.TestModernAIUsesATalonGatePairRetail` (the util+tac brain dials a
standing pair, sends its army through and a unit is carried).

### Modern chosen start positions

**Nanolathe Modern policy (user-authorized 2026-10-18).** A skirmish player
may choose the start position its commander begins on. Modern and Community
3.9 honour the choice; Strict 3.1 keeps retail's placement, so its setup
screen offers none, its battle entry ignores one a setup still carries, and
its configuration refuses one. The decision is `SeatRules.ChosenStarts`
(`StrictSeats` false, `ModernSeats` true; Community 3.9 binds
`ModernSeats`). It adds no simulation random draw and no per-tick state, and
a setup in which nobody chooses places every commander exactly as retail
does in every mode, so every fingerprint lock is unchanged.

**Retail behaviour.** Skirmish battle entry gives each eligible slot a stored
start number: the slot's own under fixed starts, and a cyclic permutation of
the eligible slots from the loading CRT walk under random starts; a stored
number the map lacks is the verbatim missing-start fatal
`[08 R-ENTRY-01 §5]` `[08 "Randomization for skirmish starts"]`.

**Policy.** `SkirmishPlayer.Start` is a `StartPoint`: unset (Auto), or a
point on the map in world pixels, the units the map's own `StartPos` records
use, where the player put its commander in the setup screen's Place Starts
window. `Validate` refuses coordinates outside `0..StartPointLimit`. When
at least one eligible slot chose a point, `skirmishStartAssignment`
replaces retail's step:

1. Each slot that chose a point begins there. Every authored start within
   `chosenStartClaim` (24 cells, 384 world pixels) of a chosen point is
   claimed by it before the remaining starts are assigned.
2. The others share the authored starts left, by the Location word.
   **Fixed:** each keeps its own number when that start exists and is
   unclaimed; the rest take the lowest free starts in slot order.
   **Random:** the lowest free starts, one per remaining slot, are dealt by a
   Fisher–Yates shuffle on the same disposable loading CRT the retail walk
   uses; the simulation stream is not read. Too few free starts is this
   build's error.
3. Battle entry moves each chosen point onto the map and to the nearest
   cell, ring by ring out to 48 cells, where the commander passes the spawn
   command's placement checks — its movement class's slope and water
   limits, its footprint and the units and features already there
   (`chosenStartPosition`, in the same ring and within-ring order Survival's
   spawns use). The search also excludes reserved commander footprints:
   every assigned authored commander is reserved before allocation, and
   each chosen or generated commander's resolved footprint joins the
   reservations in slot order. An authored commander keeps its exact point
   even when allocated later, so relocation beyond the original click's
   claim cannot put a chosen commander on top of it. Two players who chose
   one point begin side by side. Reservations change no world state, RNG
   draws, resource effects or commander allocation order; a setup without
   chosen or generated starts keeps retail's placement. No cell within
   reach is this build's error.

The Modern AI is told the start owners (`ai.Manager.StartOwners`) under fixed
starts when nobody chose a point; a chosen point is not an authored start,
and an unpublished owner reads as an empty start, so such a battle publishes
nothing and every Modern controller searches. Survival places its own
commanders (DESIGN_SURVIVAL §4.2) and clears any row's choice. The Community
schema's random-start exchange of the last human and computer
(DESIGN_COMMUNITY_PATCH §4.5) skips a slot that chose, and a chosen point
takes no authored start, so no schema placement replaces its commander.

**Where the choice travels.** The setup screen's Place Starts window
(DESIGN_INTERFACE_HUD_INPUT §2.6 "Start positions") and the settings file
(`start` per row with the map the points were placed on, `startMap`) carry
it to `skirmishConfigForStart`, which keeps it with its row through the
compaction; a point belongs to its map, so any change of the setup's map,
including the fallback after the selected downloaded map is deleted, clears
the points, and a settings file whose points name another map loads them as
Auto. Replays carry it as required extension 17 of configuration version 2
(DESIGN_MULTIPLAYER §8.6); a configuration with no choice keeps its
existing version-2 bytes and identity. A save needs none: the bank already holds the
commanders where they stand. The online lobby offers no control for it; a
seat's choice would need a relay seat message.

**Tests:** `TestStrictIgnoresChosenStarts` (the seam's answers, Strict's
bypass with retail's draws, and a Strict configuration's refusal),
`TestUnchosenStartsKeepRetailPlacement` (identity and the CRT walk, with
retail's draw count), `TestChosenPointsUnderFixedLocation` (a point claims
the authored start beside it), `TestChosenPointsUnderRandomLocation` (the
deal replayed from the CRT definition),
`TestChosenPointsRefuseWhatTheMapCannotSeat`, `TestChosenPointPlacesTheCommander`,
`TestChosenRelocationAvoidsLaterAuto` (terrain relocation onto a later Auto
start, full footprint separation with shared chosen points, and unchanged
simulation draws, retained CRT and resources),
`TestStartReservationsLeaveRetailPlacementUnchanged` (ordinary Modern and
Strict's bypass retain exact authored positions),
`TestChosenStartsEncodeAsConfigurationVersion2`,
`TestChosenStartsRoundTripTheLocalAdapter`,
`TestTheSettingsFileKeepsEachRowsStartPoint`, `TestPlaceStartsFollowsTheRules`
and, on the retail art, `TestSkirmishScreenPlacesStartsOnTheMapRetail`, which
places a player through the window's own clicks and composes the screen's
battle under Strict 3.1 (retail's starts) and Modern (the chosen commander
within a few cells of the click).

### Modern extra start positions

**Nanolathe Modern policy (user-authorized 2026-10-18).** A skirmish may
seat more players than its map has start positions. Retail refuses such a
setup at Start with `There are too many players enabled for this map`, and
its battle entry treats a missing start as fatal `[08 R-ENTRY-01 §5]`;
Strict 3.1 keeps both. Modern and Community 3.9 seat the extra players at
generated starts (`SeatRules.ExtraStarts`: `StrictSeats` false,
`ModernSeats` true). Of the 99 stock multiplayer maps, 97 author ten start
positions, Luschinfloggen six and one thirteen, so the policy matters mostly
for eight players on Luschinfloggen and for downloaded maps with few starts.

**Policy.** Whatever retail can place is placed as retail places it, draws
included, so a setup within the map's starts is unchanged. Then:

1. A slot whose retail start the map lacks takes the lowest authored start
   no slot holds, in slot order (`reassignMissingStarts`). With chosen
   points (DESIGN_SESSIONS_AI_SAVE "Modern chosen start positions"), the
   players the free authored starts do not cover are the extra ones: under
   fixed starts the last in slot order, under random starts dealt together
   with the free starts by the same Fisher–Yates shuffle.
2. Each slot still without a start gets a generated one, in slot order
   (`generateExtraStarts`): 32 cells drawn from the disposable loading CRT
   over the usable terrain, four cells in from its edges, are kept when they
   lie in the largest connected region of the commander's movement class,
   so no player begins cut off from the rest of the map, and are tried
   farthest first from every start already decided — authored, chosen or
   generated — taking the first whose nearest cell in that region passes the
   spawn command's placement checks. With no candidate in the region the
   region's cell nearest the map centre is tried; no cell at all is this
   build's error. Candidate searches exclude the assigned authored
   commanders' reserved footprints and earlier generated footprints, so
   terrain relocation cannot select a later Auto commander's cell.
3. The generated start joins the chosen points, and battle entry creates
   the commander on the nearest standable cell as it does for those,
   excluding every authored commander and each earlier resolved commander
   by its full footprint, including when chosen and generated starts mix.

The simulation stream is not read, and the same seed generates the same
starts. A battle with a generated start publishes no start owners to the
Modern AI, as one with a chosen point does not. The setup screen's Start
preflight drops its too-many-players refusal under these rules. The policy
is single-player only: an online battle keeps kind 3's retail answer to a
missing start, the jittered point it draws for every commander
`[08 R-ENTRY-01 §5]`, and its lobby keeps its own capacity, the map's start
positions (DESIGN_MULTIPLAYER §6.6).

**Tests:** `TestExtraStartsReuseUnusedStartsThenGenerate` (unused authored
starts first, then generation, and retail's assignment without the policy),
`TestExtraPlayersBeginAtGeneratedStarts` (four players on a two-start
synthetic map: the generated starts are apart from every other, repeat with
the seed, and Strict 3.1 keeps the missing-start fatal),
`TestGeneratedStartsAvoidLaterAuto` (a region-centre fallback beside a later
Auto commander, with and without chosen starts) and, on the retail
corpus, `TestEightPlayersOnASmallMapRetail` (eight players on Luschinfloggen:
Strict's preflight refuses, Modern composes eight commanders at least 128
pixels apart, the same with the same seed).

### Modern allied sight

**Nanolathe Modern policy (user-authorized 2026-10-18).** Allies may share
line of sight, radar and the explored map, chosen per battle: the skirmish
setup screen's Allied Sight option for a single-player skirmish, the online
lobby host's for an online skirmish (DESIGN_MULTIPLAYER §6.7 "Allied
sight"). Modern and Community 3.9 honour the choice; Strict 3.1 keeps
retail's separate sight in a single-player skirmish, which never merges an
ally's current sight `[03 §3.2]` `[03 R-VIS-01 §7]`, and the approved online
policy's shared sight in an online room, so neither screen offers the
choice there, battle entry and load ignore it, and the configuration
refuses it. The decision is `SeatRules.AlliedSightChoice` (`StrictSeats`
false, `ModernSeats` true). It adds no random draw or per-tick state, and a
battle without the choice is unchanged in every mode, so every fingerprint
lock is unchanged. Survival's survivors always share (DESIGN_SURVIVAL §4.3).

**Policy.** `SkirmishConfig.AlliedSight` makes each team one side through
the vision-team mechanism Survival and online teams already use
(`visibility.Service.SetVisionTeam`): a member's coverage reaches every
member's grids, its explored-map bit every member's history, radar and sonar
from any member's units reveal contacts for all, members' units are friendly
and members' jammers do not blind each other. `setAlliedVisionTeams` reads
each team from the alliance table — the mutual allies of each existing,
non-observer player, which in a skirmish are exactly the players sharing an
ally group other than 5 `[08 R-SKIR-01 §2]` — once at battle entry, before
any coverage is published. A player alone in its group joins no team.
Single-player alliances cannot change in battle, so the teams and the
coverage reference counts stay balanced. The computer players' sight
predicates read the same grids, so a Classic or Modern ally sees what its
team sees.

**Where the choice travels.** The setup screen's button (DESIGN_INTERFACE_HUD_INPUT
§2.6 "Allied sight") and the settings file (`alliedSight`, omitted when off)
carry it to the setup. Replays carry it as required extension 18 of configuration version
2 (DESIGN_MULTIPLAYER §8.6), whose zero value keeps version 1's rule, so a
battle without it keeps its existing version-2 bytes. A save records it in the
sidecar (`alliedSight`, DESIGN_MODS_MUTATORS §7.2): the bank keeps the
alliance rows but not the ally groups `[08 R-SKIR-01 §2]` "Save
persistence", so the load rebuilds the teams from the restored rows before
the restored units publish coverage. A campaign takes none.

**Tests:** `TestAlliedSightTeamsFollowTheAllianceTable`,
`TestAlliedSightIsRequiredExtension18`,
`TestAlliedSightSharesATeamsSightRetail` (a three-player battle on Ashap
Plateau: under Modern the human sees its teammate's commander only with the
option, under Strict 3.1 never, and never the opponent's; also after a
Modern save and a load with and without the sidecar flag), `TestTheSettingsFileKeepsAlliedSight`,
`TestOnlineLobbyHostChoosesAlliedSight` and, on the retail art,
`TestSkirmishScreenSharesAlliedSightRetail`.

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
* Two `TODO(question)` markers in `internal/mission`'s local `InitialMission`
  scanner identify the remaining boundaries: run-specific uninitialized
  coordinate contents, for which zero remains explicit host policy, and
  adversarial decimal rounding through the retail intermediate converter,
  for which Go's binary32 rounding remains the host implementation. The
  former needs execution-state evidence for an individual run; the latter
  needs an arithmetic trace of the decimal converter. Prefix handling,
  integer wrap, scansets, continuation and caller seeds are established and
  implemented `[04 §3.6]` `[fmt ota]`.

No `TODO(question)`, `TODO(T23)` or `TODO(T25)` marker remains in
`internal/triggers`, `internal/save`, `internal/headless`
or `cmd/nanolathe-headless`.

Open questions carried by the contracts above rather than by a marker, each
with the observation that would settle it:

* **The exhaustive placement helper's negative-row outcome.** The loop geometry
  and row test are established; whether an out-of-grid read faults or returns
  a garbage verdict depends on allocator placement. A retail probe with a
  top-row deposit or the plot grid's allocation placement would settle that
  outcome `[08 R-AI-03 §6]` `[08 R-AI-03 §7.1]`.
* **Other readers of the movement class's `MinWaterDepth` word.** The class
  routine, classifier and scatter helper's readers are established; a reader
  census would settle whether more exist `[08 R-AI-03 §6]`.
* **Two order gate-mask bit names the construction task tests.** The
  package consumes the established masks rather than inventing their names;
  a trace over the order descriptor table's consumers settles the names
  `[08 R-AI-01 §17]`.
* **The initial-group low-nibble reader**, and **what a unit-created
  notification means** — every shipped condition ignores that slot, so the slot
  is kept and read by nobody `[08 R-TRIG-01 §11]`.
* **Whether any stock language or patch archive authors the four save-screen
  gadgets SC25 names.** A census of those archives' layouts settles it
  `[08 R-SAVE-02 §1]`.
* **Which retail spans inside a save body remain opaque.** Only those the
  corrections section does not name; every account it names is implemented
  `[08 R-SAVE-02 §13]`.

The Nanolathe policy decision this section listed — a Modern AI player's
income in the reserved sets — was settled by the user on 2026-09-25: a player
marked Modern is paid in full in every rule set ("Modern AI computer player",
"Per-player selection").

Two formerly open items are established: rally probe validation selects its
grid using the session's LineOfSight bit `[08 R-AI-01 §7]`; planner state,
including the wave engagement latch and rally working state, is reconstructed
on load and is not serialized `[08 R-SAVE-02 §11-A]`.

### Zero Classic construction placement

The Zero Alpha 5 content profile explicitly declares
`AIBuilderPlacementLimit=127` in the existing Community feature vocabulary.
`ai.Manager.Community` receives it at composition and rebinding. A positive
numeric limit overrides the older ProTA placement shortcut; zero preserves
its existing five/ten selection. Strict projects zero and therefore keeps five.
Only the first construction pass changes; reposition still starts at five,
so counts five through 126 may reach both independent passes. The selected
Modern AI controller does not call this Classic task. No extra state, RNG draw,
resource mutation or save field is introduced. The feature digest records the
nonzero parameter. The integer is bounded by the signed32 comparison type.

The source contract is [TA Zero, Classic construction cutoff](../research/extensions/ta-zero-engine.md#historical-classic-ai-construction-cutoff).
Focused tests lock the 4/5/126/127 boundaries, unchanged reposition, explicit
numeric precedence and Strict projection. Existing ProTA tests retain its
five/ten behavior.
