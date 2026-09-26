package tactics

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Launch conditions and timings (ticks).
const (
	minLaunchValue = 300  // a main squad worth less than this waits
	minLaunchUnits = 3    // ... or with fewer members than this
	gatheredShare  = 60   // percent of the squad that must be present to launch
	stageRadius    = 350  // "at the stage point"
	stageWait      = 600  // longest wait at the stage for stragglers
	retreatWait    = 750  // longest retreat before regrouping
	coolTicks      = 1350 // a zone that repelled an attack is avoided this long
	stragglerDist  = 1000 // members farther than this from the centre are sent to the stage
)

// runSquad advances one squad's state machine and issues its orders.
func (a *Army) runSquad(b *core.Board, s *squad) {
	a.curSq = s
	defer a.clearSquad()
	if len(s.members) == 0 {
		if s.id == sqStrike {
			a.noteStrike(b, s) // a wing lost to the last aircraft (unseen.go)
		}
		s.target = tgtNone
		a.setState(b, s, stGather)
		return
	}
	switch s.id {
	case sqEscort:
		a.runEscort(b, s)
		return
	case sqFighter:
		a.runFighters(b, s)
		return
	case sqStrike:
		a.runStrike(b, s)
		return
	}
	switch s.state {
	case stDefend:
		a.runDefend(b, s)
	case stRetreat:
		a.runRetreat(b, s)
	case stApproach:
		a.runApproach(b, s)
	case stEngage:
		a.runEngage(b, s)
	default:
		a.runGather(b, s)
	}
}

// gatherPoint is where a squad waits: the main gather point, or for the
// home guard a point just in front of the base.
func (a *Army) gatherPoint(b *core.Board, s *squad) (int32, int32) {
	switch s.id {
	case sqDefend:
		if a.passageSquad(s) {
			return a.dgx, a.dgz
		}
		return b.HomeX + (a.gx-b.HomeX)/3, b.HomeZ + (a.gz-b.HomeZ)/3
	case sqNaval:
		return a.navalGather(b)
	}
	return a.gx, a.gz
}

// fallbackPoint is where a squad already at its gather point retreats to.
func (a *Army) fallbackPoint(b *core.Board, s *squad) (int32, int32) {
	if s.id == sqNaval {
		if x, z, ok := a.homeWaterPoint(b); ok {
			return x, z
		}
	}
	return b.HomeX, b.HomeZ
}

// zoneGain is what attacking zone z is worth to squad s: raiders count
// economy, the fleet only what it can engage from the water.
func (a *Army) zoneGain(s *squad, z *zone) int64 {
	raider := s.id == sqRaid || (s.id == sqMain && s.held && a.P.Harass)
	switch {
	case raider:
		if z.eco <= 0 {
			return 0
		}
		return z.eco*2 + z.value/4
	case s.id == sqNaval:
		return z.nval
	}
	return z.value
}

// zoneAlive reports whether a targeted zone still holds something for the
// squad (the fleet: something it can engage from the water).
func (a *Army) zoneAlive(s *squad, z *zone) bool {
	if s.id == sqNaval {
		return z.nval > 0
	}
	return z.value > 0
}

// zoneTarget is the point squad s attacks zone z at and the enemy there.
func (a *Army) zoneTarget(s *squad, z *zone) (int32, int32, *force, *force) {
	if s.id == sqNaval {
		return z.wx, z.wz, &z.nstat, &z.nmob
	}
	return z.x, z.z, &z.stat, &z.mob
}

func (a *Army) runGather(b *core.Board, s *squad) {
	gx, gz := a.gatherPoint(b, s)
	// A fight that finds us while waiting.
	if a.contact(b, s) {
		return
	}
	if s.id != sqDefend {
		if a.chooseTarget(b, s) {
			if a.canLaunch(b, s) {
				a.launch(b, s)
				return
			}
			if s.id == sqMain {
				if n, v := a.launchMin(b, s); s.total.n < n || s.total.value < v {
					a.early.small++
				}
			}
		}
	}
	a.issue(b, s.members, okMove, gx, gz, 0, false)
}

// canLaunch requires the squad gathered and big enough to matter.
func (a *Army) canLaunch(b *core.Board, s *squad) bool {
	if s.id == sqRaid {
		return s.total.n >= 2 && s.present.n*100 >= s.total.n*gatheredShare
	}
	if s.id == sqNaval && s.total.n >= 2 && s.total.value >= minLaunchValue*2 {
		return s.present.value*100 >= s.total.value*gatheredShare || b.Tick-s.since > 1200
	}
	if n, v := a.launchMin(b, s); s.total.value < v || s.total.n < n {
		return false
	}
	return s.present.value*100 >= s.total.value*gatheredShare || b.Tick-s.since > 1200
}

// launchMin is the least squad (members, value) a ground squad launches
// with: a held main squad raids with the harass size (harass.go).
func (a *Army) launchMin(b *core.Board, s *squad) (int32, int64) {
	if a.P.Harass && s.id == sqMain && s.held {
		return a.P.HarassUnits, a.P.HarassValue
	}
	return minLaunchUnits, minLaunchValue
}

