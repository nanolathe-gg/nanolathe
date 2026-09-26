# Modern AI research framework (prototype)

Status: prototype. Nothing here is bound by a rule set: Strict 3.1,
Community 3.9 and Modern keep their planners for every Classic computer
player. The game plays `util+tac` for each computer player the lobby marks
Modern, in any rule set (play against it, §7); the arena's `aikit` sets,
which only `cmd/ai-arena` registers, run the prototype brains. The policy (a
private generator per computer player, controller state, background
thinking; user-approved 2026-09-24; per player in every mode with full
income, 2026-09-25) is owned by DESIGN_SESSIONS_AI_SAVE "Modern AI computer
player". Runs below named `modern-ai` used the since-retired rule set that
put every computer player on the Modern AI before the choice became per
player; `--gameplay modern --ai-player all=modern` plays the same battles
(the Great Divide seed 7 run keeps its `state_hash`).

`main` carries the util+tac brain (the `utility` and `tactics` packages),
the survival brain, the arena with its `scripted`, `utility`, `tactics`,
`util+tac` and `retail` contestants, and `tools/ai-arena-summary` and
`tools/ai-arena-analyze`. The other research contestants (`planner`,
`plan+tac`, `mcts` and its variants, `replay`, `forces`) and the research
tools cited below and in the brain READMEs (`tools/ai-human-bench`,
`tools/ai-layout-bench`, `tools/ai-report`, `tools/ai-tune`, `tools/ai-viz`,
`tools/tad-extract`, `cmd/ai-minimap`) remain on the research branch
`modern-ai-v2`; results that name them are the record of that evaluation.

## 1. Layers

```
session phase 5 ──► ai.Manager.Tick ──► RuleSet.Planner (aikit.HostPlanner, zero size)
                                              │ m.Ext holds the per-player *aikit.Host
                                              ▼
Host.Step(tick)
  1. gates (retail's two dispatch gates, Manager.StepGates)
  2. apply the pending command batch if due (reaction latency)
  3. if a think is due: build a fair Obs on the sim thread, then
       sync:  Brain.Think(kit, obs) now
       async: hand the obs to the host's worker goroutine; join at due
  4. engine upkeep (weapon maintenance + strategic/target-registry refresh)
     — identical for every brain, so weapons never depend on the AI chosen
```

* `internal/aikit` — host, observation, command executor, unit roles
  (`Table`, `UnitInfo`), map analysis (`MapInfo`: metal spots, starts, wind,
  sectors), integer influence `Grid`, `Persona`.
* `internal/aikit/core` — the chassis: a `Board` (blackboard rebuilt per
  think, persistent builder tasks, spot claims, threat/value/own-power grids)
  and four replaceable `Policy` layers run in order
  **Strategy → Economy → Army → Production**. `core.New(label, s, e, a, p)`
  composes a brain. `core.Script*` / `core.WaveArmy` are the scripted
  baseline policies; any layer can be swapped for another package's policy.
* `internal/aikit/brains/<name>` — one package per approach.
* `cmd/ai-arena` — `match` (one game → JSON, optional replay trace) and
  `tournament` (many games across processes). Brains are registered for the
  tool in `cmd/ai-arena/brain_<name>.go` with `registerBrain` in an `init`.

## 2. Rules every brain follows

1. **Think is pure.** It reads only the `*aikit.Obs` it is handed, the `Kit`'s
   immutable tables (`Table`, `Map`), its persona and its own state. Never
   read `*units.Unit`, the session, or any global mutable state. Never keep a
   pointer into the Obs across thinks (buffers are reused); copy values.
2. **Integer arithmetic only** in decisions (docs/INVARIANTS.md I2). Scores
   are `int32`/`int64`. No `float32`/`float64` in a brain.
