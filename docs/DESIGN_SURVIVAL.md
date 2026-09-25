# Design — Survival

A single-player mode with no victory. The human, with up to two allied
computer players, starts near the middle of a map, builds an economy and a
defended base, and then holds out against **waves** of attacking units that
arrive from the map edges. Waves grow and climb the build tree over time; the
battle ends when the human is defeated, and the result is a score.

**Status: implemented; tuning continues from play-testing.** The
maintainer's decisions of 2026-09-23 are in §2. Proposals awaiting
confirmation are in §14. Every tuning value is collected in §7 and is a
Survival design choice, not retail behaviour.

This document owns the Survival session shape, the wave director, the tech
tier derivation, the Survival result and its front-end entry. It builds on
mechanisms owned elsewhere and restates none of them: skirmish entry,
termination and results
([DESIGN_SESSIONS_AI_SAVE §3.1](DESIGN_SESSIONS_AI_SAVE.md)), the skirmish
computer player (§2.4 there), order queues and stances
([DESIGN_UNITS_ORDERS_COB](DESIGN_UNITS_ORDERS_COB.md)), the movement class
layers ([DESIGN_MOVEMENT_PATH](DESIGN_MOVEMENT_PATH.md)), the random streams
([DESIGN_RUNTIME_DETERMINISM](DESIGN_RUNTIME_DETERMINISM.md)) and the retail
front-end screens
([DESIGN_INTERFACE_HUD_INPUT §3.4](DESIGN_INTERFACE_HUD_INPUT.md)).

## 1. Purpose and boundary

Survival reuses the whole battle: the economy, construction, movement,
combat, the computer player, the HUD. It adds only four things:

1. **A session shape.** A skirmish session with the human, zero to two allied
   computer players and one hidden **attacker** slot that has no commander,
   no economy and no planner (§4).
2. **The wave director.** Session-owned scenario logic that decides when a
   wave comes, what it contains and where it enters, creates its units
   through the ordinary allocator and gives them ordinary orders (§6).
3. **An end and a score.** Victory is never evaluated; the result screen
   reports waves survived, time alive and value destroyed (§8).
4. **An entry.** A Survival button on the single-player menu and a
   `--survival` flag (§9, §10).

**Out of scope.** Co-op (later, when multiplayer exists: a buddy row becomes
a human slot and nothing in the director changes). Saving (D2). Campaign
integration. Scripted or authored wave lists; waves are generated from the
catalog so every content mod works without Survival data.

## 2. Maintainer decisions (2026-09-23)

| ID | Decision |
|---|---|
| D1 | Survival runs under **every** gameplay mode, Strict 3.1 included. It is a scenario composed from existing mechanisms, not a gameplay rule (§3). |
| D2 | **No saving.** A Survival battle exists only live; if you die, you die, as in an online match. |
| D3 | The mode is called **Survival**. |
| D4 | The human starts at or near the map centre, optionally with one or two allied computer players; waves arrive from random directions, grow and rise in tech level; there is no victory, and the score is kept from kills, losses, damage and time alive (refined by D10). Each wave is distinct and is followed by downtime to build, expand and repair. |
| D5 | Survival is a separate entry on the single-player menu whose setup reuses the skirmish screen in a Survival configuration (§9). |
| D6 | The survivors are one side against the world: they share line of sight, the explored map and radar, and their income, but each controls only their own units (§4.3). Decided after the first play-test. |
| D7 | Income is split evenly among the living survivors; each spends only their own stock, so a computer buddy cannot drain the human's (§4.3). Proposed in answer to the maintainer's question and adopted for the prototype. |
| D8 | Survival adds metal deposits near the start site, matched to the map's own, so staying central pays and the opening has metal (§4.5). The maintainer's idea after the first play-test. |
| D9 | Each survived wave pays every living survivor 1000 metal and 1000 energy (§6.9). The maintainer's idea after landing. |
| D10 | The score belongs to the team and shows on screen. It is earned only against the attacker: priced damage dealt plus each survived wave's budget, with fast-clear and clean-wave bonuses. Time alive, income, repair and overkill earn nothing, and nothing is subtracted; losses and waste are shown as stats (§8). Adopted 2026-09-24 from the maintainer's ideas after discussion. |

