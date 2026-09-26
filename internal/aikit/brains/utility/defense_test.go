package utility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

// defUnits is a small authored-looking table: a commander and a
// constructor, the towers a base can hold (light, heavy, a missile tower,
// a flak gun, a strategic gun, an anti-missile system, a torpedo launcher)
// and the buildings they protect. The numbers are of the stock kind; the
// plan reads only the UnitInfo fields, never a key. The missile tower's
// AirDPS exceeds its DPS as aikit.BuildTable makes it for the stock one
// (its anti-air is credited from its damage table; the same weapon fires
// at ground units for its default damage); the flak gun's one weapon is
// to-air, so its DPS and AirDPS are the same fire.
type defUnits struct {
	t                                                              *aikit.Table
	com, con, llt, hlt, rl, flak, gun, amd, tl, fac, mex, sol, rad *aikit.UnitInfo
}

func newDefUnits() *defUnits {
	d := &defUnits{t: &aikit.Table{}}
	mk := func(key string, role aikit.Role, m, e, hp, dps, air, rng int32) *aikit.UnitInfo {
		u := &aikit.UnitInfo{Index: int32(len(d.t.Units)), Key: key, Side: "ARM", Role: role,
			Metal: m, Energy: e, Value: m + e/aikit.EnergyPerMetal, HP: hp, DPS: dps, AirDPS: air, Range: rng,
			BuildTime: 5000, FootX: 2, FootZ: 2, Depth: 1}
		d.t.Units = append(d.t.Units, u)
		return u
	}
	d.com = mk("com", aikit.RoleCommander|aikit.RoleBuilder|aikit.RoleMobile, 2000, 20000, 3000, 70, 0, 200)
	d.com.BuildPower, d.com.Speed, d.com.Depth = 300, 35, 0
	d.con = mk("con", aikit.RoleBuilder|aikit.RoleMobile, 120, 2410, 700, 0, 0, 0)
	d.con.BuildPower, d.con.Speed, d.con.Depth = 80, 23, 2
	d.llt = mk("llt", aikit.RoleDefense, 262, 2546, 750, 120, 0, 300)
	d.hlt = mk("hlt", aikit.RoleDefense, 584, 5398, 1230, 192, 0, 430)
	d.rl = mk("rl", aikit.RoleDefense|aikit.RoleAntiAir, 79, 843, 295, 23, 48, 700)
	d.gun = mk("gun", aikit.RoleDefense|aikit.RoleArtillery, 4184, 64680, 1800, 285, 0, 4096)
	d.amd = mk("amd", aikit.RoleDefense, 1437, 88000, 780, 4, 0, 32000)
	d.tl = mk("tl", aikit.RoleDefense|aikit.RoleSonar, 804, 2658, 1450, 153, 0, 400)
	d.tl.Def = &content.UnitDef{MinWaterDepth: 1}
	d.fac = mk("fac", aikit.RoleFactory, 620, 1000, 2580, 0, 0, 0)
	d.fac.FootX, d.fac.FootZ, d.fac.BuildPower = 6, 6, 100
	d.mex = mk("mex", aikit.RoleExtractor, 50, 500, 200, 0, 0, 0)
	d.mex.MetalMake = 100000
	d.sol = mk("sol", aikit.RoleEnergy, 150, 0, 300, 0, 0, 0)
	d.sol.EnergyMake = 20
	d.rad = mk("rad", aikit.RoleRadar, 50, 500, 100, 0, 0, 0)
	d.flak = mk("flak", aikit.RoleDefense|aikit.RoleAntiAir, 823, 20000, 1524, 216, 216, 700)
	d.flak.Def = &content.UnitDef{Weapon1Def: &content.WeaponDef{ID: 1, DamageDefault: 108, ReloadTime: 15, ToAirWeapon: true}}
	d.com.Builds = []*aikit.UnitInfo{d.llt, d.tl, d.mex, d.sol, d.fac}
	d.con.Builds = []*aikit.UnitInfo{d.llt, d.hlt, d.rl, d.gun, d.amd, d.tl, d.mex, d.sol, d.fac, d.rad}
	return d
}

// defWorld is one think's situation: the brain, its board and the
// observation it last saw.
type defWorld struct {
	d   *defUnits
	k   *aikit.Kit
	b   *core.Board
	st  *Strategy
	e   *Economy
	obs *aikit.Obs
}

