# Design — Runtime and determinism

Everything the engine does that must happen the same way twice. This document
owns the 30 Hz budget, the two random streams, fixed-point arithmetic, the
fixed pools, the composition of a battle, the twelve-phase authoritative tick
and its publication boundary, the committed frame, build identity, and the boot
order the two commands share.

Packages: `internal/clock`, `internal/sim/rng`, `internal/sim/numeric`,
`internal/pool`, `internal/session` (composition, the tick, publication),
`internal/frame` (the published side), `internal/version`, `internal/parity`,
and the boot paths in `cmd/nanolathe` and `cmd/nanolathe-headless`.

Related documents: [ARCHITECTURE.md](ARCHITECTURE.md) for the package map, the
dependency graph and the citation routing that makes a bare `[Cn]` in
`internal/session` or `internal/clock` resolve to §3.2 below;
[INVARIANTS.md](INVARIANTS.md) for the rules every diff is reviewed against —
I1, I2, I3, I4, I5, I6 and I7 are this document's subject matter seen from the
review side; [SPEC_CONFLICTS.md](SPEC_CONFLICTS.md) for places the reference
install disproves the written contract.

## 1. Purpose and boundary

A run of Nanolathe is a sequence of authoritative sub-ticks. This area answers
four questions and nothing else.

* **How many sub-ticks run now?** `internal/clock` turns a scaled host time
  into a runnable count of `0..5` `[01 §4.1]` `[01 §4.2]`. The simulation never
  reads a wall clock; the caller supplies the scaled integer.
* **What happens inside one?** `internal/session` increments the global tick
  and runs the twelve phases of `[01 §4.4]` in order, then the sharing and
  result tail, then publishes.
* **Where do the numbers come from?** Two random streams and one fixed-point
  vocabulary. Call order on the streams is behavior, not an implementation
  detail `[01 §7.1]` `[01 §7.2]` [I4]; world state is 16.16 with `uint16`
  angles and retail's narrowing rules `[01 §8]` [I2] [I3].
* **What can presentation see?** One committed frame per completed sub-tick,
  sampled at the committed tick with no interpolation `[03 §2.4]` [I6].

The boundary out of this area is `*frame.Frame`. The boundary in is a compiled
catalog, a terrain, a player configuration and a pair of seeds. Nothing else
crosses: no sim package reads input, a camera, a device or the host clock, and
no presentation package writes simulation state or draws from a session stream.

What this area does **not** own. The *content* of each phase belongs to the
package that does the work — units and orders and COB
(DESIGN_UNITS_ORDERS_COB), movement and paths (DESIGN_MOVEMENT_PATH), the
ledger and construction (DESIGN_ECONOMY_CONSTRUCTION), weapons and projectiles
(DESIGN_WEAPONS_PROJECTILES), visibility and wind (DESIGN_WORLD_VISIBILITY).
The session's *state machine*, battle entry, mission and skirmish setup,
results and saves are DESIGN_SESSIONS_AI_SAVE; this document takes them as
given and describes only where a battle's services come from and when the tick
may run. Consumption of the committed frame — drawing it — is
DESIGN_PRESENTATION_CLIENT.

## 2. Packages and key types

### 2.1 `internal/clock` — the 30 Hz budget

One `State` holds the whole scheduler block: the scaled anchor, the last signed
delta, a `float32` fractional carry, the global tick, the requested and active
speed words, the pause bit, and three fields that exist only so the 28-byte
save box round-trips (the clamped pending count, the speed-slew counter, the
flag word).

`ScaledNow(tickCount)` is the one conversion from host milliseconds to the
engine's timebase, `floor(ms × 30 / 1000)` `[01 §4.1]`. It lives here so no
other package invents its own.

`AdvanceSP(scaledNow)` is the single-player budget. It forms
`raw = float64(delta) × activeSpeed×0.1 + float64(carry)`, floors that binary64,
narrows through the signed-64/low-word helper, stores `raw − floor(raw)` back
as `float32`, and clamps the retained signed integer to `0..5`
`[01 §4.2]`. Excess work is dropped, not queued. A wrapped host counter gives a
negative delta, which clamps to zero rather than being repaired as elapsed
time. The `float64` product and the `float32` carry are the I2 allowlist row
for this package and are not an approximation of something integral: retail
computes the product in double precision and stores a single-precision
remainder.

The speed pair is clamped `1..20` and adapted by a hysteresis counter that
increments on a capped sample (a pre-clamp truncation of six or more) and
decrements on a normal one; sustained capping steps the active speed down,
sustained normality steps it back toward the request `[01 §4.3]`. The
asymmetry — an active value *above* the request is left alone — is retail's.

Pause is a branch, not a multiplier. The single-player path short-circuits
before the budget is evaluated at all, so the anchor, delta and carry all
stall and unpausing yields exactly one capped burst of at most five sub-ticks.
`AdvanceMP` is the other half of that asymmetry: it evaluates the budget every
iteration, advances the anchor even while paused, and discards the integer
while keeping the carry, so no burst occurs. It is unreachable today and is
kept so the asymmetry is not silently lost.

`BeginSubTick` is the only writer of `GlobalTick`, and increments it before
phase 1 `[01 §4.4]`. `SaveBox`/`LoadBoxChecked` are the 28-byte scheduler image
`[08 "Scheduler and random state in saves"]`; loading validates into a
temporary value first, so a malformed box never partially applies. Neither
random stream is in that box.

### 2.2 `internal/sim/rng` — two streams, and call order

There are exactly two streams and they are not interchangeable.

**Simulation** is Park–Miller by Schrage: multiplier `16807`, quotient
`127773`, modulus `0x7fffffff`, with the 32-bit wrap kept so a restored state
outside the generator's ordinary invariant behaves as retail's arithmetic does
`[01 §7.1]`. `NewSimulation` applies the battle-entry seed transform
`(seed ^ 0x66e29572) | 1`. `Uint32n` is one draw plus a modulo for every bound;
a bound below two — tested as a **signed** 32-bit compare, so a bound with its
top bit set counts as below two — returns zero **without advancing**. There is
deliberately no chunk-concatenation path here: Park–Miller yields 31 bits, so
adding one would make the wind heading's `simRand(0x10000)` cost two draws and
return a different value `[01 §7.3]`.