## 3. Policy: a scenario, not a rule

Survival is to skirmish what a campaign mission is: a way a battle is set up
and driven, not a change to how units, weapons, the economy or the computer
player behave. The wave director creates units through the same allocator
that mission placement and factories use and gives them the same orders a
human can give. Every rule decision inside a tick is still answered by the
bound `session.RuleSet`, so a Survival battle under Strict 3.1 plays retail
rules against Survival waves, and under Modern plays the Modern policies.

It therefore adds **no seam** to `RuleSet` and reads no mode word. It is
selected by the battle-entry request, like the map, and fixed for the whole
battle.

Consequences:

- **The retail baseline is untouched.** A session that is not a Survival
  session never constructs a director, so no skirmish, campaign or
  fingerprint lock changes. Strict 3.1 in skirmish or campaign remains the
  retail baseline.
- **The director draws from the simulation stream** at one fixed site (§6.8),
  only in Survival sessions.
- **The Modern spawn command stays Modern-only.** The director shares its
  validation and creation path but not its gate; it is not a chat command.

## 4. The session

### 4.1 Slots

| Slot | Who | Controller state | Alliance | Commander | Economy |
|---|---|---|---|---|---|
| 0 | the human | human (1) | team 0 | yes | skirmish start resources |
| 1–2 | optional buddies | computer (2) | team 0 | yes | skirmish start resources |
| last row | the attacker | computer (2) | none | **no** | **none** (zero resources, no storage bonus) |

- **Attacker slot.** The row after the last buddy, so the skirmish setup
  record's rows stay packed and every row below `NumPlayers` is live, which
  the skirmish constructor assumes. Co-op later inserts human rows before
  it.
- **Why controller state 2.** The skirmish computer player only treats an
  owner as hostile when its controller state is human, computer or remote,
  and idle units only receive their default mission when the owner is human
  or computer. The attacker must be both targetable by buddies and refilled
  when idle.
- **A passive manager, not no manager.** Every non-observer slot's
  `ai.Manager` step also rebuilds that slot's combat target registry and runs
  its autonomous weapon maintenance, so an attacker without one would never
  aim. The attacker keeps its manager (and its eight setup draws) with
  `Passive` set, which skips the computer-policy classification sweep and the
  virtual tasks. It never builds, and the director gives its units their
  orders and stances.
- **Side.** The attacker owns units of every side; unit ownership is not tied
  to side. Its record carries a side only for logo and colour, and a colour no
  human or buddy uses.
- **Unit limit.** The session's per-player limit, as for everyone.

### 4.2 Start site

Map `StartPos` specials are ignored. The human's commander is placed at the
**centre site**:

1. Take the **largest connected region** of the commander's movement class
   (§6.6), so nobody starts on an island or a plateau with no way out.
2. Consider candidates every 4 cells of that region within a third of the
   smaller map dimension of the centre. Score each by the anchors, sampled
   every 2 cells within 12 cells of it, where the first factory on the
   commander's own build menu passes placement. Take the highest score, ties
   to the nearer candidate. The map centre alone chose a cramped spot on Ashap
   Plateau in the first play-test; measuring room with the side's own factory
   keeps the choice data-driven.
3. The commander stands on the region's cell nearest that candidate.

Buddies are placed on the same region, on a ring of radius `BuddyRing` around
the centre site at evenly spaced angles starting due east, each snapped to
the nearest cell of that region where the commander passes the spawn
command's placement checks. A buddy's
computer player then builds around its own commander as it does in skirmish.

### 4.3 One side against the world

Retail never shares vision between allies — every reader tests only the local
player's own bit [03 R-VIS-01 §7] — and shares resources only with remote
humans in a networked session [05 R-SHARE-01 §3]. Survival adds both, for the
survivors only, in every mode:

- **Vision.** `visibility.Service.SetVisionTeam` is called once at battle
  entry with the survivors. A member's line-of-sight coverage is stamped into
  every member's grid and its explored-map bit into every member's bit, and a
  change by any member refreshes the local fog. Reference counts stay balanced
  because the team never changes. The sensor pass treats every member as the
  viewing side: a member's radar and sonar reveal contacts for all, members'
  units are friendly, and members' jammers do not blind each other. The
  attacker's grids are untouched.
