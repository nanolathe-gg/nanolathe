package utility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// armyBoard is two built factories and a factory frame.
func armyBoard() *core.Board {
	fac := &aikit.UnitInfo{Role: aikit.RoleFactory}
	return &core.Board{O: &aikit.Obs{Own: []aikit.OwnUnit{{Info: fac, Built: true}, {Info: fac, Built: true}, {Info: fac}}},
		Factories: []int32{0, 1}, Frames: []int32{2}}
}

// Production is short while the metal store banks and the working
// factories (built and not written off, framed, or walked to) are fewer
// than one per aFacIncome of income, or while every factory is walled in;
// towers then yield (a raided zone's do not) and a factory's need has a
// floor per missing factory. army=0 changes nothing.
func TestArmyYieldAndFactories(t *testing.T) {
	b := armyBoard()
	s := &shared{p: Params{Army: aYield | aFactories}, tick: aFacFrom * 1800}
	s.mCap, s.mStock, s.mInc, s.mExp = 1000, 700, 27000, 20000
	s.observeArmy(b) // 4.5 carried, 3 working
	if !s.arm.short || s.arm.working != 3 || s.arm.missing != 1500 {
		t.Fatalf("banking, 3 of 4.5: %+v, want short with 1500 missing", s.arm)
	}
	if y := s.towerYield(dcGround, 1000, one); y != aYieldMul {
		t.Errorf("ground tower yield %d, want %d", y, aYieldMul)
	}
	if y := s.towerYield(dcGround, 6000, one); y != aYieldMul/4 {
		t.Errorf("ground tower yield at need 6000: %d, want one tower behind at %d‰ (%d)", y, aYieldMul, aYieldMul/4)
	}
	if y := s.towerYield(dcGround, 1000, 2*one); y != one {
		t.Errorf("a raided zone's tower yields: %d", y)
	}
	if y := s.towerYield(dcWater, 1000, one); y != one {
		t.Errorf("a water tower yields: %d", y)
	}
	if n := s.factoryNeed(b, 3000); n != 1500 {
		t.Errorf("factory need %d, want 1500 (1.5 missing)", n)
	}
	s.tick = aFacFrom*1800 - 1
	if n := s.factoryNeed(b, 3000); n != 0 {
		t.Errorf("before minute %d: factory need %d, want none", aFacFrom, n)
	}
	s.tick = aFacFrom * 1800
	s.mInc = 60000 // ten carried, seven missing: capped
	s.observeArmy(b)
	if n := s.factoryNeed(b, 3000); n != aFacNeedMax {
		t.Errorf("factory need %d, want the cap %d", n, aFacNeedMax)
	}
	for _, c := range []struct {
		why                string
		stock, income, exp int64
	}{
		{"store low", 500, 27000, 20000},
		{"spending above income", 700, 27000, 30000},
		{"enough factories", 700, 15000, 10000},
	} {
		s.mStock, s.mInc, s.mExp = c.stock, c.income, c.exp
		s.observeArmy(b)
		if s.arm.short || s.towerYield(dcGround, 1000, one) != one || s.factoryNeed(b, 3000) != 0 {
			t.Errorf("%s: short %v yield %d need %d", c.why, s.arm.short, s.towerYield(dcGround, 1000, one), s.factoryNeed(b, 3000))
		}
	}
	// Both factories walled in: a first factory's urgency, and towers yield.
	s.deadFacs = 2
	s.observeArmy(b)
	if n := s.factoryNeed(b, 3000); n != 3000 || !s.arm.short {
		t.Errorf("every factory walled in: need %d short %v, want 3000 and short", n, s.arm.short)
	}
	s.p.Army = 0
	s.observeArmy(b)
	if s.arm.short || s.towerYield(dcGround, 1000, one) != one || s.factoryNeed(b, 3000) != 0 {
		t.Error("army=0 acted")
	}
}

// The ceiling part: no ground tower is owed once our towers (built,
// framed, or ordered beyond the frames) are worth the tier's tower count in
// the reference tower, or aValShare of all we own, the larger.
func TestArmyCeiling(t *testing.T) {
	s := &shared{p: Params{Army: aCeiling}, k: &aikit.Kit{Persona: aikit.PersonaHard}, tick: 20 * 1800}
	pl := &defPlan{}
	pl.cRef[dcGround] = 155
	lim := topDefenses.at(100, s.tick) * 155 / one // 19.8 towers of 155
	s.arm.ownV = 10000
	s.arm.towerV = lim - 1
	if pl.ceiling(s) {
		t.Fatalf("towers worth %d under the curve's %d: ceiling reached", s.arm.towerV, lim)
	}
	pl.commits = []defCommit{{cls: dcGround, v: 155}}
	if !pl.ceiling(s) {
		t.Errorf("an order beyond the frames did not count toward the ceiling")
	}
	pl.frame[dcGround] = 155
	if pl.ceiling(s) {
		t.Errorf("an order for a counted frame counted twice")
	}
	s.arm.ownV = 40000 // 15% of it is above the curve
	s.arm.towerV = 5999
	if pl.ceiling(s) {
		t.Errorf("towers worth %d under 15%% of %d: ceiling reached", s.arm.towerV, s.arm.ownV)
	}
	s.arm.towerV = 6000
	if !pl.ceiling(s) {
		t.Errorf("towers worth %d at 15%% of %d: no ceiling", s.arm.towerV, s.arm.ownV)
	}
	s.p.Army = 0
	if pl.ceiling(s) {
		t.Error("army=0 has a ceiling")
	}
}