3. **Randomness only from `Kit.Rand`.** Neither simulation stream may be
   drawn: an asynchronous think runs on another core and would race the
   simulation for its draws. For variety, each host owns one private PCG32
   generator (`aikit.Rand`, PCG-XSH-RR 64/32) seeded from the battle's
   simulation seed (`ai.Manager.BattleSeed`) and the player slot, hashed
   through the SplitMix64 finalizer, and hands
   it to its brain as `Kit.Rand`. Draw from it only in `Init` and `Think`
   (a policy's `Init`/`Plan`), never in `Explain` or `Report`, which run only
   under instrumentation. A game therefore replays exactly from its seed,
   and the synchronous and asynchronous hosts stay identical. No
   `math/rand`, no other generator (docs/INVARIANTS.md I4, "Modern AI
   exception").
4. **No map iteration** in a decision path (I1). Maps are fine for lookup.
5. **No clocks** (`time.Now`) — cost is measured host-side by the arena.
6. **Allocation discipline.** Reuse slices held on the brain (`buf[:0]`).
   A steady-state think should allocate ~0 bytes; measure with `-allocs`.
7. **Commands are intents.** `Kit.Move/Patrol/Attack/AttackPos/Guard/Repair/
   Reclaim/Stop/Build/Produce` enqueue player-level actions; the host applies
   them after the persona's reaction latency, revalidating every actor and
   target, and building placement is resolved on the simulation thread.
   A group order is one action. `Kit.Last` reports the previous batch's
   applied / dropped-by-APM / stale / failed counts (with reasons).
8. **Fairness.** The Obs holds own units, enemies in line of sight (typed),
   enemies inside own radar coverage (untyped blips), remembered sightings
   (last seen position; cleared only when the spot is looked at again) and
   allied units in sight (`Obs.Allies`, by the same sight test; a Survival
   team's shared sight, §3).
   Start positions and metal spots are public map knowledge.

## 3. What the observation gives you

`Obs`: `Tick`, `Metal`/`Energy` (`Stock`, `Cap`, `Income`, `Expense` per
second), `Own []OwnUnit` (handle, `*UnitInfo`, position, HP, `Built`,
`Progress`, coarse `Order` class, `QueueLen` — factories count queued items —
and a brain-owned `Tag` set with `Kit.SetTag`), `Enemy []Contact`,
`Memory []Remembered`, `Allies []AllyUnit`, `Allied` (the allied slots),
`UnitCount/UnitLimit`, `WindPermille`.

`Allies` (2026-09-25) are the allied players' units in the owner's sight:
handle and instance (`Gen`), owner, `*UnitInfo`, position, `HP`/`MaxHP`,
`Built` and `Progress`. They carry no order, queue or tag (the owner cannot
command them), no radar blips and no memory: an ally out of sight is not
listed. The host tests each allied unit with the same sight predicate as an
enemy, in the same unit walk, so they show what the owner's own line of sight
shows in a skirmish and what the team's shared sight shows in a Survival
battle (DESIGN_SURVIVAL §4.3) — never more. The commands that accept a
friendly target take an allied handle: `Kit.Repair` (code 8, a repair or,
on an unfinished frame, an assist: the resolver admits a target on
nano-reach alone, with no ownership test [04 R-ORD-02 §1], the repair order
has none either [04 R-ORD-01 §5], and the repair bills the repairer's energy
[05 R-WORK-01 §3]) and `Kit.Guard` (code 7, a target that is not hostile).
A brain that does not read the list plays as before: 18 arena matches
(util+tac hard, medium and easy against retail on The Pass, Comet Catcher
and Great Divide, seeds 1–2, 27,000 ticks) hash as before with the timing
removed, and `modern-ai` skirmishes on The Pass and Great Divide, a Strict
3.1 skirmish, and Strict 3.1, Modern and `modern-ai` Survival battles on
Painted Desert (the last with the survival brain before it read allies)
keep their `state_hash` and simulation draws. Cost: about 120 ns per allied
unit in sight, most of it the sight predicate. In Survival battles with an
active human stand-in (§16.5 of DESIGN_SURVIVAL; identical games before and
after, 30 minutes, per-player minimum of two runs) the observation went from
46–49 to 78–82 µs a think with 260–285 allied units (Painted Desert, two
buddies), 38–41 to 68–72 µs with 230–250 (Comet Catcher), and 24–29 to
37–40 µs with 75–110 (Great Divide, one buddy); its 99th percentile from
210–250 to 370–400 µs. Without allies (the arena) it is unchanged: median
16.8 against 17.0 µs over the 18 matches.

`UnitInfo`: roles (`RoleCommander, RoleBuilder, RoleFactory, RoleExtractor,
RoleEnergy, RoleMetalMaker, RoleStorage, RoleRadar, RoleDefense, RoleCombat,
RoleAir, RoleNaval, RoleHover, RoleScout, RoleAntiAir, RoleArtillery,
RoleBomber, RoleFighter, RoleMobile …`), costs, `Value` (metal + energy/60),
`HP`, `DPS`, `AirDPS`, `Range`, `Speed` (wu/s), `BuildPower`, `EnergyMake`,
`WindGen` (× `Obs.WindPermille`/1000), `MetalMake` (extractors: ×1e5 per
footprint metal unit), `Depth` (build-tree tier), `Builds` (products under the
session's build rules). `Strength()` = DPS×HP/16, a Lanchester-style product.

`MapInfo`: sizes, `Spots` (extractor sites with metal sums, water flag),
`UniformMetal`, `Starts`, `WindMin/WindMax`, `TidalPermille`, sector grid
(128 wu). `core.Board` adds role lists, home/rally/enemy-base estimates,
`Threat`/`EnemyValue`/`OwnPower` grids, spot states, enemy strength by
domain, `NearHomeThreat` and its centroid.

## 4. Personas (difficulty as execution limits and ambition)

`Persona{ThinkEvery, Reaction, APM, Burst, Attention, Skill, Ambition, Async}` —
`easy`, `medium`, `hard`, `max` are built in. Difficulty changes how often a
brain looks, how late its commands land, how many actions it may take and
how many operations it may run at once (`Attention`, brain-interpreted) —
never resources. `Skill` 0..100 gates refinements a human pays attention for
(micro, focus fire). `Ambition` 1..100 (easy 35, medium 80, hard and max
100; 0 means unset and normalizes to 100) is how much the brain attempts —
constructors, expansion reach, factories, tech, defenses, the army it
builds toward — not how well it decides; the utility brain reads it as the
percent of the top human skill tier's build curves it attempts (§8). Arena
player spec:
`brain[:persona[:side[:k=v,...]]]` with overrides `think=`, `react=`,
`apm=`, `attention=`, `skill=`, `ambition=`, `async=1`.

## 5. Evaluation protocol

Build once: `go build -o /tmp/ai-arena-<you> ./cmd/ai-arena`.

```
ai-arena match -map "great divide" -seed 1 -ticks 36000 -p mybrain:hard -p retail -out r.json \
    [-starts slot|random|swap] [-score default|invested] [-raw-seed] [-trace t.json] [-allocs]
ai-arena tournament -spec spec.json -out DIR -jobs 3
tools/ai-arena-summary DIR [DIR ...] [--by-map] [--score recorded|default|invested] [--json OUT]
tools/ai-arena-analyze analysis.json DIR [DIR ...] [--anchor retail]
```

**Host.** Run tournaments niced (`nice -n 15`) with at most three match
processes; `-jobs` defaults to 3. The host is shared with other work, and
the arena measures think and step times that contention would distort.
Raise the cap only on a dedicated machine.

**Maps and length.** Development pool (land, 1v1): `great divide`, `the
pass`, `dark side`, `red planet`, `full moon`, `sherwood`. Held-out pool:
`metal heck`, `comet catcher`, `evad river confluence`, `ashap plateau`,
`painted desert`, `coast to coast`. Water pool: `hundred isles`, `sail
away`, `shore to shore`, `lake shore`, `ring atoll`, `pillopeens`, `brain
coral`, `canal crossing`. Games last 20 minutes (36000 ticks) at the `hard`
persona unless a test says otherwise; long games last 40 minutes (72000).

**Seeds.** A spec's seed is not the battle seed. The arena plays
`headless.ArenaMapSeed(seed, map)`: the map name, trimmed and lower-cased,
is hashed with 32-bit FNV-1a; that hash is the low word and the seed the
high word of a 64-bit value; the SplitMix64 output function finishes it; the
low 32 bits seed both battle streams. A Modern brain draws its style and
jitter from the battle seed and its slot, so a seed list reused on every
map would otherwise replay the same few draws on every map: under raw
seeds, a two-seed, twelve-map mirror sampled 2 style pairings; mixed, the
same 24 games sampled 16. `match -raw-seed` (spec `"raw_seeds": true`)
plays the seed itself and reproduces runs made before 2026-09-24. Results
record `seed`, `battle_seed` and `crt_seed`.

**Slot orders and mirrors.** `swap_sides` plays every pair in both slot
orders; sides follow the slot unless a spec names one. When both specs are
identical, the reversed order resolves to the same arguments and is the
same deterministic game, so the tournament plays it once. The summary and
analysis tools also count a repeated configuration with an identical
outcome once, so older directories are read correctly.

**Start positions.** Slot order is not neutral on its own (think stagger,
stepping order, the per-slot random stream), so a start advantage must be
separable from a slot advantage. A spec's `"starts"` is `slot` (slot *i* at
the map's start *i*, the default), `random` (the retail randomized
assignment [08 "Randomization for skirmish starts"], drawn from the battle's
CRT seed: with two players a coin decides whether they exchange starts) or
`both` (every order plays once at slot starts and once swapped). The
session has no explicit assignment, so `match -starts swap` plays the
randomized assignment with the first CRT seed, counting up from the battle
seed, whose gate exchanges the two starts, and checks the commanders'
placement before the first tick; the simulation seed is unchanged. Every
result records each slot's `start`.

**Ending and scoring.** Every 30 ticks the arena checks for survivors; when
at most one player still has its commander and a unit, the game ends
"decisive" (a commander kill; with none left it is a mutual kill, a draw).
At the tick limit each survivor gets a score from the last sample (every
150 ticks). A unit's value is metal + energy / 60, and only finished units
count: factories and every unit that is not a builder, combat unit or
defense are economy value E; combat units are army value A; a defense adds
half its value to each. K is the value of other players' finished units
destroyed while this player dealt the last damage, L the value of this
player's finished units destroyed by any cause. Divisions truncate.

* `default` score: S = A + E + K − L/2. Builders, the commander,
  nanoframes and stock count nothing.
* `invested` score (`-score invested`, for investment tests such as the
  growth switch): S + B + N, where B is the value of finished builders
  other than the commander and N sums, over this player's nanoframes,
  value × built fraction (each truncated). The commander (everyone starts
  with one) and stock still count nothing.

A player that is out scores −1. The game adjudicates on the score the match
names (default `default`; results record `score_kind` and both scores): the
highest score wins "points" when the runner-up's is negative or 10 × best ≥
13 × runner-up (a 30% margin); otherwise it is a draw ("timeout"). A
negative score therefore loses on points at any margin.

**Reporting.** `tools/ai-arena-summary` reports per pairing: W-D-L split
into commander kills, points wins, timeouts and mutual kills; the share of
decisive games won; the points share (W + D/2) / games with a 90% interval
from a map-cluster bootstrap (whole maps resampled, 2000 resamples, seed
20260924); the points share re-adjudicated at margins 1.1, 1.3, 1.5 and
2.0; the share by slot and by start; the number of distinct style
pairings sampled; kill/loss, income and army; and per-contestant cost
(think mean/p99/max µs, host step mean/max µs, allocations per think,
command outcomes). `tools/ai-arena-analyze` rates contestants
(Bradley–Terry) with map-cluster bootstrap intervals and leaves mirror
games out of the ratings. State a points share as a difference only when
its map-cluster interval excludes 50%, and give the decisive-only share,
the margin sweep and the style-pairing count beside it.

**Protocol v2 (2026-09-24)** — `cmd/ai-arena/testdata/protocol-v2/v2-*.json`,
run by the research branch's `tools/ai-report/round2/specs/run-v2.sh`
(niced, three processes, then summarized):

| spec | maps | seeds | length | games |
|---|---|---|---|---|
| `v2-land` | 12 land (both pools) | 8 | 20 min | 384 |
| `v2-water` | 8 water | 8 | 20 min | 256 |
| `v2-ladder` | 12 land | 8 | 20 min | 768 |
| `v2-long` | 6 land | 8 | 40 min | 144 (mirror once) |

All use map-mixed seeds, both slot orders with mirrors played once, and
random starts. Every result in §6 and §8 (the brain's "V2" results
included) was produced under protocol v1: raw seeds (the same style draws on
every map), mirrors played in both orders, starts fixed to slots and
game-resampled intervals, all of which overstate precision. The `int4` specs beside the v2 ones on the research branch carry `raw_seeds` so they
still reproduce. Protocol v3 (§5.3) asks the same questions in a fraction
of the time, and it reads verdicts on a *t* interval over map means instead
of the bootstrap, which overstates significance.

### 5.1 Simulation-thread cost (2026-09-25)

What the computer players cost the simulation thread, which the game runs
at 30 ticks a second beside a presentation that may run at 120 frames
(8.3 ms a frame).

**What is timed.** `Host.Step` runs in the manager's phase-5 slot: the
first tick's hand-off (`begin`), a join that finds the worker still busy
(`join`), the command apply (`apply`), the observation (`observe`), a
synchronous think or its hand-off to the worker (`think`) and the retail
engine upkeep (`upkeep`, which every computer player runs, the retail
planner's included). The arena reports each part per player
(`cost.parts`, `cost.join_waits`, `cost.prep_us`, the preparation's worker
time), per match the per-tick sum of every player's step
(`host_timing.ai_tick_mean_us`/`_p99_us`/`_max_us`, and ticks over 1 and
4 ms), the longest tick and the collector's activity (`host_timing.gc`);
`-allocs` adds each step's allocations. `aikit.PartProbe` is the host-side
hook; the session reads no clock. Host tools also get the per-tick series
(`ArenaHostTiming.Ticks`, `AITicks`, `PartTicks`), which the result file
leaves out.

**Method.** A match is deterministic, so a loaded host only adds time:
each configuration is played several times and the per-tick minimum over
the runs is taken before any percentile. Wall-clock maxima of single runs
are not evidence on a shared host — at load 100 on 12 cores, 1% of ticks
lose 10–30 ms to other processes, and a 4-player match showed 3–11 ms
thinks whose thread CPU time never reached 1 ms. The per-part maxima below
are therefore thread CPU time, read only around the parts that do work
(`begin`, `apply`, `observe`, `think`) by a throwaway measurement build:
on macOS a per-thread clock read is a system call, and reading one at
every part of every step (as an arena option did, briefly) put two thirds
of a profiled match's samples in system calls. Deterministic work counts
(cells walked, placements tried) back the timings, and every change is
checked for identity first.

**Configurations.** The Pass (small) and Comet Catcher (large), with 2, 4
and 8 computer players — the 1-, 3- and 7-opponent lobbies with the
player's own slot also a computer player, so the figures are an upper
bound — all `util+tac` hard, seed 1, 72,000 ticks (40 minutes; the 8-player
The Pass game ends at tick 30,120). "Base" is `modern-ai-v2` 32690a1f with
the instrumentation only; "new" is this branch. The host carried a load of
80–175 on 12 cores throughout.

**Targets** (set from the baseline): the AI's simulation-thread work under
1% of the 33 ms tick on average and under 1 ms at the 99th percentile of
ticks, with 7 opponents; no tick over 4 ms of AI work except the battle's
first; no simulation-thread wait at a join in real time.

**Results.** Thread CPU of the AI's working parts per tick (begin, apply,
observe, think; the retail upkeep is left out as every computer player
runs it), summed over the players:

| map | players | mean µs | p99 µs | max µs | ticks > 1 ms | ticks > 4 ms |
|---|---|---|---|---|---|---|
| The Pass | 2 | 19 → 18 | 276 → 265 | 6,203 → 2,920 | 48 → 43 | 1 → 0 |
| The Pass | 4 | 66 → 16 | 166 → 136 | 188,535 → 2,604 | 63 → 21 | 57 → 0 |
| The Pass | 8 | 40 → 30 | 171 → 138 | 6,637 → 1,886 | 3 → 5 | 2 → 0 |
| Comet Catcher | 2 | 38 → 35 | 594 → 519 | 3,729 → 3,699 | 94 → 75 | 0 → 0 |
| Comet Catcher | 4 | 93 → 63 | 833 → 573 | 5,087 → 5,081 | 357 → 190 | 5 → 1 |
| Comet Catcher | 8 | 118 → 96 | 730 → 655 | 4,550 → 3,294 | 399 → 326 | 2 → 0 |

(Per-tick minimum over two runs for The Pass with 2 and 4 players and
Comet Catcher with 8, one run otherwise.)

Wall clock of the whole host step (upkeep included), per-tick minimum over
three runs ("new" at the board and observation change, before the last
three placement and table changes); the retail planner's step in the same
battles, per-tick minimum over two runs, for comparison:

| map | players | AI mean µs | AI p99 µs | retail mean µs | retail p99 µs |
|---|---|---|---|---|---|
| The Pass | 2 | 19 → 19 | 261 → 261 | 25 | 129 |
| The Pass | 4 | 70 → 20 | 242 → 245 | 50 | 230 |
| The Pass | 8 | 35 → 28 | 293 → 292 | 96 | 507 |
| Comet Catcher | 2 | 28 → 28 | 373 → 374 | 27 | 185 |
| Comet Catcher | 4 | 64 → 62 | 647 → 642 | 60 | 420 |
| Comet Catcher | 8 | 108 → 100 | 808 → 804 | 135 | 994 |

The Modern AI's simulation-thread cost is the retail planner's on
average: below it with more players, above it at the 99th percentile
with fewer (its apply and synchronous think land on think ticks, the
retail planner's work is spread). Against the targets: the average is
0.3% of the tick with 7 opponents, the 99th percentile under 1 ms
everywhere, and no tick over 4 ms of AI work remains but one in 72,000
(a failed factory search on Comet Catcher, 5.1 ms).

- *Joins.* The shipped personas think synchronously, so a controller joins
  its worker once, for the preparation (the map analysis and the brain's
  `Init`, with the first think behind it). The preparation took 2–53 ms on
  the loaded host against a deadline of at least the reaction window
  (200 ms at hard); unpaced matches, whose first deadline arrives within
  milliseconds, waited once or twice a match, and paced at 30 ticks a
  second (8-player Comet Catcher, 9,000 ticks; before and after, and with
  asynchronous personas) no controller waited at all.
- *Thinks.* No think exceeded 1 ms of thread CPU in any configuration
  (per-tick maxima 0.2–0.9 ms, summed over the players); the 3–110 ms
  wall-clock "thinks" of single runs were the host's other processes.
- *Spikes.* What remains over 1 ms is the command apply's building
  placement: a factory search that finds no site on broken ground costs
  2–5 ms of thread CPU. Players apply on different ticks (each thinks on
  its own phase), so these do not stack as players are added.
- *Allocation and collection.* The host step's allocations (with `-allocs`,
  synchronous personas, the probe's own bookkeeping excluded): 4-player
  The Pass 646 → 484 bytes a tick (5.4 → 2.6 objects), 8-player Comet
  Catcher 2,671 → 2,366 bytes a tick (8.3 → 8.1 objects), of which thinks
  are 40 and 320 bytes a tick (a steady think allocates nothing; the rest
  is per-handle memory growing with new handles). The rest of the step's
  allocations are the order nodes the commands create and the retail
  upkeep's strategic refresh, which every computer player runs. That is
  21% (was 26%) and 14% (was 16%) of what the whole match allocates, about
  0.1–0.5 MB a second of game; a 40-minute match collects 2 and 8 times,
  before and after, with total pauses of a few to a few tens of
  milliseconds whose spread between runs is larger than any difference.

**What changed** (each change result-neutral: six arena matches —
hard, medium, easy, retail and asynchronous personas on three maps and
three seeds, 27,000 ticks — the 40-minute 4-player The Pass and 8-player
Comet Catcher matches, and four displayless `modern-ai` runs hash as
before):

- *Factory exits.* A factory site search tries up to 13,122 anchors, and
  the exit guard drew an 81×81 macro-cell window for each one that passed
  the cheaper checks and walked it to see whether the planned factory's
  exit reaches the window's edge. On broken ground nearly every anchor
  faces the same enclosed pocket, and a search that finds no site walked
  it thousands of times (4-player The Pass: 74,581 windows drawn, 87
  million cells walked; single searches of 150–270 ms). The executor now
  labels each free region once per tick and class; a region that fits
  inside the window's interior cannot reach its edge whatever the
  footprint blocks (`exitPocket`), and an anchor facing one is refused
  before the other checks when running them could change nothing
  (`sealedSite`: every guarded factory's picture is already current).
  Otherwise the walk runs over the tick's cached answers without drawing
  a window, and the macroFree cache keeps three classes, so asking about
  one no longer drops another's answers (same match: 161 windows, 1.1
  million cells). `TestExitSealedIsTheWindowWalk` pins the answer to the
  window walk.
- *Tactics picture.* The loss check walked the army's whole per-handle
  memory, and the hurt grid decayed every sector, each think; they now
  walk the units observed last think and the sectors holding damage.
- *Board and observation.* The board resets the handle index from the
  handles it set; the observation finds jammers in its own-unit pass.
- *Site searches.* Within one search the placement validator's and the
  row rules' answers are kept per anchor, so a factory's second pass (a
  metal spot allowed in its lane) reads the first pass's instead of
  asking again: nothing changes the world while a search runs.
- *Unit table and map analysis.* Every host built the catalog summary on
  the battle's first tick (a quarter of a millisecond each); the battle
  builds it once and its hosts share it, like the map analysis. The
  preparation's depth-site searches reuse one table instead of allocating
  one per call (27 MB over an 8-player match's preparations).

**Remaining.**

- *Think on the worker.* The shipped personas think on the simulation
  thread, where thinks are 35–45% of the AI's work. An asynchronous persona
  plays the identical game (`TestAsyncHostPlaysTheSynchronousGameRetail`)
  and leaves the simulation thread the observation, the apply and a
  hand-off: per-tick AI time (wall, minimum of three runs) falls from 20
  to 14 µs on 4-player The Pass and from 100 to 66 µs on 8-player Comet
  Catcher (p99 804 → 757 µs), and paced at 30 ticks a second no think
  was late for its batch. The play set's personas are chosen in
  `mods/aikit`; the design says the shipped personas think synchronously
  (DESIGN_GAMEPLAY_RULES "The Modern AI controller").
- *Placement.* A factory search that finds no site still costs 2–5 ms of
  thread CPU on broken ground. Over half of it is the placement
  validator formatting an error message for each anchor it rejects; a
  yes/no form for searches belongs to `internal/world`.
- *Observation.* The session's line-of-sight predicate is about half of the
  observation; it copies its target description by value for every
  hostile unit (`internal/session`).
- *Utility.* `freeSafeN` and the defence plan's `observeFlow` recompute
  region answers every think that depend only on the terrain and where a
  unit stands (together about 7% of the AI's simulation-thread CPU in the
  8-player Comet Catcher match), and the brain asks again for a factory
  whose search just failed (14 failed searches in a 40-minute 2-player
  The Pass game, up to four within 1,000 ticks).
- *Placement and observation, measured after the fix (PERF2, 2026-09-25;
  every result, fingerprint and `state_hash` unchanged).* Searches ask
  `world.Terrain.PlacementLegal`, the validator's own gates without the
  message; formatting was 7–18% of a failed factory search here, not over
  half. Per search, minimum of three (The Pass) or two (Comet Catcher) runs
  of one build switching between the two forms, 40 minutes, 4 players: The
  Pass's 26 failed factory searches 1.16 → 1.08 ms (max 1.94 → 1.77),
  Comet Catcher's two 1.87 → 1.52 ms (max 2.20 → 1.80), all searches −6%
  and −4%. The rest is the row layout (`newRowSite`) and the exit guard
  (`sealedSite`, `exitPocket`, `macroFree` and the grids they allocate).
  The session now fills the line-of-sight query in place (13 rather than
  26 ns per query in cache; over two 8-player Comet Catcher matches, CPU
  building queries 1.86 → 0.80 s, frame publication's share 1.20 →
  0.38 s), but the observation's predicate is unchanged within noise
  (1.26–1.50 → 1.26–1.32 s a match): it waits on memory, and the copies
  ran in the shadow of its cache misses. The miss worth removing is in
  `internal/visibility`: the Community off-map test reads the movement
  filing for every target, though only an aircraft's answer uses it.
  Reading it only for a flying target (an uncommitted experiment, same
  game) took the predicate to 0.92–0.98 s and the observation from 2.30
  to 1.96 s a match (−26% and −15%).
- *Utility, measured after the fix (PERF-U, 2026-09-25).* The 40-minute
  8-player Comet Catcher match (`util+tac` hard, seed 1) unless named;
  "base" is 5ff86ca0, and the new build plays `fac_backoff=0` beside it
  so that both play one game. The host carried a load of 10–60 on 12
  cores.
  - *Region and territory answers* (result-identical: the arena set —
    hard, medium and easy against retail on three maps, seeds 1–2,
    27,000 ticks — and the 8-player match hash as before, an asynchronous
    persona plays the synchronous game, and the displayless `modern-ai`
    Great Divide seed 7 run keeps its `state_hash`). Profiled,
    the region lookups were the smaller part: a quarter of `freeSafeN` was
    `Reach.At` for each builder (one label read on ground it stands on),
    over half the two square roots of `spotTerritory` for every free spot.
    It now finds once what depends only on the map and the definitions —
    which spots each builder definition's extractors fit, which spots each
    constructor type counts from its home region — skips a spot that
    counts nowhere, tests threat before territory, and takes territory
    from each spot's kept distances to home (fixed) and to the believed
    enemy base (found again when the belief moves;
    `TestSpotTerrIsSpotTerritory`); a builder's region is kept while it
    stands still. `freeSafeN`, wall time summed over the eight players
    (an instrumented build, minimum of two runs): 412 → 122 ms a match,
    10.7 → 3.2 µs a call. `observeFlow` starts a unit's walk from the
    region its last walk ended in, one label read less per moving unit:
    84 → 71 ms (another pair of runs, 91 → 89: within the host's noise);
    its time is the pass over every own unit, not the lookups. CPU
    profile, two matches each (before the extractor fits were kept per
    definition): the think 4.96 → 3.96 s, the whole host step 14.3 →
    13.3 s.
  - *Per-handle tables.* Each grows once, on first use, to the owner's
    lowest observed handle plus the unit limit (its pool slice ends
    before that), rather than entry by entry as handles appear. Allocated
    (heap profile) 40 → 11.5 MB a match, the process's total 902 → 873 MB;
    with `-allocs` the host step's allocations 2,391 → 2,002 B a tick
    summed over the players, the thinks' 5,009 → 1,914 B (the rest is the
    tables' one allocation spread over the thinks). The live size is
    unchanged, about 12 MB for eight players within a live heap of about
    210 MB (sampled every 20 ms: peak in-use heap 387–404 → 383–398 MB,
    peak live heap 209–212 → 211–216 MB, three runs each): the tables
    still index by absolute handle from 0, because `defense_audit.go`,
    `opening.go` and `style.go` read the commitment and factory-register
    tables by handle. Indexing from the slice's first handle would take
    eight players' tables to about a fifth of that, but needs those
    readers changed.
  - *Per tick* (wall, per-tick minimum of three runs, the battle's first
    30 ticks — the preparation's join — left out):

    | map | players | mean µs | p99 µs | max µs | ticks > 1 ms | ticks > 4 ms |
    |---|---|---|---|---|---|---|
    | Comet Catcher | 8 | 78.6 → 75.3 | 765 → 754 | 1,737 → 1,559 | 62 → 69 | 0 → 0 |
    | The Pass | 2 | 17.6 → 17.3 | 238 → 235 | 1,414 → 1,384 | 22 → 22 | 0 → 0 |

    The saving `freeSafeN` accounts for is about 4 µs a tick in the
    8-player match; an earlier set of three runs each (before the
    extractor fits were kept per definition, under heavier load) read
    81.4 → 73.1 µs, p99 768 → 737 µs, 114 → 78 ticks over 1 ms. The ticks
    over 1 ms in The Pass are its failed factory searches, which the
    back-off below addresses.

  - *Factory search back-off* (`fac_backoff`, default 2, its own commit).
    Failed factory searches came back in two ways the brain's failure
    test did not see: another factory at the same request point in the
    same think (the factory products share the zone's point, and the
    failure count that moves it is read before the failure is noticed),
    and a builder that carried on with an earlier order (a failed build
    replaces no order, and only an idle builder counted as failed). When
    a factory build's batch reports a search that found no site and its
    builder is not building that factory at the next think, the request
    point rests for 600 ticks: `fac_backoff=1` for that factory (the
    proposal), `2` for every factory. Gate A (protocol v2 land pool, 192
    games each against `fac_backoff=0`): part 1 49.7% of points [49.2,
    50.0], 56-79-57, and no fewer failed searches (0.76 against 0.70 a
    player-game: the same factory's repeats fell, another factory at the
    point took their place); part 2 49.2% [47.9, 50.3], 55-79-58 (16/16
    commander kills), decisive-only 50.0%, margin sweep 49.7 / 49.2 / 49.5
    / 49.7%, and 0.60 against 0.74 failed searches a player-game (over
    1 ms: 0.58 against 0.71), repeats at a point within 600 ticks 44 → 2.
    Play changed in 17 of the 96 map-seed pairs, where part 2 took 45.6%
    of 34 games. The 40-minute 2-player The Pass game (per-tick minimum
    of three runs): 16 → 12 failed factory searches (part 1: the same 16,
    an unchanged game), their time 25 → 21 ms, ticks over 1 ms of AI work
    27 → 15, the largest 1.77 → 1.42 ms. At the default the arena set and
    the 8-player match above still hash as before and the `modern-ai` run
    keeps its `state_hash`: no back-off changed those games. What remains:
    a point asked again after its 600 ticks (The Pass: 930–1,200 ticks
    apart), and two builders sent to the same factory in one think, whose
    searches share a batch.

### 5.3 Faster tournaments (2026-09-25)

Protocol v2 took 465 s for Gate A (192 games of 20 minutes), 2,587 s for
Gate B (two pairs, 192 games of 40 minutes) and 7,317 s for a 9-variant
ablation (432 games of 40 minutes). All three ran with four niced processes
on a 12-core host (6 performance cores, 6 efficiency cores) shared with
other agents. Protocol v3 plays the same questions for a fraction of that
cost, and it reads its verdicts on an interval that holds its error rate.
Each part below was measured before it was adopted. The evidence is the
match files of every earlier protocol v2 tournament (random starts,
map-mixed seeds): 2,504 distinct 40-minute games and 15,678 distinct
20-minute games in 187 pairings.

Those files keep every player's metric series, sampled every 150 ticks
(only `summary.json` drops it). A match is deterministic, so the first
*t* ticks of a full-length game are the same ticks a shorter or
adjudicated match plays. Rules for ending a game early can therefore be
tested on recorded games without replaying them.

**Where the time goes.** A game's cost grows with the armies. Per tick,
wall time fits *a* + *b*·units², with *R*² 0.78 to 0.90 on the three runs
above. In the 40-minute pool, Plains and Passes and Greenhaven take 71% to
72% of the time. By that model, the first 20 minutes of a 40-minute game
cost about 19% of it. A CPU profile of a 40-minute Greenhaven game broke
down as follows:

- frame publication, 36%;
- the simulation's phases, 56%;
- the collector, under 1% of wall time (0.3% to 0.8% across the three
  runs);
- the AI's host steps, 2% to 6% of wall time.

**Per-game cost: no frame publication (adopted).** The arena has no
presentation consumer, and nothing authoritative reads a published frame
[I6]. A match now drops the session's frame buffer after composition, so
the session skips publication. `match -publish` restores publication, for
checking.

- *Same games.* Six 40-minute games and twelve 20-minute games hash
  identically with and without publication once their timing fields are
  removed. `TestArenaSkipsPublicationAndAdjudicates` (retail) locks this.
- *Faster.* Run side by side, the Greenhaven game took 166 s of user CPU
  with publication and 93 s without. At two processes each, the six
  40-minute games took 856 s of summed wall time with publication and 467 s
  without (−45%). The twelve 20-minute games took 448 s and 294 s (−34%).
- *Setup.* Setting up a match (content, composition, map analysis) costs
  about 1.3 s of CPU, a tenth of a 20-minute game. Matches do not share it;
  a process that played several matches could.
- *Collector.* `GOGC` of 100, 200, 400 and off with a 1 GiB limit gave 88.2,
  88.2, 88.0 and 91.2 s of CPU on the same game, with identical results.
  Collector tuning was not adopted.
- *What remains.* The remaining cost is simulation work that belongs to the
  engine: path search (27% of the CPU), unit scripts (15%), economy (10%),
  allocation (about 10%), feature motion (5%) and the AI's host (5%).
- *Measurement.* Results record `host_timing.process_cpu_seconds`, the
  match process's CPU time. The "quiet host" figures below are that time
  divided by the process count.

**Scheduling (adopted: longest first; opt-in: slots, auto jobs).** In v2 the
last games to finish added this much after the rest were done (tournament
wall time minus the summed game time divided by four):

- Gate A: 44 s (9%);
- Gate B: 51 s (2%);
- the ablation: 127 s (2%).

`tournament -order longest`, now the default, starts the games expected to
take longest first. The estimates are a measured per-map table at 20 and
40 minutes. The order changes when a game is played, never the game or its
id.

`-jobs auto` is opt-in: it takes the host's free cores, logical CPUs less
the one-minute load, at most four. Match slots are shared by default (user
decision, 2026-09-25):

- `-slots auto`, the default, shares half the host's logical CPUs among
  every tournament on the host, through a pool in the user's cache
  directory (`nanolathe/arena-slots`). A tournament still runs at most
  `-jobs` games of its own.
- `-slots dir:n` shares *n* match slots among every tournament that names
  the same directory, and `-slots off` shares none. The pool uses exclusive
  locks on *n* files, which are released when the holder exits.

During the runs below, the agents' tournaments together kept about 22
match processes (four of them these runs') on the 12 cores, at load 90 to
150. Each process got 50% to 65% of a core. A pool that every agent's
tournament joins would remove that oversubscription. It works only if
every tournament uses it, so it is not a v3 default.

**Adjudication (adopted for 40-minute games: `-adjudicate 2.5,1,10`).** A
match ends as the leader's win, with reason "adjudicated", when three
conditions hold:

- the leader's score (the match's score kind, read from the samples) is at
  least *ratio* times every other live player's score;
- the lead has held at every sample of the last *minutes*;
- the game has reached minute *from* or later.

A score below zero counts as zero, and the leader's score must be positive.
Each rule was replayed on the recorded games. It stops a game at the first
sample where it holds, before the game's own end. A disagreement means the
adjudicated winner is not the recorded result, whether that result was the
other player or a draw. Rules were tried on the default score, army plus
economy value and army value, with ratios from 1.5 to 4, windows of 1 to
5 minutes and start minutes from 5 to 30. Selected rules at 40 minutes
(2,504 games: 1,362 commander kills, 664 points wins, 478 draws):

| rule (score) | adjudicated | disagree (95% upper) | of which draws | cost saved (model) |
|---|---|---|---|---|
| 1.5, 1, 10 | 80.5% | 14.7% (16.3%) | 207 of 296 | 58% |
| 2, 3, 10 | 38.3% | 2.71% (3.95%) | 21 of 26 | 17% |
| **2.5, 1, 10** | **41.6%** | **2.11% (3.18%)** | **18 of 22** | **13%** |
| 2, 5, 10 | 24.5% | 2.12% (3.59%) | 12 of 13 | 12.5% |
| 3, 1, 10 | 32.0% | 1.12% (2.12%) | 8 of 9 | 7% |

The adopted rule changes 0.88% of all games. It changed no pairing's t90
verdict (defined below) among 36 complete 40-minute pairings. The points
share moved by a mean of −0.05 points (mean absolute 0.49, at most 2.1).
The rule 2, 3, 10 saves more, but its upper bound on disagreement exceeds
3%.

At 20 minutes a game has no expensive late half to cut. The rules within
the 3% limit save 1.5% to 5.5%, and the five that save more than 3% change
the verdicts of 1 to 3 of 143 pairings, so 20-minute games are played
out. The "cost saved" column uses the fitted per-tick cost model, not a
measurement.

**Sequential gates (adopted).** A spec's `"sequential": {"looks": [2, 4, 6,
8]}` plays the seeds in blocks. A block is one seed on every map, in both
slot orders. Every look sees every map with the slot order balanced, so
the slot-0 bias cancels inside each block. At each look the runner
computes these statistics:

- A's mean points per block;
- the mean of those block means on each map;
- the share, which is the mean of the map means;
- Student's *t* on the spread of the map means, with maps − 1 degrees of
  freedom.

Games on one map are not independent evidence. Across the recorded
pairings, the block scores of one map are correlated: the median
intraclass correlation is 0.08 at 20 minutes and 0.16 at 40 minutes, and
the mean is 0.17 at both lengths.

A look decides "better" or "worse" when |*t*| reaches the look's
O'Brien–Fleming bound. That bound is the nominal level for a two-sided
10% test over the planned looks, computed by integrating the boundary
crossing on a grid and read on the *t* distribution. A look before the
last stops with "no difference" (futility) when the chance of a verdict at
the last look, on the current trend, is below 0.20. A decided pair's
queued games are dropped, and its games already running past the look are
killed. `sequential.json` records every look, the verdicts and the games
that were left out. Both summary tools leave those games out.

Operating characteristics were measured by replaying the recorded
tournaments. Each pairing was replayed with 40 random seed orders against
the fixed-sample *t* verdict on all its seeds. Null tournaments were built
from the same pairings by flipping the sign of each map's effect and of
each block's residual at random. Futility is 0.20, and 40-minute games
were adjudicated with 2.5, 1, 10.

| pool | pairings | same verdict | games used | null: false verdicts | null: games used |
|---|---|---|---|---|---|
| Gate A: 20 min, 12 maps × 8 seeds, looks 2/4/6/8 | 31 | 95.6% | 53% | 8.5% | 54% |
| Gate B: 40 min, 6 maps × 8 seeds, looks 2/4/6/8 | 8 | 96.2% | 51% | 8.4% | 54% |
| ablation: 40 min, 6 maps × 4 seeds, looks 2/4 | 20 | 89.8% | 77% | 9.5% | 71% |
| 20 min, 12 maps × 4 seeds, looks 2/4 | 58 | 98.8% | 77% | 9.3% | 72% |

**Verdicts on t90, not the bootstrap (adopted).** The same null
tournaments show that protocol v2's map-cluster bootstrap percentile
interval overstates significance:

| maps | bootstrap (nominal 10%) | *t* on map means (nominal 10%) | null tournaments |
|---|---|---|---|
| 6 | 18.8% | 10.0% | 720 |
| 12 | 15.8% | 10.4% | 1,780 |

With only 6 or 12 clusters, the bootstrap underestimates the spread.
`tools/ai-arena-summary` now prints `t90` beside the bootstrap: the share
of map means with its 90% *t* interval, and "yes" when that interval
excludes 50%. For example, Gate B's 40-minute night0 against hard is
41.1%. Its bootstrap interval is [34.4, 47.4], a loss, and its t90 is
[32.0, 50.3], no verdict.

**Continuous outcomes (not adopted).** Each metric was compared with the
points share on the same pairings, as the standardized effect (estimate
over map-cluster standard error):

| metric | pairings | sign agrees | median \|z\| ratio to points | \|z\| larger |
|---|---|---|---|---|
| final score share (dead = 0) | 143 at 20 min, 37 at 40 min | 93%, 92% | 0.94, 0.97 | 50%, 49% |
| log score ratio | same | 92%, 92% | 0.81, 0.95 | 40%, 46% |
| log value-killed ratio | same | 82%, 78% | 0.60, 0.56 | 41%, 27% |
| points, draws split by score share | same | 99%, 100% | 1.01, 1.01 | 55%, 62% |

No metric is consistently narrower than the points share, so none is
adopted.

**Screening (adopted: at 40 minutes, not 20).** The first 20 minutes of the
recorded 40-minute games replay a 20-minute screen at 19% of the cost. It
does not predict the 40-minute results:

- the shares correlate at 0.82, but the sign agrees in only 29 of 36
  pairings;
- the screen shrinks effects: across the 36 pairings a 20-minute effect is
  0.73 times the 40-minute one (least squares), and more for late switches.
  In the ablation, no-raids was 37.5% at 40 minutes and 46.9% at 20, and
  no-style was 58.3% and 51.0%;
- the screen found none of the three significant 40-minute losses in the
  corpus.

A switch that pays late therefore looks neutral at 20 minutes. The
ablation instead screens at 40 minutes with the looks [2, 4]. Replayed on
the recorded 9-variant ablation (seed order as specified, adjudicated), it
reached the fixed four-seed verdict for 9 of 9 switches and played 26 of
the 36 seed blocks. The five switches with no trend stopped after two
seeds. A finalist that needs more evidence than four seeds can give is run
as a Gate B.

**Protocol v3.** The templates are
`cmd/ai-arena/testdata/protocol-v3/*.json`: replace the pair and keep the
rest. All of them use map-mixed seeds, both slot orders, random starts and
no publication (the match default).

| spec | maps | seeds | length | adjudication | looks | games (maximum) |
|---|---|---|---|---|---|---|
| `v3-gate-a` | 12 land | 8 | 20 min | none | 2/4/6/8 | 192 |
| `v3-gate-b` | 6 long | 8 | 40 min | 2.5, 1, 10 | 2/4/6/8 | 96 per pair |
| `v3-ablation` | 6 long | 4 | 40 min | 2.5, 1, 10 | 2/4 | 48 per variant |

Run a tournament with `nice -n 12 ai-arena tournament -spec S -out D -jobs 4`
(it joins the host's shared slot pool by default), then
`tools/ai-arena-summary D`.

**The long pool (user decision, 2026-09-25).** Plains and Passes and
Greenhaven cost 63–72% of the 40-minute pool's time, and dropping them
would leave too few maps for a verdict (below). The v3 long pool therefore
replaces them with Red Planet and Full Moon, land maps of the Gate A pool
whose 20-minute games cost about 10 s, like The Pass and Sherwood: The Pass,
Show Down, Red Planet, Full Moon, Sherwood, Great Divide. The timings and
replays in this section were measured on the old pool.

Each gate reads its sequential verdict for A, the pair's first contestant:

- Gate A passes unless the verdict is "worse" (no loss).
- Gate B passes on "better" (a gain).
- An ablation reports every switch's verdict.

Report the t90 interval with the decisive-only share, the margin sweep and
the style-pairing count. The bootstrap interval stays in the summary only
for comparison with v2 results.

**Measured.** Each v3 run below was played with four niced processes on
the shared host, at load 30 to 150 on 12 cores (each process got 52% to
66% of a core). "Quiet host" is the run's summed process CPU divided by
four. That is an upper bound: CPU time itself grows under contention. The
demonstration's 48 games used 1,958 s of CPU at load 125 to 150; replayed
at a lighter load (87% of a core each), the same games at full length used
1,657 s. The per-tick cost model says the cut games do 85% of that work,
so contention inflated their CPU time by a factor of 1.38. "v2, same games"
is what protocol v2 would cost on the same pairs and code, at the run's
own CPU rate: every game played to its end, and publication added back.
The factors are the demonstration's measured work per game (adjudication
saves 14.6% of a 40-minute game's work) and publication's measured share
(CPU ×1/0.55 at 40 minutes, ×1/0.66 at 20).

| run | games played | v3 wall, loaded | v3 quiet host | v2, same games, quiet host |
|---|---|---|---|---|
| Gate A: hard vs bal, 20 min | 144 of 192 | 584 s | 347 s | 701 s |
| Gate B: hard vs bal, 40 min | 48 of 96 | 960 s | 490 s (324 s at the lighter load) | 2,135 s (1,411 s) |
| 9-switch ablation, 40 min | 312 of 432 | 5,574 s | 3,131 s | 9,230 s |

For reference, protocol v2 as run earlier (other pairs, older code, a
lighter load) took 465 s for Gate A, 2,587 s for two Gate B pairs and
7,317 s for the ablation.

Gate A and a Gate B pair now finish well under 30 minutes, on a loaded
host and on a quiet one. The 9-switch 40-minute ablation does not. On a
quiet host it would take at most 52 minutes of four cores, and about 38
if its CPU was inflated as the demonstration's was. Plains and Passes and
Greenhaven are 63% of its CPU. Two alternatives were replayed and
rejected:

- dropping those two maps leaves 4 maps (3 degrees of freedom); the
  screen then almost never reaches a verdict (1.2% of replays);
- stopping at 25 or 30 minutes costs 34% or 54% of the 40-minute games,
  but still misses 2 of the 3 significant 40-minute verdicts.

What does fit in 30 minutes is a batch of four or five switches, or all
nine on six quiet performance cores. A futility level of 0.3 instead of
0.2 would play 13% fewer ablation games (Gate B: 44% of games instead of
51%) at the same agreement in the replays. The runs used 0.2, which
remains the default.

**Demonstration.** The Gate B pair `util+tac:hard` against
`util+tac:hard::style=balanced,jitter=0,label=bal` was played under v3
(`v3-gate-b` with that pair):

- *Stopped at the second look.* After 48 of the 96 games, hard took 32.3%
  (*t* −4.71 against a bound of 3.68; the first look, at 31.2%, had
  *t* −3.00 against 7.88). The verdict is "worse" for hard: bal took
  67.7%, t90 [60.1, 75.3].
- *Time.* 960 s of wall time at load 125 to 150, including three games of
  the third look that the decision killed. Of the 48 games, 25 were
  adjudicated.

The same 96 games were then played at full length by the same binary,
without adjudication or stopping (916 s at a lighter load):

- *Same games.* Every one of the 48 games v3 played matches the full run.
  The 23 that were not adjudicated hash identically, and the 25 that were
  are exact prefixes of the full games, all with the full game's winner.
- *Same verdict.* Over all 96 games, bal took 62.0%: t90 [56.5, 67.5]
  (bootstrap [57.8, 66.1]), 33 to 20 in commander kills. v3 reached the
  same verdict with 42% of the full run's work: half the games, and
  adjudication saving 14.6% of those games' work.
- *Slot bias.* The slot effect is plain in both runs: bal took 79.2% of
  its games in slot 0 and 44.8% in slot 1. The blocks balance it.

For comparison, the older 40-minute ablation had the same switch
(`no-style`, four seeds, earlier code) at 58.3%, bootstrap [50.0, 65.6].
At 20 minutes (the Gate A run above) bal took 55.6%, t90 [52.6, 58.5],
after 144 of 192 games. In the v3 ablation, the same switch is the only
one of the nine with a verdict.

## 6. Results (2026-09-23)

Full report with charts and replays: the "Commander AI Trials" artifact
(generator in the research branch's `tools/ai-report/`). Summary:

* **Round robin**: 1,344 games of 20 minutes, 8 contestants, 12 maps, seeds 3–4, both
  slot orders, hard personas. Bradley–Terry rating vs retail (90% bootstrap):
  `util+tac` +646 [590, 716], `plan+tac` +535, `utility` +496, `planner` +402,
  `tactics` +337, `mcts-fp` +240, `scripted` +215, `retail` 0.
  `util+tac` vs retail 47–1–0 (32 commander kills); vs `plan+tac` 24–13–11.
* **Cost** (hard, round-robin median of per-game means): `util+tac` think 54 µs,
  host step 7.9 µs; `plan+tac` think 1.3 ms. Real-time paced (30 Hz, quiet): the
  heavy planner's simulation-thread cost falls from 159 µs mean / 13 ms worst (sync)
  to 3.9 µs / 2.1 ms (async); four-player FFA: whole-AI share of a tick 5.2%
  (`util+tac`) vs 13.2% (retail).
* **Determinism**: sync and async hosts, and MCTS with 1/2/4 workers, produced
  identical games in every pair compared.
* **Difficulty**: personas as defined give a shallow ladder (easy 40% of points vs
  hard); harsher execution limits (5 s looks, 5 actions/min) reach retail strength.
  An "ambition" dial is recommended alongside.
* **Fairness**: an omniscient `util+tac` takes 61% of points against the fair one.
* **Tuning**: six cross-entropy generations (960 games of 15 minutes) over 14
  utility weights; the tuned `util+tac` beat the defaults 22–16–10 (62% of
  points, 90% interval 53–72%) on all twelve maps with fresh seeds, the same on
  training and unseen maps. The search moved toward fewer constructors and
  towers and a larger army share; per-candidate noise limits it (NTBEA and a
  league are the next step).
* **Known gaps**: uniform-metal maps, reclaim (no features in the observation),
  tech 2, naval/air task forces; map analysis should move to battle load (one-time
  ~7 ms per AI player on the first tick).

## 7. Playing against it

`mods/aikit` installs `util+tac` as the Modern AI, which the skirmish and
Survival screens choose per computer row: a row made Computer starts on the
Modern AI ("Modern AI" on its name button), the next click makes it the
Classic AI, in any gameplay mode. The lobby's difficulty picks the persona
(easy, medium or hard). Unlike retail, the difficulty does not also cut a
Modern AI player's income (retail pays easy 50% and medium 70% of its
production [05 R-ECO-01 §3]): it is paid in full in every rule set
(DESIGN_ECONOMY_CONSTRUCTION "Modern AI full income"), so the persona alone
decides how well it plays; the arena's `aikit` set pays every contestant in
full the same way. A battle the command line composes marks rows with
`--ai-player`:

```
go run ./cmd/nanolathe --map "Great Divide" --ai-player all=modern
```

The rows are saved with the other settings (a Classic row as `"ai":
"classic"`; a row without the word plays the Modern AI). A save keeps which
computer players are Modern and each one's battle seed, generator position
and configured parameters, so a loaded game draws the same styles; the
brain's memory and builder tasks are not part of the save
(DESIGN_SESSIONS_AI_SAVE "Modern AI computer player", "Saves").

**Configuring it.** The brain takes the arena's `util+tac` player-spec keys
(one builder, `mods/aikit` `NewUtilTac`, serves both), from the settings
file's `modernAI` block — for every computer player, per difficulty and per
lobby slot, the most specific winning — or, for a quick test, from
`--ai key=value,...`, which sets every computer player:

```
go run ./cmd/nanolathe --map "Great Divide" --ai-player all=modern --ai style=tower,jitter=0
```

By default each game plays the balanced style, jitters and draws a
personality around it (§8); `style=<name|random>` plays or draws an
opening archetype, `personality=<name|off>` plays a personality archetype
or none, and `jitter=0` stops the jitter and the personality's draws. The block's shape, the
precedence, where each layer's keys are listed and the strict check are in
DESIGN_SESSIONS_AI_SAVE "Modern AI computer player", "Configuration".

## 8. Variety and ambition (utility brain)

Two dials, both expressed as perturbations of the utility brain's `Params`
(`internal/aikit/brains/utility/style.go`), so every consideration keeps its
meaning, and a per-game personality drawn on top of the style (below).
`style=balanced,jitter=0,personality=off` at ambition 100 is the tuned
deterministic brain game for game (checked: identical result JSON) and
draws nothing from `Kit.Rand`. Both are calibrated on the human benchmark
(`tools/ai-human-bench`, report sections 3 and 9): 2,612 recorded ProTA 1v1
games, their six opening archetypes and per-skill-tier curves, and the
original-TA (stock unit) extracts.

**Styles are human opening archetypes.** When the brain starts (its
`Init`, on the first think's tick) the strategy layer takes the named
archetype, or with `style=random` draws one from the player's generator at
the share humans play it, and multiplies the listed parameters (percent)
before the shared model derives its per-definition tables. The default is
`balanced` since 2026-09-25 (it was `random`): over 432 40-minute games
the drawn styles took 43.6–62.9% of the points by style, expand (the most
drawn) 43.9% and tower 43.6%, and `style=balanced,jitter=0` took 58.3%
[50.0, 65.6] against the drawn default (48 games; the pass, show down,
plains and passes, greenhaven, sherwood, great divide; seeds 301–304). The
default's variety now comes from the personality (below). The perturbations are the benchmark's:
each parameter scales with the archetype's median of the matching quantity
relative to the whole corpus (fac_time with the first factory's time;
w_factory, w_cons, w_metal, w_energy, w_defense with the counts built by
minute 10; w_army with army value by minute 10; w_tech with the share on
tech 2 by minute 15; w_air with the share owning an air plant by minute
10). tech_time carries the inverse of w_tech, because the timed tech-2
transition reads it instead.

| style | archetype (human share, win rate) | weight | perturbations | lead (energy before the first extractor) |
|---|---|---|---|---|
| balanced | the tuned defaults, never drawn | 0 | none | none |
| expand | O1: factory at build 6, then constructors (30%, 52%) | 30 | fac_time 95, w_cons 110, w_defense 115, w_tech 115, tech_time 85 | 0: 9, 1: 83, 2: 8 |
| eco | O2: energy and extractors first, factory at build 9 (25%, 52%) | 25 | fac_time 115, w_factory 135, w_cons 120, w_metal 145, w_energy 140, w_defense 145, w_army 105, w_tech 125, tech_time 80, w_air 90 | 1: 70, 2: 30 |
| units | O3: factory at build 6, combat units first (23%, 48%) | 23 | fac_time 90, w_cons 65, w_metal 85, w_energy 75, w_defense 55, w_army 110, w_tech 55, tech_time 180, w_air 95 | 0: 9, 1: 83, 2: 8 |
| tower | O4: factory at build 6, an early tower (15%, 46%) | 15 | fac_time 90, w_metal 85, w_energy 95, w_defense 130, w_army 85, w_tech 90, tech_time 110, w_air 105 | 0: 9, 1: 83, 2: 8 |
| twofac | O5: factory at build 5, a second soon, often air (4%, 49%) | 4 | fac_time 80, w_factory 135, w_cons 90, w_metal 85, w_energy 80, w_defense 70, w_army 90, w_tech 90, tech_time 110, w_air 195 | 0: 9, 1: 83, 2: 8 |
| greedy | O6: no factory in the first twelve builds (3%, 52%), half strength | 3 | fac_time 125, w_factory 115, w_cons 145, w_metal 145, w_energy 135, w_defense 125, w_army 90, w_tech 125, tech_time 80, w_air 85 | 1: 40, 2: 40, 3: 20 |

The **opening lead**: humans open with an energy building 90% of the time
(first three builds EMM 35%, EME 22%, EEE 18%, EEM 13%; original TA EEE 30%)
where util+tac always opened with an extractor. While fewer energy
buildings than the drawn lead exist (built, framed or committed), in the
first three minutes, the extractor, maker and factory weights are zero for
that think. Two archetypes are moderated to hold the competence floor: in
the stock game every energy building before the first extractor cost
util+tac early income, and eco with the humans' EEEE lead scored 44%
against the baseline jitter off (lead two 35%, lead one 48%, 24 games each;
25–32% in 14 random draws mixing two to four), so eco mostly leads with
one; O6 at full strength scored 34% over 48 games (its army at minute 10 is
half the baseline's) and plays at half strength (the geometric mean of the
human perturbation and none), 46%.

**Jitter** (on by default) draws the lead, varies the opening per game —
fac_time ±25%, eco_early ±8%, w_cons ±15%, w_energy and w_metal ±10%, v_ref
±30%, w_mix ±20%, w_defense ±20%, eco_ramp ±15% — and sizes each wave at
80–125% of the strategy's attack value, redrawn when a wave has been spent
(the army fell below half of it after reaching it). Without jitter the most
likely lead is played. Values are clamped to each parameter's range.

**Ambition is the percent of the top human tier's plan.**
`Persona.Ambition` (1..100; 0 means unset and normalizes to 100): hard and
max 100, medium 80, easy 35. Below 100 the brain holds, every think,
extractors, constructors, factories and army value to its ambition percent
of the top skill tier's curve, by zeroing for that think the weight that
would add one more (w_metal and w_maker, w_cons, w_factory, w_army). The
curves are the benchmark's top-tier linear fits over minutes 3–15 (ProTA,
counts built), converted to stock alive counts by the ratio of original-TA
winners' alive counts to ProTA winners' built counts at minutes 10 and 15:

| count | top tier (ProTA, built) | stock factor | curve (stock, alive) | floor |
|---|---|---|---|---|
| extractors | −1.7 + 1.74/min | 0.58 | −0.99 + 1.01/min | 1 |
| constructors | −2.7 + 1.31/min | 0.63 | −1.70 + 0.83/min | – |
| factories | −0.02 + 0.27/min | 0.88 | −0.02 + 0.236/min | 1 |
| defenses | −8.0 + 1.81/min | 0.70 | −5.6 + 1.27/min | – |
| army value | −753 + 222/min (fit of the medians) | 1.08 | −814 + 239/min | 200 |

The lower tiers sit at 77% (low) to 87% (mid) of the top tier's income and
extractors at minute 10, so medium's 80 is the low-to-mid tiers and easy's
35 about half the low tier. Counts include nanoframes, builders walking to
a committed site and constructors queued at a factory; the army cap lifts
while the base is threatened. **Nothing banks:** when the metal store is at
least 60% full and income covers expense, the capped plan has nothing left
to buy, so towers are wanted (w_defense ×3, at least 180) up to the tier's
defense curve and the army cap lifts. Ambition also scales, once: w_tech
(to 0, quadratically), tech_time (up to 2.5×), travel_half (to 60%),
com_radius (to 70%), att_min (to 50%) and att_grow (to 40%). att_min and
att_grow now set only the army production pushes toward; the tactics army's
launch threshold (`Posture.AttackValue`) is 45% of the ambition-scaled army
curve, at least 300, read earlier or later by each style's human
first-unit lag (`attackValue` in style.go; `att_curve=0` restores the old
value).

**Personality** (P2, 2026-09-25; `internal/aikit/brains/utility/README.md`
§13.15). Eight traits from −100 to 100 — aggression, raids, towers,
expansion, tech, heavy, air, scouting — each moving parameters the brain
already has (the attack value's launch share and the army's margins, the
raid squad's and harass raid's sizes, w_defense, w_cons and w_metal,
tech_time and w_tech, v_ref and w_range, w_air, w_scout) between
measured strength-neutral extremes, so the same brain raids, builds towers
and expands in every game at rates that differ from game to game. By
default each trait is drawn on its own, triangular on ±100 (the sum of two
uniform draws in ±50), after the style, lead and jitter draws and only
with jitter. Six named archetypes (rusher, raider, turtle, boomer, tech,
flyer) are played only when configured.

Arena switches: `style=<balanced|expand|eco|units|tower|twofac|greedy|random>`
(default balanced), `jitter=0|1` (default 1),
`personality=<random|off|rusher|raider|turtle|boomer|tech|flyer>` (default
random), `trait_aggression`, `trait_raids`, `trait_towers`,
`trait_expansion`, `trait_tech`, `trait_heavy`, `trait_air`,
`trait_scouting` (−100..100; each pins its trait), persona override
`ambition=1..100`. `util+tac` reports in the result's `extra`: `style`,
`open_lead`, the first ten definitions built (`open_NN_<unit>`,
`open_hash`), `first_push_tick` (three combat units past 60% of the way to
the enemy base), how many thinks each cap held (`capped_*`), `banking`,
`lead_thinks`, `personality_mode` (0 none, 1 drawn, 2 named),
`personality_<name>` (a drawn personality is named by its strongest lean,
`steady` below 30) and every `trait_<name>`; the tactics army adds
`tac_raid_launches`.

### Results (V2, 2026-09-23)

Persona ladder (util+tac, 20-minute games, the twelve-map pool, both slot
orders, 48 games per pairing, seeds 64–65; points = (W + D/2) / games; the
hand styles and quadratic ambition of V1 are "before", on the same code
otherwise):

| pairing | before (V1 on this code) | after |
|---|---|---|
| easy vs retail | 18-11-19, 49.0% | 23-12-13, 60.4% |
| medium vs retail | 41-6-1, 91.7% | 41-5-2, 90.6% |
| hard vs retail | 48-0-0, 100% | 48-0-0, 100% |
| easy vs hard | 0-1-47, 1.0% | 0-2-46, 2.1% |
| medium vs hard | 7-9-32, 24.0% | 2-7-39, 11.5% |
| metal wasted, mean share of production (easy / medium / hard) | 13.2% / 20.4% / 5.4% | 1.5% / 2.2% / 4.8% |

(The first run of this design, with easy at ambition 40, gave easy vs
retail 59.4% and medium vs hard 15.6%. The medians of wasted metal are
0.2–0.3% for every persona after; hard's mean comes from a few winning
games that float at the end.)

Persona curves against the human tiers (median over the ladder games and
land-pool mirrors; stock-scaled human target: hard = top tier, medium =
mean of low and mid tiers, easy = half the low tier; "a/b" = util+tac /
target):

| persona | quantity | 5 min | 10 min | 15 min | 20 min |
|---|---|---|---|---|---|
| easy (½ low tier) | metal income /s | 3.0/3.4 | 7.0/6.5 | 11.0/9.4 | 16.0/13.3 |
|  | extractors | 1/1.7 | 3/3.8 | 5/5.8 | 7/7.5 |
|  | constructors | 0/0.9 | 2/2.2 | 3/3.5 | 5/5.0 |
|  | factories | 1/0.4 | 1/0.9 | 2/1.3 | 2/1.8 |
|  | army value | 248/177 | 582/733 | 979/1306 | 2147/1973 |
| medium (low–mid tiers) | metal income /s | 6.0/7.2 | 12.0/13.9 | 20.0/20.7 | 26.0/29.7 |
|  | extractors | 3/3.5 | 6/7.8 | 10/12.2 | 14/15.7 |
|  | constructors | 1/2.2 | 3/5.0 | 6/8.2 | 10/12.3 |
|  | factories | 1/0.9 | 2/1.8 | 3/3.1 | 4/4.0 |
|  | army value | 295/368 | 796/1611 | 2068/2966 | 4004/4477 |
| hard (top tier) | metal income /s | 9.0/8.4 | 22.0/16.8 | 25.0/27.2 | 23.0/39.8 |
|  | extractors | 5/4.1 | 12/9.3 | 17/13.9 | 17/18.0 |
|  | constructors | 1/2.5 | 4/6.3 | 6/11.3 | 7/17.6 |
|  | factories | 1/0.9 | 3/2.6 | 5/3.5 | 5/5.3 |
|  | army value | 218/336 | 1340/1458 | 3874/2930 | 6699/4890 |

Before, easy's income was 6.0 / 7.5 / 9.0 / 10.0 with one constructor and
one factory all game and an army of 850 at minute 20, and medium matched
hard's income (16 / 25 at minutes 10 / 15) while floating a fifth of it.
Medium's army trails the low-to-mid tiers at minute 10 (humans of every
tier field about the same army; the capped economy pays for less of one).

Style competence against the deterministic baseline at hard (jitter off,
both slot orders, twelve maps): expand 56.2%, units 50.0%, tower 60.4%, twofac 50.0% (24 games each,
seed 53), eco 43.8% and greedy 45.8% (48 games each, seeds 53–54; numbers
are the archetype's share of points). The default (random archetype,
jitter) against the baseline with a distinct seed per game: 20-11-17, 53.1% of points over 48 games (draws expand 13, eco 14, units
12, tower 6, twofac 3; greedy none).
Openings in those 48 random games: first build energy 46 of 48 (was 0); first
three EMM 37, EEM 6, EME 2, MEM 2, EMF 1 (humans: EMM 35%, EME 22%, EEE 18%, EEM 13%).

Variety across ten seeds on one map, util+tac:hard against the baseline
(deterministic → V1 hand styles → archetypes): great divide, distinct first-ten build orders 7 → 8 → 9, first push
spread 4.4 → 4.2 → 3.5 minutes, distinct structural openings
(first-factory position, constructors, towers, tech 2) 6 → 8 → 5; the pass,
build orders 7 → 9 → 9, first push spread 3.1 → 6.5 → 3.1 minutes. The archetypes
are mild variations on one plan, as human openings are (three of six sit
nearest the balanced style in the benchmark), so they vary less than the
hand styles did; the largest visible change is the energy-first opening.

Cost: unchanged. One synchronous red planet match with `-allocs`, hard mirror:
think mean 55 µs before and 55–63 µs after, p99 175–193 µs, 116–555 B and
under 0.11 objects per think either way; easy and medium 45–51 µs. Repeated
runs and the asynchronous host produced identical results, and
`style=balanced,jitter=0` at hard reproduced the pre-change result JSON.

History. V1 (the same day) drew six hand-set styles (balanced, rush, boom,
turtle, tech, swarm) and read ambition as a quadratic plan size with fixed
caps; it met its strength targets (easy 53–60% of points against retail)
but floated a fifth of the capped personas' metal, and its styles were far
from any human archetype (log distance 0.6–1.5).

### Gaps

* **First-factory family.** util+tac's first factory is the vehicle plant
  in every game: the family is decided by factory quality, which v_ref and
  w_air do not change (checked at v_ref 20–1000 and w_air up to 1000).
  Humans pick kbot 53% / vehicle 36% / air 9% on ProTA land maps and
  vehicle 63% / air 20% / kbot 10% / ship 7% in original TA. A new economy
  parameter is specified for the economy owner: `fac_first` (0..4, default
  0 = today; 1 kbot lab, 2 vehicle plant, 3 air plant, 4 shipyard) and
  `w_fac_first` (percent, 100..1000, default 300), multiplying the
  suitability of factories of that family in `evalFactory` and
  `evalFactoryN` while the brain owns none of that family (built, framed or
  committed). A factory's family is the majority family of its combat
  products: air (RoleAir), ship (a water unit or RoleNaval), otherwise kbot
  when the product definition's movement class names a KBOT class and
  vehicle (hovercraft included) otherwise. The style would then draw it per
  archetype from the benchmark's shares.
* **O4's early tower** (first tower at minute 2.3 in the human data) needs
  the proactive defense plan (`def_plan`); the reactive evaluation builds no
  tower before minute 4 without a threat, so tower differs from expand only
  in weights.
* **hard below the top tier after minute 15.** Hard (uncapped) matches the
  top tier's stock curve at minute 10 but its extractors and constructors
  plateau (constructors 4–6 against 11 at minute 15); that is the expansion
  model (report section 10 (d)), not the caps.
* Closed in round 2: the tactics army used to ignore
  `Posture.AttackValue`. The main squad now holds full offensives until the available army reaches it
  (`posture=0` restores the old army), and the value follows the tier curve
  (tactics README, "Attack posture").