// Home is at (512, 512), the only other start at (3584, 3584).
func newDefWorld(p Params) *defWorld {
	d := newDefUnits()
	m := &aikit.MapInfo{CellW: 256, CellH: 256, WorldW: 4096, WorldH: 4096, SectorW: 32, SectorH: 32,
		Starts: [][2]int32{{512, 512}, {3584, 3584}}, FootX: 2, FootZ: 2}
	for _, xz := range [][2]int32{{560, 420}, {1500, 1400}, {1560, 1470}, {1440, 1500}} {
		m.Spots = append(m.Spots, aikit.MetalSpot{CellX: xz[0]/16 - 1, CellZ: xz[1]/16 - 1, X: xz[0], Z: xz[1], Metal: 400})
	}
	rnd := aikit.PlayerRand(1, 0)
	k := &aikit.Kit{Side: "ARM", Table: d.t, Map: m, Persona: aikit.PersonaHard, Rand: &rnd}
	st, ec, _ := Policies(p)
	// The defense rules are measured against the deterministic brain: the
	// per-game style would otherwise rescale w_defense.
	if err := st.SetVariety(Variety{Style: "balanced"}); err != nil {
		panic(err)
	}
	w := &defWorld{d: d, k: k, b: &core.Board{}, st: st, e: ec}
	w.b.K = k
	st.Init(w.b)
	return w
}

// think observes own units at tick and runs the strategy layer, which
// refreshes the shared model the economy scores against.
func (w *defWorld) think(tick uint32, own []aikit.OwnUnit) {
	w.obs = &aikit.Obs{Tick: tick, Own: own,
		Metal:  aikit.Res{Stock: 400, Cap: 1000, Income: 15, Expense: 14},
		Energy: aikit.Res{Stock: 2000, Cap: 5000, Income: 250, Expense: 240}}
	w.k.Tick = tick
	w.b.Update(w.k, w.obs)
	w.st.Plan(w.b)
}

// preArmy is the default Params without the army switch (README §13.12):
// the rules below are the plan's own, which army=0 plays unchanged.
func preArmy() Params {
	p := DefaultParams()
	p.Army = 0
	return p
}

func own(h int, u *aikit.UnitInfo, x, z int32) aikit.OwnUnit {
	return aikit.OwnUnit{H: pool.Handle(h), Info: u, X: x, Z: z, HP: u.HP, MaxHP: u.HP, Built: true, Progress: 100}
}

// base is a commander at home, a constructor, a factory in front of home,
// a home extractor and solar, and a three-extractor expansion toward the
// enemy.
func (w *defWorld) base() []aikit.OwnUnit {
	d := w.d
	return []aikit.OwnUnit{
		own(1, d.com, 512, 512), own(2, d.con, 700, 700),
		own(3, d.fac, 660, 640), own(4, d.mex, 560, 420), own(5, d.sol, 400, 400),
		own(6, d.mex, 1500, 1400), own(7, d.mex, 1560, 1470), own(8, d.mex, 1440, 1500),
	}
}

func TestDefenseClassify(t *testing.T) {
	d := newDefUnits()
	s := &shared{p: DefaultParams()}
	s.setup(&aikit.Kit{Side: "ARM", Table: d.t, Map: &aikit.MapInfo{}})
	for _, c := range []struct {
		u    *aikit.UnitInfo
		want uint8
	}{{d.llt, dcGround}, {d.hlt, dcGround}, {d.rl, dcAir}, {d.flak, dcAir}, {d.tl, dcWater}, {d.gun, dcNone}, {d.amd, dcNone}, {d.fac, dcNone}, {d.com, dcNone}} {
		if got := classify(c.u, &s.info[c.u.Index]); got != c.want {
			t.Errorf("classify(%s) = %d, want %d", c.u.Key, got, c.want)
		}
	}
}

