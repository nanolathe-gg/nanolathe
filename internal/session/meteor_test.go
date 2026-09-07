package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestMeteorDeterminism_TwoRunsIdentical verifies meteor scheduler determinism per [08 "Meteor showers"] [06 §6.5] I4.
// Two sessions with identical seeds and meteor config must produce identical projectile states and RNG draw counts after N ticks.
func TestMeteorDeterminism_TwoRunsIdentical(t *testing.T) {
	cat := &content.Catalog{
		Weapons: map[string]*content.WeaponDef{},
		Meteor: &content.MeteorDefaults{
			MeteorWeapon:   "meteor",
			MeteorRadius:   300,
			MeteorDensity:  2,
			MeteorDuration: 5,
			MeteorInterval: 60,
		},
		Units:  map[string]*content.UnitDef{},
		Maps:   map[string]*content.MapHeader{},
		Sides:  []*content.SideDef{{Commander: "armcom"}},
		Sounds: map[string]*content.SoundCategory{},
	}
	// Minimal commander def for sliced pool
	cat.Units[content.CanonicalKey("armcom")] = &content.UnitDef{UnitName: "armcom", MaxDamage: 100, SightDistance: 128}
	cat.Units[content.CanonicalKey("armcom")].ObjectName = "armcom"
	// Meteor weapon flagged
	w := &content.WeaponDef{ID: 0, Meteor: true, Name: "meteor"}
	w.CanonicalKey = content.CanonicalKey("meteor")
	cat.Weapons[w.CanonicalKey] = w
	// Another weapon id 1 for fallback
	w1 := &content.WeaponDef{ID: 1, Meteor: false, Name: "other"}
	w1.CanonicalKey = content.CanonicalKey("other")
	cat.Weapons[w1.CanonicalKey] = w1

	run := func(seedSim, seedCrt uint32) (projCount int, crtDraws uint64, simDraws uint64, met MeteorState) {
		terrain := &world.Terrain{CellW: 64, CellH: 64, Plot: make([]world.PlotCell, 64*64)}
		// Production terrains always carry a full plot ([GAP T14]: ExpandPlot
		// sizes Plot to CellW*CellH); collision indexing trusts that invariant.
		for i := range terrain.Plot {
			terrain.Plot[i].SetFeature(world.PlotFeatureNone)
		}
		s := &Session{
			Catalog: cat,
			Combat:  &combat.Service{},
			World:   terrain,
		}
		s.Combat = &combat.Service{}
		s.Wind = world.NewWind(100, 2000)
		s.Catalog = cat
		s.Meteor = MeteorState{
			Enabled:       true,
			WeaponName:    "meteor",
			Weapon:        w,
			Radius:        300,
			DurationTicks: 150,  // 5*30
			IntervalTicks: 1800, // 60*30
			PerHitDelay:   15,   // 30/2
			NextStrike:    15,
			Initialized:   true,
		}
		s.SeedSessionRNG(seedSim, seedCrt)
		for tick := uint32(1); tick <= 200; tick++ {
			s.stepAuthoritativePhases(tick)
		}
		projCount = s.Combat.Count()
		crtDraws = s.CrtRNG().Draws()
		simDraws = s.SimRNG().Draws()
		met = s.Meteor
		return
	}

	c1, dCrt1, dSim1, m1 := run(12345, 67890)
	c2, dCrt2, dSim2, m2 := run(12345, 67890)
	if c1 != c2 {
		t.Fatalf("meteor determinism: projectile counts differ %d vs %d", c1, c2)
	}
	if dCrt1 != dCrt2 {
		t.Fatalf("meteor determinism: CRT draws differ %d vs %d", dCrt1, dCrt2)
	}
	if dSim1 != dSim2 {
		t.Fatalf("meteor determinism: Sim draws differ %d vs %d (meteors must consume zero sim draws) [06 §6.5]", dSim1, dSim2)
	}
	if m1.NextStrike != m2.NextStrike || m1.StrikeEnds != m2.StrikeEnds || m1.NextHit != m2.NextHit || m1.Active != m2.Active {
		t.Fatalf("meteor determinism: scheduler state diverged %+v vs %+v", m1, m2)
	}
	runNoMeteor := func(seedSim, seedCrt uint32) (uint64, uint64) {
		terrain := &world.Terrain{CellW: 64, CellH: 64, Plot: make([]world.PlotCell, 64*64)}
		// Production terrains always carry a full plot ([GAP T14]: ExpandPlot
		// sizes Plot to CellW*CellH); collision indexing trusts that invariant.
		for i := range terrain.Plot {
			terrain.Plot[i].SetFeature(world.PlotFeatureNone)
		}
		s := &Session{
			Catalog: cat,
			Combat:  &combat.Service{},
			World:   terrain,
		}
		s.Combat = &combat.Service{}
		s.Wind = world.NewWind(100, 2000)
		s.Catalog = cat
		s.Meteor = MeteorState{
			Enabled:     false,
			Initialized: true,
		}
		s.SeedSessionRNG(seedSim, seedCrt)
		for tick := uint32(1); tick <= 200; tick++ {
			s.stepAuthoritativePhases(tick)
		}
		return s.CrtRNG().Draws(), s.SimRNG().Draws()
	}
	crtNo, simNo := runNoMeteor(12345, 67890)
	// [R-CORE-01 §4.4.1] corrected draw gating, exact deterministic census for
	// this fixture. Both runs include exactly ONE wind interval draw (phase 8
	// fires once in 200 ticks for bounds 100..2000).
	// Enabled: 4 scheduling draws at the single due evaluation (tick 15,
	// interval 1800) + 2 draws per hit (11 hits across the 150-tick window at
	// spacing 15) + 1 wind = 27.
	// Disabled: NextStrike = trunc(30/density) = 0 at entry and this synthetic
	// fixture carries a zero interval, so the scheduler is due every other
	// tick: 100 due evaluations × 4 draws + 1 wind = 401. The earlier
	// "four per tick" reading is superseded — draws happen only on due
	// evaluations [R-CORE-01 §4.4.1].
	if dCrt1 != 27 {
		t.Fatalf("enabled storm CRT draws = %d, want 27 (1 wind + 4 scheduling + 22 per-hit) [06 §6.5][R-CORE-01 §4.4.1]", dCrt1)
	}
	if crtNo != 401 {
		t.Fatalf("disabled storm CRT draws = %d, want 401 (1 wind + 100 due evaluations x 4) [06 §6.5][R-CORE-01 §4.4.1]", crtNo)
	}
	if dSim1 != simNo {
		t.Fatalf("meteor should consume zero sim draws [06 §6.5]: enabled sim %d vs disabled %d", dSim1, simNo)
	}
	rSim := rng.NewSimulation(999)
	rCrt := rng.NewCRT(999)
	simBefore := rSim.Draws()
	crtBefore := rCrt.Draws()
	_, _, _, _ = combat.MeteorSchedule(&rCrt, 64, 64)
	if rSim.Draws() != simBefore {
		t.Fatalf("MeteorSchedule must not consume sim draws")
	}
	if rCrt.Draws()-crtBefore != 4 {
		t.Fatalf("MeteorSchedule must consume 4 CRT draws")
	}
}

// TestMeteorWithoutTerrainDoesNotSchedule verifies that the scheduler has no
// map-independent geometry source. An invalid session therefore leaves the
// meteor state and CRT stream untouched rather than substituting dimensions.
func TestMeteorWithoutTerrainDoesNotSchedule(t *testing.T) {
	weapon := &content.WeaponDef{ID: 0, Meteor: true, Name: "meteor"}
	s := &Session{
		Meteor: MeteorState{
			Enabled:       true,
			Initialized:   true,
			Weapon:        weapon,
			WeaponName:    "meteor",
			NextStrike:    0,
			DurationTicks: 30,
			PerHitDelay:   1,
		},
		Combat: &combat.Service{},
	}
	s.SeedSessionRNG(12345, 67890)
	s.stepAuthoritativePhases(1)
	if got := s.Combat.Count(); got != 0 {
		t.Fatalf("nil-world meteor scheduled %d projectiles", got)
	}
	if got := s.CrtRNG().Draws(); got != 0 {
		t.Fatalf("nil-world meteor consumed %d CRT draws", got)
	}
}

var _ = combat.MeteorSchedule