- **Income.** Once a tick, before any settlement, the director finds each
  living survivor whose settlement ran since its last visit and splits that
  pass's production evenly among the living survivors: the earner keeps 1/n
  and each teammate is credited 1/n, moved stock to stock (`survival.SplitIncome`).
  Each survivor spends only their own stock. A share that does not fit in a
  teammate's storage returns to the earner, whose next settlement clamps and
  counts waste as usual, so stock is conserved. The resource bar shows the
  survivor's own stock and storage, and its income as the team's production
  divided among the living survivors.

Alternatives considered: a single pooled stock (rejected: the computer buddies
build continuously and would keep the pool near empty, starving the human),
and opt-in sharing through the retail SHARE screen (kept as a possible
addition; it is not needed for the team to play as one side).

### 4.4 Session kind

A Survival session is a skirmish session (`Mission.Type` skirmish, retail
session kind 2) with a non-nil `Session.Survival`. Everything that branches on
skirmish — unit pool ordering, visibility setup, the score panel, cheats'
availability — keeps its skirmish answer except where §8 and §11 say
otherwise.

### 4.5 Extra metal deposits

Stock metal patches are indestructible features with a `metal` value; the
map-load deposit pass writes that value into the cells under each one
[05 R-FEAT-01 §7]. Survival adds copies of the map's own deposit feature near
the start site, inside the mission feature pass and before the deposit pass,
so they look and mine exactly like the map's patches:

1. **Which feature:** the deposit feature (non-zero `metal`, indestructible)
   the map places most often, ties to the lower key. A map with none — an
   all-metal map such as Metal Heck, or one whose metal is uniform ground
   metal such as The Pass — gets none.
2. **How many:** the median number of deposits within 24 cells of the map's
   authored start positions, times the number of survivors, less those
   already within 24 cells of the start site, at most 12. A map built around
   its centre (King of the Hill) already has enough and gets none.
3. **Where:** on evenly spaced bearings from the start site, each at the first
   radius from 6 to 28 cells that fits, swinging the bearing out to ±45° in
   11.25° steps if none does. A spot fits when the deposit's footprint and two
   clear cells around it are in the start region, hold no feature and no
   yard, and the side's first metal extractor could be placed there.

Measured with two buddies: Painted Desert 8 of 8, Great Divide 6 of 8, Ashap
Plateau 3, Coast to Coast 2.

## 5. Tech tiers

Content has no tech-level field, and inventing one per unit would break every
mod. The tier is derived from the build tree:

- Start from each side's commander. Walk the build products
  (`construction.BuildProducts` under the bound rules) breadth-first.
- Entering a unit through an **immobile builder** (a factory) costs one tier;
  entering it through a mobile builder costs nothing. Immobile means
  `bmcode` 0, the structure test the spawn command's placement already uses:
  stock factories author `canmove`, so that key cannot tell them apart. A unit's tier is the
  least cost over all routes from any side's commander (0-1 breadth-first
  search; ties and order are fixed by the build-menu order, never by map
  iteration).

In the stock catalog this gives the familiar ladder with no data: kbot-lab
and vehicle-plant units are tier 1; units of the advanced labs, which only a
tier-1 construction unit can build, are tier 2; units of a factory that only a
tier-2 builder can build are tier 3. The same walk classifies any mod.

**The wave pool** is every tiered unit that moves (`canmove` and a non-zero
`bmcode`), is not a builder or a commander, has a positive metal cost, and
has at least one resolved weapon that can hurt a base: not the empty "No
Weapon" record, not an interceptor, and not anti-air only. Mobile anti-nukes
would otherwise spend a wave's budget on nothing. The weapon flags are read
as authored. Each pool unit also carries
its **domain**: air (`CanFly`), hover, amphibious, naval (a floater without
hover or amphibious ability) or ground.

**Cost.** A unit's wave cost is `metal + energy / R`, where `R` is the median
energy-to-metal ratio over the wave pool, computed once per battle from the
catalog. Deriving `R` keeps mods with different cost scales balanced without
a table.

