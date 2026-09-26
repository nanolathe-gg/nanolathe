# tactics — tactical army prototype

An army policy (`core.Policy` + `core.Explaining`) for the Modern AI research
framework ([docs/MODERN_AI_RESEARCH.md](../../../../docs/MODERN_AI_RESEARCH.md)):
squads with roles, influence maps, Lanchester square-law engagement
prediction, threat-aware routing and skill-gated micro. It replaces only the
army layer of the scripted baseline, so every comparison with `scripted`
isolates the army:

```
tactics = core.New("tactics", core.ScriptStrategy{}, &core.ScriptEconomy{}, tactics.New(params), &core.ScriptProduction{})
```

Registered in `cmd/ai-arena/brain_tactics.go`. Ablation switches in the player
spec: `route=0 micro=0 raid=0 defend=0 escort=0 budget=0 air=0 naval=0
posture=0 passage=0 unseen=0`, margin shifts `em=<‰> rm=<‰> nm=<‰>` (e.g.
`tactics:hard:0:micro=0,label=T-nomicro`), for the attack posture
`pv=main` (measure the main squad alone) and `pagg=<‰>` (aggression slope),
and for early harassment `harass=0 probe=0 tour=0` with the tuning knobs
`hn=<units> hv=<value> sm=<‰> raidv=<value>`.
With `air=0,naval=0` the army plays exactly as before the air and naval
forces existed (identical games), and with `posture=0` exactly as before it
honored the strategy's posture, so one arena binary plays new against old.

## Attack posture (2026-09-23, `posture.go`)

The strategy layer publishes `core.Posture` every think: `AttackValue`, the
army value at which to launch an offensive, and `Aggression` 0..100. The
utility strategy derives the attack value from the persona's army tier curve
(45 % of it at the persona's ambition, at least 300, shifted in time by the
style), the enemy estimate (`att_ratio`) and the wave jitter
([docs/MODERN_AI_RESEARCH.md](../../../../docs/MODERN_AI_RESEARCH.md) §8;
*Results → Attack value calibration* below).
The scripted `WaveArmy` always launched on `AttackValue`; this army launched
on its own prediction alone, so in `util+tac` styles and ambition changed only
how hard production pushed the army. With `posture` on (the default, in every
brain that uses this army):

* **Hold.** The main squad launches no *offensive* — a march on a target
  zone, or an exploration of an unseen start position — while the
  *available army* is below `AttackValue`. The available army is the value
  of every squad's members: the army the strategy sized the attack value
  against, less scouts and the units that cannot fight now (withdrawn,
  stuck, or called back from goals out of reach). `pv=main` measures the
  main squad alone.
* **What a held squad still does.** It takes a *clearly undefended* target —
  a predicted ratio of at least the raid margin, 3.0 — as a *soft raid*,
  fights enemies that come within its reach (field skirmishes), answers
  incidents and defends. A soft raid chains only to further clearly
  undefended targets. The raid, home-guard, escort, air, naval and
  amphibious squads keep their own rules.
* **Offensive.** Once allowed, an offensive chains from target to target as
  before until the squad regroups, retreats or turns to defend; the next one
  waits for the attack value again. The attack value only *permits* an
  offensive: every target must still clear the engage margin and every
  fight the retreat margin, so a squad at the attack value never walks into
  a fight it is predicted to lose.
* **Aggression** (`pagg=<‰ per point>`, default 0) lowers every squad's
  engage margin by the slope per point of aggression above 50 (raises it
  below 50), by at most 150 ‰, and the retreat margin by half as much: an
  army ahead overall may take narrower local fights. The engage margin stays
  above an even fight (the smallest base margin, skill 100, is 1200 ‰). At
  4 ‰ per point it measured the same as 0 (see *Results*), so it is off.
* `posture=0` restores the previous army: the result JSON of a checked game
  (great divide seed 121, hard against medium, both `posture=0`) is
  identical to the pre-change binary's apart from the new report counters.

The arena result's `extra` gains, per player: `tac_off_n` (offensives
begun), `tac_off_first_tick`, `tac_off_first_value` (the main squad's
value then) and `tac_off_first_av` (the attack value then),
`tac_off_value_mean` (the main squad's mean value at the start of each
offensive), `tac_soft_raids` and
`tac_soft_first_tick`, `tac_held_thinks` (thinks the hold kept the squad
from a target it would otherwise have attacked), and `tac_av_M`,
`tac_avail_M`, `tac_main_M` (the attack value, the available army and the
main squad's value at minutes 5, 10 and 15). With `posture=0` an
"offensive" is every launch that begins a series (from a regroup, or from a
field fight), so the counters compare old and new directly. Explain adds a
posture note (attack value, aggression, margins, available value, offensive
and held flags).

## Early harassment (G1, 2026-09-24, `harass.go`)

Held by the posture, the main squad took only clearly undefended targets,
and only once it was three units worth 300; with nothing known it never
looked, and the scouts looked at the start position nearest to *home*
next, zig-zagging over ten-start maps. In hard mirrors the first unit came
at minute 4.4 (median), the first target was known at 5.0, the first
launch came at 7.3 and the first kill at 6.3 (human winners: 4.7). Three
switches, all on by default (*Results → Early harassment*):

* `harass`: while held, the main squad raids with **two units worth 200**
  (`hn=`, `hv=`) and weighs targets as the raid squad does, by economy
  (`2·eco + value/4`; a zone of lone mobiles is no raid target), still only
  those at the soft margin (3.0, `sm=`). Out on such a raid it **turns back
  like a raider**: field fights only at the engage margin, and it leaves
  the target or the fight below the engage margin (1.26 at hard) instead
  of the retreat margin (0.79), so a raid that finds a tower or a defending
  army goes home instead of trading. An offensive keeps the old rules.
* `probe`: held with no target known, a main squad of the harass size
  explores the nearest unseen start position (the offensive's exploration
  needs 600 in value and no hold), as such a raid.
* `tour`: a scout looks next at the unseen start position nearest to
  itself, not to home.

The raid squad still splits off at 1500 (`raidv=` moves it). With all three
off the army plays exactly as before (identical result JSON). The report
adds `tac_first_member_tick`, `tac_first_zone_tick`,
`tac_first_launch_tick`, `tac_first_fight_tick` (ground squads) and
`tac_small_thinks` / `tac_blind_thinks` (thinks the main squad had a target
but was too small to launch, or waited with none known).

## Headline (the ground army, before the air and naval forces)

* With a working army on both sides (both players ARM, start positions
  swapped), `tactics:hard` beats `scripted:hard` **30-18-0** over 48 games
  (the 8 dark-side games are draws decided before any army exists, see
  *Framework findings*), trading about 1.8 : 1 in value.
* Against `retail` (ARM mirror, 48 games) it wins **46-2-0 with 37 commander
  kills**; the scripted army wins 46-2-0 with only 7, and trades 16 : 1
  versus 1.7 : 1.
* On the prescribed dev pool with default sides the scoreline is
  **12-1-11 vs retail and 12-0-12 vs scripted** — identical in shape to the
  11-2-11 reference, because the scripted production/economy never fields a
  CORE army (slot 1). Every slot-0 (ARM) game is won, 10 of 12 by commander
  kill against either opponent; the scripted army in the same seat kills the
  commander in 4 of 12 against retail and 1 of 12 against scripted.
* Persona matters as intended: easy < medium < hard against the same
  opponent, easy still plays coherent squads and retreats.
* Cost: ~40 µs mean, ~130 µs p99 per think for the whole brain (the army is
  ~30 µs of it), ~35 B allocated per think (slice warm-up only), async result
  series identical to sync.

## Air and naval forces (2026-09-23)

The army originally knew only ground squads: aircraft joined the main army,
ships were sent at land targets, and on water-separated maps the land army
walked to the shore and waited there for the rest of the game. Two switches,
both on by default, add forces for the other domains. Details below under
*Air task forces* and *Naval and amphibious forces*; results under
*Results → Air and naval forces*. In short:

* On land without aircraft nothing changes (identical games); with air
  plants on both sides the new army takes 54 % of the points against the
  old one, wins the air war 1.4 : 1 and destroys 1.4× the economy it loses.
* On water maps against the old army it takes 52 % of the points with a
  positive fleet and economy exchange; against retail it wins 40-6-2 (90 %
  of points; the old army 36-10-2, 85 %).
* The util+tac think got a third cheaper (cached disc kernels), and the new
  forces cost about 3 µs per think on land and about 10 µs on water maps.
* With the economy unit's water/air production merged (real fleets and air
  wings, no test forcer) and its region maps: every squad goal is now
  reachable for its members' movement class (coast to coast trapped units
  20 → 2 at most), and against retail the water pool is 30-2-0 with 11
  commander kills over two seeds (the integration run: 16-0-0 with none on
  one seed); against the old brain 23-8-1 with 10. The land pool is
  unchanged (24-0-0 against retail, 8-9-7 against the old brain).

| switch | adds |
|---|---|
| `air` | fighter squad (intercept, escort, combat air patrol), strike wing (bombers and gunships), air scouts, anti-air memory, escort persistence; ground squads stop answering air raids |
| `naval` | fleet squad, reach tables (goals reachable per movement class, land cut), amphibious assault squad, fleet and sea-hit memories |

### Unit classes (`class.go`)