// contact reacts to an enemy force within reach of the squad: fight it if
// the prediction allows, otherwise fall back. It reports whether it acted.
func (a *Army) contact(b *core.Board, s *squad) bool {
	var en, stat force
	var ex, ez int32
	if !a.localMobile(b, s, &ex, &ez) {
		return false
	}
	a.localEnemy(b, s.cx, s.cz, s.rng+350, &en, &stat)
	eff := effectiveStatic(&stat, &s.hist, s.present.dps)
	en.merge(&eff)
	own := a.withSupport(b, s)
	r := ratio(&own, &en)
	s.enemy, s.ratio = en, r
	accept := a.retreatMargin(b)
	if s.id == sqRaid || s.soft {
		accept = a.engageMargin(b) // raiders avoid even fights
	}
	if r >= accept {
		s.target = tgtSkirmish
		s.tx, s.tz = ex, ez
		s.sx, s.sz = s.cx, s.cz
		s.launch = s.total.strength()
		a.setState(b, s, stEngage)
		a.issue(b, s.members, okPatrol, ex, ez, 0, true)
		return true
	}
	a.retreat(b, s)
	return true
}

// withSupport is the squad's present force plus the own support at its
// position: a fight at home is fought together with the base.
func (a *Army) withSupport(b *core.Board, s *squad) force {
	own := s.present
	sup := a.supportAt(b, s.cx, s.cz)
	own.merge(&sup)
	return own
}

// supportAt is the own static defenses that cover (x, z) and the own
// commander when it is close.
func (a *Army) supportAt(b *core.Board, x, z int32) force {
	var f force
	o := b.O
	for _, i := range b.Defenses {
		u := &o.Own[i]
		r := int64(u.Info.Range + 100)
		if aikit.Dist2(u.X, u.Z, x, z) <= r*r {
			f.add(u.Info, int64(u.HP), 1000)
		}
	}
	if b.Commander >= 0 {
		c := &o.Own[b.Commander]
		if aikit.Dist2(c.X, c.Z, x, z) <= 600*600 {
			f.add(c.Info, int64(c.HP), 1000)
			f.dps += commanderDPSBonus
		}
	}
	return f
}

// localMobile finds the value-weighted centre of visible armed enemy
// mobiles within the squad's reach.
func (a *Army) localMobile(b *core.Board, s *squad, x, z *int32) bool {
	reach := int64(s.rng + 350)
	var sx, sz, n int64
	for i := range b.O.Enemy {
		c := &b.O.Enemy[i]
		if c.Info == nil || c.Info.DPS <= 0 || !c.Info.Role.Has(aikit.RoleMobile) || c.Info.Role.Has(aikit.RoleAir) {
			continue
		}
		if aikit.Dist2(c.X, c.Z, s.cx, s.cz) > reach*reach {
			continue
		}
		sx += int64(c.X)
		sz += int64(c.Z)
		n++
	}
	if n == 0 {
		return false
	}
	*x, *z = int32(sx/n), int32(sz/n)
	return true
}

// chooseTarget scores every candidate zone for the squad: value destroyed
// per expected loss, discounted by distance, among zones where the
// predicted strength ratio clears the engage margin. The squad's current
// target keeps a bonus so the choice does not flicker.
func (a *Army) chooseTarget(b *core.Board, s *squad) bool {
	margin := a.squadMargin(b, s)
	if s.id == sqRaid {
		margin = raidMargin
	}
	// Held by the posture, the main squad takes only a clearly undefended
	// target (posture.go).
	s.held = a.postureHold(b, s)
	full := margin
	if sm := a.softTargetMargin(); s.held && margin < sm {
		margin = sm
	}
	heldBack := false
	best := int32(tgtNone)
	var bestScore, bestRatio int64
	var bestEnemy force
	for _, zi := range a.candidates {
		z := &a.zones[zi]
		gain := a.zoneGain(s, z)
		if gain <= 0 {
			continue
		}
		if b.Tick < z.coolUntil && s.total.strength() < z.coolStrength*9/4 {
			continue
		}
		zx, zz, zstat, zmob := a.zoneTarget(s, z)
		if !a.reachable(b, s, zx, zz) {
			continue
		}
		stat := effectiveStatic(zstat, &s.hist, s.present.dps)
		en := *zmob
		en.merge(&stat)
		a.corridor(b, s.cx, s.cz, zx, zz, &en)
		own := &s.total
		if s.id == sqNaval {
			a.fleetReserve(b, zx, zz, &en)
			a.seaHurtReserve(zx, zz, &en)
			a.landReserve(b, zx, zz, &en)
			if z.nship*2 < z.nval {
				own = &s.surf // a coast is shelled by guns, not torpedoes
			}
		}
		r := ratio(own, &en)
		d := int64(aikit.Dist(s.cx, s.cz, zx, zz))
		score := int64(-1)
		if r >= margin {
			loss := s.total.value * lossPermille(r) / 1000
			// Travel is paid in the army's time: its value idles for the
			// walk (a unit's worth every five minutes of travel).
			speed := int64(s.speed)
			if speed < 20 {
				speed = 20
			}
			travel := s.total.value * d / (speed * 300)
			score = gain * 1000 / (loss + travel + 60)
			if zi == s.target {
				score = score * 13 / 10
			}
		}
		if s.held && r >= full && r < margin {
			heldBack = true
		}
		a.noteGoal(b.Tick, s.id, zi, zx, zz, gain, r, score)
		if score > bestScore {
			best, bestScore, bestRatio, bestEnemy = zi, score, r, en
		}
	}
	if heldBack && best < 0 {
		a.off.held++
	}
	if best >= 0 {
		z := &a.zones[best]
		s.target = best
		s.tx, s.tz, _, _ = a.zoneTarget(s, z)
		s.enemy, s.ratio = bestEnemy, bestRatio
		a.markGoal(s.id, best)
		return true
	}
	// The fleet with nothing it can reach from the water sails toward the
	// enemy base to find their coast (and learn the way).
	if s.id == sqNaval {
		if x, z, ok := a.navalExplore(b, s); ok {
			s.enemy = a.enemyFleet
			a.landReserve(b, x, z, &s.enemy)
			s.ratio = ratio(&s.total, &s.enemy)
			if s.ratio >= margin {
				s.target, s.tx, s.tz = tgtExplore, x, z
				return true
			}
		}
	}
	// Nothing known to attack: a strong enough main army explores the
	// start positions nobody has looked at lately; held, it probes with
	// the harass size (harass.go).
	if (s.id == sqMain || s.id == sqAmph) && len(a.candidates) == 0 && a.mayExplore(s) {
		if x, z, ok := a.exploreTarget(b, s.cx, s.cz, s, nil); ok && a.reachable(b, s, x, z) {
			s.enemy = a.enemyMob
			s.ratio = ratio(&s.total, &a.enemyMob)
			if s.ratio >= a.engageMargin(b) {
				s.target, s.tx, s.tz = tgtExplore, x, z
				return true
			}
		}
	}
	s.target = tgtNone
	return false
}

