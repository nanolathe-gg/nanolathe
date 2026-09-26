package utility

import (
	"strconv"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Census: instrumentation for the arena's Report beside the tower audit
// (defense_audit.go), never read by a decision. It answers where the
// income goes once the defense plan is on:
//
//   - at fixed minutes, the towers standing (a missile tower once) and
//     their value, and the factories working (built, not written off);
//   - every builder order, by what it built and by whether the metal
//     store was banking when it was given (bankPercent full while income
//     covers expense), per audit phase;
//   - how many of the towers built ever had an enemy unit their fire can
//     engage within reach (sampled every censusEvery ticks), and the share
//     of tower samples that did: a tower nothing ever comes near spent its
//     cost for its score value alone.

// censusMin are the minutes the census is taken at.
var censusMin = [...]int32{5, 10, 15, 20, 25, 30, 35, 40}

// censusEvery is the engagement sampling interval (ticks).
const censusEvery = 150

// censusKinds are the builder orders the census counts.
var censusKinds = [...]commitKind{cMex, cEnergy, cMaker, cStorage, cFactory, cDefense, cRadar, cUpgrade, cAssist, cGuard, cClear}

// towerSeen is one tower slot's engagement record (by handle).
type towerSeen struct {
	def     *aikit.UnitInfo
	gen     uint32
	engaged bool
}

// towerCensus is the census's state and counters.
type towerCensus struct {
	next    int
	towers  [len(censusMin)]int32
	value   [len(censusMin)]int64
	facs    [len(censusMin)]int32
	bankWas bool // the store banked at the last think (for the orders it gave)
	orders  [3][2][len(censusKinds)]int32

	sampleT     uint32
	seen        []towerSeen
	nSeen, nHit int32
	samples     [3]int64
	hits        [3]int64
}

// census takes this think's census (auditThink, every think with the plan
// on).
func (pl *defPlan) census(s *shared, b *core.Board) {
	c := &s.zones.audit.cen
	o := b.O
	// Orders given at the last think, by kind and banking state then.
	if s.lastThink != 0 {
		ph := owedPhase(s.lastThink)
		bank := 0
		if c.bankWas {
			bank = 1
		}
		for _, i := range b.Builders {
			u := &o.Own[i]
			cm := s.commitIf(u)
			if cm == nil || cm.tick != s.lastThink {
				continue
			}
			for k, kind := range censusKinds {
				if cm.kind == kind {
					c.orders[ph][bank][k]++
				}
			}
		}
	}
	c.bankWas = s.mCap > 0 && s.mStock*100 >= s.mCap*bankPercent && s.mInc >= s.mExp
	for c.next < len(censusMin) && s.tick >= uint32(censusMin[c.next])*1800 {
		var n int32
		var v int64
		for _, i := range b.Defenses {
			u := &o.Own[i]
			if pl.cls[u.Info.Index] == dcNone {
				continue
			}
			n++
			v += s.info[u.Info.Index].costMeq
		}
		c.towers[c.next], c.value[c.next] = n, v
		c.facs[c.next] = int32(len(b.Factories)) - s.deadFacs
		c.next++
	}
	if s.tick < c.sampleT+censusEvery && c.sampleT != 0 {
		return
	}
	c.sampleT = s.tick
	ph := owedPhase(s.tick)
	for _, i := range b.Defenses {
		u := &o.Own[i]
		cl := pl.cls[u.Info.Index]
		if cl == dcNone {
			continue
		}
		for len(c.seen) <= int(u.H) {
			c.seen = append(c.seen, towerSeen{})
		}
		ts := &c.seen[u.H]
		if ts.def != u.Info || ts.gen != u.Gen {
			*ts = towerSeen{def: u.Info, gen: u.Gen}
			c.nSeen++
		}
		c.samples[ph]++
		if !pl.inReach(b, u, cl) {
			continue
		}
		c.hits[ph]++
		if !ts.engaged {
			ts.engaged = true
			c.nHit++
		}
	}
}

// inReach reports whether an enemy unit the tower's fire can engage stands
// within its range: aircraft for an anti-air tower, ground units (and, for
// a missile tower, aircraft too) otherwise.
func (pl *defPlan) inReach(b *core.Board, u *aikit.OwnUnit, cl uint8) bool {
	r := int64(u.Info.Range) + 32
	for i := range b.O.Enemy {
		e := &b.O.Enemy[i]
		if e.Info == nil || !e.Visible || !e.Info.Role.Has(aikit.RoleMobile) {
			continue
		}
		air := e.Info.Role.Has(aikit.RoleAir)
		if air && cl != dcAir && !pl.dual[u.Info.Index] {
			continue
		}
		if !air && cl == dcAir && !pl.dual[u.Info.Index] {
			continue
		}
		if aikit.Dist2(u.X, u.Z, e.X, e.Z) <= r*r {
			return true
		}
	}
	return false
}

// report publishes the census.
func (c *towerCensus) report(add func(name string, value int64)) {
	for i, m := range censusMin {
		if i >= c.next {
			break
		}
		ms := strconv.Itoa(int(m))
		add("census_towers_"+ms, int64(c.towers[i]))
		add("census_tower_value_"+ms, c.value[i])
		add("census_facs_"+ms, int64(c.facs[i]))
	}
	for ph, name := range owedPhases {
		for bank, bn := range [2]string{"free", "bank"} {
			for k, kind := range censusKinds {
				if n := c.orders[ph][bank][k]; n > 0 {
					add("census_orders_"+name+"_"+bn+"_"+commitNames[kind], int64(n))
				}
			}
		}
		add("census_tower_samples_"+name, c.samples[ph])
		add("census_tower_hits_"+name, c.hits[ph])
	}
	add("census_towers_seen", int64(c.nSeen))
	add("census_towers_engaged", int64(c.nHit))
}