// Ground fire is DPS less the to-air weapons' fire: the missile tower
// fires its one weapon at ground units too (and serves the ground plan
// under the front rules), the flak gun only at aircraft (so it is neither
// a missile tower nor rated for the ground plan).
func TestDefenseGroundFire(t *testing.T) {
	w := newDefWorld(DefaultParams())
	d := w.d
	s := w.e.s
	if g := s.info[d.flak.Index].gndDPS; g != 0 {
		t.Errorf("flak ground DPS %d, want 0", g)
	}
	if g := s.info[d.rl.Index].gndDPS; g != int64(d.rl.DPS) {
		t.Errorf("missile tower ground DPS %d, want its DPS %d", g, d.rl.DPS)
	}
	w.think(7*1800, w.base())
	pl := w.e.plan()
	pl.refresh(s, w.b)
	if !pl.dual[d.rl.Index] || pl.dual[d.flak.Index] {
		t.Errorf("missile tower dual %v (want true), flak dual %v (want false)", pl.dual[d.rl.Index], pl.dual[d.flak.Index])
	}
	if q := quality(s, d.flak, dcGround, 150); q != 0 {
		t.Errorf("flak rated %d for the ground plan, want 0", q)
	}
}

// On the stock units: the missile towers serve the ground plan, the flak
// guns do not.
func TestDefenseMissileTowersRetail(t *testing.T) {
	cat, _ := retailcat.Shared(t)
	tab := aikit.BuildTable(cat, &construction.ModernRules{})
	m := &aikit.MapInfo{CellW: 256, CellH: 256, WorldW: 4096, WorldH: 4096, SectorW: 32, SectorH: 32}
	for _, side := range []string{"ARM", "CORE"} {
		k := &aikit.Kit{Side: side, Table: tab, Map: m}
		s := &shared{p: DefaultParams()}
		s.setup(k)
		pl := &defPlan{}
		pl.setup(s, &core.Board{K: k})
		for _, c := range []struct {
			key  string
			dual bool
		}{{"armrl", true}, {"corrl", true}, {"armflak", false}, {"corflak", false}} {
			u := tab.Lookup(c.key)
			if u == nil {
				t.Fatalf("%s: not in the retail table", c.key)
			}
			if pl.cls[u.Index] != dcAir || pl.dual[u.Index] != c.dual {
				t.Errorf("%s (as %s): class %d dual %v, want anti-air, dual %v", c.key, side, pl.cls[u.Index], pl.dual[u.Index], c.dual)
			}
		}
	}
}

// The quality blend is the square law on a small income (the light tower
// wins) and strength per cost on a large one (the heavy tower wins).
func TestDefenseQualityShiftsWithIncome(t *testing.T) {
	d := newDefUnits()
	s := &shared{p: preArmy()}
	s.setup(&aikit.Kit{Side: "ARM", Table: d.t, Map: &aikit.MapInfo{}})
	if l, h := quality(s, d.llt, dcGround, 150), quality(s, d.hlt, dcGround, 150); l <= h {
		t.Errorf("small income: light %d should beat heavy %d", l, h)
	}
	if l, h := quality(s, d.llt, dcGround, 6000), quality(s, d.hlt, dcGround, 6000); h <= l {
		t.Errorf("large income: heavy %d should beat light %d", h, l)
	}
	s.spendable = 0
	if v := blend(s); v != 150 {
		t.Errorf("blend floor = %d, want 150", v)
	}
	s.spendable = 1 << 40
	if v := blend(s); v != 6000 {
		t.Errorf("blend cap = %d, want 6000", v)
	}
}

func TestDefenseLateralSlots(t *testing.T) {
	want := [][2]int64{{0, 0}, {defLane, 0}, {-defLane, 0}, {2 * defLane, 0}, {-2 * defLane, 0}, {3 * defLane, 0}, {-3 * defLane, 0}, {0, defLane}}
	for k, w := range want {
		if side, row := lateral(int32(k)); side != w[0] || row != w[1] {
			t.Errorf("lateral(%d) = (%d, %d), want (%d, %d)", k, side, row, w[0], w[1])
		}
	}
}

