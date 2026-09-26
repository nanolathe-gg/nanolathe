package utility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/content"
)

func TestParamSpecsInRange(t *testing.T) {
	p := DefaultParams()
	s := p.slots()
	for i, sp := range Specs {
		if *s[i] != sp.Default || sp.Default < sp.Min || sp.Default > sp.Max {
			t.Errorf("%s: default %d outside [%d,%d] or not applied (%d)", sp.Name, sp.Default, sp.Min, sp.Max, *s[i])
		}
	}
}

func TestParseParams(t *testing.T) {
	p, err := ParseParams(map[string]string{"w_energy": "120", "w_threat_typo_free": "1"})
	if err == nil {
		t.Fatal("unknown parameter accepted")
	}
	p, err = ParseParams(map[string]string{"w_energy": "120", "h": "99999", "async": "1", "think": "10"})
	if err != nil {
		t.Fatal(err)
	}
	if p.WEnergy != 120 {
		t.Errorf("w_energy = %d, want 120", p.WEnergy)
	}
	if p.Horizon != 180 {
		t.Errorf("h = %d, want clamped 180", p.Horizon)
	}
	if _, err := ParseParams(map[string]string{"w_army": "x"}); err == nil {
		t.Error("non-integer accepted")
	}
}

// The curves are the vocabulary every consideration is written in; their
// end points and midpoints are the contract.
func TestCurves(t *testing.T) {
	cases := []struct {
		name      string
		got, want int64
	}{
		{"lin below", lin(0, 10, 20), 0},
		{"lin mid", lin(15, 10, 20), 500},
		{"lin above", lin(30, 10, 20), 1000},
		{"lin falling", lin(12, 20, 10), 800},
		{"half zero", half(0, 50), 1000},
		{"half at h", half(50, 50), 500},
		{"inv half", inv(500, 100, 5000), 2000},
		{"inv clamp", inv(10, 100, 5000), 5000},
		{"mul", mul(500, 500), 250},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}

func TestDecisionTopK(t *testing.T) {
	var d decision
	d.reset(0, nil, 0, 0)
	for _, sc := range []int64{5, 50, 0, 20, 40, 30, 10, 60} {
		c := cand{score: sc}
		d.offer(&c)
	}
	want := []int64{60, 50, 40, 30, 20}
	if d.n != len(want) {
		t.Fatalf("n = %d", d.n)
	}
	for i, w := range want {
		if d.top[i].score != w {
			t.Errorf("top[%d] = %d, want %d", i, d.top[i].score, w)
		}
	}
}

// Every style, ambition and jitter entry names a real parameter; a typo
// would silently perturb nothing.
func TestVarietyTablesNameParams(t *testing.T) {
	for _, st := range Styles {
		for _, m := range st.mods {
			if paramIndex(m.name) < 0 {
				t.Errorf("style %s: unknown parameter %q", st.Name, m.name)
			}
		}
	}
	for _, m := range ambitionScales {
		if paramIndex(m.name) < 0 {
			t.Errorf("ambition: unknown parameter %q", m.name)
		}
	}
	for _, m := range openingJitter {
		if paramIndex(m.name) < 0 {
			t.Errorf("jitter: unknown parameter %q", m.name)
		}
	}
	if Styles[0].Name != "balanced" || len(Styles[0].mods) != 0 || Styles[0].Weight != 0 {
		t.Error("style 0 must be the unperturbed, never drawn balanced style")
	}
	var total int32
	for _, st := range Styles {
		total += st.Weight
	}
	if total != 100 {
		t.Errorf("style weights are human shares in percent; they sum to %d", total)
	}
}

// balanced without jitter at full ambition is exactly the tuned brain and
// draws nothing; a named style draws nothing without jitter.
func TestVarietyDeterministicBaseline(t *testing.T) {
	for _, name := range []string{"balanced", "units"} {
		r := aikit.PlayerRand(51, 0)
		k := &aikit.Kit{Persona: aikit.PersonaHard, Rand: &r}
		var vr variety
		vr.v = Variety{Style: name}
		p := DefaultParams()
		vr.begin(k, &p)
		if fresh := aikit.PlayerRand(51, 0); r != fresh {
			t.Errorf("%s without jitter drew from the generator", name)
		}
		if name == "balanced" && p != DefaultParams() {
			t.Errorf("balanced changed Params: %+v", p)
		}
		if name == "units" && (p.FacTime >= DefaultParams().FacTime || vr.lead != 1) {
			t.Errorf("units: fac_time %d not earlier or lead %d not its modal 1", p.FacTime, vr.lead)
		}
		if name == "balanced" && vr.lead != 0 {
			t.Errorf("balanced has an opening lead %d", vr.lead)
		}
	}
}

// The same seed and slot choose the same style and Params; the draw varies
// across seeds.
func TestVarietyDrawReplays(t *testing.T) {
	draw := func(seed uint32) (int, Params) {
		r := aikit.PlayerRand(seed, 1)
		k := &aikit.Kit{Persona: aikit.PersonaHard, Rand: &r}
		var vr variety
		vr.v = DefaultVariety()
		vr.v.Style = "random"
		p := DefaultParams()
		vr.begin(k, &p)
		return vr.style, p
	}
	styles := map[int]bool{}
	for seed := uint32(51); seed < 71; seed++ {
		s1, p1 := draw(seed)
		s2, p2 := draw(seed)
		if s1 != s2 || p1 != p2 {
			t.Fatalf("seed %d did not replay", seed)
		}
		styles[s1] = true
	}
	if len(styles) < 3 {
		t.Errorf("20 seeds drew only %d styles", len(styles))
	}
}

// Ambition 100 is the full plan; lower ambition attempts less (later tech,
// shorter reach) and never more.
func TestAmbitionScales(t *testing.T) {
	full := DefaultParams()
	applyAmbition(&full, 100)
	if full != DefaultParams() {
		t.Error("ambition 100 changed Params")
	}
	low := DefaultParams()
	applyAmbition(&low, 20)
	d := DefaultParams()
	if low.WTech*10 > d.WTech || low.TechTime <= d.TechTime || low.TravelHalf >= d.TravelHalf {
		t.Errorf("ambition 20 did not shrink the plan: %+v", low)
	}
}

// The tier curves are the top human tier's fits in stock alive counts; the
// cap is the ambition percent of them. Checked at minute 10 against the
// fits: extractors 0.58 × (−1.7 + 17.4), constructors 0.63 × (−2.7 +
// 13.1), factories 0.88 × (−0.02 + 2.7), army 1.08 × (−753 + 2217).
func TestTierCurves(t *testing.T) {
	const tenMin = 18000
	cases := []struct {
		name      string
		got, want int64
	}{
		{"extractors", topExtractors.at(100, tenMin), 9110},
		{"constructors", topConstructors.at(100, tenMin), 6600},
		{"factories", topFactories.at(100, tenMin), 2340},
		{"army", topArmy.at(100, tenMin), 1576000},
		{"half", topExtractors.at(50, tenMin), 4555},
	}
	for _, c := range cases {
		if d := c.got - c.want; d < -150 && c.name != "army" || d > 150 && c.name != "army" || (c.name == "army" && (d < -20000 || d > 20000)) {
			t.Errorf("%s at 10 min = %d, want about %d", c.name, c.got, c.want)
		}
	}
}

func TestVarietyFrom(t *testing.T) {
	v, err := VarietyFrom(map[string]string{"style": "eco", "jitter": "0", "w_army": "120"})
	if err != nil || v.Style != "eco" || v.Jitter {
		t.Errorf("got %+v, %v", v, err)
	}
	if v, _ := VarietyFrom(nil); v != DefaultVariety() || !v.NoTowerTime || v.NoWideBase {
		t.Errorf("default %+v: want tower timing off and the wider base on", v)
	}
	if v, _ := VarietyFrom(map[string]string{"tower_time": "1"}); v.NoTowerTime {
		t.Error("tower_time=1 left tower timing off")
	}
	if _, err := VarietyFrom(map[string]string{"style": "nope"}); err == nil {
		t.Error("unknown style accepted")
	}
	if _, err := VarietyFrom(map[string]string{"jitter": "2"}); err == nil {
		t.Error("bad jitter accepted")
	}
}

// fleet: a light boat is a quarter of its shipyard's heaviest hull or less,
// and the fleet keeps two of them plus one per six ships.
func TestFleetLightBoats(t *testing.T) {
	hull := func(i int32, v int32) *aikit.UnitInfo {
		return &aikit.UnitInfo{Index: i, Value: v, Role: aikit.RoleMobile | aikit.RoleCombat | aikit.RoleNaval}
	}
	pt, roy, sub := hull(0, 116), hull(1, 973), hull(2, 1213)
	yard := &aikit.UnitInfo{Index: 3, Role: aikit.RoleFactory, Builds: []*aikit.UnitInfo{pt, roy, sub}}
	k := &aikit.Kit{Table: &aikit.Table{Units: []*aikit.UnitInfo{pt, roy, sub, yard}}}
	s := &shared{p: Params{Naval: 1, Fleet: 1}, k: k, info: make([]staticInfo, 4), count: make([]int32, 4)}
	s.setupFleet(k)
	if s.fleetWar() {
		t.Error("no enemy navy seen: the fleet switch does not act")
	}
	s.navalShare = fleetNaval
	if !s.fleetWar() {
		t.Error("an enemy navy: it acts")
	}
	if !s.info[0].light || s.info[1].light || s.info[2].light || s.info[3].hull {
		t.Fatalf("light %v %v %v, yard hull %v", s.info[0].light, s.info[1].light, s.info[2].light, s.info[3].hull)
	}
	s.count[0], s.count[1] = 2, 4 // six ships: three light boats kept
	if s.lightFull() {
		t.Error("two light boats in a fleet of six: room for a third")
	}
	s.count[0] = 3
	if !s.lightFull() {
		t.Error("three light boats in a fleet of seven: full")
	}
}

// growth: the constructor target ramps in from minute 8 to the human ratio
// (0.55 at minute 12, 0.75 by 20) per finished extractor, plus at most two
// for claimable spots; the factory cap is two from minute 8 or one per
// 14 metal/s, and only while investing is safe.
func TestGrowthTargets(t *testing.T) {
	k := &aikit.Kit{Persona: aikit.PersonaHard}
	s := &shared{p: Params{Growth: gConstructors | gFactories, SpotsPerCon: 3}, k: k}
	s.mexBuilt, s.freeSafe = 20, 30
	for _, c := range []struct {
		min  uint32
		want int64
	}{{7, 2000}, {10, 20*275 + 2000}, {12, 20*550 + 2000}, {20, 20*750 + 2000}} {
		s.tick = c.min * 1800
		if got := s.consTarget(); got != c.want {
			t.Errorf("minute %d: constructor target %d, want %d", c.min, got, c.want)
		}
	}
	s.facDefs = []int32{0}
	s.count = []int32{2}
	s.tick, s.mInc = 9*1800, 20000
	if !s.factoryFull() {
		t.Error("two factories at 20 metal/s, minute 9: full")
	}
	s.mInc = 42000
	if s.factoryFull() {
		t.Error("two factories at 42 metal/s: room for a third")
	}
	s.mInc, s.armyRatio = 20000, 1500
	if s.factoryFull() {
		t.Error("behind in army: the cap does not bind")
	}
	s.armyRatio = 0
	s.k = &aikit.Kit{Persona: aikit.PersonaMed}
	if s.factoryFull() || s.part(gConstructors) {
		t.Error("below full ambition growth does nothing")
	}
}

// fac_first: a product's family is air, ship, kbot (a KBOT movement class
// or editor class — stock kbots author tank movement classes) or vehicle
// (hovercraft included); until one of the chosen family is owned, its
// factories rate as the best factory and the others w_fac_first times less.
func TestFactoryFamily(t *testing.T) {
	unit := func(r aikit.Role, mc, ted string) *aikit.UnitInfo {
		return &aikit.UnitInfo{Role: r | aikit.RoleMobile | aikit.RoleCombat, Def: &content.UnitDef{MovementClass: mc, Unknown: map[string]string{"TEDClass": ted}}}
	}
	for _, c := range []struct {
		name string
		u    *aikit.UnitInfo
		want int32
	}{
		{"fighter", unit(aikit.RoleAir, "", "VTOL"), famAir},
		{"boat", unit(aikit.RoleNaval, "BOATS4", "SHIP"), famShip},
		{"kbot on a tank class", unit(0, "TANKSH2", "KBOT"), famKbot},
		{"kbot class", unit(0, "KBOTSS2", ""), famKbot},
		{"tank", unit(0, "TANKSH2", "TANK"), famVehicle},
		{"hovercraft", unit(aikit.RoleHover, "TANKHOVER3", "TANK"), famVehicle},
	} {
		if got := productFamily(c.u, false); got != c.want {
			t.Errorf("%s: family %s, want %s", c.name, famNames[got], famNames[c.want])
		}
	}
	lab, plant := &aikit.UnitInfo{Index: 0}, &aikit.UnitInfo{Index: 1}
	s := &shared{p: Params{FacFirst: famKbot, WFacFirst: 300}, firstFam: famKbot,
		facFam: []int8{int8(famKbot), int8(famVehicle)}, famFacs: []int32{0}, count: []int32{0, 0}}
	if got := s.familySuit(lab, 800); got != 1000 {
		t.Errorf("chosen family, none owned: suitability %d, want the best's 1000", got)
	}
	if got := s.familySuit(plant, 1000); got != 333 {
		t.Errorf("other family, none of the chosen owned: suitability %d, want 1000/3", got)
	}
	s.count[0] = 1
	if got, got2 := s.familySuit(lab, 800), s.familySuit(plant, 1000); got != 800 || got2 != 1000 {
		t.Errorf("chosen family owned: suitabilities %d %d, want 800 1000", got, got2)
	}
}

// fac_first=5 draws the family once per game from the stock human shares,
// never the shipyard where ships do not reach and never the air plant
// where the land army does (here: no terrain, so vehicle 63 : kbot 10).
func TestFactoryFamilyDraw(t *testing.T) {
	mk := func(i int32, r aikit.Role, ted string) *aikit.UnitInfo {
		return &aikit.UnitInfo{Index: i, Role: r | aikit.RoleMobile | aikit.RoleCombat, Def: &content.UnitDef{Unknown: map[string]string{"TEDClass": ted}}}
	}
	kb, tk, ac, sh := mk(0, 0, "KBOT"), mk(1, 0, "TANK"), mk(2, aikit.RoleAir, "VTOL"), mk(3, aikit.RoleNaval, "SHIP")
	units := []*aikit.UnitInfo{kb, tk, ac, sh}
	for i, p := range []*aikit.UnitInfo{kb, tk, ac, sh} {
		units = append(units, &aikit.UnitInfo{Index: int32(4 + i), Role: aikit.RoleFactory, Builds: []*aikit.UnitInfo{p}})
	}
	var n [famShip + 1]int
	for seed := uint32(0); seed < 400; seed++ {
		r := aikit.PlayerRand(seed, 0)
		k := &aikit.Kit{Table: &aikit.Table{Units: units}, Rand: &r}
		s := &shared{p: Params{FacFirst: famDraw}, k: k, info: make([]staticInfo, len(units))}
		s.setupFamily(k)
		n[s.firstFam]++
		if len(s.famFacs) != 1 || s.facFam[s.famFacs[0]] != int8(s.firstFam) {
			t.Fatalf("seed %d: family %d, factories %v", seed, s.firstFam, s.famFacs)
		}
	}
	if n[famShip] != 0 || n[famAir] != 0 || n[famNone] != 0 || !(n[famVehicle] > 4*n[famKbot] && n[famKbot] > 0) {
		t.Errorf("draws kbot %d vehicle %d air %d ship %d none %d", n[famKbot], n[famVehicle], n[famAir], n[famShip], n[famNone])
	}
}

// growth 16: the constructor floor is one from minute 5 and two from 8.
func TestConstructorFloor(t *testing.T) {
	s := &shared{}
	for _, c := range []struct {
		min  uint32
		want int64
	}{{4, 0}, {5, 1000}, {7, 1000}, {8, 2000}, {30, 2000}} {
		s.tick = c.min * 1800
		if got := s.consFloor(); got != c.want {
			t.Errorf("minute %d: floor %d, want %d", c.min, got, c.want)
		}
	}
}