Every definition gets a kind from its authored keys, never its name:
*ground* (walks; wades shallow water), *hover* (`canhover`, or a movement
class reaching any depth), *amphibious* (`amphibious`, or a maximum water
depth that reaches the map's sea level — the sea floor is never lower), *naval*
(mobile with a minimum water depth), and for aircraft *fighter* (anti-air
damage at least its total), *bomber* (a dropped weapon — checked first,
because some bombs carry an anti-air damage credit), *gunship* (direct fire),
*torpedo* (torpedoes only) and *air scout* (unarmed; air builders and
transports are left alone). `surfDPS` is the damage of weapons that can hit
land or the water surface (a submarine has none: it cannot shell a coast).

### Air task forces (`air.go`)

**Anti-air picture.** `tac_aa` holds ground and sea anti-air; enemy aircraft
that shoot aircraft are a separate air-to-air force with a centroid.
`tac_aamem` remembers anti-air: every 5 s it fades (time constant 3 min) and
takes in what is known now, so mobile anti-air the host forgot after a
minute still counts; a sector where our aircraft lost hit points without
known anti-air keeps that damage rate as unexplained anti-air. Queries take
the larger of the two.

**Fighters** (squad `fighter`), every think in order:
1. *Intercept* the most pressing visible enemy aircraft: bombers, gunships
   and torpedo planes ×3 over our base, assets, squads or strike (×2
   elsewhere), scouts, constructors and transports ×1.5, fighters ×1; value
   over distance; never one sheltered by anti-air above the fighters' HP/18
   dps. The fight is judged air-to-air (anti-air damage × hit points, square
   law) against enemy aircraft that shoot back within 900 wu plus anti-air
   there; below the retreat margin they leave it. Attack + queued patrol
   there (2 actions), sticky like focus fire.
2. *Escort* a strike in flight: patrol with the wing's centre.
3. *Combat air patrol* between home and the main army (or in front of the
   base), re-aimed only when the point moves 450 wu.

**Strike wing** (squad `strike`: bombers, gunships, torpedo planes).
* *Rally* behind the base (450 wu away from the enemy), clear of own
  factories (aircraft parked on a pad stop production).
* *Launch* when at least 3 are at the rally, or 2 after a 60 s wait — never
  one by one. Late members wait at the rally until the wing returns.
* *Target*: remembered enemies (mobiles only if seen in the last 5 s, a
  commander in the last 30 s — the attack order follows the unit; not
  aircraft, not anti-air) weighted for a strike: extractors and constructors
  ×3, energy and makers ×1.5, artillery ×2, factories, radar and storage ×1,
  defenses ×½, the commander 8000; bombs skip submarines, torpedoes take only
  ships. The 12 best by value over distance are judged in full: passes
  needed (pass damage: bombs 3 s of their clamped DPS, gunships 6 s) must be
  ≤ 3 (≤ 6 against a commander: it ends the game); gain adds half of buildings within 200 wu (splash); expected damage =
  anti-air at the target × (time inside 600 wu of cover per pass + 3 s per
  pass) + anti-air along the line × 2 s + enemy air-to-air × 6 s when their
  fighters are within 2500 wu and ours are weaker. Expected loss = damage ×
  wing value / wing hit points. Accepted when gain ≥ 1.5 × loss and the
  damage is under half the wing's hit points (unless the target is the
  commander); score = gain / (loss + travel + 50).
* *Route* around remembered anti-air with the sector router (cost 10 + 4 ×
  anti-air dps) when that is 30 % cheaper than the line: up to 3 queued
  moves, then the attack.
* *Sortie*: re-attack while the target lives; when it dies, the next
  acceptable target within 1500 wu; abort when the wing is below 45 % of its
  launch hit points, when anti-air at the target turns out more than twice
  as costly as expected, after 30 s over the area or 90 s of approach; then
  fly back and regroup. Badly hurt members withdraw to the rally.

**Air scouts** fly idle unarmed aircraft to the least recently seen of:
unexplored start positions (×3), enemy target zones (×2) and metal spots
that are not ours away from home (×1), by staleness over distance, avoiding
anti-air above the scout's HP/20. Zones an own unit stood in recently are
fresh.

**Anti-air escort.** With `air` on, enemy aircraft stay "about" for 3
minutes after they were last seen (the host forgets them after one), so the
anti-air escort squad (attention ≥ 4) does not dissolve between raids. Enemy
aircraft no longer create ground incidents: the main army no longer turns
around for bombers it cannot shoot.

### Reach (`reach.go`, with `naval`)

Every squad goal must be reachable for the movement class of the members
it is given to. At Init the army labels each mobile surface unit of its
side with a movement profile (`aikit.MoveClassOf`, footprint capped at 3
cells on land and 4 at sea exactly as the utility economy does, so both
share the map's cached region maps, about a dozen flood fills) and
summarizes each profile's region map (`aikit.MapInfo.Reach`) into a
per-sector table of the (up to two) regions present, each sector filled
the first time it is asked for (filling all of them at Init cost 41 ms per
player on Seven Islands). A think then asks "can these members get within
fire range of that point" with a few table lookups:

* each squad records its members' movement groups (class, region where
  they stand, value, longest range); a target, exploration start, incident
  or naval zone is taken only when groups worth at least half the squad can
  come within their weapon range (+32 wu) of it — a strait narrower than the
  guns' range still counts;
* stage points and the gather point must lie in the squad's main region
  (the gather point in the home walkers' region, falling back to home);
* a member whose standing order leads out of its reach (given before, or
  by a squad of other classes) is called back instead of pressing against
  a shore: members with the squad's body hold there, ships go to the fleet's
  gather point, others to the squad's gather point when they can reach it,
  else home; every order a squad issues is filtered the same way;
* the land route is *cut* when the home region of our most numerous walker
  class does not come within 800 wu of the enemy base: hovercraft and
  amphibious units then form the assault squad;
* ground scouts only visit starts and spots they can reach.

The fleet's "water" is the sectors of the regions our ships sail in (rebuilt
when that set of regions changes; before the first ship, the water metal
spots). Without region tables (no terrain, as in unit tests) every goal is
taken on trust and the land route is never cut. The learned water and reach
learning that stood in for the tables before them never ran once the tables
existed and were deleted (2026-09-24).

### Naval and amphibious forces (`naval.go`)

**Water.** `wdist` is the sector distance to known water (breadth-first,
recomputed when the water changes). A remembered enemy is *coastal* when it
floats or stands within 520 wu of known water; ships fight it from the
nearest known water sector (searched within 6 sectors).

**Fleet** (squad `naval`: ships and submarines) runs the ground state
machine with its own terms:
* gathers on its water within 1500 wu of home water nearest the enemy,
  clear of own factories (ships parked on a shipyard's pad stop it), falling
  back to 600 wu out to sea past the yard; with region tables and the enemy
  base located it gathers forward instead — the water nearest the enemy
  base that is at least 1600 wu (or 35 % of the way) from it with low
  danger — so it holds the sea and does not sail home after each fight;
  next to a naval constructor under threat, to escort it;
* targets only zones' coastal value (`nval`), at their water access point,
  judged against defenders there, the unseen part of the remembered enemy
  fleet (the largest fleet seen fades with a 5-minute time constant; counted fully near
  where it was seen or the enemy base, half elsewhere) and remembered
  hits (`tac_seahurt`: damage rates where our ships were hurt, fading over
  3 minutes); a coast is judged with surface guns only (torpedoes do not
  shell land); the engage margin is 250 ‰ higher (`nm=` shifts it):
  submarines are unseen without sonar and coastal guns outrange a ship's
  sight;
* with nothing coastal known and at least 1800 value it goes looking: to
  the enemy base once buildings have shown where it is, otherwise to the
  nearest start position nobody has looked at lately;
* launches with 2 ships worth 600;
* turns back when *blind*: taking more than 1.5 % of its hit points per
  second (3 % for a fleet worth 5000 winning 4:1 what it sees) and more than
  1.5 × the damage the known enemy around it deals — shot by what it cannot
  see, like torpedo launchers only sonar shows; in a fight only once that is
  costing units (strength below 85 % of what it engaged with);
* counts the enemy commander presumed at work among its factories when that
  base is within its guns;
* answers incidents within reach of known water; walkers no longer answer
  incidents made only of ships away from home.

**Amphibious assault** (squad `amph`): while the land route is cut,
hovercraft and amphibious units leave the walkers and run the main army's
logic with reach everywhere (stages on known land or water).

### Picture (rebuilt every think, `picture.go`)

Sector-resolution integer grids (`aikit.Grid`, 128 wu):

| grid | contents |
|---|---|
| `tac_danger` | enemy ground damage per second reaching each sector: static defenses at range+64, mobiles at range + 3 s of travel, freshness-weighted; radar blips as 40 dps |
| `tac_static` | the static-defense part of danger (routing, stage points, "do not chase into defenses") |
| `tac_aa` | enemy anti-air reach (for the escort/air decisions) |
| `tac_assets` | own buildings and builders (what incidents threaten) |
| `tac_hurt` | damage own units took recently (HP drop since last think, decays ×¾ per think) |
| `tac_known` | sectors any own or freshly seen enemy ground unit has stood in — learned passability; start positions and land metal spots seed it |
| `tac_aamem` | remembered anti-air (with `air`, see above) |
| `tac_water`, `tac_seahurt` | the fleet's water and where ships were hurt (with `naval`) |

Discs are painted from falloff kernels cached per radius (`disc.go`): the
same cell values as `aikit.Grid.AddDisc` (a test checks them) without a
square root per cell, which cut the whole util+tac think by about a third.

**Freshness.** A remembered mobile counts fully for 5 s after it was seen,
then fades linearly to 25 % at the 60 s memory limit (it still exists
somewhere near). Buildings count fully until the host forgets them.

**Target zones.** Remembered enemies are binned into 4×4-sector zones
(512 wu). Each zone's value is weighted by what destroying it is worth:
extractors and constructors ×3, factories ×2, energy/storage ×1.5, radar ×2,
defenses ×½, mobile units ×1, the commander 8000 (its authored cost is
~30 000; 8000 makes a killable commander the best target without swamping
everything else). If the commander is not in memory it is presumed at work in
the zone with the most factory value: that zone gets half its value and half
its fighting strength (of the commander of the side that built its most
valuable factory — our own side in a mirror game). For each zone the defenders are estimated once per
think: static defenses whose range+150 covers the centre, mobiles within
700 wu fully and within 1600 wu at half (they can reinforce), radar blips as
generic units.

**Incidents.** Visible armed enemy mobiles (or blips) near own assets, within
the base radius, or within 800 wu of the own commander are clustered
(700 wu) into at most 4 incidents, ordered by the own value at risk.

### Engagement prediction (`force.go`)

A `force` sums dps, hp, anti-air dps, range×dps and value. Lanchester's square
law makes fighting strength `Σdps × Σhp` (twice the units, four times the
strength). `ratio(own, enemy)` is own/enemy strength in permille after a
range adjustment: the side with the longer dps-weighted range gets up to +25 %
effective damage (1 % per 8 wu). Static defenses are discounted by the share of
the squad's damage that outranges them (a per-squad range histogram; fully
outranged defenses keep 40 % of their fire, the part landing while units walk
in). The expected loss of a win at ratio r is `1 − √(1 − 1/r)` of the squad
(r = 2 → 29 %, r = 4 → 13 %); a fight at r ≤ 1 loses everything.