**CRT** is the MSVCRT recurrence `state = state × 214013 + 2531011` with the
result `(state >> 16) & 0x7fff`, fifteen bits per draw `[01 §7.2]`. Its bounded
sampler consumes exactly one draw at every bound: the widening loop shifts the
mask and the result left fifteen bits and ORs `0x7fff` into both, so above
32,767 the sample's low fifteen bits are all ones and only its top bits carry
entropy. That is the behavior being cloned, not a transcription slip [I11].
There is no `bound < 2` guard on this stream — retail's CRT sites are raw
inline `rand() % n` expressions — so a bound of one still spends its draw.

Nanolathe owns both streams per session. `Session.SimRNG` and `Session.CrtRNG`
return that session's state; `SeedSessionRNG` installs a fresh pair and resets
the global tick. This isolates sessions for reproducible runs. Retail resets
the simulation stream at battle entry, but preserves the main-thread CRT
history; the entry seed belongs to a separate, temporary loading-thread block
`[01 R-CORE-02]` `[01 R-PLAT-01 §7]`. Nanolathe's approved lifetime and
presentation isolation divergence is defined once in §5. `rng.Global` exists
for process bootstrap only and is nil until seeded.

The owners are intentionally ordered by entry and runtime role:

| Role | Nanolathe owner and lifetime | Retail relationship |
|---|---|---|
| battle simulation | `Session.SimRNG`, freshly installed from the explicit battle seed pair | same battle-entry reset role `[01 R-CORE-02]` |
| skirmish setup shuffle | disposable CRT created from the explicit battle CRT seed and discarded after placement | same temporary loading-owner shape; retail seeds the loading thread's CRT separately `[01 R-CORE-02]` `[01 R-PLAT-01 §7]` |
| authoritative battle CRT | `Session.CrtRNG`, freshly installed from the explicit battle seed pair and retained for that session | retail instead retains the main thread's process-lifetime CRT `[01 R-PLAT-01 §7]` |
| briefing animation | private front-end CRT; its final state is never a battle seed | retail briefing draws advance the main-thread CRT carried into battle `[01 R-CORE-02]` |
| audio queue and music | each copies the supplied front-end or session CRT state when bound and advances its own history | retail variants and track choice draw the live main-thread CRT `[01 R-DET-01 §5]` |
| segmented-projectile presentation | client copy taken at battle binding | retail draws the live main-thread CRT `[01 R-DET-01 §5]` |
| effect strips, including nanolathe particles | phase-11 session state; draws the retained session CRT and publishes completed particles | retail's tick-side effect draws use the main-thread CRT `[01 R-DET-01 §4]` `[03 R-STRIP-01 §3]` |
| battle restore | load supplies a fresh explicit pair; the save restores neither stream | retail reseeds simulation while main-thread CRT history continues `[08 "Scheduler and random state in saves"]` `[01 R-CORE-02]` |

Which stream draws, and when, is a per-phase fact `[01 R-DET-01 §4]`
`[01 R-DET-01 §5]`. Gameplay draws from the simulation stream. The retained
session CRT serves the meteor scheduler `[06 §6.5]`, the camera shake driver
`[03 §5.6]`, the wind next-change interval `[01 §7.3]` and the effect strips'
own particle draws `[03 R-STRIP-01 §3]`. Audio variant selection `[03 §8.3]`
and segmented-projectile presentation use their private copies. In retail every tick-side
CRT draw continues the stream seeded at process start; battle entry's own CRT
reseed ran on retail's loading thread and its block was discarded with that
thread, so nothing reseeds the tick-side CRT stream `[01 R-PLAT-01 §7]`
`[08 R-ENTRY-01 §2]`. A consumer draws the documented number of times even when
it discards the result [I4]. Nanolathe's client and audio owners copy stream
state at their binding seams (§3.3, DET-01); briefing has a separate front-end
stream, and skirmish setup discards its local stream before the tick begins.

### 2.3 `internal/sim/numeric` — fixed point, angles, trig

`Fixed` is signed 16.16 backed by `int64`; one map pixel is 65,536 world units
`[03 §2.1]`. `Angle` is `uint16`, 65,536 per circle `[04 §5.1]`.

Three narrowing rules coexist and are not interchangeable [I3]. `Int` truncates
toward zero, because retail routes float→integer through a helper that sets
round-control to truncate for the store `[01 §8]` `[01 R-DET-01 §1]`. `Floor`
and the cell/tile conversions in `internal/world` floor, because retail uses an
arithmetic shift with a sign correction — the two disagree on every negative
value with a fraction, which is the map's west and north edges. `Mul` forms the
product at full width and shifts down, so it floors; `Div` is a divide, not a
shift, so it truncates toward zero. That asymmetry is the hardware's.

`TruncateFloat64ToLow32` owns the shared runtime/parser conversion: signed
64-bit truncation followed by the signed low word, with zero for non-finite
or out-of-range results [01 R-DET-01 §1]. `TruncateFloat32ToLow32` only widens
an already rounded single-precision input into that same operation. It does
not change any expression's stored precision. Runtime ports, resource debits
and refunds, AI scores/centres, capture budgets, flight and follower distances,
mission coordinates/times, strip spans and result/HUD totals retain the low
word before widening into Go containers or performing subsequent division.
Round-to-nearest bearings, vector normalization, integer cell shifts and the
modern presentation zoom remain separate operations. The bounded RT-01 audit
also routes ballistic-pitch stores, area-distance roots, direct-pitch and
ballistic-distance roots, HUD health percentage arithmetic, and type-7 beam
distance/count operands through their documented integer boundaries. Bounded
positive resurrection and reclaim terms need no conversion change. The authored
integer caller audit routes GUI header/version fields and self-destruct seed
text through the TDF decimal accessor before their smaller stores; the
synthetic placement codec retains that same boundary for InitialGroup. Build
lists request consecutive numbered keys until the first absent key, removing
the unsupported host-width highest-suffix scan [02 R-CAT-01 §5]. Host tool
arguments, generated interface names, explicit composition selectors and
Nanolathe save identifiers remain their own input formats. The permissive
InitialMission integer scanner remains the explicit Unknown described below;
this conversion audit does not establish its scanning grammar. Ordinary
victory/defeat flags, timers and AnyUnit boundaries use the same authored
decimal accessor: a present empty or malformed field is zero, while only a
missing field is absent; `%i` applies exclusively to the separate typed
argument scan families [08 R-TRIG-01 §2].