// A tower point never sits beside a factory or in the corridor in front of
// its exit (+Z), built or framed, and keeps a walkable gap from other
// towers and buildings.
func TestDefenseSiteClearOfFactory(t *testing.T) {
	w := newDefWorld(DefaultParams())
	own := w.base()
	own = append(own, aikit.OwnUnit{H: 9, Info: w.d.fac, X: 2000, Z: 2000, HP: 10, MaxHP: w.d.fac.HP, Progress: 5},
		aikit.OwnUnit{H: 10, Info: w.d.llt, X: 1200, Z: 1200, HP: 100, MaxHP: w.d.llt.HP, Built: true, Progress: 100})
	w.think(300, own)
	pl := w.e.plan()
	for _, c := range []struct {
		x, z int32
		want bool
	}{
		{660, 640 + 150, false}, // right beside it
		{660, 640 + 600, false}, // deep in its exit corridor
		{660 + 90, 640 + 400, false},
		{660 + 300, 640 + 400, true}, // beside the corridor
		{660, 640 - 300, true},       // behind it
		{2000, 2400, false},          // in front of the framed factory
		{1250, 1250, false},          // beside another tower
		{1400, 1200, true},           // a walkable gap away from it
		{560 + 60, 420, false},       // glued to an extractor
	} {
		if got := pl.clear(w.b, c.x, c.z); got != c.want {
			t.Errorf("clear(%d, %d) = %v, want %v", c.x, c.z, got, c.want)
		}
	}
}

// The opening asks for nothing at two minutes, then a light tower by five:
// placed toward the enemy, clear of the factory, and only light towers from
// a commander that cannot build anything heavier. Strategic guns and
// anti-missile systems are never base defense, and nothing asks for
// anti-air or water towers without aircraft or a navy. (Without the front
// rules: the layout switch off.)
func TestDefensePlanOpening(t *testing.T) {
	p := DefaultParams()
	p.Layout = 0
	w := newDefWorld(p)
	d := w.d
	w.think(2*1800, w.base())
	com := &w.obs.Own[0]
	if c := w.e.defenseCand(w.b, com, d.llt); c.score != 0 {
		t.Fatalf("minute 2: light tower scored %d, want 0", c.score)
	}
	w.think(5*1800+300, w.base())
	com, con := &w.obs.Own[0], &w.obs.Own[1]
	c := w.e.defenseCand(w.b, com, d.llt)
	if c.score <= 0 || c.kind != cDefense || c.spot != -1 {
		t.Fatalf("minute 5: light tower %+v, want a positive defense candidate", c)
	}
	pl := w.e.plan()
	if pl.deficit[dcGround] < pl.cRef[dcGround] {
		t.Errorf("minute 5: ground deficit %d, want at least one light tower (%d)", pl.deficit[dcGround], pl.cRef[dcGround])
	}
	if !pl.clear(w.b, c.x, c.z) {
		t.Errorf("site (%d,%d) is not clear of the factory", c.x, c.z)
	}
	z := pl.zone[dcGround]
	if aikit.Dist2(c.x, c.z, 3584, 3584) >= aikit.Dist2(pl.zAncX[z], pl.zAncZ[z], 3584, 3584) {
		t.Errorf("site (%d,%d) is not ahead of its anchor (%d,%d) toward the enemy", c.x, c.z, pl.zAncX[z], pl.zAncZ[z])
	}
	for _, p := range []*aikit.UnitInfo{d.gun, d.amd, d.rl, d.tl} {
		if c := w.e.defenseCand(w.b, con, p); c.score != 0 {
			t.Errorf("%s scored %d, want 0", p.Key, c.score)
		}
	}
	// A builder's choice among its own products: the constructor's light
	// tower is scored against its heavy one, the commander's alone.
	if c := w.e.defenseCand(w.b, com, d.llt); c.f[1] != one {
		t.Errorf("commander light tower quality × variety = %d, want %d (its best option)", c.f[1], one)
	}
}