// exploreValue is the main army value at which it goes looking for an
// enemy it has not found.
const exploreValue = 600

// startStale is how long a looked-at start position stays explored.
const startStale = 5400

// exploreTarget is the unexplored start position (not home) nearest to
// (x, z).
// With reach tables, only start positions the squad (or the unit) can get
// near are considered.
func (a *Army) exploreTarget(b *core.Board, x, z int32, s *squad, u *aikit.OwnUnit) (int32, int32, bool) {
	m := b.K.Map
	best := -1
	var bestD int64
	for i := range m.Starts {
		st := &m.Starts[i]
		if aikit.Dist2(st[0], st[1], b.HomeX, b.HomeZ) < 800*800 || !m.MaybeEnemyStart(i) {
			continue
		}
		if a.startSeen[i] != 0 && b.Tick-a.startSeen[i] < startStale {
			continue
		}
		if a.reachReady && a.P.Naval {
			if s != nil && a.reachShare(b, s, st[0], st[1], 150) < 500 {
				continue
			}
			if u != nil && !a.unitReaches(b, u, a.unit(u), st[0], st[1], okMove) {
				continue
			}
		}
		d := aikit.Dist2(st[0], st[1], x, z)
		if best < 0 || d < bestD {
			best, bestD = i, d
		}
	}
	if best < 0 {
		return 0, 0, false
	}
	return m.Starts[best][0], m.Starts[best][1], true
}

// corridorWidth is how far from the line to a target an enemy mobile
// force still stands in the way.
const corridorWidth = 700

// corridor adds the enemy mobiles standing between (ax, az) and the
// target (bx, bz) to en: an army on the way has to be fought first. Those
// near the target were counted by the zone (fully within nearMobile, half
// within reinforceDist); the corridor tops them up to full weight.
func (a *Army) corridor(b *core.Board, ax, az, bx, bz int32, en *force) {
	a.memIndex(b)
	o := b.O
	dx, dz := int64(bx-ax), int64(bz-az)
	l2 := dx*dx + dz*dz
	// Only zones that can hold a mobile within corridorWidth of the segment
	// are visited: inside its bounding box grown by the width, and with the
	// zone's centre near enough to the segment for any point of the zone.
	zx0, zz0 := a.zoneXZ(min(ax, bx)-corridorWidth, min(az, bz)-corridorWidth)
	zx1, zz1 := a.zoneXZ(max(ax, bx)+corridorWidth, max(az, bz)+corridorWidth)
	// A zone's points lie within half its diagonal (0.71 of its edge) of
	// its centre; the rest of a whole edge covers the projection's
	// rounding (a thousandth of the segment's length), so no mobile the
	// full scan would count is skipped.
	const zoneWorld = zoneSectors * aikit.SectorWorld
	const zoneReach = corridorWidth + zoneWorld
	for zz := zz0; zz <= zz1; zz++ {
		for zx := zx0; zx <= zx1; zx++ {
			zi := zz*a.zoneW + zx
			k0, k1 := a.mobHead[zi], a.mobHead[zi+1]
			if k0 == k1 {
				continue
			}
			zcx, zcz := zx*zoneWorld+zoneWorld/2, zz*zoneWorld+zoneWorld/2
			if segDist2(ax, az, dx, dz, l2, zcx, zcz) > zoneReach*zoneReach && zx != 0 && zz != 0 && zx != a.zoneW-1 && zz != a.zoneH-1 {
				continue // edge zones also hold points clamped in from off the map
			}
			for _, i := range a.mobIdx[k0:k1] {
				r := &o.Memory[i]
				if segDist2(ax, az, dx, dz, l2, r.X, r.Z) > corridorWidth*corridorWidth {
					continue
				}
				w := freshness(r, b.Tick)
				dt := aikit.Dist2(r.X, r.Z, bx, bz)
				switch {
				case dt <= nearMobile*nearMobile:
					continue
				case dt <= reinforceDist*reinforceDist:
					w /= 2
				}
				en.addEnemy(r.Info, int64(r.Info.HP), w)
			}
		}
	}
	// Forces that threw a squad back recently stay in the way even when
	// they are no longer in sight or memory.
	for i := range a.repulses {
		rp := &a.repulses[i]
		age := b.Tick - rp.tick
		if rp.tick == 0 || age >= repulseTicks {
			continue
		}
		px, pz := int64(rp.x-ax), int64(rp.z-az)
		t := int64(0)
		if l2 > 0 {
			t = (px*dx + pz*dz) * 1000 / l2
			if t < 0 {
				t = 0
			} else if t > 1000 {
				t = 1000
			}
		}
		cx, cz := ax+int32(dx*t/1000), az+int32(dz*t/1000)
		if aikit.Dist2(rp.x, rp.z, cx, cz) > corridorWidth*corridorWidth && aikit.Dist2(rp.x, rp.z, bx, bz) > reinforceDist*reinforceDist {
			continue
		}
		w := int64(1000) - int64(age)*1000/repulseTicks
		g := rp.enemy.scaledDPS(w, 1000)
		g.hp = g.hp * w / 1000
		g.value = g.value * w / 1000
		en.merge(&g)
	}
}