## 6. The wave director

### 6.1 States

```
Grace ──► Warning ──► Active ──► Downtime ──► Warning ──► …
```

- **Grace**: from battle start for `FirstWaveDelay`.
- **Warning**: `WarningTime` before a wave; the wave is already planned so its
  direction and domain can be announced (§6.7).
- **Active**: the wave's units are created over several ticks (§6.5).
- **Downtime**: once every unit has arrived, the next warning comes
  `Downtime` after the wave is destroyed — but never later than `Downtime +
  Straggle` after its last unit arrived. The wave then counts as survived.

The first play-test found the waves too far apart: on a large map the units
spend one to three minutes walking in from the edge, and waiting for a wave
to die let travel time set the rhythm. Timing from arrival lets a slow wave
overlap the next one instead of stalling the battle.

### 6.2 Budget: difficulty follows game time

A wave planned at tick `t` has budget

```
Budget(t) = BaseUnits × tier1Median × 2^(t / Doubling)
```

interpolated linearly within each doubling, in integers, where `tier1Median`
is the median cost of the tier-1 pool (§5). Tying difficulty to time rather
than to the wave count keeps pressure rising while a wave is still being
fought, and makes the length of a battle a matter of one constant.

`Downtime = DowntimeBase + DowntimePerUnit × Budget / tier1Median`, at most
`DowntimeMax`: early waves come quickly, and the pause grows with the waves.

**Target length.** An average battle should last about 30–40 minutes, a
strong player an hour or more, with the first pressure inside the first few
minutes. At normal pace the budget is about 4 tier-1 units at 5 minutes, 16
at 15, 64 at 25 and 256 at 35, which brings experimental units in around 35–40
minutes. Measured with the human idle and two computer buddies defending —
a weak player, since the stock computer player neither defends its ally's
commander nor clears stragglers — the team fell between 8 and 21 minutes
(median about 13) across Great Divide, Painted Desert, Coast to Coast and
Ashap Plateau; waves were planned about every 2 minutes early and every
3½ later.

### 6.3 Tiers

Tier 1 is always unlocked; each higher tier unlocks once the budget buys
`UnlockUnits` of that tier's median unit. Picks weight the newest unlocked
tier `NewTierWeight` and each older tier 1. A unit dearer than a direction's
share of the budget is never picked, unless nothing fits, when the theme's
cheapest unit comes alone; a small wave can no longer be one experimental
unit.

### 6.4 Composition

To make each wave distinct rather than a uniform mix:

1. **Directions.** `1 + t div DirectionEvery`, capped at 3. Each is a
   random angle; directions of the same wave are at least 60° apart.
2. **Theme.** Each direction draws a domain: ground always; hover and naval
   only if some spawn cell of that domain can reach the base region (§6.6);
   air only from tick `AirFrom`. Weights are in §7. A direction that no
   unlocked unit can enter from is turned an eighth of a circle at a time,
   drawing nothing; if no direction admits anything (an island start before
   `AirFrom`), air comes early rather than the wave being empty.
3. **Signature units.** Each direction draws 1–3 unit types from the pool of
   its domain and unlocked tiers.
4. **Fill.** The budget is split evenly across directions; each direction
   adds its signature types round-robin until the next one no longer fits.
   A wave always has at least one unit.

### 6.5 Spawning

Each unit is created at a spawn cell (§6.6) through the same validated path
as the spawn command: placement check, then the ordinary fully built
creator, then movement registration. At most `SpawnPerTick` units are created
per tick so a large wave does not land in one tick. If the attacker reaches
its unit limit, the rest of the wave is dropped; later waves still rise in
tier, so pressure rises through quality once quantity is capped.

### 6.6 Spawn cells and reachability

For each direction, the entry point is where the ray from the centre site at
that angle leaves the map, moved `EdgeInset` cells inward. Ground, hover and
naval units need a cell that is passable for their movement class **and in
the same connected region of that class's layer as some cell adjacent to the
centre site**; the director searches outward along the edge from the entry
point for the nearest such cell, and drops a unit type that has none. Air
units use the entry point directly.

