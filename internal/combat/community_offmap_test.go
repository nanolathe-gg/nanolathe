package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func offMapFixture(t *testing.T, rules Rules, flying bool) (*Service, *units.World, *world.Terrain, *units.Unit) {
	t.Helper()
	w := newCombatFixtureWorld(32, nil)
	terrain := &world.Terrain{CellW: 4, CellH: 4, Plot: make([]world.PlotCell, 16)}
	def := &content.UnitDef{UnitName: "off-map", MaxDamage: 100, Limit: -1, FootprintX: 1, FootprintZ: 1, ModelTopFixed: int32(numeric.FixedFromInt(16))}
	u := splashUnit(t, w, def, 1, -8, 0, 8)
	if flying {
		u.Move.ModeMirror = 2
	}
	s := NewServiceWithProjectileCapacity(300)
	s.Rules = rules
	s.Community = community.Features{OffMapAircraftMarginTiles: 1}
	s.ControlByte = func(uint8) uint8 { return ControlByteHuman }
	s.VisitOffMapFiled = func(yield func(pool.Handle, uint64) bool) { yield(u.Handle, 1) }
	s.IsOffMapFiled = func(h pool.Handle) bool { return h == u.Handle }
	return s, w, terrain, u
}

func TestCommunityOffMapProjectileAgeBoundaryAndStrictBypass(t *testing.T) {
	s, w, terrain, target := offMapFixture(t, CommunityRules{}, true)
	p := &Projectile{Pos: Vec3{X: numeric.FixedFromInt(-8), Y: numeric.FixedFromInt(8), Z: numeric.FixedFromInt(8)}, ShooterSide: 0, CreationTick: 10}
	if victim, keep := s.communityOffMapProjectile(p, w, terrain, 460); !keep || victim != target.Handle {
		t.Fatalf("age 450 victim/keep=%d/%v, want %d/true", victim, keep, target.Handle)
	}
	if victim, keep := s.communityOffMapProjectile(p, w, terrain, 461); keep || victim != 0 {
		t.Fatalf("age 451 victim/keep=%d/%v, want 0/false", victim, keep)
	}
	for i := 0; i < communityProjectileHighWater-1; i++ {
		if _, ok := s.Reserve(); !ok {
			t.Fatalf("reserve %d below high-water failed", i)
		}
	}
	if victim, keep := s.communityOffMapProjectile(p, w, terrain, 460); !keep || victim != target.Handle {
		t.Fatalf("count 269 victim/keep=%d/%v, want %d/true", victim, keep, target.Handle)
	}
	if _, ok := s.Reserve(); !ok {
		t.Fatal("reserve at high-water failed")
	}
	if victim, keep := s.communityOffMapProjectile(p, w, terrain, 460); keep || victim != 0 {
		t.Fatalf("count 270 victim/keep=%d/%v, want 0/false", victim, keep)
	}

	strict, strictWorld, strictTerrain, _ := offMapFixture(t, StrictRules{}, true)
	if victim, keep := strict.communityOffMapProjectile(p, strictWorld, strictTerrain, 460); keep || victim != 0 {
		t.Fatalf("Strict with enabled table victim/keep=%d/%v, want bypass", victim, keep)
	}
}

func TestCommunityOnMapSecondChanceExcludesNoExplode(t *testing.T) {
	s, w, terrain, target := offMapFixture(t, CommunityRules{}, true)
	p := &Projectile{Pos: Vec3{X: numeric.FixedFromInt(-8), Y: numeric.FixedFromInt(8), Z: numeric.FixedFromInt(8)}, ShooterSide: 0}
	if got := s.communityOnMapOffMapVictim(p, &content.WeaponDef{}, w, terrain); got != target.Handle {
		t.Fatalf("ordinary fallback victim=%d, want %d", got, target.Handle)
	}
	if got := s.communityOnMapOffMapVictim(p, &content.WeaponDef{NoExplode: true}, w, terrain); got != 0 {
		t.Fatalf("noexplode fallback victim=%d, want none", got)
	}
}

func TestCommunityOffMapSplashStrictDistanceAndNoAirFilter(t *testing.T) {
	s, w, terrain, target := offMapFixture(t, CommunityRules{}, false)
	weapon := &content.WeaponDef{AreaOfEffect: 32, DamageDefault: 10, EdgeEffectiveness: 1, UnitsOnly: true}
	// The target box ends at X=0. Sixteen world units is exactly the radius
	// and is rejected; fifteen is admitted. The target is not airborne, which
	// the splash pass deliberately does not require.
	s.ExplodeWeaponAt(w, terrain, weapon, Vec3{X: numeric.FixedFromInt(16), Y: target.Y, Z: target.Z}, 0, 1)
	if target.Health != 100 {
		t.Fatalf("distance==radius damaged off-map target: health=%d", target.Health)
	}
	s.ExplodeWeaponAt(w, terrain, weapon, Vec3{X: numeric.FixedFromInt(15), Y: target.Y, Z: target.Z}, 0, 2)
	if target.Health != 90 {
		t.Fatalf("distance radius-1 left health=%d, want 90", target.Health)
	}
}

func TestCommunityOffMapDistanceWrapsStoredPositionAddition(t *testing.T) {
	def := &content.UnitDef{FootprintX: 1, FootprintZ: 1}
	u := &units.Unit{
		Def: def,
		// Adding the -8-world-unit minimum extent wraps this signed 32-bit
		// stored position to +32764 world units before separation is formed.
		X: numeric.Fixed(int64(-2147483648) + int64(numeric.FixedFromInt(4))),
	}
	if got := communityOffMapDistance(Vec3{}, u); got != 32764 {
		t.Fatalf("wrapped off-map distance=%d, want 32764", got)
	}
}
