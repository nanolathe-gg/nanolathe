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
  - three rows: the human, always Player, and two buddy rows that a click
    walks from Open to Modern AI, Classic AI and back to Open, so a new ally
    starts on the Modern AI (user decision 2026-09-25); the caption names
    the buddy's AI (§16, and DESIGN_INTERFACE_HUD_INPUT §2.6 "Computer AI");
    the allegiance icons are hidden (the team is fixed);
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
- `--ai-player 2=<classic|modern>` and `--ai-player 3=<classic|modern>`
  choose the buddies' AI by lobby row (§16), and `--ai-player all=modern`
  marks every buddy; an omitted buddy is Classic, and row 4 (the attacker,
  with two buddies) is refused.
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

The Modern AI's survival brain for Modern computer buddies (§16) is in,
chosen per buddy in every gameplay mode: the scenario record and the published warnings, the
composition, the survival layer, its keys, and its tests.

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
- The survival brain's open items are in §16.7.

## 16. Computer survivors under the Modern AI

Each buddy is Classic or Modern, chosen per buddy on the Survival screen
(§9) or by `--ai-player` (§10), in every gameplay mode (user decision
2026-09-25; [DESIGN_SESSIONS_AI_SAVE](DESIGN_SESSIONS_AI_SAVE.md#modern-ai-computer-player)
"Per-player selection"); `--ai-player all=modern` marks both. A Modern
buddy is the
[Modern AI computer player](DESIGN_SESSIONS_AI_SAVE.md#modern-ai-computer-player).
Its skirmish brain looks for an enemy base, spreads a wide base and attacks
out. In Survival there is no base to find and the waves come to the team,
so a Survival battle's Modern buddies play a **survival brain** instead
(`internal/aikit/brains/survival`, composed by `mods/aikit/survival.go`).
It belongs to the Modern AI policy (user request 2026-09-24: "a special AI
for the survival mode as it needs to build towers, walls, centralize its
base, protect its commander, work with the player and other AI"), not to
Survival: a Classic buddy runs its set's retail planner exactly as before,
a Modern buddy plays under the battle's rules — Strict 3.1 rules in a
Strict 3.1 battle — and Survival itself (§3) is unchanged. One battle may
mix a Modern and a Classic buddy.

### 16.1 What the session tells the survivors

At battle entry, once the commanders are placed, the session sets
`ai.Manager.Survival` on every survivor's manager (never the attacker's):
the start site, the team slot-ascending with the computer buddies marked,
and where each member's commander was placed. When a warning begins
(§6.7) the director publishes what it announces — the wave's number, its
arrival tick, and each direction's bearing, entry point and theme (air,
naval or hover; any other group, amphibious included, as ground, since the
announcement names it as a plain direction). Nothing the announcement does
not tell the human is published; the user authorized the Modern AI to use
what the human is told.

Only the Modern AI controller reads the record. It is set outside any
tick and draws nothing; a Classic buddy's step carries it dormant, so no
fingerprint moves, and a battle that is not Survival never has one.
Survival cannot be saved (§11), so the save sidecar carries none of it.

**Threads.** A published warning never changes and the list is replaced
whole (an atomic pointer), and the brain asks for the warnings published
at or before its observation's tick. The director publishes in phase 1 and
the controller observes in phase 5, so a think on the simulation thread and
one on a worker read the same list.

### 16.2 Composition

`newUtilTacHost` builds the survival brain when the manager is a Survival
computer buddy and its configured parameters do not say `survival=0`; any
other manager gets exactly the brain it got before. The survival brain is
the util+tac layers — utility strategy, economy and production, tactics
army — built by `NewUtilTac` from the survival defaults with the
configured parameters on top, key by key, and wrapped layer by layer:

| Default | Why |
|---|---|
| `def_plan=0` | the utility's own towers only answer danger; the survival layer plans the ring |
| `w_scout=0` | there is no base to scout |
| `tech_time=12` | the waves climb the build tree |
| `wide_base=0`, `style=balanced` | a compact base; the tuned weights |
| `raid=0`, `harass=0`, `probe=0`, `tour=0` | there is nothing to raid or probe |

The survival layer's own keys are `survival.Specs`: `survival` (0 plays the
skirmish brain), `sv_tower`, `sv_walls` and `sv_claim` (§16.3), validated
by `mods/aikit` `ValidateParams` like every other key.

