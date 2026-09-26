package utility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// The opening switches parse from a player spec; out-of-range values and
// non-integers are errors, and none of them is set by default.
func TestOpeningSwitches(t *testing.T) {
	v, err := VarietyFrom(map[string]string{"open_reclaim": "150", "open_reclaim_hi": "700", "open_fam": "1", "open_army": "1", "open_follow": "2"})
	if err != nil || v.OpenReclaim != 150 || v.OpenReclaimHi != 700 || !v.OpenFam || v.OpenArmy != 1 || v.OpenFollow != 2 {
		t.Fatalf("switches: %+v, %v", v, err)
	}
	for _, kv := range []map[string]string{{"open_reclaim": "401"}, {"open_fam": "2"}, {"open_army": "4"}, {"open_follow": "3"}} {
		if _, err := VarietyFrom(kv); err == nil {
			t.Errorf("%v accepted", kv)
		}
	}
}

// A job is the four richest metal features within the Clear's radius of
// its feature: fifteen ticks plus half the energy and metal pools of work
// each [05 R-WORK-01 §5], and the walk from the job's feature to each.
func TestOpenReclaimJobs(t *testing.T) {
	f := func(x, z, m, e int32) aikit.Feature {
		return aikit.Feature{X: x, Z: z, Metal: m, Energy: e, Reclaimable: true}
	}
	o := &aikit.Obs{Features: []aikit.Feature{
		f(1000, 1000, 50, 10), f(1100, 1000, 40, 0), f(1000, 1100, 30, 0), f(1050, 1050, 20, 0), f(1100, 1100, 10, 0),
		f(2000, 2000, 60, 0),
		{X: 1010, Z: 1010, Energy: 200, Reclaimable: true}, // a tree: no metal
		{X: 1020, Z: 1020, Metal: 500},                     // not reclaimable
	}}
	s := &shared{}
	s.open.rec.w = 100
	jobs := s.openJobs(o)
	if len(jobs) != 6 {
		t.Fatalf("%d jobs, want one per reclaimable metal feature (6)", len(jobs))
	}
	j := jobs[0]
	// Picks 50, 40, 30, 20 (the 10 is fifth): work 15+30 + 15+20 + 15+15 + 15+10.
	if j.picks != 4 || j.metal != 140 || j.work != 135 || j.walk != 0+100+100+70 {
		t.Errorf("job at the first feature: %+v", j)
	}
	if lone := jobs[5]; lone.picks != 1 || lone.metal != 60 || lone.work != 45 || lone.walk != 0 {
		t.Errorf("lone feature: %+v", lone)
	}
	// The bucketed estimate is the plain one over every feature.
	o.Features[0].X = 1159 // still within 160 of the others' cells
	s.open.rec.ok = false
	s.tick++
	if got := s.openJobs(o); got[0].picks != 4 {
		t.Errorf("after a move: %+v", got[0])
	}
}

func openBoard(stock, capM int32) (*shared, *core.Board, *Economy) {
	s := &shared{p: DefaultParams(), needM: 2000}
	s.p.ComRadius = 900
	s.mStock, s.mCap, s.mInc, s.mExp = int64(stock), int64(capM), 4000, 10000
	s.open.rec = openReclaim{w: 200}
	o := &aikit.Obs{Features: []aikit.Feature{{X: 1300, Z: 1000, Metal: 200, Reclaimable: true}}}
	b := &core.Board{O: o, HomeX: 1000, HomeZ: 1000, Threat: aikit.NewGrid(&aikit.MapInfo{SectorW: 64, SectorH: 64})}
	return s, b, &Economy{s: s}
}