Every wave class is labelled at battle load, not when the first wave is
planned: on a large map the flood fills take a visible fraction of a second.
Regions are 4-connected components of the class's terrain-only footprint
passability, one flood fill per movement class on first use. Structures and
features are ignored on purpose: attackers fight through what the player
builds, and a walled base must not make every edge "unreachable". The movement
layers promise no global reachability, so this is Survival's own code.

The base region of a class is the region of its passable cell nearest the
centre site within `LandReach` cells (`WaterReach` for naval classes, which
fight from the shore). A class with no such cell never enters. Each unit is
created at the nearest cell of its base region to the entry point, within
`SpawnReach` cells, where the spawn command's placement checks pass.

### 6.7 Orders and warnings

- **Stance.** Every created unit is set to roam and fire at will, as the
  computer player sets its own units.
- **Target.** For each unit, the nearest living unit of the human team
  (the human and every slot allied with them), structures (`bmcode` 0)
  before mobile units.
- **Order.** A patrol to the target's position, so units engage whatever they
  meet on the way. Every `RetargetEvery` ticks the director re-issues the
  order to any attacker unit whose queue holds no issued work (only its
  default mission) or whose target has died, with a fresh target.
- **Warning.** At Warning, a priority announcement names the wave, its
  directions (compass words) and any air or naval theme; the countdown voice
  cues play for the last five seconds. On Active, a second announcement.

### 6.8 Determinism