`Sin` and `Cos` are *the* simulation trig: one 512-entry table where entry `i`
is `round(8192 · sin(i·2π/512))`, indexed as `((angle + 32) >> 7) & 511` with
cosine reading the same table a quarter turn ahead. The pre-add of 32 puts the
entry boundaries a quarter step early and is part of the contract, not a
rounding convenience. `MulRound` is the shared component routine,
`(entry × magnitude + 0x1000) >> 13` as a 64-bit product with an arithmetic
shift — it floors after the half is added, so it is neither symmetric
round-to-nearest nor truncation `[04 §5.1]` `[04 R-MOV-01 §4]`. The renderer's
model trig is separate and floating point `[03 §2.4]`; the two implementations
are never shared. `AngleFromAtan2` is the bearing helper: a `float64` transient
scaled by `65536/2π` and narrowed under round-to-nearest-even before the
`uint16` store, which is one of the two round-to-nearest sites retail has
`[01 R-DET-01 §2]`.

The remaining presentation audit uses the same conversion for HUD fill and
percentage stores, nanoframe bands, shade-row selection and segmented-beam
integer operands. Nearest-angle and nearest-point transforms remain separate;
modern zoom/cursor coordinates and host audio resampling remain host operations.
The map tidal value is a single-precision scalar, not an integer conversion:
`Terrain.Tidal` retains that store directly through economy and AI consumers
[03 R-TERR-01 §6][05 R-PROD-01 §4]. The old fixed-point round trip lost small
authored fractions and has been removed. Mission command integer scan grammar
and overflow remain a separate unresolved input audit, not permission to apply
the floating-point helper to `%d` fields.

### 2.4 `internal/pool` — fixed capacity, slot 0 null

`Handle` is a `uint16` slot index. Slot 0 is the null sentinel and is never
allocated. There are no generation bits: a stale handle whose slot has been
freed and reused aliases the new occupant, and that is retail `[01 §6.1]`
`[04 §2.3]` `[06 §5.1]` [I5].

`Units` is sliced per player. Battle entry allocates `limit × 10 + 1` records
in ten per-player slices of `limit` each; the slice order is a validated
permutation of the ten logical slots, which is the identity order for every
session kind this engine builds. Allocation scans the *owning* player's slice
for the lowest free slot and reuses freed slots immediately, so a slice-full
failure is reported even when other players have room. The per-definition limit
gate is checked before the scan. Freeing clears the occupancy identity and the
alive flag and deliberately retains the stamped slot number, which is what a
forced-slot restore addresses. Allocation and free make zero random draws.

`CobThreads` is the eight-bit per-unit thread mask; allocation picks the lowest
clear bit `[01 §6.1]`.

`Projectiles` is the 300-record pool and the sole allocation, dead and count
authority for projectiles. `Reserve` appends at the active-span tail and never
fills holes, so it fails at capacity even when earlier records are dead;
`MarkDead` sets a flag without decrementing the count; `Compact` is stable,
reads the *current* count so clones appended during the phase are included,
copies survivors down preserving order, publishes the reduced count only after
the scan, and repairs the follow-camera link — clearing it to null when the
followed record was removed `[01 §6.2]` `[06 §5.1]` `[06 §5.2]`.

Retail's record byte sizes (280 for a unit, 107 for a projectile, 164 for a COB
thread, 86 for an order node) are identity and behavioral limits, not a Go
memory layout [I13].

### 2.5 `internal/session` — composition, the tick, publication

**Composition.** A battle is a compiled catalog plus a terrain plus a player
configuration, assembled into one `Session` that holds the clock, the two
streams, and a fixed set of service objects — the unit world, visibility, the
path scheduler, the economy ledger, construction, features, movement, combat,
ten optional computer-player managers indexed by slot, the mission, and the
frame buffer. The two production constructors are the skirmish one and the
mission one; both validate their configuration, compile or accept one immutable
catalog, load terrain and apply the mission schema, size the sliced unit pool
from the session's unit limit, and seed both streams **before** any battle-setup
draw so the slot shuffle and commander placement start from a known state
`[01 R-CORE-02]` `[08 R-ENTRY-01 §2]`. Battle entry, start positions, saves and
results are DESIGN_SESSIONS_AI_SAVE; what matters here is that there is one
composition, shared by the windowed and the displayless entry, and that a third
must not appear.

**The pump.** `Session.Step(scaledNow)` runs authoritative ticks only in the
battle state; in any other state it drives one state-machine dispatch and
returns. In battle it calls `AdvanceSP`, and for each of the `0..5` runnable
sub-ticks it increments the global tick, runs one complete sub-tick, and
publishes. A transition out of battle mid-batch (an abort, or a result latching)
breaks the loop. After the last runnable sub-tick the executor tail runs once
per pump.

**The twelve phases.** One named method per phase, one call site and one
implementation each, in `[01 §4.4]` order. This is the table PLAN_03 kept as a
registration map, restated against the code:

| Phase | `[01 §4.4]` name | What runs |
|---:|---|---|
| 1 | network drain | the single-player form: due human commands are applied |
| 2 | per-unit sweep | players `0..9`, slots ascending within each slice; per unit the micro-order of `[04 §5.4]`: pre-update, weapon service, exactly one COB drain, the two order-queue pumps, build work, movement integration, then slot-end death finalization. The owner's player-row gate decides whether a slot is visited at all and whether its work block runs `[04 R-MOV-03 §10]` |
| 3 | projectiles | interceptor guidance, the integration and collision pass over the count captured at entry, then detonation and the hostile-damage sweep; the pool compactor runs at this phase's tail `[06 §5]` |
| 4 | effects and feature motion | the effect pool's integration and compaction, and features' motion half |
| 5 | orders, path, economy, occupancy **and visibility stamps** | the path scheduler first, then per player `0..9` ascending: the ledger's per-player tick with the computer player's tick supplied as the before-deadline callback, then that player's dirty-checked visibility stamp sweep. The sensor and deadline pass runs inside the *local* viewing player's iteration, after that player's stamp sweep `[01 R-CORE-01 §4.4.1]` `[03 R-SENSOR-01]` |
| 6 | feature lifecycle | burning, reclaim and death processing |
| 7 | sequence advancement | the presentation-owned model-texture cursor service; no simulation state advances here `[03 R-CRD-005 §1]` |
| 8 | wind change | the complete scheduled redraw: strict deadline gate, one CRT interval draw, the simulation strength draw, the simulation heading draw only when the strength is nonzero, then the vectors, the published scalar and the change flag `[01 §7.3]` |
| 9 | meteor shower | the strike scheduler: four CRT scheduling draws on every due evaluation even when the storm is disabled, then a radius and an angle draw per hit; zero simulation draws `[06 §6.5]` |
| 10 | camera and shake | the authoritative shake driver, exactly two CRT draws per active tick, publishing its offset on the committed frame; the scroll toward the camera target stays presentation-owned |
| 11 | ten object-list sweeps | the ten effect strips: strips ascending, objects in insertion order, the removal verdict evaluated *before* the update, a positive verdict destroying with stable left compaction `[03 R-STRIP-01 §2]` |
| 12 | cadence flip | the radar blink countdown: a positive countdown decrements, zero reloads to seven and toggles the phase bit `[01 R-CORE-03]` |