Own force in a fight includes own static defenses covering the squad and the
own commander within 600 wu (`withSupport`), so a fight at home is judged as
the base plus the army. The enemy commander's manual super-weapon is modelled
as +150 dps.

Margins come from the persona's skill (permille of strength):
engage at `1600 − 4·skill` (hard 1.26, easy 1.52), retreat below
`450 + 4·skill` (hard 0.79, easy 0.53). The gap is the hysteresis; a
retreated-from zone is avoided for 45 s unless the squad has grown by 1.5×
in linear strength, and a squad that has lost 75 % of its strength since the
engagement began pulls out regardless.

Raiders use a fixed engage margin of 3.0 and only accept fights in the field
above the engage margin (they avoid even fights).

### Squads (`army.go`, `squad.go`)

Membership is the unit tag (`Kit.SetTag`): 1 main, 2 raid, 3 defend,
4 escort, 5 fighter, 6 strike, 7 naval, 8 amph; 20 scouts, 21 withdrawn,
22 air scouts. With the switches on, fighters, other combat aircraft and
ships go to their own squads at any attention (they have nothing else to
do); withdrawn aircraft go to the air rally and ships to the fleet's gather
point, and rejoin their own squad there. New units (tag 0, or the tag of a squad
that switched off) are assigned: AA-only units to the escort, the home guard
until it holds its need, fast units (≥ 50 wu/s, not artillery or air) to the
raid squad up to 6 units and 20 % of the army, everything else to main.

`Persona.Attention` caps concurrent squads; optional squads also need army
size (hysteresis on army value):

| squad | attention | on at / off below | role |
|---|---|---|---|
| main | 1 | always | the army; handles everything at attention 1 |
| raid | ≥ 2 | 1500 / 900 | fast units hunting economy with little defense |
| defend | ≥ 3 | 800 / 500, and a remembered threat | home guard sized to the recent threat (decays ~e-fold per minute), ≤ 15 % of the army |
| escort | ≥ 4 | enemy aircraft seen (with `air`: in the last 3 minutes) | AA units patrolling at the main army's centre |
| fighter | any | `air` | intercept, escort strikes, combat air patrol |
| strike | any | `air` | bombers, gunships, torpedo planes |
| naval | any | `naval` | ships and submarines |
| amph | any | `naval` and the land route cut | hovercraft and amphibious units |

Merge/split: a raid squad with no viable target for a minute folds into main
and fast units go to main for two minutes; a waiting main army hands fast
units to an under-strength (< 3) raid squad; a waiting home guard releases
everything above 1.5× its need + 200 to main; withdrawn units rejoin (home
guard first) when home.

**Body.** A squad's centre is its densest point — the member with the most
value within 500 wu, re-centred on those neighbours — searched among members
within 1200 wu of the previous centre while they hold ≥ 30 % of its value, so
reinforcements at home do not move the fighting body. "Present" force is the
members within 650 wu of the centre. A member standing still for 30 s while
ordered somewhere > 400 wu away is **stuck** and left out of the squad (and
its strength) until it moves again — unless it is fighting where it stands
(hurt in the last 5 s, or an enemy within its weapon range).

**State machine.**

* **gather** — wait at the gather point: on the line from home toward the
  enemy army's centroid (or the enemy base), as far forward as 45 % while the
  danger there is ≤ 60 dps and the ground is known. This keeps a waiting army
  between the enemy and the base. Choose a target; launch when the squad is
  ≥ 60 % present (or has waited 40 s) and worth ≥ 300 with ≥ 3 units. With
  nothing known to attack, a main army worth ≥ 600 explores the nearest start
  position nobody has looked at for 3 minutes.
* **approach** — move (routed) to a stage point: walked back from the target
  toward the squad until outside the target's static reach + 300 (≥ 600) with
  low static danger, on known ground. The target is re-evaluated every think;
  a flip below the retreat margin aborts. At the stage, engage when ≥ 75 %
  present or after 20 s.
