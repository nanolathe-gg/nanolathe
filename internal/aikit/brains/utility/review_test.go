package utility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// The pool recycles a slot without a generation, so a unit of the same
// type in a dead unit's slot has the same handle and definition; only
// OwnUnit.Gen tells them apart. Every per-handle table starts afresh for
// the new unit.
func TestRecycledHandleStartsAfresh(t *testing.T) {
	tank := &aikit.UnitInfo{Index: 0, Key: "tank", Role: aikit.RoleMobile | aikit.RoleCombat, DPS: 50, HP: 500}
	con := &aikit.UnitInfo{Index: 1, Key: "con", Role: aikit.RoleMobile | aikit.RoleBuilder}
	fac := &aikit.UnitInfo{Index: 2, Key: "fac", Role: aikit.RoleFactory, FootX: 6, FootZ: 6}
	k := &aikit.Kit{Table: &aikit.Table{Units: []*aikit.UnitInfo{tank, con, fac}}}
	s := &shared{p: Params{Layout: 1}, k: k, info: make([]staticInfo, 3)}

	old := aikit.OwnUnit{H: 7, Gen: 1, Info: con}
	now := aikit.OwnUnit{H: 7, Gen: 2, Info: con}
	s.commitOf(&old).kind = cMex
	s.bstateOf(&old).stuckUntil = 9000
	if c := s.commitOf(&now); c.kind != cNone {
		t.Errorf("commitment inherited from the slot's last unit: %v", c.kind)
	}
	if s.commitIf(&old) != nil {
		t.Error("the dead unit's commitment still answers for its slot")
	}
	if bs := s.bstateOf(&now); bs.stuckUntil != 0 || bs.blockSpot != -1 {
		t.Errorf("builder state inherited: stuck until %d, block spot %d", bs.stuckUntil, bs.blockSpot)
	}
	facOld := aikit.OwnUnit{H: 9, Gen: 1, Info: fac}
	s.fstateOf(&facOld).dead = true
	if s.fstateOf(&aikit.OwnUnit{H: 9, Gen: 2, Info: fac}).dead {
		t.Error("a rebuilt factory in the slot inherited its predecessor's written-off state")
	}
	pr := &Production{s: s}
	pr.regOf(&facOld).prod = tank
	if pr.regOf(&aikit.OwnUnit{H: 9, Gen: 2, Info: fac}).prod != nil {
		t.Error("a rebuilt factory inherited its predecessor's queued product")
	}

	// Stuck units: a tank that left and died, then a tank of the same type
	// in its slot standing at the factory for 90 s, is stuck there; a
	// tank that appears in the slot of one first seen long ago is not
	// stuck until it has stood 90 s itself.
	factory := aikit.OwnUnit{H: 1, Gen: 1, Info: fac, X: 1000, Z: 1000, Built: true}
	stuck := func(tick uint32, u aikit.OwnUnit) int32 {
		s.tick = tick
		o := &aikit.Obs{Tick: tick, Own: []aikit.OwnUnit{factory, u}}
		b := &core.Board{O: o, Factories: []int32{0}, Combat: []int32{1}}
		s.observeTraps(b)
		return s.facTrapped[0]
	}
	at := func(gen uint32, x int32) aikit.OwnUnit {
		return aikit.OwnUnit{H: pool.Handle(5), Gen: gen, Info: tank, X: x, Z: 1100, Built: true}
	}
	stuck(0, at(1, 1000))
	stuck(300, at(1, 2000)) // left
	if n := stuck(600, at(2, 1000)); n != 0 {
		t.Fatalf("a new tank counted stuck on sight: %d", n)
	}
	if n := stuck(600+trapTicks, at(2, 1000)); n != 1 {
		t.Errorf("the slot's new tank, parked 90 s at the factory, stuck count %d, want 1", n)
	}
	s.traps = nil
	stuck(0, at(3, 1000)) // first seen at tick 0, never leaves, dies
	if n := stuck(trapTicks+100, at(4, 1000)); n != 0 {
		t.Errorf("a tank just out of the factory inherited its predecessor's 90 s: stuck count %d", n)
	}
}