Phase 5 is shared, and the order inside it matters. Both the settlement pass and
the computer player hang off the same per-player loop. The session owns that
loop and passes the manager's tick as a callback into the ledger's per-player
entry, which keeps the planner after the per-tick helpers and before the
deadline compare without an economy↔planner import cycle. A slot the gate skips
invokes neither the callback nor a deadline advance
`[05 "Authoritative settlement order"]`.

**The tail.** After phase 12, inside the same sub-tick, the sharing pass runs
(the transport tail of `[01 §4.4]`) and then the per-sub-tick result work.
Publication follows, outside the phase registry. Once per pump, including a
zero-runnable pump, the executor tail runs three stages: the three empty
barrier routines; the in-battle message-ring retire; and temporary-sight expiry
with its own compaction. There is no separate pending-list stage
`[01 R-PLAT-02 §7]` `[01 R-PLAT-02 §8]`. The message retire can remove at most
one overdue line on a zero-runnable pump; sight expiry uses the unchanged
global tick and its strict comparison. Optional diagnostic tracing records
the tail on every pump, subject to its configured bound.

**Temporary sight.** A unit a player loses keeps revealing its sight radius for
sixty ticks. The records are twenty fixed slots carrying their own stored
coverage tile pair and coverage byte, appended by the central death handler in
every session kind, removed through the visibility decrement publisher when
their expiry tick falls strictly below the current tick, then compacted in
place. The pass makes no draw on either stream `[08 R-SESS-01 §3]`
`[01 R-PLAT-02 §5]`.

**Publication.** One `frame.Buffer.Publish` per completed sub-tick, after
sharing and result work. The session copies unit, projectile, feature, effect
and strip views, the order queues and build progress, the economy and player
rows, the visibility and fog channels, the radar picture, the selection and
command page, the shake offset, the result, and the scheduler's two speed words
plus the unit limit. Nothing presents between phases.

### 2.6 `internal/frame` — the committed tick-end copy

`Buffer` is two `Frame` slots and an atomic committed index. `BeginWrite`
returns and resets the slot that is not committed; `Publish(tick)` requires
strictly increasing ticks and makes the slot readable; `Current` returns the
committed frame or nil before the first publication. There is no third slot, no
clone on publish, no retained previous frame, no `alpha`, and no interpolation
— retail's draw path samples the accumulators exactly as committed at the
current tick `[03 §2.4]` [I6]. A five-tick catch-up burst publishes five
committed frames in order; a render frame that ran zero sub-ticks samples the
last committed tick.

The one asymmetry inside the buffer is events. The two slots carry current
*state*, which the next publication legitimately supersedes; an event is a
one-shot occurrence, so committed events join a retained queue in raise order
at publication and survive until the presentation side drains them. A
publication cadence faster than the drain therefore supersedes state but never
discards an occurrence `[03 R-AUD-01 §7]`.

The reader lifetime contract is explicit: the caller must finish reading
`Current` before the next `BeginWrite`, because that call reuses the other slot.
The package adds no speculative locking.

### 2.7 `internal/version` — build identity

`Profile` is a name, not a content hash. Retail's scheduler block stores a
profile identity and recomputes content identity separately, so content
identity lives in the VFS manifest hash and the catalog hash and never here
`[01 §3.1]`.

### 2.8 Boot order and the command line

Retail's startup order is diagnostics, singleton, CRT seed, command line,
window, timebase, content mount, language, registry, then the pump `[01 §2.1]`.
Nanolathe keeps the ordering and skips the platform specifics: parse the
command line, print the profile identity, mount the install, prove the mount
produced game data, then hand the process to a run path. Content paths are
relative to an explicit root; retail sets the process working directory instead,
which is a library-hostile equivalent with no behavioral difference. The window
is opened by the run path rather than before the mount, because there is no
window-owned display context the mount depends on.

`--root` defaults to `$NANOLATHE_TA_ROOT` and then to `~/TotalAnnihilation`.
The mount is proved by two required products, `gamedata/moveinfo.tdf` and
`gamedata/sidedata.tdf`; a miss produces the standard diagnostic naming the
logical path, the providers searched and what was expected, never a panic.
`--remaster` mounts an art override above every retail tier.

Three run paths share that boot: the windowed shell (the authored front end, or
a direct battle with `--map`), `--shot` (compose one frame after a number of
authoritative ticks and exit without opening a window), and `--headless`. The
separate `nanolathe-headless` command is the fourth entry point but the same
composition — `-map` or `-mission`, `-difficulty`, `-seed`, `-ticks`,
`-report`, plus two host-side pprof flags that never reach the session.

Seeds are the one host handle on determinism. Without `--seed` the two streams
take separate time sources, mirroring retail's split so an unseeded run does not
accidentally couple them. With `--seed N` both are fixed: the simulation stream
takes the battle-entry transform of `N` and the CRT stream takes `uint32(N)`.
That coupling is a deliberate debug divergence, and it is what makes a seeded
run reproducible even though wind, meteors and shake spend CRT draws.

## 3. Contracts

Two numbered sets meet here. The boot set keeps the bootstrap plan's numbers
under a `B` prefix so it cannot collide with the runtime set; a comment reading
`PLAN_00 C6` is **B6** here. The runtime set keeps its own numbers, because
`internal/clock` and `internal/session` cite them bare as `Cn`.