### 16.3 The survival layer

- **Home.** A buddy's home — where the utility economy centres its base,
  where its commander works and shelters — is its start moved out on the
  bearing from the start site to 560 wu from the site, on dry land the
  commander reaches from its start. The session places buddies 320 wu out
  (§4.2), inside a commander's death explosion (stock: 950 wu across, 9,999
  damage): a buddy's commander that died near its start, or sheltered
  between the two starts, took the human's commander with it.
- **Facing.** Each buddy's base faces outward. The board's enemy point,
  which the utility zones orient a base by, is 1,500 wu beyond the buddy's
  home on the bearing from the start site through it. The zones put
  factories toward that point and energy and makers on its flanks, and cap
  each class's distance at a share of it (factories 35–40 %, energy
  45–50 %), so the base stays compact and on the buddy's own side, clear of
  the ground around the start site.
- **Metal spots.** A free spot nearer another survivor's start than this
  buddy's is held back: the human's within 480 wu of its start for good (a
  buddy's extractor there is a building a wave goes for, and it brought
  the fight to the human's commander), its other spots for the first three
  minutes (after that, since income is split evenly, whoever builds the
  extractor the team gains), and the other buddy's for good (both are
  survival brains and would race for them).
- **Sectors.** The ground around the start site is cut into sixteen
  bearings. Each belongs to the computer buddy whose start bearing is
  nearest, so the two buddies never tower the same lane; a lone buddy owns
  them all, the human's side included.
- **Tower ring.** From minute 2 the buddy keeps `sv_tower` percent of what
  it has received — stock gained plus spent, so the wave rewards and its
  share of the teammates' production count — as towers of its own
  planning, and whatever stands above half of both stores besides (full
  stores are resources nobody is spending, and they waste the next wave
  reward). The towers are spread over its sectors by weight: every owned
  sector that has reachable ground between 640 and 1,300 wu from the site
  1, a warned direction's sector +5 (half that to its neighbours, a sixth
  to the next), and a fading history of the armed attackers seen on each
  bearing +6 in all. The next tower goes to the sector furthest behind its
  share whose site will do (else the next), 72 wu beyond the buddy's
  outermost core building on that bearing
  (factories, energy, makers, storage, radar; at most 1,300 wu from the
  site) and at least 640 wu from the site, shifted
  sideways for each tower already there so they form a line across the
  approach. (A wave goes for the building nearest it, so a tower drawn in
  beside the human's start brought the fight to the human's commander.)
  The site is pulled in (not nearer than 640 wu) until the point is on the
  dry land the buddy's commander class reaches from its start, and moved out
  or in by turns after a failed placement. The tower is the one the builder can make with
  the most ground firepower times hit points per cost, heavier and
  longer-ranged towers winning as income grows, none dearer than 90
  seconds of income or than the stores hold, whichever is more; while aircraft threaten, a sector's second tower is
  anti-air. Towers go through the executor's layout rules and exit guard
  (`Kit.BuildKeep`), so they never seal a factory.
- **Walls.** A warned or attacked sector with two towers gets a segment of
  three wall pieces 128 wu beyond its towers' line, across the bearing: at
  most one segment per 90 seconds and three per sector, never more than
  half its towers, later rows staggered sideways. A segment spans a fraction
  of its sector's arc, so the bearings between segments stay open —
  corridors for the buddy's units and the human's, and the lanes the
  attackers are funnelled into. Pieces avoid own factories' exit lanes and
  metal spots.
- **Repairs.** Damaged buildings (below 70 %) with no stronger attacker at
  them are repaired, towers and factories first — its own and, within
  1,300 wu of the site, its teammates' (§16.4).