// segDist2 is the squared distance from (x, z) to the segment from (ax, az)
// by (dx, dz) (l2 its squared length), by projection clamped to its ends.
func segDist2(ax, az int32, dx, dz, l2 int64, x, z int32) int64 {
	px, pz := int64(x-ax), int64(z-az)
	t := int64(0)
	if l2 > 0 {
		t = (px*dx + pz*dz) * 1000 / l2
		if t < 0 {
			t = 0
		} else if t > 1000 {
			t = 1000
		}
	}
	cx, cz := ax+int32(dx*t/1000), az+int32(dz*t/1000)
	return aikit.Dist2(x, z, cx, cz)
}

// repulseTicks is how long a force that threw a squad back is remembered
// where it stood (fading linearly).
const repulseTicks = 2700

// maxRepulses bounds the remembered repulses.
const maxRepulses = 4

type repulse struct {
	x, z  int32
	tick  uint32
	enemy force
}

// noteRepulse remembers the enemy force a squad retreats from, replacing
// the oldest entry.
func (a *Army) noteRepulse(b *core.Board, s *squad) {
	if s.enemy.dps <= 0 {
		return
	}
	x, z := s.tx, s.tz
	if s.target != tgtSkirmish {
		x, z = s.cx+(s.tx-s.cx)/3, s.cz+(s.tz-s.cz)/3
	}
	k := 0
	for i := range a.repulses {
		if a.repulses[i].tick < a.repulses[k].tick {
			k = i
		}
	}
	a.repulses[k] = repulse{x: x, z: z, tick: b.Tick, enemy: s.enemy}
}

// launch picks a stage point short of the target, outside the reach of its
// defenses, routes there through low-threat sectors and starts the
// approach.
func (a *Army) launch(b *core.Board, s *squad) {
	a.noteLaunch(b, s)
	a.stagePoint(b, s)
	a.planRoute(b, s)
	a.setState(b, s, stApproach)
	a.issueRoute(b, s, s.sx, s.sz, false)
}

// stagePoint walks back from the target toward the squad until it is
// beyond the static reach at the target and the danger is low.
func (a *Army) stagePoint(b *core.Board, s *squad) {
	d := int64(aikit.Dist(s.tx, s.tz, s.cx, s.cz))
	standoff := int64(600)
	if s.enemy.dps > 0 {
		if r := s.enemy.meanRange() + 300; r > standoff {
			standoff = r
		}
	}
	if d <= standoff {
		s.sx, s.sz = s.cx, s.cz
		return
	}
	lim := s.present.dps / 4
	if lim < gatherDanger {
		lim = gatherDanger
	}
	// The stage must be ground a unit has stood on: a point on the line
	// can be a cliff or water on a winding map. The fleet stages on known
	// water, the amphibious squad on either.
	m := b.K.Map
	var first bool
	var fx, fz int32
	var foff int64
	for off := standoff; off < d; off += aikit.SectorWorld {
		x := s.tx + int32(int64(s.cx-s.tx)*off/d)
		z := s.tz + int32(int64(s.cz-s.tz)*off/d)
		sec := m.Sector(x, z)
		var ok bool
		switch {
		case a.reachReady && a.P.Naval && s.mainGroup() != nil:
			g := s.mainGroup()
			ok = a.hasRegion(g.cls, g.reg, sec)
		case s.id == sqNaval:
			ok = a.water[sec] != 0
		case s.id == sqAmph:
			ok = a.known[sec] != 0 || a.water[sec] != 0
		default:
			ok = a.known[sec] != 0
		}
		if int64(a.static.At(x, z)) > lim || !ok {
			continue
		}
		// Out of passages: the first acceptable point in one is kept only
		// when no open point follows within stageSlack (a long pass cannot
		// be stepped around; a ramp can).
		if !a.blobInPassage(b, s, x, z) {
			s.sx, s.sz = x, z
			return
		}
		if !first {
			first, fx, fz, foff = true, x, z, off
		}
		if off-foff >= stageSlack {
			break
		}
	}
	if first {
		s.sx, s.sz = fx, fz
		return
	}
	s.sx, s.sz = s.cx, s.cz
}

// stageSlack is how much farther back from the target a stage point may
// move to leave a passage (world units).
const stageSlack = 640