* **engage** — patrol to the target (patrol fights what it meets). The local
  prediction uses visible enemies within range+400 at current health, fresh
  remembered mobiles at half weight and statics covering the squad (plus the
  target's statics within 700 wu of it). Below the retreat margin → retreat.
  When the target is cleared, chain to the next target within 1500 wu, else
  launch or regroup. Reinforcements far from the body wait at the gather
  point until they are worth 400 (or a quarter of the body), then move to the
  stage point together and join from there.
* **retreat** — move to the gather point (home if already there) and record a
  **repulse**: the enemy force and where it stood, remembered for 90 s
  (fading) and added to any target whose approach corridor or surroundings
  include it.
* **defend** — patrol to the incident.

Contact while gathering or approaching (visible enemy mobiles within
range+350) becomes a field **skirmish** if the prediction is at least the
retreat margin, otherwise a retreat.

**Target choice.** For each candidate zone: enemy = zone mobiles + zone
statics (outrange-discounted) + the **corridor** (enemy mobiles within 700 wu
of the line to the target, topped up to full weight) + repulses. Reject below
the engage margin; otherwise

```
score = gain × 1000 / (expected loss + travel + 60)
expected loss = squad value × (1 − √(1 − 1/r))
travel        = squad value × distance / (slowest speed × 300)   (a unit's worth per 5 min walked)
```

with a 30 % bonus for the current target. Raiders score economy only
(`2·eco + value/4`).

**Defense.** Incidents are answered most valuable first by the home guard
(at home or within 1500 wu), raiders (within 2500 wu and ratio ≥ 1.5), then
the main army (if the incident is at home, near the commander or threatens
≥ 600 of assets and is worth ≥ 200 — a lone raider is left to the base — and
main is not winning a fight far away), accumulating responders until the
responders plus own defenses/commander there reach 1.5.

### Threat-aware routing (`route.go`)

Dijkstra over the sector grid (binary heap on reused buffers, 8-neighbour,
step = mean cell cost × 10 or 14). Cell cost = 10 + 40 × danger / squad dps
(so a strong squad barely detours around what it can crush) + 12 on unknown
ground, capped at 2000. The route is used only when it costs < 80 % of the
straight line's cost; it is compressed to at most 3 turning points on known
ground and issued as one move plus queued moves (1 action each; a direct
move when the budget cannot pay for the route).

### Micro (skill-gated)

* **Withdraw** (skill ≥ 40): members under 30 % hp, worth ≥ 90 and hurt in
  the last 3 s walk home (one group order), then rejoin the home guard. They
  leave the squad at once: no later order of the same think (focus fire,
  patrol, intercept, the next attack run) sends them back.
* **Focus fire** (skill ≥ 60): among visible enemies within the squad's range
  + 150, pick the highest `(10·dps + value) / remaining hp` (enemy commander
  ×8, only when the local ratio ≥ 2); never a mobile standing where static
  danger × 3 exceeds the squad's dps (no chasing into defenses). Members in
  reach get `Attack` + queued `Patrol` back to the target (2 actions), so none
  idles when the victim dies; the target is sticky (×1.5) and switched at
  most every 45 ticks.

### Command discipline

* Per-unit order memory (kind, point, target, tick, retries). An order is sent
  only to members that do not already carry it, as one group command. A
  carried order is re-sent only when it evidently did not take effect after
  the reaction latency (idle and not at the destination), at most twice per
  20 s.
* **Budget mirror.** The policy replays the host's action bucket exactly:
  refill at APM/1800 per tick to the burst at each batch application, minus
  every command that reached the executor (`Last.Applied + Stale + Failed`).
  The army's budget for a think is the projection to this batch's application
  tick, minus what strategy/economy already emitted this think, minus one
  action per factory that production will ask for; retreats, defense and
  focus fire may use that reserve. The army therefore never has its own
  commands dropped (`Kit.Last.DroppedAPM` in its games comes from the
  economy's own over-emission).

### Explain

`SquadViews` (role:state, target kind, route, predicted ratio, present/total,
centre, target, own present strength, predicted enemy strength), `Goals` (the
latest target evaluations with value, ratio and score; rejected ones score
−1), notes (budget, gather point, enemy army estimate, incidents, engagement
statistics, stuck units) and the six grids above.

## Parameters

| name | value | meaning |
|---|---|---|
| engage / retreat margin | 1600−4·skill / 450+4·skill ‰ | square-law strength ratio to attack / to pull out |
| raid margin | 3000 ‰ | raiders need overwhelming local superiority |
| commanderValue / DPS bonus | 8000 / +150 | enemy commander as a target / its super-weapon |
| zone | 4 sectors (512 wu) | target granularity |
| nearMobile / reinforceDist | 700 / 1600 wu | full / half weight of enemy mobiles around a target |
| corridorWidth | 700 wu | enemy mobiles this close to the approach line count |
| repulse memory | 2700 ticks | forces that threw a squad back stay in the way |
| coolTicks | 1350 | a zone that repelled an attack is avoided |
| minLaunchValue / gatheredShare | 300 / 60 % | launch conditions |
| stage wait / retreat wait | 600 / 750 ticks | regroup timeouts |
| presentRadius / clusterRadius / bodyRadius | 650 / 500 / 1200 wu | squad body |
| stuckTicks | 900 | ordered but motionless → left out |
| raid squad | ≥ 50 wu/s, ≤ 6 units, ≤ 20 % army; on 1500 / off 900 | |
| home guard | ≤ 15 % army; on 800 / off 500 | |
| withdraw | skill ≥ 40, < 30 % hp, value ≥ 90 | |
| focus fire | skill ≥ 60, switch ≥ 45 ticks | |
| route | cost 10 + 40·danger/dps + 12 unknown; used if < 80 % of direct; ≤ 3 waypoints | |
| strike wing | ≥ 3 (2 after 60 s); gain ≥ 1.5 × loss; damage < ½ wing HP; ≤ 3 passes | |
| strike sortie | abort < 45 % HP or anti-air ×2 worse; linger 30 s; chain 1500 wu | |
| anti-air memory | fade every 5 s, time constant 5400 ticks; escort persistence 5400 | |
| fighters | skip targets under anti-air > HP/18; retreat margin; CAP re-aim 450 wu | |
| fleet | engage margin + 250 ‰ (`nm=`); coastal within 520 wu; launch 2 ships / 600; explore ≥ 1800 | |
| fleet memory / sea hits | 9000 / 5400 ticks | |
| blind | damage > 1.5 %/s of hit points and > 1.5 × known enemy dps | |
| passages | gather/stage blob out of `InPassage`: radius 27 wu × √members (64–320), gather ≥ 400 wu from home, stage slack 640 wu | `passage=0` off |
| posture hold | main squad: no offensive while the available army < `Posture.AttackValue` (`pv=main`: the main squad's value) | `posture=0` off |
| soft target | predicted ratio ≥ 3000 ‰ (the raid margin) while held | |
| aggression slope | engage margin − slope × (aggression − 50), ≤ ±150 ‰; retreat margin half | `pagg=`, default 0 |
| unseen land army | envelope of the largest enemy land army seen (fire, hit points, anti-air, value), time constant 9000 ticks; its unseen part counts for the fleet's targets and the strike wing's anti-air within 2500 wu of where it was seen or of the enemy base | `unseen=0` off |
| strike calibration | expected loss × realized/expected (1–5), expected gain × realized/expected (⅕–1), prior 400 each side, over the sorties flown this game | `unseen=0` off |

A sweep of the margins (engage −150/+200 ‰, retreat ±150 ‰; 24 games each,
seeds 3-4, ARM mirror vs scripted, on the build before corridors and
repulses) moved nothing beyond noise (14-15 wins each, kill/loss 1.47-1.65),
so the defaults stay.

## Results

### Early harassment (G1, 2026-09-24)

Protocol v2 (docs/MODERN_AI_RESEARCH.md §5): twelve-map land pool, seeds
301–308, map-mixed seeds, random starts, both slot orders, 36000 ticks,
hard. "Off" is `harass=0,probe=0,tour=0`; v1 is the v2-land v1 string plus
the same three switches off. First kill is the median minute of
`first_attack_tick` over every player-game (a game without one counts as its
end); "killed / lost by 8" is the mean value destroyed / lost by minute 8
(series sample at tick 14400); the milestones are medians of the new
counters.

| hard mirror (96 games, 192 player-games) | before (off) | after |
|---|---|---|
| first kill, min (90 % map-cluster interval) | 6.29 [5.61, 7.80] | **5.51** [4.67, 6.59] |
| value killed by minute 8, mean (median) | 151 (92) | **228** (118) |
| first combat unit / first target known, min | 4.37 / 5.00 | 4.38 / **4.11** |
| first launch / first fight, min | 7.30 / 7.14 | **5.84** / **5.77** |
| soft raids per game | 3.3 | 3.9 |
| thinks too small to launch / with no target | 91 / 162 | 57 / 64 |

The first kill came more than half a minute earlier on seven of the
twelve maps (comet catcher 9.14 → 6.76, metal heck 7.82 → 5.72, full moon
5.90 → 4.37, red planet 5.87 → 4.58, great divide 5.65 → 4.42, ashap
plateau 6.72 → 5.71, sherwood 5.75 → 4.92) and at most 0.2 min on the
others (dark side and the pass were already under 4, coast to coast stays
at 12, evad river confluence at 8.5: its first squad member comes at minute
11.7). The rest of the gap to human winners (4.7) is the first combat unit:
the first squad member (scouts excluded) comes at minute 4.4, humans' first
combat unit at 2.9, and that is production's.

Strength (points = (W + D/2) / games, 90 % map-cluster interval):

| pairing | games | W-D-L | points | killed / lost by 8 |
|---|---|---|---|---|
| after vs off | 192 | 63-65-64 | 49.7 % [45.3, 53.9] | 194 / 191 |
| after vs v1 | 192 | 67-63-62 | **51.3 %** [41.1, 62.0] | 325 / 164 |
| water pool, after vs retail (8 maps) | 128 | 123-4-1, 37 commander kills | 97.7 % | 73 / 14 |
| land, before vs retail (seeds 301–304) | 96 | 90-6-0, 66 commander kills | 96.9 % | 181 / 32 |
| land, after vs retail (seeds 301–304) | 96 | 89-6-1, 68 commander kills | 95.8 % | 464 / 41 |

Against the old army the early raids trade evenly (194 killed, 191 lost by
minute 8) and the result is even; against v1 the new army destroys twice
what it loses by minute 8. Against retail on land the first kill moves from
minute 8.41 to 7.17 and the value destroyed by minute 8 from 181 to 464.

Tuning knobs, hard mirrors (first kill, killed by 8, mean / median) and a
head-to-head against the default where measured:

| knob | first kill | killed by 8 | against the default |
|---|---|---|---|
| default (2 units / 200, soft margin 3.0, raid squad at 1500) | 5.51 | 228 / 118 | |
| `sm=2000` | 5.51 | 237 / 150 | |
| `hn=1,hv=100` (first launch 4.66, fight 5.04) | 5.48 | 223 / 153 | |
| `raidv=600` | 5.51 | 342 / 150 | 56-68-68, 46.9 % [44.0, 50.0] |

None moves the first kill, and an early raid squad costs points, so the
defaults stay.

Cost (hard mirrors, sync, `-allocs`, seed 301, shared host): think mean /
p99 µs before 33–52 / 77–116, after 42–53 / 105–135 on red planet and great
divide (a repeated pre-change game varied from 33 to 42 µs); 29–260 B and
under 0.25 objects per think either way, warm-up only. `async=1` reproduced
the synchronous game (identical result JSON) on great divide 301, red
planet 302 and sherwood 303.

### Review fixes (F4, 2026-09-24)

Fixes from the read-only review of `modern-ai-v2`, one commit each:

1. Per-unit memory resets when the slot holds another instance
   (`OwnUnit.Gen`); focus, intercept, strike and order targets carry the
   instance too, and a strike no longer counts a newcomer in a dead
   member's slot among its survivors.
2. Members whose order leads out of their reach are recalled by their own
   squad; the list was reset by `issue` before `recall` read it, so they
   got no order at all.
3. Routes with more than three turns keep the thinned subset, not the first
   three raw turns (the compression reallocated past the squad's array).
4. The presumed enemy commander is the one of the side that built the base
   zone's factories: in an ARM mirror none was presumed (on Great Divide,
   seed 301, the enemy base was predicted at 28000 ‰ instead of 1526 ‰).
5. Anti-air, threat and sea-hit memories and the remembered fleet fade in
   thousandths and reach zero on their time constants (they stopped at 35,
   119 and 35; the fleet faded with 15000 ticks, not 9000).
6. A withdrawal holds for the rest of the think (no patrol, intercept or
   attack run sends the unit back), and a unit fighting in place is not
   "stuck".
7. No panic when every member is worth nothing; core spot claims can name
   their builder (`ClaimSpot`) and end when it dies; unused `core.Task`
   fields and the learned-reach fallback (dead once reach tables exist) are
   deleted.
8. Remembered armed ground units are indexed by zone once per think for
   the per-zone defender and corridor questions (same sums; 1.3× / 2.0× /
   2.9× faster at 100 / 300 / 600 remembered units in the benchmark,
   unchanged think time at arena sizes).
9. Reach-table sectors are filled on first ask: Init 41 → 0.1 ms per player
   on Seven Islands and 14 → 0.1 ms on Two Continents (plus any region
   flood fills the economy did not already request, up to 6.5 ms). A game
   asks for about a tenth of the class × sector entries (Seven Islands
   27 000 of 307 000, 5–10 ms; Two Continents 11 500 of 101 000, 2–3 ms),
   mostly in the one think in which the first ship makes the fleet's water
   scan a naval class's whole table (think max about 5 ms on Seven Islands,
   was 2 ms; mean and p99 unchanged). Passage verdicts cost under 0.1 ms
   per game.

util+tac:hard against v1 (the switches-off brain), 36000 ticks, both
orders, seeds 301–302 (mirror: seed 301, both sides ARM):

| pool | games | before W-D-L | pts | com kills / lost | after W-D-L | pts | com kills / lost |
|---|---|---|---|---|---|---|---|
| land | 48 | 16-18-14 | 52.1 | 10 / 8 | 19-11-18 | 51.0 | 11 / 7 |
| water | 32 | 22-10-0 | 84.4 | 7 / 0 | 19-13-0 | 79.7 | 7 / 0 |
| ARM mirror | 16 | 4-5-7 | 40.6 | 1 / 6 | 4-2-10 | 31.2 | 1 / 7 |

Both brains share this army, so both changed; the differences are within
the noise of these samples (about ±9 points at 32 games, ±12 at 16).
Items 8 and 9 give identical games (40 of 40 against the build before
each), and `async=1` stays identical to sync (12 of 12).

### Passages (T5, 2026-09-24, `passage.go`)

A squad waiting in a ramp closes it. With `passage` on (default; `passage=0`
restores the old points, identical result JSON to the pre-change binary on
all twelve land maps) the main gather point, the home guard's point and the
ground squads' stage points keep the gathered squad out of terrain passages
by the layout's rule (`MapInfo.InPassage`), for the squad's land classes,
over a blob of 27 wu per √member (64–320 wu): the gather point waits on
the home side of the first passage on its line, shrinking the blob before
falling back; a stage point steps back out of a passage unless that costs
more than 640 wu (a long pass cannot be stepped around). Fleet, air and
amphibious squads are unchanged. Hard mirrors, 40 minutes, seeds 141–152
(24 player-games per row); "waiting in a passage" is the share of thinks
the main squad waited gathered with its centre or half-radius ring in a
passage by the rule (`tac_wait_passage` / `tac_wait_thinks`, measured with
the rule off too):

| map | rule | trapped_max worst / mean | trapped_mean | waiting in a passage |
|---|---|---|---|---|
| The Pass | off | 4 / 0.38 | 0.023 | 70 % |
| The Pass | on | **1 / 0.17** | 0.001 | **9 %** |
| Ashap Plateau | off | 3 / 0.50 | 0.007 | 10 % |
| Ashap Plateau | on | **2 / 0.21** | 0.004 | **4 %** |

(On The Pass the rule marks the whole middle of the map a passage, so the
army now waits at the plateau's edge instead of out in the pass.) Land
pool head to head, seeds 161–164, 96 games: on vs `passage=0` 37-31-28,
**54.7 %** of points. Hard mirrors on each land map, seed 165: painted
desert gives the identical game; the other eleven differ because a big
army's blob (or a stage point) meets some passage there at times. Think
cost (hard mirror, sync, `-allocs`, seed 161): red planet 30.1 / 85 µs and
40.3 / 119 µs (mean / p99) on, 29.7 / 111 and 40.2 / 114 off; The Pass
10.7 / 29 and 11.4 / 31 on, 14.0 / 33 and 14.7 / 36 off; 49–693 B per
think either way. Verdicts are cached per class on a 64 wu grid. `async=1`
reproduces the synchronous game (The Pass 161).

### Attack value calibration (T4, 2026-09-24)

With the posture honored, the utility strategy's old attack value (`att_min`
+ `att_grow` per minute, scaled by ambition to 67 % and 61 % at easy) held
hard's first offensive to minute 12–15 and easy's and medium's mostly past
the game. The strategy (`utility/style.go`, `strategy.go`) now publishes:

* **Launch value**: 45 % of the persona's army tier curve — `topArmy` (the
  top human tier's army, −814 + 239/min) scaled by ambition, the same
  curve the ambition caps hold the army to, so every persona's own army
  can reach it — at least 300 (this army's smallest launch), at least
  `att_ratio` % of the enemy estimate as before, jittered per wave.
* **Style lag**: each style reads the curve later or earlier by its human
  archetype's first-combat-unit minute less the corpus median (2.96 min;
  human benchmark sections 3 and 4): expand (O1) 3.68 → +43 s, eco (O2)
  3.17 → +12 s, units (O3) 1.90 → −64 s, tower (O4) 3.56 → +36 s, twofac
  (O5) 3.12 → +9 s, greedy (O6) 5.05 → +63 s (half strength, like its
  other perturbations). The benchmark has no per-archetype first kill or
  army value at minute 10; the lag is the only per-archetype timing used.
* **Production unchanged**: production doubles the army share while the
  army is below the posture's attack value, so a lower launch value alone
  also built a smaller army (at minute 10 about a quarter less at hard; a
  first version without the correction scored 50.0 % against the
  pre-posture brain, with medium at 89.6 % against retail). Production
  therefore pushes toward its own target, which the strategy publishes in
  its shared state: the larger of the attack value and the tuned build
  value (`att_min` + `att_grow` per minute, the old attack value), so the
  army is built as before and only the launch moves. (The first landed
  version scaled `w_army` by a copy of production's army factor instead;
  the shared target replaced it.)
* **Switches**: `att_curve=0` restores the old attack value exactly
  (checked: identical result JSON to the pre-change binary on great
  divide 131, hard against medium); `att_share=<percent>`,
  `att_floor=<value>`, `att_lag=0`, `att_build=0` (no production
  correction) for tuning.
* **Arena**: `players[].first_attack_tick` is now written — the tick the
  player first destroyed a finished unit of another player (credited as
  in `value_killed`), the human benchmark's "first kill" (humans: minute
  4.45 top tier, 4.74 winners, 5.12 low tier).

Before = this branch after the T3 merge (the posture honored, the old
attack value); after = the calibration. 36000-tick games, both slot
orders, hard unless stated, twelve-map land pool. "First offensive" is the
median minute of `tac_off_first_tick` over the games with one (in brackets
the games without), "soft first" the median minute of the first soft raid,
"wave" the main squad's mean value at the start of an offensive, "first
kill" the median `first_attack_tick` (not recorded before).

**Strength gate** (hard against the pre-posture brain
`util+tac:hard::posture=0,att_curve=0`):

| pairing | seeds | games | W-D-L | points |
|---|---|---|---|---|
| after vs pre-posture | 131–136 | 144 | 52-41-51 | **50.3 %** |
| same, share 55 % | 131–134 | 96 | 38-22-36 | 51.0 % |
| before (T3) vs pre-posture | 131–134 | 96 | 29-35-32 | 48.4 % |
| first version, share 55 %, no production correction | 131–134 | 96 | 33-30-33 | 50.0 % |

**Persona ladder** (seeds 121–122, 48 games each; easy also 125–126):

| pairing | before | after |
|---|---|---|
| easy vs retail (96 games) | 51-19-26, 63.0 % | 55-20-21, **67.7 %** |
| medium vs retail | 40-7-1, 90.6 % | 41-7-0, **92.7 %** |
| hard vs retail | 47-1-0, 99.0 % | 48-0-0, **100 %** |
| medium vs hard | 2-8-38, 12.5 % | 2-10-36, 14.6 % |

| persona (games) | first offensive | soft first | wave | first kill | available army at 10 min |
|---|---|---|---|---|---|
| easy vs retail (96) | 18.8 [91] → **9.1 [15]** | 9.2 → 14.7 | 2115 (5 games) → **744** | 9.2 | 502 → 502 |
| medium vs retail and hard (96) | 17.1 [71] → **8.1 [18]** | 8.2 → 9.3 | 3000 → **1060** | 8.0 | 750 → 664 |
| hard vs retail (48) | 12.8 [27] → **8.1 [4]** | 7.6 → 8.1 | 3045 → **1381** | 8.8 | 1133 → 1181 |
| hard vs medium (48) | 12.6 [24] → **7.7 [7]** | 7.1 → 7.0 | 2735 → **1459** | 6.3 | 1086 → 979 |
| hard vs pre-posture (144) | — → **7.7–8.1** | 7.9 | 1561–1743 | 6.1–6.9 | |

Every persona now launches full offensives in most games, with waves in
ambition order (easy 744, medium 1060, hard 1381–1459); hard's first
comes at minute 7.7–8.1, where the pre-posture army's first launch was
(6.8–7.2, worth 440–660). Soft raids come later or not at all once full
offensives are allowed (hard 3.1 → 0.6 per game against retail).

**Styles** (hard, `style=<name>,jitter=0`, seed 127, 24 games each against
`util+tac:hard::posture=0,att_curve=0,style=balanced,jitter=0`):

| style | first offensive, before → after | soft first after | wave, before → after | points, before → after |
|---|---|---|---|---|
| units (O3) | 13.5 [8] → **6.3 [1]** | 11.7 | 3011 → 1668 | 60.4 → 60.4 % |
| twofac (O5) | 13.4 [13] → 7.6 [3] | 8.6 | 2790 → 1281 | 43.8 → 37.5 % |
| balanced | 13.4 [12] → 8.2 [0] | 7.2 | 3090 → 1459 | 56.2 → 54.2 % |
| eco (O2) | 13.1 [10] → 8.2 [4] | 7.6 | 2982 → 1597 | 47.9 → 47.9 % |
| expand (O1) | 12.6 [8] → 8.3 [4] | 6.9 | 3180 → 1673 | 52.1 → 47.9 % |
| tower (O4) | 14.8 [10] → 9.6 [2] | 6.7 | 2985 → 1858 | 52.1 → 47.9 % |
| greedy (O6) | 14.2 [12] → **10.4 [2]** | 8.1 | 2790 → 1531 | 47.9 → 47.9 % |
| all seven (168) | | | | 58-57-53, 51.5 % → 57-51-60, 49.1 % |

Units attacks first and greedy last, 4.1 minutes apart (before: 2.2
minutes, in no archetype order). Eco lands with balanced: its archetype
fields units only 0.2 min after the corpus median, so the data do not
place it late; expand and tower, 0.6–0.7 min later than the corpus, land
later than eco.

**Water pool** (8 maps, seeds 121–122, 32 games against retail): after
32-0-0 with 12 commander kills and kill/loss 5.90; before 32-0-0 with 10
and 6.06.

**Cost** (red planet 121, hard mirror, sync, `-allocs`): think mean / p99
27.2 / 86 µs and 25.1 / 69 µs after, 26.9 / 88 µs and 31.7 / 90 µs before;
170 and 629 B per think (0.04 and 0.10 objects), before 157 and 1563 B.
`async=1` gave the same game as sync (great divide 131, hard against
medium, identical result JSON).

### Attack posture (T3, 2026-09-23)

`util+tac` with the posture honored against the same brain with
`posture=0` ("base", or a `0` suffix), 36000-tick games on the twelve-map
land pool unless stated, both slot orders, hard unless stated, `-jobs 3` on
a shared machine. W-D-L is from the first-named side; points = (W + D/2) /
games. "First offensive" is the median minute of `tac_off_first_tick` over
the games that had one (in brackets the games without); "first outing" is
the earlier of the first offensive and the first soft raid.

**Strength.** Unchanged within noise.

| pairing | seeds | games | W-D-L | points | decisive won / lost |
|---|---|---|---|---|---|
| on vs base | 123–124 | 48 | 17-14-17 | **50.0 %** | 7 / 11 |
| on, main squad measured (`pv=main`) vs base | 121–122 | 48 | 16-17-15 | 51.0 % | 8 / 10 |
| on, whole `Board.ArmyValue` measured (first build) vs base | 121–122 | 48 | 16-17-15 | 51.0 % | 7 / 9 |
| `pagg=4` vs base | 121–122 | 48 | 16-18-14 | 52.1 % | 9 / 10 |
| `pagg=4` vs base | 123–124 | 48 | 17-14-17 | 50.0 % | 9 / 11 |
| seven styles, jitter off, vs balanced `posture=0` | 127 | 168 | 63-34-71 (before 60-40-68) | 47.6 % (before 47.6 %) | |
| `plan+tac` on vs off | 125–126 | 48 | 20-13-15 | 55.2 % | |

Across the five 48-game posture runs the held army won more games on
points (42 won, 27 lost) and lost its commander somewhat more often (40
commander kills, 51 commander losses) — within noise at this sample.

**When and how big (hard, on vs base, seeds 123–124).** The first outing
does not move (7.0 → 7.1 min): the first target the army takes is usually
undefended, and it now counts as a soft raid (8.3 per game, from minute
7.0). The full offensive moves from minute 7.0 (4 of 96 games without; the
main squad worth 499 at the first, 1390 on average) to minute 14.8 (21 of
48 without; 2354 at the first, 2715 on average). The attack value is well
above the army until late: attack value / available army 1519 / 240 at
minute 5, 2392 / 1205 at 10, 3172 / 2744 at 15.

**Persona ladder** (seeds 121–122, 48 games per pairing; before =
`posture=0` on both sides):

| pairing | before | after |
|---|---|---|
| easy vs retail | 27-7-14, 63.5 % | 29-9-10, 69.8 % |
| medium vs retail | 43-4-1, 93.8 % | 43-5-0, 94.8 % |
| hard vs retail | 48-0-0, 100 % | 48-0-0, 100 % |
| easy vs hard | 1-2-45, 4.2 % | 0-4-44, 4.2 % |
| medium vs hard | 2-7-39, 11.5 % | 4-7-37, 15.6 % |
| easy vs retail, seeds 125–126 | 25-8-15, 60.4 % | 23-10-15, 58.3 % |

| persona (ladder games) | first offensive, before → after | mean offensive value | soft raids / game | first outing | attack value / available army at 10 min, 15 min |
|---|---|---|---|---|---|
| easy (96) | 8.7 [14] → 16.5 [89] | 706 → 2667 | 4.2 | 8.7 → 8.8 | 1456 / 444, 1916 / 850 |
| medium (96) | 7.9 [7] → 16.5 [65] | 898 → 3051 | 4.5 | 7.9 → 8.3 | 1978 / 744, 2700 / 1926 |
| hard vs retail (48) | 7.2 [3] → 12.3 [20] | 1301 → 3096 | 2.1 | 7.2 → 7.8 | 2231 / 990, 3050 / 4285 |
| hard vs easy and medium (96) | 6.8 [5] → 11.6 [40] | 982 → 2738 | 6.0 | 6.8 → 6.9 | 2300 / 1297, 2958 / 3394 |

Ambition now shows in the offensive: hard launches its first at minute
11.6–12.3, medium and easy at 16.5 and only in 31 of 96 and 7 of 96 games.
Easy never catches its attack value: the strategy scales `att_min` and
`att_grow` to 67 % and 61 % at ambition 35 while the ambition caps hold its
army to 35 % of the top tier's, so the attack value stays about three times
the army it can field (1456 against 444 at minute 10). Easy therefore plays
as a defender that raids undefended targets; it is not weaker for it (64.1 %
of points against retail over the 96 games after, 62.0 % before).

**Styles** (hard, `style=<name>,jitter=0`, seed 127, 24 games per style
against `util+tac:hard::posture=0,style=balanced,jitter=0`):

| style | first offensive, before → after | mean offensive value, before → after | soft raids / game | first outing | points, before → after |
|---|---|---|---|---|---|
| balanced | 6.4 [1] → 12.8 [10] | 1600 → 3391 | 8.5 | 6.4 → 6.4 | 50.0 → 41.7 % |
| expand | 6.5 [2] → 12.6 [12] | 1408 → 3428 | 8.8 | 6.5 → 6.5 | 45.8 → 41.7 % |
| eco | 6.9 [2] → 12.4 [6] | 1754 → 3479 | 5.5 | 6.9 → 6.9 | 47.9 → 58.3 % |
| units | 6.1 [1] → 13.0 [9] | 1454 → 3270 | 10.6 | 6.1 → 6.2 | 56.2 → 54.2 % |
| tower | 6.6 [0] → 12.9 [10] | 1505 → 2957 | 7.1 | 6.6 → 6.6 | 47.9 → 47.9 % |
| twofac | 6.4 [4] → 12.4 [11] | 1194 → 2890 | 6.3 | 6.4 → 6.3 | 41.7 → 52.1 % |
| greedy | 7.6 [2] → 13.3 [10] | 1449 → 3420 | 6.2 | 7.6 → 7.8 | 43.8 → 37.5 % |

The styles still barely differ in offensive timing (12.4–13.3 min, spread
0.9 min; before 6.1–7.6): no style perturbs the attack parameters, so every
style waits for the same attack-value curve and their armies grow at
similar rates. They differ in what they do before it: units soft-raids
twice as often as eco (10.6 against 5.5 per game).

**Water pool** (hundred isles, sail away, shore to shore, lake shore, ring
atoll, pillopeens, brain coral, canal crossing; seeds 121–122, 32 games
each): on vs retail 32-0-0 with 7 commander kills and kill/loss 5.82; base
vs retail 32-0-0 with 7 and 5.36. The main squad holds little there (its
median value is 0 at minute 10 and about 1000 at 15); the fleet, air and
amphibious squads, which the hold leaves alone, carry the attack.

**Calibration probes** (strategy parameters passed in the player spec; the
strategy itself is unchanged):

| probe | seeds | games | W-D-L | points | first offensive | mean offensive value |
|---|---|---|---|---|---|---|
| hard, `att_min=400,att_grow=100`, vs base | 125–126 | 48 | 21-13-14 | 57.3 % | 11.1 [10] | 2202 |
| same | 127–128 | 48 | 16-18-14 | 52.1 % | 10.8 [11] | 1941 |
| easy, `att_min=418,att_grow=86` (att × ambition after the strategy's own scaling), vs retail | 125–126 | 48 | 23-12-13 | 60.4 % | 15.8 [19] | 1398 |

A lower attack value at hard gives earlier and more frequent offensives
(2.3–2.5 per game against 1.0) at 54.7 % of points over 96 games; scaled
with ambition like the army caps, easy launches an offensive in 29 of 48
games, later and smaller than hard's (minute 15.8, value about 1400).

**Cost** (red planet seed 121, hard mirror, sync, `-allocs`): think mean /
p99 28.6 / 80 µs and 28.7 / 78 µs with the posture, 30.7 / 83 µs and 27.5 /
76 µs without; 159 and 658 B (0.15 and 0.04 objects) per think averaged
over the game against 813 and 704 B without — warm-up only. `async=1` gave
the same game as sync (great divide 121, hard against medium: identical
result JSON including the new counters).

### Reach and conversion, with the economy's water production (F2)

After merging `modern-ai-v2` (the economy now builds shipyards, naval
constructors, about 17 ships per water game, air plants, bombers,
fighters, an air scout, hovercraft plants and tech 2), plain `util+tac`
plays against `retail` and against the old brain `v1` =
`util+tac:hard::naval=0,air=0,tech=0,layout=0,style=balanced,jitter=0`,
the integration specs, seeds 111-112, both seats, 36000 ticks.

| pool | opponent | games | W-D-L | points | commander kills | value killed/lost |
|---|---|---|---|---|---|---|
| water (8 maps) | retail | 32 | **30-2-0** | 97 % | **11** (111: 5, 112: 6) | 4.65 |
| water | v1 | 32 | 23-8-1 | 84 % | 10 (111: 4, 112: 6) | 4.11 |
| land (12 maps, seed 112) | retail | 24 | 24-0-0 | 100 % | 18 | 11.5 |
| land | v1 | 24 | 8-9-7 | 52 % | 10 | 0.76 |

Integration baseline before this work (seed 111 water, 112 land): water vs
retail 16-0-0 with 0 commander kills, vs v1 11-4-1 with 5; land vs retail
24-0-0 (19), vs v1 8-8-8. The one water loss against v1 (lake shore 112,
seat 1) is on points; the build before the last gather change won that
game.

Coast to coast (seed 112), trapped units (arena: never left 320 wu of
where they appeared while their order points further) max / mean:

| seat, opponent | before | after |
|---|---|---|
| 1 vs v1 | 20 / 3.75 | 2 / 0.61 |
| 0 vs v1 | 5 / 1.04 | 4 / 0.41 |
| 0 vs retail | 7 / 0.87 | 0 / 0 (and a commander kill) |
| 1 vs retail | 10 / 1.34 | 6 / 1.73 |

The walkers now hold at home on coast to coast (the enemy is across the
sea); the remaining trapped units are ships that never left a yard walled
in by a field of 33 tidal generators, which the region maps (terrain only)
do not see.

Where commanders still survive: shore to shore (the economy builds only
walkers there — 71 of them hold at home, nothing can cross), canal
crossing (the hovercraft assault crosses and fights but has not broken a
base in 20 minutes), pillopeens against v1 (the fleet is almost all patrol
boats, which trade evenly with a shore army of about 150 ground units
around a commander that stays inland), and sail away/ring atoll against
retail (the fleet reaches the base at minute 17-19 and meets torpedo
launchers only its sonar ships see). Conversion there needs the economy to
field a stronger naval mix, hovercraft or air on those maps.

Cost (hard, sync, `-allocs`, whole brain including the new economy): think
mean / p99 39.5 / 109 µs on great divide (v1 35.4 / 101), 58.2 / 174 µs on
coast to coast (v1 55.7), 95.3 / 354 µs on pillopeens (v1 61.6) — the army
is about half of that (37 µs on pillopeens), the rest the economy.
Allocation 59-345 B per think averaged over a game (v1 486-804 B), warm-up
only. `async=1` gave identical games on pillopeens 111 (vs retail) and
canal crossing 112 (vs v1). With `naval=0,air=0` the army is identical to
the merge base.

### Air and naval forces (2026-09-23)

`util+tac` never builds an air plant or a shipyard yet (another work unit
is changing its economy), so these games use the arena brain `forces`
(`cmd/ai-arena/brain_forcestest.go`): util+tac plus test-only overrides
that build N air plants (`force_air=N`, from minute 3, two bombers per
fighter and a scout first), N shipyards (`force_naval=N`, from minute 2,
boats, destroyers and submarines in turn) or N hovercraft plants
(`force_hover=N`, from minute 5). Both sides of a "new vs base" pairing run
the same forced production; `base` is the same brain with
`air=0,naval=0`, i.e. the army as it was. All games 36000 ticks, hard,
seeds 71-74 (71-72 against retail on land), both slot orders, `-jobs 3` on
a loaded shared machine. Per-class value comes from a small uncommitted
arena patch (see *Framework findings* 10); "their loss / our loss" is the
opponent's lost value of a class against ours.

| pairing | pool | games | W-D-L | points | value killed/lost |
|---|---|---|---|---|---|
| util+tac new vs old | land | 48 | 20-8-20 | 50 % | identical games |
| util+tac new vs retail | land | 24 | 24-0-0 | 100 % | 18.9 |
| util+tac old vs retail | land | 24 | 23-1-0 | 98 % | 17.3 |
| forces new vs old, 2 air plants | land | 48 | 24-4-20 | **54 %** | 1.02 |
| forces new vs old, 2 air plants + 2 yards | water | 48 | 13-24-11 | 52 % | 0.83 (1.11 without one commander) |
| forces new vs retail, same | water | 48 | **40-6-2**, 8 decisive | **90 %** | 3.20 |
| forces old vs retail, same | water | 48 | 36-10-2, 11 decisive | 85 % | 5.38 |
| forces new vs old, 2 hovercraft plants | water-separated | 32 | 6-22-4 | 53 % | 1.31 |

By class (their loss / our loss):

| pairing | air | naval | ground (incl. hover) | economy | factories |
|---|---|---|---|---|---|
| new vs old, land + air | **36708 / 26723** | — | 79915 / 84470 | **46931 / 33175** | 46459 / 41760 |
| new vs old, water | 13997 / 13534 | **53412 / 48452** | 12475 / 8798 | **10288 / 5209** | 3441 / 6296 |
| new vs retail, water | 655 / 7454 | 11136 / 40297 | 6713 / 3972 | 23238 / 2084 | 29831 / 2082 |
| old vs retail, water | 658 / 13551 | 17524 / 48848 | 8268 / 6243 | 23857 / 464 | 37283 / 0 |
| new vs old, hovercraft | — | — | 16842 / 13637 | 3843 / 2775 | 1959 / 618 |

Reading the numbers:

* **Land, no aircraft**: util+tac never builds any, and on dry maps reach
  learning is off, so the new army plays exactly the old games (the 20-8-20
  is the slot advantage of each map, mirrored). No regression is possible
  there; against retail it is 24-0-0 (retail builds aircraft, so the games
  differ).
* **Land with air plants**: strikes (116 launched, 117 targets destroyed, 28
  aborted) and fighters (339 intercepts) win the air war and the economy
  exchange; great divide 6-1-1, the other maps near even (their slot
  advantage; commander kills decide most land games). The build before the
  last air fixes scored 24-6-18 (56 %) on the same games.
* **Water against the old army**: the old army's ships sit in the main
  army near home, which is a strong defensive posture; the new fleet trades
  positively only since it counts the unseen enemy fleet and remembered hits
  (earlier versions attacked coasts into the enemy's whole fleet and lost
  18308 : 12119). Most games are draws: the test economy still spends most
  of its metal on walkers that cannot reach anything. The one commander
  loss is lake shore seed 71 in seat 0, which the old army also loses in
  that seat (35490 vs 31729).
* **Water against retail**: 90 % of points against the old army's 85 %;
  the new fleet kills fewer commanders (8 vs 11: it will not sail into
  unseen coastal guns). Its two losses: ring atoll in the CORE seat, where
  the test economy never got a shipyard up and a dozen bombers found little
  worth their losses, and lake shore seed 74, where retail's ground army
  took a base whose economy stayed small. Both armies lose ships to
  retail's floating defenses and torpedo launchers, which are unseen until
  in range. Two fixes found here: fighters escorted strikes into the
  target's anti-air even with no enemy fighters about (air losses 12538 →
  7454 once removed), and the splash bonus counted buildings a pass would
  not kill (38-6-4 → 40-6-2).
* **Hovercraft**: once walkers are found cut off, hovercraft cross and
  fight (sail away 2-6-0, pillopeens 1-7-0).

Development history on the water pool (new vs old, 24-48 games each):
48 % → 44 % (fleet losses 17300 : 5461 against) → 54 % after capping strike
losses and fixing strike launch/abort flapping → 47 % with 4 seeds → the
fleet memory and sea hits → 52-53 %. A more cautious fleet (`nm=600`)
scored the same as the default within noise (53 % vs 52 %).

Cost (hard, sync, same machine and time; the machine was shared and
loaded, so compare within a row):

| game | think mean / p99 µs | allocation per think |
|---|---|---|
| util+tac old, great divide vs retail | 34.4 / 118 | 90.6 B, 0.060 objects |
| util+tac new, great divide vs retail | **25.7 / 84** | 76.3 B, 0.030 objects |
| forces new, sail away vs retail (2 + 2 plants) | 60.4 / 187 | 189.6 B, 0.041 objects |
| same-run medians, land (new vs old, identical games) | 38.8 vs 35.8 | |
| same-run medians, land with air plants | 40.7 vs 32.4 | |
| same-run medians, water with fleets and air | 43.8 vs 35.4 | |

Allocation is warm-up only (per-handle memory, disc kernels); steady-state
thinks allocate nothing. `async=1` produced identical games (scores,
commands, build lists, time series, army counters) to sync on sail away
seed 72 (forces vs retail) and hundred isles seed 73 (forces vs forces).

All games 36000 ticks (20 min), hard personas unless stated, `-jobs 3` on a
shared 12-core machine. W-D-L is from tactics' side.

### Prescribed dev tournament (default sides)

`/tmp/ai-tactics-dev`, spec `tactics:hard` vs `retail` and vs
`scripted:hard`, 6 maps × seeds 1-2 × both slot orders:

```
pairing (A vs B)          W-D-L (A)  decisive  kill/loss A  metal/s A:B  army A:B  min
retail vs tactics/hard     11-1-12      12        0.89        15:20     1087:1116  16.8
scripted/hard vs tactics   12-0-12      11        0.12        21:22     4058:1364  17.8
```

Split by tactics' slot (slot 0 = ARM, slot 1 = CORE):

| tactics | vs | W-D-L | decisive | kill/loss | value killed / lost |
|---|---|---|---|---|---|
| ARM | retail (CORE) | 12-0-0 | 10 | 14.4 | 21931 / 2001 |
| CORE | retail (ARM) | 0-1-11 | 0 | 0.25 | 403 / 5798 |
| ARM | scripted (CORE) | 12-0-0 | 10 | 16.3 | 23349 / 2320 |
| CORE | scripted (ARM) | 0-0-12 | 0 | 0.35 | 146 / 2775 |

References with the scripted army in the same seats: scripted ARM vs retail
CORE 11-1-0 with 4 decisive, kill/loss 4.8 (the 11-2-11 reference);
scripted ARM vs scripted CORE 12-0-0 with 1 decisive, kill/loss 3.2.
The CORE rows are the scripted baseline's games exactly (all 12 CORE games
against retail have the same end tick and scores as the scripted reference): on CORE the
scripted economy/production never builds a combat unit (see *Framework
findings*), so there is no army to command and the army layer cannot change
the outcome. **The prescribed scoreline cannot separate army layers on this
pool; the ARM rows and the mirror below can.**