- **Builders.** The layer takes up to `sv_claim` percent of the
  constructors, at least one (twice that while a wave is warned), from the
  utility economy, which is not shown them while they work; their orders are issued
  after the economy's, from the action budget it left. When a tower is owed
  and none is free, the constructor whose work is cheapest to break off
  (anything but a factory or a tower) is taken. A builder whose job ended
  before it started building (a site that would not take the building, a
  builder boxed in by rows) is left to the economy for 30 seconds, three
  minutes after three failures in a row. With no constructor at all (a
  poor map), from minute 6 the commander builds towers within 700 wu of its
  start when both stores can fund the tower (half again its cost in stock,
  or the store four-fifths full) — even breaking off work the economy is
  not paying for when both stores are four-fifths full, or a walk to a
  factory site it has not begun after they have stood so for 90 seconds
  (on The Pass the only factory site lies across the map).
- **Commander.** Its death takes the buddy's whole base with it (the
  skirmish commander-death rule). When the armed attackers within 650 wu
  are more than half the strength of the commander itself, the buddy's
  units and its towers there, or it is below 60 % health with attackers
  near, it is sent 300 wu from its home, to whichever of eight points
  stands farthest from them at least 560 wu from the start site, and kept
  from the economy for 15 seconds. While a warned wave is due within
  30 seconds or armed attackers are within 1,300 wu of it, it is kept
  within 700 wu of its home, among its towers and units rather than out on
  an extractor run — but not before its first factory stands (on The Pass
  the only factory site lies far out, and the leash kept it from ever
  being built).
- **Army.** The posture never permits an offensive. The tactics army is
  shown a Survival world: its home is the start site itself — the team's
  weakest point is the human's commander, and a wave that walks past the
  buddy's side reaches it first; its enemy point lies 1,600 wu
  out on the threatened bearing (an allied building under fire, §16.4, else
  the warned ground direction the buddy weighs most, else the armed
  attackers in view, else its outer side), so
  it gathers on the towers' line; and its picture holds only attackers
  within 1,400 wu beyond the buddy's perimeter, so it fights what comes and
  does not chase stragglers to a map edge.

### 16.4 The team in sight

The observation lists the allied units in sight (`aikit.Obs.Allies`,
MODERN_AI_RESEARCH §3): in Survival the team shares sight (§4.3), so a
buddy sees the human's and the other buddy's units wherever the team does,
with their type, position, hit points and completion. Each think the
survival layer reads from them:

- **Covered lanes.** Allied towers (framed or built) out on the ring's
  ground, at least 480 wu from the site, count for their bearing's sector:
  they cancel the sector's owed tower value up to what it is owed, never
  more, so a lane the human or the other buddy already towers draws none
  of this buddy's towers, which go to its other sectors, and the value
  they would have cost stays with the economy. A tower nearer the site
  guards the human's core, which the ring stands in front of, and covers
  no lane.
- **The team's perimeter.** Allied factories, energy, makers, storage and
  radar push each sector's perimeter out as the buddy's own do, so its
  towers stand in front of the whole team's base.