// A zone is attacked by enemy ground units seen near it (strength-ticks,
// full at aAtkFull) or by a building lost there; the place part scales its
// front exposure from aPlaceLo to all of it, and the type part lets direct
// fire compete where the next ground tower goes to an attacked zone.
func TestArmyAttackedZones(t *testing.T) {
	pl := &defPlan{atkW: []int64{0, aAtkFull / 2, aAtkFull}, heatLoss: []int64{0, 0, 0}}
	pl.cRef[dcGround] = 155
	if a, b, c := pl.attacked(0), pl.attacked(1), pl.attacked(2); a != 0 || b != 500 || c != one {
		t.Errorf("attacked %d %d %d, want 0 500 1000", a, b, c)
	}
	pl.heatLoss[0] = 155
	if a := pl.attacked(0); a != one {
		t.Errorf("a lost tower's worth: attacked %d, want 1000", a)
	}
	pl.heatLoss[0] = 0
	s := &shared{p: Params{Army: aPlace}}
	if e := pl.placeExp(s, 0, 2000); e != 2000*aPlaceLo/one {
		t.Errorf("unattacked zone exposure %d, want %d", e, 2000*aPlaceLo/one)
	}
	if e := pl.placeExp(s, 2, 2000); e != 2000 {
		t.Errorf("attacked zone exposure %d, want 2000", e)
	}
	s.p.Army = 0
	if e := pl.placeExp(s, 0, 2000); e != 2000 {
		t.Errorf("army=0 exposure %d", e)
	}
	pl.typeOn, pl.zone[dcGround] = true, 1
	if m, ok := pl.typeMix(); !ok || m != one {
		t.Errorf("half-attacked zone: mix %d %v, want full", m, ok)
	}
	pl.zone[dcGround] = 0
	if m, ok := pl.typeMix(); !ok || m != 0 {
		t.Errorf("unattacked zone: mix %d %v, want missile towers only", m, ok)
	}
}

// The type part through the plan: with five missile towers of five the
// front rules let a light tower compete, the type part does not until the
// zone the next tower goes to has been attacked; the square law then
// rates the light tower above the heavy one.
func TestArmyTypeInThePlan(t *testing.T) {
	p := DefaultParams()
	p.Army = aType
	w := newDefWorld(p)
	d := w.d
	base := w.base()
	for i := 0; i < 5; i++ {
		base = append(base, own(21+i, d.rl, 1300+int32(i)*200, 700))
	}
	w.think(12*1800, base)
	pl := w.e.plan()
	pl.refresh(w.e.s, w.b)
	con := &w.obs.Own[1]
	w.e.defenseCand(w.b, con, d.rl) // the builder's normalizers
	if m := pl.mixOf(d.llt, dcGround); m != 0 {
		t.Errorf("unattacked zone: light mix %d, want 0 (the front rules alone give %d)", m, pl.missileMix())
	}
	pl.atkW[pl.zone[dcGround]] = aAtkFull
	pl.emit++ // a new plan: the normalizers are recomputed
	w.e.defenseCand(w.b, con, d.rl)
	if m := pl.mixOf(d.llt, dcGround); m != one {
		t.Errorf("attacked zone: light mix %d, want full", m)
	}
	vr := blend(w.e.s)
	if ql, qh := quality(w.e.s, d.llt, dcGround, vr), quality(w.e.s, d.hlt, dcGround, vr); ql <= qh {
		t.Errorf("square law: light tower %d not above heavy %d", ql, qh)
	}
}

// The cover part weighs the home zone for the commander as the tactics
// army weighs an enemy commander and holds its exposure at nominal; the
// surplus part takes the budget's slack from metal coverage and caps what
// raids add. Both leave the plan alone when off.
func TestArmyCoverAndSurplus(t *testing.T) {
	s := &shared{p: Params{Army: aCover | aSurplus}}
	if b := s.comBonus(); b != aComBonus {
		t.Errorf("cover: commander bonus %d, want %d", b, aComBonus)
	}
	if e := s.coverExp(3, 3, 250); e != one {
		t.Errorf("cover: home exposure %d, want %d", e, one)
	}
	if e := s.coverExp(4, 3, 250); e != 250 {
		t.Errorf("cover: another zone's exposure %d, want 250", e)
	}
	s.covE, s.covM = 2000, aSurCovLo
	if sl, th := s.surplusTerms(900, 3000); sl != 0 || th != aSurThreat {
		t.Errorf("surplus at coverage %d: slack %d threat %d, want 0 and %d", s.covM, sl, th, aSurThreat)
	}
	s.covM = aSurCovHi
	if sl, _ := s.surplusTerms(900, 1000); sl != one {
		t.Errorf("surplus at coverage %d: slack %d, want %d", s.covM, sl, one)
	}
	s.p.Army = 0
	if b, e := s.comBonus(), s.coverExp(3, 3, 250); b != defComBonus || e != 250 {
		t.Errorf("army=0: bonus %d exposure %d", b, e)
	}
	if sl, th := s.surplusTerms(700, 3000); sl != 700 || th != 3000 {
		t.Errorf("army=0: slack %d threat %d", sl, th)
	}
}