### ARM mirror (both sides ARM, positions swapped)

Both players field an army, so this is the army-layer comparison.
6 maps × seeds 1-4 × both slot orders = 48 games per pairing.

| pairing | W-D-L | decisive | kill/loss | value killed / lost |
|---|---|---|---|---|
| tactics:hard vs scripted:hard | **30-18-0** | 7 | 1.77 | 13664 / 7597 |
| tactics:hard vs retail | **46-2-0** | 37 | 16.4 | 27440 / 3094 |
| scripted:hard vs retail (reference) | 46-2-0 | 7 | 1.68 | |

By map vs scripted: great divide 8-0-0, full moon 7-1-0, sherwood 7-1-0,
red planet 6-2-0, the pass 2-6-0, dark side 0-8-0 (both commanders die at
tick 8430 in every hard-vs-hard dark side game, before either army exists).
The remaining draws are mostly the weaker start position of a map (the pass
and red planet slot 0, where tactics' economy trails by minutes); tactics
still out-trades there.

### Ablations (ARM mirror vs scripted:hard, seeds 1-2, 24 games each)

| variant | W-D-L | decisive | kill/loss | mean score margin |
|---|---|---|---|---|
| full | 14-10-0 | 4 | 1.64 | 15622 |
| `micro=0` | **10-14-0** | 2 | **1.21** | **8103** |
| `raid=0` | 12-12-0 | 5 | 1.31 | 15028 |
| `route=0` | 12-12-0 | 5 | 1.49 | 16353 |
| `defend=0` | 16-8-0 | 3 | 1.76 | 13246 |
| `attention=1` | 15-9-0 | 5 | 1.45 | 15596 |
| `budget=0` | 15-9-0 | 3 | 1.62 | 13998 |

(4 of each 24 are the dark-side draws.) Micro — focus fire and withdrawal — is
the one component whose removal is clearly visible at this sample size; the
others are within noise (±2 wins). Routing rarely triggers on these maps
(the danger map seldom makes a detour 20 % cheaper), and the hard budget is
rarely binding (see the easy persona for where it is).

### Persona (ARM mirror, seeds 1-2, 24 games each)

| pairing | W-D-L | decisive | kill/loss | apm-drop / game |
|---|---|---|---|---|
| tactics:hard vs scripted:hard | 14-10-0 | 4 | 1.64 | 65 (vs easy) |
| tactics:medium vs scripted:hard | 12-9-3 | 0 | 1.15 | 239 |
| tactics:easy vs scripted:hard | 7-15-2 | 0 | 1.12 | 402 |
| tactics:hard vs tactics:easy | 14-8-2 | 3 | — | |

Decomposing easy on top of hard (vs scripted:hard, seeds 1-2; run on the
build before corridors and repulses, whose plain hard row was 15-9-0,
kill/loss 1.99):

| hard persona except | W-D-L | kill/loss |
|---|---|---|
| think 45, react 45 (easy timing) | 16-8-0 | 1.37 |
| attention 1, skill 20 (easy judgement) | 6-15-3 | 0.94 |
| APM 25 (easy hands) | 6-12-6 | 1.00 |

Timing alone costs trade efficiency but not results; losing the micro and the
tighter margins, or the action budget, costs most of the edge. At APM 25 the
scripted economy's command spam eats most of the budget (economy and
production are starved as well — the whole brain is weaker, not only the
army). Easy still behaves coherently in traces: one squad gathers, approaches
through a stage point, fights, retreats on a flipped prediction (retreat
margin 0.53) and explores; it simply acts late and seldom.