// The plan follows the game every think, whether or not a builder prices
// a tower: a building that stood at one think and is gone at the next is
// a loss, and the budget accrued over a minute does not depend on how
// often the plan refreshed.
func TestDefensePlanEveryThink(t *testing.T) {
	busy := func(own []aikit.OwnUnit) []aikit.OwnUnit {
		for i := range own {
			if own[i].Info.Role.Has(aikit.RoleBuilder) {
				own[i].Order = aikit.OrderBuild // no builder prices a tower
			}
		}
		return own
	}
	w := newDefWorld(DefaultParams())
	extra := append(w.base(), own(30, w.d.sol, 300, 600))
	w.think(6*1800, busy(extra))
	w.e.Plan(w.b)
	w.think(6*1800+15, busy(w.base()))
	w.e.Plan(w.b)
	if pl := w.e.plan(); pl.lossRecent == 0 {
		t.Error("a solar lost between two thinks with every builder busy was not seen")
	}

	var bud [2]int64
	for i, every := range []uint32{15, 1800} {
		w := newDefWorld(DefaultParams())
		for tick := uint32(6 * 1800); tick <= 7*1800; tick += every {
			w.think(tick, busy(w.base()))
			w.e.Plan(w.b)
			if tick == 6*1800 {
				w.e.plan().budG = 0 // the first refresh banks the opening
			}
		}
		bud[i] = w.e.plan().budG
	}
	if bud[0] <= 0 || bud[0]-bud[1] > 1 || bud[1]-bud[0] > 1 {
		t.Errorf("budget after a minute: %d refreshing every think, %d refreshing once", bud[0], bud[1])
	}
}

// def_plan=0 is exactly the reactive evaluation.
func TestDefensePlanOffIsReactive(t *testing.T) {
	p := DefaultParams()
	p.DefPlan = 0
	w := newDefWorld(p)
	w.think(12*1800, w.base())
	con := &w.obs.Own[1]
	w.e.bestDef = 1
	for _, q := range w.d.con.Builds {
		if q.Role.Has(aikit.RoleDefense) {
			if ef := w.e.s.eff(q, w.e.s.aaNeed); ef > w.e.bestDef {
				w.e.bestDef = ef
			}
		}
	}
	for _, q := range []*aikit.UnitInfo{w.d.llt, w.d.hlt, w.d.rl} {
		if got, want := w.e.defenseCand(w.b, con, q), w.e.evalDefense(w.b, con, q); got != want {
			t.Errorf("%s: def_plan=0 gave %+v, reactive %+v", q.Key, got, want)
		}
	}
}

// The plan's amounts follow w_defense (a turtle style raising it builds
// more, a rush style lowering it builds less), capped at defWMax.
func TestDefenseAmountsScaleWithWeight(t *testing.T) {
	var shares, floors []int64
	for _, wd := range []int32{0, 18, 60, 300, 400} {
		p := DefaultParams()
		p.WDefense = wd
		w := newDefWorld(p)
		w.think(15*1800, w.base())
		pl := w.e.plan()
		pl.refresh(w.e.s, w.b)
		shares = append(shares, pl.shareG(w.e.s, w.b))
		floors = append(floors, pl.floorG(w.e.s))
	}
	for i := 1; i < len(shares); i++ {
		if shares[i] < shares[i-1] || floors[i] < floors[i-1] {
			t.Errorf("amounts not monotonic in w_defense: shares %v floors %v", shares, floors)
		}
	}
	if shares[0] != 0 || floors[0] != 0 {
		t.Errorf("w_defense=0 still budgets: share %d floor %d", shares[0], floors[0])
	}
	if shares[3] != shares[4] {
		t.Errorf("share above the cap still grows: %v", shares)
	}
}

// A building that disappears with nothing of ours in its place was lost:
// its zone remembers the raid and half its cost funds the budget; a
// building replaced in place is not a loss.
func TestDefenseLossFundsBudget(t *testing.T) {
	p := DefaultParams()
	p.Layout = 0 // under the front rules the bank is already full at minute 10
	w := newDefWorld(p)
	base := w.base()
	w.think(10*1800, base)
	pl := w.e.plan()
	pl.refresh(w.e.s, w.b)
	before := pl.budG
	// The expansion's first extractor (handle 6) is gone; the home
	// extractor (handle 4) was rebuilt in place under a new handle.
	var after []aikit.OwnUnit
	for _, u := range base {
		switch u.H {
		case 6:
			continue
		case 4:
			u.H = 20
		}
		after = append(after, u)
	}
	w.think(10*1800+30, after)
	pl.refresh(w.e.s, w.b)
	z := pl.zoneOf(1500, 1400)
	if pl.heatLoss[z] != w.e.s.info[w.d.mex.Index].costMeq {
		t.Errorf("raid heat at the expansion = %d, want one extractor (%d)", pl.heatLoss[z], w.e.s.info[w.d.mex.Index].costMeq)
	}
	if h := pl.heatLoss[pl.zoneOf(560, 420)]; h != 0 {
		t.Errorf("rebuilt home extractor counted as lost: heat %d", h)
	}
	if pl.budG <= before {
		t.Errorf("budget %d did not grow from %d after a loss", pl.budG, before)
	}
}

