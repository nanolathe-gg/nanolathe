package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Production kinds share the decision record with the economy.
const (
	pCons commitKind = 20 + iota
	pCombat
	pScout
)

// queueDepth keeps each factory at most this many products deep (the one
// in production plus one queued), so choices track the situation.
const queueDepth = 2

// factoryReg remembers the last product this brain queued at a factory.
type factoryReg struct {
	def  *aikit.UnitInfo
	gen  uint32
	prod *aikit.UnitInfo
	tick uint32
}

// Production scores every product of each factory that has room in its
// queue: constructors for unmet economy spending or expansion room, combat
// units by cost-efficiency with counters to what was seen, one scout.
type Production struct {
	s         *shared
	ring      [8]decision
	next      int
	reg       []factoryReg
	scouts    int32 // scouts ever queued
	airScouts int32 // unarmed air scouts ever queued
	cleared   int32 // blocked pads cleared
	// combatQueued counts combat products (not scouts) ever queued
	// (open_army part 2).
	combatQueued int32
	buf          []pool.Handle
}

// maxScouts bounds scouts over a game: a scout that keeps dying is not
// finding anything new.
const maxScouts = 3

func (pr *Production) Init(b *core.Board) { pr.s.setup(b.K) }

func (pr *Production) regOf(u *aikit.OwnUnit) *factoryReg {
	pr.reg = handleSlots(pr.reg, u.H, pr.s.hcap)
	r := &pr.reg[u.H]
	if r.def != u.Info || r.gen != u.Gen {
		*r = factoryReg{def: u.Info, gen: u.Gen}
	}
	return r
}

func (pr *Production) Plan(b *core.Board) {
	s := pr.s
	k := b.K
	budget := int(s.budget) - k.Emitted()
	if budget > 0 {
		budget -= pr.clearPads(b, budget)
	}
	if budget > 0 {
		pr.produce(b, budget)
	}
	s.spendAPM(k.Emitted())
}

// clearPads moves own units parked on the pad of a factory that has
// stopped starting units out along its exit lane (+Z, the side the executor
// keeps clear). It returns the actions used.
func (pr *Production) clearPads(b *core.Board, budget int) int {
	s := pr.s
	o := b.O
	used := 0
	for _, fi := range b.Factories {
		if used >= budget {
			break
		}
		f := &o.Own[fi]
		fs := s.fstateOf(f)
		if !fs.blocked || s.tick-fs.lastClear < 300 {
			continue
		}
		pr.buf = pr.buf[:0]
		for i := range o.Own {
			u := &o.Own[i]
			if u.Built && u.Info.Role.Has(aikit.RoleMobile) && onPad(f, u.X, u.Z) {
				pr.buf = append(pr.buf, u.H)
			}
		}
		fs.lastClear = s.tick
		if fs.clears < 250 {
			fs.clears++
		}
		if len(pr.buf) == 0 || fs.dead {
			continue
		}
		m := b.K.Map
		ex := f.X
		ez := clampWorld(f.Z+f.Info.FootZ*8+128, m.WorldH)
		b.K.Move(pr.buf, ex, ez, false)
		pr.cleared++
		used++
	}
	return used
}

