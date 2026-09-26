package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Tower audit: instrumentation for the arena's Report, never read by a
// decision. Every think the defense plan owes a ground-plan tower — its
// ground deficit is at least half the class's reference tower, the least
// need planCand acts on — is resolved at the next think, once the
// builders' choices of that think are known:
//
//   - ordered: a builder took a ground-plan tower order that think (it may
//     still have failed to place: see place_fail below);
//   - no_site: the plan found no zone with a site for the tower;
//   - no_builder: no builder priced a ground tower that think — every
//     builder was busy (the plan itself refreshes every think since README
//     §13.9 #3, but only an idle or reassessing builder prices a tower);
//   - priced: builders priced one and none took it.
//
// Each builder that priced a tower in an owed think is resolved too:
//
//   - built: it took the order; place_fail: it took it, but the order left
//     it idle (the executor found no site under the rules);
//   - com_veto: the commander, while the front rules leave ground towers to
//     a constructor that can build a missile tower (or the site lay beyond
//     the commander's radius);
//   - metal_wait: the opening tower waited for the metal store;
//   - energy_wait: its tower would have beaten what it chose without the
//     energy-shortage term (lin(covE, 400, 800));
//   - outscored: another option, or its current task, scored higher (or no
//     product of its was worth a tower).
//
// Every think is counted, so the owed share says whether the budget (the
// deficit) or the conversion into orders binds; an owed spell's length,
// from the first owed think to the order that ends it, is summed too.
// Banking thinks — the metal store at least bankPercent full while income
// covers expense — are counted, and those among them in which the ambition
// caps' banking rule would have raised w_defense (towers below the tier
// curve) had it applied at full ambition (style.go limit).

// Owed-think outcomes.
const (
	owedOrdered uint8 = iota
	owedNoSite
	owedNoBuilder
	owedPriced
	owedN
)

var owedNames = [owedN]string{"ordered", "no_site", "no_builder", "priced"}

// Pricing outcomes.
const (
	priceBuilt uint8 = iota
	pricePlaceFail
	priceCom
	priceMetal
	priceEnergy
	priceOutscored
	priceN
)

var priceNames = [priceN]string{"built", "place_fail", "com_veto", "metal_wait", "energy_wait", "outscored"}

// Audit phases: before minute 10, minutes 10–20, after 20.
var owedPhases = [3]string{"10", "20", "late"}

// towerPrice is one builder's best ground-plan tower in a think.
type towerPrice struct {
	h           pool.Handle
	def         *aikit.UnitInfo
	gen         uint32
	score, noE  int64 // best score, and the same without the energy term
	scored      bool  // a product reached scoring
	com, metal  bool  // vetoes met on the way
	energyShort bool  // the energy term was below full
}

// towerAudit is the audit's state and its counters.
type towerAudit struct {
	tick   uint32 // the think being audited
	open   bool   // a think is being audited
	owed   bool
	site   bool
	since  uint32 // first tick of the current owed spell (0: none)
	prices []towerPrice

	thinks [3]int32
	owedN  [3]int32
	n      [3][owedN]int32
	pn     [3][priceN]int32
	spell  [3]int64 // Σ owed-spell ticks ended by an order, by the phase it ended in
	spells [3]int32
	terms  [3][6]int64 // Σ over thinks: the budget share and its base, slack, threat and army terms (permille); thinks
	bank   [3]int32    // banking thinks
	bankT  [3]int32    // of which below the tier's tower curve (the banking rule's case)
	cen    towerCensus // towers, factories and orders over time (defense_census.go)
}

func owedPhase(t uint32) int {
	switch {
	case t < 10*1800:
		return 0
	case t < 20*1800:
		return 1
	}
	return 2
}

// auditThink resolves the previous think's audit and opens this one's. It
// runs at the first refresh of each think, after finish.
func (pl *defPlan) auditThink(s *shared, b *core.Board) {
	a := &s.zones.audit
	if a.open {
		ph := owedPhase(a.tick)
		a.thinks[ph]++
		if a.owed {
			a.owedN[ph]++
			r := pl.auditResolve(s, b, ph)
			a.n[ph][r]++
			if r == owedOrdered {
				if a.since != 0 {
					a.spell[ph] += int64(a.tick - a.since)
					a.spells[ph]++
				}
				a.since = 0
			}
		} else {
			a.since = 0
		}
	}
	a.open, a.tick, a.prices = true, s.tick, a.prices[:0]
	ph := owedPhase(s.tick)
	a.terms[ph][0] += pl.share
	for i, v := range pl.terms {
		a.terms[ph][i+1] += v
	}
	a.terms[ph][5]++
	a.owed = pl.deficit[dcGround]*2 >= pl.cRef[dcGround]
	a.site = pl.zone[dcGround] >= 0
	if a.owed && a.since == 0 {
		a.since = s.tick
	}
	if s.mCap > 0 && s.mStock*100 >= s.mCap*bankPercent && s.mInc >= s.mExp {
		a.bank[ph]++
		n := int64(0)
		for c := dcGround; c < dcCount; c++ {
			n += int64(pl.cnt[c])
		}
		n -= int64(pl.nDual) // a missile tower counts in two classes
		if n*1000 < topDefenses.at(100, s.tick) {
			a.bankT[ph]++
		}
	}
	pl.census(s, b)
}