// The early reclaim offers a Clear only while metal stalls (with the stall
// gate), fades out by openReclaimEnd, keeps the commander within its leash
// and skips a pile just sent to.
func TestOpenReclaimGates(t *testing.T) {
	builder := &aikit.OwnUnit{X: 1000, Z: 1000, Info: &aikit.UnitInfo{Speed: 40, Def: &content.UnitDef{CanReclamate: true}}}
	offer := func(s *shared, b *core.Board, e *Economy, u *aikit.OwnUnit) int64 {
		var d decision
		d.reset(s.tick, u.Info, u.X, u.Z)
		e.evalOpenReclaim(b, u, &d)
		if d.n == 0 {
			return 0
		}
		return d.top[0].score
	}
	s, b, e := openBoard(20, 1000)
	s.tick = 3600
	if offer(s, b, e, builder) == 0 {
		t.Fatal("stalled: no Clear offered")
	}
	s.mStock = 500
	if offer(s, b, e, builder) != 0 {
		t.Error("half-full store: a Clear offered")
	}
	s.mStock = 300
	if offer(s, b, e, builder) == 0 {
		t.Error("store below half: no Clear offered")
	}
	s.mStock, s.mInc = 20, 20000
	if offer(s, b, e, builder) != 0 {
		t.Error("income above expense: a Clear offered")
	}
	s.mInc = 4000
	s.tick = openReclaimEnd
	if offer(s, b, e, builder) != 0 {
		t.Error("a Clear offered at openReclaimEnd")
	}
	s.tick = 3600
	com := *builder
	com.Info = &aikit.UnitInfo{Speed: 40, Role: aikit.RoleCommander, Def: &content.UnitDef{CanReclamate: true}}
	b.O.Features[0].X = 2200
	s.open.rec.ok = false // a new listing
	if offer(s, b, e, &com) != 0 {
		t.Error("the commander offered a Clear beyond its leash")
	}
	if offer(s, b, e, builder) == 0 {
		t.Error("a constructor was held to the commander's leash")
	}
	s.open.rec.sent(2200, 1000, s.tick, 200)
	if offer(s, b, e, builder) != 0 {
		t.Error("a pile just sent to was offered again")
	}
	s.tick += openRecentTicks
	s.open.rec.ok = false
	if offer(s, b, e, builder) == 0 {
		t.Error("the pile stayed skipped after the listing refreshed")
	}
	s.open.rec.w = 0
	if offer(s, b, e, builder) != 0 {
		t.Error("open_reclaim=0 offered a Clear")
	}
}

// Scouts wait for the first combat unit that is not a scout (open_army 1),
// until minute four; without the part they never wait.
func TestScoutsWait(t *testing.T) {
	flash := &aikit.UnitInfo{Index: 1, Role: aikit.RoleCombat | aikit.RoleMobile}
	fav := &aikit.UnitInfo{Index: 2, Role: aikit.RoleCombat | aikit.RoleMobile | aikit.RoleScout}
	plant := &aikit.UnitInfo{Index: 3, Role: aikit.RoleFactory}
	o := &aikit.Obs{Own: []aikit.OwnUnit{{H: 5, Info: plant, Built: true, QueueLen: 1}}}
	b := &core.Board{O: o, Factories: []int32{0}}
	s := &shared{tick: 3000}
	pr := &Production{s: s}
	if s.scoutsWait(b, pr) {
		t.Error("waits without open_army")
	}
	s.open.army = armyScouts
	if !s.scoutsWait(b, pr) {
		t.Error("no combat unit: scouts do not wait")
	}
	r := pr.regOf(&o.Own[0])
	r.prod, r.tick = fav, s.tick
	if !s.scoutsWait(b, pr) {
		t.Error("a scout queued counts as the first combat unit")
	}
	r.prod = flash
	if s.scoutsWait(b, pr) {
		t.Error("a combat unit queued: scouts still wait")
	}
	r.prod = nil
	s.armyCount = 1
	if s.scoutsWait(b, pr) {
		t.Error("a combat unit built: scouts still wait")
	}
	s.armyCount, s.tick = 0, openScoutWait
	if s.scoutsWait(b, pr) {
		t.Error("scouts wait after minute four")
	}
}

// open_fam draws the family after the style, lead and jitter draws (so the
// switch leaves them as they were) from the stock shares tilted by the
// style; the tilts only scale the families an archetype's row lists.
func TestOpenFamilyDraw(t *testing.T) {
	units := famTable()
	var n [famShip + 1]int
	for seed := uint32(0); seed < 600; seed++ {
		r := aikit.PlayerRand(seed, 0)
		k := &aikit.Kit{Persona: aikit.PersonaHard, Rand: &r, Table: &aikit.Table{Units: units}}
		var off, on variety
		off.v, on.v = DefaultVariety(), DefaultVariety()
		on.v.OpenFam = true
		p1, p2 := DefaultParams(), DefaultParams()
		off.begin(k, &p1)
		r2 := aikit.PlayerRand(seed, 0)
		k2 := &aikit.Kit{Persona: aikit.PersonaHard, Rand: &r2, Table: k.Table}
		on.begin(k2, &p2)
		if off.style != on.style || p1 != p2 || off.lead != on.lead || off.waveJit != on.waveJit {
			t.Fatalf("seed %d: open_fam changed the style draws", seed)
		}
		s := &shared{p: p2, k: k2, info: make([]staticInfo, len(units)), count: make([]int32, len(units))}
		on.index(s)
		n[s.firstFam]++
	}
	// No terrain: ships reach nothing and the land army everything, so
	// only kbot and vehicle are drawn, vehicle far more often.
	if n[famShip] != 0 || n[famAir] != 0 || n[famNone] != 0 || !(n[famVehicle] > 3*n[famKbot] && n[famKbot] > 20) {
		t.Errorf("draws kbot %d vehicle %d air %d ship %d none %d", n[famKbot], n[famVehicle], n[famAir], n[famShip], n[famNone])
	}
	// Without jitter the most likely family is played and nothing drawn.
	r := aikit.PlayerRand(9, 0)
	k := &aikit.Kit{Persona: aikit.PersonaHard, Rand: &r, Table: &aikit.Table{Units: units}}
	var vr variety
	vr.v = Variety{Style: "balanced", OpenFam: true}
	p := DefaultParams()
	vr.begin(k, &p)
	s := &shared{p: p, k: k, info: make([]staticInfo, len(units)), count: make([]int32, len(units))}
	vr.index(s)
	if fresh := aikit.PlayerRand(9, 0); r != fresh || s.firstFam != famVehicle {
		t.Errorf("balanced without jitter: family %d, drew %v", s.firstFam, r != fresh)
	}
	units2 := styleShares(&Styles[styleIndex("tower")])
	if units2[famKbot] != famShares[famKbot]*137 || units2[famShip] != famShares[famShip]*100 {
		t.Errorf("tower tilt: %v", units2)
	}
	if b := styleShares(&Styles[0]); b[famVehicle] != famShares[famVehicle]*100 {
		t.Errorf("balanced is tilted: %v", b)
	}
}

