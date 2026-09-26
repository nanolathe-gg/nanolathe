package utility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// expand 1: from minute expand_from an extractor site is scored with a
// metal need of at least paceNeed while our extractors trail the top
// tier's curve and energy is not short; on the curve, before expand_from
// or without the part the brain scores its plain metal need.
func TestExpandPace(t *testing.T) {
	s := &shared{p: Params{Expand: xPace, ExpandFrom: 15}, k: &aikit.Kit{Persona: aikit.PersonaHard}}
	s.needM, s.needE, s.needFirm = 200, 900, 900
	for _, c := range []struct {
		min      uint32
		mex      int32
		floor    int64
		mexNeed  int64
		describe string
	}{
		{14, 2, 0, 200, "before minute expand_from"},
		{16, 10, paceNeed, paceNeed, "behind the curve (15.2 at minute 16)"},
		{16, 16, 0, 200, "on the curve"},
	} {
		s.tick, s.mexBuilt = c.min*1800, c.mex
		if got := s.paceFloor(); got != c.floor {
			t.Errorf("%s: floor %d, want %d", c.describe, got, c.floor)
		}
		if got := s.mexNeed(); got != c.mexNeed {
			t.Errorf("%s: extractor need %d, want %d", c.describe, got, c.mexNeed)
		}
	}
	s.tick, s.mexBuilt, s.needE = 16*1800, 10, 1500
	if got := s.mexNeed(); got != 200 {
		t.Errorf("energy short: extractor need %d, want the plain need 200", got)
	}
	s.p.Expand, s.needE = 0, 900
	if s.paceFloor() != 0 || s.mexNeed() != 200 || s.mexTravelHalf() != int64(s.p.TravelHalf) {
		t.Error("expand off still moves the extractor need or travel")
	}
}

// expand 2: the constructor target ramps in over xConsRamp minutes from
// minute expand_from to the human ratio per finished extractor, plus one per
// spots_per_con claimable spots (at most xRoomCons), held to the top
// tier's constructor curve, and yields to the army under pressure or when
// outnumbered by half.
func TestExpandConstructors(t *testing.T) {
	s := &shared{p: Params{Expand: xBuilders, ExpandFrom: 15, SpotsPerCon: 3}, k: &aikit.Kit{Persona: aikit.PersonaHard}}
	s.mexBuilt, s.freeSafe = 20, 30
	for _, c := range []struct {
		min  uint32
		want int64
	}{
		{14, 0},
		{15, 2000},  // the ratio not yet ramped in: room only
		{17, 8740},  // 20 × 337 (675 ramped in by half) + 2000, under the curve's 12.4
		{20, 14900}, // 20 × 750 + 2000 = 17000, held to the curve's 14.9
	} {
		s.tick = c.min * 1800
		if got := s.consExpand(); got != c.want {
			t.Errorf("minute %d: constructor target %d, want %d", c.min, got, c.want)
		}
	}
	s.tick, s.mexBuilt, s.freeSafe = 20*1800, 4, 3
	if got, want := s.consExpand(), int64(4*750+1000); got != want {
		t.Errorf("four extractors, three free spots: target %d, want %d", got, want)
	}
	s.pressure = 200
	if s.consExpand() != 0 {
		t.Error("the base under pressure: constructors still targeted")
	}
	s.pressure, s.armyRatio = 0, xConsRatio+1
	if s.consExpand() != 0 {
		t.Error("outnumbered by half: constructors still targeted")
	}
	s.armyRatio, s.p.Expand = 0, xPace|xHold|xContest
	if s.consExpand() != 0 {
		t.Error("without the builders part: a constructor target")
	}
}

// expand 8: a spot on the contested line counts as ours while our army
// holds it (at least xContestPower and twice the threat); spots already
// ours or deep in enemy territory are left as they are.
func TestExpandContested(t *testing.T) {
	m := &aikit.MapInfo{WorldW: 4096, WorldH: 4096, SectorW: 32, SectorH: 32}
	b := &core.Board{OwnPower: aikit.NewGrid(m), Threat: aikit.NewGrid(m)}
	s := &shared{p: Params{Expand: xContest}}
	b.OwnPower.AddDisc(2000, 2000, 0, 100)
	b.Threat.AddDisc(2000, 2000, 0, 40)
	b.OwnPower.AddDisc(1000, 3000, 0, 60)
	for _, c := range []struct {
		x, z int32
		terr int64
		want bool
		why  string
	}{
		{2000, 2000, 500, true, "our army holds a contested spot"},
		{2000, 2000, 1000, false, "already ours"},
		{2000, 2000, xContestTerr - 1, false, "beyond the contested line"},
		{1000, 3000, 500, false, "too little of our army there"},
		{3000, 1000, 500, false, "no army there"},
	} {
		if got := s.contested(b, c.x, c.z, c.terr); got != c.want {
			t.Errorf("%s: contested %v, want %v", c.why, got, c.want)
		}
	}
	b.Threat.AddDisc(2000, 2000, 0, 20) // threat 60: 100 < 2 × 60
	if s.contested(b, 2000, 2000, 500) {
		t.Error("a spot our army does not outweigh twice counted as held")
	}
	s.p.Expand = xPace | xBuilders | xHold
	if s.contested(b, 1000, 3000, 500) || s.contested(b, 2000, 2000, 500) {
		t.Error("without the contest part a contested spot counted as ours")
	}
}