// planRoute fills the squad's waypoints when routing around danger is
// worth it: the threat-weighted cheapest path costs clearly less than the
// straight line. Cell cost is a base step plus danger relative to the
// squad's own firepower, plus a penalty for ground no unit has walked.
func (a *Army) planRoute(b *core.Board, s *squad) {
	s.nwp = 0
	s.routed = false
	if !a.P.Route || s.id == sqNaval {
		return // ships follow the engine's water paths
	}
	m := b.K.Map
	own := s.present.dps
	if own < 20 {
		own = 20
	}
	for i := range a.rt.cost {
		c := int64(10)
		dv := int64(a.danger.V[i])
		c += dv * 40 / own
		if a.known[i] == 0 {
			c += 12
		}
		if c > 2000 {
			c = 2000
		}
		a.rt.cost[i] = int32(c)
	}
	from, to := m.Sector(s.cx, s.cz), m.Sector(s.sx, s.sz)
	if from == to {
		return
	}
	direct := a.rt.lineCost(m, s.cx, s.cz, s.sx, s.sz)
	cost := a.rt.search(from, to)
	if cost >= routeInf || int64(cost)*10 >= int64(direct)*8 {
		return
	}
	a.routeInto(m, s, a.known)
}

// routeInto compresses the router's path into squad s's waypoints. The
// compression collects every turn before it keeps an even subset, so it
// works in a scratch slice that can hold more turns than the squad's
// array, and the kept subset is copied into the array.
func (a *Army) routeInto(m *aikit.MapInfo, s *squad, ok []uint8) {
	a.wpBuf = a.rt.waypoints(m, a.wpBuf, ok)
	s.nwp = int32(copy(s.wps[:], a.wpBuf))
	s.routed = s.nwp > 0
}

func (a *Army) runApproach(b *core.Board, s *squad) {
	if a.contact(b, s) {
		return
	}
	// Keep the target honest: it may have been cleared or reinforced.
	if s.target >= 0 {
		z := &a.zones[s.target]
		if !a.zoneAlive(s, z) {
			if !a.chooseTarget(b, s) {
				s.target = tgtNone
				a.setState(b, s, stGather)
				return
			}
			a.launch(b, s)
			return
		}
		zx, zz, zstat, zmob := a.zoneTarget(s, z)
		stat := effectiveStatic(zstat, &s.hist, s.present.dps)
		en := *zmob
		en.merge(&stat)
		a.corridor(b, s.cx, s.cz, zx, zz, &en)
		if s.id == sqNaval {
			a.landReserve(b, zx, zz, &en) // unseen.go: the army that defends that coast
		}
		s.enemy = en
		s.ratio = ratio(&s.total, &en)
		if s.ratio < a.pullMargin(b, s) {
			a.retreat(b, s)
			return
		}
	}
	atStage := aikit.Dist2(s.cx, s.cz, s.sx, s.sz) <= stageRadius*stageRadius
	if atStage && s.arrived == 0 {
		s.arrived = b.Tick
	}
	if s.dmgRate > 0 && a.blind(s, s.enemy.dps) {
		// Shot at by what it cannot see on the way in: turn back.
		s.why = whyBlind
		if s.target >= 0 {
			z := &a.zones[s.target]
			z.coolUntil = b.Tick + coolTicks
			z.coolStrength = s.total.strength()
		}
		a.retreat(b, s)
		return
	}
	travel := uint32(0)
	if s.speed > 0 {
		travel = uint32(aikit.Dist(s.cx, s.cz, s.sx, s.sz) / s.speed * 30)
	}
	timedOut := b.Tick-s.since > 2400+travel*2
	grouped := s.present.value*100 >= s.total.value*75
	if (atStage && (grouped || b.Tick-s.arrived > stageWait)) || timedOut {
		s.launch = s.total.strength()
		a.setState(b, s, stEngage)
		a.issue(b, s.members, okPatrol, s.tx, s.tz, 0, true)
		return
	}
	a.issueRoute(b, s, s.sx, s.sz, false)
}

func (a *Army) runEngage(b *core.Board, s *squad) {
	// A field skirmish follows the enemy it met (re-aimed only when it has
	// moved well away, so the patrol is not re-sent every think).
	if s.target == tgtSkirmish {
		var ex, ez int32
		if a.localMobile(b, s, &ex, &ez) && aikit.Dist2(ex, ez, s.tx, s.tz) > 300*300 {
			s.tx, s.tz = ex, ez
		}
	}
	// The fight as it stands: what is around the squad now, plus the
	// defenses at the target once the squad is close to it.
	var en, stat force
	a.localEnemy(b, s.cx, s.cz, s.rng+400, &en, &stat)
	if s.target >= 0 && aikit.Dist2(s.cx, s.cz, s.tx, s.tz) <= 700*700 {
		_, _, zstat, _ := a.zoneTarget(s, &a.zones[s.target])
		if zstat.dps > stat.dps {
			stat = *zstat
		}
	}
	eff := effectiveStatic(&stat, &s.hist, s.present.dps)
	en.merge(&eff)
	s.enemy = en
	own := a.withSupport(b, s)
	s.ratio = ratio(&own, &en)
	spent := s.launch > 0 && s.total.strength()*100 < s.launch*25
	// Unseen fire ends an engagement only once it is costing units: the
	// squad down to 85 % of the strength it engaged with.
	blind := a.blind(s, en.dps) && s.launch > 0 && s.total.strength()*100 < s.launch*85
	pull := a.pullMargin(b, s)
	if s.ratio < pull || spent || blind {
		switch {
		case spent:
			s.why = whySpent
		case blind && s.ratio >= pull:
			s.why = whyBlind
		default:
			s.why = whyRatio
		}
		if s.target >= 0 {
			z := &a.zones[s.target]
			z.coolUntil = b.Tick + coolTicks
			z.coolStrength = s.total.strength()
		}
		a.retreat(b, s)
		return
	}
	// Target cleared: chain to the next one nearby, else regroup.
	if a.targetCleared(b, s) {
		a.stats.cleared++
		if a.chooseTarget(b, s) && aikit.Dist2(s.cx, s.cz, s.tx, s.tz) <= 1500*1500 {
			a.noteLaunch(b, s)
			s.launch = s.total.strength()
			a.stagePoint(b, s)
			a.issue(b, s.members, okPatrol, s.tx, s.tz, 0, true)
			return
		}
		if s.target >= 0 || s.target == tgtExplore {
			a.launch(b, s)
			return
		}
		a.setState(b, s, stGather)
		a.runGather(b, s)
		return
	}
	a.withdrawDamaged(b, s)
	if a.focusFire(b, s) {
		return
	}
	a.engageOrders(b, s)
}

