package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Income into army, and towers that pay (army parameter, README §13.12).
//
// Against the same brain without the defense plan (def_plan=0) the plan
// lost the 20-minute race (56.5% of points to def_plan=0 over 192 protocol
// v2 games) and in 40-minute games against v1 it held 31 towers worth
// 10,300 at minute 30 and 43 worth 19,600 at minute 40 (v1: an army of
// 18,900 at 30 minutes to our 11,600). Three traced commander deaths at
// minutes 14–15.5 each came after our army was gone: 18–29 enemy units
// within 700 wu of the commander, none or one of ours, and at most four of
// ten towers in reach. The towers bought the army's loss: the budget, a
// share of income tripled by raids, took 20–42% of income at minutes 6–11
// in one of them while metal coverage stood at 0.3–0.6, and a behind
// plan's tower need (up to 8,000) outranks a factory's (at most 2,000).
//
// Parts (the parameter is their sum):
//
//   - 1 yield: while production is short — the metal store banks
//     (bankPercent full while income covers expense) and the working
//     factories are fewer than the income carries, one per aFacIncome of
//     metal income, or none works — a ground or anti-air tower is scored
//     as if the plan were one tower behind (need aYieldNeed), at aYieldMul
//     of that, unless its zone has been raided (the plan's urgency above
//     nominal).
//   - 2 factories: while production is short, from minute aFacFrom, a
//     factory's need is at least aFacNeed per missing factory (up to
//     aFacNeedMax), and with no working factory at all (the only one
//     walled in) it is as urgent as a first factory. From the opening
//     (the store banks at 60% of a small cap in the first minutes) the
//     floor cost 20-minute games: 36.5% of points against def_plan=0
//     where the yield part alone took 43.2% (96 games each).
//   - 4 type: the tower quality blend is the square law (aTypeVR), and
//     a builder that can build a missile tower builds direct-fire towers
//     only for a zone ground units have attacked (attacked at least half:
//     enemy ground units seen near it, or a building lost there);
//     elsewhere missile towers, as people's towers are about nine in ten.
//     The same value buys about twice the towers, near the people's count.
//   - 8 place: a zone's front exposure is aPlaceLo of the front rules'
//     where no attack came, all of it where one did.
//   - 16 ceiling: no ground tower is owed while our towers (built,
//     framed, or ordered beyond the frames) are worth the tier's tower
//     count curve (topDefenses at the persona's ambition) in the ground
//     plan's reference tower, or aValShare of all we own, whichever is
//     larger.
//   - 32 cover: the home zone weighs the commander as the tactics army
//     weighs an enemy commander (aComBonus, not defComBonus) and its
//     ground exposure is at least nominal.
//   - 64 surplus: the ground budget accrues from surplus only (slack
//     follows metal coverage from aSurCovLo to aSurCovHi) and raids raise
//     it by aSurThreat at most.
//
// The default is 21 (yield, type, ceiling): against army=0 it took 53.4%
// of the points in 20-minute games (192) and 54.7% in 40-minute games
// (96), and v1's share of 40-minute games fell from 60.4% to 54.2%.
// Factories, place, cover and surplus are measured and off (README
// §13.12 has every part's numbers).
const (
	aYield     = 1 << iota // towers yield to production while metal banks
	aFactories             // factories follow banked income; a walled-in only factory is replaced
	aType                  // tower types by the square law; direct fire where ground attacks came
	aPlace                 // towers where attacks came
	aCeiling               // tower value held to the humans' count of missile towers or a share of what we own
	aCover                 // the home zone, where the commander works, weighs as the enemy weighs the commander
	aSurplus               // the ground budget accrues from surplus; raids raise it by half at most
)

const (
	aFacIncome  = 6000   // milli metal/s of income per working factory (v1's ratio at minutes 20–30)
	aYieldMul   = 250    // permille of a tower's score while production is short
	aYieldNeed  = 1500   // permille: the need a yielding tower is scored at (one tower behind)
	aFacNeed    = 1000   // permille of factory need per missing factory while production is short
	aFacNeedMax = 4000   // permille: the most a missing factory's need reaches
	aFacFrom    = 12     // minute from which the factories part's floor per missing factory acts
	aTypeVR     = 150    // the tower quality blend under the type part (the square law)
	aAtkFull    = 240000 // strength-ticks of enemy ground units seen near a zone that make it fully attacked
	aPlaceLo    = 400    // permille of the front exposure a zone keeps with no attack seen (place part)
	aValShare   = 150    // permille of our standing value the towers may hold (ceiling part)
	aComBonus   = 8000   // the home zone's asset weight for the commander (cover part; the tactics army's target value of a commander)
	aSurCovLo   = 600    // metal coverage (permille) at and below which the surplus part accrues nothing
	aSurCovHi   = 1100   // metal coverage from which it accrues in full
	aSurThreat  = 1500   // the most raids and danger raise the budget under the surplus part (permille)
)

// armyState is the army switch's per-think view of production.
type armyState struct {
	working int32 // factories built and not written off, plus factory frames and orders
	missing int64 // milli: factories the income carries beyond working
	short   bool  // production is short (see the header)
	towerV  int64 // value of our towers, built or framed (ceiling)
	ownV    int64 // value of everything of ours built or framed but the commander (ceiling)
}

// apart reports whether one part of army acts.
func (s *shared) apart(bit int32) bool { return s.p.Army&bit != 0 }