// famTable is a kbot, a vehicle, an aircraft and a boat, each with its own
// factory (definitions 4..7).
func famTable() []*aikit.UnitInfo {
	mk := func(i int32, r aikit.Role, ted string) *aikit.UnitInfo {
		return &aikit.UnitInfo{Index: i, Role: r | aikit.RoleMobile | aikit.RoleCombat, Def: &content.UnitDef{Unknown: map[string]string{"TEDClass": ted}}}
	}
	kb, tk, ac, sh := mk(0, 0, "KBOT"), mk(1, 0, "TANK"), mk(2, aikit.RoleAir, "VTOL"), mk(3, aikit.RoleNaval, "SHIP")
	units := []*aikit.UnitInfo{kb, tk, ac, sh}
	for i, p := range []*aikit.UnitInfo{kb, tk, ac, sh} {
		units = append(units, &aikit.UnitInfo{Index: int32(4 + i), Role: aikit.RoleFactory, Builds: []*aikit.UnitInfo{p}})
	}
	return units
}

// open_follow: 1 keeps the first family's preference for a second
// factory of it; 2 prefers a vehicle plant next.
func TestOpenFollow(t *testing.T) {
	lab, plant := &aikit.UnitInfo{Index: 4}, &aikit.UnitInfo{Index: 5}
	s := &shared{p: Params{WFacFirst: 300}, firstFam: famKbot, count: make([]int32, 8)}
	k := &aikit.Kit{Table: &aikit.Table{Units: famTable()}}
	s.info = make([]staticInfo, 8)
	s.labelFamilies(k)
	s.keepFamily(k)
	s.count[4] = 1 // a lab owned
	if got := s.familySuit(plant, 1000); got != 1000 {
		t.Errorf("no follow-up: the preference outlived the first lab (%d)", got)
	}
	s.setFollow(1)
	if got, got2 := s.familySuit(plant, 1000), s.familySuit(lab, 800); got != 333 || got2 != 1000 {
		t.Errorf("follow 1 with one lab: plant %d lab %d, want 333 1000", got, got2)
	}
	s.count[4] = 2
	if got := s.familySuit(plant, 1000); got != 1000 {
		t.Errorf("follow 1 with two labs: plant %d, want 1000", got)
	}
	s.count[4] = 1
	s.setFollow(2)
	if got, got2 := s.familySuit(plant, 800), s.familySuit(lab, 1000); got != 1000 || got2 != 333 {
		t.Errorf("follow 2 with one lab: plant %d lab %d, want 1000 333", got, got2)
	}
	s.count[5] = 1
	if got := s.familySuit(lab, 1000); got != 1000 {
		t.Errorf("follow 2 with a plant: lab %d, want 1000", got)
	}
}

// Pricing the early reclaim allocates nothing once its scratch has grown:
// every builder deciding in a think reads the same jobs.
func TestOpenReclaimAllocs(t *testing.T) {
	s, b, e := openBoard(20, 1000)
	s.tick = 3600
	for i := 0; i < 60; i++ {
		b.O.Features = append(b.O.Features, aikit.Feature{X: 900 + int32(i*37%400), Z: 800 + int32(i*53%500), Metal: 40, Reclaimable: true})
	}
	u := &aikit.OwnUnit{X: 1000, Z: 1000, Info: &aikit.UnitInfo{Speed: 40, Def: &content.UnitDef{CanReclamate: true}}}
	var d decision
	e.evalOpenReclaim(b, u, &d)
	if n := testing.AllocsPerRun(50, func() {
		s.tick++
		d.reset(s.tick, u.Info, u.X, u.Z)
		e.evalOpenReclaim(b, u, &d)
	}); n != 0 {
		t.Errorf("%.1f allocations per think", n)
	}
}