// engageOrders keeps the members with the body patrolling to the target.
// Reinforcements do not walk into the fight one by one: they wait at the
// gather point until they are a group worth sending, then move to the stage
// point together.
func (a *Army) engageOrders(b *core.Board, s *squad) {
	o := b.O
	a.tmp = a.tmp[:0]
	for _, i := range s.members {
		u := &o.Own[i]
		if a.units[u.H].withdraw != 0 {
			continue
		}
		if aikit.Dist2(u.X, u.Z, s.cx, s.cz) > stragglerDist*stragglerDist {
			continue
		}
		a.tmp = append(a.tmp, i)
	}
	a.issue(b, a.tmp, okPatrol, s.tx, s.tz, 0, true)
	a.reinforce(b, s)
}

// reinforce sends members far from the body to the stage point once enough
// of them wait at the gather point, and gathers them otherwise. Members
// already on their way keep going.
func (a *Army) reinforce(b *core.Board, s *squad) {
	o := b.O
	gx, gz := a.gatherPoint(b, s)
	a.tmp = a.tmp[:0]
	a.tmp2 = a.tmp2[:0] // arrived at the stage: join the fight
	var waiting int64
	for _, i := range s.members {
		u := &o.Own[i]
		if aikit.Dist2(u.X, u.Z, s.cx, s.cz) <= stragglerDist*stragglerDist {
			continue
		}
		um := a.unit(u)
		if um.ordKind == okMove && um.ordX == s.sx && um.ordZ == s.sz {
			if u.Order == aikit.OrderIdle && aikit.Dist2(u.X, u.Z, s.sx, s.sz) <= stageRadius*stageRadius {
				a.tmp2 = append(a.tmp2, i)
			}
			continue // already sent to join
		}
		if um.ordKind == okPatrol && um.ordX == s.tx && um.ordZ == s.tz {
			continue // joined from the stage
		}
		a.tmp = append(a.tmp, i)
		if aikit.Dist2(u.X, u.Z, gx, gz) <= 600*600 {
			waiting += int64(u.Info.Value)
		}
	}
	if len(a.tmp2) > 0 {
		a.issue(b, a.tmp2, okPatrol, s.tx, s.tz, 0, false)
	}
	if len(a.tmp) == 0 {
		return
	}
	if waiting >= reinforceValue || waiting*4 >= s.present.value {
		a.issue(b, a.tmp, okMove, s.sx, s.sz, 0, false)
		return
	}
	a.issue(b, a.tmp, okMove, gx, gz, 0, false)
}

// reinforceValue is the value of a reinforcement group sent on its own.
const reinforceValue = 400

// targetCleared reports whether nothing worth fighting remains at the
// target: the zone holds no remembered value and no enemy is in sight
// around the target point.
func (a *Army) targetCleared(b *core.Board, s *squad) bool {
	if s.target >= 0 && a.zoneAlive(s, &a.zones[s.target]) {
		return false
	}
	for i := range b.O.Enemy {
		c := &b.O.Enemy[i]
		if aikit.Dist2(c.X, c.Z, s.tx, s.tz) <= 600*600 {
			return false
		}
	}
	if s.target == tgtExplore {
		// Exploring: done when the squad has reached the start position or
		// something to attack has turned up.
		found := len(a.candidates) > 0
		if s.id == sqNaval {
			found = a.navalTargets > 0
		}
		return found || aikit.Dist2(s.cx, s.cz, s.tx, s.tz) <= 500*500
	}
	return true
}

// retreat pulls the squad back to its gather point (the raid squad and a
// squad fighting at home fall back toward the base).
func (a *Army) retreat(b *core.Board, s *squad) {
	if s.state == stEngage || s.state == stApproach {
		a.noteRepulse(b, s)
	}
	a.setState(b, s, stRetreat)
	s.sx, s.sz = a.gatherPoint(b, s)
	if aikit.Dist2(s.cx, s.cz, s.sx, s.sz) <= 500*500 {
		s.sx, s.sz = a.fallbackPoint(b, s)
	}
	s.focus = 0
	a.issue(b, s.members, okMove, s.sx, s.sz, 0, true)
}

func (a *Army) runRetreat(b *core.Board, s *squad) {
	if aikit.Dist2(s.cx, s.cz, s.sx, s.sz) <= 450*450 || b.Tick-s.since > retreatWait {
		s.target = tgtNone
		a.setState(b, s, stGather)
		a.runGather(b, s)
		return
	}
	a.issue(b, s.members, okMove, s.sx, s.sz, 0, true)
}

func (a *Army) runDefend(b *core.Board, s *squad) {
	a.withdrawDamaged(b, s)
	s.tx, s.tz = s.defX, s.defZ
	var en, stat force
	a.localEnemy(b, s.cx, s.cz, s.rng+400, &en, &stat)
	eff := effectiveStatic(&stat, &s.hist, s.present.dps)
	en.merge(&eff)
	if en.dps > 0 {
		s.enemy = en
		own := a.withSupport(b, s)
		s.ratio = ratio(&own, &en)
	}
	if a.focusFire(b, s) {
		return
	}
	a.issue(b, s.members, okPatrol, s.defX, s.defZ, 0, true)
}

