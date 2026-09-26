package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Proactive defense plan (def_plan=1). Static defense is budgeted rather
// than only answered (README §3 "Defense plan"):
//
//   - How much: a ground budget accrues as a share of income — w_defense/20
//     × (1.5% until minute 12, the expansion race, rising to 7% by minute
//     25), i.e. 4.5% to 21% at the default — banked to at most four light
//     towers ahead, halved while energy is short, raised by raids
//     (buildings lost, danger at our buildings) and by an outnumbered
//     army, lowered while ours dominates; a lost building also funds half
//     its cost. An opening floor asks for a light tower by 5:00 and, once
//     the approaches have proven exposed, a second by 10:00. Anti-air
//     follows the enemy air threat and, once aircraft are possible, 40% of
//     the ground towers' value (at least a two-tower screen by minute 20);
//     water towers a seen navy and, with the naval switch, danger at the
//     naval base or a water extractor (they stand at the naval base).
//     Towers wait out an energy shortage unless their zone was raided.
//   - Where: own buildings are grouped into 512-wu zones. The next tower
//     goes to the zone with the most exposure-weighted assets per tower
//     already there (extractors weigh most, then factories; exposure rises
//     toward the enemy and with the raids and threat the zone has seen;
//     away from home a pair comes before a new strongpoint), ahead of the
//     zone's front building toward the observed approach, in lateral
//     slots 160 wu from other towers and 96 wu from other buildings, off
//     the corridors our ground units keep crossing, never beside a factory
//     or in the corridor in front of its exit. Under the layout switch the
//     executor places a tower at the nearest cell to that point that keeps
//     2 cells from everything (never packed into a row), stays out of
//     ramps and passes, and passes the exit guard.
//   - What: each product the builder can make is scored by a quality blend
//     that favours cheap towers on a small income and heavier and
//     longer-ranged ones as income grows, with a variety term so a mix
//     builds up, relative to the builder's own best option.
//
// With the layout switch also on (the default) the front rules in
// defense_front.go replace the budget's share curve, let missile towers
// serve the ground plan and hold direct-fire towers to the humans' share.
// With def_plan=0 the reactive evaluation in economy.go (evalDefense)
// scores defenses instead, unchanged.
func (e *Economy) defenseCand(b *core.Board, u *aikit.OwnUnit, p *aikit.UnitInfo) cand {
	if e.s.p.DefPlan == 0 {
		return e.evalDefense(b, u, p)
	}
	pl := e.plan()
	pl.refresh(e.s, b)
	return e.planCand(pl, b, u, p)
}

// planCand scores one defense product for one builder against the plan:
// w_defense × need × zone urgency × quality × variety (together, relative
// to the builder's best option) × affordability (relative to the class's
// cheapest tower) × travel × site threat.
func (e *Economy) planCand(pl *defPlan, b *core.Board, u *aikit.OwnUnit, p *aikit.UnitInfo) cand {
	c := e.planCandCls(pl, b, u, p, pl.cls[p.Index])
	if pl.dual[p.Index] {
		// A missile tower is scored for the ground plan too (front rules).
		if g := e.planCandCls(pl, b, u, p, dcGround); g.score > c.score {
			c = g
		}
	}
	return c
}

// planCandCls scores one product for one class of the plan.
func (e *Economy) planCandCls(pl *defPlan, b *core.Board, u *aikit.OwnUnit, p *aikit.UnitInfo, cl uint8) cand {
	s := e.s
	sp := &s.p
	c := cand{kind: cDefense, prod: p, spot: -1, spacing: 2}
	if cl == dcNone || pl.zone[cl] < 0 || pl.deficit[cl] <= 0 {
		return c
	}
	// Need: how many of the class's cheapest towers the plan is behind,
	// 1.5 per tower, capped, and up to twice that once overdue. The budget
	// is already a share of income, so affordability only ranks products
	// against the class's cheapest.
	need := clamp(pl.deficit[cl]*1500/pl.cRef[cl], 0, 4000)
	if need < 750 {
		return c // less than half a tower behind
	}
	var tp *towerPrice // the tower audit's record (defense_audit.go; instrumentation)
	if cl == dcGround {
		tp = pl.auditPrice(s, u)
		if pl.comLeaves(u) {
			tp.com = true
			return c
		}
	}
	need = mul(need, pl.overdue(s, cl))
	// Towers cost several times more energy than metal: they wait out an
	// energy shortage rather than stall the factories and constructors
	// (a raided zone's tower does not wait).
	needE := need
	if pl.urg[cl] <= one {
		needE = mul(need, lin(s.covE, 400, 800))
	}
	// An opening tower (asked for by the floor, not the budget) waits until
	// the metal store covers its metal: drained at minute five, the store
	// would stall constructor production for a minute.
	if cl == dcGround && pl.budG < pl.floorG(s) && s.mStock < int64(p.Metal) {
		tp.metal = true
		return c
	}
	x, z := pl.x[cl], pl.z[cl]
	if u.Info.Role.Has(aikit.RoleCommander) && aikit.Dist2(x, z, b.HomeX, b.HomeZ) > int64(sp.ComRadius)*int64(sp.ComRadius) {
		if tp != nil {
			tp.com = true
		}
		return c
	}
	vr := blend(s)
	bestQ, bestQV := pl.norms(s, u.Info, cl, vr)
	q := quality(s, p, cl, vr) * one / bestQ
	if q < defMinQ {
		return c
	}
	qv := mul(mul(q, pl.varietyOf(p, cl)), pl.mixOf(p, cl)) * one / bestQV
	aff := e.afford(p) * one / max64(e.affordCost(pl.cRef[cl]), 1)
	dist := int64(aikit.Dist(u.X, u.Z, x, z))
	travel := half(dist/speedOf(u.Info), int64(sp.TravelHalf))
	// A frame started where enemy fire reaches is lost with everything
	// spent on it.
	thr := half(int64(b.Threat.At(x, z)), int64(sp.ThreatHalf))
	c.x, c.z = x, z
	w := int64(sp.WDefense) * defWeight
	c.score = mul(mul(mul(mul(mul(mul(w, needE), pl.urg[cl]), qv), aff), travel), thr)
	c.score = mul(c.score, s.towerYield(cl, needE, pl.urg[cl])) // army: towers yield to production (army.go)
	c.f = [4]int64{needE, qv, aff, travel}
	if tp != nil {
		tp.scored = true
		tp.score = max64(tp.score, c.score)
		noE := c.score
		if needE < need {
			tp.energyShort = true
			noE = mul(mul(mul(mul(mul(mul(w, need), pl.urg[cl]), qv), aff), travel), thr)
		}
		tp.noE = max64(tp.noE, noE)
	}
	return c
}

// affordCost is afford for a purchase of cost-equivalent v.
func (e *Economy) affordCost(v int64) int64 {
	s := e.s
	supply := s.supplyM + s.supplyE*10/int64(s.p.ERatio)
	return half(v*1000/max64(supply, 1), affordHalf)
}
