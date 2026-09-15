package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// The world contains an unseen enemy in range, while the actual combat
// registry keeps it in the secondary list only [04 R-SPEC-01 §8].
func registryOrderFixture(t *testing.T) (*units.Unit, *units.Unit, *rng.Simulation, func()) {
	t.Helper()
	w := newOrdersFixtureWorld(8, &content.Catalog{})
	def := &content.UnitDef{UnitName: "guard", MaxDamage: 100, Limit: -1}
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	eh, err := w.Create(&content.UnitDef{UnitName: "enemy", MaxDamage: 100, Limit: -1}, 1, numeric.FixedFromInt(2000), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u, enemy := w.Unit(h), w.Unit(eh)
	u.Activated = true
	u.InstallWeapon(0, &content.WeaponDef{Range: 4096})
	enemy.Flags |= visibility.SeenBit
	s := &combat.Service{}
	econ := &economy.Service{}
	sim := rng.NewSimulation(7)
	b := &QueueBinding{
		Lookup: w.Unit, SimRNG: &sim,
		Hostility: func(a, b *units.Unit) bool { return a.Owner != b.Owner },
		World: &WorldQueryAdapter{ForEachUnit: func(visit func(pool.Handle, *units.Unit) bool) {
			visit(enemy.Handle, enemy)
		}},
		Weapons: &WeaponAdapter{TargetsInRadius: func(actor *units.Unit, x, z numeric.Fixed, radius int32) []pool.Handle {
			return s.TargetsInRadius(actor.Owner, x, z, radius, w)
		}},
	}
	QueueForUnit(u).SetBinding(b)
	s.RebuildTargetRegistryIfDue(30, 0, w, nil, nil, econ)
	upgrade := func() {
		def.IsTargetingUpgrade = true
		s.RebuildTargetRegistryIfDue(60, 0, w, nil, nil, econ)
	}
	return u, enemy, &sim, upgrade
}

// A stationary guard searches around its previous target, not its own
// position. It must not turn that rescan into unrestricted enemy knowledge
// [04 R-ORD-01 §3][04 R-SPEC-01 §8].
func TestStationaryGuardRescanUsesTargetRegistry(t *testing.T) {
	u, enemy, sim, upgrade := registryOrderFixture(t)
	n := &Node{ID: Lookup("Guard_NoMove"), Owner: u.Handle, Phase: 3, GoalX: enemy.X, GoalZ: enemy.Z}
	before := sim.Draws()
	if code := guardNoMoveHandler(u, n, 0, 30); code != Code(0) || n.Target != 0 || u.SlotAt(0).Target.Kind != units.TargetNone {
		t.Fatal("guard acquired an unseen enemy without a targeting upgrade")
	}
	if sim.Draws() != before {
		t.Fatal("empty registry query consumed a random draw")
	}
	upgrade()
	if code := guardNoMoveHandler(u, n, 0, 60); code != Code(2) || n.Target != enemy.Handle || n.Phase != 1 {
		t.Fatal("upgraded guard did not acquire the contact around its saved goal")
	}
	if u.SlotAt(0).Target.Unit != enemy.Handle || sim.Draws() != before {
		t.Fatal("guard failed to bind the one candidate or advanced RNG for a bound-one pick")
	}
}

// Wait's scan has the same registry gate; a hidden live enemy alone must not
// complete it [04 R-ORD-01 §2][04 R-SPEC-01 §8].
func TestWaitScanUsesTargetRegistry(t *testing.T) {
	u, enemy, sim, upgrade := registryOrderFixture(t)
	u.X = enemy.X - numeric.FixedFromInt(100)
	n := &Node{ID: Lookup("Wait"), Owner: u.Handle, Param1: 300, Param2: 100}
	before := sim.Draws()
	if code := waitHandler(u, n, 0, 30); code != Code(2) || n.Param1 >= 300 {
		t.Fatal("Wait completed for an unseen enemy without an upgrade")
	}
	if sim.Draws() != before+1 {
		t.Fatal("unsatisfied Wait must consume its one deadline draw")
	}
	upgrade()
	before = sim.Draws()
	if code := waitHandler(u, n, 0, 60); code != Code(5) || sim.Draws() != before {
		t.Fatal("Wait failed to complete on a registry contact without drawing")
	}
}