// expand 4: an extractor placement that fails where no feature in view
// and no building of ours stands holds the spot on the board (an enemy
// building we have not seen) and lifts its first timed block; a building
// of ours on the spot, or a reclaimable wreck in view, keeps the block.
func TestExpandHoldsFailedSpot(t *testing.T) {
	p := DefaultParams()
	p.Expand = xHold
	w := newDefWorld(p)
	d := w.d
	units := func(extra ...aikit.OwnUnit) []aikit.OwnUnit {
		return append([]aikit.OwnUnit{own(1, d.com, 512, 512), own(2, d.con, 700, 700), own(3, d.fac, 660, 640)}, extra...)
	}
	e, s := w.e, w.e.s
	w.think(10*1800, units())
	con := &w.obs.Own[1]
	failAt := func(spot int32) {
		c, bs := s.commitOf(con), s.bstateOf(con)
		bs.streak = 0
		*c = commitment{def: con.Info, gen: con.Gen, kind: cMex, prod: d.mex, spot: spot, tick: s.tick}
		if sp := e.fail(c, bs); sp == spot {
			e.holdFailed(w.b, sp)
		} else {
			t.Fatalf("failure at spot %d blamed %d", spot, sp)
		}
	}
	failAt(1)
	if w.b.Spots[1] != core.SpotHeld || s.spotBlock[1] != 0 {
		t.Errorf("unseen building: spot %v, block to %d (want held, no block)", w.b.Spots[1], s.spotBlock[1])
	}
	w.think(10*1800+15, units())
	if w.b.Spots[1] != core.SpotHeld {
		t.Errorf("hold lost by the next think: %v", w.b.Spots[1])
	}
	var dd decision
	dd.reset(s.tick, con.Info, con.X, con.Z)
	e.evalMexN(w.b, &w.obs.Own[1], s.bstateOf(&w.obs.Own[1]), &dd)
	for i := 0; i < dd.n; i++ {
		if dd.top[i].spot == 1 {
			t.Error("a held spot was offered to a builder")
		}
	}
	// A building of ours on the spot blocks it itself.
	sp2 := &w.k.Map.Spots[2]
	w.think(10*1800+30, units(own(9, d.sol, sp2.X+16, sp2.Z)))
	con = &w.obs.Own[1]
	failAt(2)
	if w.b.Spots[2] == core.SpotHeld || s.spotBlock[2] <= s.tick {
		t.Errorf("own building on the spot: spot %v, block to %d (want a timed block)", w.b.Spots[2], s.spotBlock[2])
	}
	// A reclaimable wreck in view: layout clears it, the block stays.
	sp3 := &w.k.Map.Spots[3]
	w.obs.Features = []aikit.Feature{{X: sp3.X + 20, Z: sp3.Z, Metal: 80, Reclaimable: true, Blocking: true}}
	failAt(3)
	if w.b.Spots[3] == core.SpotHeld || s.spotBlock[3] <= s.tick {
		t.Errorf("wreck in view: spot %v, block to %d (want a timed block)", w.b.Spots[3], s.spotBlock[3])
	}
}

// expand 16: from minute expand_from, while metal is not short and
// working factories (built or framed, written-off ones excluded) are fewer
// than one per xFacIncome of income, the factory weight rises by xSpendPer
// per missing factory up to xSpendMax, and the assist weight by half that;
// otherwise nothing moves.
func TestExpandSpend(t *testing.T) {
	fac := &aikit.UnitInfo{Role: aikit.RoleFactory}
	b := &core.Board{O: &aikit.Obs{Own: []aikit.OwnUnit{{Info: fac, Built: true}, {Info: fac, Built: true}, {Info: fac}}},
		Factories: []int32{0, 1}, Frames: []int32{2}}
	base := Params{Expand: xSpend, ExpandFrom: 15, WFactory: 100, WAssist: 50}
	s := &shared{p: base, tick: 14 * 1800}
	s.mCap, s.mStock, s.mInc, s.mExp, s.covM = 1000, 700, 27000, 20000, 900
	if s.spendWeights(b); s.p != base {
		t.Error("the spend part acted in the opening")
	}
	s.tick = 15 * 1800
	s.spendWeights(b) // the store banking; 4.5 wanted, 3 working: 1.5 short
	if s.p.WFactory != 400 || s.p.WAssist != 125 {
		t.Errorf("1.5 factories short: w_factory %d w_assist %d, want 400 and 125", s.p.WFactory, s.p.WAssist)
	}
	s.p, s.deadFacs = base, 2
	s.spendWeights(b) // two written off: 3.5 short, capped
	if s.p.WFactory != 800 {
		t.Errorf("3.5 short: w_factory %d, want the cap 800", s.p.WFactory)
	}
	s.p, s.deadFacs, s.mStock, s.covM = base, 0, 100, surplusCov
	s.spendWeights(b) // supply covers demand with a tenth to spare
	if s.p.WFactory != 400 {
		t.Errorf("metal coverage at the surplus mark: w_factory %d, want 400", s.p.WFactory)
	}
	for _, c := range []struct {
		why                       string
		stock, income, cost, covM int64
	}{
		{"metal short, store low", 500, 27000, 20000, 900},
		{"store full but spending above income", 700, 27000, 30000, 900},
		{"enough factories", 700, 15000, 10000, 2000},
	} {
		s.p = base
		s.mStock, s.mInc, s.mExp, s.covM = c.stock, c.income, c.cost, c.covM
		s.spendWeights(b)
		if s.p != base {
			t.Errorf("%s: weights moved to %d / %d", c.why, s.p.WFactory, s.p.WAssist)
		}
	}
	s.p = Params{ExpandFrom: 15, WFactory: 100, WAssist: 50}
	s.mStock, s.mInc, s.mExp = 700, 27000, 20000
	s.spendWeights(b)
	if s.p.WFactory != 100 {
		t.Error("without the spend part the factory weight moved")
	}
}
