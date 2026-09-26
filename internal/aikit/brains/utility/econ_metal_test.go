package utility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// metalWorld is defWorld's table with a metal maker and an energy store,
// a persona of choice and four spots: one under our extractor by home,
// one free 700 wu out (inside the medium commander's leash of 846 wu),
// one free 1,330 wu out and one under our second extractor beside it.
type metalWorld struct {
	*defWorld
	mkr, estor *aikit.UnitInfo
}

const (
	spotHome = iota
	spotNear
	spotFar
	spotOurs
)

func newMetalWorld(p Params, per aikit.Persona) *metalWorld {
	d := newDefUnits()
	add := func(u *aikit.UnitInfo) *aikit.UnitInfo {
		u.Index, u.Side, u.BuildTime, u.FootX, u.FootZ, u.Depth = int32(len(d.t.Units)), "ARM", 5000, 2, 2, 1
		u.Value = u.Metal + u.Energy/aikit.EnergyPerMetal
		d.t.Units = append(d.t.Units, u)
		return u
	}
	mkr := add(&aikit.UnitInfo{Key: "mkr", Role: aikit.RoleMetalMaker, Metal: 1, Energy: 1154, HP: 100, MetalMake: 100, EnergyUse: 60})
	estor := add(&aikit.UnitInfo{Key: "estor", Role: aikit.RoleStorage, Metal: 170, Energy: 1700, HP: 1000, EnergyStore: 6000})
	d.con.Builds = append(d.con.Builds, mkr, estor)
	m := &aikit.MapInfo{CellW: 256, CellH: 256, WorldW: 4096, WorldH: 4096, SectorW: 32, SectorH: 32,
		Starts: [][2]int32{{512, 512}, {3584, 3584}}, FootX: 2, FootZ: 2}
	for _, xz := range [][2]int32{{560, 420}, {1100, 900}, {1500, 1400}, {1560, 1470}} {
		m.Spots = append(m.Spots, aikit.MetalSpot{CellX: xz[0]/16 - 1, CellZ: xz[1]/16 - 1, X: xz[0], Z: xz[1], Metal: 400})
	}
	rnd := aikit.PlayerRand(1, 0)
	k := &aikit.Kit{Side: "ARM", Table: d.t, Map: m, Persona: per, Rand: &rnd}
	st, ec, _ := Policies(p)
	if err := st.SetVariety(Variety{Style: "balanced"}); err != nil {
		panic(err)
	}
	w := &metalWorld{defWorld: &defWorld{d: d, k: k, b: &core.Board{}, st: st, e: ec}, mkr: mkr, estor: estor}
	w.b.K = k
	st.Init(w.b)
	return w
}

// thinkWith observes the base and the resources at tick.
func (w *metalWorld) thinkWith(tick uint32, own []aikit.OwnUnit, metal, energy aikit.Res) {
	w.obs = &aikit.Obs{Tick: tick, Own: own, Metal: metal, Energy: energy}
	w.k.Tick = tick
	w.b.Update(w.k, w.obs)
	w.st.Plan(w.b)
}

// base: the commander and a constructor at home, a factory, extractors on
// the home spot and the far pair's second spot, and eight solar
// collectors (160 energy a second of steady output).
func (w *metalWorld) metalBase() []aikit.OwnUnit {
	d := w.d
	u := []aikit.OwnUnit{own(1, d.com, 512, 512), own(2, d.con, 700, 700), own(3, d.fac, 660, 640),
		own(4, d.mex, 560, 420), own(5, d.mex, 1560, 1470)}
	for i := 0; i < 8; i++ {
		u = append(u, own(10+i, d.sol, 300+int32(i)*40, 300))
	}
	return u
}

func (w *metalWorld) con() *aikit.OwnUnit { return &w.obs.Own[1] }

// mexOffer is the extractor spot the constructor is offered, -1 for none.
func (w *metalWorld) mexOffer() int32 {
	e := w.e
	u := w.con()
	var d decision
	d.reset(e.s.tick, u.Info, u.X, u.Z)
	if e.s.p.Naval != 0 {
		e.evalMexN(w.b, u, e.s.bstateOf(u), &d)
	} else {
		e.evalMex(w.b, u, e.s.bstateOf(u), &d)
	}
	if d.n == 0 {
		return -1
	}
	return d.top[0].spot
}

var (
	metalShortRes = aikit.Res{Stock: 0, Cap: 1000, Income: 5, Expense: 20}
	metalAmpleRes = aikit.Res{Stock: 900, Cap: 1000, Income: 20, Expense: 5}
	energyFullRes = aikit.Res{Stock: 4900, Cap: 5000, Income: 300, Expense: 100}
)