// The front rules (layout and plan on, the defaults): the missile tower is
// the ground plan's reference tower and serves it; the first tower is a
// missile tower, from a constructor, while the commander leaves ground
// towers to constructors; the bank stays in light-tower units.
func TestDefenseFrontOpening(t *testing.T) {
	w := newDefWorld(DefaultParams())
	d := w.d
	w.think(7*1800, w.base())
	pl := w.e.plan()
	pl.refresh(w.e.s, w.b)
	if !pl.front || !pl.dual[d.rl.Index] || pl.dual[d.llt.Index] || pl.dual[d.hlt.Index] {
		t.Fatalf("front %v, missile flags rl %v llt %v hlt %v", pl.front, pl.dual[d.rl.Index], pl.dual[d.llt.Index], pl.dual[d.hlt.Index])
	}
	if rl := w.e.s.info[d.rl.Index].costMeq; pl.cRef[dcGround] != rl || pl.cBank != w.e.s.info[d.llt.Index].costMeq {
		t.Errorf("ground reference %d (want the missile tower's %d), bank unit %d", pl.cRef[dcGround], rl, pl.cBank)
	}
	com, con := &w.obs.Own[0], &w.obs.Own[1]
	if c := w.e.defenseCand(w.b, com, d.llt); c.score != 0 {
		t.Errorf("commander light tower scored %d beside a constructor that builds missile towers", c.score)
	}
	rl := w.e.defenseCand(w.b, con, d.rl)
	if rl.score <= 0 {
		t.Fatalf("missile tower %+v, want a positive ground-plan candidate", rl)
	}
	for _, p := range []*aikit.UnitInfo{d.llt, d.hlt} {
		if c := w.e.defenseCand(w.b, con, p); c.score != 0 {
			t.Errorf("first tower: %s scored %d, want the missile tower first", p.Key, c.score)
		}
	}
}

// Direct-fire towers are held to the humans' share: none while missile
// towers are under frontMixLo of the ground-plan towers, fully from
// frontMixHi.
func TestDefenseFrontMix(t *testing.T) {
	w := newDefWorld(preArmy())
	d := w.d
	tower := func(h int, u *aikit.UnitInfo, x, z int32) aikit.OwnUnit { return own(h, u, x, z) }
	base := append(w.base(), tower(20, d.llt, 900, 900))
	w.think(12*1800, base)
	pl := w.e.plan()
	pl.refresh(w.e.s, w.b)
	con := &w.obs.Own[1]
	w.e.defenseCand(w.b, con, d.rl) // the builder's normalizers
	if m := pl.mixOf(d.llt, dcGround); m != 0 {
		t.Errorf("one light tower, no missile tower: light mix %d, want 0", m)
	}
	if m := pl.mixOf(d.rl, dcGround); m != one {
		t.Errorf("missile tower mix %d, want %d", m, one)
	}
	for i := 0; i < 4; i++ {
		base = append(base, tower(21+i, d.rl, 1300+int32(i)*200, 700))
	}
	w.think(12*1800+30, base)
	pl.refresh(w.e.s, w.b)
	w.e.defenseCand(w.b, &w.obs.Own[1], d.rl)
	want := lin(800, frontMixLo, frontMixHi)
	if m := pl.mixOf(d.llt, dcGround); m != want {
		t.Errorf("four missile towers of five: light mix %d, want %d", m, want)
	}
	if pl.have[dcGround] != pl.have[dcAir]+w.e.s.info[d.llt.Index].costMeq {
		t.Errorf("ground have %d, anti-air have %d: missile towers should count toward both", pl.have[dcGround], pl.have[dcAir])
	}
}