// A first failure to place an extractor blames the spot (blocked a minute
// per failure there, and returned so the builder clears it); a boxed-in
// builder's repeat failure blames nothing; and once a frame of ours stands
// on the spot its failures are forgotten.
func TestSpotFailures(t *testing.T) {
	w := newDefWorld(DefaultParams())
	e, s := w.e, w.e.s
	w.think(3*1800, w.base())
	con := &w.obs.Own[1]
	c, bs := s.commitOf(con), s.bstateOf(con)
	*c = commitment{def: con.Info, gen: con.Gen, kind: cMex, prod: w.d.mex, spot: 1, tick: s.tick}
	if sp := e.fail(c, bs); sp != 1 || s.spotFails[1] != 1 || s.spotBlock[1] != s.tick+1800 {
		t.Errorf("first failure: blamed %d, fails %d, blocked to %d (want spot 1, 1, %d)", sp, s.spotFails[1], s.spotBlock[1], s.tick+1800)
	}
	*c = commitment{def: con.Info, gen: con.Gen, kind: cMex, prod: w.d.mex, spot: 2, tick: s.tick}
	if sp := e.fail(c, bs); sp != -1 || s.spotFails[2] != 0 {
		t.Errorf("a boxed-in builder's second failure blamed spot %d (fails %d)", sp, s.spotFails[2])
	}
	s.spotFails[1] = 5
	sp := &w.k.Map.Spots[1]
	frame := aikit.OwnUnit{H: 40, Gen: 1, Info: w.d.mex, X: sp.X, Z: sp.Z, HP: 10, MaxHP: w.d.mex.HP, Progress: 5}
	w.think(3*1800+15, append(w.base(), frame))
	if s.spotFails[1] != 0 {
		t.Errorf("an extractor frame on the spot left %d failures counted", s.spotFails[1])
	}
}

// A rich pile does not end the search: a richer or nearer one later in
// the list still wins (only a lane feature outranks every pile).
func TestBestClearKeepsLooking(t *testing.T) {
	s := &shared{needM: 3000}
	o := &aikit.Obs{Features: []aikit.Feature{
		{X: 1000, Z: 1000, Metal: 700, Reclaimable: true},
		{X: 300, Z: 300, Metal: 700, Reclaimable: true},
	}}
	b := &core.Board{O: o}
	u := &aikit.OwnUnit{X: 0, Z: 0}
	if v, x, z := s.bestClear(b, u); v != 3000 || x != 300 || z != 300 {
		t.Errorf("bestClear = %d at (%d, %d), want the nearer pile at (300, 300)", v, x, z)
	}
}

// A frame and the order of the builder building it are one tower, as in
// have: built towers plus the larger of the frames and the orders.
func TestTowersAtCountsFramesOnce(t *testing.T) {
	pl := &defPlan{zAncX: []int32{1000}, zAncZ: []int32{1000}}
	for c := range pl.zCnt {
		pl.zCnt[c], pl.zCntF[c] = make([]int32, 1), make([]int32, 1)
	}
	pl.zCnt[dcGround][0], pl.zCntF[dcGround][0] = 2, 1
	pl.commits = []defCommit{{cls: dcGround, x: 1010, z: 1000}}
	if n := pl.towersAt(0, dcGround); n != 3 {
		t.Errorf("two built, a frame and its builder's order: %d towers, want 3", n)
	}
	pl.commits = append(pl.commits, defCommit{cls: dcAir, dual: true, x: 990, z: 1000})
	if n := pl.towersAt(0, dcGround); n != 4 {
		t.Errorf("and a missile tower ordered: %d ground-plan towers, want 4", n)
	}
}