### 3.1 Boot — B1…B6

**B1 — one module.** The module path is `github.com/nanolathe-gg/nanolathe` and
`vfs/` and `formats/` compile under it with no remnant of an earlier module.

**B2 — one content root.** `--root` defaults to `$NANOLATHE_TA_ROOT`, then to
`~/TotalAnnihilation`. Every content lookup is relative to that root, mirroring
retail's working-directory rule `[02 §1]`.

**B3 — one diagnostic shape.** Missing content produces
`nanolathe: <what failed>: logical path <path>, providers searched [<a>, <b>],
expected <product>`, never a panic and never a bare not-found error.

**B4 — two seeds, two sources.** Absent an override the simulation stream is
seeded at battle entry as `(t ^ 0x66e29572) | 1` from the performance counter
and the CRT stream is seeded separately at process start `[01 §2.1]`
`[01 §7.1]` `[01 §7.2]`. `--seed N` fixes both as a debug divergence, and the
pair reaches the session before any setup work.

**B5 — validate before dispatch.** Startup validates the selected install and
its content before handing the process to a run path. The windowed entry,
`--shot`, `--headless` and `nanolathe-headless` all take the same validated
content and the same battle composition; a second composition is the thing that
must not appear.

**B6 — archives at the root.** The install root holds the archives directly and
the loose `gamedata/` directory of a real install is empty, so the probe is for
mounted products, never for `gamedata/` on disk (SC1, SC2).

### 3.2 Runtime core — C1…C19

**C1 — the budget.** `raw = double(delta) × effectiveSpeed + double(carry)`;
floor the saved binary64, then narrow to the signed low word; store
`raw − floor(raw)` as `float32`; clamp the retained integer to
`0..5`. Excess is dropped, not queued `[01 §4.2]`.

**C2 — a wrapped counter clamps.** A negative delta from a wrapped host counter
clamps to zero and is not repaired as elapsed time `[01 §4.2]`.

**C3 — speed.** The multiplier is `activeSpeed × 0.1` with `activeSpeed`
clamped `1..20`. The lag-throttle expression `max(0.01, (3600 − min(lag, 3600))
/ 2700)` is implemented and unreachable, and is kept rather than deleted
`[01 §4.2]`.

**C4 — pause is a branch.** The single-player path short-circuits the budget and
stalls the anchor, so unpause yields one capped burst of at most five. The
multiplayer path runs the budget, discards the integer and keeps the carry.
Both exist `[01 §4.3]`.

**C5 — the 100 ms gate is not the budget.** It drives only the audio and media
keepalive `[01 §4.3]`.

**C6 — the tick increments first.** The global tick increments before phase 1
of each sub-tick, and `clock.BeginSubTick` is its only writer `[01 §4.4]`.

**C7 — the phase order is the twelve entries of §2.5, every sub-tick.** One
named method per phase, one call site and one implementation each, in one
place. Registration order within a phase is part of behavior; phase 2's
per-unit micro-order and phase 5's planner-before-deadline position are part of
the contract `[01 §4.4]` `[04 §5.4]` [I7].

**C8 — Park–Miller.** `16807 / 127773 / 2836 / 0x7fffffff` via Schrage. Every
bound is one draw plus a modulo. A bound below two, compared as a signed 32-bit
value, returns zero **without advancing**. There is no chunking on this stream
`[01 §7.1]`.

**C9 — the CRT stream.** `state = state × 214013 + 2531011`, result
`(state >> 16) & 32767`. Bounds above 32,767 go through the widening loop,
which still spends exactly one draw. There is no `bound < 2` guard: a bound of
one consumes its draw `[01 §7.2]`.

**C10 — seeding.** Retail seeds the simulation stream at battle entry from its
performance-counter source; the main-thread CRT is seeded at process startup
and continues across battle entry. Setup seeds a separate loading-thread CRT
block, discarded with that thread `[01 §7.1]` `[01 R-CORE-02]`
`[01 R-PLAT-01 §7]`. Nanolathe's composition supplies a fresh pair per session;
`--seed N` fixes both (B4). Briefing entry uses that same battle seed source,
never the briefing stream's final state. Skirmish setup seeds a disposable CRT
from the explicit battle CRT seed and discards it before ticking. The retained
CRT lifetime and private presentation histories are the §5 divergence, not
reseed parity claims.

**C11 — wind is phase 8 alone.** The complete scheduled redraw is one phase: a
strict deadline gate, then one CRT interval draw `((crt × 10) / 0x8000 + 5) ×
30`, then `simRand(maxWind − minWind) + minWind`, then `simRand(0x10000)`
truncated to sixteen bits **only when the strength is nonzero**, then the
vectors, the clamped published scalar and the one-tick change flag. Battle entry
zeroes the deadline and consumes no wind draw — the zeroed deadline fails the
strict gate while the global tick is still zero — so the first chain fires in
sub-tick 1. Briefing-screen wind draws are front-end display state with no
battle-side reader. Phase 9 is the meteor-shower scheduler, not a wind callback
`[01 §7.3]` `[01 R-CORE-01 §4.4.1]` `[01 R-CORE-02]`.

**C12 — pools.** Units allocate lowest-free within the owner's slice with slot 0
null and immediate reuse; projectiles append at the tail; COB threads pick the
lowest clear mask bit `[01 §6.1]`.

**C13 — the projectile count is captured at entry.** Records appended during the
scan wait for the next phase, but the tail compactor reads the *current* count
and does include them `[01 §6.2]`.

**C14 — the scheduler box is 28 bytes.** Scaled anchor, pending count, last
delta, `float32` carry, global tick, requested and active speed, slew, flags —
in that order, little-endian. Neither random stream is saved, and there is no
Nanolathe-authored continuation format
`[08 "Scheduler and random state in saves"]`.

**C15 — publish once per completed sub-tick.** A five-tick burst publishes five
committed frames in order; a render frame that ran zero sub-ticks samples the
last committed tick `[03 §2.4]` [I6].

**C16 — there is no `alpha`.** Presentation does not interpolate between ticks
and no simulation package computes or observes an interpolation fraction
`[03 §2.4]` [I6].