// The front rules' pacing: nothing before 3:30, a flat share from minute 6
// to 20, more after; and the switches off keep the old shares exactly.
func TestDefenseFrontPacing(t *testing.T) {
	for _, c := range []struct{ min, want int64 }{{3, 0}, {6, frontEarly}, {12, frontEarly}, {20, frontEarly}, {25, frontEarly + frontLate/2}, {30, frontEarly + frontLate}} {
		if got := frontBase(c.min * 1800); got != c.want {
			t.Errorf("frontBase(minute %d) = %d, want %d", c.min, got, c.want)
		}
	}
	on := newDefWorld(DefaultParams())
	p := DefaultParams()
	p.Layout = 0
	off := newDefWorld(p)
	for _, w := range []*defWorld{on, off} {
		w.think(8*1800, w.base())
		w.e.plan().refresh(w.e.s, w.b)
	}
	if a, b := on.e.plan().share, off.e.plan().share; a <= b {
		t.Errorf("minute 8: front share %d‰ not above the old %d‰", a, b)
	}
	if pl := off.e.plan(); pl.front || pl.haveDual || pl.dual[off.d.rl.Index] || pl.cRef[dcGround] != pl.cBank {
		t.Errorf("layout off still applies the front rules: front %v dual %v cRef %d bank %d", pl.front, pl.dual[off.d.rl.Index], pl.cRef[dcGround], pl.cBank)
	}
}

// A choke's neck — nearer the choke than its farthest site, and within
// chokeNeck at least — holds only the post's own towers: other tower
// points there are refused, beyond it they are not.
func TestDefenseFrontNeck(t *testing.T) {
	pl := &defPlan{chokes: []aikit.Choke{{X: 1000, Z: 1000, NSites: 2, Sites: [4][2]int32{{700, 1000}, {1000, 1500}}}}}
	for _, c := range []struct {
		x, z int32
		want bool
	}{{1000, 1000, true}, {1000, 1450, true}, {1300, 1000, true}, {1000, 1510, false}, {1600, 1000, false}} {
		if got := pl.inNeck(c.x, c.z); got != c.want {
			t.Errorf("inNeck(%d, %d) = %v, want %v", c.x, c.z, got, c.want)
		}
	}
	pl.chokes[0].NSites = 0
	if !pl.inNeck(1000, 1300) || pl.inNeck(1000, 1330) {
		t.Errorf("a post without sites keeps a neck of %d wu", chokeNeck)
	}
}

// Tower timing's ceiling: no ground tower is owed once the towers reach
// timeCap of the tier's curve (8.5 at minute 10 at full ambition), with or
// without the budget for more; with tower_time off the plan owes them.
func TestDefenseTimeCeiling(t *testing.T) {
	for _, on := range []bool{true, false} {
		w := newDefWorld(preArmy())
		w.e.s.zones.f2.towers = on
		d := w.d
		base := w.base()
		for i := 0; i < 9; i++ {
			base = append(base, own(21+i, d.rl, 1300+int32(i)*200, 700))
		}
		w.think(10*1800, base)
		pl := w.e.plan()
		pl.refresh(w.e.s, w.b)
		pl.budG = pl.have[dcGround] + 2*pl.cBank
		pl.finish(w.e.s, w.b)
		if got := pl.deficit[dcGround] > 0; got == on {
			t.Errorf("tower_time %v, nine towers at minute 10: ground deficit %d", on, pl.deficit[dcGround])
		}
		if c := w.e.defenseCand(w.b, &w.obs.Own[1], d.rl); (c.score > 0) == on {
			t.Errorf("tower_time %v: missile tower scored %d", on, c.score)
		}
	}
}

// Tower timing's pacing: the front share ×1.5 to minute 10, back to ×1 by
// minute 16; and the budget share with tower_time on exceeds it off at
// minute 8 by that factor.
func TestDefenseTimePacing(t *testing.T) {
	for _, c := range []struct{ min, want int64 }{{0, 1500}, {10, 1500}, {13, 1250}, {16, 1000}, {25, 1000}} {
		if got := timeBoost(c.min * 1800); got != c.want {
			t.Errorf("timeBoost(minute %d) = %d, want %d", c.min, got, c.want)
		}
	}
	var share [2]int64
	for i, on := range []bool{true, false} {
		w := newDefWorld(DefaultParams())
		w.e.s.zones.f2.towers = on
		w.think(8*1800, w.base())
		w.e.plan().refresh(w.e.s, w.b)
		share[i] = w.e.plan().terms[0]
	}
	if share[0] != share[1]*1500/1000 {
		t.Errorf("minute 8 base share %d‰ on, %d‰ off: want ×1.5", share[0], share[1])
	}
}