// Part 1: no maker while a free safe spot stands open; with every spot
// taken the maker is the metal left to buy. metal=0 builds it anyway.
func TestMakersWaitForFreeSpots(t *testing.T) {
	for _, metal := range []int32{7, 0} {
		p := DefaultParams()
		p.Metal = metal
		w := newMetalWorld(p, aikit.PersonaHard)
		w.thinkWith(12*1800, w.metalBase(), metalShortRes, energyFullRes)
		if w.e.s.freeSafe == 0 {
			t.Fatal("the world has no free safe spot")
		}
		c := w.e.evalMaker(w.b, w.con(), w.mkr)
		if held := c.score == 0; held != (metal != 0) {
			t.Errorf("metal=%d: maker score %d with %d free safe spots", metal, c.score, w.e.s.freeSafe)
		}
		// Every spot ours: the maker is the last resort.
		all := append(w.metalBase(), own(30, w.d.mex, 1100, 900), own(31, w.d.mex, 1500, 1400))
		w.thinkWith(12*1800+30, all, metalShortRes, energyFullRes)
		if c := w.e.evalMaker(w.b, w.con(), w.mkr); w.e.s.freeSafe != 0 || c.score <= 0 {
			t.Errorf("metal=%d: no free spot (%d) and the maker scores %d", metal, w.e.s.freeSafe, c.score)
		}
	}
}

// Part 2: no energy building or energy store while the store is nine
// tenths full and not draining. The reported expense is what builds
// request, so a full store with metal short holds too; a full store
// draining while metal is ample does not, nor does the opening lead.
func TestEnergyFollowsMetal(t *testing.T) {
	cases := []struct {
		name          string
		metal, energy aikit.Res
		lead          bool
		held          bool
	}{
		{"full and covering, metal short", metalShortRes, energyFullRes, false, true},
		{"full and covering, metal ample", metalAmpleRes, energyFullRes, false, true},
		{"full, requested above income, metal short", metalShortRes, aikit.Res{Stock: 4900, Cap: 5000, Income: 100, Expense: 300}, false, true},
		{"full and draining, metal ample", metalAmpleRes, aikit.Res{Stock: 4900, Cap: 5000, Income: 100, Expense: 300}, false, false},
		{"four fifths full", metalShortRes, aikit.Res{Stock: 4000, Cap: 5000, Income: 300, Expense: 100}, false, false},
		{"the opening lead", metalShortRes, energyFullRes, true, false},
	}
	for _, metal := range []int32{7, 0} {
		for _, c := range cases {
			p := DefaultParams()
			p.Metal = metal
			w := newMetalWorld(p, aikit.PersonaHard)
			tick := uint32(12 * 1800)
			base := w.metalBase()
			if c.lead {
				// Two energy buildings of a lead of four, in the first
				// minute.
				tick = 60 * 30
				base = base[:7]
				w.st.vr.lead = maxLead // (Init indexed the energy buildings: tower timing counts towers)
			}
			w.thinkWith(tick, base, c.metal, c.energy)
			sol := w.e.evalEnergy(w.b, w.con(), w.d.sol)
			held := c.held && metal != 0
			if (sol.score == 0) != held {
				t.Errorf("metal=%d, %s: solar score %d, want held %v", metal, c.name, sol.score, held)
			}
			if held {
				if st := w.e.evalStorage(w.b, w.con(), w.estor); st.score != 0 {
					t.Errorf("metal=%d, %s: energy store scores %d", metal, c.name, st.score)
				}
			}
		}
	}
	// With the store full and nothing held, the store is wanted: the
	// energy-store check above is not vacuous.
	p := DefaultParams()
	p.Metal = 0
	w := newMetalWorld(p, aikit.PersonaHard)
	w.thinkWith(12*1800, w.metalBase(), metalShortRes, energyFullRes)
	if st := w.e.evalStorage(w.b, w.con(), w.estor); st.score <= 0 {
		t.Errorf("metal=0: a full energy store does not want storage (score %d)", st.score)
	}
}

// Part 4: when the ambition extractor cap binds (medium at minute 3 with
// two extractors), a free spot inside the commander's leash of home stays
// in the plan and one beyond it waits; without the part the cap holds
// every spot. At full ambition there is no cap.
func TestHomeSpotsUnderTheCap(t *testing.T) {
	for _, naval := range []int32{0, 1} {
		for _, c := range []struct {
			metal     int32
			per       aikit.Persona
			near, far int32 // offer with both spots free, and with only the far one
		}{
			{7, aikit.PersonaMed, spotNear, -1},
			{3, aikit.PersonaMed, -1, -1},
			{0, aikit.PersonaMed, -1, -1},
			{7, aikit.PersonaHard, spotNear, spotFar},
		} {
			p := DefaultParams()
			p.Metal, p.Naval = c.metal, naval
			w := newMetalWorld(p, c.per)
			w.thinkWith(3*1800, w.metalBase(), metalShortRes, energyFullRes)
			if got := w.mexOffer(); got != c.near {
				t.Errorf("naval=%d metal=%d %s: offered spot %d, want %d", naval, c.metal, c.per.Name, got, c.near)
			}
			w.e.s.spotBlock[spotNear] = 1 << 30
			w.thinkWith(3*1800+30, w.metalBase(), metalShortRes, energyFullRes)
			if got := w.mexOffer(); got != c.far {
				t.Errorf("naval=%d metal=%d %s, near spot blocked: offered spot %d, want %d", naval, c.metal, c.per.Name, got, c.far)
			}
		}
	}
}