// Params{} is clamped into range, so no divisor is zero: the model sets
// up and thinks. Ambition 0 (unset) is the full plan everywhere, as the
// persona normalizes it.
func TestZeroParamsAndAmbition(t *testing.T) {
	w := newDefWorld(Params{})
	if p := w.e.s.p; p.ERatio != Specs[paramIndex("e_ratio")].Min || p.Horizon != Specs[paramIndex("h")].Min {
		t.Errorf("Params{} not clamped: e_ratio %d h %d", p.ERatio, p.Horizon)
	}
	w.think(3*1800, w.base())
	s := &shared{p: Params{Growth: 1}, k: &aikit.Kit{}}
	if !s.growth() || ambition(&s.k.Persona) != 100 {
		t.Error("ambition 0 is not the full plan")
	}
	p := DefaultParams()
	applyAmbition(&p, 0)
	if p != DefaultParams() {
		t.Error("ambition 0 scaled Params")
	}
}

// The bucketed pile sums and tower spacing index answer exactly as the
// plain scans over every feature and every own unit they replace.
func TestIndexesMatchScans(t *testing.T) {
	rnd := aikit.PlayerRand(7, 0)
	var feats []aikit.Feature
	for i := 0; i < 300; i++ {
		feats = append(feats, aikit.Feature{X: int32(rnd.Range(200, 3400)), Z: int32(rnd.Range(200, 3400)),
			Metal: int32(rnd.Range(-50, 300)), Reclaimable: rnd.Range(0, 9) > 0})
	}
	s := &shared{}
	o := &aikit.Obs{Features: feats}
	piles := s.piles(o)
	for i := range feats {
		f := &feats[i]
		if !f.Reclaimable || f.Metal <= 0 {
			continue
		}
		var want int64
		for j := range feats {
			g := &feats[j]
			if g.Reclaimable && g.Metal > 0 && aikit.Dist2(g.X, g.Z, f.X, f.Z) <= clearRadius*clearRadius {
				want += int64(g.Metal)
			}
		}
		if piles[i] != want {
			t.Fatalf("pile %d at (%d, %d) = %d, scan %d", i, f.X, f.Z, piles[i], want)
		}
	}

	d := newDefUnits()
	kinds := []*aikit.UnitInfo{d.fac, d.llt, d.mex, d.sol, d.con, d.rad}
	var own []aikit.OwnUnit
	for i := 0; i < 120; i++ {
		u := kinds[rnd.Range(0, int32(len(kinds)-1))]
		own = append(own, aikit.OwnUnit{H: pool.Handle(i + 1), Info: u, X: int32(rnd.Range(50, 4000)), Z: int32(rnd.Range(50, 4000))})
	}
	b := &core.Board{O: &aikit.Obs{Tick: 30, Own: own}, K: &aikit.Kit{Map: &aikit.MapInfo{WorldW: 4096, WorldH: 4096}}}
	pl := &defPlan{}
	scan := func(x, z int32) bool {
		for i := range own {
			f := &own[i]
			r := f.Info.Role
			if r.Has(aikit.RoleMobile) {
				continue
			}
			d2 := aikit.Dist2(x, z, f.X, f.Z)
			switch {
			case r.Has(aikit.RoleFactory):
				hw, hz := f.Info.FootX*8+64, f.Info.FootZ*8
				if d2 < defFacClear*defFacClear || (x >= f.X-hw && x <= f.X+hw && z >= f.Z-hz && z <= f.Z+hz+defFacExit) {
					return false
				}
			case r.Has(aikit.RoleDefense):
				if d2 < defSpace*defSpace {
					return false
				}
			default:
				if d2 < defGap*defGap {
					return false
				}
			}
		}
		return true
	}
	agree := 0
	for i := 0; i < 2000; i++ {
		x, z := int32(rnd.Range(32, 4064)), int32(rnd.Range(32, 4064))
		if got, want := pl.clear(b, x, z), scan(x, z); got != want {
			t.Fatalf("clear(%d, %d) = %v, scan %v", x, z, got, want)
		} else if got {
			agree++
		}
	}
	if agree == 0 {
		t.Error("no point was clear: the comparison tested nothing")
	}
}
