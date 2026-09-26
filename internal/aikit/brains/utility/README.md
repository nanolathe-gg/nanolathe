# Utility-AI brain (`utility`)

Prototype computer player for the Modern AI research framework
(`docs/MODERN_AI_RESEARCH.md`). Every decision enumerates candidate
actions, scores each with explicit integer *considerations* multiplied
together, and takes the best; standing commitments and a hysteresis margin
keep builders from flip-flopping. It supplies three `core.Policy` layers
over one shared model:

```
core.New("utility", Strategy, Economy, core.WaveArmy{}, Production)
         strategy ─► economy ─► army (scripted waves) ─► production
```

`utility.Policies(p)` returns the three layers for composing with another
army layer; `utility.New(p)` is the standalone brain registered in
`cmd/ai-arena` as `utility` (`utility:hard::w_energy=120,att_min=600`).

## 1. Scoring vocabulary

All arithmetic is `int64`; no floats, no randomness, no map ranging in a
think.

* A **consideration** is a permille value: 1000 = neutral / satisfied,
  0 vetoes, >1000 amplifies. A **score** is `weight × 10 × Π
  considerations / 1000ⁿ`, so a nominal option (weight 100, every
  consideration 1000) scores **1000**. `minScore` = 30 is the floor for
  acting at all.
* Response curves (`curves.go`): `lin(x, x0, x1)` clamped ramp (rising or
  falling), `half(x, h) = 1000·h/(h+x)` hyperbolic decay (1000 at 0, 500 at
  `h`), `inv(p) = 10⁶/p` clamped reciprocal, `mul(a, b) = a·b/1000`.
* **Return** of an economy building = gain (metal-equivalent per second) ×
  100 / cost (metal-equivalent), i.e. 1000 = pays back in 100 s. Energy is
  priced at `e_ratio/10` energy per metal, both for costs and for gains.

## 2. Shared model (rebuilt first every think by the strategy layer)

| quantity | definition |
|---|---|
| spend capacity | Σ build power × median metal (energy) per work unit — buildings for builders, mobile products for factories — computed once from the side's commander build tree |
| pending | per product: max(own nanoframes, builders committed and still walking) × expected gain; so a planned solar counts before its frame exists, without double counting |
| supply | income + pending + stock/`h` (short run); income + pending only (long run); long-run energy uses *expected* income (steady output + expected wind), not the current gust |
| demand | max(expense, `demand_bp`‰ × spend capacity), but metal demand is capped by what the energy supply can accompany at `e_ratio`, and energy demand by what the metal supply can use (+ standing upkeep) — a starved resource makes the other look ample |
| coverage / need | coverage = supply/demand (‰); need = mean of the short- and long-run reciprocals, cut by up to ¾ as the store fills from 85% to 100% (a full store is waste) |
| spendable | min(metal supply, energy supply ÷ `e_ratio`) — what spending the scarcer resource allows |
| firm need | steady (non-wind) output + stock/`h` against extractor upkeep ×1.25 (extraction stops in an energy stall) |
| wind | expected scalar from the map's authored speed range (uniform draw, capped at 1 per draw; divisor 5000 per [05 "Wind generation"]); relative spread = range/√12/mean |
| enemy | army value from memory discounted by age (`half(age, 60 s)`), a staleness prior (up to half our own army when nothing was seen for 2 min), air strength, anti-air strength (units and towers), defense share, income from seen extractors |
| places | home; energy field 320 wu behind home; maker/storage field beside it; factory field 160 wu toward the enemy; decayed danger at the most threatened own building; the own building nearest the enemy; territory = `clamp(2·dEnemy/(dEnemy+dHome))` (1000 on our half, 0 at their base) using the start farthest from ours until enemy buildings are seen |

## 3. Economy layer (idle builders)

(§3–§5 describe the brain with the §11 switches off; §11 lists what each
switch changes.)

Each think it first detects failures and retreats, then scores options for
up to `Attention` builders (rotating start), within the APM budget left
after reserving one action for the army and one per factory with room.

| candidate | considerations (multiplied) |
|---|---|
| extractor at every free spot | metal need × return (spot metal × extraction / cost) × travel `half(dist/speed, travel_half)` × territory × route threat `half(Threat.LineMax(builder→spot), threat_half)` (squared for the commander) — cheap terms first, the route query only when the bound can still win |
| energy (solar / wind / tidal) | energy need (steady producers: max with firm need) × return (expected output) × **wind risk** `1 − spread × wind share after building` (all generators share one global wind) × travel × site threat |
| metal maker | metal need × `lin(need, 700, 1500)` (only while metal is genuinely short) × return × energy surplus judged on *expected* income after this maker's upkeep (a full store lowers the bar if steady income alone carries every draw) × travel × threat |
| storage | overflow (store ≥ 70% full × coverage 1.2–3.0) × `half(count)` × travel × threat |
| factory | unmet army spending: (spendable × (100−EcoShare)% − factory capacity incl. planned) / this factory's drain; first-factory urgency rising to 3000 by `fac_time`; × suitability (best product efficiency vs the best factory on offer × air plant exposure `half(enemy AA, 3000)` × same-type `half(count, 1.5)` × tech-2 readiness `w_tech × lin(metal income, 12, 30/s)`) × affordability `half(cost ÷ supply, 90 s)` × travel |
| defense | danger (decayed threat at our most threatened building, or a late-game baseline at the front; anti-air towers answer seen aircraft) × efficiency vs the best tower × `half(towers within 500 wu)` × affordability × travel |
| radar | time ramp (1–3 min) × `half(3 × count)` × affordability × travel × threat |
| assist nanoframe | resource slack `lin(min(covM, covE), 500, 1400)` × priority (factory 1500, economy 1000, defense 1000+danger, other 700) × travel |
| guard factory | `lin(covM, 900, 2500)` × army share × travel — only factories that are producing |

With the `expand` switch (off by default, §13.10), from minute
`expand_from` an extractor site is scored with a metal need of at least
1000‰ while our extractors trail the top human tier's curve, and while
metal is not short with fewer than one factory per 6 metal/s this
think's factory and guard weights rise; its contest part counts spots
on the contested line that our army holds as ours.

With the `army` switch (default 21, §13.12) the defense plan's towers
yield to production while the metal store banks and factories are
fewer than one per 6 metal/s of income, direct-fire towers are built
only for zones ground units attacked (missile towers elsewhere), and no
ground tower is owed while the towers are worth the tier's count of
missile towers or 15% of all we own.

Stability and bookkeeping:

* **Commitments**: a builder with a build order keeps it; assist/guard
  commitments are re-scored every 10 s and replaced only if the best option
  beats the current one by `hysteresis`%.
* **Reservations**: an assignment immediately adds its gain / capacity to
  the model and recomputes needs, so later builders in the same think see
  it; commitments count toward diminishing returns until their frame exists.
* **Failures**: a build issued last think that left its builder idle
  failed. The builder avoids that product/spot for 15 s; the first failure
  also blocks the spot (1 min × failures, at most 8; the count resets once
  a frame of ours stands there) or rotates that product's field a quarter
  turn about home. Three failures in a row ⇒ the builder is boxed
  in and only assists for a minute (these do not count against the site).
  With `layout`, a failed spot with a reclaimable feature in view is
  cleared by its builder (§13.9). With the `expand` switch's hold part
  a failure that meets neither a feature in view nor a building of ours
  holds the spot on the board until a unit of ours sees it empty,
  instead of the timed block (§13.10).
* **Retreat**: a builder in threat (`≥ threat_half` and > 2× our power
  there; the commander at half that, when away from home or hurt) moves
  home; after 15 s it is reconsidered.
* **One order per think**: a builder already ordered this think (a
  retreat, an exit to open, a spot to clear) is not decided again; a
  second order would replace the first.
* **Latency**: the next think always sees the applied batch (the host does
  not think while a batch is pending), so the order class plus commitments
  are enough to avoid re-issuing.

## 4. Production layer (factories)

Factories with fewer than two queued products (one building + one queued)
get one product each, within the APM budget. A factory whose pad has held
no nanoframe for 20 s while its queue is not empty is *blocked*: own units
parked on the pad are moved out along the +Z exit lane (every 10 s); after
three clears it is written off from spend capacity and its type's field
rotates.

| candidate | considerations |
|---|---|
| constructor (builder that can build extractors or energy) | max(eco spending gap (spendable × EcoShare% − builder capacity) / drain, expansion room `freeSafeSpots / (builders × spots_per_con)`), capped 1200 × `half(builders owned or coming, 4)` × energy gate `lin(covE, 300, 600)` |
| combat | army share `(100−EcoShare)×20`, doubled while the army is below the strategy's attack size × efficiency `DPS·HP / (V·(V+v_ref))` normalized to the factory's best (anti-air damage credited by `aaNeed`) × counter (`1 + aaNeed × AirDPS share × w_aa/50 + defense share × lin(range, 300, 800) × w_range/50`) × variety `half(share of army × w_mix/100, 300)` × energy gate `lin(covE, 600, 900)` (0 when the energy store is empty and falling) |
| scout (armed, fast) | only with no live scout, after minute 1, at most 3 per game, doubled while the enemy base is unknown |