### Cost

Sync hard persona, `-allocs`, per think of the whole brain (scripted
economy + production + tactics army):

| map | think mean / p99 / max µs | host step mean µs | alloc B / objects per think |
|---|---|---|---|
| great divide | 41.9 / 133 / 359 | 7.0 | 32.6 / 0.018 |
| red planet | 49.0 / 137 / 7979* | 7.5 | 44.1 / 0.076 |
| sherwood | 39.5 / 86 / 228 | 13.1 | 33.3 / 0.019 |
| great divide, easy | 37.1 / 93 / 101 | 3.3 | 63.0 / 0.054 |

(scripted brain on the same games: 11-15 µs mean, 80-100 B/think.)
\*Max values include scheduler noise from other tournaments on the host.
Allocation is slice warm-up (per-handle unit memory, member lists); the
steady state allocates nothing. Route searches run only on launch.
`async=1` produced identical result series, command outcomes and build lists
to sync on red planet seed 2 (final build) and great divide seed 1 (an
earlier build).

### Replay trace

`/tmp/ai-tactics-trace.json` (`tactics:hard` vs `retail`, great divide seed 1,
decisive at 26910). Main squad: gathers at the forward gather point, launches
at ratio 1.28 via 2 waypoints, re-evaluates during the approach and retreats
when a defended zone's prediction drops to 0.44, re-launches at a softer
zone, retreats again at 0.72, then engages the base area (ratio 1.7-8.4) with
40 focus orders and kills the commander in the field. Engagement statistics
at the end: launched 4, engaged 3, retreated 2 in fight / 4 before, withdrew
2 units, 3 members stuck behind buildings.