- **Clear of the human's buildings.** Every allied building keeps a box
  48 wu beyond its footprint, and an allied factory the 160 wu in front of
  it (its exit lane, toward +Z as the executor keeps an own factory's). A
  tower site in one moves 96 or 192 wu to either side, then up to 256 wu
  out in front, when that clears it. Failing that, a site beside a
  building stands — the placement search keeps footprints apart, and on a
  cramped map (Ashap Plateau) the team's buildings fill the ring's ground,
  where refusing such sites left the human's side without towers — and a
  site in a factory's lane is pulled in further. Wall pieces are dropped
  from anywhere in a box.
- **Repairs.** A teammate's damaged building within 1,300 wu of the site is
  repaired as the buddy's own are (§16.3). The repair order takes a
  friendly target on nano-reach alone, with no ownership test
  [04 R-ORD-02 §1], and bills the repairer's energy [05 R-WORK-01 §3].
- **Defence.** The allied building under fire that the buddy weighs most
  (its value times the share of its hit points lost, armed attackers
  within 480 wu) sets the army's threatened bearing (§16.3).
- **Metal spots.** A spot an allied extractor stands on (within 40 wu of
  its centre) is held from the economy, which could not see it taken.
- **Commander.** While the armed attackers within 650 wu of the buddy's
  commander are more than a quarter of the strength about it (§16.3), or
  it is below 80 % health, it keeps 560 wu from every allied commander:
  one nearer is sent to whichever of the eight points 300 wu (else 600 wu)
  from its home stands farthest from the attackers, 560 wu from the site
  and 560 wu from every allied commander; every refuge (§16.3) avoids
  allied commanders so. A commander in no danger stays: kept away from the
  human's whenever any attacker was near, it no longer killed a lone
  raider at the start site, and idle humans died to one in the first
  minutes. The buddy cannot move the human's commander, and proximity
  costs nothing while no commander dies.

### 16.5 Evaluation (2026-09-25)

Displayless Survival battles through an evaluation harness kept outside
the repository, which composes the battle like `nanolathe-headless
--survival` and samples it. The human's slot is idle, as in the §6.2
measurement: the stand-in is the same in every variant, so the buddies'
contribution is what varies, and the battle ends when the idle human's
commander dies. Six maps (Painted Desert, Great Divide, Ashap Plateau,
Coast to Coast, The Pass, Comet Catcher), seeds 1–3, one and two buddies,
normal pace, the lobby's hard difficulty (the hard persona). The variants:
(a) `--gameplay modern`, the retail planner; (b) the since-retired
`modern-ai` set (every buddy Modern, which `--gameplay modern --ai-player
all=modern` now plays) with `survival=0`, the skirmish util+tac brain as
before this section; (c) `modern-ai`, the survival brain.

| Variant | Battles | Mean minutes | Median | Mean wave reached | Mean score | Structures lost to waves | Towers / walls a battle |
|---|---|---|---|---|---|---|---|
| (a) retail | 36 | 20.2 | 18.2 | 8.4 | 46k | 26 | 8 / 0 |
| (b) util+tac | 36 | 28.8 | 29.8 | 10.5 | 247k | 78 | 63 / 0 |
| (c) survival | 36 | 30.2 | 35.6 | 11.0 | 267k | 78 | 32 / 9 |

Paired by scenario, (c) outlasts (a) in 27 of 36 battles (+10.0 minutes,
90 % map-cluster interval +5.8 to +14.6) and (b) in 21 of 36 (+1.4
minutes, −0.4 to +3.3); it reaches a later wave than (b) in 16 and an
earlier one in 8. With two buddies (c) averages 38.5 minutes against
34.8; with one, 22.0 against 22.8. On the relentless pace (seed 1, two
buddies, six maps) the means are 13.1, 28.2 and 30.3 minutes. The first
fifteen minutes, where most one-buddy battles are lost, were measured
separately on seeds 11–15 (60 battles a variant): the human's commander
was alive at fifteen minutes in 50 of 60 with (c) and 43 of 60 with (b).
Walls and towers trap nothing: the survival buddies never had more than
four ground units standing boxed in at once (the arena's measure: two
minutes within 320 wu with an order leading farther), as with (b); the
retail planner reached six.

What the numbers taught, in order: a buddy commander's death explosion
killed the idle human's commander more often than any wave (the home at
560 wu, §16.3); the army's defence centre at the human's start was worth
more than the buddy's own; more towers did not help (`sv_tower=40`
outlasted the default in 5 of 16 two-buddy battles, fewer in 11); and a
commander stand beside the human, a commander that attacks raiders at the
start site, a guard squad kept at the start site and a commander that
flees sooner and fights back all measured worse or no better and were
dropped. The noise is large — a battle's waves are drawn from the
simulation stream, so any change of play changes every later wave — and
the paired intervals above are what to trust.

Cost, late waves (two buddies, over 100 attackers alive): a survival
buddy's think took 196 µs (median over the pool's buddies; util+tac 460
µs) and its host step 35 µs a tick on average; a CPU profile of Painted Desert's
minutes 35–51 put both controllers' whole step at 2.4 % of the tick,
their thinking at 0.5 %, and most of the tick in frame publication (28 %)
and path search (21 %).