func (pr *Production) produce(b *core.Board, budget int) {
	s := pr.s
	sp := &s.p
	o := b.O
	k := b.K
	// Constructors queued but not yet framed.
	var queued int32
	for _, fi := range b.Factories {
		f := &o.Own[fi]
		r := pr.regOf(f)
		if r.prod != nil && f.QueueLen >= 1 && s.tick-r.tick < 1800 && s.info[r.prod.Index].canEco {
			queued++
		}
	}
	pending := s.consPending
	if queued > pending {
		pending = queued
	}
	eco := int64(b.Posture.EcoShare)
	threatened := b.NearHomeThreat > 0
	ext := sp.Naval != 0 || sp.Air != 0 || sp.Tech != 0
	// Army share, doubled while the army is below the size the strategy
	// wants it to reach (deficit falls linearly to zero at that size).
	deficit := lin(int64(b.ArmyValue), s.armyTarget, 0)
	armyF := clamp(mul((100-eco)*20, one+deficit), 200, 2400)
	// Energy gates: combat pauses before an energy stall (which would also
	// stop extraction); constructors pause only in a deep one.
	gate := lin(s.covE, 600, 900)
	if s.eStock*20 < s.eCap && s.eInc < s.eExp {
		gate = 0
	}
	consGate := lin(s.covE, 300, 600)
	if threatened {
		armyF = max64(armyF, 1200)
		gate, consGate = one, one
	}
	ecoCap := s.bpBld * s.rMB / 1000
	desiredEco := s.spendable * eco / 100
	guard := int8(-1) // reach_mix: whether the stranded home guard is full (-1 = not yet asked)
	// growth: constructors to the human ratio, from surplus only.
	// growth: constructors to the human ratio from surplus (steep beyond
	// it), and a floor of one or two on any map (econ_growth.go).
	steep := s.part(gConstructors) && s.growSafe() && s.surplus()
	var consT int64
	if steep {
		consT = s.consTarget()
	}
	if s.part(gConsFloor) {
		consT = max64(consT, s.consFloor())
	}
	// expand: constructors follow the expansion (econ_expand.go).
	consT = max64(consT, s.consExpand())
	// open_army: scouts wait for the first combat unit, and combat units
	// follow the first constructor (opening.go).
	scoutWait := s.scoutsWait(b, pr)
	armyFirst := s.armyFirst(pr, pending)
	for _, fi := range b.Factories {
		if budget <= 0 {
			break
		}
		f := &o.Own[fi]
		if f.QueueLen >= queueDepth || s.fstateOf(f).blocked {
			continue
		}
		builders := int64(s.builders + pending)
		expand := clamp(int64(s.freeSafe)*1000/max64(builders*int64(sp.SpotsPerCon), 1), 0, 1200)
		// Normalizer: the best combat efficiency this factory offers now.
		var bestEff int64 = 1
		if ext {
			bestEff = pr.bestEffN(f.Info)
		} else {
			for _, q := range f.Info.Builds {
				if s.info[q.Index].combat {
					if ef := s.eff(q, s.aaNeed); ef > bestEff {
						bestEff = ef
					}
				}
			}
		}
		// How far this factory's army reaches the enemy: a factory whose
		// units cannot get there makes combat units only to defend.
		freach := s.factoryReach(f.Info)
		// reach_mix: a stranded factory adds combat units only to a home
		// guard (econ_reach.go).
		noArmy := false
		if sp.ReachMix != 0 && s.reach != nil && freach < strandReach {
			if guard < 0 {
				guard = 0
				if s.homeGuardFull(b) {
					guard = 1
				}
			}
			noArmy = guard == 1
		}
		var d decision
		d.reset(s.tick, f.Info, f.X, f.Z)
		for _, q := range f.Info.Builds {
			si := &s.info[q.Index]
			if !q.Role.Has(aikit.RoleMobile) {
				continue
			}
			var c cand
			c.prod, c.spot = q, -1
			switch {
			case si.canEco:
				drain := max64(int64(q.BuildPower)*s.rMB/1000, 1)
				gap := clamp((desiredEco-ecoCap)*1000/drain, 0, 1200)
				room := expand
				if sp.Naval != 0 && s.terr.ready {
					// The room this constructor type would have (its class's
					// reachable spots and water work), not our builders'.
					room = clamp(int64(s.consRoom[q.Index])*1000/max64(builders*int64(sp.SpotsPerCon), 1), 0, 1200)
				}
				need := max64(gap, room)
				// Each builder already owned or coming makes the next worth less.
				dim := half(int64(s.cons+pending)*1000, 4000)
				// growth: below the target a constructor is wanted
				// outright; beyond the ratio target each one more is worth
				// less (econ_growth.go).
				if short := consT - int64(s.cons+pending)*1000; short > 0 {
					need = max64(need, one+min64(short, 2000)/2)
					dim = one
				} else if steep {
					dim = half(one-short, 500)
				}
				c.kind = pCons
				c.score = mul(mul(mul(int64(sp.WCons)*10, need), dim), consGate)
				if armyFirst && !threatened {
					// Combat units first (open_army part 2): a constructor
					// still goes ahead when no combat unit can be made.
					c.score /= armyFirstCut
				}
				c.f = [4]int64{need, dim, consGate, int64(s.cons + pending)}
				if sp.Tech != 0 && si.advCon {
					// The tech plan wants a few advanced builders however
					// many ordinary ones there are (its own count decays it).
					if w := mul(mul(int64(sp.WCons)*10, s.advConsWanted()), consGate); w > c.score {
						c.score = w
						c.f = [4]int64{s.advConsWanted(), one, consGate, int64(s.tech.advCons)}
					}
				}
			case sp.Air != 0 && q.Role.Has(aikit.RoleScout|aikit.RoleAir) && !q.Role.Has(aikit.RoleCombat):
				// An air scout sees what land scouts cannot reach.
				if scoutWait || s.airScouts > 0 || s.minutes < 2 || pr.airScouts >= maxScouts || (b.EnemyKnown && s.minutes >= 10) {
					break
				}
				need := int64(one)
				if !b.EnemyKnown {
					need = 2 * one
				}
				c.kind = pScout
				c.score = mul(int64(sp.WScout)*10, need)
				c.f = [4]int64{need, 0, 0, 0}
			case q.Role.Has(aikit.RoleScout) && q.Role.Has(aikit.RoleCombat):
				if scoutWait || s.scouts > 0 || s.minutes < 1 || pr.scouts >= maxScouts || (b.EnemyKnown && s.minutes >= 10) {
					break
				}
				need := int64(one)
				if !b.EnemyKnown {
					need = 2 * one
				}
				c.kind = pScout
				c.score = mul(int64(sp.WScout)*10, need)
				c.f = [4]int64{need, 0, 0, 0}
			case ext && si.combatN && (sp.Naval != 0 || si.combat):
				if noArmy {
					break
				}
				c.kind = pCombat
				c.score, c.f = pr.combatScoreN(q, bestEff, armyF, gate, freach)
			case !ext && si.combat:
				c.kind = pCombat
				c.score, c.f = pr.combatScore(q, bestEff, armyF, gate)
			}
			if c.score > 0 {
				d.offer(&c)
			}
		}
		if d.n == 0 || d.top[0].score < minScore {
			continue
		}
		best := &d.top[0]
		k.Produce(f.H, best.prod, 1)
		budget--
		r := pr.regOf(f)
		r.prod, r.tick = best.prod, s.tick
		s.count[best.prod.Index]++
		switch best.kind {
		case pCons:
			pending++
			armyFirst = s.armyFirst(pr, pending)
			ecoCap += int64(best.prod.BuildPower) * s.rMB / 1000
		case pCombat:
			s.armyCount++
			scoutWait = false
			pr.combatQueued++
			armyFirst = s.armyFirst(pr, pending)
		case pScout:
			if best.prod.Role.Has(aikit.RoleCombat) {
				s.scouts++
				pr.scouts++
			} else {
				s.airScouts++
				pr.airScouts++
			}
		}
		d.chosen, d.note = 0, noteAssign
		pr.ring[pr.next] = d
		pr.next = (pr.next + 1) % len(pr.ring)
	}
}