**C17 — one sine table.** 512 entries, entry `i` is
`round(8192 · sin(i·2π/512))`, indexed with the pre-add of 32, cosine a quarter
turn ahead, products through the shared component routine that adds half the
scale and shifts down thirteen bits arithmetically. It is the only trig any
simulation package calls; the renderer's model trig is separate and floating
point `[04 §5.1]` `[03 §2.4]`.

**C18 — no substreams.** No per-entity, per-player or name-seeded stream helper
exists anywhere in the tree. Such a helper looks deterministic and reproduces
nothing retail does [I4].

**C19 — temporary-sight expiry is the executor tail's last step.** After the
twelve phases, the barriers, the message-ring retire and the pending
compaction, every record whose expiry tick is strictly below the current tick
has its byte-grid footprint removed through the visibility decrement publisher,
and the list — at most twenty records — is compacted in place and stably.
Records are appended by the central death handler in every session kind with
expiry `globalTick + 60`. The sweep makes no draw on either stream
`[03 R-COMP-02 §2]` `[08 R-SESS-01 §3]` `[01 R-PLAT-02 §5]`.

### 3.3 Determinism tokens — DET-01…DET-06 and PROC-03

These names appear in Go comments, mostly in `internal/session`. This section is
their definition of record.

**DET-01 — random-stream ownership.** There is no package-global fallback in
the tick path. The session constructs the two streams it owns and injects them;
the skirmish setup shuffle receives a disposable CRT and cannot advance the
retained session CRT; `rng.Global` is process bootstrap only. Presentation
draws exclusively from private copies of stream *state*. Three source-inspecting guards in
`internal/architecture` enforce it: presentation packages do not import
`internal/sim/rng` except through a commented, shrink-only allowlist of files
that copy state into a private copy; `rng.Global` is referenced only from the
stream package, session bootstrap, the commands and tests; and only
`internal/sim/rng` and `internal/session` construct a stream in non-test code
`[01 §7.1]` `[01 §7.2]` [I4].

**DET-02 — one phase registry.** `Session.Step` delegates to a single complete
sub-tick boundary, which calls each of the twelve phases exactly once, in
order, from one site. Sharing, result work and publication sit outside that
registry so the phase graph cannot absorb outer work `[01 §4.4]`.

**DET-03 — the wind redraw is complete inside phase 8.** One CRT interval draw,
then simulation strength, then simulation heading, then the vectors; battle
entry zeroes the deadline and draws nothing (C11).

**DET-04 — the shake driver is authoritative.** It runs in phase 10 with exactly
two CRT draws per active tick and publishes its cumulative offset on the
committed frame. The scroll toward the camera target remains presentation-owned
`[01 R-CORE-01 §4.4.1]` `[03 §5.6]`.

**DET-05 — unused.** No site carries it; it is retired rather than reassigned,
so an old comment quoting it is not silently given a new meaning.

**DET-06 — the visibility publication seam is inside phase 5.** The path
scheduler first, then per player ascending the orders and work pump, then that
player's dirty-checked stamp sweep. It is not a phase of its own, and no
post-phase-12 visibility pass exists `[01 R-CORE-01 §4.4.1]`.

**PROC-03 — the parity-drift ratchets.** The non-test sources of the
authoritative packages are checked in two ways. The I1 guard type-checks the
active package graph, so a map range is detected whether its expression is a
local, a selector, a named map type or a map-returning call. Every current map
range is an individually reviewed record containing its package path, function
and normalized containing function; a changed ordering dependency or a new
range fails until its ordering is reviewed. The I2 guard keeps the shrink-only
per-file baseline for ordinary
`float64` occurrences, while each existing I2 operation in a formerly exempt
file has a declaration-scoped count and reason. Resolved numeric fields in package-level named struct declarations are
separately named, including grouped and aliased scalar fields and numeric
containers. The walk terminates recursive types and leaves other named struct
owners to their own declaration audit. Local declarations and top-level function signatures retain declaration
token counts. Function-valued fields and dynamically held interface values
are outside the numeric-storage walk; this is not a semantic proof of all
floating-point uses. New recorded fields or operations cannot
inherit a file-wide exception. Counts may
fall freely — a fall means the baseline or scoped record is tightened in the
same commit — and any rise names the affected site. These ratchets enforce "no
new debt"; they do not certify the existing occurrences as correct. They live
in `internal/architecture` beside the boundary guard that keeps the displayless
command's dependency closure free of the client and the device packages.

### 3.4 Not implemented

* **The multiplayer clock paths.** `AdvanceMP` and the lag-throttle factor of
  C3 are implemented and unreachable: nothing supplies remote progress, so the
  factor is always one and the multiplayer pause branch is never taken. They are
  kept because the single-player/multiplayer asymmetry of `[01 §4.3]` is a
  behavior, and deleting one half would lose it.
* **The executor tail's barrier routines.** Three empty routines, in their
  place in the order and doing nothing, because that is what
  `[01 R-PLAT-02 §7]` establishes them to be.
* **The message-ring retire.** The tail's second step is presentation work: the
  in-battle text ring belongs to the frame package and the session only keeps
  the step's position, exposing it as an optional hook so a presentation layer
  can retire on the session's pump rather than its own `[01 R-PLAT-02 §8]`
  `[07 R-CAM-01 §7]`.
* **Phase 7 advances no simulation state.** The model-texture cursor service is
  presentation-owned; the phase exists so its cadence is the authoritative one
  `[03 R-CRD-005 §1]` [I6].

## 4. Determinism verification

**The report.** The displayless runner emits one JSON document per run: the
scenario kind and identity, both seeds and both final stream states, the
simulation and CRT **draw counts**, the catalog and VFS manifest hashes, the
final tick, the status, the session state and result, the deliberately partial
`state_hash`, and per-player rows (live units, units created, orders submitted,
kills, losses, order-intent census, first attack, and periodic task-group
samples). The legacy JSON key `state_hash` contains the version-prefixed result
of `Session.PartialStateFingerprint`: `partial-v1:<digest>`. The version also
enters the hash input; changes to the included fields or serialization require
a version bump. The observer draws no random numbers and writes no
authoritative field. This diagnostic is not a complete parity gate.

**Fingerprint scope, version 1.** The canonical serialization is the ordered
writer in `internal/session/p28_parity_trace.go`; numeric floats enter as exact
bit patterns and definitions as canonical keys. It covers these bounded views:

