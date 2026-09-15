package combat

import (
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// Radar contact alone cannot feed the area query used by stationary guards
// and Wait. The same player's active upgrade opens its fallback [04 R-SPEC-01 §8].
func TestRegistryAreaQueryRequiresUpgradeForRadarContact(t *testing.T) {
	f := newRegistryFixture(t, true)
	f.sensorTick(1)
	if f.enemy.Flags&visibility.SeenBit == 0 {
		t.Fatal("fixture radar did not detect the enemy")
	}
	s := &Service{}
	s.rebuildTargetRegistry(30, 0, f.world, f.vis, f.terrain, f.econ)
	query := func() []pool.Handle {
		return s.TargetsInRadius(0, f.enemy.X, f.enemy.Z, 640, f.world)
	}
	if got := query(); len(got) != 0 {
		t.Fatalf("unseen enemy acquired without an upgrade: %v", got)
	}
	upgrade := &content.UnitDef{UnitName: "upgrade", MaxDamage: 100, Limit: -1, IsTargetingUpgrade: true}
	h, err := f.world.Create(upgrade, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.world.Unit(h).Activated = true
	s.rebuildTargetRegistry(60, 0, f.world, f.vis, f.terrain, f.econ)
	if got := query(); !slices.Equal(got, []pool.Handle{f.enemy.Handle}) {
		t.Fatalf("upgrade fallback = %v, want detected enemy", got)
	}
}

// The query preserves cached membership and order while refreshing liveness
// and range. A nonempty primary result suppresses secondary fallback [06 §3.1].
func TestRegistryAreaQueryPreservesPrimaryMembershipAndOrder(t *testing.T) {
	f := newRegistryFixture(t, false)
	f.onProjectedGrid()
	s := &Service{}
	s.rebuildTargetRegistry(30, 0, f.world, f.vis, f.terrain, f.econ)
	h, err := f.world.Create(&content.UnitDef{UnitName: "second", MaxDamage: 100, Limit: -1}, 2, f.enemy.X, f.enemy.Y, f.enemy.Z)
	if err != nil {
		t.Fatal(err)
	}
	s.targets.primary[0] = []pool.Handle{h, f.enemy.Handle}
	s.targets.secondary[0] = []pool.Handle{f.shooter.Handle}
	s.targets.gate[0] = true
	f.enemy.Hidden = true
	f.econ.Players[0].Allies[f.enemy.Owner] = true
	query := func() []pool.Handle {
		return s.TargetsInRadius(0, f.shooter.X, f.shooter.Z, 640, f.world)
	}
	if got := query(); !slices.Equal(got, []pool.Handle{h, f.enemy.Handle}) {
		t.Fatalf("cached primary order/membership changed: %v", got)
	}
	f.world.Unit(h).Dying = true
	if got := query(); !slices.Equal(got, []pool.Handle{f.enemy.Handle}) {
		t.Fatalf("dying primary was not filtered: %v", got)
	}
	f.enemy.X += numeric.FixedFromInt(1000)
	if got := query(); !slices.Equal(got, []pool.Handle{f.shooter.Handle}) {
		t.Fatalf("empty primary did not select secondary: %v", got)
	}
}

// Both squared axes truncate after multiplication, the boundary is inclusive,
// and deltas/radius products use signed-32 arithmetic [06 §3.1].
func TestRegistryAreaQueryRangeArithmetic(t *testing.T) {
	f := newRegistryFixture(t, false)
	s := &Service{}
	s.targets.primary[0] = []pool.Handle{f.enemy.Handle}
	for _, tc := range []struct {
		name   string
		x, z   numeric.Fixed
		radius int32
		want   bool
	}{
		{"boundary", numeric.FixedFromInt(3), numeric.FixedFromInt(4), 5, true},
		{"square before truncation", numeric.FixedFromInt(3) + 32768, numeric.FixedFromInt(4), 5, false},
		{"truncate each square", numeric.Fixed(58982), numeric.Fixed(58982), 0, true},
		{"wrapping delta", numeric.Fixed(1<<32) + numeric.FixedFromInt(3), numeric.FixedFromInt(4), 5, true},
		{"signed radius product", 0, 0, 46341, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.enemy.X, f.enemy.Z = tc.x, tc.z
			got := s.TargetsInRadius(0, 0, 0, tc.radius, f.world)
			if (len(got) != 0) != tc.want {
				t.Fatalf("query = %v, want admission %v", got, tc.want)
			}
		})
	}
}
