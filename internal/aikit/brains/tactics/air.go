package tactics

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Air task forces.
//
// Fighters hold air superiority: they intercept enemy aircraft over our
// base, army and fleet, escort our strikes, and otherwise fly a combat air
// patrol between the base and the army. Bombers and gunships form a strike
// wing that waits at a rally point behind the base until it is complete,
// then strikes the most valuable target it can destroy for the least
// expected loss to anti-air — economy, constructors, an exposed commander,
// artillery — and flies home to regroup. Anti-air is remembered: a
// persistent grid keeps what was seen (mobile anti-air fades over minutes
// rather than vanishing when it leaves sight) and where our aircraft were
// shot at without a known cause. Unarmed aircraft scout for the strikes.

// Strike wing timings and margins.
const (
	minStrikeWing  = 3    // aircraft a strike waits for
	strikeWaitMax  = 1800 // a smaller wing flies after waiting this long (ticks)
	strikeMargin   = 1500 // target value over expected loss, permille
	strikeLinger   = 900  // longest stay over the target area (ticks)
	strikeRetreat  = 450  // strength share (permille) below which a strike returns
	rallyRadius    = 700  // "at the rally point"
	strikeChain    = 1500 // next target considered while over the target area
	aaExposure     = 600  // world units flown inside anti-air cover per pass (in and out)
	maxStrikeCands = 12
	airHurtRadius  = 3 * aikit.SectorWorld
	aaMemTicks     = 5400 // time constant of the anti-air memory's decay
	airSeenTicks   = 5400 // enemy aircraft stay "seen" this long for the escort
	fighterTol     = 18   // fighters engage over anti-air below HP/this (dps)
	scoutStale     = 2700 // a zone looked at this recently is not re-scouted
	// A commander is struck when seen in the last 30 s (it works around its
	// base; the attack order follows the unit) and killable in six passes:
	// it ends the game.
	comFresh  = 900
	comPasses = 6
)

// airRally is where aircraft regroup: behind the base, away from the enemy,
// and clear of own factories (aircraft parked on a pad stop production).
// The choice is kept until a factory appears near it.
func (a *Army) airRally(b *core.Board) (int32, int32) {
	if a.rallySet && b.Tick < a.rallyTick+300 {
		return a.rallyX, a.rallyZ
	}
	a.rallySet, a.rallyTick = true, b.Tick
	m := b.K.Map
	dx, dz := int64(b.EnemyX-b.HomeX), int64(b.EnemyZ-b.HomeZ)
	d := aikit.ISqrt64(dx*dx + dz*dz)
	if d < 1 {
		dx, dz, d = 1, 0, 1
	}
	// Straight back, then turned a quarter and a half-quarter to either side.
	var x, z int32
	for k := 0; k < len(rallyTurns); k++ {
		t := &rallyTurns[k]
		rx := (-dx*t[0] - dz*t[1]) / 1000
		rz := (-dz*t[0] + dx*t[1]) / 1000
		x = clampTo(b.HomeX+int32(rx*450/d), m.WorldW)
		z = clampTo(b.HomeZ+int32(rz*450/d), m.WorldH)
		if !a.nearFactory(b, x, z) {
			break
		}
	}
	a.rallyX, a.rallyZ = x, z
	return x, z
}

// rallyTurns are (cos, sin) permille of the turns tried for the air rally.
var rallyTurns = [...][2]int64{{1000, 0}, {707, 707}, {707, -707}, {0, 1000}, {0, -1000}}

func clampTo(v, hi int32) int32 {
	if v < 64 {
		return 64
	}
	if v > hi-64 {
		return hi - 64
	}
	return v
}