// auditResolve classifies an owed think and each builder's pricing in it
// (see the header).
func (pl *defPlan) auditResolve(s *shared, b *core.Board, ph int) uint8 {
	a := &s.zones.audit
	ordered := false
	for i := range a.prices {
		tp := &a.prices[i]
		var c *commitment
		if int(tp.h) < len(s.commit) {
			if cc := &s.commit[tp.h]; cc.def == tp.def && cc.gen == tp.gen && cc.tick == a.tick {
				c = cc
			}
		}
		r := priceOutscored
		switch {
		case c != nil && c.prod != nil && pl.groundPlan(c.prod) && c.kind == cDefense:
			r, ordered = priceBuilt, true
		case c != nil && c.prod != nil && pl.groundPlan(c.prod) && c.kind == cNone:
			r, ordered = pricePlaceFail, true // Economy.fail cleared it: the order left its builder idle
		case !tp.scored && tp.com:
			r = priceCom
		case !tp.scored && tp.metal:
			r = priceMetal
		case tp.scored && tp.energyShort:
			w := int64(minScore)
			if c != nil && c.score > w {
				w = c.score
			}
			if tp.score <= w && tp.noE > w {
				r = priceEnergy
			}
		}
		a.pn[ph][r]++
	}
	if !ordered {
		// A builder that did not price (its order came from elsewhere).
		for _, i := range b.Builders {
			u := &b.O.Own[i]
			if c := s.commitIf(u); c != nil && c.tick == a.tick && c.prod != nil && pl.groundPlan(c.prod) && (c.kind == cDefense || c.kind == cNone) {
				ordered = true
				break
			}
		}
	}
	switch {
	case ordered:
		return owedOrdered
	case !a.site:
		return owedNoSite
	case len(a.prices) == 0:
		return owedNoBuilder
	}
	return owedPriced
}

// groundPlan reports whether a product serves the ground plan.
func (pl *defPlan) groundPlan(p *aikit.UnitInfo) bool {
	return pl.cls[p.Index] == dcGround || pl.dual[p.Index]
}

// auditPrice returns the builder's price record for this think.
func (pl *defPlan) auditPrice(s *shared, u *aikit.OwnUnit) *towerPrice {
	a := &s.zones.audit
	for i := range a.prices {
		if tp := &a.prices[i]; tp.h == u.H && tp.def == u.Info && tp.gen == u.Gen {
			return tp
		}
	}
	a.prices = append(a.prices, towerPrice{h: u.H, def: u.Info, gen: u.Gen})
	return &a.prices[len(a.prices)-1]
}

// report publishes the audit's counters.
func (a *towerAudit) report(add func(name string, value int64)) {
	for ph, name := range owedPhases {
		add("tower_thinks_"+name, int64(a.thinks[ph]))
		add("tower_owed_"+name, int64(a.owedN[ph]))
		for r := uint8(0); r < owedN; r++ {
			add("tower_owed_"+name+"_"+owedNames[r], int64(a.n[ph][r]))
		}
		for r := uint8(0); r < priceN; r++ {
			add("tower_price_"+name+"_"+priceNames[r], int64(a.pn[ph][r]))
		}
		add("tower_spell_ticks_"+name, a.spell[ph])
		add("tower_spells_"+name, int64(a.spells[ph]))
		if t := a.terms[ph][5]; t > 0 {
			// Mean budget terms over the phase's thinks, permille.
			for i, k := range [...]string{"share", "base", "slack", "threat", "army"} {
				add("tower_mean_"+k+"_"+name, a.terms[ph][i]/t)
			}
		}
		add("tower_bank_"+name, int64(a.bank[ph]))
		add("tower_bank_below_"+name, int64(a.bankT[ph]))
	}
	a.cen.report(add)
}