The director runs once per tick at the **end of phase 1**, after human
commands are applied, which is where the spawn command already creates units.
It draws only from `Session.SimRNG`, in this order: wave planning when a
Warning starts (directions, then themes, then signature types, then the
fill), then unit creation (the allocator's own draws). Retargeting draws
nothing. It keeps its state in `Session.Survival`, ranges over no map and
reads no wall clock. It is paused with the session.

### 6.9 Wave reward

When a wave counts as survived (§6.1), whether destroyed or outlasted, each
living survivor receives `WaveReward` metal and `WaveReward` energy, each
capped at that survivor's storage; what does not fit is lost, as overflow
is. The grant goes straight into each stock, so the income split (§4.3)
never sees it and every survivor gets the full amount. It draws nothing.
The message line reports it with the wave and its score (§8):
`Wave N cleared: +1000 metal and energy, score +1840 (fast, clean)`, or
`survived` when the wave was outlasted rather than destroyed.

## 7. Tuning (initial values)

All values are ticks at 30 Hz or plain numbers. They are Survival design
choices for play-testing, recorded here so they change in one place.

| Name | Initial | Meaning |
|---|---|---|
| `FirstWaveDelay` | 60 s (relaxed 90 s, relentless 45 s) | Battle start to wave 1's warning |
| `WarningTime` | 30 s | Warning before a wave arrives |
| `Straggle` | 60 s | Most the next warning waits past arrival + downtime |
| `DowntimeBase`, `DowntimePerUnit`, `DowntimeMax` | 30 s, 2.5 s per tier-1 unit, 150 s | Pause after a wave |
| `WaveReward` | 1000 | Metal and energy per living survivor per survived wave, up to storage |
| `FastClearBonus` | 50 % | Bonus on a wave's budget for a clear at arrival, falling linearly to none at the deadline (§8) |
| `CleanWaveBonus` | 25 % | Bonus on a wave's budget when no finished structure fell to it (§8) |
| `BaseUnits` | 2 | Budget at battle start, in median tier-1 units |
| `Doubling` | 5 min (relaxed 6.5 min, relentless 4 min) | Budget doubling time |
| `UnlockUnits` | 8 | A tier unlocks when the budget buys this many of its median unit |
| `NewTierWeight` | 3 | Weight of the newest unlocked tier |
| `DirectionEvery` | 12 min | Game time per extra direction |
| `AirFrom` | 5 min | First time a wave may be airborne |
| Theme weights | ground 6, hover 2, air 2, naval 2 | Before the eligibility filter |
| `SpawnPerTick` | 2 | Units created per tick |
| `EdgeInset` | 2 cells | Entry point inset from the map edge |
| `RetargetEvery` | 90 ticks | Idle retarget cadence |
| `BuddyRing` | 20 cells | Buddy distance from the centre site |
| `LandReach` | 8 cells | Base region search radius for land and hover classes |
| `WaterReach` | 40 cells | Base region search radius for naval classes |
| `SpawnReach` | 48 cells | Spawn cell search radius around an entry point |
| Pace | relaxed and relentless change only `FirstWaveDelay` and `Doubling` | |

## 8. End and score

- **No victory.** `EvaluateResult` skips the victory sweep for a Survival
  session; an empty attacker between waves never arms the countdown.
- **Defeat** is the skirmish rule: the local player has no live units. The
  setup's commander-death rule applies as in skirmish, with only *continues*
  and *game ends* offered (a respawn has no meaning here). Allied buddies do
  not keep the battle going after the human's defeat.
- **The attacker is not a player in the result.** It has no result row, is
  never a winner, its "obliterated" announcement is suppressed, and it never
  takes the kill lead.
- **Survival counters**, per human and buddy slot: priced damage dealt to
  attacker units, value destroyed (wave cost of attacker units that slot
  killed), value lost (wave cost of its own units lost); and for the team,
  waves survived, time alive and the points the survived waves scored.
- **Score** is the team's, earned only against the attacker, so nothing a
  survivor does without the waves involved can farm it:
  - **Priced damage.** Each hit a survivor lands on an attacker unit is worth
    the unit's wave cost times the share of its maximum health the hit
    removed, counting only health the unit still had. Every hit on one unit
    therefore sums to exactly its cost when it dies from full health,
    whoever landed them, damage past its last point of health earns
    nothing, and a unit that regains health is never worth more than its
    cost. Combat reports the health each accepted packet removed through its
    `HealthLost` observer; the session keeps each attacker unit's running
    total and credits the difference in value.
  - **Wave points.** A survived wave scores its budget, plus up to
    `FastClearBonus` of it when it is cleared rather than outlasted,
    falling linearly from its arrival to the deadline on which the next wave
    comes anyway (§6.1), plus `CleanWaveBonus` of it when no finished
    survivor structure fell to its weapons while it was the active wave.
    Structures an owner reclaims or self-destructs do not count.
  - **Nothing else.** Time alive already pays through the growing budgets;
    income, repair and overkill would reward play that does not beat the
    waves; losses are not subtracted, so sacrificing units is not punished
    twice. Waste already has its authored result columns.
- **On screen.** The HUD shows `Score N` on a line under the wave line (§9),
  and the wave message reports what each wave scored (§6.9).
- **The result screen** keeps its seven authored columns and adds two
  Nanolathe lines under the rows: time alive, waves survived and the team
  score; then the team's damage value with each survivor's share by name,
  and the wave points (a presentation divergence, like the Mods & Mutators
  loading-screen lines).
- **Best scores** are kept per map, mod, mutators, rule set, buddy count and
  Survival options in the host settings directory, outside the simulation. A
  battle in which a cheat was used is shown but not recorded.

## 9. Front end

- **Entry.** A **Survival** button on the single-player menu, cloned from the
  authored Skirmish button one authored pitch down, the pattern of the
  Nanolathe options categories
  ([DESIGN_INTERFACE_HUD_INPUT §3.4.1](DESIGN_INTERFACE_HUD_INPUT.md)).
- **Setup.** It opens `skirmish.gui` in a Survival configuration
  (`cmd/nanolathe/survival_menu.go`). The screen keeps its own rows and
  switches and swaps them into the shared setup state while it is open, so
  every skirmish control handler serves it unchanged; the skirmish rows are
  set aside and restored on leaving, and the settings file only ever records
  the skirmish rows. Changes from the skirmish screen:
  - three rows: the human, always Player, and two buddy rows that toggle
    between Open and Computer; the allegiance icons are hidden (the team is
    fixed);
  - Wave Pace (Normal, Relaxed, Relentless) takes the start-location
    control's framed slot and label, cloned from Difficulty;
  - Air Waves and Naval Waves (On, Off) sit under the player box, cloned from
    Mapping, with labels cloned from the screen's own;
  - Commander, Mapping, Line of Sight and Difficulty (which applies to the
    buddies) keep their skirmish meaning;
  - Start checks only that the map loads — start positions and opponents do
    not matter — and builds the battle with `session.SurvivalConfigFor`.
  The background's baked "Skirmish Setup" title stays; it is authored art.
  The post-battle return and Restart go back to the Survival screen and
  setup.
- **In battle.** The slide strip shows the wave number and state, and the
  team's score on the line under it (§8). There is no
  minimap marker today; an entry-edge marker is follow-up presentation work.

## 10. Command line and headless

- `--survival` on `nanolathe` and `nanolathe-headless`, with `--map`; with
  `--survival-buddies 0..2`. Rejected with `--mission` and `--load-save`.
- The headless report's scenario kind is `survival`, with the Survival
  counters and the last wave reached. This is how the director is tested.

## 11. No saving

- The battle options window greys the Save Game button, and the save action
  is refused for a Survival session at the session boundary as well.
- The results screen's save button is already campaign-only.
- There is no quicksave or autosave to gate. Loading a save from the menu is
  unaffected.

## 12. Verification

- **Tiers.** On the retail catalog: a kbot-lab combat unit is tier 1, an
  advanced-lab combat unit tier 2, and no pool unit is a builder or a
  commander. Relationships, not a census.
- **Director determinism.** Two headless Survival runs with one seed produce
  identical wave plans and results; a different seed produces a different
  first plan.
- **Isolation.** A non-Survival session constructs no director and draws the
  same numbers as before; the existing fingerprint locks do not move.
- **End.** An attacker with no live units never arms victory; local defeat
  ends the battle; the attacker has no result row.
- **No save.** A save request in a Survival session is refused.
- **Score.** Hits on one unit, split between shooters, sum to exactly its
  cost and overkill earns nothing; only a cleared wave earns the fast-clear
  bonus, and an earlier clear earns more.
- **Performance.** A long headless Survival run on a stock map stays within
  the simulation benchmark's per-tick budget at the unit limit.

## 13. Implementation status

Implemented: the session shape (§4),
the roomiest start site (§4.2), shared vision, radar and income (§4.3), the
extra deposits (§4.5),
tiers and pool (§5), the director (§6), the §7 values, no victory, the result
gates, the counters and the team score with priced damage and wave bonuses
(§8), the HUD wave and score lines and the result lines, `--survival`, `--survival-buddies`, `--survival-pace`,
`--survival-no-air` and `--survival-no-naval` on both commands with the
`survival` report block (§10), and the save refusal (§11). Tests: planner
determinism, budget, tiers and switches, region labelling, the retail tier
walk, and a retail battle that ends in defeat and replays under Modern and
Strict 3.1.

The single-player menu button and the Survival setup screen (§9) are in.

Not yet: best scores; an entry-edge minimap marker.

## 14. Proposals awaiting confirmation

| ID | Proposal |
|---|---|
| P1 | Attacker in the row after the last buddy, controller state computer, a passive manager, no commander, no economy (§4.1). |
| P2 | Centre site on the commander class's largest connected region (§4.2). |
| P3 | Tier = factories entered on the cheapest route from a commander (§5). |
| P4 | Time-driven budget, size-scaled downtime, arrival-timed waves, budget-driven tier unlock and themed directions as in §6.1–§6.4 with the §7 values; a 30–40 minute average battle. |
| P5 | Waves patrol to the nearest human-team unit, structures first, and are retargeted when idle (§6.7). |
| P6 | Superseded by D10 (§8). |
| P7 | Buddies do not extend the battle after the human's defeat (§8). |
| P8 | Cheats allowed but the result is not recorded as a best score (§8). |

## 15. Open

- `TODO(question)`: what a single-waypoint patrol does on arrival
  ([DESIGN_UNITS_ORDERS_COB §3.5](DESIGN_UNITS_ORDERS_COB.md)). If it
  completes rather than cycling, the retarget cadence already covers it; the
  prototype records the observed behaviour here.
- Reachability flood fills per class and their rebuild cost on large maps.
- An entry-edge minimap marker.
- Balance: every §7 value is a starting point for play-testing.