// airPicture rebuilds the enemy air picture and the anti-air memory. It
// runs after buildPicture, which filled a.aa with ground and sea anti-air.
func (a *Army) airPicture(b *core.Board) {
	o := b.O
	tick := b.Tick
	a.enemyAirSup = force{}
	a.enemyAirN = 0
	var fx, fz, fw int64
	for i := range o.Memory {
		r := &o.Memory[i]
		info := r.Info
		if !info.Role.Has(aikit.RoleAir) {
			continue
		}
		if r.LastSeen == tick && (info.DPS > 0 || info.AirDPS > 0) {
			a.airSeen = tick
		}
		if info.AirDPS <= 0 {
			continue
		}
		w := freshness(r, tick)
		var g force
		g.add(info, int64(info.HP), w)
		g.dps, g.rngW = g.aa, int64(info.Range)*g.aa
		a.enemyAirSup.merge(&g)
		a.enemyAirN++
		fx += int64(r.X) * w
		fz += int64(r.Z) * w
		fw += w
	}
	if fw > 0 {
		a.enemyAirX, a.enemyAirZ = int32(fx/fw), int32(fz/fw)
	}
	// Anti-air memory: every few seconds the memory fades and takes in
	// what is known now (queries take the larger of the two, see aaAt); a
	// sector where our aircraft were hurt without known anti-air keeps the
	// damage rate as unexplained anti-air.
	if a.aaTick == 0 {
		a.aaTick = tick
	}
	if dt := int64(tick - a.aaTick); dt >= memFadeTicks {
		a.aaTick = tick
		fadeGrid(a.aaMem, a.aaMemK, dt, aaMemTicks, a.aa)
	}
	dt := int64(a.dt)
	for i := range o.Own {
		u := &o.Own[i]
		if !u.Built || !u.Info.Role.Has(aikit.RoleAir) {
			continue
		}
		um := a.unit(u)
		if um.hurtTick != tick || um.airDmg <= 0 {
			continue
		}
		rate := um.airDmg * 30 / int32(max64(dt, 15))
		if int32(a.aaAt(u.X, u.Z)) < rate {
			a.addDisc(a.aaMem, u.X, u.Z, airHurtRadius, rate)
		}
	}
}

// memFadeTicks is how often the fading memories (anti-air, sea hits) are
// aged: a whole-grid pass, so not every think.
const memFadeTicks = 150

// fadeGrid ages a remembered grid by dt ticks with time constant tau, one
// exponential step. g holds whole units for queries and painting; k holds
// the same memory in thousandths, so a small value keeps fading instead of
// stopping where v·dt/tau truncates to zero (below tau/dt: 35 for a
// 5400-tick constant aged every 150 ticks), and every cell reaches zero on
// its time constant. A cell painted since the last fade (g above k)
// restarts from its painted value. floor, when not nil, holds each cell up
// to what is known now.
func fadeGrid(g *aikit.Grid, k []int64, dt, tau int64, floor *aikit.Grid) {
	for i := range g.V {
		v := k[i]
		if p := int64(g.V[i]) * 1000; p > v {
			v = p
		}
		if v != 0 {
			v -= v * dt / tau
			if v < 0 {
				v = 0
			}
		}
		if floor != nil {
			if c := int64(floor.V[i]) * 1000; c > v {
				v = c
			}
		}
		k[i] = v
		g.V[i] = int32(v / 1000)
	}
}

// aaAt is the anti-air at a point: what is known now or remembered.
func (a *Army) aaAt(x, z int32) int32 {
	return max32(a.aa.At(x, z), a.aaMem.At(x, z))
}