// runEscort keeps anti-air units with the main army, patrolling at its
// centre so they engage aircraft over it without leading the ground fight.
func (a *Army) runEscort(b *core.Board, s *squad) {
	m := &a.sq[sqMain]
	if s.state == stDefend {
		a.issue(b, s.members, okPatrol, s.defX, s.defZ, 0, true)
		return
	}
	x, z := a.gx, a.gz
	if len(m.members) > 0 {
		// Trail the main army slightly toward home.
		x = m.cx + (b.HomeX-m.cx)/10
		z = m.cz + (b.HomeZ-m.cz)/10
	}
	s.tx, s.tz = x, z
	// Re-sent only when the army moved well away from the last order.
	for _, i := range s.members {
		um := a.unit(&b.O.Own[i])
		if um.ordKind == okPatrol && aikit.Dist2(um.ordX, um.ordZ, x, z) <= 400*400 {
			x, z = um.ordX, um.ordZ
			break
		}
	}
	a.issue(b, s.members, okPatrol, x, z, 0, false)
}

// ---------------------------------------------------------------------------
// Micro.

// Micro gates by persona skill.
const (
	skillWithdraw = 40
	skillFocus    = 60
	withdrawPct   = 30 // hit points percent below which a unit is pulled out
	withdrawValue = 90 // cheaper units are not worth the action
)

// withdrawDamaged sends badly damaged members home (one group order).
func (a *Army) withdrawDamaged(b *core.Board, s *squad) {
	if !a.P.Micro || b.K.Persona.Skill < skillWithdraw {
		return
	}
	o := b.O
	a.tmp = a.tmp[:0]
	for _, i := range s.members {
		u := &o.Own[i]
		if u.Info.Value < withdrawValue || u.MaxHP <= 0 || u.HP*100 >= u.MaxHP*withdrawPct {
			continue
		}
		um := a.unit(u)
		if um.withdraw != 0 || b.Tick-um.hurtTick > 90 {
			continue // not under fire now
		}
		a.tmp = append(a.tmp, i)
	}
	if len(a.tmp) == 0 {
		return
	}
	wx, wz, _ := a.withdrawPoint(b, o.Own[a.tmp[0]].Info)
	if !a.issue(b, a.tmp, okMove, wx, wz, 0, true) {
		return
	}
	a.stats.withdrawn += int64(len(a.tmp))
	for _, i := range a.tmp {
		u := &o.Own[i]
		a.unit(u).withdraw = b.Tick
		b.K.SetTag(u.H, tagWithdrawn)
	}
	// They leave the squad now: the orders that follow this think (focus
	// fire, the patrol, an intercept, the next attack run) and the merge
	// and split must not take them back.
	s.members = a.notWithdrawn(b, s.members)
}

// notWithdrawn filters list in place to the units not walking home
// damaged.
func (a *Army) notWithdrawn(b *core.Board, list []int32) []int32 {
	o := b.O
	n := 0
	for _, i := range list {
		if a.units[o.Own[i].H].withdraw == 0 {
			list[n] = i
			n++
		}
	}
	return list[:n]
}

// focusFire concentrates the squad on the enemy it can kill fastest for
// the damage it removes: highest (damage per second + value share) per
// remaining hit point among visible enemies within reach, never one
// standing under static defenses the squad could not beat. The attack is
// followed by a queued patrol back to the target so no unit idles when
// the victim dies. It reports whether it issued orders.
func (a *Army) focusFire(b *core.Board, s *squad) bool {
	if !a.P.Micro || b.K.Persona.Skill < skillFocus {
		return false
	}
	o := b.O
	reach := int64(s.rng + 150)
	var best pool.Handle
	var bestG uint32
	var bestP int64
	var bx, bz int32
	curAlive := false
	for i := range o.Enemy {
		c := &o.Enemy[i]
		if c.Info == nil || !c.Visible {
			continue
		}
		if aikit.Dist2(c.X, c.Z, s.cx, s.cz) > reach*reach {
			continue
		}
		info := c.Info
		if info.Role.Has(aikit.RoleAir) && s.present.aa <= 0 {
			continue
		}
		if info.Role.Has(aikit.RoleMobile) && int64(a.static.At(c.X, c.Z))*3 > s.present.dps {
			continue // do not chase it into defenses
		}
		hp := int64(info.HP)*int64(c.HPPct)/100 + 100
		p := (int64(info.DPS)*10 + int64(info.Value)) * 10000 / hp
		if info.Role.Has(aikit.RoleCommander) {
			if s.ratio < 2000 {
				continue
			}
			p *= 8
		}
		if c.H == s.focus && c.Gen == s.focusG {
			curAlive = true
			p = p * 3 / 2 // stickiness: switching costs an action and aim time
		}
		if p > bestP {
			best, bestG, bestP, bx, bz = c.H, c.Gen, p, c.X, c.Z
		}
	}
	if best == 0 {
		s.focus = 0
		return false
	}
	if best == s.focus && bestG == s.focusG && curAlive {
		// Already on it; only newcomers need the order.
		return a.focusIssue(b, s, best, bestG, bx, bz)
	}
	if b.Tick-s.focusTick < 45 && curAlive {
		return false
	}
	s.focus, s.focusG = best, bestG
	s.focusTick = b.Tick
	return a.focusIssue(b, s, best, bestG, bx, bz)
}