| Owner | Included values |
| --- | --- |
| Clock | Published clock fields and normalized save box, read on a value copy. |
| Units | The fields serialized by `writeParityUnit`: health/status, positions, selected mover values, cargo, weapon slots, order records, thread stacks/waits, piece transforms/flags; per-player creation counts. |
| Economy | Unit buckets and archived counters, and the player fields explicitly serialized from `ParitySnapshot`, including stocks, capacities, totals, deadlines and alliances. |
| Movement | Provider request order and goal values; active scheduler request and exposed cadence/scale fields; collision projection (identity, position, planar velocity, speed, heading, footprint, mode and anchor values, blocked/dirty flags); serialized route flags and retained/current route points. |
| Features | The live instance fields serialized by `writeFeature`, including position, sinking state and burn cursor. |
| RNG | Session initialization flag, both stream states and draw counts. |

**Excluded state and limits.** Projectile records and their pool order, meteor
scheduling, AI tasks and registries, trigger/result latches, visibility and
temporary sight, active path-search working storage and provider cursors, COB
statics/animation scheduling, and construction service internals are not fully
represented. Movement also omits vertical collision velocity, stamped occupancy,
`LastProposalTick`, route `WantsRepath`/`LastRequestTick`, and scheduler work
accumulators/cursors. All owner rows above are bounded projections, not complete
owner snapshots. Equality therefore cannot establish equal future behavior. Use the
owning subsystem's state/ordering tests for those contracts; a future extension
must audit the additional state and publish a new fingerprint version.

Collision history, optional path-result history, path-failure diagnostics and
callback trace buffers are excluded. Enabling, recording, limiting and clearing
these traces must leave the fingerprint unchanged. Pending path requests come
from live provider/scheduler views, not opt-in trace records.

**The reference run.** The regression fingerprint is a fixed map, a fixed seed
and a fixed tick count:

```
nanolathe-headless -map "ashap plateau" -seed 7 -ticks 6000
```

Its value is deliberately not written down here. Two runs of the same command
and fingerprint version must agree. Changes to included values must explain
the difference; changes outside the listed coverage need direct contract
checks. Both draw counts must also agree for identical seeded setups [I4].
Neither a matching digest nor matching draw counts certify omitted state.

**Guards that read source, not runtime.** `internal/architecture` inspects the
tree with the Go parser and, for map ranges, Go type information over the
authoritative package graph: the three DET-01 stream guards, the PROC-03
ratchets, the platform boundary (only the Ebitengine
adapter, the audio backend and the desktop command may reach the window
modules), the displayless command's dependency closure, and the rule that
authoritative packages import no host clock, device or foreign random source.
Being source-inspecting is the point — the guard catches an architectural
dependency before any feature exercises it.

**Tests that pin the tick.** The phase registry's order and its per-phase RNG
draw deltas are pinned together, so a phase that starts drawing, or draws in a
different order, fails as loudly as a phase that moves. Beside them sit the
checks that path publication follows movement, that exactly one publication
happens per completed sub-tick, and that the committed frame's tick matches the
sub-tick that produced it.

**Passive evidence capture.** `internal/parity` is the opt-in recorder used
when a run has to be compared against another run or against a retail
observation: a stable hash over arbitrary recorded values that sorts map keys
and preserves float bits, plus trace and image writers. It runs no simulation
work, samples no clock and owns no random stream, so enabling it cannot move a
hash. The session's own trace hook is nil and no-op until switched on, and its
selection is a sorted handle list rather than a map so it cannot perturb
iteration order.

**The floating-point allowlist.** Every `float64` and `float32` in the
authoritative tree maps to a row of I2 or is presentation-only. Two of those
rows are this document's: the clock budget's `float64` product with its
`float32` carry `[01 §4.2]`, and the simulation trig table's construction at
initialization, whose authoritative entries are integers `[04 §5.1]`.

## 5. Divergences

* **SC18 — deterministic initialization.** Established: retail's default
  allocator does not fill, and allocation failure terminates the process
  `[01 R-PLAT-01 §5]`. Go zero-initialization is a host policy, not a traced
  retail fill operation. Unknown: constructor and slot-reuse writes versus
  reads before initialization for incompletely traced records; the decider is
  a writer/reader census in the owning unit, projectile, order or COB contract.
* **The dual-stream `--seed` override.** Retail has no way to fix both streams.
  Coupling them behind one flag is a debug affordance, not a runtime mode: the
  unseeded path uses separate sources (B4, C10).
* **CRT lifetime and presentation isolation.** Retail's main-thread CRT begins
  at process startup; front-end, tick-side, audio and rendering consumers
  interleave on that history, while battle setup uses and discards a separate
  loading-thread CRT `[01 R-CORE-02]` `[01 R-PLAT-01 §7]`. Nanolathe starts a
  retained CRT from every battle's explicit seed, gives skirmish setup a
  disposable CRT from that same seed, and gives briefing, audio, music and
  segmented-projectile presentation private histories. Briefing duration and
  render/audio cadence therefore cannot move authoritative battle state, and
  setup draws cannot move gameplay history. This is the approved isolation
  policy behind I4 and I6; it is one behavior with no compatibility mode.
* **A zero bound on the CRT sampler.** Retail's unsigned divide would fault. The
  draw is still consumed — retail draws before the divide — and zero is
  returned rather than panicking (C9) [I11].
* **The working directory.** Retail sets the process working directory at
  startup; Nanolathe keeps an explicit root and never calls `os.Chdir`. A
  library-friendly equivalent with no behavioral difference (B2).
* **Frame header bounds.** The save container's header offsets are
  bounds-checked where retail does not; that is the sanctioned exception to I11
  and belongs to DESIGN_SESSIONS_AI_SAVE, which owns the container.

No other entry in [SPEC_CONFLICTS.md](SPEC_CONFLICTS.md) touches the clock, the
streams, the pools, the tick or the committed frame.

## 6. Research map

