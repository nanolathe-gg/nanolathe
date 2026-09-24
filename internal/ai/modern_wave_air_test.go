package ai

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Modern wave air targets (DESIGN_SESSIONS_AI_SAVE "Modern wave air
// targets"): with an airborne target, a member that can engage it is ordered
// at it and one that cannot is ordered at the nearest grounded hostile, or
// given no order when there is none; the retail step orders every member at
// the airborne target.
func TestModernWaveAirTargetsSplitByCapability(t *testing.T) {
	own := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "attacker"}, UnitName: "attacker", CanAttack: true, CanMove: true, BMCode: 1, MaxDamage: 100}
	enemy := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "enemy"}, UnitName: "enemy", MaxDamage: 100}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"attacker": own, "enemy": enemy}}
	build := func(withGround bool) (*units.World, *Manager, pool.Handle, pool.Handle, pool.Handle, pool.Handle) {
		w := newAIFixtureWorld(8, cat)
		aa, _ := w.Create(own, 0, numeric.FixedFromInt(0), 0, numeric.FixedFromInt(0))
		plain, _ := w.Create(own, 0, numeric.FixedFromInt(0), 0, numeric.FixedFromInt(16))
		for _, h := range []pool.Handle{aa, plain} {
			w.Unit(h).Group = 5
			w.Unit(h).Flags |= units.ArmedStatus
		}
		plane, _ := w.Create(enemy, 1, numeric.FixedFromInt(100), 0, numeric.FixedFromInt(0))
		w.Unit(plane).Move.ModeMirror = airborneMode
		var ground pool.Handle
		if withGround {
			ground, _ = w.Create(enemy, 1, numeric.FixedFromInt(600), 0, numeric.FixedFromInt(0))
		}
		m := &Manager{Player: 0, IsAlliance: func(uint8, uint8) bool { return false }}
		m.CanPursueAir = func(member, target *units.Unit) bool { return member.Handle == aa }
		return w, m, aa, plain, plane, ground
	}
	e := runtimeEconomy(0, 2)
	e.Players[1].Exists, e.Players[1].ControllerState = true, 1
	// The fixture's units resolve no attack descriptor, so a member's pick is
	// read from the submitted goal: the chosen hostile's position.
	target := func(w *units.World, h pool.Handle) (pool.Handle, bool) {
		q := orders.QueueOfUnit(w.Unit(h))
		if q == nil || len(q.Primary()) == 0 {
			return 0, false
		}
		n := q.Primary()[0]
		for _, e := range w.Iter() {
			if e.Owner == 1 && e.X == n.GoalX && e.Z == n.GoalZ {
				return e.Handle, true
			}
		}
		return 0, true
	}

	w, m, aa, plain, plane, ground := build(true)
	m.modernWaveAir = true
	if !m.airTargetSplit(w.Unit(plane)) {
		t.Fatal("an airborne target under the Modern step did not split")
	}
	m.broadcastAirSplit(w, e, 5, 3, 0, w.Unit(plane), 0, 0, 0, 12, false)
	if got, ok := target(w, aa); !ok || got != plane {
		t.Fatalf("anti-air member ordered at %d (%v), want the aircraft %d", got, ok, plane)
	}
	if got, ok := target(w, plain); !ok || got != ground {
		t.Fatalf("member without anti-air ordered at %d (%v), want the grounded hostile %d", got, ok, ground)
	}

	w, m, _, plain, plane, _ = build(false)
	m.modernWaveAir = true
	m.broadcastAirSplit(w, e, 5, 3, 0, w.Unit(plane), 0, 0, 0, 12, false)
	if _, ok := target(w, plain); ok {
		t.Fatal("member without anti-air was ordered at the aircraft when no grounded hostile exists")
	}

	// The retail step (and a grounded target) never splits.
	w, m, _, _, plane, ground = build(true)
	if m.airTargetSplit(w.Unit(plane)) {
		t.Fatal("the retail step split an airborne target")
	}
	m.modernWaveAir = true
	if m.airTargetSplit(w.Unit(ground)) {
		t.Fatal("a grounded target split")
	}
}