func (a *Army) focusIssue(b *core.Board, s *squad, t pool.Handle, tg uint32, x, z int32) bool {
	o := b.O
	a.tmp = a.tmp[:0]
	for _, i := range s.members {
		u := &o.Own[i]
		if a.units[u.H].withdraw != 0 || u.Info.DPS <= 0 {
			continue
		}
		r := int64(u.Info.Range + 300)
		if aikit.Dist2(u.X, u.Z, x, z) <= r*r {
			a.tmp = append(a.tmp, i)
		}
	}
	a.collect(b, a.tmp, okAttack, s.tx, s.tz, t, tg)
	if len(a.buf) == 0 {
		return true
	}
	left := a.avail - a.spent
	if left < 2 {
		return false
	}
	a.spend(true)
	a.spend(true)
	a.stats.focus++
	b.K.Attack(a.buf, t, false)
	b.K.Patrol(a.buf, s.tx, s.tz, true)
	a.record(b, okAttack, s.tx, s.tz, t, tg)
	// Members not in reach still need the ordinary patrol.
	return false
}

// ---------------------------------------------------------------------------
// Merge, split and scouts.

func (a *Army) mergeSplit(b *core.Board) {
	k := b.K
	o := b.O
	main := &a.sq[sqMain]
	// A raid squad without a viable target for a minute folds into the
	// main army, and new fast units go to the main army for two minutes.
	if r := &a.sq[sqRaid]; r.active && len(r.members) > 0 && r.state == stGather {
		if r.target == tgtNone {
			if r.idleSince == 0 {
				r.idleSince = b.Tick
			}
		} else {
			r.idleSince = 0
		}
		if r.idleSince != 0 && b.Tick-r.idleSince > 1800 {
			for _, i := range r.members {
				k.SetTag(o.Own[i].H, sqMain)
			}
			r.idleSince = 0
			a.raidBlockUntil = b.Tick + 3600
		}
	}
	// A waiting main army hands fast units to an under-strength raid squad.
	if r := &a.sq[sqRaid]; r.active && main.state == stGather && b.Tick >= a.raidBlockUntil && r.state == stGather && r.total.n < 3 {
		moved := int32(0)
		for _, i := range main.members {
			if r.total.n+moved >= 4 {
				break
			}
			if isFast(o.Own[i].Info) {
				k.SetTag(o.Own[i].H, sqRaid)
				moved++
			}
		}
	}
	// A waiting home guard keeps only what recent threats justify; the
	// rest joins the main army.
	if d := &a.sq[sqDefend]; d.active && d.state == stGather && len(d.members) > 0 {
		keep := a.defendNeed(b)
		if d.total.value > keep*3/2+200 {
			var v int64
			for _, i := range d.members {
				v += int64(o.Own[i].Info.Value)
				if v > keep {
					k.SetTag(o.Own[i].H, sqMain)
				}
			}
		}
	}
}

// scouts look at the start positions nobody has seen lately, nearest to
// home first, then walk the far metal spots in turn.
func (a *Army) scouts(b *core.Board) {
	o := b.O
	m := b.K.Map
	for _, i := range b.Combat {
		u := &o.Own[i]
		if u.Tag != tagScout || u.Order != aikit.OrderIdle {
			continue
		}
		// With the tour on, the next start is the one nearest the scout.
		fx, fz := b.HomeX, b.HomeZ
		if a.P.Tour {
			fx, fz = u.X, u.Z
		}
		x, z, ok := a.exploreTarget(b, fx, fz, nil, u)
		if !ok {
			n := int32(len(m.Spots))
			for tries := int32(0); tries < n; tries++ {
				a.scoutNext = (a.scoutNext + 1) % n
				sp := &m.Spots[a.scoutNext]
				if !sp.Water && aikit.Dist2(sp.X, sp.Z, b.HomeX, b.HomeZ) > 800*800 && a.static.At(sp.X, sp.Z) == 0 &&
					(!a.reachReady || !a.P.Naval || a.unitReaches(b, u, a.unit(u), sp.X, sp.Z, okMove)) {
					x, z, ok = sp.X, sp.Z, true
					break
				}
			}
		}
		if !ok {
			continue
		}
		one := a.tmp[:0]
		one = append(one, i)
		a.issue(b, one, okMove, x, z, 0, false)
	}
}

// ---------------------------------------------------------------------------
// Goal notes for Explain.

// noteGoal keeps the evaluations of the latest think that weighed targets,
// so Explain shows them even on a think that did not.
func (a *Army) noteGoal(tick uint32, sq, zi, x, z int32, value, r, score int64) {
	if a.goalsTick != tick {
		a.goals = a.goals[:0]
		a.goalsTick = tick
	}
	g := goalRec{squad: sq, zone: zi, x: x, z: z, value: value, ratio: r, score: score}
	if len(a.goals) < maxGoals {
		a.goals = append(a.goals, g)
		return
	}
	// Replace the weakest entry.
	w := 0
	for i := range a.goals {
		if a.goals[i].score < a.goals[w].score {
			w = i
		}
	}
	if score > a.goals[w].score {
		a.goals[w] = g
	}
}

func (a *Army) markGoal(sq, zi int32) {
	for i := range a.goals {
		if a.goals[i].squad == sq && a.goals[i].zone == zi {
			a.goals[i].chosen = true
		}
	}
}

func (a *Army) clearSquad() { a.curSq = nil }