// combatScore: army share × efficiency (normalized to the factory's best)
// × anti-air counter × range counter × variety × energy gate.
func (pr *Production) combatScore(q *aikit.UnitInfo, bestEff, armyF, gate int64) (int64, [4]int64) {
	s := pr.s
	sp := &s.p
	eff := s.eff(q, s.aaNeed) * 1000 / bestEff
	counter := int64(one)
	if q.AirDPS > 0 && s.aaNeed > 0 {
		aaQ := min64(int64(q.AirDPS)*1000/max64(int64(q.DPS), 1), 1000)
		counter += mul(mul(s.aaNeed, aaQ), int64(sp.WAA)*20)
	}
	if s.defShare > 0 {
		counter += mul(mul(s.defShare, lin(int64(q.Range), 300, 800)), int64(sp.WRange)*20)
	}
	share := int64(s.count[q.Index]) * 1000 / int64(s.armyCount+1)
	mix := half(share*int64(sp.WMix)/100, 300)
	sc := mul(mul(mul(mul(mul(int64(sp.WArmy)*10, armyF), eff), counter), mix), gate)
	return sc, [4]int64{armyF, eff, counter, mix}
}

// bestEffN is the best reach-weighted efficiency among a factory's combat
// products (the switches' normalizer).
func (pr *Production) bestEffN(f *aikit.UnitInfo) int64 {
	s := pr.s
	var best int64 = 1
	for _, q := range f.Builds {
		si := &s.info[q.Index]
		if !si.combatN || (s.p.Naval == 0 && !si.combat) {
			continue
		}
		if ef := s.effFleet(q, s.aaNeed) * max64(s.reachOf(q), 150) / one; ef > best {
			best = ef
		}
	}
	return best
}

// combatScoreN is combatScore with the switches' model: efficiency counts
// torpedoes only against ships and is weighted by how far the unit
// reaches the enemy (normalized to the factory's best), and the whole score
// by how far the factory's army reaches (a factory whose units cannot get
// there makes combat units only to defend).
func (pr *Production) combatScoreN(q *aikit.UnitInfo, bestEff, armyF, gate, freach int64) (int64, [4]int64) {
	s := pr.s
	sp := &s.p
	if sp.Naval != 0 && s.navalIdle >= navalIdleMax && (q.Role.Has(aikit.RoleNaval) || s.info[q.Index].water) {
		// Ships already launched are not being used: make no more.
		return 0, [4]int64{}
	}
	if s.fleetWar() && s.info[q.Index].light && s.lightFull() {
		// Light boats beyond the few kept for scouting and raiding.
		return 0, [4]int64{}
	}
	eff := s.effFleet(q, s.aaNeed) * max64(s.reachOf(q), 150) / one * 1000 / bestEff
	counter := int64(one)
	if q.AirDPS > 0 && s.aaNeed > 0 {
		aaQ := min64(int64(q.AirDPS)*1000/max64(int64(q.DPS), 1), 1000)
		counter += mul(mul(s.aaNeed, aaQ), int64(sp.WAA)*20)
	}
	if s.defShare > 0 {
		counter += mul(mul(s.defShare, lin(int64(q.Range), 300, 800)), int64(sp.WRange)*20)
	}
	share := int64(s.count[q.Index]) * 1000 / int64(s.armyCount+1)
	mix := half(share*int64(sp.WMix)/100, 300)
	sc := mul(mul(mul(mul(mul(mul(int64(sp.WArmy)*10, armyF), eff), counter), mix), gate), freach)
	return sc, [4]int64{armyF, eff, counter, mix}
}
