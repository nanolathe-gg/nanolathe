package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/world"
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
	// Disabled: NextStrike and interval are zero in this fixture, so it is due
	// on every tick: 200 due evaluations × 4 draws + 1 wind = 801. The due
	// block clears Active after its four draws, and does not defer its next
	// check because of that clear [06 §6.5].
	if dCrt1 != 27 {
		t.Fatalf("enabled storm CRT draws = %d, want 27 (1 wind + 4 scheduling + 22 per-hit) [06 §6.5][R-CORE-01 §4.4.1]", dCrt1)
	}
	if crtNo != 801 {
		t.Fatalf("disabled storm CRT draws = %d, want 801 (1 wind + 200 due evaluations x 4) [06 §6.5]", crtNo)
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

func TestMeteorSchedulerDueAndHitBoundaries(t *testing.T) {
	terrain := &world.Terrain{CellW: 64, CellH: 64, Plot: make([]world.PlotCell, 64*64)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	weapon := &content.WeaponDef{ID: 0, Name: "noweapon"}

	t.Run("disabled due spends scheduling draws only", func(t *testing.T) {
		s := &Session{World: terrain, Combat: &combat.Service{}, Meteor: MeteorState{
			Initialized: true, NextStrike: 10, DurationTicks: 5, IntervalTicks: 10,
		}}
		s.SeedSessionRNG(1, 2)
		s.tickMeteor(10)
		if got := s.CrtRNG().Draws(); got != 4 {
			t.Fatalf("disabled due draws = %d, want 4", got)
		}
		if s.Meteor.Active || s.Combat.Count() != 0 {
			t.Fatalf("disabled due state = %+v, projectiles=%d", s.Meteor, s.Combat.Count())
		}
	})

	t.Run("inclusive final hit", func(t *testing.T) {
		s := &Session{World: terrain, Combat: &combat.Service{}, Meteor: MeteorState{
			Enabled: true, Initialized: true, Weapon: weapon, NextStrike: 10,
			DurationTicks: 0, IntervalTicks: 10, PerHitDelay: 1,
		}}
		s.SeedSessionRNG(1, 2)
		s.tickMeteor(10)
		if got := s.Combat.Count(); got != 1 {
			t.Fatalf("opening/final tick projectiles = %d, want 1", got)
		}
		if s.Meteor.Active || s.Meteor.NextHit != 11 {
			t.Fatalf("inclusive final state = %+v", s.Meteor)
		}
		if got := s.CrtRNG().Draws(); got != 6 {
			t.Fatalf("opening/final tick draws = %d, want 6", got)
		}
	})

	t.Run("zero spacing while active attempts once per tick", func(t *testing.T) {
		s := &Session{World: terrain, Combat: &combat.Service{}, Meteor: MeteorState{
			Enabled: true, Active: true, Initialized: true, Weapon: weapon,
			NextStrike: 100, StrikeEnds: 100, NextHit: 10, PerHitDelay: 0,
		}}
		s.SeedSessionRNG(1, 2)
		s.tickMeteor(10)
		s.tickMeteor(11)
		if got := s.Combat.Count(); got != 2 {
			t.Fatalf("zero-spacing projectiles = %d, want 2", got)
		}
		if s.Meteor.NextHit != 11 || !s.Meteor.Active {
			t.Fatalf("zero-spacing state = %+v", s.Meteor)
		}
		if got := s.CrtRNG().Draws(); got != 4 {
			t.Fatalf("zero-spacing draws = %d, want 4", got)
		}
	})

	t.Run("negative spacing retains modular next-hit bits", func(t *testing.T) {
		s := &Session{World: terrain, Combat: &combat.Service{}, Meteor: MeteorState{
			Enabled: true, Active: true, Initialized: true, Weapon: weapon,
			NextStrike: 100, StrikeEnds: 100, NextHit: 10, PerHitDelay: -1,
		}}
		s.SeedSessionRNG(1, 2)
		s.tickMeteor(10)
		if got := s.Meteor.NextHit; got != 9 {
			t.Fatalf("negative spacing next hit = %#x, want %#x", got, uint32(9))
		}
		if got := s.CrtRNG().Draws(); got != 2 {
			t.Fatalf("negative-spacing draws = %d, want 2", got)
		}
	})

	t.Run("due block re-arms an already active storm", func(t *testing.T) {
		s := &Session{World: terrain, Combat: &combat.Service{}, Meteor: MeteorState{
			Enabled: true, Active: true, Initialized: true, Weapon: weapon,
			NextStrike: 10, StrikeEnds: 99, NextHit: 99, DurationTicks: 5, IntervalTicks: 20, PerHitDelay: 3,
		}}
		s.SeedSessionRNG(1, 2)
		s.tickMeteor(10)
		if !s.Meteor.Active || s.Meteor.StrikeEnds != 15 || s.Meteor.NextStrike != 35 || s.Meteor.NextHit != 13 {
			t.Fatalf("already-active due state = %+v", s.Meteor)
		}
		if got := s.CrtRNG().Draws(); got != 6 {
			t.Fatalf("already-active due draws = %d, want 6", got)
		}
	})
}

func TestHumanMeteorCommandForceArmsAndArgumentOnlySetsEnabled(t *testing.T) {
	terrain := &world.Terrain{CellW: 64, CellH: 48}
	s := &Session{World: terrain, Meteor: MeteorState{
		Initialized: true, DurationTicks: 5, IntervalTicks: 20,
	}}
	s.SeedSessionRNG(1, 2)

	wantCRT := rng.NewCRT(2)
	wantX, wantZ, wantOriginX, wantOriginZ := combat.MeteorSchedule(&wantCRT, terrain.CellW, terrain.CellH)
	s.applyHumanCommand(HumanCommand{Kind: HumanMeteor}, 10)
	if s.Meteor.Enabled || !s.Meteor.Active {
		t.Fatalf("forced disabled storm enabled/active = %t/%t, want false/true", s.Meteor.Enabled, s.Meteor.Active)
	}
	if s.Meteor.StrikeEnds != 15 || s.Meteor.NextStrike != 35 || s.Meteor.NextHit != 10 {
		t.Fatalf("forced storm deadlines = %d/%d/%d, want 15/35/10", s.Meteor.StrikeEnds, s.Meteor.NextStrike, s.Meteor.NextHit)
	}
	if s.Meteor.TargetX != wantX || s.Meteor.TargetZ != wantZ || s.Meteor.OriginX != wantOriginX || s.Meteor.OriginZ != wantOriginZ {
		t.Fatalf("forced storm geometry = target(%d,%d) origin(%d,%d), want target(%d,%d) origin(%d,%d)", s.Meteor.TargetX, s.Meteor.TargetZ, s.Meteor.OriginX, s.Meteor.OriginZ, wantX, wantZ, wantOriginX, wantOriginZ)
	}
	if got := s.CrtRNG().Draws(); got != 4 {
		t.Fatalf("forced storm draws = %d, want 4", got)
	}

	armed := s.Meteor
	s.applyHumanCommand(HumanCommand{Kind: HumanMeteor, Meteor: HumanMeteorCommand{ArgumentPresent: true, Enabled: true}}, 11)
	wantEnabled := armed
	wantEnabled.Enabled = true
	if s.Meteor != wantEnabled || s.CrtRNG().Draws() != 4 {
		t.Fatalf("enable form changed storm state or RNG: got %+v draws %d, want %+v draws 4", s.Meteor, s.CrtRNG().Draws(), wantEnabled)
	}
	s.applyHumanCommand(HumanCommand{Kind: HumanMeteor, Meteor: HumanMeteorCommand{ArgumentPresent: true}}, 12)
	if s.Meteor != armed || s.CrtRNG().Draws() != 4 {
		t.Fatalf("disable form changed storm state or RNG: got %+v draws %d, want %+v draws 4", s.Meteor, s.CrtRNG().Draws(), armed)
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
