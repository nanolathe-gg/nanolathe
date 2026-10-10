# Design — Multiplayer

Battles between people over the network. Every client runs the complete
simulation and only player commands travel: a relay puts them in one order,
tells every client which tick each one runs on, and decides how far the
battle may advance. The model is **relayed deterministic lockstep**.

**What exists.** Online skirmish for 2–10 seats and online Survival for 2–3
survivors under Modern gameplay, with computer players the room's host adds
beside the humans (§6.6), through the hosted relay at
`relay.nanolathe.gg` (§12, §16.5), from the native and browser builds.
Players start a match from the main menu's MULTI entry (§16.6), or from the
command line with `--relay-address` and `--relay-room` (§16.5.4). A local
two-window play test runs over a loopback relay (§16.4). Underneath are the
determinism milestones: one simulation on every host (M1, §16.1),
seat-attributed commands with complete configuration and content identities
(M2, §16.2), and canonical checkpoints with bounded histories (M3, §16.3).
Every skirmish and Survival battle, online or not, is recorded to a local
replay file that plays back in the game or verifies headless (§10). §1 lists what is deliberately not built yet.

This document owns the lockstep model and its determinism contract, the
command stream and its wire form, battle configuration and identity, state
checks and desync handling, replays, joining and spectating, the relay and
lobby protocol, and the threat model. It restates none of the mechanisms it
builds on: the tick, the clock and the random streams
([DESIGN_RUNTIME_DETERMINISM](DESIGN_RUNTIME_DETERMINISM.md)); sessions,
skirmish setup, results and saves
([DESIGN_SESSIONS_AI_SAVE](DESIGN_SESSIONS_AI_SAVE.md)); rule selection
([DESIGN_GAMEPLAY_RULES](DESIGN_GAMEPLAY_RULES.md)); the input queue and the
paused-input boundary
([DESIGN_INTERFACE_HUD_INPUT §3.12](DESIGN_INTERFACE_HUD_INPUT.md));
mutators and restrictions ([DESIGN_MODS_MUTATORS](DESIGN_MODS_MUTATORS.md));
and Survival ([DESIGN_SURVIVAL](DESIGN_SURVIVAL.md)).

Section numbers are stable citation anchors for code and other documents;
a missing number is a retired section.

## 1. Purpose and boundary

**Current scope.**

1. Online skirmish for 2–10 seats with teams chosen in the lobby, and
   online Survival for 2–3 survivors, under Modern gameplay, by
   relayed lockstep through the hosted relay (§12, §16.5, §16.6), from the
   native and browser builds. At least two seats are human; the room's
   host may add Classic or Modern computer players, each with its own
   difficulty (§6.6).
2. The host's map, mod, mutators and unit restrictions, frozen into the
   room's configuration when it is created and adopted by every joiner
   (§8.6, §16.6).
3. Pre-start checks — matching configuration identities and rehearsal
   digests from every seat (§8.2, §16.7) — and a unit checksum every 30
   ticks during play (§16.4).
4. A local two-window play test over a loopback relay (§16.4).
5. Local replays of every skirmish and Survival battle, single-player and
   online, played back in the game or verified headless (§10).

**Designed for later.** The contracts here also cover what the design grows
into, in the order of §16: computer players added by any human, in
skirmish and Survival, under every gameplay mode including Strict 3.1 (§6);
replays checked across platforms (§10); LAN and direct-IP
play through a relay embedded in the hosting client (§12.1); watchers and
spectators (§11.4); rejoining after a disconnect (§11.2); match-wide view
restrictions (§8.4); and eventual competitive play with verified results
(§12.6).

**Not built yet.** Computer players added by a joiner; Strict 3.1 and
Community online;
alliance changes during a battle; watchers and spectators; replay rewind
and sharing;
reconnect, departures and removal votes; room lists, chat and display
names; the embedded LAN relay; pause and speed changes; relay-drawn seeds;
ranked play.

**Out of scope.**

- Interoperating with retail `TotalA.exe`, TA Forever, DirectPlay or any
  other engine, and the retail packet formats (§3.2). Play is
  Nanolathe-to-Nanolathe.
- Retail-format multiplayer saves `[08 "Multiplayer saves"]`, and saving a
  multiplayer battle at all in the first versions (§11.5).
- Co-op campaign missions.
- Server-side fog. Lockstep clients hold the whole state (§13); a server
  that simulates and sends only permitted state is a possible later
  architecture (§12.6).
- Rollback, client-side prediction and client anti-cheat software.

## 2. The design in one page

```
  client A ──commands──▶ ┌───────────────────────┐ ──grants──▶ client A
  client B ──commands──▶ │ relay                 │ ──grants──▶ client B
                         │ orders, stamps, paces │
                         └───────────────────────┘
```

- **Same inputs, same battle.** Every client runs the whole battle from the
  same simulation contract, content, configuration, seed pair and command
  stream, and therefore holds the same state after every tick (§5). Before
  a match starts, the clients' identities, initial unit checksums and
  rehearsal digests must agree (§8.2, §16.7).
- **One stream.** A client sends its player's commands to the relay. The
  relay stamps each with the sending seat and the tick it runs on, and
  grants ticks: no client simulates past the last granted tick, so the relay
  is the battle's clock (§4). Online battles run at normal speed with no
  pause (§4.4).
- **Commands only.** Units, scripts, projectiles and effects are computed on
  each client. Traffic follows commands and their actor lists, not world
  objects.
- **Checked, not trusted.** The clients' unit checksums are compared every
  30 ticks and a mismatch stops the match (§16.4). The complete canonical
  checkpoint and its histories exist for diagnosis (§9, §16.3). Every
  receiver validates every command inside its own simulation, permissions
  included (§7.2). Hashes detect disagreement; they do not prove a client
  honest (§13).
- **Content-free relay.** The relay runs no simulation, never decodes a
  command and never touches Cavedog assets (§12).
- **One simulation on every host.** A window, a headless host and a test
  process compute the same battle from the same inputs. Nothing a host, its
  renderer or its audio device supplies may decide a draw, a pool slot or a
  lifetime (§5.3 L9).
- **Interface state stays local.** Selection, camera, build-menu pages and
  the other interface state never enter the stream (§7.3).
- **Replays use the same inputs**: configuration, seeds and stream (§10).

## 3. Retail baseline

### 3.1 What retail does

Retail multiplayer, **Established** unless marked:

- **Each machine owns its players.** A machine runs the full per-unit work
  only for its local human and the computer players it hosts, then emits
  their unit-state stream; a remote peer's units are puppets driven by that
  stream `[05 R-SHARE-01 §1]` `[04 R-MOV-03 §1]`
  `[08 "Unit-sync ownership and body structure"]`. No orders travel
  `[08 "Command canonicalization"]`.
- **Receivers trust seated peers.** Live-battle receivers recheck no
  ownership, host, watcher, alliance or cheat permission
  `[08 "Packet framing and dispatch"]`.
- **The shooter's machine resolves hits** `[06 R-DMG-01 §9]`, and economy is
  overwritten by periodic copies rather than compared
  `[08 "Economy and integrity checks"]`.
- **Peers do not agree on randomness.** Each machine seeds both streams
  itself; no packet carries a seed `[08 "Lockstep advancement"]`
  `[01 §7.1]`.
- **No input barrier.** A machine 900 or more ticks ahead of its slowest
  peer throttles itself `[08 "Soft pacing — no per-tick input barrier"]`.
- **No state check or repair** `[08 "Full-world hash"]`
  `[08 "No rollback or world snapshot resync"]`.
- **Peer-to-peer over DirectPlay** `[08 "DirectPlay transport"]`, with unit
  identities allocated in transport-identity order.

### 3.2 Why Nanolathe does not reproduce it

1. **There is no exact retail multiplayer outcome to match.** Each machine
   ran its own random streams and saw other players through a lossy state
   stream, so a battle's outcome was assembled from several partial
   simulations.
2. **It is a second simulation mode.** Packet-driven units are multiplayer
   transport outside Nanolathe's scope `[04 R-MOV-03 §1]`; the single-player
   boundary keeps only the packets the local path constructs
   `[08 R-OOS-01 §1]`.
3. **It trusts admitted clients with other players' state**, including
   removal and player-record changes, and compares nothing
   `[08 "Packet framing and dispatch"]`.
4. **It needs a full mesh**, which home routers make hard and which exposes
   every player's address.
5. **It buys no cross-play.** TA Forever admits only clients whose
   executable and DLL hashes match.

### 3.3 What Nanolathe keeps

The player-visible multiplayer rules — lobby options, alliances and
sharing, how a battle ends — are rules, not transport. A Strict 3.1 online
battle keeps the established baseline except the online policies of §15.
Unsettled retail behaviour stays a `TODO(question)`. Established unless
marked:

| Rule | Retail | Online form |
|---|---|---|
| Host options | Every peer copies the host's commander-death rule, cheat gate, mapping and line-of-sight bits and unit limit at battle entry `[08 R-ENTRY-01 §2]`. *Watching allowed* gates watchers; *game closed* gates joins and added computers `[08 R-SKIR-01 §12]`. No command-line word presets them `[01 R-PLAT-01 §2]` `[08 R-SKIR-01 §7]`. | Part of the agreed configuration (§8.1), watching allowed included. Game closed is room admission (§12.4). |
| Commander death | Game continues, game ends (a sweep of the owner's units) or deathmatch (the sweep, then a respawn) `[08 R-SKIR-01 §3]`. | Configuration; the sweep runs for every seat (§6). Online entry refuses Deathmatch for now (§16.4.1). |
| Mapping, line of sight | Mapped or unmapped; permanent, circular or true `[03 R-VIS-01 §1]`. | Configuration. |
| Unit limit | An equal slice per player `[08 R-SKIR-01 §6]` `[05 R-SHARE-01 §7]`. | Configuration. |
| Starting resources | The host's value times 100, at least 200 `[08 R-ENTRY-01 §5]`. | Per-row metal and energy. |
| Unit restrictions | Multiplayer only: a count of 0–100 or none per unit. A 0 removes the definition at battle entry; a positive count caps each player's records at creation. A lobby seeds `wacky` definitions at 0 and never offers `norestrict` ones `[08 R-SKIR-01 §10]` `[05 R-SHARE-01 §8]` `[05 R-SHARE-01 §9]` `[05 R-SHARE-01 §10]`. | `content.Restrictions` on the battle's catalog clone, nothing seeded ([DESIGN_MODS_MUTATORS §15](DESIGN_MODS_MUTATORS.md#15-unit-restrictions)). Online it is field 12, carried from the host (§8.6, §16.6). A restriction lobby screen, and a decision on retail's seeded `wacky` state, come later (Q16). |
| Unit roster | Peers keep only units every peer holds compatibly `[08 "Unit-data negotiation and catalog retention"]`. | Identical catalogs are required (§8.2); roster negotiation is later (Q15). |
| Cheats | The typing machine checks the cheat gate, but the developer phrase unlocks commands regardless; received lines are chat, never dispatched `[07 R-CAM-01 §6]` `[08 "Lockstep advancement"]`. | The agreed cheat permission, enforced by every receiver (§7.1, §7.2). |
| Alliances | Row A holds declarations and row B mirrors them; only the target machine is told, so third machines keep stale copies; computers are allied by teams `[05 R-SHARE-01 §1]` `[07 R-FE-01 §7]`. | Seat commands on one shared directed matrix (Q22, §6.7). |
| Allied sight | Current sight is never merged `[03 §3.2]`. *Share mapping* copies explored tiles every 450 ticks `[05 R-SHARE-01 §6]`; *share radar* marks the sharer's own units friendly `[03 R-VIS-01 §7]`. | Retail eligibility, with Q24's history scope and Q25's request-tick timing (§6.7). |
| Automatic sharing | Every 60 ticks, for the local player, surplus flows to a poorer allied remote human `[05 R-SHARE-01 §3]`. | Every human seat, slots ascending (§6.3). |
| Giving | The share screen gives resources and units `[05 R-SHARE-01 §5]`; a transferred unit is a fresh record, with different copy rules for a remote new owner `[05 R-WORK-01 §15]`. | Seat commands (§6.3). |
| Pause and speed | Watchers may pause; only players change speed; receivers trust any admitted peer. Entry is unpaused at speed 10 `[08 "Lockstep advancement"]` `[07 R-CAM-01 §2]` `[01 §4.3]` `[08 R-ENTRY-01 §2]` `[08 R-ENTRY-01 §3]`. | Normal speed and no pause (Q5); any later support is relay pacing (§4.4). |
| Ending | Each machine judges its own human every 30 ticks; authored triggers are not polled; won and lost paths share one countdown `[08 R-SKIR-01 §3]` `[08 R-TRIG-01 §6]`. The no-live-human site ends the battle on its sixth consecutive true tick `[08 R-SESS-01 §1]`. | Per human seat (§6.3). Shared victory reads Q22's matrix. |
| Defeated players | With watching allowed a defeated player becomes a watcher and is asked to continue watching `[07 R-FE-01 §9]`; one hosting a live computer always watches `[08 R-SKIR-01 §3]` `[08 R-LEAVE-01 §9]`. | Per seat (§6.6, §11.1). |
| Watchers | A seat with no commander that sees everything and is left out of scores `[08 R-ENTRY-01 §5]`; needs *watching allowed* `[08 R-SKIR-01 §12]` `[07 R-HUD-04 §1]`. | Observer rows (§11.4). |
| Resign, host loss, timeout | Resign tears down only the leaving machine; host loss migrates the host flag `[08 R-LEAVE-01 §7]` `[08 R-LEAVE-01 §8]`. A timeout dialog or the host's reject removes a silent machine `[08 R-LEAVE-01 §6]` `[08 R-LEAVE-01 §1]`, destroying its units, transferring nothing, and taking its hosted computers with it `[08 R-LEAVE-01 §3]` `[08 R-LEAVE-01 §9]`. | Rejoin and final removal are distinct; final removal comes from resignation or a vote, never a timer (§11.1). |
| Computer players | One per human machine, hosted by its creator; difficulty is unsynchronized hosting-machine state `[08 R-SKIR-01 §13]` `[08 R-AI-01 §21]`. | Every replica runs each computer with its host's perspective; explicit per-computer difficulty (§6.6, Q23, Q26). |
| Saving | Disabled in multiplayer `[08 R-SAVE-02 §4]`. | Not offered (§11.5). |
| Replays | Retail has none `[08 "Replay"]` `[08 "Bounded absence"]`; the community recorder writes `[fmt tad]`. | A Nanolathe feature (§10). |
| Chat | All, allies, enemies or chosen players; watchers cannot open battle chat `[07 §5]` `[07 R-CAM-01 §6]`. | Relay messages with sender-resolved recipients (§12.2), later. |
| Unit identities | Transport-identity order. | Seat order, as in single player. |

## 4. The lockstep model

### 4.1 Terms

| Term | Meaning |
|---|---|
| Tick | One authoritative sub-tick, 30 per second at normal speed `[01 §4.4]`. |
| Seat | A player slot 0–9. A human seat belongs to one connection; a computer seat to the battle. |
| Stream | The battle's ordered entries, identical for every client and, later, every spectator and replay. |
| Entry | A command or other input with a monotonically increasing stream position, bound to the tick it runs on. |
| Grant | The relay's statement that the entries for ticks through T are complete and those ticks may run. A client never runs an ungranted tick. |
| Pump | One host batch of sub-ticks followed by the executor tail `[01 R-PLAT-02 §7]`. |
| Checkpoint | The state after a completed tick and its executor tail, identified by the tick number (§9.1). |
| Digest | A hash of authoritative state at a checkpoint (§9). |

### 4.2 The stream

The relay owns the stream. Today it holds two kinds of entry:

1. **Command** — `{tick, seat, clientSequence, position, payload}`: a seat's
   gameplay command (§7). The relay sets `seat` from the admitted
   connection; a client cannot claim another seat. The payload is opaque to
   the relay.
2. **Grant** — `{tick, position}`: seals that tick and the complete command
   prefix through `position`. No later entry may be assigned to a sealed
   tick.

Later milestones add **session events** bound to a tick (a resignation, a
final removal after a passed vote, the controller restart of a snapshot
rejoin; §9.1, §11.1), **notes** that change no simulation state (pause,
resume and speed, §4.4; a seat entering the drop state or completing its
rejoin, §11), and, in single-player replays only, **pump ends** (§4.5).
Removal votes themselves are relay traffic and never enter the stream.

**Tick assignment.** The relay assigns each command the earliest tick no
client can yet have run, `lastGrant + 1`, and appends it. Entries bound to
one tick keep the relay's arrival order; one seat's commands keep their
sending order. The stream is append-only. A client learns each command's
stream position from the grant that seals it. A competitive scheduler may
later assign a later tick under a declared policy (§12.6).

**Client sequence.** Each human seat numbers its commands with an unsigned
64-bit sequence that belongs to the seat for the whole battle. The first
command carries 1 and each later one the previous value plus one. The relay
accepts a command only when its sequence is exactly one more than the
seat's last accepted value:

- at or below the last accepted value is a duplicate: it is dropped
  silently, with no stream entry, no second receipt and no payload
  comparison;
- above the next expected value is a gap: the relay refuses it with the
  expected value, stamps nothing, and accepts nothing more from the seat
  until the expected value arrives;
- on a later resume the relay reports the last accepted value and the
  client resends from the next one under the original numbers (§11.2).

One seat's commands therefore enter the stream in numbering order and are
never reordered, skipped or applied twice. The relay never buffers a
command that arrives ahead of its turn.

**Agreed inputs only.** A tick's arithmetic, draws, pool occupancy and
lifetimes depend only on the configuration, the seeds and the entries bound
to ticks up to it — never on when or how a host received them, its wall
clock, its pacing, its renderer or its preferences. What the relay supplies
(tick assignment and stream order today; later the seeds, final-removal
events and controller restarts) is an agreed input: every client applies it
identically at its stated tick, and a replay records it.

**Application.** A client applies the commands bound to tick T in phase 1
of tick T, in stream order, through the typed command path that drains the
input queue
([DESIGN_RUNTIME_DETERMINISM §2.5](DESIGN_RUNTIME_DETERMINISM.md), phase 1).
Nothing else in the stream changes simulation state. In a lockstep battle a
stamped entry waits for phase 1 of its own tick even while the client is in
a menu; it is never applied early, and it holds everything queued behind
it. The client may preview accepted orders. Local network buffers never
enter state checks.

The single-player session keeps its queue and its paused-input boundary
([DESIGN_INTERFACE_HUD_INPUT §3.12](DESIGN_INTERFACE_HUD_INPUT.md)): that
boundary applies the queue's prefix through phase 1's own path with the
number of the tick that has not yet run (`Session.stepPausedInput`), so its
result is what phase 1 of that tick would have produced. Single-player and
lockstep battles run one code path from phase 1 inward.

### 4.3 Latency and playout

A seat's command latency is its round trip to the relay plus its **playout
reserve**: granted ticks the client holds so that jitter does not stall it.
Each seat pays only its own latency; nobody waits for the slowest
connection except when the relay must slow down for it (§4.4). The paced
client driver and its reserve are §16.5.2.

Immediate local feedback hides the delay: the acknowledgement sound, move
marker and queued build outline play when the click is sent. The session
exposes queued-but-unapplied commands to presentation
(`Session.PendingHumanCommands`). Full-world prediction and rollback are not
planned.

### 4.4 Pacing, pause and speed

**Pacing.** The relay grants ticks at normal speed — retail's speed 10,
thirty ticks a second — and accepts no pause or speed request (Q5). Its
grants still wait for a seat that falls behind, and a room with no progress
for too long fails (§16.5.2). Pacing decides *when* ticks run, never *what*
they compute. A battle therefore runs no faster than its slowest
participant can simulate.

**Pause and speed are pacing, not world state.** No phase of the tick reads
the pause bit or either speed word; their readers are the host loop, the
HUD's publication and the single-player paused-input boundary. If pause and
speed are offered later, the relay owns them: to pause it seals its last
grant, appends a Pause note and grants nothing more until Resume; a speed
change alters the grant rate; no grant is ever retracted. Who may pause or
change speed would be a new policy decision; retail's authority is in §3.3.

**Host pacing state stays out of the battle.** The clock's anchor, delta,
fractional carry, pause bit, requested and active speeds, and the load
hysteresis that lowers speed when a host cannot keep up `[01 §4.3]`
describe host timing and are excluded from state checks in every session
kind. In a lockstep battle the host calls that pause or change speed
(`Session.SetPaused`, `Session.AdjustSpeed` — reached from the battle menu,
debug capture and the preferred game speed at entry) never touch the
session; menus keep receiving grants. Retail's multiplayer clock path
(`AdvanceMP`, [DESIGN_RUNTIME_DETERMINISM §3.4](DESIGN_RUNTIME_DETERMINISM.md))
stays unused.

**Later (M6).** A pacing state table: the bounds after which grants slow
for everyone, the waiting-for-player state, and the bound after which a
connected seat that makes no progress enters the drop state (§11.1, Q28,
§19 O17).

### 4.5 Pumps are part of the stream

The executor tail runs once per host pump, after the whole catch-up batch,
using the batch's last tick `[01 R-PLAT-02 §7]`; its last step expires
temporary sight `[03 R-COMP-02 §2]` `[01 R-PLAT-02 §5]`
(`Session.runRetailPostLoopTail`). Temporary sight is visibility state, so
the tick at which it expires depends on how many ticks a host ran per pump.

**In a lockstep battle every granted tick is its own pump**
(`StepGranted`, §16.4.2), and the tail runs after every tick. A one-tick
pump is an ordinary retail pump. A single-player replay records the
host's actual pump boundaries as pump entries (§10). A pump that runs
no tick changes no authoritative state, so it needs no record; the clock's
movement on such pumps is host pacing (§4.4).

## 5. Lockstep identity

### 5.1 The contract

Two admitted Nanolathe clients that start from the same simulation, content
identity, battle configuration and seed pair, and apply the same stream,
hold equivalent authoritative state after every tick; its canonical form is
identical at every checkpoint (§9.1). A Modern controller's private memory
lies outside that form; every command applied for it lies inside. Nothing
else may matter:

- which seat is local, the viewing slot, or whether the client is a
  spectator;
- the camera, the renderer and its options, audio, window size and frame
  rate;
- whether the host has a window, an art cache or an audio device (L9);
- the host operating system and processor architecture;
- host pump sizes, catch-up and frame time (recorded single-player pump ends
  remain inputs, §4.5);
- pause and game speed (§4.4);
- goroutine scheduling, including how long the Modern AI thinks.

### 5.2 What already holds

- **Ordering.** No map iteration or scheduling dependence in authoritative
  packages, enforced by source guards [I1].
- **Arithmetic.** Fixed point for world state, an exhaustive floating-point
  allowlist, and no fused multiply-add in authoritative packages or the
  load-time packages whose output the simulation reads
  (`TestAuthoritativeArithmeticIsNotFused`) [I2].
- **Random streams.** Two session-owned streams seeded from an explicit seed
  pair; presentation draws only private copies [I4].
- **Presentation boundary.** The simulation reads no wall clock, input,
  camera or renderer state [I6].
- **One input path.** Typed commands carrying handles and values, drained by
  one consumer in phase 1 (`internal/session/commands.go`).
- **Computer players.** The Modern AI thinks on a background goroutine, but
  its commands apply at a fixed reaction deadline, and its private
  generator is seeded from the battle seed and the seat [I1] [I4]. Both
  computer players' engine upkeep draws from the simulation stream inside
  the tick, so every replica runs both their upkeep and their decisions.
- **Across architectures.** The fingerprint locks agree between arm64 and
  amd64 builds (`internal/headless/rules_lock_retail_test.go`). That partial
  fingerprint omits projectiles, visibility and the computer players (L5);
  complete equivalence is M3's platform comparison.

### 5.3 Determinism hazards and their rules

Each hazard below has a rule that closes it. M1 implemented L1 and the
timing half of L9 (§16.1); the multi-seat half of L9 and L2 are §6.

**L1 — Floating-point library results differ by architecture.** Go's
standard-library `Hypot`, `Atan2`, `Acos`, `Sincos`, `Sin`, `Cos` and `Tan`
return different bits on arm64 and amd64 (assembly on one, compiler-fused
multiply-adds on the other), and some differences survive the engine's
narrowing: `Hypot(165, 52)` truncates to 173 on one architecture and 172 on
the other. Which result is right is a retail question: retail's distance
routine runs at 64-bit significand precision in a defined rounding sequence
`[01 R-DET-01 §3]` `[01 R-DET-01 §7]`. The rules:

- **Distance** is an in-repository routine that performs retail's rounding
  sequence in integer arithmetic, identical on every architecture, with a
  proven shortcut for truncating call sites (M1-C6).
- **Sine and cosine** of the 65,536 engine angles come from a table built
  once under the fusion guard.
- **`Atan2`, `Acos`, `Tan`** are in-repository implementations compiled
  under the fusion guard, matching the unfused library.
- **Conversions.** Go leaves an out-of-range float-to-integer conversion
  implementation-defined, and the architectures disagree (`int32(3e9)`,
  NaN). Every such conversion goes through a helper with one defined result
  (`numeric.TruncateFloat64ToLow32` and its kin), and the checkpoint refuses
  NaN (§9.1).

**L2 — The local seat is a simulation input.** Retail ran one viewing
player per machine, and the faithful tick inherits places where that player
decides authoritative state. §6 generalizes each.

**L3 — Host pump batching reaches the world.** §4.5.

**L4 — Commands assume one seat.** Commands now carry the relay's seat
stamp and explicit actors (§7).

**L5 — No complete state hash existed.** `Session.PartialStateFingerprint`
deliberately excludes projectiles, visibility, the computer players and
more ([DESIGN_RUNTIME_DETERMINISM §4](DESIGN_RUNTIME_DETERMINISM.md)). The
canonical checkpoint is §9 and §16.3.

**L6 — A restored battle does not continue identically.** Retail-format
saves omit both random streams
(`TestRetailBattleSaveLoadContinuationReport`), so rejoining from a save is
not available (§11.3).

**L7 — AI work has no complete checkpoint representation.** A Modern AI
worker mutates its brain, generator and pending batch before their
deadline (`internal/aikit/host.go`), and saves omit brain memory
(`internal/session/ai_save.go`). The checkpoint therefore hashes what a
controller has had applied, not its memory, and a snapshot restarts every
replica's controllers at one tick (§9.1).

**L8 — Existing hashes are not admission identities.** Unit scripts and
model heights are deliberately absent from `Catalog.Hash`, `SimArt` is
separate from it, and a build revision omits modified source. §8.2 and §8.7
define the complete identities.

**L9 — The effect pool is simulation state.** The fixed active-effect pool
`[03 §1]` — 300 records under Strict 3.1, more under the Community table —
refuses work that draws from both streams when full: a shattering piece
stops at capacity before each refused fragment's eight simulation-stream
draws `[04 R-COB-04 §3]`, and a script explosion or debris impact adds its
land-dust puffer, with its CRT draws, only when its own record was admitted
`[03 R-FX-01 §3]`. A record lives as long as its animation players'
authored per-frame holds `[06 R-WFX-01 §1]`. So:

- The holds are simulation content, compiled into `SimArt` and covered by
  the content identity (§8.7). Every host times the pool from them; no host
  can supply timing (M1-C8).
- The pool, its fragment geometry, and the strip, debris and event-window
  state that feed it live in `internal/effects` under the determinism
  guards and in the canonical checkpoint (§9.1, §14).
- What the viewer gates — the COB effect gate and the audio share of the
  per-tick event window — is the multi-seat half, in §6.3.

Of the locked scenes only the Strict 3.1 benchmark fight fills its pool;
the pool lock uses seed 5, whose pool first fills at tick 2,255 (M1-C9).
The other locked scenes stay below capacity, and the event window stays far
below its bound of 4,096.

### 5.4 Invariant amendments

INVARIANTS.md carries the text of four amendments:

- **I2.** Authoritative packages call no standard-library floating-point
  function except the exactly rounded ones (`math.Sqrt`, `math.Abs`,
  `math.Floor`, `math.Ceil`, `math.Trunc`, `math.Round`,
  `math.RoundToEven`); everything else goes through the in-repository
  implementations of L1. A floating-point value becomes an integer only
  through a helper that defines the result for a value that does not fit or
  is not a number. Source guards enforce both.
- **I6.** Which seat is local, the viewing slot and the host's pump sizes
  are presentation inputs; no authoritative state, draw count or draw order
  may depend on them outside the single-seat equivalences of §6.5. Nothing a
  host supplies after composition — renderer, art cache, audio service,
  preference — may change a lifetime, a pool's occupancy or a draw (L9).
- **I4.** A lockstep battle's seed pair is an agreed input that enters
  composition through the explicit seed handoff (§8.3).
- **I5.** Delayed human-command references carry a deterministic allocation
  serial beside the pool handle (§7.2); slot allocation and internal retail
  references are unchanged. The serial and its counter are authoritative
  state.

## 6. One world for every player

### 6.1 Owner-machine equivalence

In retail every machine ran the work of its own players — its local human
(control 1) and the computers it hosted (control 2) — and saw everyone else
through packets. A lockstep client therefore runs, **for every seat, the
work that seat's own machine would have run for it**, drawing from the
shared streams. Seats run in a canonical order, with each human's block
before the computer seats it hosts, so the settlement freeze starts on the
same due `[03 R-VIS-01 §4]` `[05 R-ECO-01 §1]`. Work falls into four kinds:

1. **Per-owner work** that retail gates on control 1 or 2 — weapons, order
   pumps, movement, settlement, commander creation, planner records, the
   death latch, kill filing, the commander sweep and the cloak-proximity
   pass `[08 R-SKIR-01 §3]` `[03 R-VIS-01 §4]` `[04 R-MOV-03 §1]`
   `[05 R-ECO-01 §1]` `[08 R-ENTRY-01 §3]` `[08 R-ENTRY-01 §5]`. It already
   runs for every seat, because no seat is ever control 3.
2. **Per-viewer work** that retail runs once per machine for its viewing
   player. It runs once per **perspective** (§6.2), one per human seat. §6.3
   lists it.
3. **Per-machine work** — wind, meteors, features, projectiles, effect
   strips — runs once per battle. Retail's split of feature damage and fire
   spread between an authority peer and the others `[05 R-FEAT-01 §8]` falls
   away: there is one world, authoritative everywhere.
4. **Presentation** — what a machine showed its player — stays on the
   client.

Puppet-only machinery — remote units' reduced controllers, their release of
map cells, the unit-state and economy packets `[04 R-COLL-01 §4]`
`[08 "Economy and integrity checks"]` — has no lockstep counterpart.

### 6.2 Perspective slots

Retail keeps an own slot and a viewing slot per machine; online both stay
on that machine's human, and the sensor phase, visibility probes and
minimap read the viewing slot `[03 R-VIS-01 §4]`. `+Control` changes the
controlling slot and `+View` only the viewing slot `[07 R-CAM-01 §6]`. A
player who becomes a watcher at defeat sees everything because its machine
clears its mapping and line-of-sight mode bits `[08 R-SKIR-01 §3]`.

A lockstep battle keeps one **perspective** per human seat: an own slot (the
seat) and a viewing slot (the seat, until `+View` moves it). Every
authoritative read of "the local player" becomes a read of a perspective,
chosen by who is acting:

- a unit's own decision — target acquisition, the direct-visibility
  predicate, cloak — reads the perspective of the machine that would have
  simulated it: its owner's, or for a computer seat its host's (§6.6);
- a per-seat block reads its own seat's perspective;
- presentation reads the local client's.

A battle with no human seat (the arena) keeps a single perspective, the
session's viewing slot.

### 6.3 What changes

Every authoritative reader of the local seat (`Session.LocalOwner`,
`Session.ViewingOwner`, the visibility service's `local` slot and the
control byte's "local human") moves to a perspective or a seat:

| Work | Retail contract | Lockstep form |
|---|---|---|
| Sensor phase | Five unit walks write the seen, sonar, jammed and decloak-timer bits from the viewing slot inside that player's 30-tick settlement deadline; a defeated or watching viewer marks every unit friendly; the first walk also marks units of owners allied toward the viewer that share radar `[03 R-SENSOR-01]` `[03 R-VIS-01 §4]` `[03 R-VIS-01 §7]`. Fallback targeting reads the seen set `[06 §3.1]`. | One pass per perspective in that seat's deadline block, its bits held per perspective; each reader reads its acting unit's perspective. Pass 4's source gate ("simulated on this machine") becomes "this perspective's seat or a computer seat it hosts". A computer seat has no pass: it reads its host's bank (§6.6). Under Modern and Community a defeated seat's pass stays an ordinary viewer's while a computer it hosts lives (§6.6). |
| Direct-visibility predicate | Reads the local player's coverage even for a query on behalf of another record `[03 §3.2]`. | Reads the querying record's perspective, for targeting, danger, the repair-pad queue, the Classic rally sight `[08 R-AI-01 §7]` and the COB effect gate. |
| COB effects | The effect opcode spawns strip objects — pool slots and, for sprinkle and smoke, CRT draws — only when the viewing player can see the unit `[04 R-COB-03 §6]` `[03 R-STRIP-01 §1]`; its frame event feeds the effect pool (L9). | Spawned when the unit is visible to **any** perspective, the union taken ahead of the strips and the pool, so every client holds the same records. Each client then draws only what its own seat may see, after the pool. |
| Effect pool | Records live for their authored holds; at capacity the pool refuses shatter fragments and land-dust puffers before their draws `[03 §1]` `[06 R-WFX-01 §1]` `[04 R-COB-04 §3]` `[03 R-FX-01 §3]`. | Timed from `SimArt` on every host (M1). The events that feed it are admitted through an effect-only window independent of audio and status events (§16.4.1). |
| Temporary sight | Recorded only for victims the viewing slot owns, at most 20 `[08 R-SESS-01 §3]`; expired in the executor tail `[01 R-PLAT-02 §5]`. | One list of 20 per perspective; expiry after every tick (§4.5). |
| End condition | Each machine evaluates its own player in that player's deadline block; online, authored triggers are never polled `[08 R-TRIG-01 §6]` `[08 R-SKIR-01 §3]`. | Every human seat's block evaluates that seat (§6.5). |
| End countdown | One countdown and ending latch per machine gate its human and hosted computers; won and lost share it; a false due never resets it `[05 R-ECO-01 §1]` `[08 R-SKIR-01 §3]`. | Per human perspective. Under Strict 3.1 it gates that human and its hosted computers; under Modern and Community it gates only the human (§6.6). The no-human site runs every tick, skips Deathmatch and ends every perspective on its sixth consecutive true tick from an unarmed countdown `[08 R-TRIG-01 §6]`. |
| Commander death | Only the owner's machine sweeps its units; a human respawns in its own block, and its visibility rebuild resets that machine's mapping history and sight grids `[08 R-SKIR-01 §3]` `[08 R-ENTRY-01 §7]`. | Sweep each owner once; respawn each eligible human in its own block; rebuild only that perspective's own and hosted-computer visibility, keeping unrelated players' history (Q24, §6.7). |
| Watcher entry | Clears mapping and line-of-sight bits for the whole watching machine and rebuilds its grids `[08 R-SKIR-01 §3]` `[08 R-ENTRY-01 §7]`. | Changes the entering perspective and the hosted computers that borrow it (Q24). |
| Automatic sharing | The local slot only, every 60 ticks `[05 R-SHARE-01 §3]`. | Every human seat, ascending. |
| Explored-map sharing | Every 450 ticks an opted-in sharer asks surviving allied humans to copy its explored bits; the share screen's gift applies at once `[05 R-SHARE-01 §3]` `[05 R-SHARE-01 §6]`. | Q25: periodic shares apply on the request tick in canonical human order; gifts apply in stream order on their tick (§6.7). |
| Unit transfer | A local and a remote branch with different copy rules `[05 R-WORK-01 §15]`. | Chosen by whether old and new owner share a machine. |
| Player-record cheats | `+Give`, `+ATM` and their kin act for the local player; `Give` takes its source from the own/controlling slot `[07 R-CAM-01 §6]`. | The issuing seat (§7.1). |
| Machine option bits | `DoubleShot` and `HalfShot` toggle bits 7 and 8 of one per-machine options word whose bit 10 is the `ShootAll` admission bit `[06 §9.2]` `[06 §3.2]`; a hit is resolved on the shooter's machine, a death explosion on the victim owner's, a shooterless record on every machine `[06 R-DMG-01 §9]`. | Per seat: each seat's bits govern its own and its hosted computers' shots, death explosions and target admission. Which seat's bits apply to shooterless damage is settled in M5 against `[06 R-DMG-01 §9]`, or left a `TODO(question)`. |
| Builder options | The Community builder preference belongs to the host player (DESIGN_COMMUNITY_PATCH §4.3). | Each seat's own, as a seat command. |
| Survival waves | Waves patrol to the nearest unit of the human team (DESIGN_SURVIVAL §6.7). | All human survivors. |
| Loss statistics | Requested-snapshot receipt latch `[06 §12.1]`. | File the victim owner's loss once; the single-player path keeps its separately reviewed correction. |
| Known-site gate | The placement check consults the viewer's map knowledge `[04 R-P0-08-B §1]`. | The issuing seat's perspective. |

For online skirmish on static lobby teams — 2–10 rows, the human seats plus
the computers the room host added — and for Survival's 2–3 survivors, the
session implements the sensor, predicate, COB-effect, effect-pool,
temporary-sight, end-condition, end-countdown, known-site, builder-option
and Survival-wave rows (§16.4.1–§16.4.2, §16.6). Only human seats have
perspectives and result rows; a computer seat reads its host's perspective
(§6.6), and Survival's attacker has no sensor pass, temporary sight,
effect-union entry or result row. The other rows are M5 work.

### 6.4 Session kind

Retail branches on the session kind beyond transport: kind 3 starts at
speed 10, takes its cheat gate and options from the host, offers watchers,
restrictions and sharing, never polls authored triggers and disables saving
`[08 R-ENTRY-01 §2]` `[08 R-OOS-01 §5]` `[08 R-TRIG-01 §1]`
`[08 R-SAVE-02 §4]`; only there does Tab open the Options, Allies, Share and
Control strip and `h` the share screen `[07 R-CAM-01 §2]` `[07 §11]`. **A
lockstep battle is kind 3** for every branch the research establishes, minus
the transport; a single-player battle stays kind 2. Each kind-3 branch an
implementation unit meets is checked against its research section, and one
the research leaves open is a `TODO(question)`.

The elimination announcement is one such branch: its kind-3 form takes one
CRT draw masked to eight entries and posts the line on the machine that ran
it `[08 R-CAMP-01 §9]`. Every retail machine takes that draw whenever any
player's live count falls to zero, so the shared world takes one CRT draw
per elimination event. Retail's own-human removal-reason check has no
shared-world counterpart.

### 6.5 Single-player battles do not move

In a single-player session every perspective read resolves to its human
seat and every computer borrows it; `+View` still moves the one viewing
slot. Single-player battles, and therefore every fingerprint lock, stay
bit-identical (M5's exit test). Nothing here is a gameplay departure in
single-player.

In a multi-seat battle the result is per seat: each seat's result is the
one its own block latched, with its own latch and countdown (§6.3). The
battle goes on while any human seat is still playing, and ends for every
client once every human seat has latched a result or been finally removed,
or through the no-human countdown `[08 R-SESS-01 §1]` `[08 R-TRIG-01 §6]`.
Later, with departures (§11.1): a seat out of play has not left, and its
units count as live until a vote removes it, in every mode. A finally
removed seat is absent from the end-condition sweeps in every mode, as
retail's cleared record is `[08 R-LEAVE-01 §3]` `[08 R-LEAVE-01 §5]`: under
Modern and Community its retained idle units can still be destroyed, but no
survivor has to destroy them to win (Q29). If every human still playing is
out of play and none returns within `RejoinGraceMilliseconds`, the relay
ends the battle without a result (Q28).

### 6.6 Computer seats

Retail allows one computer per human machine. Its creator hosts it for the
whole battle: only that machine runs its planner and full unit work, and
the computer borrows that machine's sensor picture. Removing the human
removes its computer too and destroys both players' units
`[08 R-SKIR-01 §13]` `[08 R-AI-01 §21]` `[08 R-LEAVE-01 §9]`
`[08 R-LEAVE-01 §10]`.

**Online.** Every client runs every computer seat, Classic or Modern, each
with its own difficulty, side, team and colour. Its **host seat** is the
human that added it, fixed in the configuration; reconnects cannot change
it. Today only the room host adds computers, so every computer row's host
seat is 0, the room's creator (§16.6).

- **Rows.** Every client composes the same order: the present humans in
  relay-slot order, so a human's row is its relay slot, then the host's
  computers in their stored order, then Survival's attacker. A computer
  keeps its stored colour unless a human or an earlier computer holds it,
  and then takes the lowest free colour; the attacker takes the first
  colour left after them.
- **Limits.** A skirmish holds up to ten rows within the map's start
  positions; Survival holds up to three survivors, so a two-human room may
  add one computer survivor. Computers are excluded from Deathmatch.
- **Perspective.** A computer has no sensor pass of its own. It reads its
  host's: the host's status bank, its explored-history bit under Permanent
  LOS, and pass 4's "simulated on this machine" gate, which counts the
  host's computers (§6.3). The Modern AI's own sight predicate reads the
  same borrowed status, so it keeps the underwater sonar exception it has in
  single-player. A computer on a lobby team shares that team's sight and
  radar (§6.7).
- **Results.** A computer has no result row. It plays until it is destroyed
  or the battle ends, which happens once every human row has latched a
  result (§6.5). Victory sweeps count a hostile computer as an opponent,
  and its elimination takes the shared CRT draw (§6.4).
- **Cost.** Every client pays for every computer. Measured over 20 minutes
  on The Pass, two humans and six Modern computers cost 0.4 ms a tick
  natively and 1.5 ms in the js/wasm build under Node, at worst 8.8 ms a
  tick over 30 ticks, well inside the 33 ms tick; single wasm ticks reach
  about 100 ms, at the controllers' preparation and at garbage collection.

While a host is out of play but not finally removed, its computers keep
running with its perspective in every mode. At the human's final removal
(§11.1, not built online yet) the modes differ:

- **Strict 3.1** removes the human seat and every computer seat it hosts
  together, in ascending slot order, each through the destruction contract
  of §11.1 — retail's peer removal of the machine group for a voted removal
  `[08 R-LEAVE-01 §2]` `[08 R-LEAVE-01 §9]`, and for a resignation the silent
  deletion the other machines perform for retail's menu quit
  `[08 R-LEAVE-01 §7]` (§19 O23).
- **Modern and Community** keep its computers running with the host's
  perspective, which goes on updating because the removed human's per-seat
  block and sensor pass keep running for it (Q29). This is a Nanolathe
  Modern policy, selected together with the retention of the removed
  human's units by one `session.RuleSet` answer (§11.1, Q28).

**Nanolathe Modern policy — a hosted computer outlives its host's defeat.**
The session-owned `SeatRules` seam of `session.RuleSet` answers
`ComputersStopWithHost`:

- **Strict 3.1** (`StrictSeats`, true) keeps retail's machine. One
  countdown and ending latch gate the human and its hosted computers
  `[05 R-ECO-01 §1]` `[08 R-SKIR-01 §3]`: when the host's own due block
  writes its countdown or ending bit, the same values are copied onto its
  computers' records, so they stop settling with it. A defeated host's
  sensor pass is a defeated viewer's and marks every unit friendly
  `[03 R-VIS-01 §4]`.
- **Modern and Community** (`ModernSeats`, false; Community composes
  Modern's answer) gate only the human. A hosted computer keeps settling,
  and so keeps playing, after its host is defeated or has won. While any of
  its units lives, the defeated host's pass stays an ordinary viewer's, so
  the computer keeps normal sight and the defeated human watches with
  normal fog; once its computers are gone the defeated marking applies.
  Binding projects the answer onto the visibility service
  (`SetDefeatedHostKeepsSight`).

The policy adds no random draw. Its resource effect is that the computer's
settlement continues under Modern and Community. It reaches only online
battles with hosted computers; single-player computers have no host seat
and are unchanged. Tests: `TestHostedComputerOutlivesItsDefeatedHost` covers
settlement under both answers, `TestDefeatedHostKeepsItsComputersSight` the
borrowed picture under both, and the visibility test
`TestDefeatedHostKeepsSightWhileHosting` the pass with and without a live
computer.

**Difficulty.** Retail difficulty belongs to the hosting machine and is not
synchronized `[08 R-AI-01 §21]`. Online, difficulty is explicit per
computer seat, for Classic and Modern computers in every mode, and an
online configuration carries **no battle-wide difficulty word** (Q28). Each
reader concerns one computer seat and takes that seat's value:

| Reader | Use | Seat whose difficulty applies |
|---|---|---|
| Classic profile plan gate (`profile.SetDifficulty`) | `plan` directives `[08 R-AI-01 §12]`, effective where a planner record exists `[08 R-AI-01 §21]` | The computer whose planner it gates |
| Modern parameter layering (`AIOverrides.For` through `ControllerDifficulty`) | Nanolathe's parameter layer | That Modern computer |
| Modern persona selection (`personaFor`, also the Survival buddy host) | Nanolathe's persona | That Modern computer |
| Ledger production discount (`economy.Service.SetPlayerSelector`) | A computer's production credit `[05 R-ECO-01 §3]` `[05 R-ECO-01 §11]` | The producing computer |
| Construction refund discount (`construction.Service.RefundSelector`) | Refund credits `[05 R-ECO-01 §11]` | The refunded computer |
| Transfer recipient discount (`economy.Service.Transfer`) | Credit to a control-2 recipient `[05 R-SHARE-01 §2]` | The recipient computer |
| Computer-income projection (`projectSeatIncome`, `ComputerIncomeRules`) | Projects each seat's word onto its ledger record, which refunds also read | Each computer, before its full-income mark |
| Commander-respawn grant (dormant) | Respawned computer commander's stored resources scaled by difficulty `[08 R-SKIR-01 §3]` | The respawning computer; unreachable while computers are excluded from Deathmatch |

A battle whose computers carry their own difficulties binds one
`*ai.Profile` per distinct difficulty, each Classic manager to its own
row's, and gives each computer's ledger record its own discount word
outside any tick. A battle with one difficulty — every single-player
battle — keeps the one profile and the battle-wide word, with its order of
operations unchanged. Classic keeps its rule set's difficulty behaviour;
Modern keeps its persona selection and full-income contract. A human seat
consults no difficulty.

**Nanolathe Modern policy — online computer-seat cap.** Strict 3.1 keeps one
computer per human. Modern and Community allow a human several computers
within the session's available lobby seats, Survival's layout constraints
included. `SeatRules.ComputerSeatsPerHuman` selects the cap through the
same `session.RuleSet` composition (`StrictSeats` answers one, `ModernSeats`
the available seats; Community composes Modern's answer); the
final-removal answer of §11.1 joins that seam in M6. Single-player
admission and difficulty are unchanged.

Online rooms run Modern rules today (§16.6), so the Strict answers of both
policies are exercised by tests until other rule sets are offered online.
Tests cover a second computer refused under Strict and admitted under
Modern/Community, seat exhaustion, Deathmatch refusal, fixed host
attribution, a configuration mismatch from one seat's difficulty, two
differently configured computers on one host each reading its own value,
two independent clients agreeing tick for tick with computers present, and
human-only rooms — 2 and 3 skirmish seats, 2 and 3 Survival survivors —
locked to the digests and checksums they had before computer seats existed.
A computer using a sensor perspective of its own would be a separate
gameplay decision.

### 6.7 Approved online alliance and exploration policies

**Nanolathe online policy (Q22, Q24, Q25).** These apply to multiplayer
sessions in every gameplay mode, Strict 3.1 included. They define the
common-world form of retail's separate machine copies; they do not rewrite
the retail research or change single-player, and no room switch selects
them. Not built online yet.

**Alliance declarations (Q22).** One directed matrix of declarations in the
common world; the incoming rows are its transpose, not a separate copy. An
admitted declaration applies at its command's tick and stream position on
every replica. A declaring toward B does not declare B toward A. Team
initialization, declaration admission, one-sided consumers, mutuality and
the other shared-victory requirements keep their researched contracts
(§3.3, §7.1). The intentional difference from retail is that a third
seat's victory evaluation sees the same declarations as the sender and
recipient. Tests need at least three human seats.

**Explored history (Q24).** One canonical explored-history grid per player,
identical on every replica — not one grid merged across players. On respawn
or watcher entry, the transition's established visibility rebuild applies
to the entering human and the computer seats borrowing its perspective;
unrelated players' histories and modes are kept. Retail's wipe of the
entering machine's copies of every player's history is deliberately
omitted. Current sight, radar sharing, sharing eligibility and Survival's
team sight are unchanged.

**Allied sight.** In an online skirmish, seats on the same lobby team share
current sight and radar: each member's perspective sees what any teammate's
sensors cover, as Survival's survivors already do (DESIGN_SURVIVAL). Explored
history stays one grid per player (Q24); what a teammate sees also explores
each member's own grid, as it does for Survival's survivors, so a teammate's
view never draws as unexplored. Resource, mapping and radar sharing commands
stay refused online. This is Nanolathe's online policy;
retail never merges allies' sight by default `[03 §3.2]`. Single-player
battles are unchanged.

**Sharing time (Q25).** Periodic explored-map shares run at their existing
due site on the request tick, humans in canonical seat order, each copying
the source's history as it stands when that request applies. Explicit
share-screen gifts are seat commands applied on their tick in stream order.
No wall-clock arrival or guessed transport delay changes either, so an
earlier share can affect a later source's history. Copying explored bits
draws no random number and changes no resource.

## 7. Commands

### 7.1 Classification

Every `HumanCommandKind` falls into one of four classes:

| Kinds | Retail or engine behaviour | Lockstep class |
|---|---|---|
| `SelectionReplace`, `SelectionToggle`, `SelectionClear`, `GroupRecall`, `BuildPage` | Selection is bit `0x10` of each unit's status word, with two visited bits; the build page lives in the same word. Phase 2 drops units that stop being ready from the selection `[04 R-MOV-03 §1]`. | **Local.** Client state; the client applies the same readiness rule to its own selection. |
| `BigBrother`, `ShiftState` | Interface state written into the selection in phase 2 `[07 R-CAM-01 §12]`; `+BigBrother` is ungated everywhere `[07 R-CAM-01 §6]`. | **Local.** It gives no command over units the seat does not own (§7.2). |
| `NoShake` | Stops the shake driver and its two CRT draws per active tick (DET-04). | **Local** online: the driver always runs and the client declines the offset. Single-player keeps its behaviour. |
| `GroupAssign` | Writes each unit's group number, which is also the computer player's task-group index (`internal/ai/groups.go`, `internal/construction/inheritance.go`). | **Seat command** carrying the group's complete new membership; it also clears the number from the seat's other units. |
| `Order`, `Stop`, `SelfDestruct`, `Stance`, `Cloak` | Read the selection when no units are given. | **Seat commands**, always with explicit units. |
| `Activation`, `MobileBuild`, `FactoryBuild`, `CancelProduction`, `Stockpile`, `CancelQueuedMove`, `CommunityKickout`, `CommunityOrderDrag` | Explicit units of the local owner; the order drag runs the viewer's known-site gate. | **Seat commands**, by handle and allocation serial (§7.2); the known-site gate on the seat's perspective. |
| `BuilderOptions` | Live player state; the payload names its owner. | **Seat command**; the owner field leaves the payload. |
| `Give` | `+Give p n metal`, open in every session kind with no alliance test: a transfer from the own/controlling slot to seat `p`, the typed integer converted to single precision; a negative amount reverses it `[07 R-CAM-01 §6]` `[05 R-SHARE-01 §2]`. | **Seat command** from the issuing seat. **Nanolathe online policy (Q8):** a positive whole amount within its producer's range is ordinary sharing; any other amount is rejected at validation unless the room's cheat permission is on, when the signed retail transfer applies to any amount that producer can generate (§7.4.1). Single-player keeps retail behaviour, negative amounts included. |
| `SetResource` | `+NoMetal p n`, `+NoEnergy p n`: write any player's stock `[07 R-CAM-01 §6]`; in retail multiplayer the next economy copy overwrote it `[08 "Economy and integrity checks"]`. | **Seat command limited to the issuing seat's own stock, behind the online cheat permission**, in every mode (Q8). |
| `SetLogo` | `+Logo n p` sets any seat's insignia `[07 R-CAM-01 §6]`. | **Local** presentation override; the record keeps its configured logo. |
| *(new)* share toggles, thresholds and gifts | `+ShareMetal`, `+ShareEnergy`, `+ShareMapping`, `+ShareRadar`, `+ShareAll`, `+SetShareMetal`, `+SetShareEnergy` flip the issuer's own options `[07 R-CAM-01 §6]` `[05 R-SHARE-01 §3]`; the share screen is `[05 R-SHARE-01 §5]`; retail's receiver does not check the sender `[05 R-SHARE-01 §4]`. | **Seat commands** acting on the issuing seat. Share options are per-seat battle state in the checkpoint, not configuration. |
| `View`, `ATM`, `DoubleShot`, `HalfShot`, `Visibility`, `Meteor`, `MakeSelectable`, `Spawn` | Retail cheats, most behind the cheat gate `[08 R-OOS-01 §5]`, and the Modern testing spawn. `MakeSelectable` and the line-of-sight-type half of `Visibility` are ungated in retail `[07 R-CAM-01 §6]`. `DoubleShot`/`HalfShot` toggle the typing machine's damage gates `[06 §9.2]`. | **Seat commands only when the room enabled cheats** (Q8), the ungated two included, enforced by every receiver. `View` and `Visibility` change only the issuing seat's perspective; `DoubleShot`/`HalfShot` set the issuing seat's own bits `[06 R-DMG-01 §9]` (§6.3); `ATM` and `Spawn` act for the issuing seat; `Meteor` and `MakeSelectable` act on the whole battle. |
| *(new)* `DeclareAlliance`, `SharedVictory` | Allies-window declarations and shared victory `[05 R-SHARE-01 §1]` `[07 R-FE-01 §7]`. | **Seat commands.** A declaration `{target, value}` needs a seated, non-eliminated, non-watching issuer and a distinct such human target outside its team; shared victory `{on}` refuses watchers and seats in teams of two or more. They update Q22's matrix (§6.7). |
| *(not yet a command)* `ShootAll` | Toggles bit 10 of the per-machine options word, ungated `[07 R-CAM-01 §6]` `[06 §3.2]`. | **Seat command** behind the cheat permission when added. |
| `Gameplay` | Switches the rule set at the phase-1 boundary (DESIGN_GAMEPLAY_RULES §5). | **Lobby only**; refused in a lockstep battle. |

### 7.2 Attribution and validation

Receiver-side authorization is Nanolathe's contract; retail checks roles
only in the sending interface `[08 "Packet framing and dispatch"]`.

- **The relay names the seat.** A payload never carries one (§4.2).
- **Every receiver authorizes the kind and arguments** before dispatch: the
  command's online class, the issuing seat's role, the agreed cheat
  permission, gameplay availability, bounded arguments and session state. A
  sender-side check is convenience only: the relay is payload-opaque, and
  an unauthorized command every client accepted would hash identically.
  The developer phrase and developer commands cannot widen the stream
  classes or bypass the permission. An invalid command has the same
  rejection and no partial effect on every honest replica; diagnostics are
  drained outside the tick.
- **The session validates the actors.** Phase 1 admits only units the
  entry's seat owns, inside the tick on identical state, so a forged or
  stale command is refused identically everywhere.
- **References survive the delay.** Pools reuse a freed slot at once and
  carry no generation [I5], so a command names a unit by handle plus
  allocation serial. A session counter assigns a fresh serial on each
  successful creation, forced-slot and transfer creation included; failure
  does not advance it, it never derives from mutable state and never wraps
  silently. A reference whose serial fails is dropped, never redirected to
  the slot's new occupant. Targets use the same form and may belong to any
  seat. Publication identities never validate a command.
- **Ordering is the stream's.** Commands on one tick apply in stream order
  (§4.2). The session's command sequence, which tracked moves record in
  their order nodes, is the stream position, the same everywhere.

**Visibility admission.** Casual rooms keep the researched target admission
(Q9). Ranked play needs a separate approved contract for direct targets,
radar contacts, stale sight and area commands: checking only current
visibility rejects honest delayed orders, and trusting a client's claimed
sight permits fabricated targets.

### 7.3 Selection and control groups

Selection changes no gameplay — no phase reads it — so the client keeps the
selection, the build page and the visited bits, and resolves every order's
units from its own selection when the order is sent, as dispatchers resolve
their actors from the committed frame (DESIGN_INTERFACE_HUD_INPUT §3.7).
Removing these interface bits from the session moved the fingerprint locks
once, by exactly those bits (M2-C7). Control-group assignment stays a seat
command because the group number is also computer-player state; recalling
a group is selection and stays local.

### 7.4 Wire form

The session encodes and decodes command payloads, because it owns their
types. A payload is a kind byte followed by the kind's fields; the encoding
is versioned with the protocol, unknown kinds are refused, and every kind
has round-trip and fuzz tests. The relay never decodes a payload; it limits
size and rate. These are explicit wire schemas, never Go layouts, `gob`,
pointers or implicit enum ordinals; lengths are bounded before allocation
and actor counts before dispatch.

Payload rules that keep receivers independent of the sender's host:

- `Order` carries every value the host derives from a setting or gesture —
  the interface type that selects the resolution branch and the
  assigned-position, tracked-move and area-target flags — so no receiver
  consults a preference. Captured gesture intent is kept apart from claims
  about the world. Its staged count is a replay-staging input and stays off
  the wire.
- `MobileBuild` carries a site height the sender validated; every receiver
  checks site and height against its own world through the owning
  placement contract before any queue cancellation or insertion, keeping
  the repeated-click and facing behaviour.
- `CommunityOrderDrag` names its order by the complete queue receipt, with
  an allocation serial in place of a publication identity.
- `GroupAssign` carries complete membership, the empty set included.
- `BuilderOptions` drops its owner field.
- `CancelQueuedMove` names an order by the stream position its receipt
  reported (§7.2).
- `SetResource` and `Give` keep binary32 amounts; online the source is the
  stamped seat and a gift names its recipient.
- A local enqueue adapter and a stamped-entry adapter both reach one phase-1
  implementation; neither payload authors its seat, tick or sequence, and
  only the local adapter has the paused drain.
- Only interface-owned status bits leave the session; group, eligibility
  and gameplay flags stay.

Codec and authorization rejection happen before any order queue, resource
or random stream changes. Once an authorized command reaches its owning
service, that service keeps its researched ordering and failure behaviour
(for example Community order drag interrupts movement before its placement
test, DESIGN_COMMUNITY_PATCH §7).

**Single-player replay context.** `NoShake` changes the single-player CRT
draw schedule and `Gameplay` its rule set, so both have single-player
replay schemas, including target-player fields online authorization
forbids. Their context comes from the session or replay kind, never from a
payload; online admission refuses them.

#### 7.4.1 Version 1 primitives and limits

**Nanolathe protocol contracts**, not retail findings. The command schema
version is `1`, negotiated outside the payload. A payload starts with one
kind byte; its context comes from the admitted session or replay header.

| Notation | Encoding and accepted domain |
|---|---|
| `u8` / `bool` | One byte; a boolean is exactly 0 or 1. Enumerations accept only the listed values. |
| `u16`, `u32`, `u64` | Shortest unsigned base-128 varint, limited to the named width; overflow and overlong encodings are rejected. |
| `s32`, `s64` | Zigzag of the named signed width, then shortest unsigned varint. No narrowing through Go `int`. |
| `fixed` | `s64`, the raw value of `numeric.Fixed`. |
| `point` | X, Y, Z, each `fixed`. There is no generic on-map test; each command keeps its coordinate semantics. |
| `ref` | Handle `u16`, then allocation serial `u64`. Both zero is null; exactly one zero is invalid. |
| `actors` | Count `u16`, then that many non-null `ref` values in captured order. At most the agreed per-player unit limit; duplicates or repeated handles are invalid. Empty means no actors, never implicit selection. |
| `key` | Length `u16`, then 1..255 bytes of the canonical content key from `content.CanonicalKey`, no NUL, bytes above ASCII preserved. Noncanonical input is refused on decode; an explicitly optional key permits zero length. Content whose command-addressable keys exceed 255 bytes is rejected before ready. |
| `amount` | Four little-endian bytes of IEEE binary32. Online: finite, with negative zero canonicalized to positive. Online `Give` without the cheat permission accepts only a whole number from 1 to 2^31 (2,147,483,648), the positive values its producer — a typed signed 32-bit integer converted to single precision `[07 R-CAM-01 §6]` — can generate; with the permission any whole number from −2^31 to 2^31, zero included, and the signed transfer applies `[05 R-SHARE-01 §2]`. Non-finite or non-whole amounts are rejected in every online room. Single-player replay preserves all 32 bits. |
| `position` | `point`, InterfaceType `u8` (0 left, 1 right), HasFeature `bool`, the captured contextual feature intent. |

`ResolvePos.IsWreck` and `FeatureResurrectable` have no authoritative
readers and no wire fields; the adapter sets them false. `HasFeature`
selects a resolver branch but proves no feature and confers no visibility.
`StagedCount` is a replay-staging input, not a command field.

**Representation ceilings.** The unit limit is **20..3276**, so ten owner
slices hold at most 32,760 units. An area list has a `u16` count of entries
`{Target ref, Position position}`, at most **65,535**, in captured order. A
command is at most **4 MiB** (4,194,304 bytes), checked before parsing, with
every count checked before allocating. An oversized gesture is refused
visibly before submission; nothing is split, truncated or continued.

**Per-command work (Q28).** Online admission adds two **Nanolathe protocol
limits**: an area list holds at most **10,000** entries (the per-unit order
queue's guard capacity, `orders.OOMGuardQueue`), and actor count times entry
count is at most **1,048,576** (2^20), which admits the largest repair drag
at the default unit limit of 1,000. A command over either is rejected whole
before any queue, resource or random stream changes (M2-C5); the local
sender refuses it first. The single-player replay context keeps the
representation ceilings.

These bounds fit one maximum selection and maximum admissible area list in
one atomic command: the largest online `Order` (10,000 entries, 104 actors)
is 451,394 bytes, and at the representation ceilings the largest `Order` is
2,991,707 bytes.

#### 7.4.2 Kind and payload table

Numbers are explicit protocol constants, never derived from `iota` or Go
layout. `S` is a supported seat schema; `D` a seat schema whose online
application waits for M5 (rejected before mutation); `L` local-only, with no
online or authoritative replay payload; `R` single-player replay only. S and
D kinds also have a single-player replay form where the local operation
exists. The online session additionally admits `MobileBuild`, checking
the issuing seat's known-site predicate (§16.4.2).

Fields are in wire order. Records are named `<Kind>Payload` under
`session.SeatCommand` with exactly these fields.

| Number | Kind / class | Fields after kind byte |
|---|---|---|
| 1 | SelectionReplace / L | None; reserved, reject in codecs |
| 2 | SelectionToggle / L | None; reserved, reject in codecs |
| 3 | SelectionClear / L | None; reserved, reject in codecs |
| 4 | Order / S | Actors `actors`, Code `u8` 1..14, Target `ref`, Position `position`, Queued `bool`, AssignedPosition `bool`, TrackQueuedMove `bool`, Targets area list |
| 5 | Stop / S | Actors `actors` |
| 6 | Activation / S | Unit `ref`, Activate `bool`, Queued `bool` |
| 7 | MobileBuild / D | Builder `ref`, Product `key`, Position `point`, Facing `u8` 0..3, Queued `bool`, AppendOnly `bool` |
| 8 | FactoryBuild / S | Builder `ref`, Product `key`, Count `s32` (nonzero, bounded below) |
| 9 | CancelProduction / S | Unit `ref` |
| 10 | Stockpile / S | Unit `ref`, Count `s32` (nonzero, bounded below) |
| 11 | BuildPage / L | None; reserved, reject in codecs |
| 12 | GroupAssign / S | Group `u8` 1..9, Members `actors` |
| 13 | GroupRecall / L | None; reserved, reject in codecs |
| 14 | Stance / S | Actors `actors`, Fire `bool`, Value `u8` 0..2 |
| 15 | Cloak / S | Actors `actors`, Cloak `bool` |
| 16 | SelfDestruct / S | Actors `actors`, Queued `bool` |
| 17 | NoShake / R | No fields; toggles the single-player driver. Online it is a local visual preference. |
| 18 | ATM / S, cheat | No fields; the issuing seat's credit operation |
| 19 | SetResource / S, cheat | Resource `u8` (0 metal, 1 energy), Amount `amount`; replay adds Player `u8` 0..9 **before** Resource |
| 20 | SetLogo / R | Player `u8` 0..9, Logo `u8` 0..255; online a local override only |
| 21 | View / D, cheat | Player `u8` 0..9 |
| 22 | Give / S; cheat for any amount but a positive whole one | Player `u8` 0..9 (recipient), Resource `u8` (0 metal, 1 energy), Amount `amount` with `Give`'s bound (§7.4.1) |
| 23 | MakeSelectable / S, cheat | No fields |
| 24 | Visibility / D, cheat | ToggleMask `u8` 0..7, ClearMask `u8` 0..7; overlapping bits keep toggle-then-clear semantics |
| 25 | DoubleShot / D, cheat | No fields; toggles the issuing seat's own gate (§6.3 "Machine option bits") |
| 26 | HalfShot / D, cheat | No fields; toggles the issuing seat's own gate |
| 27 | Meteor / S, cheat | ArgumentPresent `bool`, Enabled `bool`; Enabled must be false when ArgumentPresent is false |
| 28 | BigBrother / L | None; reserved, reject in codecs |
| 29 | ShiftState / L | None; reserved, reject in codecs |
| 30 | CancelQueuedMove / S | Sequence `u64` 1..MaxUint64, Actors `actors` |
| 31 | Spawn / S, cheat | Unit `key`, Position `point`; still requires the owning rule's spawn permission |
| 32 | BuilderOptions / S | Guard[0..2], then Patrol[0..2], six `u8` values each 0..2; replay adds Owner `u8` 0..9 first |
| 33 | CommunityOrderDrag / D | Unit `ref`, Index `u16`, DescriptorID `s32`, CreationTick `u32`, Target `ref` (null in v1), Goal `point`, BuildProduct optional `key`, BuildFacing `u8` 0..3, Destination `point` |
| 34 | CommunityKickout / S | Unit `ref`, Destination `point`; requires the Community feature |
| 35..43 | ShareMetal, ShareEnergy, ShareMapping, ShareRadar, ShareAll, SetShareMetal, SetShareEnergy, ShareGift, DeclareAlliance / D | Reserved in that order; no v1 payload, rejected even where a receiver has local helpers |
| 44 | SharedVictory / D | Reserved; no v1 payload |
| 45 | ShootAll / D, cheat | Reserved; no v1 payload |
| 46 | DeveloperSpawn / R | Pattern `key` (wildcards allowed), Owner `u8` (the typed integer's low byte), Position `point`; replay only (DESIGN_DEVELOPER_TOOLS §8). Online codecs and receivers reject it. |
| 255 | Gameplay / R; lobby-only online | Mode name `key`, resolved through the registered rule sets |

Every unlisted number is invalid. Sharing and alliance kinds need M5's
owning service contract and a new schema version; reserving numbers
authorizes no threshold or gift arithmetic. `GroupAssign` has no Preserve
or filter Mask: those belong to recall. Replay `Give` takes its source from
the own/controlling slot at drain time `[07 R-CAM-01 §6]` and keeps signed
amounts and the negative-amount edge `[05 R-SHARE-01 §2]`; online uses the
stamp and §7.4.1's bound. Replay `SetLogo`, `NoShake`, `View`,
`BuilderOptions` and `Gameplay` keep their single-player effects; online
context cannot request replay-only fields or bypass cheat checks.

Online counted production accepts −32767..−1 and 1..32767; the local
adapter normalizes Count 0 to 1 before recording. Single-player replay
accepts other nonzero signed-32 counts except MinInt32. Counted commands
are never coalesced or split.

`AssignedPosition` requires a single actor, Code 2, null Target, empty
Targets and TrackQueuedMove false. `TrackQueuedMove` requires Code 2,
Queued true, null Target, empty Targets and AssignedPosition false. An area
list requires a null outer Target and both flags false. The outer Position
is always encoded.

#### 7.4.3 Application and stale data

Validate the complete payload and issuer first. A live foreign actor
rejects the **whole command** before any friendly actor mutates. A dead or
serial-mismatched actor is stale, not foreign: stale actors are removed,
keeping the survivors' relative order. An empty survivor list does nothing,
except `GroupAssign`, whose surviving membership (even empty) replaces the
group among the issuer's units. Capability gates then run in the existing
gameplay order; `SelfDestruct` keeps its whole-selection cancellation pass.
Ordinary `Order` actors are visited in ascending handle order, so the
encoder emits that order and the decoder refuses an unsorted list; area
orders keep actor and target order. The wire never accepts duplicates.

A stale singular actor makes the command a no-op. A stale explicit target
makes an ordinary order a no-op; it never becomes a ground click. An area
order drops only stale explicit-target entries, keeps targetless feature
entries and keeps area semantics even when none survive. A changed queue
receipt makes a Community drag a no-op; current draggable orders are
targetless, so its Target is null.

**Single-player stale targets differ.** Today's single-player applier
resolves an ordinary order whose target has died as a ground order at the
captured position. The local adapter and its replay context keep that
result; any change to it needs research and is declared under M2-C7.

`MobileBuild` derives the canonical placement centre and height through
the owning placement service and rejects mismatching input before queue
mutation, including the known-site and occupancy checks for a new click and
for cancellation; a delayed click whose site is no longer admissible does
nothing. Community drag keeps its interruption before the gameplay
placement test. Neither borrows a client's viewer or swaps `LocalOwner`.
`Order` and `CommunityKickout` keep their coordinate semantics, including
targetless points outside the map, and reject unrepresentable intermediate
values before mutation. `Spawn` keeps `spawnCommandPlacement`: cell
coordinates within terrain bounds, then the mission position fixup.

Malformed encoding, forbidden kind or role, invalid scalars or
combinations, missing content keys and live foreign actors reject before
any gameplay service, queue, resource or RNG mutation. An authorized
gameplay failure keeps the owning service's partial-work semantics. Every
admitted entry consumes its stream position even if rejected or stale. A
receipt says rejected, no-op or applied; it does not claim every actor
achieved the order.

#### 7.4.4 Public boundary for U1, U2 and U6

These names and signatures are the shared contract; implementations stay
in their owning packages. `pool.UnitRef` is a value type, not a generation
added to the allocator. `frame.UnitView` carries `AllocationSerial uint64`
beside Slot; `InstanceID` stays presentation-cache identity.

```go
// internal/pool
type UnitRef struct { Handle Handle; Serial uint64 }

// internal/units
// Unit adds AllocationSerial uint64. World owns the battle counter.
func (w *World) Reference(h pool.Handle) pool.UnitRef
func (w *World) LookupReference(r pool.UnitRef) *Unit
func (w *World) LastAllocationSerial() uint64

// internal/session
type CommandContext uint8 // explicit constants: OnlineCommand=1, SinglePlayerReplay=2
type SeatCommandKind uint8 // explicit numbers from the table
type SeatCommand struct { /* Kind plus one named typed payload from the table */ }
type CommandStamp struct { Seat uint8; Tick uint32; Position uint64 }
type CommandReceipt struct { Stamp CommandStamp; Outcome CommandOutcome; Diagnostic string } // Diagnostic: the rejection's text, empty otherwise
type CommandOutcome uint8 // explicit constants: CommandApplied=1, CommandNoOp=2, CommandRejected=3
func EncodeSeatCommand(context CommandContext, c SeatCommand) ([]byte, error)
func DecodeSeatCommand(context CommandContext, payload []byte) (SeatCommand, error)
func (s *Session) EnqueueSeatCommand(stamp CommandStamp, c SeatCommand) error
func (s *Session) DrainCommandReceipts() []CommandReceipt
```

`SeatCommand` has `Kind` and payload fields named as the table's kinds
(fieldless kinds have no record), each typed `<Kind>Payload` with the
table's names, widths and order. `Actors` and `Members` are
`[]pool.UnitRef`; `Position` is `CommandPosition` (`X,Y,Z numeric.Fixed`,
`InterfaceType uint8`, `HasFeature bool`); plain points are
`CommandPoint { X,Y,Z numeric.Fixed }`; `Targets` is
`[]CommandTarget { Target pool.UnitRef; Position CommandPosition }`.
Replay-only Player/Owner fields must be zero in online values and are
absent from online bytes; unselected payload records must be zero. Go
arrays hold Guard/Patrol, `gameplay.Mode` the Gameplay choice (only its
registered name is encoded), `economy.Res` the resource with the wire
mapping above; Amount is `float32` and Count `int32`.

`Reference` returns null for an absent or dead unit; `LookupReference`
returns nil for null, dead or mismatched references. A new World starts at
counter zero. The serial is allocated at the successful-creation boundary
before `OnCreate`, with exhaustion checked before allocation or creation RNG
work; failures leave the counter unchanged and keep the allocator's
failed-creation draws. Only a future exact snapshot restore may set the
counter or live serials. A retail-save load creates a new session and
discards prior commands and local reference maps.

The session's admitted kind chooses the context; callers cannot pass it to
`EnqueueSeatCommand`, which copies variable storage, validates the stamp and
queues for phase 1. A stamp has seat 0..9, a nonzero position and a future
unsealed tick; position gaps are legal, repeats and backward positions are
not. Queue-time errors do not advance simulation, and phase-1 rejection
still yields a receipt. Receipts are drained outside the tick and
deep-copied. `EnqueueHumanCommand` remains the single-player adapter: it
captures explicit references and membership at submission, keeps the
paused-prefix behaviour, and calls the same phase-1 implementation. A
tracked order's Sequence is the accepted stream Position; an
unacknowledged client sequence is never a cancellation target.

## 8. Battle configuration and identity

### 8.1 What the seats agree

The agreed configuration holds every battle-entry input and the match
policies that constrain the clients:

1. **The skirmish configuration** (`session.SkirmishConfig`): map; the seat
   rows (nickname, controller, side, colour, ally group, starting metal and
   energy, Classic or Modern); difficulty, which online is each computer
   row's own, never a battle-wide word (§6.6); start-location mode;
   commander-death mode; mapping, line of sight and its type; unit limit;
   Survival options; the seed pair (§8.3).
2. **The match selection** of DESIGN_MODS_MUTATORS §3 — the mod's id,
   version and archive SHA-256, the content profile, the rule set's name and
   base, the Community table, and the mutators.
3. **The entry options** (`session.SkirmishEntryOptions`): each option that
   can change the battle is part of the configuration or held at its
   default online. `AutomatedPlayers`, the arena's switch, is never set.
4. **The seat assignment**: which connection owns each human row.
5. **The remaining online configuration**: computer host seats; team
   symbols and starting shared-victory bits; per-computer difficulty (Q23);
   unit restrictions, cheat and watching permissions, pause/drop,
   scheduling and audience policies, and the match view policy (§8.4).

Its canonical encoding (§8.6) is versioned and covers every effective
field, defaults included. No battle-affecting option may remain an unagreed
machine-local default. Same-team pairs start mutually allied with shared
victory for teams of two or more; a single team holding every player
prevents START `[08 R-SKIR-01 §3]` `[05 R-SHARE-01 §1]`.

Audio volume, key bindings and renderer quality stay local. A presentation
setting the room restricts for play, such as tactical zoom-out, is match
policy instead (§8.4), whatever package implements it. The local seat is a
presentation fact (§6).

### 8.2 Identity

Before a match starts the clients' identities are compared field by field,
each with its own diagnostic (`netproto.Identity`):

| Identity | Source | Rule |
|---|---|---|
| Protocol | command schema version | Exact match. |
| Build | the common build manifest's digest (§8.7) | **Advisory**: reported, not compared. The rehearsal (§16.7) and the in-game checksums catch incompatible simulations, so an unstamped development build, `go run` included, may play online. |
| Content | the simulation-content digest of §8.7 | Exact match. `Catalog.Hash` alone is insufficient. |
| Map | SHA-256 of the map inputs the battle reads | Exact match, diagnosable apart from Content. |
| Rules | rule set name and base; `community.Features.Digest` | Exact match. |
| Mod | id, version and archive SHA-256 | Exact match. A client lacking the host's mod may later fetch it from the catalogue (Q14). |
| Configuration | SHA-256 over §8.6's canonical encoding, seeds and defaults included | Exact match. |

**Complete content identity.** It covers canonical compiled definitions and
their order, the scripts of every admitted unit (including units not yet in
the world), authoritative model geometry and derived heights, simulation
animation metadata (`SimArt`, with the effect holds of L9), the map inputs,
AI data, extension tables and mutators, and the defined fallback states of
missing inputs. A resource is classified by its consumers, never assumed
cosmetic. Content is frozen for the battle at admission (§8.7). The
existing `Catalog.Hash` keeps its regression contract and omits
`UnitDef.Script`, `ModelTop` and `ModelTopFixed`; the archive manifest hash
(`vfs.FS.ManifestHash`) covers paths and sizes, not bytes, and is not used.
A reported identity is compatibility information, not proof that a client
is unmodified (§13).

**A simulation change is a compatibility change.** Two builds whose
authoritative behaviour differs cannot share a battle, and a replay plays
only on a simulation that reproduces it (§10).

**Before the first tick.** Each client composes the battle and reports its
initial unit checksum with its identity; the relay refuses a join whose
checksum differs from the creator's. Before readying, each client also runs
the rehearsal and reports its digest; the relay starts only on equal
digests (§16.7). These catch divergent composed state and simulation
differences an identity cannot name; neither replaces content identity,
since a changed unbuilt unit's script may matter only minutes later.

### 8.3 Seeds

The host draws the seed pair with `crypto/rand` (in `cmd/nanolathe`) when it
freezes the configuration at Create. The seeds are fields of the
configuration (§8.6), so the configuration digest covers them and both
seats agree on them. Clients pass them through the explicit seed handoff
into composition, the only RNG handoff into composition [I6]. Single-player
battles choose their own seeds; a replay records its pair. Later, the relay
draws the pair in a two-stage start (§12.2).

### 8.4 Match-wide view restrictions

Approved, not built yet. A match has one effective configuration for every
setting that affects simulation or that the room restricts for fair play:
the host can, for example, disable zoomed-out tactical play for everyone.
The match view policy (field 14) gives the permitted tactical zoom bounds
and whether the full-map tactical view is available. A no-zoom-out preset
uses the camera's native 1× boundary as its minimum, whatever a player's
custom zoom lock, and blocks every route to zoom out — wheel, keys,
battle-entry flags, settings changes, renderer switches and the Community
megamap. The ordinary minimap remains.

Every player sees the restrictions in the lobby; they are part of the
configuration digest, fixed for the battle, and constrain every relevant
input and presentation boundary. Leaving the battle restores personal
preferences. A client unable to honour the policy refuses to ready. A scale
limit does not equalize the world area shown on different monitors, and a
modified client can bypass any camera restriction (§13); the lobby does not
present equal settings as an anti-cheat guarantee.

### 8.6 Effective configuration contract for U5

Configuration version 1 is a fully explicit value. Host preferences are
resolved once; then that same value is validated, frozen, encoded and
composed. Decoding never calls `SkirmishConfig.ApplyDefaults` or
`Normalize`, whose private missing-value state and zero-resource defaults
could change a deliberately selected choice; only a local request adapter
may use them before freezing. Unused rows and irrelevant fields are
canonical zero values.

Encodings are §7.4.1's. `text(N)` is a `u32` byte length then valid UTF-8
without NUL, at most N bytes, checked before allocation; `digest` is exactly
32 bytes and `id` exactly 16. Collections have a `u32` count and the stated
order; duplicate or unsorted map keys are rejected. The whole configuration
is at most 2 MiB. Its identity is SHA-256 of the literal UTF-8 domain
`nanolathe/match-config/1` (no trailing NUL) followed by this positional
encoding. There are no optional unknown fields or trailing bytes; adding a
field requires a new version.

| Order / effective field | Representation and normalization |
|---|---|
| 1. SessionKind | `u8`: 1 online skirmish, 2 online Survival. Campaign and single-player recording headers have their own admission. |
| 2. RuleName, RuleBase | `key`, then `u8` (1 Strict 3.1, 2 Modern, 3 Community 3.9). Custom named sets use their registered base; resolved through the registry, with unknown names or disagreement rejected, never normalized to Modern. |
| 3. MapName, MapSchema | Canonical logical map `text(1024)`, selected schema `u32`, resolved by the existing map-entry code. |
| 4. NumPlayers, Players | `u8` 2..10, then exactly that many rows in slot order (below). At least one human; watchers require WatchingAllowed. Survival keeps its attacker-last and survivor-alliance invariants and permits several human survivors. |
| 5. Location, CommanderDeath, Mapping, LineOfSight, LOSType | Five `u8`: location 0 randomized/1 identity; commander-death 0..2; the last three 0..1. There is no battle-wide difficulty word (Q28): each computer row carries its own, and composition never reads the single-player `SkirmishConfig.Difficulty` for an online battle (§6.6). |
| 6. UnitLimit, SimulationSeed, CRTSeed | `u16` 20..3276, then two `u32`. The Community unit-limit override is already applied. Seeds are explicit, zero included; composition uses the existing seed constructors. |
| 7. Survival | Pace `u8` (0 normal, 1 relaxed, 2 relentless), NoAir `bool`, NoNaval `bool`. All zero for SessionKind 1. |
| 8. Mod | ID `text(255)`, Version `text(255)`, Archive `digest`. All empty/zero for base content; a mod requires all three. These are public identities, not local paths, so only a mod installed from its archive can be hosted (§16.6.2). |
| 9. Content profile | Name `text(255)`, at most 64 directory pairs `{From text(255), To text(1024)}` in canonical logical-key order, then Units `u32`, Weapons `u32`, TNTBytes `u64`, LOSBytes `u64`. Units 1..65536, Weapons 1..MaxInt32, byte caps 1..MaxInt64; content defaults 512/256/16 MiB/1 MiB for absent local inputs. Redundant identity mappings are rejected. These are metadata limits, never instructions to allocate; they are validated against the locally admitted profile before loading. |
| 10. Community | Length `u32` and at most 16 KiB of the canonical `community.Features` JSON used by `Features.Digest`, with its closed vocabulary, integer validation and `RepairRate` subrecord. The resolved value is encoded; unknown fields are refused and exact re-encoding required. Strict requires exactly the zero feature value. Online the host sends the mounted content's own table, never a player's overrides (§16.6.2). |
| 11. Mutators | Eleven `u8` step indices in this order: BuildSpeed, BuildCost, Health, Damage, AreaOfEffect, Sight, Radar, Income, Salvage, FireRate, UnitSpeed. Indices 0..7 mean ¼, ½, ¾, 1, 1½, 2, 3, 4; a missing or zero Factor and 1/1 both encode 3. Applied once to the battle's catalog clone. |
| 12. Unit restrictions | At most 65535 records `{DefinitionID u16, Unit key, Limit u8}` in ascending nonzero definition-ID order. `DefinitionID` is the record's index in the unrestricted compiled catalog and `Unit` its canonical name key; `Limit` 0 removes the record from the battle catalog and 1..100 caps each player's records of it at the allocator `[05 R-SHARE-01 §8]` `[05 R-SHARE-01 §9]`. Omitted means unrestricted, and nothing is seeded: a `wacky` definition is restricted only by an explicit record. The value is `content.Restrictions` mapped as [DESIGN_MODS_MUTATORS §15.3](DESIGN_MODS_MUTATORS.md#153-names-and-resolution) states — every record carrying a named key present, with one limit — and composition applies it to the catalog clone before the mutators. Each key is verified against its immutable record, preserving duplicate-name identities; duplicate IDs, a partial name group, `norestrict` definitions and removing a side's commander are rejected. The online lobby carries the host's set (§16.6). |
| 13. Permissions | CheatsAllowed `bool`, WatchingAllowed `bool`. Neither local developer state nor interface preferences add permissions. GameClosed is room admission, outside battle identity. |
| 14. View | Player, Spectator and Replay view records, each `{MinimumScale u16, MaximumScale u16, FullMap bool}`; 64 ≤ minimum ≤ maximum ≤ 2048 in 1/1024 zoom units. No-zoom-out is minimum 1024 and FullMap false (§8.4). |
| 15. Online policies | Policy revision `u16` = 1, Scheduling `u8` = 1 (casual earliest-unsealed tick), Pacing `u8` = 1 (normal speed, no pause), Drop `u8` = 1, Audience `u8` = 1 (§11.4). Drop policy 1: retain a seat out of play idle, allow a removal vote once it has been out of play for a cumulative `RejoinGraceMilliseconds`, and on a passed vote apply the mode's final-removal rule (§11.1); its vote window, cooldown and other details are fixed protocol values (Q28), not fields. Then `RejoinGraceMilliseconds` `u32` — never a removal timer — with three uses (§11.1): the cumulative out-of-play time before a removal vote may be called, the time an admitted rejoin has to complete, and the wait before a battle with no active playing human ends without a result. Then SpectatorDelayMilliseconds `u32` and ReplayReleaseDelayMilliseconds `u32`. The room supplies every value; no implicit timeout exists. |

Each player row is positional:

1. Role `u8` (1 human, 2 computer, 3 watcher, 4 Survival scenario
   attacker); Side `u8` (0..min(admitted side count−1, 255)); Color `u8`
   0..9; AllyGroup `u8` 0..5; Nickname `text(16)`; Metal `s32`; Energy
   `s32`. Finalized resources are nonnegative; explicit zero is never
   replaced by 1000. Names are valid UTF-8 within 16 bytes.
2. Participant `id`, HostSeat `u8`, ComputerKind `u8`, Difficulty `u8`.
   Human and watcher participant IDs are nonzero, unique public opaque IDs,
   not credentials; others are zero. Added computers have HostSeat 0..9
   naming an initially human row, ComputerKind 0 Classic / 1 Modern, and
   Difficulty 0..2. Other roles use HostSeat 255, ComputerKind 0 and
   Difficulty 0; the Survival attacker is not an added computer. Strict's
   one-computer cap is validated through the owning RuleSet; computers in
   Deathmatch are rejected. No socket, token or machine name enters the
   value.
3. SharedVictory `bool`, then the six Guard/Patrol bytes of §7.4.2. Teams
   initialize Q22's directed matrix from the team rows.
4. EffectiveAIParams: at most 128 `{Key text(32), Value text(32)}` pairs in
   lexical key order, at most 8192 encoded bytes per row, resolved with the
   All → difficulty → player precedence and validated by the controller's
   own validator. Omitted and explicit values are equivalent only where
   that controller's contract says so.

These bounds are protocol admission limits, not retail claims. Every
`SkirmishEntryOptions` member is accounted for: BuilderOptions become the
row's six values (online both seats use the defaults); CommunitySources
become the resolved table; ContentLimits and Mutators are explicit;
AIOverrides become the merged per-row values; Restrictions become field
12's records through `MatchUnitRestrictions` against the unrestricted
catalog, and a set in the options must equal the set those records
describe. AutomatedPlayers is false. Progress is local and excluded.
SimArt belongs to frozen content identity. Presentation recommendations,
local provenance and override-layer text never enter the identity.

The AI parameter vocabulary validator lives in its lower owning package
(`brains/utiltac`) and is shared by the mod and admission, so session does
not import `mods/aikit`.

### 8.7 Frozen content and build contracts for U4/U5

**Frozen content.** Sources are captured **before** the catalog compiles.
`content.SimulationSources` owns that closed snapshot; catalog compilation,
map/schema resolution and rule/mutator preparation read its read-only VFS.
`content.SimulationInputs` then clones the prepared catalog and freezes
SimArt, every admitted unit script and authoritative model, and keeps the
selected map/schema and AI inputs. Its VFS never falls through to live
providers, failed or missing lookups included, and late unit creation reads
only this view and the frozen objects. A second battle re-reads and
revalidates provider bytes before reusing a parsed cache; path or provider
metadata is not provenance. The capture covers the known simulation
resource families and the selected map's inputs in loader discovery order,
recording misses; it exposes no arbitrary resource registry. A catalog or
SimArt from another capture is rejected.

Manifest entries are `{Family u8, Key text(1024), Ordinal u32, Presence u8,
SemanticDigest digest}` in family, ordinal, key order. Presence is 0 defined
absence, 1 present, 2 owning-loader fallback; fallback entries also carry
the resolved fallback key `text(1024)` (empty otherwise). Families are 1
catalog, 2 COB, 3 model, 4 simulation art, 5 map, 6 AI, 7 extension/mutator
inputs. Ordinal keeps record identity where names repeat. The digest is
domain `nanolathe/sim-content/1`, then entry count `u32` and entries.
Provider, path and size diagnostics are returned separately and excluded.

| Family | Identity requirement |
|---|---|
| Catalog | Unit record-ID order, duplicate names included; weapon slots; side order; feature, movement and category definitions; build membership and download placement order; LOS and meteor definitions; compiled SightShapes. Every compiled field the simulation reads, including those `Catalog.Hash` omits. |
| COB/model | Every admitted unit's resolved program and missing/fallback state, units not yet created included; the parsed model hierarchy, piece origins, authoritative geometry and derived heights. Never a filename alone. |
| SimArt | Canonical sequence names and defined misses, ordered frame geometry and holds, and feature animation metadata the simulation consumes. |
| Map | The exact selected OTA/TNT inputs and schema, plus scenario inputs actually consumed. |
| AI | The selected profile and default-fallback state, with ordered directives and argument order (repeated multipliers do not commute). |
| Extensions/mutators | The effective Community table and the applied mutator vector, tied to the prepared clone; a mutator is never applied twice. |

**Build manifest.** The common build manifest uses domain
`nanolathe/sim-build/1` and these fields in order: SourceTree `digest`;
GoVersion `text(64)`; GoMod and GoSum digests; module count (at most 4096)
and `{Path text(1024), Version text(255), Sum text(255)}` sorted by
path/version; build tags (at most 64, sorted); GOEXPERIMENT `text(1024)`;
CGOEnabled `bool`; ordered build arguments (at most 64 `text(1024)`);
variant count (1..16) and sorted `{GOOS key, GOARCH key, ArchitectureLevel
key, ToolchainArchive digest}`. The whole manifest is at most 1 MiB.
Installer settings such as readonly modules, trimpath, buildvcs and ldflags
are included, and uncontrolled environment inputs are cleared. SourceTree
hashes the canonical build-source inventory — relative UTF-8 paths in
lexical order, file kind and executable mode, lengths and bytes, every
build input and dependency replacement — excluding only the stamp
generated from it. The installer stamps release builds through
`internal/version/stampgen`; a development build is unstamped. Binary
hashes and native-equivalence evidence are detached attestations keyed by
build digest and variant.

Online the manifest is advisory (§8.2): a client reports its digest, the
startup report prints it, and nothing refuses a match because of it. The
rehearsal (§16.7) and the in-game checksums are what detect a simulation
difference.

### 8.8 Public identity API

These values are package-owned, with private effective storage and copying
constructors and accessors; no method returns a mutable alias into a
frozen value. Catalog and model pointers follow the immutable-definition
convention; instances and mutator transforms use per-battle clones.

```go
// internal/content
type SimulationSources struct { /* private immutable captured source set */ }
func CaptureSimulationSources(fs vfs.FSOps, mapName string) (*SimulationSources, error)
func (s *SimulationSources) Filesystem() vfs.FSOps
type SimulationInputRequest struct {
    Catalog *Catalog // prepared once under the selected rules/mutators
    SimArt *SimArt   // optional precompiled value, validated against snapshot
    MapOTA, MapTNT string
    MapSchema uint32
    AIProfile string
    CommunityDigest [32]byte
    Mutators Mutators
}
type SimulationInputs struct { /* private frozen storage */ }
func FreezeSimulationInputs(sources *SimulationSources, r SimulationInputRequest) (*SimulationInputs, error)
func (i *SimulationInputs) Digest() [32]byte
func (i *SimulationInputs) Manifest() []SimulationInput
func (i *SimulationInputs) Catalog() *Catalog
func (i *SimulationInputs) SimArt() *SimArt
func (i *SimulationInputs) Model(key string) (*model.Model, bool)
func (i *SimulationInputs) Filesystem() vfs.FSOps

// internal/session
type MatchConfigRequest struct { /* public positional fields of §8.6, no defaults */ }
type EffectiveMatchConfig struct { /* private validated copy */ }
func ResolveMatchConfig(r MatchConfigRequest) (EffectiveMatchConfig, error)
func EncodeMatchConfig(c EffectiveMatchConfig) ([]byte, error)
func DecodeMatchConfig(payload []byte) (EffectiveMatchConfig, error)
func (c EffectiveMatchConfig) Digest() [32]byte
func ValidateMatchInputs(c EffectiveMatchConfig, inputs *content.SimulationInputs) error
func NewAdmittedSkirmish(inputs *content.SimulationInputs, c EffectiveMatchConfig, progress content.Progress) (*Session, error)

// internal/version
type BuildManifest struct { /* public positional fields of §8.7 */ }
func CurrentBuildManifest() (BuildManifest, error)
func EncodeBuildManifest(m BuildManifest) ([]byte, error)
func DecodeBuildManifest(payload []byte) (BuildManifest, error)
func (m BuildManifest) Digest() ([32]byte, error)
```

Field 12 adds two session functions: `MatchUnitRestrictions(cat
*content.Catalog, r content.Restrictions) ([]MatchUnitRestriction, error)`
maps a host's set against the unrestricted catalog, and
`RestrictionsFromMatch(cat *content.Catalog, records []MatchUnitRestriction)
(content.Restrictions, error)` reads records back, refusing what field 12
rejects. `FreezeMatchInputs` performs the second against the install's own
unrestricted catalog.

`SimulationInput` is the typed manifest record of §8.7 (Family and Presence
`uint8`, Key and FallbackKey strings, Ordinal `uint32`, SemanticDigest
`[32]byte`); diagnostic provenance has a separate accessor. Configuration
types mirror §8.6's names and widths, with `MatchSeat`, `MatchView`,
`MatchPolicies`, `MatchMod` and `MatchContentProfile` records and
`[]AIParam` per row. Codecs own validation and return no partially valid
value. Resolve and decode establish **schema validity**;
`ValidateMatchInputs` then establishes **admission validity** against the
same frozen content (map/schema, side ordinals, restrictions, content
profile, rule/mutator inputs and supported consumer policies), and a
constructor calls it before allocating a world or consuming either RNG. A
hash match alone is not admission. `NewAdmittedSkirmish` composes the
single-seat shape; every online battle composes through
`NewPlaytestSkirmish` (§16.4.2).

## 9. State digest and desync

**In play today** the clients compare the small unit checksum of §16.4 every
30 ticks, at the same completed tick; a mismatch stops the match without a
result (§9.3). The complete canonical checkpoint below is implemented for
diagnosis (§16.3) and is not yet exchanged during play.

### 9.1 The canonical snapshot

One versioned serializer writes the complete authoritative state in
canonical order — players 0–9, pool slots ascending, projectiles in pool
order, the effect pool with its fragment geometry, the strip objects and
the debris arena (L9), both stream states and draw counts, and the global
tick, but none of the clock's pacing fields (§4.4). Every float is written
as its exact bit pattern and every definition as a canonical content
reference bound to the content identity (§8.7). A NaN in authoritative
state fails the checkpoint as a defect, since its bit pattern differs by
processor (L1). Owners publish logical field schemas and deterministic
iteration order; pointers, Go layout, goroutine state and presentation
caches are not a schema. Its uses are the digest (SHA-256 of the
serialization, truncated to 128 bits on the wire), the desync bundle
(§9.4) and, later, rejoin by snapshot (§11.3). §16.3 holds the format.

**Boundary.** A checkpoint is the state after a tick's executor tail and
before any entry of the next tick applies; a pause changes nothing about
it. In a single-player replay the tail belongs to the last tick of its
recorded pump, and a live single-player host captures before its
paused-input boundary applies anything, so a recording and its playback
hash the same state. Only checkpoints of the same tick are compared.
Network and input queues stay outside world state.

**Modern AI.** A controller's memory — brain, private generator,
observations and any unapplied batch — is mutated by its worker between
deadlines and is **not** checkpointed. Nothing it holds reaches the world
except commands applied on the simulation thread at its reaction deadline,
so a diverged controller shows as different commands. The checkpoint
carries, per computer seat, a running hash of the commands applied for it
and the tick of its next deadline: simulation-thread values, so a
checkpoint joins no worker and costs the same whatever the workers do.
Engine upkeep for both kinds of computer player, and the Classic planner's
records, are ordinary world state.

A snapshot does not carry controller memory either. As a load does, a
snapshot rejoin restarts the controllers from observation on **every**
replica at once: the relay binds a controller-restart event to the first
tick after the snapshot's checkpoint, and in phase 1 of that tick each
client joins its workers, keeps each generator's position, discards the
controllers and rebuilds them (Q21). A brain that cannot restart from
observation cannot be offered in a room that allows snapshot rejoin.

**Completeness.** An owner-by-owner inventory gives every field that can
affect future behaviour, and a reason and reconstruction rule for every
exclusion (§16.3.5); it is reviewed whenever authoritative state changes.
The partial fingerprint locks keep their separate regression contract.

### 9.2 Cadence

The designed exchange: each client computes the digest at the checkpoint
after every tick divisible by 30 and sends it; the relay compares the
required participants for that checkpoint (catching-up clients and
spectators excluded), following its ordered membership. Each client keeps
owner sub-digests for the last 64 digest ticks and a cheap per-tick record
for the last 600 ticks — both stream states and draw counts, pool counts
and rolling per-owner summaries — so two clients' records name the first
differing tick and owner even when the divergence cannot be reproduced on
one machine. These histories are implemented (§16.3.7, §16.3.80). Each
required report will have a relay-side deadline; withholding digests while
reporting progress is a protocol failure.

### 9.3 What a desync does

**Today.** When the 30-tick checksums differ the relay fails the room and
every client stops at that tick without a result, whatever the room's
size. With two participants no strict majority exists; with more, a
majority continuation needs the digest exchange of §9.2.

**Later:**

- **Casual rooms: the battle goes on for those who agree.** When the
  participants reporting one digest are a strict majority, they continue;
  each dissenting seat enters the reconnectable-drop state (§11.1) and is
  offered the rejoin path (§11.2), and before that path exists it stays
  retained idle, removable only by vote. A seat that disagrees again at the
  same checkpoint never completes its rejoin. With no strict majority the
  battle ends without a result; once exact snapshots exist (M8) a dissenter
  is resynchronized from an agreeing snapshot, and a battle with no
  majority continues from the room creator's state.
- **Rated rooms: no client majority decides.** The battle stops without a
  certified result until a trusted replayer or referee rules (§12.6).

Neither policy silently replaces a client's state, and a seat moved to
rejoin is not thereby a loser.

### 9.4 Desync bundles and diagnosis

Later. A bundle holds the replay, the complete
build/platform/content/configuration identities, the sub-digest window, the
tick ring and a snapshot with its checkpoint tick. The headless tool
replays at finer cadence to localize a reproducible divergence and says
when it cannot reproduce one. Every explained desync becomes a regression
scenario (§17).

## 10. Replays

Every fresh skirmish and Survival battle is recorded to a replay file on the
local machine, single-player and online alike: each online player records
the stream its own client executes. A replay holds the battle's agreed
configuration and the inputs that reached phase 1, never the world, so
playback recomputes the battle and checks it against the recording's unit
checksums. Nothing a recorder does changes what a tick computes: with no
recorder attached a battle is bit-identical, and a recorder only copies
what the session or the relay hands it. The `internal/replay` package owns
the format, the recorders, the composition and the player; the session
owns the recording seams (`SetReplayRecorder`, `PrepareRecordedBattle`,
`StepRecordedPump`, `SetPresentationPerspective`).

**What is recorded.**

- **Single-player.** Every local human command, converted to the
  `SinglePlayerReplay` command form against the state it is applied to and
  stamped with the tick that actually applies it — the paused-input
  boundary included — and its local sequence number. A command the replay
  form cannot express stops the recording ("recording stopped"); the battle
  goes on. The host's pump boundaries are recorded too, because the
  executor tail's temporary-sight expiry depends on where pumps end (§4.5);
  a pump that runs no tick changes nothing and is not recorded.
- **Online.** The relay stream as granted: each tick's commands with their
  seats and stream positions. A recorder wraps the battle's
  `lockstep.Client`, so the relay, the driver and the network overlay are
  unchanged.
- **Checks.** `UnitStateChecksum` every 30 ticks, the cadence the online
  acknowledgement uses (§9.2), and the battle's initial checksum in the
  header.
- **Not recorded.** Campaign missions, battles continued from a saved game,
  watched or computer-only battles, measured or staged windows, content from
  an extra `--root` or `--mod-config`, a mod without an archive digest, the
  loopback play test (§16.4) and `--metal` windows.

**Header.** Format version, a kind byte (single-player or online,
skirmish or Survival), the encoded battle configuration (§8.6; for
single-player the one `MatchConfigForFreshBattle` resolves from the fresh
battle request), the match identity of §8.2 with an advisory build digest,
the initial unit checksum, the recording seat, the map name, the seats with
their names, sides, colours and roles, and the start time.

**File format, version 1.** The magic `NLREPLAY`, the version, the header,
then DEFLATE chunks of about 64 KiB of entries, each opening with the
stream state at its start so it decodes alone: command, pump run
(run-length: count × ticks per pump), checksum, and end (final tick and
reason: finished, left, recording stopped, room failed). Integers are the
netproto version-1 primitives. A file that stops inside a chunk, or without
its end entry, plays to its last whole chunk and is listed as incomplete.
A busy single-player battle takes about 4 KB a minute, half of it
checksums.

**Files.** Replays live in `$XDG_DATA_HOME/nanolathe/replays`, else
`~/.local/share/nanolathe/replays`, on every platform (in the browser
under `/settings`, which persists); `--replay-dir` chooses another. A file
is named `YYYY-MM-DD_HH-MM-SS_<map>_<skirmish|survival>[_online].nlreplay`
in local time. It is written as `.part`, sealed and synced about once a
minute from a host goroutine, and renamed into place when the battle ends;
a recording that ran no tick is removed. Each new recording prunes the
oldest finished replays to the newest 100.

**Playback.** Playback refuses a file whose content, map, mod, rules or
configuration identity differs from what this install can compose, and
names the difference; the build digest is advisory, since the checksums
decide. It composes the battle exactly as battle entry did — single-player
through the admitted path, which composes the battle ordinary single-player
entry composes, online through the online composition for the recording
seat — then runs the recorded pumps (`StepRecordedPump`) or granted ticks
(`EnqueueSeatCommand` and `StepGranted`), applying the recorded commands
through phase 1. The first checksum that differs ends the playback with
the tick it diverged at. A playback runs as the recorded seat and takes no
commands of its own; the simulation's local and viewing seats never move.
Viewing is presentation only (`SetPresentationPerspective`): any recorded
human seat's view, or the full map with fog off.

**Watching.** The main menu's REPLAYS button opens the Replays screen
(DESIGN_INTERFACE_HUD_INPUT "Replays"). It lists the replay directory
newest first — start time, map, game type, length and players — and marks a
recording without its end entry as incomplete, the file being recorded now
(which cannot be deleted), and a file that cannot be read, with the reason
in plain words. Watch composes the recorded battle on a job goroutine and
enters it as the recorded seat; a replay recorded under another installed
mod first mounts that mod through the ordinary content reload, as an online
join does, and a missing mod, a different copy of it or a missing map is
named instead. Delete asks first. A playback takes no orders; its overlay
and keys pause it, step its speed from ¼× to 8×, skip ahead a minute at a
time, cycle the recorded human seats' views and the full map, and return
to the Replays screen. At the recording's end, or at the first checksum
this build computes differently, it holds there with the end or
divergence line.

**Command line.** `--verify-replay FILE` plays a replay headless as fast
as it can, mounting the replay's own mod unless `--mod` chooses one, prints
one summary line and exits non-zero at the first mismatch or an
incompatibility. `--replay FILE` opens it in the window. `--record-replay
FILE` records a `--headless` or `--shot` battle to that file.

**Later.** Pacing notes, chat and session events, rewind, sharing, and a
canonical-checkpoint digest beside the unit checksums. Compatibility
requires a simulation that reproduces the recording; there is no
cross-release compatibility program (Q12, Q20).

## 11. Seats over time

**Today** a lost connection, a closed window or a client failure ends the
room for both seats without a result and removes no seat (§16.5). In the
lobby, a joiner who leaves frees seat 2 and the host leaving closes the
room (§16.6). The rest of this section is the approved design for M6–M7.

### 11.1 Leaving

A resignation and a voted final removal are session events with a tick the
relay assigns (§4.2); a resignation is itself a final removal. A
reconnectable drop and a completed rejoin are membership notes that change
no simulation state. No client may remove another seat by naming it in a
payload; retail instead accepts a peer's removal notice without a host
check `[08 R-LEAVE-01 §1]` `[08 R-LEAVE-01 §2]`.

- **Resign** is the seat's own immediate final removal, with no vote, and
  ends that seat's battle without a win. Retail's surrender question has a
  menu variant (peers delete positive-health units with no script,
  explosion or wreck, and run the kill script, effect-only explosion and
  wreck for a unit at non-positive health) and an exit variant (30000
  self-damage first) `[08 R-LEAVE-01 §7]`. **A Strict resignation is the
  menu variant as the other machines see it** (§19 O23): the resignation
  event deletes every unit of the resigning seat and, under Strict, of the
  computer seats it hosts, silently — no `Killed` script, explosion, wreck,
  kill or loss credit — while a unit already at non-positive health keeps
  the researched receiver path. No self-damage is applied. Each record is
  then cleared as a voted removal clears it, seats in ascending slot order
  and each seat's units in ascending pool order `[08 R-LEAVE-01 §4]`; that
  order is the protocol's, since retail's for the menu variant is only a
  Supported inference. Modern and Community follow the retention policy
  below.
- **A reconnectable drop** is a seat with no connection in play: its
  connection was lost or closed, the desync policy took it out of play
  (§9.3), or it made no progress past the lag bound (§4.4). A seat with a
  connection in play is **active**; one whose resume is admitted but whose
  rejoin has not completed is **rejoining**, still out of play, and returns
  to the drop state if the rejoin does not complete within
  `RejoinGraceMilliseconds`. An out-of-play seat's units are retained idle
  in every mode and its hosted computers keep running; the reconnectable
  window is itself a Nanolathe addition, since no retail reconnect protocol
  is known `[08 R-LEAVE-01 §10]`. No timer ends the state.
- **Final removal** comes only from the seat's own resignation or a passed
  removal vote, and closes the rejoin window. In Strict 3.1 a voted removal
  destroys the seat's units by the established peer-removal contract:
  effect-only self-destruct and no kill credit; a positive-health unit
  leaves no wreck, while an already zero-health unit keeps the full
  preamble and script-selected wreck. Stocks freeze; resources, scores,
  identities and the unit-limit slice are not redistributed
  `[08 R-LEAVE-01 §3]` `[08 R-LEAVE-01 §4]` `[08 R-LEAVE-01 §5]`.
- **Computer seats** follow their human host by mode (§6.6, Q26). Retail
  removes every slot of a departed human's machine, hosted computers
  included, in ascending slot order, each through the peer-removal path
  `[08 R-LEAVE-01 §2]` `[08 R-LEAVE-01 §9]`. Strict 3.1 removes a human's
  hosted computers with its final removal in that order — peer removal for
  a vote, silent deletion for a resignation. Modern and Community keep them
  running. Defeat alone removes no computer.
- **The hosting client** of an embedded relay is the relay: losing it ends
  the battle without a result. Retail migrates its host flag instead
  `[08 R-LEAVE-01 §8]`. A hosted relay has no such seat.

**Nanolathe online policy — the removal vote (Q27, Q28).** Retail removes a
silent machine through its time-out dialog, whose `REJECT` or time-out plus
120 seconds removes one silent machine and which closes when silent slots
span two or more machines `[08 R-LEAVE-01 §6]`, or through the host's
reject `[08 R-LEAVE-01 §1]`; each machine decides for itself. A lockstep
battle needs one decision applied everywhere at one tick, so in every mode
a vote replaces both:

- **Subject.** Only a seat out of play (dropped or rejoining). An active
  seat cannot be voted out; there is no vote-kick.
- **Caller.** Any active human seat still playing, other than the subject.
- **Electorate and threshold.** The active human seats still playing,
  excluding the subject, fixed when the vote opens. A strict majority
  passes; a tie fails; a sole eligible seat decides alone; an uncast
  ballot is no. The call casts the caller's yes; each voter casts at most
  one ballot and cannot change it.
- **Timing.** A vote may be called only once the subject has been out of
  play for a cumulative `RejoinGraceMilliseconds` since it was last active
  (§8.6 field 15), so repeated reconnects cannot reset it. Only a completed
  rejoin cancels an open vote and resets the grace. A vote stays open
  **30 seconds**, closing early once its outcome is fixed; after a failed
  or tied vote another against the same subject waits **60 seconds**; at
  most one vote per subject is open. A refused call carries its reason.
- **Mechanism.** Calls, ballots and results are relay messages, never
  stream entries. The relay tallies; on a pass it authors one final-removal
  event at the next assignable tick, the only part of the vote a simulation
  sees.
- **Nobody left to vote.** When no human seat still playing is active the
  relay holds its grants, and if none returns within
  `RejoinGraceMilliseconds` it ends the battle without a result. Watchers
  and spectators do not keep it running.

**Nanolathe Modern policy — final seat removal (Q6, Q26, Q29).** Strict 3.1
keeps the machine-group removal above. Modern and Community retain a
finally removed human's units under that owner, idle, with no new
commands, and keep its hosted computers running with its perspective. That
perspective stays live: the removed human's per-seat block — sensor pass,
temporary sight and the rest of §6.3 — keeps running; only its end-condition
evaluation stops (§6.5). Its retained units can be destroyed, but no
survivor needs to destroy them to win. Retention transfers nothing and
draws no random number of its own. Single-player, commander defeat and
reconnectable absence never select this policy.

**One answer covers both halves (Q28).** The choice between Strict removal
(destroy the units and remove the hosted computers) and Modern retention
(retain the units and keep the computers) is one answer through the
existing `gameplay.Mode` and `session.RuleSet` composition
(DESIGN_GAMEPLAY_RULES §9), supplied by every reserved set and joining the
`SeatRules` seam (§6.6); no room flag or second registry chooses it. Seat
lifecycle and vote state belong to the session and the relay.

M6 tests cover drops and final removals in all three reserved modes,
repeated removal and removal while grants are held, with Strict's
destruction, wreck, credit, loss-statistic and RNG effects, Modern and
Community retention, the Strict resignation's silent deletion, hosted
computers in each mode, and every vote outcome and refusal above.

### 11.2 Rejoining by fast-forward

Later (M7). The relay keeps the battle's configuration, seeds and stream. A
rejoining client receives them, composes the battle and runs the stream at
full speed without presentation or audio until it reaches the live grant.
Admission repeats the identity checks. A per-match seat credential proves
ownership without an account, and a new connection epoch fences off the old
connection.

The relay keeps each seat's last accepted client sequence and stream
position for the whole rejoin window, under §4.2's rules; its reply to a
resume names them, and the client resends its unacknowledged commands from
the next value, so a loss between acceptance and echo neither duplicates
nor loses an order. A catching-up client neither paces the battle nor votes
on digests; it becomes active through an ordered membership transition
after matching a required checkpoint, and that completed rejoin alone
cancels an open removal vote. Rejoin is offered only within measured
catch-up limits; reconnection is never promised merely because replay
outruns real time.

### 11.3 Rejoining from a snapshot

Later (M8), once the canonical snapshot passes exact restore and
continuation tests. The relay arranges a common checkpoint and obtains a
snapshot from an agreeing participant (casual) or a trusted referee
(competitive), validated before restore and verified by continuation before
the seat activates. Retail-format saves are not used (L6). The snapshot
carries no controller memory; every client restarts its controllers at the
following tick (§9.1).

### 11.4 Watchers and spectators

Later (M5–M7). Two different things:

- **A watcher** is retail's: an observer row in the configuration, chosen in
  the lobby where the host allows watching, with no commander (its
  placement draws still taken), the whole map visible and no score
  `[08 R-ENTRY-01 §5]` `[08 R-SKIR-01 §12]`. A defeated seat becomes one
  where watching is allowed or it still hosts a live computer; otherwise it
  takes the ordinary lost ending `[08 R-SKIR-01 §3]`.
- **A spectator** is Nanolathe's: a stream consumer with no row. It receives
  the stream delayed by a lobby-chosen interval, sends nothing but
  permitted chat, may join at any time by fast-forward, and may view any
  perspective or the whole map.

The relay enforces spectator release times on live frames, catch-up
history, snapshots and replay downloads; a client-side delay is
bypassable. Ranked rooms need explicit rules for full-vision watchers and
spectator chat.

### 11.5 Saving

Retail disables saving in multiplayer `[08 R-SAVE-02 §4]`, so a Strict 3.1
online battle offers none, and the first versions offer none in any mode.
A later multiplayer save would be the canonical snapshot plus the
configuration, resumed only by the same seats. Survival battles cannot be
saved in any case (DESIGN_SURVIVAL §11).

## 12. The relay and the lobby

### 12.1 Shape

One Go package, `internal/relay`, implements the relay. It runs in:

- **The hosted relay**, `cmd/nanolathe-server`: independent rooms of 2 to 10
  human seats reached by a six-character room code. Clients connect
  outward, so nobody opens a port. The project's instance is
  `relay.nanolathe.gg` on DigitalOcean App Platform (§16.5.6); the protocol
  is open and anyone may run one.
- **The loopback relay** of the local two-window play test (§16.4.2).
- Later, a relay **embedded** in the hosting player's client for LAN and
  direct-IP battles.

The relay holds no simulation, no catalog and no Cavedog asset, and never
decodes a command; it sees configurations as opaque bytes, commands,
acknowledgements and checksums.

### 12.2 Messages

The hosted protocol is **version 6**; §16.5.1–§16.5.2 and §16.6.1 hold its
rules. Messages are bounded, length-prefixed binary frames in `netproto`
primitives, carried over TLS or WebSocket (§12.3).

**Versions.** The relay serves versions 5 and 6 in the same rooms. It
accepts a Hello or a Describe of either, records the version each seat's
Hello spoke and answers that seat's Welcome in it; a Description is the
same in both. Only version-6 seats receive Progress, so a version-5 seat
receives exactly the version-5 stream. A Hello or Describe of any other
version is refused naming the served versions and the client's. A client
speaks version 6 and refuses a Welcome in any other version, naming both;
against a version-5 relay it therefore receives that relay's version
refusal.

| Direction | Message | Content |
|---|---|---|
| client → relay | Hello | protocol version, room code (empty to create), the seat's `LocalHello` (a joiner asks for any seat), flags (auto-start), and from a creator the room size and the encoded base configuration |
| client → relay | Describe | a room code; answered with that room's base configuration and size |
| client → relay | Team, Side, Colour | the sender's team, side or colour (0–9), while it is not ready; a colour another present seat holds changes nothing |
| client → relay | Configuration | from the host before Start, a replacement base configuration |
| client → relay | Ready | the ready flag, and when set the seat's configuration-identity digest and rehearsal digest (§16.7) |
| client → relay | Start | from the host seat only |
| client → relay | Submit | the client sequence and an opaque command payload (§4.2, §7.4) |
| client → relay | Acknowledge | the tick executed, the unit checksum at every 30th tick, the battle-ended bit and the seat-final bit |
| relay → client | Welcome | the room code and the assigned seat |
| relay → client | Description | the room's base configuration bytes and size |
| relay → client | Lobby state | the room size; per seat whether it is present and ready, its team, side and colour; and a mismatch bit when every present seat is ready but their digests differ |
| relay → client | Configuration | the room's latest base configuration, to each joiner and after every host change |
| relay → client | Started | the match has started, with the sender's slot; grants follow |
| relay → client | Grant | a sealed tick and its commands `{seat, sequence, position, payload}` |
| relay → client | Progress | version 6 only, after Started: the last tick compared across playing seats, the newest sealed tick, and per slot whether it still plays, whether its result is final, its last acknowledged tick and the relay's ping round trip (§16.5.2) |
| relay → client | Refused, Failed, Done | a refused hello, join, description or command, with its reason; a room failure with its reason; explicit normal completion |

**Later messages:** a resume with seat credential and connection epoch
(§11.2), digest reports with the agreed summary (§9.2), pacing
requests (refused while Q5 stands), seat requests and removal calls and
ballots (§11.1), chat, ping, desync and seat-status notices, and a
two-stage start in which the relay freezes the configuration, draws the
seeds and confirms Start only after every participant acknowledges that
exact configuration and its initial checkpoint.

**What the relay knows.** The relay runs no simulation, so it knows only
what connections tell it: the configuration bytes, which connection owns
which seat, readiness and digests, the stream it wrote, and each client's
acknowledgements and checksums. A rule needing a game fact gets it either
from **the sender** (a chat line's recipient seats, computed by the sending
client) or from **the clients' agreement** (a small summary carried with
agreeing digest reports: which seats still play and whether and how the
battle ended), never by the relay guessing. Command authority is decided
inside the simulation (§7.2).

### 12.3 Transport

Clients reach the hosted relay over TLS: natively (`host:port`) or as
RFC 6455 WebSocket messages behind the hosting platform's TLS terminator
(`wss://host/relay`, §16.5.6); the browser build only the latter, through
the browser's own WebSocket (§16.5.1). Certificate verification is
mandatory; plaintext is permitted only behind that terminator or for
numeric-loopback test modes. The stream needs reliable ordered delivery, so
a lost segment holds that one client until it is retransmitted.
Per-connection queues are bounded and a slow reader never blocks another
room. A datagram transport or QUIC is a later decision if impaired-network
measurements justify it.

### 12.4 Lobby and matchmaking

A room today is a frozen battle configuration reached by its code: the host
creates it, a joiner enters the code, both ready, and the host starts
(§16.6). Later the lobby grows room lists, seat editing, computer seats,
chat, display names and, last, accounts, ratings
and matchmaking as web services beside the relay. The retail battleroom
`[07 §12]` is the reference for what it offers (§3.3). Of retail's host
options, *game closed* stays room state and *watching allowed* goes into the
configuration `[08 R-SKIR-01 §12]`; a room has its own defaults
`[08 R-SKIR-01 §7]`.

### 12.5 Operations

One always-running relay instance; a restart or redeploy ends its live
rooms without a result, and there is no cross-instance room lookup,
database, autoscaling or reconnect. The room, connection, byte and time
bounds are §16.5.1, so one misbehaving room fails alone. A separate health
listener reports readiness outside the connection limit (§16.5.6).

**Public status.** `/status` (an HTML page that refreshes itself) and
`/status.json` on the relay's port show: players, matches, lobbies and
connections; totals since start of rooms, matches started, completed and
ended early by reason, and the peak player count; each open room by
sequence number with its phase, size, players, age or duration, ticks and
tick rate, orders and orders per second, bytes in and out, bytes still
queued and the last checksum-compared tick; each seat's readiness, team,
side, WebSocket round trip, acknowledgement lag in ticks and milliseconds,
and whether it is defeated or has left; and the last 20 closed rooms with
duration, players, ticks, orders and how each ended. Room pings every 5
seconds carry their send time, so each pong measures the round trip. The
page shows no room code (an invitation), address or process-level resource
figure.

**Operator log.** Once a minute the server writes one line with the same
counts plus process heap, total memory and goroutines. Routine logs carry
no room codes or credentials. Later: stream retention for rejoin, archives
under a declared release policy, and the trusted roles of §12.6.

### 12.6 Competitive authority and future state delivery

Later (M9). A relay orders input but cannot establish which world is
correct. Two trusted roles can:

- **A trusted replayer** plays the recorded stream headless after the match
  on an admitted build and reports the terminal result and digests. Ranked
  admission requires it.
- **A live referee** runs the same simulation during the match, the only
  role that can adjudicate a desync while play continues or supply a
  trusted snapshot.

Both need the game content and simulation capacity where they run, which
the content-free relay never has. The result service binds its
authenticated report to the match identity, participants, configuration and
stream; losing that authority leaves a match uncertified, never awarded.
Ranked rooms also need a declared scheduling policy (the casual
earliest-tick scheduler favours low latency, and neither a client's claimed
issue tick nor a fixed extra delay equalizes it), authenticated identities,
cheats disabled, explicit target admission (§7.2), spectator embargoes and
fixed pause, drop and adjudication rules. These room policies govern
command admission and timing only; any gameplay departure uses the existing
`session.RuleSet` seams.

Refereed lockstep still gives every client the whole world. If competitive
play must withhold hidden state, a **server-authoritative state mode** —
the server simulates and publishes each seat's permitted state — remains an
option with its own schema, recovery, bandwidth and presentation driver.
The command admission and committed-frame boundaries are kept so it can be
designed later.

## 13. Threat model

Retail's admitted peers may name other players in removal and player-record
payloads `[08 "Packet framing and dispatch"]`. Nanolathe's defences:

| Threat | Defence |
|---|---|
| Ordering another seat's units | The relay stamps the seat; every simulation drops commands whose actors the seat does not own (§7.2). |
| Rewriting another seat's options | The configuration is frozen at Create and covered by its digest; no payload can replace it (§8.6, §16.6). |
| Taking another seat's resources with an abnormal gift | Online `Give` admits only a positive whole amount unless cheats are permitted (§7.1, §7.4.1, Q8). |
| A client on a different simulation | Identity comparison, the initial checksum and the rehearsal before Start (§8.2, §16.7); the 30-tick checksum during play (§16.4). |
| Changing one's own state (resources, health, build time) | Honest replicas apply no uncommanded change; a diverging client fails the checksum and the match stops. A cheat that does not diverge the simulation is undetectable by any hash. |
| Seeing through fog | Every lockstep client holds the whole state; hashes cannot prove camera compliance. Withholding hidden state needs §12.6's different architecture. |
| Ending or stalling a match | A room that fails, desyncs or stalls ends without a result (§9.3, §16.5.2). |
| Flooding the relay | Per-room and per-connection limits; a misbehaving room fails alone (§16.5.1). Holding rooms open is a known gap (§16.5.1). |
| Attacking an opponent's connection | The hosted relay never publishes player addresses. |
| Removing another seat (later) | Only the relay authors another seat's final-removal event, on a passed vote against a seat out of play; there is no vote-kick (§11.1). |
| Seat theft or duplicate input on reconnect (later) | Per-match seat credentials, fenced connection epochs and per-seat client sequences (§4.2, §11.2). |
| Forged results (later) | Ranked results need the trusted replayer and result service (§12.6). |
| Automation, macros | Not preventable; community moderation. |

**Whoever runs the relay is trusted** with the stream's order, tick
assignment and checksum comparison, and later the seeds, digest outcomes
and the removal-vote tally; clients cannot check those from the stream. A
hosted relay places that trust with its operator; an embedded relay will
place it with its hosting player. Client anti-cheat software is not
planned.

## 14. Packages

The relay stays content-free and simulation-free: commands cross it as
opaque payloads, encoded and decoded by the session that owns their type.

| Package | Responsibility |
|---|---|
| `internal/netproto` | The protocol leaf, standard library only: version-1 wire primitives, a bounded reader that refuses malformed input before allocating, and the seat `Identity`. |
| `internal/relay` | The loopback and hosted relays: rooms and codes, the lobby, client-sequence admission, tick assignment and grants, pacing, checksum comparison, the terminal handshake, TLS and WebSocket transports and the health listener. Imports only `netproto`. |
| `internal/lockstep` | The client drivers: grant-gated ticks, playout, acknowledgements and checksums (`NewLocalDriver`, `NewPacedDriver`). |
| `internal/session` | Seat commands and their codec (§7), match configuration and admission (§8), perspectives and per-seat results (§6, §16.4.1), granted single-tick pumps (§4.5), the rehearsal (§16.7), the replay recording and playback seams (§10), and the canonical checkpoint and histories (§16.3). |
| `internal/content` | Frozen simulation inputs and the content manifest (§8.7); effect holds in `SimArt` (L9). |
| `internal/version` | The build manifest and its stamp (§8.7). |
| `internal/sim/numeric`, `internal/sim/checkpoint`, `internal/effects` | The portable numeric kernel (L1); the canonical checkpoint encoder (§16.3); the authoritative effect pool (L9). |
| `cmd/nanolathe-server` | The hosted relay; imports only `relay` and `netproto`. |
| `internal/replay` | The replay file format, the single-player and online recorders, the composition of a recorded battle and the synchronous player (§10). |
| `cmd/nanolathe` | The MULTI entry, online screen and lobby, configuration freezing and adoption, the play-test and relay launch options, replay files, playback and `--verify-replay`, and the battle host. |

Guards in `internal/architecture`:

- **Network boundary** (`TestNetworkStaysInTheModFetcher`). `net` and
  `crypto/tls` are imported only by `internal/modfetch`, `internal/relay`,
  `internal/lockstep` and the commands; `net/http` only by
  `internal/modfetch` and `internal/relay`; no authoritative package ever
  imports them.
- **Content-free relay.** `internal/relay` imports no engine package but
  `netproto`, and `cmd/nanolathe-server`'s dependencies stop at `relay` and
  `netproto` (`TestRelayCommandHasNoSimulationOrDesktopDependency`).
- **Authoritative list.** Any new authoritative package, `internal/effects`
  included, joins `authoritativeDirs` and inherits the determinism guards;
  authoritative packages cannot import `internal/render`.
- **Conversions and library calls.** The two I2 source guards (§5.4).

DESIGN_MODS_MUTATORS decision D5 admits a relay connection as the client's
one network use beside mod and map downloads (Q1).

## 15. Decisions

The current policy rules, by ID. Each is a Nanolathe decision, not a retail
finding.

| ID | Rule |
|---|---|
| Q1 | Multiplayer is relayed deterministic lockstep, in scope with replays and the lobby; the client may connect to a relay (DESIGN_MODS_MUTATORS D5). |
| Q2 | Multiplayer is a mode-independent session kind, offered under every gameplay mode, with owner-machine equivalence and the online command-authorization exception of Q8. Mechanical departures still use `session.RuleSet`; single-player is unchanged except the declared M1/M2 changes. |
| Q3 | "Strict 3.1 online" is defined by owner-machine equivalence (§6), which keeps every single-seat battle bit-identical. |
| Q4 | A computer seat borrows the perspective of the human that added it, fixed in the configuration. Out of play, that human's computers keep running with its perspective in every mode; final removal follows Q26 and Q29. |
| Q5 | Nobody pauses or changes speed online: normal speed, thirty ticks a second. Relay grants still wait for lag or disconnect. A later policy needs a decision before §4.4's mechanism is enabled. |
| Q6 | A dropped seat is retained idle until a removal vote passes (Q27). On final removal Strict 3.1 destroys its units and removes its hosted computers in ascending slot order (peer removal for a vote, silent deletion for a resignation, §19 O23); Modern and Community keep the units idle and the computers running. One rule answer selects both halves (§11.1). |
| Q7 | Delivery follows §16's milestones. |
| Q8 | Cheat and debug commands need the room's cheat permission, enforced by every receiver in every online mode, including retail's ungated world-changing commands (own-stock `+NoMetal`/`+NoEnergy`, `+Selectable`, `+LOSType`, later `+ShootAll`). Competitive rooms disable it; the developer phrase cannot bypass it. Online `Give` of a positive whole amount within its producer's range is ordinary sharing; any other amount needs the permission, under which the signed retail transfer applies to every whole amount from −2^31 to 2^31; non-finite or non-whole amounts are always rejected. `SetResource` is cheat-gated. Single-player keeps retail behaviour. |
| Q9 | Casual rooms keep the researched target admission; ranked play needs an approved delayed-observation contract first (§7.2). |
| Q10 | The project runs one public relay reached by room code; the protocol is open. |
| Q11 | Display names until ranked play needs accounts. |
| Q12 | A replay needs a simulation that reproduces it; no cross-release compatibility program is planned. |
| Q13 | Desync: a room ends without a result. Later, casual rooms continue with a strict majority and move dissenters to rejoin or the drop state; rated rooms are uncertified until a trusted replayer or referee rules (§9.3). |
| Q14 | A client lacking the host's mod may fetch it through the verified catalogue download (later). |
| Q15 | Identical catalogs are required; roster negotiation may come later as a catalog filter. |
| Q16 | Retail's unit-restriction table is configuration field 12; the online lobby carries the host's restrictions (§16.6). A restriction-editing lobby screen, and a decision on retail's seeded `wacky` state, come later. |
| Q17 | The architecture must support eventual competition: verified results by a trusted replayer, a live referee where rooms promise mid-match recovery, and an explicit integrity-level decision (§12.6). |
| Q18 | A match may restrict tactical views for every player, enforced in ordinary clients (§8.4). |
| Q19 | No cross-play with other engines is planned. |
| Q20 | One simulation per room; a replay or desync bundle names the build it needs. |
| Q21 | A snapshot rejoin may restart the Modern AI's controllers on every client (§9.1), subject to O21's play test. |
| Q22 | One shared directed alliance matrix in every online mode, row B its transpose; declarations stay one-way and every victory requirement remains (§6.7). |
| Q23 | Strict keeps one computer per human; Modern and Community allow several within the available seats. Every computer has explicit agreed difficulty and a fixed human host; computers are excluded from Deathmatch for now; there is no battle-wide difficulty word (§6.6). |
| Q24 | One canonical explored history per player; a respawn or watcher rebuild resets only the entering human's own and hosted-computer histories (§6.7). |
| Q25 | Periodic map shares apply on the request tick in canonical human order; explicit gifts on their tick in stream order; no transport-delay emulation (§6.7). |
| Q26 | At a human's final removal Strict 3.1 removes its hosted computers with it in ascending slot order `[08 R-LEAVE-01 §2]` `[08 R-LEAVE-01 §9]`; Modern and Community keep them running with the host's live perspective. While the human is only out of play its computers run in every mode (§6.6, §11.1). |
| Q27 | Final removal goes through a vote whose subject is a seat out of play; there is no vote-kick; the electorate is the connected human seats still playing; no timer removes a seat; resignation needs no vote (§11.1). |
| Q28 | Protocol values (Nanolathe's, revisable here): a rejoining seat counts as out of play; the grace is cumulative out-of-play time; only a completed rejoin cancels a vote; an uncompleted rejoin returns to the drop state after `RejoinGraceMilliseconds`; a 30-second vote window that closes early; a 60-second cooldown after a failed or tied vote; one open vote per subject; a fixed electorate; the call casts the caller's yes and ballots cannot change; a battle with no active playing human ends without a result after the grace; a lag bound moves a connected seat that makes no progress into the drop state (M6); the client-sequence rules of §4.2 and the Refused message; one `session.RuleSet` answer for unit retention and hosted computers; at most 10,000 area entries and an actor-times-entry product of at most 1,048,576 per online command; no battle-wide difficulty word. |
| Q29 | A finally removed seat is absent from the end-condition sweeps in every mode, as retail's cleared record is `[08 R-LEAVE-01 §3]` `[08 R-LEAVE-01 §5]`; a seat only out of play blocks victory until voted out. Under Modern and Community the removed human's per-seat block keeps running, so the perspective its computers borrow stays live (§6.5, §11.1). |
| Q30 | Under Modern and Community a hosted computer keeps playing, with normal sight, after its host human is defeated: the host's countdown gates only the human, and the defeated host's sensor pass stays an ordinary viewer's while one of its computers lives. Strict 3.1 keeps retail's one countdown per machine (§6.6). |

## 16. Delivery plan

Multiplayer is delivered in milestones, each behind the one before it.
Online play (§16.4–§16.7) took the pieces of M5 and M6 it needs,
in dependency order, ahead of M4; it does not complete those milestones.
Every single-player battle stays bit-identical except where a contract here
explicitly says otherwise.

| Milestone | Delivers | State, or done when |
|---|---|---|
| **M0 Adoption** | The §15 decisions, the scope in ARCHITECTURE and the agent instructions, the invariant amendments (§5.4). | Done. |
| **M1 One simulation on every host** | Effect holds in `SimArt` and the pool timed from them on every host (L9); the portable numeric kernel and defined conversions (L1); fingerprint locks from amd64, arm64 and js/wasm builds and a lock scene that fills the Strict effect pool. | Done (§16.1). |
| **M2 Commands, configuration and identity** | Seat-attributed commands, receiver-side permissions, allocation serials, local interface state, explicit wire schemas, complete content/build/configuration identities and the match policy fields. | Done (§16.2). |
| **M3 Canonical checkpoints and digest** | The reviewed state inventory, the canonical writer and owner sub-digests, the bounded histories and the computer seats' application records. | Implemented; native cross-platform comparison pending (§16.3). |
| **Online play** | Perspectives, lobby teams and per-seat results for 2–10 seats, computers the room host adds, and online Survival (part of M5); the loopback and hosted relays, room codes, the lobby, the rehearsal and the browser build's relay connection (part of M6). | Done (§16.4–§16.7). |
| **M4 Replays** | Recorder, playback, pump ends, pacing notes, digest checks and a headless replay command. | First step built (§10): automatic local recording, playback and `--verify-replay`. Done when long Strict and Modern replays agree across platforms and between a windowed recording and headless playback, through pauses, speed changes and late AI workers. |
| **M5 One world per player** | The rest of §6 — alliances, sharing, watchers, per-seat option bits — and the multi-seat harness (§17). Computer seats the room host adds are built (§6.6). | The harness passes for two to four human seats with computer seats under every registered rule set; single-seat locks unchanged. |
| **M6 LAN and room codes** | The embedded relay and LAN/direct lobby, chat, departures with removal votes and the mode's final-removal rule (§11.1), shared view restrictions (§8.4), digest exchange, desync bundles and the casual desync policy (§9), and the pacing state table (§4.4). | Mixed-platform battles finish on a LAN and through the hosted relay; hostile commands are refused; §11.1's tests pass in every reserved mode; a seeded desync in a three-seat battle leaves two seats playing; impaired-network and CPU-stall runs meet budgets declared before acceptance. |
| **M7 Public service** | Room list, hardened rooms and queues, seat credentials and reconnect, measured fast-forward rejoin, delayed spectators and controlled archives. | Public play plus duplicate/half-open reconnect, slow-reader, digest-withholding and embargo-bypass tests pass; rejoin is advertised only within measured limits. |
| **M8 Exact snapshots and recovery** | Full world-state readers, the controller-restart event, checkpoint transfer and continuation (§9.1, §11.3). | Byte-identical round trips and matching long continuations, computers restarted at the snapshot tick on every replica; malformed snapshots refused; late-game rejoin within a declared budget. |
| **M9 Competitive admission** | Trusted replayer, authenticated results and accounts, a live referee where promised, approved scheduling/target/pause/drop/watcher/view policies, and the integrity-level decision (§12.6). | Unequal-RTT, hostile-client, colluding-hash, result-forgery, authority-loss and spectator-isolation tests pass. |
| **Later product work** | Ratings and matchmaking, any multiplayer save, filtered state delivery if chosen. | Scoped after the correctness and integrity gates. |

### 16.1 M1: one simulation on every host

M1 makes every host compute the same battle: one portable numeric kernel for
authoritative arithmetic (L1) and effect timing from content, not from a
renderer (L9). It is implemented. Its vectors pass natively on darwin/arm64,
linux/amd64 (v1 and v3) and windows/amd64, and under js/wasm; live
comparisons with Go's library run only in `GOAMD64=v1` builds, and every
target checks an independently generated digest of the same 16,384 radian
input pairs. Full cross-platform
world equivalence is M3's test, not M1's.

| Unit | Delivers |
|---|---|
| **U1 Numeric kernel** | `internal/sim/numeric`: distance and its truncating shortcut, the radian functions, the angle table, two conversions, their vectors; Go's BSD-licensed routines adapted unfused and credited in `NOTICE.md`. |
| **U2 Call sites and guards** | Every library call and raw float-to-integer conversion in authoritative packages routed through the kernel; the two I2 source guards. |
| **U3 Effect timing from content** | Effect holds compiled into `SimArt`; the pool timed from them at composition; the renderer timing resolver removed; the Strict pool lock. |
| **U4 Pool under the guards** | The fixed effect pool, its admission adapter and fragment/debris state in the authoritative package `internal/effects` (in `authoritativeDirs`); draw lists and trail particles stay in `internal/render`. |
| **U5 Ratchets** | Locks run from amd64 and js/wasm builds beside the native one in `tools/check-retail`; kernel vectors in CI on linux/amd64, darwin/arm64, windows/amd64 and js/wasm. |

**Public API.** Package `internal/sim/numeric`:

```go
// Distance is retail's two-argument distance [01 R-DET-01 §7].
func Distance(x, y float64) float64

// TruncatedDistance is TruncateFloat64ToLow32(Distance(x, y)) for every
// operand pair, reached by a shortcut whose proof is in its comment.
func TruncatedDistance(x, y float64) int32

// The radian functions. Each returns the same bits on every platform.
func SinRadians(x float64) float64
func CosRadians(x float64) float64
func TanRadians(x float64) float64
func Atan2Radians(y, x float64) float64
func AcosRadians(x float64) float64

// AngleSinCos is the sine and cosine of a 16-bit angle word, from a table of
// all 65,536.
func AngleSinCos(a Angle) (sin, cos float64)

// The conversions that complete TruncateFloat64ToLow32.
func TruncateFloat64ToInt64(v float64) int64
func RoundFloat64ToInt32(v float64) int32
```

Package `internal/content`:

```go
// EffectEntryHolds reports the per-frame holds of one effect entry, each
// max(authored, 1), for the default bank or a bank a weapon names.
func (a *SimArt) EffectEntryHolds(bank, entry string) ([]int32, bool)
```

**Contracts.**

- **M1-C1 Distance.** `Distance` performs the sequence of `[01 R-DET-01 §7]`
  in integer arithmetic, special operands, overflow and truncating underflow
  included. NaN class and precedence are established; payload and sign stay
  a research Unknown and a code-site placeholder. For finite operands it
  equals, bit for bit, a big-number model kept in the test: every ordered
  integer pair to 3,000 (retail tier; a sample in the fast tier), raw 16.16
  deltas, fractional and wide-exponent operands and the section's vectors.
  `TruncatedDistance` returns the truncation of `Distance` for every pair;
  its comment proves when the shortcut may answer, and its test adds
  distances within a few units in the last place of a whole number.
- **M1-C2 Radian functions.** Results equal the Go 1.27.1 unfused
  `GOAMD64=v1` library's bit for bit: asserted live in a v1 build and against
  committed reference data everywhere. A v3 library may fuse and is no live
  reference. Every product is rounded before it is added; the fusion guard
  holds with no new allowance.
- **M1-C3 Angle table.** Entry `a` is the two radian functions of
  `float64(a) * 2 * math.Pi / 65536`.
- **M1-C4 Conversions.** `TruncateFloat64ToInt64` truncates toward zero and
  returns the processor's 64-bit indefinite for a NaN, an infinity or a value
  that does not fit `[01 R-DET-01 §1]`. `RoundFloat64ToInt32` rounds to
  nearest even and returns the 32-bit indefinite, `0x80000000`, in those
  cases `[01 R-DET-01 §2]`.
- **M1-C5 Call sites.** No authoritative package calls a library
  floating-point function outside I2's exactly rounded list or converts a
  float to an integer outside the kernel. Both are source guards with no
  allowance outside `internal/sim/numeric`. The locks hold on arm64 and amd64.
- **M1-C6 Cost.** The kernel costs the simulation benchmark at most three
  percent of process CPU per tick over the library calls it replaced; the
  five sites that truncate a distance call `TruncatedDistance` for that.
- **M1-C7 Holds.** `SimArt` holds every entry of the default effect bank and
  of every bank a weapon names, compiled in sorted order from the battle's
  own files; a missing bank or entry reports unknown. Banks compile under the
  loader policy the client shares (`content.EffectBankMaxBytes`,
  `content.EffectBankGAFLimits`); a bank that fails is listed in
  `SimArt.Diagnostics`.
- **M1-C8 One timing source.** The pool reads holds from `SimArt`, bound at
  composition before the first unit script runs. No host can supply timing.
- **M1-C9 The pool lock.** The Strict benchmark scene with seed 5 is locked
  at step 4,500. Its pool is first seen full at the end of the step reaching
  tick 2,255, and by step 4,500 the effect service has refused 143
  admissions for a full pool (a cumulative count no tick reads; shatter quads
  refused inside the pool excluded). The test asserts a refusal at capacity
  before comparing the fingerprint, and forcing the timing lookup off moves
  the constant. The other locked scenes stay below their pools' capacity.
- **M1-C10 Guards.** The map-order, float, fusion, goroutine and import
  guards read the pool's code, and `internal/render` holds no state a tick
  reads. The numeric-portability and fusion guards also read the load-time
  packages the simulation reads (`vfs`, `formats`, `internal/content`,
  `internal/community`, `internal/gameplay`), listed apart from
  `authoritativeDirs`. A closure test classifies every in-module import of
  `internal/session` as authoritative, load-time input or a named
  presentation edge.
- **M1-C11 Ratchets.** `tools/check-retail` runs the locks from an amd64
  build where the host can execute one, and from a js/wasm build under
  Node 22 or later with `GOMAXPROCS=1`, because the browser build's seats
  play beside native ones. Each says SKIPPED where the host cannot run it;
  a lock that runs and fails fails the gate.

### 16.2 M2: commands, configuration and identity

M2 makes the command boundary explicit and the battle inputs identifiable.
It is implemented. Command schemas, size proof, stale references and
command APIs are §7.4.1–§7.4.4; configuration, frozen inputs and build
identity are §8.6–§8.8. Many detailed encoding rules are recorded at their
code sites; this section keeps the contracts. `NewAdmittedSkirmish` composes
only the single-seat shape; every online battle composes through
`NewPlaytestSkirmish` (§16.4.1). A command whose application needs per-seat
perspectives (M5) stays refused online, naming its gate; tests never fake it
by changing `LocalOwner` or `ViewingOwner` around dispatch.

| Unit | Delivers | Contracts |
|---|---|---|
| **U0 Schemas** | §7.4's schemas, §8's input inventory, the API boundary below | M2-C3–C5, M2-C9 |
| **U1 Allocation references** | Battle-wide creation serials, committed references | M2-C1, M2-C3 |
| **U2 Explicit commands** | Stamped seat commands, receiver authorization, one shared applier | M2-C2–M2-C4, M2-C7 |
| **U3 Local interface state** | Selection and other interface state held by the client | M2-C6, M2-C7 |
| **U4 Frozen simulation content** | Captured, frozen simulation inputs | M2-C8 |
| **U5 Build and configuration identities** | Configuration codec and digest, build manifest, `SeatRules`; U5b match admission | M2-C9, M2-C10 |
| **U6 Codecs and admission integration** | `internal/netproto` primitives, the seat-command codec, the join comparison | M2-C4, M2-C5, M2-C10, M2-C11 |

**API boundary.** The shared reference holds a `pool.Handle` and a nonzero
`uint64` allocation serial, exposed by the unit world and copied into the
committed frame. Session owns the typed seat command, its payload codec and
the adapter that accepts externally stamped seat, tick and stream position;
that metadata is never in the payload, and the local adapter supplies it
with no network. Content exposes one immutable admitted-input value with its
digest and manifest, and composition consumes that value. Configuration and
build have separate canonical encoders and digests. Names and signatures are
in §7.4.4 and §8.8.

**Allocation references (U1).** Both successful creation paths assign the
serial after the fallible COB bind and before creation callbacks; nanoframes,
forced-slot reconstruction and ownership-transfer replacements share the
sequence. A reused slot rejects the old reference, while internal raw-slot
damage keeps its retail aliasing. Committed frames copy the serial apart
from the publication-only `InstanceID`. Near exhaustion an in-flight creation
reserves capacity before pool allocation, so a nested binder cannot take an
outer creation's final serial; failure releases the reservation and keeps
any allocator RNG draws. The reservation is transient call state, not
checkpoint state; the counter and each unit's serial are.

**Frozen simulation content (U4).** Skirmish and Survival entry capture
sources before the catalog compiles (`content.CaptureSimulationSources`),
compile, resolve the map and prepare rules and mutators through the capture,
then freeze (`content.FreezeSimulationInputs`) every admitted unit's program
and model (never-built units included), the animation table, the map's OTA
and TNT, the AI profile and the extension inputs. A lookup the sealed view
never captured is a defined miss recorded in `UncapturedLookups`
(`ErrSimulationInputNotCaptured`), so an edit during a battle reaches only
the next battle. Composition binds models and programs from the frozen
inputs; a battle composed without them (campaign, restore, fixtures) reads
each model once through a cache keyed by provider entry and content digest,
with its own COB loader. `Catalog.Hash` and `Catalog.Manifest` are unchanged
by capture. The manifest digests only what the simulation reads; unit models
as the parsed hierarchy plus derived heights, feature and weapon models as
authored bytes; the map family as the selected map's header, OTA, TNT and
schema index. A supplied `SimArt` is checked file by file against the
capture; the single-player adapter recompiles a stale one. Audio keeps the
live mount. Campaign missions and restores are not frozen.

**Configuration and build identities (U5).** `MatchConfigRequest` carries
§8.6's fifteen fields and the positional seat rows. `ResolveMatchConfig`
validates and freezes a deep copy with its canonical encoding;
`DecodeMatchConfig` refuses oversize payloads, overlong or overflowing
varints, booleans other than 0 or 1, counts over their bounds before
allocating, trailing bytes and any non-canonical encoding. `Digest()` is
SHA-256 over `nanolathe/match-config/1` and that encoding.
`NewMatchConfigRequest` adapts `SkirmishConfig` and `SkirmishEntryOptions`.
Every field a role does not read must hold its canonical value, or the
request is rejected; `SharedVictory` must equal the team rule
`[05 R-SHARE-01 §1]`. `version.BuildManifest` encodes §8.7's fields under
`nanolathe/sim-build/1`; `internal/version/stampgen`, run by both installers
on the verified archive, writes `internal/version/stamp_generated.go`, and a
build without it reports itself unstamped, as does the startup report. The
Modern AI parameter vocabulary check is the leaf
`internal/aikit/brains/utiltac`; the online computer-seat cap is asked of
`session.SeatRules` (§6.6).

**Match admission (U5b).** Skirmish entry is `prepareSkirmishEntry`
(capture, compile, prepare, select the map, freeze) then `composeSkirmish`
(compose from exactly those inputs, audio mount as a parameter); the
single-player constructors call both. The frozen inputs record the freeze's
selections (map name and files, schema index, preparing rule, Community
digest, canonical mutators, restrictions). `ValidateMatchInputs` compares the
configuration with them and reports every failure at once (`errors.Join`),
wrapped as `ErrMatchConfigurationRejected`, `ErrMatchMapMismatch`,
`ErrMatchRulesMismatch` and `ErrMatchContentMismatch`, in that order:
map-entry selection re-run on the frozen map for the seat count must equal
the recorded and configured schema; every seat's side ordinal is checked;
restrictions must equal the frozen catalog's (§8.6 field 12); the catalog's
compile limits must equal the content profile's. Permissions, views,
policies and participant identities are not compared. `NewAdmittedSkirmish`
admits, then composes the single-seat shape (one human, no watcher, one
difficulty for every computer) without a second capture or `Normalize`, with
the agreed Community table as a final base layer that must resolve to itself
and each computer's parameters as its own layer; any other shape returns
`ErrMatchNeedsMultiSeat`. A retail test composes four setups both ways and
requires equal content digests and partial fingerprints at entry and every
600 ticks through 1,800. `FreezeMatchInputs` is the public front half. The
content profile's name and directory table stay unattested, a
`TODO(question)` in `match_admission.go`.

**Explicit commands (U2).** §7.4.4's session block exists. Stamped entries
ride the phase-1 queue in `HumanCommand`, so the drain and the paused
boundary (which stops at a stamped entry) are unchanged. One applier,
`applyBound`, serves the local adapter and the stamped path; the local
adapter captures references at submission and keeps its legacy semantics
otherwise. Online, the supported kinds apply; ATM, own-stock SetResource,
MakeSelectable and Meteor need the cheat permission, Spawn the permission and
the Modern set; `Give` follows Q8 and Q28; CommunityKickout needs its
feature. MobileBuild, CommunityOrderDrag, View, Visibility, DoubleShot,
HalfShot and the reserved kinds 35–45 are refused naming M5, except that the
online session admits MobileBuild with its known-site check (§16.4.2).
Local kinds,
replay-only NoShake and SetLogo, lobby-only Gameplay and unlisted numbers
are never admitted online; the single-player replay context applies every
S, D and R kind. Receiver rules: a stream position exceeds every position
accepted so far (§4.2); an online stamp names a human row not finally
removed whose record is neither Watcher nor observer; a replay stamp equals
`LocalOwner`; online coordinates fit the signed 32-bit raw 16.16 range (a
`TODO(question)` at `validPoint` covers formation offsets near the edge);
`CommandNoOp` is a stale or empty actor list, a stale singular actor, a stale
online ordinary target or a changed drag receipt; `CommandApplied` means
dispatched. `humanUnit` admits the issuer's units through a separate
attribution field, so group destinations serve remote seats.

**Codecs and the join (U6).** The version-1 primitives of §7.4.1 live in the
leaf `internal/netproto` (standard library only, audited by every
architecture guard), with a bounded reader that refuses before allocating;
the configuration codec and build manifest encode through them, pinned by
golden digests. `EncodeSeatCommand`/`DecodeSeatCommand` cover all 28
payload-carrying kinds of §7.4.2 in their contexts and establish schema
validity before gameplay code runs. A reference with serial 0 or a duplicate
actor has no version-1 form and is refused (`ErrSeatCommandNoWireForm`); a
replay form for them is a `TODO(question)` for M4. Negative zero is written
as zero and refused on decode. Work limits, unit limit, coordinate range,
permissions, key existence and ownership stay with the receiver.
`CompareMatchIdentity(local MatchJoin, remote netproto.Identity)` reports
every compared mismatch at once, one wrapped category each, in the order
protocol, content, map, rules, mod, configuration, never short-circuiting;
the build field is advisory (§8.2). The
map identity is SHA-256 of `nanolathe/match-map/1` over the frozen
manifest's map-family entries; the join compares the mounted mod with the
configuration's and the other seat's.

**Local interface state (U3).** Selection, the visited set, build pages,
BigBrother, held Shift and the online logo and shake overrides live in
`hud.LocalInterface`, keyed by `pool.UnitRef` in sorted slices. The session
reads no selection (Stance, Cloak and GroupAssign carry `Handles`) and
publishes `CloakRequested`, `StockpileRounds`, `RangeEligible` and, each
tick, `frame.InterfaceFacts`: step 7's readiness verdicts, reported in visit
order by a write-free observer on the units sweep
(`units.SetReadinessObserver`). The host applies local kinds at once, fills
selection-derived actors when it sends a command, and consumes every tick's
facts before the next input batch. Group assignment is a seat command;
recall is local, with unpublished assignments overlaid. The fact queue holds
256 ticks; when full it keeps the oldest and the host resyncs after the gap.
Online `NoShake` and `SetLogo` are local only. Across a retail save
`[08 R-SAVE-02 §6]` the host writes its selection and pages into detached
status words, and a load seeds local state once (`AdoptStatusWords`); the
visited bits are a `TODO(question)` in `internal/hud/localstate.go` and are
neither written nor seeded.

**Contracts.**

- **M2-C1 Serial lifetime.** One counter per battle. Each successful
  ordinary, nanoframe, transfer or forced-slot creation gets a distinct
  nonzero serial before its creation notification can publish it; failure
  consumes none and does not undo draws already taken. Exhaustion fails
  deterministically before wrapping. The counter and each live serial are
  checkpoint state. A session reconstructed from a retail save gets a new
  reference namespace and accepts no command retained from before.
- **M2-C2 Attribution.** The stamped seat, not a payload owner or local
  viewer, authorizes actors and seat-owned mutations. The receiver rejects a
  watcher, a removed seat, a foreign actor and a forbidden kind. Cheats need
  the agreed online permission in every rule set, whatever the developer
  state. The local adapter keeps single-player cheat, `Give` and `View`
  behavior, including `Give`'s own/controlling-slot source
  `[07 R-CAM-01 §6]`. No kind falls through to the local path online.
- **M2-C3 References and order.** A serial mismatch never resolves to the
  slot's new occupant. Each actor and target list has defined duplicate,
  empty, stale-member and stale-target behavior and processing order
  (§7.4.3). Stream positions order tracked commands; local interface events
  never enter the stream. A local pending receipt is distinct from the
  accepted stream receipt; payloads are deep-copied at enqueue. A handle
  captured at submission is never redirected to a unit created in its slot
  before phase 1.
- **M2-C4 Validation boundary.** Decode, validate and authorize before any
  gameplay mutation; reject unknown kinds and versions, extra bytes, invalid
  booleans and enums, overflowing or noncanonical integers and counts above
  their bound before allocating. A rejection leaves queues, resources, RNG
  and state unchanged but consumes its stream position. A gameplay service's
  researched partial work is not rolled back (§7.4). Tests send online `Give`
  without the cheat permission with negative, zero, non-whole, non-finite and
  out-of-range amounts, each rejected before any stock changes, beside a
  positive whole gift to a non-ally, which applies as retail's transfer does;
  with the permission a negative gift applies the signed transfer. Area
  orders over §7.4.1's work limits are rejected whole.
- **M2-C5 Command-size proof.** The worst-case payload is derived from the
  admitted unit capacity and every variable-length field. No kind splits
  implicitly into separately scheduled commands. The encoder-derived maxima
  are 2,991,707 bytes for the largest `Order` at the representation ceilings
  (single-player replay context) and 451,394 bytes under the online work
  limits (104 actors × 10,000 entries), both within the 4 MiB command
  ceiling; an area order whose outer target is not null is refused.
- **M2-C6 Local interface.** Local state is keyed by allocation reference.
  Selection readiness uses the existing predicate's full inputs, carrier
  readiness included. Group assignment writes authoritative group numbers;
  recall changes only local selection. BigBrother follows simulated ticks.
  The phase-2 readiness clear and the sweep-tail observation are preserved,
  including a unit unready and ready again within one tick. A host that
  presents only the latest frame of a catch-up batch still gets ordered facts
  for every tick and reaches the same local state. Online `NoShake` is
  presentation only. Local state never enters the multiplayer digest.
- **M2-C7 Fingerprint evidence.** A change that moves interface bits names
  them, enumerates their readers and writers, and compares baseline and
  candidate with only those bits masked; gameplay bits stay unmasked.
  Constants are never updated from unexplained output, and allocation
  serials stay out of the old partial fingerprint. Any other single-player
  change is declared with evidence; the one declared is M2-C3's correction.
  The selected bit, both visited bits and the page field (bits 22–25) are no
  longer session output; no lock constant moved.
- **M2-C8 Frozen content.** The content digest covers the effective
  definitions in semantic order, all admitted scripts, model geometry and
  derived heights, `SimArt` sequences and holds, the map, AI and extension
  inputs and applied mutators, with defined missing-input fallbacks. Later
  creation consumes the admitted value. A file edited during a battle leaves
  that battle unchanged and changes the next one's identity. Provenance
  paths stay out of identity, so identical inputs on different installs
  agree. Catalog regression hashes are preserved.
- **M2-C9 Effective configuration.** Canonicalize once, validate, freeze and
  hash exactly the value composition consumes. Equivalent defaults
  normalize to one value; every effective difference changes identity.
  Encodings are versioned, domain-separated and unambiguously framed, with
  explicit ordered collections; unknown required fields are rejected. Tests
  change one field at a time, refuse a spliced battle-wide difficulty byte
  and mutate every byte of a valid encoding, requiring a refusal or an
  identical re-encoding.
- **M2-C10 Build and admission.** The common release manifest identifies
  source contents, dependency and toolchain inputs and simulation-relevant
  build choices and lists the tested platform variants; a per-variant binary
  hash never rejects another variant of the same release, and a revision or
  `version.Profile` string never substitutes for the manifest. Online the
  build identity is advisory: it is reported and never refuses a match, and
  unstamped builds may play; the rehearsal (§16.7) and the in-game checksum
  detect simulation differences (§8.2, §8.7). Mismatches of the compared
  identities are reported per category, and no initial-state check replaces
  them.
- **M2-C11 Single-player replay inputs.** Every local command that changes
  authoritative state, RNG or later commands is accounted for; replaying
  single-player `NoShake` and `Gameplay` keeps their effects. The online
  context rejects the same bytes before dispatch, a replay decode delivered
  to an online session is refused at phase 1, and no payload-supplied mode
  bypasses the check. M4 records and replays these inputs.

**Effective-input inventory.** Each row is identity input; tests change one
field at a time and cover equivalent defaults.

| Input family | Treatment |
|---|---|
| `SkirmishConfig` | Gameplay selection, map, seat rows in slot order (controller, side, colour, team, nickname, resources, Classic or Modern), per-computer difficulty (Q23; no battle-wide word online, §8.6 field 5), location and commander-death modes, mapping/LOS/type, unit limit, both seeds, every Survival option. |
| `SkirmishEntryOptions.BuilderOptions` | Each seat's initial six-value preference; later changes are seat commands. No receiver reads its own preference. |
| `CommunitySources`, match/mod selection | Rule table and digest, base and name, content profile, mod identity, version and archive digest, mutators; provenance stays separate. |
| `ContentLimits` | Every effective table size and read cap. |
| `AIOverrides` | The canonical parameters each computer consumes, after all layers resolve. |
| `SimArt` | The compiled content, defined misses included. |
| `AutomatedPlayers`, `Progress` | Online override fixed false; progress callbacks excluded. |
| Additional online fields (§8.1) | Computer host seats, teams and shared victory, restrictions, permissions, drop, pacing and audience policies. |
| Seat assignment | Human rows in protocol identities; reconnect secrets and socket details excluded. |
| View policy (§8.4) | Tactical scale bounds and full-map permission, stored and compared; enforcement is later work. |

### 16.3 M3: canonical checkpoints and digest

M3 delivers the canonical checkpoint writer, the full and owner digests, the
bounded histories and single-seat verification. Owner writers, session
capture, both histories and fault diagnosis are implemented, and local cost
measurements pass the declared limits (§16.3.83); native-platform comparison
is pending. These are Nanolathe implementation contracts under §9, not retail
findings or gameplay policies.

M3 leaves the existing partial fingerprint and its locks unchanged. Exact
restore is M8, recording and playback are M4, multi-seat perspective
generalization is M5, and relay desync decisions are M6. A retail save, a
committed frame, a debug capture or `PartialStateFingerprint` is not a
complete checkpoint. `RecordAIControllers` is a save operation that joins
workers, so the regular checkpoint never uses it.

#### 16.3.1 State-owner inventory

The authoritative field inventory is §16.3.5; it is not permission to
serialize every Go field, and every change to an authoritative type updates
its disposition there.

- **One section per field.** A field belongs to one canonical section even
  when several services hold its pointer. The ownership edges are recorded,
  including the scheduler shared by session and movement, COB state held by
  units, temporary sight held by the session but affecting visibility, and
  effect storage beside publication. Sub-digest labels name logical owners,
  not packages.
- **Content identity.** Definitions are identified by the family, record
  ordinal and key `content.SimulationInput` already uses, never by name
  alone. A dynamic definition or continuation without such a key needs an
  explicit logical representation before it can be written.
- **Exclusions require evidence.** Host clock anchors, carry and speed
  hysteresis, network and input queues, local interface state, audio
  playback, frame buffers, renderer caches, tracing and profiling are outside
  world state (§4.4, §9.1). Consumed stream position and pump information are
  external metadata; unconsumed inputs are never hashed. A scratch field is
  excluded only if it is overwritten before every read, and a derived field
  only if its reconstruction preserves every value and iteration/tie order.
  A `Save` omission or a comment calling a field a cache is not that
  evidence. The Modern brain, private generator, observation and unapplied
  command contents keep §9.1's explicit exclusion; M8 owns their restart.

#### 16.3.2 Capture and encoding contracts

**M3-C1 Boundary.** Capture runs on the simulation thread after all work of
the checkpoint tick and before any next-tick input or paused-input mutation.
In single-player, an interior tick of a multi-tick pump is captured after its
publication, and the pump's final executed tick after `runRetailPostLoopTail`
(the publication observer runs before that tail, so it is not the capture
site). A pump shortened by battle termination uses its actual final tick.
Full digests and the tick ring use the same boundary. Entry state is captured
separately, after battle-entry initialization, and is not labelled a
completed tick. Zero-tick pumps neither append nor replace a checkpoint
(§4.5).

Single-player pump semantics are unchanged. Tests compare host schedules
that replay **the same explicit pump boundaries and input positions**:
regrouping single-player ticks can legitimately change sight expiry, and a
targeted test with a temporary-sight expiry inside a multi-tick pump exposes
that. Each granted online tick is its own pump (M5). A known pre-M5 host-kind
dependency is a reported gate; it is never hidden by leaving state out of the
hash.

**M3-C2 Canonical values.** One versioned byte schema, independent of Go
layout, native integer width, addresses and map iteration, with section IDs
and order, field widths, signed representation, lengths, presence and
definition/reference keys specified (§16.3.6). Semantic sequence order is
preserved, including heaps and linked lists; canonical sorting applies only
to unordered lookup tables. Every floating field keeps its exact bit pattern,
signed zero included. A NaN fails capture with the owner and logical field
path, returns no usable digest and changes nothing (§9.1). The online
`amount` validator is not reused, and no floating arithmetic is added.

**M3-C3 Identity and failure.** The schema is bound to M2's complete content
identity and effective configuration; build and platform provenance stay in
comparison and bundle metadata. A missing admission attestation is named, not
assumed from equal initial state. Missing owners, unrepresentable references
and non-quiescent transient state fail capture; a nil optional owner has an
explicit representation. No reflection, `unsafe`, retail-save encoding or
renderer accessor supplies the schema.

**M3-C4 One writer.** The canonical encoding streams to an `io.Writer`, so
hashing needs no full snapshot. The full digest is SHA-256 of those exact
bytes; only its first 16 bytes go on the wire (§9.1). Owner sub-digests use
the same owner encodings under an explicit owner/schema domain, not a second
field list. Diagnostic byte capture and digest capture agree. A writer error
returns failure, never a digest of truncated state. Capturing, repeating a
capture or enabling history changes no world value, RNG draw, queue, pending
callback or worker state.

**M3-C5 Modern AI boundary.** Regular capture never calls `Join`,
`Generator`, `RecordAIControllers`, a brain method or a worker-owned reader.
The simulation thread publishes a value-only record per computer seat:
controller kind, applied-command chain, next-deadline presence and tick, and
the engine-side fields §16.3.5 retains. An absent or pending deadline is
distinct from tick zero. The record holds actual application order and
allocation references, not Go pointers or worker batch storage. The chain
covers accepted no-ops and failed or partially applied actions at their
commit sites (§16.3.7); hashing emitted intents or success counts alone does
not satisfy §9.1. Classic AI gets no new command path, and Modern AI's
scheduling and APM behaviour are not changed to make them observable.

**M3-C6 Bounded diagnosis.** Sessions retain the last 64 owner-digest
checkpoints at the 30-tick cadence and a 600-tick ring (§9.2). A ring row
holds its tick, both RNG states and draw counts, pool counts and per-owner
fixed-width summaries, and is computed without the canonical writer or a
retained snapshot (§16.3.7). A rolling sum is diagnostic evidence, not an
equality proof. Comparison uses only overlapping ticks and reports when the
first divergence predates the window or the owner is not covered. Host reads
copy records without draining data a later bundle needs. Both per-tick cost
and retained memory are measured.

#### 16.3.3 Public API gate and work sequence

Session owns checkpoint boundaries, capture configuration and bounded
report/history access (§16.3.6–§16.3.7). Each state owner writes its logical
section through the shared encoder without exporting mutable internals or
importing session. The value-only Modern AI record sits at the existing
`ai`/`aikit` boundary, so session does not import the controller. Content
owns definition-key resolution. The streaming encoder is the
standard-library-only leaf `internal/sim/checkpoint`. Checkpointing observes
the bound rules: it adds no gameplay seam, registry or selectable policy.
Exact snapshot readers and relay APIs are outside it, and no convenience API
is added for a speculative M8 reader.

The 16.3.x headings and the code use these work-unit labels:

| Label | Scope |
|---|---|
| U0 | Field dispositions, schema and API (§16.3.5–§16.3.8) |
| U1 | Streaming encoder, framing, error propagation and content-owned keys |
| U2 | Units, allocation, orders and COB writers |
| U3 | Terrain, features, visibility, movement and path writers |
| U4 | Economy, construction, combat and effects writers |
| U5 | Classic and Modern computer-player state and application records |
| U6 | Session/runtime/mission/Survival writers, composition, capture lifecycle and both histories |
| U7 | Acceptance: single-seat harness, platform and host-kind evidence, fault diagnosis, cost |

#### 16.3.4 Acceptance and carried gaps

**M3-C7 Completeness evidence.** Each owner's dispositions are reviewed
against its actual readers and writers. Small authored fixtures mutate
representative retained fields and require the full digest and the owning
sub-digest to change; varying excluded caches, allocation addresses, lookup
insertion order and presentation settings must change nothing. Fixtures
include same-name definitions with distinct records, a freed and reused unit
slot, a paused COB return or wait, a suspended search, stale target lists,
temporary-sight expiry, a full effect pool and Survival spawn progress.
Testing a writer against its own output is insufficient. Round trips and
long restored continuations are M8 tests.

**M3-C8 Equivalence and diagnosis.** Scripted single-seat Strict, Modern and
Community scenes with the same content, configuration, input positions and
pump ends (one to five ticks, pauses included) agree at every requested
checkpoint on native Darwin/arm64, Linux/amd64 v1 and v3, and Windows/amd64,
and across windowed and headless hosts with audio and presentation
variations. Modern worker completion timing varies under a fixed reaction
contract; checkpoint reads never wait, and later application histories
agree. A seeded fixed-width fault between full-digest ticks is located to its
first retained tick and owner from two rings alone; ring wrap, absent
overlap, summary blind spots and error reporting are tested. A failure owned
by M5 is a recorded gate, never fixed by omitting state or changing
single-player behaviour. M4 extends these tests to replay files.

**M3-C9 Cost and landing.** Cost is measured as bytes, encoding CPU and
allocations per checkpoint, digest-only versus byte capture, ring CPU per
tick and retained memory, on matching busy scenes in sequential
`tools/sim-bench` runs, disabled against the baseline and enabled at §9.2's
cadence, with idle and busy AI workers measured apart from deadline joins.
The numerical limits are §16.3.83. Disabled capture adds no tick work, and
every single-seat fingerprint lock and RNG history is preserved.

**Carried gaps.**

- The replay-only zero-serial/duplicate-actor proposal (`seat_command_codec.go`)
  is left to M4; M3 does not widen the command schema.
- The visited-bit meaning is a research question; the inventory guesses no
  mapping.
- Profile name/directory attestation is an admission gap
  (`match_admission.go`); §16.3.8 gives the fail-closed conditions.
- The preparing rule identity and cloned content limits are admitted by
  §16.3.34. None of these is settled by an initial-state digest; O10 (§19)
  and exact continuation (M8) stay open.

#### 16.3.5 U0 field dispositions

This is a contract for writing the implementation's state, not evidence of
retail behaviour, and it refines §16.3.1. **Retain** means actual stored
values, even where a supported rule switch is needed to expose a reader;
**binding** means an admitted immutable identity or a checked composition
edge; **exclude** means the stated reconstruction or no-reader reason
applies. The dispositions are reviewed again whenever the named
implementation changes; preserving current values does not close a retail
gap.

**Field order and widths.** Table field names are schema names. Within a
record, retained fields are encoded in bytewise lexical order of their
source spelling, with abbreviated coordinate/array families expanded into
individual named fields; nested records follow the same rule unless an
explicit framing below overrides it. Declaration order, padding and pointer
layout are irrelevant. Fixed-width source scalar types give the wire width;
Go `int`/`uint` use 64 bits, and named numeric types their underlying width.
Sequences carry their length and keep stored order; maps sort by encoded
logical key (numbers numerically, strings by raw bytes, compound keys
componentwise). Fixed arrays omit a length. Optional values carry explicit
presence. Each writer documents its expanded field list beside its
implementation, and a new retained field changes the schema version.

**Session, runtime and scenario.**

| Type/source | Retain or bind | Exclude or boundary condition |
|---|---|---|
| `Session`, `session.go`, `state.go`, `result.go` | `State`, `Clock.GlobalTick`; `Gameplay`, active `Rules` identity, effective `Community`, `EntryCommunity`, mutators/restrictions, builder options; RNG initialization, entry seeds, both stream states and draw counts; `LocalOwner`, `EnemyOwner`, `ViewingOwner`; `VictoryDone`, `DefeatDone`, `Latch` (`Countdown`, `Bits`, `Pending`); `resultPending`, winner/loser/reason/draw fields, `resultArmedTick`, commander-death array, all Deathmatch counters, `deathsWithNoRecordedCause`; latched result `Ended`, `Draw`, `Winners`, `Losers`, `Reason`, `Tick`, `ArmedTick`, `Countdown`. | No pending battle transition at capture. `result.Kind`, `WinnerTeam`, score presentation and column maxima are views of retained accounting. `Snapshot`, publication copies, scratch walks, trace/probe/observer fields, diagnostics, `bigBrother` facts, HUD/debug display and audio device state are excluded. |
| Session configuration | Effective skirmish/player values, mode-independent options, campaign slot/side/known arrays, `Progress` bank fields, `battleEntryTailDone`; immutable mission/map inputs are admitted bindings. `seatCommands.removed` belongs here when implemented. | Nicknames/colours and source provenance are metadata unless a live consumer affects work; strip colour selection is classified below. `rulesDefaultsApplied` is load bookkeeping. Human/network queues, sequences, receipts and last consumed stream position are external metadata; no command dispatch may be in progress. Clock anchor, delta, carry, requested/active speed, pause and slew are host pacing (§4.4). |
| `Wind`, `MeteorState`, camera shake driver | Wind `Strength`, `Heading`, `Scalar`, `DirX`, `DirZ`, `NextChange`, `Changed`, effective `Min`/`Max`; every `MeteorState` scalar and its weapon identity; shake active/duration/remaining/amplitudes/offsets and `noShake` (the driver controls CRT work). | Wind `LastChange` has no runtime reader; `BriefingCountdown` is front-end state. Camera/view transforms are excluded, the driver's RNG gates are not. |
| `postLoopState`, `eyeballRecord`, visibility stamps | Temporary-sight records in list order: `owner`, `sightDistance`, `heightByte`, `x/y/z`, `expiry`, `cx/cz`, `emitter`, `published`. Stamp map by handle, each `cx/cz/radius`. | Message-retirement callbacks, tail traces and publication counts do not control sight. Interior and final pump ticks stay distinct (C1). |
| `communitySchemaState`, mission/triggers | `active`, mission binding, `playerByStart`, `neutralOwner`, ordered `deferredPlacements`, `nextDeferred`; ordered victory/defeat trigger lists, each `Kind`, `Type`, `Args`, `Completed`, `Celebrated`, `CenterReady`, `CenterX/Y/Z`. | Mission type, schema/start positions, placements, specials, initial features, wind bounds, authored triggers, difficulty, use-only and campaign selection are immutable input bindings. Diagnostics are excluded. Mutable trigger progress is never replaced by the authored list. |
| `survivalState`, `survivalUnit`, `survivalClass` | `attacker`, slot-ordered `team`, `settled`, all account `Stock/Capacity/Earned` pairs; effective `tuning`, `opts`, pool records and indices; centre/start class/region, `classes` in allocation order; phase/end, wave/plan (group/pick order), spawn cursors, last spawn, wave-unit membership, retarget deadline; survived/wave points/clean-loss, per-player stats, removed-health map; each unit's `h`, `wave`, `target`, `infecting`, `shun`, `shunSerial`, shun cell/deadline. Classes retain key, copied profile, region dimensions/labels/sizes and base. | Region caches were built against the terrain of their time and are never regenerated from today's world. Pool definitions use content keys. `walk` is rebuilt scratch. Deposit/report history and coordinates have only setup/report consumers after entry. |
| Survival shared AI input | `info` is one scenario-owned record, including ordered warnings and their data; managers bind that same record. The wave budget, group angles/domains/picks, pool tier/domain/cost data and all effective tuning values. | Worker-owned copies stay under §9.1. Survival's result view is reconstructed from the retained director and stats. |

**Allocation, units, orders and COB.** Raw handles keep weak-handle
semantics; allocation references also preserve object identity. A stale
pointer is never redirected to the slot's current occupant.

| Type/source | Retained logical fields | Exclusions and representation |
|---|---|---|
| `pool.Units`, `units.World` | Arena limit, alive/definition arrays, player slice start/end bounds; physical slots tagged never allocated/live/freed residual; live/created counters and last successful allocation serial. A freed raw record retains exactly `Handle`, `Owner`, `Kills`, `Remaining`. | `used` is validated from alive records; `slotIndex` is the identity index. Pending allocation serials must be zero. Finalized definition-index maps derive from the admitted catalog; unfinalized fixture maps are represented explicitly or refused. Iteration hints are scratch. |
| `units.Unit` | `AllocationSerial`, `Handle`, `Owner`, `X/Y/Z`, `Health`, `MaxHealth`, last damage side/cause, `Alive`, `Dying`, death cause/hooks, `Remaining`; `Flags`, build/busy/yard/bugger-off/armour/building/group/mover/restored-mode/pending state; occupancy/sight cells, footprint, structure facing, reveal deadline; bob phase, engagement target, metal spot, activation/cloak/hidden/kills/paralysis/stun; current/prior samples and move tier; placement index/identity/name; physical piece flags. | Definition, scripts and orders are explicit edges. `LOSByte`, restored AI group and weapon target-fixup words are load staging; minimap `BlinkSuppress` and `Move.PendingHeading/PendingSpeed` are presentation/parity bookkeeping, distinct from authoritative steering. |
| Unit nested records | Every weapon slot's `Reload`, `Flags`, desired yaw/pitch, `Ammo`, muzzle/aim-origin pieces, distance, weapon key, target and aim readiness (`IssueBit`, `Ready`, `readyWord`); target kind/raw unit/X/Z. Move mode/mirror/heading/pitch/bank/speed/velocities. Attachment carrier/piece and ordered cargo. | Script/VM/bridge aliases must agree rather than be encoded as unrelated copies. `readyWord` is restored separately and is not a Boolean. The desired-aim initialization question stays open. |
| `orders.Queue`, `Node` | Primary/secondary order; danger and firing-position state; `lastPumpTick`. Each node's ID/phase/gates/deadline/owner/target, goal/guard/cache coordinates, parameters, creation/satisfied/flags/move/path state, build key/facing, caption flag, human move sequence, crowded-arrival and automatic-work/attack/next-target state. | No detached pump node or detached-successor context at capture. Installed nodes must have consumed `QueuedIssue`/`GoalSupplied`. Retail subtype words are save/restore staging. `secondaryTick` and diagnostics are debug-only. |
| Queue auxiliary state | Danger impacts/contacts in slot order with every validity, sector, tick, coordinate and failure-deadline field; response/resume/return nodes, anchor/withdrawal/decision/quiet/opportunity state; contacts keep allocation identity. Firing-position node/owner/target identities, active/attempt/start state. Crowded-arrival active/since/lastTick/X/Z/goal coordinates. | A node or old allocation held outside the current queue is still a graph root. Queue index is not an object identity. Attested handlers/adapters and their owner bindings replace function pointers. |
| `cob.VM`, `Thread`, `axisAnim` | All eight thread slots, every stack cell (including cells above SP and in inactive threads), PC/status/SP/sleep/waits/signal mask; statics, pieces, animation lanes, dirty, active count, tick denominator; thread identities, next identity, last-return value/validity/identity arrays; every move/turn/spin lane target/speed/busy/acceleration/active field; piece rotations/translations. | Local allocation can reveal old stack cells. Diagnostics, drain/pose caches, cache revisions and scratch busy flags are excluded. Piece shading/visibility cache booleans are recomputed; the active render-flag store is retained because COB and debris read it. Presentation-only VMs are unsupported (`presentationInstructionLimit` must be zero). |
| COB binding/bridge | Program/model identities; VM alias, `createInvoked`; pending gameplay return continuations, identified by VM/thread allocation identity, continuation kind/mode, target unit allocation/weapon slot and captured raw deletion key. | Program code and model data are frozen inputs; piece links derive from them. Lifecycle trace queues, link notes, last-started/last-query scratch and transform caches are excluded. A trace-only return closure is not a gameplay continuation. |

The production asynchronous gameplay return is combat's slot-aim completion;
its value descriptor is attached at the existing callback installation site,
which is not executed, replaced or cancelled. Trace-enabled return closures
produce the same state as tracing disabled. An unrecognized callback with a
gameplay effect fails capture. `combat.pendingAims` has writes and deletes
but no reader and is excluded; the actual readiness and continuation are not.
Feature sequence/geometry/burn/smoke/steam/sound ports and the
visibility/movement reader ports are checked composition bindings; a non-nil
function pointer alone does not establish the admitted binding.

**World, visibility, movement and paths.**

| Type/source | Retained logical fields | Exclusions and representation |
|---|---|---|
| Terrain/plots | Row-major occupancy words, metal, feature index/sentinel, anchor/damage, gameplay flags; `metalSeeded`; feature names/definitions in record order, including nil rows and runtime appends. | Immutable geometry/heights, physics/map constants and LOS words bind admitted map inputs. The entry void sweep must be finished. Never-explored marker and placer nibble are presentation-only; any otherwise unclassified flag bits are retained. Static obstacle revision is diagnostic. |
| `features.Service`, `Instance` | Global reproduction cursor, arena held, instances by sorted anchor index, exact active head-to-tail order; definition, cell/position/velocity/orientation, footprint, burning/animating/countdown/suppression, cursor frame/delay/sequence presence/key, active/arena/runtime-live flags, animation selector, damage accumulator. | `runtimeLive` affects replacement transforms. Lookup caches rebuild from sorted keys; active linkage reconstructs from the retained active order. Active-walk/pending-burn handoffs are rejected. Last reproduction index, reclaim/status/sinking/settled/shadow presentation and saved anchor staging are excluded. |
| `visibility.Service` | Semantic mode bits, dimensions, row-major word mask; byte grids by player then row; local/team/viewer-defeated; footprints by observer ID with owner/cells/height/radius/quantized/live/stored cells/stored byte; effective Community inputs. | Fog caches, the mode cache-valid bit, publication versions/identities and rebuild flags are excluded. Spokes derive from immutable ray tables. The sensor index rebuilds every sensor tick; `sensorInputs` and `sensorStatusByID` have no production readers. Contact bits live in unit flags; cadence in session/ledger. |
| Movement base state | Per-handle routes, steers, collisions, flights, copied profiles/names, working sets, previous move tier/SFX band; layer registry, learned terrain, pending layers, active orders/next activation, arrival handles, move/record goals, provider, first requests, unreachable/jam/traffic/pocket state, work tick/smoothing budget, pilots, repair landings, air-base lists; effective fallback/Community/path-player/unit-limit inputs and current tick. | The tick must be ended, the overlap scan inactive and the provider eligibility cache invalid. Diagnostics, path failures, history/lab counters and per-call scratch walks are excluded. `passAlliance`/`trafficNow` are replaced at BeginTick for the recognized pure rules; unknown stateful rules cannot claim that exclusion. |
| Route/profile/steer | Entire route `Points[20]`, count/active/dirty/repath/request/status/first-hold/pending; all eight profile footprint/water/slope fields; steer X/Z, heading/pending heading, dirty/speed/max velocity/turn rate/height/sea-level/definition flags/acceleration/brake. | Inactive route storage remains readable. `Route.StaticRevision` has only diagnostic readers. |
| Collision/flight | All collision scalars, yard values, half-bias state, filing, saved/proposal/stamp/blocker/lean/turn fields; all flight scalars including mode mirror, targets, gravity/bank/pitch, and unit/command edges. Filing retains `Filed`, `OffMap`, `SX`, `SZ`, `Seq`, cargo included. The selected air sector is nil/record index/sentinel, never recomputed from position; tags are nil 0, record 1 then zero-based u32 index, sentinel 2. | Flight-command `Flags` is stream/publication bookkeeping; the command otherwise retains payload/owner/unit, position, velocity and heading. Immutable air-sector records bind terrain. Retained collision residuals are not reconstructed from current transforms. |
| Occupancy/layers | Row-major ground/air cells, plane dimensions, link sequence, off-map and sector heads, all link next/prev/cell/linked/off-map values; pending filing rows and duplicate-suppression rows. Layer names in allocation order, membership, each copied profile/dimensions/packed cells/watermark and per-handle commit tick/set. | Logical occupants are presence plus signed i64 identity, not identity-plus-one storage. Ground/air counts derive from occupied entries. Grid revision is diagnostic. Plot and mover occupancy may differ legitimately. Class stamp buffers are overwritten scratch. |
| Movement goals/state | Active order/token, arrival order/goal/threshold/payload/border, move order/coordinates/goal, record goals in installation order; working-set search/goal/activation/through. Learned-grid dimensions/words. Clearance route order/cells; unreachable order/activation/since/goal; jam run/replan/until/limit/cooldown/pocket; traffic side/deadline/round/steering/ahead/through/routeless/start/goal presence/coordinates; pocket order/since/grants/token. | Goal pointer sharing affects working-set reuse, so aliases are preserved. Repair landings retain admission order, exact unit/node/pad, piece/reserved/holding/anchor. Movement owns the stale combat air-base lists once. |
| Claim/arrival pilots | Claim dimensions/have/serial/own rows; nullable owner grids, all/slow directional counters and written lists. Arrival move-ground/standby, per-handle rows (all seen/node/place/footprint/coordinate/check/exchange/member/best/stood fields), claim/generation/reservation grids. A composite pilot has four ordered nullable child states. | Claim trail and arrival live/fresh/member walks reset before use. Counter wrap does not discard marks: captured claim serials and arrival membership tags remain readable. |
| Path provider/scheduler | Requests by player/handle with raw unit, player, start, goal and activation; provider cursors/started/tick/players/limit. Scheduler base/set/scales/call count/have-last, optional active request/player/scale, player cursor/service counts/accumulators/allowance/unit limit/player count. | Staged indexes derive from keys; sweep polls reset per call. Inactive request residuals, traces and diagnostics are excluded. |
| Goals/searches | Point centre/radius/**radiusSq**; annulus centre/inner/outer/**innerSq/outerSq**; rectangle; saved forms reconstruct the same three variants. Search config descriptor and scale; logical entries sorted `(Z,X)` with status/direction/node; nearest/distance/presence, tolerance/presence, notified/seeded/done/result points/status, popped/setup/expanded counts; nodes in allocation order with cell/G/H/F/terrain/run/parent/direction/open/closed/hSet; heap **array order** `(id,f)` and spent state. | Thresholds are stored, not recomputed. Heap positions and node back-references are validated derivations. Workspace generation/capacity/lending/dense-versus-sparse storage is excluded after every logically visible entry is extracted, including rays and terminal entries without nodes. The search fan is scratch. |
| Search wrappers/payloads | Straighten/smooth variant, wrapped search, config, probes, done/status/out. Air marker flags/radius/altitude/heading/attach piece/unit/raw target/goal/radial; velocity marker saved flags/aux/trailing, unit/position/velocity/commanded/steer. | Wrapper finishing buffers are synchronous scratch. Payloads preserve sharing with record goals and flight commands. Unknown goal/search/kernel/pilot/payload variants fail capture. |

Suspended path closures carry value metadata recorded at their creation
sites: selected class/layer and copied profile; requester/owner/footprint;
captured revision tick; base/learned/through/jam/static/hostile view variants
and bindings; the wedge override's captured start and bounds; the optional
finishing-leg view; the claim owner grid, **all versus slow** row, shared
own-row identity, captured serial, dimensions/footprint/per/against; and the
revision callback's registry/class/profile/requester/tick. These are operands
of the existing closures, not a new rule interface, and current units cannot
reconstruct them. Capture never invokes the closures or snapshots mutable
claim rows in place of the shared references. Retail, Straighten and Smooth
kernels are the closed set; laboratory or custom variants return an explicit
error until reviewed.

**Economy, construction, combat and effects.**

| Type/source | Retained logical fields | Exclusions and representation |
|---|---|---|
| `economy.Player`, ledger | Slot-ordered stock/capacity/mirror/AI production/consumption/update/waste/totals/pass counters, kills/losses/commander counters, archived mirrors; all bucket production/requested/accepted/carry and archived production/requested pairs, Metal before Energy. Per-unit buckets by physical handle. Player exists/control/observer/option/full-income/ended/countdown, directed allies, autoshares/thresholds/storage bonus, side/watcher/rejection/result auxiliary. Service reference player, networked, optional selector and effective Community. | Archived accounting is retained as battle accounting even where consumers are reports. Names/logos/rank/timers/sensor-call counts are presentation or diagnostics. `aiAggregatesPrepared` is reset before the next settlement consumer. Callbacks bind session/unit/world operands. |
| Construction | Builder links by product; placements by product with rectangle/definition/creation-oriented yard; two repair-bank entries per builder in slot order, target/remainder; repair-world binding; kick records X/Y/Z/valid; effective Community/rules/mode/limit/special-state bindings. | The rotation cache derives from immutable definition/facing; row-registration caches from the immutable descriptor registry. Active reclaim/VTOL/completion pump contexts are rejected. Diagnostic admission/message/command/permanent/kill records and builder debug identity are excluded. Progress, products and queues are unit/order state. |
| Combat pool | Count/capacity/dead flags and **every projectile record through capacity**, residual records beyond count included; all stored fields: weapon, position/start/target, unit/projectile/shooter/side/muzzle references, velocity/speed/distance/angles, creation/burst/expiry/smoke deadlines, burst flags/count, beam/two-phase/dead, orientation and cached cell/floor/state/marker values. | Reservation clears only part of a reused record, so the live prefix alone is not enough. The pool diagnostic payload and compaction scratch are excluded. Full residuals are a deliberate conservative inclusion. |
| Combat service | Double/half shot, opaque liquid, effective Community/rules; target last-rebuild/gate and primary/secondary lists in stored order, scan cursors; death-notified map with allocation identities; Modern incoming live-span target/shooter/weapon/motion/beam-invalid, modern tick/next-projectile tick; ordered transport captures (allocation/handle/type/health), tick/presence. | Air-base lists belong to movement. Impact stack and transport pending handoff must be empty. Query/candidate scratch and one-visit firing observations are excluded. Model box centres and weapon lookup derive from immutable content. Process-global projectile presentation IDs are excluded. |
| Community area damage | Cells with stamp/head/tail/count; nodes in insertion order with unit/next; width/height/limit/built tick/built/stamp; hit-generation array and counter. | The current generation is zero outside a damage transaction. Counter wrap skips zero without clearing marks and admission uses `>=`, so these are not scratch. Rebuild walk and saturation count are excluded. |
| Effect admission | `EffectService.max`, `nextID`, `lastSequence`; bound fixed-pool identity. Event-buffer effective limits, next ID/sequence and exhausted flag. | Zero or exhausted identity can refuse future events. Diagnostics are excluded. The bound production service has no ownerless pending fallback; an unsupported nonempty fallback fails capture. |
| Fixed effects/fragments | Capacity, ordered records, fragment slots/cursors/round-robin, gravity/sea level and effective fragment step inputs; record source/target/kind, XYZ/velocity/gravity/expiry/model-presence/fragment slot/explode-on-hit; both animation players (index/countdown/loop/active/frame count/durations). Fragment live/base velocity/angles/angular rates/vertices. | Durations are stored values, producer overrides included. Graphic/presentation identity/flash/material metadata is excluded once lifecycle operands are retained; `FrozenFragmentMaterial` is host artwork, differs in headless fallback and never controls admission, physics or RNG. |
| Debris | Storage charge/count/cursor/serial; ordered partition occupied/start/charge/slot/generation; each slot's live flag and generation/point span/position/angles/velocity/angular rates/lifetime/fall/explode-on-hit; occupied geometry spans. | Dead-slot payload and free point spans are overwritten before reuse. Expired slots can leave charged partitions, so those partition-generation links are retained. Model/piece/material/smoke/fire drawing metadata is excluded. |
| Strips | Ten lists in strip order, container insertion order, live/capacity/steady limits; family/window/spawn deadlines/interval, source/destination/extents, particle life, phase modulus, frame-delay/count inputs, smoke selector; particles in order with coordinates/velocities/expiry/frame/delay/phase/last-frame and deferred frame-draw state. | Recycled particle capacity is scratch. Colour-only fields are classified separately; a cursor that controls a later lifetime or draw is retained even if publication also reads it. |

**Computer players.** Manager and executor state is retained even for a
Modern seat; the exception covers only worker-owned planner material.

| Owner | Retain | Exclude or bind |
|---|---|---|
| `ai.Manager`, `Strategic` | Player/passive/controller/modern-wave-air, deadlines/origin/surface metal/mission gate/factory allocation/countdown/loss deadline; all nine ordered groups; wave engagement/rally initialization, best/probe/drift coordinates/score/targets. Strategic centre/radius, ordered metal spots, land/water region dimensions/offsets, refresh/build-capable/live count/unit limit/max wind and bound/readiness flags. Sorted counts/class/init/single vectors. Effective controller parameters, battle seed, start positions/owners. | The sorted catalog type cache and per-pass unit walks rebuild deterministically. Strategic initialization draw ledgers and intermediates are not future state; their resulting regions and readiness are. Shared analysis and Modern resume-generator material fall under §9.1/M8. Survival input binds the scenario record. |
| AI profile | Presence, plan, effective weight/limit maps and per-record maps; name, directive stream/text-loaded, all-plan and fixture tables; applied-catalog presence/key and record-ID mapping. | These **values** are encoded until profile name/directory admission is complete. Capture never calls profile application. Record IDs use admitted definition identity; equal names do not collapse. |
| `aikit.Host`, executor | Simulation-thread initialized/next-think/deadline presence/ticks, effective execution persona, batch serial, application chain; APM tokens/last fill; pending reservation ring and next index; four guard-grid slots with origin/factory/sealed/cost/seeds/built/seen/stamp/reach/reachStamp; self-grid seen/stamp; dedupe unit/cell stamps/indexes/generation; three free-cache slots, free sequence/slot/last tick and each cache's stamp/value/generation/tick/class/used/asked/component/region/seen fields. | Cache TTLs deliberately expose old placement pictures, so free caches are kept; their tick-equality reuse is not an unconditional overwrite. Grid search queues/distances/heaps, rebuilt blocking/placement walks, reset-valid row scratch, immutable placement geometry and aggregate stats are excluded. |
| Modern exception | Controller presence and pending deadline, mirrored on the simulation thread at existing assignments. | Brain, private generator, kit, observation, unapplied command arrays and map analysis are never inspected. Map analysis includes initial observed live-world information, so it is an explicit worker exception, not immutable content. Flight/ready/probe/worker counters are scheduling or diagnostics. An unknown `Manager.Ext` fails capture. |

Generation-tagged arrays are retained wherever wrap can expose an old tag;
AI dedupe/exit-grid marks and Community hit generations skip zero without
clearing old marks. A reset that seems harmless in a short match is not an
exclusion proof. This specifies existing behaviour and fixes no wrap
behaviour.

#### 16.3.6 Canonical format and public API

**Leaf and files.** `internal/sim/checkpoint/{encoder,format,refs}.go` is a
standard-library-only leaf: owners import it without pointing back to session
or content, and it selects no rules. It does not reuse `netproto.Writer`,
whose buffered varints and online-amount contract differ. Content keys live
in `internal/content/checkpoint_refs.go`. ARCHITECTURE §2–§3 records the
dependencies.

```go
// internal/sim/checkpoint
const SchemaVersion uint16 = 1
const OwnerCount = 13

type Owner uint16
type Digest [32]byte
type Definition struct { Family uint8; Ordinal uint32; Key string }
type Allocation struct { Handle uint32; Serial uint64 }
type ObjectID uint32 // zero is absent; IDs are local to a typed object table

type Encoder struct { /* sticky error, logical field path, streaming sink */ }
func NewEncoder(w io.Writer) *Encoder
// Methods: Field(path) (error context only, emits no bytes); Bool; U8, U16,
// U32, U64; I8, I16, I32, I64; F32, F64; Bytes (u32 length then bytes);
// String (same framing, no normalization); Count(n int) (checked u32,
// negative or overflow fails); Definition; Allocation; Fail(err); Err().

type Identity struct { Content, Config Digest }
type Digests struct { Full Digest; Owners [OwnerCount]Digest }
type Capture struct { /* section order, streaming hashes, optional sink */ }
func NewCapture(identity Identity, out io.Writer) (*Capture, error)
func (c *Capture) Section(owner Owner, present bool) (*Encoder, error)
func (c *Capture) Finish() (Digests, error)

// Typed, capture-local interners; never iterate a pointer-keyed map.
type References[T comparable] struct { /* lookup plus encounter-order list */ }
func (r *References[T]) Add(value T) (ObjectID, error)
func (r *References[T]) Find(value T) (ObjectID, bool)
func (r *References[T]) Values() []T // detached list in assigned-ID order
```

**Encoding.** The all-zero `T` is absent; callers use only the reviewed
pointer types below. `Add` detects ID exhaustion; `Find` never adds;
`Values` is for simulation-thread composition only. No address is encoded.
Errors carry owner and logical field path, and all writes stop at the first
failure, short writes included; a failed capture returns zero digests and
invalidates partial diagnostic bytes. Integers are little-endian, signed
values two's complement; floats are bit copies, NaNs fail, and infinities and
signed zero keep their bits. Booleans are exactly 0 or 1. Definitions encode
Family, Ordinal, Key with M2's numerical family IDs; allocations encode
Handle, Serial. Optional records emit presence then payload. A raw handle is
u32, not an allocation reference.

**Stream and sections.** The header is the ASCII bytes `NLCPSTAT`, schema
u16, content SHA-256, configuration SHA-256, then section count u16.
Sections appear once each in this order, each prefixed by owner u16 and
present u8; payloads are self-delimiting, with no padded record or trailing
length, and an absent section has no payload. Entry/tick kind and tick
number are runtime payload fields.

| ID | Section | Ownership edges |
|---|---|---|
| 1 | runtime | Session configuration, tick/RNG/lifecycle/result/drivers; no scenario internals |
| 2 | units | Arena, current/freed slots, reachable allocation records and physical unit piece flags; no order/VM bodies |
| 3 | orders | Unit-to-queue roots, queue/node tables and auxiliary order state |
| 4 | scripts | Unit-to-VM/bridge roots, VM tables, continuations and unbound VM fallback piece flags |
| 5 | world | Mutable plots, feature table, feature instances/active order |
| 6 | visibility | Visibility service, session stamps and temporary sight |
| 7 | movement | Occupancy/layers/movers/pilots/goals/payloads; nested air-base registry |
| 8 | paths | Provider/scheduler, goal/search tables and suspended accessor descriptors |
| 9 | economy | Player and unit accounts |
| 10 | construction | Placement links, repair/kick state |
| 11 | combat | Projectiles, targeting, damage/death and prediction state |
| 12 | effects | Event admission, effect service/pools, debris and session strips |
| 13 | computers-scenario | Computer managers/executors/application history, mission/schema/Survival |

**Digests.** The full digest is SHA-256 of the whole stream. Each owner
digest is SHA-256 of ASCII `NLCPSECT`, schema u16, content and configuration
digests, then that section's exact owner/presence/payload bytes, so absent
owners still have a defined digest. `Capture.Section` tees one encoding to
the full hash, the owner hash and the optional sink. `Finish` rejects
missing, repeated or out-of-order sections and returns nothing usable after
an error. `Full[:16]` is the wire digest; histories keep all 32 bytes.

**Content keys.** Content owns the typed resolver; every entry point rejects
a value without an admitted identity:

```go
// internal/content; constructed from the already-frozen battle inputs
func (in *SimulationInputs) CheckpointKeys() (*CheckpointKeys, error)
func (k *CheckpointKeys) Unit(v *UnitDef) (checkpoint.Definition, error)
func (k *CheckpointKeys) Weapon(v *WeaponDef) (checkpoint.Definition, error)
func (k *CheckpointKeys) Feature(v *FeatureDef) (checkpoint.Definition, error)
func (k *CheckpointKeys) Model(v *model.Model) (checkpoint.Definition, error)
func (k *CheckpointKeys) SightShapes(v *SightShapes) (checkpoint.Definition, error)
func (k *CheckpointKeys) LOSTables(v *LOSTables) (checkpoint.Definition, error)
func (k *CheckpointKeys) FeatureSequence(filename, sequence string, delays []int32) (checkpoint.Definition, error)
func (k *CheckpointKeys) FeatureSequenceAbsent(filename, sequence string) error
func (k *CheckpointKeys) ProgramForUnit(unit *UnitDef, v *cob.Program) (checkpoint.Definition, error)
type CheckpointFeature struct {
    Variant uint8 // 1 admitted definition, 2 normalized copy
    Base checkpoint.Definition
    FootprintX, FootprintZ, Damage, Metal, Energy int32 // variant 2 only
}
func (k *CheckpointKeys) NormalizedFeature(base, value *FeatureDef) (CheckpointFeature, error)
```

Nil is the caller's presence flag, never an invented record. Keys use the M2
manifest family, record ordinal and key. `Feature` resolves only admitted
base records; a feature edge writes tag 1 plus that key, or tag 2 plus the
`CheckpointFeature` payload (Base, then the five values in API order), which
records a `NormalizeDef` copy. The normalized resolver verifies exactly the
five-field transform and the unchanged remaining semantic fields rather than
trusting the caller. `ProgramForUnit` resolves the unit's admitted COB
manifest record and verifies the program against its semantic digest, so a
fallback recompiled from the sealed input with a new pointer still resolves.
Validation never rereads a live provider. Other dynamic definitions fail
until a reviewed value variant exists; there is no name-only match, current
filesystem read or pointer-to-string fallback.

**Owner APIs and graph discovery.** Each stateful owner exposes
`WriteCheckpoint(e *checkpoint.Encoder, c *CheckpointContext) error` on its
existing state type. `CheckpointContext` is owner-local, holds only admitted
keys and the typed reference tables it needs, and lower packages never import
an upper context. Scalar leaf owners (`pool`, `rng`, `clock`, wind, event
buffer, trigger, animation player) use `WriteCheckpoint(e
*checkpoint.Encoder) error`. The shared contexts and the typed tables they
expose for simulation-thread composition (none is a host API):

| Context and constructor argument | Tables |
|---|---|
| `units.NewCheckpointContext(keys *content.CheckpointKeys)` | `Keys`, `Allocations References[*Unit]`, `VMs References[*cob.VM]` |
| `orders.NewCheckpointContext(u *units.CheckpointContext)` | `Units`, `Queues References[*Queue]`, `Nodes References[*Node]` |
| `path.NewCheckpointContext()` | `Goals References[Goal]`, `Searches References[Search]` |
| `movement.NewCheckpointContext(o *orders.CheckpointContext, p *path.CheckpointContext)` | `Orders`, `Paths`, `Layers References[*ClassLayer]`, `Payloads References[GoalPayload]`; private tables for claim/arrival/composite pilots and claim-row holders |

Interface tables admit only the closed pointer variants below; unknown or
typed-nil concrete values are rejected before the generic interner. Higher
contexts borrow the lower contexts and keys and add no second interner for
the same kind. COB's value context carries resolved program identity, and its
VM keeps value descriptors beside the installed continuations; it does not
import content. Session owns context composition.

Every state owner also implements `CollectCheckpointReferences(c
*CheckpointContext) (added int, err error)` on its existing `World`,
`Service`, `System`, `Pump`, `Scheduler`, `Manager` or `Session`. A collector
adds its own roots, scans already-discovered objects in its tables and calls
the exposed `Add` tables for lower-owner edges; movement's internal roots and
private pilot/row-holder tables are discovered by `System` itself. Session
first adds arena allocations in physical order, then calls collectors in
section-ID order, repeating until a pass adds zero. Within an object,
outgoing edges follow field order and retained sequence order. IDs are per
typed table and start at 1; cycles terminate through lookup. `added` counts
only new objects. Overflow or unsupported state fails collection. The world
cannot mutate during collection or writing. Writers use `Find` and fail on an
undiscovered reference; only collectors call `Add`. Table storage is
discarded after capture.

**Tables and references.** Object tables have fixed u16 IDs: allocations 1,
queues 2, nodes 3, VMs 4, class layers 5, pilots 6, claim-row holders 7,
payloads 8, goals 9, searches 10, ground move-goal handles 11. A section
writes its non-table record, then its ordered root-link sequences, then its
tables in ascending table ID. A table is ID u16, record count u32, then
records in assigned-ID order (the record ID is the implicit 1-based
ordinal). A reference is target table ID u16 plus ObjectID u32; an absent
edge is the table ID plus zero. Root links follow physical-unit order using
allocation object IDs, retired allocations after arena roots; other roots
use the owner's retained sequence or key order. Present sections emit their
tables, empty ones included: units 1; orders 2–3; scripts 4; movement 5–8
and 11; paths 9–10. An absent section emits no table bytes. Unknown table
tags fail capture.

**Union tags** are u8 and precede the variant payload. Unit slots: never
allocated 0, live 1, freed residual 2. Goals: point 1, annulus 2, rectangle 3.
Searches: retail session 1, straightener 2, smoother 3. Pilots: no-pilot 1,
claims 2, arrival 3, ordered composite 4 (nil is reference zero). Payloads:
air marker 1, air velocity 2. COB gameplay continuation: none 0, slot-aim 1;
slot-aim carries thread-slot u8, thread allocation identity u64, captured raw
unit key u32, target allocation, weapon slot u8 and the existing aim-mode
value. Descriptor metadata is installed and cleared atomically with
`onReturn`, including thread claim/kill/return and non-nil program
replacement; nil-program replacement keeps its receiver semantics and makes
capture unsupported. Trace-only wrappers carry no gameplay descriptor. Known
binding identities are absent 0 or the canonical composed owner 1; any
callback with different behaviour needs a reviewed variant.

**Path accessor boundary.** Movement keeps construction-time value metadata
beside each suspended closure and supplies this path-owned value:

```go
// internal/path
// Nodes are topologically ordered; input index zero means absent.
type CheckpointAccessor struct {
    Kind uint8
    Inputs []uint32
    Layer, Learned, ClaimCounts, ClaimOwn checkpoint.ObjectID
    Requester uint32
    Owner uint8
    Profile [8]int32
    Tick, Serial uint32
    Width, Height, FootprintX, FootprintZ int32
    Start Cell
    Bounds Rect
    Through uint8
    Wall uint8
    Row uint8
    Per, Against int32
}
type CheckpointAccessors struct {
    Nodes []CheckpointAccessor
    Passable, Leg, Cost, Revise uint32
}
func (c *CheckpointContext) SetAccessors(search Search, values CheckpointAccessors) error
```

Each node writes every field in lexical field order, with irrelevant fields
zero and validated. Inputs are 1-based indexes into preceding nodes. Profile
order is footprint X/Z, max/min water depth, max/bad slope, max/bad water
slope.

| Kind | Inputs and meaningful operands |
|---|---|
| 1 class view | No inputs; Layer, Requester, Owner, Profile, Tick, footprint |
| 2 learned overlay | One base-view input; Learned (owner+1, zero absent), footprint |
| 3 through movers | One base-view input; Owner, footprint, Through |
| 4 static view | One base-view input; Layer, footprint |
| 5 hostile/keeps-ground | One base-view input; Owner; Wall 1 hostile-only, 2 keeps-ground |
| 6 wedge override | One base-view input and one override-view input; Start, Bounds, footprint |
| 7 claims cost | No inputs; ClaimCounts, ClaimOwn, Owner, Serial, Width/Height, footprint, Per/Against; Row 2 all, 3 slow |
| 8 class revision | No inputs; Layer, Profile, Requester, Tick |

Descriptor object operands are plain u32 values with schema-fixed tables
(Layer table 5, ClaimCounts/ClaimOwn table 7), so they carry no table tag.
Kind 6 Bounds holds the captured strict overlap boundary operands, computed
with the existing int32 start±clamped-footprint arithmetic, not the search
bounds or a new inclusive rectangle; its footprint is the raw profile
footprint passed to the override view.

ClaimCounts and ClaimOwn refer to separate private comparable row-holder
objects in table 7: holder tag 1 is an own row (`[]uint32`), tag 2 a count
row (`[][8]uint8`). Pilot grids and suspended accessors reference the
**same** holders, attached when rows and closures are created and preserving
the actual backing rows, older rows after replacement included. Holders copy
no counts and intern no uncomparable slices, which keeps count and own rows
independently aliased; the Row selector records whether the count row was
all or slow. Only nodes reachable from the final callback roots are emitted:
a replaced passability or cost callback leaves no superseded descriptor,
while a wedge keeps the preceding view as its base. Revision metadata keeps
its actual registry/class key so capture validates the canonical registry and
selected layer without calling `For` or `Revise`. `SetAccessors` validates
local node indexes and kinds and rejects conflicting repeated registrations;
movement and session validate cross-table IDs before writing. No validation
invokes a closure, and path writers read only these values. Fixture callbacks
without a reviewed descriptor are unsupported, never an all-zero view.

Unit-owned `any` orders are checked by the orders collector and written as
unit-to-queue roots there, so units need not import orders; likewise the
scripts section writes unit-to-VM roots and the VM table once. A reference to
a retired allocation keeps its own identity and reachable record, without
resolving to a live slot. Detached nodes reachable from danger, repair or
movement are retained even when absent from queues. This typed discovery is
diagnostic plumbing, not a gameplay registry or reflection walker; unknown
concrete values fail in the owning type switch.

**Session access.** These run on a composed session's owning thread between
pumps, never concurrently with a tick:

```go
// internal/session
const CheckpointOwnerCount = checkpoint.OwnerCount

type CheckpointBoundary uint8 // 1 entry, 2 interior tick, 3 final pump tick
type CheckpointPosition struct {
    Tick uint32
    Boundary CheckpointBoundary
    Pump uint64
    ConsumedInput uint64
}
type OwnerSummary struct { Words, Sum uint64 }
type CheckpointRingRow struct {
    Position CheckpointPosition
    SimulationState, CRTState uint32
    SimulationDraws, CRTDraws uint64
    UnitCount, ProjectileCount, EffectCount, FragmentCount, DebrisCount, StripCount uint32
    Owners [CheckpointOwnerCount]OwnerSummary
}
type CheckpointRecord struct {
    Position CheckpointPosition
    Digests checkpoint.Digests
}
type CheckpointHistory struct {
    Records []CheckpointRecord // at most 64; detached, oldest first
    Ticks []CheckpointRingRow  // at most 600; detached, oldest first
}
type CheckpointCaptureResult struct {
    Pending bool
    Record CheckpointRecord
    Err error
}
func (s *Session) EnableCheckpoints() error
func (s *Session) DisableCheckpoints()
func (s *Session) RequestCheckpointCapture(out io.Writer) error
func (s *Session) CheckpointCaptureResult() CheckpointCaptureResult
func (s *Session) CheckpointHistory() CheckpointHistory
```

- **Enable** captures and records entry state only at completed battle entry
  and otherwise fails (no guessed partial history). It keeps the
  constructor's admitted identities, checks supported bindings and enables
  the fixed cadence. A disabled session does no traversal, hashing or
  history work. **Disable** clears history and cancels a pending request
  with an explicit error.
- **Requests.** One byte request may be pending; a nil sink, a disabled
  session or a second request fails. A request runs at the next C1 boundary
  (not a zero-tick pump), even off cadence, and replaces the previous capture
  result. The sink is used synchronously, must not re-enter the session and
  receives bytes only; blocking I/O belongs to a host-owned memory spool
  followed by an out-of-tick file write.
- **Results and history.** Failures are reported through the result or
  history status, never a simulation log or world mutation. A cadence
  failure is kept as the latest capture error even without a pending request,
  and failed records are not added. Off-cadence requests neither shift the
  cadence nor use one of the 64 cadence slots. Automatic captures update only
  the cadence history and never replace the entry/request result, even a
  later cadence tick in the pump that delivered requested bytes. Hosts read
  cadence records from history; repeated reads do not drain.
- **Metadata.** Pump and consumed-input position and build/platform
  provenance are metadata outside the canonical stream; boundary kind and
  world tick are inside it.

Whole-session capture admission uses `NewAdmittedSkirmish` (one human with
Classic or Modern computers, or Survival), which retains its frozen inputs
and resolved configuration; no zero identity is synthesized for other
constructors. Campaign and retail-load constructors lack that binding, so
enabling capture on them returns an explicit unsupported-admission error
until a constructor supplies equivalent immutable identity. This does not
authorize multiplayer campaign or save support.

#### 16.3.7 AI application records and the tick ring

**Application history.** Both controller kinds keep one per-player chain and
application count on the simulation thread, behind this value boundary in
`internal/ai`, which the Modern host implements through the existing manager
extension; session never imports a controller implementation.

```go
// internal/ai
type ControllerCheckpoint struct {
    Present, Initialized bool
    NextThinkPresent bool
    NextThinkTick uint32
    DeadlinePresent bool
    DeadlineTick uint32
    NextBatchSerial, ApplicationCount uint64
    ApplicationHash checkpoint.Digest
    Tokens int64
    LastFill uint32
}
type ControllerCheckpointProvider interface {
    ControllerCheckpoint() ControllerCheckpoint
    WriteControllerCheckpoint(*checkpoint.Encoder, *CheckpointContext) error
    AppendControllerCheckpointSummary(*checkpoint.Summary) error
}
```

The provider reads only simulation-thread fields and the executor values of
§16.3.5. Batch serials are assigned when the simulation thread marks a batch
pending or due, not when a worker finishes; Classic synchronous decisions use
a simulation-thread decision serial. Zero means no batch, and a serial or
count that would wrap fails checkpoint reporting instead; commands still
execute normally when diagnostics fail.

**Chain.** Enabling checkpoints at entry sets the chain to SHA-256 of ASCII
`NLCPAIST`, schema u16, content and configuration hashes, player u8 and
controller kind u8 (Classic 1, Modern 2). Each attempt replaces it with
SHA-256 of ASCII `NLCPAIAP`, schema u16, the previous hash and the canonical
attempt record. Counts and serials are u64. This is Nanolathe diagnostic
framing, not retail.

**Attempt record.** Player/controller, tick, batch or decision serial,
command ordinal, typed intent and operands, ordered observed actors as
allocation references (emission order, not sorted), optional observed
target, optional product definition, APM verdict, ordered operations and
terminal outcome. Command kinds and resolved order IDs keep their existing
numerical values; scalars use §16.3.6. APM tags: unlimited 1, debited 2,
rejected 3 (Classic is unlimited). Each operation has a u16 tag:

| Tag | Operation | Operand payload |
|---|---|---|
| 1 | Queue purge | Actor allocation; actual purge invocation, including an empty queue |
| 2 | Drop leading automatic | Actor allocation; actual invocation |
| 3 | Insert order | Actor allocation; actual inserted node's retained value fields, with target raw-handle semantics |
| 4 | Coalesce stockpile | Actor allocation, resolved row, actual capped count and whether an existing node was changed |
| 5 | Typed build attempt | Actor allocation, product key, resolved site/facing/count, stable admission verdict |
| 6 | Activation | Actor allocation, requested Boolean, including an already-equal value |
| 7 | Placement reservation | Actual pending-ring slot and complete new reservation value |
| 8 | Guard-grid mutation | Cache slot, affected cell/index and new stored value, in mutation order |

Typed-build verdicts: success 1, rejected product 2, rejected site 3,
rejected owner/actor 4, rejected limit 5, unavailable binding 6, other
failure 7; they classify existing return paths without changing admission or
exposing error strings. Terminal tags: accepted no-op 1, success 2,
stale/rejected with no committed operation 3, partial application 4. A
rejected APM attempt has no world operation but stays in the chain; the APM
debit precedes later stale validation. A new executor mutation kind needs a
schema update, never a reused tag. Capture-local graph IDs never enter the
chain: inserted node values use command operands, allocation identities and
content keys.

**Commit sites.** Modern: `executor.apply/exec` for attempt, APM and
per-actor disposition; `execBuild` for reservation and grid mutations
**before** a possible failed typed build; `execProduce` for the normalized
count; `execReplace` for purge/reclaim preceding a failed replacement;
`execStockpile` for actual coalescing; `execUnblock` for accepted no-ops and
chosen blockers; `execClear/reclaimFeature/reclaimUnit` for ordered partial
work. Classic: `constructionPlacePass`, `doResourceGroup`,
`queueExactResult`, `issueMobileBuild`, `submitResolvedOrder`, retaining
purge and insertion even when resolution gives row zero, and actual
activation calls. There is no new command dispatcher, and ordinary
strategic/group upkeep (already encoded in full) is not in the chain.
Unknown error text is never a protocol value; an unclassified state fails
checkpoint reporting.

**Cheap ring algorithm.** Each owner starts `Words=0, Sum=0`. For each
selected scalar word in the order below, increment Words and add
`Words * word` to Sum modulo 2^64. Signed scalars are sign-extended to 64
bits, unsigned values and booleans zero-extended, binary32/binary64 their
exact bits. An optional record contributes its presence first, a variable
list its length first; arrays, slots and lists keep their existing order.
No strings, pointer values, map iteration, canonical encoder or SHA work is
used. A selected NaN reports the C2 capture failure. Metadata and the six
pool counts have their own row fields.

| Owner | Ring words, in this order (coordinates expand X, Y, Z) |
|---|---|
| runtime | Tick; lifecycle state; RNG initialization and both stream state/draw pairs; end latch fields; pending/ended/draw result bits; commander-death slots; wind strength/heading/scalar/next-change; meteor active/next-strike/end/next-hit; shake active/remaining |
| units | Physical slot tag, raw handle, allocation serial, owner, health/remaining/flags, XYZ, heading/speed, pending/stun/paralysis; each weapon slot reload/ammo/readiness; live/created counters in player order |
| orders | Per physical live unit: queue presence, primary then secondary lengths and node ID/phase/target/goal/deadline/parameters/flags in traversal order; last pump tick |
| scripts | Per physical live unit: VM presence; each thread status/PC/SP/sleep/waits/signal mask and all 32 stack words; statics; active count, next identity and thread identities |
| world | Feature count, reproduction cursor, arena held; row-major plots' occupancy words/metal/feature index/anchor-damage/gameplay flags |
| visibility | Mode semantic bits/local/team/viewer-defeated; word mask then player byte grids row-major; temporary-sight list length and owner/coordinates/expiry/published |
| movement | Per-handle route count/active/dirty/status/request tick and all point coordinates; steer X/Z/heading/speed/dirty; collision stamp/plane/blocker/blocked; flight mode/XYZ/velocity; work tick/budget |
| paths | Scheduler base/set/scales/call count/player cursor/allowance, service counts/accumulators; active request presence/player/unit/start/activation; per-handle working-search presence, popped/setup/expanded counts, node count and heap count |
| economy | Ten players in slot order: Stock, Capacity, Mirror (each Production, Requested, Accepted, Carry), AIProduction, AIConsumption, UpdateTime, GameEnded, EndGameCountdown, Allies; resource pairs Metal then Energy. Then unit-bucket count and each physical row's Buckets, Metal then Energy, each Production, Requested, Accepted, Carry |
| construction | Per-handle repair-bank target/remainders and kick XYZ/valid; placement count and builder-link count |
| combat | Pool count/capacity; all projectile records' dead/weapon/XYZ/target/shooter/velocity/expiry/burst remaining; target rebuild gates/cursors, Modern next-projectile tick, area generation counter |
| effects | Event next ID/sequence/exhausted; service next ID/last sequence; ordered effect XYZ/velocity/expiry and both animation indices/countdowns; fragment/debris/strip counts; each strip's family/spawn/window and each particle XYZ/expiry/frame/delay/phase |
| computers-scenario | Slot manager presence/controller/deadlines/countdown; group lengths; application count and four little-endian u64 words of its chain; next-think/deadline presence/ticks, APM tokens/refill; trigger completed/celebrated; Survival phase/end/wave/spawn cursors/next-retarget/score and accounts |

Owners implement read-only `AppendCheckpointSummary(s *checkpoint.Summary)
error` on existing state; session walks live unit queues and VMs directly for
their owners. Summaries never collect the full reference graph. Nested owners
append to the section's accumulator, so word numbering continues across
fragments. The leaf `Summary` has `Word(uint64)` and `Result() (words
uint64, sum uint64)`, and its zero value is ready to use.

**Blind spots** are deliberate and tested: unlisted fields, equal-length
string changes, detached order/VM objects not rooted in live slots, deep path
frontier changes without count changes, the geometric/effect residuals left
out above and weighted-sum collisions can escape a row. A matching ring
therefore proves neither equality nor the location of every fault; full
owner digests cover the retained schema, and diagnosis reports unresolved
intervals and owners rather than inventing a first divergence. History is
fixed-size: at most 600 rows, 64 records and one capture result, holding no
geometry, nodes, strings or worker state. Reference discovery and full hashes
run only on full-capture ticks or explicit byte requests.

#### 16.3.8 U0 decisions and remaining implementation gates

Rules every owner writer follows:

- **Quiescence.** Entry capture follows opening publication; runtime capture
  follows tick publication and C1's tail, with event staging empty. A
  boundary where `publishFrame` returned before reset fails capture, as does
  a tail or observer that adds events. There is no pending next-tick queue.
- **Coverage.** Each writer accounts for every field against §16.3.5; a
  missing disposition fails review. Unknown callbacks, searches, pilots,
  rules or extensions are explicit unsupported states, never empty records.
- **Strip exclusions.** Table nano-colour cursors, the object
  owner-colour/known/infected/cursor/colour selector, particle
  colour/sample/sequence and the write-only reserved word are
  presentation-only; animation and deferred frame-draw state are kept.
- **Host-dependent audio admission.** `emitPositional` admits audio events
  only with an audio service and an audible viewer, and they share effect
  admission limits and IDs. The counters are kept and the mismatch is
  exposed; the host-equivalence gate is not claimed until it is resolved.
- **Open gaps** stay with their owners: feature slot reuse (EC-G2), SFX-band
  save persistence, desired-aim initialization, visited-bit interpretation,
  the `readyToSpawn` tick-wrap sentinel, the replay-only actor relaxation
  (M4) and O10. Profile name/directory attestation is a pre-online identity
  gap; a required attestation that is absent fails enable.

#### 16.3.9 U1 implementation and verification

`internal/sim/checkpoint` is a standard-library-only leaf streaming the
schema into SHA-256 and an optional diagnostic sink; the I2 guard allows only
`Encoder.F64`'s bit-copy parameter. `SimulationInputs.CheckpointKeys` uses
frozen manifest records and admitted objects, keeps equal-name unit ordinals
and rejects foreign objects. Fallback COB instances are checked against the
owning unit's admitted semantic digest. Model admission keeps the loader's
heights, so key validation reads no files and leaves the content digest
unchanged. Feature references validate the malformed-definition gate and its
five normalized fields.

#### 16.3.10 U2 implementation and verification

The unit, allocator, order, COB and model owners write their records.
Binding fields are explicit u8 tags: absent 0, and 1 only for a binding the
session composition attested (§16.3.28 and the U6 sections). An unattested
nonnil binding refuses capture; no function address identifies a binding.

**Section 2.** The World record, lexically: `OnCapture`, `OnCreate`,
`OnDeath`, `OnDeathExtra`, `attachmentObserver`, `cobBinder`, `cobFS`,
`cobLoader`, `createdCounters`, `extraction`, `lastAllocationSerial`,
`liveCounters`, `pool`, `pose`, `simulationRNG`. Pool fields are `alive`,
`defID`, `limit` and player-ordered `slices` (`end`, `start`). The
physical-slot sequence includes the null sentinel, with §16.3.6's three tags:
a live slot carries a table-1 reference; a freed residual carries `Handle`
u32, `Kills` i32, `Owner` u8, `Remaining` f32. Table 1 follows, including
retained old allocations. Creation must be idle, serials distinct successful
allocations of this battle, and catalog maps must match admitted records.
Unit `RenderPieceFlags` follows `Remaining` as a byte sequence; `statusCue`
and `yardTransaction` are binding tags after the death latches. Raw handles
are u32; `numeric.Fixed` is full i64.

**Section 3.** `units.World.CollectCheckpointReferences` registers physical
allocations, then allocation/VM edges; `orders.Pump` discovers queues, nodes
and retired allocations from danger and firing-position edges, to a fixed
point. The section writes allocation-to-queue roots, queue table 2, node
table 3. No collector creates a queue, invokes a handler or advances a
script. Detached pump execution and unconsumed input-only order fields fail.

**Section 4.** `units.World.WriteScriptCheckpoint`: per allocation root, the
table-1 reference, table-4 VM reference, `ScriptState` presence, optional
`Binding` (model key plus tags `PresentationSink`, `SFXSink`, `SFXVisible`,
`SimulationRNG`), optional `Bridge` (`createInvoked`); then VM table 4 with
the program identity from `cob.CheckpointContext`. Each VM belongs to exactly
one allocation and has a program. VM fields are lexical beside
`VM.WriteCheckpoint`: all eight thread slots, every stack cell, animation
lanes, return/thread identities and raw aim-ready words. The VM binding
record has twelve tags: `cargoContains`, `carrierIdentity`, `explosionSink`,
`portBindings`, `portFuncs`, `renderFlags`, `scriptTouched`, `sfxSink`,
`sfxVisible`, `simRng`, `transportAttach`, `transportDrop`. `pieceFlags`
writes source tag 1 and its bytes; tag 0 is the attested external unit store.
Piece caches, drain counters, trace queues and provenance are excluded.

`CallbackBridge.AimWithCheckpoint` records the combat receiver's allocation,
weapon slot and raw cleanup key on the same deferred aim path, armed and
cleared with the receiver at claim, kill, return and program replacement
(on return, before the callback). An unknown receiver fails capture; a
trace-only wrapper equals no receiver. `VM.CheckpointContinuations` returns
these values, and each slot-aim target must be its VM's own unit.

#### 16.3.11 U3 terrain, feature and visibility owner group

```go
// internal/world
func NewCheckpointContext(keys *content.CheckpointKeys) *CheckpointContext
// CheckpointContext exposes Keys *content.CheckpointKeys and Terrain *Terrain.
func (t *Terrain) CollectCheckpointReferences(c *CheckpointContext) (int, error)
func (t *Terrain) WriteCheckpoint(e *checkpoint.Encoder, c *CheckpointContext) error
func (t *Terrain) RecordCheckpointFeatureNormalization(base, value *content.FeatureDef)
func (t *Terrain) CheckpointFeature(keys *content.CheckpointKeys, value, base *content.FeatureDef) (content.CheckpointFeature, error)
// internal/features
func NewCheckpointContext(w *world.CheckpointContext) *CheckpointContext
// CheckpointContext exposes World *world.CheckpointContext.
func (s *Service) CollectCheckpointReferences(c *CheckpointContext) (int, error)
func (s *Service) WriteCheckpoint(e *checkpoint.Encoder, c *CheckpointContext) error
// internal/visibility
func NewCheckpointContext(keys *content.CheckpointKeys) *CheckpointContext
// CheckpointContext exposes Keys *content.CheckpointKeys.
func (s *Service) CollectCheckpointReferences(c *CheckpointContext) (int, error)
func (s *Service) WriteCheckpoint(e *checkpoint.Encoder, c *CheckpointContext) error
```

No object tables. The session frames section 5 as terrain then feature
service, section 6 as visibility service then its visibility stamps and
temporary-sight records, each presence/payload. Writers emit only their own
payload.

**Terrain:** `ClassRestamp`, `FeatureDefs`, `FeatureNames`, `Movers`, `Plot`,
`metalSeeded`. Plot rows: `AnchorWord` u16, `Feature` u16, `Flags` u8 (minus
the unexplored bit and placer nibble), `Metal` u8, `OccupantA` i16,
`OccupantB` i16. Heights and geometry are immutable admitted inputs. Capture
needs the entry void sweep complete and no mission undo/stamp/replay
transaction, and never runs fixup or recomputes LOS data. Runtime admission
appends only definitions; a definition edge is presence plus the §16.3.6
feature variant.

**Feature provenance.** Normalization keeps the original base on the
instance and records the same relation in the terrain's definition table.
`CheckpointFeature` resolves that base and validates against admitted keys;
no name lookup infers provenance, and a stale, foreign or changed copy fails.

**Features:** `BurnFrameGeometry`, `BurnSmoke`, `BurnSound`, `BurnWeapon`,
`Crt`, `GeothermalSteam`, `SequenceFrames`, `Sim`, `Terrain`, `Wind`,
`arenaHeld`, `cursor`, then instances by sorted anchor key and the active
head-to-tail key sequence. Links, map ownership, arena charge and aliases are
validated, never repaired. Caches marked for rebuild are excluded unrefreshed.
Active-walk and pending-burn handoffs fail. A live cursor (delay, sequence
presence/key, frame) must match the frozen sequence its definition names;
lookup never calls `SequenceFrames`. `FeatureSequenceAbsent` validates cached
misses against admitted absence metadata.

**Visibility:** Community values/binding tags, dimensions, rule binding,
byte grids, sorted observer footprints, local player, semantic mode bits
(all but fog-valid), ray-table and shape presence/keys, teams, terrain
presence, viewer-defeated, word mask. Masks and ray tables resolve as
admitted catalog objects. Readers are never invoked. Derived fog, spokes,
sensor indexes and counters are excluded and not rebuilt. The key-collection
loop and its numeric sort are pinned by the I1 architecture audit.

#### 16.3.12 U3 path-owner framing

The scheduler record writes retained source fields lexically, including the
optional active request's `activePlayer` and `activeScale` (inactive residuals
excluded), then goal table 9 and search table 10. Movement's
candidate-provider payload precedes it. Each search variant writes its
descriptor `Nodes` before its `cfg`; `CostDir`, `LegValue`, `PassableValue`,
`Revise` become descriptor-root u32 indexes. Node-store presence is explicit;
`nodes` includes the reserved zero row, then `scale`. Dense and sparse
workspaces emit the same entries sorted by `(Z, X)`, omitting all-zero
entries. `SetAccessors` propagates through the closed straighten/smooth chain
without calling `Config` or a callback. Unknown or typed-nil goals and
searches are unsupported. `CheckpointKernelKind(Kernel) (uint8, error)`:
Retail or nil 1, Straighten 2, Smooth 3; anything else refuses.

#### 16.3.13 U4 economy-owner API

```go
// internal/community: all fields lexically, nested RepairRate lexically;
// Boolean bytes and signed 64-bit Go ints, with no profile application.
func (f Features) WriteCheckpoint(e *checkpoint.Encoder) error
// internal/economy
func NewCheckpointContext(w *world.CheckpointContext) *CheckpointContext
// CheckpointContext exposes World *world.CheckpointContext.
func (s *Service) CollectCheckpointReferences(c *CheckpointContext) (int, error)
func (s *Service) WriteCheckpoint(e *checkpoint.Encoder, c *CheckpointContext) error
```

No graph objects. Fields follow §16.3.5–§16.3.6: fixed arrays uncounted,
unit buckets counted with unused rows, resource pairs Metal then Energy,
float bits kept and NaNs refused. The selector is presence plus i64. Terrain
is singleton presence with the world context's alias; callback and wind
bindings need attestation (§16.3.48). Names, logos, rank/display timers,
sensor diagnostics and `aiAggregatesPrepared` are excluded. The direct
summary uses §16.3.7's order, reads only its scalars and commits atomically.

`world.Wind.WriteCheckpoint` keeps Changed, DirX, DirZ, Heading, Max, Min,
NextChange, Scalar, Strength; its summary writes strength, heading, raw
scalar bits and next-change. LastChange and BriefingCountdown are excluded.

#### 16.3.14 U3 movement composition and metadata

Movement keeps each working set's construction-time accessor operands (and
claim-row holders); replacing a set drops them, resuming keeps them. The
pilot call borrows a temporary descriptor-root record, so composite pilots
keep the last cost callback's operands. Capture never rebuilds operands,
calls a rule or evaluates a callback.

Private table 11 holds `*moveGoal`, because cleanup compares goal identity
with `recordGoals.ground`; it follows tables 5–8. Section 7 writes Scheduler
and pathProvider presence; section 8 starts with
`System.WritePathProviderCheckpoint(e, c) error`, then the path scheduler and
tables. Provider requests follow sorted player/handle keys. Provider and
scheduler ports need attestation (§16.3.33); the eligibility cache must be
invalid; class-table identity is an admission binding. `unreachableLive` and
`pocketLive` keep i64 values.

Cheap movement: physical Routes, Steers, Collisions, Flights lengths, each
slot's presence then selected values (all twenty route points; the collision
stamp as HasStamp, StampedAnchor.X/Z, LastStampTick, StampedPlane, BlockerID,
Blocked), then workTick and workSmooth. Cheap path: scheduler words, then the
working-set length and rows in handle order with search presence; a search
adds charged popped count (retail popped plus wrapper probes), setupSteps,
expanded, allocated nodes excluding zero, and heap entries.
`path.AppendSearchCheckpointSummary(Search, *checkpoint.Summary) error`
invokes no Search method and fails atomically on unknown or cyclic wrappers.

#### 16.3.15 U4 construction-owner API

```go
// internal/construction
func NewCheckpointContext(o *orders.CheckpointContext, w *world.CheckpointContext) *CheckpointContext
// Exposes Orders *orders.CheckpointContext, World *world.CheckpointContext.
func (s *Service) CollectCheckpointReferences(c *CheckpointContext) (int, error)
func (s *Service) WriteCheckpoint(e *checkpoint.Encoder, c *CheckpointContext) error
func (s *Service) AppendCheckpointSummary(s *checkpoint.Summary) error
```

No object tables. Builder links and placements use sorted numeric product
keys. Placements write definition presence/key, rectangle and the exact
creation-oriented yard sequence, never regenerated. Repair banks keep the full
per-handle array and both fixed slots; kick records keep the whole array,
invalid rows included. `world.FootprintRect.WriteCheckpoint(*checkpoint.Encoder) error`
writes anchor(cellX,cellZ), extent(depth,initialized,width), maxX, maxZ.
OnRefresh, StatusText and RepairBankFallbacks are excluded; other bindings
need attestation (§16.3.67). Active reclaim/VTOL/completion contexts fail.
The summary writes repair-bank rows (target, remainder), kick records (X, Y,
Z, valid), then placement and builder-link counts.

#### 16.3.16 U4 combat-owner API

```go
// internal/pool: implemented scalar leaf, no allocation or compaction.
func (p *Projectiles) WriteCheckpoint(e *checkpoint.Encoder) error
// internal/combat
func NewCheckpointContext(u *units.CheckpointContext, w *world.CheckpointContext) *CheckpointContext
// Exposes Units *units.CheckpointContext, World *world.CheckpointContext.
func (s *Service) CollectCheckpointReferences(c *CheckpointContext) (int, error)
func (s *Service) WriteCheckpoint(e *checkpoint.Encoder, c *CheckpointContext) error
func (s *Service) AppendCheckpointSummary(s *checkpoint.Summary) error
```

No graph tables. The pool leaf writes stored capacity, count, then the full
dead-flag row; a lazy zero-value pool keeps zero capacity and an absent row.
Every arena record through the initialized capacity is kept, residuals
included. Incoming shots keep the physical count span in stored order.
Death-notified keys are sorted raw handles. Target lists keep order, gates
and cursors; movement writes the air-base lists. Area-damage rows keep stored
values. Impact stacks, pending transport handoffs and a nonzero damage
generation refuse. Bindings need attestation (§16.3.70).

The summary writes pool count/capacity, record count, then per projectile
Dead, WeaponID (signed scalar), Pos(X,Y,Z), TargetUnit, TargetProjectile,
TargetPos(X,Y,Z), Shooter, Velocity(X,Y,Z), ExpiryTick, BurstRemaining; then
targets.lastRebuild[10], targets.gate[10], scanCursor.next[10],
modernNextProjectileTick and communityAreaGenCounter.

#### 16.3.17 U4 effects-owner API

```go
// internal/frame
func (b *EventBuffer) WriteCheckpoint(e *checkpoint.Encoder) error
func (b *EventBuffer) AppendCheckpointSummary(s *checkpoint.Summary) error
// internal/effects
func NewCheckpointContext(keys *content.CheckpointKeys, p *FixedEffectPool) *CheckpointContext
// Exposes Keys *content.CheckpointKeys, Pool *FixedEffectPool.
func (s *EffectService) CollectCheckpointReferences(c *CheckpointContext) (int, error)
func (s *EffectService) WriteCheckpoint(e *checkpoint.Encoder, c *CheckpointContext) error
func (p *FixedEffectPool) CollectCheckpointReferences(c *CheckpointContext) (int, error)
func (p *FixedEffectPool) WriteCheckpoint(e *checkpoint.Encoder, c *CheckpointContext) error
func (p *DebrisPool) WriteCheckpoint(e *checkpoint.Encoder) error
func (a *EffectAnimPlayer) WriteCheckpoint(e *checkpoint.Encoder) error
// Each service/pool also exposes AppendCheckpointSummary(*checkpoint.Summary) error.
```

Section 12 composes, with presence each: EventBuffer, EffectService,
FixedEffectPool, DebrisPool, the session strip table. The event window must
be empty. Buffer limits, the exhausted flag and next identities are kept;
drop diagnostics are not. A service's owner is nil or the context Pool. Art
and terrain/impact ports need attestation (§16.3.68); the fragment-stepping
flag must equal TerrainHeight presence. Duration rows and animation cursors
are kept unadvanced; fragment material is excluded.

Debris emits the active block prefix (charge, generation, occupied, slot,
start), the slot array (length, live bits, live payloads), then geometry:
occupied-span count and, per block, index i64, point start i64 (block start
/ 12), point count u32 (`(charge - 110) / 12`) and XYZ i64 triples.
Allocation charges 110 plus 12 per point and absorbs a remainder below 9, so
the span is recoverable after its slot expired or was reused.

The cheap section: EventBuffer nextID, nextSequence, exhausted; service
nextID, lastSequence; fixed record count and per record X, Y, Z, VX, VY, VZ,
ExpiryTick, AnimA.Idx, AnimA.Countdown, AnimB.Idx, AnimB.Countdown; live
fragment count; live debris slot count; then the session's strip summary.

#### 16.3.19 U5 computer-owner API and staging

```go
// internal/ai
func NewCheckpointContext(u *units.CheckpointContext, w *world.CheckpointContext) *CheckpointContext
// Exposes Units *units.CheckpointContext, World *world.CheckpointContext.
func (m *Manager) CollectCheckpointReferences(c *CheckpointContext) (int, error)
func (m *Manager) WriteCheckpoint(e *checkpoint.Encoder, c *CheckpointContext) error
func (m *Manager) AppendCheckpointSummary(s *checkpoint.Summary) error
```

No graph tables. Factory is an allocation edge; groups and rally targets keep
raw u32 handles. Manager, Strategic and profile fields are lexical. Profile
maps and StartOwners write nil/present before their sorted contents, since
absent and empty differ in play. Profile record IDs sort by admitted
definition identity. setupDrawsReady is kept; the draw ledger is excluded.
Catalog aliases, Survival input, rules, callbacks and external controllers
need admission (§16.3.71, §16.3.75); unknown Manager.Ext values fail. No
writer invokes a profile, rule, producer, planner or worker.

The cheap fragment writes Controller, the ten Deadlines, countdown and the
nine group lengths (resource through rally). The session then appends the
controller record: application count, four little-endian u64 hash words,
next-think presence/tick, deadline presence/tick, tokens, last fill.
Application-chain framing uses Classic 1, Modern 2.

#### 16.3.20 U5 Modern executor writer API

The executor writer never reads the mapInfo, obs, kit, brain, generator or
batch pointers (the Modern worker exception) and never joins or samples
workers.

```go
// internal/aikit/checkpoint_executor.go
func (e *executor) writeCheckpoint(enc *checkpoint.Encoder, c *ai.CheckpointContext) error
func (p *Persona) writeCheckpoint(enc *checkpoint.Encoder) error
// internal/aikit/checkpoint_grids.go
func (d *gridDedupe) writeCheckpoint(enc *checkpoint.Encoder, path string) error
func (g *exitGrid) writeGuardCheckpoint(enc *checkpoint.Encoder, path string) error
func (g *exitGrid) writeSelfCheckpoint(enc *checkpoint.Encoder, path string) error
func (f *freeCache) writeCheckpoint(enc *checkpoint.Encoder, path string) error
func (p *pendingSite) writeCheckpoint(enc *checkpoint.Encoder, path string) error
```

| Record | Retained fields, source-lexical |
|---|---|
| executor | dedupe, freeSeq, freeSlot, frees, grids, lastFill, lastTick, m, nextPending, pending, selfGrid, spotCover, table, tokens |
| gridDedupe | cellIdx, cellStamp, gen, unitIdx, unitStamp |
| guard exitGrid | built, cost, fac, ox, oz, reach, reachStamp, sealed, seeds, seen, stamp |
| self exitGrid | cost length as i64, seen, stamp |
| freeCache | asked, class, comp, compGen, gen, regions, seen, seenStamp, stamp, tick, used, val |
| MoveClass | FootX, FootZ, MaxDepth, MaxSlope, MaxWaterSlope, MinDepth |
| freeRegion | wide, x0, x1, z0, z1 |
| pendingSite | cx, cz, fx, fz, g, tick |
| Persona | APM, Ambition, Attention, Burst, Omniscient, Reaction, Skill, ThinkEvery |

Fixed arrays are uncounted (four grids, three free caches, sixteen pending
reservations); slices have exact counts and stored order. Guard cost has a
presence byte (its nil test drives reuse); self-grid cost writes only its
length, which decides whether the next ensure clears seen. Persona Name and
Async are not encoded. Excluded as rebuilt or reset scratch: who/blk/dd,
distances, predecessor/queue/heap storage, free-cache queues, guardFacs,
nGuard, keep, rowNear, cutBuf, blkBuf, rows, lanes, allyTowers, featDist,
featOrder; Places and stats too. The applied batch.rowNear enters the
attempt framing instead. The m and table edges need attested manager,
terrain and table provenance (§16.3.73, §16.3.75).

#### 16.3.21 U5 order application receipts

AI history observes the existing queue methods during an application; it is
diagnostic, simulation-thread-only and absent by default, and never resolves,
creates or rebinds a queue.

```go
// internal/orders/checkpoint_receipts.go
type CheckpointOrderReceipt struct {
    Kind uint8
    Segment uint8
    Index int64
    Node Node
    PreviousCount, Added uint32
    Preparation uint8
}
type CheckpointOrderObserver interface {
    RecordCheckpointOrder(CheckpointOrderReceipt)
}
func (q *Queue) SetCheckpointObserver(CheckpointOrderObserver) CheckpointOrderObserver
func (n *Node) WriteCheckpointValue(*checkpoint.Encoder) error
```

SetCheckpointObserver returns the previous observer; nested scopes replace
and restore. The callback is synchronous, borrows payloads read-only and
never affects gameplay. An active observer makes a queue checkpoint fail; no
observer identity enters the bytes.

Receipt kinds: purge 1, drop-leading-auto 2, inserted-node 3, tail-coalesced
4, primary-preparation 5. Purge and drop precede their invocation, even on an
empty queue. Preparation subkind 1 precedes the stop-firing-position call,
subkind 2 the rules BeforeCommand call, both before Push's allocation guard.
Insert receipts follow the mutation in Push, PushHead, PushSecondary and
appendTail, with node, segment (primary 1, secondary 2) and zero-based index.
Coalescence emits after the tail changed, with old count, actual add and
final node; a fallback emits ordinary Push receipts and a refusal nothing.

WriteCheckpointValue uses §16.3.6's node-value schema. The chain maps
receipts 1–3 to operations 1–3 (3 also keeps segment and index), adds
operation 9 for preparation (actor, subkind) and 10 for coalescence (actor,
segment, index, old count, add, node), distinct from the stockpile's
capped-count operation 4. The journal is an ordered application receipt, not
a second mutation log.

#### 16.3.22 U5 application-chain envelope API

```go
// internal/ai/checkpoint_history.go
func NewApplicationHistory(checkpoint.Identity, uint8, uint8) (*ApplicationHistory, error)
func (h *ApplicationHistory) NextSerial() uint64
func (h *ApplicationHistory) BeginAttempt(tick uint32, serial uint64, ordinal uint32,
    writeIntent func(*checkpoint.Encoder) error) *ApplicationAttempt
func (a *ApplicationAttempt) Operation(kind uint16, write func(*checkpoint.Encoder) error)
func (a *ApplicationAttempt) Finish(apm, terminal uint8)
func (h *ApplicationHistory) Fail(error)
func (h *ApplicationHistory) Snapshot() (ApplicationHistoryState, error)
func (h *ApplicationHistory) WriteCheckpoint(*checkpoint.Encoder) error
func (h *ApplicationHistory) AppendCheckpointSummary(*checkpoint.Summary) error
```

The history binds the admitted content/configuration digests, player 0–9 and
kind Classic 1 or Modern 2, starting at serial 1, count zero and §16.3.7's
initial digest, and keeps only those between attempts. Nil disables it.

Attempt bytes: player u8, kind u8, tick u32, serial u64, ordinal u32, the
controller's typed intent/actors/target/product, APM u8, operation count u32,
operations (tag u16, operands), terminal u8, with no extra length framing.
Writer callbacks only encode observed values.

A serial is consumed even by an empty batch; serial, count and operation
overflow are refused. Overlapping attempts, unknown tags, a Classic APM tag
other than unlimited, and an APM rejection with operations or another
terminal invalidate reporting; the first failure is sticky and gameplay
continues. Snapshot refuses an active attempt or a failure. Full framing:
presence, count u64, 32 hash bytes, kind u8, next serial u64, player u8. The
ring fragment is §16.3.7's five words.

#### 16.3.23 U5 typed intent and order-receipt codecs

Value-only codecs in `ai` take observed allocations and admitted content
identities, resolved by the adapter through CheckpointKeys; they do no lookup
or normalization.

```go
// internal/ai/checkpoint_intent.go
type ApplicationOperands struct {
    Actors []checkpoint.Allocation
    Target *checkpoint.Allocation
    Product *checkpoint.Definition
}
func (v ClassicApplicationIntent) WriteCheckpoint(*checkpoint.Encoder) error
func (v ModernApplicationIntent) WriteCheckpoint(*checkpoint.Encoder) error
// internal/ai/checkpoint_order_receipts.go
func (a *ApplicationAttempt) RecordOrder(actor checkpoint.Allocation, receipt orders.CheckpointOrderReceipt)
```

| Intent | Bytes, then Operands |
|---|---|
| Classic 1, order | kind u8, Code i64, ResolvedRow u8, Modifier u8, Argument i32, X/Y/Z i64, raw target u32 |
| Classic 2, typed build | kind u8, BuildKind i64, UnitKey string, X/Z i64, Count i64, BuildRequest.Tick u32 |
| Classic 3, activation | kind u8, Active Boolean |
| Modern, CmdKind 1–14 | Kind u8, Queued Boolean, raw target u32, X/Z i32, ProductIndex i32, ProductKey string, Slot/Count/Spot/Spacing i32, Keep/Exact Boolean, RowNear i32 |

Operands: actor count u32 and each allocation in observed order (duplicates
kept), target presence and allocation, product presence and definition.
Allocations need nonzero handle and serial; products need the admitted
family, a nonzero ordinal and a `unit/` key. Modern's ProductIndex and
ProductKey come from the observed UnitInfo (or zero and empty), validated
against the immutable table. Unknown kinds invalidate reporting; row zero is
a valid resolved order.

RecordOrder maps receipts 1/2/3/4/5 to operations 1/2/3/10/9: purge and drop
write the actor; insertion actor, segment u8, index i64, node values;
coalescence adds previous count u32 and add u32 before the node; preparation
actor and subkind u8. The node owner is kept as supplied. Receipt count is
not a committed-effect count.

#### 16.3.24 U5 manager binding and Modern scheduling metadata

```go
// internal/ai/checkpoint_application.go
func (m *Manager) EnableCheckpointApplications(checkpoint.Identity, *content.CheckpointKeys) error
func (m *Manager) CheckpointApplicationHistory() *ApplicationHistory
func (m *Manager) CheckpointApplicationKey(*content.UnitDef) (checkpoint.Definition, error)
func CheckpointAllocation(*units.Unit) (checkpoint.Allocation, error)
// internal/ai/checkpoint_controller.go
func (v ControllerCheckpoint) WriteCheckpoint(*checkpoint.Encoder) error
func (v ControllerCheckpoint) AppendCheckpointSummary(*checkpoint.Summary) error
```

A manager installs its history only before creating its controller; the
helper rejects missing keys, unsupported kind or player, an existing history
or Ext value, and never resets a chain. CheckpointAllocation reads a unit's
handle and serial, retired units included, never a slot lookup.

ControllerCheckpoint is a detached value, not admission; the provider also
implements `AppendControllerCheckpointSummary(*checkpoint.Summary) error`.
Full encoding, lexical: ApplicationCount u64, ApplicationHash bytes,
DeadlinePresent Boolean, DeadlineTick u32, Initialized Boolean, LastFill u32,
NextBatchSerial u64, NextThinkPresent Boolean, NextThinkTick u32, Present
Boolean, Tokens i64. Summary: the count/hash five, next-think presence/tick,
deadline presence/tick, tokens, refill tick. Stored ticks are kept even when
not present.

The Modern Host borrows its manager's history at NewHost. Simulation-thread
mirrors hold deadline presence/tick, the batch serial and an
application-in-progress marker, set at the Step site that marks a batch
pending (empty batches included) before a worker starts; deadline presence
clears after applyBatch. They change no scheduling. Writers refuse missing or
failed history, an application in progress or a foreign history, and never
read worker state. The full Host payload is the controller record, effective
Persona, then the executor record (§16.3.20).

#### 16.3.25 U5 typed-build return classification

`ai.WithCheckpointBuildVerdict(error, uint8) error` tags a return branch
without changing text or unwrap chain; `ai.CheckpointBuildVerdict(error)
(uint8, bool)` reads the tag (nil is success 1) and never parses text.
Verdicts are §16.3.7's 1–7 (CheckpointBuildSuccess, Product, Site, Owner,
Limit, Binding, Other); an unclassified error fails reporting, not the
command. The session's typed-build closure maps missing world, catalog,
queue or descriptor to binding 6; a missing or dead builder to owner 4; an
empty or unknown product, or one without a movement profile, to product 2;
allocator exhaustion to limit 5; a nonpositive count to other 7, using
construction's sentinel errors with errors.Is. The stockpile path's private
result helper returns success 1, binding 6 (missing descriptor or queue),
owner 4 (nil unit), other 7 (nonpositive count) or product 2 (the stockpile
slot predicate refused). A success return does not prove insertion; receipts decide that.

#### 16.3.26 U5 producer observation helpers

```go
// internal/ai/checkpoint_producers.go
func (h *ApplicationHistory) ActiveAttempt() *ApplicationAttempt
func (a *ApplicationAttempt) ObserveQueue(*orders.Queue, *units.Unit) func()
func (a *ApplicationAttempt) CommittedOperations() uint32
func (a *ApplicationAttempt) IssuedOrders() uint32
func (a *ApplicationAttempt) CoalescedOrders() uint32
func (a *ApplicationAttempt) RecordStockpile(checkpoint.Allocation, orders.ID, int64, bool)
func (a *ApplicationAttempt) RecordActivation(checkpoint.Allocation, bool)
func (a *ApplicationAttempt) RecordBuild(checkpoint.Allocation, BuildRequest, error)
```

ObserveQueue scopes an existing queue to the issuing allocation and returns
its restoration; the session calls it only after its lazy queue bind.

| Operation | Operands |
|---|---|
| 4 stockpile | actor, resolved row u8, capped count i64, coalesced Boolean |
| 5 typed build | actor, raw Builder u32, UnitKey string, X/Z i64, facing u8 (always zero), Count i64, Kind i64, Tick u32, verdict u8 |
| 6 activation | actor, requested Boolean, recorded before the setter even if equal |

IssuedOrders counts operations 3 and 10, CoalescedOrders 10 alone, and
CommittedOperations 1/2/3/6/7/8/9/10 (empty purge/drop/preparation
included); 4 and 5 are metadata and prove no commit. Counts are temporary
bookkeeping, never payload.

#### 16.3.27 U5 Classic producer instrumentation

A Classic serial belongs to one actor submission after the caller's
selection and admission gates, with ordinal zero; group broadcasts give each
submitted member a serial in member order. The attempt begins before the
queue mutation and observes the queue after its bind. Orders are intent kind
1, mobile and factory builds 2, activation 3. Products resolve through the
manager's frozen keys; an unknown product keeps its raw key. A typed call
emits operation 5 after it returns; queueExactResult borrows the enclosing
attempt. Outcome: resolved order plus actual insertion or coalescence is
success; otherwise partial if any operation committed, else rejected; a nil
typed return alone is not success. Classic has no accepted no-op and always
uses the unlimited APM tag. Gameplay is identical with diagnostics enabled,
disabled or failed.

#### 16.3.28 U6 binding authority primitive

```go
type BindingAuthority struct { /* private, non-zero-sized */ }
func NewBindingAuthority() *BindingAuthority
func (a *BindingAuthority) Matches(expected *BindingAuthority) bool
```

Matches is true only for the same nonnil token, which emits no bytes. The
admitted composition owns one token; owner contexts borrow it with the exact
expected aliases, and canonical installers stamp only the slot they install.
Each slot keeps its own authority, and an ordinary setter clears it, so a
replacement refuses capture without function equality or addresses. No
getter exposes the token. Publicly writable callback storage cannot claim
the proof; a copied callback carries its provenance with it.

#### 16.3.29 U5 exact insertion completion

A preparation callback can insert a node before the outer Push reaches its
allocation guard, so orders reports completion separately:

```go
type CheckpointInsertionResult struct {
    Method uint8 // Push 1, CoalesceTail 2
    Row ID
    Inserted, Coalesced bool // mutually exclusive; both false means refused
}
type CheckpointInsertionObserver interface {
    RecordCheckpointInsertion(CheckpointInsertionResult)
}
func (a *ApplicationAttempt) InsertionIndex() uint64
func (a *ApplicationAttempt) InsertionAfter(uint64, checkpoint.Allocation, uint8) (orders.CheckpointInsertionResult, bool)
```

Only the public outer Push or CoalesceTail reports, once, after every
ordinary return (refusals included) and after nested callbacks, to its entry
observer. A producer takes InsertionIndex just before its insertion or typed
call, after cleanup, and accepts only a later completion for that actor and
method. Cleanup can change an actor's handle or serial: the journal keeps the
allocation observed before cleanup. The result is never an operation or a
gameplay signal.

#### 16.3.30 Path binding admission preparation

```go
// internal/path
func NewSchedulerWithCheckpointBindings(SearchFunc, PublishFunc, *checkpoint.BindingAuthority) *Scheduler
func (s *Scheduler) SetSearchWithCheckpointBinding(SearchFunc, *checkpoint.BindingAuthority)
func (s *Scheduler) SetPublishWithCheckpointBinding(PublishFunc, *checkpoint.BindingAuthority)
func (s *Scheduler) SetCandidateProviderWithCheckpointBinding(CandidateProvider, *checkpoint.BindingAuthority)
func (c *CheckpointContext) SetSchedulerBindings(*Scheduler, *checkpoint.BindingAuthority) error
func CheckpointProviderMatches[T any, P interface { *T; CandidateProvider }](s *Scheduler, expected P, a *checkpoint.BindingAuthority) bool
```

Search, publish and provider have separate authority slots (§16.3.28).
Provider installation still calls PlayerCount then UnitLimit once; capture
calls neither. Registration rejects nil or conflicting schedulers. Absent
slots write tag 0, verified present slots 1. The provider helper takes a
nonnil concrete pointer and never invokes it, so unknown providers refuse
without a panic.

#### 16.3.31 U5 Modern placement receipts

Operation 7 records a pending-ring write with its cursor advance: slot i64;
row cx/cz/fx/fz i32, group u8, tick u32; resulting nextPending i64, emitted
after both assignments in placedRow. Operation 8 records a write to guard
slot 0–3: slot i64, field u8, action u8, operands.

| ID | Field | Value |
|---|---|---|
| 1 | built | u32 |
| 2 | cost | i32 slice with presence |
| 3 | fac | raw u32 handle |
| 4, 5 | ox, oz | i32 |
| 6 | reach | u32 slice |
| 7 | reachStamp | u32 |
| 8 | sealed | Boolean |
| 9 | seeds | i32 slice |
| 10 | seen | u32 slice |
| 11 | stamp | u32 |

Actions: assign 1 (value); zero-filled replace 2 (u32 length, after cost's
presence for field 2); element assign 3 (index i64, value); seeds truncate 4
(u32 length); seeds append 5 (index i64, i32 value). Each is recorded right
after its assignment in ensure, buildExitGrid, overlay, flood, gridFor and
guardPlaced, equal values included, skipped branches not.
`observeCheckpointLayout(*ai.ApplicationAttempt) func()` scopes the four
guard slots only; guard capture refuses while it is active. selfGrid,
dedupe, free caches, spotCover and batch setup rely on full-checkpoint
coverage instead.

#### 16.3.32 U5 Modern command scope and outcome helpers

```go
// internal/aikit; diagnostic helpers, not a second command dispatcher.
func (e *executor) newCheckpointCommand(*Command, *batch, uint32, uint64, uint32) *checkpointCommand
func (e *executor) checkpointAttempt() *ai.ApplicationAttempt
func (e *executor) checkpointActor(*units.Unit) checkpoint.Allocation
func (e *executor) checkpointAccept()
func (e *executor) checkpointReject()
func (e *executor) checkpointNoop()
func (e *executor) checkpointOrderOutcome(uint64, checkpoint.Allocation, uint8)
func (e *executor) checkpointBuildOutcome(uint64, checkpoint.Allocation, ai.BuildRequest, error)
func (c *checkpointCommand) finish(uint8)
```

The executor's temporary checkpointApplication and checkpointBatchSerial
exist only during application; capture refuses them. Each emitted command
begins before the APM test with its batch ordinal and copies §16.3.23's
operands and observed actor span. The command scope and §16.3.31's layout
observer are restored on every unwind; finish runs only on ordinary return,
so a panic leaves history incomplete and capture refuses.

Outcomes are recorded at the existing branches and never read by gameplay.
OrderOutcome accepts only the exact fresh completion; BuildOutcome records
operation 5 and accepts a nil return with a fresh CoalesceTail completion;
stockpile operation 4 uses its completion's Coalesced flag. Stop's purge is
success even when empty; the already-open Unblock branch is the explicit
no-op. Terminal: APM rejection 3 with no operations; else accepted work
success 2; else a no-op without commits 1; else partial 4 if anything
committed, rejected 3 if not.

#### 16.3.33 Movement path binding installation

```go
func NewSystemWithCheckpointBindings(*world.Terrain, Profile, *OccupancyGrid, *checkpoint.BindingAuthority) *System
func (s *System) ConfigurePathWithCheckpointBinding(int, int32, func(int) bool, *checkpoint.BindingAuthority)
func (c *CheckpointContext) SetPathBindings(*System, *units.World, *checkpoint.BindingAuthority) error
```

The ordinary and admitted constructors share one body; the admitted one
stamps only the search, publisher, provider and default eligibility closures
it installs. Ordinary ConfigurePath clears the eligibility authority once its
guards allow installation; the admitted variant stamps it. SetPathBindings
records the system, world and authority, registers the scheduler and checks
the provider with CheckpointProviderMatches. Capture revalidates without
invoking any callback; eligibility writes 0 or 1. Other movement callbacks
have their own installation contracts.

#### 16.3.34 Frozen catalog preparation admission

SimulationInputRequest records PreparingRuleName and PreparingRuleBase, the
exact resolved strings skirmish preparation used, exposed by PreparingRule.
They are admission metadata and change neither digest nor gameplay. Match
admission requires them to agree with the selected registered set and the
effective Community table; a freeze without them cannot admit a multiplayer
configuration. Catalog.Clone preserves the four compile Limits, which the
manifest encodes and admission compares, zero included. A frozen restriction
set is admitted only as field 12 describes it; an empty field 12 admits only
unrestricted content (§16.6).

#### 16.3.35 Session checkpoint history storage

Boundaries are CheckpointEntry (1), CheckpointInteriorTick (2) and
CheckpointFinalPumpTick (3). The ring holds 64 digest records and 600 tick
rows in fixed arrays, in insertion order through tick wrap, and appends
without allocating. Reads return detached oldest-first copies. Entry's digest
is the first record; only completed runtime ticks enter the tick ring.
Off-cadence byte captures belong to the separate result.

#### 16.3.37 U6 session strip fragment

The session owns the last fragment of section 12, after the event buffer,
effect service, fixed pool and debris (§16.3.17):

```go
func (t *stripTable) writeCheckpoint(e *checkpoint.Encoder) error
func (t *stripTable) appendCheckpointSummary(s *checkpoint.Summary) error
```

The caller writes table presence; a nil table is an error. Neither method
creates objects, calls back, looks up content or draws RNG; the cheap form
allocates nothing.

Full payload: `live i64`, `poolCapacity i64`, `steadyCap i64`, then the ten
strip lists with no outer length, each a u32 length and objects in insertion
order. Object fields, lexical: `dst[3] i64`, `dstExtent[3] i64`, `family u8`,
`frameCountBase i32`, `frameDelayParam i32`, `nextSpawn u32`,
`particleLife i32`, `particles` (u32 length, ordered), `phaseModulus i32`,
`smokeSelector u8`, `spawnInterval i32`, `src[3] i64`, `srcExtent[3] i64`,
`windowEnd u32`. Particle fields: `expiry u32`, `frame i32`, `frameDelay i32`,
`lastFrame i32`, `lastFrameDraw i32`, `lastFrameDrawn bool`, `phase i32`,
`vx/vy/vz i64`, `x/y/z i64`. These are stored values, not recomputed
animation. Colour-only fields, `reservedWord` and recycled particle storage
stay excluded (§16.3.5). The full writer requires live ≥ 0 and equal to the
sum of list lengths, steady capacity ≥ 0, pool capacity > 0 and ≥ live, and
family in 1–6; a list may exceed the steady limit (it governs future
eviction, so never normalize). Count overflow refuses.

Cheap fragment: live count, then per object in strip/list order family,
nextSpawn, windowEnd, particle count and each particle's x/y/z, expiry,
frame, frameDelay, phase. Section composition appends table presence once for
both forms. The cheap walk checks only presence; a refusal leaves the summary
unchanged.

#### 16.3.38 U6 scenario fragment

Section 13 writes the computer-manager fragment, then Mission presence with
its mutable `Defeat` and `Victory` trigger lists, the nonoptional
`communitySchema` record, and Survival presence and payload. Nil trigger rows
keep presence. Lists carry u32 lengths, fixed arrays none; §16.3.5 lexical
order and source widths apply (raw handles u32, Go ints i64).

```go
type checkpointScenarioContext struct {
    keys *content.CheckpointKeys
    mission *mission.Mission // exact object retained by admitted composition
}
func (s *Session) writeScenarioCheckpoint(*checkpoint.Encoder, *checkpointScenarioContext) error
func (s *Session) appendScenarioCheckpointSummary(*checkpoint.Summary) error
// internal/triggers
func (t *Trigger) WriteCheckpoint(*checkpoint.Encoder) error
// internal/survival
func (r *Regions) WriteCheckpoint(*checkpoint.Encoder) error
// internal/ai
func (s *SurvivalInfo) WriteCheckpoint(*checkpoint.Encoder) error
```

The session methods live in `checkpoint_scenario.go`; each leaf's caller
writes presence. Admitted composition builds the context from its admitted
Mission object (frozen OTA/TNT and schema), which a terrain name cannot
replace; any other session or community-schema mission pointer refuses, and
campaign and restore entry stay unsupported (§16.3.6). `IsRestore`, mission
type, OTA/schema, placements/specials/initial features, wind bounds,
use-only, difficulty and campaign selection are immutable entry bindings;
`Mission.order` is load diagnostic state. A nonnil trigger pointer repeated
across the lists refuses, since notification can mutate Args.

| Record | Lexical fields |
|---|---|
| communitySchemaState | active bool; deferredPlacements []i64; mission binding presence; neutralOwner i8; nextDeferred i64; playerByStart [10]i8 |
| Trigger | Args [3]i32; Celebrated bool; CenterReady bool; CenterX/Y/Z i32; Completed bool; Kind u8; Type string |
| survivalState | accts [10]Account; attacker u8; centreX/Z i32; classes ordered records; cleanLost bool; info presence/payload; lastSpawn u32; nextG/P i64; nextRetarget u32; opts; phase u8; phaseEnd u32; plan; pool; removed; settled [10]u32; startClass local u32 reference; startRegion i32; stats [10]SurvivalStats; survived i64; team []u8; tuning; units ordered records; wave i64; wavePoints i64; waveUnits []u32 |
| survivalClass | base i32; key string; profile; regions |
| survivalUnit | h u32; infecting bool; shun u32; shunCellX/Z i32; shunSerial u64; shunUntil u32; target u32; wave i64 |
| movement.Profile | BadSlope, BadWaterSlope u8; FootPrintX/Z i16; MaxSlope u8; MaxWaterDepth i32; MaxWaterSlope u8; MinWaterDepth i32 |
| survival.Regions | H, W i32; label presence then length/values []i32; sizes []i32 |
| survival.Account | Capacity, Earned, Stock F32 |
| SurvivalStats | Damage, Destroyed, Lost i64 |
| survival.Options | NoAir, NoNaval bool |
| survival.Pool | MaxTier, RatioE, RatioM, Tier1Median i64; Units ordered records |
| survival.Unit | Cost i64; Def presence/admitted key; Domain u8; Key string; Tier i64 |
| survival.Wave | Budget i64; Groups ordered records; Number i64 |
| survival.Group | Angle u16; Domain u8; Picks []i64 |
| ai.SurvivalInfo | Attacker u8; CentreX/Z i32; Computer []bool; Starts [][2]i32; Team []u8; warnings pointer presence then ordered records |
| ai.SurvivalWarning | Arrive u32; Groups ordered records; Tick u32; Wave i32 |
| ai.SurvivalApproach | Angle u16; Domain string; X/Z i32 |

Tuning, lexical and without applied defaults: AirFrom u32, BaseUnits i64,
BuddyRing i32, CleanWaveBonus i64, DirectionEvery u32, Doubling u32,
DowntimeBase u32, DowntimeMax u32, DowntimePerUnit u32, EdgeInset i32,
FastClearBonus i64, FirstWaveDelay u32, MaxDirections i64, NewTierWeight i64,
RetargetEvery u32, SpawnPerTick i64, Straggle u32, ThemeWeights [5]i64,
UnlockUnits i64, WarningTime u32, WaveReward F32.

- `accts` is one physical `[10]Account` reused by the Metal then Energy
  income passes; unused residual rows are kept.
- Classes keep allocation order, unique and nonnil. `startClass` is zero when
  absent, else the one-based class index; a foreign pointer refuses.
- Regions keep labels and sizes without rebuilding; nil labels have an
  explicit absence tag.
- `removed` is a u32 count and sorted handle-u32/health-i32 pairs; stale
  handles are never redirected.
- Pool definitions resolve through `keys.Unit(actualDef)`, with Key stored
  separately. Nothing (tiers, prices, plans, groups) is reconstructed.
- SurvivalInfo loads its atomic warnings pointer once and writes the whole
  published list, not the filtering `Warnings` getter; the manager binding
  validator requires the scenario-owned info alias. `info.Team` stays
  independent of `team`. Setup/report-only deposits, history and walk scratch
  stay excluded (§16.3.5).

Cheap fragment, after managers: Mission presence; Defeat length and each
trigger's presence, Completed, Celebrated; Victory likewise; Survival
presence; phase, phaseEnd, wave, nextG, nextP, nextRetarget, survived,
wavePoints; stats[0..9] Damage/Destroyed/Lost; accts[0..9]
Capacity/Earned/Stock as exact F32 bits. No map, region, warning, pool or
reference walk. Selected NaNs are checked before the local summary copy
commits; unselected tuning NaNs fail only the full writer.

#### 16.3.39 U6 runtime and visibility-tail fragments

Callback-free private session leaves, run after whole-session admission,
binding and boundary preflight:

```go
func (s *Session) writeCheckpointRuntime(*checkpoint.Encoder, *content.CheckpointKeys, CheckpointBoundary) error
func (s *Session) appendCheckpointRuntimeSummary(*checkpoint.Summary) error
func (s *Session) writeCheckpointVisibilityTail(*checkpoint.Encoder) error
func (s *Session) appendCheckpointVisibilityTailSummary(*checkpoint.Summary) error
```

No reference table, no rule selection. Runtime is boundary u8 (1/2/3), then:

```text
CampaignSlot i64; Clock.GlobalTick u32; Community; DefeatDone bool;
EnemyOwner u8; EntryCommunity; Gameplay string; Latch; LocalOwner u8;
Meteor; Mutators; Progress; RNGCrtSeed u32; RNGSimSeed u32; Restrictions;
Rules { Base string; Name string }; Skirmish; State u8; VictoryDone bool;
ViewingOwner u8; Wind presence/payload; battleEntryTailDone bool;
builderOptionsReady bool; campaignPlayerSide [10]i8;
campaignPlayerSideKnown [10]bool; deathmatchActive bool;
deathmatchAttempts u16; deathmatchExhausted bool;
deathsWithNoRecordedCause i64; noShake bool; pendingCommanderDeaths [10]bool;
playerBuilderOptions [10]BuilderOptions; result; resultArmedTick u32;
resultPending bool; resultPendingDraw bool; resultPendingLosers []i64;
resultPendingReason string; resultPendingWinner i64;
rngCrt { State u32; draws u64 }; rngInitialized bool;
rngSim { State u32; draws u64 }; seatCommands.removed [10]bool;
shakeActive bool; shakeAmpX i32; shakeAmpY i32; shakeDuration i32;
shakeOffsetX i32; shakeOffsetY i32; shakeRemaining i32.
```

| Record | Fields |
|---|---|
| Latch | Bits u16; Countdown i16; Pending u8 |
| Progress | BetweenMissions i64; Thumbs [25]u8; WL [10]u8 |
| BuilderOptions | Guard [3]u8; Patrol [3]u8 |
| Result | ArmedTick u32; Countdown i16; Draw bool; Ended bool; Losers []i64; Reason string; Tick u32; Winners []i64 |
| Meteor | Active bool; DurationTicks i32; Enabled bool; Initialized bool; IntervalTicks i32; NextHit u32; NextStrike u32; OriginX/Z i32; PerHitDelay i32; Radius i32; StrikeEnds u32; TargetX/Z i32; Weapon presence/admitted definition; WeaponName string |
| Skirmish | CommanderDeath i64; Difficulty i64; Gameplay string; LOSType i64; LineOfSight i64; Location i64; MapName string; Mapping i64; NumPlayers i64; Players [10]SkirmishPlayer; RNGCrtSeed u32; RNGSimSeed u32; Survival options; UnitLimit i64 |
| SkirmishPlayer | AI u8; AllyGroup i64; Controller i64; Energy i64; Metal i64; Side i64 |
| Survival options | Enabled bool; NoAir bool; NoNaval bool; Pace u8 |
| Mutators | AreaOfEffect, BuildCost, BuildSpeed, Damage, FireRate, Health, Income, Radar, Salvage, Sight, UnitSpeed; each Factor has Den u8 then Num u8 |
| Restrictions | u32 entry count, then each Count u8, Unit string in the canonical order returned by Entries |

Community and EntryCommunity use `community.Features.WriteCheckpoint`; Wind
its own writer. RNG state is read directly with pure Draws, never through the
initializing SimRNG/CrtRNG getters. Rules names identify bindings preflight
already verified; matching names is not verification. Raw factors, duplicate
result slots and every stored scalar are kept without normalization or
defaults. Meteor's Weapon resolves through `keys.Weapon`, never by name. Leaf
refusals: missing session, encoder, keys or Clock; unknown boundary; pending
battle transition; active seat-command dispatch; invalid count or key
encoding; foreign meteor definition; NaN wind. A terminal final-tick state
need not still be Battle.

Visibility tail: section 6 writes the visibility service fragment (cheap form
§16.3.41), then postLoop presence and, when present, the eyeball count and
each record's `cx i32`, `cz i32`, `emitter u8`, `expiry u32`, `heightByte u8`,
`owner u8`, `published bool`, `sightDistance i16`, `x/y/z i64`; then the
visStamps count and numerically sorted `handle u32`, `cx/cz/radius i32`.
Stamp keys outside u32 refuse; nil and empty maps both encode zero; eyeballs
keep list order. Tail-allocation and publication helpers are never called.

Cheap words. Runtime: Clock.GlobalTick, State, rngInitialized,
rngSim.State/draws, rngCrt.State/draws, Latch.Bits/Countdown/Pending,
resultPending, result.Ended, result.Draw, pendingCommanderDeaths[0..9], Wind
presence and summary, Meteor.Active/NextStrike/StrikeEnds/NextHit,
shakeActive/remaining. Tail: eyeball count, then each record's owner, x/y/z,
expiry, published (an absent tail adds a zero count). Both commit a local
summary copy on success and walk no map, graph, content or callback.

Community sources are future-behavior input (SetRules, BindRules and command
validation reread them). The admitted constructor has empty sources in Strict
and otherwise one CommandLine override holding only Base equal to the frozen
admitted table; §16.3.45 validates that shape without resolving sources.
RuleSet seams and Features declarations need verified provenance: equal
Name/Base strings do not bless changed interfaces, and unknown concrete
interfaces fail without being invoked or compared.

#### 16.3.40 U6 admitted session receipt

`NewAdmittedSkirmish` keeps a private checkpoint admission receipt, made
after validation and before composition: the frozen inputs, the resolved
configuration, the admitted Mission pointer and one fresh BindingAuthority.
`skirmishEntry` binds it to the exact new Session before any service wiring;
only completed composition marks it ready. Every canonical installation
requires that owner, so a shallow copy cannot reuse it or attest closures
that capture the original. Ordinary constructors carry no receipt and are
unsupported for whole-session capture.

The receipt is provenance, not proof that every binding is verified; it
enables no history and changes no gameplay. U6 preflight still validates
mission inputs, owner aliases, rule and callback installations and the C1
boundary. Capture keys are built only when diagnostics are enabled. The
receipt is outside the stream and has no getter for its authority or
configuration. A Modern host created by the tick-zero prime is attached by
§16.3.43.

#### 16.3.41 U6 visibility cheap fragment

`(*visibility.Service).AppendCheckpointSummary(*checkpoint.Summary) error`
appends section 6's service part before §16.3.39's tail: the semantic mode
bits (all stored bits except ModeFogCacheValid), local player, team[0..9],
viewerDefeated, word-mask length and words, then each of the ten byte-grid
lengths and row-major bytes, one unsigned word per scalar. The caller owns
service presence; a missing service or accumulator errors with no partial
append. It reads no footprint map, dimension, sensor cache, rule or reader,
allocates nothing and does not refresh visibility. Nil and empty grids both
contribute zero length.

#### 16.3.42 U6 closed stateless rule tags

Visibility, movement, orders, construction and combat expose
`CheckpointRulesKind(Rules) (uint8, error)`; AI exposes
`CheckpointPlannerKind(Planner) (uint8, error)`. Each is an exhaustive type
switch over the reviewed stateless implementations, with no method call,
interface comparison or function address.

| Tag | Rules | Planner |
|---|---|---|
| 0 | nil | nil |
| 1 | StrictRules | RetailPlanner |
| 2 | CommunityRules | ModernPlanner |
| 3 | ModernRules | — |

Value forms (where they implement the interface) and nonnil pointer forms are
accepted; typed nil, custom embeddings, laboratory variants and other types
refuse. Each owner writer puts the tag in its rule/planner byte; nil fixture
bytes are unchanged. AI's ConstructionRules uses construction's classifier;
path kernels use `path.CheckpointKernelKind`. A tag proves implementation
identity only, not a binding (orders: §16.3.59, §16.3.62). Session preflight
separately checks the reserved Name/Base pair, Gameplay, empty Features and
every owner's projection (§16.3.45), never rebinding or normalizing; live
phase-1 switches may change the current set. The Modern controller planner
registered by mods is not `ai.ModernPlanner` and is admitted only through
§16.3.44.

#### 16.3.43 U6 completed-entry Modern history attachment

The battle-entry prime may already have built and stepped a Modern host when
`NewAdmittedSkirmish` returns. Recording starts at the completed entry
boundary; the initial checkpoint describes the prime's effects. Attachment
never joins or reads a worker, recreates the host or resets state.

```go
// internal/ai; the concrete caller proves the controller implementation.
func EnableControllerCheckpointApplications[T any](m *Manager, expected *T, identity checkpoint.Identity, keys *content.CheckpointKeys) error
// internal/aikit
func (h *Host) EnableCheckpointApplications(checkpoint.Identity, *content.CheckpointKeys) error
```

The manager helper requires nonnil manager, keys and expected pointer,
ControllerModern, no history, and `m.Ext.(*T)` equal to expected; then it
installs a fresh kind-2 history with the keys. It proves pointer ownership,
not planner or binding admission. The ordinary method keeps its no-Ext
refusal.

Host attachment refuses, before any mutation, unless: host and manager are
nonnil; Ext ownership is exact; neither host nor manager has history; no host
application, executor command scope or nonzero host/executor batch serial is
active; the executor manager is the host's when initialized and nil before;
a pending deadline implies initialization. It then calls the manager helper.
If `checkpointDeadlinePresent`, the scheduled batch is serial 1 and the next
is 2; otherwise the batch serial is zero and the next is 1. Count is zero and
the chain holds the initial hash. Prime attempts are never fabricated.
Attachment reads only simulation-thread ownership, initialization and the
diagnostic scope/deadline fields — never `b`, `flight`, `ready`, the brain,
private RNG, observation or results — and calls no Step, begin, join,
normalize or producer. There is one chain, the manager's.

#### 16.3.44 U6 Modern planner registration witness

The single Modern AI registration slot is extended, with no registry and no
lobby choice of controller. The reviewed mods/aikit init calls
`RegisterModernAIWithCheckpointBinding[T ai.ModernAIStep](step T)`; ordinary
`RegisterModernAI` registers identical gameplay without a witness and is
unsupported for full capture. Both share the duplicate, nil and zero-size
checks.

At registration T must be step's concrete dynamic type and a zero-size
struct; interface instantiations, pointers and stateful types refuse. The
private generated witness is only the exact `planner.(T)` assertion (no
method call, interface comparison or reflection), so a different zero-size
embedding fails. Step and witness resolve together under the registration
mutex once per session, before a Modern player starts, and the witness sits
privately beside `Session.modernAI`. `checkpointModernPlannerMatches(ai.Planner)
bool` requires that witness and matches both the session's retained planner
and the owner's, without lookup, locking, callback or worker read.

#### 16.3.45 U6 reserved rule and feature projection preflight

`validateCheckpointRuleBindings(checkpointRuleContext) error` verifies the
selected reserved set and every owner's projection. The context
(`entryMode gameplay.Mode`, `entryCommunity community.Features`,
`defaultCommunity community.Features`) is built by whole-session admission
from the entry receipt and the mainline table frozen at entry. It requires a
nonnil Session, Rules.Name equal to its reserved Base word and Gameplay, empty
Rules.Features, and this matrix, using the owner classifiers:

| Seam | Strict 3.1 | Community 3.9 | Modern |
|---|---|---|---|
| Visibility, movement, orders, construction, combat | StrictRules | CommunityRules | ModernRules |
| Path | RetailKernel | RetailKernel | SmoothKernel |
| RuleSet Planner | RetailPlanner | RetailPlanner | ModernPlanner |
| UnitLimit | StrictUnitLimit | ModernUnitLimit | ModernUnitLimit |
| ScriptPorts | StrictScriptPorts | CommunityScriptPorts | ModernScriptPorts |
| ComputerIncome | StrictComputerIncome | CommunityComputerIncome | ModernComputerIncome |
| Seats | StrictSeats | ModernSeats | ModernSeats |

Nil (including Path's nil fallback), typed nil, custom embeddings and
laboratory implementations refuse; the four session-owned interfaces use
§16.3.42's type-switch discipline. The leaf never looks up or builds a rule
set, calls its methods, resolves features or rebinds owners.

CommunitySources are checked against the entry mode: Content and Player
empty; Strict entry has no CommandLine source; other entry has exactly one
override holding only Base, pointing at entryCommunity (compare with
`{Base: actual.Base}`). EntryCommunity equals that value (zero in Strict).
Current Community is zero in Strict; outside Strict it is defaultCommunity
after a Strict entry with empty sources, else entryCommunity.

Each present owner's concrete kind and exact `projectCommunity` projection is
verified: visibility's two scalar feature fields; combat's and construction's
projected Features; movement's Rules, Kernel and GridClaimTieBreak-only
Features; economy's AIDifficultyIncome-only Features; Build.OrderBinding's
Rules and orderCommunity Features; each manager's ConstructionRules and
aiCommunity. Classic managers use the current planner; Modern managers need
`checkpointModernPlannerMatches`. Unknown Controller values refuse. Nil owners
are skipped here; whole-session admission owns presence, aliases and callback
proof. Tests build expected projections from these lists, so a new projected
field is a contract change.

#### 16.3.46 U6 unit lifecycle binding ownership

**Installation proof pattern.** This and the binding sections after it share
one pattern. A callback slot is private, with a getter and an ordinary setter.
The ordinary setter clears only its own slot's proof. A
`…WithCheckpointBinding(callback, *checkpoint.BindingAuthority)` sibling, used
only at reviewed session composition sites, performs the same installation
once and then, for a nonnil callback and authority, records private proof
naming the exact owner object, slot and authority. Getters, copied owners and
same-function reinstalls never carry proof. Nothing is invoked or compared.
A `CheckpointContext` registration names the expected owner, related
pointers and authority: missing or conflicting registration refuses
atomically, an identical one is idempotent, and it never stamps the live
owner. Capture refuses a registered foreign owner even with every slot empty,
requires each present slot's own proof, and leaves unregistered all-absent
fixtures unchanged. Each slot's former zero byte becomes a validated presence
boolean at its lexical position; proof is never wire data and never affects
RNG or gameplay.

The four World lifecycle callbacks follow the pattern with unchanged call and
installation positions: getters `DeathHook`, `DeathExtraHook`, `CreateHook`,
`CaptureHook`; setters `SetDeathHook`, `SetDeathExtraHook`, `SetCreateHook`,
`SetCaptureHook`. `(*units.CheckpointContext).SetLifecycleBindings(*World,
*checkpoint.BindingAuthority) error` registers world and authority. The
session attests its canonical create, death and capture installations. The
extra death wrapper keeps gameplay chaining but is attested only when its
captured predecessor is absent; any predecessor, even another canonical
wrapper, is unsupported. Ordinary constructors and headless observation
wrappers get no proof, and the recorder rejects unreviewed wrappers.

#### 16.3.47 U6 copied COB port installation proof

```go
func (v *VM) BindPortWithCheckpointBinding(Port, func([]int32) int32, checkpoint.Allocation, *checkpoint.BindingAuthority)
func (v *VM) BindPortBindingWithCheckpointBinding(Port, PortBinding, checkpoint.Allocation, *checkpoint.BindingAuthority)
func (c *CheckpointContext) SetOwnerBindings(*VM, checkpoint.Allocation, *checkpoint.BindingAuthority) error
```

VM engine ports follow the §16.3.46 pattern per copied map entry, with proof
naming the exact VM, a nonzero owning allocation and the authority (zero
allocation cannot attest). Invocation and fallback order are unchanged, as is
`BindingRequest`. The two maps have independent proof slots because explicit
bindings can fall back to legacy functions per read or write arm; nil-only
entries keep no proof. Capture reads installed entries, never request maps.
Parent admission owns the unit/VM/program graph and builds the context from
that verified allocation.

Encoding at `VM.bindings.portBindings`: 0 when no Read/Write is nonnil, else 1,
a u32 active-row count and rows in numeric Port order, each
`port i64; readPresent bool; writePresent bool`. At `portFuncs`: 0, or 1, a
u32 count and each `port i64`. Nil-only rows are omitted; negative and
full-width keys are not narrowed. Keys are gathered and sorted before values
are read; readPort/writePort are never called.

#### 16.3.48 U6 economy callback and wind ownership

```go
func (s *Service) CloakCostHook() func(*units.Unit) float32
func (s *Service) CloakDueHook() func(*units.Unit) bool
func (s *Service) EndConditionHook() func(int, uint32)
func (s *Service) SetCloakCost(func(*units.Unit) float32)
func (s *Service) SetCloakDue(func(*units.Unit) bool)
func (s *Service) SetEndCondition(func(int, uint32))
func (c *CheckpointContext) SetBindings(*Service, *world.Wind, *checkpoint.BindingAuthority) error
```

Economy's three callbacks follow the §16.3.46 pattern (each setter has a
`WithCheckpointBinding` sibling), with unchanged installation and invocation
order. Registration records the Service, the expected wind pointer (possibly
nil) and authority; capture also checks Wind pointer identity and the existing
Terrain equality. Canonical closures: CloakCost reads only its unit; CloakDue
reads the Session's clock and the unit's flags and deadline; EndCondition
reads the Session's mission, players, ledger, countdown and result. The
session installs the first two at composition and EndCondition lazily, when
absent, at tickPlayers, with the exact-owner authority (§16.3.40). The
CloakCost, CloakDue, EndCondition and Wind slots become presence booleans.

#### 16.3.49 U6 visibility reader and terrain ownership

```go
func (c *CommunityState) AlliedReader() func(PlayerID, PlayerID) bool
func (c *CommunityState) OffMapReader() func(uint16) bool
func (c *CommunityState) SetAllied(func(PlayerID, PlayerID) bool)
func (c *CommunityState) SetOffMap(func(uint16) bool)
func (s *Service) SetAlliedWithCheckpointBinding(func(PlayerID, PlayerID) bool, *checkpoint.BindingAuthority)
func (s *Service) SetOffMapWithCheckpointBinding(func(uint16) bool, *checkpoint.BindingAuthority)
func (c *CheckpointContext) SetBindings(*Service, *world.Terrain, *checkpoint.BindingAuthority) error
```

The CommunityState readers `allied` and `offMap` follow the §16.3.46 pattern,
proof naming the owning Service. A copied projection keeps its callback's
proof, valid only on that same Service. Scalar feature edits leave reader
proof alone (§16.3.45 checks the scalars). Registration keeps the expected
terrain (possibly nil); capture validates service and terrain identity.
Unregistered absent-reader fixtures keep their terrain-presence encoding;
whole-session admission always registers the actual terrain. Ray-table and
sight-shape checks are unchanged. `Community.Allied` and `Community.OffMap`
become presence booleans; a disabled reader still needs proof because a later
rule change can consume it. `projectCommunity` installs both at its
only-if-absent sites, capturing the exact Session, with the exact-owner
authority (§16.3.40).

#### 16.3.50 U6 feature callback, RNG and wind ownership

```go
func (c *CheckpointContext) SetBindings(*Service, *rng.Simulation, *rng.CRT, *world.Wind, *checkpoint.BindingAuthority) error
```

Features' six callbacks — SequenceFrames, BurnWeapon, BurnSound,
GeothermalSteam, BurnFrameGeometry, BurnSmoke — follow the §16.3.46 pattern
through `NameHook()`, `SetName(fn)` and `SetNameWithCheckpointBinding(fn,
authority)`, signatures unchanged. Registration keeps three expected owner
pointers (each possibly nil); capture requires Service, RNG and wind identity.
The Terrain context check, active-traversal and burn-handoff refusal, content
and sequence identities, arena and cache validation remain; no RNG accessor
runs. The six slots and Crt, Sim and Wind become presence booleans.

Canonical callbacks capture the Session, not its owners: BurnSmoke and
GeothermalSteam append strip records; BurnWeapon reads the current catalog,
combat, units, terrain and clock; BurnSound publishes the positional cue;
SequenceFrames reads the immutable simArt; BurnFrameGeometry reads the private
featureSequence resolver, whose installation admission checks against the
frozen art (§16.3.53). ShadowSequenceResolved stays presentation-only.
Installation sites and only-if-nil guards are unchanged. Restore's temporary
sound wrapper uses ordinary setters and loses proof, consistent with
unsupported capture of restored sessions. Cursor delay metadata is validated
against admitted sequence keys; capture fills no cache.

#### 16.3.51 U6 provisional COB port installation receipts

Unit creation installs script ports before COB Create but commits the
allocation serial only after that fallible bind succeeds (in ordinary and
forced allocation, before extraction, the mover tail, activation and the
creation hook). The serial is never predicted and its commit never moves.
Pre-create installation therefore uses a receipt:

```go
func (v *VM) BindPortWithPendingCheckpointBinding(Port, func([]int32) int32, *checkpoint.BindingAuthority) *CheckpointPortInstallation
func (v *VM) BindPortBindingWithPendingCheckpointBinding(Port, PortBinding, *checkpoint.BindingAuthority) *CheckpointPortInstallation
func (r *CheckpointPortInstallation) Seal(checkpoint.Allocation) error
```

Each call runs the ordinary setter once and, for a present callback and
authority, creates pending proof and a private receipt for that VM, map
family, key and installation; otherwise neither. Pending proof never admits a
capture. Seal requires a nonzero handle and serial and the receipt's proof
still installed in the original VM's entry; any replacement, even a canonical
reinstall, invalidates it. The first seal records the allocation, an
identical repeat is idempotent, a different allocation refuses, and refusal
changes nothing. Sealed rows use §16.3.47's encoding and validation.

The units owner keeps receipts beside the exact `*Unit` and seals them right
after the successful serial assignment in each allocator path. Failed creation
never seals; nested creations keep their own lists. A proof refusal never
changes creation success; that installation is simply unsupported at capture.

#### 16.3.52 U6 closed frozen-filesystem identity

```go
func (in *SimulationInputs) CheckpointFilesystemMatches(vfs.FSOps) bool
```

Admission never calls a filesystem's `SimulationInputs()` to prove its
source. This pure closed-type check accepts only the freezer's four private
snapshot views (plain, range, ordered, range-and-ordered) with nonnil wrapper
and base, a non-transient base, the exact snapshot the inputs' sources retain
and that exact inputs pointer; actual and retained views must be the same
concrete pointer after narrowing. Typed nil, other snapshots, copies, foreign
wrappers, transient capture views and user implementations refuse. It runs no
filesystem method and emits nothing. It proves source ownership only; the
open helper is a composition facility, never capture proof.

#### 16.3.53 U6 session art resolver provenance

The effect-frame and feature-sequence resolvers are admitted only when both
are installed from the exact nonnil frozen SimArt. Private helpers call each
public setter once at its composition site and record Session and art beside
the resolver; an ordinary setter clears only its resolver's proof before its
work (including the effect setter's smoke-frame resolution). Getters, copied
fields, equal-valued art and reinstalls transfer nothing. Preflight checks
simArt against the admitted art and both proofs, calling no resolver and
filling no cache; missing art or resolvers and custom replacements refuse. No
wire bytes.

#### 16.3.54 U6 per-unit yard and status callback ownership

```go
func (u *Unit) SetYardOpenTransactionWithCheckpointBinding(YardOpenTransaction, *checkpoint.BindingAuthority)
func (u *Unit) SetStatusCueSinkWithCheckpointBinding(StatusCueSink, *checkpoint.BindingAuthority)
```

Both unit slots follow the §16.3.46 pattern with proof naming the exact
`*Unit`; ordinary setters keep their nil-receiver no-op, and
`SetLifecycleBindings` supplies the expected world and authority. The yard
callback is installed before the serial commits, so proof names the Unit
pointer, and the allocation collector still validates the committed handle
and serial. Retired allocations keep their proof and follow graph discovery.
Only the statusCue and yardTransaction bytes change. The session wires both at
the pre-Create yard installer, the status-sink sweep and the creation hook.

#### 16.3.55 U6 COB runtime binding admission

The VM's remaining runtime bindings are admitted as actual installations,
never by calling a reader or sink. The twelve lexical binding slots stay;
ports keep §16.3.47; the others are validated presence bytes, except
renderFlags, whose present byte is followed by kind 1 (direct external slice)
or 2 (mapped handlers). pieceFlags source is 0, with no duplicate bytes, for a
validated external unit store; unbound local storage keeps source 1 and its
bytes.

```go
// internal/cob; each receipt has private fields.
type CheckpointVMInstallation struct { /* private */ }
func (*CheckpointVMInstallation) Seal(checkpoint.Allocation) error
func (*VM) BindScriptTouchedWithPendingCheckpointBinding(func(), *checkpoint.BindingAuthority) *CheckpointVMInstallation
func (*VM) BindTransportQueriesWithPendingCheckpointBinding(func(int32) bool, func() int32, *checkpoint.BindingAuthority) [2]*CheckpointVMInstallation
func (*VM) BindTransportMutationsWithPendingCheckpointBinding(func(int32, int32, int32), func(int32), *checkpoint.BindingAuthority) [2]*CheckpointVMInstallation
func (*VM) SetSFXVisibleWithPendingCheckpointBinding(func(int, int32) bool, *checkpoint.BindingAuthority) *CheckpointVMInstallation
func (*VM) BindRenderFlagsWithPendingCheckpointBinding([]uint8, *checkpoint.BindingAuthority) *CheckpointVMInstallation
func (*VM) BindRenderFlagHandlersWithPendingCheckpointBinding(func() []uint8, func(int, uint8, bool) bool, []uint8, []int, *checkpoint.BindingAuthority) *CheckpointVMInstallation
func (*Binding) SFXVisibleReader() func(int, int32) bool
func BindStrictWithCheckpointBinding(vfs.FSOps, BindingRequest, *checkpoint.BindingAuthority) (*Binding, *CheckpointVMInstallation, error)
func (*Binding) SetSFXSinkWithPendingCheckpointBinding(SFXSink, func(int, int32) bool, *checkpoint.BindingAuthority) *CheckpointVMInstallation
func (*CheckpointContext) SetRuntimeSources(*Binding, *rng.Simulation, []uint8, []int) error
func SetCheckpointPresentationSink[T any, P interface { *T; PresentationSink }](*CheckpointContext, P) error
func SetCheckpointExplosionSink[T any, P interface { *T; ExplosionSink }](*CheckpointContext, P) error
func (*VM) ExplosionSink() ExplosionSink
func (*VM) ValidateCheckpointBindings(*CheckpointContext) error
```

Receipts follow §16.3.51. Ordinary setters clear only the affected proofs;
paired transport slots are independent. Render proof is cleared by either
render setter, UnbindRenderFlags and both program-reset branches.

Render flags: mapped proof keeps the exact flag and piece-map slices captured
beside the canonical closures in `units.bindRenderFlags`; both handlers must
be present, renderFlagsBound true and direct storage nil. Capture compares
lengths and nonempty backing starts with the unit's flags and
`Binding.PieceMap`, and piece-map values with the admitted program/model
links. Direct storage must alias the unit flag slice. Bound nil or empty
storage stays distinct from local fallback through the tags. Mixed handler
forms and the fallback loader's closures are unsupported. The getter is never
called; its scratch is excluded because it overwrites every element first.

Visibility reader and SFX: Binding's visibility callback is private and
`BindingRequest` is a copied input. The admitted constructor stamps the copied
Binding and VM reader before PreCreate; ordinary `BindStrict` stays unproved.
Ordinary SetSFXSink clears the Binding/VM reader proof; the admitted sibling
records both on one receipt. A later VM-only reader replacement refuses.

Runtime sources and sinks: registration needs §16.3.47's VM registration,
checks Binding.VM and records the expected Binding, RNG and external slices.
VM and Binding RNG must equal the expected pointer. Sinks register only as
nonnil concrete pointers through the closed generic functions and are
rechecked at capture. VM SFX accepts only the value `PresentationSinkAdapter`
around the registered presentation pointer; `Binding.PresentationSink` must
match and `Binding.SFXSink` may be nil or that adapter; ExplosionSink must be
the registered pointer. Typed nil, custom, noncomparable and partial
mismatches refuse without calls. The session first verifies its own sink
(exact Session, publication and clock; source handle equal to the unit;
piece-map values equal to admitted links; explosion sink pointing at the same
presentation sink). These sinks are simulation-relevant — they append
authoritative strip records and perform bounded-arena admission — so their
names do not make them presentation. After ValidateCheckpointBindings
succeeds, the unit owner writes the Binding's model key and presence bytes for
PresentationSink, SFXSink, SFXVisible and SimulationRNG, in that order.

#### 16.3.56 U6 compiled-program future-allocation subset

Production checkpoint admission supports only catalogs whose every admitted
unit record has a compiled, nonempty script. This is an explicit subset, not
a claim about all authored content, and capture proves it every time.

```go
// internal/content
func (k *CheckpointKeys) ValidateCompiledAllocationPrograms(*Catalog) error
```

The check walks every nonnil catalog record in stored order, including
duplicates and never-allocated definitions, and requires exactly the admitted
record set, ordinals and names, each UnitDefID as admitted (not assumed equal
to the ordinal), each admitted COB entry SimulationInputPresent, and each
Script nonnil with nonempty Code and a semantic digest equal to the admitted
program. It loads, normalizes and caches nothing and leaves ProgramForUnit's
fallback alone. CheckpointKeys construction checks every admitted catalog and
COB record against its ordinal and snapshots holes and slice length.

Under this subset both loader branches are unreachable (hasLoadableCOB
returns on the compiled program, which the binder consumes), so loader cache
contents are irrelevant and never touched at capture. Missing, empty,
fallback or mutated programs refuse. Supporting the freezer's discarded
LoadFromFS probe outcomes would need its own frozen-input contract.

```go
func (*CheckpointContext) SetWorldBindings(*World, *content.SimulationInputs, *world.Terrain, *rng.Simulation, *cob.CachedLoader, *checkpoint.BindingAuthority) error
func (*World) SetAttachmentObserverWithCheckpointBinding(AttachmentObserver, *checkpoint.BindingAuthority)
func (*World) SetCOBSourceWithCheckpointBinding(vfs.FSOps, *cob.CachedLoader, *checkpoint.BindingAuthority)
func (*World) SetCOBBinderWithCheckpointBinding(COBBinder, vfs.FSOps, *checkpoint.BindingAuthority)
func (*World) SetExtractionSamplerWithCheckpointBinding(ExtractionSampler, *checkpoint.BindingAuthority)
func (*World) SetCreationPoseWithCheckpointBinding(CreationPose, *checkpoint.BindingAuthority)
func (*World) SetSimulationRNGWithCheckpointBinding(*rng.Simulation, *checkpoint.BindingAuthority)
func (*World) AttachmentObserver() AttachmentObserver
func (*World) CreationPose() CreationPose
```

These World slots follow the §16.3.46 pattern (SetCOBSource clears source and
loader proof). The binder's extra filesystem is its captured source, and the
session builds its model resolver from that source. Registration is consistent
with SetLifecycleBindings, with terrain, RNG and loader as explicit expected
pointers. Capture verifies catalog identity with `inputs.Catalog()`, the
compiled-program condition, loader and RNG, and both the current and the
binder's source through `CheckpointFilesystemMatches`; extraction must be a
nonnil `*world.Terrain` equal to the expected terrain. The session checks
AttachmentObserver and CreationPose by closed `*movement.System` assertion
against its movement owner. Seven binding bytes become presence tags.

#### 16.3.57 U6 terrain movement installation ownership

```go
// internal/world
type FootprintRestampOwner interface {
    NoteFeatureFootprint(int32, int32, int16, int16)
}
func (*Terrain) ClassRestamp() FootprintRestamp
func (*Terrain) SetClassRestamp(FootprintRestamp)
func (*Terrain) Movers() MobileOccupancy
func (*Terrain) SetMovers(MobileOccupancy)
func (*Terrain) SetClassRestampOwnerWithCheckpointBinding(FootprintRestampOwner, *checkpoint.BindingAuthority)
func (*Terrain) CheckpointMovementOwner(*checkpoint.BindingAuthority) (FootprintRestampOwner, bool)
func (*CheckpointContext) SetMovementBindings(*Terrain, *checkpoint.BindingAuthority) error
// internal/movement
func (*CheckpointContext) SetTerrainBindings(*System, *world.CheckpointContext, *OccupancyGrid, *checkpoint.BindingAuthority) error
```

Both terrain movement ports are private. One immutable receipt covers their
exact terrain, authority and restamp owner; either ordinary setter, even with
the same value, invalidates it. The admitted installer takes
`owner.NoteFeatureFootprint` itself, so it cannot pair another function with a
claimed owner, and invokes nothing; nil installs nil without proof. The owner
getter succeeds only for the current receipt's terrain and authority. World
registration records a valid current receipt (idempotent, atomic on
conflict); capture requires it to be still current and on the actual terrain.
ClassRestamp and Movers become presence bytes, with no receipt data.

Movement makes the concrete checks world cannot: the receipt owner is a
nonnil `*System` equal to the current one; Movers is value-form gridOccupancy
over the registered grid; System.Terrain and System.Grid match the
registration; a nonnil grid's plot is that terrain. The world context is
registered only after these pass, a conflict changes neither context, and
capture repeats them. Movement's Terrain byte becomes a presence tag. The
production constructor order is SetMovers(gridOccupancy), AttachPlot(terrain),
then SetClassRestampOwnerWithCheckpointBinding(system, authority); the
ordinary constructor passes nil authority. Other movement bindings have their
own sections (§16.3.61, §16.3.64, §16.3.69).

#### 16.3.58 U6 order callback storage boundary

QueueBinding and its five adapters (MovementGoalAdapter, WorldQueryAdapter,
WorkAdapter, WeaponAdapter, PresentationAdapter) store callbacks privately.
Each callback field Name has `NameHook()` and `SetName(fn)`, neither invoking
it nor transferring proof; signatures are unchanged (including
MovementGoalAdapter.RunAir's AirLegRunner). Nil-receiver access still panics;
no fallback is added. For each of the six types T, `TConfig` holds the public
field set and `NewT(TConfig) *T` copies it into a fresh owner without
invoking callbacks; later edits to the config cannot replace a copied
callback. Noncallback fields (rules, projected Community, services, RNG and
adapter pointers) stay directly visible on T. Callers use the getter, setter
and constructor with unchanged evaluation and invocation order.

#### 16.3.59 U6 order binding proof and payload

```go
// internal/orders
func (*CheckpointContext) SetBindings(*QueueBinding, *economy.Service, *rng.Simulation, *checkpoint.BindingAuthority) error
func (*CheckpointContext) ValidateBinding(*QueueBinding) error
func (*QueueBinding) WriteCheckpoint(*checkpoint.Encoder, *CheckpointContext) error
```

Every callback on §16.3.58's six types follows the §16.3.46 pattern through
`SetNameWithCheckpointBinding(fn, authority)`, and each T has
`NewTWithCheckpointBinding(TConfig, *checkpoint.BindingAuthority) *T`, sharing
one implementation with the ordinary constructor (nil authority). SetBindings
records the binding and its five current adapter pointers, with economy and
RNG as explicit expected pointers (possibly nil); every collection and write
rechecks callback proof and adapter pointers. Economy must be nil when
expected nil, else a nonnil concrete `*economy.Service` equal to it; SimRNG
must equal the expected pointer. Rules classify through `CheckpointRulesKind`;
Community uses its complete writer. QueueBinding.Validate and Ready are never
called. An absent binding is explicit and valid for fixtures.

`QueueBinding.WriteCheckpoint` writes absent 0, or 1 followed by the fields in
public-name lexical order: BuildList, BuilderOptions, Community, CurrentTick,
Damage, DangerCanRespond, DangerRouteFeasible, DangerStepFeasible,
DangerVisible, Economy, Hostility, Lookup, ModernAIPlayer, Movement,
Presentation, ReclaimFeature, Resources, Rules, SimRNG, TransportAdmission,
Weapons, Work, World. Each callback and verified service or RNG is one
presence bool, Rules its u8 kind, Community its body; each adapter is a
presence bool then its callbacks' presence bools in public-name lexical order
(including Ready and RunAir). No pointer IDs or proof bytes. The payload sits
at the queue's binding byte; nil-fixture bytes are identical. A validation or
writer failure is sticky and yields no usable digest.

The session's `newOrderBinding` is the only canonical factory. The session
keeps a private record of the five constructed adapters and the movement
System their bound methods capture, and capture requires those aliases
unchanged, because the Work and Presentation closures capture the factory's
worldQueries object and slot proof alone would miss a replaced
`binding.World`. Lookup and Hostility copied from the proved world adapter
carry proof for the destination slot. Mapping-word copies and owned row
handlers are §16.3.66 and §16.3.62.

#### 16.3.60 U6 unit script installation and capture contexts

```go
// internal/units; same operands as the ordinary strict unit helper, then authority.
func BindCOBForUnitWithCheckpointBinding(vfs.FSOps, *Unit, *model.Model, *rng.Simulation, cob.PresentationSink, func(int, int32) bool, func(*cob.Binding) error, *checkpoint.BindingAuthority) (*cob.Binding, error)
func (*Unit) RetainCheckpointPortInstallation(*cob.CheckpointPortInstallation)
func (*Unit) RetainCheckpointVMInstallation(*cob.CheckpointVMInstallation)
func (*CheckpointContext) ScriptBindings(*Unit) (*cob.CheckpointContext, error)
```

Pending COB receipts (§16.3.51, §16.3.55) live on the exact Unit, never on a
predicted serial. The admitted helper shares the ordinary binding path and
callback order; nil authority means no proof. It records Unit and authority
before PreCreate and retains the admitted VM; transport attestation and script
preflight require that same VM. Port, script-touched and mapped-render
installations use pending receipts at their original sites, and the
Binding/VM visibility receipt is retained only on success. The fallback loader
and ordinary binders acquire no proof.

A nonnil receipt without a prior admitted installation is a sticky diagnostic
failure. Receipts queue on the unit before allocation and seal against the
actual Handle/AllocationSerial right after each successful serial assignment,
so nested creation seals each unit independently. A seal error (first kept)
is a diagnostic refusal only: it never fails creation, changes RNG, calls
gameplay code or suppresses a callback. Copied Units reuse nothing. None of
this metadata is checkpoint bytes.

ScriptBindings, after reference discovery, requires a discovered nonzero
allocation and VM, admitted keys and the lifecycle World/authority, and
registers that VM and allocation on a lower COB context kept by Unit pointer
(reused only while VM, program key, allocation and Binding are unchanged). It
retains the Unit.RenderPieceFlags header and rechecks it at each access. Root
registers Binding/RNG/render slices and the closed session sinks (§16.3.55).
Preflight calls ValidateCheckpointBindings before any script bytes, writes
Binding's four presence booleans, then the VM. Without registration,
all-absent fixtures keep their bytes and a present binding refuses. No script,
render getter or callback runs.

#### 16.3.61 U6 movement auxiliary ownership and lazy occupancy

```go
// internal/movement
func (*System) BindWorldWithCheckpointBinding(*units.World, *checkpoint.BindingAuthority)
func (*System) AttachOverlapBindingWithCheckpointBinding(func(uint8) uint8, *checkpoint.BindingAuthority)
func (*CheckpointContext) SetAuxiliaryBindings(*System, *units.World, *checkpoint.BindingAuthority) error
```

- **World observer and overlap.** The admitted siblings share the ordinary
  bodies. The grid keeps separate proofs for ownerState and claimConflict;
  ordinary AttachOverlap clears the first, ordinary AttachOverlapBinding both.
  Capture checks the grid, the concrete *System and its aliases without
  calling either callback.
- **AirSectors.** The admitted constructor snapshots the AirSectors it just
  built (identities, dimensions, all records with padding and sentinel).
  Capture validates against that snapshot and never rebuilds from current
  terrain; copies, replaced grids and mutated records refuse. The slot is a
  validated presence byte.
- **SetAuxiliaryBindings** records System, World (nil for fixtures) and
  authority, agrees with SetPathBindings in either order and refuses conflicts
  atomically. It proves no callback by itself.
- **Lazy occupancy.** Air and cells planes are each nil or exactly
  planeW*planeH; counts equal nonzero stored occupants. Capture writes the
  actual zero or full length and never creates a plane; a nonnil empty slice
  with nonzero dimensions is malformed.

#### 16.3.62 U6 copied owned-order handlers

```go
// internal/orders; all fields of the two values below are private.
type CheckpointHandlerSource struct { /* exact owner and authority */ }
type CheckpointOwnedHandler struct { /* function, kind, source */ }
const CheckpointConstructionWake uint8 = 1
const CheckpointGetBuilt uint8 = 2
const CheckpointAirStandby uint8 = 3
func NewCheckpointHandlerSource[T any](*T, *checkpoint.BindingAuthority) *CheckpointHandlerSource
func RegisterCheckpointHandlerSource[T any](*CheckpointContext, uint8, *CheckpointHandlerSource, *T, *checkpoint.BindingAuthority) error
func NewCheckpointOwnedHandler(OwnedHandler, uint8, *CheckpointHandlerSource) CheckpointOwnedHandler
func (CheckpointOwnedHandler) Handler() OwnedHandler
func (*Queue) SetOwnedHandlerWithCheckpointBinding(ID, CheckpointOwnedHandler)
```

Owned handlers are immutable function/kind/source values whose provenance
travels with them, BindQueue's inheritance included. Function addresses are
never compared and handlers never called. Registration matches the concrete
owner by closed generic assertion and pointer comparison; each kind has one
source per capture, sharing one authority with binding registration.

Kind 1 admits only BuildingBuild, MobileBuild, VTOL_MobileBuild, ReclaimUnit
and VTOL_ReclaimUnit; kind 2 only GetBuilt; kind 3 only VTOL_Standby. BindQueue
copies proofs only in its inheritance branch. A handler without proof, or
proof without a handler, refuses. Bytes: tag 0 for empty/all-nil storage;
otherwise tag 1, u32 active count, then ascending rows of row ID u8 and kind
u8. Construction and movement create the sources (§16.3.64).

#### 16.3.63 U6 frozen input mutation validation

```go
// internal/content
func (*SimulationInputs) ValidateCheckpointInputs() error
func (*CheckpointKeys) ValidateMovementClasses(map[string]*MovementClass) error
```

Cached hashes do not prove admitted objects unchanged, so
FreezeSimulationInputs retains a private typed snapshot before it returns
(local diagnostic metadata, not a manifest, wire field or M2 identity).
CheckpointKeys validates it before building keys and root revalidates it
before each full capture. The snapshot keeps original pointers, scalar values
(floats by bits), ordered slices with holes and sorted raw-key lookup rows;
validation never ranges current maps, calls getters, reads files or rebuilds
caches. Catalog.Clone is not a snapshot.

It covers: the catalog's unitRecords, Units index, Weapons/Features/Movement
membership (nil entries included), ordered Sides, weaponRecords/weaponByID
and CategoryRegistry.byName; every consumed definition value, including
category masks, weapon damageOrder and Damage, resolved links, limits, build
menus, download placements, meteor, sight/LOS tables, Survival roster fields,
extension flags such as NanolatheInfector and exact economy/cost values;
every movement-class field; side anchor keys and rectangles; complete model
and SimArt sequences (known misses, geometry, frame delays, effect holds,
visit order); and the selected map header and schema. Provenance, warnings,
sounds, Survival AttackerSkin, other installed maps and SimArt bookkeeping
stay excluded under the existing consumer audit.

ValidateMovementClasses compares a supplied map's membership, admitted class
pointers and values; an equal-valued copy cannot replace an admitted pointer,
and the map is never repaired.

#### 16.3.64 U6 construction and movement handler producers

```go
// internal/construction
func NewServiceWithCheckpointBinding(*world.Terrain, *content.Catalog, *units.World, *economy.Service, *checkpoint.BindingAuthority) *Service
func (*Service) RegisterCheckpointOrderHandlers(*orders.CheckpointContext, *checkpoint.BindingAuthority) error
// internal/movement
func (*System) RegisterCheckpointOrderHandlers(*orders.CheckpointContext, *checkpoint.BindingAuthority) error
```

The admitted constructors (construction's shares NewService's body; movement
uses NewSystemWithCheckpointBindings) keep one owner/authority and an opaque
handler source. At the existing lazy creation sites the owned-handler value
is kept beside the cached function — construction wake kind 1, GetBuilt kind
2, movement standby kind 3 — and every queue installation transfers it
through the admitted setter. Nothing creates handlers early, and an ordinary
cached handler never gains proof. Registration requires the exact owner,
authority and source; copies and ordinary constructors refuse. Movement's
airLegHandler writes its actual presence after its proof is checked. Root
supplies the authority at construction creation and registers both producers
before collecting queues.

#### 16.3.65 U6 canonical session script bindings

The session's strict unit binder uses §16.3.60's helper: port 16, query ports
7–15, adopted ports 32/69–75 and both transport mutation callbacks retain
pending receipts on the unit before Create. The per-unit presentation sink
keeps two diagnostic aliases for admitted composition, the exact Unit and the
Terrain receiver of port 16's method value, so a replaced terrain cannot hide
the old ground-height receiver; explosion's private sink must be that same
sink. After reference discovery root registers each unit's Binding,
simulation RNG, render backing, piece map and the validated sinks. Capture
calls no callback, script, render getter or filesystem operation and never
rebinds a VM.

#### 16.3.66 U6 copied mapping-word sources

```go
// internal/orders; private function and original installation proof.
type CheckpointMappingWord struct { /* immutable copied value */ }
func (*WorldQueryAdapter) CheckpointMappingWord() CheckpointMappingWord
func (CheckpointMappingWord) Reader() func(int32, int32) (uint16, bool)
func (*CheckpointContext) ValidateMappingWord(CheckpointMappingWord) error
// internal/movement
func (*ClassLayers) BindMappingWordWithCheckpointBinding(orders.CheckpointMappingWord)
```

The getter copies the callback with its proof only while the proof names this
adapter and authority; a copy keeps its original proof after the slot
changes, and Reader returns the bare function, which carries no provenance.
ValidateMappingWord checks the original adapter against the registered one
without calling the callback. ClassLayers and each ClassLayer keep the copied
value beside their mapping callback; every retained mapping installation,
diagnostic and laboratory paths included, uses the admitted sibling, and
For's inheritance copies it. Mapping bytes become actual presence; a nonnil
mapping without proof refuses. No new mapping read, registry, layer or path
request is introduced.

#### 16.3.67 U6 construction callback storage and production bindings

```go
// internal/construction
func (*CheckpointContext) SetBindings(*Service, *content.SimulationInputs, *units.World, *economy.Service, *combat.Service, *movement.System, *orders.QueueBinding, *checkpoint.BindingAuthority) error
func SetCheckpointPresentationSink[T any, P interface { *T; EmitNanolathe(frame.Event) bool }](*CheckpointContext, P) error
```

CRTRandom, IsSpecialSecondState, ModelForFactory and ModelForUnit are private
slots with ordinary Set/Hook accessors and Set<Name>WithCheckpointBinding,
under §16.3.28's slot rules; all four production installations are at session
composition. Allocator and LimitChecker have none and keep their nonnil
refusals. SetBindings retains one exact tuple: the §16.3.64 owner, the frozen
catalog, the expected World/Economy/Combat/Movement/OrderBinding, and a
repairWorld that is nil or that world. The presentation sink is matched by
concrete pointer (typed nil and foreign implementations refuse without method
calls); it appends authoritative strips, so it is not display state. Capture
never calls CrtRNG, which may initialize a stream, any callback or the sink.
Absent binding bytes become actual booleans in place.

#### 16.3.68 U6 effect art and fragment bindings

```go
// internal/effects
func (*EffectService) CheckpointFixedPool() *FixedEffectPool
func (*CheckpointContext) SetBindings(*EffectService, *content.SimArt, *world.Terrain, uint32, *checkpoint.BindingAuthority) error
func (*FixedEffectPool) SetFragmentStepContextWithCheckpointBinding(FragmentStepContext, *world.Terrain, uint32, *checkpoint.BindingAuthority)
func (*EffectService) SetFragmentStepContextWithCheckpointBinding(FragmentStepContext, *world.Terrain, uint32, *checkpoint.BindingAuthority)
```

The pool retains fragmentContext between ticks; fragmentStepping is the stored
TerrainHeight-presence predicate and must be coherent with it. The admitted
setter records exact pool, terrain, completed tick and authority; ordinary
SetFragmentStepContext clears the proof. The sole production installation,
bindFragmentStepContext, builds World.HeightAt and
fragmentImpactSink{debrisImpactSink{s,tick}} just before effect advancement.
That sink admits effects and smoke strips and is authoritative; its tick must
equal section 1's completed GlobalTick. Art, Impact and TerrainHeight bytes
become validated presence. heightAt has no production installation and keeps
its nonnil refusal.

#### 16.3.69 U6 movement class and callback bindings

```go
// internal/movement
func (*CheckpointContext) SetCompositionBindings(*System, *content.CheckpointKeys, *checkpoint.BindingAuthority) error
```

Damage and ProductFootprint are private slots under §16.3.28's rules, both
installed by root at composition (Damage captures Session; ProductFootprint
captures Session and reads its current Catalog). SetCompositionBindings
requires the §16.3.64 System and authority, keys equal to the shared unit
context's and classes accepted by ValidateMovementClasses, and refuses a
conflicting terrain/path/auxiliary authority in either order. Classes stays
publicly settable because identity and values are verified directly. Classes,
Damage and ProductFootprint bytes become actual presence. Root registers this
in prepareCheckpointMovement before reference discovery. The two callbacks
keep their call sites in cargo cascade and air build approach
[06 §12.1][04 R-ORD-02 §2].

#### 16.3.70 U6 combat callbacks and reaction ownership

```go
// internal/combat
func NewReactionSeamsWithCheckpointBinding(ReactionSeamsConfig, *checkpoint.BindingAuthority) *ReactionSeams
func (*CheckpointContext) SetBindings(*Service, *content.SimulationInputs, *features.Service, *world.Wind, *ReactionSeams, *checkpoint.BindingAuthority) error
```

Combat's ten Service callbacks and eight ReactionSeams callbacks are private
slots under §16.3.28's rules; the configs copy initial fields without invoking
anything. SetBindings retains one tuple whose Features, ProjectileWind and
Reaction equal the expected pointers. InfectionThreat and UnderAttackSilenced
are package functions, VisitOffMapFiled captures the movement Grid, and the
rest capture Session. The Events callback is authoritative (shake, strips).
Capture calls no callback, rule method or lookup cache.

The ten callback and two pointer-edge bytes become actual presence. Reaction
keeps its presence byte and, when present, eight booleans in field-name order:
Allied, ArmConstructionThrottle, ObserverNotice, PurgeOrdersOnDamage,
RetaliationOrder, SlotAcquisitionAdmits, UnderAttackNotice,
UnderAttackSilenced. The active-impact, transport-handoff and area-transaction
refusals stay. Full session admission also needs §16.3.72.

#### 16.3.71 U6 Classic manager composition bindings

```go
// internal/ai
func (*Manager) InitializeBattleStateWithCheckpointBinding(*world.Terrain, RallyBattleBindings, *checkpoint.BindingAuthority) bool
func (*Strategic) BindEnergyEnvironmentWithCheckpointBinding(func() (float32, float32), *checkpoint.BindingAuthority)
func (*Strategic) BindTargetRegistryRebuildWithCheckpointBinding(func(uint32, uint8), *checkpoint.BindingAuthority)
func (*CheckpointContext) SetBindings(*Manager, *content.SimulationInputs, *rng.Simulation, *orders.QueueBinding, *SurvivalInfo, *checkpoint.BindingAuthority) error
```

The nine Manager callbacks (CanPursueAir, IsAlliance, JammerSuppresses,
QueueBuildTyped, RallyProbeKnown, RallyShotTimeAdmits, RallyVisible,
UnitVisible, WeaponMaintenance) and Strategic's energyEnvironment and
rebuildRegistry setters follow §16.3.28's rules; Strategic proofs do not copy
with an embedded Strategic. The admitted initializer stamps its three rally
slots only on an accepted one-shot installation. SetBindings retains one
Manager/inputs/catalog/RNG/order-binding/Survival tuple; Manager.Catalog and
Strategic.Catalog equal the frozen catalog, and Profile.appliedCatalog is nil
or that catalog. Capture applies no profile and rebuilds nothing. The headless
observation wrapper's ordinary QueueBuildTyped replacement invalidates
admission. Survival is nil or the survivor's scenario info, which the scenario
owner serializes. These tags become actual presence. Ext and Modern planner
admission are §16.3.75's.

#### 16.3.72 U6 static paralyze task installation

```go
// internal/combat
func SetParalyzeTaskPush(func(*units.Unit, uint32, uint32)) *CheckpointParalyzeTaskInstallation
func ParalyzeTaskPushHook() func(*units.Unit, uint32, uint32)
func ValidateCheckpointParalyzeTaskInstallation(*CheckpointParalyzeTaskInstallation) error
// internal/orders
func ValidateCheckpointParalyzeTaskBinding() error
```

The package-global paralyze task callback is a static composition seam
([06 §10], [06 R-DMG-01 §11]), not a gameplay policy. Its storage is private;
each nonnil installation returns a new opaque receipt, and nil clears it.
Orders keeps its receipt from installing PushParalyzeCredit, and root
validates it before full combat/session admission without invoking or
reinstalling the callback. A copied or forged receipt, missing callback or
later replacement refuses. It adds no payload byte.

#### 16.3.73 U6 Modern AI immutable table provenance

```go
// internal/aikit
func (*Table) ValidateCheckpointBindings(*content.Catalog, *content.CheckpointKeys) (uint8, error)
```

BuildTable keeps a private snapshot after its final fighter classification
(and at its nil-catalog return), changing nothing it computes: Table and
catalog identity, the closed construction-rule tag (nil 0, Strict 1,
Community 2, Modern 3), Units and Capped in order, byKey/byDef/
defensiveFeatures membership, every UnitInfo value and pointer, and each
Builds slice in order with duplicates. Unknown rules build normally but mark
provenance unsupported. Validation compares against the snapshot without
calling BuildTable, rule or lookup methods, workers, reflection or sorting,
and returns the original rule tag, which may differ from the manager's current
rules. The executor's table byte is presence plus that u8 (§16.3.75).

#### 16.3.74 U6 immutable mission provenance

```go
// internal/mission
func SnapshotCheckpointInputs(*Mission) (*CheckpointInputs, error)
func (*CheckpointInputs) Validate(*Mission) error
// formats
func SnapshotCheckpointOTAInputs(*OTA) (*CheckpointOTAInputs, error)
func (*CheckpointOTAInputs) Validate(*OTA) error
```

Deferred Community placement and the lava, score and meteor consumers reread
authored values, so immutable mission and OTA/TDF values are snapshotted at
pre-compose admission and validated before ready and in full-capture
preflight, with no parsing, getters or file reads. It adds no wire payload.

- **Mission:** Type, TerrainKey, selected Schema (Name, StartPositions),
  ordered Units/Specials/Features with every field, WindBounds, UseOnlyPath,
  IsRestore, campaign fields and Difficulty. Victory/Defeat and triggers stay
  mutable payload (§16.3.38). Campaign and restore remain unsupported.
- **Formats:** the OTA, Document and Section pointers and the whole raw tree —
  OTA strings and schemas, every Section and Item field and edge, resolved
  indexes — with no key whitelist. Line/Column locations are excluded.

Snapshot records a fixed ordered list with a visited set; validation walks only
that list, so inserted cycles or replacement nodes refuse without being
followed.

#### 16.3.75 U6 closed Modern planner and controller bridge

```go
// internal/ai
func NewCheckpointModernPlanner[T ModernAIStep](T) CheckpointModernPlanner
func (CheckpointModernPlanner) Matches(Planner) bool

type CheckpointControllerOwner interface {
    ControllerCheckpointProvider
    EnableCheckpointApplications(checkpoint.Identity, *content.CheckpointKeys) error
    ValidateCheckpointBindings(*Manager, *CheckpointContext) error
}
func NewCheckpointControllerSource[T any, P interface { *T; CheckpointControllerOwner }]() CheckpointControllerSource
func (CheckpointControllerSource) Valid() bool
func (CheckpointControllerSource) EnableApplications(*Manager, checkpoint.Identity, *content.CheckpointKeys) error
func (CheckpointControllerSource) WriteCheckpoint(*Manager, *checkpoint.Encoder, *CheckpointContext) error
func (CheckpointControllerSource) AppendSummary(*Manager, *checkpoint.Summary) error
func (*CheckpointContext) SetModernBindings(*Manager, Planner, CheckpointModernPlanner, CheckpointControllerSource, *checkpoint.BindingAuthority) error
func (*CheckpointContext) ValidateModernManager(*Manager) error
// internal/aikit
func CheckpointControllerSource() ai.CheckpointControllerSource
func (*Host) ValidateCheckpointBindings(*ai.Manager, *ai.CheckpointContext) error
// internal/session
func RegisterModernAIWithCheckpointBinding[T ai.ModernAIStep](T, ai.CheckpointControllerSource)
```

The §16.3.44 planner witness is an opaque ai-owned value, so Manager and
Session check the same proof. The controller source takes no callback: its
adapters assert Ext to exactly P (typed nil refused) before calling only that
owner's diagnostic methods. The one production instance is [Host, *Host],
from aikit, passed by mods/aikit at Modern registration; Session keeps step,
witness and source together. There is no second registry or dynamic
capability selection.

SetModernBindings stages one manager/authority/witness/source tuple and
requires Modern controller kind, a valid player, matching keys and an enabled,
successful, inactive history for that player. Nil Ext is valid before lazy
Host creation. Its authority and §16.3.71's must agree, in either order.

Bytes: stateless Planner tags 0–2, the registered Modern step 3; Manager.Ext
absent 0 or admitted Host 1, the Host payload being the separate controller
fragment. EnableApplications is an entry-time operation, never a capture one.
NewHost always keeps self and manager provenance, since entry priming can
precede enabling; in begin, after executor assignment and before worker
preparation, the executor, manager, table, catalog and terrain aliases are
retained and later validated with §16.3.73's snapshot. The executor's manager
byte is presence; its table byte is presence plus the original rule u8.
Nothing reads kit, obs, mapInfo, brain, rand, batch, ready, flight, Shared or
worker readiness.

#### 16.3.76 U6 materialized terrain input provenance

```go
// internal/world
func (*Terrain) ValidateCheckpointInputs(*content.SimulationInputs, string) error
```

Heights and their floor bounds never change in battle (no terrain deformation
[03 R-TERR-01 §3]), and LOS terrain words are built once [03 §3.5]. At the end
of a successful world.Load the terrain keeps its receiver, filesystem,
catalog, requested terrain key (the mission's, not the display MapName) and
TNT path, and snapshots CellW/CellH/Version, the plot height/min/max triples,
SeaLevel, the three gravities, LavaWorld, water damage, WindMin/WindMax/Tidal
(exact float32 bits; NaN refuses), PlayRight/PlayBottom, losWords and
losBuildCount. Validation compares these without reads, recomputation or
allocation; copies and hand-built terrains refuse. Occupancy, metal,
features, anchor/damage words and flags stay mutable payload; renderer tiles
and entry undo metadata stay excluded. Session validates before readiness and
at each full capture.

#### 16.3.77 U6 remaining cheap summary leaves

The §16.3.7 summary is implemented on units.World, orders.Queue, cob.VM,
cob.AimSlot, features.Service and world.Terrain: selected-only walks into a
local Summary committed on success, with no allocation or callbacks. A
selected NaN refuses; signed fields extend their source type.

| Leaf | Words, in order |
|---|---|
| Units | physical slot count (slot zero included); empty slot tag 0; freed residual tag 2, Handle, Owner, Remaining bits; live slot tag 1, Handle, AllocationSerial, Owner, Health, Remaining bits, Flags, X/Y/Z, Move.Heading/Speed, Pending, Stunned, ParalyzeExpire, then per weapon slot Reload, Ammo, IssueBit, Ready and the raw readyWord; then live and created counters per player |
| Queue | primary length and nodes, secondary length and nodes, lastPumpTick; per node ID, Phase, Target, GoalX/Y/Z, Deadline, Param1/2/3, Flags |
| VM | per physical thread (eight) Status, PC, SP, Sleep, WaitPiece, WaitAxis, WaitThread, SignalMask and 32 Stack words; statics length/values, activeThreadCount, nextIdentity, eight threadIdentity values |
| Section 5 | feature count, reproduction cursor, arenaHeld; plot length and per cell OccupantA/B, Metal, Feature, AnchorWord, FlagByte, with the full writer's exclusions |

Session appends these only for live unit slots. Known blind spots, caught
only by full digests: residual Kills, detached queue nodes, VM instruction
and callback data, unselected geometry and feature progress, strings.

#### 16.3.78 U6 capture lifecycle composition

- **Positions.** The pump ordinal counts ExecuteStep plans that Run,
  zero-tick pumps included. ConsumedInput counts drained commands passed to
  applyHumanCommand, refusals and no-ops included; it is not the relay stream
  position (M4 maps the two). Paused consumption advances it without a
  checkpoint. Neither enters canonical bytes. Interior capture follows
  publication when another tick will execute; the last executed tick follows
  the retail tail. Requests off the cadence update the result only, and
  publication is checked by the frame buffer's PublishedTick and an empty
  staged event window, never by presentation contents.
- **Full capture** validates inputs once, seeds physical allocations, then
  repeats owner collectors in section order until nothing is added; sections
  1–13 are all present. The computer fragment is ten fixed slot rows, each
  manager presence and, when present, Manager payload, ApplicationHistory
  payload, controller presence and any Modern Host payload. Scenario follows.
  Cheap computer rows carry the Host's eleven selected words, or for an absent
  controller the history's five words and six zero words.
- **Enable** requires the completed admitted entry, initialized RNG, no
  executed tick and the host's opening publication; a sticky admission flag
  tells a wrapped runtime tick zero from entry. It attaches application
  histories, then captures the entry, stopping what it attached on failure.
- **Disable** stops those histories through their sticky Fail and clears the
  rings and pending sinks (a pending result gets a cancellation error). A
  stopped chain cannot restart on that Session (§16.3.24); a fresh admitted
  entry is required.
- **Failures.** Recursive capture or stepping from a sink refuses; a panicking
  sink still cleans up. Capture errors affect diagnostics only and append no
  row. Disabled pumps do no traversal or hashing.

FixedEffectPool.CheckpointCounts() (records, fragments int) supplies the
ring's pool counts, and Session reuses its thirteen Summary accumulators.

#### 16.3.79 U7 measurement composition

The displayless simulation benchmark's opt-in `SimBenchOptions.Checkpoints
bool` (`--sim-benchmark-checkpoints`) runs the same scene, seeds, resources,
rules and content through the M2 single-seat admitted constructor, stages the
armies, publishes the opening and calls EnableCheckpoints once. Unsupported
admission is reported, never bypassed: the three-computer scene cannot use
Strict's one-computer-per-human limit, so Modern scenes provide the
comparison. The fixture room uses profile `retail`, participant identity 1,
player view 1024..2048 without full-map, spectator/replay view 256..2048 with
full-map, revision/scheduling/pacing/drop/audience 1, cumulative grace
90000 ms and zero audience delays; these are fixture inputs, not lobby
defaults.

A run fails if capture diagnostics fail. The report adds `checkpoints` and
optional `checkpoint_records`, `checkpoint_ticks`, `checkpoint_tick` and
`checkpoint_digest` (latest full SHA-256, hex). No wall-clock reader enters
Session. Limits are §16.3.83 (M3-C9). The census counts deaths with
`units.World.DeathDispatches() uint64`, incremented just before the primary
death-hook dispatch in FinalizeDeath and excluded from the unit stream and
summary.

#### 16.3.80 U7 bounded history comparison

`session.CompareCheckpointHistories(a, b CheckpointHistory)
(CheckpointComparison, error)` compares detached histories only, bounded to
64 records and 600 rows. Runtime rows are interior or final; digest records
may also be entry, which, if present, comes first at tick/pump/input zero. It
refuses duplicate positions, repeated tick labels, invalid boundaries,
out-of-order ticks (forward delta nonzero and below half the u32 range) and
decreasing pump or input ordinals.
The peers must already share content/configuration identity. Only exact
`CheckpointPosition` matches (tick, boundary, pump ordinal, consumed input)
are compared; tick wrap is preserved and labels never sorted.

```
type CheckpointDifference struct {
    Position CheckpointPosition
    Owners [CheckpointOwnerCount]bool // array index + 1 is the section ID
    RNG, Pools bool // tick-row evidence only
    Full bool // full digest differs; record evidence only
}
type CheckpointComparison struct {
    ComparedTicks, ComparedRecords int
    TickDifference, RecordDifference *CheckpointDifference
    MayPredateTicks bool
    UncoveredOwners [CheckpointOwnerCount]bool
}
```

The first differing common row compares selected `(Words, Sum)`, both RNG
states and draw counts and the six pool counts; the first differing common
record compares full and owner digests. `MayPredateTicks` means the first
common row already differs. `UncoveredOwners` marks owners whose full digest
differs where their summary is equal at the same position. A nil difference
means the comparable evidence was equal, not that the worlds are.

#### 16.3.81 U7 encoding cost without schema changes

Dense writers format diagnostic paths only on error. Encoder's
`FieldChild(prefix, name string)` and `FieldIndex(prefix string, index int,
suffix string)` keep the parts of `prefix + "." + name` and `prefix + "[" +
decimal(index) + "]" + suffix` and format them only on failure; ordinary Field
clears them. The two SHA-256 destinations share one 4 KiB buffer, flushed
before each section closes, while a caller's output sink stays synchronous and
unbuffered. The content semantic digest batches through one 512-byte buffer.
None of this changes bytes, boundaries, hash domains or validation.

#### 16.3.82 U7 portable scene and live fault evidence

`TestCheckpointPortableScript` is asset-free: an in-memory HPI with two
weaponless commanders composes two copies per reserved mode through the real
compiler, freeze and admitted constructor, and thirteen explicit pumps run 30
ticks (one-to-five-tick batches, a paused Stop, empty commands, zero-tick
input retention). It compares bytes, the thirteen owner digests, RNG
histories and pump/input positions. Its 507 identity, checkpoint and owner
rows are the M3-C8 comparison, which must agree across native Darwin/arm64,
Linux/amd64 v1/v3 and Windows/amd64 CI jobs.

`TestCheckpointLiveFaultDiagnosis` injects one health decrement before tick 17
in one copy; the two detached histories place the first selected difference
at 17 and the first full difference at 30 in every mode, with RNG equal
through tick 60.

#### 16.3.83 U7 cost acceptance limits

These are M3 diagnostic limits, not M6 latency acceptance. A limit is never
loosened to pass a run. They are measured on an Apple M3 Pro (Darwin/arm64,
Go 1.27.1), fixed-state probes at GOMAXPROCS=4 and the simulation benchmark at
GOMAXPROCS=2, comparing median process CPU of alternating runs on identical
workloads.

| Measurement | Limit |
|---|---|
| Fixed-state digest, ordinary tick 900 and busy-modern-classic-v1 tick 300 | ≤50 ms/op |
| Same states, byte capture into a reused buffer | ≤66.667 ms/op |
| Selected row in those states | ≤1 ms/op, zero allocations |
| Fixed-state writer allocation | ≤8 MiB/op, ≤200,000 allocations/op; canonical bytes ≤8 MiB |
| Retained 64-record/600-row history | ≤256 KiB |
| Modern controller leaf, with work outstanding or complete | identical bytes and summaries; ≤33.333 µs/op, ≤8 KiB/op, ≤256 allocations/op; summary zero allocations; capture never waits for worker work |
| Disabled capture, three 250-unit armies, seeds 7/7, 1,200 + 300 ticks | CPU ≤5%, bytes and objects per tick ≤1% above the pre-instrumentation baseline; identical census and fingerprints |
| Enabled capture on that scene | CPU and p95 tick ≤11.111 ms, maximum tick ≤166.667 ms, ≤2 MiB/tick |

The enabled limits permit a visible checkpoint hitch; M6 must set and meet its
own interactive latency bound. The busy fixture is the admitted two-seat Ashap
Plateau entry with 67 extra units per seat, six factory queues and 88 move
orders; at tick 300 it needs at least 100 live units and active movement and
building, and measurement must change no RNG stream, state, row, census or
history.

### 16.4 Two-client play-test slice

Two clients control different human seats of one Modern skirmish. Both
replicas simulate both seats; presentation selects the local seat, and no
client is forced onto the other's perspective. The work is the §6 subset
those seats exercise: owner-relative visibility and targeting, per-seat
settlement and end state, and host-independent effect admission. The
ordinary single-seat path and its fingerprint locks are unchanged.
Unsupported configurations and commands are refused explicitly; no
unfinished mechanic is replaced by a guess.

The relay receives seat-attributed commands, assigns stream order and the
next unsealed tick, and sends the same sealed grants to both clients (§4).
Each granted tick runs as one pump at normal speed, and a client advances
only when it holds the complete sealed prefix. Network code stays in the
host, relay and lockstep packages (§14). The whole-state fog limitation of
§13 applies.

**Unit checksum.** `Session.UnitStateChecksum()` is SHA-256 over the domain
`nanolathe/playtest-units/v1`, the completed tick `u32`, then the live
allocations in player-slice and slot order: handle `u16`, allocation serial
`u64`, owner `u8`, dying `u8`, X/Y/Z raw fixed-point `i64` and health `i32`,
all fixed-width little-endian. Live nanoframes and dying records are
included; freed records are absent. It needs no sorting, allocation, worker
join, pointer address, host clock or viewing state. Each client reports the
tick-zero value with its identity and the value at every tick divisible by
30; they are compared at the same completed tick, and a mismatch stops the
match and reports the tick (§9.3). This is a partial check: it catches
hidden divergence only once it reaches unit positions or health, and equal
checksums do not prove equal state. The M3 checkpoint writer and histories
remain diagnostics, not prerequisites. State checks neither synchronize a
world nor authorize a command.

#### 16.4.1 Prototype composition interfaces

These interfaces implement §6 for two hostile human seats with common
visibility settings. They add no checkpoint schema.

**Visibility** reuses the ten coverage grids. It adds
`SensorUnit.AllocationSerial uint64`, `Service.EnableOwnerPerspectives()`,
`Service.SensorTickForPerspective(owner PlayerID, defeated bool, tick uint32,
activePlayers int, units []SensorUnit)`, and
`Service.StatusForPerspective(owner PlayerID, id uint16, allocationSerial
uint64, unitOwner PlayerID, fallback uint32) uint32`. Disabled lookup returns
fallback unchanged. Enabled lookup overlays only the sensor mask `0x1700`
from that perspective's allocation-qualified bank; an unseen allocation gets
constructor status (sonar only for its own perspective). Serial zero returns
that seed without storing it, because initial COB creation precedes serial
assignment. The existing sensor algorithm writes each bank at that human's
deadline, and its locally-simulated source gate also requires the source
owner to equal that human. The shared reveal deadline remains the unit
field. History sampling uses the querying owner online; single-player keeps
its local history reader. Consumers select the actor's perspective,
fallback targeting and underwater visibility included. There is no second
visibility service or rules registry.

**Effect events.** `frame.NewEventBufferWithIndependentEffects(Limits)
*EventBuffer` supplies a second bounded effect-only channel, and
`EventBuffer.EffectEvents() []Event` borrows its current window; ordinary
buffers return their staging events. The online channel has its own IDs,
sequences and exhaustion and receives valid routed effect events before any
presentation-window refusal: COBSFX, Nanolathe, MuzzleFlash, SmokeStart,
SmokeEnd, ProjectileTrail, Impact, WaterImpact, Explosion, LHTFlash and
Corpse. Audio, status, announcements, music and shake never enter it. The
presentation verdict of Admit and all publication ordering are preserved;
Reset clears both windows and keeps counters. The session composes this
buffer before unit scripts run and passes `EffectEvents` to the effect
consumer. The COB producer uses the union of both human views (§6.3); local
drawing applies its own visibility afterwards, using the viewing seat's
point-visibility predicate for fixed effects and whole debris after the
shared pools are copied, while strips keep their drawing gates.

**Per-seat results.** `Session.onlineResults` (`*onlineResultState`, nil in
ordinary sessions) has ten optional rows, each with its own EndLatch and
pending and final Result; `newOnlineResultState([10]bool)` initializes the
admitted rows. `evaluateOnlineSeatResult(player int, tick uint32)` runs at
that player's due before settlement gates and publishes only its row's
ending and countdown fields. `stepOnlineNoHumanEnd(tick uint32)` runs after
the player loop with the same latches; `onlineSeatEnded(int)` controls
command admission and `onlineBattleEnded()` the shared terminal transition.
`ResultForSeat(uint8)` returns a detached presentation result, and
`GetResult` and committed frames select the local client's row and its
countdown, including the final publication before grants stop. Defeat
precedes victory; an opponent that never created a unit cannot satisfy
victory; false due predicates keep the countdown; crossing the signed
countdown below zero is terminal; a mutual wipe is defeat with no winner
`[08 R-TRIG-01 §6]` `[08 R-SESS-01 §1]`. Commander sweeps do not stop
because one local row ended. The kind-3 elimination announcement keeps its
one CRT draw and eight-entry selection `[08 R-CAMP-01 §9]`. Deathmatch and
respawn are refused at entry. Single-player results are unchanged.

#### 16.4.2 Play-test entry and local transport interfaces

`session.NewPlaytestSkirmish(inputs, config, localSeat, progress)` admits
Modern online skirmish with 2–10 human rows on static teams, and online
Survival with 2–3 human survivors plus the attacker as the last row, and
composes every human with controller byte 1 in canonical seat order. The
local seat must be a human row; computer and watcher rows, cheats, watching,
Deathmatch and other rule sets are refused. Victory is retail's kind-3 sweep
with shared victory `[08 R-SKIR-01 §3]`, reading row B as the transpose of
the one shared matrix (Q22); Survival has no victory and each seat gets the
Survival result line. `PrepareGrantedBattle()` completes entry dispatch
without wall-clock stepping; composition calls it before presentation
exists, which is the same world because dispatch runs no tick, and the map
schema comes from the compiled catalog's headers. `StepGranted(tick)`
accepts exactly the next tick and runs one pump; `OnlineBattleEnded()` is
the shared termination condition. The local seat affects presentation only.
Mobile-build admission checks the issuing seat's known-site predicate and
the derived site height before any queue cancellation or replacement;
construction's later terrain and occupancy checks still apply.

`Session.CaptureOnlineCommand(HumanCommand) (SeatCommand, error)` is the
quiescent host adapter after local selection resolves: it captures current
allocation references without enqueuing or mutating a world. Its closed
vocabulary is Order, Stop, Activation, MobileBuild, FactoryBuild,
CancelProduction, Stockpile, GroupAssign, Stance, Cloak, SelfDestruct,
CancelQueuedMove and BuilderOptions, keeping the local producer's count
defaults and ordering and then the online codec's validation. Unsupported
kinds and references fail explicitly. The driver translates a pending move
receipt to its assigned stream position before transmitting
CancelQueuedMove; a client sequence is never a stream position.

**Loopback transport.** A development-only, bounded two-connection relay on
a numeric loopback address, with no authentication, reconnect or final
removal; a lost connection aborts the test without a result. Both
connections report their full `netproto.Identity` and tick-zero unit
checksum before grants begin, and any difference refuses entry. Commands
stay opaque. It seals at most one tick per 1/30 second and waits for both
acknowledgements before the next grant, so a slow client slows both. It
compares the checksum at every tick divisible by 30 and stops on a
mismatch. Client sequences follow §4.2; the seat comes from the admitted
connection.

```go
type LocalHello struct { Seat uint8; Identity netproto.Identity; InitialChecksum [32]byte }
type LocalCommand struct { Seat uint8; Sequence, Position uint64; Payload []byte }
type LocalGrant struct { Tick uint32; Position uint64; Commands []LocalCommand }
func ListenLocal(address string) (*LocalRelay, error)
func ListenLocalWithCommandDelay(address string, delay time.Duration) (*LocalRelay, error)
func (r *LocalRelay) Addr() string
func (r *LocalRelay) Close() error
func DialLocal(ctx context.Context, address string, hello LocalHello) (*LocalClient, error)
func (c *LocalClient) Submit(payload []byte) (uint64, error)
func (c *LocalClient) ReadGrant() (LocalGrant, error)
func (c *LocalClient) Acknowledge(tick uint32, checksum [32]byte, ended bool) error
func (c *LocalClient) Close() error
```

Dial admits and sends the hello without waiting for the second seat;
ReadGrant waits for the ready barrier. An acknowledgement names exactly the
preceding grant, and only multiples of 30 carry a checksum. Both terminal
acknowledgements finish the stream without another tick. Socket reads and
writes are bounded and cancellable by Close. The loopback relay holds at
most 64 commands and 8 MiB of pending payload per tick (each command at most
4 MiB) and refuses rather than buffers more; every envelope length is
checked before allocation. LocalClient supports one reader and serialized
concurrent Submit and Acknowledge calls. No network dependency enters an
authoritative package.

**Driver.** `lockstep.NewLocalDriver(session, client)` returns
`(*LocalDriver, error)` with `Pump() (bool, error)` (nonblocking, at most one
granted tick), `Submit(session.HumanCommand) (uint64, error)` and
`Close() error`; it requires a prepared battle. Network reads run
separately; only the host calls Pump and Submit and touches the session, and
it drains command receipts after Pump and reports refusals. A terminal
transport error freezes the battle and reports it without awarding a
result. Pause, speed changes, saves and asynchronous wall-clock stepping are
disabled. The one pending tracked move of the resource double-click keeps
its receipt until the grant assigns a stream position; a closed driver never
executes a buffered grant.

**Launching the local play test.** Use the same build and retail asset
installation for both windows. Start seat 1 first, then seat 2:

```sh
nanolathe --root ~/TotalAnnihilation --mod none --map 'ashap plateau' --fullscreen=false --local-mp-listen 127.0.0.1:39731
nanolathe --root ~/TotalAnnihilation --mod none --map 'ashap plateau' --fullscreen=false --local-mp-join 127.0.0.1:39731
```

The first window waits until both clients pass entry checks. The launch
fixes Modern rules, two hostile human seats and seeds 7/11 (identical
`--seed N` arguments replace both), ignores saved content selection, and
rejects explicit mods, mutators, restrictions, AI and probe or benchmark
options. Presentation settings stay local. Selection, orders and build
controls submit to the relay; menus keep receiving grants; pause, speed,
save, load, restart and world-changing chat commands are unavailable.
Closing a window stops the test on its peer.

#### 16.4.3 Local responsiveness experiment

`--local-mp-command-delay-ms N` on the listener adds a fixed 0..1000 ms wait
to both seats' orders (default zero). It is a host diagnostic, not a game
rule or simulated ping. `ListenLocalWithCommandDelay` holds accepted
commands until their monotonic host deadline and releases only the ready
prefix in stream order; empty grants continue at the ordinary pace and
expose only the last released position. The 64-command and 8 MiB pending
bounds cover delayed commands. It adds no worker or simulation state,
changes no match identity, and delays neither acknowledgements nor grants.
It does not emulate jitter, loss, asymmetric links or TCP retransmission. A
cancellation that must first wait for its stream-position receipt can pay
the delay twice.

```sh
nanolathe --root ~/TotalAnnihilation --mod none --map 'ashap plateau' --fullscreen=false --local-mp-listen 127.0.0.1:39731 --local-mp-command-delay-ms 100
nanolathe --root ~/TotalAnnihilation --mod none --map 'ashap plateau' --fullscreen=false --local-mp-join 127.0.0.1:39731
```

### 16.5 Hosted relay

The hosted relay serves the online battles of §16.6 — 2–10 human seats,
with §16.4's Modern battle, unit checksum, command vocabulary and per-seat
simulation — over the Internet, with no artificial order delay. A lost
connection of a seat still playing ends the room without a result or any
gameplay removal; a seat whose result is final may leave (§16.6.1). Room
lists, computer seats, reconnect and removal votes are later work.

#### 16.5.1 Transport and admission contract

`internal/relay` reuses the loopback relay's grant, command and
acknowledgement payloads and identity comparison, and adds the hosted
handshake and lobby (§16.6.1). The hosted protocol is version 6, and the
relay also serves version-5 seats (§12.2); a hello of another version is
refused naming the served versions and the client's.

```go
type HostedConfig struct {
    TLSConfig *tls.Config
    InsecureLoopback bool
    MaxRooms int // zero selects 16; admitted range 1..256
    MaxConnections int // zero selects 512; admitted 2..1024, two per room at least
}
type HostedDialOptions struct {
    TLSConfig *tls.Config
    InsecureLoopback bool
}
func ListenHosted(address string, config HostedConfig) (*HostedServer, error)
func (s *HostedServer) Addr() string
func (s *HostedServer) Close() error
func DialHosted(ctx context.Context, address, room string, hello LocalHello, options HostedDialOptions) (*LocalClient, string, error)
```

**Rooms and admission.** A client sends an empty room code to create a room
or a code to join one. The server assigns the creator seat 0 and each
joiner the lowest free seat, refusing a conflicting hello seat. Room codes
are six characters from `CFHJKMNPRTVWX23456789`, each symbol equally likely
and drawn with cryptographic randomness; they are private invitations, not
credentials.
The alphabet leaves out every character a player can misread or mistype as
another (0, 1, O, I, L, D and Q, and S, Z, B and G beside 5, 2, 8 and 6) and
every vowel, so a code never spells a word. A typed code ignores case,
spaces and dashes, and reads S, Z, B and G as 5, 2, 8 and 6. Joining a full,
unknown, closed or started room is refused. A join of an auto-start room
(§16.6.1) compares the joiner's identity and initial checksum with the
creator's: Protocol, Content, Map, Rules, Mod and Configuration must match,
and Build is advisory and not compared (§8.2). A mismatch refuses the
joiner, naming the field, and leaves the creator's identity in place. A
lobby compares digests at Ready instead (§16.7).

**Trust.** TLS is mandatory except an explicitly requested numeric-loopback
test listener and connection. Clients verify the server certificate; a
custom root (`--relay-ca`) supports private test certificates, and no option
skips verification. `cmd/nanolathe-server` serves many independent rooms
with no assets, simulation, renderer or audio imports. Routine logs carry
no room codes or credentials.

**Limits.** These are Nanolathe host limits, not retail behaviour or an
advertised public capacity:

- at most 512 established and pending connections (`--max-connections`);
  the deployed image serves 128 rooms (`--max-rooms`, admitted 1..256);
- per room at most 64 commands and 256 KiB pending; client frames of at
  most 256 KiB; at most 1 MiB plus 4 KiB queued per peer, its one waiting
  progress report included — so one misbehaving room holds about 1.3 MiB
  (pending commands and one shared queue of grant bodies) plus 256 KiB per
  seat (its reader's frame and its writer's in-flight copy), and every
  connection the relay admits at once stays under the image's 384 MiB soft
  memory limit. A hosted client refuses to send a larger command;
- deadlines of 10 seconds for the handshake, 30 minutes for a lobby to
  start, 5 seconds per write, and 10 seconds without execution progress
  once the battle runs;
- once grants flow, a client that receives no relay message for 25 seconds
  stops; that exceeds the 10-second progress abort. Pings do not count,
  since a browser answers them without telling the page.

Per-peer writers are bounded and separate from room pacing, so a slow
reader blocks no other room. Every close and error path releases its
connection and room capacity. A transient accept error, such as descriptor
exhaustion, is retried with bounded backoff; only Close ends the service.
The health listener sits outside the connection limit (§16.5.6).

**The browser build** reaches the relay through the browser's own
WebSocket, never a TCP socket or Go's TLS. The URL rules are the native
client's: `wss://host/relay`, and `ws://` only with the numeric-loopback
test switch. The browser performs TLS and certificate verification with its
own trust store, so a custom trust root (`--relay-ca`) is refused, as is a
`host:port` address, which needs a TCP socket; it also answers the relay's
pings itself. The client requires subprotocol `nanolathe-relay-v1`, checks
it once the socket opens and receives binary messages as array buffers. Its
connection gives the client the same byte stream as the native one: reads
cross message boundaries; a write becomes binary messages within the
relay's client frame bound and waits, under its deadline, while the
browser's send buffer holds more than 1 MiB plus 4 KiB; read and write
deadlines work; and the connection fails when unread relay messages exceed
twice the largest one it accepts. Its browser callbacks only copy, record
or signal, so none blocks Go's single wasm thread. `tools/check-retail` and
the CI browser job play a hosted match from a js/wasm client under Node 22
or later, whose global WebSocket stands in for the browser's, against a
native relay on loopback.

**Known gaps (M7).** One client can hold rooms open by creating a room,
joining it with a second socket and acknowledging at 30 Hz. The final done
or failure frame can be lost to a TCP reset on Linux. The server answers a
client's WebSocket close frame with its own but never starts the closing
handshake: it ends a room by closing the socket after the done or failure
frame.

#### 16.5.2 Continuous grants and client playout

**Relay pacing.** After Started a room seals at most 30 ticks a second
without waiting to acknowledge every tick. Seals keep a 30 Hz phase: a
timer that wakes late does not delay the next seal, and only a gap of a
whole interval or more, such as a wait at the lead bound, restarts the
phase without a burst. Only released stream positions are sealed. A room
may lead its slowest acknowledged seat by at most 30 ticks; at that bound
grants wait. Each seat acknowledges consecutive executed ticks; an
acknowledgement ahead of the last grant, a duplicate or a gap fails the
room. Each 30-tick checksum is compared at that same tick even when the
reports arrive at different times. A seat with no execution progress for
10 seconds fails the room; other rooms continue.

**Terminal handshake.** On the first terminal acknowledgement the room
stops granting. Both seats must report the same terminal tick and outcome
bit, then receive explicit normal completion. Surplus grants already sent
are drained without running any tick after the shared terminal state. A
discrepancy fails the room instead of awarding a result.

**Progress report.** After Started the relay sends each playing version-6
seat a Progress message (§12.2) about once a second, and at once when a
seat leaves or its result becomes final. It carries the agreed tick — the
last tick whose ended bits, and at every 30th tick unit checksums, the
relay has compared across every playing seat — the newest tick it had
sealed, against which each seat's lag is measured, and per slot whether it
still plays, whether its result is final, its last acknowledged tick and
the relay's latest WebSocket ping round trip, zero when unmeasured (over
native TLS, or before the first pong). A seat holds at most one unwritten
report, in its queue place behind the frames before it, so a slow reader
receives the latest state rather than a backlog; a report that does not fit
the seat's queue bounds is dropped and never fails a room. Reports are host
diagnostics and never reach a simulation. `relay.LocalClient` consumes them
inside ReadGrant and returns the latest from `Progress()
(HostedMatchProgress, bool)`: `Agreed`, `Sealed` and per slot `Playing`,
`Final`, `Acked` and `RTT`. `Traffic() LocalTraffic` counts the client's relay
messages and their bytes, length prefixes included, in each direction since
it connected, the hello and lobby included. Both may be called from any
goroutine.

**Client playout.** `internal/lockstep` has `Client` (Submit, ReadGrant,
Acknowledge and Close, with `relay.LocalClient`'s signatures) and
`NewPacedDriver(*session.Session, Client) (*LocalDriver, error)`.
`Completed() bool` reports explicit normal relay completion, never a local
result, close or failure. At the shared end the hosted result overlay waits
for it; a seat defeated while others play on sees its own result at once
(§16.6.2). The
paced driver uses a bounded 32-entry receive queue, monotonic host time, a
one-tick reserve (about 33 ms) and a steady-state release of at most 30
ticks a second. It starts with two grants and refills the reserve after an
underrun. A lone final grant waits at most two normal intervals, so a final
tick never waits forever for a second grant. Catch-up runs at most 33 ticks
a second when more than two grants are waiting.

The window host calls Pump once per 30 Hz host step, and its steps land on
display refreshes, so they arrive unevenly. A hidden browser page makes no
host steps; its background step pumps instead, at each grant's arrival
(DESIGN_BROWSER_HOST §4 contract 10). Deadlines therefore keep their
phase through lateness of up to two normal intervals and discard only
lateness beyond that. One Pump runs every due tick, at most three, each
through its own `StepGranted`. The normal interval equals the host step
period; hosts updating below 20 Hz cannot sustain 30 ticks a second. No
wall clock enters the session. Input submission is immediate, and commands
take effect only on their sealed ticks. The driver records compact timing
observations for the latency probe without changing payloads or simulation
state.

#### 16.5.4 Starting the hosted play test

Players normally use the MULTI screen (§16.6.2). The command line remains
for tests and probes. The server is a standalone standard-library program
that needs no retail installation:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o nanolathe-server ./cmd/nanolathe-server
./nanolathe-server --listen :39032 --tls-cert /path/to/fullchain.pem --tls-key /path/to/privkey.pem
```

The first client creates a room and prints its code in the game messages
and standard error; the second supplies it. Both use the same content, map
and optional seed:

```sh
nanolathe --root ~/TotalAnnihilation --mod none --map 'ashap plateau' --fullscreen=false --relay-address wss://relay.nanolathe.gg/relay
nanolathe --root ~/TotalAnnihilation --mod none --map 'ashap plateau' --fullscreen=false --relay-address wss://relay.nanolathe.gg/relay --relay-room ABC234
```

`--relay-address` takes `host:port` for native TLS or a `wss://host/relay`
URL. `--relay-ca cert.pem` trusts a private test certificate. For a test on
one machine, run the server with `--listen 127.0.0.1:39032
--insecure-loopback` and give both clients `--relay-address 127.0.0.1:39032
--relay-insecure-loopback`; these plaintext switches refuse non-loopback
addresses. The local delay flags cannot combine with the hosted path, and
the content-selection protections of §16.4.2 apply.

**Latency probe.** The opt-in real-socket sweep:

```sh
NANOLATHE_RETAIL_ASSETS=~/TotalAnnihilation NANOLATHE_RELAY_LATENCY=1 GOMAXPROCS=4 GOFLAGS=-trimpath go test -p 4 -tags retail ./cmd/nanolathe -run '^TestHostedRelayLatencyRetail$' -count=1 -v
```

It runs two independently paced real sessions, a headless host pumping on
30 Hz host steps over a 75 Hz display (`NANOLATHE_RELAY_DISPLAY_HZ` selects
another rate), loopback proxies with per-direction scheduled delay and
jitter at 0, 50, 100 and 150 ms RTT plus an asymmetric stall case, five
seconds of measured play after warm-up, and the unit checksum. Samples
measure command submission through application, excluding input polling
and display. `NANOLATHE_RELAY_WEBSOCKET=1` runs it over WebSocket, and
`NANOLATHE_RELAY_ENDPOINT=wss://host/relay` runs one two-seat battle through
that remote service with no local relay or injected delay. Targets: steady
30 Hz within five percent on a stable link, and p95 command latency below
225 ms at 100 ms RTT with up to 10 ms one-way jitter. The probe models
ordered delay, not packet loss, congestion or a real international route,
and never runs in the ordinary gates.

#### 16.5.6 Private App Platform deployment

DigitalOcean App Platform's public ingress is HTTP with platform-managed
TLS, so the relay also speaks WebSocket for the same hosted hello, rooms,
commands, grants and acknowledgements; nothing about the simulation,
command schema, pacing or admission changes. Sources:
[App Platform routing](https://docs.digitalocean.com/products/app-platform/how-to/manage-internal-routing/),
[deployment limits](https://docs.digitalocean.com/products/app-platform/details/limits/)
and [WebSocket RFC 6455](https://www.rfc-editor.org/rfc/rfc6455).

The server adds `ListenHostedWebSocket(address string, config HostedConfig,
behindTLSProxy bool) (*HostedServer, error)` and the client
`DialHostedWebSocket(context.Context, url, room string, LocalHello,
HostedDialOptions) (*LocalClient, string, error)`; the native TCP APIs stay.
WebSocket messages carry the existing bounded wire frames, with no JSON,
command interpretation or new module dependency: RFC 6455 binary messages,
subprotocol `nanolathe-relay-v1`, path `/relay`, with masking,
fragmentation and control frames handled. An offered extension, such as the
permessage-deflate every browser offers, is declined by naming none in the
upgrade response (RFC 6455 §9.1), so frames with reserved bits set are still
rejected; the Origin header is not checked. A client's close frame is
answered with a close frame and ends that connection as a departure, never
as normal completion. Text and oversized messages are rejected, HTTP headers
bounded at 8 KiB and pending sockets bounded. Pings every 5 seconds keep a waiting room alive at the
proxy. `GET /healthz` reports readiness and no room information;
`--health-listen` serves it on a separate listener outside the connection
limit, so connections filling the relay's slots cannot fail the platform
health check.

Server flags `--websocket` and `--behind-tls-proxy` select HTTP behind the
platform's TLS terminator; public plaintext is permitted only with the
proxy flag, and standalone WebSocket TLS and loopback test mode remain. The
client accepts `wss://host/relay` and refuses credentials, fragments,
queries and other paths; `ws://` needs the numeric-loopback test switch.
Certificate verification is always on.

**Deployment.** `deploy/relay` is exported as a reproducible source snapshot
into the private `nanolathe-relay` repository with the upstream revision and
source-file digests; the engine tree stays the source of truth for protocol
code. It holds the MIT license, a Linux/amd64 multi-stage Dockerfile whose
static non-root image contains only the relay, the wire package and the
server command (no assets or simulation), CI and deployment instructions.
The App Platform app's settings live in the platform, not in the repository:
one always-running 512 MiB instance serving the relay on `:8080`, deployed
on every push to the relay repository's main branch. The image defaults to
`GOMEMLIMIT=384MiB` and `GOMAXPROCS=1`, which the app does not override, and
the app configures no HTTP health check. A restart or redeploy ends
active rooms; there is no cross-instance room lookup, database, autoscaling
or reconnect. The custom hostname `relay.nanolathe.gg` needs both its DNS
CNAME and its registration on the app before App Platform issues the
certificate.

### 16.6 First online lobby

The main menu's MULTI entry offers two choices, Create Game and Join Game,
with the server (default `relay.nanolathe.gg`) off the main path (§16.6.2).
Create opens a lobby at once; players join with its code; the host adjusts
the settings while everyone picks a team, side and colour; when every player
is ready the host starts the match. The lobby's rules:

- **Rooms hold up to 10 players, computers included.** A skirmish can
  start with 2 up to the most start positions any of the map's network
  schemas offers; Survival with 2–3 survivors, ignoring start positions
  (DESIGN_SURVIVAL). At least two players are human. Gameplay is Modern.
- **The host may add computer players** (§6.6), Classic or Modern, each
  with its own difficulty, side, team and colour. They are rows of the base
  configuration, hosted by seat 0, and every seat composes them after its
  present humans.
- **The host's settings stay open until Start.** The host chooses the game
  type (Skirmish or Survival), the map, Survival's pace and its no-air and
  no-naval options, and the standard skirmish options the configuration
  carries. Each change replaces the room's base configuration, which every
  seat adopts. The room starts from the host's current skirmish map.
- **The host's mod, mutators and unit restrictions are fixed when the room
  is created.** Every seat composes from the room's configuration, never
  from its own preferences. Field 12 carries the host's restrictions,
  mapped from `content.Restrictions` as DESIGN_MODS_MUTATORS §15.3 states,
  with nothing seeded. A different mod means a new room.
- **Teams and sides.** Each player picks their own team (none or 1–5) and
  side (any side the catalog defines) by clicking them while not ready.
  Teams are static: alliances are set from them at entry, teams of two or
  more share victory `[08 R-SKIR-01 §3]` `[05 R-SHARE-01 §1]`, declaration
  and sharing commands stay refused online, and teammates share sight
  (§6.7). A skirmish with every player on one team cannot start
  `[08 R-SKIR-01 §12]`. Survival has no team choice: the survivors are one
  side.
- **Colours.** Each player has one of the ten player colours (0–9, the logo
  colours) and no two present players share one. A seat that is created or
  joins takes the lowest colour no present seat holds, so a room's first
  players are 0, 1, 2 and so on, and a leaver's colour is free again. A
  player changes their own colour while not ready; a colour another present
  player holds cannot be taken, and asking for it changes nothing. In
  Survival the survivors keep their colours and the attacker takes the
  first colour no survivor holds, red when it is free (DESIGN_SURVIVAL §4.1).
- **Readiness follows the final configuration.** Any host setting, team,
  side or colour change, join or leave clears every seat's ready, because
  it changes the configuration every seat composes. Pressing Ready composes
  the final configuration — the base configuration plus the present seats
  in ascending seat order as slots, with their teams, sides and colours,
  then the base's computers —
  prepares it, runs the rehearsal (§16.7) and reports both digests.
- **The host draws the seed pair** with `crypto/rand` when it creates the
  room; the configuration digest covers it (§8.3).
- **Room codes are six characters** from the 21-symbol alphabet of §16.5.1.
  A lobby waits at most 30 minutes before Start, kept alive by the
  5-second WebSocket pings.
- **Leaving.** Before Start, a joiner who leaves frees its seat; the host
  leaving closes the room. After Start, a player whose result is final
  (defeated) may leave and the match continues; any other disconnect ends
  the match for everyone, since there is no reconnect yet (§11.2).
- **Survival online** keeps the single-player rules: the survivors share
  sight, radar and income, the waves hunt the survivor team, there is no
  victory, each seat gets the Survival result line, and the battle cannot
  be saved. It records no best scores.

#### 16.6.1 Relay lobby protocol

The relay treats configurations as opaque bytes and interprets no gameplay.

- **Create**: the hello carries an empty code, the `LocalHello` for seat 0,
  the flags, the room size (2–10) and the host's encoded base configuration
  (`session.EncodeMatchConfig`, at most 64 KiB). The welcome returns the
  new code and seat 0.
- **Describe**: a short-lived connection sends only a code and receives the
  base configuration and the room size, or a refusal for an unknown, full,
  closed or started room.
- **Join**: the hello carries the code and a `LocalHello` asking for any
  seat, and no configuration. The relay compares only the protocol and
  assigns the lowest free seat, returned in the welcome. Content and
  configuration are compared at Ready instead, because they depend on who
  is present.
- **Lobby state** (relay to every seat, after every change): the room size;
  per seat, present, ready, team, side and colour; and a mismatch bit when
  every present seat is ready but their digests differ.
- **Team** and **Side** (client to relay): the sender's team (0–5) or side,
  accepted only while that seat is not ready.
- **Colour** (client to relay): the sender's colour (0–9), accepted only
  while that seat is not ready and no other present seat holds it; otherwise
  the room keeps its state. A colour above 9 is a protocol error, as a team
  above 5 is. The creator's seat takes colour 0 and each joiner the lowest
  colour no present seat holds; a room never has more seats than colours.
- **Configuration** (host to relay, before Start): a replacement base
  configuration, at most 64 KiB, which the relay keeps for Describe and
  sends to every seat. Each joiner also receives the current configuration
  after its welcome.
- A host setting, team, side or colour change, join or leave clears every
  seat's ready.
- **Ready** (client to relay): the ready flag and, when set, the seat's
  configuration-identity digest (its final `MatchJoin` identity without the
  advisory build) and its rehearsal digest. **Start** (host only): accepted
  when at least two seats are present, all of them ready, with equal
  digests; otherwise the relay repeats the lobby state. **Started** then
  goes to every seat with its slot, the rank of its seat among the present
  seats, before the first grant; grants stamp commands with slots.
- **During the match** acknowledgements carry the battle-ended bit and the
  seat-final bit. The relay compares every playing seat's checksum at the
  same tick and runs the terminal handshake across all of them. A seat that
  has reported its result final may disconnect: the relay stops waiting for
  it. Any other disconnect after Start fails the room.
- Commands submitted before Start wait for the first grant. An
  acknowledgement before Start fails the room. Ready, Team, Side, Colour and
  Start after Start are ignored.
- A creator's hello may set the **auto-start** flag: the room starts as soon
  as it is full. `DialHosted` and `DialHostedWebSocket` create two-seat
  auto-start rooms and report ready at once with zero digests, for the
  command-line play test.

`internal/relay`'s lobby API: `DescribeHostedRoom(ctx, address, room,
options) (HostedRoomDescription, error)` with the configuration and size;
`OpenHostedLobby(ctx, address, room, hello, config, size, options)
(*HostedLobby, error)`; and on `*HostedLobby`, `Code()`, `Seat()`,
`State()` (the latest `HostedLobbyState` snapshot — size, per seat present,
ready, team, side and colour, the mismatch bit, the configuration version
and Started, with the local slot once started — never blocking),
`Configuration()` (the latest base configuration bytes), `SetTeam(team)`,
`SetSide(side)`, `SetColor(color)` (below `HostedColors`, 10),
`SetConfiguration(config)` (host only),
`SetReady(ready, identity, rehearsal)`, `Start()` (seat 0 only), `Battle()` (the battle client once
Started, which then owns the connection) and `Close()`. `address` is
`host:port` for TLS or a `wss://host/relay` URL, as for `--relay-address`.

The session's lobby helpers: `session.OnlineMatchSetup{Survival, MapName,
Seats []OnlineSeat{Team, Side, Color}, SimSeed, CRTSeed, SurvivalOptions,
SideCount}` with `Rows()` (the seats, plus the attacker in Survival);
`NewOnlineMatchRequest(setup, options, room)`, which names each seat
"Player n" with participant slot+1, its own side and colour and the ally
group of its team (survivors share group 2, and the attacker takes the first
colour no survivor holds, red when free), and refuses a colour above 9 or
one two seats share;
`OnlineMapCapacity(cat, map)`, the most start positions any network schema
offers, at most 10, which the lobby enforces; `OnlineMapSchema(fs, cat,
map, rows)`, the schema for the total row count; and `OnlineSides(cat)`, the
sides' names in index order.

#### 16.6.2 Client flow

- **MULTI** is enabled wherever the mounted content can play a skirmish, the
  browser build included; it greys only where Skirmish does, such as the
  browser's demo, which has no skirmish maps. The browser reaches the relay
  through its own WebSocket (§16.5.1). It opens a small chooser over the main menu: one
  sentence saying how online play works, Create Game and Join Game, and
  Cancel.
  Join Game asks for the room code in a popup (pasted or typed; case,
  spaces and dashes ignored), which stays open with the reason in plain
  words when the join is refused. A small Server control, off the main
  path, changes the relay (a bare host expands to `wss://host/relay`;
  `ws://` is the plaintext test opt-in, accepted only on a numeric loopback
  address).
- **Create** opens a 10-seat room with the host's current skirmish map,
  mod, mutators and restrictions. Only a mod installed from its archive can
  be hosted, because field 8 needs the archive digest. Field 10 holds only
  the mounted content's own Community table, and every seat uses the default
  builder options.
- **Join** describes the room and adopts its mod; if the room's mod is
  installed but not mounted, the client remounts it through the ordinary
  content reload, telling the player and saving it as the player's
  selection. A missing mod is reported by name and nothing is joined. Each
  later base configuration is adopted as it arrives; a map the player does
  not have is reported by name and keeps Ready disabled.
- **Lobby**: the room code shown large, with Copy where the host clipboard
  allows; a row per player with their team, side, colour and readiness, the
  local player's own team, side and colour changed by clicking while not
  ready; the host's settings (game type, Change map through the ordinary
  map picker, Survival's options and the standard skirmish options);
  Ready/Not ready; Start for the host, enabled when everyone present is
  ready, the digests agree and the player count fits the map or Survival's
  limit; Leave. Ready composes, prepares and rehearses off the game
  goroutine before reporting.
- **Colours**: the lobby shows the colour the relay gave each arrival, the
  lowest free one. A left click on the player's own colour steps it one on
  and a right click one back, skipping every colour another present player
  holds, as the single-player setup screen does. Only present players hold
  colours. The Survival attacker has no row and holds none: it takes the
  first colour no survivor holds when the battle is composed, so with the
  default 0, 1, 2 it is not red unless a survivor moves off red.
- **Computers**: the host adds one from the Add computer row after the
  last row, shown while one more fits with room left for two humans — at
  most eight in a skirmish and one in Survival. A new computer plays the
  Modern AI at the host's skirmish difficulty, with no team and the lowest
  free colour; the host edits its kind, difficulty, side, team and colour
  with the row's controls, and clicking its name steps Modern AI, Classic
  AI, removed. Each change replaces the base configuration and clears
  readiness like any host setting; guests see the rows read-only. The
  base's two placeholder humans take the two lowest colours no computer
  holds, so the base stores each computer's colour as the host chose it,
  and the lobby shows the colour composition will give it. Ready and Start
  count computers against the map's start positions, ten players and
  Survival's three survivors, and the one-team check includes them.
- **Started**: the client enters its prepared battle and drives it from the
  lobby's battle client. A defeated player sees their result and may leave
  while the others play on. When the battle ends or the connection fails,
  leaving returns to the chooser.

### 16.7 Rehearsal check

**Purpose.** Before a player presses Ready, the client proves that its
simulation computes what every other player's does. Identities (§8.2) can only
say that the declared inputs match; two builds with different simulation
code can report equal identities. The rehearsal runs the simulation itself.

**Contract.** `session.RehearsalDigest(inputs, config)` runs a short
deterministic battle composed from the room's same frozen inputs and
effective configuration, separate from the match session, and returns a
32-byte digest of its final state. A fixed, seat-symmetric script of
ordinary seat commands drives every human seat: commander moves, builds and
weapon fire, derived from the frozen catalog so that it exercises the
room's own content. It runs 900 ticks; in Survival it runs until the first
wave has finished spawning, at most the first wave's delay plus its warning
plus 300 ticks, so the wave director is exercised too. The script is defined by the code beside
`RehearsalDigest`; this document does not duplicate it. The final state
is digested under its own domain separation. The client sends the digest
with Ready (§16.6.1); the relay accepts Start only when every present seat
is ready with equal digests, and reports a mismatch otherwise. The 30-tick
checksum during play (§16.4) remains the final guard.

**Determinism.** The digest depends only on the frozen inputs and the
configuration. It is independent of which seat is local, of local
preferences and presentation, and of host time; no wall clock enters it.
Two honest clients of the same simulation always report the same digest.

**Bound.** It runs within about a second for a ten-player map and for
Survival up to its first wave, computers included, so readiness stays
prompt: composing, rehearsing and readying a two-human, two-computer
skirmish takes about half a second, and Survival with a computer survivor
about 0.8 s.

**What it covers.** Honest version mismatches: builds whose simulations
differ in a way the script reaches, which the identities cannot see. This
replaces any requirement for a stamped build: the build manifest is
advisory, and unstamped builds, `go run` included, may play online (§8.2,
§8.7).

**What it does not cover.** A cheat that does not diverge the simulation,
such as reading hidden state, is undetectable by any hash (§13). A cheat or
difference that diverges the simulation later is caught by the in-game
checksum, which stops the match.

## 17. Verification

**In force now.**

- **Two-human sessions.** Two admitted copies agree while selecting
  different presentation seats and receiving the same seat commands;
  movement, construction and combat from both seats, periodic checksum
  agreement, and commander loss with opposite local outcomes and identical
  per-seat results on both replicas. The final grant publishes the result
  the host needs without another tick, and both clients enter results from
  that frame, a battle menu open included.
- **Relays and drivers.** Real loopback connections carrying both seats'
  commands, a cancellation before its stream position is assigned, race
  tests, and a closed driver that cannot execute a buffered grant. Hosted
  socket contracts: TLS trust and refusal, identity mismatch, room
  isolation, full, expiry and close, bounded input and slow readers, a
  delayed checksum mismatch and final-grant handling; WebSocket framing and
  handshake rejection, health, lifecycle and connection bounds; a headless
  two-session match through WebSocket; a mixed version-5 and version-6
  room in which only version-6 seats receive progress reports, through a
  defeated seat's departure; exact traffic accounting; a js/wasm client
  playing both seats of a hosted match under Node's WebSocket against a
  native relay, with its deadlines, refusals and measured round trips; and
  a deterministic lockstep test modelling the window host at 20–240 Hz.
- **Lobby.** Relay tests for the lobby states, refusals, leaving, Describe,
  the protocol version, the readiness digests and the mismatch bit; session
  tests that field 12 is admitted online, applied to both seats' catalog
  clones, and changes identity; client tests for MULTI routing,
  configuration adoption and refusal text; and one headless two-client match
  through a real relay, started from a lobby, with a mutator and a
  restriction.
- **Rehearsal.** Its digest must be independent of the local seat, local
  preferences and host time, equal on both seats of one room, and
  different when the simulation differs in a way the script reaches
  (§16.7).
- **Codecs and identity.** Round-trip and fuzz tests for every seat-command
  kind, the configuration and the manifests; hostile inputs (forbidden
  cheat or rule-change commands, foreign actors, reused slots, excessive
  lengths and counts, invalid numbers) rejected with resources, RNG and
  command order unchanged; a changed unbuilt unit's script, model geometry
  or simulation animation metadata refused before start; each effective
  configuration field changing identity.
- **Checkpoints.** M3's completeness fixtures, two-ring fault localization
  and cost limits (§16.3.4, §16.3.80–§16.3.83).
- **Computer seats.** Two clients with real Modern computers on both teams
  and a Classic one agree every 30 ticks over 10,800 ticks, through the host
  seat's defeat (`TestOnlineModernComputersAgreeAcrossClientsRetail`); a
  Modern worker held late makes the simulation wait and changes nothing
  (`TestLateModernAIWorkerChangesNothingRetail`,
  `TestLateWorkerBatchLandsOnItsDeadline`); a two-client lobby match with a
  Modern and a Classic computer agrees through the relay; human-only rooms
  keep their pre-computer digests (§6.6).
- **Replays.** Single-player recordings from the window, with pumps of one
  to five ticks and commands at the paused-input boundary, and with Classic
  or asynchronous Modern computers, verify headless; both seats' recordings
  of an online match hold the same stream and verify; a changed checksum is
  reported at its tick (§10).
- **Fingerprint locks.** The fifteen original locks are unchanged on both
  architectures; the Strict effect-pool lock runs seed 5 at step 4,500
  (M1-C9). The locks run from amd64, arm64 and js/wasm builds on every
  retail gate run whose host can execute them (M1-C11), as does the online
  Modern AI lock (`TestOnlineModernAIFingerprintIsLocked`, which also locks
  the rehearsal digest with the real Modern AI). M2 moved locks only
  by the interface bits that left the hashed status words, plus
  single-player changes declared under M2-C7. Later milestones move no single-seat lock
  except where §16 says so, and each move carries its reason.
- **Latency probe** (opt-in): §16.5.4's targets.

**Later.**

- **The multi-seat harness** (M5). One process composes the same battle N
  times, each session believing a different seat is local, with pump sizes
  of one to five ticks and different presentation settings, some with a
  renderer and audio bound, feeds all one scripted stream and compares
  complete digests after every tick. Short synthetic battles run in
  `tools/check`; long retail battles in `tools/check-retail`.
- **Cross-architecture replays** (M4): the same replay files on
  darwin/arm64, linux/amd64 and windows/amd64 report identical canonical
  digests, on native hardware at every supported CPU level, with AI workers
  deliberately delayed.
- **Host kinds** (M4): the same stream through a windowed client and a
  headless host, past the tick at which the Strict effect pool fills, with
  matching digests.
- **Match settings** (M6): view-policy clamps through every zoom route,
  both renderers, the Community megamap, resize, fullscreen and reconnect,
  checked on actual captures.
- **Departures and desync** (M6): §11.1's removal and vote tests; a seeded
  one-field fault in one of three participants (two continue) and one of
  two (the battle ends); false digests and summaries move no honest seat.
- **Reconnect and audience isolation** (M7): connection loss before and
  after acceptance and echo, exactly-once admission, credential isolation,
  digest deadlines, snapshot bounds and spectator delays.
- **Impaired networks** (M6–M7): latency, jitter, stalls, loss and
  backpressure, with command latency, stall counts and queue bounds against
  budgets declared before acceptance.
- **Competitive policy** (M9): unequal latency, dishonest reports, pause
  abuse, forbidden targets, forged results and loss of the result
  authority.
- **Cost.** The simulation benchmark before and after M5's per-seat work,
  catch-up on large late battles, and a battle at the advertised seat and
  unit limits on the weakest supported hardware before a lobby offers them.

## 18. Research map

| Research | Used for |
|---|---|
| `[08 "Lockstep advancement"]`, `[08 "Soft pacing — no per-tick input barrier"]`, `[08 "No rollback or world snapshot resync"]`, `[08 "Full-world hash"]`, `[08 "Synchronization and integrity checks"]`, `[08 "Economy and integrity checks"]`, `[08 "Unit-sync ownership and body structure"]`, `[08 "DirectPlay transport"]`, `[04 R-MOV-03 §1]`, `[06 R-DMG-01 §9]`, `[05 R-SHARE-01 §1]`, `[01 §7.1]` | The retail model and why it is not reproduced (§3.1, §3.2) |
| `[08 R-ENTRY-01 §2]`, `[08 R-ENTRY-01 §3]`, `[08 R-ENTRY-01 §5]`, `[08 R-SKIR-01 §3]`, `[08 R-SKIR-01 §6]`, `[08 R-SKIR-01 §10]`, `[08 R-TRIG-01 §1]`, `[08 R-TRIG-01 §6]`, `[08 R-SESS-01 §1]`, `[08 R-OOS-01 §5]`, `[08 R-SAVE-02 §4]`, `[08 "Multiplayer saves"]`, `[08 "Bounded absence"]`, `[08 "Session end and reporting"]`, `[08 "Disconnect, resign, and peer loss"]`, `[08 "Unit-data negotiation and catalog retention"]`, `[05 R-SHARE-01 §3]`, `[05 R-SHARE-01 §5]`, `[05 R-SHARE-01 §6]`, `[05 R-SHARE-01 §7]`, `[05 R-SHARE-01 §9]`, `[05 R-WORK-01 §15]`, `[07 R-CAM-01 §2]`, `[07 R-CAM-01 §6]`, `[07 §5]`, `[07 §11]`, `[07 R-FE-01 §9]`, `[07 R-HUD-04 §1]`, `[01 R-PLAT-01 §2]`, `[03 R-VIS-01 §1]`, `[03 R-VIS-01 §7]`, `[03 §3.2]`, `[05 R-SHARE-01 §4]`, `[08 R-SKIR-01 §7]`, `[08 R-SKIR-01 §12]`, `[08 R-LEAVE-01 §1]`–`[08 R-LEAVE-01 §10]` | The player-visible rules an online battle keeps (§3.3, §6.4, §7.1, §11) |
| `[03 R-SENSOR-01]`, `[03 R-VIS-01 §4]`, `[08 R-SESS-01 §3]`, `[08 R-ENTRY-01 §7]`, `[04 R-COB-03 §6]`, `[03 R-STRIP-01 §1]`, `[04 R-P0-08-B §1]`, `[08 R-AI-01 §7]`, `[06 §3.1]`, `[06 §12.1]`, `[05 R-ECO-01 §1]`, `[05 R-FEAT-01 §8]`, `[04 R-COLL-01 §4]`, `[07 R-CAM-01 §12]` | Per-viewer and per-owner work (§6) |
| `[01 §4.3]`, `[01 §4.4]`, `[01 R-PLAT-02 §5]`, `[01 R-PLAT-02 §7]`, `[03 R-COMP-02 §2]` | The clock, the pump and the executor tail (§4) |
| `[fmt tad]` | The community recorder's files, which Nanolathe replays do not read (§3.3) |
| `[01 R-DET-01 §3]`, `[01 R-DET-01 §7]`, `[03 §1]`, `[06 R-WFX-01 §1]`, `[04 R-COB-04 §3]`, `[03 R-FX-01 §3]`, `[06 §3.2]`, `[08 R-CAMP-01 §9]` | One simulation on every host: the distance routine, the effect pool and what it gates, an ungated mode bit, and a kind-3 branch with a draw (§5.3, §6.4, §7.1) |

## 19. Open questions

Unsettled retail behaviour becomes a `TODO(question)` at its code site and
stays in the owning research document's Unknown list. The items here are
engineering measurements and Nanolathe policy choices still open, and two
settled items that other documents cite by ID. None is permission to invent
a retail rule.

| ID | Question | What settles it |
|---|---|---|
| O10 | Checkpoint cost on every supported platform, and exact restore | M3's native cross-platform comparison; M8's exact-restore and continuation evidence. |
| O11 | Rejoin time on the largest and latest battles, and whether snapshot recovery must precede public rejoin | Live-target catch-up measurements before M7; advance M8 if the window cannot be met. |
| O12 | Kind-3 branches beyond those §6.4 lists | Each implementation unit audits the code it touches against the research. |
| O13 | Competitive scheduling, input and view age limits, and the acceptable latency advantage | Compare arrival scheduling with a shared-horizon candidate under unequal RTT, jitter and malicious reports before M9. |
| O14 | Fair admission of delayed direct targets, radar contacts and area orders | Research the existing contracts, specify bounded observation evidence and validate it against the scheduler (§7.2). |
| O15 | Ranked integrity level: full-state lockstep with verified results, or filtered state delivery | An explicit product decision before advertising ranked guarantees (§12.6). |
| O16 | Whether competitive view policy also equalizes world viewport coverage | Accept resolution and aspect differences, or define and test a common world-area bound (§8.4). |
| O17 | Start, pacing and reconnect state tables, including the lag bound that moves a connected seat with no progress into the drop state | M6 owns the start and pacing tables; M7 the reconnect tables and their state-machine tests. |
| O18 | A manual retail observation confirming the distance routine `[01 R-DET-01 §7]` (a reach test at an offset such as 20 by 99, whose truncated distance is 100 and exact distance 101), and the retail forms of the other library calls wherever a stored value could change `[01 R-DET-01 §3]` | Manual retail observation; research. |
| O21 | Whether restarting Modern controllers from observation at a snapshot tick is acceptable play (§9.1, Q21) | Play-test computer seats across forced restarts, compared with a save and load. |
| O22 | *Settled.* Where the engine took an exact integer root or compared squares and retail calls its distance routine on whole numbers: leash, work and construction reach and air-order distances use the portable kernel; ordinary guard arrival and unit reclaim remain squared tests; the dogfight reads a signed high word before comparing with 160 `[04 R-STANCE-01 §4]` `[04 R-AIR-01 §8]` `[05 R-WORK-01 §2]`. The correction is a shared retail mechanic, so it applies to Modern too (DESIGN_MOVEMENT_PATH §3.4). | — |
| O23 | *Decided:* a Strict resignation is retail's menu quit as the other machines see it — the leaver's units, and its hosted computers' units, are deleted silently, with no `Killed` script, explosion, wreck or credit, a unit already at non-positive health keeps the researched receiver path, and no self-damage applies; the records are then cleared as a voted removal clears them (§11.1) `[08 R-LEAVE-01 §7]`. Still open as research: the slot order in which retail peers empty the leaver's records. | A trace of the transport's player-destroyed notices when the leaver's session closes; until then the protocol uses the voted removal's order (§11.1). |
