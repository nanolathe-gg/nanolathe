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
		s := &Session{
			Catalog: cat,
			Combat:  &combat.Service{},
		}
		s.World = nil // fallback 64x64 dimensions used in tickMeteor when world nil
		s.Combat = &combat.Service{}
		s.Wind = world.NewWind(100, 2000)
		s.Catalog = cat
		s.Meteor = MeteorState{
			Enabled:       true,
			WeaponName:    "meteor",
			Weapon:        w,
			Radius:        300,
			Density:       2,
			DurationTicks: 150,  // 5*30
			IntervalTicks: 1800, // 60*30
			PerHitDelay:   15,   // 30/2
			NextStrike:    15,
			Initialized:   true,
		}
		s.SeedSessionRNG(seedSim, seedCrt)
		for tick := uint32(1); tick <= 200; tick++ {
			s.authoritativeTick(tick)
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
		s := &Session{
			Catalog: cat,
			Combat:  &combat.Service{},
		}
		s.World = nil
		s.Combat = &combat.Service{}
		s.Wind = world.NewWind(100, 2000)
		s.Catalog = cat
		s.Meteor = MeteorState{
			Enabled:     false,
			Initialized: true,
		}
		s.SeedSessionRNG(seedSim, seedCrt)
		for tick := uint32(1); tick <= 200; tick++ {
			s.authoritativeTick(tick)
		}
		return s.CrtRNG().Draws(), s.SimRNG().Draws()
	}
	crtNo, simNo := runNoMeteor(12345, 67890)
	if dCrt1 <= crtNo {
		t.Fatalf("meteor CRT consumption: enabled draws %d should exceed disabled %d (four per tick scheduling) [06 §6.5]", dCrt1, crtNo)
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

var _ = combat.MeteorSchedule