## Strengths

* Air: strikes wait for a full wing, pick undefended economy, route around
  remembered anti-air and never fly a sortie expected to lose half the wing;
  fighters intercept raids over everything we own and escort strikes.
* Naval: the fleet only engages what it can hit from known water, keeps
  clear of our yards, remembers the enemy fleet and where it was shot, and
  turns back when shot by what it cannot see.
* Water-separated maps: walkers learn they are cut off; hovercraft and
  amphibious units take the fight across.

* Does not feed units into defended zones: kill/loss 14-16 : 1 against the
  retail AI versus 1.7-4.8 for the wave army, and far more commander kills.
* Fights at home are judged with the base's defenses and commander.
* Command-efficient: every order is tracked; the budget mirror means the
  army's own commands are never dropped, and production keeps a reserve.
* Deterministic, integer, allocation-free in steady state, async-safe.
* Explain output makes every decision inspectable (grids, goals with ratios,
  squad predictions, engagement statistics).

## Weaknesses

* Water and reach are learned in play (no terrain in the Kit): the fleet
  cannot target a coast it has not sailed near, and a jam on a wet map can
  be mistaken for the sea for three minutes.
* The fleet loses ships to floating defenses and torpedo launchers it only
  sees when in range; submarines are invisible to ships without sonar.