Near-home threat lifts the army share to ≥1200 and removes the energy gates.
A constructor target (growth's floor and ratio, §13.4, and the `expand`
switch's builders part, §13.10) makes a constructor wanted outright
below it.

## 5. Strategy layer (posture)

* EcoShare = phase (`eco_early` → `eco_late` over `eco_ramp` minutes)
  − 30 × base pressure (enemy strength near home / (that + ours near
  home)) − up to 20 when the estimated enemy army outnumbers ours
  (`lin(ratio, 1, 3)`), clamped 10–95.
* AttackValue (the tactics army's launch threshold) = max(45% of the
  ambition-scaled army tier curve read at the style's lag, at least 300;
  estimated enemy army × `att_ratio`%), jittered per wave (`attackValue`
  in style.go; `att_curve=0` restores max(`att_min` + `att_grow`×minutes,
  …)). Production's army target is the larger of that and the jittered
  build value `att_min` + `att_grow`×minutes (`armyTarget`), so the army
  is built as before and only the launch moves (tactics README,
  "Attack value calibration").
* Aggression = our army share of the two; Tech when metal income ≥ 15/s
  after minute 6; labels `opening`, `expand`, `build-up`, `pressure`,
  `defend` (defend has hysteresis: enter at pressure 400‰, leave below
  200‰).

## 6. Parameters

Weights are percent of nominal (100 = neutral). `ParseParams` clamps into
range and rejects unknown names or non-integers (so a search never silently
runs defaults); persona keys (`think`, `react`, `apm`, `attention`,
`skill`, `async`, `label`) are ignored.

| name | default | range | meaning |
|---|---|---|---|
| `h` | 45 | 10–180 | projection horizon (s): stock counts as stock/h of supply |
| `demand_bp` | 600 | 100–1000 | permille of build-power spend capacity assumed as demand |
| `e_ratio` | 110 | 40–300 | planned energy per metal ×10 (prices energy, sets E:M balance) |
| `w_metal` | 100 | 0–400 | extractor weight |
| `w_energy` | 100 | 0–400 | energy producer weight |
| `w_factory` | 100 | 0–400 | factory weight |
| `w_defense` | 60 | 0–400 | static defense weight |
| `def_plan` | 1 | 0–1 | switch: the proactive defense plan (`defense*.go`, §12); 0 = the reactive evaluation only |
| `w_radar` | 40 | 0–400 | radar weight |
| `w_maker` | 60 | 0–400 | metal maker weight |
| `w_storage` | 15 | 0–400 | storage weight |
| `w_assist` | 50 | 0–400 | assist nanoframe / guard factory weight |
| `w_tech` | 60 | 0–400 | tech-2 factory desire |
| `w_cons` | 100 | 0–400 | constructor production weight |
| `w_army` | 100 | 0–400 | combat production weight |
| `w_aa` | 100 | 0–400 | anti-air counter strength |
| `w_range` | 60 | 0–400 | range counter to enemy static defense |
| `w_mix` | 100 | 0–400 | army variety pressure |
| `w_scout` | 60 | 0–400 | scout production weight |
| `v_ref` | 150 | 20–1000 | efficiency blend: DPS*HP/(V*(V+v_ref)) |
| `travel_half` | 35 | 5–300 | travel seconds that halve a site's score |
| `threat_half` | 20 | 2–400 | threat level that halves a site's score |
| `hysteresis` | 50 | 0–300 | percent better an option must be to drop an assist |
| `eco_early` | 75 | 20–95 | EcoShare at minute 0 |
| `eco_late` | 40 | 10–80 | EcoShare after the ramp |
| `eco_ramp` | 12 | 2–30 | minutes from eco_early to eco_late |
| `att_min` | 800 | 100–6000 | minimum wave value |
| `att_grow` | 150 | 0–1000 | wave value added per minute |
| `att_ratio` | 130 | 50–400 | wave value as percent of estimated enemy army |
| `com_radius` | 900 | 300–3000 | commander leash from home (world units) |
| `spots_per_con` | 3 | 1–12 | free safe spots per builder before more builders are wanted |
| `fac_time` | 100 | 20–600 | seconds by which the first factory is fully urgent |
| `w_air` | 100 | 0–1000 | air plant desire (percent of its anti-air-damped suitability) |
| `naval` | 1 | 0–1 | switch: water-map play (§11.1) |
| `air` | 1 | 0–1 | switch: air plants and aircraft when useful (§11.2) |
| `tech` | 1 | 0–1 | switch: timed tech-2 transition (§11.3) |
| `tech_time` | 16 | 4–40 | minutes by which the first tech-2 factory is fully wanted |
| `w_upgrade` | 300 | 0–1000 | extractor upgrade weight, percent of a new extractor's |
| `layout` | 1 | 0–1 | switch: rows and zones as people build, factory exits kept open, reclaim (§12) |
| `w_reclaim` | 60 | 0–400 | weight of reclaiming wrecks near the base and clearing factory lanes |
| `reach_mix` | 1 | 0–1 | switch: reach over the plausible enemy starts; stranded factories make a home guard only (§13.1, §13.8) |
| `fleet` | 1 | 0–1 | switch: against an enemy navy, heavier hulls, light boats kept to a few (§13.2, §13.8) |
| `tidal_field` | 1 | 0–1 | switch: water economy in fields, later shipyards on a ring, clear of the yards (§13.3) |
| `growth` | 16 | 0–31 | human economy shape at full ambition, a sum of parts: 1 expansion, 2 tech-2 timeline, 4 constructors per extractor, 8 factories per income, 16 constructor floor (§13.4) |
| `fac_first` | 0 | 0–5 | first factory family: 0 none, 1 kbot lab, 2 vehicle plant, 3 air plant, 4 shipyard, 5 drawn per game (§13.5) |
| `w_fac_first` | 300 | 100–1000 | percent by which other factories rate below the chosen family's until one of it is owned (§13.5) |
| `expand` | 0 | 0–31 | mid-game expansion, a sum of parts: 1 extractors on the expansion pace, 2 constructors follow the expansion, 4 failed spots held until seen empty, 8 contested spots behind our army, 16 factories follow the income while metal banks (§13.10) |
| `expand_from` | 15 | 4–30 | minute from which `expand`'s pace, builders and spend parts act (§13.10) |
| `army` | 21 | 0–127 | income into army and towers that pay, a sum of parts: 1 towers yield to production, 2 factories follow banked income, 4 tower types, 8 towers where attacks came, 16 a tower value ceiling, 32 the commander's zone covered, 64 towers from surplus (§13.12) |
| `metal` | 7 | 0–7 | metal use at every ambition, a sum of parts: 1 makers last, 2 energy follows metal, 4 home spots under the ambition cap (§13.14) |

`naval=0,air=0,tech=0,layout=0,army=0` is the brain before §11–§12
(verified identical, result JSON for result JSON, on a land and a water
map); one arena binary plays new against old with
`util+tac:hard::naval=0,air=0,tech=0,layout=0,army=0,label=base`.
Likewise `reach_mix=0,fleet=0,tidal_field=0,growth=0,fac_first=0,army=0`
is the brain before §13 (§13.6), and `army=0` the brain before §13.12.
`army` acts only through the defense plan and the switches' factory
evaluation, so with `def_plan=0` and `naval=air=tech=0` (the v1 string)
it changes nothing.

Fixed (not yet parameters): minimum score 30, reassess 10 s, retreat 15 s,
affordability half-life 90 s, pad stall 20 s, queue depth 2, 3 scouts.

## 7. Results (defaults, `hard` persona, 20-minute games)

Dev pool (great divide, the pass, dark side, red planet, full moon,
sherwood; seeds 1–2; both slot orders; 48 games). W-D-L is utility's.
Baseline for reference: scripted vs retail is 11-2-11 on this pool.

| opponent | W-D-L | decisive wins | kill/loss (utility) median | metal/s at end (utility:opp) | army value at end |
|---|---|---|---|---|---|
| retail | **24-0-0** | 8 | 3.4 | 32 : 13 | 10617 : 1096 |
| scripted:hard | **17-7-0** | 4 | 2.7 | 30 : 17 | 10410 : 2942 |

By side against scripted: as ARM 12-0-0, as CORE 5-7-0 (every draw is
utility as CORE, ahead on points by 2–24%, under the 30% adjudication
margin). By map, the draws are great divide 2, sherwood 2, the pass 2
(both brains are maker-limited at 9–12 metal/s there), dark side 1.

Held-out pool (metal heck, comet catcher, evad river confluence, ashap
plateau, painted desert, coast to coast; seed 1; 24 games):

| opponent | W-D-L | decisive | kill/loss median | metal/s end | army end |
|---|---|---|---|---|---|
| retail | 11-1-0 | 4 | 1.3 | 40 : 19 | 9398 : 921 |
| scripted:hard | 10-2-0 | 2 | 1.4 | 42 : 28 | 10698 : 2435 |

The one draw against retail is metal heck (a uniform-metal map) as CORE;
scripted out-extracts us there (66 vs 50 metal/s).

Economy health across the dev pool (per match mean):

| contestant | idle builders | idle factories | metal wasted | metal/s @10 min | metal/s end | extractors end | energy <20 (samples) |
|---|---|---|---|---|---|---|---|
| utility (ARM) | 5.9% | 0.1% | 346 | 20.5 | 32.6 | 18.5 | 5.1% |
| utility (CORE) | 4.4% | 0.1% | 561 | 21.1 | 29.5 | 15.2 | 5.0% |
| scripted (ARM) | 15.1% | 0.3% | 2244 | 18.8 | 22.8 | 13.3 | 4.8% |
| retail | 32% | 37% | 62–220 | 10.1 | 12.9 | 8.8 | 4.7% |

("idle builders" is the arena's count, which also counts guarding.)
Commands: ~360 applied per game, ~8 failed placements, 0 dropped by APM.

Attack sizing variant (`att_min=500,att_grow=80,att_ratio=110`, same
48-game pool, measured against the defaults of that iteration, which scored
24-0-0 / 21-3-0 with 7 and 1 decisive wins and kill/loss 10.7 / 2.3):
24-0-0 vs retail with 14 decisive and kill/loss 14.3, but 17-7-0 vs
scripted with kill/loss 1.2 — earlier waves finish retail more often and
trade worse against a real army, so the defaults keep the larger waves.

## 8. Cost

One synchronous match with `-allocs` (think = `Board.Update` + all four
layers, 2400 thinks):

| map | think mean | p99 | max | alloc per think |
|---|---|---|---|---|
| great divide | 30.5 µs | 112 µs | 618 µs | 33 B (0.12 objects; slice-table growth) |
| red planet | 26.9 µs | 109 µs | 545 µs | 24 B (0.006 objects) |

Host step mean 5–6 µs. Tournament-wide means under load (3 jobs plus other
agents' tournaments): think 21–28 µs mean, 80–92 µs p99; isolated maxima of
several ms are scheduler/GC noise (they do not repeat between identical
runs).
`async=1` produces a result JSON identical to the synchronous run (series,
kills, losses, commands, built counts, score) on both maps checked.

## 9. Strengths and weaknesses

Strengths
* Economy: ~2.5× retail's income, ~1.5–2× scripted's; near-zero idle
  factories, few idle builders, little waste; energy balanced against
  metal with no persistent stalls; no dropped actions.
* Every decision is inspectable: `Explain` lists the top five candidates
  per builder/factory decision with their considerations, plus the model
  (supply, demand, coverage, needs, enemy estimate, wind, APM budget,
  factory states).
* Robust to several engine realities found while iterating: builders boxed
  in by their own buildings, unreachable retreat points, units parked on
  factory pads, walled-in factories, gust-driven energy.

Weaknesses
* Army control is the scripted wave; most wins are on points, not
  decisive. Kill/loss is only ~2.7 against scripted and ~1.3 on the
  held-out maps.
* CORE-side games against scripted are close (draws): lower extractor
  counts, more energy trouble on windy maps.
* Uniform-metal maps: extraction lags scripted (the need model damps
  extractors when metal is not the binding constraint).
* No reclaim (features are not in the observation), no geothermal, no
  transport. Water maps, air plants and tech 2 are covered by the §11
  switches; their own gaps are listed in §11.6.
* Hand calibration between categories (e.g. the first-factory urgency) is
  where utility systems are brittle; these are exposed as parameters for
  the search.

## 10. Ideas and framework notes

* Evolutionary tuning of the table above against a mixed opponent pool
  (retail + scripted + other brains), both sides, with score ratio as a
  graded fitness rather than W-D-L.
* A per-command result in `Kit.Last` (per actor) would make failure
  attribution exact; today a build that is accepted and then dropped by
  the unit (boxed in) is indistinguishable from success until the next
  think.
* The board's enemy guess is the *nearest* other start; on maps with many
  starts that is usually not the enemy (this brain uses the farthest start
  for territory; field directions still follow the board, which measured
  better).
* `WaveArmy` rallies close to home, so new units can park on factory pads
  and stop production; the army layer should move fresh units off pads
  (this brain's production layer does it as a fallback).
* The executor's site search does not consider features around a
  factory's exit; a walled-in factory is only detected after the fact.
* An activate/deactivate command would let the economy switch metal
  makers off in an energy dip instead of avoiding them.
* `UnitInfo` could expose `MakesMetal` and a geothermal flag (read here
  from the definition and its yard map).

## 11. Water maps, air plants and tech 2 (switches)

Three switches, default on (`naval`, `air`, `tech`); with all three off
none of this runs. Diagnostics that motivated them: on island maps the
brain built a land army that could never reach the enemy (or, on hundred
isles, no factory at all: the start island has no land site), it never
built an air plant, and it reached tech 2 once in eight 40-minute games.

### 11.1 Terrain model and water play (`naval`; `air` also needs it)

Built once in the policies' `Init`, which the host runs on the simulation
thread before the first think (`water_map.go`):

* **Region maps.** `aikit.MapInfo.Reach(class)` labels the footprint
  anchors a movement class can stand on by connected region, with the
  placement validator's per-cell test (too deep, too shallow, too steep on
  land or under water; void cells excluded; features and units ignored).
  Footprints are capped (3 cells on the ground, 4 at sea), so a side
  needs about a dozen maps. Each costs ~1 ms of CPU on a 544×864-cell
  map (scanline union-find); the whole analysis is 10–35 ms once per AI
  player, mostly allocation.
* **Naval base.** The shipyard site nearest home (`MapInfo.DepthSites`,
  per sea region) in a sea that is large or comes within 1500 wu of
  another start; ships' home region is the one there.
* **Home regions and reach.** Each class's home region (nearest home, or
  the naval base for ships), its distance to every start, and the regions
  from which each metal spot can be built on.
* **Water sites.** One anchor per water building near the naval base with
  the depth band its yard map actually enforces (sampled cells bound the
  maximum depth; floating cells `w C Y` ignore it but must lie below the
  waterline), and whether a land factory with lanes fits near home.

Every armed mobile product gets a **reach** to the enemy base, permille:
1000 when its class's home region comes within weapon range + 100 wu of
the start, falling to 0 at range + 700; aircraft 1000. Until enemy
buildings are seen it is the mean over the other starts; then the start
nearest them. Reach enters:

| where | how |
|---|---|
| factory quality | best efficiency × reach among its combat products (ships included), normalized to the best factory the builder can make |
| factory need | own factory build power counts toward army spending × its best product reach (floor 150): a stranded land factory does not satisfy the army |
| combat production | efficiency × own reach (floor 150), normalized per factory, and the whole score × the factory's reach: a factory whose units cannot get there makes combat units only to defend |
| builders | only spots and sites their class can work from where they stand; a water spot takes the builder's extractor whose depth band fits it |
| water buildings | tidal generators, floating makers and defenses, underwater storage and shipyards go to their anchors; water defenses answer enemy ships seen (torpedo towers) or danger near the naval base |
| constructor production | room per constructor type: free safe spots its class can reach and extract, plus standing water work at the naval base (3 spots' worth, 6 where the tide is ≥ 15) |
| efficiency | torpedo damage counts × the enemy's naval share; paralyzer damage not at all |

### 11.2 Air (`air`)

Root cause of "no air plant, even with `w_air=1000`": the air term in
`evalFactory` tested the *plant's* `RoleAir`, which a plant never has
(only aircraft fly), so `w_air` multiplied nothing; and suitability was the
plant's best ground Lanchester efficiency (DPS×HP/(V·(V+v_ref))), which
rates aircraft at ~7% of a kbot lab (a bomber's DPS is its bombs spread
over their pass time, fighters fire at aircraft only), so it never beat a
second lab.

Now an air plant's suitability is at least `airWant`: 1000 − land reach
(full when land units cannot get to the enemy) or, on land maps, a second
line rising to 350 from minute 8 to 14; × `half(enemy AA, 4000)` ×
`w_air`/100. Its products are scored like any factory's (bombers by
efficiency, fighters with the anti-air counter when enemy aircraft are
seen, air constructors as constructors); one unarmed air scout is made
while the enemy base is unknown. Armed aircraft that are mostly anti-air
are now classified `RoleFighter` (the damage-table anti-air credit arrived
after the classification).

### 11.3 Tech 2 (`tech`, `tech_time`, `w_upgrade`)

* **Timeline.** `want = lin(metal income, 15, 28/s) × lin(t, ⅔·tech_time,
  tech_time)`, × safety `1 − lin(enemy army / ours, 0.9, 1.6)` (or a
  filling metal store). The first tech-2 factory (one that makes an
  advanced constructor) gets need ≥ 1.5·want and suitability max(q, 600)·
  want; later ones need metal income 20–40/s. A non-tech-2 depth-3
  factory (hover plant) keeps the old income gate, lifted by 1 − land
  reach.
* **Advanced constructors**: the first is wanted at 1500 as soon as a
  tech-2 factory stands, a second from 25–40/s income, a third 40–60/s,
  independent of how many ordinary builders there are.
* **Moho mines.** An advanced builder's extractor list is its richer
  extractor: on free spots it builds that; on our extractors it
  *upgrades* (`Kit.Replace`: reclaim ours, then build the new one queued
  on the same spot; gain = the difference, weighted by `w_upgrade`).
  Fusion, advanced makers and tech-2 defenses come from the ordinary
  scoring once advanced builders exist.

### 11.4 Results (defaults, `hard` persona, `util+tac`)

`util+tac` against `util+tac:hard::naval=0,air=0,tech=0` (base) and the
retail planner; fresh seeds, both slot orders, the same binary.

**Water pool** (hundred isles, sail away, shore to shore, lake shore, ring
atoll, pillopeens, brain coral, canal crossing; seeds 61–62; 20 minutes;
32 games per pairing):

| pairing | W-D-L | points | score share | decisive W / L |
|---|---|---|---|---|
| base vs retail (before) | 20-2-10 | 66% | 55% | 5 / 0 |
| **util+tac vs retail** | **32-0-0** | **100%** | **84%** | 9 / 0 |
| **util+tac vs base** | **19-12-1** | **78%** | **74%** | 12 / 0 |

Before, base lost every hundred isles and brain coral game to retail (no
factory site / a stranded army) and split ring atoll; now every map is
4-0-0 against retail. Against base: hundred isles, ring atoll and brain
coral 4-0-0, lake shore 3-0-1, pillopeens 2-2-0, sail away and shore to
shore 1-3-0, canal crossing 0-4-0 — the draws are games ahead on points
(49–56% score share) that neither side can convert: the army layer does
not yet use ships or aircraft (§11.5).

Built per game on the water pool:

| | shipyards (T1 / T2) | naval constructors | ships | air plants | bombers / fighters / air scouts | tidal | underwater extractors | floating makers | T2 factories | hover plants |
|---|---|---|---|---|---|---|---|---|---|---|
| base | 0 / 0 | 0 | 0 | 0 | 0 / 0 / 0 | 1.0 | 0 | 0.1 | 0.4 | 0 |
| util+tac | 2.2 / 0.8 | 3.8 | 17.5 | 1.6 | 5.1 / 0.6 / 1.0 | 12.9 | 6.7 | 1.8 | 1.5 | 0.3 |
| retail | 1.0 / 0.5 | 2.4 | 3.4 | 1.1 | 0.3 / 0.4 / 0.2 | 3.8 | 3.7 | 1.3 | 1.0 | 1.3 |

Rechecked with the final build (layout on, seed 63, 16 games per
pairing): 16-0-0 against retail (87% score share, 8 decisive) and 10-6-0
against base (81% of points).

**Land pool** (dev pool; seeds 61–63; 20 minutes): util+tac vs base
16-2-18, 47% of points, 49% score share, 13/13 decisive. Every seed's pair
of games is split by slot order (the side that wins a map wins it with
either brain); the switches add ~0.3 air plants and 0.4 tech-2 factories
per game there. An earlier build of the same switches scored 12-1-11 on
seeds 61–62. No regression within noise.

**40-minute mirrors** (great divide, plains and passes, the bayou, dark
side, red planet; seed 65; both slot orders; 10 games per pairing):

| pairing | W-D-L | points | built per game (util+tac) |
|---|---|---|---|
| util+tac vs base | 6-0-4 | 60% | T2 factories 0.9, advanced constructors 0.8, fusion 0.2 (base: T2 factories 0.6) |
| util+tac vs `tech=0` | 5-0-5 | 50% | as above (`tech=0`: T2 factories 0.3, advanced constructors 0) |
| `tech_time=12` vs `tech=0` | 5-0-5 | 50% | T2 factories 1.3, advanced constructors 0.9, fusion 0.5 |
| `tech_time=12,w_upgrade=700` vs `tech=0` | 5-0-5 | 50% | T2 factories 1.0, advanced constructors 0.9, moho 0.4, fusion 0.3 |

Mirrors are decided by 14–33 minutes and, as on the land pool, by slot
order: in no pair did one brain win both orders against `tech=0`, so the
tech plan is neither a gain nor a loss yet. Tech-2 factories and
advanced constructors appear three times as often; moho upgrades
(`Kit.Replace`, verified: nine mohos in one forced 30-minute game) rarely
happen before the game is decided. Earlier, stricter-timed versions lost
(4-0-8) by weakening the army at minutes 15–20: tech spending has to come
from surplus, not from the army race.

### 11.5 What the army layer must do for ships and aircraft

This unit decides what is built; the army layer (`tactics`, or
`core.WaveArmy`) commands it. For ships and aircraft to pay off it needs:

* **Ships** are in `Board.Combat` like land units, and today receive the
  same land waypoints and targets; they end up pressed against shores, and
  resolver refusals (`FailResolve` in the arena's command counts) run 10–35
  per water game against 0–10 on land. Group naval units separately, route
  them over their class's region (`Map.Reach(aikit.MoveClassOf(info))`,
  built in Init), and give them targets within weapon range of water:
  enemy ships and submarines, water extractors, tidal generators and
  shipyards first, then coastal buildings (destroyers and cruisers outrange
  most coastal towers). Submarines (`WaterDPS`) can only hit water targets.
* **Aircraft** idle at home. Bombers (`RoleBomber`) should raid economy
  targets away from seen anti-air (extractors, energy, factories); gunships
  follow the army; fighters (`RoleFighter`, now classified correctly)
  patrol over the army and base and answer enemy aircraft; the unarmed air
  scout (`RoleScout|RoleAir`) should visit uncleared start positions so
  the enemy base becomes known (which also switches this brain's reach
  table from the mean over starts to the right one).
* **Hovercraft and amphibious units** cross water that land units cannot:
  route them over their own class's region, not the land one.
* Air constructors are economy units: leave them to the economy layer.

### 11.6 Remaining gaps

* The army layer does not command aircraft and treats ships as land units
  (§11.5); most water games against base end on points or as draws.
* Reach ignores features and units (a forest can still wall a region) and
  caps footprints at 3 cells on land, 4 at sea.
* The commander keeps its leash, so island maps rely on naval, air and
  hover constructors for spots beyond it; a hover plant needs a land
  constructor first.
* Initialization costs 10–50 ms on the first AI tick of a large water map
  (about a dozen region maps plus site searches); moving map analysis to
  battle load would hide it.
* The first tech-2 factory arrives around minute 16 in mirrors that are
  usually decided by then; upgrades compete with fusion on the ordinary
  return scale and are rare. A surplus-driven or opponent-aware timing
  (tech when the enemy techs, or when the army is safely ahead) is the
  next thing to try.

## 12. Base layout (`layout`, `w_reclaim`)

Play-test report: in long games rows of wind generators, metal makers and
trees sealed a kbot lab and a vehicle plant (every unit they made stayed
inside), and the base became a sprawl of loosely spaced buildings, trees
and unreclaimed wrecks. The first answer (a street grid of 16-cell
blocks) kept exits open but cost land strength and made bases less
compact. The layout now follows a benchmark of how people build:
`tools/ai-layout-bench` over ~1,900 human land-1v1 player-games
(`~/ta-demos/derived/layout/REPORT.md` §8, rules L1–L7). Its conventions
are the same for winners and losers, so they are adopted as style.

**Rows** (executor, `layout.go`; the brain asks with `Kit.BuildKeep`).
For land buildings:

| rule | what the executor enforces |
|---|---|
| L1 | energy buildings touch each other, and so do makers and storage: a same-class neighbour is flush (gap 0) or a street away, never 1–2 cells (too narrow for any mover) |
| L2 | a block (flush same-class buildings) is a row: at most 8 buildings, 24 cells long, 10 deep (two ranks) |
| L3 | streets between blocks at least 3 cells (a 3×3 tank passes) |
| L4 | nothing within 3 cells of a factory |
| L5 | a factory's front (+Z, where its units leave) clear of buildings over its full width plus a cell for 12 cells; the first 8 must be ground its units can drive on (no cliff, no deep water) and, with 3 cells on its other sides, free of blocking trees, rocks and wrecks; a factory site whose front keeps a metal spot out of it (and a cell around it) is preferred, and no extractor is placed in an own factory's front (§12.4) |
| — | towers, radar and other buildings keep 2 cells from anything; extractors stay on their spots, water buildings at their anchors |
| — | nothing stands in a terrain passage: a site is refused when, across any row or column of its footprint, ground the nearby factories' units cannot cross lies within 8 cells on both sides (a ramp, pass or gully; a single cliff or the map edge is fine) |

A row building first extends a nearby row of its class (along the row
before a second rank, nearest the request point; a row within 700 wu of
the point, or the radius the brain sets with `Kit.SetRowNear`: 250 wu
with the wider base, §12.4); failing that it starts a new row at the
nearest anchor to the request point that keeps the rules (up to 40 cells
away). A building that fits nowhere under the rules is not
placed (the brain's failure handling moves its field).

**Zones** (brain, `econ_layout.go`) — L6–L7: factories forward (bearing
0/±15/±30/±45° about the enemy direction in turn, 280 + 130 wu per
factory, at most 1,100 wu or 35% of the way), energy on the flanks (70°,
alternating sides every six buildings, 220 + 18 wu per building, up to
1,300), makers and storage at 45° on the other flank (240 + 22 per
building). The first buildings stand close (the commander builds them) and
each one moves its class's point outward. Nothing is zoned behind the
start, and the perimeter and forward strips are left to the defense plan
(`def_plan`, `defense.go`): the executor puts each tower at the nearest
cell to the plan's point that keeps 2 cells from everything — never packed
into a row, so no tower narrows a 3–5-cell street — outside factory lanes
and terrain passages, and through the exit guard (measured: median 14–41 wu
from the plan's point, 90% within 100 wu, none refused, over three
20-minute mirrors). With `naval` on, water towers stand at the
naval base and are also wanted while danger stands there or at a water
extractor (with `def_plan=0` the reactive evaluation covers that case).

**Exit guard.** For each own factory near a site (up to four), a local
picture 81×81 macro cells of 2×2 plot cells (a 1280 wu radius): free,
removable (a reclaimable blocking feature, one of our buildings, priced
3 + value/50, extractors ×4) or blocked (terrain the factory's units
cannot cross, permanent features, factories, anything not ours). A site
is refused if the free cells no longer connect the macro row in front of
the factory to the window edge, and a factory site is refused if its own
exit is closed. Pictures are reused for 10 s; sites accepted since are
overlaid until their frames appear.

**Opening a sealed exit.** The brain counts ground combat units that stay
within 320 wu of where they appeared for 90 s and charges them to the
nearest factory; two stuck units there (or a pad that stays blocked after
two clears) send the nearest builder that can reclaim — the commander
only when no constructor can — with `Kit.Unblock`, at most every 45 s per
factory. The executor runs a Dijkstra over removal costs from the exit to
the window edge and queues reclaims of up to four blockers along the
cheapest way out, nearest the exit first; an open exit is left alone.

**Features and reclaim.** `Obs.Features` lists the trees, rocks and wrecks
within 1600 wu of the start that block movement or hold metal, refreshed
by the executor every 10 s when it applies a batch (in the owner's view;
the observation's other fields are untouched). The brain clears a blocking
feature in a factory's front lane first, then — while metal is needed —
the richest pile of wrecks per distance (`Kit.Clear` reclaims up to four
features within 400 wu of the point: blocking ones in factory lanes, then
those with metal). A clear that finds nothing rests clearing for two
minutes. `w_reclaim` weights it.

**Metal makers** (layout or tech switch). A maker or moho mine under way
counts its upkeep against expected energy, and none is started for 90 s
after an energy stall. Rows make makers easy to place; without this, 30–40
of them starved the extractors in 40-minute games (metal income swinging
between 5 and 100 as energy ran out).

**Ships.** With naval on, the same tracker follows ships: when three or
more have not moved 320 wu from where they were launched for 90 s, no more
ships or shipyards are built — the army layer is not using them.

### 12.1 Results

**Layout** — the benchmark's measures (`tools/ai-layout-bench`, util+tac
mirrors on its six land maps, 40 minutes; medians, shares pooled). *Before*
is the report's util+tac column (the brain before this section); *streets*
the first answer (24 player-games, seeds 101–102); *rows* the current
build (36 player-games, seeds 101–103, defense plan on).

| rule | measure | humans | before | streets | rows |
|---|---|---|---|---|---|
| L1 | energy touching another, 10 / 20 min | 80% / 88% | 24% / 32% | 85% / 88% | 100% / 97% |
| L1 | nearest gap energy (15), maker (20), cells | 0, 0 | 1, 2 | 0, 0 | 0, 0 |
| L2 | buildings per block; long × short cells (15) | 5; 18 × 7.5 | 4; 17 × 12 | 4; 12 × 10 | 5; 18 × 9 |
| L3 | street between blocks, 10 / 20 | 3 / 3 | 2 / 2 | 4 / 4 | 3 / 3 |
| L4 | factory → nearest building, 10 / 20; touching (20) | 3 / 3; 0% | 2 / 1; 33% | 4 / 1.5; 50% | 3 / 3.5; 0% |
| L5 | least clear side, 10 / 20 | 3 / 4 | 2 / 1 | 5 / 0.5 | 5 / 5 |
| L5 | +Z clear ≥ 12 (20) | 64% | 60% | 30% | 100% (72% counting map trees) |
| L5 | other sides < 3 (20) | 8–15% | 26–31% | 22–31% | 1–3% (2–4% with trees) |
| L6 | economy radius p80, 10 / 20 (wu) | 855 / 1202 | 414 / 504 | 421 / 565 | 484 / 791 |
| L6 | core density, 10 / 20 | 0.14 / 0.17 | 0.21 / 0.30 | 0.18 / 0.26 | 0.18 / 0.18 |
| L6 | structures, 10 / 20 | 40 / 90 | 35 / 73 | 32 / 60 | 34 / 76 |
| L7 | factories: distance, bearing (10) | 638, 38° | 252, 33° | 346, 37° | 466, 28° |
| L7 | energy: bearing, share behind (10) | 60°, 0% | 142°, 75% | 133°, 63% | 65°, 0% |
| L7 | makers: distance, bearing (20) | 768, 46° | 325, 87° | 318, 78° | 262, 45° |
| — | towers standing, 10 / 20 (defense plan) | 7 / 26 | 0 / 0 | 0 / 0 | 2 / 17 |

**Points** against the switches-off brain (`naval=0,air=0,tech=0,layout=0`;
`style=balanced,jitter=0` on both sides, both slot orders, the defense
plan on both):

| games | W-D-L | points | earlier builds |
|---|---|---|---|
| 20 min, land pool (six maps), seeds 64–67 | 21-10-17 | 54% | rows before the defense merge 61%; streets 46% |
| 40 min, show down, plains and passes, evad river confluence, greenhaven, seeds 82–84 | 6-11-7 | 48% | rows before the merge 54%; streets 48% |

**Defense plan after the integration** (its own guard: `util+tac:hard`
against `def_plan=0`, the 12-map land pool, seeds 91–98 on the six
development maps and 91–94 on the six held-out ones, both slot orders,
20 minutes): 49.3% ± 2.8 (s.e., 143 scored games), against 45.8% ± 2.7
measured on its own branch (without §11–§12). Towers standing at 10 / 15 /
20 minutes 1 / 6 / 13 (22 traced 40-minute games, seeds 93–94) against 1 /
5 / 14. Tower sites land a median 14–34 wu from the plan's point, 89–92%
within 100 wu, none refused (three 20-minute mirrors).

**Trapped units** (the arena's count; 40-minute games): mirrors worst 3,
mean 0.39 per player (18 games); defense plan against `def_plan=0` worst 2
on either side (24 games); against the switches-off brain worst 3 (mean
0.33) where the switches-off side has 4 (0.88). Before the passage rule
The Pass reached 10 in mirrors and 16–26 on the `def_plan=0` side: radars
(sited halfway to the army's rally point) and a tower stood in the ramp
that is the base's only way down, and 130+ units queued there back to a
kbot lab whose exit, by the guard's picture, was open (the same game now
ends with 2). The remaining counts of 2–9 in 20-minute games
on coast to coast are an army held at its gathering point in open ground
beside a factory, not a sealed exit (rendered).

### 12.2 Cost and remaining gaps

Synchronous `-allocs` matches (20 minutes, all switches and the defense
plan on, against the switches-off brain with `def_plan=0`): think 40–73 µs
mean and 120–207 µs p99 (the opponent 30–44 and 90–129), 43–233 B and
0.03–0.08 objects allocated per think; the host step mean rises 2–3 µs
(row planner, passage test and exit guard run in the executor). `async=1`
reproduces the synchronous result exactly (sail away, 10 minutes), and the
switches off reproduce the brain before this section result for result
(great divide, sail away, and with `def_plan=0`).

Gaps:
* Spread: bases are compact and zoned; §12.3's front rules move makers
  out (257 → 571 wu against people's 768) but the economy radius at 20
  minutes stays near 800 wu against 1,202, because the executor extends
  any row within 700 wu of a request point first. §12.4's wider base
  sets a smaller radius with `Kit.SetRowNear` (so `def_plan=0` and
  `layout=0` games keep the default).
* The trapped metric also counts an army held at home; a factory-exit
  measure (the guard's picture reports sealed exits) would count only
  what the layout can cause.
* The passage test uses terrain only: a gap between a cliff and a line of
  permanent rocks, or between two of our rows, is judged by the exit guard
  alone.

### 12.3 Front rules: tower pacing, chokes and spread

With `def_plan=1` and `layout=1` (the defaults) the plan and the layout
follow the human front (design notes in the header comments of
`defense_front.go`, `econ_layout.go` "Spread" and `aikit/layout.go`
"Passages"); `def_plan=0` and `layout=0` games are bit-identical to the
builds before it (great divide, the pass, sherwood).

* **Pacing and mix.** The base budget is 48‰ of income from minute 6
  (rising from 3:30), flat to minute 20, then 30‰ more by minute 30,
  still through `wScale`, slack, raids and army balance. A missile tower
  (anti-air whose ground fire is worth its cost) also serves the ground
  plan and is its reference tower; direct-fire towers get no weight until
  missile towers are 60% of ground-plan towers (full from 85%). The first
  tower is a missile tower whenever the builder can make one, and the
  commander leaves ground towers to constructors that can.
* **Chokes.** `aikit.MapInfo.Chokes`, run once at Init (0.6–14 ms on the
  pool, about 31 ms on greenhaven), finds the narrow places on land routes
  from the enemy starts to our start and our-side spots and what each one
  guards; each choke is a ground zone of up to four towers at sites beside
  its mouth on our side, never inside the passage (`InPassage`, the same
  rule as `inCorridor`). A choke's neck holds only its post's towers, and
  economy and factory points keep 480 wu from chokes guarding home.
  Grid-zone exposure is 250 + 4f (was 500 + 2f) and towers stand
  0.4 wu per permille of front fraction further ahead.
* **Spread.** Energy and maker request points move out 36 wu a minute
  (from 300 and 360 wu), at least 160 wu beyond the class's farthest
  building on that flank; factories start at 340 wu, then 150 wu apart;
  points stay in home's region and behind chokes guarding home.

Plan against `def_plan=0` (12-map land pool, both slot orders, 144 games,
20 minutes):

| build | W-D-L | points | score share | missile / light / heavy built per game |
|---|---|---|---|---|
| before | 51-31-62 | 46.2% ± 3.7 | 48.9% ± 2.9 | 6.2 / 3.5 / 1.7 |
| front rules | 47-38-59 | 45.8% ± 3.6 | 47.4% ± 2.8 | 12.9 / 3.4 / 2.0 |

Paired on the same games, points −0.3 ± 3.7. Water pool against retail
29-3-0 → 30-2-0. Easy and medium against retail (12 games each) build 7.3
and 4.6 towers per game; hard against `def_plan=0` 18.3.

Towers (layout-bench mirrors, six maps, seeds 101–103, 40 minutes, 36
player-games; medians):

| measure | before | front rules | humans |
|---|---|---|---|
| standing 10 / 15 / 20 min | 2 / 8 / 20 | 4 / 10 / 24 | 5 / 13 / 21 (original TA) |
| first tower / first missile tower (min) | 4.9 / 12 | 6 / 6 | 5.9 |
| missile towers of all at 20 min | 13 of 20 | 17 of 24 | ~9 in 10 |
| forward fraction at 20 min | 0.13 | 0.21 | 0.21 (original TA 0.30) |
| beyond a quarter of the way / behind the start | 19% / 3% | 43% / 3% | 42% / 8% |
| within 400 wu of an own extractor | 63% | 64% | 46–52% |
| extractors in anti-air range | 51% | 70% | 67% |

Layout on the same bench, before → front rules (humans): economy radius
p80 478 / 797 → 614 / 791 wu at 10 / 20 min (855 / 1202); makers at 20
min 257 → 571 wu (768); energy distance 349 / 512 → 464 / 544 (514 /
728); factory distance at 10 min 473 → 512 (638); structures at 20 min
80 → 74 (90); L1, L3 and L5 unchanged or better.

Trapped units: bench mirrors worst 1 → 3 (mean 0.17 → 0.22); a 40-minute
trap set (the pass, show down, plains and passes, greenhaven, sherwood,
wretched ridges) worst 2 → 2; The Pass over six seeds 3, mean 0.50 (12
before the neck rule: an army gathered in the ramp with rows and towers
in front of its mouth). Think cost in matched mirrors rises a few µs
(red planet 36 / 47 → 45 / 42 µs mean; the pass 17 / 26 → 20 / 29).

Gaps: towers at 10 and 15 minutes trail people (4 and 10 against 5 and
13, interquartile 3–5 and 6–14) and pushing further costs points; chokes
are rare on the pool (The Pass and one side of Ashap Plateau); tunables
(`frontEarly`/`frontLate`, `frontMixLo`/`frontMixHi`, `frontExp`,
`frontAhead`, `spread*`, `chokeNeck`, `chokeMaxClr`) are constants, not
parameters.

### 12.4 Towers on time and a wider base (`tower_time`, `wide_base`)

Unit G3. With `def_plan=1` and `layout=1` (the defaults) the §12.3 build
held 4 / 10 / 22 towers at 10 / 15 / 20 minutes (people: original-TA
winners 5 / 13 / 21, ProTA 7 / 15 / 26) and an economy radius of ~830 wu
at 20 minutes (people ~1,200). Two parts answer it, each with a player-spec
switch read by `VarietyFrom` (not a `params.go` row): `wide_base=0` turns
the wider base off, and tower timing is **off by default** (`tower_time=1`
turns it on; see "Default" below). With both off the game is the §12.3 one
except for the factory-lane fix below, which applies whenever `layout` is
on. Design notes are in the header comments of `defense_time.go`,
`layout_base.go` and `defense_audit.go`.

**Audit.** The arena's Report now carries a tower audit (`tower_*`: every
think a ground tower is owed — deficit at least half the reference tower —
is resolved at the next as ordered, no site, no builder deciding, or priced
and not taken, and each builder's pricing as built, place failure,
commander veto, opening metal wait, energy wait or outscored; also owed
spells, banking thinks and the budget's mean terms) and a zone audit
(`layout_*`: request points capped or pulled back, the enemy estimates at
10 and 20 minutes). Before, on the layout bench (18 mirrors, 40 minutes):

| phase | owed thinks | owed thinks: no builder deciding / no site | pricings: built / commander veto / metal wait / energy wait / outscored | mean owed spell |
|---|---|---|---|---|
| before minute 10 | 30% | 96% / 0% | 221 / 204 / 44 / 12 / 52 | 27 s |
| minutes 10–20 | 52% | 90% / 2% | 1051 / 501 / 0 / 36 / 213 (28 place failures) | 10 s |
| after 20 | 70% | 74% / 15% | 1726 / 2531 / 0 / 51 / 999 (106) | 11 s |

The review's leads, verified: the banking rule (`style.go`) returns before
its tower boost at full ambition (the store banked in 20% of thinks before
minute 10, 7% below the tier's tower curve); the commander vetoes a third
of the pricings (it builds only the light tower and the front rules leave
ground towers to a constructor that can build a missile tower); the energy
wait vetoes few (12 of 533 before minute 10). Builders take a tower almost
whenever one prices it: the budget binds early, not the conversion.

**Tower timing** (`defense_time.go`): the front rules' base share ×1.5 to
minute 10, back to ×1 by 16 (`timeBoost`); the banking rule's tower boost
at full ambition too; `pickZone` tries six zones (no site in 26% of owed
thinks at minutes 10–20 with the pacing and three zones, 7 bench games;
1% with six); and a ceiling — no ground tower is owed while towers standing, framed or
ordered reach 1.2× the tier's defense curve (`topDefenses` at the
persona's ambition: 8.5 / 16.2 / 23.8 at full ambition), at least one. A
first build also let the commander build its light tower when the plan
was a whole one behind: its commander was killed twice as often (12 to 6
in 96 games) and it had no ceiling (28 towers at 20 minutes); both were
reworked. The energy wait was left alone (few vetoes).

**Wider base** (`layout_base.go`): the zones ask the executor
(`Kit.SetRowNear`, every think) to extend only rows within 250 wu of the
request point instead of 700; the zones' caps and directions read
`enemyBase` — the start nearest an enemy player's remembered factories (or
other base buildings, not extractors or towers), before any the farthest
start — instead of the board's estimate (at 10 minutes enemyBase was
within 50 wu of the true enemy start for 28 of 36 players, the board's
for 1; median error 1 wu against 469, the board's reading 0.89 of the true
distance at 10 minutes and 0.82 at 20); an energy or maker flank narrows
toward the enemy (energy 70°/55°/40°, makers 45°/30°) until its ray keeps
the requested distance on home ground and 96 wu inside the map, then tries
the other flank (the home-ground rule had pulled 18% of energy points back,
5% now); and a factory that fails to place turns to the next front bearing
and moves 160 wu further out per failure since the factory count last
changed (a great divide start by the south edge failed its first factory
for six minutes when aimed at the true enemy start).

**Factory lanes** (executor, a fix): a factory whose kept front lane
covered a metal spot was walled in by the extractor built there later (4
of 277 factories in 18 mirrors; one first factory made a constructor that
never left its pad and its side lost at minute 10). A factory's new-row
search now first keeps its lane (and a cell around it) off metal spots,
then allows one; and no extractor is placed in an own factory's lane
(standing, framed or accepted). 0 of 249 factories faced an extractor after.

Layout bench (util+tac mirrors, six maps, seeds 101–103, 40 minutes, 36
player-games; medians, shares pooled; before is b3777c4e):

| measure | before | after | humans |
|---|---|---|---|
| towers standing 10 / 15 / 20 min | 4 / 10 / 22 (IQR 2–5 / 7–13 / 14–29) | 5 / 12 / 23 (4–6 / 10–15 / 17–25) | 5 / 13 / 21 (original TA); 7 / 15 / 26 (ProTA) |
| towers, means | 3.6 / 10.2 / 21.8 | 5.1 / 11.8 / 21.1 | |
| first tower (min) | 5.5 | 5.4 | 5.9 |
| forward fraction at 20; beyond a quarter / behind | 0.20; 41% / 3% | 0.17; 34% / 3% | 0.21; 42% / 8% |
| L6 economy radius p80, 10 / 15 / 20 (wu) | 597 / 702 / 832 | 649 / 761 / 1018 | 855 / – / 1202 |
| L7 energy distance 10 / 20; bearing, share behind (10) | 460 / 531; 66°, 9% | 543 / 734; 67°, 0% | 514 / 728; 60°, 0% |
| L7 makers distance, bearing (20) | 552, 45° | 588, 45° | 768, 46° |
| L7 factories distance, bearing (10) | 500, 34° | 502, 29° | 638, 38° |
| L1 energy touching another, 10 / 20 | 95% / 96% | 91% / 92% | 80% / 88% |
| L2 buildings per block; long × short (15) | 5; 19 × 9 | 5; 19 × 9 | 5; 18 × 7.5 |
| L3 street, 10 / 20 | 3 / 3 | 3 / 3 | 3 / 3 |
| L4 factory → nearest, 10 / 20; touching (20) | 5 / 4.5; 3% | 4 / 4; 4% | 3 / 3; 0% |
| L5 least clear side, 10 / 20; +Z ≥ 12 (20); others < 3 | 6.5 / 7; 100%; 0–2% | 7 / 7.5; 99%; 0–1% | 3 / 4; 64%; 8–15% |
| L6 core density, 10 / 20; structures | 0.14 / 0.15; 32 / 78 | 0.13 / 0.14; 32 / 74 | 0.14 / 0.17; 40 / 90 |
| trapped (arena count) worst, mean | 1, 0.08 | 1, 0.08 | |

Without the pacing boost (the other three tower changes only) the bench
held 4 / 11 / 22: the boost is what brings the early towers.

Points (protocol v2: 12-map land pool, map-mixed seeds, random starts,
both slot orders, 20 minutes; shares for the first-named side, 90%
map-cluster intervals):

| games | first side's points share | W-D-L (commanders killed / lost) | notes |
|---|---|---|---|
| both parts against both off, seeds 301–308, 192 | 49.2% [44.8, 53.4] | 57-75-60 (17 / 16) | margins 1.5 / 2.0: 47.7% / 49.0%; towers built per game 21.8 (value 4,157) against 22.4 (4,605) |
| tower timing alone against both off, seeds 311–314, 96 | 43.8% [38.0, 49.0] | 22-40-34 (7 / 7) | 51.0% / 50.5% for the off side at margins 1.5 / 2.0: small points losses at 20 minutes; the same towers per game (23.1 / 23.3), earlier |
| the first tower-timing build alone (commander rule, no ceiling), 96 | 42.7% [38.0, 47.4] | 20-42-34 (6 / 12) | reworked |
| tower timing without the pacing boost, 96 | 46.4% [42.2, 50.5] | 24-41-31 (10 / 9) | the bench's towers barely move (4 / 11 / 22) |
| wider base alone against both off, 96 | 55.7% [49.0, 62.5] | 37-33-26 (11 / 8) | |
| plan (both parts) against `def_plan=0`, seeds 301–308, 192 | 38.5% [30.5, 46.6] | 46-56-90 (15 / 55) | towers built per game 21.1 (value 4,095) against 1.1 (347) |
| plan with both parts off against `def_plan=0`, same games | 44.3% [37.8, 50.8] | 54-62-76 (18 / 37) | margins 1.5 / 2.0: 43.8% / 45.1% |

So the defense plan already trailed the reactive evaluation under
protocol v2 (44.3%; §12.3 measured 45.8% under v1), and this round
widens the gap against that brain by about six points (paired on the
same 192 games, `def_plan=0` takes 55.7% against the plan with both parts
off and 61.5% against it with them on; the plan side's commander is lost
55 times against 37) while it is level against the same brain without
the two parts (49.2%). Red planet decides much of it (`def_plan=0` 72% →
94% on its 16 games): there the plan side's metal income at 10 minutes
was 11/s against the reactive side's 22/s (16/s with both parts off),
which is not yet understood. Water pool against retail (8 maps, seeds
301–302, both orders, 32 games): 30-2-0, as before.

Cost and determinism: synchronous `-allocs` matches of both parts
against both off (20 minutes, seed 301): sherwood think 55.8 / 57.0 µs mean, p99 158 / 152,
host step 10.7 / 8.7 µs; comet catcher 71.4 / 78.1 µs, p99 208 / 230,
step 11.5 / 12.2 µs; 50–78 B and under 0.1 objects allocated per think.
In the land tournament the two think 40.0 / 39.5 µs mean. `async=1`
reproduces the synchronous game exactly (sherwood mirror, seed 302, 20
minutes). The audit reads the plan's state and never feeds a decision:
the instrumentation commit alone reproduced the build before game for
game (18 bench mirrors, 40 minutes).

Bases (rendered at 20 minutes, before and after, on comet catcher, great
divide, painted desert, red planet, sherwood and the pass): the energy
now runs out along both flanks in rows of four to six, block after block
with 3-cell streets (comet catcher to ~1,200 wu, where it had ringed the
start within ~500), the flank that met a map edge turned toward the enemy
instead of stopping (painted desert), and nothing stands behind the
start; the factories spread forward as before, lanes open. Some starts
show single energy buildings where a flank's point moved on before a
second joined them (red planet), which is why L1's touching share fell
from 95% to 91% (people 80–88%). The pass keeps its compact base (the
chokes' mouth rule and the terrain hold it).

Gaps: the economy radius at 20 minutes is ~1,000 wu against people's
~1,200 — makers stay at ~590 wu (people ~770) and factories at ~500 at 10
minutes (~640), and the zones' absolute caps (factories 1,200 wu, energy
1,500, makers 1,400) bind in a third to a half of requests; early towers
cost points against the same brain without them (the paced towers are the
same number by minute 20, built earlier, and the score at 20 minutes is
economy and army), and the round loses ground against `def_plan=0`
(above); the audit's "no builder deciding" dominates owed thinks
because only an idle or reassessing builder prices a tower — a fairer
measure would count decision opportunities. Tunables that are constants
and could become parameters: `timeEarly`/`timeFull`/`timeFade`, `timeCap`,
`timeZones` (`defense_time.go`); `wideRowNear`, `wideEdge`,
`wideFacStep`/`wideFacSteps` and the flank angles (`layout_base.go`); and
the switches `tower_time` and `wide_base` themselves.

**Default.** Re-measured after merging with the tactics round's early
raids (which turn back at towers), 12 land maps × 8 seeds, protocol v2,
192 games per pair: the brain without tower timing took 53.4% of the
points [49.2, 57.6] against the one with it (before the merge, 56.2%), so
tower timing is off by default and `tower_time=1` keeps it for play-tests.
The defense plan itself still costs points in brain-against-brain play:
`def_plan=0` took 59.1% [52.1, 65.4] against the default and killed the
commander 50 times against 21. It stays on because play-testers asked for
base defenses; making its towers pay is open work. (Re-measured on the
build before §13.12: 56.5% [49.5, 63.0], 43 commander kills against 24.
With the `army` switch's default, §13.12: 52.3%, 39 against 25.)

## 13. Growth and production mix (E3a)

Six items. The bomber roles are a fix; every other item is a parameter
whose off value reproduces the brain before this section result for
result (§13.6). Defaults since E4 (§13.8): `reach_mix=1,fleet=1,tidal_field=1,growth=16`
(`fac_first=0`), the fleet switch acting only against an
enemy navy; the brain before §13 is
`reach_mix=0,fleet=0,tidal_field=0,growth=0,fac_first=0` (with the
tactics army's `unseen=0` for its E4 part).
Human evidence: the recorded-game benchmark (`tools/ai-human-bench`,
REPORT.md sections 3, 8 and 10 (a) and (d)); aggregates only.

### 13.0 Bomber roles (`aikit.BuildTable`, a fix)

Roles were assigned before the damage-table anti-air credit, and the
reclassification that followed turned any aircraft whose anti-air credit
was at least half its damage into a fighter. The tech-2 bombers (armpnix,
corhurc) carry, besides their bombs, an anti-air gun whose credit
outweighs the bombs, so they came out as fighters. A dropped weapon never
acquires a target on its own [06 §3.2], so an aircraft with one is now a
bomber first, and a bomb's own damage table never counts as anti-air. A
retail-asset test pins armpnix and corhurc (bombers), armthund and
corshad (tech-1 bombers) and armfig and corveng (fighters).

### 13.1 Reach (`reach_mix`)

Until enemy buildings are seen, the reach table (§11.1) is a prior over
where the enemy is. It was the mean over every other start, which on maps
with clustered starts counts the starts right beside ours: on shore to
shore four of the nine others were land-reachable (three stand within
256 wu of ours), so land reach read 444‰ while the enemy sat across the
sea, and the brain built 34–71 tanks there and no shipyard or hovercraft;
on coast to coast the same prior (444‰, 375‰ without our own neighbour)
kept a vehicle plant making tanks. With the switch:

* **Plausible starts.** The prior is the mean over the starts farther
  than 400 wu from home that no mobile unit of ours has stood within
  350 wu of while no enemy building was seen (the board's own rules for
  "our start" and a cleared start); when every one has been looked at,
  the whole prior returns. Seen enemy buildings select their start as
  before, never one of ours.
* **Stranded factories.** A factory whose best combat product reaches
  below 300‰ is stranded: it still makes constructors, and combat units
  while the stranded home guard (combat units reaching below 300‰) is
  worth less than `att_min` plus a quarter of the estimated enemy army,
  or while the base is threatened; no second stranded factory is built
  (only a first one, for constructors, or the tech plan's first tech-2
  factory).
* **No air plant first while the land army may reach** (land reach at
  least 300‰). The lower prior tipped the air-favouring twofac style
  into an air-first opening on evad river confluence (17 bombers, a
  points draw against retail where the brain without the switch won);
  air-first openings score a third of the points where tanks reach
  (§13.5).

Production then shifts to what reaches (air, amphibious tech 2, ships):
on the water pool against retail (seeds 111–112, 32 games) the land army
built per game fell from 31.2 to 16.2 units and aircraft rose from 4.3 to
7.2, at the same 29-3-0; on shore to shore (seed 111) 34 tanks became 7
tanks, 6 amphibious tanks and 16 bombers. The brain's own reach model
says hovercraft reach no better than ships on coast to coast, so no
hover plant is built there; the shipyard is the first factory once the
nearby starts are seen empty.

### 13.2 Fleet (`fleet`)

Square-law efficiency (DPS·HP / (V·(V+v_ref))) favours the cheapest hull:
fleets were mostly patrol boats (68 on one pillopeens game). Human fleets
in the stock unit set (the original game's 1v1 recordings with ships,
58 player-games) are about one light boat in seven, the rest destroyers,
submarines and tech-2 hulls; in ProTA (a rebalance) patrol boats are 80%.
With the switch naval combat products are rated with v_ref × 4 (at the
default 600 a destroyer out-rates a patrol boat), and light boats — a
quarter of the value of their shipyard's heaviest hull or less — are kept
to two plus one per six ships for scouting and raiding. Destroyers also
carry sonar and depth charges (torpedo damage already counts against the
enemy's naval share).

| water pool vs retail, per game | light boats | destroyers | tech-2 hulls | W-D-L | commander kills |
|---|---|---|---|---|---|
| before (seeds 111–112) | 10.2 | 3.9 | 1.2 | 29-3-0 | 7 |
| `reach_mix` only | 10.4 | 4.1 | 1.5 | 29-3-0 | 7 |
| `reach_mix,fleet` | 3.4 | 5.3 | 0.7 | 29-3-0 | 9 |

### 13.3 Water layout (`tidal_field`)

Every water building was requested at its product's anchor, the valid
site nearest the naval base, which is where the shipyard stands: the
executor packed tidal generators, floating makers and storage around the
yards (coast to coast: 28 tidal generators ringing two shipyards, a
single lane left open), and the executor's exit guard does not cover
shipyards (their units do not walk out; layout.go). The fix is in the
brain's request points only:

* the water economy goes to fields 440 wu from the naval base in eight
  directions, none within 300 wu of a yard site or in a yard's +Z exit
  lane (520 wu), each workable by every builder class that could work the
  product's anchor; fields fill in blocks of six, nearest home first;
* the first shipyard stays at the naval base; later ones (tech 2
  included) go to a ring 760 wu out on the same sea, 400 wu apart and out
  of each other's lanes, built by the classes that reach a ring site from
  home (the naval constructors; the commander on the shore works the
  first yard's anchor but not the ring). With the fields alone the space
  by the naval base stayed open and five shipyards packed in side by side
  on sail away (seven ships never left); with the ring they stand apart
  and one ship stayed.

Ships that never left their launch point (stayed within 320 wu for 90 s,
from 5-second replay traces; water pool against retail, seeds 111–112):
0.66 per game before (21 in 32 games), 0.06 with the defaults (2).

### 13.4 Economy shape (`growth`)

The benchmark's recommendation (d): human land-map winners hold 0.57 /
0.66 / 0.76 constructors per extractor at minutes 10 / 15 / 20 (util+tac
0.38 / 0.41 / 0.41), keep adding extractors after minute 15 (25 → 33;
util+tac plateaus near 14), build one factory per ~14 metal/s of income
with the second around minute 8 (util+tac twice as many), and 73% of
winners have a tech-2 factory, at a median minute 12. `growth` is a sum
of parts, at full ambition only (easy and medium keep their tier caps,
style.go):

| part | what it does |
|---|---|
| 1 expansion | extraction territory full to two thirds of the way to the enemy base (contested spots become claimable), extractor travel half-life doubling from minute 10 to 20, extractor sites scored with a metal need of at least 600‰ while energy is not short, and helpers (guarding, assisting) leave for a new building that beats their help without the hysteresis margin, helping factories from a lower metal coverage |
| 2 tech 2 | the tech timeline to ¾ of `tech_time` (minute 12) on a 12–24 metal/s income gate, held back by pressure on the base or an enemy army estimate of 1.5–3× ours (not from 0.9×), first tech-2 factory rated at least nominal |
| 4 constructors | while metal is a surplus (coverage ≥ 1.1 or the store banking), constructors to 0.55 per finished extractor at minute 12 rising to 0.75 by 20 (ramped in from minute 8), plus one per `spots_per_con` claimable free spots (at most two); below it wanted outright, beyond it at a third, a fifth... |
| 8 factories | another factory only while fewer than one per 14 metal/s of income, or two from minute 8 (the first tech-2 factory exempt; a store 80% full lifts the cap; walled-in factories do not count) |
| 16 constructor floor | one constructor from minute 5, two from minute 8, on any map |

The investments of parts 1, 4 and 8 (the helper rule aside) wait for
`growSafe`: from minute 8 (the opening and the first push are the
brain's own), while our army is at least the estimated enemy army and
the base is not under pressure.

**The floor (16) is the default.** On The Pass (four metal spots) the
expansion room is zero and the economy share of a ~7 metal/s income is
less than the commander's own build power can spend, so the constructor
need was zero and the commander built alone all game (median
constructors 0.5 at minutes 5, 10, 15 and 20 in games against the brain
before §13). With the floor: 0.5 / 1 / 2 / 2, income 12.5 against 10.5
metal/s at minute 20 (8 games). The floor binds only where the brain is
that short: by minute 5 it has no constructor in 9 of 96 land-pool
games and fewer than two by minute 8 in 15. An earlier floor (one from
minute 2, two from 5, nearer the humans: first constructor at a median
minute 2.3, three alive at minute 5 for original-TA winners) moved the
opening in most games and scored 47.9% of points over 96 games; this one
50.5%.

**Parts 1–8 are not default: each moves its measure toward the humans'
and costs points in 20-minute games** against the brain without it
(12-map land pool, both slot orders, 48 games each unless noted):

| variant (builds during tuning; the last row's reference is the brain before §13) | points | constructors / extractor (10 / 15 / 20) | extractors (10 / 15 / 20) | army value (10 / 15) |
|---|---|---|---|---|
| all parts, first versions (constructors from minute 0, no safety) | 26–34% | 0.58 / 0.75 / 0.91 | 12 / 13 / 13 | 639 / 2,417 |
| all parts (31), final build | 35.4% | 0.36 / 0.66 / 0.84 | 11 / 14 / 18 | 1,226 / 2,190 |
| 4 constructors from surplus | 45.8% | 0.29 / 0.68 / 0.76 | 10.5 / 12 / 20 | 1,342 / 2,574 |
| 2 + 4 | 38.5% | 0.29 / 0.67 / 0.76 | 10.5 / 12 / 19 | 1,342 / 2,052 |
| 1 + 2 | 45.8% | 0.30 / 0.41 / 0.55 | 11 / 16 / 15.5 | 1,327 / 3,122 |
| 2 with the helper rule | 46.9% | 0.34 / 0.42 / 0.52 | 11 / 13 / 16.5 | 1,327 / 3,529 |
| the brain without them (reference) | 50% | 0.31 / 0.40 / 0.50 | 10.5 / 15 / 19 | 1,332 / 3,264 |

Constructors and a tech-2 factory are paid for with army between minutes
8 and 15, and 20-minute games are decided on points before the extra
extractors pay back. In 40-minute games (six maps, 12 games, an earlier
build of all parts) the result was 45.8%, split by slot order as usual,
with the economy well ahead at minute 20 (46 against 35 metal/s,
constructors per extractor 0.81). Humans can afford the investment with
army micro, early towers and games that last 25 minutes; this brain's
army-first plan wins the format the arena measures.

### 13.5 First factory family (`fac_first`, `w_fac_first`)

util+tac's first factory was the vehicle plant in every game. `fac_first`
names a family (1 kbot lab, 2 vehicle plant, 3 air plant, 4 shipyard; 0
none, the default); 5 draws one per game from `Kit.Rand` at the
stock-unit human shares (vehicle 63, air 20, kbot 10, ship 7), in `Init`
after the style draw. Until one factory of the family is owned (built,
framed or committed) its factories rate as the best factory on offer
(1000) and every other factory's suitability is divided by `w_fac_first`
percent (default 300). A factory's family is the majority family of its
combat products: air (RoleAir), ship (a water unit or RoleNaval), kbot
(the product's movement class names a KBOT class or its editor class
`TEDClass` is KBOT), otherwise vehicle (hovercraft included).

Three departures from the specification, each measured:

* **Kbots by editor class.** Stock kbots mostly author tank movement
  classes (armpw: TANKSH2; only the flea authors KBOTSS2), so the
  movement class alone made both labs vehicle factories.
* **Lift the family, scale the others down.** A multiplier cannot lift an
  air plant, which the ground-efficiency scale rates about a twentieth of
  a vehicle plant, so the family is raised to the best factory's rating;
  and multiplying it also multiplied the best factory score, bringing the
  first factory two builds early (mean build position 3.8; now 5.35, the
  same as without the preference, 5.3), and the hard persona drew two of
  96 games against retail on metal heck, where the brain without it wins.
* **Where each family is drawn.** The shipyard is drawn only where ships
  reach the plausible enemy starts (the specification), and the air
  plant only where neither vehicles nor kbots reach: with the earlier
  multiplier, air-first openings scored 32% of points against the brain
  without `fac_first` on the land pool (22 games; vehicle 52%, kbot 55%),
  their army too small when tanks can reach. On land maps the draw is
  therefore vehicle 63 : kbot 10. An explicit shipyard preference where
  ships cannot reach is dropped.

**5 is not the default: the land gate did not hold.** Against the brain
before §13 on the 12-map land pool (192 games, seeds 71–78 per map, both
slot orders) the defaults with `fac_first=5` scored 62-58-72, 47.4% of
points: games whose drawn family was the vehicle plant (the brain's own
choice) 48.8% (170 games), kbot openings 40.0% (20), two games with no
factory in the first ten builds lost. The brain's weights were tuned
with a vehicle plant first in every game (docs/MODERN_AI_RESEARCH.md
§6); kbot openings fall behind in the 20-minute race, and the air plant
first leaves no army while tanks can reach. With 5 the family draw is
the brain's only draw after the style when jitter is off, so
`style=balanced,jitter=0` stays draw-free only with `fac_first` 0–4.

### 13.6 Results

Final build, `hard` persona, 20-minute games unless noted, both slot
orders, a distinct seed per map (seed × 100 + map index) so style draws
vary across maps. "Before" is the brain before §13; the off spec
reproduces it result JSON for result JSON (great divide and sail away
mirrors and pillopeens against retail, compared with the binary built
before §13; the bomber fix changes none of them).

| gate | games | result | points |
|---|---|---|---|
| land pool: defaults against before (seeds 71–74) | 96 | 33-30-33 | 50.0% |
| land pool: defaults against v1 (`naval=0,air=0,tech=0,layout=0,style=balanced,jitter=0,def_plan=0` and §13 off; seeds 81–84) | 96 | 41-34-21 | 60.4% |
| land pool: defaults against retail (seeds 91–94) | 96 | 96-0-0, 74 commander kills | 100% (before: 96-0-0, 75 kills) |
| water pool: defaults against retail (seeds 111–112) | 32 | 31-1-0, 9 commander kills | 98.4% (before: 29-3-0, 7 kills) |
| 40-minute mirrors (great divide, the pass, show down, plains and passes, evad river confluence, greenhaven; seeds 95–96) | 12 + 12 | trapped max worst three 3, 1, 1, mean 0.38 | before: 3, 1, 1, mean 0.42 |

Water pool detail (per game, against retail, seeds 111–112): light boats
10.2 → 4.0, destroyers 3.9 → 5.7, tech-2 hulls 1.2 → 0.6, land combat
units 31.2 → 16.5, aircraft 4.3 → 7.2, ships that never left their
launch point 0.66 → 0.06. Commander kills by map: hundred isles 3, lake
shore 2, ring atoll 2, pillopeens 1, canal crossing 1 (before: hundred
isles 2, lake shore 2, sail away 1, ring atoll 1, pillopeens 1). Shore to
shore still produces no kill (4-0-0 on points either way): only aircraft
and the amphibious tech-2 tanks reach across it. Coast to coast (land
pool) against retail: 8-0-0 with 2 commander kills (before 8-0-0, 1).
The one water draw is a brain coral game.

**Human benchmark** (mirrors on the 12-map land pool, seeds 31–32 per
map; medians at minutes 10 / 15 / 20; human targets from REPORT.md:
ProTA land-map winners, built counts, and original-TA winners, alive
counts in the stock unit set):

| measure | before | defaults | `growth=31` | humans |
|---|---|---|---|---|
| constructors per extractor | 0.40 / 0.49 / 0.50 | 0.39 / 0.45 / 0.50 | 0.35 / 0.69 / 0.76 | ProTA 0.57 / 0.66 / 0.76 (target 0.55–0.75) |
| extractors | 11 / 14 / 17 | 11 / 14 / 17 | 13 / 15 / 18 | ProTA 16 / 25 / 33; original TA 9 / 13 / 18.5 |
| constructors | 4 / 5 / 7 | 4 / 5.5 / 7.5 | 4 / 9.5 / 14 | original TA 6 / 9 / 16 |
| metal income /s | 19 / 23.5 / 25 | 19.5 / 23.5 / 29 | 18 / 27 / 32.5 | original TA 15.5 / 23.2 / 30.2 |
| factories per metal/s | 0.11 / 0.14 / 0.18 | 0.11 / 0.14 / 0.17 | 0.11 / 0.12 / 0.12 | 0.07 (one per ~14) |
| tech-2 factory by minute 20 (median minute) | 31% (14.5) | 33% (14.5) | 63% (11.8) | 73% (12.1) |
| first factory family | vehicle 98%, ship 2% | vehicle 92%, ship 8% (coast to coast) | the same | original TA vehicle 63, air 20, kbot 10, ship 7; `fac_first=5` on land: vehicle 89, kbot 11 |
| army value | 1,386 / 3,230 / 4,684 | 1,215 / 2,913 / 4,493 | 1,226 / 2,407 / 4,347 | original TA 1,723 / 3,339 / 6,212 |

The defaults change the land economy little by design (the floor binds
where the brain is short: The Pass, §13.4); `growth=31` reaches the
human constructor ratio at minutes 15–20 and the tech-2 timeline, but
not the extractor growth (the brain holds about the same number of spots
against itself) or one factory per 14 metal/s, and it loses the
20-minute race (§13.4).

**Cost** (synchronous `-allocs` matches, the whole think): red planet,
defaults against before: think 29.3 µs mean, 88 µs p99, 103 B and 0.13
objects per think (before: 30.7, 94, 651 B, 0.05); pillopeens against
retail: 51.6 µs mean, 160 µs p99, 434 B and 0.17 objects (before: 64.1,
214, 429 B, 0.07). The plausible-start prior looks at the starts not yet
seen (own units × starts, until each is seen) and rebuilds its table
only when the set changes; fields, the yard ring and the family table are
built once in `Init`. `async=1` reproduced both synchronous games
exactly.

### 13.7 Gaps

* **The human economy shape costs the 20-minute race.** Every growth
  part except the floor moves its measure toward the humans' and loses
  points against the brain without it (35–47%); in 40-minute games an
  earlier build of the full set scored 45.8% (12 games, split by slot
  order as usual) with income 46 against 35 metal/s at minute 20. The
  arena's format (20 minutes, points, the army counted at its value)
  rewards this brain's army-first plan; a longer format, or towers and
  army micro that let people invest safely, would be needed before
  `growth` can default higher. Extractor growth after minute 15 is
  not reached even with all parts: in mirrors both sides hold their half
  and contest the rest (§13.10 measures what drives it and what it
  costs).
* **Kbot and air openings.** The brain's weights were tuned with a
  vehicle plant first; kbot openings score 40% and air-first 32% of
  points, so `fac_first` defaults to 0 and the human family mix is not
  played by default. Tuning the army weights per family (or per style
  with its family) is the next step; the per-archetype draw belongs in
  style.go.
* **Hover path.** Where hovercraft are the only land-ish class that
  reaches, a hover plant needs a land or air constructor first; nothing
  asks for that constructor when the only factory is a shipyard. The
  brain's own reach model rated ships above hovercraft on the maps tried,
  so this did not bind there.
* **Stranded guard size** (`att_min` plus a quarter of the enemy
  estimate) is a design constant, not tuned; stranded units still count
  as army value in the points, so fewer of them can lower the score in a
  game neither side can win by force.
* **Shipyard exits.** The executor's exit guard does not cover
  shipyards (layout.go, E3b's file); this section only moves the brain's
  request points (fields and the yard ring). A naval exit class in the
  guard would protect yards from anything the executor places.
* **Shore to shore** still ends on points: reaching needs aircraft or
  amphibious tech 2, and the army layer's strikes kill little there.
* **Metal heck** (uniform metal) remains the map where util+tac's points
  margin against retail is smallest.

### 13.8 E4: attacks the reach-aware mix and the fleet spent

The integration tournament (seeds 201–202) found the water pool against
v1 at 71.9% of points after §13 merged, from 85.9% before: shore to
shore and pillopeens lost games on points. Traces (explains and
five-second frames) showed four causes:

1. **Unseen defenders.** The fleet shelled pillopeens' enemy coast
   within 800 wu of 40–68 enemy land units with none of them in its
   prediction (a remembered mobile fades within a minute of leaving
   sight; seven destroyers lost), and the strike wing flew back to shore
   to shore's enemy base, guarded by missile trucks, every three minutes
   as its anti-air memory faded, losing 800–2,100 in bombers for 150–700
   destroyed. Fixed in the tactics army (`unseen`, its README): the
   unseen part of the largest enemy land army seen counts at naval
   targets and as anti-air at strike targets near where it was seen or
   the enemy base, and the strike model is calibrated by the sorties
   flown (realized over expected loss and gain).
2. **No land constructors.** With `reach_mix` and land reach 0 no land
   factory rated at all, so where the first factory was a shipyard (its
   constructors work only water) the commander built alone for ten
   minutes (income flat at 11 metal/s against 26). A land factory is now
   built as the constructor source while no factory makes land
   constructors (econ_reach.go).
3. **The stranded cap banked metal** (1,000–1,850 in store on shore to
   shore). It now also needs the whole army at the strategy's army
   target, and metal banking lifts it.
4. **Heavier hulls lose to coasts.** Patrol boats carry about twice a
   destroyer's damage and 1.5× its hit points per value; the square law
   favours them wherever there is no enemy navy to hunt. `fleet` now acts
   only while the enemy fields one (10% of its seen mobile strength
   afloat); against v1, which builds no ships, fleets are what they were.

What remains is a points race: on maps neither side crosses by land
(shore to shore, canal crossing) the brain with `reach_mix` builds
aircraft and hovercraft instead of a land army that cannot leave, they
do not break the enemy base, and the land army the other side keeps at
home counts for points. E4 set `reach_mix` to default 0 for that reason;
the integration owner turned it back on (1), because its cost is within
the noise (76.6% against 78.1% over 128 games, about half a standard
error) and it is scored only by the arena's points adjudication, while
without it the brain builds a land army that can never reach a human
opponent (71 tanks on shore to shore).

| water pool against v1, 20 min, both slot orders | seeds 201–204 (64 games) | seeds 201–208 (128 games) |
|---|---|---|
| e43576ff (before §13 merged; T4, T5, E3b not yet merged) | 82.0% | – |
| the integration tip without §13 (`reach_mix=0,fleet=0,tidal_field=0,growth=0`, `unseen=0`) | 82.0% | 78.5% |
| integration tip, §13 defaults then (`reach_mix=1,fleet=1`) | 71.9% on 201–202 (32 games) | – |
| E4 fixes with `reach_mix=1` | 77.3% | 76.6% (shore to shore 0-10-6) |
| **E4 defaults** (`reach_mix=0`, fleet for navies, `unseen=1`) | **78.1%** | **78.1%** |

Per map, E4 defaults on seeds 201–204 (e43576ff in brackets): brain
coral 8-0-0 (8-0-0), canal crossing 1-7-0 (0-8-0), hundred isles 8-0-0
(8-0-0), lake shore 6-2-0 (5-3-0), pillopeens 3-5-0 (8-0-0), ring atoll
6-2-0 (8-0-0), sail away 6-1-1 (4-4-0), shore to shore 0-7-1 (0-8-0). Map
by map the differences are within the seed noise: over seeds 201–208 the
tip without §13 has pillopeens 12-2-2 and the E4 defaults 7-9-0, but on
pillopeens alone over seeds 201–216 (32 games each) the tip without §13
scores 73% and the E4 tactics and fleet changes (tidal fields and growth
off) 70%.
The 85% asked for is not reached; the brain before §13 does not reach it
on these seeds either (82.0% on 201–204, 78.5% on 201–208).

Other gates, E4 defaults: water pool against retail (seeds 201–202)
32-0-0 with 12 commander kills (the tip without §13: 31-1-0, 14); land
pool against v1 (seeds 201–202, 48 games) 18-13-17, 51.0% (tip 49.0%);
land pool against retail 48-0-0 (tip 47-1-0). Think cost (synchronous,
`-allocs`): pillopeens against v1 79.9 µs mean, 264 µs p99, 418 B and
0.07 objects per think (the tip, whose game differs: 68.5 µs, 189 µs,
412 B, 0.05); red planet against retail 11.6 µs, 39 µs p99, 105 B (tip
11.9, 38, 124 B). `async=1` reproduced both games exactly.

### 13.9 Review fixes

A read-only review of the round-two brain found per-unit state that
survived slot reuse, a tower classification that let flak guns into the
ground plan, a defense plan that refreshed only when a builder priced a
tower, and a few smaller defects; one commit each.

1. **Recycled slots.** The pool reuses a slot without a generation, so
   every per-handle table (commitments, builder failures, factory state
   and last product, the stuck-unit track, the defense plan's last
   positions and loss tracking, the opening report) now keys on the
   observation's `Gen` as well as the definition, and starts afresh for
   a new unit of the same type.
2. **Missile towers.** A tower serves the ground plan (front rules) when
   its *ground* fire — DPS less its to-air weapons' fire, which never
   engages a unit that is not airborne — is worth its cost; `quality`
   rates ground towers by that fire too. Every anti-air tower passed
   before (the test was classify's own), so flak guns were sited forward
   and counted in the ground mix. It is not DPS − AirDPS: the stock
   missile towers' anti-air is credited from their damage table, so
   `aikit.BuildTable` gives armrl DPS 23 and AirDPS 48, and the missile
   tower would have left the ground plan with the flak guns (a retail
   test locks armrl/corrl in, armflak/corflak out).
3. **The defense plan every think.** `Economy.Plan` refreshes it once a
   think, so a building lost between two builder decisions is a loss and
   the history follows game time. Sightings and anchor threat are
   weighted in ticks (a whole-second floor doubled them at a 15-tick
   think), the budget carries its fraction (about 30% was truncated per
   think), the flow grid decays lazily per sector, and a move stops
   crediting sectors where its straight line leaves the ground the unit
   stands on (its movement class, same region).
4. **Failed spots.** A spot's failure count resets once a frame of ours
   stands on it; with `layout`, a first failure with a reclaimable
   feature in view within 96 wu sends the builder to clear it. The
   expected cause, a wreck on a contested spot, is rare: a diagnostic
   build of the executor's spot placement found all 213 failures in three
   40-minute games (comet catcher and sherwood mirrors, great divide
   against v1) meeting a building already on the spot — on comet catcher
   152 of 195 nearer the enemy's start, an enemy extractor we had not
   seen — and none a feature. A builder ordered earlier in a think (a
   retreat, an exit, such a clear) is no longer decided again in it (the
   second order replaced the first).
5. **Smaller.** `bestClear` no longer stops at the first pile worth 1500;
   a tower frame and its builder's order count once in a zone's slots, a
   choke post and the water site; `Policies` clamps Params into range
   (`Params{}` divided by zero); ambition 0 is the full plan everywhere;
   an unreachable air-plant branch and the unread `reachChanged` are
   gone.
6. **Cost** (results identical): pile sums once per think by cell,
   tower spacing by a per-observation building index, extractor spots
   memoized per unit, enemy income computed for Explain only.

Land pool against v1 (seeds 301–302, both slot orders, 48 games,
20 minutes); each row adds one commit to the one above:

| build | W-D-L | points | commander kills / losses | games changed |
|---|---|---|---|---|
| before (2dce0f85) | 16-18-14 | 52.1% | 10 / 8 | – |
| 1 recycled slots | 16-18-14 | 52.1% | 10 / 9 | 7 |
| 2 missile towers | 16-18-14 | 52.1% | 10 / 9 | 3 |
| 3 plan every think | 17-16-15 | 52.1% | 11 / 9 | 48 |
| 4 failed spots | 17-16-15 | 52.1% | 11 / 9 | 20 |
| 5 smaller fixes (final) | 18-16-14 | 54.2% | 8 / 10 | 39 |

40-minute `hard` mirrors (great divide, comet catcher, painted desert,
sherwood; seeds 301–302; 16 player-games; median [mean]; after the fixes
two games end by minutes 17 and 22, so fewer players stand at 20 and 30):

| measure | before | after |
|---|---|---|
| extractors standing, 10 / 15 / 20 / 30 min | 10.5 / 15.5 / 19 / 23 [10.3 / 15.5 / 19.4 / 25.6] | 11 / 18 / 21 / 27 [10.5 / 16.4 / 20.0 / 26.8] |
| towers standing, 10 / 15 / 20 min | 3 / 8.5 / 18.5 [2.8 / 8.5 / 19.3] | 3 / 8.5 / 20 [3.1 / 9.6 / 20.4] |
| constructors per extractor, 10 / 15 / 20 / 30 | 0.48 / 0.60 / 0.63 / 0.50 | 0.50 / 0.51 / 0.66 / 0.47 |

Cost (synchronous `-allocs`): the comet catcher mirror (40 minutes)
thinks 145 / 110 µs mean and 397 / 269 µs p99 before the cost commit and
146 / 111 µs and 396 / 271 µs after, the same game; great divide against
v1 39.8 → 39.6 µs mean. The utility layers are about a quarter of a
think (a CPU profile of that mirror: 0.24 s of 0.97 s thinking; the
board update and the tactics army the rest), and the per-think plan
refresh costs roughly 10 µs, so the whole think moves within the host's
noise. `async=1` reproduced the synchronous sherwood mirror exactly.

Gaps: `groundDPS` repeats `aikit`'s DPS arithmetic for to-air weapons (a
`UnitInfo` field would remove the copy); a failed extractor placement
that meets an unseen enemy building only blocks the spot here (the board
can now hold it: `expand`'s hold part, §13.10, off by default); the
plan's per-refresh decay `v × τ / (τ + dt)` is now close to an
exponential with time constant τ, and the constants were not retuned.

### 13.10 Mid-game expansion (`expand`, `expand_from`)

In 40-minute games the brain stopped growing after minute 12–15 and
lost the long game to v1 on economy (protocol v2 long pool, 96 games:
40.1% of points; metal 37 against 49 per second at minute 30). A
per-minute census of every metal spot and builder assignment in 23
40-minute games on the long pool (b3777c4e; mirrors and games against
v1, seeds 301–302) found four causes:

* **Extractors stop competing once metal banks.** On show down and
  plains and passes at minute 15 the metal store was 85% full in 15 of
  18 player-games, the metal need fell to 180–209‰, and extractor sites
  lost to towers, factories and guarding while 1–11 (show down) and
  125–159 (plains and passes) safe free spots stood open.
* **Constructors lose every factory slot to combat units.** At minute
  15 a constructor scored a median 61–306 against 506–786 for the best
  combat unit (the economy share is at `eco_late` by minute 12 and each
  constructor owned halves the next).
* **The rest lies on the contested line.** On great divide and sherwood
  both halves are taken by minute 15 (0–12 open spots, 1–8 on the line).
* **Factories do not follow the income.** v1 builds 7.8 factories by
  minute 30 against the brain's 4.9: once the defense plan's budget (a
  share of income) is behind, a tower's need reaches 6,500–8,000 while a
  factory's is capped at 2,000, so builders build towers with the store
  full (one plains and passes game kept one walled-in factory all game
  at 47 metal/s).

The review's reading of the code holds: a failed spot is blocked a
minute per failure (at most eight) and retried, an enemy-held spot is
cleared only when an own unit sees it again (`aikit` memory), and the
constructor need is the economy share's gap diminished by
`half(constructors, 4)`. (The traced cause of its 213 failures, a
building on the spot, was not re-traced here.)

`expand` is a sum of parts (econ_expand.go); `expand_from` (minutes,
default 15, range 4–30) is when pace, builders and spend start:

| part | what it does |
|---|---|
| 1 pace | extractor sites scored with a metal need of at least 1000‰ while our finished extractors trail the top tier's extractor curve (`topExtractors`, ambition-scaled) and energy is not short; extractor travel half-life doubles from minute 10 to 20 |
| 2 builders | a constructor target of 0.55 per finished extractor at minute 12 rising to 0.75 by 20, ramped in over 4 minutes, plus one per `spots_per_con` claimable free spots (at most 2), held to the top tier's constructor curve; below it a constructor is wanted outright; not under pressure or when the enemy army estimate exceeds ours by half |
| 4 hold | a failed extractor placement with no reclaimable feature in view and no building of ours on the spot holds the spot on the board (`core.Board.HoldSpot`, state `SpotHeld`) until a unit of ours stands within 200 wu with no enemy building remembered there (not in the first 30 s), five minutes at most, instead of the timed block |
| 8 contest | a spot on the contested line (territory at least 300‰) counts as ours while our armed strength there is at least 80 and twice the threat |
| 16 spend | while metal is not short (coverage at least 1.1 or the store banking) and working factories are fewer than one per 6 metal/s of income (v1's ratio at minute 30), this think's factory weight rises by 2× per missing factory (at most 8×) and the guard weight by half that |

**Default: off (`expand=0`).** No setting passed both tests: holding
the 20-minute race and gaining in 40-minute games. `expand=0`
reproduces the brain before this section result for result (two
protocol v2 baseline games checked). `expand=23` (pace, builders,
hold, spend) from minute 15 holds the 20-minute race and grows the
economy after minute 15, but the extra income did not become army, and
it does not gain in 40-minute games. The earlier build of the switch
(7ee200bd: pace from minute 4 with a 600‰ floor also on the curve,
constructors from minute 10, spend from 8) gained in 40-minute games but
lost the 20-minute race. Re-evaluated with the `army` switch's default
(§13.12), where income can become army, it still does not hold the
20-minute race.

Results (hard; 40-minute games on the v2 long pool, both slot orders,
random starts, paired game for game with the protocol v2 baseline
games of b3777c4e against v1; 20-minute games on the v2 land pool
against `expand=0`; points share with its 90% map-cluster interval):

| build | 40 min vs v1 (paired, games) | invested score | 20 min land vs off |
|---|---|---|---|
| without expand (b3777c4e) | 40.1% (96) | 40.1% | – |
| 1+2+4, pace from 4 with a 600‰ floor also on the curve, builders from 10 | 33.0% vs 36.2% (47) | 34.0% vs 35.1% | – |
| 16 alone, first rule (one factory per 10 metal/s while banking, from minute 0) | 33.3% vs 37.5% (48) | 33.3% vs 36.5% | – |
| 23 with that rule | 42.7% vs 37.5% (48) | 44.8% vs 36.5% | – |
| 23 with that rule from minute 8 (4a43fdd3) | 38.5% vs 40.1% (96) | 39.1% | – |
| 16 alone, one per 6 metal/s while not short, from minute 8 | 38.5% vs 37.5% (48) | 39.6% vs 36.5% | – |
| **23 early (7ee200bd, that 16)** | **46.4%, +6.2 [−1.0, +14.1]** (96) | **46.9%, +6.8 [+0.5, +14.1]** | **44.2% [39.9, 47.8]** (104, 7 maps) |
| 23, pace from 8 below the curve only, builders from 12, spend from 8 | – | – | 42.2% [37.5, 46.9] (96); 1, 2 and 16 alone 45.8% each (48) |
| 23, `expand_from=10` | 37.5% vs 37.5% (48) | 38.5% vs 36.5% | 46.4% [42.7, 50.5] (96) |
| **23, `expand_from=15`** | **37.0%, −3.1 [−9.9, +3.6]** (96) | **39.6%, −0.5 [−8.3, +7.3]** | **49.2% [47.7, 50.8]** (192; invested 52.1%) |

`expand=23` from minute 15 against `expand=0` in 40-minute games (96
games): 46.4% of points [43.2, 49.5], 49.0% on the invested score.

Economy in 40-minute games against v1 (means at minutes 10 / 15 / 20 /
30, 96 games each; constructors exclude the commander):

| measure | without expand | 23 from 15 | 23 early | v1 |
|---|---|---|---|---|
| extractors | 9.2 / 14.2 / 16.5 / 18.4 | 9.2 / 14.0 / 15.0 / 22.5 | 9.5 / 13.6 / 17.6 / 23.6 | 10.2 / 15.4 / 19.8 / 25.3 |
| constructors | 4.2 / 7.2 / 9.8 / 9.8 | 4.2 / 7.1 / 11.2 / 15.0 | 4.1 / 8.9 / 13.5 / 17.0 | 3.6 / 5.7 / 6.5 / 7.9 |
| metal income /s | 16.7 / 25.6 / 30.9 / 37.2 | 16.8 / 25.8 / 29.0 / 40.2 | 17.3 / 25.9 / 32.4 / 46.3 | 19.2 / 29.4 / 37.9 / 49.0 |
| factories (20 / 30) | 4.0 / 4.9 | 4.1 / 5.5 | 4.6 / 5.7 | 5.9 / 7.8 |
| army value | 1,361 / 3,736 / 6,256 / 11,215 | 1,356 / 3,720 / 5,736 / 11,315 | 1,343 / 3,134 / 5,767 / 11,140 | 1,341 / 3,785 / 8,200 / 16,417 |

Against `expand=0` (96 games, 40 minutes) `expand=23` from minute 15
holds 25.1 extractors, 17.6 constructors and 48.0 metal/s at minute 30
against 23.5, 11.5 and 39.3, with the same army (11,600 against
11,800). Human targets (REPORT.md (d)): original-TA winners 9 / 13 /
18.5 extractors and 6 / 9 / 16 constructors at minutes 10 / 15 / 20;
ProTA winners build 16 / 25 / 33 extractors.

Lost and retaken spots (census, the early build against v1, seeds
301–304, 48 games): per game 9.7 of our extractors lost, 5.6 spots
retaken after a loss, 2.4 taken where an enemy extractor had stood, 4.0
spots held of which 0.8 later became ours; the same games with part 16
alone (no pace, no hold): 9.5 lost, 4.7 retaken, 2.4 from the enemy. The
contest part was measured only in an early mirror (with hold, 16
40-minute games against the brain without them: 40.6% of points, 2,047
economy value lost per game against 984) and is not in 23.

Why the economy does not become strength: the extra income went to
overflow (4,400 metal wasted by minute 30 against 3,600 without the
switch, early build; 6,100 with the first spend rule), constructors and
towers, and the factory count stayed at 5.5–5.7 against v1's 7.8 even
with the spend part. The defense plan's budget grows with income, so
its towers take the builders the growth adds (towers lost per game
against v1: 2,500–3,100 with or without the switch; v1 loses about
100), and every investment before minute 20 costs the 20-minute race,
which is decided on the army at minute 20.

Cost (synchronous `-allocs` games, `expand=23` against `expand=0`, the
same game): great divide (40 minutes, seed 301) think 60.4 / 48.2 µs
mean, 153 / 122 µs p99, 54 / 144 B per think; plains and passes (seed
302) 110 / 97 µs mean, 280 / 192 µs p99, 43 / 226 B. The switch's own
work is a few comparisons per think and a scan of the held spots; the
difference is the larger base the switch builds (every layer scans own
units). `async=1` reproduced both games exactly.

Gaps: the defense plan's income share (defense*.go) and a factory need
capped below a behind plan's tower need decide where the growth goes;
a walled-in only factory is not replaced while towers outrank it;
remembered enemy extractors on spots behind our army stay enemy-held
until a unit passes; the early build's 40-minute gain is within its
interval on the default score and was not re-measured with the
`expand_from` parameter (it is `expand=23` on 7ee200bd); §6's parameter
table does not list `expand` and `expand_from`.

### 13.11 Growth in 40-minute games

`growth` (§13.4) re-evaluated where its investments should pay: 40-minute
games on the v2 long pool against v1, seeds 301–304, both slot orders,
random starts, each part added to the default floor (16) and paired game
for game with the default's games (48 each; points share and the paired
difference with its 90% map-cluster interval):

| `growth` | default score | invested score | extractors (20 / 30) | constructors (20 / 30) | metal /s (20 / 30) | army value (20 / 30) |
|---|---|---|---|---|---|---|
| 16 (default) | 37.5% | 36.5% | 16.7 / 19.4 | 9.6 / 9.8 | 30.2 / 39.2 | 6,246 / 11,727 |
| 17 (+ expansion) | 33.3%, −4.2 [−9.4, 0.0] | 33.3%, −3.1 [−6.2, 0.0] | 18.0 / 23.2 | 10.0 / 11.6 | 32.3 / 45.0 | 5,825 / 11,496 |
| 18 (+ tech 2) | 37.5%, 0.0 [−6.2, +6.2] | 39.6%, +3.1 [−5.2, +12.5] | 17.5 / 22.1 | 10.0 / 11.5 | 32.5 / 49.4 | 5,915 / 10,555 |
| 20 (+ constructors) | 35.4%, −2.1 [−7.3, +3.1] | 37.5%, +1.0 [−5.2, +9.4] | 17.4 / 29.2 | 13.4 / 20.9 | 34.9 / 51.4 | 5,487 / 11,554 |
| 24 (+ factories per income) | 35.4%, −2.1 [−7.3, +3.1] | 36.5%, 0.0 [−7.3, +7.3] | 15.6 / 18.4 | 8.4 / 9.2 | 28.2 / 30.2 | 6,114 / 9,883 |
| 31 (all) | 28.1%, −9.4 [−19.8, +1.0] | 28.1%, −8.3 [−19.8, +2.1] | 18.2 / 27.7 | 13.8 / 18.1 | 35.1 / 61.1 | 4,915 / 9,765 |

**Recommendation: keep `growth=16` for the play set.** Every part moves
its economy measure toward the humans' in 40-minute games too (31
reaches 28 extractors and 61 metal/s by minute 30, against the humans'
18.5 extractors at minute 20), and none gains strength against v1 on
either score: the army falls behind the income as with `expand`
(§13.10), so the full set loses the most (great divide 37.5% → 0%).
Tech 2 (part 2) is the only part not behind on either score, within its
interval.

### 13.12 Income into army, towers that pay (`army`)

Unit G6. The defense plan (§12) cost strength, and in 40-minute games
the income did not become army. Measured on this build before the unit
(protocol v2, `hard`; the census is new instrumentation in the tower
audit, `defense_census.go`, never read by a decision):

* **Against `def_plan=0`** (12-map land pool, seeds 301–308, 192 games):
  `def_plan=0` took 56.5% of the points [49.5, 63.0] and killed the
  commander 43 times against 24. The plan side held 3.1 / 8.1 / 20.0
  towers at 10 / 15 / 20 minutes worth 853 / 2,483 / 5,679
  (metal-equivalent), and an army of 2,632 at minute 15 against 3,251.
  Half its towers never had an enemy unit their fire could engage within
  reach (1,558 of 3,120), and 7–10% of tower samples did. Between
  minutes 10 and 20 its builders took 26 tower orders a game and 4.6
  factory orders.
* **Why commanders died.** Three traced deaths at minutes 14–15.5 (full
  moon, great divide, red planet): each came after our army was gone —
  18–29 enemy units within 700 wu of the commander, none or one of ours
  — and at most four of ten towers stood within 500 wu. In the great
  divide game the tower budget, a share of income tripled by raids, took
  20–42% of the income at minutes 6–11 while metal coverage stood at
  0.3–0.6; builders took 21 tower orders between minutes 9 and 12, and
  the factory count stayed at one.
* **Against v1 in 40-minute games** (6 long maps, seeds 301–308, 96
  games): v1 took 60.4% of the points. At minutes 30 / 40 we held 30.8 /
  42.5 towers worth 10,288 / 19,618 and an army of 11,646 / 18,632; v1
  an army of 18,929 / 29,626 on 8.0 / 9.0 factories to our 5.9 / 6.2.
  After minute 20 our builders took 56 tower orders a game and 6
  factory orders; towers lost per game 3,711 (v1: 437).
* The tower mix was 13.9 missile towers, 3.5 light and 2.1 heavy laser
  towers per 20-minute game: 28% of the towers by count, 65% of their
  value, as the quality blend favours heavier towers as income grows.

`army` is a sum of parts (`army.go`); `army=0` plays the brain before
this section (checked result for result, below):

| part | what it does |
|---|---|
| 1 yield | while production is short — the metal store banks (60% full, income covering expense) and working factories (built and not walled in, framed or walked to) are fewer than one per 6 metal/s of income, or none works — a ground or anti-air tower is scored as if the plan were one tower behind, at a quarter, unless its zone was raided |
| 2 factories | while production is short, from minute 12, a factory's need is at least 1000‰ per missing factory (at most 4000‰); with every factory walled in, a factory is as urgent as a first factory |
| 4 type | the tower quality blend is the square law (v_ref 150: the light laser tower ahead of the heavy one); a builder that can build a missile tower builds direct fire only for a zone ground units attacked (about two tank-minutes of sightings near it, or a building lost there), missile towers elsewhere |
| 8 place | a zone's front exposure is 40% of the front rules' where no attack came, all of it where one did |
| 16 ceiling | no ground tower is owed while our towers (built, framed, or ordered beyond the frames) are worth the tier's tower count curve (`topDefenses`) in missile towers, or 15% of all we own, whichever is larger |
| 32 cover | the home zone weighs the commander 8000 (the tactics army's target value of a commander; 1500 before) and its ground exposure is at least nominal |
| 64 surplus | the ground budget's slack follows metal coverage from nothing at 0.6 to full at 1.1, and raids raise the budget by half at most (three times before) |

**Screens** (against `def_plan=0`, development seeds 311–314 on the
12-map land pool, both slot orders, random starts, 20 minutes, 96 games
each; the points share and its difference from the brain without the
switch paired game for game, 90% map-cluster interval; towers standing
at 10 / 15 / 20 minutes and their value at 20). The brain without the
switch took 38.0% on these seeds, with 2.9 / 7.9 / 16.7 towers worth
4,703.

| `army` | points | paired difference | towers 10 / 15 / 20 | value at 20 |
|---|---|---|---|---|
| 1 | 43.2% | +5.2 [+1.6, +9.9] | 2.8 / 7.6 / 16.5 | 4,628 |
| 3 (factories from the opening) | 36.5% | −1.6 [−6.2, +3.6] | 2.6 / 7.3 / 15.3 | 4,328 |
| 4, first build (outside attacked zones the front rules' mix) | 43.8% | +5.7 [−1.6, +13.0] | 3.1 / 8.4 / 18.0 | 4,989 |
| 8 | 42.7% | +4.7 [−1.0, +10.4] | 2.9 / 8.0 / 16.7 | 4,769 |
| 16 | 43.8% | +5.7 [+1.0, +10.9] | 2.8 / 6.0 / 12.4 | 3,399 |
| 32 | 40.6% | +2.6 [−3.1, +7.8] | 3.0 / 7.9 / 18.2 | 5,101 |
| 48 | 39.1% | +1.0 [−6.8, +8.9] | 2.7 / 5.9 / 14.0 | 4,000 |
| **4** (type) | **46.9%** | **+8.9 [+2.6, +15.6]** | 5.3 / 13.9 / 24.3 | 4,442 |
| 17 | 42.7% | +4.7 [−2.1, +10.9] | 2.7 / 5.9 / 13.0 | 3,594 |
| 20 | 49.5% | +11.5 [+5.7, +17.7] | 4.8 / 10.9 / 19.3 | 3,532 |
| **21** | **51.6%** | **+13.5 [+8.3, +18.8]** | 4.7 / 10.3 / 18.4 | 3,419 |
| 64 | 49.5% | +11.5 [+5.7, +17.2] | 2.2 / 6.0 / 12.6 | 3,692 |
| 65 | 50.0% | +12.0 [+5.2, +19.3] | 2.2 / 5.8 / 12.0 | 3,502 |
| 68 | 50.5% | +12.5 [+4.7, +21.9] | 3.8 / 8.4 / 14.7 | 2,954 |
| 85 | 49.5% | +11.5 [+4.2, +19.8] | 3.4 / 7.7 / 13.5 | 2,723 |

The type part is the one that moves the count: the same tower value
buys about twice the towers (people's 5 / 13 / 21), and direct fire
stands where ground units came. The ceiling and the surplus part each
cut the value; the surplus part also cuts the count well below people's,
so it is not in the default. The factories part from the opening built
a factory early and lost the army race; it now waits for minute 12.
Cover and place do not pay on their own. With the first type build,
20, 23 (factories from the opening), 31, 53 and 61 took 42.2%, 37.0%,
34.4%, 41.7% and 41.7%.

**Default: `army=21`** (yield, type, ceiling). Protocol v2 (seeds
301–308, both slot orders, random starts, `hard`; the first-named side's
points share with its 90% map-cluster interval):

| gate | games | W-D-L (commanders killed / lost) | points | notes |
|---|---|---|---|---|
| A: 21 against 0, 12 land maps, 20 minutes | 192 | 65-75-52 (22 / 14) | **53.4% [50.5, 56.2]** | decisive-only 61.1%; margins 1.5 / 2.0: 54.2% / 50.5% |
| B: 21 against 0, 6 long maps, 40 minutes | 96 | 43-19-34 (26 / 20) | **54.7% [50.5, 58.9]** | margins 1.5 / 2.0: 53.6% / 54.2% |
| B: 21 against v1, 40 minutes | 96 | 37-14-45 (23 / 32) | **45.8% [33.3, 58.3]** | 0 against v1: 39.6% [31.2, 47.4]; paired +6.2 [−5.2, +17.2] |
| towers: 21 against `def_plan=0`, 20 minutes | 192 | 58-67-67 (25 / 39) | 47.7% [41.7, 54.2] | 0: 43.5% [37.0, 50.5] (24 / 43); paired +4.2 [+0.0, +8.6] |

So `def_plan=0` takes 52.3% of the points against the default, from
56.5%, and kills its commander 39 times in 192 games, from 43; the
defense plan still costs points against it, by less.

Towers (census, means over the 192 games against `def_plan=0`; people:
original-TA winners 5 / 13 / 21 standing at 10 / 15 / 20 minutes):

| measure | army=0 | army=21 |
|---|---|---|
| towers standing at 10 / 15 / 20 min | 3.1 / 8.1 / 20.0 | 4.6 / 11.0 / 19.1 |
| their value at 10 / 15 / 20 | 853 / 2,483 / 5,679 | 790 / 1,902 / 3,416 |
| built per game: missile / light / heavy laser towers | 13.9 / 3.5 / 2.1 | 16.6 / 1.3 / 0.3 |
| towers that ever had an enemy in reach | 50% | 55% |
| army at 15 min (`def_plan=0`'s) | 2,632 (3,251) | 2,743 (3,120) |

In the 40-minute games (both pairs, 192 games) the default held 25.9 /
37.7 towers at 30 / 40 minutes worth 5,391 / 11,585 (army=0 against v1:
30.8 / 42.5 worth 10,288 / 19,618), lost 1,431 in towers a game (3,711),
and had 6.4 / 7.7 factories, 50 / 64 metal/s and an army of 11,716 /
21,055 at 30 / 40 minutes (army=0 in its 40-minute games against the
default: 5.9 / 6.2, 42 / 51 and 11,339 / 17,425; v1 against the default:
8.4 / 9.0, 49 / 49 and 17,868 / 22,803). After minute 20 its builders
took 25 tower orders a game (44 before) and 7 factory orders (4).

**`expand` re-evaluated** (§13.10; `expand=23` from minute 15 on top of
`army=21`, protocol v2 seeds 301–308): it does not pass either gate.

| pair | games | W-D-L (commanders) | points (the first side) |
|---|---|---|---|
| 21 against 21 with `expand=23`, 20 minutes, land pool | 192 | 67-75-50 (16 / 19) | 54.4% [52.3, 56.2] |
| 21 against 21 with `expand=23`, 40 minutes, long pool | 96 | 44-18-34 (33 / 22) | 55.2% [50.0, 61.5] |
| 21 with `expand=23` against v1, 40 minutes | 96 | 29-19-48 (20 / 33) | 40.1% [28.1, 53.1]; paired with 21's games −5.7 [−10.9, −0.5] |

With the switch the economy grows as before (31.9 extractors, 65.5
metal/s and 8.6 factories at minute 40 against 28.8, 62.2 and 7.2) and
the army at 40 minutes is the same (20,079 against 20,591), but its
constructors and extractors are lost more often and its commander is
lost 67 times in 192 games against 42 kills.

**Each part against the brain without the switch** (development seeds
311–314, 12 land maps, 20 minutes, 96 games each; the part's points
share with its 90% map-cluster interval; the full-protocol gates of the
default are above):

| `army` | against army=0 | against `def_plan=0` (paired difference, screens above) |
|---|---|---|
| 1 yield | 48.4% [45.3, 51.0] | +5.2 [+1.6, +9.9] |
| 2 factories | 50.0% [49.0, 51.0] | (with 1, from the opening: −1.6) |
| 4 type | **59.9% [53.6, 66.7]** | +8.9 [+2.6, +15.6] |
| 8 place | 47.4% [43.2, 51.6] | +4.7 [−1.0, +10.4] |
| 16 ceiling | 53.1% [48.4, 57.3] | +5.7 [+1.0, +10.9] |
| 32 cover | 44.3% [37.0, 51.0] | +2.6 [−3.1, +7.8] |
| 64 surplus | 50.5% [45.3, 56.2] | +11.5 [+5.7, +17.2] |
| 21 (default) | 56.2% [51.6, 62.0] | +13.5 [+8.3, +18.8] |
| 23 (default + factories) | 57.3% [52.6, 62.5] | – |

On the protocol seeds 23 against army=0 took 53.6% [51.3, 55.7] (192
games, 20 minutes; the default 53.4%).

**Against the 3.1 AI** (`retail`, `-level hard`, 12 land maps, seeds
301–302, both slot orders, random starts, 48 games each): the default
48-0-0 with 39 commander kills, 100% of the points; army=0 46-0-2 (two
mutual kills) with 37, 97.9%.

**Which parts are in the default** (40-minute games, long pool, seeds
301–308, 96 games each, against the default):

| pair | W-D-L (commanders) | points (the first side) | at 40 minutes: army, factories, metal/s, towers (value) |
|---|---|---|---|
| 20 (no yield) against 21 | 35-23-38 (24 / 19) | 48.4% [41.7, 55.2] | 16,169, 6.3, 46.2, 32.0 (10,084) against 21's 20,659, 7.2, 54.6, 32.2 (9,602) over its 192 games |
| 23 (with factories) against 21 | 34-26-36 (22 / 25) | 49.0% [45.3, 52.1] | 21,533, 7.6, 60.9, 35.5 (10,706) |

Yield is kept (the army at 40 minutes is larger with it; against
`def_plan=0` it gained +5.2 in the screens), though neither pair is
beyond noise. The factories part raises factories and income at 40
minutes without a points gain, so it stays off; so do place, cover and
surplus, which did not pass against army=0 or cut the towers below
people's count.

**Cost and determinism** (synchronous `-allocs` matches, the default
against army=0 in the same game, random starts, seed 301): sherwood 32.4
/ 35.4 µs think mean, 81 / 91 µs p99, 33 / 295 B and 0.02 / 0.04 objects
allocated per think; comet catcher 48.3 / 51.8 µs, 148 / 135 µs p99, 58
/ 435 B; great divide over 40 minutes 57.7 / 68.2 µs, 160 / 147 µs p99,
50 / 225 B. The switch's own work is one pass over our units per think
(production and the ceiling's values) and a few comparisons in the
plan; the smaller tower count makes the plan's scans cheaper. Host step
means 6.5–9.9 µs against 7.1–12.5. `async=1` reproduced the synchronous
game exactly on sherwood (seed 302, the default asynchronous), red
planet (seed 302, army=0 asynchronous) and great divide (seed 303, 40
minutes). `army=0` reproduced the build before this section result for
result (great divide against `def_plan=0`, seed 301; the pass against v1
over 40 minutes, seed 301 — with the default binary, so the v1 string is
unchanged by the default).

Gaps:
* The defense plan still costs points against `def_plan=0` (52.3% to
  it, from 56.5%) and red planet decides much of it (25% of the points
  there): the plan side's extractors trail at minute 10 (7.3 against
  11.9) while its builders take ten tower orders in the first ten
  minutes.
* Towers engage about as rarely as before (55% of them ever have an
  enemy in reach, 7–9% of samples): the type part makes them cheap and
  puts direct fire where ground units came, the place part (exposure
  from observed attacks) did not pay on its own.
* The ceiling's 15% of our value binds late (40-minute games: towers
  worth ~11,600 at minute 40 against ~7,000 by the count curve). A
  build with 10% held 28.6 towers worth 10,276 at minute 40 and took
  43.8% [31.2, 56.2] of the points against v1 (96 games; paired with the
  default's −2.1 [−6.2, +2.6]), so the share stays at 15%.
* v1 still takes 54.2% of the points in 40-minute games: its army at 30
  minutes is 17,868 against our 11,716 on 8.4 factories against 6.4. The
  factories part closes the factory gap by less than one factory and
  gains nothing; `expand` grows the economy, not the army.
* Constants that could become parameters: `aFacIncome`, `aYieldMul`,
  `aYieldNeed`, `aAtkFull`, `aTypeVR`, `aValShare` and the parts' others
  (`army.go`).

### 13.13 Openings and the first ten minutes (G4: `open_reclaim`, `open_army`, `open_fam`)

Unit G4 set out to close the early-economy and first-army gap to human
players without losing strength. Nanolathe Modern AI policy (the brain's
own play, no retail behaviour claimed). Every part is a player-spec
switch read by `VarietyFrom` (style.go), not a `params.go` row; design
notes are in the header comments of `opening.go`, `open_reclaim.go` and
`econ_family.go`.

| switch | default | what it does |
|---|---|---|
| `open_reclaim=<weight>` | 200 | early reclaim while metal is short (below); 0 turns it off |
| `open_reclaim_hi=<permille>`, `open_reclaim_end=<minutes>` | 500, 12 | tuning: the metal store at which the reclaim gate shuts, and the minute it ends (full to half of it) |
| `open_army=<parts>` | 3 | early army, a sum of parts: 1 scouts wait for the first combat unit, 2 combat units follow the first constructor |
| `open_fam=0\|1` | 0 | the first factory's family drawn per game from the stock human shares, tilted by the style; without jitter the most likely family, no draw |
| `open_follow=0..2` | 0 | measured, not default: after the first factory, 1 a second of its family, 2 a vehicle plant |

`open_reclaim=0,open_army=0` (with `open_fam` off, the default) is the
brain before this section, result JSON for result JSON (a sherwood mirror
checked against the binary built before it). Specs that name the
pre-§13.13 brain — the protocol v2 `v1` label, and any "off" side — need
`open_reclaim=0,open_army=0` added.

**Evidence.** Human numbers are aggregates of the recorded-game benchmark
(`~/ta-demos/derived/REPORT.md` sections 3, 4 and 8) and, for the stock
unit set, of the original-TA 1v1 extracts (`~/ta-demos/extract`, v3.1 and
tada, 180 player-games with named units); the brain's are the switches-off
side of the protocol v2 gate games below (hard, 20-minute land pool,
medians of 192 player-games):

| measure | humans (stock units) | util+tac hard before |
|---|---|---|
| first build energy | 90% | 100% (the style leads, §8 of the research doc) |
| first factory started (min) | 1.21 (ProTA 1.66) | 1.50 (completed 1.87) |
| first factory product | constructor 53%, combat 33%, scout 8% | constructor 44%, scout 41% (two scouts in a row 40%), combat 14% |
| first constructor / first combat unit completed (min) | 2.49 / 3.66 | 3.65 / 4.3–4.5 (the first squad member the same; mean 4.9–5.0) |
| army value at minute 5 | 368 (original-TA winners; ProTA land 416) | ~205 |
| first factory family | vehicle 62%, air 19%, kbot 12%, ship 7% | vehicle 97% (ship 2%, kbot 1%) |
| features reclaimed by minute 5 (Painted Desert recordings, per player) | 27–42 (metal produced 2,800–3,500) | 0–1 Clear (metal produced ~1,300) |

The energy-first opening and the human factory timing were already in
place (style leads; the first factory is started at a median 1.5 minutes).

#### Early reclaim (`open_reclaim`, open_reclaim.go)

Reclaiming a feature takes fifteen ticks plus half its energy and metal
pools whatever the builder's build power [05 R-WORK-01 §5]. Each metal
feature in `Obs.Features` is a candidate job: a `Kit.Clear` of radius 160
at it, whose metal and work are those of the four richest metal features
around it and whose time adds the walk there and from it to each. The
score is metal need × job rate (metal per second of walk and work,
5 m/s rated 1000, at most 3000) × site threat (squared for the commander,
which keeps its leash) × the short-metal gate × the opening fade (full to
minute 6, none from 12) × the weight. The gate is what made it pay:
spending above income and the metal store below half, fully open from an
eighth; the builders already at work then spend the whole income, so one
more building would only share it. A pile just sent to is skipped until
the feature listing refreshes (330 ticks).

Reclaiming whenever a pile was near cost the extractor build-up
(ten-minute screens on the six feature maps: metal produced by minute 5
+1% at weight 100, −16% at 200, −26% at 300). Feature metal within the
listing's 1,600 wu is 0 on great divide, the pass, comet catcher and coast
to coast, 100–1,000 on sherwood, full moon and evad river confluence, and
675–2,025 on dark side, red planet, metal heck, ashap plateau and painted
desert.

#### Early army (`open_army`, opening.go, production.go)

* **1 scouts wait**: no scout before the first combat unit that is not a
  scout is built or queued, until minute 4. Alone it moved the first
  product to constructors (first constructor 3.24 min) but the first
  combat unit later (4.69).
* **2 combat after the first constructor**: once a constructor is built,
  framed or queued, constructors rate eight times lower (a constructor
  still goes ahead when no combat unit can be made) until two combat units
  have been queued, until minute 6.

#### First-factory family (`open_fam`, `open_follow`, econ_family.go)

`open_fam` draws the family once per game, after the style, lead and
jitter draws (turning it on leaves them unchanged), from the stock-unit
human shares (vehicle 63, air 20, kbot 10, ship 7; §13.5) with each
style's tilt: the archetype's ProTA first-factory family share over the
corpus's (kbot labs 51%, vehicle plants 27%, air plants 11%, shipyards
11%) for the families its row lists — expand kbot 116 / vehicle 93, eco
kbot 104 / vehicle 111 / air 73 / ship 91, units kbot 55 / vehicle 119 /
air 173 / ship 182, tower kbot 137 / vehicle 78 / air 64, twofac air 382 /
kbot 63 / ship 173 / vehicle 26; balanced and greedy untilted. The ProTA
shares themselves (kbot 53% on land) are not used: that mod rebalances
the units. The shipyard is drawn only where ships reach and the air plant
only where no land army does (§13.5). An explicit `fac_first` wins.
Drawn over the 384 on-side games of the two gate runs with it (12 land
maps): vehicle 82.3%, kbot 17.2%, shipyard 0.5% (coast to coast), air
plant none; kbot by style expand 21% (116 games), eco 18% (112), units 4%
(92), tower 29% (48), twofac 50% (8), greedy 0% (8).

Kbot openings are the cost. Forced kbot against forced vehicle (96 games):
34.4% of points [28.6, 40.1]; extractors at minute 10 8.0 against 10.2 and
income at 20 minutes 26 against 35 metal/s. The stock kbot constructor
builds at 80 and walks 23–26 wu/s against the vehicle constructor's 100
and 38–41, and the gap opens from minute 4 at the same constructor count.
Human kbot openers in the stock recordings show the same (extractors
built by minute 10: median 9 against 12 for vehicle openers, 21 and 111
player-games). Neither follow-up helped: a second kbot factory 31.8%
[26.6, 37.0], a vehicle plant next 33.9% [29.2, 38.0] (the kbot openers
already build one; the lag is before it), and more kbot constructors
(`w_cons=140` in kbot openings) 30.7% [26.0, 35.4].

#### Results

Hard, protocol v2 (§5 of the research doc): the 12-map land pool, seeds
301–308, both slot orders, random starts, 20 minutes, 192 games per pair
(96 for the screens), against the brain with every opening switch off;
the first-named side's points share with its 90% map-cluster interval
and W-D-L (commanders killed / lost).

| part (Gate A) | games | W-D-L (killed / lost) | points share |
|---|---|---|---|
| reclaim, gate at 200‰ of the store (first build) | 192 | 70-71-51 (21 / 12) | 54.9% [51.8, 58.1] |
| **reclaim, gate at 500‰ (default)** | 192 | 75-69-48 (23 / 10) | **57.0% [53.4, 60.9]** |
| screens (96): gate 200‰ weight 400; 200‰ to minute 20; 500‰ weight 200 / 300; 700‰; 1000‰; 500‰ to minute 20 | 96 each | | 52.1, 54.2, 61.5, 59.9, 58.3, 56.8, 61.5% |
| army 1 (scouts wait) | 192 | 62-72-58 (22 / 14) | 51.0% [48.7, 53.9] |
| army 2 (combat after the first constructor) | 192 | 54-72-66 (16 / 20) | 46.9% [43.0, 51.0] |
| **army 3 (both, default)** | 192 | 58-75-59 (16 / 14) | **49.7% [47.1, 52.3]** |
| family draw (`open_fam=1`) | 192 | 58-73-61 (20 / 17) | 49.2% [46.6, 51.6]: kbot openings 38.2% (34 games), vehicle 51.3% (157) |
| all three | 192 | 63-75-54 (24 / 13) | 52.3% [47.7, 57.0]: kbot openings 36.8% (34), vehicle 55.4% (157) |
| **defaults (reclaim + army 3)** | 192 | 67-78-47 (23 / 9) | **55.2% [49.7, 60.7]** |

The reclaim's gain is on the feature maps (points share by map, reclaim
alone: red planet 72%, ashap plateau 69%, sherwood 62%, full moon 62%,
painted desert 59%, evad river confluence 59%; coast to coast and metal
heck 50%, where it never fires), 4.7 Clears per game.

Gate B (40 minutes, the v2 long pool: the pass, show down, plains and
passes, greenhaven, sherwood, great divide; seeds 301–308, 96 games for
the defaults, 48 for the parts):

| Gate B | games | W-D-L (killed / lost) | points share | invested score |
|---|---|---|---|---|
| **defaults** | 96 | 39-27-30 (22 / 19) | **54.7% [50.5, 58.9]** | 54.2% [50.0, 58.3] |
| reclaim alone | 48 | 19-13-16 (14 / 11) | 53.1% [47.9, 61.5] | 52.1% [45.8, 60.4] |
| army 3 alone | 48 | 17-15-16 (9 / 10) | 51.0% [42.7, 61.5] | 51.0% [42.7, 61.5] |
| family draw alone (`open_fam=1`) | 48 | 18-14-16 (13 / 13) | 52.1% [47.9, 56.2]: kbot openings 85% (10 games), vehicle 43% (38) | 51.0% [46.9, 55.2] |

Early metrics (the Gate A games of the defaults: hard, 192 player-games
per side; times are medians, values means; "metal produced" integrates
the observed income between thinks, `open_metal_5/10` in the Report):

| measure | before | after | humans |
|---|---|---|---|
| first factory started (min) | 1.50 | 1.50 | 1.21 stock (ProTA 1.66) |
| first constructor completed (min) | 3.65 | 3.21 | 2.49 |
| first combat unit, not a scout (min) | 4.40 | 3.42 | 3.66 |
| first squad member, `tac_first_member_tick` (min, median / mean) | 4.40 / 5.01 | 3.42 / 3.56 | – |
| first scout (min) | 3.03 | 4.22 | – |
| metal produced by minute 5 / 10 | 1,689 / 5,774 | 1,742 / 5,864 | – |
| army value at minute 5 / 10 | 201 / 1,085 | 299 / 1,272 | 368 / 1,723 (original-TA winners) |
| metal income at minute 10 / 20 (/s) | 17.7 / 30.4 | 18.1 / 34.3 | 15.5 / 30.2 (original TA, all) |

In the 40-minute games the first combat unit comes at 3.32 minutes
against 3.84 and the army at minute 5 is 327 against 257.

**Replay benchmark** (`brains/replay`; hard in the recorded loser's seat
against the re-enacted winner, the recorded starts, R1's plan; before =
the switches off, after = the defaults; the human loser's metal produced
is the recording's cumulative counter, the brain's the integrated income,
army values in the arena's definition):

| recording | human loser metal 5 / 10, army 5 / 10 | before (seeds 1, 2) | after (seeds 1, 2) |
|---|---|---|---|
| painted desert (a) | 3,531 / 9,772; 360 / 4,492 | metal 1,350 / 5,068 and 1,116 / 5,057; army 166 / 1,673 and 0 / 1,381; won at 19.2 and 19.0 min | metal 1,532 / 5,161 and 1,505 / 5,454; army 305 / 887 and 166 / 1,362; won at 15.6 and 13.6 min (22 and 15 Clears) |
| painted desert (b) | 3,032 / 9,312; 992 / 3,908 | metal 1,168 / 5,642 and 1,589 / 5,888; army 212 / 808 and 166 / 1,196; won at 19.6 and 44.0 min | metal 1,393 / 5,274 and 1,710 / 6,777; army 351 / 1,277 and 332 / 1,263; won at 17.2 and 22.1 min (16 and 12 Clears) |
| comet catcher | 1,742 / 8,015; 132 / 1,797 | metal 1,488 / 5,487 and 1,488 / 6,092; army 0 / 974 and 248 / 1,078; lost on points and by commander kill | metal 1,444 / 5,511 and 1,444 / 6,214; army 342 / 1,507 and 342 / 1,964; lost on points twice (commander kept) |

On painted desert the brain's metal by minute 5 rises from 0.32–0.52 to
0.43–0.56 of the human loser's and its army from 0–0.46 to 0.33–0.85; the
human players reclaimed 27–42 features by minute 5, where the brain's
first Clears come when its metal first runs short (from minutes 2–3).

**Ladder** (12 maps, seed 301, both orders, 24 games per pairing; the
first-named persona's points share):

| pairing | switches off | defaults |
|---|---|---|
| easy vs medium | 14.6% [6.2, 22.9] (1-5-18) | 12.5% [4.2, 22.9] (2-2-20) |
| hard vs medium | 83.3% [72.9, 93.8] (17-6-1) | 72.9% [62.5, 83.3] (15-5-4) |

easy < medium < hard still holds; the gap between medium and hard
narrows by ten points (both personas play the switches; why medium gains
more was not traced).

**Cost and determinism** (synchronous `-allocs` matches, 20 minutes, seed
301, the defaults against the switches off; the host ran other agents'
work at a load average of 140, so microseconds are noisy): sherwood think
47.1 / 51.6 µs mean, 140 / 147 µs p99, 50 / 207 B and 0.03 / 0.05 objects
per think; painted desert 69.6 / 65.3 µs mean, 216 / 171 µs p99, 889 /
266 B and 1.77 / 0.03 objects. The reclaim's pricing allocates nothing
once its scratch has grown (a test pins it), so the painted desert
difference comes from elsewhere in a game the switches change (sherwood
goes the other way); where was not traced. `async=1` reproduced three
synchronous games exactly (sherwood and painted desert as above, red
planet seed 303).

#### Gaps

* **Painted desert's first five minutes.** The brain's metal by minute 5
  is still about half the human loser's: people reclaim everything within
  reach in the first minutes, between the commander's builds, where the
  gate waits for metal to run short (from minutes 2–3). Reclaiming
  whenever a pile was near cost the extractor build-up; reclaiming while
  a build waits on resources, or a constructor kept on reclaim while
  piles last, are the next things to try.
* **Metal features only.** `Kit.Clear` reclaims features holding metal
  (and lane blockers), so trees are never reclaimed for energy, and
  `Obs.Features` lists only the 1,600 wu around the start.
* **Kbot openings** lose about two games in three against vehicle
  openings in the stock unit set (the constructor's build power and
  speed), so the family draw is a switch, off by default; the style tilts
  come from ProTA archetypes, whose unit balance differs.
* **The first constructor** still comes about 0.7 minutes after the
  human median; the commander does not help the first factory.
* **Constants, not parameters:** the Clear radius (160 wu), reference
  rate (5 m/s) and rate cap (3×), the fade (minutes 6–12), the scouts'
  wait (minute 4), the combat-first window (minute 6), its two combat
  units and its eightfold cut.
* **State.** The opening state rides in the tech state (`s.tech.open`)
  because `shared` is in model.go; it belongs in a field of its own.

### 13.14 Metal use at every difficulty (`metal`)

#### The report

A play test on Great Divide (medium, Core, fixed starts, `--gameplay
modern-ai`, since retired for `--ai-player all=modern`): several metal spots stood open, yet the brain built metal
makers, ran its metal store empty (every build slowed) and kept adding
solar collectors and wind generators.

Reproduced with `ai-arena match -map "Great Divide" -seed 1 -ticks 18000
-level medium -p retail::0 -p util+tac:medium:1`: at minute 9 metal
income 9/s with the store at 0 of 1,250 and energy 255/s with the store
full; 5 extractors while the explain notes counted 22 free safe spots; by
minute 10 8 solar collectors, 6 wind generators and 3 makers against 7
extractors. The extractor cap held in 422 thinks, the factory cap in 411
and the constructor cap in 292. The metal audit (below) on that game: 3
makers, all started while a free safe spot stood open; 14 energy starts;
7,530 of the 18,000 ticks with the energy store nine tenths full and the
metal store empty.

#### Causes (traced)

* **The ambition extractor cap.** Below ambition 100 `variety.limit`
  holds extractors to the persona's share of the top tier's curve by
  zeroing the extractor and maker weights for the think (medium: about 3
  at minute 5, 6 at minute 9). Near-home spots waited with far ones, and
  an idle builder had energy, radar, storage and towers left to build.
  At t396 s a constructor's candidates were wind (energy need 225‰,
  chosen), solar, radar and storage; no extractor was offered.
* **Energy never stops scoring.** A resource's need is the reciprocal of
  its coverage, and a full store cuts it by at most three quarters, so
  with metal the binding resource energy still scored 150–900‰ of
  nominal, far above the least a choice must score (30). The energy
  expense the observation reports is what builds request (the settlement's
  requested total), not what they draw: while metal is short builds
  stall and draw less, the store stays full, and the expense still reads
  above income (at minute 8.2: requested 346/s against income 199/s with
  the store at 1,350 of 1,350), so coverage said energy was short.
* **Makers outscored far spots.** When the cap did not bind, a maker's
  return per cost (1,587‰) against an extractor's (797–1,178‰) times the
  extractor's travel (570–680‰) made the maker the better build: all 3
  makers in the game were chosen over an extractor candidate on offer to
  the same builder (t287: maker 2,247 against extractor 1,841).

#### Rules

`metal` is a sum of parts (default 7, all):

| part | rule |
|---|---|
| 1 makers last | no metal maker is started while a free, safe extractor spot on our side stands open that a builder of ours can reach and fit (`shared.freeSafe`: free on the board, not blocked after a failure, territory ≥ 600‰, threat below `threat_half`; with the terrain model, reachable by a builder class that can make an extractor that fits it; the commander only within its leash) |
| 2 energy follows metal | no energy building and no energy storage is started while the energy store is at least nine tenths full and not draining: income covers expense, or metal is short (coverage below nominal), when the expense read is only requested. The opening lead (energy before the first extractor, first three minutes) is exempt. A builder with nothing worth building assists, guards, reclaims or waits |
| 4 home spots | when the ambition extractor cap binds, spots within the commander's leash of home (`com_radius`, which ambition scales: 846 wu at medium, 724 at easy) stay in the plan; only spots beyond it wait for the tier curve, and makers wait as before |

At ambition 100 there is no cap, so hard plays parts 1 and 2. `metal=0`
is the brain before this section, game for game (checked: identical
result JSON on the reproduction game).

##### Why the cap stays the lower personas' handicap

The brief allowed moving the handicap elsewhere. Each alternative was
tried on the development subset (great divide, red planet, sherwood,
comet catcher, the pass, dark side; seeds 301–302; both slot orders;
random starts; 24 games per pairing; "vs 3.1" is the persona against the
3.1 AI at the same level with the 3.1 income discount on both sides):

| variant (metal) | easy vs 3.1 easy | medium vs 3.1 medium | hard vs 3.1 hard | easy vs medium | hard vs medium | energy full, metal empty (easy / medium, s of 1,200) |
|---|---|---|---|---|---|---|
| before (0) | 85.4% | 91.7% | 97.9% | 27.1% | 87.5% | 459 / 351 |
| 1+2+4 (7, default) | 89.6% | 93.8% | 97.9% | 25.0% | 70.8% | 461 / 421 |
| 1+2, no extractor cap | 93.8% | 97.9% | 97.9% | 43.8% | 54.2% | 105 / 212 |

Without the extractor cap (parts 1 and 2 alone, the count cap removed)
the economy looks right at every persona — energy-full-and-metal-empty
time falls by three quarters at easy — but easy scores 44% and hard 54%
of points against medium: the cap is what separates the personas. Moving
the handicap to constructors (the constructor cap read at ambition
squared, no extractor cap) made easy stronger still against 3.1 easy
(97.9% on the subset: constructors cost metal, and the metal went to the
army), and a rest between builder jobs (9 ticks per point of ambition
below 100: 20 s at easy, 6 s at medium, no extractor cap) changed nothing
measurable (93.8% against 3.1 easy, as without it). The cap therefore stays; part 4 lets it spare the spots
beside home, and parts 1 and 2 keep what it holds back from turning into
makers and energy.

What remains under the cap is the handicap itself: a medium or easy
persona that has built its home spots and reached its curve waits with
metal short until the curve allows the next spot beyond home; its idle
builders build nothing rather than filler (the reproduction game at
minutes 7–9: three to four idle constructors, best candidate a radar at
19–28, below the 30 a choice must score). Its extractor counts match the
human tiers it is calibrated on (medium at minute 10: 7 on great divide,
the low-to-mid tier curve 7.8).

#### Results

Hard, Gate A (protocol v2 land pool: 12 maps, seeds 301–308, both slot
orders, random starts, 192 games, `metal=7` against `metal=0`): 76-72-44,
**58.3% of points [53.6, 62.5]**, decisive-only 84.1% (37 of 44), margin
sweep 60.9 / 58.3 / 57.8 / 57.0%, 26 style pairings. Makers started 3.3 →
1.3 per game (with a free spot 2.1 → 0), energy starts 25.8 → 20.5 (with
the store full and metal short 1.0 → 0), extractors at minutes 10 / 15
9.3 / 13.4 → 10.1 / 14.9, army at 10 / 15 / 20 minutes 1,089 / 2,829 /
4,904 → 1,367 / 3,471 / 5,406. Think time unchanged (128 against 130 µs
mean).

Easy against 3.1 easy (3.1 income discount on both sides), the whole land
pool (192 games each, paired game for game with a 90% map-cluster
interval):

| easy build | points | paired vs before | makers (free) | energy starts (full) | energy full, metal empty | extractors 5 / 10 / 15 | army 10 / 15 / 20 |
|---|---|---|---|---|---|---|---|
| before (`metal=0`) | 70.1% | – | 0.7 (0.5) | 14.3 (3.9) | 541 s | 1.1 / 2.7 / 4.3 | 303 / 467 / 725 |
| parts 1+2 (`metal=3`) | 77.3% | +7.3 [+4.4, +10.4] | 0.2 (0) | 10.3 (0) | 665 s | 1.1 / 2.8 / 4.3 | 353 / 627 / 929 |
| default (`metal=7`) | 77.9% | +7.8 [+4.2, +11.5] | 0.3 (0) | 11.9 (0) | 468 s | 3.5 / 4.4 / 5.8 | 330 / 722 / 1,042 |

**Easy is stronger** against 3.1 easy by about 8 points, and the gain is
parts 1 and 2 (the metal no longer spent on filler energy and makers goes
to the army), not the home spots. The brief requires easy no stronger than
before; the lever that takes those points back is not settled (see Gaps).

Retail matrix subset (six maps, seeds 301–302, both orders, 24 games per
cell; our persona against 3.1 at each level with the 3.1 income discount
on both sides), before → after: at 3.1 easy, easy 85.4 → 89.6, medium
83.3 → 91.7, hard 85.4 → 97.9; at 3.1 medium, easy 81.2 → 87.5, medium
91.7 → 93.8, hard 97.9 → 100; at 3.1 hard, easy 83.3 → 91.7, medium 100 →
93.8, hard 97.9 → 97.9. `metal=0` on the final build reproduced all 216
before games exactly.

Ladder before (v2-ladder, 768 games; 3.1 at hard, full income): easy vs
3.1 74.2%, medium vs 3.1 92.4%, easy vs medium 19.0%, medium vs hard
19.8%. After: not run on the full ladder; the subset head-to-head above
(easy vs medium 25.0%, hard vs medium 70.8%) keeps the ladder monotone,
with medium nearer hard.

Reproduction game (great divide, seed 1, medium, 13 minutes), before →
after: makers 3 → 0, energy starts 24 → 11, energy full with metal empty
7,650 → 4,170 ticks, extractors at minute 5 3 → 6, at minute 10 6 → 7.

#### Audit (arena `extra`)

| key | meaning |
|---|---|
| `metal_makers`, `metal_makers_free` | maker starts; those while a free safe spot stood open (part 1 holds the second at 0) |
| `metal_energy`, `metal_energy_full` | energy starts; those while the energy store was nine tenths full and metal short (coverage below nominal) |
| `metal_energy_held` | energy starts while part 2's condition held: only the opening lead (the first think's energy in most games) |
| `metal_full_empty_ticks` | ticks with the energy store nine tenths full and the metal store at most 5% full |
| `metal_mex_05/10/15`, `metal_free_05/10/15` | finished extractors and free safe spots at the first think at or after minutes 5, 10 and 15 |

#### Gaps

* **Easy is about 8 points stronger against 3.1 easy** (70.1% → 77.9%
  on the land pool). Candidate levers not yet measured: the army tier
  curve (cap and launch value) read at ambition squared below 100, with
  or without the banking rule lifting the army cap; a factory rest
  between units. Tried without effect or with the wrong sign: a rest
  between builder jobs, the constructor cap at ambition squared.
* The cap's metal-bound waits remain for easy and medium (energy full,
  metal empty, idle builders while spots beyond home stand open). A
  handicap that keeps the ladder without them was not found: the three
  tried either collapse the ladder or strengthen easy.
* Energy need still reads the requested expense below a full store:
  while metal is short and the store sits between half and nine tenths,
  energy can still look short. Part 2 covers the full store only.
* `freeSafe` counts spots up to 70% of the way to the enemy base
  (territory ≥ 600‰); on large maps makers wait for spots a builder
  would reach only after a long walk.

### 13.15 Personality (P2: `personality`, `trait_<name>`)

Unit P2 (2026-09-25). The user asked (2026-09-24): "It would be nice to
keep some of the AI behaviors & personalities configurable (not in the
UI). By default we could randomize it, so each round the AI might behave
slightly differently but overall still smart." and then (2026-09-25):
"Maybe for the variety we don't need completely different styles, just
weights between them changing a bit. So some raids still happen, some
towers still get built, etc, just at different rates to randomize things
a bit." Nanolathe Modern AI policy (the brain's own play, no retail
behaviour claimed); design notes are in the header of `personality.go`.

**The default is balanced.** Until this unit each game drew one of the
six opening archetypes (§8 of the research doc). They lost points to the
tuned brain: over 432 40-minute games the drawn styles took 43.6% (tower)
to 62.9% (twofac) of the points, expand, the most drawn, 43.9%, and
`style=balanced,jitter=0` took 58.3% [50.0, 65.6] against the drawn
default (48 games, the coordinator's measurement at 5ff86ca0). So every
game now plays the balanced style with its opening jitter, and a style is
played only when configured (`style=<name>`, or `style=random` for the
old draw). The variety comes from the personality.

**Traits.** Eight integers from −100 to 100, 0 neutral. A trait at ±100
moves each of its parameters to the percent its row lists, linearly from
none at 0; every moved parameter is clamped into its range (§6). No trait
switches anything off: at every value the brain still raids, builds
towers, expands and techs, at other rates.

| trait (key) | at −100 | at +100 |
|---|---|---|
| aggression (`trait_aggression`) | launch share of the attack value 133% (60% of the tier curve); engage margin +80‰, retreat margin +40‰ | 67% (30%); −80‰, −40‰ |
| raids (`trait_raids`) | the raid squad splits off at 140% of its value (2,100); the harass raid's least value 150% (300) | 80% (1,200); 90% (180) |
| towers (`trait_towers`) | w_defense 60% | 150% |
| expansion (`trait_expansion`) | w_cons 80%, w_metal 85% | 125%, 120% |
| tech (`trait_tech`) | tech_time 130%, w_tech 60% | 70%, 150% |
| heavy (`trait_heavy`) | v_ref 60%, w_range 50% | 180%, 200% |
| air (`trait_air`) | w_air 50% | 140% |
| scouting (`trait_scouting`) | w_scout 30% | 200% |

Aggression's launch share is `waveShare` (45%, or `att_share`) of
`attackValue`; production's army target (`armyTarget`) does not follow it,
so an aggressive personality launches earlier with the army it has rather
than building less. Its margin shift and the raid trait's sizes reach the
tactics army as a temper (`Strategy.ArmyTemper`, `tactics.Temper`), which
`mods/aikit` `NewUtilTac` hands the army and the army folds into its
parameters once, at its `Init` (core runs the strategy's `Init` first);
they scale `em`, `rm`, `raidv` and `hv` as configured.

**The draw.** With jitter on (the default) each trait is drawn on its own
as the sum of two uniform draws in −50..50: triangular on ±100, most games
near neutral (|t| below 30 in half the traits), a few leaning hard (|t|
above 60 in 16%, above 80 in 4%). The draws come from `Kit.Rand` in
`Init` only, two per trait, after the style, lead and jitter draws and
before the first-factory family draw (`open_fam`), so turning the
personality off leaves every earlier draw as it was
(`TestPersonalityDrawsAfterTheOthers`); a pinned trait is drawn too and
then replaced, so pinning one trait leaves the others as drawn
(`TestPinnedTraitsAndNamedArchetypes`). A game's personality is therefore
reproducible from its seed and slot, and the synchronous and asynchronous
hosts draw the same. `jitter=0` draws nothing: the personality is then
neutral, and `style=balanced,jitter=0` stays the deterministic brain. A
drawn personality is named by its strongest lean — aggressive or patient,
raider or quiet, turtle or open, expander or compact, early-tech or
late-tech, heavy or light, flyer or grounded, scout or unscouted — or
`steady` when no trait leans 30 or more.

**Archetypes** (played only when configured, `personality=<name>`; with
jitter each trait varies by up to ±25 about the centre, clamped to ±100).
They pull several traits together; as wholes they were not measured
against the default.

| personality | aggression | raids | towers | expansion | tech | heavy | air | scouting |
|---|---|---|---|---|---|---|---|---|
| rusher | 70 | 40 | −50 | −30 | −50 | −40 | 0 | 20 |
| raider | 20 | 80 | −20 | 0 | −20 | −60 | 20 | 60 |
| turtle | −60 | −50 | 80 | −20 | 30 | 40 | −20 | −30 |
| boomer | −40 | −20 | −30 | 80 | 20 | 0 | 0 | 0 |
| tech | −20 | −30 | 20 | 20 | 80 | 60 | 30 | 0 |
| flyer | 0 | 20 | −20 | 0 | 20 | 0 | 90 | 30 |

There is no kbot personality: kbot first factories lose about two games
in three (§13.13), and no trait touches the first factory's family (v_ref
and w_air do not move it, §13.5).

**Keys** (player-spec, settings `modernAI`, `--ai`; `VarietyFrom`):
`personality=<random|off|rusher|raider|turtle|boomer|tech|flyer>`
(default `random`, the draw above); `trait_<name>=-100..100` pins a trait
with or without a personality; `style=<name|random>` (default
`balanced`); `jitter=0`.

**Report and explain.** The arena's `extra` carries `personality_mode` (0
none, 1 drawn, 2 named), `personality_<name>` = 1 and every trait's value
under its key; the tactics army adds `tac_raid_launches` (raid-squad
launches, chains included). The strategy's explain notes add `style
balanced, personality raider (drawn) aggression +12 raids +64 …`.

**Saves.** A save's sidecar keeps the generator's seed and position
(DESIGN_SESSIONS_AI_SAVE "Saves"); a load rebuilds each controller, whose
`Init` draws the same personality from the seed, and the army takes the
same temper. Checked on a four-player skirmish of Modern AI players on the pass
(one player with a pinned trait): every computer player's style, traits and
army temper (`em`, `rm`, `raidv`, `hv`) were the same after the load.

#### Results

Hard, protocol v2 (§5 of the research doc): the 12-map land pool, both
slot orders, random starts, 20 minutes unless stated; points shares with
their 90% map-cluster intervals.

**Trait extremes.** Each trait pinned at −100 and at +100 with
`personality=off`, against `personality=off` (development seeds 311–314,
96 games each; at the time the default still drew a style per game, on
both sides). The trait side's points share (intervals about ±4–6 points),
and what the trait moves (means over the trait side's player-games; in
brackets the `personality=off` side over all 1,536 games):

| trait | −100 | +100 | narrowed | what moves: −100 / +100 (off) |
|---|---|---|---|---|
| aggression | 52.1% | 47.4% | – | first offensive, minute 9.3 / 7.7 (8.4) |
| raids | 49.5% | 45.8%; 49.0% narrowed | +100 from a raid split at 60% and a harass value of 75% to 80% / 90%; −100 from 160% / 200% to 140% / 150%, so raids stay present (not re-measured) | harass raids 1.5 / 5.0 (5.0); raid-squad launches 1.7 / 2.3, narrowed 2.2 (1.8) |
| towers | 52.6% | 47.9% | – | towers standing at 15 minutes 7.6 / 11.7 (10.4) |
| expansion | 53.6% | 47.4% | – | extractors at 10 minutes 8.7 / 9.6 (9.3); factories built 5.0 / 5.8 (5.4) |
| tech | 48.4% | 50.5% | – | tech-2 factories built 0.31 / 0.89 (0.71) |
| heavy | 47.9% | 52.1% | – | the unit mix (not in the census) |
| air | 51.0% | 44.8% at 250%; 46.9% at 175%; 49.0% at 140% | +100 from w_air 250% to 140% | air plants built 0.34 / 1.09 at 250%, 0.82 at 175%, 0.52 at 140% (0.46) |
| scouting | 50.5% | 53.1% | – | scouts built 0.9 / 3.0 (2.7) |

**Gate A** (seeds 301–308, 192 games per pair; the default is the
balanced style with jitter and a drawn personality):

| pair | W-D-L of the first (commanders killed / lost) | points, first side | margins 1.1 / 1.5 / 2.0 |
|---|---|---|---|
| default against `style=balanced,jitter=0` | 65-63-64 (25 / 29) | **50.3% [46.1, 54.7]** | 49.2 / 49.7 / 48.2% |
| jitter alone (`personality=off`) against `style=balanced,jitter=0` | 64-64-64 (28 / 23) | 50.0% [46.9, 53.4] | 49.7 / 52.6 / 52.1% |
| default against jitter alone (the personality on against off) | 65-60-67 (19 / 31) | 49.5% [46.4, 52.3] | 49.5 / 49.0 / 48.4% |

By the drawn traits (the default's 384 player-games in the first and
third pairs; a trait "leans" at |t| ≥ 30): no lean's points share is
beyond noise — aggression ≥ 30 43.9% (74 player-games), towers ≤ −30
43.6% (94), expansion ≥ 30 45.8% (96), scouting ≤ −30 59.1% (88), the
others 44.9–54.6%.