| Behaviour | Owning research |
|---|---|
| Startup order, singleton, CRT seed before the command line, timebase after the window | `[01 §2.1]`, `[01 R-PLAT-02 §1]` |
| The application pump, activation gating, the battle host pump | `[01 R-PLAT-01 §1]`, `[01 §2.3]` |
| The quit request, exit path and shutdown sequence | `[01 R-PLAT-02 §2]` |
| The command-line switch census (none alters the content root) | `[01 R-PLAT-01 §2]` |
| Profile identity is a name, not a content hash | `[01 §3.1]` |
| Timebase: `floor(ms × 30 / 1000)`; the performance counter seeds, it does not drive | `[01 §4.1]` |
| The scaled-clock timer table and its two audio registrants | `[01 R-PLAT-02 §4]` |
| Budget arithmetic, the `float32` carry, the `0..5` clamp, negative wrap deltas | `[01 §4.2]` |
| Speed clamp, hysteresis, the pause asymmetry, the 100 ms keepalive gate | `[01 §4.3]`, `[01 R-PLAT-01 §3]` |
| The twelve-phase order, the per-unit micro-order, event visibility across phases | `[01 §4.4]`, `[04 §5.4]` |
| Phase 9 identity, phase 10 shake arithmetic, phase 11's object family, the phase 5 visibility seam | `[01 R-CORE-01 §4.4.1]` |
| Battle RNG seeding and the chronological draw census | `[01 R-CORE-02]` |
| Phase 12: the radar blink countdown, its reset, consumers and non-persistence | `[01 R-CORE-03]` |
| The thread census; which thread's CRT block each consumer reads | `[01 R-PLAT-01 §4]`, `[01 R-PLAT-01 §7]` |
| The temporary-sight list and the start barrier | `[01 R-PLAT-02 §5]` |
| The executor tail runs unconditionally, including on a zero-runnable pump | `[01 R-PLAT-02 §7]` |
| The tail's ring is the in-battle message ring, and the tail has three steps | `[01 R-PLAT-02 §8]` |
| Fixed pools: unit slicing, the projectile pool, COB threads, slot 0 | `[01 §6.1]`, `[04 §2.3]` |
| Queues, the captured count, the fixed iteration order | `[01 §6.2]`, `[06 §5.1]`, `[06 §5.2]` |
| The allocator's fill policy and failure path | `[01 R-PLAT-01 §5]` |
| Park–Miller with Schrage, the bound-below-two rule, the seed transform | `[01 §7.1]` |
| The CRT recurrence and its widening sampler | `[01 §7.2]` |
| Sampling, the wind draw chain, save implications | `[01 §7.3]` |
| The float→int conversion census; the two round-to-nearest sites; control-word mutations | `[01 R-DET-01 §1]`, `[01 R-DET-01 §2]`, `[01 R-DET-01 §3]` |
| The per-phase random draw table; the CRT stream's other consumers; lane claims re-checked | `[01 R-DET-01 §4]`, `[01 R-DET-01 §5]`, `[01 R-DET-01 §6]` |
| `__ftol` truncation toward zero | `[01 §8]` |
| Fixed-point scale, the arithmetic shift with sign correction | `[03 §2.1]` |
| The 512-entry sine table, the index pre-add, the component routine | `[04 §5.1]`, `[04 R-MOV-01 §4]` |
| The draw path does not interpolate; the committed sample | `[03 §2.4]` |
| Phase 7's model-texture sequence traversal | `[03 R-CRD-005 §1]` |
| Strip storage and the phase-11 sweep; the strip pool and base object | `[03 R-STRIP-01 §2]`, `[03 R-STRIP-01 §3]`, `[03 R-FX-02 §1]` |
| Temporary-sight expiry as the executor's last step | `[03 R-COMP-02 §2]` |
| Screen shake and audio variant draws on the CRT stream | `[03 §5.6]`, `[03 §8.3]` |
| Committed events survive a faster publication cadence | `[03 R-AUD-01 §7]` |
| Meteor scheduling draws | `[06 §6.5]` |
| Battle entry seeding, the session words, the pre-tick state and the first pump | `[08 R-ENTRY-01 §2]`, `[08 R-ENTRY-01 §9]` |
| The single-player temporary-sight producer | `[08 R-SESS-01 §3]` |
| The 28-byte game-time box; RNG is not saved | `[08 "Scheduler and random state in saves"]` |
| Which state may run authoritative ticks | `[08 "Session states"]` |
| The per-player settlement loop the planner hangs off | `[05 "Authoritative settlement order"]` |
| Content paths are relative to the install root | `[02 §1]` |
| The in-battle message ring the tail retires from | `[07 R-CAM-01 §7]` |

## 7. Not implemented and open

No `TODO(question)`, `TODO(T23)` or `TODO(T25)` marker remains in
`internal/clock`, `internal/sim/rng`, `internal/sim/numeric`, `internal/pool`,
`internal/frame`, `internal/version` or the two commands' boot paths. Three
markers in `internal/session` belong to families other documents own, and are
listed here because the file is here:

* `TODO(T23)` in the effect-strip flame spawner — a span under five world units
  (including a degenerate zero-length teleport) makes the segment life zero, and
  retail's per-axis divide faults on it, so there is no behavior to clone. The
  placeholder is the family's minimum life of one tick; the start-frame CRT draw
  is spent either way, so the draw census is unchanged `[03 R-FX-02 §2]`. The
  strip families themselves are DESIGN_PRESENTATION_CLIENT.
* Two `TODO(question)` markers at the world-click order producer — whether the
  producer receives a goal point alongside a target handle, and whether the side
  panel's non-world-click issues run the same duplicate test. Both need a trace
  of the interface's call into the producer; they belong to
  DESIGN_INTERFACE_HUD_INPUT `[07 §9]`.

Open questions carried by the contracts above rather than by a marker:

* **Constructor initialization and reuse** (SC18): which fields are read before
  a constructor or later writer initializes them, and which survive slot reuse.
  A complete per-record writer/reader census would settle those residuals; the
  allocator's no-fill default and fatal failure path are established
  `[01 R-PLAT-01 §5]`.
* **The singleton semaphore's release point** is not named by the traced
  shutdown sequence. It is a platform residual with no simulation effect, and
  Nanolathe creates no semaphore `[01 R-PLAT-02 §2]`.
* **The multiplayer lag reading** is ambiguous between remote progress and
  earliest pending order in the same code; the throttle is unreachable, so the
  ambiguity has no consequence today `[01 §4.2]`.
* **Phase 5's per-player callback identity** — that the position the planner
  occupies is an auxiliary player-level update rather than a dedicated hook — is
  a supported inference and is marked as such at the call site
  `[01 R-CORE-01 §4.4.1]`.