* Damaged aircraft are never repaired (no air repair pads are built), and
  gunships and bombers do not help defend the base against ground raids.
* The retreat improvements (routed retreats, freeing members trapped behind
  buildings) were not attempted: at 48 games per comparison the noise
  (about ±7 % of points) is larger than any effect they could plausibly
  have, so they could not be shown to help.

* With the posture honored, the full offensive follows the strategy's attack
  value (see *Results → Attack value calibration*): hard's first comes at
  minute 7.7–8.1 and easy's at minute 9.1, later than human first kills
  (4.5–5.1); until then the main army raids economy with two units or more
  (*Early harassment*: first kill 5.5 in hard mirrors), skirmishes and
  defends.
* Predictions only know what was seen. An unseen army waiting at its rally
  point is invisible until contact; corridors and repulse memory reduce but
  do not remove approach→contact→retreat cycles.
* Retreats are straight moves to the gather point — units are shot while
  walking away, and there is no kiting.
* Withdrawn units are never repaired (the scripted economy does not repair
  units), so withdrawal only preserves value.
* The raid, home-guard, escort and routing components show no measurable
  win-rate effect on the dev pool at 24-game samples; they add complexity.
* Squads can strand members that are trapped behind buildings (they are
  excluded, not freed).
* Large armies still move as one blob to one patrol point; no flanking or
  concave.

## Framework findings (outside this package)

1. **CORE never fields an army with the scripted economy/production.**
   `ScriptProduction` picks the cheapest `RoleBuilder` as "constructor", which
   on CORE is `cormlv` (a minelayer). `ScriptEconomy` then gives those idle
   "builders" Guard/Repair orders that the resolver rejects every think
   (`FailResolve` ≈ 2 600 per game) and 4 000-9 000 commands per game are
   dropped by the APM limit, so factories sit idle with metal at cap. Every
   slot-1 game on the default pool is army-less for every army policy. Fix:
   only count builders that can build an extractor/factory as constructors
   (or exclude minelayers from `RoleBuilder`), and back off after
   `FailResolve`/`FailNoSite` using `Kit.Last.Reasons`.
2. **Base layout traps units.** Energy placed at spacing 1 forms rows of
   solars with one-cell gaps; on great divide seed 1, 25 of 57 own ground
   units stood trapped near home at the end. Placement should keep
   unit-width lanes (spacing ≥ 2, or lanes like the factory exit lane).
3. **Economy command spam starves later layers.** Failing builds are
   re-emitted every think; on some maps (sherwood) the army had 517 commands
   withheld for lack of budget. A per-layer reservation or a `Kit` helper
   exposing the budget projection (the mirror in `refreshBudget`) would let
   every layer plan within it.
4. **Dark side, hard vs hard, ARM mirror:** both commanders die at tick 8430
   in every game regardless of the army policy.
5. Helpful additions: the destination of a unit's current order in
   `OwnUnit` (brains must track it themselves), and a persistent per-owner
   "last seen" enemy estimate beyond the 60 s mobile memory.

6. **Fighter/bomber roles in the table** (`aikit/info.go`): the economy unit
   now reclassifies aircraft that are mostly anti-air as fighters after the
   damage-table credit, which fixes the stock fighters; but the tech-2
   bombers armpnix and corhurc, whose bombs name aircraft in their damage
   table (anti-air 80 of 80 and 81 of 75), now become `RoleFighter`. A
   dropped weapon should make a bomber first. This package classifies from
   weapon flags itself (`class.go`) and is unaffected.
7. **Terrain in the Kit** — done by the economy unit (`MapInfo.Reach`,
   `DepthSite`, floor heights); the army uses it (see *Reach*). Region maps
   ignore buildings: a yard walled in by tidal generators, or a gather lane
   blocked by a base, still reads as open water/ground.
8. **Production still builds walkers that cannot reach anything.** On
   shore to shore and coast to coast the economy's own explain reports land
   reach 0 ‰ yet it keeps building tanks (71 on shore to shore, no shipyard
   or hovercraft); on pillopeens its fleet is almost all patrol boats.
   Walker production should follow land reach to the enemy, and the naval
   mix should add destroyers (sonar) and heavier hulls once the sea is held.
9. **`core.Brain` does not forward `aikit.Reporter`** to its policies, so
   util+tac cannot publish the army's counters; the `forces` test brain wraps
   it to do so. Forwarding to every policy that implements `Report` would
   make it general.
10. **Per-class losses in the arena result.** The arena counts value lost per
    player but not by unit class; the air/naval results below were measured
    with a small uncommitted patch to `internal/headless/arena.go` that adds
    `lost_<class>` (by trace role) to each player's `extra`. Worth keeping.
11. **The host forgets aircraft after a minute**, so `Board.EnemyAir` drops to
    zero between raids; the army now keeps its own "air about" timestamp.

12. **`first_attack_tick`** was never set in the arena result; since T4
    it is the player's first kill of a finished enemy unit.
13. **The attack value** (T3 findings, addressed in T4 — see *Results →
    Attack value calibration*):
    * ambition scales `att_min` and `att_grow` to 67 % and 61 % at easy
      while the ambition caps hold easy's army to 35 % of the top tier's,
      so easy's attack value stays about three times its army and it
      launches a full offensive in 7 of 96 games. Scaling both linearly
      with ambition (like the army cap) gave offensives in 29 of 48 games
      at minute 15.8, value about 1400, 60.4 % against retail;
    * at hard the attack value (800 + 150/min, or 130 % of the enemy
      estimate) is twice the available army at minute 10, so offensives
      start at minute 11.6–14.8. `att_min=400,att_grow=100` started them
      at minute 11 with 54.7 % of points against the `posture=0` brain
      over 96 games (humans' first kill is at minute 4.5–5.1 by tier);
    * no style perturbs `att_min`, `att_grow` or `att_ratio`, so styles
      barely differ in offensive timing (12.4–13.3 min). Archetype-specific
      values need a per-archetype first-attack measure from the human
      benchmark (it records first combat unit per archetype, 1.9 min for
      O3 to 5.1 min for O6, but not first kill);
    * `docs/MODERN_AI_RESEARCH.md` §8 *Gaps* still says the tactics army
      ignores `Posture.AttackValue`.

## Ideas

* Predict unseen enemy army size from its known economy and elapsed time,
  and place the unlocated part between its base and its last sightings.
* Retreat along the router's low-danger path; covering fire from ranged units
  while short-range ones pull back; kite with range advantage.
* Split the main army for pincer attacks when the square law allows (the
  predicted margin for each half against its target).
* Coordinate with the economy: ask for repairs of withdrawn units, request
  unit types (anti-air, artillery vs defenses) from the prediction's failures.
* Learn the engagement margins online from observed exchange rates per map.