**Against an active human (the team in sight, §16.4).** An idle human
never builds, so the allied behaviour was measured against a stand-in that
does: the harness plays slot 0 with util+tac at the hard persona,
`style=tower,jitter=0,w_scout=0,raid=0,harass=0,probe=0,tour=0,att_curve=0,att_min=6000,att_grow=1000`
— a turtle that opens on a tower, builds its base and 50–100 towers around
the start site, never scouts or raids, and holds its army at home until it
is worth 6,000 plus 1,000 a minute. It was chosen over the retail planner
because it plays the same way in every variant (a fixed style, no jitter,
no draw from the simulation stream; the retail planner draws there) and
builds as a human who holds ground does, while the retail planner towers
little (eight a battle as a buddy above) and sends its army out. Its
commander works across its base, up to 900 wu from the site. (The harness
gives slot 0 the computer's control byte while its commander lives and the
human's again at its death: the session polls the end conditions only on
the human's slot.) Same pool as above; (c) is the survival brain as merged
before §16.4, (d) this one.

| Variant | Battles | Mean minutes | Median | Mean wave | Human's structures lost to waves | Buddies' | Buddy towers | within 384 wu of a teammate's | in a sector a teammate towered | Allied repairs ordered |
|---|---|---|---|---|---|---|---|---|---|---|
| (c) | 36 | 40.4 | 43.0 | 14.0 | 83 | 74 | 46 | 24 | 24 | 0 |
| (d) | 36 | 40.4 | 42.7 | 14.0 | 79 | 63 | 40 | 19 | 17 | 9 |

(Per battle; the tower columns count the buddies' finished towers and
those that stood, when finished, within 384 wu of another survivor's
finished tower, or in the same one of sixteen bearings about the site,
beyond 160 wu, as one.) Paired by scenario, (d) lasts as long as (c):
−0.0 minutes, 90 % map-cluster interval −2.1 to +2.0, longer in 18
battles and shorter in 17 (one buddy +1.5, −2.0 to +5.1; two −1.6, −4.4
to +1.7). The team loses fewer structures to the waves — 3.01 a minute
against 3.39 (−0.67 to −0.10), the buddies 1.31 against 1.57 (−0.38 to
−0.13), the human 1.70 against 1.82 (−0.30 to +0.08) — with 6 fewer buddy
towers a battle (−7.9 to −4.3), 5 fewer of them beside a teammate's (−8.3
to −2.3; by sector −7.1, −11.0 to −3.5). Buddy units carried a repair
order on a teammate's unit 13.0 times a battle against 7.6 (the rest
are orders the engine gives by itself, as a patrolling constructor
repairs what it passes); the survival layer ordered 9.4 of them. Boxed-in units: at most seven at once against six, in 30
battles each.

The commanders: buddy commanders stood within 480 wu of the human's with
attackers within 1,300 wu of them for 218 seconds a battle against 262
(summed over the buddies, sampled every second). A buddy commander's
death was followed within 15 ticks by a teammate's commander within 600 wu
of it — the explosion — in 2 battles of 36 with (d) and 5 with (c) where
the victim had a quarter of its health or more the tick before, and in 3
and 3 where it had none left (the human's commander died first, to the
wave, and took the buddy's with it); buddy commanders died before the
human's in 18 and 19 battles. In both kills that remain, and in four of
the five traced in earlier versions, the buddy's commander could not move
while it was killed — every move it was given ended within a few ticks —
and the human's commander stood 300–440 wu away (§16.7).

Against the idle human of the first evaluation, same pool, (d) plays as
(c) did: 29.8 against 30.2 minutes, −0.4 (−1.9 to +1.1), longer in 13 and
shorter in 16; explosion kills 3 and 1, with 8 and 7 more where the
human's commander was already dead. (The base reproduces (c)'s 30.2
minutes above exactly.)