// aaLineMax is the largest anti-air along a segment (one sample per
// sector).
func (a *Army) aaLineMax(ax, az, bx, bz int32) int64 {
	return int64(max32(a.aa.LineMax(ax, az, bx, bz), a.aaMem.LineMax(ax, az, bx, bz)))
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// airForce is the air-to-air fighting force of a group of own aircraft.
func airForce(f *force, info *aikit.UnitInfo, hp int64) {
	f.dps += int64(info.AirDPS)
	f.hp += hp
	f.rngW += int64(info.Range) * int64(info.AirDPS)
	f.value += int64(info.Value)
	f.n++
}

// ---------------------------------------------------------------------------
// Fighters.

func (a *Army) runFighters(b *core.Board, s *squad) {
	a.withdrawDamaged(b, s)
	if a.intercept(b, s) {
		return
	}
	st := &a.sq[sqStrike]
	if a.enemyAirSup.dps > 0 && st.active && len(st.members) > 0 && st.hasCentre && (st.state == stApproach || st.state == stEngage) {
		// Escort against enemy fighters: fly with the wing (the patrol is
		// re-aimed as it moves), not ahead of it into the target's
		// anti-air. With no enemy fighters known, escorting only adds
		// fighters to the anti-air's targets.
		a.setState(b, s, stApproach)
		s.tx, s.tz = st.cx, st.cz
		a.airPatrol(b, s, st.cx, st.cz, false)
		return
	}
	a.setState(b, s, stGather)
	x, z := a.capPoint(b)
	s.tx, s.tz = x, z
	a.airPatrol(b, s, x, z, false)
}

// airPatrol sends a patrol re-aimed only when the point moved well away
// from the one the members carry (aircraft patrol constantly; re-sending
// every think would waste actions).
func (a *Army) airPatrol(b *core.Board, s *squad, x, z int32, urgent bool) {
	for _, i := range s.members {
		um := a.unit(&b.O.Own[i])
		if um.ordKind == okPatrol && aikit.Dist2(um.ordX, um.ordZ, x, z) <= 450*450 {
			x, z = um.ordX, um.ordZ
			break
		}
	}
	a.issue(b, s.members, okPatrol, x, z, 0, urgent)
}

// capPoint is the combat air patrol: over the base, reaching out toward
// the main army when it is in the field.
func (a *Army) capPoint(b *core.Board) (int32, int32) {
	m := &a.sq[sqMain]
	if len(m.members) > 0 && m.hasCentre && aikit.Dist2(m.cx, m.cz, b.HomeX, b.HomeZ) <= 3000*3000 {
		return (b.HomeX + m.cx) / 2, (b.HomeZ + m.cz) / 2
	}
	return b.HomeX + (a.gx-b.HomeX)/3, b.HomeZ + (a.gz-b.HomeZ)/3
}

// intercept attacks the most pressing visible enemy aircraft the fighters
// can beat: raiders over our base, army, fleet or strike first, then any
// aircraft in reach that is not sheltered by anti-air.
func (a *Army) intercept(b *core.Board, s *squad) bool {
	o := b.O
	var own force
	for _, i := range s.members {
		u := &o.Own[i]
		if a.units[u.H].withdraw != 0 {
			continue
		}
		airForce(&own, u.Info, int64(u.HP))
	}
	if own.dps <= 0 {
		return false
	}
	tol := own.hp / fighterTol
	var best pool.Handle
	var bestG uint32
	var bestP int64
	var bx, bz int32
	curAlive := false
	for i := range o.Enemy {
		c := &o.Enemy[i]
		if c.Info == nil || !c.Visible || !c.Info.Role.Has(aikit.RoleAir) {
			continue
		}
		info := c.Info
		ours := a.overOurs(b, c.X, c.Z)
		d := int64(aikit.Dist(s.cx, s.cz, c.X, c.Z))
		if !ours && d > 2500 {
			continue
		}
		if int64(a.aaAt(c.X, c.Z)) > tol {
			continue // sheltered by anti-air
		}
		w := int64(1000)
		ck := a.cls(info).kind
		switch {
		case ck == ukBomber || ck == ukGunship || ck == ukTorpedo:
			w = 2000
			if ours {
				w = 3000
			}
		case ck == ukAirScout || info.Role.Any(aikit.RoleBuilder|aikit.RoleTransport):
			w = 1500
		}
		p := (int64(info.Value) + 50) * w / (d + 600)
		if c.H == s.focus && c.Gen == s.focusG {
			curAlive = true
			p = p * 3 / 2
		}
		if p > bestP {
			best, bestG, bestP, bx, bz = c.H, c.Gen, p, c.X, c.Z
		}
	}
	if best == 0 {
		s.focus = 0
		return false
	}
	// The fight: enemy aircraft that shoot back near the target plus
	// ground anti-air there.
	var en force
	for i := range o.Enemy {
		c := &o.Enemy[i]
		if c.Info == nil || !c.Info.Role.Has(aikit.RoleAir) || c.Info.AirDPS <= 0 {
			continue
		}
		if aikit.Dist2(c.X, c.Z, bx, bz) <= 900*900 {
			airForce(&en, c.Info, int64(c.Info.HP)*int64(c.HPPct)/100)
		}
	}
	if g := int64(a.aaAt(bx, bz)); g > 0 {
		en.dps += g
		en.hp += blipHP
		en.rngW += g * 600
	}
	r := ratio(&own, &en)
	s.enemy, s.ratio = en, r
	if r < a.retreatMargin(b) {
		return false
	}
	a.setState(b, s, stDefend)
	s.tx, s.tz = bx, bz
	if (best != s.focus || bestG != s.focusG) && curAlive && b.Tick-s.focusTick < 45 {
		best, bestG = s.focus, s.focusG
	}
	if best != s.focus || bestG != s.focusG {
		s.focus, s.focusG = best, bestG
		s.focusTick = b.Tick
		a.stats.intercepts++
	}
	a.collect(b, s.members, okAttack, bx, bz, best, bestG)
	if len(a.buf) == 0 {
		return true
	}
	if a.avail-a.spent < 2 {
		return true
	}
	a.spend(true)
	a.spend(true)
	b.K.Attack(a.buf, best, false)
	b.K.Patrol(a.buf, bx, bz, true)
	a.record(b, okAttack, bx, bz, best, bestG)
	return true
}

// overOurs reports whether a point is over our base, assets, or one of our
// squads in the field.
func (a *Army) overOurs(b *core.Board, x, z int32) bool {
	if a.asset.At(x, z) > 0 || aikit.Dist2(x, z, b.HomeX, b.HomeZ) <= core.BaseRadius*core.BaseRadius {
		return true
	}
	for _, id := range [...]int32{sqMain, sqRaid, sqNaval, sqAmph, sqStrike} {
		q := &a.sq[id]
		if q.active && len(q.members) > 0 && q.hasCentre && aikit.Dist2(x, z, q.cx, q.cz) <= 1200*1200 {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Strike wing.

// strikeCand is one possible strike target.
type strikeCand struct {
	h      pool.Handle
	g      uint32 // instance (Remembered.Gen)
	x, z   int32
	value  int64 // weighted value
	hp     int64
	pre    int64 // cheap prefilter score
	mobile bool
}

// airWeight is what destroying a unit is worth to a strike: economy and
// constructors most, artillery (hard for the ground army to reach) twice,
// the commander only when fresh; anti-air is never a bombing target.
func (a *Army) airWeight(info *aikit.UnitInfo) int64 {
	v := int64(info.Value)
	r := info.Role
	switch {
	case r.Has(aikit.RoleAir):
		return 0
	case r.Has(aikit.RoleCommander):
		return commanderValue
	case info.AirDPS > 0:
		return 0
	case r.Has(aikit.RoleExtractor), r.Has(aikit.RoleBuilder):
		return v * 3
	case r.Any(aikit.RoleEnergy | aikit.RoleMetalMaker):
		return v * 3 / 2
	case r.Has(aikit.RoleArtillery):
		return v * 2
	case r.Any(aikit.RoleFactory | aikit.RoleRadar | aikit.RoleSonar | aikit.RoleStorage):
		return v
	case r.Has(aikit.RoleDefense):
		return v / 2
	}
	return v
}

// wing summarizes the strike wing members present at (x, z).
type wing struct {
	n, value, hp, pass, speed int64
	torpOnly                  bool
}

func (a *Army) wingAt(b *core.Board, s *squad, x, z, radius int32, sortie uint32) wing {
	var w wing
	w.torpOnly = true
	o := b.O
	for _, i := range s.members {
		u := &o.Own[i]
		um := a.unit(u)
		if um.withdraw != 0 {
			continue
		}
		if sortie != 0 && um.sortie != sortie {
			continue
		}
		if sortie == 0 && aikit.Dist2(u.X, u.Z, x, z) > int64(radius)*int64(radius) {
			continue
		}
		c := a.cls(u.Info)
		w.n++
		w.value += int64(u.Info.Value)
		w.hp += int64(u.HP)
		switch c.kind {
		case ukBomber:
			w.pass += int64(c.surfDPS) * 3
			w.torpOnly = false
		case ukGunship:
			w.pass += int64(c.surfDPS) * 6
			w.torpOnly = false
		case ukTorpedo:
			w.pass += int64(u.Info.DPS) * 3
		}
		if sp := int64(u.Info.Speed); w.speed == 0 || sp < w.speed {
			w.speed = sp
		}
	}
	if w.speed < 60 {
		w.speed = 60
	}
	return w
}

// strikeTarget chooses the best target for wing w flying from (fx, fz),
// within maxDist when positive. It fills s.tgtH/tx/tz/enemy and returns
// whether a target clears the margin.
func (a *Army) strikeTarget(b *core.Board, s *squad, w *wing, fx, fz int32, maxDist int64) bool {
	o := b.O
	if w.n == 0 || w.pass <= 0 {
		return false
	}
	// Prefilter: weighted value discounted by distance, top candidates.
	cands := a.scand[:0]
	for i := range o.Memory {
		r := &o.Memory[i]
		info := r.Info
		v := a.airWeight(info)
		if v <= 0 {
			continue
		}
		c := a.cls(info)
		mobile := !r.Building
		if mobile && b.Tick-r.LastSeen > 150 && !info.Role.Has(aikit.RoleCommander) {
			continue // a moving target must be fresh
		}
		naval := c.kind == ukNaval
		if w.torpOnly {
			if !naval {
				continue // torpedoes hit only ships
			}
		} else if naval && c.surfDPS == 0 && c.torp {
			continue // bombs and guns miss submarines
		}
		com := info.Role.Has(aikit.RoleCommander)
		if com && b.Tick-r.LastSeen > comFresh {
			continue
		}
		d := int64(aikit.Dist(fx, fz, r.X, r.Z))
		if maxDist > 0 && d > maxDist {
			continue
		}
		pre := v * 1000 / (d + 1500)
		if len(cands) == maxStrikeCands && pre <= cands[len(cands)-1].pre {
			continue
		}
		sc := strikeCand{h: r.H, g: r.Gen, x: r.X, z: r.Z, value: v, hp: int64(info.HP), pre: pre, mobile: mobile}
		k := len(cands)
		if k < maxStrikeCands {
			cands = append(cands, sc)
		} else {
			k--
		}
		for k > 0 && cands[k-1].pre < pre {
			cands[k] = cands[k-1]
			k--
		}
		cands[k] = sc
	}
	a.scand = cands
	// Current health of the candidates in sight.
	for j := range o.Enemy {
		e := &o.Enemy[j]
		if e.Info == nil || e.HPPct >= 100 {
			continue
		}
		for ci := range cands {
			if cands[ci].h == e.H && cands[ci].g == e.Gen {
				cands[ci].hp = cands[ci].hp * int64(e.HPPct) / 100
				break
			}
		}
	}
	var bestScore int64
	best := -1
	var bestLoss, bestRawLoss, bestRawGain int64
	lossCal, gainCal := a.strikeCal()
	for ci := range cands {
		c := &cands[ci]
		passes := (c.hp + w.pass - 1) / w.pass
		if passes > 3 && !(c.value >= commanderValue && passes <= comPasses) {
			a.noteGoal(b.Tick, s.id, -1, c.x, c.z, c.value, 0, -1)
			continue
		}
		gain := c.value
		// Neighbours within bomb splash that one pass would also destroy
		// add half their worth.
		for j := range o.Memory {
			r := &o.Memory[j]
			if (r.H == c.h && r.Gen == c.g) || !r.Building || int64(r.Info.HP) > w.pass {
				continue
			}
			if aikit.Dist2(r.X, r.Z, c.x, c.z) <= 200*200 {
				gain += a.airWeight(r.Info) / 2
			}
		}
		aaT := max64(int64(a.aaAt(c.x, c.z)), a.aaReserve(b, c.x, c.z))
		aaR := a.aaLineMax(fx, fz, c.x, c.z)
		// Seconds inside anti-air cover: in and out of the cover over the
		// target once per pass, plus a crossing on the way.
		exp := aaT*(aaExposure*passes/w.speed+passes*3) + aaR*2
		if a.enemyAirSup.dps > 0 && aikit.Dist2(a.enemyAirX, a.enemyAirZ, c.x, c.z) <= 2500*2500 {
			esc := &a.sq[sqFighter]
			if !esc.active || esc.present.aa*esc.present.hp < a.enemyAirSup.dps*a.enemyAirSup.hp {
				exp += a.enemyAirSup.dps * 6
			}
		}
		loss := w.value
		if w.hp > 0 && exp*w.value/w.hp < loss {
			loss = exp * w.value / w.hp
		}
		// The model's loss and gain (the target's own value: destroyed
		// targets are counted without their splash), then as calibrated by
		// the sorties flown so far (unseen.go).
		rawLoss, rawGain := loss, c.value
		exp = exp * lossCal / 1000
		if loss = loss * lossCal / 1000; loss > w.value {
			loss = w.value
		}
		gain = gain * gainCal / 1000
		// No sortie is flown expecting to lose more than half the wing,
		// except against an exposed commander (a decisive kill).
		suicide := exp*2 > w.hp && !(c.value >= commanderValue && exp < w.hp)
		d := int64(aikit.Dist(fx, fz, c.x, c.z))
		travel := w.value * d / (w.speed * 900)
		score := int64(-1)
		if gain*1000 >= loss*strikeMargin && !suicide {
			score = gain * 1000 / (loss + travel + 50)
			if c.h == s.tgtH && c.g == s.tgtG {
				score = score * 13 / 10
			}
		}
		rt := int64(ratioCap)
		if loss > 0 {
			rt = gain * 1000 / loss
		}
		a.noteGoal(b.Tick, s.id, a.zoneOf(c.x, c.z), c.x, c.z, gain, rt, score)
		if score > bestScore {
			bestScore, best, bestLoss, bestRawLoss, bestRawGain = score, ci, loss, rawLoss, rawGain
		}
	}
	if best < 0 {
		return false
	}
	c := &cands[best]
	s.tgtH, s.tgtG, s.tx, s.tz, s.tgtGain = c.h, c.g, c.x, c.z, c.value
	s.rawLoss, s.rawGain = bestRawLoss, bestRawGain
	s.ratio = int64(ratioCap)
	if bestLoss > 0 {
		s.ratio = c.value * 1000 / bestLoss
	}
	a.markGoal(s.id, a.zoneOf(c.x, c.z))
	s.expLoss = bestLoss
	return true
}

// memoryOf returns the memory record of a unit (handle and instance), or
// nil: a different unit in the same slot is not the one remembered.
func (a *Army) memoryOf(b *core.Board, h pool.Handle, g uint32) *aikit.Remembered {
	o := b.O
	for i := range o.Memory {
		if o.Memory[i].H == h && o.Memory[i].Gen == g {
			return &o.Memory[i]
		}
	}
	return nil
}

func (a *Army) runStrike(b *core.Board, s *squad) {
	rx, rz := a.airRally(b)
	switch s.state {
	case stApproach, stEngage:
		a.runSortie(b, s, rx, rz)
		return
	case stRetreat:
		if aikit.Dist2(s.cx, s.cz, rx, rz) <= rallyRadius*rallyRadius || b.Tick-s.since > retreatWait {
			a.noteStrike(b, s) // the sortie is home: what it cost, losses on the way back included
			a.setState(b, s, stGather)
			break
		}
		a.issue(b, s.members, okMove, rx, rz, 0, true)
		return
	}
	// Gather: wait at the rally until the wing is complete, then strike.
	if s.state != stGather {
		a.setState(b, s, stGather)
	}
	w := a.wingAt(b, s, rx, rz, rallyRadius, 0)
	total := int64(len(s.members))
	ready := w.n >= minStrikeWing || (w.n >= 2 && w.n == total && b.Tick-s.since > strikeWaitMax)
	if ready && (b.Tick-s.lastRouteCheck >= 60 || s.tgtH != 0) {
		s.lastRouteCheck = b.Tick
		if a.strikeTarget(b, s, &w, rx, rz, 0) {
			a.launchStrike(b, s, rx, rz)
			return
		}
		s.tgtH = 0
	}
	a.issue(b, s.members, okMove, rx, rz, 0, false)
}

// launchStrike sends the members present at the rally, routed around
// remembered anti-air, to attack the chosen target.
func (a *Army) launchStrike(b *core.Board, s *squad, rx, rz int32) {
	o := b.O
	a.tmp = a.tmp[:0]
	for _, i := range s.members {
		u := &o.Own[i]
		um := a.unit(u)
		if um.withdraw != 0 || aikit.Dist2(u.X, u.Z, rx, rz) > rallyRadius*rallyRadius {
			continue
		}
		a.tmp = append(a.tmp, i)
	}
	if len(a.tmp) == 0 {
		return
	}
	a.airRoute(b, s, rx, rz, s.tx, s.tz)
	n := int64(s.nwp) + 1
	if a.avail-a.spent-a.prodReserve < n {
		if a.avail-a.spent-a.prodReserve < 1 {
			return
		}
		s.nwp = 0
		n = 1
	}
	a.buf = a.buf[:0]
	a.bufIdx = a.bufIdx[:0]
	for _, i := range a.tmp {
		a.buf = append(a.buf, o.Own[i].H)
		a.bufIdx = append(a.bufIdx, i)
		a.unit(&o.Own[i]).sortie = b.Tick
	}
	k := b.K
	for i := int32(0); i < s.nwp; i++ {
		a.spend(false)
		k.Move(a.buf, s.wps[i][0], s.wps[i][1], i > 0)
	}
	a.spend(false)
	k.Attack(a.buf, s.tgtH, s.nwp > 0)
	a.record(b, okAttack, s.tx, s.tz, s.tgtH, s.tgtG)
	s.sortie = b.Tick
	w := a.wingAt(b, s, 0, 0, 0, s.sortie)
	s.launch = w.hp
	s.launchValue, s.sortieGain = w.value, 0
	s.launchLoss, s.launchGain = s.rawLoss, s.rawGain
	a.setState(b, s, stApproach)
	a.stats.strikes++
}

// airRoute fills s.wps with at most maxWaypoints turns around remembered
// anti-air when that is clearly cheaper than the straight line.
func (a *Army) airRoute(b *core.Board, s *squad, fx, fz, tx, tz int32) {
	s.nwp = 0
	s.routed = false
	if !a.P.Route || a.aaLineMax(fx, fz, tx, tz) == 0 {
		return
	}
	m := b.K.Map
	for i := range a.rt.cost {
		c := int64(10) + int64(max32(a.aaMem.V[i], a.aa.V[i]))*4
		if c > 2000 {
			c = 2000
		}
		a.rt.cost[i] = int32(c)
	}
	// The target's own cover cannot be avoided.
	from, to := m.Sector(fx, fz), m.Sector(tx, tz)
	if from == to {
		return
	}
	direct := a.rt.lineCost(m, fx, fz, tx, tz)
	cost := a.rt.search(from, to)
	if cost >= routeInf || int64(cost)*10 >= int64(direct)*7 {
		return
	}
	for i := range a.airOK {
		a.airOK[i] = 1
	}
	a.routeInto(m, s, a.airOK)
}

// runSortie follows a strike in progress: chain to the next target over
// the area, abort when the wing is spent or the anti-air turned out worse
// than predicted, and fly home when done.
func (a *Army) runSortie(b *core.Board, s *squad, rx, rz int32) {
	o := b.O
	w := a.wingAt(b, s, 0, 0, 0, s.sortie)
	if w.n == 0 {
		a.endStrike(b, s)
		return
	}
	// Late members wait at the rally; they do not trickle in.
	a.tmp = a.tmp[:0]
	a.tmp2 = a.tmp2[:0]
	for _, i := range s.members {
		u := &o.Own[i]
		um := a.unit(u)
		if um.withdraw != 0 {
			continue
		}
		if um.sortie == s.sortie {
			a.tmp2 = append(a.tmp2, i)
		} else {
			a.tmp = append(a.tmp, i)
		}
	}
	if len(a.tmp) > 0 {
		a.issue(b, a.tmp, okMove, rx, rz, 0, false)
	}
	near := aikit.Dist2(s.cx, s.cz, s.tx, s.tz) <= 900*900
	if near && s.state == stApproach {
		a.setState(b, s, stEngage)
	}
	if w.hp*1000 < s.launch*strikeRetreat {
		a.stats.strikeAborts++
		a.endStrike(b, s)
		return
	}
	if s.state == stEngage && b.Tick-s.since > strikeLinger {
		a.endStrike(b, s)
		return
	}
	if s.state == stApproach && b.Tick-s.since > 2700 {
		a.endStrike(b, s)
		return
	}
	if r := a.memoryOf(b, s.tgtH, s.tgtG); r != nil {
		s.tx, s.tz = r.X, r.Z
		// Re-judge with what is known now: abort when the anti-air around
		// the target turned out far worse than the launch expected.
		if aaT := int64(a.aaAt(r.X, r.Z)); aaT > 0 && w.hp > 0 {
			loss := aaT * (aaExposure/w.speed + 3) * w.value / w.hp
			if loss > w.value {
				loss = w.value
			}
			if loss > s.expLoss*2+50 && loss*strikeMargin > a.airWeight(r.Info)*1000 {
				a.stats.strikeAborts++
				a.endStrike(b, s)
				return
			}
		}
		a.withdrawDamaged(b, s)
		a.tmp2 = a.notWithdrawn(b, a.tmp2) // not another attack run for the hurt
		a.collect(b, a.tmp2, okAttack, s.tx, s.tz, s.tgtH, s.tgtG)
		if len(a.buf) > 0 && a.spend(true) {
			b.K.Attack(a.buf, s.tgtH, false)
			a.record(b, okAttack, s.tx, s.tz, s.tgtH, s.tgtG)
		}
		return
	}
	// Destroyed (or gone): the next target over the area, else home.
	a.stats.strikeKills++
	s.sortieGain += s.tgtGain
	s.tgtH = 0
	if a.strikeTarget(b, s, &w, s.cx, s.cz, strikeChain) {
		a.buf = a.buf[:0]
		a.bufIdx = a.bufIdx[:0]
		for _, i := range a.tmp2 {
			a.buf = append(a.buf, o.Own[i].H)
			a.bufIdx = append(a.bufIdx, i)
		}
		if len(a.buf) > 0 && a.spend(true) {
			b.K.Attack(a.buf, s.tgtH, false)
			a.record(b, okAttack, s.tx, s.tz, s.tgtH, s.tgtG)
		}
		return
	}
	a.endStrike(b, s)
}

// endStrike flies the wing back to the rally.
func (a *Army) endStrike(b *core.Board, s *squad) {
	rx, rz := a.airRally(b)
	s.tgtH = 0
	a.setState(b, s, stRetreat)
	s.sx, s.sz = rx, rz
	a.issue(b, s.members, okMove, rx, rz, 0, true)
}

// ---------------------------------------------------------------------------
// Air scouts.

// airScouts fly idle unarmed aircraft over what is least recently seen and
// worth seeing — unexplored start positions, the enemy's zones, far metal
// spots — away from remembered anti-air.
func (a *Army) airScouts(b *core.Board) {
	o := b.O
	m := b.K.Map
	for i := range o.Own {
		u := &o.Own[i]
		if !u.Built || a.cls(u.Info).kind != ukAirScout || u.Info.Role.Any(aikit.RoleBuilder|aikit.RoleTransport) {
			continue
		}
		if u.Tag != tagAirScout {
			b.K.SetTag(u.H, tagAirScout)
		}
		um := a.unit(u)
		if u.Order != aikit.OrderIdle && b.Tick-um.ordTick < 1800 {
			continue
		}
		tol := int64(u.HP) / 20
		var bx, bz int32
		var bestS int64 = -1
		consider := func(x, z int32, weight int64) {
			zi := a.zoneOf(x, z)
			age := int64(b.Tick - a.zoneSeen[zi])
			if a.zoneSeen[zi] != 0 && age < scoutStale {
				return
			}
			if age > 18000 || a.zoneSeen[zi] == 0 {
				age = 18000
			}
			aa := int64(a.aaAt(x, z))
			if aa > tol {
				return
			}
			d := int64(aikit.Dist(u.X, u.Z, x, z))
			sc := age * weight / (d + 2000) * 100 / (100 + aa*10)
			if sc > bestS {
				bestS, bx, bz = sc, x, z
			}
		}
		for si := range m.Starts {
			st := &m.Starts[si]
			if aikit.Dist2(st[0], st[1], b.HomeX, b.HomeZ) < 800*800 || !m.MaybeEnemyStart(si) {
				continue
			}
			consider(st[0], st[1], 3)
		}
		for _, zi := range a.candidates {
			z := &a.zones[zi]
			consider(z.x, z.z, 2)
		}
		for si := range m.Spots {
			sp := &m.Spots[si]
			if b.Spots[si] == core.SpotOurs || aikit.Dist2(sp.X, sp.Z, b.HomeX, b.HomeZ) < 1500*1500 {
				continue
			}
			consider(sp.X, sp.Z, 1)
		}
		if bestS < 0 {
			continue
		}
		one := a.tmp[:0]
		one = append(one, int32(i))
		a.issue(b, one, okMove, bx, bz, 0, false)
	}
}