// observeArmy refreshes the view of production (Strategy.Plan, after the
// model).
func (s *shared) observeArmy(b *core.Board) {
	a := &s.arm
	*a = armyState{}
	if s.p.Army == 0 {
		return
	}
	n := int64(len(b.Factories)) - int64(s.deadFacs)
	for _, i := range b.Frames {
		if b.O.Own[i].Info.Role.Has(aikit.RoleFactory) {
			n++
		}
	}
	if s.pendFacBP > 0 {
		n++ // a builder walking to a factory site
	}
	a.working = int32(n)
	a.missing = max64(s.mInc*1000/aFacIncome-n*1000, 0)
	built := int64(len(b.Factories)) - int64(s.deadFacs)
	bank := s.mCap > 0 && s.mStock*100 >= s.mCap*bankPercent && s.mInc >= s.mExp
	a.short = (bank && a.missing > 0) || (built <= 0 && len(b.Factories) > 0)
	if s.apart(aCeiling) {
		for i := range b.O.Own {
			u := &b.O.Own[i]
			r := u.Info.Role
			if r.Has(aikit.RoleCommander) {
				continue
			}
			v := s.info[u.Info.Index].costMeq
			a.ownV += v
			if r.Has(aikit.RoleDefense) && !r.Has(aikit.RoleMobile) {
				a.towerV += v
			}
		}
	}
}

// ceiling reports, under the ceiling part, that the towers (built,
// framed, or ordered beyond the frames) have reached their value ceiling:
// the tier's tower count curve (topDefenses at the persona's ambition) in
// the ground plan's reference tower, or aValShare of all we own, the
// larger. The frames that standing orders are set against are summed per
// class, as the plan counts them, so a missile tower's frame offsets two
// orders' worth (the measured rule; orders beyond the frames count a
// little late).
func (pl *defPlan) ceiling(s *shared) bool {
	if !s.apart(aCeiling) {
		return false
	}
	var framed, ordered int64
	for c := dcGround; c < dcCount; c++ {
		framed += pl.frame[c]
	}
	for i := range pl.commits {
		ordered += pl.commits[i].v
	}
	v := s.arm.towerV + max64(ordered-framed, 0)
	lim := max64(topDefenses.at(ambition(&s.k.Persona), s.tick)*pl.cRef[dcGround]/one, s.arm.ownV*aValShare/one)
	return v >= lim
}

// towerYield is the yield part's multiplier (permille) on a tower of
// class cl scored at need (permille) whose zone urgency is urg.
func (s *shared) towerYield(cl uint8, need, urg int64) int64 {
	if !s.apart(aYield) || !s.arm.short || cl == dcWater || urg > one || need <= 0 {
		return one
	}
	return aYieldMul * min64(need, aYieldNeed) / need
}

// factoryNeed is the factories part's floor on a factory's need (permille):
// per missing factory while production is short, and a first factory's
// urgency when every factory we have is walled in.
func (s *shared) factoryNeed(b *core.Board, first int64) int64 {
	if !s.apart(aFactories) {
		return 0
	}
	var need int64
	if s.arm.short && s.tick >= aFacFrom*1800 {
		need = min64(aFacNeed*max64(s.arm.missing, one)/one, aFacNeedMax)
	}
	if len(b.Factories) > 0 && int64(len(b.Factories)) == int64(s.deadFacs) && s.pendFacBP == 0 {
		need = max64(need, first)
	}
	return need
}

// attacked is how far enemy ground units have come at a zone (permille):
// strength-ticks seen near it (decaying over ten minutes), or a building
// lost there.
func (pl *defPlan) attacked(z int32) int64 {
	if z < 0 || int(z) >= len(pl.atkW) {
		return 0
	}
	return max64(lin(pl.atkW[z], 0, aAtkFull), lin(pl.heatLoss[z], 0, pl.cRef[dcGround]))
}

// typeMix is the type part's mix term for a direct-fire ground tower (from
// a builder that could build a missile tower instead): full where the
// plan's next ground tower goes to a zone ground units have attacked, none
// elsewhere — the human mix, about nine missile towers in ten.
func (pl *defPlan) typeMix() (int64, bool) {
	if !pl.typeOn {
		return 0, false
	}
	if a := pl.attacked(pl.zone[dcGround]); a >= one/2 {
		return one, true
	}
	return 0, true
}

// placeExp scales a zone's front exposure under the place part: aPlaceLo
// of it where no attack came, all of it where one did.
func (pl *defPlan) placeExp(s *shared, z int32, exp int64) int64 {
	if !s.apart(aPlace) {
		return exp
	}
	return exp * (aPlaceLo + (one-aPlaceLo)*pl.attacked(z)/one) / one
}

// comBonus is the home zone's asset weight for the commander: defComBonus,
// or aComBonus under the cover part.
func (s *shared) comBonus() int64 {
	if s.apart(aCover) {
		return aComBonus
	}
	return defComBonus
}

// coverExp is the home zone's ground exposure under the cover part: at
// least nominal, where the front rules rate the start's own zone at a
// quarter.
func (s *shared) coverExp(z, home int32, exp int64) int64 {
	if !s.apart(aCover) || z != home {
		return exp
	}
	return max64(exp, one)
}

// surplusTerms are the ground budget's slack and threat terms under the
// surplus part: slack follows metal coverage from nothing at aSurCovLo to
// full at aSurCovHi (and energy's as before), and raids raise the budget
// by at most aSurThreat. A traced great divide game against def_plan=0
// put 20–42% of its income into the tower budget at minutes 6–11 (raids
// tripled the share) while metal coverage stood at 0.3–0.6, and lost its
// army and then its commander at minute 15.
func (s *shared) surplusTerms(slack, threat int64) (int64, int64) {
	if !s.apart(aSurplus) {
		return slack, threat
	}
	return min64(lin(s.covE, 700, 1200), lin(s.covM, aSurCovLo, aSurCovHi)), min64(threat, aSurThreat)
}