What the development runs taught: a repair a builder could not reach (a
construction ship sent ashore) was reordered every think, some 3,900
times a battle on Coast to Coast; a tower site that had to be clear of
every allied building gave up the human's side of a cramped map (Ashap
Plateau: five of six battles shorter); the human's own core towers
counted as covering the ring's lanes; and a commander kept from the
human's whenever any attacker was near no longer killed a lone raider at
an idle human's start site (Coast to Coast, two buddies: battles of 41 and
35 minutes ended at 5 and 7). §16.3–§16.4 describe the rules as fixed.
Cost, late waves: a survival buddy's think 401 µs (median; (c) 344 µs),
its observation 135 µs (90 µs; the allied units in sight,
MODERN_AI_RESEARCH §3).

### 16.6 Verification

- `mods/aikit.TestSurvivalBuddiesPlayTheirOwnAIRetail`: in a Strict 3.1
  Survival battle with one buddy of each kind, the Modern buddy plays the
  survival brain, the Classic buddy the retail step, and the attacker is
  never a Modern AI player; `cmd/nanolathe.TestSurvivalBuddiesCycleOpenModernClassic`
  and `cmd/nanolathe.TestSetupScreensChooseEachComputerRowsAIRetail` lock the
  screen's walk, its captions and the battle it builds.
- `mods/aikit.TestSurvivalBrainOnlyForComputerSurvivors`: the Modern AI's
  controller builds the survival brain only for a Survival computer buddy
  and not with `survival=0`; a manager with no scenario (every other
  battle), the human's and the attacker's get the skirmish brain.
- `mods/aikit.TestValidateParamsReadsTheSurvivalKeys`: the keys join the
  strict vocabulary with their ranges.
- `mods/aikit.TestSurvivalWarningsFollowTheObservationTick`: a warning is
  visible only at or after the tick it was published, and a list already
  handed out never changes.
- `mods/aikit.TestSurvivalBuddiesPlayTheSurvivalBrainRetail`: a four-minute
  Painted Desert Survival battle under Modern with both buddies marked
  Modern gives both the survival brain,
  which hears the warnings, and buddies thinking asynchronously play the
  same battle (equal partial-state fingerprint) as buddies thinking on the
  simulation thread.
- `mods/aikit.TestSurvivalBuddyRepairsAnAllyRetail`: a buddy's
  observation lists the human's commander, and a repair ordered on it
  becomes the buddy commander's `RepairUnit` order and restores its hit
  points.
- `internal/aikit.TestObsListsAlliedUnitsInSight`: allied units the sight
  predicate passes are listed with their state, apart from the own and
  enemy lists; one out of sight is not.
- `internal/aikit/brains/survival`: sector binning, the sector split
  between buddies, the parameters, the warned direction's draw on the
  tower share, the home clear of the human's commander, allied towers
  covering their lane, sites clear of allied buildings and lanes, allied
  repairs, the repairer that can reach, and the commander's distance
  from allied commanders.
- Isolation, checked once by hand against the base commit (headless runs,
  partial-state fingerprints and simulation draws equal): a `modern-ai`
  (now `--ai-player all=modern` under Modern) skirmish on The Pass and a
  Strict 3.1 skirmish on Great Divide (18,000 ticks), and Strict 3.1 and
  Modern Survival battles on Painted Desert with two buddies (36,000
  ticks).

### 16.7 Open

- The buddy cannot move the human's commander: a human who walks it to a
  buddy's base while a wave is there still stands inside that buddy
  commander's explosion (§16.4).
- A dying buddy commander that stands near the human's is usually one
  that cannot move: each move order it is given ends within a few ticks,
  with the commander where it was (§16.5). Why it is held — its own
  buildings, the Modern movement policies, or the order path — is not
  traced; it lies outside the survival brain.
- A buddy's early economy on a cramped or uniform-metal start (The Pass)
  is the utility economy's known gap (MODERN_AI_RESEARCH §6): the first
  factory site may lie across the map and extraction stays at the
  commander's own.
- Every value in §16.3 is a first tuning against an idle human. A
  playing human draws waves to its own buildings and fights beside the
  buddies; play-testing should decide the guard's share, the tower share
  and the human's reserved spots.
